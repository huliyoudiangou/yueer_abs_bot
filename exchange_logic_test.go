package main

import (
	"os"
	"strings"
	"testing"
)

func TestExchangeImmediateUseConfirmFlowsAreGuarded(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	exchangeStart := strings.Index(text, `case "WAITING_EXCHANGE_CHOICE":`)
	if exchangeStart < 0 {
		t.Fatal("WAITING_EXCHANGE_CHOICE branch missing")
	}
	exchangeEnd := strings.Index(text[exchangeStart:], `case "WAITING_EXCHANGE_RENEW_USE_CONFIRM":`)
	if exchangeEnd < 0 {
		t.Fatal("exchange branch boundary missing")
	}
	exchangeBlock := text[exchangeStart : exchangeStart+exchangeEnd]
	for _, want := range []string{
		"determineExchangeInviteUseMode(userID)",
		`session.SetTemp("exchange_invite_hash", inviteHash)`,
		`session.SetStep("WAITING_EXCHANGE_INVITE_USE_CONFIRM")`,
		`session.SetTemp("exchange_renew_hash", renewHash)`,
		`session.SetStep("WAITING_EXCHANGE_RENEW_USE_CONFIRM")`,
		"确认使用邀请码",
		"确认使用续期卡",
	} {
		if !strings.Contains(exchangeBlock, want) {
			t.Fatalf("exchange immediate use guard missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`session.SetTemp("exchange_invite_code", code)`,
		`session.SetTemp("exchange_renew_code", code)`,
	} {
		if strings.Contains(exchangeBlock, forbidden) {
			t.Fatalf("exchange flow should not store raw code in session: %s", forbidden)
		}
	}

	renewStart := strings.Index(text, `case "WAITING_EXCHANGE_RENEW_USE_CONFIRM":`)
	if renewStart < 0 {
		t.Fatal("WAITING_EXCHANGE_RENEW_USE_CONFIRM branch missing")
	}
	renewEnd := strings.Index(text[renewStart:], `case "WAITING_EXCHANGE_INVITE_USE_CONFIRM":`)
	if renewEnd < 0 {
		t.Fatal("renew immediate use branch boundary missing")
	}
	renewBlock := text[renewStart : renewStart+renewEnd]
	for _, want := range []string{
		`text != "确认使用续期卡"`,
		`session.GetTemp("exchange_renew_hash")`,
		"redeemRenewCodeByHash(userID, renewHash)",
		"sendRenewRedeemResult(bot, chatID, userID, result)",
		"卡密仍未消费",
	} {
		if !strings.Contains(renewBlock, want) {
			t.Fatalf("renew immediate use branch missing %q", want)
		}
	}

	inviteStart := strings.Index(text, `case "WAITING_EXCHANGE_INVITE_USE_CONFIRM":`)
	if inviteStart < 0 {
		t.Fatal("WAITING_EXCHANGE_INVITE_USE_CONFIRM branch missing")
	}
	inviteEnd := strings.Index(text[inviteStart:], `case "WAITING_RED_POINTS":`)
	if inviteEnd < 0 {
		t.Fatal("invite immediate use branch boundary missing")
	}
	inviteBlock := text[inviteStart : inviteStart+inviteEnd]
	for _, want := range []string{
		`text != "确认使用邀请码"`,
		"determineExchangeInviteUseMode(userID)",
		"convertTrialToFormalWithInviteCode(userID, inviteHash)",
		`case exchangeInviteUseRegister:`,
		`session.SetTemp("invite_hash", inviteHash)`,
		`session.SetStep("WAITING_REG_USER")`,
		"当前账号已经拥有正式 ABS 账号",
	} {
		if !strings.Contains(inviteBlock, want) {
			t.Fatalf("invite immediate use branch missing %q", want)
		}
	}
}
