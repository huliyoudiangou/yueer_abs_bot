package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestRegistrationExpireAtForExistingUser(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	defaultExpireAt := now.AddDate(0, 0, 30)

	if got, update := registrationExpireAtForExistingUser(nil, &defaultExpireAt); !update || got == nil || !got.Equal(defaultExpireAt) {
		t.Fatalf("nil existing expiry should initialize default expiry, got=%v update=%t", got, update)
	}

	expired := now.AddDate(0, 0, -1)
	if got, update := registrationExpireAtForExistingUser(&expired, &defaultExpireAt); !update || got == nil || !got.Equal(defaultExpireAt) {
		t.Fatalf("expired existing expiry should be raised to default expiry, got=%v update=%t", got, update)
	}

	soon := now.AddDate(0, 0, 1)
	if got, update := registrationExpireAtForExistingUser(&soon, &defaultExpireAt); !update || got == nil || !got.Equal(defaultExpireAt) {
		t.Fatalf("short remaining expiry should be raised to default expiry, got=%v update=%t", got, update)
	}

	longer := now.AddDate(0, 0, 60)
	if got, update := registrationExpireAtForExistingUser(&longer, &defaultExpireAt); update || got != nil {
		t.Fatalf("longer existing expiry should be preserved, got=%v update=%t", got, update)
	}

	if got, update := registrationExpireAtForExistingUser(&defaultExpireAt, &defaultExpireAt); update || got != nil {
		t.Fatalf("same existing expiry should be preserved, got=%v update=%t", got, update)
	}
}

func TestRegistrationExpireAtForExistingUserPermanentDefault(t *testing.T) {
	now := time.Date(2026, 6, 15, 10, 0, 0, 0, time.UTC)
	existing := now.AddDate(0, 0, 30)

	if got, update := registrationExpireAtForExistingUser(nil, nil); update || got != nil {
		t.Fatalf("nil existing expiry and permanent default should need no update, got=%v update=%t", got, update)
	}

	if got, update := registrationExpireAtForExistingUser(&existing, nil); !update || got != nil {
		t.Fatalf("finite existing expiry should be cleared when default registration is permanent, got=%v update=%t", got, update)
	}
}

func TestRegistrationExistingUserUpdateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "WAITING_REG_PASS":`)
	if start < 0 {
		t.Fatal("WAITING_REG_PASS block missing")
	}
	end := strings.Index(text[start:], `case "WAITING_BIND_USER":`)
	if end < 0 {
		t.Fatal("WAITING_REG_PASS boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"userRes := tx.Model(&User{})",
		`Where("id = ? AND telegram_id = ?", existU.ID, userID)`,
		"userRes.RowsAffected == 0",
		"REGISTRATION_USER_STATE_CHANGED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("existing registration user update guard missing %q", want)
		}
	}
	if strings.Contains(block, "tx.Model(&existU).Updates(updates).Error") {
		t.Fatal("existing registration user update still ignores RowsAffected")
	}
}

func TestRegistrationInvitePrecheckDistinguishesReadErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "WAITING_REG_INVITE":`)
	if start < 0 {
		t.Fatal("WAITING_REG_INVITE block missing")
	}
	end := strings.Index(text[start:], `case "WAITING_TRIAL_FORMAL_INVITE":`)
	if end < 0 {
		t.Fatal("WAITING_REG_INVITE boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`DB.Where("code_hash = ? AND is_used = ?", inviteHash, false).First(&invite).Error`,
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"注册邀请码预校验读取失败",
		"formatPlainError(err)",
		"邀请码暂时读取失败",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("registration invite precheck read-error guard missing %q", want)
		}
	}
	if strings.Contains(block, `if err := DB.Where("code_hash = ? AND is_used = ?", inviteHash, false).First(&invite).Error; err != nil {
			replyText(bot, chatID,`) {
		t.Fatal("registration invite precheck still maps all read errors to invalid code")
	}
}

func TestRegistrationInviteReservationReturnValueOnlyAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("invite_registration.go")
	if err != nil {
		t.Fatalf("read invite_registration.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func reserveInviteCodeForRegistrationWithAudit(")
	if start < 0 {
		t.Fatal("reserveInviteCodeForRegistrationWithAudit missing")
	}
	end := strings.Index(text[start:], "func releaseInviteCodeReservationWithAudit(")
	if end < 0 {
		t.Fatal("reserveInviteCodeForRegistrationWithAudit boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"var txInvite InviteCode",
		`tx.Where("code_hash = ? AND is_used = ?", inviteHash, false).First(&txInvite).Error`,
		`Where("id = ? AND is_used = ?", txInvite.ID, false)`,
		`"RESERVE_INVITE_CODE"`,
		"invite = txInvite",
		"return InviteCode{}, err",
		"return invite, nil",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("registration invite reservation return guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"First(&invite).Error",
		"return invite, err",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("registration invite reservation still exposes transactional intermediate invite: %s", unsafe)
		}
	}
}

func TestRegistrationNewUserCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	helperStart := strings.Index(text, "func createRegisteredUserInTx(")
	if helperStart < 0 {
		t.Fatal("createRegisteredUserInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func hasActiveAbsAccount(")
	if helperEnd < 0 {
		t.Fatal("createRegisteredUserInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"entry := *user",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"REGISTERED_USER_CREATE_MISSED",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("registered user create guard missing %q", want)
		}
	}

	start := strings.Index(text, `case "WAITING_REG_PASS":`)
	if start < 0 {
		t.Fatal("WAITING_REG_PASS block missing")
	}
	end := strings.Index(text[start:], `case "WAITING_BIND_USER":`)
	if end < 0 {
		t.Fatal("WAITING_REG_PASS boundary missing")
	}
	block := text[start : start+end]
	if !strings.Contains(block, "createRegisteredUserInTx(tx, &user)") {
		t.Fatal("registration flow does not use createRegisteredUserInTx")
	}
	if strings.Contains(block, "tx.Create(&User{") {
		t.Fatal("registration flow still creates User directly")
	}
}

func TestEnsureUserWalletCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	start := strings.Index(text, "func ensureUserWalletInTx(")
	if start < 0 {
		t.Fatal("ensureUserWalletInTx missing")
	}
	end := strings.Index(text[start:], "func ensureUserWallet(")
	if end < 0 {
		t.Fatal("ensureUserWalletInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := tx.Create(&u)",
		"isUniqueConstraintError(res.Error)",
		"res.RowsAffected == 0",
		"USER_WALLET_CREATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("user wallet create guard missing %q", want)
		}
	}
	if strings.Contains(block, "tx.Create(&u).Error") {
		t.Fatal("ensureUserWalletInTx still checks only create error")
	}
}

func TestEnsureUserWalletReturnValueOnlyAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	start := strings.Index(text, "func ensureUserWallet(")
	if start < 0 {
		t.Fatal("ensureUserWallet missing")
	}
	end := strings.Index(text[start:], "func executeBlindBoxOpen(")
	if end < 0 {
		t.Fatal("ensureUserWallet boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"txUser, txDisplayName, innerErr := ensureUserWalletInTx(tx, tgUser)",
		"u = txUser",
		"displayName = txDisplayName",
		"if err != nil {\n\t\treturn User{}, \"\", err\n\t}",
		"return u, displayName, nil",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("ensureUserWallet return guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"u, displayName, innerErr = ensureUserWalletInTx(tx, tgUser)",
		"return u, displayName, err",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("ensureUserWallet still exposes transactional intermediate user: %s", unsafe)
		}
	}
}
