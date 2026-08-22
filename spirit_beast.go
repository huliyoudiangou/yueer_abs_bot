package main

// ==========================================
// 护宗神兽 · 喂养（Phase 1）
//
// 设计（锁定）：
// - 解锁：宗门声望 >= 2000
// - 喂养：消耗宗门声望，神兽等级 +1
// - 成本档位（方案B，按当前等级）：<10 级 20 / 10-29 级 25 / 30-59 级 35 / 60 级+ 50
// - 三阶段（buff 为全宗世界 Boss 伤害加成，加法叠加）：
//   10 级 → 1 阶 +1%；30 级 → 2 阶 +2%；60 级 → 3 阶 +3.5%
// - 贡献：每次喂养记 SectBeastContribution（个人贡献）
//
// 资产规则：
// - 宗门声望扣减用条件更新（prestige >= cost）防并发超扣
// - 扣声望/升神兽/记贡献在同一事务
// ==========================================

import (
	"errors"
	"fmt"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const sectBeastUnlockPrestige = 2000 // 解锁门槛：宗门声望

// 喂养成本档位（方案B）：当前等级达到 MinLevel 后采用该档成本
var sectBeastFeedCostBands = []struct {
	MinLevel int
	Cost     int
}{
	{0, 20},
	{10, 25},
	{30, 35},
	{60, 50},
}

// 阶段阈值与 buff：达到 Level → 进入 Stage，buffPct 为伤害加成
var sectBeastStageThresholds = []struct {
	Stage   int
	Level   int
	BuffPct float64
}{
	{1, 10, 0.01},
	{2, 30, 0.02},
	{3, 60, 0.035},
}

// sectBeastStageNames 各阶段神兽称谓（0=未喂养）
var sectBeastStageNames = []string{
	"封印灵兽",
	"觉醒护宗",
	"金甲护宗",
	"圣辉护宗",
}

// sectBeastFeedCost 当前等级的喂养成本
func sectBeastFeedCost(level int) int {
	cost := sectBeastFeedCostBands[0].Cost
	for _, b := range sectBeastFeedCostBands {
		if level >= b.MinLevel {
			cost = b.Cost
		}
	}
	return cost
}

// sectBeastStageForLevel 按等级计算应处阶段
func sectBeastStageForLevel(level int) int {
	stage := 0
	for _, s := range sectBeastStageThresholds {
		if level >= s.Level {
			stage = s.Stage
		}
	}
	return stage
}

// sectBeastStageBuff 阶段对应 buff（小数）
func sectBeastStageBuff(stage int) float64 {
	for _, s := range sectBeastStageThresholds {
		if s.Stage == stage {
			return s.BuffPct
		}
	}
	return 0
}

// sectBeastNextStageLevel 下一阶段所需等级（满阶返回 0）
func sectBeastNextStageLevel(stage int) int {
	for _, s := range sectBeastStageThresholds {
		if s.Stage > stage {
			return s.Level
		}
	}
	return 0
}

// FeedSectBeast 喂养护宗神兽一次（扣宗门声望 → 等级+1 → 记贡献）
func FeedSectBeast(userID int64) (*SectBeast, int, error) {
	var member SectMember
	if err := db.Where("user_id = ?", userID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, 0, fmt.Errorf("你尚未加入宗门，无法喂养护宗神兽")
		}
		return nil, 0, err
	}

	var result *SectBeast
	var cost int
	err := db.Transaction(func(tx *gorm.DB) error {
		var sect Sect
		if err := tx.Where("id = ?", member.SectID).First(&sect).Error; err != nil {
			return fmt.Errorf("宗门不存在")
		}
		if sect.Prestige < sectBeastUnlockPrestige {
			return fmt.Errorf("护宗神兽尚未觉醒：需宗门声望 %d，当前 %d",
				sectBeastUnlockPrestige, sect.Prestige)
		}

		var beast SectBeast
		if err := tx.Where("sect_id = ?", sect.ID).First(&beast).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			beast = SectBeast{SectID: sect.ID}
		}

		cost = sectBeastFeedCost(beast.Level)
		if sect.Prestige < cost {
			return fmt.Errorf("宗门声望不足：喂养需 %d，当前 %d", cost, sect.Prestige)
		}

		// 条件更新扣声望（并发安全：WHERE prestige >= cost）
		res := tx.Model(&Sect{}).Where("id = ? AND prestige >= ?", sect.ID, cost).
			Update("prestige", gorm.Expr("prestige - ?", cost))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("宗门声望不足：喂养需 %d，当前 %d", cost, sect.Prestige)
		}

		// 神兽升级 + 阶段（条件更新防并发丢更新：同一 level 上恰好一个事务能成功，
		// 失败者提示重试；声望扣减与升级同事务，失败整体回滚不产生“扣了没升”）
		// 首次喂养（beast.ID==0）靠 sect_id 唯一索引防并发重复建行
		oldStage := beast.Stage
		newLevel := beast.Level + 1
		if beast.ID == 0 {
			beast.Level = newLevel
			beast.TotalFed += cost
			if newStage := sectBeastStageForLevel(newLevel); newStage > beast.Stage {
				beast.Stage = newStage
			}
			if err := tx.Create(&beast).Error; err != nil {
				if isUniqueConstraintError(err) {
					// 并发首喂：同门已建行，本次回滚（声望同退）
					return fmt.Errorf("神兽正被同门喂养，请稍后再试")
				}
				return err
			}
		} else {
			resBeast := tx.Model(&SectBeast{}).
				Where("id = ? AND level = ?", beast.ID, beast.Level).
				Updates(map[string]interface{}{
					"level":     newLevel,
					"total_fed": gorm.Expr("total_fed + ?", cost),
				})
			if resBeast.Error != nil {
				return resBeast.Error
			}
			if resBeast.RowsAffected == 0 {
				return fmt.Errorf("神兽正被同门喂养，请稍后再试")
			}
			beast.Level = newLevel
			beast.TotalFed += cost
			if newStage := sectBeastStageForLevel(newLevel); newStage > beast.Stage {
				// 升阶条件更新：每次喂养 level+1，阶段至多前进一阶，条件保证只有一个事务成功
				resStage := tx.Model(&SectBeast{}).
					Where("id = ? AND stage = ?", beast.ID, beast.Stage).
					Update("stage", newStage)
				if resStage.Error != nil {
					return resStage.Error
				}
				if resStage.RowsAffected > 0 {
					beast.Stage = newStage
				}
			}
		}
		if beast.Stage > oldStage {
			log.Printf("[灵侍] 护宗神兽升阶 sect=%d user=%d level=%d stage=%d",
				sect.ID, userID, beast.Level, beast.Stage)
		}

		// 贡献记录
		c := SectBeastContribution{
			UserID:    userID,
			SectID:    sect.ID,
			Buff:      cost,
			PointType: "个人贡献",
		}
		if err := tx.Create(&c).Error; err != nil {
			return err
		}
		result = &beast
		return nil
	})
	if err != nil {
		log.Printf("[灵侍] 神兽喂养失败 user=%d sect=%d err=%s",
			userID, member.SectID, formatTelegramSendError(err))
		return nil, 0, err
	}
	return result, cost, nil
}

