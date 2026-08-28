package main

// ==========================================
// 藏经阁 · 宗门功法（用户侧命令入口）
// 入口：🏯 宗门 → 藏经阁（文本命令「藏经阁」）。
// 动作：升级藏经阁 / 解锁功法 / 观法 / 修习（均私聊）。
// ==========================================

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

// sectManualCodeFromInput 把 黄/玄/地/天 或拼音 code 归一为内部 code。
func sectManualCodeFromInput(raw string) (string, bool) {
	switch strings.TrimSpace(raw) {
	case "黄", "huang":
		return manualCodeYellow, true
	case "玄", "xuan":
		return manualCodeXuan, true
	case "地", "di":
		return manualCodeEarth, true
	case "天", "tian":
		return manualCodeHeaven, true
	default:
		return "", false
	}
}

// handleSectLibrary 藏经阁面板：等级 / 已解锁功法 / 我的修习进度。群聊提示转私聊。
func handleSectLibrary(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.From == nil {
		return
	}
	if !msg.Chat.IsPrivate() {
		replyText(bot, msg.Chat.ID, "📚 藏经阁仅在私聊开放，请私聊我使用。")
		return
	}
	var member SectMember
	if err := DB.Where("user_id = ?", msg.From.ID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, msg.Chat.ID, "❌ 您当前没有加入宗门。")
		} else {
			log.Printf("⚠️ 藏经阁面板成员档案读取失败: user=%d err=%s", msg.From.ID, formatPlainError(err))
			replyText(bot, msg.Chat.ID, "❌ 藏经阁读取失败，请稍后再试。")
		}
		return
	}
	var sect Sect
	if err := DB.Where("id = ?", member.SectID).First(&sect).Error; err != nil {
		log.Printf("⚠️ 藏经阁面板宗门档案读取失败: sect=%d user=%d err=%s", member.SectID, msg.From.ID, formatPlainError(err))
		replyText(bot, msg.Chat.ID, "❌ 藏经阁读取失败，请稍后再试。")
		return
	}

	level, err := getSectLibraryLevelTxChecked(DB, member.SectID)
	if err != nil {
		log.Printf("⚠️ 藏经阁面板等级读取失败: sect=%d user=%d err=%s", member.SectID, msg.From.ID, formatPlainError(err))
		replyText(bot, msg.Chat.ID, "❌ 藏经阁读取失败，请稍后再试。")
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("📚 **%s · 藏经阁**\n\n", escapeMarkdown(sect.Name)))
	b.WriteString(fmt.Sprintf("藏经阁等级：`Lv.%d/%d`\n宗门声望：`%d`\n", level, sectLibraryMaxLevel, sect.Prestige))
	if level < sectLibraryMaxLevel {
		b.WriteString(fmt.Sprintf("下级消耗声望：`%d`（宗主/长老）\n", sectLibraryLevelCosts[level+1]))
	} else {
		b.WriteString("已达最高等级。\n")
	}

	// 已解锁功法
	b.WriteString("\n**【已解锁功法】**\n")
	var unlocks []SectManualUnlock
	if err := DB.Where("sect_id = ?", member.SectID).Order("id asc").Find(&unlocks).Error; err != nil {
		log.Printf("⚠️ 藏经阁面板解锁列表读取失败: sect=%d err=%s", member.SectID, formatPlainError(err))
	}
	unlockedSet := map[string]bool{}
	for i := range unlocks {
		unlockedSet[unlocks[i].ManualCode] = true
	}
	wroteUnlock := false
	for _, cfg := range sectManualCatalog {
		if unlockedSet[cfg.Code] {
			b.WriteString(fmt.Sprintf("· %s（%s品，%d层上限，每层 +%d%%）\n", sectManualName(cfg.Code), cfg.Tier, cfg.MaxDepth, cfg.BonusPerLayer))
			wroteUnlock = true
		}
	}
	if !wroteUnlock {
		b.WriteString("暂无。宗主/长老可解锁功法。\n")
	}

	// 我的灵侍（含编号，供 观法/修习 使用）+ 修习进度
	b.WriteString("\n**【我的灵侍】**\n")
	var servants []UserSpiritServant
	if err := DB.Where("user_id = ?", msg.From.ID).Find(&servants).Error; err != nil {
		log.Printf("⚠️ 藏经阁面板灵侍列表读取失败: user=%d err=%s", msg.From.ID, formatPlainError(err))
	}
	if len(servants) == 0 {
		b.WriteString("尚无灵侍。可先到「灵墟捕捉」收服，再用 `观法` 安排修习。\n")
	}
	// 按战力（含装备+功法）高→低排序（与「灵侍图鉴」同口径，SortServantsByPower 内部按 ID 稳定兜底），
	// 仅展示前 10：灵侍无持有上限，全量列出可能超过 Telegram 单条 4096 字符上限（发送失败仅记日志、用户无感知）。
	if len(servants) > 0 {
		SortServantsByPower(msg.From.ID, servants)
	}
	const servantListLimit = 10
	shown := len(servants)
	if shown > servantListLimit {
		shown = servantListLimit
	}
	for i := 0; i < shown; i++ {
		s := &servants[i]
		st := getServantManualStudy(s.ID)
		if st == nil {
			b.WriteString(fmt.Sprintf("· ID %d %s %s品 · 尚未修习功法\n", s.ID, escapeMarkdown(s.Name), s.Quality))
			continue
		}
		name := sectManualName(st.ManualCode)
		b.WriteString(fmt.Sprintf("· ID %d %s %s品 · 修习：%s %d 层（战力 +%d%%）\n", s.ID, escapeMarkdown(s.Name), s.Quality, name, st.Depth, manualBonusPercent(st.ManualCode, st.Depth)))
	}
	if len(servants) > shown {
		b.WriteString(fmt.Sprintf("…共 %d 只灵侍，其余编号见「灵侍图鉴」。\n", len(servants)))
	}

	b.WriteString("\n发送：`升级藏经阁`、`解锁功法 <黄|玄|地|天>`、`观法 <灵侍编号> <黄|玄|地|天>`、`修习 <灵侍编号> <黄|玄|地|天>`（观法/修习扣个人声望）。")
	replyText(bot, msg.Chat.ID, b.String())
}

