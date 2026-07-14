package main

import (
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const adminMenuCallbackPrefix = "admin:"

func handleAdminMenuEntry(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) bool {
	if msg == nil || msg.From == nil || msg.Chat == nil {
		return false
	}

	role := getUserRole(msg.From.ID)
	if role != "super_admin" && role != "admin" {
		return false
	}

	key := ""
	switch text {
	case "📊 系统总览", "📊 管理总览":
		key = "overview"
	case "👤 用户管理":
		key = "users"
	case "💰 资产账务":
		key = "assets"
	case "🎫 卡密续期":
		key = "cards"
	case "🎲 活动运营":
		key = "events"
	case "📚 求书工单":
		key = "books"
	case "📿 修仙配置":
		key = "cultivation"
	case "🛡 安全运维":
		key = "ops"
	case "⚙️ 系统配置":
		key = "config"
	case "🧾 流水查询":
		key = "flow"
	default:
		return false
	}

	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "管理员面板请在私聊中使用。")
		return true
	}

	clearSession(msg.From.ID)
	textOut, markup := renderAdminMenu(key, role)
	sendMenuPanel(bot, msg.Chat.ID, textOut, markup)
	return true
}

func handleAdminMenuCallback(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery) bool {
	if cb == nil || cb.From == nil || !strings.HasPrefix(cb.Data, adminMenuCallbackPrefix) {
		return false
	}
	if cb.Message == nil || cb.Message.Chat == nil {
		answerCallback(bot, cb.ID, "管理员菜单已失效，请重新打开。")
		return true
	}
	if !cb.Message.Chat.IsPrivate() {
		answerCallback(bot, cb.ID, "请私聊使用管理员菜单。")
		return true
	}

	role := getUserRole(cb.From.ID)
	if role != "super_admin" && role != "admin" {
		answerCallback(bot, cb.ID, "权限不足")
		return true
	}

	parts := strings.Split(cb.Data, ":")
	if len(parts) < 2 {
		answerCallback(bot, cb.ID, "未知管理员菜单。")
		return true
	}

	switch parts[1] {
	case "cmd":
		if len(parts) < 3 {
			answerCallback(bot, cb.ID, "未知管理指令。")
			return true
		}
		command, superOnly, ok := adminMenuCommandText(parts[2])
		if !ok {
			answerCallback(bot, cb.ID, "未知管理指令。")
			return true
		}
		if superOnly && role != "super_admin" {
			answerCallback(bot, cb.ID, "该操作仅限超级管理员")
			return true
		}
		answerCallback(bot, cb.ID, "已执行")
		dispatchAdminMenuCommand(bot, cb, command)
	default:
		textOut, markup := renderAdminMenu(parts[1], role)
		answerCallback(bot, cb.ID, "已切换")
		editMenuPanel(bot, cb.Message.Chat.ID, cb.Message.MessageID, textOut, markup)
	}

	return true
}

func renderAdminMenu(key string, role string) (string, tgbotapi.InlineKeyboardMarkup) {
	if role == "admin" {
		return renderNormalAdminMenu(key)
	}
	return renderSuperAdminMenu(key)
}

func renderNormalAdminMenu(key string) (string, tgbotapi.InlineKeyboardMarkup) {
	switch key {
	case "overview":
		return "📊 **【管理总览】**", menuMarkup(
			adminCmdRow("🖥 系统监控", "monitor"),
			adminCmdRow("🔎 查询用户", "query_user"),
			adminBackRow(),
		)
	case "books":
		return "📚 **【求书工单】**", menuMarkup(
			adminCmdRow("📚 待处理求书", "book_pending"),
			adminCmdRow("📚 我的处理工单", "book_handling"),
			adminBackRow(),
		)
	case "flow":
		return "🧾 **【流水查询】**", menuMarkup(
			adminCmdRow("查流水", "flow"),
			adminCmdRow("交易查单帮助", "market_help"),
			adminCmdRow("查看每日净修为", "daily_stat_query"),
			adminBackRow(),
		)
	default:
		return "请选择管理员功能。", menuMarkup(adminBackRow())
	}
}

