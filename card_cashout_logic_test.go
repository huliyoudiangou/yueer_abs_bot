package main

import (
	"os"
	"strings"
	"testing"
)

func TestCodeCashoutRate(t *testing.T) {
	cases := []struct {
		original int
		want     int
	}{
		{15, 9},
		{150, 90},
		{300, 180},
		{450, 270},
		{1825, 1095},
	}
	for _, tt := range cases {
		if got := calculateCodeCashoutPoints(tt.original); got != tt.want {
			t.Fatalf("calculateCodeCashoutPoints(%d) = %d, want %d", tt.original, got, tt.want)
		}
	}
}

func TestCodeCashoutTransactionGuards(t *testing.T) {
	data, err := os.ReadFile("card_cashout.go")
	if err != nil {
		t.Fatalf("read card_cashout.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func executeCodeCashout(")
	end := strings.Index(text[start:], "func closeCashedOutMarketplaceUnitsInTx(")
	if start < 0 || end < 0 {
		t.Fatal("cashout transaction helper boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"DB.Transaction(func(tx *gorm.DB) error",
		"is_used = ? AND cashed_out_at IS NULL",
		`"is_used":          true`,
		`"cashed_out_at":`,
		"res.RowsAffected != 1",
		"closeCashedOutMarketplaceUnitsInTx(tx, quote.Hash)",
		`"code_cashout"`,
		"applyPointDeltaInTx(",
		"writeAuditLogInTx(",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("cashout transaction guard missing %q", want)
		}
	}
}

func TestCodeCashoutNeverPersistsPlainCodeInSession(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)
	if strings.Contains(text, `SetTemp("cashout_code"`) {
		t.Fatal("cashout session must not retain plaintext code")
	}
	for _, want := range []string{
		`SetTemp("cashout_hash", quote.Hash)`,
		`SetTemp("cashout_preview", quote.Preview)`,
		`SetStep("WAITING_CODE_CASHOUT_CONFIRM")`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("cashout session guard missing %q", want)
		}
	}
}
