package main

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const (
	bookAnnouncementRecoveryConfirmSentStep   = "WAITING_CONFIRM_BOOK_ANNOUNCEMENT_SENT"
	bookAnnouncementRecoveryConfirmAbsentStep = "WAITING_CONFIRM_BOOK_ANNOUNCEMENT_ABSENT"
)

// HandleBookAnnouncementRecoveryCommand provides the human reconciliation
// required when Telegram's response was ambiguous. It is intentionally private
// chat + super-admin + exact second confirmation only.
func HandleBookAnnouncementRecoveryCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) bool {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return false
	}
	text = strings.TrimSpace(text)
	session := getSession(msg.From.ID)
	step := session.GetStep()
	isRecoveryStep := step == bookAnnouncementRecoveryConfirmSentStep || step == bookAnnouncementRecoveryConfirmAbsentStep
	isRecoveryCommand := text == "公告异常" || text == "查公告异常" || strings.HasPrefix(text, "公告异常已发 ") || strings.HasPrefix(text, "公告异常未发 ")
	if !isRecoveryStep && !isRecoveryCommand {
		return false
	}
	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "❌ 公告异常处置仅允许超级管理员私聊 Bot 执行。")
		return true
	}
	if !requireSuperAdmin(bot, msg.Chat.ID, msg.From.ID) {
		if isRecoveryStep {
			clearSession(msg.From.ID)
		}
		return true
	}

	if isRecoveryStep {
		return handleBookAnnouncementRecoveryConfirmation(bot, msg, text, session, step)
	}
	if text == "公告异常" || text == "查公告异常" {
		showUncertainBookAnnouncements(bot, msg.Chat.ID)
		return true
	}

	fields := strings.Fields(text)
	if len(fields) < 2 {
		sendPlainText(bot, msg.Chat.ID, "❌ 格式错误。请发送“公告异常”查看处置说明。")
		return true
	}
	reqID64, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil || reqID64 == 0 {
		sendPlainText(bot, msg.Chat.ID, "❌ 工单 ID 无效。")
		return true
	}
	reqID := uint(reqID64)
	delivery, err := loadUncertainBookRequestAnnouncementDelivery(reqID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendPlainText(bot, msg.Chat.ID, "❌ 该工单当前不是 uncertain 状态，禁止人工覆盖。")
		} else {
			sendPlainText(bot, msg.Chat.ID, "❌ 公告状态读取失败，已拒绝操作。")
		}
		return true
	}

	session.SetTemp("book_announcement_recovery_req_id", strconv.FormatUint(uint64(reqID), 10))
	if strings.HasPrefix(text, "公告异常已发 ") {
		if len(fields) != 3 {
			sendPlainText(bot, msg.Chat.ID, "❌ 格式：公告异常已发 <工单ID> <Telegram消息ID>")
			return true
		}
		messageID, parseErr := strconv.Atoi(fields[2])
		if parseErr != nil || messageID <= 0 {
			sendPlainText(bot, msg.Chat.ID, "❌ Telegram 消息 ID 无效。")
			return true
		}
		session.SetTemp("book_announcement_recovery_message_id", strconv.Itoa(messageID))
		session.SetStep(bookAnnouncementRecoveryConfirmSentStep)
		UserSessions.Store(msg.From.ID, session)
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("⚠️ 二次确认\n\n请再次核对目标群 %d 中存在公告凭证 %s，且消息 ID 为 %d。\n\n确认无误请完整回复：\n确认公告已发 #%d %d\n\n其他输入将取消。", delivery.TargetChatID, formatBookAnnouncementDeliveryKey(reqID), messageID, reqID, messageID))
		return true
	}

	if len(fields) != 2 {
		sendPlainText(bot, msg.Chat.ID, "❌ 格式：公告异常未发 <工单ID>")
		return true
	}
	session.SetStep(bookAnnouncementRecoveryConfirmAbsentStep)
	UserSessions.Store(msg.From.ID, session)
	sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("🚨 二次确认\n\n只有在目标群 %d 中搜索公告凭证 %s，确认不存在原公告后，才能解除重发锁。\n\n确认不存在请完整回复：\n确认公告未发 #%d\n\n误判会造成重复公告，其他输入将取消。", delivery.TargetChatID, formatBookAnnouncementDeliveryKey(reqID), reqID))
	return true
}

