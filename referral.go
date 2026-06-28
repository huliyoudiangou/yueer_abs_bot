package main

import (
	"errors"
	"fmt"
	"log"
	"net/url"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const (
	accountTypeFormal = "formal"
	accountTypeTrial  = "trial"

	referralStartPrefix = "ref_"

	referralTrialDays          = 7
	referralTaskHours          = 10.0
	referralRewardPoints       = 10
	referralDailyActivationMax = 3
	referralMonthlyRewardMax   = 150
	referralMinInviterMajor    = 1
	referralMinInviterMinor    = 1

	referralStatusActive    = "active"
	referralStatusEffective = "effective"
)

type referralStats struct {
	Activated   int64
	Effective   int64
	MonthReward int
	MonthKey    string
}

var (
	errReferralInvalidCode        = errors.New("REFERRAL_INVALID_CODE")
	errReferralSelfInvite         = errors.New("REFERRAL_SELF_INVITE")
	errReferralInviterNotEligible = errors.New("REFERRAL_INVITER_NOT_ELIGIBLE")
	errReferralDailyLimit         = errors.New("REFERRAL_DAILY_LIMIT")
	errReferralAlreadyTried       = errors.New("REFERRAL_ALREADY_TRIED")
	errReferralExistingAccount    = errors.New("REFERRAL_EXISTING_ACCOUNT")
	errReferralNoActivation       = errors.New("REFERRAL_NO_ACTIVATION")
	errReferralTrialExpired       = errors.New("REFERRAL_TRIAL_EXPIRED")
	errReferralTaskNotComplete    = errors.New("REFERRAL_TASK_NOT_COMPLETE")
	errReferralAlreadyEffective   = errors.New("REFERRAL_ALREADY_EFFECTIVE")
	errReferralCultivationLow     = errors.New("REFERRAL_CULTIVATION_LOW")
	errTrialCannotUseRenewCode    = errors.New("TRIAL_CANNOT_USE_RENEW_CODE")
	errTrialFormalInviteOnly      = errors.New("TRIAL_FORMAL_INVITE_ONLY")
)

func normalizeAccountType(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return accountTypeFormal
	}
	return value
}

func isTrialAccount(u User) bool {
	return normalizeAccountType(u.AccountType) == accountTypeTrial
}

func isFormalAccount(u User) bool {
	return !isTrialAccount(u)
}

func referralInviterMeetsCultivationRequirement(major int, minor int) bool {
	if major > referralMinInviterMajor {
		return true
	}
	return major == referralMinInviterMajor && minor >= referralMinInviterMinor
}

func requireReferralInviterCultivationInTx(tx *gorm.DB, userID int64) error {
	var cul Cultivation
	if err := tx.Where("user_id = ?", userID).First(&cul).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errReferralCultivationLow
		}
		return err
	}
	if !referralInviterMeetsCultivationRequirement(cul.MajorRealm, cul.MinorRealm) {
		return errReferralCultivationLow
	}
	return nil
}

func referralDayBounds(t time.Time) (time.Time, time.Time, string) {
	loc := time.FixedZone("CST", 8*3600)
	local := t.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1), start.Format("2006-01-02")
}

func referralMonthKey(t time.Time) string {
	loc := time.FixedZone("CST", 8*3600)
	return t.In(loc).Format("200601")
}

func referralStatsText(stats referralStats) string {
	return fmt.Sprintf("累计激活：`%d`\n有效新人：`%d`\n本月奖励：`%d/%d` 积分", stats.Activated, stats.Effective, stats.MonthReward, referralMonthlyRewardMax)
}

func referralStatsUnavailableText() string {
	return fmt.Sprintf("累计激活：读取失败\n有效新人：读取失败\n本月奖励：读取失败/%d 积分", referralMonthlyRewardMax)
}

