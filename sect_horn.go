package main

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

type SectHornBroadcast struct {
	gorm.Model

	HornID string `gorm:"index;not null"`

	Scope string `gorm:"index;not null"` // sect / world

	SectID   int64  `gorm:"index"`
	SectName string `gorm:"index"`

	SenderID   int64  `gorm:"index;not null"`
	SenderName string `gorm:"index"`
	SenderRole string `gorm:"index"`

	Cost    int
	Content string `gorm:"not null"`

	RecipientCount int
	SuccessCount   int
	FailedCount    int

	Status      string     `gorm:"index;not null"`
	SubmittedAt time.Time  `gorm:"index;not null"`
	CompletedAt *time.Time `gorm:"index"`
	LastError   string
}

func (SectHornBroadcast) TableName() string {
	return "sect_horn_broadcasts"
}

type SectHornDelivery struct {
	gorm.Model

	HornID string `gorm:"index;not null"`

	UserID   int64  `gorm:"index;not null"`
	UserName string `gorm:"index"`

	Status string `gorm:"index;not null"`
	Error  string
	SentAt *time.Time `gorm:"index"`
}

func (SectHornDelivery) TableName() string {
	return "sect_horn_deliveries"
}

func createSectHornBroadcastInTx(tx *gorm.DB, broadcast *SectHornBroadcast) error {
	if tx == nil || broadcast == nil {
		return fmt.Errorf("SECT_HORN_BROADCAST_INVALID")
	}
	broadcast.HornID = formatPlainValue(broadcast.HornID)
	broadcast.Scope = formatPlainValue(broadcast.Scope)
	broadcast.SectName = formatPlainValue(broadcast.SectName)
	broadcast.SenderName = formatPlainValue(broadcast.SenderName)
	broadcast.SenderRole = formatPlainValue(broadcast.SenderRole)
	broadcast.Status = formatPlainValue(broadcast.Status)
	broadcast.LastError = formatPlainValue(broadcast.LastError)
	res := tx.Create(broadcast)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SECT_HORN_BROADCAST_CREATE_MISSED")
	}
	return nil
}

func createSectHornDeliveriesInTx(tx *gorm.DB, deliveries []SectHornDelivery) error {
	if tx == nil || len(deliveries) == 0 {
		return fmt.Errorf("SECT_HORN_DELIVERIES_INVALID")
	}
	for i := range deliveries {
		deliveries[i].HornID = formatPlainValue(deliveries[i].HornID)
		deliveries[i].UserName = formatPlainValue(deliveries[i].UserName)
		deliveries[i].Status = formatPlainValue(deliveries[i].Status)
		deliveries[i].Error = formatPlainValue(deliveries[i].Error)
	}
	res := tx.CreateInBatches(&deliveries, 500)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != int64(len(deliveries)) {
		return fmt.Errorf("SECT_HORN_DELIVERIES_CREATE_MISSED")
	}
	return nil
}

type sectHornRecipient struct {
	UserID   int64
	UserName string
}

type sectHornPreview struct {
	Scope          string
	SectID         int64
	SectName       string
	SenderRole     string
	Cost           int
	RecipientCount int
	Content        string
}

type sectHornCreateResult struct {
	HornID         string
	Scope          string
	SectID         int64
	SectName       string
	Cost           int
	RecipientCount int
}

type sectHornCooldownError struct {
	Until time.Time
	Text  string
}

func (e *sectHornCooldownError) Error() string {
	return "SECT_HORN_COOLDOWN"
}

const (
	sectHornScopeSect  = "sect"
	sectHornScopeWorld = "world"

	sectHornCost  = 2
	worldHornCost = 5

	sectHornContentMinRunes = 5
	sectHornContentMaxRunes = 200

	sectHornSectCooldown        = 5 * time.Minute
	sectHornWorldSectCooldown   = time.Hour
	sectHornWorldGlobalCooldown = 5 * time.Minute

	sectHornStatusQueued    = "queued"
	sectHornStatusSending   = "sending"
	sectHornStatusCompleted = "completed"
	sectHornStatusFailed    = "failed"

	sectHornDeliveryPending = "pending"
	sectHornDeliverySent    = "sent"
	sectHornDeliveryFailed  = "failed"

	sectHornDispatchInterval = 100 * time.Millisecond
	sectHornStaleSendingAge  = 10 * time.Minute
)

