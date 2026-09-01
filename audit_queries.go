package main

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func showPointTransactions(bot *tgbotapi.BotAPI, chatID int64, requesterID int64, targetID int64, days int) {
	role := getUserRole(requesterID)

	if requesterID != targetID {
		if role != "admin" && role != "super_admin" {
			replyText(bot, chatID, "❌ 你只能查询自己的积分流水。")
			return
		}
	}

	maxDays := 1
	limit := 30

	if role == "admin" {
		maxDays = 7
		limit = 100
	} else if role == "super_admin" {
		maxDays = 30
		limit = 100
	}

	if requesterID == targetID && role != "admin" && role != "super_admin" {
		days = 1
	}

	if days <= 0 {
		days = 1
	}
	if days > maxDays {
		replyText(bot, chatID, fmt.Sprintf("❌ 当前权限最多只能查询最近 %d 天积分流水。", maxDays))
		return
	}

	start := time.Now().AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())

	var logs []PointTransaction
	if err := DB.Where("user_id = ? AND created_at >= ?", targetID, start).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error; err != nil {
		replyText(bot, chatID, "❌ 查询积分流水失败，请稍后再试。")
		return
	}

	if len(logs) == 0 {
		if requesterID == targetID {
			replyText(bot, chatID, "📒 你今天还没有积分流水。")
		} else {
			replyText(bot, chatID, "📒 该用户在查询范围内没有积分流水。")
		}
		return
	}

	title := "📒 我的今日积分流水"
	if requesterID != targetID {
		title = fmt.Sprintf("📒 用户 `%d` 最近 %d 天积分流水", targetID, days)
	} else if days > 1 {
		title = fmt.Sprintf("📒 我的最近 %d 天积分流水", days)
	}

	var builder strings.Builder
	builder.WriteString(title)
	builder.WriteString("\n\n")

	for _, item := range logs {
		sign := "+"
		if item.Delta < 0 {
			sign = ""
		}

		builder.WriteString(fmt.Sprintf(
			"%s  %s%d  %s\n余额：%d → %d\n%s\n\n",
			item.CreatedAt.Format("01-02 15:04"),
			sign,
			item.Delta,
			pointTransactionTypeMarkdown(item.Type),
			item.BalanceBefore,
			item.BalanceAfter,
			pointTransactionDescriptionMarkdown(item.Description),
		))
	}

	if len(logs) == limit {
		builder.WriteString(fmt.Sprintf("仅显示最近 %d 条。", limit))
	}

	replyText(bot, chatID, builder.String())
}

func handlePointTransactionQuery(bot *tgbotapi.BotAPI, chatID int64, requesterID int64, text string) {
	text = strings.TrimSpace(text)

	if text == "我的流水" || text == "积分流水" || text == "📒 我的流水" {
		showPointTransactions(bot, chatID, requesterID, requesterID, 1)
		return
	}

	role := getUserRole(requesterID)
	if role != "admin" && role != "super_admin" {
		replyText(bot, chatID, "❌ 权限不足。")
		return
	}

	parts := strings.Fields(text)
	if len(parts) < 2 {
		replyText(bot, chatID, "用法：查流水 用户ID [天数]\n例如：查流水 123456789 7")
		return
	}

	targetID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || targetID <= 0 {
		replyText(bot, chatID, "❌ 用户ID格式错误，请输入纯数字 Telegram ID。")
		return
	}

	days := 1
	if len(parts) >= 3 {
		rawDays := strings.TrimSuffix(parts[2], "天")
		parsedDays, err := strconv.Atoi(rawDays)
		if err != nil || parsedDays <= 0 {
			replyText(bot, chatID, "❌ 天数格式错误，例如：查流水 123456789 7")
			return
		}
		days = parsedDays
	}

	showPointTransactions(bot, chatID, requesterID, targetID, days)
}