// handleUpgradeSectLibraryCmd 升级藏经阁（宗主/长老）。
func handleUpgradeSectLibraryCmd(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.From == nil {
		return
	}
	if !msg.Chat.IsPrivate() {
		replyText(bot, msg.Chat.ID, "📚 升级藏经阁仅在私聊开放，请私聊我使用。")
		return
	}
	oldLv, newLv, err := upgradeSectLibrary(msg.From.ID, getTelegramDisplayName(msg.From))
	if err != nil {
		replySectManualError(bot, msg, err, "升级藏经阁")
		return
	}
	replyText(bot, msg.Chat.ID, fmt.Sprintf("✅ **藏经阁升级成功！**\n等级：Lv.%d -> Lv.%d\n消耗宗门声望：`%d`", oldLv, newLv, sectLibraryLevelCosts[newLv]))
}

// handleUnlockSectManualCmd 解锁功法（宗主/长老）。
func handleUnlockSectManualCmd(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, codeRaw string) {
	if msg == nil || msg.From == nil {
		return
	}
	if !msg.Chat.IsPrivate() {
		replyText(bot, msg.Chat.ID, "📚 解锁功法仅在私聊开放，请私聊我使用。")
		return
	}
	code, ok := sectManualCodeFromInput(codeRaw)
	if !ok {
		replyText(bot, msg.Chat.ID, "❌ 功法参数无效，可用：黄 / 玄 / 地 / 天。\n示例：`解锁功法 黄`")
		return
	}
	if err := unlockSectManual(msg.From.ID, getTelegramDisplayName(msg.From), code); err != nil {
		replySectManualError(bot, msg, err, "解锁功法")
		return
	}
	replyText(bot, msg.Chat.ID, fmt.Sprintf("✅ **功法解锁成功！**\n%s 已对全宗开放（观法扣个人声望）。", sectManualName(code)))
}

// handleStudySectManualCmd 观法入门：指定灵侍开始修习一门功法。
func handleStudySectManualCmd(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, servantIDRaw, codeRaw string) {
	if msg == nil || msg.From == nil {
		return
	}
	if !msg.Chat.IsPrivate() {
		replyText(bot, msg.Chat.ID, "📚 观法仅在私聊开放，请私聊我使用。")
		return
	}
	servantID, err := strconv.ParseUint(strings.TrimSpace(servantIDRaw), 10, 64)
	if err != nil || servantID == 0 {
		replyText(bot, msg.Chat.ID, "❌ 灵侍编号无效。\n示例：`观法 12 黄`")
		return
	}
	code, ok := sectManualCodeFromInput(codeRaw)
	if !ok {
		replyText(bot, msg.Chat.ID, "❌ 功法参数无效，可用：黄 / 玄 / 地 / 天。\n示例：`观法 12 黄`")
		return
	}
	if err := studySectManual(msg.From.ID, uint(servantID), code); err != nil {
		if errors.Is(err, errSectPersonalPrestigeNotEnough) {
			// 个人声望不足：预估贡献兑换并发起二次确认（确认命令：确认观法兑换）
			offerStudyManualPrestigeExchange(bot, msg, uint(servantID), code)
			return
		}
		replySectManualError(bot, msg, err, "观法")
		return
	}
	replyText(bot, msg.Chat.ID, fmt.Sprintf("✅ **观法成功！**\n灵侍已开始修习 %s（第 1 层）。\n继续升层：`修习 %d %s`", sectManualName(code), servantID, sectManualTierLabel(code)))
}

