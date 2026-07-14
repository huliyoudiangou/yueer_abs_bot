package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

func HandleCultivationAdminWriteCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, text string) bool {
	text = strings.TrimSpace(text)

	if message == nil || message.From == nil {
		return isCultivationAdminWriteCommand(text)
	}

	session := getSession(message.From.ID)
	step := session.GetStep()
	pendingWrite := step == "WAITING_CULTIVATION_ADMIN_WRITE_REASON" || step == "WAITING_CONFIRM_CULTIVATION_ADMIN_WRITE"
	if !pendingWrite && !isCultivationAdminWriteCommand(text) {
		return false
	}

	registerIncomingGroupCommandForAutoDelete(message)

	if !isSuperAdmin(message.From.ID) {
		sendPlainText(bot, message.Chat.ID, "权限不足：修仙配置写入指令仅限超级管理员。")
		if pendingWrite {
			clearSession(message.From.ID)
		}
		return true
	}

	if !message.Chat.IsPrivate() {
		sendPlainText(bot, message.Chat.ID, "修仙配置写入属于高危操作，请私聊机器人执行，群内不会生效。")
		if pendingWrite {
			clearSession(message.From.ID)
		}
		return true
	}

	if pendingWrite {
		return handleCultivationAdminWriteSession(bot, message, text, session)
	}

	normalizedText := normalizeCultivationAdminWriteCommand(text)
	if normalizedText == "" {
		sendPlainText(bot, message.Chat.ID, "指令格式错误。")
		return true
	}

	session.SetTemp("cultivation_admin_write_command", normalizedText)
	session.SetStep("WAITING_CULTIVATION_ADMIN_WRITE_REASON")
	UserSessions.Store(message.From.ID, session)

	sendPlainText(
		bot,
		message.Chat.ID,
		fmt.Sprintf(
			"高危操作：该指令会修改全服修仙配置。\n\n待执行指令：\n%s\n\n请输入本次变更原因，"+adminReasonRequirementText+"：",
			formatPlainValue(normalizedText),
		),
	)
	return true
}

func handleCultivationAdminWriteSession(bot *tgbotapi.BotAPI, message *tgbotapi.Message, text string, session *SessionState) bool {
	switch session.GetStep() {
	case "WAITING_CULTIVATION_ADMIN_WRITE_REASON":
		if text == "取消" {
			sendPlainText(bot, message.Chat.ID, "已取消修仙配置写入。")
			clearSession(message.From.ID)
			return true
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			sendPlainText(bot, message.Chat.ID, adminReasonInvalidText)
			return true
		}

		pendingCommand := session.GetTemp("cultivation_admin_write_command")
		if pendingCommand == "" || !isCultivationAdminWriteCommand(pendingCommand) {
			sendPlainText(bot, message.Chat.ID, "修仙配置写入会话状态异常，已中止。请重新发起流程。")
			clearSession(message.From.ID)
			return true
		}

		session.SetTemp("cultivation_admin_write_reason", reason)
		session.SetStep("WAITING_CONFIRM_CULTIVATION_ADMIN_WRITE")
		UserSessions.Store(message.From.ID, session)

		sendPlainText(
			bot,
			message.Chat.ID,
			fmt.Sprintf(
				"高危操作二次确认：该指令会修改全服修仙配置。\n\n待执行指令：\n%s\n\n原因：%s\n\n确认执行请回复：确认执行修仙配置\n取消请回复：取消",
				formatPlainValue(pendingCommand),
				formatPlainValue(reason),
			),
		)
		return true

	case "WAITING_CONFIRM_CULTIVATION_ADMIN_WRITE":
		if text != "确认执行修仙配置" {
			sendPlainText(bot, message.Chat.ID, "已取消修仙配置写入。")
			clearSession(message.From.ID)
			return true
		}

		pendingCommand := session.GetTemp("cultivation_admin_write_command")
		reason, ok := validateAdminReason(session.GetTemp("cultivation_admin_write_reason"))
		if pendingCommand == "" || !isCultivationAdminWriteCommand(pendingCommand) || !ok {
			sendPlainText(bot, message.Chat.ID, "修仙配置写入会话状态异常，已中止。请重新发起流程。")
			clearSession(message.From.ID)
			return true
		}

		reply, err := executeCultivationAdminWriteCommand(message.From.ID, pendingCommand, reason)
		if err != nil {
			sendPlainText(bot, message.Chat.ID, formatPlainError(err))
			clearSession(message.From.ID)
			return true
		}

		sendPlainText(bot, message.Chat.ID, reply)
		clearSession(message.From.ID)
		return true
	}

	clearSession(message.From.ID)
	sendPlainText(bot, message.Chat.ID, "修仙配置写入会话状态异常，已中止。请重新发起流程。")
	return true
}