func renderSuperAdminMenu(key string) (string, tgbotapi.InlineKeyboardMarkup) {
	switch key {
	case "overview":
		return "📊 **【系统总览】**", menuMarkup(
			adminCmdRow("🖥 系统监控", "monitor"),
			adminCmdRow("🛠 后台状态", "background_status"),
			adminCmdRow("📊 审计概览", "audit_summary"),
			adminCmdRow("📋 审计日志", "audit_logs"),
			adminCmdRow("🔎 查询用户", "query_user"),
			adminCmdRow("查流水", "flow"),
			adminCmdRow("查看每日净修为", "daily_stat_query"),
			adminCmdRow("刷新全服今日净修为", "daily_stat_refresh_all"),
			adminCmdRow("🔎 卡密查码", "query_code"),
			adminBackRow(),
		)
	case "users":
		return "👤 **【用户管理】**\n\n⚠️ 暂停、删除、清理遗孀均为高危操作。", menuMarkup(
			adminCmdRow("🔎 查询用户", "query_user"),
			adminCmdRow("🛑 暂停/恢复", "suspend"),
			adminCmdRow("🗑️ 删除用户", "delete_user"),
			adminCmdRow("👑 授权管理员", "promote_admin"),
			adminCmdRow("🏳️ 设置白名单", "whitelist"),
			adminCmdRow("🧹 清理遗孀", "clean_widows"),
			adminBackRow(),
		)
	case "assets":
		return "💰 **【资产账务】**\n\n⚠️ 调账会改变用户积分。", menuMarkup(
			adminCmdRow("🛠️ 操控用户积分", "adjust_points"),
			adminCmdRow("查流水", "flow"),
			adminCmdRow("交易查单帮助", "market_help"),
			adminCmdRow("邀请码价格", "invite_price"),
			adminCmdRow("续期卡价格", "renew_price"),
			adminBackRow(),
		)
	case "cards":
		return "🎫 **【卡密续期】**", menuMarkup(
			adminCmdRow("🔑 生成邀请码", "gen_invite"),
			adminCmdRow("💳 生成续期卡", "gen_renew"),
			adminCmdRow("🔎 卡密查码", "query_code"),
			adminCmdRow("邀请码价格", "invite_price"),
			adminCmdRow("续期卡价格", "renew_price"),
			adminBackRow(),
		)
	case "events":
		return "🎲 **【活动运营】**", menuMarkup(
			adminCmdRow("🎲 积分抽奖", "lottery"),
			adminCmdRow("创建积分抽奖", "lottery_create"),
			adminCmdRow("抽奖详情", "lottery_detail"),
			adminCmdRow("🎁 查看GitHub福利", "github_benefit_view"),
			adminCmdRow("设置GitHub福利名额", "github_benefit_quota"),
			adminCmdRow("开启GitHub福利", "github_benefit_enable"),
			adminCmdRow("关闭GitHub福利", "github_benefit_disable"),
			adminBackRow(),
		)
	case "books":
		return "📚 **【求书工单】**", menuMarkup(
			adminCmdRow("📚 待处理求书", "book_pending"),
			adminCmdRow("📚 我的处理工单", "book_handling"),
			adminBackRow(),
		)
	case "cultivation":
		return "📿 **【修仙配置】**", menuMarkup(
			adminCmdRow("查看修仙配置", "cult_view"),
			adminCmdRow("查看境界配置", "realm_view"),
			adminCmdRow("查看突破配置", "break_view"),
			adminCmdRow("重载修仙配置", "cult_reload"),
			adminCmdRow("设置突破成功率", "set_break_rate"),
			adminCmdRow("设置突破消耗", "set_break_cost"),
			adminCmdRow("设置突破冷却", "set_break_cooldown"),
			adminCmdRow("设置突破最低修为", "set_break_min"),
			adminCmdRow("设置境界门槛", "set_realm_hours"),
			adminCmdRow("设置小境界门槛", "set_minor_hours"),
			adminBackRow(),
		)
	case "ops":
		return "🛡 **【安全运维】**\n\n⚠️ 备份、清理遗孀属于高风险运维动作。", menuMarkup(
			adminCmdRow("📦 备份数据", "backup"),
			adminCmdRow("📦 备份状态", "backup_status"),
			adminCmdRow("🛠 后台状态", "background_status"),
			adminCmdRow("📊 审计概览", "audit_summary"),
			adminCmdRow("📋 审计日志", "audit_logs"),
			adminCmdRow("🧹 清理遗孀", "clean_widows"),
			adminCmdRow("🔎 卡密查码", "query_code"),
			adminBackRow(),
		)
	case "config":
		return "⚙️ **【系统配置】**\n\n⚠️ 开放注册会改变开户注册边界，用户补偿会变更全体用户资产。", menuMarkup(
			adminCmdRow("🚪 开放注册", "open_registration"),
			adminCmdRow("🛡 用户补偿", "user_compensation"),
			adminCmdRow("🌐 设置线路", "set_lines"),
			adminCmdRow("邀请码价格", "invite_price"),
			adminCmdRow("续期卡价格", "renew_price"),
			adminBackRow(),
		)
	default:
		return "请选择管理员功能。", menuMarkup(adminBackRow())
	}
}

