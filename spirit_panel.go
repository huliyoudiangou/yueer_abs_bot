package main

// ==========================================
// 万灵阁 · 灵侍系统面板（Phase 1）
//
// 本文件只包含 UI 面板与 sp: 回调路由。
// 玩法逻辑在 spirit_servant.go / lingjing.go / spirit_config.go。
//
// 约定（设计锁定）：
// - 所有灵侍操作仅限私聊（群聊提示转私聊）。
// - 按钮驱动 inline keyboard，EditMessageText 原地刷新。
// - callback 前缀统一 "sp:"。
// - 兑换数量走档位按钮（100/300/500/1000 积分），不开放自由输入。
// - 面板不持有的资产生成任何副作用前必须走事务函数。
// ==========================================

import (
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

// sp: 回调常量
const (
	spCbHome      = "sp:home"
	spCbList      = "sp:list"
	spCbBag       = "sp:bag"
	spCbCatch     = "sp:catch"
	spCbTeam      = "sp:team"
	spCbPush      = "sp:push"
	spCbBeast     = "sp:beast"
	spCbHelp      = "sp:help"
	spCbExPrefix  = "sp:ex:" // sp:ex:100 / sp:ex:300 / sp:ex:500 / sp:ex:1000
	spCbZonePreix = "sp:zone:"
)

// 灵晶斋兑换档位（积分）
var spiritExchangeTiers = []int{100, 300, 500, 1000}

// ------------------------------------------
// 面板渲染（返回 text + keyboard 供新建或编辑复用）
// ------------------------------------------

func spiritPanelHome(db *gorm.DB, userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	lingjing, err := GetUserWalletBalance(db, userID)
	if err != nil {
		log.Printf("[灵侍] 查询钱包失败 user=%d err=%v", userID, err)
		lingjing = 0
	}
	var total int64
	db.Model(&UserSpiritServant{}).Where("user_id = ?", userID).Count(&total)

	text := fmt.Sprintf(
		"🐉 万灵阁\n"+
			"━━━━━━━━━━━━━━\n"+
			"💎 灵晶：%d  (✨ 灵尘 %d)\n"+
			"🐾 灵侍：%d 只\n"+
			"━━━━━━━━━━━━━━\n"+
			"灵晶由积分单向兑换（1 积分 = %d 灵晶），\n"+
			"每日上限 %d 积分（= %d 灵晶）。",
		lingjing, LingchenFromLingjing(lingjing), total,
		LingjingExchangeRate(), LingjingDailyCap()/LingjingExchangeRate(), LingjingDailyCap())

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐾 灵侍图鉴", spCbList),
			tgbotapi.NewInlineKeyboardButtonData("🪷 灵晶斋", spCbBag),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏹 灵墟捕捉", spCbCatch),
			tgbotapi.NewInlineKeyboardButtonData("🛡 出战队列", spCbTeam),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🗺 灵墟推图", spCbPush),
			tgbotapi.NewInlineKeyboardButtonData("🔮 护宗神兽", spCbBeast),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("ℹ️ 玩法说明", spCbHelp),
		),
	)
	return text, kb
}

func spiritPanelList(db *gorm.DB, userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	var servants []UserSpiritServant
	if err := db.Where("user_id = ?", userID).Order("quality desc, star desc, level desc").Limit(10).Find(&servants).Error; err != nil {
		log.Printf("[灵侍] 图鉴查询失败 user=%d err=%v", userID, err)
	}
	var b strings.Builder
	b.WriteString("🐾 灵侍图鉴\n━━━━━━━━━━━━━━\n")
	if len(servants) == 0 {
		b.WriteString("你尚未收服任何灵侍。\n前往「灵墟捕捉」遇见你的第一只灵侍吧！")
	} else {
		for i := range servants {
			s := &servants[i]
			deploy := ""
			if s.IsDeployed {
				deploy = "〔出战〕"
			}
			b.WriteString(fmt.Sprintf("· %s%s %s品·%s Lv.%d ⭐%d 战力%d\n",
				s.Name, deploy, s.Quality, s.Attribute, s.Level, s.Star, GetBattlePower(s)))
		}
		if len(servants) == 10 {
			b.WriteString("\n（仅显示前 10 只）")
		}
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)),
	)
	return b.String(), kb
}

