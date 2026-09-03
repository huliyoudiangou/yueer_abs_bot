package main

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"
	"unicode"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const (
	bookRequestStatusPending   = "pending"
	bookRequestStatusClaimed   = "claimed"
	bookRequestStatusNeedInfo  = "need_info"
	bookRequestStatusUploaded  = "uploaded"
	bookRequestStatusCompleted = "completed" // 兼容历史旧状态
	bookRequestStatusRejected  = "rejected"
	bookRequestStatusCancelled = "cancelled"

	bookRequestPendingLimit = 5

	bookRequestWhitelistCost         = 10
	bookRequestWhitelistWeeklyLimit  = 3
	bookRequestWhitelistMonthlyLimit = 10
	bookRequestNormalCost            = 15
	bookRequestNormalWeeklyLimit     = 3
	bookRequestNormalMonthlyLimit    = 5
	bookRequestShortCost             = 20
	bookRequestShortWeeklyLimit      = 1
	bookRequestShortMonthlyLimit     = 3

	bookRequestNoteMaxLen = 300
	bookRequestLinkMaxLen = 500

	// 滞留工单巡检阈值（业务决策：pending 24h / claimed 48h / need_info 24h 提醒 + 24h 宽限）。
	// pending 与 claimed 只提醒不自动收尾；need_info 提醒一次后超时自动取消并退积分。
	bookRequestPendingRemindAfter    = 24 * time.Hour
	bookRequestPendingRemindMaxCount = 2
	bookRequestClaimedRemindAfter    = 48 * time.Hour
	bookRequestClaimedRemindMaxCount = 2
	bookRequestNeedInfoRemindAfter   = 24 * time.Hour
	bookRequestNeedInfoCancelAfter   = 48 * time.Hour
	bookRequestStalePatrolInterval   = time.Hour

	bookRequestLinkRequirementText = "必须以 https:// 开头，仅支持 ximalaya.com / www.ximalaya.com / m.ximalaya.com / xima.tv，路径不能为首页，且不能包含空格、换行、制表符、URL 账号密码信息或其他控制/分隔字符"
	bookRequestNoteInvalidText     = "内容不符合要求，请输入最多 300 字、可换行且不含制表符或其他控制字符的说明。"
)

type bookRequestUserPlan struct {
	Cost         int
	WeeklyLimit  int
	MonthlyLimit int
}

func registrationExpireAtForExistingUser(existingExpireAt *time.Time, defaultExpireAt *time.Time) (*time.Time, bool) {
	if defaultExpireAt == nil {
		return nil, existingExpireAt != nil
	}
	if existingExpireAt == nil || existingExpireAt.Before(*defaultExpireAt) {
		return defaultExpireAt, true
	}
	return nil, false
}

func createRegisteredUserInTx(tx *gorm.DB, user *User) error {
	if tx == nil || user == nil {
		return fmt.Errorf("REGISTERED_USER_INVALID")
	}
	entry := *user
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("REGISTERED_USER_CREATE_MISSED")
	}
	*user = entry
	return nil
}

func hasActiveAbsAccount(userID int64) bool {
	var u User
	if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		return false
	}

	ok, err := userHasUsableLocalAbsAccountAt(u, time.Now())
	if err != nil {
		log.Printf("⚠️ 有效 ABS 账号状态读取失败: user=%d abs=%s err=%s", userID, formatPlainValue(u.AbsUserID), formatPlainError(err))
		return false
	}
	return ok
}

const botMessageAutoDeleteDelay = 10 * time.Minute

