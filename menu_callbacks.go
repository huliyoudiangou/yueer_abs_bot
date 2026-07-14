package main

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const (
	menuCallbackPrefix = "menu:"
	shopCallbackPrefix = "shop:"
)

type treasureShopItem struct {
	ID    string
	Name  string
	Price int
}

var treasureShopItems = []treasureShopItem{
	{ID: "1", Name: "聚灵丹", Price: 120},
	{ID: "2", Name: "九转造化丹", Price: 350},
	{ID: "3", Name: "万年仙玉髓", Price: 1000},
	{ID: "4", Name: "筑基丹", Price: 100},
	{ID: "5", Name: "降尘丹", Price: 200},
	{ID: "6", Name: "九曲灵参丹", Price: 500},
	{ID: "7", Name: "补天丹", Price: 1000},
}

func treasureShopPointDescriptionName(name string) string {
	return lotteryDisplayText(name, 80, "-")
}

func handleMenuEntry(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) bool {
	if msg == nil || msg.From == nil || msg.Chat == nil {
		return false
	}

	key := ""
	switch text {
	case "📋 我的档案":
		key = "profile"
	case "📆 每日修行":
		key = "daily"
	case "🏪 聚宝斋", "聚宝斋":
		if !msg.Chat.IsPrivate() {
			sendPlainText(bot, msg.Chat.ID, "聚宝斋需私聊交易，请私聊我进入。")
			return true
		}
		clearSession(msg.From.ID)
		showTreasureShopHome(bot, msg.From, msg.Chat.ID)
		return true
	case "📿 修仙":
		key = "cultivation"
	case "🏯 宗门":
		key = "sect"
	case "🎮 活动":
		key = "activity"
	case "📚 求书中心":
		key = "book"
	case "🧾 资产流水", "🧾 资产交易":
		key = "assets"
	case "⚙️ 账号服务":
		key = "account"
	default:
		return false
	}

	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "请私聊我打开功能菜单。")
		return true
	}

	clearSession(msg.From.ID)
	textOut, markup := renderFeatureMenu(key, msg.From.ID)
	sendMenuPanel(bot, msg.Chat.ID, textOut, markup)
	return true
}

func handleMenuCallback(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery) bool {
	if cb == nil || cb.From == nil {
		return false
	}
	if strings.HasPrefix(cb.Data, shopCallbackPrefix) {
		handleShopCallback(bot, cb)
		return true
	}
	if !strings.HasPrefix(cb.Data, menuCallbackPrefix) {
		return false
	}
	if cb.Message == nil || cb.Message.Chat == nil {
		answerCallback(bot, cb.ID, "菜单已失效，请重新打开。")
		return true
	}
	if !cb.Message.Chat.IsPrivate() {
		answerCallback(bot, cb.ID, "请私聊我使用菜单。")
		return true
	}

	parts := strings.Split(cb.Data, ":")
	if len(parts) < 2 {
		answerCallback(bot, cb.ID, "未知菜单。")
		return true
	}

	switch parts[1] {
	case "home":
		answerCallback(bot, cb.ID, "已返回主菜单")
		sendUserMainMenu(bot, cb.Message.Chat.ID, "✅ 已为您切换至主菜单：")
	case "cmd":
		if len(parts) < 3 {
			answerCallback(bot, cb.ID, "未知指令。")
			return true
		}
		command, ok := menuCommandText(parts[2])
		if !ok {
			answerCallback(bot, cb.ID, "未知指令。")
			return true
		}
		answerCallback(bot, cb.ID, "已执行")
		dispatchMenuCommand(bot, cb, command)
	case "tip":
		if len(parts) < 3 {
			answerCallback(bot, cb.ID, "请前往群内操作。")
			return true
		}
		answerCallback(bot, cb.ID, "请前往群内操作")
		tip := "请前往官方群内发送对应指令。"
		if parts[2] == "race" {
			tip = "🏇 赛马需在群内进行，请在开放时间发送 `发起赛马`。"
		} else if parts[2] == "dice" {
			tip = "🎲 骰子需在群内进行，请在群内发送 `发起骰子`。"
		}
		sendPlainText(bot, cb.Message.Chat.ID, tip)
	default:
		textOut, markup := renderFeatureMenu(parts[1], cb.From.ID)
		answerCallback(bot, cb.ID, "已切换")
		editMenuPanel(bot, cb.Message.Chat.ID, cb.Message.MessageID, textOut, markup)
	}

	return true
}