func loadReferralStats(inviterID int64, now time.Time) (referralStats, error) {
	stats := referralStats{MonthKey: referralMonthKey(now)}
	if err := DB.Model(&ReferralActivation{}).
		Where("inviter_id = ?", inviterID).
		Count(&stats.Activated).Error; err != nil {
		return stats, err
	}
	if err := DB.Model(&ReferralActivation{}).
		Where("inviter_id = ? AND status = ?", inviterID, referralStatusEffective).
		Count(&stats.Effective).Error; err != nil {
		return stats, err
	}
	if err := DB.Model(&ReferralActivation{}).
		Where("inviter_id = ? AND reward_month_key = ?", inviterID, stats.MonthKey).
		Select("COALESCE(SUM(reward_points), 0)").
		Scan(&stats.MonthReward).Error; err != nil {
		return stats, err
	}
	return stats, nil
}

func consumeReferralDailyActivationQuotaInTx(tx *gorm.DB, inviterID int64, dayKey string, now time.Time) error {
	res := tx.Exec(`
		INSERT INTO referral_daily_activation_quotas (
			created_at,
			updated_at,
			inviter_id,
			day_key,
			activation_count
		)
		VALUES (?, ?, ?, ?, 1)
		ON CONFLICT(inviter_id, day_key) DO UPDATE SET
			activation_count = referral_daily_activation_quotas.activation_count + 1,
			updated_at = excluded.updated_at
		WHERE referral_daily_activation_quotas.activation_count < ?
	`, now, now, inviterID, dayKey, referralDailyActivationMax)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errReferralDailyLimit
	}
	return nil
}

func consumeReferralMonthlyRewardQuotaInTx(tx *gorm.DB, inviterID int64, monthKey string, points int, now time.Time) (bool, error) {
	if points <= 0 {
		return false, nil
	}
	res := tx.Exec(`
		INSERT INTO referral_monthly_reward_quotas (
			created_at,
			updated_at,
			inviter_id,
			month_key,
			reward_points
		)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(inviter_id, month_key) DO UPDATE SET
			reward_points = referral_monthly_reward_quotas.reward_points + excluded.reward_points,
			updated_at = excluded.updated_at
		WHERE referral_monthly_reward_quotas.reward_points + excluded.reward_points <= ?
	`, now, now, inviterID, monthKey, points, referralMonthlyRewardMax)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func parseReferralStartPayload(text string) (string, bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) < 2 {
		return "", false
	}
	payload := strings.TrimSpace(fields[1])
	if !strings.HasPrefix(payload, referralStartPrefix) {
		return "", false
	}
	code := strings.TrimSpace(strings.TrimPrefix(payload, referralStartPrefix))
	return code, code != ""
}

func referralLink(bot *tgbotapi.BotAPI, code string) string {
	botName := ""
	if bot != nil {
		botName = strings.TrimSpace(bot.Self.UserName)
	}
	if botName == "" {
		return fmt.Sprintf("/start %s%s", referralStartPrefix, code)
	}
	return fmt.Sprintf("https://t.me/%s?start=%s", botName, url.QueryEscape(referralStartPrefix+code))
}

func ensureReferralCode(userID int64) (ReferralCode, error) {
	var code ReferralCode
	err := DB.Transaction(func(tx *gorm.DB) error {
		var u User
		if err := tx.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserNotFound
			}
			return err
		}
		if strings.TrimSpace(u.AbsUserID) == "" || !isFormalAccount(u) {
			return errReferralInviterNotEligible
		}
		if err := requireReferralInviterCultivationInTx(tx, userID); err != nil {
			return err
		}

		var txCode ReferralCode
		err := tx.Where("user_id = ?", userID).First(&txCode).Error
		if err == nil {
			if !txCode.IsEnabled {
				res := tx.Model(&ReferralCode{}).
					Where("id = ? AND user_id = ? AND is_enabled = ?", txCode.ID, userID, false).
					Update("is_enabled", true)
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					return fmt.Errorf("REFERRAL_CODE_STATE_CHANGED")
				}
				txCode.IsEnabled = true
			}
			code = txCode
			return nil
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		for i := 0; i < 8; i++ {
			candidate := generateRandomCode(10)
			txCode := ReferralCode{
				UserID:    userID,
				Code:      candidate,
				IsEnabled: true,
			}
			res := tx.Create(&txCode)
			if res.Error == nil && res.RowsAffected > 0 {
				code = txCode
				return nil
			}
			if res.Error == nil {
				return fmt.Errorf("CREATE_REFERRAL_CODE_MISSED")
			}
			if !isUniqueConstraintError(res.Error) {
				return res.Error
			}
		}
		return fmt.Errorf("CREATE_REFERRAL_CODE_FAILED")
	})
	if err != nil {
		return ReferralCode{}, err
	}
	return code, nil
}

