package main

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

func bindLocalUserWithAudit(actorID int64, username string, absUserID string, securityCodeHash string, expireAt *time.Time) error {
	username = strings.TrimSpace(username)
	if username == "" {
		return fmt.Errorf("username_empty")
	}
	if absUserID == "" {
		return errAbsUserIDEmpty
	}
	if securityCodeHash == "" {
		return errSecurityPepperNotConfigured
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var target User
		err := tx.Where("telegram_id = ?", actorID).First(&target).Error
		switch {
		case err == nil:
			oldUsername := target.Username
			oldAbsUserID := target.AbsUserID
			expireInitialized := target.ExpireAt == nil

			updates := map[string]interface{}{
				"username":      username,
				"abs_user_id":   absUserID,
				"security_code": securityCodeHash,
				"status":        "active",
				"account_type":  accountTypeFormal,
			}
			if expireInitialized {
				updates["expire_at"] = expireAt
			}

			res := tx.Model(&User{}).
				Where("id = ? AND telegram_id = ?", target.ID, actorID).
				Updates(updates)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return fmt.Errorf("target_binding_changed")
			}

			return writeAuditLogInTx(
				tx,
				actorID,
				"BIND_USER",
				fmt.Sprintf("%d", actorID),
				0,
				fmt.Sprintf("updated local bind record old_username=%s old_abs_user_id=%s new_username=%s new_abs_user_id=%s expire_initialized=%t",
					formatPlainValue(oldUsername), formatPlainValue(oldAbsUserID), formatPlainValue(username), formatPlainValue(absUserID), expireInitialized),
			)
		case errors.Is(err, gorm.ErrRecordNotFound):
			user := User{
				TelegramID:   actorID,
				Username:     username,
				AbsUserID:    absUserID,
				SecurityCode: securityCodeHash,
				ExpireAt:     expireAt,
				Status:       "active",
				AccountType:  accountTypeFormal,
			}
			if err := createBoundLocalUserInTx(tx, &user); err != nil {
				return err
			}

			return writeAuditLogInTx(
				tx,
				actorID,
				"BIND_USER",
				fmt.Sprintf("%d", actorID),
				0,
				fmt.Sprintf("created local bind record username=%s abs_user_id=%s expire_initialized=%t",
					formatPlainValue(username), formatPlainValue(absUserID), expireAt != nil),
			)
		default:
			return err
		}
	})
}

// renameLocalUsernameWithAudit 在本地档案中把当前用户的用户名改为 newUsername。
// 调用前必须已完成安全码与密码校验，且已在 ABS 服务端成功改名。
// 事务内二次校验所有权与唯一性，避免并发场景下写坏数据。
func renameLocalUsernameWithAudit(actorID int64, oldUsername string, newUsername string, absUserID string) error {
	newUsername = strings.TrimSpace(newUsername)
	if newUsername == "" {
		return fmt.Errorf("username_empty")
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var target User
		if err := tx.Where("telegram_id = ?", actorID).First(&target).Error; err != nil {
			return err
		}
		if strings.TrimSpace(target.AbsUserID) == "" {
			return errAbsUserIDEmpty
		}
		if target.Username == newUsername {
			return errUsernameUnchanged
		}

		// 唯一性二次校验：用户名是登录凭证且带唯一索引，需排除自身后判断是否被他人占用。
		var conflictCount int64
		if err := tx.Model(&User{}).
			Where("username = ? AND telegram_id <> ?", newUsername, actorID).
			Count(&conflictCount).Error; err != nil {
			return err
		}
		if conflictCount > 0 {
			return errUsernameTaken
		}

		res := tx.Model(&User{}).
			Where("id = ? AND telegram_id = ? AND username = ?", target.ID, actorID, target.Username).
			Update("username", newUsername)
		if res.Error != nil {
			if isUniqueConstraintError(res.Error) {
				return errUsernameTaken
			}
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("target_username_changed")
		}

		return writeAuditLogInTx(
			tx,
			actorID,
			"RENAME_USERNAME",
			fmt.Sprintf("%d", actorID),
			0,
			fmt.Sprintf("renamed local username old_username=%s new_username=%s abs_user_id=%s",
				formatPlainValue(oldUsername), formatPlainValue(newUsername), formatPlainValue(absUserID)),
		)
	})
}

func createBoundLocalUserInTx(tx *gorm.DB, user *User) error {
	if tx == nil || user == nil {
		return fmt.Errorf("BOUND_USER_INVALID")
	}
	entry := *user
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("BOUND_USER_CREATE_MISSED")
	}
	*user = entry
	return nil
}

func rebindLocalUserWithAudit(actorID int64, targetID uint, expectedAbsUserID string) error {
	if targetID == 0 {
		return errUserNotFound
	}
	if expectedAbsUserID == "" {
		return errAbsUserIDEmpty
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var target User
		if err := tx.Where("id = ?", targetID).First(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserNotFound
			}
			return err
		}
		if target.AbsUserID != expectedAbsUserID {
			return fmt.Errorf("target_abs_user_changed")
		}

		oldTelegramID := target.TelegramID
		res := tx.Model(&User{}).
			Where("id = ? AND telegram_id = ? AND abs_user_id = ?", target.ID, oldTelegramID, expectedAbsUserID).
			Updates(map[string]interface{}{
				"telegram_id":  actorID,
				"status":       "active",
				"account_type": accountTypeFormal,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("target_binding_changed")
		}

		return writeAuditLogInTx(
			tx,
			actorID,
			"REBIND_USER",
			fmt.Sprintf("%d", actorID),
			0,
			fmt.Sprintf("rebound local user record username=%s abs_user_id=%s old_telegram_id=%d new_telegram_id=%d",
				formatPlainValue(target.Username), formatPlainValue(target.AbsUserID), oldTelegramID, actorID),
		)
	})
}

func unbindLocalUserWithAudit(actorID int64, expectedAbsUserID string) error {
	if expectedAbsUserID == "" {
		return errAbsUserIDEmpty
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var target User
		if err := tx.Where("telegram_id = ?", actorID).First(&target).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserNotFound
			}
			return err
		}
		if target.AbsUserID != expectedAbsUserID {
			return fmt.Errorf("target_abs_user_changed")
		}

		unboundTelegramID := -actorID
		res := tx.Model(&User{}).
			Where("id = ? AND telegram_id = ? AND abs_user_id = ?", target.ID, actorID, expectedAbsUserID).
			Updates(map[string]interface{}{
				"telegram_id": unboundTelegramID,
				"status":      "unbound",
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("target_binding_changed")
		}

		return writeAuditLogInTx(
			tx,
			actorID,
			"UNBIND_USER",
			fmt.Sprintf("%d", actorID),
			0,
			fmt.Sprintf("unbound local user record username=%s abs_user_id=%s old_telegram_id=%d new_telegram_id=%d",
				formatPlainValue(target.Username), formatPlainValue(target.AbsUserID), actorID, unboundTelegramID),
		)
	})
}
