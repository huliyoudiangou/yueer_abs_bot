package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestRenewRedeemErrorCodeMapsTrialRenewCode(t *testing.T) {
	if got := renewRedeemErrorCode(errTrialCannotUseRenewCode); got != "TRIAL_CANNOT_USE_RENEW_CODE" {
		t.Fatalf("renewRedeemErrorCode(trial) = %s, want TRIAL_CANNOT_USE_RENEW_CODE", got)
	}
	if got := renewRedeemErrorCode(errRenewCodeOwnerMismatch); got != "RENEW_CODE_OWNER_MISMATCH" {
		t.Fatalf("renewRedeemErrorCode(owner mismatch) = %s, want RENEW_CODE_OWNER_MISMATCH", got)
	}
}

func TestFormatPlainValueRemovesFormatControlsBeforeRedaction(t *testing.T) {
	raw := "alpha\nbeta\u2028gamma\u2029delta \u202eevil t\u202eoken=secret-token pass\u202eword=secret-pass"
	got := formatPlainValue(raw)
	for _, forbidden := range []string{
		"\n",
		"\u2028",
		"\u2029",
		"\u202e",
		"\u2066",
		"secret-token",
		"secret-pass",
	} {
		if strings.Contains(got, forbidden) {
			t.Fatalf("formatPlainValue retained unsafe fragment %q in %q", forbidden, got)
		}
	}
	for _, want := range []string{"alpha beta gamma delta evil", "token=***", "password=***"} {
		if !strings.Contains(got, want) {
			t.Fatalf("formatPlainValue(%q) = %q, missing %q", raw, got, want)
		}
	}
}

func TestRenewCodeRedeemChecksUserExpireUpdateRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `func redeemRenewCodeByHash(`)
	if start < 0 {
		t.Fatal("redeemRenewCodeByHash missing")
	}
	end := strings.Index(text[start:], `func sendRenewRedeemResult(`)
	if end < 0 {
		t.Fatal("redeemRenewCodeByHash boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"userRes := tx.Model(&User{})",
		`Where("id = ? AND telegram_id = ? AND account_type <> ?", u.ID, userID, accountTypeTrial)`,
		"userRes.RowsAffected == 0",
		"RENEW_USER_STATE_CHANGED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("renew code redeem user update guard missing %q", want)
		}
	}
	if strings.Contains(block, `tx.Model(&u).Update("expire_at", newExpireAt).Error`) {
		t.Fatal("renew code redeem still ignores user expire_at RowsAffected")
	}
}

func TestRenewCodeRedeemBusinessErrorsOnlyForMissingRecords(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `func redeemRenewCodeByHash(`)
	if start < 0 {
		t.Fatal("redeemRenewCodeByHash missing")
	}
	end := strings.Index(text[start:], `func sendRenewRedeemResult(`)
	if end < 0 {
		t.Fatal("redeemRenewCodeByHash boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`tx.Where("code_hash = ? AND is_used = ?", renewHash, false).First(&rCode).Error`,
		`tx.Where("telegram_id = ?", userID).First(&u).Error`,
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"return errInvalidRenewCode",
		"return errUserNotFound",
		"return err",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("renew code redeem read error guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`if err := tx.Where("code_hash = ? AND is_used = ?", renewHash, false).First(&rCode).Error; err != nil {
				return errInvalidRenewCode
			}`,
		`if err := tx.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
				return errUserNotFound
			}`,
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("renew code redeem still maps all read errors to business error: %s", unsafe)
		}
	}
}

func TestSectTaskErrorCodesSupportWrappedSentinels(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "daily incomplete",
			err:  fmt.Errorf("claim daily task: %w", errSectDailyTaskNotAllCompleted),
			want: "SECT_DAILY_TASK_NOT_ALL_COMPLETED",
		},
		{
			name: "daily already claimed",
			err:  fmt.Errorf("claim daily task: %w", errSectDailyTaskAlreadyClaimed),
			want: "ALREADY_CLAIMED",
		},
		{
			name: "weekly not achieved",
			err:  fmt.Errorf("settle weekly task: %w", errSectWeeklyTaskNotAchieved),
			want: "SECT_WEEKLY_TASK_NOT_ACHIEVED",
		},
		{
			name: "weekly already settled",
			err:  fmt.Errorf("settle weekly task: %w", errSectWeeklyTaskAlreadySettled),
			want: "SECT_WEEKLY_TASK_ALREADY_SETTLED",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := sectErrorCode(tt.err); got != tt.want {
				t.Fatalf("sectErrorCode() = %s, want %s", got, tt.want)
			}
		})
	}
}
