package main

// ==========================================
// 灵墟推图 · PVE 战斗（Phase 1）
//
// 6 章（对应六大灵墟区域，境界解锁与捕捉一致）×（10 普通关 + 1 Boss）
// 神行符体力 10/日，每次挑战消耗 1；三星解锁扫荡（奖励 40%，每关每日 3 次）
// 战斗引擎为回合制，PVP 镜场后续复用 runBattle。
//
// 资产规则：
// - 体力消耗与奖励发放同事务（db.Transaction）
// - 奖励走 EarnLingjing（有流水）
// - 进度 upsert 用唯一索引 (user_id, chapter_id, stage_id) 兜底
// ==========================================

import (
	"fmt"
	"log"
	"math/rand"
	"time"

	"gorm.io/gorm"
)

const (
	divineTravelDailyCap = 10 // 神行符：每日 10 点
	divineTravelCost     = 1  // 每次挑战消耗 1 点
	sweepDailyLimit      = 3  // 每关每日扫荡上限
	sweepRewardRatio     = 40 // 扫荡奖励 = 关卡奖励的 40%
	starUpRewardRatio    = 20 // 升星奖励 = 关卡奖励的 20%
	maxBattleRounds      = 25 // 战斗最大回合
	maxTeamSize          = 5  // 出战上限
	stagesPerChapter     = 10 // 普通关数
	bossStageID          = 11 // 第 11 关 = 章节 Boss
)

// chapterBossNames 章节 Boss 名（各区域顶阶灵侍首领）
var chapterBossNames = []string{
	"竹影螳螂王", // ch1 青竹林海
	"雾隐魅狐",  // ch2 迷雾深谷
	"裂地犰狳王", // ch3 断岳山脉
	"罗刹夜魅",  // ch4 幽冥绝岭
	"覆海蛟王",  // ch5 归墟海眼
	"五爪金龙",  // ch6 不周山巅
}

// wuxingCounter 五行克制（攻方 → 所克）
var wuxingCounter = map[string]string{
	"木": "土", "土": "水", "水": "火", "火": "金", "金": "木",
}

// attrMult 属性倍率：攻方 vs 守方
func attrMult(attEl, defEl string) float64 {
	if attEl == "" || defEl == "" || attEl == defEl {
		return 1.0
	}
	if (attEl == "阴" && defEl == "阳") || (attEl == "阳" && defEl == "阴") {
		return 1.30 // 阴阳相冲
	}
	if wuxingCounter[attEl] == defEl {
		return 1.25 // 五行克制
	}
	if wuxingCounter[defEl] == attEl {
		return 0.85 // 被克
	}
	return 1.0
}

// BattleFighter 战斗实体（队员/敌方通用，PVP 复用）
type BattleFighter struct {
	Name    string
	Quality string
	Element string // 五行 / 阴 / 阳
	MaxHP   int
	HP      int
	ATK     int
	DEF     int
	SPD     int
}

// SpiritBattleResult 一场战斗的结果
type SpiritBattleResult struct {
	Win         bool
	Stars       int // 败 0，胜 1-3
	TeamHPLeft  int
	TeamHPTotal int
	Rounds      int
}

// chapterZone 章节 → 灵墟区域
func chapterZone(chapterID int) *SpiritZone {
	if chapterID < 1 || chapterID > len(SpiritZones) {
		return nil
	}
	return &SpiritZones[chapterID-1]
}

// buildStageEnemy 生成关卡敌人（数值按章节品阶线性成长）
func buildStageEnemy(chapterID, stageID int) *BattleFighter {
	zone := chapterZone(chapterID)
	if zone == nil {
		return nil
	}
	tier := zone.Tier
	base := 60 + 70*tier
	power := int(float64(base) * (1 + 0.08*float64(stageID-1)))
	isBoss := stageID == bossStageID
	if isBoss {
		power = base * 3
	}
	name := fmt.Sprintf("%s·野灵 %d", zone.Name, stageID)
	el := SpiritAttributes[(chapterID+stageID)%5]
	if isBoss {
		name = chapterBossNames[chapterID-1]
		el = SpiritAttributes[(chapterID*2+1)%5]
	}
	atk := 15 + 12*tier + stageID*2
	if isBoss {
		atk += 30
	}
	return &BattleFighter{
		Name:    name,
		Quality: SpiritQualityNames[tier],
		Element: el,
		MaxHP:   power * 2,
		HP:      power * 2,
		ATK:     atk,
		DEF:     8 + 6*tier,
		SPD:     40 + tier*5,
	}
}