func adminMenuCommandText(key string) (command string, superOnly bool, ok bool) {
	commands := map[string]struct {
		Text      string
		SuperOnly bool
	}{
		"monitor":                {"系统监控", false},
		"background_status":      {"后台状态", true},
		"audit_summary":          {"审计概览", true},
		"audit_logs":             {"审计日志", true},
		"query_user":             {"查询用户", false},
		"flow":                   {"查流水", false},
		"market_help":            {"交易行帮助", false},
		"daily_stat_query":       {"查看每日净修为", false},
		"daily_stat_refresh_all": {"刷新全服今日净修为", true},
		"book_pending":           {"📚 待处理求书", false},
		"book_handling":          {"📚 我的处理工单", false},
		"query_code":             {"查码", true},
		"suspend":                {"暂停", true},
		"delete_user":            {"删除用户", true},
		"promote_admin":          {"授权", true},
		"whitelist":              {"白名单", true},
		"clean_widows":           {"清理遗孀", true},
		"adjust_points":          {"操控", true},
		"open_registration":      {"开放注册", true},
		"user_compensation":      {"用户补偿", true},
		"invite_price":           {"邀请码价格", true},
		"renew_price":            {"续期卡价格", true},
		"gen_invite":             {"生成邀请码", true},
		"gen_renew":              {"生成续期卡", true},
		"lottery":                {"积分抽奖", true},
		"lottery_create":         {"创建积分抽奖", true},
		"lottery_detail":         {"抽奖详情", true},
		"github_benefit_view":    {"查看github福利", true},
		"github_benefit_quota":   {"设置github福利名额", true},
		"github_benefit_enable":  {"开启github福利", true},
		"github_benefit_disable": {"关闭github福利", true},
		"cult_view":              {"查看修仙配置", true},
		"realm_view":             {"查看境界配置", true},
		"break_view":             {"查看突破配置", true},
		"cult_reload":            {"重载修仙配置", true},
		"set_break_rate":         {"设置突破成功率", true},
		"set_break_cost":         {"设置突破消耗", true},
		"set_break_cooldown":     {"设置突破冷却", true},
		"set_break_min":          {"设置突破最低修为", true},
		"set_realm_hours":        {"设置境界门槛", true},
		"set_minor_hours":        {"设置小境界门槛", true},
		"backup":                 {"备份", true},
		"backup_status":          {"备份状态", true},
		"set_lines":              {"设置线路", true},
	}
	item, exists := commands[key]
	if !exists {
		return "", false, false
	}
	return item.Text, item.SuperOnly, true
}

func dispatchAdminMenuCommand(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery, command string) {
	if cb == nil || cb.Message == nil || cb.Message.Chat == nil || cb.From == nil {
		return
	}
	clearSession(cb.From.ID)
	msg := &tgbotapi.Message{
		MessageID: cb.Message.MessageID,
		From:      cb.From,
		Chat:      cb.Message.Chat,
		Text:      command,
	}
	handleInteractiveMessage(bot, msg)
}

func adminCmdRow(label string, key string) []tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(label, "admin:cmd:"+key))
}

func adminBackRow() []tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("返回管理员面板", "admin:overview"))
}
