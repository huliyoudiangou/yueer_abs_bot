package main

import (
	"errors"
	"fmt"
	"log"
	"math"
	"strings"
	"time"
	"unicode"

	"gorm.io/gorm"
)

func getTodayAuditDeltaTotalTx(tx *gorm.DB, actorID int64, action string) (int, error) {
	startOfDay, endOfDay := auditDayRange(time.Now())

	var total int
	if err := tx.Model(&AuditLog{}).
		Where("actor_id = ? AND action = ? AND created_at >= ? AND created_at < ?", actorID, action, startOfDay, endOfDay).
		Select("COALESCE(SUM(ABS(delta)), 0)").
		Scan(&total).Error; err != nil {
		return 0, err
	}

	return total, nil
}

func validateAdminReason(text string) (string, bool) {
	reason := strings.TrimSpace(text)
	if len([]rune(reason)) < 5 {
		return "", false
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	if containsDisallowedControl(reason, false) {
		return "", false
	}
	if len([]rune(reason)) > 200 {
		reasonRunes := []rune(reason)
		reason = string(reasonRunes[:200])
	}
	return reason, true
}

const (
	adminReasonRequirementText = "至少 5 个字，且不能包含换行、制表符或其他控制/分隔字符"
	adminReasonInvalidText     = "原因不符合要求，请输入" + adminReasonRequirementText + "。"

	serverLinesMaxLen          = 4000
	serverLinesRequirementText = "1-4000 字，可换行，不能包含制表符或其他控制/分隔字符"
	serverLinesInvalidText     = "线路配置内容不符合要求，请输入" + serverLinesRequirementText + "。"

	securityCodeAttemptPurpose = "security_code"
	securityCodeMaxFailures    = 5
	securityCodeLockDuration   = 10 * time.Minute
)

type securityAttemptFailureState struct {
	FailCount   int
	LockedUntil *time.Time
	Message     string
}

func securityAttemptRemainingMinutes(lockedUntil time.Time, now time.Time) int {
	remaining := int(math.Ceil(lockedUntil.Sub(now).Minutes()))
	if remaining < 1 {
		return 1
	}
	return remaining
}

func nextSecurityAttemptFailureState(currentFailCount int, currentLockedUntil *time.Time, maxFailures int, lockDuration time.Duration, now time.Time, remainingFormat string, lockMessage string) (securityAttemptFailureState, error) {
	if maxFailures <= 0 || lockDuration <= 0 || remainingFormat == "" || lockMessage == "" {
		return securityAttemptFailureState{}, fmt.Errorf("SECURITY_ATTEMPT_INVALID")
	}
	if currentFailCount < 0 {
		currentFailCount = 0
	}

	if currentLockedUntil != nil && currentLockedUntil.After(now) {
		return securityAttemptFailureState{
			FailCount:   maxFailures,
			LockedUntil: currentLockedUntil,
			Message:     lockMessage,
		}, nil
	}

	failCount := currentFailCount + 1
	if currentLockedUntil != nil {
		failCount = 1
	}

	if failCount >= maxFailures {
		lockedUntil := now.Add(lockDuration)
		return securityAttemptFailureState{
			FailCount:   maxFailures,
			LockedUntil: &lockedUntil,
			Message:     lockMessage,
		}, nil
	}

	return securityAttemptFailureState{
		FailCount: failCount,
		Message:   fmt.Sprintf(remainingFormat, maxFailures-failCount),
	}, nil
}

func validateServerLinesContent(raw string) (string, bool) {
	content := strings.ReplaceAll(raw, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false
	}
	if len([]rune(content)) > serverLinesMaxLen {
		return "", false
	}
	for _, r := range content {
		if r != '\n' && unicode.IsControl(r) {
			return "", false
		}
	}
	if containsDisallowedControl(content, true) {
		return "", false
	}
	return content, true
}

func serverLinesMarkdownBody(raw string) string {
	content, ok := validateServerLinesContent(raw)
	if !ok {
		return "⚠️ 线路配置异常，请联系管理员更新。"
	}
	return escapeMarkdown(content)
}

func recordSecurityAttemptFailureInTx(tx *gorm.DB, userID int64, purpose string, maxFailures int, lockDuration time.Duration, now time.Time, remainingFormat string, lockMessage string) (string, error) {
	if tx == nil || userID == 0 || strings.TrimSpace(purpose) == "" || maxFailures <= 0 || lockDuration <= 0 {
		return "", fmt.Errorf("SECURITY_ATTEMPT_INVALID")
	}

	purpose = strings.TrimSpace(purpose)
	var attempt SecurityAttemptLock
	err := tx.Where("user_id = ? AND purpose = ?", userID, purpose).First(&attempt).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}

	if err == nil {
		return updateSecurityAttemptFailureInTx(tx, &attempt, maxFailures, lockDuration, now, remainingFormat, lockMessage)
	}

	state, err := nextSecurityAttemptFailureState(0, nil, maxFailures, lockDuration, now, remainingFormat, lockMessage)
	if err != nil {
		return "", err
	}

	create := SecurityAttemptLock{
		UserID:      userID,
		Purpose:     purpose,
		FailCount:   state.FailCount,
		LockedUntil: state.LockedUntil,
		LastFailAt:  &now,
	}
	res := tx.Create(&create)
	if res.Error != nil {
		if isUniqueConstraintError(res.Error) {
			var existing SecurityAttemptLock
			if readErr := tx.Where("user_id = ? AND purpose = ?", userID, purpose).First(&existing).Error; readErr != nil {
				return "", readErr
			}
			return updateSecurityAttemptFailureInTx(tx, &existing, maxFailures, lockDuration, now, remainingFormat, lockMessage)
		}
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "", fmt.Errorf("SECURITY_ATTEMPT_CREATE_MISSED")
	}
	return state.Message, nil
}

