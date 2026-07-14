package main

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const (
	openRegistrationModeQuota    = "quota"
	openRegistrationModeDuration = "duration"
	openRegistrationStatusActive = "active"
	openRegistrationStatusClosed = "closed"

	openRegistrationMaxQuota           = 10000
	openRegistrationMaxDurationMinutes = 10080

	openRegistrationReservationReserved  = "reserved"
	openRegistrationReservationCompleted = "completed"
	openRegistrationReservationReleased  = "released"

	compensationStatusQueued     = "queued"
	compensationStatusProcessing = "processing"
	compensationStatusCompleted  = "completed"

	compensationGrantPending        = "pending"
	compensationGrantApplied        = "applied"
	compensationGrantSent           = "sent"
	compensationGrantDeliveryFailed = "delivery_failed"

	compensationMaxPoints = 10000
	compensationMaxDays   = 365
)

var (
	errOpenRegistrationUnavailable = errors.New("OPEN_REGISTRATION_UNAVAILABLE")
	errOpenRegistrationFull        = errors.New("OPEN_REGISTRATION_FULL")
	errOpenRegistrationExpired     = errors.New("OPEN_REGISTRATION_EXPIRED")
	errOpenRegistrationState       = errors.New("OPEN_REGISTRATION_STATE_CHANGED")

	openRegistrationCreateMutex sync.Mutex
	compensationDispatchMutex   sync.Mutex
)

type OpenRegistrationCampaign struct {
	gorm.Model
	CampaignID string `gorm:"uniqueIndex;not null"`
	Mode       string `gorm:"index;not null"`
	Quota      int
	EndsAt     *time.Time `gorm:"index"`
	Status     string     `gorm:"index;not null"`
	CreatedBy  int64      `gorm:"index;not null"`
	Reason     string
	ClosedAt   *time.Time
}

func (OpenRegistrationCampaign) TableName() string { return "open_registration_campaigns" }

type OpenRegistrationReservation struct {
	gorm.Model
	CampaignID  string `gorm:"uniqueIndex:idx_open_registration_campaign_user;index;not null"`
	UserID      int64  `gorm:"uniqueIndex:idx_open_registration_campaign_user;index;not null"`
	Status      string `gorm:"index;not null"`
	ReservedAt  time.Time
	CompletedAt *time.Time
	ReleasedAt  *time.Time
}

func (OpenRegistrationReservation) TableName() string {
	return "open_registration_reservations"
}

type CompensationCampaign struct {
	gorm.Model
	CampaignID     string `gorm:"uniqueIndex;not null"`
	CreatedBy      int64  `gorm:"index;not null"`
	Points         int
	Days           int
	Announcement   string
	Status         string `gorm:"index;not null"`
	RecipientCount int
	AppliedCount   int
	SuccessCount   int
	FailedCount    int
	CreatedAtRun   time.Time `gorm:"index;not null"`
	CompletedAt    *time.Time
	LastError      string
}

func (CompensationCampaign) TableName() string { return "compensation_campaigns" }

type CompensationGrant struct {
	gorm.Model
	CampaignID           string `gorm:"uniqueIndex:idx_compensation_campaign_user;index;not null"`
	UserID               int64  `gorm:"uniqueIndex:idx_compensation_campaign_user;index;not null"`
	UserName             string
	Points               int
	Days                 int
	Status               string `gorm:"index;not null"`
	BalanceBefore        int
	BalanceAfter         int
	ExpireBefore         *time.Time
	ExpireAfter          *time.Time
	DaysApplied          int
	AppliedAt            *time.Time
	SentAt               *time.Time
	DeliveryError        string
	ReactivationRequired bool
	ReactivationOK       bool
	ReactivationError    string
	AttemptCount         int
	LastAttemptAt        *time.Time
	LastError            string
}

func (CompensationGrant) TableName() string { return "compensation_grants" }

func runServiceOperationsMigrations() {
	mustExecMigration(`
		CREATE UNIQUE INDEX IF NOT EXISTS idx_open_registration_single_active
		ON open_registration_campaigns(status)
		WHERE status = 'active' AND deleted_at IS NULL;
	`)
}

func closeExpiredOpenRegistrationInTx(tx *gorm.DB, now time.Time) error {
	if tx == nil {
		return fmt.Errorf("OPEN_REGISTRATION_TX_EMPTY")
	}
	return tx.Model(&OpenRegistrationCampaign{}).
		Where("status = ? AND mode = ? AND ends_at IS NOT NULL AND ends_at <= ?", openRegistrationStatusActive, openRegistrationModeDuration, now).
		Updates(map[string]interface{}{"status": openRegistrationStatusClosed, "closed_at": &now}).Error
}

func openRegistrationCampaignAvailability(campaign OpenRegistrationCampaign, used int64, now time.Time) error {
	switch campaign.Mode {
	case openRegistrationModeDuration:
		if campaign.EndsAt == nil || !now.Before(*campaign.EndsAt) {
			return errOpenRegistrationExpired
		}
	case openRegistrationModeQuota:
		if campaign.Quota < 1 {
			return errOpenRegistrationUnavailable
		}
		if used >= int64(campaign.Quota) {
			return errOpenRegistrationFull
		}
	default:
		return errOpenRegistrationUnavailable
	}
	return nil
}