// stageReward 关卡首通奖励（灵晶）
func stageReward(chapterID, stageID int) int {
	base := 20 + chapterID*15
	if stageID == bossStageID {
		return base * 6
	}
	return base + stageID*5
}

// teamToFighters 出阵灵侍 → 战斗实体
func teamToFighters(team []UserSpiritServant) []*BattleFighter {
	out := make([]*BattleFighter, 0, len(team))
	for i := range team {
		s := &team[i]
		out = append(out, &BattleFighter{
			Name:    s.Name,
			Quality: s.Quality,
			Element: s.Attribute,
			MaxHP:   s.HP,
			HP:      s.HP,
			ATK:     s.ATK + s.MAG/2,
			DEF:     s.DEF,
			SPD:     s.SPD,
		})
	}
	return out
}

// calcDamage 伤害 = max(1, 攻*1.2 - 防*0.7) × 属性倍率 ± 品阶压制差
func calcDamage(att, def *BattleFighter) int {
	base := float64(att.ATK)*1.2 - float64(def.DEF)*0.7
	if base < 1 {
		base = 1
	}
	mult := attrMult(att.Element, def.Element)
	mult += QualitySuppressRate[att.Quality] - QualitySuppressRate[def.Quality]
	if mult < 0.2 {
		mult = 0.2
	}
	return int(base * mult)
}