func handleShopCallback(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery) {
	if cb.Message == nil || cb.Message.Chat == nil {
		answerCallback(bot, cb.ID, "聚宝斋入口已失效，请重新打开。")
		return
	}
	if !cb.Message.Chat.IsPrivate() {
		answerCallback(bot, cb.ID, "聚宝斋需私聊交易。")
		return
	}

	parts := strings.Split(cb.Data, ":")
	if len(parts) < 2 {
		answerCallback(bot, cb.ID, "未知聚宝斋操作。")
		return
	}

	switch parts[1] {
	case "home":
		answerCallback(bot, cb.ID, "已返回聚宝斋")
		textOut, markup := renderTreasureShopHome(cb.From.ID)
		editMenuPanel(bot, cb.Message.Chat.ID, cb.Message.MessageID, textOut, markup)
	case "items":
		answerCallback(bot, cb.ID, "丹药奇珍已刷新")
		textOut, markup := renderTreasureShopItems(cb.From.ID)
		editMenuPanel(bot, cb.Message.Chat.ID, cb.Message.MessageID, textOut, markup)
	case "bag":
		answerCallback(bot, cb.ID, "乾坤袋已刷新")
		textOut, markup := renderTreasureBag(cb.From.ID)
		editMenuPanel(bot, cb.Message.Chat.ID, cb.Message.MessageID, textOut, markup)
	case "use":
		answerCallback(bot, cb.ID, "已打开乾坤袋")
		dispatchMenuCommand(bot, cb, "乾坤袋")
	case "item":
		if len(parts) < 3 {
			answerCallback(bot, cb.ID, "未知商品。")
			return
		}
		item, ok := treasureShopItemByID(parts[2])
		if !ok {
			answerCallback(bot, cb.ID, "未知商品。")
			return
		}
		answerCallback(bot, cb.ID, "请确认购买")
		textOut := treasureShopBuyConfirmMarkdownText(item)
		markup := tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("确认购买", "shop:confirm:"+item.ID),
				tgbotapi.NewInlineKeyboardButtonData("返回货架", "shop:items"),
			),
		)
		editMenuPanel(bot, cb.Message.Chat.ID, cb.Message.MessageID, textOut, markup)
	case "confirm":
		if len(parts) < 3 {
			answerCallback(bot, cb.ID, "未知商品。")
			return
		}
		item, ok := treasureShopItemByID(parts[2])
		if !ok {
			answerCallback(bot, cb.ID, "未知商品。")
			return
		}
		if err := purchaseTreasureShopItem(cb.From.ID, item.Name, item.Price); err != nil {
			if errors.Is(err, errInsufficientPoints) {
				answerCallback(bot, cb.ID, "积分不足")
				editMenuPanel(bot, cb.Message.Chat.ID, cb.Message.MessageID, "❌ 您的积分不足，购买失败。", treasureShopBackMarkup())
			} else {
				log.Printf("⚠️ 聚宝斋按钮购买失败: user=%d item=%s price=%d err=%s", cb.From.ID, formatPlainValue(item.Name), item.Price, formatPlainError(err))
				answerCallback(bot, cb.ID, "交易失败")
				editMenuPanel(bot, cb.Message.Chat.ID, cb.Message.MessageID, "❌ 交易异常，购买失败，请稍后再试。", treasureShopBackMarkup())
			}
			return
		}
		clearSession(cb.From.ID)
		answerCallback(bot, cb.ID, "购买成功")
		textOut := treasureShopBuySuccessMarkdownText(item.Name)
		editMenuPanel(bot, cb.Message.Chat.ID, cb.Message.MessageID, textOut, treasureShopBackMarkup())
	default:
		answerCallback(bot, cb.ID, "未知聚宝斋操作。")
	}
}

