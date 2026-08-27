package main

// ==========================================
// 宗门运势 · 每日卦象（用户侧命令入口）
// 入口：🏯 宗门 → 宗门运势（文本命令「宗门运势」），宗主/长老可「开运」。
// ==========================================

import (
	"errors"
	"fmt"
	"log"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

// loadSectFortuneContext 读取操作者成员与宗门档案（用于运势面板/开运前置）
func loadSectFortuneContext(msg *tgbotapi.Message) (*SectMember, *Sect, error) {
	var member SectMember
	if err := DB.Where("user_id = ?", msg.From.ID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, errNotInSect
		}
		return nil, nil, err
	}
	var sect Sect
	if err := DB.Where("id = ?", member.SectID).First(&sect).Error; err != nil {
		return nil, nil, err
	}
	return &member, &sect, nil
}

// handleSectFortune 宗门运势面板：查看今日卦象；宗主/长老可开运。群聊提示转私聊。
func handleSectFortune(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.From == nil {
		return
	}
	if !msg.Chat.IsPrivate() {
		replyText(bot, msg.Chat.ID, "🔮 宗门运势仅在私聊开放，请私聊我使用。")
		return
	}
	member, sect, err := loadSectFortuneContext(msg)
	if err != nil {
		if errors.Is(err, errNotInSect) {
			replyText(bot, msg.Chat.ID, "❌ 您当前没有加入宗门。")
		} else {
			log.Printf("⚠️ 宗门运势面板读取失败: user=%d err=%s", msg.From.ID, formatPlainError(err))
			replyText(bot, msg.Chat.ID, "❌ 宗门运势读取失败，请稍后再试。")
		}
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🏮 **%s · 宗门运势**\n\n", escapeMarkdown(sect.Name)))

	f := GetSectFortuneToday(member.SectID)
	if f == nil {
		b.WriteString("今日尚未开运，全宗暂无卦象。\n")
	} else {
		b.WriteString(fmt.Sprintf("今日卦象：**%s**\n开运人：%s\n已开运：%d 次\n\n", sectFortuneBuffName(f.BuffType), escapeMarkdown(f.OpenedByName), f.RollCount))
	}

	b.WriteString("卦象对全宗当日生效（贡献 / 秘境奖励 / 今日净修为 / 世界Boss伤害 / 灵晶兑换）。\n\n")

	if canUpgradeSectAsset(member.Role) {
		if f != nil && f.RollCount >= sectFortuneDailyLimit {
			b.WriteString(fmt.Sprintf("今日开运已达上限（%d 次）。\n", sectFortuneDailyLimit))
		} else {
			nextIdx := 0
			if f != nil && f.RollCount < sectFortuneDailyLimit {
				nextIdx = f.RollCount
			}
			b.WriteString(fmt.Sprintf("开运消耗宗门声望 `%d`（宗主/长老）。\n发送 `开运` 抽取/刷新当日卦象。", sectFortuneCostByRoll[nextIdx]))
		}
	} else {
		b.WriteString("普通成员仅可查看今日卦象，宗主/长老可开运。")
	}

	replyText(bot, msg.Chat.ID, b.String())
}

// handleOpenSectFortune 开运：抽/刷新当日卦象（宗主/长老）。群聊提示转私聊。
func handleOpenSectFortune(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.From == nil {
		return
	}
	if !msg.Chat.IsPrivate() {
		replyText(bot, msg.Chat.ID, "🔮 开运仅在私聊开放，请私聊我使用。")
		return
	}
	buff, cost, roll, err := openSectFortune(msg.From.ID, getTelegramDisplayName(msg.From))
	if err != nil {
		switch sectErrorCode(err) {
		case "NOT_IN_SECT":
			replyText(bot, msg.Chat.ID, "❌ 您当前没有加入宗门，无法开运。")
		case "ONLY_OWNER":
			replyText(bot, msg.Chat.ID, "❌ 只有宗主或长老可以开运。")
		case "PRESTIGE_NOT_ENOUGH":
			replyText(bot, msg.Chat.ID, "❌ 宗门声望不足，无法开运。")
		case "SECT_FORTUNE_DAILY_LIMIT":
			replyText(bot, msg.Chat.ID, "❌ 今日开运次数已达上限。")
		case "SECT_FORTUNE_CONCURRENT":
			replyText(bot, msg.Chat.ID, "⚠️ 开运发生并发冲突，请稍后重试。")
		default:
			log.Printf("⚠️ 宗门运势开运失败: user=%d err=%s", msg.From.ID, formatPlainError(err))
			replyText(bot, msg.Chat.ID, "❌ 开运失败，请稍后再试。")
		}
		return
	}
	replyText(bot, msg.Chat.ID, fmt.Sprintf("🔮 **开运成功！**\n\n今日第 `%d` 次开运\n卦象：**%s**\n消耗宗门声望：`%d`\n\n该卦象对全宗今日生效。", roll, sectFortuneBuffName(buff), cost))
}