func createAutoDeleteMessageRecord(chatID int64, messageID int, deleteAt time.Time) error {
	if DB == nil || chatID == 0 || messageID == 0 {
		return nil
	}

	res := DB.Create(&AutoDeleteMsg{
		ChatID:    chatID,
		MessageID: messageID,
		DeleteAt:  deleteAt,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("AUTO_DELETE_MSG_CREATE_MISSED")
	}
	return nil
}

func registerAutoDeleteMessage(chatID int64, messageID int) {
	if chatID == 0 || messageID == 0 {
		return
	}

	if err := createAutoDeleteMessageRecord(chatID, messageID, time.Now().Add(botMessageAutoDeleteDelay)); err != nil {
		log.Printf("⚠️ 登记自动删除消息失败: chat=%d message=%d err=%s", chatID, messageID, formatPlainError(err))
	}
}

func registerIncomingGroupCommandForAutoDelete(msg *tgbotapi.Message) {
	if msg == nil || msg.Chat == nil {
		return
	}

	// 私聊消息不删除
	if msg.Chat.IsPrivate() {
		return
	}

	registerAutoDeleteMessage(msg.Chat.ID, msg.MessageID)
}

func sendAutoDelete(bot *tgbotapi.BotAPI, chattable tgbotapi.Chattable) (tgbotapi.Message, error) {
	sentMsg, err := bot.Send(chattable)
	if err == nil && sentMsg.Chat.ID < 0 {
		registerAutoDeleteMessage(sentMsg.Chat.ID, sentMsg.MessageID)
	}
	return sentMsg, err
}

func sendNoAutoDelete(bot *tgbotapi.BotAPI, chattable tgbotapi.Chattable) (tgbotapi.Message, error) {
	return bot.Send(chattable)
}

func enqueueAutoDelete(bot *tgbotapi.BotAPI, chattable tgbotapi.Chattable, kind string, priority telegramAsyncPriority, dedupeKey string) bool {
	return enqueueTelegramAsync(telegramAsyncJob{
		Kind:      kind,
		DedupeKey: dedupeKey,
		Priority:  priority,
		Send: func() error {
			_, err := sendAutoDelete(bot, chattable)
			return err
		},
	})
}

func enqueueNoAutoDelete(bot *tgbotapi.BotAPI, chattable tgbotapi.Chattable, kind string, priority telegramAsyncPriority, dedupeKey string) bool {
	return enqueueTelegramAsync(telegramAsyncJob{
		Kind:      kind,
		DedupeKey: dedupeKey,
		Priority:  priority,
		Send: func() error {
			_, err := sendNoAutoDelete(bot, chattable)
			return err
		},
	})
}

func sendPlainText(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("发送 Telegram 文本消息失败: %s", formatTelegramSendError(err))
	}
}

func isTelegramMessageNotModifiedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "message is not modified")
}

func isTerminalTelegramDeleteError(err error) bool {
	if err == nil {
		return false
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "message to delete not found") ||
		strings.Contains(errText, "message can't be deleted") ||
		strings.Contains(errText, "not enough rights") ||
		strings.Contains(errText, "not enough rights to delete messages")
}

func isTerminalTelegramUnpinError(err error) bool {
	if err == nil {
		return false
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "message to unpin not found") ||
		strings.Contains(errText, "message not found") ||
		strings.Contains(errText, "message can't be unpinned") ||
		strings.Contains(errText, "message is not pinned") ||
		strings.Contains(errText, "not enough rights") ||
		strings.Contains(errText, "not enough rights to manage pinned messages")
}

func getTelegramDisplayName(user *tgbotapi.User) string {
	if user == nil {
		return "未知用户"
	}
	if user.UserName != "" {
		return "@" + user.UserName
	}
	if strings.TrimSpace(user.FirstName) != "" {
		return strings.TrimSpace(user.FirstName)
	}
	return fmt.Sprintf("%d", user.ID)
}

