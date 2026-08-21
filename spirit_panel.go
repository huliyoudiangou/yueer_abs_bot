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
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

// sp: 回调常量
const (
	spCbHome           = "sp:home"
	spCbList           = "sp:list"
	spCbListPagePrefix = "sp:list:page:" // sp:list:page:{page} 图鉴分页（每页10只）
	spCbBag            = "sp:bag"
	spCbCatch          = "sp:catch"
	spCbTeam           = "sp:team"
	spCbPush           = "sp:push"
	spCbMirror         = "sp:mirror"
	spCbForge          = "sp:forge"
	spCbBeast          = "sp:beast"
	spCbHelp           = "sp:help"
	spCbExPrefix       = "sp:ex:"         // sp:ex:100 / sp:ex:300 / sp:ex:500 / sp:ex:1000
	spCbZonePrefix     = "sp:zone:"       // sp:zone:qingzhu
	spPullPrefix       = "sp:pull:"       // sp:pull:qingzhu:fusu
	spCbChapterPrefix  = "sp:chapter:"    // sp:chapter:1
	spCbStagePrefix    = "sp:stage:"      // sp:stage:1:3
	spCbFightPrefix    = "sp:fight:"      // sp:fight:1:3
	spCbSweepPrefix    = "sp:sweep:"      // sp:sweep:1:3
	spCbEggs           = "sp:eggs"        // sp:eggs
	spEggHatchPrefix   = "sp:eggs:hatch:" // sp:eggs:hatch:{eggID}
	spCbStarUpPrefix   = "sp:starup:"     // sp:starup:{servantID} / sp:starup:confirm:{id}:{sacID}
	spCbFeed           = "sp:feed"        // sp:feed 灵侍养成面板
	spCbFeedDoPrefix   = "sp:feed:do:"    // sp:feed:do:{servantID}:{page}（page 可省略，喂养后留在原页）
	spCbFeedPagePrefix = "sp:feed:page:"  // sp:feed:page:{page} 养成分页（每页10只）
)

// 灵晶斋兑换档位（积分）
var spiritExchangeTiers = []int{100, 300, 500, 1000}

// ------------------------------------------
// 面板渲染（返回 text + keyboard 供新建或编辑复用）
// ------------------------------------------

func spiritPanelHome(db *gorm.DB, userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	lingjing, err := GetUserWalletBalance(db, userID)
	if err != nil {
		log.Printf("[灵侍] 查询钱包失败 user=%d err=%s", userID, formatTelegramSendError(err))
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
			tgbotapi.NewInlineKeyboardButtonData("🪞 镜场", spCbMirror),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔮 护宗神兽", spCbBeast),
			tgbotapi.NewInlineKeyboardButtonData("🔥 锻造炉", spCbForge),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("ℹ️ 玩法说明", spCbHelp),
			tgbotapi.NewInlineKeyboardButtonData("🥚 灵侍蛋", spCbEggs),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🌿 灵侍养成", spCbFeed),
		),
	)
	return text, kb
}

// spiritListPageSize 灵侍图鉴分页：每页 10 只
const spiritListPageSize = 10