func renderFeatureMenu(key string, userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	switch key {
	case "profile":
		return "📋 **【我的档案】**", menuMarkup(
			menuCmdRow("👤 我的信息", "profile"),
			menuCmdRow("📊 听书报告", "report"),
			menuCmdRow("📒 我的流水", "flow"),
			menuBackRow(),
		)
	case "daily":
		return "📆 **【每日修行】**", menuMarkup(
			menuCmdRow("📆 每日签到", "signin"),
			menuCmdRow("🌅 刷新今日净修为", "refresh_my_daily_stat"),
			menuCmdRow("📊 听书报告", "report"),
			menuCmdRow("🌊 天道奖池", "fusion"),
			menuCmdRow("🏆 财富榜", "wealth"),
			menuBackRow(),
		)
	case "cultivation":
		return "📿 **【修仙】**", menuMarkup(
			menuCmdRow("👤 修仙档案", "profile"),
			menuCmdRow("⚡ 突破", "breakthrough"),
			menuCmdRow("📈 修仙榜", "cultivation_rank"),
			menuBackRow(),
		)
	case "sect":
		rows := [][]tgbotapi.InlineKeyboardButton{
			menuCmdRow("🏯 我的宗门", "my_sect"),
			menuCmdRow("🏆 宗门排行", "sect_rank"),
			menuCmdRow("👥 宗门成员", "sect_members"),
			menuCmdRow("📜 宗门任务", "sect_tasks"),
			menuCmdRow("🎁 领取任务奖励", "sect_claim"),
			menuCmdRow("📊 宗门贡献榜", "sect_contrib"),
			menuCmdRow("🏅 宗门周榜", "sect_weekly"),
			menuCmdRow("🛒 宗门商店", "sect_shop"),
			menuCmdRow("🛠 宗门科技", "sect_tech"),
			menuCmdRow("🎁 宗门抽奖", "sect_lottery"),
			menuCmdRow("⛰ 宗门秘境", "sect_realm"),
			menuCmdRow("📣 宗门喇叭", "sect_horn"),
			menuCmdRow("🌍 世界喇叭", "world_horn"),
			menuBackRow(),
		}
		return "🏯 **【宗门】**", menuMarkup(rows...)
	case "activity":
		return "🎮 **【活动】**", menuMarkup(
			menuCmdRow("🐉 世界Boss", "world_boss"),
			menuCmdRow("📍 Boss状态", "boss_status"),
			menuCmdRow("⚔️ 参加Boss", "join_boss"),
			menuCmdRow("🏆 Boss排行", "boss_rank"),
			menuCmdRow("🎲 积分抽奖", "lottery"),
			menuCmdRow("🎁 GitHub福利", "github_benefit"),
			menuTipRow("🏇 发起赛马", "race"),
			menuTipRow("🎲 发起骰子", "dice"),
			menuBackRow(),
		)
	case "book":
		rows := [][]tgbotapi.InlineKeyboardButton{
			menuCmdRow("📚 提交求书", "book_new"),
			menuCmdRow("📋 我的求书", "book_mine"),
		}
		if isBookRequestAdmin(userID) {
			rows = append(rows,
				menuCmdRow("📚 待处理求书", "book_pending"),
				menuCmdRow("📎 我的处理工单", "book_handling"),
			)
		}
		rows = append(rows, menuBackRow())
		return "📚 **【求书中心】**", menuMarkup(rows...)
	case "assets":
		return "🧾 **【资产交易】**", menuMarkup(
			menuCmdRow("📒 我的流水", "flow"),
			menuCmdRow("🛒 交易行", "marketplace"),
			menuCmdRow("📦 我的购买", "market_orders"),
			menuCmdRow("🧾 订单/举报帮助", "market_help"),
			menuCmdRow("🏷 上架商品", "market_sell"),
			menuCmdRow("🪙 积分兑换", "exchange"),
			menuCmdRow("🎁 积分盲盒", "blind_box"),
			menuCmdRow("🧧 制作积分红包", "red_packet"),
			menuBackRow(),
		)
	case "account":
		return "⚙️ **【账号服务】**", menuMarkup(
			menuCmdRow("📝 注册账户", "register"),
			menuCmdRow("🔗 绑定账号", "bind"),
			menuCmdRow("🎫 使用邀请码", "invite"),
			menuCmdRow("💳 使用续期卡", "renew"),
			menuCmdRow("🌐 获取线路", "lines"),
			menuNavRow("🛡 账户安全", "security"),
			menuBackRow(),
		)
	case "security":
		var userCount int64
		if err := DB.Model(&User{}).Where("telegram_id = ?", userID).Count(&userCount).Error; err != nil {
			log.Printf("⚠️ 查询账户安全本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			return "❌ 账户安全状态暂时无法读取，请稍后再试。", menuMarkup(
				menuNavRow("返回账号服务", "account"),
				menuBackRow(),
			)
		}
		if userCount == 0 {
			return "⚠️ 您还未绑定账户。", menuMarkup(
				menuNavRow("返回账号服务", "account"),
				menuBackRow(),
			)
		}

		return "🛡️ **【账户安全】**", menuMarkup(
			menuCmdRow("🆔 修改用户名", "change_username"),
			menuCmdRow("🔐 修改密码", "password"),
			menuCmdRow("🔄 仅解绑不删号", "unbind"),
			menuCmdRow("🗑 删号注销", "delete_account"),
			menuNavRow("返回账号服务", "account"),
			menuBackRow(),
		)
	default:
		return "请选择功能。", menuMarkup(menuBackRow())
	}
}

