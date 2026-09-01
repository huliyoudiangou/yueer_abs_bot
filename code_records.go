package main

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const renewCodeSourcePointExchange = "point_exchange"

func createRenewCodeRecord(tx *gorm.DB, rawCode string, days int) error {
	return createRenewCodeRecordWithMeta(tx, rawCode, days, "", 0)
}

func createRenewCodeRecordWithMeta(tx *gorm.DB, rawCode string, days int, source string, ownerUserID int64) error {
	if tx == nil {
		tx = DB
	}

	codeHash := hashSensitiveToken(rawCode)
	if codeHash == "" {
		return errSecurityPepperNotConfigured
	}

	res := tx.Create(&RenewCode{
		Code:        "internal-renew-" + generateRandomCode(16),
		CodeHash:    codeHash,
		CodePreview: maskSecret(rawCode),
		Days:        days,
		Source:      strings.TrimSpace(source),
		OwnerUserID: ownerUserID,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("RENEW_CODE_CREATE_MISSED")
	}
	return nil
}

func createInviteCodeRecord(tx *gorm.DB, rawCode string) error {
	if tx == nil {
		tx = DB
	}

	codeHash := hashSensitiveToken(rawCode)
	if codeHash == "" {
		return errSecurityPepperNotConfigured
	}

	res := tx.Create(&InviteCode{
		Code:        "internal-invite-" + generateRandomCode(16),
		CodeHash:    codeHash,
		CodePreview: maskSecret(rawCode),
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("INVITE_CODE_CREATE_MISSED")
	}
	return nil
}

type exchangeInviteUseMode string

const (
	exchangeInviteUseNone     exchangeInviteUseMode = ""
	exchangeInviteUseTrial    exchangeInviteUseMode = "trial"
	exchangeInviteUseRegister exchangeInviteUseMode = "register"
)

func determineExchangeInviteUseMode(userID int64) (exchangeInviteUseMode, error) {
	var u User
	err := DB.Where("telegram_id = ?", userID).First(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return exchangeInviteUseRegister, nil
	}
	if err != nil {
		return exchangeInviteUseNone, err
	}
	if isTrialAccount(u) {
		return exchangeInviteUseTrial, nil
	}
	if strings.TrimSpace(u.AbsUserID) == "" {
		return exchangeInviteUseRegister, nil
	}
	return exchangeInviteUseNone, nil
}

type renewRedeemResult struct {
	Days           int
	NewExpireAt    time.Time
	AbsUserID      string
	NeedReactivate bool
}

func redeemRenewCodeByHash(userID int64, renewHash string) (renewRedeemResult, error) {
	var result renewRedeemResult
	if strings.TrimSpace(renewHash) == "" {
		return result, errSecurityPepperNotConfigured
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		var rCode RenewCode
		if err := tx.Where("code_hash = ? AND is_used = ?", renewHash, false).First(&rCode).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errInvalidRenewCode
			}
			return err
		}

		if rCode.Source == renewCodeSourcePointExchange && rCode.OwnerUserID != 0 && rCode.OwnerUserID != userID {
			return errRenewCodeOwnerMismatch
		}

		var u User
		if err := tx.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserNotFound
			}
			return err
		}
		if isTrialAccount(u) {
			return errTrialCannotUseRenewCode
		}

		res := tx.Model(&RenewCode{}).
			Where("id = ? AND is_used = ?", rCode.ID, false).
			Updates(map[string]interface{}{
				"is_used":    true,
				"used_by_id": userID,
			})

		if res.Error != nil {
			return res.Error
		}

		if res.RowsAffected == 0 {
			return errInvalidRenewCode
		}

		now := time.Now()
		if u.ExpireAt == nil || u.ExpireAt.Before(now) {
			result.NewExpireAt = now.AddDate(0, 0, rCode.Days)
		} else {
			result.NewExpireAt = u.ExpireAt.AddDate(0, 0, rCode.Days)
		}

		userRes := tx.Model(&User{}).
			Where("id = ? AND telegram_id = ? AND account_type <> ?", u.ID, userID, accountTypeTrial).
			Update("expire_at", result.NewExpireAt)
		if userRes.Error != nil {
			return userRes.Error
		}
		if userRes.RowsAffected == 0 {
			return fmt.Errorf("RENEW_USER_STATE_CHANGED")
		}

		if err := writeAuditLogInTx(
			tx,
			userID,
			"USE_RENEW_CODE",
			fmt.Sprintf("renew_code_id=%d", rCode.ID),
			0,
			fmt.Sprintf("user %s(%d) used renew code %s for %d days; expire_at=%s; need_reactivate=%t",
				formatPlainValue(u.Username), userID, formatPlainValue(rCode.CodePreview), rCode.Days, result.NewExpireAt.Format(time.RFC3339), u.IsSuspended && u.AbsUserID != ""),
		); err != nil {
			return err
		}

		result.Days = rCode.Days
		result.AbsUserID = u.AbsUserID
		result.NeedReactivate = u.IsSuspended && u.AbsUserID != ""

		return nil
	})
	if err != nil {
		return renewRedeemResult{}, err
	}
	return result, nil
}

