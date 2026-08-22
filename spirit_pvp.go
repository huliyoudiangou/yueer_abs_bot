package main

// ==========================================
// 镜场 · 异步 PVP（Phase 1）
//
// 玩法（设计锁定）：
// - 镜像上架：出阵队伍快照入库，24 小时有效，期间可被他人挑战
// - 攻击镜像：随机匹配（优先战力 ±50% 区间）或指定目标（复仇/反击）
// - 奖励：胜 30 灵晶 / 负 10 灵晶（均发攻方），每人每日上限 10 次
// - 复仇：24 小时内被对方破镜后，可定向攻击对方镜像（计入每日上限）
//
// 资产规则：
// - 战斗奖励走 EarnLingjing（pvp_battle 流水），与战斗记录同事务
// - 每日上限按 SpiritPvpBattle(attacker_id, day_key) 计数，仅成功战斗计数
// - 用户级锁 lockUser 防并发重复结算
// ==========================================

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"time"

	"gorm.io/gorm"
)

const (
	pvpWinReward      = 30 // 攻方胜利奖励（灵晶）
	pvpLoseReward     = 10 // 攻方失败安慰（灵晶）
	pvpDailyLimit     = 10 // 每日攻击次数上限
	pvpMirrorTTLHours = 24 // 镜像有效期（小时）
	pvpHistoryLimit   = 10 // 战绩展示条数
	pvpRevengeLimit   = 5  // 复仇列表展示条数
	pvpRevengeWindowH = 24 // 复仇窗口（小时）
)

// TeamBattleResult 双队战斗结果（A 方视角）
type TeamBattleResult struct {
	Win      bool
	HPLeftA  int
	HPLeftB  int
	HPTotalA int
	HPTotalB int
	Rounds   int
}

// runTeamBattle 双队回合制引擎（PVP 镜场；A=攻方，B=守方）
// 复用 calcDamage（属性克制/阴阳相冲/品阶压制），与 PVE 引擎共享数值规则
func runTeamBattle(teamA, teamB []*BattleFighter) *TeamBattleResult {
	res := &TeamBattleResult{}
	for _, f := range teamA {
		res.HPTotalA += f.MaxHP
	}
	for _, f := range teamB {
		res.HPTotalB += f.MaxHP
	}

	for round := 1; round <= maxBattleRounds; round++ {
		res.Rounds = round

		type actor struct {
			f    *BattleFighter
			side int // 0=A(攻) 1=B(守)
		}
		var order []actor
		for _, f := range teamA {
			if f.HP > 0 {
				order = append(order, actor{f: f, side: 0})
			}
		}
		for _, f := range teamB {
			if f.HP > 0 {
				order = append(order, actor{f: f, side: 1})
			}
		}
		// SPD 降序，同速攻方先手
		for i := 1; i < len(order); i++ {
			for j := i; j > 0; j-- {
				a, b := order[j], order[j-1]
				if a.f.SPD > b.f.SPD || (a.f.SPD == b.f.SPD && a.side < b.side) {
					order[j], order[j-1] = b, a
				} else {
					break
				}
			}
		}

		for _, a := range order {
			if a.f.HP <= 0 {
				continue
			}
			var pool []*BattleFighter
			if a.side == 0 {
				for _, f := range teamB {
					if f.HP > 0 {
						pool = append(pool, f)
					}
				}
			} else {
				for _, f := range teamA {
					if f.HP > 0 {
						pool = append(pool, f)
					}
				}
			}
			if len(pool) == 0 {
				break
			}
			target := pool[rand.Intn(len(pool))]
			target.HP -= calcDamage(a.f, target)
			if target.HP < 0 {
				target.HP = 0
			}
		}

		deadA, deadB := true, true
		for _, f := range teamA {
			if f.HP > 0 {
				deadA = false
			}
		}
		for _, f := range teamB {
			if f.HP > 0 {
				deadB = false
			}
		}
		if deadB && !deadA {
			res.Win = true
			break
		}
		if deadA && !deadB {
			res.Win = false
			break
		}
		if deadA && deadB {
			res.Win = false // 同归于尽：守方胜利（攻方需明确取胜）
			break
		}
	}

	for _, f := range teamA {
		res.HPLeftA += f.HP
	}
	for _, f := range teamB {
		res.HPLeftB += f.HP
	}
	// 超时（25 回合未分胜负）：按剩余血量占比判定，平局守方胜
	if res.HPTotalA > 0 && res.HPTotalB > 0 {
		ratioA := float64(res.HPLeftA) / float64(res.HPTotalA)
		ratioB := float64(res.HPLeftB) / float64(res.HPTotalB)
		if ratioA < ratioB {
			res.Win = false
		} else if ratioA > ratioB {
			res.Win = true
		}
	}
	return res
}

// ==========================================
// 镜像上架 / 查询
// ==========================================