var (
	sectHornCreateMutex   sync.Mutex
	sectHornDispatchMutex sync.Mutex
	sectHornLinkPattern   = regexp.MustCompile(`(?i)(https?://|www\.|t\.me/|telegram\.me/)`)
)

func sectHornPointDescriptionSectName(name string) string {
	return lotteryDisplayText(name, 80, "-")
}

func handleSectHornStart(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, scope string, rawContent string) {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return
	}

	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "📣 喇叭需要私聊我发送，避免正文提前公开。\n\n用法：宗门喇叭 内容\n或：世界喇叭 内容")
		return
	}

	content, err := validateSectHornContent(rawContent)
	if err != nil {
		sendPlainText(bot, msg.Chat.ID, sectHornUsageText(scope)+"\n\n"+sectHornValidationMessage(err))
		return
	}

	preview, err := buildSectHornPreview(msg.From.ID, scope, content, time.Now())
	if err != nil {
		sendPlainText(bot, msg.Chat.ID, sectHornActionErrorText(err))
		return
	}

	session := getSession(msg.From.ID)
	session.SetTemp("sect_horn_scope", preview.Scope)
	session.SetTemp("sect_horn_content", preview.Content)
	session.SetStep("WAITING_CONFIRM_SECT_HORN")
	UserSessions.Store(msg.From.ID, session)

	sendPlainText(bot, msg.Chat.ID, formatSectHornPreview(preview))
}

func handleSectHornSession(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, session *SessionState, text string) {
	if msg == nil || msg.Chat == nil || msg.From == nil || session == nil {
		return
	}

	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "📣 喇叭确认请在私聊中完成。")
		return
	}

	scope := session.GetTemp("sect_horn_scope")
	content := session.GetTemp("sect_horn_content")
	wantConfirm := sectHornConfirmText(scope)
	if text != wantConfirm {
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("请回复：%s\n发送“取消”可退出。", wantConfirm))
		return
	}

	sectHornCreateMutex.Lock()
	result, err := createSectHornBroadcast(msg.From, scope, content)
	sectHornCreateMutex.Unlock()

	if err != nil {
		sendPlainText(bot, msg.Chat.ID, sectHornActionErrorText(err))
		clearSession(msg.From.ID)
		return
	}

	clearSession(msg.From.ID)
	sendPlainText(bot, msg.Chat.ID, fmt.Sprintf(
		"✅ %s已提交。\n\n扣除积分：%d\n接收人数：%d\n系统会逐位道友私聊送达，完成后回报送达结果。",
		sectHornTitle(result.Scope),
		result.Cost,
		result.RecipientCount,
	))

	go deliverSectHornBroadcast(bot, result.HornID)
}

func StartSectHornDispatcher(bot *tgbotapi.BotAPI) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("⚠️ 宗门喇叭投递调度器 panic，已恢复: panic=%s", formatPlainValue(r))
			}
		}()

		time.Sleep(5 * time.Second)
		dispatchPendingSectHorns(bot)

		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			dispatchPendingSectHorns(bot)
		}
	}()
}

func dispatchPendingSectHorns(bot *tgbotapi.BotAPI) {
	if DB == nil || bot == nil {
		return
	}

	staleBefore := time.Now().Add(-sectHornStaleSendingAge)
	var broadcasts []SectHornBroadcast
	if err := DB.
		Where("status = ? OR (status = ? AND updated_at < ?)", sectHornStatusQueued, sectHornStatusSending, staleBefore).
		Order("created_at ASC").
		Limit(5).
		Find(&broadcasts).Error; err != nil {
		log.Printf("⚠️ 查询待投递喇叭失败: err=%s", formatPlainError(err))
		return
	}

	for _, broadcast := range broadcasts {
		deliverSectHornBroadcast(bot, broadcast.HornID)
	}
}