func renderTreasureShopHome(userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	points := 0
	pointsAvailable := true
	var u User
	if err := DB.Select("points").Where("telegram_id = ?", userID).First(&u).Error; err == nil {
		points = u.Points
	} else {
		pointsAvailable = false
		log.Printf("⚠️ 聚宝斋钱包读取失败: user=%d err=%s", userID, formatPlainError(err))
	}

	var itemCount int64
	itemCountAvailable := true
	if err := DB.Model(&Inventory{}).Where("user_id = ? AND quantity > 0", userID).Count(&itemCount).Error; err != nil {
		itemCountAvailable = false
		log.Printf("⚠️ 查询聚宝斋乾坤袋数量失败: user=%d err=%s", userID, formatPlainError(err))
	}

	text := treasureShopHomeMarkdownText(points, pointsAvailable, itemCount, itemCountAvailable)
	return text, tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("丹药奇珍", "shop:items"),
			tgbotapi.NewInlineKeyboardButtonData("我的乾坤袋", "shop:bag"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("吞服丹药", "shop:use"),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("返回主菜单", "menu:home"),
		),
	)
}

func treasureShopHomeMarkdownText(points int, pointsAvailable bool, itemCount int64, itemCountAvailable bool) string {
	pointsText := gardenCountText(int64(points), pointsAvailable)
	itemCountText := gardenCountText(itemCount, itemCountAvailable)
	if overview := treasureShopEffectOverviewMarkdownText(); overview != "" {
		return fmt.Sprintf("🏪 **【聚宝斋】**\n\n可用积分：`%s`\n乾坤袋物品：`%s` 种\n\n📌 **丹药功效速览**\n%s\n\n点击【丹药奇珍】可查看价格并购买。", pointsText, itemCountText, overview)
	}
	return fmt.Sprintf("🏪 **【聚宝斋】**\n\n可用积分：`%s`\n乾坤袋物品：`%s` 种\n\n丹药奇珍会标注简要功效，购买前可先确认用途。", pointsText, itemCountText)
}