func validateXmlyLink(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	for _, r := range raw {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return "", false
		}
	}

	if len(raw) < 10 || len(raw) > bookRequestLinkMaxLen {
		return "", false
	}

	if strings.ContainsAny(raw, " \r\n\t") {
		return "", false
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}

	if u.Scheme != "https" {
		return "", false
	}
	if u.User != nil {
		return "", false
	}

	host := strings.ToLower(u.Hostname())
	allowedHosts := map[string]bool{
		"ximalaya.com":     true,
		"www.ximalaya.com": true,
		"m.ximalaya.com":   true,
		"xima.tv":          true,
	}

	if !allowedHosts[host] {
		return "", false
	}

	if u.Path == "" || u.Path == "/" {
		return "", false
	}

	return raw, true
}

func containsDisallowedControl(text string, allowNewline bool) bool {
	for _, r := range text {
		if r == '\n' && allowNewline {
			continue
		}
		if r < 0x20 || r == 0x7f || r == '\u2028' || r == '\u2029' {
			return true
		}
	}
	return false
}

func validateBookRequestNote(raw string) (string, bool) {
	note := strings.TrimSpace(raw)
	if len([]rune(note)) > bookRequestNoteMaxLen {
		return "", false
	}
	if containsDisallowedControl(note, true) {
		return "", false
	}
	return note, true
}

func markBookRequestUserReplied(db *gorm.DB, req BookRequest, actorName string, replyNote string, now time.Time) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}
	newNote := strings.TrimSpace(req.UserNote + "\n\u8865\u5145\uff1a" + replyNote)
	updated := false
	err := db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&BookRequest{}).
			Where("id = ? AND user_id = ? AND status = ?", req.ID, req.UserID, bookRequestStatusNeedInfo).
			Updates(map[string]interface{}{
				"status":         bookRequestStatusClaimed,
				"user_note":      newNote,
				"last_action_at": &now,
				"remind_count":   0,
			})
		if res.Error != nil {
			return fmt.Errorf("book request user reply update failed: %s", formatPlainError(res.Error))
		}
		if res.RowsAffected == 0 {
			return nil
		}
		updated = true
		return createBookRequestLogInTx(tx, req.ID, req.UserID, actorName, "user_reply", req.Status, bookRequestStatusClaimed, replyNote)
	})
	return updated, err
}

// refundBookRequestInTx 在事务内退还工单积分（业务规则：cancelled / rejected 退还，uploaded 不退）。
// 已退还（RefundedAt 非空）或无扣费记录（CostPaid<=0）时跳过并返回 0。
// 幂等由 refunded_at IS NULL 条件保证；调用方负责在同一事务内完成状态变更与审计。
func refundBookRequestInTx(tx *gorm.DB, req *BookRequest, actorID int64, actorName string, reason string, now time.Time) (int, error) {
	if tx == nil || req == nil || req.ID == 0 {
		return 0, fmt.Errorf("BOOK_REQUEST_REFUND_INVALID")
	}
	if req.RefundedAt != nil || req.CostPaid <= 0 {
		return 0, nil
	}

	res := tx.Model(&BookRequest{}).
		Where("id = ? AND refunded_at IS NULL", req.ID).
		Update("refunded_at", now)
	if res.Error != nil {
		return 0, fmt.Errorf("book request refund update failed: %s", formatPlainError(res.Error))
	}
	if res.RowsAffected == 0 {
		// 已被并发操作退还，不重复退款。
		return 0, nil
	}
	req.RefundedAt = &now

	if err := applyPointDeltaInTx(
		tx,
		req.UserID,
		req.CostPaid,
		"book_request_refund",
		fmt.Sprintf("求书工单 #%d 取消，退还 %d 积分", req.ID, req.CostPaid),
		"book_request",
		fmt.Sprintf("%d", req.ID),
	); err != nil {
		// 用户行缺失（已注销）时无法入账：跳过退款并保留工单取消流程，其余错误回滚整个事务。
		if errors.Is(err, errUserNotFound) {
			log.Printf("⚠️ 求书工单退款跳过（用户不存在）: req=%d user=%d amount=%d", req.ID, req.UserID, req.CostPaid)
			return 0, nil
		}
		return 0, err
	}

	if err := createBookRequestLogInTx(tx, req.ID, actorID, actorName, "refund", req.Status, req.Status,
		fmt.Sprintf("refund %d points; reason=%s", req.CostPaid, reason)); err != nil {
		return 0, err
	}
	if err := writeAuditLogInTx(tx, actorID, "REFUND_BOOK_REQUEST", fmt.Sprintf("%d", req.ID), req.CostPaid,
		fmt.Sprintf("book request refund %d points; reason=%s", req.CostPaid, reason)); err != nil {
		return 0, err
	}
	return req.CostPaid, nil
}