func deliverSectHornBroadcast(bot *tgbotapi.BotAPI, hornID string) {
	if DB == nil || bot == nil || strings.TrimSpace(hornID) == "" {
		return
	}

	sectHornDispatchMutex.Lock()
	defer sectHornDispatchMutex.Unlock()

	staleBefore := time.Now().Add(-sectHornStaleSendingAge)
	res := DB.Model(&SectHornBroadcast{}).
		Where(
			"horn_id = ? AND (status = ? OR (status = ? AND updated_at < ?))",
			hornID,
			sectHornStatusQueued,
			sectHornStatusSending,
			staleBefore,
		).
		Update("status", sectHornStatusSending)
	if res.Error != nil {
		log.Printf("⚠️ 喇叭投递状态占用失败: horn=%s err=%s", formatPlainValue(hornID), formatPlainError(res.Error))
		return
	}
	if res.RowsAffected == 0 {
		return
	}

	var broadcast SectHornBroadcast
	if err := DB.Where("horn_id = ?", hornID).First(&broadcast).Error; err != nil {
		log.Printf("⚠️ 读取喇叭广播失败: horn=%s err=%s", formatPlainValue(hornID), formatPlainError(err))
		return
	}

	var deliveries []SectHornDelivery
	if err := DB.
		Where("horn_id = ? AND status = ?", hornID, sectHornDeliveryPending).
		Order("id ASC").
		Find(&deliveries).Error; err != nil {
		log.Printf("⚠️ 读取喇叭投递明细失败: horn=%s err=%s", formatPlainValue(hornID), formatPlainError(err))
		return
	}

	messageText := formatSectHornDeliveryMessage(broadcast)
	for _, delivery := range deliveries {
		now := time.Now()
		msg := tgbotapi.NewMessage(delivery.UserID, messageText)
		if _, err := sendNoAutoDelete(bot, msg); err != nil {
			errText := truncateRunes(formatTelegramSendError(err), 240)
			updateRes := DB.Model(&SectHornDelivery{}).
				Where("id = ? AND status = ?", delivery.ID, sectHornDeliveryPending).
				Updates(map[string]interface{}{
					"status": sectHornDeliveryFailed,
					"error":  errText,
				})
			if updateRes.Error == nil && updateRes.RowsAffected == 0 {
				log.Printf("SECT_HORN_DELIVERY_FAILED_STATE_CHANGED horn=%s user=%d", formatPlainValue(hornID), delivery.UserID)
			}
			if updateRes.Error != nil {
				updateErr := updateRes.Error
				log.Printf("⚠️ 记录喇叭投递失败状态异常: horn=%s user=%d err=%s", formatPlainValue(hornID), delivery.UserID, formatPlainError(updateErr))
			}
			log.Printf("⚠️ 喇叭私聊投递失败: horn=%s user=%d err=%s", formatPlainValue(hornID), delivery.UserID, errText)
		} else {
			updateRes := DB.Model(&SectHornDelivery{}).
				Where("id = ? AND status = ?", delivery.ID, sectHornDeliveryPending).
				Updates(map[string]interface{}{
					"status":  sectHornDeliverySent,
					"sent_at": &now,
					"error":   "",
				})
			if updateRes.Error == nil && updateRes.RowsAffected == 0 {
				log.Printf("SECT_HORN_DELIVERY_SENT_STATE_CHANGED horn=%s user=%d", formatPlainValue(hornID), delivery.UserID)
			}
			if updateRes.Error != nil {
				updateErr := updateRes.Error
				log.Printf("⚠️ 记录喇叭投递成功状态异常: horn=%s user=%d err=%s", formatPlainValue(hornID), delivery.UserID, formatPlainError(updateErr))
			}
		}
		time.Sleep(sectHornDispatchInterval)
	}

	completeSectHornBroadcast(bot, hornID)
}