// handleAdvanceSectManualCmd 修习升层：已修习功法的灵侍加深一层。
func handleAdvanceSectManualCmd(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, servantIDRaw, codeRaw string) {
	if msg == nil || msg.From == nil {
		return
	}
	if !msg.Chat.IsPrivate() {
		replyText(bot, msg.Chat.ID, "📚 修习仅在私聊开放，请私聊我使用。")
		return
	}
	servantID, err := strconv.ParseUint(strings.TrimSpace(servantIDRaw), 10, 64)
	if err != nil || servantID == 0 {
		replyText(bot, msg.Chat.ID, "❌ 灵侍编号无效。\n示例：`修习 12 黄`")
		return
	}
	code, ok := sectManualCodeFromInput(codeRaw)
	if !ok {
		replyText(bot, msg.Chat.ID, "❌ 功法参数无效，可用：黄 / 玄 / 地 / 天。\n示例：`修习 12 黄`")
		return
	}
	if err := advanceSectManual(msg.From.ID, uint(servantID), code); err != nil {
		replySectManualError(bot, msg, err, "修习")
		return
	}
	st := getServantManualStudy(uint(servantID))
	depth := 0
	if st != nil {
		depth = st.Depth
	}
	replyText(bot, msg.Chat.ID, fmt.Sprintf("✅ **修习成功！**\n当前第 `%d` 层，战力 +%d%%", depth, manualBonusPercent(code, depth)))
}

// sectManualTierLabel 返回功法品阶的中文标签（用于提示文案）。
func sectManualTierLabel(code string) string {
	switch code {
	case manualCodeYellow:
		return "黄"
	case manualCodeXuan:
		return "玄"
	case manualCodeEarth:
		return "地"
	case manualCodeHeaven:
		return "天"
	default:
		return code
	}
}

// replySectManualError 把功法操作错误映射到用户提示。
func replySectManualError(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, err error, action string) {
	switch sectErrorCode(err) {
	case "NOT_IN_SECT":
		replyText(bot, msg.Chat.ID, "❌ 您当前没有加入宗门。")
	case "ONLY_OWNER":
		replyText(bot, msg.Chat.ID, "❌ 只有宗主或长老可以执行该操作。")
	case "SECT_MANUAL_NOT_FOUND":
		replyText(bot, msg.Chat.ID, "❌ 功法不存在。")
	case "SECT_MANUAL_ALREADY_UNLOCKED":
		replyText(bot, msg.Chat.ID, "❌ 该功法已解锁。")
	case "SECT_MANUAL_NOT_UNLOCKED":
		replyText(bot, msg.Chat.ID, "❌ 该功法尚未解锁，请宗主/长老先解锁。")
	case "SECT_MANUAL_LIBRARY_LEVEL_LOW":
		replyText(bot, msg.Chat.ID, "❌ 藏经阁等级不足，请先升级藏经阁。")
	case "SECT_MANUAL_SERVANT_QUALITY_LOW":
		replyText(bot, msg.Chat.ID, "❌ 该灵侍品阶不足，无法修习此功法。")
	case "SECT_MANUAL_ALREADY_STUDYING":
		replyText(bot, msg.Chat.ID, "❌ 该灵侍已在修习功法（一灵侍一门）。")
	case "SECT_MANUAL_NOT_STUDIED":
		replyText(bot, msg.Chat.ID, "❌ 该灵侍尚未修习该功法，请先 `观法`。")
	case "SECT_MANUAL_MAX_DEPTH":
		replyText(bot, msg.Chat.ID, "❌ 该功法已达最高层。")
	case "SECT_MANUAL_DEPTH_CHANGED":
		replyText(bot, msg.Chat.ID, "⚠️ 修习层数刚刚发生变化，请重新查看藏经阁。")
	case "SECT_MANUAL_SERVANT_NOT_OWNED":
		replyText(bot, msg.Chat.ID, "❌ 该灵侍不属于你。")
	case "PRESTIGE_NOT_ENOUGH":
		replyText(bot, msg.Chat.ID, "❌ 宗门声望不足。")
	case "PERSONAL_PRESTIGE_NOT_ENOUGH":
		replyText(bot, msg.Chat.ID, "❌ 个人声望不足。")
	case "MAX_LEVEL":
		replyText(bot, msg.Chat.ID, "❌ 藏经阁已达最高等级。")
	case "RESOURCE_NOT_ENOUGH":
		// 仅 升级藏经阁 的等级并发冲突会走到这里（等级条件更新 RowsAffected=0），
		// 声望扣减与升级在同一事务内，失败即整体回滚，无资产损失。
		replyText(bot, msg.Chat.ID, "❌ 藏经阁等级刚刚发生变化（并发升级），请重新查看藏经阁。")
	default:
		log.Printf("⚠️ %s失败: user=%d err=%s", action, msg.From.ID, formatPlainError(err))
		replyText(bot, msg.Chat.ID, fmt.Sprintf("❌ %s失败，请稍后再试。", action))
	}
}

