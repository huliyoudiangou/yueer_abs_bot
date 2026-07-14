package main

import (
	"os"
	"strings"
	"testing"
)

func TestBlindBoxOpenReturnMessagesOnlyAfterSuccess(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func executeBlindBoxOpen(")
	if start < 0 {
		t.Fatal("executeBlindBoxOpen missing")
	}
	end := strings.Index(text[start:], "func writeAuditLog(")
	if end < 0 {
		t.Fatal("executeBlindBoxOpen boundary missing")
	}
	block := text[start : start+end]

	for _, want := range []string{
		"var txReplyMsg, txBroadcastMsg string",
		"txReplyMsg = resultPrefix",
		"txBroadcastMsg = fmt.Sprintf",
		"if err != nil {\n\t\treturn \"\", \"\", err\n\t}",
		"return txReplyMsg, txBroadcastMsg, nil",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("blind box transaction return guard missing %q", want)
		}
	}

	for _, forbidden := range []string{
		"return replyMsg, broadcastMsg, err",
		"var replyMsg, broadcastMsg string",
		"replyMsg = resultPrefix",
		"broadcastMsg = fmt.Sprintf",
	} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("blind box still exposes transactional intermediate message: %s", forbidden)
		}
	}
}

func TestBlindBoxPrizeProbabilityBoundaries(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func executeBlindBoxOpen(")
	if start < 0 {
		t.Fatal("executeBlindBoxOpen missing")
	}
	end := strings.Index(text[start:], "func writeAuditLog(")
	if end < 0 {
		t.Fatal("executeBlindBoxOpen boundary missing")
	}
	block := text[start : start+end]

	for _, want := range []string{
		"case roll <= 71:",
		"【谢谢惠顾】",
		"case roll <= 91:",
		"【3天续期卡】",
		"case roll <= 94:",
		"【专属邀请码】",
		"case roll <= 99:",
		"【30天续期月卡】",
		"case roll <= 100:",
		"【365天尊享年卡】",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("blind box probability boundary missing %q", want)
		}
	}
}