func completeSectHornBroadcast(bot *tgbotapi.BotAPI, hornID string) {
	var sentCount int64
	var failedCount int64
	if err := DB.Model(&SectHornDelivery{}).Where("horn_id = ? AND status = ?", hornID, sectHornDeliverySent).Count(&sentCount).Error; err != nil {
		markSectHornBroadcastFailed(hornID, fmt.Sprintf("统计成功投递数失败: %s", formatPlainError(err)))
		log.Printf("⚠️ 喇叭成功投递统计失败: horn=%s err=%s", formatPlainValue(hornID), formatPlainError(err))
		return
	}
	if err := DB.Model(&SectHornDelivery{}).Where("horn_id = ? AND status = ?", hornID, sectHornDeliveryFailed).Count(&failedCount).Error; err != nil {
		markSectHornBroadcastFailed(hornID, fmt.Sprintf("统计失败投递数失败: %s", formatPlainError(err)))
		log.Printf("⚠️ 喇叭失败投递统计失败: horn=%s err=%s", formatPlainValue(hornID), formatPlainError(err))
		return
	}

	now := time.Now()
	completeRes := DB.Model(&SectHornBroadcast{}).
		Where("horn_id = ? AND status = ?", hornID, sectHornStatusSending).
		Updates(map[string]interface{}{
			"status":        sectHornStatusCompleted,
			"success_count": int(sentCount),
			"failed_count":  int(failedCount),
			"completed_at":  &now,
		})
	if completeRes.Error != nil {
		log.Printf("⚠️ 喇叭完成状态写入失败: horn=%s err=%s", formatPlainValue(hornID), formatPlainError(completeRes.Error))
		return
	}
	if completeRes.RowsAffected == 0 {
		log.Printf("SECT_HORN_BROADCAST_COMPLETE_STATE_CHANGED horn=%s", formatPlainValue(hornID))
		return
	}

	var broadcast SectHornBroadcast
	if err := DB.Where("horn_id = ?", hornID).First(&broadcast).Error; err != nil {
		log.Printf("⚠️ 喇叭完成回执读取广播失败: horn=%s err=%s", formatPlainValue(hornID), formatPlainError(err))
		return
	}

	receipt := fmt.Sprintf(
		"📣 %s送达完成。\n\n接收人数：%d\n成功送达：%d\n失败：%d\n\n失败通常表示对方尚未打开过 Bot、已屏蔽 Bot 或 Telegram 临时拒收；本次喇叭不自动退款。",
		sectHornTitle(broadcast.Scope),
		broadcast.RecipientCount,
		sentCount,
		failedCount,
	)
	msg := tgbotapi.NewMessage(broadcast.SenderID, receipt)
	if _, err := sendNoAutoDelete(bot, msg); err != nil {
		log.Printf("⚠️ 喇叭回执发送失败: horn=%s sender=%d err=%s", formatPlainValue(hornID), broadcast.SenderID, formatTelegramSendError(err))
	}
}

func markSectHornBroadcastFailed(hornID string, reason string) {
	reason = formatPlainValue(reason)
	now := time.Now()
	failRes := DB.Model(&SectHornBroadcast{}).
		Where("horn_id = ? AND status <> ?", hornID, sectHornStatusCompleted).
		Updates(map[string]interface{}{
			"status":       sectHornStatusFailed,
			"last_error":   reason,
			"completed_at": &now,
		})
	if failRes.Error != nil {
		log.Printf("⚠️ 喇叭失败状态写入失败: horn=%s err=%s", formatPlainValue(hornID), formatPlainError(failRes.Error))
		return
	}
	if failRes.RowsAffected == 0 {
		log.Printf("SECT_HORN_BROADCAST_FAILED_STATE_CHANGED horn=%s", formatPlainValue(hornID))
	}
}