func activeOpenRegistrationAt(db *gorm.DB, now time.Time) (OpenRegistrationCampaign, bool, error) {
	if db == nil {
		return OpenRegistrationCampaign{}, false, fmt.Errorf("OPEN_REGISTRATION_DB_EMPTY")
	}
	if err := closeExpiredOpenRegistrationInTx(db, now); err != nil {
		return OpenRegistrationCampaign{}, false, err
	}
	var campaign OpenRegistrationCampaign
	err := db.Where("status = ?", openRegistrationStatusActive).Order("id DESC").First(&campaign).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return OpenRegistrationCampaign{}, false, nil
	}
	if err != nil {
		return OpenRegistrationCampaign{}, false, err
	}
	var used int64
	if campaign.Mode == openRegistrationModeQuota {
		if err := db.Model(&OpenRegistrationReservation{}).
			Where("campaign_id = ? AND status IN ?", campaign.CampaignID, []string{openRegistrationReservationReserved, openRegistrationReservationCompleted}).
			Count(&used).Error; err != nil {
			return OpenRegistrationCampaign{}, false, err
		}
	}
	if err := openRegistrationCampaignAvailability(campaign, used, now); err != nil {
		if errors.Is(err, errOpenRegistrationFull) || errors.Is(err, errOpenRegistrationExpired) || errors.Is(err, errOpenRegistrationUnavailable) {
			return OpenRegistrationCampaign{}, false, nil
		}
		return OpenRegistrationCampaign{}, false, err
	}
	return campaign, true, nil
}

func reserveOpenRegistrationSlot(userID int64, campaignID string, now time.Time) (OpenRegistrationReservation, error) {
	if userID == 0 || strings.TrimSpace(campaignID) == "" {
		return OpenRegistrationReservation{}, errOpenRegistrationUnavailable
	}
	var reservation OpenRegistrationReservation
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := closeExpiredOpenRegistrationInTx(tx, now); err != nil {
			return err
		}
		var campaign OpenRegistrationCampaign
		if err := tx.Where("campaign_id = ? AND status = ?", campaignID, openRegistrationStatusActive).First(&campaign).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errOpenRegistrationUnavailable
			}
			return err
		}
		if campaign.Mode == openRegistrationModeDuration {
			if campaign.EndsAt == nil || !now.Before(*campaign.EndsAt) {
				return errOpenRegistrationExpired
			}
		}
		var existing OpenRegistrationReservation
		err := tx.Where("campaign_id = ? AND user_id = ?", campaignID, userID).First(&existing).Error
		if err == nil {
			if existing.Status == openRegistrationReservationReserved || existing.Status == openRegistrationReservationCompleted {
				reservation = existing
				return nil
			}
			res := tx.Model(&OpenRegistrationReservation{}).
				Where("id = ? AND status = ?", existing.ID, openRegistrationReservationReleased).
				Updates(map[string]interface{}{
					"status": openRegistrationReservationReserved, "reserved_at": now,
					"released_at": nil, "completed_at": nil,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errOpenRegistrationState
			}
			existing.Status = openRegistrationReservationReserved
			existing.ReservedAt = now
			reservation = existing
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			reservation = OpenRegistrationReservation{CampaignID: campaignID, UserID: userID, Status: openRegistrationReservationReserved, ReservedAt: now}
			if err := tx.Create(&reservation).Error; err != nil {
				return err
			}
		} else {
			return err
		}
		if campaign.Mode == openRegistrationModeQuota {
			var used int64
			if err := tx.Model(&OpenRegistrationReservation{}).
				Where("campaign_id = ? AND status IN ?", campaignID, []string{openRegistrationReservationReserved, openRegistrationReservationCompleted}).
				Count(&used).Error; err != nil {
				return err
			}
			if used > int64(campaign.Quota) {
				return errOpenRegistrationFull
			}
		}
		return nil
	})
	return reservation, err
}

func completeOpenRegistrationReservationInTx(tx *gorm.DB, reservationID uint, userID int64, now time.Time) error {
	if reservationID == 0 {
		return nil
	}
	res := tx.Model(&OpenRegistrationReservation{}).
		Where("id = ? AND user_id = ? AND status = ?", reservationID, userID, openRegistrationReservationReserved).
		Updates(map[string]interface{}{"status": openRegistrationReservationCompleted, "completed_at": &now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errOpenRegistrationState
	}
	return nil
}

func releaseOpenRegistrationReservation(reservationID uint, userID int64, reason string) error {
	if reservationID == 0 {
		return nil
	}
	now := time.Now()
	return DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&OpenRegistrationReservation{}).
			Where("id = ? AND user_id = ? AND status = ?", reservationID, userID, openRegistrationReservationReserved).
			Updates(map[string]interface{}{"status": openRegistrationReservationReleased, "released_at": &now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		return writeAuditLogInTx(tx, userID, "OPEN_REGISTRATION_RELEASE", fmt.Sprintf("reservation=%d", reservationID), 0, "registration slot released: "+formatPlainValue(reason))
	})
}

