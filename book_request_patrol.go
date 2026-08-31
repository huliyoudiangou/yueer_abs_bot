package main

import (
	"fmt"
	"log"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

// 求书工单滞留巡检。
//
// 业务规则（已确认）：
//   - pending   超 24h 无人接单 → 提醒所有管理员（最多 2 次），不自动收尾；
//   - claimed   超 48h 未处理   → 提醒接单人（最多 2 次），不自动收尾；
//   - need_info 超 24h 用户未补充 → 提醒用户（一次）；
//     超 48h 仍未补充 → 自动取消并按 CostPaid 退还积分（RefundedAt 幂等防重复退款）。
//
// 巡检由后台调度器每小时触发一次（见 runBookRequestStalePatrolIfNeeded），
// 提醒次数用 RemindCount 去重（状态转移时重置），扫描走 (status, last_action_at) 索引。
var (
	bookRequestStalePatrolMu      sync.Mutex
	bookRequestStalePatrolRunning bool
	bookRequestStalePatrolLastRun time.Time
)

func runBookRequestStalePatrolIfNeeded(bot *tgbotapi.BotAPI, now time.Time) {
	if bot == nil || DB == nil || AppConfig == nil {
		return
	}

	bookRequestStalePatrolMu.Lock()
	if bookRequestStalePatrolRunning || now.Sub(bookRequestStalePatrolLastRun) < bookRequestStalePatrolInterval {
		bookRequestStalePatrolMu.Unlock()
		return
	}
	bookRequestStalePatrolRunning = true
	bookRequestStalePatrolLastRun = now
	bookRequestStalePatrolMu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ 求书工单滞留巡检 panic，已恢复: panic=%s", formatPlainValue(r))
		}
		bookRequestStalePatrolMu.Lock()
		bookRequestStalePatrolRunning = false
		bookRequestStalePatrolMu.Unlock()
	}()

	patrolStalePendingBookRequests(bot, now)
	patrolStaleClaimedBookRequests(bot, now)
	patrolStaleNeedInfoBookRequests(bot, now)
}

// patrolStalePendingBookRequests 待接单超时的工单：向管理员发送汇总提醒（不自动收尾）。
func patrolStalePendingBookRequests(bot *tgbotapi.BotAPI, now time.Time) {
	cutoff := now.Add(-bookRequestPendingRemindAfter)
	var reqs []BookRequest
	if err := DB.
		Where("status = ? AND remind_count < ? AND COALESCE(last_action_at, created_at) < ?",
			bookRequestStatusPending, bookRequestPendingRemindMaxCount, cutoff).
		Order("created_at ASC").
		Limit(20).
		Find(&reqs).Error; err != nil {
		log.Printf("⚠️ 求书工单滞留巡检读取失败: stage=pending err=%s", formatPlainError(err))
		return
	}

	due := filterBookRequestsDueForRemind(reqs, now, bookRequestPendingRemindAfter)
	if len(due) == 0 {
		return
	}

	builder := fmt.Sprintf("⏰ 求书工单滞留提醒\n\n以下工单已超过 %s 无人接单：\n", formatBookRequestDurationText(bookRequestPendingRemindAfter))
	for _, req := range due {
		createdText := req.CreatedAt.Format("2006-01-02 15:04")
		builder += fmt.Sprintf("• #%d  %s（提交于 %s）\n", req.ID, displayBookRequestText(req.UserName, "未知用户"), createdText)
	}
	builder += "\n请及时接单处理，避免用户久等。"

	adminIDs := collectBookRequestAdminIDs()
	for adminID := range adminIDs {
		sendPlainText(bot, adminID, builder)
	}

	for _, req := range due {
		if err := markBookRequestReminded(req, now, "stale_remind_pending"); err != nil {
			log.Printf("⚠️ 求书工单滞留提醒标记失败: stage=pending req=%d err=%s", req.ID, formatPlainError(err))
		}
	}
}