func normalizeCultivationAdminWriteCommand(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "确认") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "确认"))
	}
	return text
}

func isCultivationAdminWriteCommand(text string) bool {
	text = normalizeCultivationAdminWriteCommand(text)
	return text == "重载修仙配置" ||
		strings.HasPrefix(text, "设置突破成功率 ") ||
		strings.HasPrefix(text, "设置突破消耗 ") ||
		strings.HasPrefix(text, "设置突破冷却 ") ||
		strings.HasPrefix(text, "设置突破最低修为 ") ||
		strings.HasPrefix(text, "设置境界门槛 ") ||
		strings.HasPrefix(text, "设置小境界门槛 ")
}

func executeCultivationAdminWriteCommand(actorID int64, text string, reason string) (string, error) {
	text = strings.TrimSpace(text)

	if text == "重载修仙配置" {
		if err := ReloadCultivationRules(); err != nil {
			return "", fmt.Errorf("重载修仙配置失败：%w", err)
		}

		if err := writeAuditLogInTx(DB, actorID, "RELOAD_CULTIVATION_RULES", "cultivation_rules", 0, fmt.Sprintf("超级管理员手动重载修仙配置缓存，原因：%s", formatPlainValue(reason))); err != nil {
			return "", fmt.Errorf("写入修仙配置重载审计失败：%w", err)
		}
		return "修仙配置缓存已重载。", nil
	}

	parts := strings.Fields(text)
	if len(parts) == 0 {
		return "", fmt.Errorf("指令格式错误。")
	}

	switch parts[0] {
	case "设置突破成功率":
		if len(parts) != 3 {
			return "", fmt.Errorf("用法：设置突破成功率 大境界 成功率百分比\n例如：设置突破成功率 1 80")
		}

		fromMajor, err := parseNonNegativeInt(parts[1], "大境界")
		if err != nil {
			return "", err
		}

		percent, err := parseFloatRange(parts[2], "成功率百分比", 0, 100)
		if err != nil {
			return "", err
		}

		successRate := percent / 100
		if err := validateBreakthroughSuccessRateForProduction(fromMajor, successRate); err != nil {
			return "", err
		}

		return updateBreakthroughConfigField(
			actorID,
			fromMajor,
			"success_rate",
			successRate,
			fmt.Sprintf("突破成功率调整为 %.0f%%", percent),
			reason,
		)

	case "设置突破消耗":
		if len(parts) != 3 {
			return "", fmt.Errorf("用法：设置突破消耗 大境界 积分\n例如：设置突破消耗 1 100")
		}

		fromMajor, err := parseNonNegativeInt(parts[1], "大境界")
		if err != nil {
			return "", err
		}

		pointsCost, err := parseNonNegativeInt(parts[2], "积分消耗")
		if err != nil {
			return "", err
		}
		if err := validateBreakthroughPointsCostForProduction(fromMajor, pointsCost); err != nil {
			return "", err
		}

		return updateBreakthroughConfigField(
			actorID,
			fromMajor,
			"points_cost",
			pointsCost,
			fmt.Sprintf("突破积分消耗调整为 %d", pointsCost),
			reason,
		)

	case "设置突破冷却":
		if len(parts) != 3 {
			return "", fmt.Errorf("用法：设置突破冷却 大境界 小时\n例如：设置突破冷却 1 24")
		}

		fromMajor, err := parseNonNegativeInt(parts[1], "大境界")
		if err != nil {
			return "", err
		}

		cooldownHours, err := parseNonNegativeInt(parts[2], "冷却小时")
		if err != nil {
			return "", err
		}
		if err := validateBreakthroughCooldownForProduction(fromMajor, cooldownHours); err != nil {
			return "", err
		}

		return updateBreakthroughConfigField(
			actorID,
			fromMajor,
			"cooldown_hours",
			cooldownHours,
			fmt.Sprintf("突破冷却调整为 %d 小时", cooldownHours),
			reason,
		)

	case "设置突破最低修为":
		if len(parts) != 3 {
			return "", fmt.Errorf("用法：设置突破最低修为 大境界 小时\n例如：设置突破最低修为 1 50")
		}

		fromMajor, err := parseNonNegativeInt(parts[1], "大境界")
		if err != nil {
			return "", err
		}

		minHours, err := parseNonNegativeFloat(parts[2], "最低修为小时")
		if err != nil {
			return "", err
		}
		if err := validateBreakthroughMinHoursForProduction(fromMajor, minHours); err != nil {
			return "", err
		}

		return updateBreakthroughConfigField(
			actorID,
			fromMajor,
			"min_total_hours",
			minHours,
			fmt.Sprintf("突破最低修为调整为 %.1f 小时", minHours),
			reason,
		)

	case "设置境界门槛":
		if len(parts) != 4 {
			return "", fmt.Errorf("用法：设置境界门槛 大境界 最低小时 最高小时\n最高小时填 0 表示无上限\n例如：设置境界门槛 1 10 50")
		}

		major, err := parseNonNegativeInt(parts[1], "大境界")
		if err != nil {
			return "", err
		}

		minHours, err := parseNonNegativeFloat(parts[2], "最低小时")
		if err != nil {
			return "", err
		}

		maxHours, err := parseNonNegativeFloat(parts[3], "最高小时")
		if err != nil {
			return "", err
		}

		return updateRealmThreshold(actorID, major, minHours, maxHours, reason)

	case "设置小境界门槛":
		if len(parts) != 4 {
			return "", fmt.Errorf("用法：设置小境界门槛 大境界 小境界 所需小时\n例如：设置小境界门槛 1 2 20")
		}

		major, err := parseNonNegativeInt(parts[1], "大境界")
		if err != nil {
			return "", err
		}

		minor, err := parseNonNegativeInt(parts[2], "小境界")
		if err != nil {
			return "", err
		}

		requiredHours, err := parseNonNegativeFloat(parts[3], "所需小时")
		if err != nil {
			return "", err
		}

		return updateMinorRealmThreshold(actorID, major, minor, requiredHours, reason)
	}

	return "", fmt.Errorf("未知修仙配置写入指令。")
}