// offerStudyManualPrestigeExchange 观法个人声望不足时：预估自动贡献兑换并发起二次确认（确认命令：确认观法兑换）。
// 确认时按实时状态重算差额（多退少不补：期间声望已够则跳过兑换；贡献不够则提示捐献）。
func offerStudyManualPrestigeExchange(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, servantID uint, code string) {
	cfg, ok := sectManualConfigByCode(code)
	if !ok {
		replySectManualError(bot, msg, errSectManualNotFound, "观法")
		return
	}
	var member SectMember
	if err := DB.Where("user_id = ?", msg.From.ID).First(&member).Error; err != nil {
		replyText(bot, msg.Chat.ID, "❌ 宗门档案读取失败，请稍后再试。")
		return
	}
	shortfall := cfg.EntryCost - member.PersonalPrestige
	if shortfall <= 0 {
		// 报错后声望刚好变足（并发变动），直接重试观法
		if err := studySectManual(msg.From.ID, servantID, code); err != nil {
			replySectManualError(bot, msg, err, "观法")
			return
		}
		replyText(bot, msg.Chat.ID, fmt.Sprintf("✅ **观法成功！**\n灵侍已开始修习 %s（第 1 层）。\n继续升层：`修习 %d %s`", sectManualName(code), servantID, sectManualTierLabel(code)))
		return
	}
	requiredContribution := shortfall * sectContributionToPrestigeCost
	if member.Contribution < requiredContribution {
		replyText(bot, msg.Chat.ID, fmt.Sprintf(
			"❌ 个人声望不足：观法 %s 需要 `%d`，你当前 `%d`（差 `%d`）。\n自动兑换需 `%d` 贡献，你当前只有 `%d` 贡献。\n请先 `捐献宗门` 获取贡献，或手动兑换：`贡献换声望 %d`。",
			sectManualName(code), cfg.EntryCost, member.PersonalPrestige, shortfall, requiredContribution, member.Contribution, shortfall))
		return
	}
	session := getSession(msg.From.ID)
	session.SetTemp("study_manual_servant_id", strconv.FormatUint(uint64(servantID), 10))
	session.SetTemp("study_manual_code", code)
	session.SetStep("WAITING_STUDY_MANUAL_EXCHANGE")
	UserSessions.Store(msg.From.ID, session)
	replyText(bot, msg.Chat.ID, fmt.Sprintf(
		"❌ 个人声望不足：观法 %s 需要 `%d`，你当前 `%d`（差 `%d`）。\n可自动兑换 `%d` 贡献（个人声望 +%d、宗门声望 +%d）补足。\n当前贡献：`%d`（足够）\n确认兑换请发送：`确认观法兑换`",
		sectManualName(code), cfg.EntryCost, member.PersonalPrestige, shortfall, requiredContribution, shortfall, shortfall, member.Contribution))
}