// patrolStaleClaimedBookRequests 已接单但长期未处理的工单：提醒接单人（不自动收尾）。
func patrolStaleClaimedBookRequests(bot *tgbotapi.BotAPI, now time.Time) {
	cutoff := now.Add(-bookRequestClaimedRemindAfter)
	var reqs []BookRequest
	if err := DB.
		Where("status = ? AND remind_count < ? AND last_action_at < ?",
			bookRequestStatusClaimed, bookRequestClaimedRemindMaxCount, cutoff).
		Order("last_action_at ASC").
		Limit(20).
		Find(&reqs).Error; err != nil {
		log.Printf("⚠️ 求书工单滞留巡检读取失败: stage=claimed err=%s", formatPlainError(err))
		return
	}

	due := filterBookRequestsDueForRemind(reqs, now, bookRequestClaimedRemindAfter)
	if len(due) == 0 {
		return
	}

	for _, req := range due {
		notifyID := req.AssigneeID
		if notifyID == 0 {
			notifyID = req.AdminID
		}
		if notifyID == 0 {
			continue
		}
		sendPlainText(bot, notifyID, fmt.Sprintf(
			"⏰ 求书工单 #%d 已接单超过 %s 未处理。\n\n用户已支付积分，请尽快跟进处理。",
			req.ID,
			formatBookRequestDurationText(bookRequestClaimedRemindAfter),
		))

		if err := markBookRequestReminded(req, now, "stale_remind_claimed"); err != nil {
			log.Printf("⚠️ 求书工单滞留提醒标记失败: stage=claimed req=%d err=%s", req.ID, formatPlainError(err))
		}
	}
}

// patrolStaleNeedInfoBookRequests 需补充信息超时的工单：先提醒用户，宽限期后自动取消并退款。
func patrolStaleNeedInfoBookRequests(bot *tgbotapi.BotAPI, now time.Time) {
	remindCutoffBegin := now.Add(-bookRequestNeedInfoCancelAfter)
	remindCutoff := now.Add(-bookRequestNeedInfoRemindAfter)

	// 提醒：超过 24h 未补充、尚未提醒过、且还没到自动取消时间（避免与取消消息重复轰炸）。
	var remindReqs []BookRequest
	if err := DB.
		Where("status = ? AND remind_count = 0 AND last_action_at >= ? AND last_action_at < ?",
			bookRequestStatusNeedInfo, remindCutoffBegin, remindCutoff).
		Order("last_action_at ASC").
		Limit(20).
		Find(&remindReqs).Error; err != nil {
		log.Printf("⚠️ 求书工单滞留巡检读取失败: stage=need_info_remind err=%s", formatPlainError(err))
		return
	}

	for _, req := range remindReqs {
		sendPlainText(bot, req.UserID, fmt.Sprintf(
			"❓ 你的求书 #%d 需要补充信息已超过 %s：\n\n%s\n\n请在 %s 内直接回复补充内容，否则工单将自动取消并退还积分。",
			req.ID,
			formatBookRequestDurationText(bookRequestNeedInfoRemindAfter),
			displayBookRequestText(req.AdminNote, "请补充更详细的信息"),
			formatBookRequestDurationText(bookRequestNeedInfoCancelAfter-bookRequestNeedInfoRemindAfter),
		))

		if req.AssigneeID != 0 {
			sendPlainText(bot, req.AssigneeID, fmt.Sprintf(
				"⏰ 求书 #%d 已要求补充信息超过 %s，用户仍未回复，已向用户发送提醒。",
				req.ID,
				formatBookRequestDurationText(bookRequestNeedInfoRemindAfter),
			))
		}

		if err := markBookRequestReminded(req, now, "stale_remind_need_info"); err != nil {
			log.Printf("⚠️ 求书工单滞留提醒标记失败: stage=need_info req=%d err=%s", req.ID, formatPlainError(err))
		}
	}

	// 自动取消：超过 48h 未补充（无论是否提醒成功），取消并退还积分。
	var cancelCutoff = now.Add(-bookRequestNeedInfoCancelAfter)
	var staleReqs []BookRequest
	if err := DB.
		Where("status = ? AND last_action_at < ?", bookRequestStatusNeedInfo, cancelCutoff).
		Order("last_action_at ASC").
		Limit(20).
		Find(&staleReqs).Error; err != nil {
		log.Printf("⚠️ 求书工单滞留巡检读取失败: stage=need_info_cancel err=%s", formatPlainError(err))
		return
	}

	for _, req := range staleReqs {
		autoCancelStaleNeedInfoBookRequest(bot, req, now)
	}
}