func updateSecurityAttemptFailureInTx(tx *gorm.DB, attempt *SecurityAttemptLock, maxFailures int, lockDuration time.Duration, now time.Time, remainingFormat string, lockMessage string) (string, error) {
	if tx == nil || attempt == nil {
		return "", fmt.Errorf("SECURITY_ATTEMPT_INVALID")
	}

	state, err := nextSecurityAttemptFailureState(attempt.FailCount, attempt.LockedUntil, maxFailures, lockDuration, now, remainingFormat, lockMessage)
	if err != nil {
		return "", err
	}

	res := tx.Model(&SecurityAttemptLock{}).
		Where("id = ? AND user_id = ? AND purpose = ?", attempt.ID, attempt.UserID, attempt.Purpose).
		Updates(map[string]interface{}{
			"fail_count":   state.FailCount,
			"locked_until": state.LockedUntil,
			"last_fail_at": &now,
		})
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "", fmt.Errorf("SECURITY_ATTEMPT_STATE_CHANGED")
	}
	return state.Message, nil
}

func verifyUserSecurityCodeWithCooldown(userID int64, input string, stored string) (bool, string) {
	return verifySensitiveTokenWithPersistentCooldown(userID, securityCodeAttemptPurpose, input, stored)
}

func verifySensitiveTokenWithPersistentCooldown(userID int64, purpose string, input string, stored string) (bool, string) {
	now := time.Now()

	// 数据库尚未初始化时保留极简兜底，避免异常路径 panic。
	if DB == nil {
		if verifySensitiveToken(input, stored) {
			return true, ""
		}
		return false, "❌ 安全码错误。"
	}

	ok := false
	message := ""

	err := DB.Transaction(func(tx *gorm.DB) error {
		var attempt SecurityAttemptLock
		err := tx.Where("user_id = ? AND purpose = ?", userID, purpose).First(&attempt).Error

		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		hasAttempt := err == nil

		if hasAttempt && attempt.LockedUntil != nil && attempt.LockedUntil.After(now) {
			remaining := securityAttemptRemainingMinutes(*attempt.LockedUntil, now)
			message = fmt.Sprintf("⏳ 安全码错误次数过多，请 %d 分钟后再试。", remaining)
			return nil
		}

		if verifySensitiveToken(input, stored) {
			ok = true

			if hasAttempt && (attempt.FailCount > 0 || attempt.LockedUntil != nil || attempt.LastFailAt != nil) {
				res := tx.Model(&SecurityAttemptLock{}).
					Where("id = ? AND user_id = ? AND purpose = ?", attempt.ID, attempt.UserID, attempt.Purpose).
					Updates(map[string]interface{}{
						"fail_count":   0,
						"locked_until": nil,
						"last_fail_at": nil,
					})
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					return nil
				}
				return nil
			}

			return nil
		}

		var recordErr error
		message, recordErr = recordSecurityAttemptFailureInTx(
			tx,
			userID,
			purpose,
			securityCodeMaxFailures,
			securityCodeLockDuration,
			now,
			"❌ 安全码错误。剩余尝试次数：%d",
			"⏳ 安全码错误次数过多，请 10 分钟后再试。",
		)
		return recordErr
	})

	if err != nil {
		log.Printf("⚠️ 安全码失败次数持久化异常: user=%d purpose=%s err=%s", userID, formatPlainValue(purpose), formatPlainError(err))

		// 数据库锁表异常时不放大故障：正确安全码仍允许通过，错误安全码拒绝。
		if verifySensitiveToken(input, stored) {
			return true, ""
		}
		return false, "❌ 安全码校验失败，请稍后重试。"
	}

	return ok, message
}