func validateReferralCodeForStart(code string, inviteeID int64) error {
	var ref ReferralCode
	if err := DB.Where("code = ? AND is_enabled = ?", strings.TrimSpace(code), true).First(&ref).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return errReferralInvalidCode
	}
	if ref.UserID == inviteeID {
		return errReferralSelfInvite
	}
	if err := requireReferralInviterCultivationInTx(DB, ref.UserID); err != nil {
		if errors.Is(err, errReferralCultivationLow) {
			return errReferralInviterNotEligible
		}
		return err
	}
	return nil
}

func createReferralTrialUserInTx(tx *gorm.DB, user *User) error {
	if tx == nil || user == nil {
		return fmt.Errorf("REFERRAL_TRIAL_USER_INVALID")
	}
	entry := *user
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("REFERRAL_TRIAL_USER_CREATE_MISSED")
	}
	*user = entry
	return nil
}

func createReferralActivationInTx(tx *gorm.DB, activation *ReferralActivation) error {
	if tx == nil || activation == nil {
		return fmt.Errorf("REFERRAL_ACTIVATION_INVALID")
	}
	entry := *activation
	res := tx.Create(&entry)
	if res.Error != nil {
		if isUniqueConstraintError(res.Error) {
			return errReferralAlreadyTried
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("REFERRAL_ACTIVATION_CREATE_MISSED")
	}
	*activation = entry
	return nil
}

func createReferralTrialAccountInTx(tx *gorm.DB, inviteeID int64, username string, absUserID string, secCodeHash string, referralCode string, now time.Time) (time.Time, error) {
	referralCode = strings.TrimSpace(referralCode)
	if referralCode == "" {
		return time.Time{}, errReferralInvalidCode
	}

	var code ReferralCode
	if err := tx.Where("code = ? AND is_enabled = ?", referralCode, true).First(&code).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, err
		}
		return time.Time{}, errReferralInvalidCode
	}
	if code.UserID == inviteeID {
		return time.Time{}, errReferralSelfInvite
	}

	var inviter User
	if err := tx.Where("telegram_id = ?", code.UserID).First(&inviter).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return time.Time{}, err
		}
		return time.Time{}, errReferralInviterNotEligible
	}
	if strings.TrimSpace(inviter.AbsUserID) == "" || !isFormalAccount(inviter) {
		return time.Time{}, errReferralInviterNotEligible
	}
	if err := requireReferralInviterCultivationInTx(tx, code.UserID); err != nil {
		if errors.Is(err, errReferralCultivationLow) {
			return time.Time{}, errReferralInviterNotEligible
		}
		return time.Time{}, err
	}

	var existingActivation ReferralActivation
	if err := tx.Where("invitee_id = ?", inviteeID).First(&existingActivation).Error; err == nil {
		return time.Time{}, errReferralAlreadyTried
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return time.Time{}, err
	}

	_, _, activationDayKey := referralDayBounds(now)
	if err := consumeReferralDailyActivationQuotaInTx(tx, code.UserID, activationDayKey, now); err != nil {
		return time.Time{}, err
	}

	trialEndsAt := now.AddDate(0, 0, referralTrialDays)
	var existingUser User
	err := tx.Where("telegram_id = ?", inviteeID).First(&existingUser).Error
	if err == nil {
		if strings.TrimSpace(existingUser.AbsUserID) != "" {
			return time.Time{}, errReferralExistingAccount
		}
		updates := map[string]interface{}{
			"username":         username,
			"abs_user_id":      absUserID,
			"security_code":    secCodeHash,
			"status":           "active",
			"is_suspended":     false,
			"expire_at":        trialEndsAt,
			"account_type":     accountTypeTrial,
			"trial_started_at": now,
			"trial_ends_at":    trialEndsAt,
		}
		userRes := tx.Model(&User{}).
			Where("id = ? AND telegram_id = ? AND abs_user_id = ?", existingUser.ID, inviteeID, "").
			Updates(updates)
		if userRes.Error != nil {
			return time.Time{}, userRes.Error
		}
		if userRes.RowsAffected == 0 {
			return time.Time{}, fmt.Errorf("REFERRAL_TRIAL_USER_STATE_CHANGED")
		}
	} else if errors.Is(err, gorm.ErrRecordNotFound) {
		trialUser := User{
			TelegramID:     inviteeID,
			Username:       username,
			AbsUserID:      absUserID,
			SecurityCode:   secCodeHash,
			Status:         "active",
			ExpireAt:       &trialEndsAt,
			IsSuspended:    false,
			AccountType:    accountTypeTrial,
			TrialStartedAt: &now,
			TrialEndsAt:    &trialEndsAt,
		}
		if err := createReferralTrialUserInTx(tx, &trialUser); err != nil {
			return time.Time{}, err
		}
	} else {
		return time.Time{}, err
	}

	activation := ReferralActivation{
		CodeID:           code.ID,
		InviterID:        code.UserID,
		InviteeID:        inviteeID,
		Status:           referralStatusActive,
		TrialStartedAt:   now,
		TrialEndsAt:      trialEndsAt,
		ActivationDayKey: activationDayKey,
	}
	if err := createReferralActivationInTx(tx, &activation); err != nil {
		return time.Time{}, err
	}

	if err := writeAuditLogInTx(
		tx,
		inviteeID,
		"REFERRAL_TRIAL_REGISTER",
		fmt.Sprintf("referral_activation_id=%d", activation.ID),
		0,
		fmt.Sprintf("invitee=%d registered trial account by inviter=%d code_id=%d abs_user_id=%s trial_ends_at=%s",
			inviteeID, code.UserID, code.ID, formatPlainValue(absUserID), trialEndsAt.Format(time.RFC3339)),
	); err != nil {
		return time.Time{}, err
	}

	return trialEndsAt, nil
}

