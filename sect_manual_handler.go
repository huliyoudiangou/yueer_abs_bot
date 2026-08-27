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

	// 我的修习进度
	b.WriteString("\n**【我的灵侍功法】**\n")
	var servants []UserSpiritServant
	if err := DB.Where("user_id = ?", msg.From.ID).Order("id asc").Find(&servants).Error; err != nil {
		log.Printf("⚠️ 藏经阁面板灵侍列表读取失败: user=%d err=%s", msg.From.ID, formatPlainError(err))
	}
	wroteStudy := false
	for i := range servants {
		s := &servants[i]
		st := getServantManualStudy(s.ID)
		if st == nil {
			continue
		}
		name := sectManualName(st.ManualCode)
		b.WriteString(fmt.Sprintf("· ID %d %s %s品 · 修习：%s %d 层（战力 +%d%%）\n", s.ID, escapeMarkdown(s.Name), s.Quality, name, st.Depth, manualBonusPercent(st.ManualCode, st.Depth)))
		wroteStudy = true
	}
	if !wroteStudy {
		b.WriteString("尚无灵侍修习功法。\n")
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
	default:
		log.Printf("⚠️ %s失败: user=%d err=%s", action, msg.From.ID, formatPlainError(err))
		replyText(bot, msg.Chat.ID, fmt.Sprintf("❌ %s失败，请稍后再试。", action))
	}
}
