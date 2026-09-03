package main

import (
	"fmt"
	"log"
	"regexp"
	"strings"
	"unicode"

	"gorm.io/gorm"
)

func writeAuditLog(actorID int64, action string, target string, detail string) {
	writeAuditLogWithDelta(actorID, action, target, 0, detail)
}

func writeAuditLogWithDelta(actorID int64, action string, target string, delta int, detail string) {
	if err := writeAuditLogInTx(DB, actorID, action, target, delta, detail); err != nil {
		log.Printf("⚠️ 写入审计日志失败: actor=%d action=%s target=%s err=%s", actorID, formatPlainValue(action), formatPlainValue(target), formatPlainError(err))
	}
}

func writeAuditLogInTx(tx *gorm.DB, actorID int64, action string, target string, delta int, detail string) error {
	if tx == nil {
		return fmt.Errorf("AUDIT_TX_EMPTY")
	}
	role, err := getAuditActorRoleInTx(tx, actorID)
	if err != nil {
		return err
	}

	target = formatAuditTextForStorage(target, auditTargetMaxRunes)
	detail = formatAuditTextForStorage(detail, auditDetailMaxRunes)

	res := tx.Create(&AuditLog{
		ActorID:   actorID,
		ActorRole: role,
		Action:    action,
		Target:    target,
		Delta:     delta,
		Detail:    detail,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("AUDIT_LOG_CREATE_MISSED")
	}
	return nil
}

const (
	auditTargetMaxRunes = 200
	auditDetailMaxRunes = 1000
)

func formatAuditTextForStorage(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	formatted := formatAuditTextForDisplay(text)
	runes := []rune(formatted)
	if len(runes) <= maxRunes {
		return formatted
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func formatAuditTextForDisplay(text string) string {
	return formatDiagnosticTextForDisplay(text)
}

func normalizeAuditTextForReadability(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	lastSpace := false
	for _, r := range text {
		if unicode.Is(unicode.Cf, r) {
			continue
		}
		if r <= ' ' || r == 0x7f || r == '\u2028' || r == '\u2029' {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimSpace(b.String())
}

func formatDiagnosticTextForDisplay(text string) string {
	return normalizeAuditTextForReadability(redactSensitiveAuditText(text))
}

func redactSensitiveAuditText(text string) string {
	if text == "" {
		return ""
	}
	text = stripUnicodeFormatControls(text)

	patterns := []struct {
		re   *regexp.Regexp
		repl string
	}{
		{
			re:   regexp.MustCompile("(?i)(\"?(?:password|token|api[_-]?key|authorization|secret|security[_-]?code|backup_encrypt_key|security_pepper|telegram_bot_token)\"?\\s*[:=]\\s*)(\"[^\"]*\"|`[^`]*`|bearer\\s+[A-Za-z0-9._~+/=-]+|[^\\s&,，;；}]+)"),
			repl: "${1}***",
		},
		{
			re:   regexp.MustCompile("((?:密码|安全码|卡密|邀请码|续期卡|备份密钥|安全密钥)\\s*[:：=]\\s*)(`[^`]*`|[^\\s,，;；。]+)"),
			repl: "${1}***",
		},
		{
			re:   regexp.MustCompile("(?i)\\b(TELEGRAM_BOT_TOKEN|ABS_API_KEY|BACKUP_ENCRYPT_KEY|SECURITY_PEPPER)\\s*[:=]\\s*[^\\s&,，;；]+"),
			repl: "${1}=***",
		},
		{
			re:   regexp.MustCompile("(?i)\\b(token|api[_-]?key|password|secret|authorization|security[_-]?code|backup_encrypt_key|security_pepper|telegram_bot_token)=([^&\\s\"'`,;]+)"),
			repl: "${1}=***",
		},
		{
			re:   regexp.MustCompile(`(?i)(https://api\.telegram\.org/)bot[^/\s]+/`),
			repl: "${1}bot***:***/",
		},
		{
			re:   regexp.MustCompile("(?i)(https?://)([^\\s/@:]+):([^\\s/@]+)@"),
			repl: "${1}***:***@",
		},
		{
			re:   regexp.MustCompile("(?i)bearer\\s+[A-Za-z0-9._~+/=-]+"),
			repl: "Bearer ***",
		},
	}

	redacted := text
	for _, pattern := range patterns {
		redacted = pattern.re.ReplaceAllString(redacted, pattern.repl)
	}
	return redacted
}

func stripUnicodeFormatControls(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	changed := false
	for _, r := range text {
		if unicode.Is(unicode.Cf, r) {
			changed = true
			continue
		}
		b.WriteRune(r)
	}
	if !changed {
		return text
	}
	return b.String()
}

func formatMarkdownError(err error) string {
	if err == nil {
		return ""
	}
	return escapeMarkdown(truncateRunes(formatDiagnosticTextForDisplay(err.Error()), 500))
}

func formatPlainError(err error) string {
	if err == nil {
		return ""
	}
	return formatPlainValue(err)
}

func formatPlainValue(value any) string {
	return truncateRunes(formatDiagnosticTextForDisplay(fmt.Sprint(value)), 500)
}

func formatTelegramSendError(err error) string {
	return formatPlainError(err)
}

func formatSystemConfigErrorForMarkdown(text string) string {
	if strings.TrimSpace(text) == "" {
		return "无"
	}
	return escapeMarkdown(truncateRunes(formatDiagnosticTextForDisplay(text), 500))
}

func pointTransactionTypeText(txType string) string {
	switch txType {
	case "sign_in":
		return "每日签到"
	case "sign_streak_bonus":
		return "连签奖励"
	case "blind_box_cost":
		return "盲盒消费"
	case "code_cashout":
		return "本服卡密回收"
	case "exchange_invite":
		return "兑换邀请码"
	case "exchange_renew":
		return "兑换续期卡"
	case "redpacket_send":
		return "发红包"
	case "redpacket_grab":
		return "抢红包"
	case "admin_adjust":
		return "管理员调账"
	case "race_bet":
		return "赛马下注"
	case "race_refund":
		return "赛马退款"
	case "race_win":
		return "赛马中奖"
	case "dice_bet":
		return "骰子下注"
	case "dice_refund":
		return "骰子退款"
	case "dice_win":
		return "骰子中奖"
	case "pai_gow_bet":
		return "牌九下注"
	case "pai_gow_refund":
		return "牌九退款"
	case "pai_gow_win":
		return "牌九中奖"
	case "book_request_cost":
		return "求书工单"
	case "book_request_refund":
		return "求书工单退款"
	case "breakthrough_auto_buy":
		return "突破代购"
	case "breakthrough_refund":
		return "突破返还"
	case "breakthrough_fail_penalty":
		return "突破失败惩罚"
	case "breakthrough_splash_penalty":
		return "雷劫外溢惩罚"
	case "breakthrough_treasure_fee":
		return "至宝突破渡劫费"
	case "sect_create":
		return "创建宗门"
	case "sect_join":
		return "加入宗门"
	case "sect_donate":
		return "宗门捐献"
	case "sect_shop_points":
		return "宗门商店"
	case "sect_secret_realm_reward":
		return "宗门秘境"
	case "sect_horn":
		return "宗门喇叭"
	case "beast_feed_points":
		return "神兽喂养"
	case "world_horn":
		return "世界喇叭"
	case "shop_buy_item":
		return "聚宝斋购买"
	case "marketplace_buy":
		return "交易行购买"
	case "marketplace_sell":
		return "交易行售出"
	case "marketplace_fee":
		return "交易行手续费"
	case "garden_plot_open":
		return "药园开垦"
	case "garden_seed_buy":
		return "购买种子"
	case "garden_herb_sell":
		return "药铺回收"
	case "garden_recipe_unlock":
		return "参悟丹方"
	case "garden_alchemy_cost":
		return "炼丹炉火"
	case "legacy_compensation":
		return "历史补偿"
	case "world_boss_reward":
		return "世界Boss奖励"
	case "lingjing_exchange":
		return "灵晶兑换"
	case "lottery_reward":
		return "积分抽奖奖励"
	case "lottery_entry_cost":
		return "积分抽奖参与"
	case "lottery_loser_refund":
		return "抽奖未中奖返还"
	case "lottery_cancel_refund":
		return "抽奖取消退款"
	case "service_outage_compensation":
		return "服务异常补偿"
	case "referral_reward":
		return "邀请拉新奖励"
	default:
		return txType
	}
}

func pointTransactionTypeMarkdown(txType string) string {
	return escapeMarkdown(pointTransactionTypeText(txType))
}

func pointTransactionDescriptionMarkdown(description string) string {
	description = strings.TrimSpace(formatDiagnosticTextForDisplay(description))
	if description == "" {
		return "-"
	}
	return escapeMarkdownPreservingEscapes(description)
}