func spiritPanelList(db *gorm.DB, userID int64, page int) (string, tgbotapi.InlineKeyboardMarkup) {
	var total int64
	if err := db.Model(&UserSpiritServant{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
		log.Printf("[灵侍] 图鉴计数失败 user=%d err=%s", userID, formatTelegramSendError(err))
	}
	totalPages := int((total + spiritListPageSize - 1) / spiritListPageSize)
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * spiritListPageSize

	var servants []UserSpiritServant
	if err := db.Where("user_id = ?", userID).
		Order("quality desc, star desc, level desc, id asc").
		Offset(offset).Limit(spiritListPageSize).Find(&servants).Error; err != nil {
		log.Printf("[灵侍] 图鉴查询失败 user=%d page=%d err=%s", userID, page, formatTelegramSendError(err))
	}
	var b strings.Builder
	if totalPages > 1 {
		b.WriteString(fmt.Sprintf("🐾 灵侍图鉴（第 %d/%d 页 · 共 %d 只）\n", page, totalPages, total))
	} else {
		b.WriteString("🐾 灵侍图鉴\n")
	}
	b.WriteString("━━━━━━━━━━━━━━\n")
	if total == 0 {
		b.WriteString("你尚未收服任何灵侍。\n前往「灵墟捕捉」遇见你的第一只灵侍吧！")
	} else {
		for i := range servants {
			s := &servants[i]
			deploy := ""
			if s.IsDeployed {
				deploy = "〔出战〕"
			}
			capMark := ""
			if s.Star >= QualityMaxStar[s.Quality] {
				capMark = "（满星）"
			}
			b.WriteString(fmt.Sprintf("· %s%s %s品·%s Lv.%d ⭐%d 战力%d%s\n",
				s.Name, deploy, s.Quality, s.Attribute, s.Level, s.Star, GetBattlePower(s), capMark))
		}
		b.WriteString("点击下方灵侍进入升星。")
	}
	var rows [][]tgbotapi.InlineKeyboardButton
	for i := range servants {
		s := &servants[i]
		label := fmt.Sprintf("⭐ %s", s.Name)
		if s.Star >= QualityMaxStar[s.Quality] {
			label += "（满星）"
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("%s%d", spCbStarUpPrefix, s.ID))))
	}
	if totalPages > 1 {
		var pageRow []tgbotapi.InlineKeyboardButton
		if page > 1 {
			pageRow = append(pageRow, tgbotapi.NewInlineKeyboardButtonData("◀ 上一页", fmt.Sprintf("%s%d", spCbListPagePrefix, page-1)))
		}
		if page < totalPages {
			pageRow = append(pageRow, tgbotapi.NewInlineKeyboardButtonData("下一页 ▶", fmt.Sprintf("%s%d", spCbListPagePrefix, page+1)))
		}
		rows = append(rows, pageRow)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
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
	lingjing, err := GetUserWalletBalance(db, userID)
	if err != nil {
		lingjing = 0
	}
	cul := GetOrCreateCultivation(userID)
	majorRealm := 0
	if cul != nil {
		majorRealm = cul.MajorRealm
	}

	var b strings.Builder
	b.WriteString("🏹 灵墟\n")
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString(fmt.Sprintf("💎 灵晶：%d\n", lingjing))
	b.WriteString("选择灵墟区域进行捕捉：\n\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, z := range SpiritZones {
		unlocked := majorRealm >= z.Tier
		pity := GetUserZonePity(userID, z.Key)
		status := "✅"
		label := z.Name
		if !unlocked {
			status = "🔒"
			label = fmt.Sprintf("%s（需境界%d阶）", z.Name, z.Tier)
		}
		pityStr := ""
		if pity != nil && pity.TianPity > 0 {
			pityStr = fmt.Sprintf(" 天保%d/%d", pity.TianPity, TianPityThreshold)
		}
		if pity != nil && pity.ShengPity > 0 {
			if th, ok := ShengPityThreshold[z.Key]; ok {
				pityStr += fmt.Sprintf(" 圣保%d/%d", pity.ShengPity, th)
			}
		}
		rowText := fmt.Sprintf("%s %s%s", status, label, pityStr)
		cbData := spCbZonePrefix + z.Key
		if !unlocked {
			cbData = "sp:locked"
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(rowText, cbData),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelZoneDetail 区域详情：显示灵索选择 + 保底进度
func spiritPanelZoneDetail(userID int64, zoneKey string) (string, tgbotapi.InlineKeyboardMarkup) {
	var zone *SpiritZone
	for i := range SpiritZones {
		if SpiritZones[i].Key == zoneKey {
			zone = &SpiritZones[i]
			break
		}
	}
	if zone == nil {
		return "未知区域", tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 灵墟", spCbCatch)))
	}

	lingjing, err := GetUserWalletBalance(db, userID)
	if err != nil {
		lingjing = 0
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🏹 %s\n", zone.Name))
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString(fmt.Sprintf("💎 灵晶：%d\n", lingjing))
	b.WriteString("品阶概率（万分率）：\n")
	for i, q := range SpiritQualityNames {
		if i < len(zone.SpawnRates) && zone.SpawnRates[i] > 0 {
			b.WriteString(fmt.Sprintf("  %s：%.2f%%\n", q, float64(zone.SpawnRates[i])/100.0))
		}
	}
	b.WriteString("━━━━━━━━━━━━━━\n")

	// 保底进度
	pity := GetUserZonePity(userID, zone.Key)
	if pity != nil {
		b.WriteString(fmt.Sprintf("天品保底：%d/%d抽\n", pity.TianPity, TianPityThreshold))
		if th, ok := ShengPityThreshold[zone.Key]; ok {
			b.WriteString(fmt.Sprintf("圣品保底：%d/%d抽\n", pity.ShengPity, th))
		}
	} else {
		b.WriteString(fmt.Sprintf("天品保底：0/%d抽\n", TianPityThreshold))
	}
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString("选择灵索进行捕捉：\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	for _, rope := range SpiritRopes {
		afford := lingjing >= rope.Cost
		label := fmt.Sprintf("%s（%d灵晶）", rope.Name, rope.Cost)
		if rope.Bonus > 0 {
			label += fmt.Sprintf(" +%.0f%%", rope.Bonus*100)
		}
		cb := spPullPrefix + zone.Key + ":" + rope.Key
		if !afford {
			label = "❌ " + label
			cb = "sp:nojing"
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, cb),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 灵墟", spCbCatch)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelCatchResult 捕捉结果面板
func spiritPanelCatchResult(userID int64, result *CatchResult, zoneKey string) (string, tgbotapi.InlineKeyboardMarkup) {
	var zone *SpiritZone
	for i := range SpiritZones {
		if SpiritZones[i].Key == zoneKey {
			zone = &SpiritZones[i]
			break
		}
	}
	zoneName := zoneKey
	if zone != nil {
		zoneName = zone.Name
	}

	var b strings.Builder
	b.WriteString("🏹 捕捉结果\n")
	b.WriteString("━━━━━━━━━━━━━━\n")

	if result.UsedShengP {
		b.WriteString("✨ 圣品保底触发！\n")
	}
	if result.UsedTianP {
		b.WriteString("🌟 天品保底触发！\n")
	}

	if result.Success && result.Servant != nil {
		s := result.Servant
		b.WriteString(fmt.Sprintf("🎉 捕捉成功！\n"))
		b.WriteString(fmt.Sprintf("━━━━━━━━━━━━━━\n"))
		b.WriteString(fmt.Sprintf("名称：%s\n", s.Name))
		b.WriteString(fmt.Sprintf("品阶：%s品\n", s.Quality))
		b.WriteString(fmt.Sprintf("属性：%s\n", s.Attribute))
		b.WriteString(fmt.Sprintf("战力：%d\n", GetBattlePower(s)))
		b.WriteString(fmt.Sprintf("区域：%s\n", zoneName))
	} else if result.Escape {
		b.WriteString(fmt.Sprintf("💨 遇到%s品灵侍，但逃脱了！\n", result.EncounterQ))
		b.WriteString("灵晶已消耗，保底计数已累加。\n")
	} else {
		b.WriteString("捕捉异常，请稍后重试。\n")
	}

	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🏹 再次捕捉", spCbZonePrefix+zoneKey),
			tgbotapi.NewInlineKeyboardButtonData("🏹 灵墟", spCbCatch),
		),
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
		"· 升星：星级上限 凡3/灵4/玄5/地6/天7/圣9；≤3★ 需同品同属性祭品，4-6★ 另需同星级，7-9★ 需同名同星级灵侍；段1-2 可消耗灵魄替代祭品，段3 可消耗万能真身碎片替代同名要求；升星后等级重置\n" +
		"· 推图：六大章节各 10 关 + Boss，神行符 10/日，三星可扫荡\n" +
		"· 镜场：上架镜像供道友挑战，胜 30 / 负 10 灵晶，10 次/日，24h 内可复仇\n" +
		"· 锻造：锻造炉产出兵甲/魂魄两类装备（目标品质50%/-1档30%/-2档20%），穿戴提升战力；精炼：每级该装备属性 +2%，上限 +10，成本随等级与品阶递增；熔炼返还 40%\n" +
		"· 护宗神兽：宗门声望 2000 解锁，喂养耗 20-50 声望（随等级递增），三阶为全宗提供 +1%/+2%/+3.5% 世界Boss伤害\n" +
		"· 灵侍蛋：击败章节 Boss 每次 30% 概率掉蛋（地阶及以下），在灵侍蛋面板孵化为对应品阶灵侍\n" +
		"· 道具：灵魄随推图胜利掉落（普通关 25%/章节 Boss 50%）；万能真身碎片随第 5/6 章 Boss 掉落（10%）；扫荡不掉道具\n" +
		"· 养成：养成面板喂养灵侍升级（每级耗灵晶 凡30/灵50/玄80/地120/天200/圣300），每级 +2% 一级基础属性，等级上限 = 星级 × 10\n" +
		"━━━━━━━━━━━━━━\n" +
		"所有灵侍操作仅在私聊进行，请道友移步私聊。"
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)),
	)
	return text, kb
}

// ------------------------------------------
// 灵墟推图（PVE）
// ------------------------------------------

// parseSpStageData 解析 "sp:xxx:章节:关卡" → (章节, 关卡)
func parseSpStageData(data, prefix string) (int, int, bool) {
	rest := strings.TrimPrefix(data, prefix)
	parts := strings.SplitN(rest, ":", 2)
	if len(parts) != 2 {
		return 0, 0, false
	}
	ch, err1 := strconv.Atoi(parts[0])
	st, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil || ch < 1 || ch > len(SpiritZones) || st < 1 || st > bossStageID {
		return 0, 0, false
	}
	return ch, st, true
}

// spiritPanelPush 推图主界面：章节列表
func spiritPanelPush(db *gorm.DB, userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	cul := GetOrCreateCultivation(userID)
	majorRealm := 0
	if cul != nil {
		majorRealm = cul.MajorRealm
	}

	var b strings.Builder
	b.WriteString("🗺 灵墟推图\n")
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString(fmt.Sprintf("🏃 神行符：%d/%d（每日恢复）\n", GetUserStamina(userID), divineTravelDailyCap))
	b.WriteString("每章 10 关 + 1 Boss，每次挑战消耗 1 神行符\n")
	b.WriteString("首通全额奖励，升星 20%，三星可扫荡（每日3次）\n\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	for i, z := range SpiritZones {
		chapterID := i + 1
		var cleared int64
		db.Model(&SpiritStageProgress{}).
			Where("user_id = ? AND chapter_id = ? AND stars >= ?", userID, chapterID, 1).
			Count(&cleared)
		if majorRealm >= z.Tier {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					fmt.Sprintf("%s · %d/%d 关", z.Name, cleared, bossStageID),
					fmt.Sprintf("%s%d", spCbChapterPrefix, chapterID))))
		} else {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					fmt.Sprintf("🔒 %s（需境界%d阶）", z.Name, z.Tier), "sp:chlocked")))
		}
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelStages 章节详情：11 个关卡按钮
func spiritPanelStages(userID int64, chapterID int) (string, tgbotapi.InlineKeyboardMarkup) {
	zone := chapterZone(chapterID)
	if zone == nil {
		return "未知章节", tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 推图", spCbPush)))
	}
	var progs []SpiritStageProgress
	db.Where("user_id = ? AND chapter_id = ?", userID, chapterID).Find(&progs)
	progMap := make(map[int]int, len(progs))
	for _, p := range progs {
		progMap[p.StageID] = p.Stars
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🗺 第%d章 · %s\n", chapterID, zone.Name))
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString(fmt.Sprintf("🏃 神行符：%d/%d\n", GetUserStamina(userID), divineTravelDailyCap))
	b.WriteString("选择关卡：\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	for stage := 1; stage <= bossStageID; stage++ {
		label := fmt.Sprintf("%d", stage)
		if stage == bossStageID {
			label = "👑 Boss"
		}
		if stars := progMap[stage]; stars > 0 {
			label += " " + strings.Repeat("★", stars)
		}
		unlocked := stage == 1 || progMap[stage-1] >= 1
		if !unlocked {
			label = "🔒 " + label
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("%s%d:%d", spCbStagePrefix, chapterID, stage))))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 推图", spCbPush)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelStageDetail 关卡详情：敌人预览 + 挑战/扫荡
func spiritPanelStageDetail(userID int64, chapterID, stageID int) (string, tgbotapi.InlineKeyboardMarkup) {
	zone := chapterZone(chapterID)
	if zone == nil {
		return "未知章节", tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 推图", spCbPush)))
	}

	// 解锁判断：前关至少 1 星
	if stageID > 1 {
		var prevP SpiritStageProgress
		db.Where("user_id = ? AND chapter_id = ? AND stage_id = ?", userID, chapterID, stageID-1).
			First(&prevP)
		if prevP.Stars < 1 {
			bossTag := ""
			if stageID == bossStageID {
				bossTag = "（章节 Boss）"
			}
			text := fmt.Sprintf("⚔️ 第%d关%s\n━━━━━━━━━━━━━━\n🔒 上一关尚未通关，此关未解锁。\n先击败「%s」再来挑战。",
				stageID, bossTag, buildStageEnemy(chapterID, stageID-1).Name)
			kb := tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🔙 章节", fmt.Sprintf("%s%d", spCbChapterPrefix, chapterID))))
			return text, kb
		}
	}

	var myProg SpiritStageProgress
	stars := 0
	sweepCount := 0
	if err := db.Where("user_id = ? AND chapter_id = ? AND stage_id = ?", userID, chapterID, stageID).
		First(&myProg).Error; err == nil {
		stars = myProg.Stars
		if myProg.SweepDay == time.Now().Format("20060102") {
			sweepCount = myProg.SweepCount
		}
	}

	enemy := buildStageEnemy(chapterID, stageID)
	bossTag := ""
	if stageID == bossStageID {
		bossTag = "（章节 Boss）"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("⚔️ 第%d关%s\n", stageID, bossTag))
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString(fmt.Sprintf("敌人：%s\n", enemy.Name))
	b.WriteString(fmt.Sprintf("属性：%s｜HP %d｜ATK %d\n", enemy.Element, enemy.MaxHP, enemy.ATK))
	b.WriteString(fmt.Sprintf("我方星级：%d/3\n", stars))
	b.WriteString(fmt.Sprintf("首通奖励：%d 灵晶｜扫荡奖励：%d 灵晶\n",
		stageReward(chapterID, stageID), stageReward(chapterID, stageID)*sweepRewardRatio/100))
	b.WriteString(fmt.Sprintf("🏃 神行符：%d/%d\n", GetUserStamina(userID), divineTravelDailyCap))

	var rows [][]tgbotapi.InlineKeyboardButton
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(
			"⚔️ 挑战（-1 神行符）", fmt.Sprintf("%s%d:%d", spCbFightPrefix, chapterID, stageID))))
	if stars >= 3 {
		if sweepCount < sweepDailyLimit {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					fmt.Sprintf("🔄 扫荡（今日 %d/%d）", sweepCount, sweepDailyLimit),
					fmt.Sprintf("%s%d:%d", spCbSweepPrefix, chapterID, stageID))))
		} else {
			b.WriteString("\n今日扫荡已用尽（3次），明日再来。")
		}
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 章节", fmt.Sprintf("%s%d", spCbChapterPrefix, chapterID))))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// ------------------------------------------
// 镜场（异步 PVP）
// ------------------------------------------