// collectBookRequestAdminIDs 汇总应收到求书工单通知的管理员（环境配置 + 角色管理员）。
func collectBookRequestAdminIDs() map[int64]bool {
	adminIDs := make(map[int64]bool)

	if AppConfig != nil {
		for id := range AppConfig.AdminIDs {
			adminIDs[id] = true
		}
	}

	var dbAdmins []User
	if err := DB.Where("role IN ?", []string{"admin", "super_admin"}).Find(&dbAdmins).Error; err != nil {
		log.Printf("⚠️ 求书工单通知管理员列表读取失败: err=%s", formatPlainError(err))
	} else {
		for _, admin := range dbAdmins {
			adminIDs[admin.TelegramID] = true
		}
	}

	return adminIDs
}

func reloadBookRequestAfterClaim(db *gorm.DB, req *BookRequest, reqID uint, adminID int64, adminName string, now time.Time) error {
	if req != nil {
		req.Status = bookRequestStatusClaimed
		req.AssigneeID = adminID
		req.AssigneeName = adminName
		req.AdminID = adminID
		req.AdminName = adminName
		req.ClaimedAt = &now
		req.LastActionAt = &now
	}
	if db == nil {
		return fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}
	return db.Where("id = ?", reqID).First(req).Error
}

func reloadBookRequestAfterFinish(db *gorm.DB, req *BookRequest, reqID uint, status string, adminID int64, adminName string, now time.Time) error {
	if req != nil {
		req.Status = status
		req.AdminID = adminID
		req.AdminName = adminName
		req.LastActionAt = &now
		req.CompletedAt = &now
	}
	if db == nil {
		return fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}
	return db.Where("id = ?", reqID).First(req).Error
}

func reloadBookRequestAfterNeedInfo(db *gorm.DB, req *BookRequest, reqID uint, note string, adminID int64, adminName string, now time.Time) error {
	if req != nil {
		req.Status = bookRequestStatusNeedInfo
		req.AdminNote = note
		req.AdminID = adminID
		req.AdminName = adminName
		req.AssigneeID = adminID
		req.AssigneeName = adminName
		req.LastActionAt = &now
	}
	if db == nil {
		return fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}
	return db.Where("id = ?", reqID).First(req).Error
}

func reloadBookRequestAfterAdminNote(db *gorm.DB, req *BookRequest, reqID uint, note string, adminID int64, adminName string, now time.Time) error {
	if req != nil {
		req.AdminNote = note
		req.AdminID = adminID
		req.AdminName = adminName
		req.LastActionAt = &now
	}
	if db == nil {
		return fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}
	return db.Where("id = ?", reqID).First(req).Error
}

func recordBookRequestAdminMessageID(db *gorm.DB, req *BookRequest, chatID int64, messageID int) error {
	if db == nil || req == nil || req.ID == 0 || chatID == 0 || messageID == 0 {
		return fmt.Errorf("BOOK_REQUEST_ADMIN_MESSAGE_ID_INVALID")
	}
	res := db.Model(&BookRequest{}).
		Where("id = ?", req.ID).
		Updates(map[string]interface{}{
			"admin_chat_id":    chatID,
			"admin_message_id": messageID,
		})
	if res.Error != nil {
		return fmt.Errorf("book request admin message id update failed: %s", formatPlainError(res.Error))
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("book request admin message id update missed: req=%d", req.ID)
	}
	req.AdminChatID = chatID
	req.AdminMessageID = messageID
	return nil
}

