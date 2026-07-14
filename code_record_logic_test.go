package main

import (
	"os"
	"strings"
	"testing"
)

func TestInviteAndRenewCodeRecordCreatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	cases := []struct {
		name      string
		signature string
		end       string
		missed    string
		unsafe    string
	}{
		{
			name:      "renew",
			signature: "func createRenewCodeRecord(",
			end:       "func createInviteCodeRecord(",
			missed:    "RENEW_CODE_CREATE_MISSED",
			unsafe:    "}).Error",
		},
		{
			name:      "invite",
			signature: "func createInviteCodeRecord(",
			end:       "func getUserRoleFromDBChecked(",
			missed:    "INVITE_CODE_CREATE_MISSED",
			unsafe:    "}).Error",
		},
	}

	for _, tt := range cases {
		start := strings.Index(text, tt.signature)
		if start < 0 {
			t.Fatalf("%s code record helper missing", tt.name)
		}
		end := strings.Index(text[start:], tt.end)
		if end < 0 {
			t.Fatalf("%s code record helper boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range []string{
			"res := tx.Create(&",
			"res.Error != nil",
			"res.RowsAffected == 0",
			tt.missed,
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s code record create guard missing %q", tt.name, want)
			}
		}
		if strings.Contains(block, tt.unsafe) {
			t.Fatalf("%s code record create still returns Create(...).Error directly", tt.name)
		}
	}
}