func convertTrialToFormalWithInviteCode(userID int64, inviteHash string) (time.Time, error) {
	var nextExpireAt time.Time
	err := DB.Transaction(func(tx *gorm.DB) error {
		var u User
		if err := tx.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserNotFound
			}
			return err
		}
		if !isTrialAccount(u) {
			return errTrialFormalInviteOnly
		}

		var invite InviteCode
		if err := tx.Where("code_hash = ? AND is_used = ?", inviteHash, false).First(&invite).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errInvalidInviteCode
			}
			return err
		}

		res := tx.Model(&InviteCode{}).
			Where("id = ? AND is_used = ?", invite.ID, false).
			Updates(map[string]interface{}{
				"is_used":    true,
				"used_by_id": userID,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errInvalidInviteCode
		}

		var defaultExpireAt *time.Time
		if AppConfig.AccountValidDays > 0 {
			exp := time.Now().AddDate(0, 0, AppConfig.AccountValidDays)
			defaultExpireAt = &exp
		}

		updates := map[string]interface{}{
			"account_type": accountTypeFormal,
		}
		var txNextExpireAt time.Time
		if next, shouldUpdate := registrationExpireAtForExistingUser(u.ExpireAt, defaultExpireAt); shouldUpdate {
			if next == nil {
				updates["expire_at"] = nil
			} else {
				updates["expire_at"] = next
				txNextExpireAt = *next
			}
		} else if u.ExpireAt != nil {
			txNextExpireAt = *u.ExpireAt
		}

		userRes := tx.Model(&User{}).
			Where("id = ? AND telegram_id = ? AND account_type = ? AND abs_user_id = ?", u.ID, userID, accountTypeTrial, u.AbsUserID).
			Updates(updates)
		if userRes.Error != nil {
			return userRes.Error
		}
		if userRes.RowsAffected == 0 {
			return fmt.Errorf("REFERRAL_TRIAL_CONVERT_USER_STATE_CHANGED")
		}

		if err := writeAuditLogInTx(
			tx,
			userID,
			"TRIAL_CONVERT_FORMAL",
			fmt.Sprintf("invite_code_id=%d", invite.ID),
			0,
			fmt.Sprintf("trial user %s(%d) converted to formal account with invite code %s; expire_at=%s",
				formatPlainValue(u.Username), userID, formatPlainValue(invite.CodePreview), txNextExpireAt.Format(time.RFC3339)),
		); err != nil {
			return err
		}

		if err := writeAuditLogInTx(
			tx,
			userID,
			"USE_INVITE_CODE",
			fmt.Sprintf("invite_code_id=%d", invite.ID),
			0,
			fmt.Sprintf("trial user %s(%d) used invite code %s for formal conversion",
				formatPlainValue(u.Username), userID, formatPlainValue(invite.CodePreview)),
		); err != nil {
			return err
		}
		nextExpireAt = txNextExpireAt
		return nil
	})
	if err != nil {
		return time.Time{}, err
	}
	return nextExpireAt, nil
}

