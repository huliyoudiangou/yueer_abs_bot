package main

import (
	"strings"
	"testing"
	"time"
)

func TestAccountStatusDisplayListeningAbuseFreeze(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.Local)
	end := now.Add(2 * time.Hour)
	active := true

	display := buildAccountStatusDisplay(accountStatusDisplayInput{
		User: User{
			TelegramID: 1,
			Username:   "alice",
			AbsUserID:  "abs-1",
			Status:     "active",
		},
		Now:          now,
		Mode:         accountStatusDisplaySelf,
		ActiveFreeze: &ListeningAbuseRecord{FreezeEndAt: &end},
		AbsActive:    &active,
	})

	if display.Kind != accountStatusListeningAbuseFreeze {
		t.Fatalf("kind = %s, want %s", display.Kind, accountStatusListeningAbuseFreeze)
	}
	if display.LocalAllowsAccess {
		t.Fatal("listening abuse freeze should not allow local access")
	}
	if !strings.Contains(display.Text, "播放异常临时暂停") {
		t.Fatalf("status text = %q, want listening abuse freeze text", display.Text)
	}
	if !strings.Contains(display.Text, "ABS 仍启用，需核查") {
		t.Fatalf("status text = %q, want ABS active mismatch note", display.Text)
	}
}

func TestAccountStatusDisplayAbsDisabledOutOfSync(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.Local)
	expireAt := now.Add(24 * time.Hour)
	inactive := false

	display := buildAccountStatusDisplay(accountStatusDisplayInput{
		User: User{
			TelegramID: 2,
			Username:   "bob",
			AbsUserID:  "abs-2",
			Status:     "active",
			ExpireAt:   &expireAt,
		},
		Now:       now,
		Mode:      accountStatusDisplayAdmin,
		AbsActive: &inactive,
	})

	if display.Kind != accountStatusAbsDisabledOutOfSync {
		t.Fatalf("kind = %s, want %s", display.Kind, accountStatusAbsDisabledOutOfSync)
	}
	if display.LocalAllowsAccess {
		t.Fatal("ABS disabled mismatch should not allow access")
	}
	if !strings.Contains(display.Text, "ABS 服务端已停用") {
		t.Fatalf("status text = %q, want ABS disabled text", display.Text)
	}
}

func TestAccountStatusDisplayExpiredButAbsActive(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.Local)
	expireAt := now.Add(-time.Second)
	active := true

	display := buildAccountStatusDisplay(accountStatusDisplayInput{
		User: User{
			TelegramID: 3,
			Username:   "carol",
			AbsUserID:  "abs-3",
			Status:     "active",
			ExpireAt:   &expireAt,
		},
		Now:       now,
		Mode:      accountStatusDisplayAdmin,
		AbsActive: &active,
	})

	if display.Kind != accountStatusExpired {
		t.Fatalf("kind = %s, want %s", display.Kind, accountStatusExpired)
	}
	if display.LocalAllowsAccess {
		t.Fatal("expired account should not allow local access")
	}
	if !strings.Contains(display.Text, "已过期") || !strings.Contains(display.Text, "ABS 仍启用，需核查") {
		t.Fatalf("status text = %q, want expired text with ABS mismatch note", display.Text)
	}
}

func TestAccountStatusDisplayExpiredOverridesFreeze(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.Local)
	expireAt := now.Add(-time.Second)
	freezeEnd := now.Add(2 * time.Hour)
	inactive := false

	display := buildAccountStatusDisplay(accountStatusDisplayInput{
		User: User{
			TelegramID: 33,
			Username:   "chen",
			AbsUserID:  "abs-33",
			Status:     "active",
			ExpireAt:   &expireAt,
		},
		Now:          now,
		Mode:         accountStatusDisplaySelf,
		ActiveFreeze: &ListeningAbuseRecord{FreezeEndAt: &freezeEnd},
		AbsActive:    &inactive,
	})

	if display.Kind != accountStatusExpired {
		t.Fatalf("kind = %s, want %s", display.Kind, accountStatusExpired)
	}
	if strings.Contains(display.Text, "预计") {
		t.Fatalf("status text = %q, expired account should not promise temporary freeze recovery", display.Text)
	}
}

func TestAccountStatusDisplayWhitelistIgnoresExpireAt(t *testing.T) {
	now := time.Date(2026, 6, 20, 10, 0, 0, 0, time.Local)
	expireAt := now.Add(-24 * time.Hour)
	active := true

	display := buildAccountStatusDisplay(accountStatusDisplayInput{
		User: User{
			TelegramID:  4,
			Username:    "dave",
			AbsUserID:   "abs-4",
			Status:      "active",
			ExpireAt:    &expireAt,
			IsWhitelist: true,
		},
		Now:       now,
		Mode:      accountStatusDisplaySelf,
		AbsActive: &active,
	})

	if display.Kind != accountStatusWhitelist {
		t.Fatalf("kind = %s, want %s", display.Kind, accountStatusWhitelist)
	}
	if !display.LocalAllowsAccess {
		t.Fatal("whitelist account should allow local access")
	}
	if !strings.Contains(display.Text, "白名单") {
		t.Fatalf("status text = %q, want whitelist text", display.Text)
	}
}
