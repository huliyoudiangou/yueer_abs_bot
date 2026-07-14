package main

import (
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func HandleCultivationAdminReadOnlyCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, text string) bool {
	if text != "查看修仙配置" && text != "查看突破配置" && text != "查看境界配置" {
		return false
	}

	if message == nil || message.From == nil {
		return true
	}

	registerIncomingGroupCommandForAutoDelete(message)

	if !isCultivationConfigAdmin(message.From.ID) {
		sendPlainText(bot, message.Chat.ID, "你没有权限查看修仙配置。")
		return true
	}

	var reply string
	switch text {
	case "查看修仙配置":
		reply = FormatCultivationConfigSummary()
	case "查看突破配置":
		reply = FormatBreakthroughConfigs()
	case "查看境界配置":
		reply = FormatCultivationRealmConfigs()
	default:
		return false
	}

	sendLongPlainText(bot, message.Chat.ID, reply)
	return true
}

func isCultivationConfigAdmin(userID int64) bool {
	return isSuperAdmin(userID)
}

func sendLongPlainText(bot *tgbotapi.BotAPI, chatID int64, text string) {
	const telegramMessageLimit = 3800

	runes := []rune(text)
	if len(runes) <= telegramMessageLimit {
		msg := tgbotapi.NewMessage(chatID, text)
		if _, err := sendAutoDelete(bot, msg); err != nil {
			log.Printf("发送 Telegram 消息失败: err=%s", formatTelegramSendError(err))
		}
		return
	}

	for start := 0; start < len(runes); start += telegramMessageLimit {
		end := start + telegramMessageLimit
		if end > len(runes) {
			end = len(runes)
		}

		msg := tgbotapi.NewMessage(chatID, string(runes[start:end]))
		if _, err := sendAutoDelete(bot, msg); err != nil {
			log.Printf("发送 Telegram 长消息失败: err=%s", formatTelegramSendError(err))
			return
		}
	}
}