func spiritPanelBag(db *gorm.DB, userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	lingjing, err := GetUserWalletBalance(db, userID)
	if err != nil {
		lingjing = 0
	}
	// 今日已兑换
	today := time.Now().Format("20060102")
	var quota DailyLingjingQuota
	spent := 0
	if err := db.Where("user_id = ? AND day_key = ?", userID, today).First(&quota).Error; err == nil {
		spent = quota.Spent
	}
	remain := DailyMaxExchangePoints - spent
	if remain < 0 {
		remain = 0
	}

	text := fmt.Sprintf(
		"🪷 灵晶斋\n"+
			"━━━━━━━━━━━━━━\n"+
			"💎 灵晶：%d（灵尘 %d）\n"+
			"📅 今日已兑换：%d 积分\n"+
			"🧮 今日剩余额度：%d 积分\n"+
			"━━━━━━━━━━━━━━\n"+
			"选择下方档位，将积分单向兑换为灵晶：",
		lingjing, LingchenFromLingjing(lingjing), spent, remain)

	var rows [][]tgbotapi.InlineKeyboardButton
	var tierRow []tgbotapi.InlineKeyboardButton
	for _, tier := range spiritExchangeTiers {
		label := fmt.Sprintf("兑 %d 积分→%d 灵晶", tier, tier*LingjingExchangeRate())
		tierRow = append(tierRow, tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("%s%d", spCbExPrefix, tier)))
		if len(tierRow) == 2 {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(tierRow...))
			tierRow = nil
		}
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)))
	return text, tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// 未开放玩法的占位面板
func spiritPanelComingSoon(title, body string) (string, tgbotapi.InlineKeyboardMarkup) {
	text := title + "\n━━━━━━━━━━━━━━\n" + body + "\n\n🏗 该玩法正在建设中，敬请期待。"
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)),
	)
	return text, kb
}

func spiritPanelCatch(db *gorm.DB, userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	var b strings.Builder
	b.WriteString("🏹 灵墟\n━━━━━━━━━━━━━━\n六大灵墟区域随境界解锁：\n")
	for _, z := range SpiritZones {
		b.WriteString(fmt.Sprintf("· %s（境界门槛 %d 阶）\n", z.Name, z.Tier))
	}
	b.WriteString("\n🏗 捕捉玩法即将开放，先来灵晶斋备好灵晶吧！")
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🪷 灵晶斋", spCbBag)),
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)),
	)
	return b.String(), kb
}

func spiritPanelTeam(db *gorm.DB, userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	var deployed []UserSpiritServant
	db.Where("user_id = ? AND is_deployed = ?", userID, true).Find(&deployed)
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🛡 出战队列（%d/5）\n━━━━━━━━━━━━━━\n", len(deployed)))
	if len(deployed) == 0 {
		b.WriteString("尚无出战灵侍。")
	} else {
		for i := range deployed {
			s := &deployed[i]
			b.WriteString(fmt.Sprintf("· %s %s品 Lv.%d ⭐%d\n", s.Name, s.Quality, s.Level, s.Star))
		}
	}
	b.WriteString("\n\n🏗 编队调整即将开放。")
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)),
	)
	return b.String(), kb
}

func spiritPanelHelp() (string, tgbotapi.InlineKeyboardMarkup) {
	text := "ℹ️ 万灵阁玩法说明\n" +
		"━━━━━━━━━━━━━━\n" +
		"· 灵晶：灵侍体系专用货币，由积分单向兑换，不可逆反\n" +
		"· 灵尘：1 灵晶 = 100 灵尘，为最小计量单位\n" +
		"· 灵侍品阶：凡/灵/玄/地/天/圣，共六阶\n" +
		"· 灵侍属性：金木水火土阴阳（阴阳仅地阶以上可得）\n" +
		"· 捕捉：灵墟按境界开放六大区域，需消耗缚灵索\n" +
		"· 升星：吞噬同阶灵侍提升星级，战力大涨\n" +
		"· 推图 / 镜场斗法 / 护宗神兽：建设中\n" +
		"━━━━━━━━━━━━━━\n" +
		"所有灵侍操作仅在私聊进行，请道友移步私聊。"
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)),
	)
	return text, kb
}