func createBookRequestLog(requestID uint, actorID int64, actorName string, action string, oldStatus string, newStatus string, note string) {
	if DB == nil || requestID == 0 {
		return
	}

	res := DB.Create(&BookRequestLog{
		RequestID: requestID,
		ActorID:   actorID,
		ActorName: actorName,
		Action:    action,
		OldStatus: oldStatus,
		NewStatus: newStatus,
		Note:      note,
	})
	if res.Error != nil {
		err := res.Error
		log.Printf("⚠️ 写入求书工单日志失败: req=%d actor=%d action=%s err=%s", requestID, actorID, formatPlainValue(action), formatPlainError(err))
	}
	if res.Error == nil && res.RowsAffected == 0 {
		err := fmt.Errorf("BOOK_REQUEST_LOG_CREATE_MISSED")
		log.Printf("⚠️ 写入求书工单日志未命中: req=%d actor=%d action=%s err=%s", requestID, actorID, formatPlainValue(action), formatPlainError(err))
	}
}

func createBookRequestLogInTx(tx *gorm.DB, requestID uint, actorID int64, actorName string, action string, oldStatus string, newStatus string, note string) error {
	if tx == nil {
		return fmt.Errorf("BOOK_REQUEST_LOG_DB_EMPTY")
	}
	if requestID == 0 {
		return fmt.Errorf("BOOK_REQUEST_LOG_REQUEST_EMPTY")
	}

	res := tx.Create(&BookRequestLog{
		RequestID: requestID,
		ActorID:   actorID,
		ActorName: actorName,
		Action:    action,
		OldStatus: oldStatus,
		NewStatus: newStatus,
		Note:      note,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("BOOK_REQUEST_LOG_CREATE_MISSED")
	}
	return nil
}

func isMenuLikeBookRequestReply(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}

	menuWords := []string{
		"/start", "/admin",
		"注册", "绑定", "签到", "兑换", "卡密回收", "回收卡密", "邀请码", "续期卡",
		"求书", "我的求书", "待处理求书", "我的处理工单",
		"我的信息", "听书报告", "取消", "返回",
	}

	for _, word := range menuWords {
		if strings.Contains(text, word) {
			return true
		}
	}

	return false
}

func canOperateBookRequest(req BookRequest, actorID int64) bool {
	if isSuperAdmin(actorID) {
		return true
	}

	return req.AssigneeID != 0 && req.AssigneeID == actorID
}

func bookRequestStatusText(status string) string {
	switch status {
	case bookRequestStatusPending:
		return "待接单"
	case bookRequestStatusClaimed:
		return "处理中"
	case bookRequestStatusNeedInfo:
		return "需补充信息"
	case bookRequestStatusUploaded, bookRequestStatusCompleted:
		return "已上传"
	case bookRequestStatusRejected:
		return "暂无资源"
	case bookRequestStatusCancelled:
		return "已取消"
	default:
		return "未知"
	}
}

// isBookRequestClosedStatus 返回工单是否已终结（不再接受接单、备注、要求补充等操作）。
// completed 为历史遗留状态，与 uploaded 同义。
func isBookRequestClosedStatus(status string) bool {
	switch status {
	case bookRequestStatusUploaded, bookRequestStatusCompleted,
		bookRequestStatusRejected, bookRequestStatusCancelled:
		return true
	default:
		return false
	}
}

// isBookRequestOperableStatus 返回工单是否处于管理员可操作状态（已接单/需补充信息）。
func isBookRequestOperableStatus(status string) bool {
	switch status {
	case bookRequestStatusClaimed, bookRequestStatusNeedInfo:
		return true
	default:
		return false
	}
}