// spiritPanelMirror 镜场主界面
func spiritPanelMirror(userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	remaining := GetPvpDailyRemaining(userID)
	mirror := GetMyMirror(userID)
	revTargets := GetPvpRevengeTargets(userID)

	var b strings.Builder
	b.WriteString("🪞 镜场\n")
	b.WriteString("━━━━━━━━━━━━━━\n")
	if mirror != nil {
		b.WriteString(fmt.Sprintf("我方镜像：已上架（战力 %d，%d 名灵侍），%s 前有效\n",
			mirror.TeamPower, mirror.MemberCount, mirror.ExpiresAt.Format("01-02 15:04")))
	} else {
		b.WriteString("我方镜像：未上架（上架后可被道友挑战）\n")
	}
	b.WriteString(fmt.Sprintf("⚔️ 今日攻击：%d/%d 剩余\n", remaining, pvpDailyLimit))
	b.WriteString(fmt.Sprintf("奖励：胜 %d 灵晶 / 负 %d 灵晶\n", pvpWinReward, pvpLoseReward))
	b.WriteString("镜像被破后 24 小时内可复仇对方\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🪞 上架/刷新镜像", "sp:mirror:set"),
		tgbotapi.NewInlineKeyboardButtonData("⚔️ 攻击镜像", "sp:mirror:atk")))
	if len(revTargets) > 0 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("😤 复仇（%d 人可复仇）", len(revTargets)), "sp:mirror:rev")))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("📜 战绩", "sp:mirror:hist"),
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelMirrorRevenge 复仇目标列表
func spiritPanelMirrorRevenge(userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	targets := GetPvpRevengeTargets(userID)
	var b strings.Builder
	b.WriteString("😤 复仇目标\n━━━━━━━━━━━━━━\n")
	if len(targets) == 0 {
		b.WriteString("暂无可复仇的道友（窗口 24 小时，且对方镜像需仍有效）。")
	} else {
		b.WriteString("24 小时内这些道友破过你的镜像，攻击其镜像即可复仇：\n")
	}
	var rows [][]tgbotapi.InlineKeyboardButton
	for i := range targets {
		t := &targets[i]
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("😤 复仇：%s（战力 %d）", spiritPvpUserName(t.AttackerID), t.AttackerPower),
				fmt.Sprintf("sp:mirror:rev:%d", t.AttackerID))))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 镜场", spCbMirror)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelMirrorHistory 镜场战绩