// SetupMirror 上架/刷新镜像（出阵队伍快照，24 小时有效）
func SetupMirror(userID int64) (int, error) {
	var power int
	// 境界读取放事务外（GetOrCreateCultivation 走全局连接池，事务内调用会死等连接）
	cul := GetOrCreateCultivation(userID)
	realm := 0
	if cul != nil {
		realm = cul.MajorRealm
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		team, err := pickDeployedTeamTx(tx, userID) // 战力高→低（快照含装备）
		if err != nil {
			return err
		}
		if len(team) == 0 {
			return fmt.Errorf("尚未编排出战灵侍，请先在出战队列编队")
		}
		team = enhanceServantStats(tx, userID, team) // 并入装备加成（镜像快照含装备）
		fighters := teamToFighters(team)
		b, err := json.Marshal(fighters)
		if err != nil {
			return fmt.Errorf("镜像生成失败: %s", formatTelegramSendError(err))
		}
		power = CalculateTeamPower(team, 0)
		now := time.Now()

		var mirror SpiritMirror
		if err := tx.Where("user_id = ?", userID).First(&mirror).Error; err != nil && err != gorm.ErrRecordNotFound {
			return err
		}
		mirror.UserID = userID
		mirror.TeamJSON = string(b)
		mirror.TeamPower = power
		mirror.Realm = realm
		mirror.MemberCount = len(team)
		mirror.ExpiresAt = now.Add(pvpMirrorTTLHours * time.Hour)
		if mirror.ID == 0 {
			return tx.Create(&mirror).Error
		}
		return tx.Save(&mirror).Error
	})
	if err != nil {
		return 0, err
	}
	return power, nil
}

// GetMyMirror 获取我方有效镜像（未上架或已过期返回 nil）
func GetMyMirror(userID int64) *SpiritMirror {
	var m SpiritMirror
	if err := db.Where("user_id = ?", userID).First(&m).Error; err != nil {
		return nil
	}
	if time.Now().After(m.ExpiresAt) {
		return nil
	}
	return &m
}

// findPvpTarget 查找可攻击镜像：优先战力 ±50% 区间随机，兜底全量随机
func findPvpTarget(tx *gorm.DB, attackerID int64, myPower int) *SpiritMirror {
	now := time.Now()
	var m SpiritMirror
	err := tx.Where("user_id <> ? AND expires_at > ? AND team_power BETWEEN ? AND ?",
		attackerID, now, myPower/2+1, myPower*3/2).
		Order("RANDOM()").First(&m).Error
	if err == nil {
		return &m
	}
	err = tx.Where("user_id <> ? AND expires_at > ?", attackerID, now).
		Order("RANDOM()").First(&m).Error
	if err != nil {
		return nil
	}
	return &m
}

// ==========================================
// 攻击 / 复仇
// ==========================================

// PvpAttackResult 一次镜场攻击的结果
type PvpAttackResult struct {
	Win           bool
	Reward        int
	DefenderID    int64
	DefenderName  string
	DefenderPower int
	HPLeft        int
	HPTotal       int
	Remaining     int // 本次战后今日剩余次数
	IsRevenge     bool
}