func handleAuditLogQuery(bot *tgbotapi.BotAPI, chatID int64, requesterID int64, text string) {
	if getUserRole(requesterID) != "super_admin" {
		replyText(bot, chatID, "❌ 审计日志仅限超级管理员查看。")
		return
	}

	filter := ""
	days := 1
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) >= 2 {
		if parsedDays, ok := parseAuditQueryDays(parts[1]); ok {
			days = parsedDays
		} else {
			filter = strings.TrimSpace(parts[1])
		}
	}
	if len(parts) >= 3 {
		if parsedDays, ok := parseAuditQueryDays(parts[2]); ok {
			days = parsedDays
		} else if filter == "" {
			filter = strings.TrimSpace(parts[2])
		}
	}

	if days <= 0 {
		days = 1
	}
	if days > 30 {
		replyText(bot, chatID, "❌ 审计日志最多查询最近 30 天。")
		return
	}

	start := time.Now().AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())

	query := DB.Where("created_at >= ?", start)
	filterText := "全部"
	if filter != "" {
		if actorID, err := strconv.ParseInt(filter, 10, 64); err == nil {
			query = query.Where("actor_id = ? OR target = ?", actorID, filter)
			filterText = fmt.Sprintf("用户/目标 %d", actorID)
		} else {
			action := strings.ToUpper(filter)
			query = query.Where("action = ?", action)
			filterText = action
		}
	}

	const limit = 20
	var logs []AuditLog
	if err := query.Order("created_at DESC").Limit(limit).Find(&logs).Error; err != nil {
		replyText(bot, chatID, "❌ 查询审计日志失败，请稍后再试。")
		return
	}

	if len(logs) == 0 {
		replyText(bot, chatID, "📋 查询范围内没有审计日志。")
		writeAuditLog(requesterID, "VIEW_AUDIT_LOGS", filterText, fmt.Sprintf("查看审计日志无结果，范围=%d天", days))
		return
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("📋 **审计日志**\n范围：最近 `%d` 天\n过滤：`%s`\n\n", days, escapeMarkdown(filterText)))
	for _, item := range logs {
		deltaText := ""
		if item.Delta != 0 {
			deltaText = fmt.Sprintf(" Δ`%+d`", item.Delta)
		}
		detail := truncateRunes(strings.TrimSpace(formatAuditTextForDisplay(item.Detail)), 120)
		if detail == "" {
			detail = "无详情"
		}
		target := truncateRunes(strings.TrimSpace(formatAuditTextForDisplay(item.Target)), 80)
		builder.WriteString(fmt.Sprintf(
			"%s  `%s`%s\n操作者：`%d` / `%s`\n目标：`%s`\n%s\n\n",
			item.CreatedAt.Format("01-02 15:04"),
			escapeMarkdown(item.Action),
			deltaText,
			item.ActorID,
			escapeMarkdown(item.ActorRole),
			escapeMarkdown(target),
			escapeMarkdown(detail),
		))
	}
	if len(logs) == limit {
		builder.WriteString(fmt.Sprintf("仅显示最近 `%d` 条。", limit))
	}

	replyText(bot, chatID, builder.String())
	writeAuditLog(requesterID, "VIEW_AUDIT_LOGS", filterText, fmt.Sprintf("查看审计日志，范围=%d天，返回=%d条", days, len(logs)))
}

func handleAuditSummaryQuery(bot *tgbotapi.BotAPI, chatID int64, requesterID int64, text string) {
	if getUserRole(requesterID) != "super_admin" {
		replyText(bot, chatID, "❌ 审计概览仅限超级管理员查看。")
		return
	}

	days := 1
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) >= 2 {
		if parsedDays, ok := parseAuditQueryDays(parts[1]); ok {
			days = parsedDays
		}
	}
	if days <= 0 {
		days = 1
	}
	if days > 30 {
		replyText(bot, chatID, "❌ 审计概览最多统计最近 30 天。")
		return
	}

	start := time.Now().AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())

	total, failed, highRisk, topActions, topActors, err := loadAuditSummary(start)
	if err != nil {
		replyText(bot, chatID, "❌ 统计审计概览失败，请稍后再试。")
		return
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("📊 **审计概览**\n范围：最近 `%d` 天\n\n", days))
	builder.WriteString(fmt.Sprintf("总记录：`%d`\n失败/异常：`%d`\n高危操作：`%d`\n\n", total, failed, highRisk))
	builder.WriteString("Top 操作类型：\n")
	appendAuditSummaryRows(&builder, topActions, "action")
	builder.WriteString("\nTop 操作者：\n")
	appendAuditSummaryRows(&builder, topActors, "actor")

	replyText(bot, chatID, builder.String())
	writeAuditLog(requesterID, "VIEW_AUDIT_SUMMARY", "audit_logs", fmt.Sprintf("查看审计概览，范围=%d天，总记录=%d，失败=%d，高危=%d", days, total, failed, highRisk))
}