func treasureShopEffectOverviewMarkdownText() string {
	var b strings.Builder
	for _, item := range treasureShopItems {
		if summary := pillEffectSummary(item.Name); summary != "" {
			b.WriteString(fmt.Sprintf("- **%s**：%s\n", inventoryItemMarkdownName(item.Name), escapeMarkdown(summary)))
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func renderTreasureShopItems(userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	points := 0
	pointsAvailable := true
	var u User
	if err := DB.Select("points").Where("telegram_id = ?", userID).First(&u).Error; err == nil {
		points = u.Points
	} else {
		pointsAvailable = false
		log.Printf("⚠️ 聚宝斋货架钱包读取失败: user=%d err=%s", userID, formatPlainError(err))
	}
	pointsText := gardenCountText(int64(points), pointsAvailable)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("📜 **【聚宝斋·天地奇珍】**\n\n当前积分：`%s`\n\n", pointsText))
	rows := make([][]tgbotapi.InlineKeyboardButton, 0, len(treasureShopItems)+1)
	for _, item := range treasureShopItems {
		b.WriteString(treasureShopItemMarkdownText(item))
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(treasureShopItemButtonLabel(item), "shop:item:"+item.ID),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("返回聚宝斋", "shop:home"),
	))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func treasureShopItemMarkdownText(item treasureShopItem) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[%s] %s：`%d` 积分\n", item.ID, inventoryItemMarkdownName(item.Name), item.Price))
	if effectLine := pillEffectMarkdownLine(item.Name); effectLine != "" {
		b.WriteString(effectLine)
		b.WriteString("\n")
	}
	return b.String()
}

func treasureShopItemButtonLabel(item treasureShopItem) string {
	if badge := treasureShopItemEffectBadge(item.Name); badge != "" {
		return fmt.Sprintf("%s（%d｜%s）", item.Name, item.Price, badge)
	}
	return fmt.Sprintf("%s（%d）", item.Name, item.Price)
}

func treasureShopItemEffectBadge(itemName string) string {
	switch strings.TrimSpace(itemName) {
	case "聚灵丹":
		return "修为+1.0h"
	case "九转造化丹":
		return "修为+3.0h"
	case "万年仙玉髓":
		return "修为+10.0h"
	case "筑基丹", "降尘丹", "九曲灵参丹", "补天丹":
		return "自动破境"
	default:
		return ""
	}
}

func treasureShopBuyConfirmMarkdownText(item treasureShopItem) string {
	effectLine := pillEffectMarkdownLine(item.Name)
	if effectLine != "" {
		effectLine += "\n"
	}
	return fmt.Sprintf("🏪 **【聚宝斋】**\n\n道友将购入：**【%s】**\n%s消耗积分：`%d`\n\n请确认是否购买。", inventoryItemMarkdownName(item.Name), effectLine, item.Price)
}

func treasureShopBuySuccessMarkdownText(itemName string) string {
	effectLine := pillEffectMarkdownLine(itemName)
	if effectLine != "" {
		effectLine = "\n" + effectLine
	}
	return fmt.Sprintf("🎉 **购买成功！**\n\n您已获得 **【%s】**，已放入【🎒 我的乾坤袋】中。%s", inventoryItemMarkdownName(itemName), effectLine)
}

func renderTreasureBag(userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	var inv []Inventory
	if err := DB.Where("user_id = ? AND quantity > 0", userID).Order("item_name asc").Find(&inv).Error; err != nil {
		log.Printf("⚠️ 读取聚宝斋乾坤袋失败: user=%d err=%s", userID, formatPlainError(err))
		return "❌ 乾坤袋读取失败，请稍后再试。", treasureShopBackMarkup()
	}
	if len(inv) == 0 {
		return "🎒 **【我的乾坤袋】**\n\n里面空空如也。", treasureShopBackMarkup()
	}

	var b strings.Builder
	b.WriteString("🎒 **【我的乾坤袋】**\n\n")
	for _, item := range inv {
		b.WriteString(fmt.Sprintf("- **%s** x `%d`\n", inventoryItemMarkdownName(item.ItemName), item.Quantity))
		if effectLine := pillEffectMarkdownLine(item.ItemName); effectLine != "" {
			b.WriteString("  " + effectLine + "\n")
		}
	}
	return b.String(), treasureShopBackMarkup()
}

func showTreasureShopHome(bot *tgbotapi.BotAPI, tgUser *tgbotapi.User, chatID int64) {
	if tgUser == nil {
		return
	}
	if _, _, err := ensureUserWallet(tgUser); err != nil {
		log.Printf("⚠️ 聚宝斋钱包初始化失败: user=%d err=%s", tgUser.ID, formatPlainError(err))
		replyText(bot, chatID, "❌ 钱包初始化失败，请稍后再试。")
		return
	}
	text, markup := renderTreasureShopHome(tgUser.ID)
	sendMenuPanel(bot, chatID, text, markup)
}

func treasureShopItemByID(id string) (treasureShopItem, bool) {
	for _, item := range treasureShopItems {
		if item.ID == id {
			return item, true
		}
	}
	return treasureShopItem{}, false
}

func purchaseTreasureShopItem(userID int64, itemName string, price int) error {
	itemName = strings.TrimSpace(itemName)
	if userID == 0 || itemName == "" || price <= 0 {
		return fmt.Errorf("INVALID_SHOP_PURCHASE")
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		if err := applyPointDeltaInTx(
			tx,
			userID,
			-price,
			"shop_buy_item",
			fmt.Sprintf("购买【%s】，消耗 %d 积分", treasureShopPointDescriptionName(itemName), price),
			"shop",
			itemName,
		); err != nil {
			if errors.Is(err, errPointsNotEnough) {
				return errInsufficientPoints
			}
			return err
		}

		res := tx.Clauses(inventoryQuantityUpsertClause(1)).Create(&Inventory{
			UserID:   userID,
			ItemName: itemName,
			Quantity: 1,
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("SHOP_INVENTORY_GRANT_MISSED")
		}

		return nil
	})
}

func menuCommandText(key string) (string, bool) {
	commands := map[string]string{
		"profile":               "我的信息",
		"report":                "听书报告",
		"flow":                  "我的流水",
		"signin":                "每日签到",
		"refresh_my_daily_stat": "刷新我的今日净修为",
		"fusion":                "天道奖池",
		"wealth":                "财富榜",
		"breakthrough":          "突破",
		"cultivation_rank":      "修仙榜",
		"my_sect":               "我的宗门",
		"sect_rank":             "宗门排行",
		"sect_members":          "宗门成员",
		"sect_tasks":            "宗门任务",
		"sect_claim":            "领取宗门任务奖励",
		"sect_contrib":          "宗门贡献榜",
		"sect_weekly":           "宗门周榜",
		"sect_shop":             "宗门商店",
		"sect_tech":             "宗门科技",
		"sect_lottery":          "宗门抽奖",
		"sect_realm":            "宗门秘境",
		"sect_horn":             "宗门喇叭",
		"world_horn":            "世界喇叭",
		"world_boss":            "世界Boss",
		"boss_status":           "Boss状态",
		"join_boss":             "参加Boss",
		"boss_rank":             "Boss排行",
		"lottery":               "积分抽奖",
		"github_benefit":        "github福利",
		"book_new":              "📚 求书",
		"book_mine":             "📋 我的求书",
		"book_pending":          "📚 待处理求书",
		"book_handling":         "我的处理工单",
		"exchange":              "积分兑换",
		"marketplace":           "交易行",
		"market_orders":         "我的购买",
		"market_help":           "交易行帮助",
		"market_sell":           "上架商品",
		"blind_box":             "积分盲盒",
		"red_packet":            "制作积分红包",
		"register":              "注册",
		"bind":                  "绑定",
		"invite":                "邀请码",
		"renew":                 "续期卡",
		"lines":                 "获取线路",
		"security":              "安全",
		"change_username":       "修改用户名",
		"password":              "修改密码",
		"unbind":                "仅解绑不删号",
		"delete_account":        "删号注销",
	}
	command, ok := commands[key]
	return command, ok
}

func dispatchMenuCommand(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery, command string) {
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

func menuMarkup(rows ...[]tgbotapi.InlineKeyboardButton) tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

func menuCmdRow(label string, key string) []tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(label, "menu:cmd:"+key))
}