func createSectHornBroadcast(tgUser *tgbotapi.User, scope string, rawContent string) (sectHornCreateResult, error) {
	if tgUser == nil {
		return sectHornCreateResult{}, errTelegramUserMissing
	}

	content, err := validateSectHornContent(rawContent)
	if err != nil {
		return sectHornCreateResult{}, err
	}

	now := time.Now()
	result := sectHornCreateResult{}

	err = DB.Transaction(func(tx *gorm.DB) error {
		var member SectMember
		if err := tx.Where("user_id = ?", tgUser.ID).First(&member).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errNotInSect
			}
			return err
		}
		if !canUpgradeSectAsset(member.Role) {
			return errSectNoPermission
		}

		var sect Sect
		if err := tx.Where("id = ?", member.SectID).First(&sect).Error; err != nil {
			return err
		}

		if err := checkSectHornCooldownTx(tx, scope, member.SectID, now); err != nil {
			return err
		}

		recipients, err := querySectHornRecipientsTx(tx, scope, member.SectID)
		if err != nil {
			return err
		}
		if len(recipients) == 0 {
			return errSectHornNoRecipients
		}

		cost := sectHornCostForScope(scope)
		txType := sectHornPointType(scope)
		hornID := generateSectHornID(scope, tgUser.ID)
		desc := fmt.Sprintf("使用%s，消耗 %d 积分", sectHornTitle(scope), cost)
		if scope == sectHornScopeSect {
			desc = fmt.Sprintf("在宗门【%s】使用宗门喇叭，消耗 %d 积分", sectHornPointDescriptionSectName(sect.Name), cost)
		} else if scope == sectHornScopeWorld {
			desc = fmt.Sprintf("代表宗门【%s】使用世界喇叭，消耗 %d 积分", sectHornPointDescriptionSectName(sect.Name), cost)
		}

		if err := applyPointDeltaInTx(
			tx,
			tgUser.ID,
			-cost,
			txType,
			desc,
			txType,
			hornID,
		); err != nil {
			return err
		}

		broadcast := SectHornBroadcast{
			HornID:         hornID,
			Scope:          scope,
			SectID:         member.SectID,
			SectName:       sect.Name,
			SenderID:       tgUser.ID,
			SenderName:     getTelegramNameForSect(tgUser),
			SenderRole:     member.Role,
			Cost:           cost,
			Content:        content,
			RecipientCount: len(recipients),
			Status:         sectHornStatusQueued,
			SubmittedAt:    now,
		}
		if err := createSectHornBroadcastInTx(tx, &broadcast); err != nil {
			return err
		}

		deliveries := make([]SectHornDelivery, 0, len(recipients))
		for _, recipient := range recipients {
			deliveries = append(deliveries, SectHornDelivery{
				HornID:   hornID,
				UserID:   recipient.UserID,
				UserName: recipient.UserName,
				Status:   sectHornDeliveryPending,
			})
		}
		if err := createSectHornDeliveriesInTx(tx, deliveries); err != nil {
			return err
		}

		txResult := sectHornCreateResult{
			HornID:         hornID,
			Scope:          scope,
			SectID:         member.SectID,
			SectName:       sect.Name,
			Cost:           cost,
			RecipientCount: len(recipients),
		}
		result = txResult
		return nil
	})

	if err != nil {
		return sectHornCreateResult{}, err
	}
	return result, nil
}

var (
	errSectHornInvalidScope = errors.New("SECT_HORN_INVALID_SCOPE")
	errSectHornNoRecipients = errors.New("SECT_HORN_NO_RECIPIENTS")
	errSectHornContentEmpty = errors.New("SECT_HORN_CONTENT_EMPTY")
	errSectHornContentShort = errors.New("SECT_HORN_CONTENT_SHORT")
	errSectHornContentLong  = errors.New("SECT_HORN_CONTENT_LONG")
	errSectHornControlChar  = errors.New("SECT_HORN_CONTROL_CHAR")
	errSectHornLinkBlocked  = errors.New("SECT_HORN_LINK_BLOCKED")
)

func validateSectHornContent(raw string) (string, error) {
	content := strings.TrimSpace(raw)
	if content == "" {
		return "", errSectHornContentEmpty
	}
	runeCount := len([]rune(content))
	if runeCount < sectHornContentMinRunes {
		return "", errSectHornContentShort
	}
	if runeCount > sectHornContentMaxRunes {
		return "", errSectHornContentLong
	}
	if containsDisallowedControl(content, false) {
		return "", errSectHornControlChar
	}
	if sectHornLinkPattern.MatchString(content) {
		return "", errSectHornLinkBlocked
	}
	return content, nil
}

func buildSectHornPreview(userID int64, scope string, content string, now time.Time) (sectHornPreview, error) {
	if scope != sectHornScopeSect && scope != sectHornScopeWorld {
		return sectHornPreview{}, errSectHornInvalidScope
	}

	var member SectMember
	if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return sectHornPreview{}, errNotInSect
		}
		return sectHornPreview{}, err
	}
	if !canUpgradeSectAsset(member.Role) {
		return sectHornPreview{}, errSectNoPermission
	}

	var sect Sect
	if err := DB.Where("id = ?", member.SectID).First(&sect).Error; err != nil {
		return sectHornPreview{}, err
	}

	if err := checkSectHornCooldownTx(DB, scope, member.SectID, now); err != nil {
		return sectHornPreview{}, err
	}

	recipients, err := querySectHornRecipientsTx(DB, scope, member.SectID)
	if err != nil {
		return sectHornPreview{}, err
	}
	if len(recipients) == 0 {
		return sectHornPreview{}, errSectHornNoRecipients
	}

	return sectHornPreview{
		Scope:          scope,
		SectID:         member.SectID,
		SectName:       sect.Name,
		SenderRole:     member.Role,
		Cost:           sectHornCostForScope(scope),
		RecipientCount: len(recipients),
		Content:        content,
	}, nil
}

