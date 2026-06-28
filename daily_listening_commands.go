package main

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

func handleDailyListeningStatCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, role string) bool {
	if msg == nil || msg.From == nil || msg.Chat == nil {
		return false
	}

	text = strings.TrimSpace(text)
	if !isDailyListeningStatCommandText(text) {
		return false
	}

	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "每日净修为刷新和查询请私聊 Bot 执行。")
		return true
	}

	switch {
	case text == "刷新我的今日净修为":
		handleRefreshMyDailyListeningStat(bot, msg)
		return true
	case text == "刷新宗门今日净修为":
		handleRefreshSectDailyListeningStatsCommand(bot, msg)
		return true
	case text == "刷新全服今日净修为":
		if role != "super_admin" {
			replyText(bot, msg.Chat.ID, "❌ 权限不足：该操作仅限超级管理员。")
			return true
		}
		handleRefreshAllDailyListeningStats(bot, msg)
		return true
	case strings.HasPrefix(text, "查看每日净修为"):
		if role != "super_admin" && role != "admin" {
			replyText(bot, msg.Chat.ID, "❌ 权限不足。")
			return true
		}
		handleQueryDailyListeningStat(bot, msg, text)
		return true
	default:
		return false
	}
}

func isDailyListeningStatCommandText(text string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}
	if len(fields) == 1 {
		return fields[0] == "刷新我的今日净修为" ||
			fields[0] == "刷新宗门今日净修为" ||
			fields[0] == "刷新全服今日净修为" ||
			fields[0] == "查看每日净修为"
	}
	return fields[0] == "查看每日净修为"
}

func refreshUserDailyListeningAndCultivation(userID int64) (DailyListeningStat, float64, bool, error) {
	now := time.Now()
	var u User
	if err := DB.Select("telegram_id", "abs_user_id").Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		return DailyListeningStat{}, 0, false, err
	}
	if strings.TrimSpace(u.AbsUserID) == "" {
		return DailyListeningStat{}, 0, false, errAbsUserIDEmpty
	}

	if _, ok := refreshDailyListeningStatsFromABS(userID, u.AbsUserID); !ok {
		if stat, cacheOK := getTodayDailyListeningStat(userID, now); cacheOK {
			totalEffective, totalOK := totalCultivationEffectiveHoursFromDailyStats(userID, now)
			if !totalOK {
				return DailyListeningStat{}, 0, false, errDailyListeningStatsReadFailed
			}
			return stat, totalEffective, true, errAbsRefreshFailedUsingCache
		}
		return DailyListeningStat{}, 0, false, errAbsRefreshFailed
	}

	totalEffective, syncOK := syncCultivationFromDailyListeningStatsAt(userID, now)
	if !syncOK {
		return DailyListeningStat{}, 0, false, errDailyListeningStatsReadFailed
	}
	stat, ok := getTodayDailyListeningStat(userID, now)
	return stat, totalEffective, ok, nil
}

func syncCultivationFromDailyListeningStats(userID int64) (float64, bool) {
	return syncCultivationFromDailyListeningStatsAt(userID, time.Now())
}

func syncCultivationFromDailyListeningStatsAt(userID int64, now time.Time) (float64, bool) {
	totalEffective, ok := totalCultivationEffectiveHoursFromDailyStats(userID, now)
	if !ok {
		return 0, false
	}
	cul := GetOrCreateCultivation(userID)
	if cul == nil {
		log.Printf("⚠️ 每日净修为同步读取修仙档案失败: user=%d", userID)
		return 0, false
	}
	oldHours := cul.TotalAudioTime
	cul.TotalAudioTime = totalEffective
	persistCultivationAudioTime(userID, totalEffective)
	SyncCultivationRealm(cul)
	awardSectListeningContribution(userID, oldHours, totalEffective)
	return totalEffective, true
}

func totalCultivationEffectiveHoursFromDailyStats(userID int64, now time.Time) (float64, bool) {
	total, ok := sumDailyListeningEffectiveHours(userID)
	if !ok {
		return 0, false
	}
	return total + activeSectCaveRetreatBonusHours(userID, now), true
}

func handleRefreshMyDailyListeningStat(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	stat, totalEffective, ok, err := refreshUserDailyListeningAndCultivation(msg.From.ID)
	if err != nil && !ok {
		replyText(bot, msg.Chat.ID, "❌ 刷新今日净修为失败，请稍后再试。")
		return
	}
	if !ok {
		replyText(bot, msg.Chat.ID, "📭 今日暂无有效听书记录。")
		return
	}

	replyText(bot, msg.Chat.ID, formatDailyListeningStat("✅ 今日净修为已刷新", stat, totalEffective))
}

func handleRefreshSectDailyListeningStatsCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	var member SectMember
	if err := DB.Where("user_id = ?", msg.From.ID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, msg.Chat.ID, "❌ 您当前没有加入宗门。")
		} else {
			log.Printf("⚠️ 刷新宗门每日净修为读取成员档案失败: user=%d err=%s", msg.From.ID, formatPlainError(err))
			replyText(bot, msg.Chat.ID, "❌ 宗门成员档案读取失败，请稍后再试。")
		}
		return
	}
	if !canViewSectListeningStats(member) {
		replyText(bot, msg.Chat.ID, "❌ 仅宗主或长老可以刷新宗门净修为。")
		return
	}

	count := refreshSectMembersDailyListeningStats(member.SectID, 100)
	replyText(bot, msg.Chat.ID, fmt.Sprintf("✅ 已刷新本宗门 `%d` 名成员的每日净修为。", count))
}

func handleRefreshAllDailyListeningStats(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	success, total, skipped := refreshAllDailyListeningStatsWithOptions(true)
	replyText(bot, msg.Chat.ID, fmt.Sprintf("✅ 全服今日净修为刷新完成：成功 `%d/%d`，跳过 `%d`。", success, total, skipped))
}

func handleQueryDailyListeningStat(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) {
	fields := strings.Fields(text)
	if len(fields) < 2 {
		replyText(bot, msg.Chat.ID, "用法：`查看每日净修为 用户ID [YYYY-MM-DD]`")
		return
	}

	targetID, err := strconv.ParseInt(fields[1], 10, 64)
	if err != nil || targetID == 0 {
		replyText(bot, msg.Chat.ID, "❌ 用户ID格式错误。")
		return
	}

	dayKey := sectDayKey(time.Now())
	if len(fields) >= 3 {
		dayKey = strings.TrimSpace(fields[2])
	}

	var stat DailyListeningStat
	if err := DB.Where("user_id = ? AND day_key = ?", targetID, dayKey).First(&stat).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, msg.Chat.ID, "📭 未找到该用户该日期的每日净修为记录。")
		} else {
			log.Printf("⚠️ 查询每日净修为记录失败: user=%d day=%s err=%s", targetID, formatPlainValue(dayKey), formatPlainError(err))
			replyText(bot, msg.Chat.ID, "❌ 每日净修为记录读取失败，请稍后再试。")
		}
		return
	}

	now := time.Now()
	totalEffective, totalOK := totalCultivationEffectiveHoursFromDailyStats(targetID, now)
	if !totalOK {
		replyText(bot, msg.Chat.ID, "❌ 每日净修为累计读取失败，请稍后再试。")
		return
	}
	replyText(bot, msg.Chat.ID, formatDailyListeningStat(fmt.Sprintf("📊 用户 `%d` 每日净修为", targetID), stat, totalEffective))
}

func formatDailyListeningStat(title string, stat DailyListeningStat, totalEffective float64) string {
	fetchStatus := stat.FetchStatus
	if fetchStatus == "" {
		fetchStatus = "unknown"
	}
	refreshReason := stat.RefreshReason
	if refreshReason == "" {
		refreshReason = stat.Source
	}

	retreatBonus := activeSectCaveRetreatBonusHours(stat.UserID, time.Now())
	retreatLine := ""
	if retreatBonus > 0 {
		retreatLine = fmt.Sprintf("\n洞府闭关加成：`+%.2f` 小时", retreatBonus)
	}

	return fmt.Sprintf(
		"%s\n\n日期：`%s`\n实际听书：`%.2f` 小时\n封顶计入：`%.2f` 小时\n今日净修为：`%.2f` 小时%s\n总净修为：`%.2f` 小时\n更新时间：`%s`\n来源：`%s`\n刷新状态：`%s`\n刷新原因：`%s`",
		title,
		stat.DayKey,
		stat.RawSeconds/3600.0,
		stat.CappedSeconds/3600.0,
		stat.EffectiveHours,
		retreatLine,
		totalEffective,
		stat.LastFetchedAt.Format("2006-01-02 15:04:05"),
		stat.Source,
		fetchStatus,
		refreshReason,
	)
}
