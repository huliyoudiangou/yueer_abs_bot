package main

// ==========================================
// 宗门运势 · 每日卦象（宗门声望拓展）
//
// 玩法（设计锁定，见 docs/agent/sect_prestige_expansion_planning.md §2）：
// - 宗主/长老开运：消耗宗门声望，每日最多 3 次，成本 100/200/400（×2 递增）。
// - 每次开运随机抽一个当日全宗 buff，当日最后一次抽中的卦象生效（一宗一天一条记录）。
// - 普通成员只能查看今日卦象，不能开运。
//
// 资产规则（AGENTS 资产安全）：
// - 开运扣宗门声望：事务 + 条件更新（prestige >= cost）+ RowsAffected + AuditLog 审计。
// - 每日上限与并发由 (sect_id, day_key) 唯一索引 + roll_count 条件更新兜底，不做「先查再写」。
// - 卦象 buff 效果由各收益链读取今日卦象统一叠加（见 Phase 4 接线）。
// ==========================================

import (
	"errors"
	"fmt"
	"math/rand"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SectFortune 宗门当日卦象（一宗一天一行，(sect_id, day_key) 唯一）
type SectFortune struct {
	gorm.Model
	SectID       int64  `gorm:"uniqueIndex:idx_sect_fortune_day;not null"`
	DayKey       string `gorm:"uniqueIndex:idx_sect_fortune_day;not null"`
	BuffType     string `gorm:"not null"`
	OpenedBy     int64  `gorm:"not null"`
	OpenedByName string
	Cost         int `gorm:"default:0"` // 本次开运消耗宗门声望
	RollCount    int `gorm:"default:1"` // 当日第几次开运（1..3）
}

func (SectFortune) TableName() string { return "sect_fortunes" }

const (
	sectFortuneDailyLimit = 3

	sectFortuneBuffContribution     = "contribution"      // 贡献 +10%
	sectFortuneBuffSecretRealm      = "secret_realm"      // 宗门秘境奖励 +5%
	sectFortuneBuffNetCultivation   = "net_cultivation"   // 今日净修为 +5%
	sectFortuneBuffWorldBoss        = "world_boss"        // 世界Boss 伤害 +2%
	sectFortuneBuffLingjingExchange = "lingjing_exchange" // 灵晶兑换 +5%
)

// sectFortuneCostByRoll 第 1/2/3 次开运成本（宗门声望）
var sectFortuneCostByRoll = [sectFortuneDailyLimit]int{100, 200, 400}

// sectFortuneBuffTypes 卦象池（开运时等概率随机）
var sectFortuneBuffTypes = []string{
	sectFortuneBuffContribution,
	sectFortuneBuffSecretRealm,
	sectFortuneBuffNetCultivation,
	sectFortuneBuffWorldBoss,
	sectFortuneBuffLingjingExchange,
}

// sectFortuneBuffPct 各卦象的当日效果加成百分比
var sectFortuneBuffPct = map[string]float64{
	sectFortuneBuffContribution:     0.10,
	sectFortuneBuffSecretRealm:      0.05,
	sectFortuneBuffNetCultivation:   0.05,
	sectFortuneBuffWorldBoss:        0.02,
	sectFortuneBuffLingjingExchange: 0.05,
}

func sectFortuneBuffName(buffType string) string {
	switch buffType {
	case sectFortuneBuffContribution:
		return "宗门贡献 +10%"
	case sectFortuneBuffSecretRealm:
		return "宗门秘境奖励 +5%"
	case sectFortuneBuffNetCultivation:
		return "今日净修为 +5%"
	case sectFortuneBuffWorldBoss:
		return "世界Boss 伤害 +2%"
	case sectFortuneBuffLingjingExchange:
		return "灵晶兑换 +5%"
	default:
		return "未知卦象"
	}
}

// getSectFortuneTx 读取宗门今日卦象；不存在返回 (nil, nil)。
func getSectFortuneTx(tx *gorm.DB, sectID int64, dayKey string) (*SectFortune, error) {
	if tx == nil {
		return nil, fmt.Errorf("SECT_FORTUNE_TX_EMPTY")
	}
	var f SectFortune
	if err := tx.Where("sect_id = ? AND day_key = ?", sectID, dayKey).First(&f).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return &f, nil
}

// GetSectFortuneToday 读取宗门今日卦象（展示用，无事务）
func GetSectFortuneToday(sectID int64) *SectFortune {
	f, err := getSectFortuneTx(db, sectID, sectDayKey(time.Now()))
	if err != nil {
		return nil
	}
	return f
}

// sectFortuneBuffPctForUserTx 用户所在宗门当日卦象命中 buffType 时返回其加成百分比，否则 0。
// tx 为 DB 句柄：事务内调用方必须传 tx，避免占用独立连接（小连接池下会死等）。
func sectFortuneBuffPctForUserTx(tx *gorm.DB, userID int64, buffType string) (float64, error) {
	if tx == nil {
		return 0, fmt.Errorf("SECT_FORTUNE_PCT_TX_EMPTY")
	}
	var m SectMember
	if err := tx.Where("user_id = ?", userID).First(&m).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	f, err := getSectFortuneTx(tx, m.SectID, sectDayKey(time.Now()))
	if err != nil {
		return 0, err
	}
	if f == nil || f.BuffType != buffType {
		return 0, nil
	}
	return sectFortuneBuffPct[buffType], nil
}

// sectFortuneBuffPctForUser 非事务版（读取失败/未命中均按 0 处理，供收益链路调用）
func sectFortuneBuffPctForUser(userID int64, buffType string) float64 {
	pct, err := sectFortuneBuffPctForUserTx(db, userID, buffType)
	if err != nil {
		return 0
	}
	return pct
}

// sectFortuneBuffPctForSectTx 宗门当日卦象命中 buffType 时返回其加成百分比，否则 0（按宗门读取，供全宗统一生效的收益链调用）。
func sectFortuneBuffPctForSectTx(tx *gorm.DB, sectID int64, buffType string) (float64, error) {
	if tx == nil {
		return 0, fmt.Errorf("SECT_FORTUNE_PCT_TX_EMPTY")
	}
	f, err := getSectFortuneTx(tx, sectID, sectDayKey(time.Now()))
	if err != nil {
		return 0, err
	}
	if f == nil || f.BuffType != buffType {
		return 0, nil
	}
	return sectFortuneBuffPct[buffType], nil
}

// applySectFortuneNetCultivationBuff 应用「今日净修为 +5%」卦象：
// 用户所在宗门当日命中 net_cultivation 卦象时，按比例放大传入的净修为小时值。
// 只放大最终展示/任务口径，不改变 ABS 原始秒数与 Beijing 当日分桶规则。
func applySectFortuneNetCultivationBuff(userID int64, hours float64) float64 {
	pct := sectFortuneBuffPctForUser(userID, sectFortuneBuffNetCultivation)
	if pct <= 0 {
		return hours
	}
	return hours * (1 + pct)
}

// openSectFortune 开运（宗主/长老）：随机抽当日卦象，扣宗门声望。
// 返回 buffType、本次 cost、当日累计 roll 次数。
func openSectFortune(userID int64, operatorName string) (string, int, int, error) {
	var buffType string
	var cost int
	var roll int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var member SectMember
		if err := tx.Where("user_id = ?", userID).First(&member).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return errNotInSect
		}
		if !canUpgradeSectAsset(member.Role) {
			return errSectOnlyOwner
		}

		var sect Sect
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", member.SectID).First(&sect).Error; err != nil {
			return err
		}

		dayKey := sectDayKey(time.Now())
		existing, err := getSectFortuneTx(tx, int64(sect.ID), dayKey)
		if err != nil {
			return err
		}
		if existing != nil && existing.RollCount >= sectFortuneDailyLimit {
			return errSectFortuneDailyLimit
		}

		rollCount := 0
		if existing != nil {
			rollCount = existing.RollCount
		}
		cost = sectFortuneCostByRoll[rollCount]
		roll = rollCount + 1

		res := tx.Model(&Sect{}).
			Where("id = ? AND prestige >= ?", sect.ID, cost).
			UpdateColumn("prestige", gorm.Expr("prestige - ?", cost))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errSectPrestigeNotEnough
		}

		buffType = sectFortuneBuffTypes[rand.Intn(len(sectFortuneBuffTypes))]

		if existing == nil {
			f := SectFortune{
				SectID:       int64(sect.ID),
				DayKey:       dayKey,
				BuffType:     buffType,
				OpenedBy:     userID,
				OpenedByName: operatorName,
				Cost:         cost,
				RollCount:    1,
			}
			if err := tx.Create(&f).Error; err != nil {
				if isUniqueConstraintError(err) {
					return errSectFortuneConcurrent
				}
				return err
			}
		} else {
			upRes := tx.Model(&SectFortune{}).
				Where("id = ? AND roll_count = ?", existing.ID, rollCount).
				Updates(map[string]interface{}{
					"buff_type":      buffType,
					"opened_by":      userID,
					"opened_by_name": operatorName,
					"cost":           cost,
					"roll_count":     gorm.Expr("roll_count + 1"),
				})
			if upRes.Error != nil {
				return upRes.Error
			}
			if upRes.RowsAffected == 0 {
				return errSectFortuneConcurrent
			}
		}

		if err := writeAuditLogInTx(tx, userID, "SECT_FORTUNE_OPEN", fmt.Sprintf("sect:%d", sect.ID), cost,
			fmt.Sprintf("宗门运势开运，第 %d 次，卦象 %s，消耗宗门声望 %d", roll, sectFortuneBuffName(buffType), cost)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return buffType, cost, roll, errNotInSect
		}
	}
	return buffType, cost, roll, err
}