// ------------------------------------------
// 对外入口
// ------------------------------------------

// SendSpiritPanel 私聊主动打开万灵阁（由 🐉 万灵阁 菜单或命令触发）
func SendSpiritPanel(bot *tgbotapi.BotAPI, userID int64, chatID int64) {
	text, kb := spiritPanelHome(db, userID)
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ReplyMarkup = kb
	if _, err := bot.Send(msg); err != nil {
		log.Printf("[灵侍] 发送面板失败 user=%d chat=%d err=%v", userID, chatID, err)
	}
}

// handleSpiritCallback sp: 前缀回调路由。返回 true 表示已处理。
// 由 main.go 回调分发链调用（此时已持有 lockUser）。
func handleSpiritCallback(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery) bool {
	if cb == nil || !strings.HasPrefix(cb.Data, "sp:") {
		return false
	}
	userID := cb.From.ID
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID

	// 私聊限定：所有灵侍操作只在私聊进行
	if cb.Message.Chat != nil && !cb.Message.Chat.IsPrivate() {
		ack := tgbotapi.NewCallback(cb.ID, "万灵阁仅在私聊开放，请私聊我操作")
		ack.ShowAlert = true
		if _, err := bot.Request(ack); err != nil {
			log.Printf("[灵侍] ack失败 user=%d err=%v", userID, err)
		}
		return true
	}

	var text string
	var kb tgbotapi.InlineKeyboardMarkup
	ackText := ""

	switch {
	case cb.Data == spCbHome:
		text, kb = spiritPanelHome(db, userID)
	case cb.Data == spCbList:
		text, kb = spiritPanelList(db, userID)
	case cb.Data == spCbBag:
		text, kb = spiritPanelBag(db, userID)
	case cb.Data == spCbCatch:
		text, kb = spiritPanelCatch(db, userID)
	case cb.Data == spCbTeam:
		text, kb = spiritPanelTeam(db, userID)
	case cb.Data == spCbPush:
		text, kb = spiritPanelComingSoon("🗺 灵墟推图", "推图章节、三星通关与扫荡玩法即将开放。")
	case cb.Data == spCbBeast:
		text, kb = spiritPanelComingSoon("🔮 护宗神兽", "宗门声望达到 2000 后可解锁护宗神兽喂养。")
	case cb.Data == spCbHelp:
		text, kb = spiritPanelHelp()
	case strings.HasPrefix(cb.Data, spCbExPrefix):
		pointsStr := strings.TrimPrefix(cb.Data, spCbExPrefix)
		points := 0
		fmt.Sscanf(pointsStr, "%d", &points)
		if points <= 0 {
			ackText = "无效的兑换档位"
			text, kb = spiritPanelBag(db, userID)
			break
		}
		lingjing, err := ExchangePointsToLingjing(db, userID, points)
		if err != nil {
			log.Printf("[灵侍] 兑换失败 user=%d points=%d err=%v", userID, points, err)
			ackText = fmt.Sprintf("兑换失败：%v", err)
		} else {
			ackText = fmt.Sprintf("兑换成功：%d 积分 → %d 灵晶", points, lingjing)
		}
		text, kb = spiritPanelBag(db, userID)
	default:
		ackText = "未知操作"
		text, kb = spiritPanelHome(db, userID)
	}

	// 原地刷新面板
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ReplyMarkup = &kb
	if _, err := bot.Send(edit); err != nil {
		log.Printf("[灵侍] 面板刷新失败 user=%d cb=%s err=%v", userID, cb.Data, err)
	}

	// ACK
	ack := tgbotapi.NewCallback(cb.ID, ackText)
	if ackText != "" {
		ack.ShowAlert = true
	}
	if _, err := bot.Request(ack); err != nil {
		log.Printf("[灵侍] ack失败 user=%d err=%v", userID, err)
	}
	return true
}
