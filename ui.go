package main

import (
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	userMenuGardenText        = "🌱 药园"
	userMenuGardenMiniAppText = "🌱 打开药园"
)

// 用户主菜单
var UserMainMenu = tgbotapi.NewReplyKeyboard(
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("📋 我的档案"),
		tgbotapi.NewKeyboardButton("📆 每日修行"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("🏪 聚宝斋"),
		tgbotapi.NewKeyboardButton(userMenuGardenText),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("📿 修仙"),
		tgbotapi.NewKeyboardButton("🏯 宗门"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("🎮 活动"),
		tgbotapi.NewKeyboardButton("📚 求书中心"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("🧾 资产交易"),
		tgbotapi.NewKeyboardButton("⚙️ 账号服务"),
	),
)

func userMainMenuReplyMarkup() interface{} {
	return UserMainMenu
}

// 安全子菜单
var SafetySubMenu = tgbotapi.NewReplyKeyboard(
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("🔐 修改密码"),
		tgbotapi.NewKeyboardButton("🗑 删号注销"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("🔄 仅解绑不删号"),
		tgbotapi.NewKeyboardButton("🔙 返回主菜单"),
	),
)

// 超级管理员菜单
var SuperAdminMenu = tgbotapi.NewReplyKeyboard(
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("📊 系统总览"),
		tgbotapi.NewKeyboardButton("👤 用户管理"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("💰 资产账务"),
		tgbotapi.NewKeyboardButton("🎫 卡密续期"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("🎲 活动运营"),
		tgbotapi.NewKeyboardButton("📚 求书工单"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("📿 修仙配置"),
		tgbotapi.NewKeyboardButton("🛡 安全运维"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("⚙️ 系统配置"),
		tgbotapi.NewKeyboardButton("🔙 返回用户菜单"),
	),
)

// 普通管理员菜单：只保留只读和低风险能力。
// 高危操作统一收回超级管理员。
var NormalAdminMenu = tgbotapi.NewReplyKeyboard(
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("📊 管理总览"),
		tgbotapi.NewKeyboardButton("📚 求书工单"),
	),
	tgbotapi.NewKeyboardButtonRow(
		tgbotapi.NewKeyboardButton("🧾 流水查询"),
		tgbotapi.NewKeyboardButton("🔙 返回用户菜单"),
	),
)

func sendMenu(bot *tgbotapi.BotAPI, chatID int64, text string, isMenu interface{}) error {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = isMenu

	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("发送 Telegram 菜单失败: chat=%d err=%s", chatID, formatTelegramSendError(err))
		return err
	}
	return nil
}

func sendUserMainMenu(bot *tgbotapi.BotAPI, chatID int64, text string) error {
	return sendMenu(bot, chatID, text, userMainMenuReplyMarkup())
}

func replyText(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"

	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("发送 Telegram 消息失败: chat=%d err=%s", chatID, formatTelegramSendError(err))
	}
}