func checkSectHornCooldownTx(tx *gorm.DB, scope string, sectID int64, now time.Time) error {
	switch scope {
	case sectHornScopeSect:
		return checkSectHornRecentTx(
			tx,
			"scope = ? AND sect_id = ?",
			[]interface{}{sectHornScopeSect, sectID},
			sectHornSectCooldown,
			now,
			"同一宗门宗门喇叭冷却中",
		)
	case sectHornScopeWorld:
		if err := checkSectHornRecentTx(
			tx,
			"scope = ? AND sect_id = ?",
			[]interface{}{sectHornScopeWorld, sectID},
			sectHornWorldSectCooldown,
			now,
			"同一宗门世界喇叭冷却中",
		); err != nil {
			return err
		}
		return checkSectHornRecentTx(
			tx,
			"scope = ?",
			[]interface{}{sectHornScopeWorld},
			sectHornWorldGlobalCooldown,
			now,
			"全服世界喇叭冷却中",
		)
	default:
		return errSectHornInvalidScope
	}
}

func checkSectHornRecentTx(tx *gorm.DB, where string, args []interface{}, cooldown time.Duration, now time.Time, text string) error {
	var last SectHornBroadcast
	statuses := []string{sectHornStatusQueued, sectHornStatusSending, sectHornStatusCompleted, sectHornStatusFailed}
	cutoff := now.Add(-cooldown)
	err := tx.
		Where(where, args...).
		Where("status IN ? AND created_at >= ?", statuses, cutoff).
		Order("created_at DESC").
		First(&last).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	return &sectHornCooldownError{
		Until: last.CreatedAt.Add(cooldown),
		Text:  text,
	}
}

func querySectHornRecipientsTx(tx *gorm.DB, scope string, sectID int64) ([]sectHornRecipient, error) {
	var recipients []sectHornRecipient
	switch scope {
	case sectHornScopeSect:
		err := tx.Table("sect_members AS sm").
			Select("sm.user_id AS user_id, COALESCE(NULLIF(u.username, ''), sm.user_name) AS user_name").
			Joins("JOIN users AS u ON u.telegram_id = sm.user_id AND u.deleted_at IS NULL").
			Where("sm.sect_id = ? AND sm.deleted_at IS NULL", sectID).
			Where("(u.status IS NULL OR u.status = '' OR u.status = ?) AND u.is_suspended = ?", "active", false).
			Order("sm.joined_at ASC").
			Scan(&recipients).Error
		return dedupeSectHornRecipients(recipients), err
	case sectHornScopeWorld:
		err := tx.Model(&User{}).
			Select("telegram_id AS user_id, username AS user_name").
			Where("abs_user_id <> ''").
			Where("(status IS NULL OR status = '' OR status = ?) AND is_suspended = ?", "active", false).
			Order("telegram_id ASC").
			Scan(&recipients).Error
		return dedupeSectHornRecipients(recipients), err
	default:
		return nil, errSectHornInvalidScope
	}
}

func dedupeSectHornRecipients(recipients []sectHornRecipient) []sectHornRecipient {
	if len(recipients) <= 1 {
		return recipients
	}
	seen := make(map[int64]bool, len(recipients))
	deduped := make([]sectHornRecipient, 0, len(recipients))
	for _, recipient := range recipients {
		if recipient.UserID == 0 || seen[recipient.UserID] {
			continue
		}
		seen[recipient.UserID] = true
		deduped = append(deduped, recipient)
	}
	return deduped
}

func sectHornCostForScope(scope string) int {
	if scope == sectHornScopeWorld {
		return worldHornCost
	}
	return sectHornCost
}

func sectHornPointType(scope string) string {
	if scope == sectHornScopeWorld {
		return "world_horn"
	}
	return "sect_horn"
}

func sectHornTitle(scope string) string {
	if scope == sectHornScopeWorld {
		return "世界喇叭"
	}
	return "宗门喇叭"
}

func sectHornConfirmText(scope string) string {
	if scope == sectHornScopeWorld {
		return "确认世界喇叭"
	}
	return "确认宗门喇叭"
}