// autoCancelStaleNeedInfoBookRequest need_info 超时自动取消 + 退款（单工单事务）。
func autoCancelStaleNeedInfoBookRequest(bot *tgbotapi.BotAPI, req BookRequest, now time.Time) {
	oldStatus := req.Status
	cancelled := false
	refunded := 0

	err := DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&BookRequest{}).
			Where("id = ? AND status = ?", req.ID, bookRequestStatusNeedInfo).
			Updates(map[string]interface{}{
				"status": bookRequestStatusCancelled,
				// 展示用取消来源（同用户取消，admin_name 无逻辑依赖）。
				"admin_name":   "系统超时取消",
				"completed_at": &now,
			})
		if res.Error != nil {
			return fmt.Errorf("book request auto cancel update failed: %s", formatPlainError(res.Error))
		}
		if res.RowsAffected == 0 {
			return nil
		}
		cancelled = true

		amount, err := refundBookRequestInTx(tx, &req, 0, "system", "need_info timeout auto cancel", now)
		if err != nil {
			return err
		}
		refunded = amount

		if err := createBookRequestLogInTx(tx, req.ID, 0, "system", "auto_cancel", oldStatus, bookRequestStatusCancelled, "need_info timeout auto cancel"); err != nil {
			return err
		}
		return writeAuditLogInTx(tx, 0, "AUTO_CANCEL_BOOK_REQUEST", fmt.Sprintf("%d", req.ID), 0, "book request auto cancelled; reason=need_info timeout")
	})
	if err != nil {
		log.Printf("⚠️ 求书工单超时自动取消失败: req=%d err=%s", req.ID, formatPlainError(err))
		return
	}
	if !cancelled {
		return
	}

	var updatedReq BookRequest
	if err := DB.Where("id = ?", req.ID).First(&updatedReq).Error; err != nil {
		log.Printf("⚠️ 求书工单超时取消后读取失败: req=%d err=%s", req.ID, formatPlainError(err))
		updatedReq = req
		updatedReq.Status = bookRequestStatusCancelled
	}

	refreshStoredBookRequestAdminMessage(bot, updatedReq, true, 0, 0)

	refundText := "本次取消未产生积分退还。"
	if refunded > 0 {
		refundText = fmt.Sprintf("已退还 %d 积分。", refunded)
	}
	sendPlainText(bot, updatedReq.UserID, fmt.Sprintf(
		"📚 你的求书 #%d 因长时间未补充信息已自动取消。\n\n%s\n如仍需要该书，补充后可重新提交求书工单。",
		req.ID,
		refundText,
	))

	if updatedReq.AssigneeID != 0 {
		sendPlainText(bot, updatedReq.AssigneeID, fmt.Sprintf(
			"📚 求书 #%d 因用户长时间未补充信息已自动取消。%s",
			req.ID,
			refundText,
		))
	}

	log.Printf("✅ 求书工单超时自动取消完成: req=%d refunded=%d", req.ID, refunded)
}

// filterBookRequestsDueForRemind 按“阈值 × (已提醒次数+1)”过滤真正到期的工单，
// 让第 1/2 次提醒之间保持等间隔。
func filterBookRequestsDueForRemind(reqs []BookRequest, now time.Time, remindAfter time.Duration) []BookRequest {
	var due []BookRequest
	for _, req := range reqs {
		base := req.LastActionAt
		if base == nil {
			base = &req.CreatedAt
		}
		dueAt := base.Add(remindAfter * time.Duration(req.RemindCount+1))
		if now.After(dueAt) {
			due = append(due, req)
		}
	}
	return due
}

// markBookRequestReminded 记录一次滞留提醒（条件更新防并发重复计数）。
func markBookRequestReminded(req BookRequest, now time.Time, action string) error {
	res := DB.Model(&BookRequest{}).
		Where("id = ? AND status = ? AND remind_count = ?", req.ID, req.Status, req.RemindCount).
		Updates(map[string]interface{}{
			"remind_count":   req.RemindCount + 1,
			"last_remind_at": &now,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	createBookRequestLog(req.ID, 0, "system", action, req.Status, req.Status, fmt.Sprintf("stale reminder #%d", req.RemindCount+1))
	return nil
}

// formatBookRequestDurationText 时长的人性化文案（巡检提示用）。
func formatBookRequestDurationText(d time.Duration) string {
	hours := int(d.Hours())
	if hours >= 24 && hours%24 == 0 {
		return fmt.Sprintf("%d 天", hours/24)
	}
	return fmt.Sprintf("%d 小时", hours)
}