func sumReferralTrialRawSeconds(userID int64, start time.Time, end time.Time) (float64, error) {
	if end.Before(start) {
		return 0, nil
	}
	startKey := sectDayKey(start)
	endExclusiveKey := sectDayKey(end.AddDate(0, 0, 1))

	var total float64
	err := DB.Model(&DailyListeningStat{}).
		Where("user_id = ? AND day_key >= ? AND day_key < ?", userID, startKey, endExclusiveKey).
		Select("COALESCE(SUM(capped_seconds), 0)").
		Scan(&total).Error
	return total, err
}

func capReferralTrialTaskSeconds(seconds float64, start time.Time, end time.Time, now time.Time) float64 {
	if seconds <= 0 {
		return 0
	}
	capEnd := end
	if now.Before(capEnd) {
		capEnd = now
	}
	if !capEnd.After(start) {
		return 0
	}
	maxSeconds := capEnd.Sub(start).Seconds()
	if seconds > maxSeconds {
		return maxSeconds
	}
	return seconds
}

func claimReferralTrialTask(userID int64, now time.Time) (float64, time.Time, int, bool, error) {
	var u User
	if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, time.Time{}, 0, false, errUserNotFound
		}
		return 0, time.Time{}, 0, false, err
	}
	if !isTrialAccount(u) || strings.TrimSpace(u.AbsUserID) == "" {
		return 0, time.Time{}, 0, false, errReferralNoActivation
	}

	var activation ReferralActivation
	if err := DB.Where("invitee_id = ?", userID).First(&activation).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, time.Time{}, 0, false, errReferralNoActivation
		}
		return 0, time.Time{}, 0, false, err
	}
	if activation.EffectiveAt != nil {
		exp := time.Time{}
		if u.ExpireAt != nil {
			exp = *u.ExpireAt
		}
		return activation.RawSecondsAtEffective, exp, activation.RewardPoints, false, errReferralAlreadyEffective
	}
	if now.After(activation.TrialEndsAt) {
		return 0, time.Time{}, 0, false, errReferralTrialExpired
	}

	if _, ok := refreshDailyListeningStatsFromABS(userID, u.AbsUserID); !ok {
		log.Printf("referral task refresh failed: user=%d abs=%s", userID, formatPlainValue(u.AbsUserID))
	}

	rawSeconds, err := sumReferralTrialRawSeconds(userID, activation.TrialStartedAt, activation.TrialEndsAt)
	if err != nil {
		return 0, time.Time{}, 0, false, err
	}
	rawSeconds = capReferralTrialTaskSeconds(rawSeconds, activation.TrialStartedAt, activation.TrialEndsAt, now)
	if rawSeconds < referralTaskHours*3600 {
		return rawSeconds, time.Time{}, 0, false, errReferralTaskNotComplete
	}

	var newExpireAt time.Time
	rewardPoints := 0
	rewardGranted := false
	err = DB.Transaction(func(tx *gorm.DB) error {
		var locked ReferralActivation
		if err := tx.Where("id = ?", activation.ID).First(&locked).Error; err != nil {
			return err
		}
		if locked.EffectiveAt != nil {
			return errReferralAlreadyEffective
		}

		var invitee User
		if err := tx.Where("telegram_id = ?", userID).First(&invitee).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserNotFound
			}
			return err
		}
		if !isTrialAccount(invitee) {
			return errReferralNoActivation
		}

		base := locked.TrialEndsAt
		if invitee.ExpireAt != nil && invitee.ExpireAt.After(base) {
			base = *invitee.ExpireAt
		}
		txNewExpireAt := base.AddDate(0, 0, referralTrialDays)
		effectiveAt := now
		_, _, rewardDayKey := referralDayBounds(now)
		rewardMonthKey := referralMonthKey(now)
		txRewardPoints := 0
		txRewardGranted := false

		quotaGranted, err := consumeReferralMonthlyRewardQuotaInTx(tx, locked.InviterID, rewardMonthKey, referralRewardPoints, now)
		if err != nil {
			return err
		}
		if quotaGranted {
			txRewardPoints = referralRewardPoints
			if err := applyPointDeltaInTx(
				tx,
				locked.InviterID,
				txRewardPoints,
				"referral_reward",
				fmt.Sprintf("邀请新人 %d 试用期内听书满 %.0f 小时", userID, referralTaskHours),
				"referral",
				fmt.Sprintf("%d", locked.ID),
			); err != nil {
				return err
			}
			txRewardGranted = true
		}

		inviteeRes := tx.Model(&User{}).
			Where("id = ? AND account_type = ? AND abs_user_id = ?", invitee.ID, accountTypeTrial, invitee.AbsUserID).
			Update("expire_at", txNewExpireAt)
		if inviteeRes.Error != nil {
			return inviteeRes.Error
		}
		if inviteeRes.RowsAffected == 0 {
			return fmt.Errorf("REFERRAL_TRIAL_INVITEE_STATE_CHANGED")
		}

		updates := map[string]interface{}{
			"status":                   referralStatusEffective,
			"effective_at":             effectiveAt,
			"extended_at":              effectiveAt,
			"raw_seconds_at_effective": rawSeconds,
			"reward_points":            txRewardPoints,
			"reward_day_key":           rewardDayKey,
			"reward_month_key":         rewardMonthKey,
		}
		if txRewardGranted {
			updates["rewarded_at"] = effectiveAt
		}
		activationRes := tx.Model(&ReferralActivation{}).
			Where("id = ? AND invitee_id = ? AND status = ? AND effective_at IS NULL", locked.ID, userID, referralStatusActive).
			Updates(updates)
		if activationRes.Error != nil {
			return activationRes.Error
		}
		if activationRes.RowsAffected == 0 {
			return fmt.Errorf("REFERRAL_TRIAL_ACTIVATION_STATE_CHANGED")
		}

		if err := writeAuditLogInTx(
			tx,
			userID,
			"REFERRAL_TRIAL_TASK_CLAIM",
			fmt.Sprintf("referral_activation_id=%d", locked.ID),
			0,
			fmt.Sprintf("invitee=%d completed referral trial task; raw_hours=%.2f extension_days=%d inviter=%d reward_points=%d expire_at=%s",
				userID, rawSeconds/3600.0, referralTrialDays, locked.InviterID, txRewardPoints, txNewExpireAt.Format(time.RFC3339)),
		); err != nil {
			return err
		}
		newExpireAt = txNewExpireAt
		rewardPoints = txRewardPoints
		rewardGranted = txRewardGranted
		return nil
	})
	if err != nil {
		return rawSeconds, time.Time{}, 0, false, err
	}
	return rawSeconds, newExpireAt, rewardPoints, rewardGranted, nil
}