func spiritPanelMirrorHistory(userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	battles := GetPvpHistory(userID)
	var b strings.Builder
	b.WriteString("📜 镜场战绩\n━━━━━━━━━━━━━━\n")
	if len(battles) == 0 {
		b.WriteString("暂无战斗记录。")
	} else {
		for i := range battles {
			bt := &battles[i]
			ts := bt.CreatedAt.Format("01-02 15:04")
			if bt.AttackerID == userID {
				opp := spiritPvpUserName(bt.DefenderID)
				if bt.AttackerWin {
					b.WriteString(fmt.Sprintf("· %s ⚔️ 攻 %s：胜 + %d 灵晶\n", ts, opp, bt.Reward))
				} else {
					b.WriteString(fmt.Sprintf("· %s ⚔️ 攻 %s：负 + %d 灵晶\n", ts, opp, bt.Reward))
				}
			} else {
				opp := spiritPvpUserName(bt.AttackerID)
				if bt.AttackerWin {
					b.WriteString(fmt.Sprintf("· %s 🛡 守 %s：镜像被破（对方 %d vs 我方 %d）\n",
						ts, opp, bt.AttackerPower, bt.DefenderPower))
				} else {
					b.WriteString(fmt.Sprintf("· %s 🛡 守 %s：防守成功\n", ts, opp))
				}
			}
		}
	}
	kb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 镜场", spCbMirror),
			tgbotapi.NewInlineKeyboardButtonData("🐉 万灵阁", spCbHome)))
	return b.String(), kb
}

// pvpAttackResultPanel 攻击结果面板
func pvpAttackResultPanel(res *PvpAttackResult) (string, tgbotapi.InlineKeyboardMarkup) {
	verdict := "💀 败"
	if res.Win {
		verdict = "⚔️ 胜"
	}
	var b strings.Builder
	b.WriteString("🪞 镜场斗法\n")
	b.WriteString(fmt.Sprintf("%s — %s\n", verdict, res.DefenderName))
	b.WriteString(fmt.Sprintf("对方镜像战力：%d\n", res.DefenderPower))
	if res.HPTotal > 0 {
		b.WriteString(fmt.Sprintf("我方队伍剩余血量：%.0f%%\n", float64(res.HPLeft)/float64(res.HPTotal)*100))
	}
	b.WriteString(fmt.Sprintf("战斗奖励：+ %d 灵晶\n", res.Reward))
	b.WriteString(fmt.Sprintf("今日剩余攻击：%d 次\n", res.Remaining))

	var rows [][]tgbotapi.InlineKeyboardButton
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("⚔️ 再攻一次", "sp:mirror:atk")))
	if !res.Win {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("😤 向 %s 复仇", res.DefenderName),
				fmt.Sprintf("sp:mirror:rev:%d", res.DefenderID))))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 镜场", spCbMirror)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// ------------------------------------------
// 锻造炉 / 装备仓库
// ------------------------------------------

// spiritPanelForge 锻造炉主界面
func spiritPanelForge(db *gorm.DB, userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	lingjing, err := GetUserWalletBalance(db, userID)
	if err != nil {
		lingjing = 0
	}
	var b strings.Builder
	b.WriteString("🔥 锻造炉\n")
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString(fmt.Sprintf("💎 灵晶：%d\n", lingjing))
	b.WriteString("锻造得目标品质或以下：50%目标 / 30%-1档 / 20%-2档\n")
	b.WriteString("兵甲偏物攻（HP/攻/防/速），魂魄偏法术（HP/防/速/法）\n")
	b.WriteString("熔炼仓库装备返还 40% 锻造成本\n")

	var rows [][]tgbotapi.InlineKeyboardButton
	for qi, q := range SpiritQualityNames {
		cost := equipmentForgeCost[q]
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("兵甲·%s品 %d", q, cost), fmt.Sprintf("sp:forge:0:%d", qi)),
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("魂魄·%s品 %d", q, cost), fmt.Sprintf("sp:forge:1:%d", qi)),
		))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🎒 装备仓库", "sp:equip"),
	))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelEquip 装备仓库 + 已装备列表
func spiritPanelEquip(userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	equipped, bag := ListEquipment(userID)
	var b strings.Builder
	b.WriteString("🎒 装备\n")
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString("精炼：每级该装备属性 +2%，上限 +10，成本随等级与品阶递增。\n")
	b.WriteString("【已装备】\n")
	if len(equipped) == 0 {
		b.WriteString("暂无已装备。\n")
	} else {
		for i := range equipped {
			e := &equipped[i]
			b.WriteString(fmt.Sprintf("· %s → %s\n", equipmentStatLine(e), getServantNameByID(userID, e.ServantID)))
		}
	}
	b.WriteString("【仓库】\n")
	if len(bag) == 0 {
		b.WriteString("仓库暂无装备。\n")
	} else {
		for i := range bag {
			b.WriteString(fmt.Sprintf("· %s\n", equipmentStatLine(&bag[i])))
		}
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := range bag {
		e := &bag[i]
		refund := equipmentForgeCost[e.Quality] * equipmentMeltRefundRatio / 100
		row := []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("穿戴 %s", e.Name), fmt.Sprintf("sp:equip:put:%d", e.ID)),
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("熔炼+%d", refund), fmt.Sprintf("sp:equip:melt:%d", e.ID)),
		}
		if e.Enhance < equipEnhanceMax {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("精炼-%d", EquipEnhanceCost(e)), fmt.Sprintf("sp:equip:enhance:%d", e.ID)))
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(row...))
	}
	for i := range equipped {
		e := &equipped[i]
		row := []tgbotapi.InlineKeyboardButton{
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("卸下 %s", e.Name), fmt.Sprintf("sp:equip:off:%d", e.ID)),
		}
		if e.Enhance < equipEnhanceMax {
			row = append(row, tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("精炼-%d", EquipEnhanceCost(e)), fmt.Sprintf("sp:equip:enhance:%d", e.ID)))
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(row...))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔥 锻造炉", spCbForge),
		tgbotapi.NewInlineKeyboardButtonData("🔙 万灵阁", spCbHome),
	))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelEquipPick 选择穿戴目标灵侍