// PvpAttack 攻击镜场镜像（defenderID=0 随机匹配；>0 定向复仇/反击）
// 全程事务：次数校验 → 编队 → 选目标 → 战斗 → 发奖 → 记流水
func PvpAttack(userID int64, defenderID int64) (*PvpAttackResult, error) {
	result := &PvpAttackResult{}

	err := db.Transaction(func(tx *gorm.DB) error {
		today := time.Now().Format("20060102")

		// 1. 每日上限
		var cnt int64
		if err := tx.Model(&SpiritPvpBattle{}).
			Where("attacker_id = ? AND day_key = ?", userID, today).Count(&cnt).Error; err != nil {
			return err
		}
		if cnt >= pvpDailyLimit {
			return fmt.Errorf("今日镜场攻击次数已用尽（%d/日），明日再来", pvpDailyLimit)
		}
		result.Remaining = int(pvpDailyLimit - cnt - 1)

		// 2. 出阵队伍（战力高→低）
		team, err := pickDeployedTeamTx(tx, userID)
		if err != nil {
			return err
		}
		if len(team) == 0 {
			return fmt.Errorf("尚未编排出战灵侍，请先在出战队列编队")
		}
		team = enhanceServantStats(tx, userID, team) // 并入装备加成
		myPower := CalculateTeamPower(team, 0)

		// 3. 目标选择
		var target SpiritMirror
		if defenderID > 0 {
			if defenderID == userID {
				return fmt.Errorf("不能攻击自己的镜像")
			}
			if err := tx.Where("user_id = ?", defenderID).First(&target).Error; err != nil {
				return fmt.Errorf("对方镜像未上架，请挑战其他镜像")
			}
			if time.Now().After(target.ExpiresAt) {
				return fmt.Errorf("对方镜像已过期，请挑战其他镜像")
			}
			result.IsRevenge = true
		} else {
			t := findPvpTarget(tx, userID, myPower)
			if t == nil {
				return fmt.Errorf("暂无可挑战的镜像，请等道友上架镜像后再来")
			}
			target = *t
		}
		result.DefenderID = target.UserID
		result.DefenderPower = target.TeamPower
		// 对手昵称仅展示用，放事务后查询（spiritPvpUserName 走全局连接池，事务内查询会死等连接）

		// 4. 战斗（A=我方实时队伍，B=对方镜像快照）
		var defenders []BattleFighter
		if err := json.Unmarshal([]byte(target.TeamJSON), &defenders); err != nil || len(defenders) == 0 {
			return fmt.Errorf("对方镜像数据损坏，请挑战其他镜像")
		}
		enemyTeam := make([]*BattleFighter, 0, len(defenders))
		for i := range defenders {
			enemyTeam = append(enemyTeam, &defenders[i])
		}
		battle := runTeamBattle(teamToFighters(team), enemyTeam)
		result.Win = battle.Win
		result.HPLeft = battle.HPLeftA
		result.HPTotal = battle.HPTotalA

		// 5. 奖励（胜 30 / 负 10，发攻方）
		reward := pvpLoseReward
		if battle.Win {
			reward = pvpWinReward
		}
		result.Reward = reward
		if err := EarnLingjing(tx, userID, reward, "pvp_battle",
			fmt.Sprintf("镜场斗法：%s，%d 灵晶", outcomeText(battle.Win), reward)); err != nil {
			return err
		}

		// 6. 战斗记录（复仇列表 / 战绩 / 每日次数依据）
		b := SpiritPvpBattle{
			AttackerID:    userID,
			DefenderID:    target.UserID,
			AttackerWin:   battle.Win,
			AttackerPower: myPower,
			DefenderPower: target.TeamPower,
			Reward:        reward,
			DayKey:        today,
		}
		return tx.Create(&b).Error
	})
	if err != nil {
		log.Printf("[灵侍] 镜场攻击 user=%d defender=%d err=%s", userID, defenderID, formatTelegramSendError(err))
		return nil, err
	}
	result.DefenderName = spiritPvpUserName(result.DefenderID)
	return result, nil
}

// GetPvpRevengeTargets 我可复仇的道友（24h 内破我镜像且镜像仍有效，最多 pvpRevengeLimit 名）
func GetPvpRevengeTargets(userID int64) []SpiritPvpBattle {
	var battles []SpiritPvpBattle
	cutoff := time.Now().Add(-pvpRevengeWindowH * time.Hour)
	// 预查上限放宽到 50：先去重再截断，避免多名道友重复破镜时漏掉有效复仇目标
	if err := db.Where("defender_id = ? AND attacker_win = ? AND created_at > ?",
		userID, true, cutoff).Order("id desc").Limit(50).Find(&battles).Error; err != nil {
		return nil
	}
	var out []SpiritPvpBattle
	seen := make(map[int64]bool)
	for _, b := range battles {
		if seen[b.AttackerID] {
			continue
		}
		seen[b.AttackerID] = true
		if GetMyMirror(b.AttackerID) != nil {
			out = append(out, b)
			if len(out) >= pvpRevengeLimit {
				break
			}
		}
	}
	return out
}

// GetPvpHistory 我的镜场战绩（攻+守，新→旧，分页）
// page 从 1 起，越界钳制到 [1, 总页数]（无记录时 total=0）
func GetPvpHistory(userID int64, page, pageSize int) (battles []SpiritPvpBattle, total int64) {
	db.Model(&SpiritPvpBattle{}).
		Where("attacker_id = ? OR defender_id = ?", userID, userID).
		Count(&total)
	if total == 0 {
		return nil, 0
	}
	if pageSize <= 0 {
		pageSize = pvpHistoryLimit
	}
	if page < 1 {
		page = 1
	}
	maxPage := int((total + int64(pageSize) - 1) / int64(pageSize))
	if page > maxPage {
		page = maxPage
	}
	db.Where("attacker_id = ? OR defender_id = ?", userID, userID).
		Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&battles)
	return battles, total
}

// GetPvpDailyRemaining 今日剩余攻击次数
func GetPvpDailyRemaining(userID int64) int {
	var cnt int64
	db.Model(&SpiritPvpBattle{}).
		Where("attacker_id = ? AND day_key = ?", userID, time.Now().Format("20060102")).Count(&cnt)
	if cnt >= pvpDailyLimit {
		return 0
	}
	return pvpDailyLimit - int(cnt)
}

// spiritPvpUserName 查询用户名（失败回退编号显示）
func spiritPvpUserName(userID int64) string {
	var u User
	if err := db.Select("username").Where("telegram_id = ?", userID).First(&u).Error; err != nil || u.Username == "" {
		return fmt.Sprintf("道友#%d", userID)
	}
	return u.Username
}

func outcomeText(win bool) string {
	if win {
		return "胜"
	}
	return "负"
}