// isBookRequestUploadedStatus 返回工单是否已上传（含历史遗留 completed 状态）。
func isBookRequestUploadedStatus(status string) bool {
	switch status {
	case bookRequestStatusUploaded, bookRequestStatusCompleted:
		return true
	default:
		return false
	}
}

// bookRequestOperableStatuses 返回管理员可操作状态集合，供 SQL IN 查询复用。
func bookRequestOperableStatuses() []string {
	return []string{bookRequestStatusClaimed, bookRequestStatusNeedInfo}
}

func formatBookRequestTime(t *time.Time) string {
	if t == nil {
		return "未处理"
	}
	return t.Format("2006-01-02 15:04")
}

func displayBookRequestText(v string, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

func bookRequestWeekRange(t time.Time) (time.Time, time.Time) {
	loc := time.FixedZone("CST", 8*3600)
	local := t.In(loc)
	weekday := int(local.Weekday())
	if weekday == 0 {
		weekday = 7
	}
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -(weekday - 1))
	return start, start.AddDate(0, 0, 7)
}

func bookRequestMonthRange(t time.Time) (time.Time, time.Time) {
	loc := time.FixedZone("CST", 8*3600)
	local := t.In(loc)
	start := time.Date(local.Year(), local.Month(), 1, 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 1, 0)
}

func bookRequestUserPlanForUser(u User, now time.Time) bookRequestUserPlan {
	if u.IsWhitelist {
		return bookRequestUserPlan{
			Cost:         bookRequestWhitelistCost,
			WeeklyLimit:  bookRequestWhitelistWeeklyLimit,
			MonthlyLimit: bookRequestWhitelistMonthlyLimit,
		}
	}

	// 永久用户（无到期时间）视为长期有效，按“有效期 >= 3 个月”档位处理。
	if u.ExpireAt == nil || !u.ExpireAt.Before(now.AddDate(0, 3, 0)) {
		return bookRequestUserPlan{
			Cost:         bookRequestNormalCost,
			WeeklyLimit:  bookRequestNormalWeeklyLimit,
			MonthlyLimit: bookRequestNormalMonthlyLimit,
		}
	}

	return bookRequestUserPlan{
		Cost:         bookRequestShortCost,
		WeeklyLimit:  bookRequestShortWeeklyLimit,
		MonthlyLimit: bookRequestShortMonthlyLimit,
	}
}

func loadBookRequestUserPlanWithDB(db *gorm.DB, userID int64, now time.Time) (User, bookRequestUserPlan, error) {
	var u User
	if err := db.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		return u, bookRequestUserPlan{}, err
	}
	return u, bookRequestUserPlanForUser(u, now), nil
}

func bookRequestPlanText(plan bookRequestUserPlan) string {
	return fmt.Sprintf("每次提交消耗 %d 积分；本周限 %d 条，本月限 %d 条。", plan.Cost, plan.WeeklyLimit, plan.MonthlyLimit)
}

func bookRequestLimitMessageFromCounts(weeklyCount int64, monthlyCount int64, pendingCount int64, plan bookRequestUserPlan) string {
	if weeklyCount >= int64(plan.WeeklyLimit) {
		return fmt.Sprintf("你本周已经提交了 %d 条求书（本周上限 %d 条），请下周再试。", plan.WeeklyLimit, plan.WeeklyLimit)
	}

	if monthlyCount >= int64(plan.MonthlyLimit) {
		return fmt.Sprintf("你本月已经提交了 %d 条求书（本月上限 %d 条），请下月再试。", plan.MonthlyLimit, plan.MonthlyLimit)
	}

	if pendingCount >= bookRequestPendingLimit {
		return fmt.Sprintf("你当前已有 %d 条待处理求书（含需补充信息未回复的），请处理完成后再提交新的求书。", bookRequestPendingLimit)
	}

	return ""
}