func createOpenRegistrationCampaign(actorID int64, mode string, value int, reason string, now time.Time) (OpenRegistrationCampaign, error) {
	openRegistrationCreateMutex.Lock()
	defer openRegistrationCreateMutex.Unlock()
	if mode != openRegistrationModeQuota && mode != openRegistrationModeDuration {
		return OpenRegistrationCampaign{}, fmt.Errorf("OPEN_REGISTRATION_MODE_INVALID")
	}
	if mode == openRegistrationModeQuota && (value < 1 || value > openRegistrationMaxQuota) {
		return OpenRegistrationCampaign{}, fmt.Errorf("OPEN_REGISTRATION_QUOTA_INVALID")
	}
	if mode == openRegistrationModeDuration && (value < 1 || value > openRegistrationMaxDurationMinutes) {
		return OpenRegistrationCampaign{}, fmt.Errorf("OPEN_REGISTRATION_DURATION_INVALID")
	}
	reason = strings.TrimSpace(reason)
	if reason == "" || len([]rune(reason)) > 300 {
		return OpenRegistrationCampaign{}, fmt.Errorf("OPEN_REGISTRATION_REASON_INVALID")
	}
	campaign := OpenRegistrationCampaign{CampaignID: "OPEN-" + generateRandomCode(12), Mode: mode, Status: openRegistrationStatusActive, CreatedBy: actorID, Reason: reason}
	if mode == openRegistrationModeQuota {
		campaign.Quota = value
	} else {
		ends := now.Add(time.Duration(value) * time.Minute)
		campaign.EndsAt = &ends
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := closeExpiredOpenRegistrationInTx(tx, now); err != nil {
			return err
		}
		var active int64
		if err := tx.Model(&OpenRegistrationCampaign{}).Where("status = ?", openRegistrationStatusActive).Count(&active).Error; err != nil {
			return err
		}
		if active > 0 {
			return fmt.Errorf("OPEN_REGISTRATION_ALREADY_ACTIVE")
		}
		if err := tx.Create(&campaign).Error; err != nil {
			return err
		}
		return writeAuditLogInTx(tx, actorID, "OPEN_REGISTRATION_CREATE", campaign.CampaignID, value,
			fmt.Sprintf("mode=%s value=%d reason=%s", mode, value, formatPlainValue(reason)))
	})
	return campaign, err
}

func closeOpenRegistrationCampaign(actorID int64, now time.Time) (bool, error) {
	openRegistrationCreateMutex.Lock()
	defer openRegistrationCreateMutex.Unlock()
	closed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		var campaign OpenRegistrationCampaign
		if err := tx.Where("status = ?", openRegistrationStatusActive).First(&campaign).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil
			}
			return err
		}
		res := tx.Model(&OpenRegistrationCampaign{}).Where("id = ? AND status = ?", campaign.ID, openRegistrationStatusActive).
			Updates(map[string]interface{}{"status": openRegistrationStatusClosed, "closed_at": &now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errOpenRegistrationState
		}
		closed = true
		return writeAuditLogInTx(tx, actorID, "OPEN_REGISTRATION_CLOSE", campaign.CampaignID, 0, "open registration closed manually")
	})
	return closed, err
}

func openRegistrationStatusText(now time.Time) string {
	campaign, ok, err := activeOpenRegistrationAt(DB, now)
	if err != nil {
		return "⚠️ 开放注册状态读取失败。"
	}
	if !ok {
		return "当前状态：未开放（仍按邀请码策略注册）"
	}
	if campaign.Mode == openRegistrationModeDuration {
		return fmt.Sprintf("当前状态：开放中\n模式：限时开放\n截止：%s", campaign.EndsAt.In(dailyOperationsLocation).Format("2006-01-02 15:04:05"))
	}
	var used int64
	if err := DB.Model(&OpenRegistrationReservation{}).Where("campaign_id = ? AND status IN ?", campaign.CampaignID, []string{openRegistrationReservationReserved, openRegistrationReservationCompleted}).Count(&used).Error; err != nil {
		return "⚠️ 开放注册进行中，但名额占用数读取失败，请稍后重试。"
	}
	return fmt.Sprintf("当前状态：开放中\n模式：限制人数\n已占用：%d / %d", used, campaign.Quota)
}

type compensationRecipient struct {
	TelegramID  int64
	Username    string
	AbsUserID   string
	ExpireAt    *time.Time
	IsSuspended bool
}

func createCompensationCampaign(actorID int64, points int, days int, announcement string, now time.Time) (CompensationCampaign, error) {
	if points < 0 || points > compensationMaxPoints || days < 0 || days > compensationMaxDays || (points == 0 && days == 0) {
		return CompensationCampaign{}, fmt.Errorf("COMPENSATION_AMOUNT_INVALID")
	}
	announcement = strings.TrimSpace(announcement)
	if announcement == "" || len([]rune(announcement)) > 1000 {
		return CompensationCampaign{}, fmt.Errorf("COMPENSATION_ANNOUNCEMENT_INVALID")
	}
	var recipients []compensationRecipient
	if err := DB.Model(&User{}).Select("telegram_id", "username", "abs_user_id", "expire_at", "is_suspended").Where("telegram_id <> ?", 0).Order("id ASC").Scan(&recipients).Error; err != nil {
		return CompensationCampaign{}, err
	}
	if len(recipients) == 0 {
		return CompensationCampaign{}, fmt.Errorf("COMPENSATION_NO_RECIPIENTS")
	}
	campaign := CompensationCampaign{CampaignID: "COMP-" + generateRandomCode(12), CreatedBy: actorID, Points: points, Days: days, Announcement: announcement, Status: compensationStatusQueued, RecipientCount: len(recipients), CreatedAtRun: now}
	grants := make([]CompensationGrant, 0, len(recipients))
	for _, u := range recipients {
		grants = append(grants, CompensationGrant{CampaignID: campaign.CampaignID, UserID: u.TelegramID, UserName: u.Username, Points: points, Days: days, Status: compensationGrantPending})
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&campaign).Error; err != nil {
			return err
		}
		if err := tx.CreateInBatches(&grants, 500).Error; err != nil {
			return err
		}
		return writeAuditLogInTx(tx, actorID, "CREATE_USER_COMPENSATION", campaign.CampaignID, points,
			fmt.Sprintf("recipients=%d points_each=%d days_each=%d announcement=%s", len(recipients), points, days, formatPlainValue(announcement)))
	})
	return campaign, err
}