func handleBookAnnouncementRecoveryConfirmation(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState, step string) bool {
	reqID64, err := strconv.ParseUint(session.GetTemp("book_announcement_recovery_req_id"), 10, 64)
	if err != nil || reqID64 == 0 {
		clearSession(msg.From.ID)
		sendPlainText(bot, msg.Chat.ID, "❌ 确认上下文已失效，请重新发送“公告异常”。")
		return true
	}
	reqID := uint(reqID64)
	if step == bookAnnouncementRecoveryConfirmSentStep {
		messageID, parseErr := strconv.Atoi(session.GetTemp("book_announcement_recovery_message_id"))
		confirmText := fmt.Sprintf("确认公告已发 #%d %d", reqID, messageID)
		if parseErr != nil || messageID <= 0 || text != confirmText {
			clearSession(msg.From.ID)
			sendPlainText(bot, msg.Chat.ID, "🛑 已取消公告已发确认。")
			return true
		}
		err = confirmBookRequestAnnouncementSent(reqID, messageID, msg.From.ID, getTelegramDisplayName(msg.From), time.Now())
		clearSession(msg.From.ID)
		if err != nil {
			sendPlainText(bot, msg.Chat.ID, "❌ 状态、业务日志或审计日志未能原子提交，未修改公告状态。")
			return true
		}
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("✅ 工单 #%d 已核对为已发送，消息 ID %d；状态、成功日志和审计日志已同步登记。", reqID, messageID))
		return true
	}

	confirmText := fmt.Sprintf("确认公告未发 #%d", reqID)
	if text != confirmText {
		clearSession(msg.From.ID)
		sendPlainText(bot, msg.Chat.ID, "🛑 已取消解除公告重发锁。")
		return true
	}
	err = confirmBookRequestAnnouncementAbsent(reqID, msg.From.ID, time.Now())
	clearSession(msg.From.ID)
	if err != nil {
		sendPlainText(bot, msg.Chat.ID, "❌ 状态与审计日志未能原子提交，未解除重发锁。")
		return true
	}
	sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("✅ 工单 #%d 已核对为未发送，重发锁已安全解除。正在重新生成候选……", reqID))
	if err := sendBookAnnouncementCandidatePrompt(bot, msg.From.ID, reqID); err != nil {
		sendPlainText(bot, msg.Chat.ID, "⚠️ 重发锁已解除，但候选生成失败；稍后可从求书工单重新读取候选。")
	}
	return true
}

func showUncertainBookAnnouncements(bot *tgbotapi.BotAPI, chatID int64) {
	deliveries, err := listUncertainBookRequestAnnouncementDeliveries(20)
	if err != nil {
		sendPlainText(bot, chatID, "❌ uncertain 公告读取失败。")
		return
	}
	if len(deliveries) == 0 {
		sendPlainText(bot, chatID, "✅ 当前没有待人工核查的 uncertain 求书公告。")
		return
	}
	lines := []string{"🚨 待核查求书公告（最多 20 条）", "", "请先在对应目标群搜索唯一公告凭证，再执行二次确认："}
	for _, delivery := range deliveries {
		lines = append(lines, fmt.Sprintf("• 工单 #%d｜凭证 %s｜目标群 %d｜尝试 %d｜原因 %s", delivery.RequestID, formatBookAnnouncementDeliveryKey(delivery.RequestID), delivery.TargetChatID, delivery.AttemptCount, formatPlainValue(delivery.LastErrorCode)))
	}
	lines = append(lines, "", "群内已存在：", "公告异常已发 <工单ID> <Telegram消息ID>", "", "群内确认不存在：", "公告异常未发 <工单ID>")
	sendPlainText(bot, chatID, strings.Join(lines, "\n"))
}