func queryBookRequestLimitCounts(db *gorm.DB, userID int64, now time.Time) (int64, int64, int64, error) {
	if db == nil || userID == 0 {
		return 0, 0, 0, fmt.Errorf("BOOK_REQUEST_LIMIT_DB_EMPTY")
	}

	weekStart, weekEnd := bookRequestWeekRange(now)
	monthStart, monthEnd := bookRequestMonthRange(now)

	var weeklyCount int64
	if err := db.Model(&BookRequest{}).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, weekStart, weekEnd).
		Count(&weeklyCount).Error; err != nil {
		return 0, 0, 0, err
	}

	var monthlyCount int64
	if err := db.Model(&BookRequest{}).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, monthStart, monthEnd).
		Count(&monthlyCount).Error; err != nil {
		return 0, 0, 0, err
	}

	var pendingCount int64
	if err := db.Model(&BookRequest{}).
		Where("user_id = ? AND status IN ?", userID, []string{bookRequestStatusPending, bookRequestStatusNeedInfo}).
		Count(&pendingCount).Error; err != nil {
		return 0, 0, 0, err
	}

	return weeklyCount, monthlyCount, pendingCount, nil
}

func checkBookRequestLimitsWithDB(db *gorm.DB, userID int64, now time.Time) (string, error) {
	if db == nil || userID == 0 {
		return "", fmt.Errorf("BOOK_REQUEST_LIMIT_DB_EMPTY")
	}
	_, plan, err := loadBookRequestUserPlanWithDB(db, userID, now)
	if err != nil {
		return "", err
	}
	weeklyCount, monthlyCount, pendingCount, err := queryBookRequestLimitCounts(db, userID, now)
	if err != nil {
		return "", err
	}
	return bookRequestLimitMessageFromCounts(weeklyCount, monthlyCount, pendingCount, plan), nil
}

func checkBookRequestLimits(userID int64) string {
	limitMsg, err := checkBookRequestLimitsWithDB(DB, userID, time.Now())
	if err != nil {
		log.Printf("⚠️ 求书限额检查失败: user=%d err=%s", userID, formatPlainError(err))
		return "求书限额检查失败，请稍后再试。"
	}
	return limitMsg
}

func createBookRequestWithinLimits(req *BookRequest, now time.Time) (string, error) {
	if req == nil {
		return "", fmt.Errorf("BOOK_REQUEST_EMPTY")
	}
	if DB == nil {
		return "", fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}

	var limitMsg string
	err := DB.Transaction(func(tx *gorm.DB) error {
		_, plan, err := loadBookRequestUserPlanWithDB(tx, req.UserID, now)
		if err != nil {
			return err
		}
		weeklyCount, monthlyCount, pendingCount, err := queryBookRequestLimitCounts(tx, req.UserID, now)
		if err != nil {
			return err
		}
		msg := bookRequestLimitMessageFromCounts(weeklyCount, monthlyCount, pendingCount, plan)
		if msg != "" {
			limitMsg = msg
			return nil
		}

		// 记录创建时的实际扣费金额，作为取消/拒绝时退款的唯一依据（不按当前档位重算）。
		req.CostPaid = plan.Cost

		res := tx.Create(req)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("BOOK_REQUEST_CREATE_MISSED")
		}

		if plan.Cost > 0 {
			if err := applyPointDeltaInTx(
				tx,
				req.UserID,
				-plan.Cost,
				"book_request_cost",
				fmt.Sprintf("提交求书工单，消耗 %d 积分", plan.Cost),
				"book_request",
				fmt.Sprintf("%d", req.ID),
			); err != nil {
				return err
			}
		}

		if err := createBookRequestLogInTx(tx, req.ID, req.UserID, req.UserName, "create", "", bookRequestStatusPending, "user created book request"); err != nil {
			return err
		}
		return writeAuditLogInTx(tx, req.UserID, "CREATE_BOOK_REQUEST", fmt.Sprintf("%d", req.ID), 0, "user created book request")
	})
	return limitMsg, err
}