func spiritPanelEquipPick(userID int64, equipmentID uint) (string, tgbotapi.InlineKeyboardMarkup) {
	var eq ServantEquipment
	if err := db.Where("id = ? AND user_id = ?", equipmentID, userID).First(&eq).Error; err != nil {
		return "装备不存在或不属于你", tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🎒 装备", "sp:equip")))
	}
	var servants []UserSpiritServant
	db.Where("user_id = ?", userID).Order("star desc, level desc").Limit(10).Find(&servants)

	var b strings.Builder
	b.WriteString(fmt.Sprintf("⚔️ 穿戴 %s\n", eq.Name))
	b.WriteString("━━━━━━━━━━━━━━\n")
	if len(servants) == 0 {
		b.WriteString("你还没有灵侍。")
	} else {
		b.WriteString("选择穿戴到哪只灵侍：\n")
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := range servants {
		s := &servants[i]
		label := fmt.Sprintf("%s %s品 Lv.%d", s.Name, s.Quality, s.Level)
		var occupied int64
		db.Model(&ServantEquipment{}).
			Where("servant_id = ? AND slot_type = ?", s.ID, eq.SlotType).Count(&occupied)
		if occupied > 0 {
			label += "（替换）"
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label,
				fmt.Sprintf("sp:equip:confirm:%d:%d", equipmentID, s.ID))))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🎒 装备", "sp:equip")))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelBeast 护宗神兽面板
func spiritPanelBeast(userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	backKb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)))

	var member SectMember
	if err := db.Where("user_id = ?", userID).First(&member).Error; err != nil {
		return "🔮 护宗神兽\n━━━━━━━━━━━━━━\n你尚未加入宗门。\n加入宗门后，可与同门共养护宗神兽。", backKb
	}
	var sect Sect
	if err := db.First(&sect, member.SectID).Error; err != nil {
		return "🔮 护宗神兽\n━━━━━━━━━━━━━━\n宗门信息读取失败，请稍后再试。", backKb
	}

	var b strings.Builder
	b.WriteString("🔮 护宗神兽\n")
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString(fmt.Sprintf("宗门：%s（声望 %d）\n", sect.Name, sect.Prestige))

	if sect.Prestige < sectBeastUnlockPrestige {
		b.WriteString(fmt.Sprintf("🔒 神兽尚在封印：需宗门声望 %d，当前 %d\n",
			sectBeastUnlockPrestige, sect.Prestige))
		b.WriteString("宗门声望达标后，成员即可喂养护宗神兽。")
		return b.String(), backKb
	}

	var beast SectBeast
	if err := db.Where("sect_id = ?", sect.ID).First(&beast).Error; err != nil {
		if err != gorm.ErrRecordNotFound {
			log.Printf("[灵侍] 神兽查询失败 user=%d sect=%d err=%s", userID, sect.ID, formatTelegramSendError(err))
		}
		beast = SectBeast{SectID: sect.ID}
	}

	cost := sectBeastFeedCost(beast.Level)
	buffPct := sectBeastStageBuff(beast.Stage) * 100
	b.WriteString(fmt.Sprintf("神兽：%s（等级 %d）\n", sectBeastStageNames[beast.Stage], beast.Level))
	b.WriteString(fmt.Sprintf("阶段：%d/3｜全宗世界Boss伤害 buff：+%.1f%%\n", beast.Stage, buffPct))
	if beast.Stage < 3 {
		b.WriteString(fmt.Sprintf("下一阶段：等级 %d（当前 %d）\n", sectBeastNextStageLevel(beast.Stage), beast.Level))
	}
	b.WriteString(fmt.Sprintf("累计灌注：%d 声望\n", beast.TotalFed))
	b.WriteString(fmt.Sprintf("喂养成本：%d 宗门声望\n", cost))

	leaders := GetSectBeastLeaders(int64(sect.ID), 5)
	if len(leaders) > 0 {
		b.WriteString("喂养排行：\n")
		for i, l := range leaders {
			b.WriteString(fmt.Sprintf("  %d. %s 灌注 %d\n", i+1, spiritPvpUserName(l.UserID), l.Total))
		}
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData(
			fmt.Sprintf("🐾 喂养（-%d 声望）", cost), "sp:beast:feed")))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelEggs 灵侍蛋面板（未孵化列表 + 孵化历史）
func spiritPanelEggs(userID int64) (string, tgbotapi.InlineKeyboardMarkup) {
	bag, hatched := ListEggs(userID)
	var b strings.Builder
	b.WriteString("🥚 灵侍蛋\n")
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString("击败章节 Boss 有概率掉落灵侍蛋（地阶及以下），\n")
	b.WriteString("孵化后生成对应品阶的灵侍。\n")
	b.WriteString("【未孵化】\n")
	if len(bag) == 0 {
		b.WriteString("暂无未孵化的灵侍蛋。\n")
	} else {
		for i := range bag {
			b.WriteString(fmt.Sprintf("· %s 品灵侍蛋（来源：%s）\n", bag[i].Quality, bag[i].ZoneName))
		}
	}
	if len(hatched) > 0 {
		b.WriteString("【最近孵化】\n")
		for i := range hatched {
			b.WriteString(fmt.Sprintf("· %s 品灵侍蛋（%s）→ 已孵化\n", hatched[i].Quality, hatched[i].ZoneName))
		}
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := range bag {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("孵化 %s 品灵侍蛋", bag[i].Quality),
				fmt.Sprintf("%s%d", spEggHatchPrefix, bag[i].ID))))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 万灵阁", spCbHome)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelFeed 灵侍养成：灵侍分页列表 + 喂养（消耗灵晶升级）