func menuNavRow(label string, key string) []tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(label, "menu:"+key))
}

func menuTipRow(label string, key string) []tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData(label, "menu:tip:"+key))
}

func menuBackRow() []tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("返回主菜单", "menu:home"))
}

func treasureShopBackMarkup() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("返回聚宝斋", "shop:home"),
			tgbotapi.NewInlineKeyboardButtonData("返回主菜单", "menu:home"),
		),
	)
}

func sendMenuPanel(bot *tgbotapi.BotAPI, chatID int64, text string, markup tgbotapi.InlineKeyboardMarkup) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	msg.ReplyMarkup = markup
	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("发送二级菜单失败: chat=%d err=%s", chatID, formatTelegramSendError(err))
	}
}

func editMenuPanel(bot *tgbotapi.BotAPI, chatID int64, messageID int, text string, markup tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageText(chatID, messageID, text)
	edit.ParseMode = "Markdown"
	edit.ReplyMarkup = &markup
	if _, err := bot.Send(edit); err != nil {
		log.Printf("编辑二级菜单失败: chat=%d message=%d err=%s", chatID, messageID, formatTelegramSendError(err))
	}
}

func treasureShopItemFromText(text string) (treasureShopItem, bool) {
	id := strings.TrimSpace(text)
	if _, err := strconv.Atoi(id); err != nil {
		return treasureShopItem{}, false
	}
	return treasureShopItemByID(id)
}