type auditSummaryRow struct {
	Label string
	Count int64
}

func loadAuditSummary(start time.Time) (int64, int64, int64, []auditSummaryRow, []auditSummaryRow, error) {
	var total int64
	if err := DB.Model(&AuditLog{}).Where("created_at >= ?", start).Count(&total).Error; err != nil {
		return 0, 0, 0, nil, nil, err
	}

	var failed int64
	if err := DB.Model(&AuditLog{}).
		Where("created_at >= ? AND (action LIKE ? OR action LIKE ? OR detail LIKE ? OR detail LIKE ?)", start, "%FAILED%", "%FAIL%", "%失败%", "%异常%").
		Count(&failed).Error; err != nil {
		return 0, 0, 0, nil, nil, err
	}

	var highRisk int64
	highRisk, err := countHighRiskAuditLogs(start)
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}

	topActions, err := loadAuditSummaryRows(start, "action")
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}
	topActors, err := loadAuditSummaryRows(start, "actor_id")
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}

	return total, failed, highRisk, topActions, topActors, nil
}

func loadAuditSummaryRows(start time.Time, column string) ([]auditSummaryRow, error) {
	if column != "action" && column != "actor_id" {
		return nil, fmt.Errorf("unsupported audit summary column: %s", column)
	}

	var rows []auditSummaryRow
	if err := DB.Model(&AuditLog{}).
		Select(fmt.Sprintf("%s AS label, COUNT(*) AS count", column)).
		Where("created_at >= ?", start).
		Group(column).
		Order("count DESC").
		Limit(5).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func appendAuditSummaryRows(builder *strings.Builder, rows []auditSummaryRow, labelKind string) {
	if len(rows) == 0 {
		builder.WriteString("无记录\n")
		return
	}
	for _, row := range rows {
		label := strings.TrimSpace(formatAuditTextForDisplay(row.Label))
		if label == "" {
			label = "unknown"
		}
		if labelKind == "actor" && label == "0" {
			label = "0(system)"
		}
		builder.WriteString(fmt.Sprintf("- `%s`：`%d`\n", escapeMarkdown(label), row.Count))
	}
}

func countHighRiskAuditLogs(start time.Time) (int64, error) {
	var rows []auditSummaryRow
	if err := DB.Model(&AuditLog{}).
		Select("action AS label, COUNT(*) AS count").
		Where("created_at >= ?", start).
		Group("action").
		Scan(&rows).Error; err != nil {
		return 0, err
	}

	var total int64
	for _, row := range rows {
		if isHighRiskAuditAction(row.Label) {
			total += row.Count
		}
	}
	return total, nil
}

var highRiskAuditActionSet = map[string]struct{}{
	"ADJUST_POINTS":                          {},
	"PROMOTE_ADMIN":                          {},
	"SET_WHITELIST":                          {},
	"SET_SERVER_LINES":                       {},
	"SET_INVITE_PRICE":                       {},
	"SET_RENEW_PRICE":                        {},
	"GENERATE_INVITE_CODES":                  {},
	"GENERATE_RENEW_CODES":                   {},
	"RESERVE_INVITE_CODE":                    {},
	"RELEASE_INVITE_CODE":                    {},
	"USE_INVITE_CODE":                        {},
	"USE_RENEW_CODE":                         {},
	"REFERRAL_TRIAL_REGISTER":                {},
	"TRIAL_CONVERT_FORMAL":                   {},
	"REFERRAL_TRIAL_TASK_CLAIM":              {},
	"BIND_USER":                              {},
	"REBIND_USER":                            {},
	"UNBIND_USER":                            {},
	"MANUAL_BACKUP":                          {},
	"MANUAL_BACKUP_FAILED":                   {},
	"SIMULATE_EXPIRE":                        {},
	"SUSPEND_USER":                           {},
	"UNSUSPEND_USER":                         {},
	"AUTO_SUSPEND_EXPIRED_USER":              {},
	"AUTO_SUSPEND_EXPIRED_USER_LOCAL_FAILED": {},
	"LISTENING_ABUSE_FREEZE":                 {},
	"LISTENING_ABUSE_FREEZE_FAILED":          {},
	"LISTENING_ABUSE_RELEASE":                {},
	"LISTENING_ABUSE_RELEASE_FAILED":         {},
	"LISTENING_ABUSE_RELEASE_BLOCKED":        {},
	"LISTENING_ABUSE_AMNESTY":                {},
	"LISTENING_ABUSE_AMNESTY_FAILED":         {},
	"RENEW_REACTIVATE_USER":                  {},
	"RENEW_REACTIVATE_USER_FAILED":           {},
	"RENEW_REACTIVATE_USER_LOCAL_FAILED":     {},
	"SECT_SHOP_RENEW":                        {},
	"SECT_SHOP_RENEW_REACTIVATE":             {},
	"SELF_DELETE_USER":                       {},
	"AUTO_DELETE_EXPIRED_USER":               {},
	"FORCE_DELETE_USER":                      {},
	"CLEAN_WIDOWS":                           {},
	"CREATE_LOTTERY":                         {},
	"FORCE_DRAW_LOTTERY":                     {},
	"CANCEL_LOTTERY":                         {},
	"CREATE_SECT_LOTTERY":                    {},
	"DRAW_SECT_LOTTERY":                      {},
	"CANCEL_SECT_LOTTERY":                    {},
	"RELOAD_CULTIVATION_RULES":               {},
	"UPDATE_BREAKTHROUGH_CONFIG":             {},
	"UPDATE_REALM_THRESHOLD":                 {},
	"UPDATE_MINOR_REALM_THRESHOLD":           {},
	"UPDATE_SECT_SECRET_REALM_CONFIG":        {},
	"CLAIM_GITHUB_BENEFIT_INVITE":            {},
	"CLAIM_GITHUB_BENEFIT_RENEW":             {},
	"SET_GITHUB_BENEFIT_ENABLED":             {},
	"SET_GITHUB_BENEFIT_QUOTA":               {},
}

func highRiskAuditActions() []string {
	actions := make([]string, 0, len(highRiskAuditActionSet))
	for action := range highRiskAuditActionSet {
		actions = append(actions, action)
	}
	return actions
}

func isHighRiskAuditAction(action string) bool {
	_, ok := highRiskAuditActionSet[normalizeHighRiskAuditAction(action)]
	return ok
}

func normalizeHighRiskAuditAction(action string) string {
	action = strings.ToUpper(strings.TrimSpace(action))
	for {
		switch {
		case strings.HasSuffix(action, "_LOCAL_FAILED"):
			action = strings.TrimSuffix(action, "_LOCAL_FAILED")
		case strings.HasSuffix(action, "_FAILED"):
			action = strings.TrimSuffix(action, "_FAILED")
		default:
			return action
		}
	}
}

func parseAuditQueryDays(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "天"))
	if raw == "" {
		return 0, false
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days <= 0 {
		return 0, false
	}
	return days, true
}

func auditDayRange(t time.Time) (time.Time, time.Time) {
	loc := time.FixedZone("CST", 8*3600)
	local := t.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1)
}

func getTodayAuditDeltaTotal(actorID int64, action string) (int, error) {
	startOfDay, endOfDay := auditDayRange(time.Now())

	var total int
	if err := DB.Model(&AuditLog{}).
		Where("actor_id = ? AND action = ? AND created_at >= ? AND created_at < ?", actorID, action, startOfDay, endOfDay).
		Select("COALESCE(SUM(ABS(delta)), 0)").
		Scan(&total).Error; err != nil {
		return 0, err
	}

	return total, nil
}
