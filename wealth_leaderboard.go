package main

import (
	"fmt"
	"log"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const (
	wealthLeaderboardCallbackPrefix = "wealth_page:"
	wealthLeaderboardLimit          = 50
	wealthLeaderboardPageSize       = 30
)

func isWealthLeaderboardCommand(text string) bool {
	return strings.Contains(text, "财富榜") || strings.Contains(text, "积分榜")
}

func handleWealthLeaderboardCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.Chat == nil {
		return
	}

	users, totalUsers, page, err := loadWealthLeaderboardPage(1)
	if err != nil {
		log.Printf("⚠️ 财富榜读取失败: chat=%d page=%d err=%s", msg.Chat.ID, 1, formatPlainError(err))
		replyText(bot, msg.Chat.ID, "❌ 获取积分排行榜失败，请稍后重试。")
		return
	}
	if totalUsers == 0 {
		replyText(bot, msg.Chat.ID, "🫙 当前全服还没有任何平民玩家拥有积分记录。")
		return
	}

	markup := wealthLeaderboardPageMarkup(page, wealthLeaderboardTotalPages(totalUsers))
	out := tgbotapi.NewMessage(msg.Chat.ID, formatWealthLeaderboardPage(users, totalUsers, page))
	out.ParseMode = "Markdown"
	out.ReplyMarkup = markup
	if _, err := sendAutoDelete(bot, out); err != nil {
		log.Printf("⚠️ 财富榜发送失败: chat=%d page=%d err=%s", msg.Chat.ID, page, formatTelegramSendError(err))
	}
}

func handleWealthLeaderboardCallback(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery) bool {
	if cb == nil || cb.From == nil || !strings.HasPrefix(cb.Data, wealthLeaderboardCallbackPrefix) {
		return false
	}
	if cb.Message == nil || cb.Message.Chat == nil {
		answerCallback(bot, cb.ID, "财富榜已失效，请重新发送“财富榜”。")
		return true
	}

	pageText := strings.TrimPrefix(cb.Data, wealthLeaderboardCallbackPrefix)
	page, err := strconv.Atoi(pageText)
	if err != nil || page < 1 {
		answerCallback(bot, cb.ID, "页码无效")
		return true
	}

	users, totalUsers, actualPage, err := loadWealthLeaderboardPage(page)
	if err != nil {
		log.Printf("⚠️ 财富榜分页读取失败: chat=%d message=%d user=%d page=%d err=%s", cb.Message.Chat.ID, cb.Message.MessageID, cb.From.ID, page, formatPlainError(err))
		answerCallback(bot, cb.ID, "财富榜读取失败")
		return true
	}
	if totalUsers == 0 {
		answerCallback(bot, cb.ID, "暂无财富榜")
		return true
	}

	markup := wealthLeaderboardPageMarkup(actualPage, wealthLeaderboardTotalPages(totalUsers))
	edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, formatWealthLeaderboardPage(users, totalUsers, actualPage))
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &markup
	if _, err := bot.Send(edit); err != nil {
		if isTelegramMessageNotModifiedError(err) {
			answerCallback(bot, cb.ID, fmt.Sprintf("第 %d 页", actualPage))
			return true
		}
		log.Printf("⚠️ 财富榜分页消息编辑失败: chat=%d message=%d user=%d page=%d err=%s", cb.Message.Chat.ID, cb.Message.MessageID, cb.From.ID, actualPage, formatTelegramSendError(err))
		answerCallback(bot, cb.ID, "分页刷新失败")
		return true
	}

	answerCallback(bot, cb.ID, fmt.Sprintf("第 %d 页", actualPage))
	return true
}

func loadWealthLeaderboardPage(page int) ([]User, int, int, error) {
	if page < 1 {
		page = 1
	}

	query := wealthLeaderboardBaseQuery()
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, page, err
	}

	totalUsers := int(total)
	if totalUsers > wealthLeaderboardLimit {
		totalUsers = wealthLeaderboardLimit
	}
	totalPages := wealthLeaderboardTotalPages(totalUsers)
	if page > totalPages {
		page = totalPages
	}

	offset := (page - 1) * wealthLeaderboardPageSize
	limit := wealthLeaderboardPageSize
	if remaining := wealthLeaderboardLimit - offset; remaining < limit {
		limit = remaining
	}
	if limit < 0 {
		limit = 0
	}

	var users []User
	if limit > 0 {
		if err := wealthLeaderboardBaseQuery().
			Order("points desc, telegram_id asc").
			Limit(limit).
			Offset(offset).
			Find(&users).Error; err != nil {
			return nil, totalUsers, page, err
		}
	}

	return users, totalUsers, page, nil
}

func wealthLeaderboardBaseQuery() *gorm.DB {
	query := DB.Model(&User{}).Where("role != ? AND role != ?", "super_admin", "admin")
	if AppConfig != nil {
		envAdminIDs := make([]int64, 0, len(AppConfig.AdminIDs))
		for id := range AppConfig.AdminIDs {
			envAdminIDs = append(envAdminIDs, id)
		}
		if len(envAdminIDs) > 0 {
			query = query.Where("telegram_id NOT IN ?", envAdminIDs)
		}
	}
	return query
}

func wealthLeaderboardTotalPages(totalUsers int) int {
	if totalUsers <= 0 {
		return 1
	}
	pages := (totalUsers + wealthLeaderboardPageSize - 1) / wealthLeaderboardPageSize
	if pages < 1 {
		return 1
	}
	return pages
}

func formatWealthLeaderboardPage(users []User, totalUsers int, page int) string {
	totalPages := wealthLeaderboardTotalPages(totalUsers)
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	startRank := (page-1)*wealthLeaderboardPageSize + 1

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🏆 **全服积分财富榜 Top %d**\n页码：`%d/%d`\n\n", wealthLeaderboardLimit, page, totalPages))
	for i, u := range users {
		rank := startRank + i
		medal := "▪️"
		if rank == 1 {
			medal = "🥇"
		} else if rank == 2 {
			medal = "🥈"
		} else if rank == 3 {
			medal = "🥉"
		}
		b.WriteString(fmt.Sprintf("%s 第%d名 **%s** : `%d` 积分\n", medal, rank, escapeMarkdown(u.Username), u.Points))
	}
	if len(users) == 0 {
		b.WriteString("暂无道友上榜。\n")
	}
	b.WriteString("\n💡 *提示：每天点击【📆 每日签到】或参与群内抢红包可快速积攒财富！*")
	return strings.TrimRight(b.String(), "\n")
}

func wealthLeaderboardPageMarkup(page int, totalPages int) tgbotapi.InlineKeyboardMarkup {
	if totalPages <= 1 {
		return tgbotapi.InlineKeyboardMarkup{}
	}
	row := make([]tgbotapi.InlineKeyboardButton, 0, 2)
	if page > 1 {
		row = append(row, tgbotapi.NewInlineKeyboardButtonData("上一页", fmt.Sprintf("%s%d", wealthLeaderboardCallbackPrefix, page-1)))
	}
	if page < totalPages {
		row = append(row, tgbotapi.NewInlineKeyboardButtonData("下一页", fmt.Sprintf("%s%d", wealthLeaderboardCallbackPrefix, page+1)))
	}
	return tgbotapi.NewInlineKeyboardMarkup(row)
}