func spiritPanelFeed(userID int64, page int) (string, tgbotapi.InlineKeyboardMarkup) {
	backKb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)))

	lingjing, _ := GetUserWalletBalance(db, userID)
	var total int64
	if err := db.Model(&UserSpiritServant{}).Where("user_id = ?", userID).Count(&total).Error; err != nil {
		log.Printf("[灵侍] 养成计数失败 user=%d err=%s", userID, formatTelegramSendError(err))
	}
	totalPages := int((total + spiritListPageSize - 1) / spiritListPageSize)
	if totalPages < 1 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	offset := (page - 1) * spiritListPageSize

	var servants []UserSpiritServant
	if err := db.Where("user_id = ?", userID).
		Order("quality desc, star desc, level desc, id asc").
		Offset(offset).Limit(spiritListPageSize).Find(&servants).Error; err != nil {
		log.Printf("[灵侍] 养成查询失败 user=%d page=%d err=%s", userID, page, formatTelegramSendError(err))
	}
	var b strings.Builder
	if totalPages > 1 {
		b.WriteString(fmt.Sprintf("🌿 灵侍养成（第 %d/%d 页 · 共 %d 只）\n", page, totalPages, total))
	} else {
		b.WriteString("🌿 灵侍养成\n")
	}
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString(fmt.Sprintf("💎 灵晶：%d\n", lingjing))
	b.WriteString("喂养升级：每级 +2% 一级基础属性，等级上限 = 星级 × 10（升星重置等级并提高上限）。\n")
	if total == 0 {
		b.WriteString("你尚未收服任何灵侍。前往「灵墟捕捉」收服吧！")
		return b.String(), backKb
	}
	var rows [][]tgbotapi.InlineKeyboardButton
	for i := range servants {
		s := &servants[i]
		maxLv := MaxLevelByStar(s.Star)
		cost := FeedCostByQuality[s.Quality]
		if cost <= 0 {
			cost = FeedCostByQuality["凡"]
		}
		capMark := ""
		if s.Level >= maxLv {
			capMark = "（满级）"
		}
		b.WriteString(fmt.Sprintf("· %s %s品·%s ⭐%d Lv.%d/%d 战力%d%s\n",
			s.Name, s.Quality, s.Attribute, s.Star, s.Level, maxLv, GetBattlePower(s), capMark))
		if s.Level < maxLv {
			rows = append(rows, tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData(
					fmt.Sprintf("喂养 %s（-%d 灵晶）", s.Name, cost),
					fmt.Sprintf("%s%d:%d", spCbFeedDoPrefix, s.ID, page))))
		}
	}
	if totalPages > 1 {
		var pageRow []tgbotapi.InlineKeyboardButton
		if page > 1 {
			pageRow = append(pageRow, tgbotapi.NewInlineKeyboardButtonData("◀ 上一页", fmt.Sprintf("%s%d", spCbFeedPagePrefix, page-1)))
		}
		if page < totalPages {
			pageRow = append(pageRow, tgbotapi.NewInlineKeyboardButtonData("下一页 ▶", fmt.Sprintf("%s%d", spCbFeedPagePrefix, page+1)))
		}
		rows = append(rows, pageRow)
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// spiritPanelStarUp 升星界面：目标 + 祭品需求 + 候选祭品列表
func spiritPanelStarUp(userID int64, servantID uint) (string, tgbotapi.InlineKeyboardMarkup) {
	backKb := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🐾 灵侍图鉴", spCbList),
			tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)))

	var target UserSpiritServant
	if err := db.Where("id = ? AND user_id = ?", servantID, userID).First(&target).Error; err != nil {
		return "灵侍不存在或不属于你。", backKb
	}
	maxStar := QualityMaxStar[target.Quality]
	var b strings.Builder
	b.WriteString("⭐ 升星\n")
	b.WriteString("━━━━━━━━━━━━━━\n")
	b.WriteString(fmt.Sprintf("目标：%s %s品·%s Lv.%d ⭐%d/%d\n",
		target.Name, target.Quality, target.Attribute, target.Level, target.Star, maxStar))

	if target.Star >= maxStar {
		b.WriteString("该灵侍已达品阶星级上限。")
		return b.String(), backKb
	}
	if target.IsDeployed {
		b.WriteString("该灵侍当前出战中，请先下阵再升星。")
		return b.String(), backKb
	}

	b.WriteString(StarUpRequirementText(&target) + "\n")
	b.WriteString("升星后等级重置为 Lv.1，祭品灵侍将被消耗。\n")

	items := GetUserSpiritItems(userID)
	soulN := items[itemTypeSoul]
	shardN := items[itemTypeShard]
	stage := StarUpStage(target.Star + 1)
	b.WriteString(fmt.Sprintf("\n🧪 持有灵魄：%d  🎭 持有真身碎片：%d\n", soulN, shardN))

	cands := ListStarUpSacrifices(userID, &target)
	if len(cands) > 0 {
		b.WriteString("\n可用祭品：\n")
		for i := range cands {
			if i >= 15 {
				break
			}
			c := &cands[i]
			b.WriteString(fmt.Sprintf("· %s %s品·%s ⭐%d 战力%d\n",
				c.Name, c.Quality, c.Attribute, c.Star, GetBattlePower(c)))
		}
		if len(cands) > 15 {
			b.WriteString(fmt.Sprintf("（共 %d 只，仅显示前 15 只）", len(cands)))
		}
	} else {
		b.WriteString("\n当前没有符合条件的祭品灵侍。\n可前往灵墟捕捉，或孵化灵侍蛋获得。")
	}

	// 灵魄选项（段1-2）
	if stage <= 2 && soulN > 0 {
		b.WriteString("\n💡 可直接消耗 1 个灵魄升星，无需祭品灵侍。")
	}

	// 碎片祭品段（段3）
	var shardCands []UserSpiritServant
	if stage == 3 && shardN > 0 {
		shardCands = ListShardSacrifices(userID, &target)
		b.WriteString("\n【碎片祭品】消耗 1 个万能真身碎片，祭品仅需同品质+同星级：\n")
		if len(shardCands) == 0 {
			b.WriteString("· 暂无同品质同星级灵侍\n")
		}
		for i := range shardCands {
			if i >= 15 {
				break
			}
			c := &shardCands[i]
			b.WriteString(fmt.Sprintf("· %s %s品·%s ⭐%d 战力%d\n",
				c.Name, c.Quality, c.Attribute, c.Star, GetBattlePower(c)))
		}
		if len(shardCands) > 15 {
			b.WriteString(fmt.Sprintf("（共 %d 只，仅显示前 15 只）", len(shardCands)))
		}
	}

	var rows [][]tgbotapi.InlineKeyboardButton
	for i := range cands {
		if i >= 15 {
			break
		}
		c := &cands[i]
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("以 %s 为祭品", c.Name),
				fmt.Sprintf("sp:starup:confirm:%d:%d", servantID, c.ID))))
	}
	if stage <= 2 && soulN > 0 {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				"⭐ 灵魄升星（-1 灵魄）",
				fmt.Sprintf("sp:starup:confirm:%d:item:%s", servantID, itemTypeSoul))))
	}
	for i := range shardCands {
		if i >= 15 {
			break
		}
		c := &shardCands[i]
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("🎭 碎片祭品：%s", c.Name),
				fmt.Sprintf("sp:starup:confirm:%d:%d:%s", servantID, c.ID, itemTypeShard))))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("🐾 灵侍图鉴", spCbList),
		tgbotapi.NewInlineKeyboardButtonData("🔙 返回万灵阁", spCbHome)))
	return b.String(), tgbotapi.NewInlineKeyboardMarkup(rows...)
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
		log.Printf("[灵侍] 发送面板失败 user=%d chat=%d err=%s", userID, chatID, formatTelegramSendError(err))
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
			log.Printf("[灵侍] ack失败 user=%d err=%s", userID, formatTelegramSendError(err))
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
		text, kb = spiritPanelList(db, userID, 1)
	case strings.HasPrefix(cb.Data, spCbListPagePrefix):
		page := 0
		fmt.Sscanf(strings.TrimPrefix(cb.Data, spCbListPagePrefix), "%d", &page)
		text, kb = spiritPanelList(db, userID, page)
	case cb.Data == spCbBag:
		text, kb = spiritPanelBag(db, userID)
	case cb.Data == spCbCatch:
		text, kb = spiritPanelCatch(db, userID)
	case cb.Data == spCbTeam:
		text, kb = spiritPanelTeam(db, userID)
	case cb.Data == spCbPush:
		text, kb = spiritPanelPush(db, userID)
	case cb.Data == spCbBeast:
		text, kb = spiritPanelBeast(userID)
	case cb.Data == "sp:beast:feed":
		beast, cost, err := FeedSectBeast(userID)
		if err != nil {
			ackText = fmt.Sprintf("喂养失败：%v", err)
		} else {
			ackText = fmt.Sprintf("🐾 已喂养护宗神兽：等级 %d（-%d 声望）", beast.Level, cost)
		}
		text, kb = spiritPanelBeast(userID)
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
			log.Printf("[灵侍] 兑换失败 user=%d points=%d err=%s", userID, points, formatTelegramSendError(err))
			ackText = fmt.Sprintf("兑换失败：%v", err)
		} else {
			ackText = fmt.Sprintf("兑换成功：%d 积分 → %d 灵晶", points, lingjing)
		}
		text, kb = spiritPanelBag(db, userID)
	case strings.HasPrefix(cb.Data, spPullPrefix):
		// 捕捉：sp:pull:{zone}:{rope}
		rest := strings.TrimPrefix(cb.Data, spPullPrefix)
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 {
			ackText = "无效的捕捉指令"
			text, kb = spiritPanelCatch(db, userID)
			break
		}
		zoneKey := parts[0]
		ropeKey := parts[1]
		result, err := CatchSpiritServant(db, userID, zoneKey, ropeKey)
		if err != nil {
			log.Printf("[灵侍] 捕捉失败 user=%d zone=%s rope=%s err=%s", userID, zoneKey, ropeKey, formatTelegramSendError(err))
			ackText = fmt.Sprintf("%v", err)
			text, kb = spiritPanelZoneDetail(userID, zoneKey)
			break
		}
		if result.Success && result.Servant != nil {
			ackText = fmt.Sprintf("🎉 捕到 %s品·%s！", result.Servant.Quality, result.Servant.Name)
		} else if result.Escape {
			ackText = fmt.Sprintf("💨 %s品灵侍逃跑了！", result.EncounterQ)
		}
		text, kb = spiritPanelCatchResult(userID, result, zoneKey)
	case cb.Data == "sp:locked":
		ackText = "该区域尚未解锁，请提升修为境界"
		text, kb = spiritPanelCatch(db, userID)
	case cb.Data == "sp:chlocked":
		ackText = "该章节尚未解锁，请提升修为境界"
		text, kb = spiritPanelPush(db, userID)
	case cb.Data == "sp:nojing":
		ackText = "灵晶不足，请前往灵晶斋兑换"
		text, kb = spiritPanelBag(db, userID)
	case strings.HasPrefix(cb.Data, spCbZonePrefix):
		zoneKey := strings.TrimPrefix(cb.Data, spCbZonePrefix)
		text, kb = spiritPanelZoneDetail(userID, zoneKey)
	case strings.HasPrefix(cb.Data, spCbChapterPrefix):
		ch, err := strconv.Atoi(strings.TrimPrefix(cb.Data, spCbChapterPrefix))
		if err != nil || ch < 1 || ch > len(SpiritZones) {
			ackText = "未知章节"
			text, kb = spiritPanelPush(db, userID)
			break
		}
		text, kb = spiritPanelStages(userID, ch)
	case strings.HasPrefix(cb.Data, spCbStagePrefix):
		ch, st, ok := parseSpStageData(cb.Data, spCbStagePrefix)
		if !ok {
			ackText = "未知关卡"
			text, kb = spiritPanelPush(db, userID)
			break
		}
		text, kb = spiritPanelStageDetail(userID, ch, st)
	case strings.HasPrefix(cb.Data, spCbFightPrefix):
		ch, st, ok := parseSpStageData(cb.Data, spCbFightPrefix)
		if !ok {
			ackText = "未知关卡"
			text, kb = spiritPanelPush(db, userID)
			break
		}
		res, err := PveFight(userID, ch, st)
		if err != nil {
			ackText = fmt.Sprintf("挑战失败：%v", err)
			text, kb = spiritPanelStageDetail(userID, ch, st)
			break
		}
		if res.Win {
			ackText = fmt.Sprintf("⚔️ 胜利！获得 %d 星，+%d 灵晶", res.Stars, res.Reward)
			if res.DroppedEgg != nil {
				ackText += fmt.Sprintf("\n🥚 Boss 掉落 %s 品灵侍蛋（可在灵侍蛋面板孵化）", res.DroppedEgg.Quality)
			}
			for _, it := range res.DroppedItems {
				ackText += fmt.Sprintf("\n🎁 获得 %s ×1（升星可用）", spiritItemNames[it])
			}
		} else {
			ackText = fmt.Sprintf("💀 不敌 %s，稍作休整再战", res.EnemyName)
		}
		text, kb = spiritPanelStageDetail(userID, ch, st)
	case strings.HasPrefix(cb.Data, spCbSweepPrefix):
		ch, st, ok := parseSpStageData(cb.Data, spCbSweepPrefix)
		if !ok {
			ackText = "未知关卡"
			text, kb = spiritPanelPush(db, userID)
			break
		}
		reward, err := PveSweep(userID, ch, st)
		if err != nil {
			ackText = fmt.Sprintf("扫荡失败：%v", err)
		} else {
			ackText = fmt.Sprintf("🔄 扫荡完成，+%d 灵晶", reward)
		}
		text, kb = spiritPanelStageDetail(userID, ch, st)
	case cb.Data == spCbMirror:
		text, kb = spiritPanelMirror(userID)
	case cb.Data == "sp:mirror:set":
		power, err := SetupMirror(userID)
		if err != nil {
			ackText = fmt.Sprintf("上架失败：%v", err)
		} else {
			ackText = fmt.Sprintf("🪞 镜像已上架（战力 %d，24 小时有效）", power)
		}
		text, kb = spiritPanelMirror(userID)
	case cb.Data == "sp:mirror:atk":
		res, err := PvpAttack(userID, 0)
		if err != nil {
			ackText = fmt.Sprintf("攻击失败：%v", err)
			text, kb = spiritPanelMirror(userID)
			break
		}
		if res.Win {
			ackText = fmt.Sprintf("⚔️ 胜！+ %d 灵晶", res.Reward)
		} else {
			ackText = fmt.Sprintf("💀 败。+ %d 灵晶安慰", res.Reward)
		}
		text, kb = pvpAttackResultPanel(res)
	case cb.Data == "sp:mirror:rev":
		text, kb = spiritPanelMirrorRevenge(userID)
	case cb.Data == "sp:mirror:hist":
		text, kb = spiritPanelMirrorHistory(userID)
	case strings.HasPrefix(cb.Data, "sp:mirror:rev:"):
		targetID, err := strconv.ParseInt(strings.TrimPrefix(cb.Data, "sp:mirror:rev:"), 10, 64)
		if err != nil || targetID <= 0 {
			ackText = "无效的复仇目标"
			text, kb = spiritPanelMirror(userID)
			break
		}
		res, err2 := PvpAttack(userID, targetID)
		if err2 != nil {
			ackText = fmt.Sprintf("复仇失败：%v", err2)
			text, kb = spiritPanelMirror(userID)
			break
		}
		if res.Win {
			ackText = fmt.Sprintf("😤 复仇成功！+ %d 灵晶", res.Reward)
		} else {
			ackText = fmt.Sprintf("😤 复仇未果… + %d 灵晶", res.Reward)
		}
		text, kb = pvpAttackResultPanel(res)
	case cb.Data == spCbForge:
		text, kb = spiritPanelForge(db, userID)
	case strings.HasPrefix(cb.Data, "sp:forge:"):
		rest := strings.TrimPrefix(cb.Data, "sp:forge:")
		parts := strings.SplitN(rest, ":", 2)
		if len(parts) != 2 {
			ackText = "无效的锻造指令"
			text, kb = spiritPanelForge(db, userID)
			break
		}
		slotIdx, err1 := strconv.Atoi(parts[0])
		qualityIdx, err2 := strconv.Atoi(parts[1])
		if err1 != nil || err2 != nil {
			ackText = "无效的锻造指令"
			text, kb = spiritPanelForge(db, userID)
			break
		}
		eq, err := ForgeEquipment(userID, slotIdx, qualityIdx)
		if err != nil {
			ackText = fmt.Sprintf("锻造失败：%v", err)
		} else {
			ackText = fmt.Sprintf("🔥 锻造成功：%s", equipmentStatLine(eq))
		}
		text, kb = spiritPanelForge(db, userID)
	case cb.Data == "sp:equip":
		text, kb = spiritPanelEquip(userID)
	case strings.HasPrefix(cb.Data, "sp:equip:"):
		rest := strings.TrimPrefix(cb.Data, "sp:equip:")
		fields := strings.SplitN(rest, ":", 3)
		if len(fields) < 2 {
			ackText = "未知装备指令"
			text, kb = spiritPanelEquip(userID)
			break
		}
		switch fields[0] {
		case "confirm":
			if len(fields) == 3 {
				eqID, err1 := strconv.ParseUint(fields[1], 10, 64)
				sid, err2 := strconv.ParseUint(fields[2], 10, 64)
				if err1 == nil && err2 == nil {
					if err := EquipEquipment(userID, uint(eqID), uint(sid)); err != nil {
						ackText = fmt.Sprintf("穿戴失败：%v", err)
					} else {
						ackText = "⚔️ 已穿戴"
					}
					text, kb = spiritPanelEquip(userID)
					break
				}
			}
			ackText = "无效的穿戴指令"
			text, kb = spiritPanelEquip(userID)
		case "put", "off", "melt":
			id, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				ackText = "无效的装备指令"
				text, kb = spiritPanelEquip(userID)
				break
			}
			switch fields[0] {
			case "put":
				text, kb = spiritPanelEquipPick(userID, uint(id))
			case "off":
				if err := UnequipEquipment(userID, uint(id)); err != nil {
					ackText = fmt.Sprintf("卸下失败：%v", err)
				} else {
					ackText = "已卸下，装备回到仓库"
				}
				text, kb = spiritPanelEquip(userID)
			case "melt":
				refund, err := MeltEquipment(userID, uint(id))
				if err != nil {
					ackText = fmt.Sprintf("熔炼失败：%v", err)
				} else {
					ackText = fmt.Sprintf("♻️ 已熔炼，+ %d 灵晶", refund)
				}
				text, kb = spiritPanelEquip(userID)
			}
		case "enhance":
			id, err := strconv.ParseUint(fields[1], 10, 64)
			if err != nil {
				ackText = "无效的装备指令"
				text, kb = spiritPanelEquip(userID)
				break
			}
			var enhCost int
			err = db.Transaction(func(tx *gorm.DB) error {
				c, e := EnhanceEquipment(tx, userID, uint(id))
				if e == nil {
					enhCost = c
				}
				return e
			})
			if err != nil {
				ackText = fmt.Sprintf("精炼失败：%v", err)
			} else {
				ackText = fmt.Sprintf("🔨 精炼成功：+1（-%d 灵晶），装备属性已提升", enhCost)
			}
			text, kb = spiritPanelEquip(userID)
		default:
			ackText = "未知装备指令"
			text, kb = spiritPanelEquip(userID)
		}
	case cb.Data == spCbEggs:
		text, kb = spiritPanelEggs(userID)
	case strings.HasPrefix(cb.Data, spEggHatchPrefix):
		idStr := strings.TrimPrefix(cb.Data, spEggHatchPrefix)
		eggID, err := strconv.ParseUint(idStr, 10, 64)
		if err != nil {
			ackText = "无效的灵侍蛋指令"
			text, kb = spiritPanelEggs(userID)
			break
		}
		ser, err := HatchEgg(userID, uint(eggID))
		if err != nil {
			ackText = fmt.Sprintf("孵化失败：%v", err)
		} else {
			ackText = fmt.Sprintf("🐣 孵化成功：%s %s品灵侍（Lv.%d）", ser.Name, ser.Quality, ser.Level)
		}
		text, kb = spiritPanelEggs(userID)
	case cb.Data == spCbFeed:
		text, kb = spiritPanelFeed(userID, 1)
	case strings.HasPrefix(cb.Data, spCbFeedPagePrefix):
		page := 0
		fmt.Sscanf(strings.TrimPrefix(cb.Data, spCbFeedPagePrefix), "%d", &page)
		text, kb = spiritPanelFeed(userID, page)
	case strings.HasPrefix(cb.Data, spCbFeedDoPrefix):
		rest := strings.TrimPrefix(cb.Data, spCbFeedDoPrefix)
		parts := strings.SplitN(rest, ":", 2)
		page := 1
		if len(parts) == 2 {
			fmt.Sscanf(parts[1], "%d", &page)
		}
		if page < 1 {
			page = 1
		}
		sid, err := strconv.ParseUint(parts[0], 10, 64)
		if err != nil {
			ackText = "无效的养成指令"
			text, kb = spiritPanelFeed(userID, 1)
			break
		}
		var feedCost int
		err = db.Transaction(func(tx *gorm.DB) error {
			c, e := FeedSpirit(tx, userID, uint(sid), 1)
			if e == nil {
				feedCost = c
			}
			return e
		})
		if err != nil {
			ackText = fmt.Sprintf("喂养失败：%v", err)
		} else {
			ackText = fmt.Sprintf("🌿 喂养成功：等级 +1（-%d 灵晶），属性已提升", feedCost)
		}
		text, kb = spiritPanelFeed(userID, page)
	case strings.HasPrefix(cb.Data, spCbStarUpPrefix):
		rest := strings.TrimPrefix(cb.Data, spCbStarUpPrefix)
		fields := strings.SplitN(rest, ":", 2)
		if len(fields) == 2 && fields[0] == "confirm" {
			// confirm:{target}（灵侍ID 段）——3 种格式：
			// {target}:{sacID}            普通灵侍祭品
			// {target}:item:lingpo        灵魄直接升星
			// {target}:{sacID}:shard      真身碎片 + 祭品
			ids := strings.SplitN(fields[1], ":", 3)
			targetID, err1 := strconv.ParseUint(ids[0], 10, 64)
			var sacIDs []uint
			var useItem string
			valid := false
			if err1 == nil {
				switch len(ids) {
				case 2:
					if v, err2 := strconv.ParseUint(ids[1], 10, 64); err2 == nil {
						sacIDs = []uint{uint(v)}
						valid = true
					}
				case 3:
					if ids[1] == "item" && ids[2] == itemTypeSoul {
						useItem = itemTypeSoul
						valid = true
					} else if v, err2 := strconv.ParseUint(ids[1], 10, 64); err2 == nil && ids[2] == itemTypeShard {
						sacIDs = []uint{uint(v)}
						useItem = itemTypeShard
						valid = true
					}
				}
			}
			if valid {
				err := db.Transaction(func(tx *gorm.DB) error {
					return StarUpgrade(tx, userID, uint(targetID), sacIDs, useItem)
				})
				if err != nil {
					ackText = fmt.Sprintf("升星失败：%v", err)
				} else {
					var t2 UserSpiritServant
					if e2 := db.First(&t2, targetID).Error; e2 == nil {
						ackText = fmt.Sprintf("⭐ 升星成功：%s ⭐%d（等级重置为 1）", t2.Name, t2.Star)
					} else {
						ackText = "⭐ 升星成功"
					}
				}
				text, kb = spiritPanelStarUp(userID, uint(targetID))
				break
			}
			ackText = "无效的升星指令"
			text, kb = spiritPanelList(db, userID, 1)
			break
		}
		if len(fields) == 1 {
			sid, err := strconv.ParseUint(fields[0], 10, 64)
			if err != nil {
				ackText = "无效的升星指令"
				text, kb = spiritPanelList(db, userID, 1)
				break
			}
			text, kb = spiritPanelStarUp(userID, uint(sid))
			break
		}
		ackText = "无效的升星指令"
		text, kb = spiritPanelList(db, userID, 1)
	default:
		ackText = "未知操作"
		text, kb = spiritPanelHome(db, userID)
	}

	// 原地刷新面板
	edit := tgbotapi.NewEditMessageText(chatID, msgID, text)
	edit.ReplyMarkup = &kb
	if _, err := bot.Send(edit); err != nil {
		log.Printf("[灵侍] 面板刷新失败 user=%d cb=%s err=%s", userID, cb.Data, formatTelegramSendError(err))
	}

	// ACK
	ack := tgbotapi.NewCallback(cb.ID, ackText)
	if ackText != "" {
		ack.ShowAlert = true
	}
	if _, err := bot.Request(ack); err != nil {
		log.Printf("[灵侍] ack失败 user=%d err=%s", userID, formatTelegramSendError(err))
	}
	return true
}