// SectBeastLeader 神兽喂养贡献排行条目
type SectBeastLeader struct {
	UserID int64
	Total  int
}

// GetSectBeastLeaders 宗门喂养贡献排行（topN）
func GetSectBeastLeaders(sectID int64, topN int) []SectBeastLeader {
	var rows []SectBeastLeader
	db.Model(&SectBeastContribution{}).
		Select("user_id, SUM(buff) AS total").
		Where("sect_id = ?", sectID).
		Group("user_id").
		Order("total desc").
		Limit(topN).
		Find(&rows)
	return rows
}

// getSectBeastDamageBonus 护宗神兽世界 Boss 伤害 buff（0 / 1% / 2% / 3.5%）
// 供 world_boss 加成链调用（DB 为本模块惯例变量）
func getSectBeastDamageBonus(userID int64) float64 {
	var member SectMember
	if err := db.Where("user_id = ?", userID).First(&member).Error; err != nil {
		return 0
	}
	var beast SectBeast
	if err := db.Where("sect_id = ?", member.SectID).First(&beast).Error; err != nil {
		return 0
	}
	return sectBeastStageBuff(beast.Stage)
}

// handleSectBeastPanel 宗门菜单入口（🏯 宗门 → 护宗神兽）：发送神兽面板（新消息）。
// 由 HandleSectCommand 的「护宗神兽」文本命令触发；群聊提示转私聊。
func handleSectBeastPanel(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.From == nil || msg.Chat == nil {
		return
	}
	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "🔮 护宗神兽仅在私聊开放，请私聊我使用。")
		return
	}
	text, kb := spiritPanelBeast(msg.From.ID)
	m := tgbotapi.NewMessage(msg.Chat.ID, text)
	m.ReplyMarkup = kb
	if _, err := bot.Send(m); err != nil {
		log.Printf("[灵侍] 发送护宗神兽面板失败 user=%d chat=%d err=%s", msg.From.ID, msg.Chat.ID, formatTelegramSendError(err))
	}
}