func compensationExpireAt(expireAt *time.Time, now time.Time, days int, hasABS bool) (*time.Time, int) {
	if days <= 0 || !hasABS || expireAt == nil {
		return cloneTimePtr(expireAt), 0
	}
	base := *expireAt
	if base.Before(now) {
		base = now
	}
	next := base.AddDate(0, 0, days)
	return &next, days
}

func applyCompensationGrant(grantID uint, now time.Time) (CompensationGrant, error) {
	var out CompensationGrant
	err := DB.Transaction(func(tx *gorm.DB) error {
		var grant CompensationGrant
		if err := tx.Where("id = ?", grantID).First(&grant).Error; err != nil {
			return err
		}
		if grant.Status != compensationGrantPending {
			out = grant
			return nil
		}
		var user User
		if err := tx.Where("telegram_id = ?", grant.UserID).First(&user).Error; err != nil {
			return err
		}
		grant.BalanceBefore = user.Points
		if grant.Points > 0 {
			if err := applyPointDeltaInTx(tx, grant.UserID, grant.Points, "service_outage_compensation", fmt.Sprintf("服务异常统一补偿 %d 积分", grant.Points), "compensation_campaign", grant.CampaignID); err != nil {
				return err
			}
		}
		var refreshed User
		if err := tx.Where("telegram_id = ?", grant.UserID).First(&refreshed).Error; err != nil {
			return err
		}
		grant.BalanceAfter = refreshed.Points
		grant.ExpireBefore = cloneTimePtr(user.ExpireAt)
		grant.ExpireAfter, grant.DaysApplied = compensationExpireAt(user.ExpireAt, now, grant.Days, strings.TrimSpace(user.AbsUserID) != "")
		if grant.DaysApplied > 0 && grant.ExpireAfter != nil {
			res := tx.Model(&User{}).Where("id = ?", user.ID).Update("expire_at", grant.ExpireAfter)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return fmt.Errorf("COMPENSATION_EXPIRE_UPDATE_MISSED")
			}
			grant.ReactivationRequired = user.IsSuspended
		}
		grant.Status = compensationGrantApplied
		grant.AppliedAt = &now
		res := tx.Model(&CompensationGrant{}).Where("id = ? AND status = ?", grant.ID, compensationGrantPending).Updates(map[string]interface{}{
			"status": grant.Status, "balance_before": grant.BalanceBefore, "balance_after": grant.BalanceAfter,
			"expire_before": grant.ExpireBefore, "expire_after": grant.ExpireAfter, "days_applied": grant.DaysApplied,
			"applied_at": grant.AppliedAt, "reactivation_required": grant.ReactivationRequired,
			"last_error": "",
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("COMPENSATION_GRANT_STATE_CHANGED")
		}
		out = grant
		return nil
	})
	return out, err
}

func cloneTimePtr(v *time.Time) *time.Time {
	if v == nil {
		return nil
	}
	x := *v
	return &x
}

func compensationMessage(c CompensationCampaign, g CompensationGrant) string {
	lines := []string{"📢 【服务异常补偿通知】", "", c.Announcement, "", "本次补偿："}
	if g.Points > 0 {
		lines = append(lines, fmt.Sprintf("• 积分 +%d（当前余额 %d）", g.Points, g.BalanceAfter))
	}
	if g.Days > 0 {
		if g.DaysApplied > 0 && g.ExpireAfter != nil {
			lines = append(lines, fmt.Sprintf("• ABS 有效期 +%d 天（新到期日 %s）", g.DaysApplied, g.ExpireAfter.In(dailyOperationsLocation).Format("2006-01-02")))
		} else {
			lines = append(lines, "• ABS 天数：当前账号不适用（未绑定 ABS 或为无限期账号）")
		}
	}
	lines = append(lines, "", "给您带来的不便，敬请谅解。")
	return strings.Join(lines, "\n")
}

func StartCompensationDispatcher(bot *tgbotapi.BotAPI) {
	if bot == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		dispatchPendingCompensations(bot)
		for range ticker.C {
			dispatchPendingCompensations(bot)
		}
	}()
}