// runBattle 共享回合制战斗引擎（PVE/PVP 复用）
func runBattle(team []*BattleFighter, enemy *BattleFighter) *SpiritBattleResult {
	res := &SpiritBattleResult{}
	for _, f := range team {
		res.TeamHPTotal += f.MaxHP
	}

	for round := 1; round <= maxBattleRounds; round++ {
		res.Rounds = round

		// 行动顺序：存活单位按 SPD 降序（同速队员优先）
		type actor struct {
			f   *BattleFighter
			att bool
		}
		var order []actor
		for _, f := range team {
			if f.HP > 0 {
				order = append(order, actor{f: f, att: true})
			}
		}
		if enemy.HP > 0 {
			order = append(order, actor{f: enemy, att: false})
		}
		for i := 1; i < len(order); i++ {
			for j := i; j > 0; j-- {
				a, b := order[j], order[j-1]
				if a.f.SPD > b.f.SPD || (a.f.SPD == b.f.SPD && a.att && !b.att) {
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
			var target *BattleFighter
			if a.att {
				target = enemy
			} else {
				var alive []*BattleFighter
				for _, f := range team {
					if f.HP > 0 {
						alive = append(alive, f)
					}
				}
				if len(alive) == 0 {
					break
				}
				target = alive[rand.Intn(len(alive))]
			}
			if target.HP <= 0 {
				continue
			}
			target.HP -= calcDamage(a.f, target)
			if target.HP < 0 {
				target.HP = 0
			}
		}

		if enemy.HP <= 0 {
			res.Win = true
			break
		}
		allDead := true
		for _, f := range team {
			if f.HP > 0 {
				allDead = false
				break
			}
		}
		if allDead {
			res.Win = false
			break
		}
	}

	for _, f := range team {
		res.TeamHPLeft += f.HP
	}
	if res.Win && res.TeamHPTotal > 0 {
		ratio := float64(res.TeamHPLeft) / float64(res.TeamHPTotal)
		switch {
		case ratio >= 0.80:
			res.Stars = 3
		case ratio >= 0.45:
			res.Stars = 2
		default:
			res.Stars = 1
		}
	}
	return res
}

// ==========================================
// 神行符（体力）
// ==========================================

// getOrCreateStaminaTx 获取体力行，跨天自动重置为每日上限
func getOrCreateStaminaTx(tx *gorm.DB, userID int64) (*SpiritBattleStamina, error) {
	today := time.Now().Format("20060102")
	var s SpiritBattleStamina
	err := tx.Where("user_id = ?", userID).First(&s).Error
	if err == gorm.ErrRecordNotFound {
		s = SpiritBattleStamina{UserID: userID, DayKey: today, Stamina: divineTravelDailyCap}
		if e := tx.Create(&s).Error; e != nil {
			return nil, e
		}
		return &s, nil
	}
	if err != nil {
		return nil, err
	}
	if s.DayKey != today {
		s.DayKey = today
		s.Stamina = divineTravelDailyCap
		if e := tx.Save(&s).Error; e != nil {
			return nil, e
		}
	}
	return &s, nil
}

// GetUserStamina 剩余体力（面板展示用，非事务）
func GetUserStamina(userID int64) int {
	s, err := getOrCreateStaminaTx(db, userID)
	if err != nil {
		return 0
	}
	return s.Stamina
}

// ==========================================
// PVE 挑战 / 扫荡
// ==========================================

// PveFightResult 一次挑战的结果
type PveFightResult struct {
	Win          bool
	Stars        int
	Reward       int
	EnemyName    string
	TeamHPLeft   int
	TeamHPTotal  int
	StaminaLeft  int
	IsBoss       bool
	DroppedEgg   *SpiritEgg // Boss 胜利掉蛋（可空）
	DroppedItems []string   // 掉落道具（灵魄/真身碎片）
}

// PveFight 执行一次推图挑战（全程事务：扣体力 → 战斗 → 记进度 → 发奖励）
func PveFight(userID int64, chapterID, stageID int) (*PveFightResult, error) {
	zone := chapterZone(chapterID)
	if zone == nil {
		return nil, fmt.Errorf("章节不存在")
	}
	if stageID < 1 || stageID > bossStageID {
		return nil, fmt.Errorf("关卡不存在")
	}

	result := &PveFightResult{IsBoss: stageID == bossStageID}

	// 境界校验（事务外读取：GetOrCreateCultivation 走全局连接池，
	// 在事务内调用会在小连接池下互等连接而死锁）
	cul := GetOrCreateCultivation(userID)
	if cul == nil || cul.MajorRealm < zone.Tier {
		return nil, fmt.Errorf("境界不足，此章节需更高修为")
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		// 2. 关卡解锁：前关至少 1 星
		if stageID > 1 {
			var prev SpiritStageProgress
			if err := tx.Where("user_id = ? AND chapter_id = ? AND stage_id = ?", userID, chapterID, stageID-1).
				First(&prev).Error; err != nil || prev.Stars < 1 {
				return fmt.Errorf("上一关尚未通关，无法挑战此关")
			}
		}

		// 3. 出战队伍（战力高→低，最多 maxTeamSize）
		team, err := pickDeployedTeamTx(tx, userID)
		if err != nil {
			return err
		}
		if len(team) == 0 {
			return fmt.Errorf("尚未编排出战灵侍，请先在出战队列编队")
		}
		team = enhanceServantStats(tx, userID, team) // 并入装备加成

		// 4. 神行符消耗
		stamina, err := getOrCreateStaminaTx(tx, userID)
		if err != nil {
			return err
		}
		if stamina.Stamina < divineTravelCost {
			return fmt.Errorf("神行符已用尽，每日零点恢复")
		}
		stamina.Stamina -= divineTravelCost
		if err := tx.Save(stamina).Error; err != nil {
			return err
		}
		result.StaminaLeft = stamina.Stamina

		// 5. 战斗
		enemy := buildStageEnemy(chapterID, stageID)
		result.EnemyName = enemy.Name
		battle := runBattle(teamToFighters(team), enemy)
		result.Win = battle.Win
		result.Stars = battle.Stars
		result.TeamHPLeft = battle.TeamHPLeft
		result.TeamHPTotal = battle.TeamHPTotal
		if !battle.Win {
			return nil
		}

		// 6. 进度与奖励（首通全额 / 升星 20%）
		var prog SpiritStageProgress
		oldStars := 0
		if err := tx.Where("user_id = ? AND chapter_id = ? AND stage_id = ?", userID, chapterID, stageID).
			First(&prog).Error; err != nil {
			if !isUniqueConstraintError(err) && err != gorm.ErrRecordNotFound {
				return err
			}
			prog = SpiritStageProgress{UserID: userID, ChapterID: chapterID, StageID: stageID}
		} else {
			oldStars = prog.Stars
		}

		if battle.Stars > oldStars {
			reward := stageReward(chapterID, stageID)
			if oldStars > 0 {
				reward = reward * starUpRewardRatio / 100
			}
			prog.Stars = battle.Stars
			now := time.Now()
			prog.ClearedAt = &now
			var saveErr error
			if prog.ID == 0 {
				saveErr = tx.Create(&prog).Error
			} else {
				saveErr = tx.Save(&prog).Error
			}
			if saveErr != nil {
				// 并发首通兜底：改为更新
				saveErr = tx.Model(&SpiritStageProgress{}).
					Where("user_id = ? AND chapter_id = ? AND stage_id = ?", userID, chapterID, stageID).
					Updates(map[string]interface{}{"stars": battle.Stars, "cleared_at": now}).Error
			}
			if saveErr != nil {
				return fmt.Errorf("进度写入失败: %s", formatTelegramSendError(saveErr))
			}
			if reward > 0 {
				if err := EarnLingjing(tx, userID, reward, "pve_clear",
					fmt.Sprintf("灵墟推图奖励：第%d章第%d关", chapterID, stageID)); err != nil {
					return err
				}
				result.Reward = reward
			}
		}

		// 7. Boss 掉蛋（每次胜利 30%，扫荡不掉）
		if stageID == bossStageID {
			egg, err := dropBossEgg(tx, userID, chapterID, zone)
			if err != nil {
				return err
			}
			result.DroppedEgg = egg
		}

		// 8. 道具掉落（灵魄/万能真身碎片，扫荡不掉）
		if items := rollPveItemDrops(chapterID, stageID); len(items) > 0 {
			for _, it := range items {
				if err := addSpiritItemTx(tx, userID, it, 1); err != nil {
					return err
				}
			}
			result.DroppedItems = items
		}
		return nil
	})
	if err != nil {
		log.Printf("[灵侍] 推图挑战 user=%d ch=%d stage=%d err=%s", userID, chapterID, stageID, formatTelegramSendError(err))
		return nil, err
	}
	return result, nil
}

// PveSweep 扫荡（需三星，每关每日 3 次，奖励 = 40%）
func PveSweep(userID int64, chapterID, stageID int) (int, error) {
	if chapterZone(chapterID) == nil {
		return 0, fmt.Errorf("章节不存在")
	}
	var reward int
	err := db.Transaction(func(tx *gorm.DB) error {
		var prog SpiritStageProgress
		if err := tx.Where("user_id = ? AND chapter_id = ? AND stage_id = ?", userID, chapterID, stageID).
			First(&prog).Error; err != nil {
			return fmt.Errorf("该关尚未通关，无法扫荡")
		}
		if prog.Stars < 3 {
			return fmt.Errorf("需三星通关才可扫荡")
		}
		today := time.Now().Format("20060102")
		if prog.SweepDay != today {
			prog.SweepDay = today
			prog.SweepCount = 0
		}
		if prog.SweepCount >= sweepDailyLimit {
			return fmt.Errorf("今日扫荡次数已用尽（3次），明日再来")
		}
		reward = stageReward(chapterID, stageID) * sweepRewardRatio / 100
		if reward < 1 {
			reward = 1
		}
		prog.SweepCount++
		if err := tx.Save(&prog).Error; err != nil {
			return err
		}
		return EarnLingjing(tx, userID, reward, "pve_sweep",
			fmt.Sprintf("灵墟推图扫荡：第%d章第%d关", chapterID, stageID))
	})
	if err != nil {
		return 0, err
	}
	return reward, nil
}