// handleConfirmStudyManualExchange 二次确认：自动兑换补足个人声望并执行观法。
// 确认时按实时状态重算：声望已足够则跳过兑换直接观法；贡献不足则提示捐献后重试。
func handleConfirmStudyManualExchange(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.From == nil {
		return
	}
	if !msg.Chat.IsPrivate() {
		replyText(bot, msg.Chat.ID, "📚 确认观法兑换仅在私聊开放，请私聊我使用。")
		return
	}
	userID := msg.From.ID
	session := getSession(userID)
	servantIDRaw := session.GetTemp("study_manual_servant_id")
	code := session.GetTemp("study_manual_code")
	if servantIDRaw == "" || code == "" {
		replyText(bot, msg.Chat.ID, "❌ 没有待确认的观法兑换（可能已过期或已完成）。\n请重新发送：`观法 <灵侍编号> <黄|玄|地|天>`")
		return
	}
	servantID, err := strconv.ParseUint(servantIDRaw, 10, 64)
	if err != nil || servantID == 0 {
		clearSession(userID)
		replyText(bot, msg.Chat.ID, "❌ 观法兑换记录已失效，请重新发送：`观法 <灵侍编号> <黄|玄|地|天>`")
		return
	}
	cfg, ok := sectManualConfigByCode(code)
	if !ok {
		clearSession(userID)
		replySectManualError(bot, msg, errSectManualNotFound, "观法")
		return
	}
	var member SectMember
	if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			clearSession(userID)
			replySectManualError(bot, msg, errNotInSect, "观法")
			return
		}
		log.Printf("⚠️ 观法兑换确认成员读取失败: user=%d err=%s", userID, formatPlainError(err))
		replyText(bot, msg.Chat.ID, "❌ 宗门档案读取失败，请稍后再试。")
		return
	}

	// 个人声望已足够（期间有变动）：跳过兑换，直接观法
	if member.PersonalPrestige >= cfg.EntryCost {
		clearSession(userID)
		if err := studySectManual(userID, uint(servantID), code); err != nil {
			replySectManualError(bot, msg, err, "观法")
			return
		}
		replyText(bot, msg.Chat.ID, fmt.Sprintf("✅ **观法成功！**\n个人声望已足够（当前 `%d`），未发生兑换。\n灵侍已开始修习 %s（第 1 层）。\n继续升层：`修习 %d %s`", member.PersonalPrestige, sectManualName(code), servantID, sectManualTierLabel(code)))
		return
	}
	shortfall := cfg.EntryCost - member.PersonalPrestige
	requiredContribution := shortfall * sectContributionToPrestigeCost
	if member.Contribution < requiredContribution {
		clearSession(userID)
		replyText(bot, msg.Chat.ID, fmt.Sprintf(
			"❌ 贡献不足：自动兑换需 `%d` 贡献，你当前只有 `%d`。\n请先 `捐献宗门` 获取贡献，再发送 `观法 %d %s` 重试。",
			requiredContribution, member.Contribution, servantID, cfg.Tier))
		return
	}

	_, _, personalPrestigeAfter, err := exchangeSectContributionForPrestige(userID, shortfall)
	if err != nil {
		switch sectErrorCode(err) {
		case "CONTRIBUTION_NOT_ENOUGH":
			clearSession(userID)
			replyText(bot, msg.Chat.ID, fmt.Sprintf("❌ 贡献不足（确认期间有变动）：本次兑换需 `%d` 贡献。\n请重新发送 `观法 %d %s` 重试。", requiredContribution, servantID, cfg.Tier))
		case "NOT_IN_SECT":
			clearSession(userID)
			replySectManualError(bot, msg, errNotInSect, "观法")
		default:
			log.Printf("⚠️ 观法自动兑换失败: user=%d reward=%d err=%s", userID, shortfall, formatPlainError(err))
			replyText(bot, msg.Chat.ID, "❌ 自动兑换失败，请稍后再试（未发生扣减）。")
		}
		return
	}

	if err := studySectManual(userID, uint(servantID), code); err != nil {
		// 兑换已成功（资产不回收）；观法失败如实提示（如期间灵侍开始修习他法）
		clearSession(userID)
		replyText(bot, msg.Chat.ID, fmt.Sprintf(
			"✅ 兑换成功：消耗 `%d` 贡献，个人声望 +%d（当前 `%d`）、宗门声望 +%d。",
			shortfall*sectContributionToPrestigeCost, shortfall, personalPrestigeAfter, shortfall))
		replySectManualError(bot, msg, err, "观法")
		return
	}
	clearSession(userID)
	replyText(bot, msg.Chat.ID, fmt.Sprintf(
		"✅ **观法成功！**\n自动兑换：消耗 `%d` 贡献，个人声望 +%d（当前 `%d`）、宗门声望 +%d。\n灵侍已开始修习 %s（第 1 层）。\n继续升层：`修习 %d %s`",
		shortfall*sectContributionToPrestigeCost, shortfall, personalPrestigeAfter, shortfall, sectManualName(code), servantID, sectManualTierLabel(code)))
}