func sectHornUsageText(scope string) string {
	if scope == sectHornScopeWorld {
		return fmt.Sprintf("用法：世界喇叭 内容\n消耗：%d 积分\n限制：宗主或长老可用，正文 %d-%d 字，暂不支持链接。", worldHornCost, sectHornContentMinRunes, sectHornContentMaxRunes)
	}
	return fmt.Sprintf("用法：宗门喇叭 内容\n消耗：%d 积分\n限制：宗主或长老可用，正文 %d-%d 字，暂不支持链接。", sectHornCost, sectHornContentMinRunes, sectHornContentMaxRunes)
}

func formatSectHornPreview(preview sectHornPreview) string {
	return fmt.Sprintf(
		"📣 %s预览\n\n宗门：%s\n职位：%s\n预计接收：%d 人\n消耗积分：%d\n\n正文：\n%s\n\n确认发送请回复：%s\n取消请回复：取消",
		sectHornTitle(preview.Scope),
		preview.SectName,
		getSectRoleText(preview.SenderRole),
		preview.RecipientCount,
		preview.Cost,
		preview.Content,
		sectHornConfirmText(preview.Scope),
	)
}

func formatSectHornDeliveryMessage(broadcast SectHornBroadcast) string {
	if broadcast.Scope == sectHornScopeWorld {
		return fmt.Sprintf(
			"🌍 世界喇叭｜%s\n来自：%s（%s）\n\n%s",
			broadcast.SectName,
			broadcast.SenderName,
			getSectRoleText(broadcast.SenderRole),
			broadcast.Content,
		)
	}

	return fmt.Sprintf(
		"📣 宗门喇叭｜%s\n来自：%s（%s）\n\n%s",
		broadcast.SectName,
		broadcast.SenderName,
		getSectRoleText(broadcast.SenderRole),
		broadcast.Content,
	)
}

func sectHornValidationMessage(err error) string {
	switch {
	case errors.Is(err, errSectHornContentEmpty):
		return "正文不能为空。"
	case errors.Is(err, errSectHornContentShort):
		return fmt.Sprintf("正文至少需要 %d 个字。", sectHornContentMinRunes)
	case errors.Is(err, errSectHornContentLong):
		return fmt.Sprintf("正文最多 %d 个字。", sectHornContentMaxRunes)
	case errors.Is(err, errSectHornControlChar):
		return "正文不能包含换行、制表符或其他控制字符。"
	case errors.Is(err, errSectHornLinkBlocked):
		return "第一版喇叭暂不支持链接，请去掉网址或群链接。"
	default:
		return "正文格式不正确。"
	}
}

func sectHornActionErrorText(err error) string {
	var cooldownErr *sectHornCooldownError
	switch {
	case errors.As(err, &cooldownErr):
		remaining := time.Until(cooldownErr.Until)
		if remaining < 0 {
			remaining = 0
		}
		minutes := int(remaining.Minutes())
		if minutes < 1 && remaining > 0 {
			minutes = 1
		}
		return fmt.Sprintf("⏳ %s，还需等待约 %d 分钟。", cooldownErr.Text, minutes)
	case errors.Is(err, errNotInSect):
		return "❌ 您当前没有加入宗门。"
	case errors.Is(err, errSectNoPermission):
		return "❌ 只有宗主或长老可以使用喇叭。"
	case errors.Is(err, errPointsNotEnough):
		return "❌ 您的积分不足，喇叭发送失败。"
	case errors.Is(err, errUserNotFound):
		return "❌ 未检测到您的积分账户，请先签到或完成注册绑定。"
	case errors.Is(err, errSectHornNoRecipients):
		return "❌ 当前没有可通知的目标道友。"
	case errors.Is(err, errSectHornInvalidScope):
		return "❌ 喇叭类型不正确。"
	case errors.Is(err, errSectHornContentEmpty),
		errors.Is(err, errSectHornContentShort),
		errors.Is(err, errSectHornContentLong),
		errors.Is(err, errSectHornControlChar),
		errors.Is(err, errSectHornLinkBlocked):
		return "❌ " + sectHornValidationMessage(err)
	default:
		log.Printf("❌ 喇叭操作失败: err=%s", formatPlainError(err))
		return "❌ 喇叭发送失败，请稍后重试。"
	}
}

func generateSectHornID(scope string, userID int64) string {
	prefix := "sect"
	if scope == sectHornScopeWorld {
		prefix = "world"
	}
	return fmt.Sprintf("%s-horn-%d-%s", prefix, userID, generateRandomCode(10))
}
