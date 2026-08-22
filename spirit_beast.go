package main

// ==========================================
// 护宗神兽 · 喂养（Phase 1）
//
// 设计（锁定）：
// - 解锁：宗门声望 >= 2000
// - 喂养：神兽等级 +1，两种出资方式（1:1 等价）：
//   * 声望喂养：消耗宗门声望，仅宗主/长老可执行（普通成员无权动用宗门声望）
//   * 积分喂养：消耗个人积分（成本 = 声望成本 1:1），全体宗门成员可执行
// - 成本档位（方案B，按当前等级）：<10 级 20 / 10-29 级 25 / 30-59 级 35 / 60 级+ 50
// - 三阶段（buff 为全宗世界 Boss 伤害加成，加法叠加）：
//   10 级 → 1 阶 +1%；30 级 → 2 阶 +2%；60 级 → 3 阶 +3.5%
// - 贡献：每次喂养记 SectBeastContribution（PointType 区分出资：宗门声望/个人积分）
//
// 资产规则：
// - 声望喂养：宗门声望扣减用条件更新（prestige >= cost）防并发超扣
// - 积分喂养：applyPointDeltaInTx（条件更新 points >= cost + PointTransaction 流水），不能扣成负数
// - 出资扣减/升神兽/记贡献在同一事务
// ==========================================

import (
	"errors"
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const sectBeastUnlockPrestige = 2000 // 解锁门槛：宗门声望

// 喂养出资方式（与声望成本 1:1 等价）
const (
	sectBeastFeedModePrestige = "prestige" // 消耗宗门声望（仅宗主/长老）
	sectBeastFeedModePoints   = "points"   // 消耗个人积分（全体成员）
)

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

// FeedSectBeast 喂养护宗神兽一次（出资扣减 → 等级+1 → 记贡献）。
// mode：sectBeastFeedModePrestige 用宗门声望（仅宗主/长老）；
//
//	sectBeastFeedModePoints 用个人积分（成本与声望 1:1，全体成员可执行）
func FeedSectBeast(userID int64, mode string) (*SectBeast, int, error) {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode != sectBeastFeedModePrestige && mode != sectBeastFeedModePoints {
		return nil, 0, fmt.Errorf("未知的喂养方式")
	}

	var result *SectBeast
	var cost int
	var member SectMember
	err := db.Transaction(func(tx *gorm.DB) error {
		// 成员与职位在事务内读取：与出资扣减共用同一快照，
		// 避免并发转让宗主/任命长老时，职位读取与声望扣减之间被插入
		if err := tx.Where("user_id = ?", userID).First(&member).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("你尚未加入宗门，无法喂养护宗神兽")
			}
			return err
		}

		// 声望喂养消耗宗门资产：仅宗主/长老（与宗门升级/科技权限边界一致）
		if mode == sectBeastFeedModePrestige && !canUpgradeSectAsset(member.Role) {
			return fmt.Errorf("普通成员不能动用宗门声望喂养，请改用积分喂养")
		}

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

		if mode == sectBeastFeedModePrestige {
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
		} else {
			var u User
			if err := tx.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return fmt.Errorf("用户档案不存在，无法积分喂养")
				}
				return err
			}
			if u.Points < cost {
				return fmt.Errorf("积分不足：喂养需 %d，当前 %d", cost, u.Points)
			}
			// 积分扣除：条件更新 points >= cost + PointTransaction 流水，与神兽升级同事务
			if err := applyPointDeltaInTx(
				tx,
				userID,
				-cost,
				"beast_feed_points",
				fmt.Sprintf("喂养护宗神兽（喂养前等级 %d），消耗 %d 积分", beast.Level, cost),
				"sect_beast",
				fmt.Sprintf("%d", sect.ID),
			); err != nil {
				if errors.Is(err, errPointsNotEnough) {
					return fmt.Errorf("积分不足：喂养需 %d", cost)
				}
				return err
			}
		}

		// 神兽升级 + 阶段（条件更新防并发丢更新：同一 level 上恰好一个事务能成功，
		// 失败者提示重试；出资扣减与升级同事务，失败整体回滚不产生“扣了没升”）
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
					// 并发首喂：同门已建行，本次回滚（已扣出资同退）
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

		// 贡献记录（PointType 记录出资方式，供审计区分声望/积分）
		pointType := "个人积分"
		if mode == sectBeastFeedModePrestige {
			pointType = "宗门声望"
		}
		c := SectBeastContribution{
			UserID:    userID,
			SectID:    sect.ID,
			Buff:      cost,
			PointType: pointType,
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