func showMyReferral(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	code, err := ensureReferralCode(msg.From.ID)
	if err != nil {
		if errors.Is(err, errReferralInviterNotEligible) || errors.Is(err, errUserNotFound) {
			replyText(bot, msg.Chat.ID, "❌ 仅正式账号可生成个人邀请链接。新人体验账号需先使用正式邀请码转正。")
			return
		}
		if errors.Is(err, errReferralCultivationLow) {
			replyText(bot, msg.Chat.ID, "❌ 个人邀请链接需达到【炼气初期】后解锁。请先听书积累修为，并发送 `听书报告` 同步修仙档案。")
			return
		}
		log.Printf("create referral code failed: user=%d err=%s", msg.From.ID, formatPlainError(err))
		replyText(bot, msg.Chat.ID, "❌ 邀请链接生成失败，请稍后重试。")
		return
	}

	statsText := referralStatsUnavailableText()
	if stats, err := loadReferralStats(msg.From.ID, time.Now()); err != nil {
		log.Printf("load referral stats failed: user=%d err=%s", msg.From.ID, formatPlainError(err))
	} else {
		statsText = referralStatsText(stats)
	}

	replyText(bot, msg.Chat.ID, fmt.Sprintf(
		"🔗 **我的邀请链接**\n\n链接：`%s`\n\n规则：炼气初期及以上正式账号可邀请新人；新人通过链接注册后获得 `7` 天体验；体验期内听书满 `10` 小时可获得 `7` 天体验延期；邀请者获得 `10` 积分。\n\n限制：同一邀请链接每日最多激活 `3` 名新人；每月邀请奖励最多 `%d` 积分。\n\n%s",
		referralLink(bot, code.Code),
		referralMonthlyRewardMax,
		statsText,
	))
}

func handleReferralTaskCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	rawSeconds, newExpireAt, rewardPoints, rewardGranted, err := claimReferralTrialTask(msg.From.ID, time.Now())
	switch {
	case err == nil:
		rewardText := "邀请者本月奖励已达上限，本次不再发放积分。"
		if rewardGranted {
			rewardText = fmt.Sprintf("邀请者获得 `%d` 积分奖励。", rewardPoints)
		}
		replyText(bot, msg.Chat.ID, fmt.Sprintf(
			"✅ 新人任务完成。\n\n体验期内听书：`%.2f/10` 小时\n已获得 `7` 天新人体验延期。\n新的到期时间：`%s`\n%s",
			rawSeconds/3600.0,
			newExpireAt.Format("2006-01-02"),
			rewardText,
		))
	case errors.Is(err, errReferralTaskNotComplete):
		remain := referralTaskHours - rawSeconds/3600.0
		if remain < 0 {
			remain = 0
		}
		replyText(bot, msg.Chat.ID, fmt.Sprintf("📖 新人任务尚未完成。\n\n体验期内听书：`%.2f/10` 小时\n还需约 `%.2f` 小时。", rawSeconds/3600.0, remain))
	case errors.Is(err, errReferralAlreadyEffective):
		replyText(bot, msg.Chat.ID, "✅ 新人任务已领取过，体验延期不会重复发放。")
	case errors.Is(err, errReferralTrialExpired):
		replyText(bot, msg.Chat.ID, "⏳ 新人体验期已结束，无法再领取体验延期。")
	case errors.Is(err, errReferralNoActivation), errors.Is(err, errUserNotFound):
		replyText(bot, msg.Chat.ID, "❌ 未检测到可领取的新人邀请任务。")
	default:
		log.Printf("claim referral task failed: user=%d err=%s", msg.From.ID, formatPlainError(err))
		replyText(bot, msg.Chat.ID, "❌ 新人任务检查失败，请稍后重试。")
	}
}

func showReferralStats(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	statsText := referralStatsUnavailableText()
	if stats, err := loadReferralStats(msg.From.ID, time.Now()); err != nil {
		log.Printf("load referral stats failed: user=%d err=%s", msg.From.ID, formatPlainError(err))
	} else {
		statsText = referralStatsText(stats)
	}
	replyText(bot, msg.Chat.ID, "📊 **邀请统计**\n\n"+statsText)
}

func HandleReferralCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) bool {
	if msg == nil || msg.From == nil || msg.Chat == nil {
		return false
	}
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}

	if !msg.Chat.IsPrivate() {
		switch text {
		case "我的邀请", "邀请链接", "拉新链接", "邀请统计", "新人任务", "检查新人任务":
			sendPlainText(bot, msg.Chat.ID, "邀请链接和新人任务请私聊 Bot 执行。")
			return true
		default:
			return false
		}
	}

	switch text {
	case "我的邀请", "邀请链接", "拉新链接":
		showMyReferral(bot, msg)
		return true
	case "新人任务", "检查新人任务":
		handleReferralTaskCommand(bot, msg)
		return true
	case "邀请统计":
		showReferralStats(bot, msg)
		return true
	default:
		return false
	}
}