func updateBreakthroughConfigField(actorID int64, fromMajor int, field string, value interface{}, detail string, reason string) (string, error) {
	allowedFields := map[string]bool{
		"success_rate":    true,
		"points_cost":     true,
		"cooldown_hours":  true,
		"min_total_hours": true,
	}
	if !allowedFields[field] {
		return "", fmt.Errorf("不允许更新该突破配置字段：%s", field)
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		var cfg BreakthroughConfig
		if err := tx.Where("from_major_realm = ?", fromMajor).First(&cfg).Error; err != nil {
			return fmt.Errorf("未找到突破配置：from_major_realm=%d", fromMajor)
		}

		res := tx.Model(&BreakthroughConfig{}).
			Where("id = ? AND from_major_realm = ?", cfg.ID, fromMajor).
			Update(field, value)
		if res.Error != nil {
			return fmt.Errorf("更新突破配置失败：%w", res.Error)
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("突破配置更新未命中，请重试")
		}

		rules, err := loadCultivationRulesFromDBHandle(tx)
		if err != nil {
			return fmt.Errorf("重新加载修仙配置失败：%w", err)
		}

		if err := validateCultivationRuleSet(rules); err != nil {
			return fmt.Errorf("配置校验失败，已回滚本次修改：%w", err)
		}

		if err := writeAuditLogInTx(
			tx,
			actorID,
			"UPDATE_BREAKTHROUGH_CONFIG",
			fmt.Sprintf("from_major_realm=%d field=%s", fromMajor, field),
			0,
			fmt.Sprintf("%s，原因：%s", detail, formatPlainValue(reason)),
		); err != nil {
			return fmt.Errorf("写入修仙配置审计失败：%w", err)
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	if err := ReloadCultivationRules(); err != nil {
		return "", fmt.Errorf("数据库已更新，但刷新修仙配置缓存失败：%w", err)
	}

	return fmt.Sprintf("已更新突破配置：from_major_realm=%d，%s。", fromMajor, detail), nil
}

func updateRealmThreshold(actorID int64, major int, minHours float64, maxHours float64, reason string) (string, error) {
	if maxHours > 0 && maxHours < minHours {
		return "", fmt.Errorf("境界最高小时不能小于最低小时。")
	}

	var realmName string

	err := DB.Transaction(func(tx *gorm.DB) error {
		var realm CultivationRealmConfig
		if err := tx.Where("major_realm = ?", major).First(&realm).Error; err != nil {
			return fmt.Errorf("未找到境界配置：major_realm=%d", major)
		}

		if realm.IsMaxRealm && maxHours > 0 {
			return fmt.Errorf("当前最高境界建议保持最高小时为 0，表示无上限。")
		}

		realmName = realm.Name

		res := tx.Model(&CultivationRealmConfig{}).
			Where("id = ? AND major_realm = ?", realm.ID, major).
			Updates(map[string]interface{}{
				"min_total_hours": minHours,
				"max_total_hours": maxHours,
			})
		if res.Error != nil {
			return fmt.Errorf("更新境界门槛失败：%w", res.Error)
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("境界门槛更新未命中，请重试")
		}

		rules, err := loadCultivationRulesFromDBHandle(tx)
		if err != nil {
			return fmt.Errorf("重新加载修仙配置失败：%w", err)
		}

		if err := validateCultivationRuleSet(rules); err != nil {
			return fmt.Errorf("配置校验失败，已回滚本次修改：%w", err)
		}

		detail := fmt.Sprintf("境界 %s(%d) 门槛调整为 %.1f - %.1f 小时", formatPlainValue(realmName), major, minHours, maxHours)
		if err := writeAuditLogInTx(tx, actorID, "UPDATE_REALM_THRESHOLD", fmt.Sprintf("major_realm=%d", major), 0, fmt.Sprintf("%s，原因：%s", detail, formatPlainValue(reason))); err != nil {
			return fmt.Errorf("写入修仙配置审计失败：%w", err)
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	if err := ReloadCultivationRules(); err != nil {
		return "", fmt.Errorf("数据库已更新，但刷新修仙配置缓存失败：%w", err)
	}

	return fmt.Sprintf("已更新境界门槛：%s，最低 %.1f 小时，最高 %.1f 小时。", formatPlainValue(realmName), minHours, maxHours), nil
}

func updateMinorRealmThreshold(actorID int64, major int, minor int, requiredHours float64, reason string) (string, error) {
	if minor < 1 || minor > 4 {
		return "", fmt.Errorf("小境界只能是 1-4。")
	}

	var minorName string

	err := DB.Transaction(func(tx *gorm.DB) error {
		var cfg CultivationMinorRealmConfig
		if err := tx.Where("major_realm = ? AND minor_realm = ?", major, minor).First(&cfg).Error; err != nil {
			return fmt.Errorf("未找到小境界配置：major_realm=%d minor_realm=%d", major, minor)
		}

		minorName = cfg.Name

		res := tx.Model(&CultivationMinorRealmConfig{}).
			Where("id = ? AND major_realm = ? AND minor_realm = ?", cfg.ID, major, minor).
			Update("required_hours", requiredHours)
		if res.Error != nil {
			return fmt.Errorf("更新小境界门槛失败：%w", res.Error)
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("小境界门槛更新未命中，请重试")
		}

		rules, err := loadCultivationRulesFromDBHandle(tx)
		if err != nil {
			return fmt.Errorf("重新加载修仙配置失败：%w", err)
		}

		if err := validateCultivationRuleSet(rules); err != nil {
			return fmt.Errorf("配置校验失败，已回滚本次修改：%w", err)
		}

		detail := fmt.Sprintf("小境界 %d-%d(%s) 门槛调整为 %.1f 小时", major, minor, formatPlainValue(minorName), requiredHours)
		if err := writeAuditLogInTx(tx, actorID, "UPDATE_MINOR_REALM_THRESHOLD", fmt.Sprintf("major_realm=%d minor_realm=%d", major, minor), 0, fmt.Sprintf("%s，原因：%s", detail, formatPlainValue(reason))); err != nil {
			return fmt.Errorf("写入修仙配置审计失败：%w", err)
		}

		return nil
	})

	if err != nil {
		return "", err
	}

	if err := ReloadCultivationRules(); err != nil {
		return "", fmt.Errorf("数据库已更新，但刷新修仙配置缓存失败：%w", err)
	}

	return fmt.Sprintf("已更新小境界门槛：%d-%d %s，所需 %.1f 小时。", major, minor, formatPlainValue(minorName), requiredHours), nil
}

func parseNonNegativeInt(raw string, fieldName string) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil || value < 0 {
		return 0, fmt.Errorf("%s必须是非负整数。", fieldName)
	}
	return value, nil
}

func parseNonNegativeFloat(raw string, fieldName string) (float64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, fmt.Errorf("%s必须是非负有限数字。", fieldName)
	}
	return value, nil
}

func parseFloatRange(raw string, fieldName string, min float64, max float64) (float64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < min || value > max {
		return 0, fmt.Errorf("%s必须在 %.0f 到 %.0f 之间，且必须是有限数字。", fieldName, min, max)
	}
	return value, nil
}

func validateBreakthroughSuccessRateForProduction(fromMajor int, successRate float64) error {
	if fromMajor <= 0 {
		return nil
	}

	if successRate <= 0 {
		return fmt.Errorf("生产保护：大境界突破成功率不能设置为 0%%。")
	}

	if successRate > 0.95 {
		return fmt.Errorf("生产保护：大境界突破成功率不能超过 95%%，避免误操作导致全服无成本晋升。")
	}

	return nil
}

func validateBreakthroughPointsCostForProduction(fromMajor int, pointsCost int) error {
	if fromMajor <= 0 {
		return nil
	}

	if pointsCost <= 0 {
		return fmt.Errorf("生产保护：非凡人突破消耗不能设置为 0。")
	}

	return nil
}

func validateBreakthroughCooldownForProduction(fromMajor int, cooldownHours int) error {
	if fromMajor <= 0 {
		return nil
	}

	if cooldownHours < 1 {
		return fmt.Errorf("生产保护：非凡人突破冷却不能低于 1 小时。")
	}

	return nil
}

func validateBreakthroughMinHoursForProduction(fromMajor int, minHours float64) error {
	if fromMajor <= 0 {
		return nil
	}

	if minHours <= 0 {
		return fmt.Errorf("生产保护：非凡人突破最低修为必须大于 0 小时。")
	}

	return nil
}