func sendRenewRedeemResult(bot *tgbotapi.BotAPI, chatID int64, userID int64, result renewRedeemResult) {
	if result.NeedReactivate {
		if err := absClient.SetUserActiveStatus(result.AbsUserID, true); err != nil {
			log.Printf("⚠️ 续期后 ABS 解封失败: user=%d abs=%s err=%s", userID, formatPlainValue(result.AbsUserID), formatPlainError(err))
			auditErr := writeAuditLogInTx(DB, userID, "RENEW_REACTIVATE_USER_FAILED", fmt.Sprintf("%d", userID), 0,
				fmt.Sprintf("renew card extended account but ABS reactivation failed: tg=%d abs_user_id=%s expire_at=%s days=%d error=%s",
					userID, formatPlainValue(result.AbsUserID), result.NewExpireAt.Format(time.RFC3339), result.Days, formatPlainError(err)))
			if auditErr != nil {
				log.Printf("⚠️ 续期 ABS 解封失败审计写入失败: user=%d abs=%s err=%s", userID, formatPlainValue(result.AbsUserID), formatPlainError(auditErr))
				notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 用户续期已到账，但 ABS 恢复失败，且失败审计写入失败。\n用户：%d\nABS：%s\n到期：%s\n天数：%d\nABS错误：%s\n审计错误：%s", userID, formatPlainValue(result.AbsUserID), result.NewExpireAt.Format(time.RFC3339), result.Days, formatPlainError(err), formatPlainError(auditErr)))
			}

			replyText(bot, chatID, fmt.Sprintf(
				"⚠️ 续期已到账，新的到期时间为 `%s`。\n\nABS 解封暂时失败，系统已记录异常，请联系管理员处理。",
				result.NewExpireAt.Format("2006-01-02"),
			))
			return
		}

		if err := applyRenewReactivateLocalStatusWithAudit(userID, result.AbsUserID, result.NewExpireAt, result.Days); err != nil {
			log.Printf("⚠️ ABS 已解封，但本地解除封禁状态或审计写入失败: user=%d abs=%s err=%s", userID, formatPlainValue(result.AbsUserID), formatPlainError(err))
			auditErr := writeAuditLogInTx(DB, userID, "RENEW_REACTIVATE_USER_LOCAL_FAILED", fmt.Sprintf("%d", userID), 0,
				fmt.Sprintf("renew card reactivated ABS but local state/audit failed: tg=%d abs_user_id=%s expire_at=%s days=%d error=%s",
					userID, formatPlainValue(result.AbsUserID), result.NewExpireAt.Format(time.RFC3339), result.Days, formatPlainError(err)))
			if auditErr != nil {
				log.Printf("⚠️ 续期 ABS 已解封，但本地失败审计写入失败: user=%d abs=%s err=%s", userID, formatPlainValue(result.AbsUserID), formatPlainError(auditErr))
				notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 用户续期已到账且 ABS 已恢复，但本地权限状态或成功审计失败，且本地失败审计写入失败。\n用户：%d\nABS：%s\n到期：%s\n天数：%d\n本地错误：%s\n审计错误：%s\n请立即人工核查。", userID, formatPlainValue(result.AbsUserID), result.NewExpireAt.Format(time.RFC3339), result.Days, formatPlainError(err), formatPlainError(auditErr)))
				replyText(bot, chatID, fmt.Sprintf(
					"⚠️ 续期已到账，新的到期时间为 `%s`。\n\nABS 已恢复，但本地权限状态和失败审计写入均异常，已通知管理员人工核查。",
					result.NewExpireAt.Format("2006-01-02"),
				))
			} else {
				replyText(bot, chatID, fmt.Sprintf(
					"⚠️ 续期已到账，新的到期时间为 `%s`。\n\nABS 已恢复，但本地权限状态或审计写入失败，请联系管理员处理。",
					result.NewExpireAt.Format("2006-01-02"),
				))
			}
			return
		}
	}

	replyText(bot, chatID, fmt.Sprintf(
		"🎉 续费成功！延长 `%d` 天。\n📅 新到期时间：`%s`",
		result.Days,
		result.NewExpireAt.Format("2006-01-02"),
	))
}