func dispatchPendingCompensations(bot *tgbotapi.BotAPI) {
	if !compensationDispatchMutex.TryLock() {
		return
	}
	defer compensationDispatchMutex.Unlock()
	var campaigns []CompensationCampaign
	if err := DB.Where("status IN ?", []string{compensationStatusQueued, compensationStatusProcessing}).Order("id ASC").Find(&campaigns).Error; err != nil {
		log.Printf("补偿活动扫描失败: %s", formatPlainError(err))
		return
	}
	for _, campaign := range campaigns {
		processCompensationCampaign(bot, campaign)
	}
}

func recordCompensationCampaignError(campaignID string, err error) {
	if strings.TrimSpace(campaignID) == "" || err == nil {
		return
	}
	message := truncateRunes(formatPlainError(err), 240)
	if updateErr := DB.Model(&CompensationCampaign{}).
		Where("campaign_id = ? AND status IN ?", campaignID, []string{compensationStatusQueued, compensationStatusProcessing}).
		Update("last_error", message).Error; updateErr != nil {
		log.Printf("补偿活动错误状态写入失败: campaign=%s err=%s", formatPlainValue(campaignID), formatPlainError(updateErr))
	}
}

func recordCompensationGrantFailure(grant CompensationGrant, err error, now time.Time) {
	if grant.ID == 0 || err == nil {
		return
	}
	message := truncateRunes(formatPlainError(err), 240)
	res := DB.Model(&CompensationGrant{}).
		Where("id = ? AND status = ?", grant.ID, compensationGrantPending).
		Updates(map[string]interface{}{
			"attempt_count":   gorm.Expr("attempt_count + 1"),
			"last_attempt_at": &now,
			"last_error":      message,
		})
	if res.Error != nil {
		log.Printf("补偿用户失败状态写入失败: campaign=%s user=%d err=%s", formatPlainValue(grant.CampaignID), grant.UserID, formatPlainError(res.Error))
	} else if res.RowsAffected == 0 {
		log.Printf("补偿用户失败状态未命中待处理记录: campaign=%s user=%d grant=%d", formatPlainValue(grant.CampaignID), grant.UserID, grant.ID)
	}
	recordCompensationCampaignError(grant.CampaignID, err)
}

