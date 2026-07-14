package main

import (
	"os"
	"strings"
	"testing"
)

func TestFormatTelegramSendErrorRedactsBotTokenURL(t *testing.T) {
	raw := `Post "https://api.telegram.org/bot123456789:AAExampleSecretToken/sendMessage": context deadline exceeded`

	got := formatTelegramSendError(assertErrString(raw))

	if strings.Contains(got, "123456789:AAExampleSecretToken") {
		t.Fatalf("telegram token was not redacted: %s", got)
	}
	if !strings.Contains(got, "https://api.telegram.org/bot***:***/sendMessage") {
		t.Fatalf("telegram API URL was not redacted as expected: %s", got)
	}
}

func TestTerminalTelegramUnpinErrorClassification(t *testing.T) {
	terminal := []string{
		"Bad Request: message to unpin not found",
		"Bad Request: message is not pinned",
		"Bad Request: message can't be unpinned",
		"Forbidden: not enough rights to manage pinned messages",
	}
	for _, raw := range terminal {
		if !isTerminalTelegramUnpinError(assertErrString(raw)) {
			t.Fatalf("expected terminal unpin error for %q", raw)
		}
	}

	retryable := []string{
		"Post \"https://api.telegram.org/bot123:ABC/unpinChatMessage\": context deadline exceeded",
		"Too Many Requests: retry after 10",
		"Internal Server Error",
	}
	for _, raw := range retryable {
		if isTerminalTelegramUnpinError(assertErrString(raw)) {
			t.Fatalf("expected non-terminal unpin error for %q", raw)
		}
	}
}

func TestTelegramStartupDiagnosticsAreSanitized(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		`log.Panicf("🤖 Bot 启动失败: %s", formatTelegramSendError(err))`,
		`log.Printf("✅ Bot 已成功启动，当前运行账号: %s", formatPlainValue(bot.Self.UserName))`,
		`formatPlainValue(strings.Join(u.AllowedUpdates, ","))`,
		`"bot=" + formatPlainValue(botUsername) + "\n"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("startup diagnostic sanitization missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`log.Panic("🤖 Bot 启动失败: ", err)`,
		`log.Printf("✅ Bot 已成功启动，当前运行账号: %s", bot.Self.UserName)`,
		`allowed_updates=%v`,
		`"bot=" + botUsername + "\n"`,
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("startup diagnostic still uses raw dynamic value: %q", unsafe)
		}
	}
}

func TestTelegramMetricErrorFormatsEndpoint(t *testing.T) {
	endpoint := "sendMessage\nbroken\tpath"
	cases := []string{
		formatTelegramMetricError(endpoint, assertErrString("temporary failure"), 0),
		formatTelegramMetricError(endpoint, nil, 502),
		formatTelegramMetricError(endpoint, nil, 0),
	}

	for _, got := range cases {
		if strings.Contains(got, endpoint) {
			t.Fatalf("telegram metric error kept raw endpoint: %q", got)
		}
		if strings.ContainsAny(got, "\n\t") {
			t.Fatalf("telegram metric error contains control whitespace: %q", got)
		}
		if !strings.Contains(got, "sendMessage broken path") {
			t.Fatalf("telegram metric error missing formatted endpoint: %q", got)
		}
	}
}

type assertErrString string

func (e assertErrString) Error() string {
	return string(e)
}
