package main

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
)

func reserveInviteCodeForRegistrationWithAudit(userID int64, inviteHash string) (InviteCode, error) {
	if inviteHash == "" {
		return InviteCode{}, errInvalidInviteCode
	}

	var invite InviteCode
	err := DB.Transaction(func(tx *gorm.DB) error {
		var txInvite InviteCode
		if err := tx.Where("code_hash = ? AND is_used = ?", inviteHash, false).First(&txInvite).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errInvalidInviteCode
			}
			return err
		}

		res := tx.Model(&InviteCode{}).
			Where("id = ? AND is_used = ?", txInvite.ID, false).
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

		if err := writeAuditLogInTx(
			tx,
			userID,
			"RESERVE_INVITE_CODE",
			fmt.Sprintf("invite_code_id=%d", txInvite.ID),
			0,
			fmt.Sprintf("user %d reserved invite code %s for registration", userID, formatPlainValue(txInvite.CodePreview)),
		); err != nil {
			return err
		}
		invite = txInvite
		return nil
	})
	if err != nil {
		return InviteCode{}, err
	}
	return invite, nil
}

func releaseInviteCodeReservationWithAudit(userID int64, inviteHash string, reason string) error {
	if inviteHash == "" {
		return errInvalidInviteCode
	}

	return DB.Transaction(func(tx *gorm.DB) error {
		var invite InviteCode
		if err := tx.Where("code_hash = ? AND is_used = ? AND used_by_id = ?", inviteHash, true, userID).First(&invite).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errInvalidInviteCode
			}
			return err
		}

		res := tx.Model(&InviteCode{}).
			Where("id = ? AND is_used = ? AND used_by_id = ?", invite.ID, true, userID).
			Updates(map[string]interface{}{
				"is_used":    false,
				"used_by_id": 0,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errInvalidInviteCode
		}

		return writeAuditLogInTx(
			tx,
			userID,
			"RELEASE_INVITE_CODE",
			fmt.Sprintf("invite_code_id=%d", invite.ID),
			0,
			fmt.Sprintf("released invite code %s after registration compensation; reason: %s", formatPlainValue(invite.CodePreview), formatPlainValue(reason)),
		)
	})
}