func processCompensationCampaign(bot *tgbotapi.BotAPI, campaign CompensationCampaign) {
	if campaign.Status == compensationStatusQueued {
		res := DB.Model(&CompensationCampaign{}).
			Where("id = ? AND status = ?", campaign.ID, compensationStatusQueued).
			Update("status", compensationStatusProcessing)
		if res.Error != nil {
			log.Printf("补偿活动状态抢占失败: campaign=%s err=%s", formatPlainValue(campaign.CampaignID), formatPlainError(res.Error))
			recordCompensationCampaignError(campaign.CampaignID, res.Error)
			return
		}
		if res.RowsAffected == 0 {
			return
		}
	}

	var grants []CompensationGrant
	if err := DB.Where("campaign_id = ? AND status IN ?", campaign.CampaignID, []string{compensationGrantPending, compensationGrantApplied}).Order("id ASC").Find(&grants).Error; err != nil {
		log.Printf("补偿活动明细读取失败: campaign=%s err=%s", formatPlainValue(campaign.CampaignID), formatPlainError(err))
		recordCompensationCampaignError(campaign.CampaignID, err)
		return
	}
	for _, grant := range grants {
		current := grant
		if grant.Status == compensationGrantPending {
			attemptAt := time.Now()
			applied, err := applyCompensationGrant(grant.ID, attemptAt)
			if err != nil {
				log.Printf("补偿发放失败: campaign=%s user=%d err=%s", formatPlainValue(campaign.CampaignID), grant.UserID, formatPlainError(err))
				recordCompensationGrantFailure(grant, err, attemptAt)
				continue
			}
			current = applied
		}
		if current.ReactivationRequired && !current.ReactivationOK {
			var reactivateErr error
			var u User
			if err := DB.Where("telegram_id = ?", current.UserID).First(&u).Error; err != nil {
				reactivateErr = err
			} else if strings.TrimSpace(u.AbsUserID) == "" {
				reactivateErr = fmt.Errorf("COMPENSATION_ABS_USER_EMPTY")
			} else if err := absClient.SetUserActiveStatus(u.AbsUserID, true); err != nil {
				reactivateErr = err
			} else {
				reactivateErr = DB.Transaction(func(tx *gorm.DB) error {
					userRes := tx.Model(&User{}).
						Where("telegram_id = ? AND abs_user_id = ?", current.UserID, u.AbsUserID).
						Updates(map[string]interface{}{"is_suspended": false, "status": "active"})
					if userRes.Error != nil {
						return userRes.Error
					}
					if userRes.RowsAffected == 0 {
						return fmt.Errorf("COMPENSATION_REACTIVATE_LOCAL_STATE_CHANGED")
					}
					grantRes := tx.Model(&CompensationGrant{}).
						Where("id = ? AND reactivation_required = ?", current.ID, true).
						Updates(map[string]interface{}{"reactivation_ok": true, "reactivation_error": ""})
					if grantRes.Error != nil {
						return grantRes.Error
					}
					if grantRes.RowsAffected == 0 {
						return fmt.Errorf("COMPENSATION_REACTIVATE_GRANT_STATE_CHANGED")
					}
					return writeAuditLogInTx(tx, campaign.CreatedBy, "COMPENSATION_REACTIVATE_USER", fmt.Sprintf("%d", current.UserID), current.DaysApplied, "ABS account reactivated after service compensation")
				})
				if reactivateErr == nil {
					current.ReactivationOK = true
				}
			}
			if reactivateErr != nil {
				message := truncateRunes(formatPlainError(reactivateErr), 240)
				if err := DB.Model(&CompensationGrant{}).Where("id = ?", current.ID).Update("reactivation_error", message).Error; err != nil {
					log.Printf("补偿 ABS 恢复错误写入失败: campaign=%s user=%d err=%s", formatPlainValue(campaign.CampaignID), current.UserID, formatPlainError(err))
				}
				log.Printf("补偿 ABS 恢复失败: campaign=%s user=%d err=%s", formatPlainValue(campaign.CampaignID), current.UserID, formatPlainError(reactivateErr))
			}
		}

		msg := tgbotapi.NewMessage(current.UserID, compensationMessage(campaign, current))
		now := time.Now()
		if _, err := sendNoAutoDelete(bot, msg); err != nil {
			res := DB.Model(&CompensationGrant{}).Where("id = ? AND status = ?", current.ID, compensationGrantApplied).Updates(map[string]interface{}{"status": compensationGrantDeliveryFailed, "delivery_error": truncateRunes(formatTelegramSendError(err), 240)})
			if res.Error != nil || res.RowsAffected == 0 {
				log.Printf("补偿私聊失败状态写入异常: campaign=%s user=%d db_err=%s rows=%d", formatPlainValue(campaign.CampaignID), current.UserID, formatPlainError(res.Error), res.RowsAffected)
			}
		} else {
			res := DB.Model(&CompensationGrant{}).Where("id = ? AND status = ?", current.ID, compensationGrantApplied).Updates(map[string]interface{}{"status": compensationGrantSent, "sent_at": &now, "delivery_error": ""})
			if res.Error != nil || res.RowsAffected == 0 {
				log.Printf("补偿私聊成功状态写入异常: campaign=%s user=%d db_err=%s rows=%d", formatPlainValue(campaign.CampaignID), current.UserID, formatPlainError(res.Error), res.RowsAffected)
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err := completeCompensationCampaign(bot, campaign.CampaignID); err != nil {
		log.Printf("补偿活动完成收口失败: campaign=%s err=%s", formatPlainValue(campaign.CampaignID), formatPlainError(err))
		recordCompensationCampaignError(campaign.CampaignID, err)
	}
}

func completeCompensationCampaign(bot *tgbotapi.BotAPI, campaignID string) error {
	type compensationStatusCount struct {
		Status string
		Count  int64
	}
	var grouped []compensationStatusCount
	if err := DB.Model(&CompensationGrant{}).
		Select("status, COUNT(*) AS count").
		Where("campaign_id = ?", campaignID).
		Group("status").
		Scan(&grouped).Error; err != nil {
		return err
	}
	counts := make(map[string]int64, len(grouped))
	for _, item := range grouped {
		counts[item.Status] = item.Count
	}
	if counts[compensationGrantPending] > 0 || counts[compensationGrantApplied] > 0 {
		return nil
	}

	now := time.Now()
	var campaign CompensationCampaign
	completed := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("campaign_id = ?", campaignID).First(&campaign).Error; err != nil {
			return err
		}
		res := tx.Model(&CompensationCampaign{}).
			Where("id = ? AND status = ?", campaign.ID, compensationStatusProcessing).
			Updates(map[string]interface{}{
				"status":        compensationStatusCompleted,
				"applied_count": counts[compensationGrantSent] + counts[compensationGrantDeliveryFailed],
				"success_count": counts[compensationGrantSent],
				"failed_count":  counts[compensationGrantDeliveryFailed],
				"completed_at":  &now,
				"last_error":    "",
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		if err := writeAuditLogInTx(tx, campaign.CreatedBy, "COMPLETE_USER_COMPENSATION", campaignID, int(counts[compensationGrantSent]),
			fmt.Sprintf("recipients=%d delivered=%d delivery_failed=%d", campaign.RecipientCount, counts[compensationGrantSent], counts[compensationGrantDeliveryFailed])); err != nil {
			return err
		}
		completed = true
		return nil
	})
	if err != nil || !completed {
		return err
	}

	reactivateFailedText := "读取失败"
	var reactivateFailed int64
	if err := DB.Model(&CompensationGrant{}).
		Where("campaign_id = ? AND reactivation_required = ? AND reactivation_ok = ?", campaignID, true, false).
		Count(&reactivateFailed).Error; err != nil {
		log.Printf("补偿活动 ABS 恢复失败数读取失败: campaign=%s err=%s", formatPlainValue(campaignID), formatPlainError(err))
	} else {
		reactivateFailedText = strconv.FormatInt(reactivateFailed, 10)
	}
	sendPlainText(bot, campaign.CreatedBy, fmt.Sprintf("✅ 用户补偿已完成。\n\n活动：%s\n接收用户：%d\n成功私聊：%d\n私聊失败：%d\nABS 恢复失败：%s", campaignID, campaign.RecipientCount, counts[compensationGrantSent], counts[compensationGrantDeliveryFailed], reactivateFailedText))
	return nil
}
func HandleAdminServiceOperations(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState, role string) bool {
	if msg == nil || msg.From == nil || msg.Chat == nil || session == nil {
		return false
	}
	step := session.GetStep()
	isSession := strings.HasPrefix(step, "WAITING_OPEN_REG_") || strings.HasPrefix(step, "WAITING_COMPENSATION_")
	isCommand := text == "开放注册" || text == "用户补偿"
	if !isSession && !isCommand {
		return false
	}
	if role != "super_admin" {
		sendPlainText(bot, msg.Chat.ID, "❌ 该操作仅限超级管理员。")
		clearSession(msg.From.ID)
		return true
	}
	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "❌ 高危管理操作只能在私聊中执行。")
		clearSession(msg.From.ID)
		return true
	}

	if text == "开放注册" && !isSession {
		session.SetStep("WAITING_OPEN_REG_ACTION")
		UserSessions.Store(msg.From.ID, session)
		sendPlainText(bot, msg.Chat.ID, "👤 开放注册管理\n\n"+openRegistrationStatusText(time.Now())+"\n\n回复：\n1 - 按总人数开放\n2 - 按时长开放\n3 - 关闭当前开放注册\n取消 - 退出")
		return true
	}
	if text == "用户补偿" && !isSession {
		session.SetStep("WAITING_COMPENSATION_MODE")
		UserSessions.Store(msg.From.ID, session)
		sendPlainText(bot, msg.Chat.ID, "🛡 用户补偿（高危资产操作）\n\n回复：\n1 - 仅补偿积分\n2 - 仅补偿 ABS 天数\n3 - 积分和 ABS 天数都补偿\n取消 - 退出")
		return true
	}

	switch step {
	case "WAITING_OPEN_REG_ACTION":
		switch text {
		case "1":
			session.SetTemp("open_reg_mode", openRegistrationModeQuota)
			session.SetStep("WAITING_OPEN_REG_VALUE")
			sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("请输入开放注册总名额（1-%d）：", openRegistrationMaxQuota))
		case "2":
			session.SetTemp("open_reg_mode", openRegistrationModeDuration)
			session.SetStep("WAITING_OPEN_REG_VALUE")
			sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("请输入开放时长，单位为分钟（1-%d，最多 7 天）：", openRegistrationMaxDurationMinutes))
		case "3":
			session.SetStep("WAITING_OPEN_REG_CLOSE_CONFIRM")
			sendPlainText(bot, msg.Chat.ID, "⚠️ 确认立即关闭当前开放注册？\n请回复：确认关闭开放注册")
		default:
			sendPlainText(bot, msg.Chat.ID, "请回复 1、2、3，或取消。")
		}
		UserSessions.Store(msg.From.ID, session)
		return true
	case "WAITING_OPEN_REG_VALUE":
		v, err := strconv.Atoi(text)
		mode := session.GetTemp("open_reg_mode")
		max := openRegistrationMaxQuota
		if mode == openRegistrationModeDuration {
			max = openRegistrationMaxDurationMinutes
		}
		if err != nil || v < 1 || v > max {
			sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("请输入 1-%d 的整数：", max))
			return true
		}
		session.SetTemp("open_reg_value", strconv.Itoa(v))
		session.SetStep("WAITING_OPEN_REG_REASON")
		UserSessions.Store(msg.From.ID, session)
		sendPlainText(bot, msg.Chat.ID, "请输入本次开放注册原因（将写入审计，1-300 字）：")
		return true
	case "WAITING_OPEN_REG_REASON":
		reason := strings.TrimSpace(text)
		if reason == "" || len([]rune(reason)) > 300 {
			sendPlainText(bot, msg.Chat.ID, "原因需为 1-300 字，请重新输入：")
			return true
		}
		session.SetTemp("open_reg_reason", reason)
		session.SetStep("WAITING_OPEN_REG_CONFIRM")
		UserSessions.Store(msg.From.ID, session)
		modeText := "限制总人数"
		if session.GetTemp("open_reg_mode") == openRegistrationModeDuration {
			modeText = "限制开放时长（分钟）"
		}
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("⚠️ 请确认开放注册\n\n模式：%s\n数值：%s\n原因：%s\n\n开启后符合条件的用户无需邀请码。\n请回复：确认开启开放注册", modeText, session.GetTemp("open_reg_value"), reason))
		return true
	case "WAITING_OPEN_REG_CONFIRM":
		if text != "确认开启开放注册" {
			sendPlainText(bot, msg.Chat.ID, "请回复“确认开启开放注册”，或取消。")
			return true
		}
		v, _ := strconv.Atoi(session.GetTemp("open_reg_value"))
		campaign, err := createOpenRegistrationCampaign(msg.From.ID, session.GetTemp("open_reg_mode"), v, session.GetTemp("open_reg_reason"), time.Now())
		if err != nil {
			sendPlainText(bot, msg.Chat.ID, "❌ 开放注册开启失败："+formatPlainError(err))
			clearSession(msg.From.ID)
			return true
		}
		sendPlainText(bot, msg.Chat.ID, "✅ 开放注册已开启。\n活动编号："+campaign.CampaignID+"\n\n"+openRegistrationStatusText(time.Now()))
		clearSession(msg.From.ID)
		return true
	case "WAITING_OPEN_REG_CLOSE_CONFIRM":
		if text != "确认关闭开放注册" {
			sendPlainText(bot, msg.Chat.ID, "请回复“确认关闭开放注册”，或取消。")
			return true
		}
		closed, err := closeOpenRegistrationCampaign(msg.From.ID, time.Now())
		if err != nil {
			sendPlainText(bot, msg.Chat.ID, "❌ 关闭失败："+formatPlainError(err))
		} else if !closed {
			sendPlainText(bot, msg.Chat.ID, "ℹ️ 当前没有正在进行的开放注册。")
		} else {
			sendPlainText(bot, msg.Chat.ID, "✅ 当前开放注册已关闭，新用户恢复邀请码注册。")
		}
		clearSession(msg.From.ID)
		return true
	case "WAITING_COMPENSATION_MODE":
		if text != "1" && text != "2" && text != "3" {
			sendPlainText(bot, msg.Chat.ID, "请回复 1、2、3，或取消。")
			return true
		}
		session.SetTemp("comp_mode", text)
		if text == "1" || text == "3" {
			session.SetStep("WAITING_COMPENSATION_POINTS")
			sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("请输入每位用户统一补偿积分（1-%d）：", compensationMaxPoints))
		} else {
			session.SetTemp("comp_points", "0")
			session.SetStep("WAITING_COMPENSATION_DAYS")
			sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("请输入每位用户统一补偿 ABS 天数（1-%d）：", compensationMaxDays))
		}
		UserSessions.Store(msg.From.ID, session)
		return true
	case "WAITING_COMPENSATION_POINTS":
		v, err := strconv.Atoi(text)
		if err != nil || v < 1 || v > compensationMaxPoints {
			sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("请输入 1-%d 的整数：", compensationMaxPoints))
			return true
		}
		session.SetTemp("comp_points", strconv.Itoa(v))
		if session.GetTemp("comp_mode") == "3" {
			session.SetStep("WAITING_COMPENSATION_DAYS")
			sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("请输入每位用户统一补偿 ABS 天数（1-%d）：", compensationMaxDays))
		} else {
			session.SetTemp("comp_days", "0")
			session.SetStep("WAITING_COMPENSATION_ANNOUNCEMENT")
			sendPlainText(bot, msg.Chat.ID, "请输入事故播报说明（1-1000 字，将私发给所有用户）：")
		}
		UserSessions.Store(msg.From.ID, session)
		return true
	case "WAITING_COMPENSATION_DAYS":
		v, err := strconv.Atoi(text)
		if err != nil || v < 1 || v > compensationMaxDays {
			sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("请输入 1-%d 的整数：", compensationMaxDays))
			return true
		}
		session.SetTemp("comp_days", strconv.Itoa(v))
		session.SetStep("WAITING_COMPENSATION_ANNOUNCEMENT")
		UserSessions.Store(msg.From.ID, session)
		sendPlainText(bot, msg.Chat.ID, "请输入事故播报说明（1-1000 字，将私发给所有用户）：")
		return true
	case "WAITING_COMPENSATION_ANNOUNCEMENT":
		announcement := strings.TrimSpace(text)
		if announcement == "" || len([]rune(announcement)) > 1000 {
			sendPlainText(bot, msg.Chat.ID, "事故说明需为 1-1000 字，请重新输入：")
			return true
		}
		var count int64
		if err := DB.Model(&User{}).Where("telegram_id <> ?", 0).Count(&count).Error; err != nil {
			sendPlainText(bot, msg.Chat.ID, "❌ 用户数量读取失败，请稍后重试。")
			return true
		}
		points, _ := strconv.Atoi(session.GetTemp("comp_points"))
		days, _ := strconv.Atoi(session.GetTemp("comp_days"))
		session.SetTemp("comp_announcement", announcement)
		session.SetStep("WAITING_COMPENSATION_CONFIRM")
		UserSessions.Store(msg.From.ID, session)
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("⚠️ 高危资产操作确认\n\n接收用户：%d\n每人积分：%d\n每人 ABS 天数：%d\n预计新增总积分：%d\n事故说明：%s\n\n补偿一旦开始不可自动撤销。\n请回复：确认发放用户补偿", count, points, days, count*int64(points), announcement))
		return true
	case "WAITING_COMPENSATION_CONFIRM":
		if text != "确认发放用户补偿" {
			sendPlainText(bot, msg.Chat.ID, "请回复“确认发放用户补偿”，或取消。")
			return true
		}
		points, _ := strconv.Atoi(session.GetTemp("comp_points"))
		days, _ := strconv.Atoi(session.GetTemp("comp_days"))
		campaign, err := createCompensationCampaign(msg.From.ID, points, days, session.GetTemp("comp_announcement"), time.Now())
		if err != nil {
			sendPlainText(bot, msg.Chat.ID, "❌ 补偿任务创建失败："+formatPlainError(err))
			clearSession(msg.From.ID)
			return true
		}
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("✅ 补偿任务已进入队列。\n活动编号：%s\n接收用户：%d\n系统将逐人幂等发放并私聊通知，完成后回执。", campaign.CampaignID, campaign.RecipientCount))
		clearSession(msg.From.ID)
		go dispatchPendingCompensations(bot)
		return true
	}
	return false
}
