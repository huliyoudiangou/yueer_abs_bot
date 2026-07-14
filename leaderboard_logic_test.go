package main

import (
	"os"
	"strings"
	"testing"
)

func TestLeaderboardUserListReadErrorsAreLogged(t *testing.T) {
	source, err := os.ReadFile("leaderboard.go")
	if err != nil {
		t.Fatalf("read leaderboard.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func GenerateAndSendLeaderboard(")
	if start < 0 {
		t.Fatal("GenerateAndSendLeaderboard missing")
	}
	end := strings.Index(text[start:], "func sendAndManageLeaderboardPin(")
	if end < 0 {
		t.Fatal("GenerateAndSendLeaderboard boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"榜单用户列表读取失败",
		"formatPlainValue(timeframe)",
		"formatPlainError(err)",
		"if len(users) == 0",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("leaderboard user list read guard missing %q", want)
		}
	}
	if strings.Contains(block, `err != nil || len(users) == 0`) {
		t.Fatal("leaderboard user list read errors are still collapsed into no users")
	}
}

func TestLeaderboardAbsUserIDUsesEscapedHelper(t *testing.T) {
	source, err := os.ReadFile("leaderboard.go")
	if err != nil {
		t.Fatalf("read leaderboard.go err = %v", err)
	}
	text := string(source)
	if strings.Contains(text, `fmt.Sprintf("/api/users/%s/listening-stats", user.AbsUserID)`) {
		t.Fatal("leaderboard ABS user stats endpoint should not embed raw AbsUserID")
	}
	if !strings.Contains(text, `absUserListeningStatsPath(user.AbsUserID)`) {
		t.Fatal("leaderboard ABS user stats endpoint should use escaped ABS user path helper")
	}
}

func TestLeaderboardStatsFailuresAreLoggedAndAllFailureSkipsSend(t *testing.T) {
	source, err := os.ReadFile("leaderboard.go")
	if err != nil {
		t.Fatalf("read leaderboard.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func GenerateAndSendLeaderboard(")
	if start < 0 {
		t.Fatal("GenerateAndSendLeaderboard missing")
	}
	end := strings.Index(text[start:], "func normalizeLeaderboardDateKey(")
	if end < 0 {
		t.Fatal("GenerateAndSendLeaderboard boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"statsSuccess := 0",
		"statsRequestFailures := 0",
		"statsParseFailures := 0",
		"statsRequestFailures++",
		"statsParseFailures++",
		"statsSuccess++",
		"榜单部分用户统计读取失败",
		"formatPlainValue(timeframe)",
		"榜单统计全部失败，跳过本期发送",
		"if statsSuccess == 0",
		"return",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("leaderboard stats failure guard missing %q", want)
		}
	}
	if strings.Contains(block, "if err != nil || code != 200 {\n\t\t\t\treturn\n\t\t\t}") ||
		strings.Contains(block, "if err := json.Unmarshal(body, &stats); err != nil {\n\t\t\t\treturn\n\t\t\t}") {
		t.Fatal("leaderboard stats failures are still skipped silently")
	}
}

func TestLeaderboardPinStateReadErrorsAreLogged(t *testing.T) {
	source, err := os.ReadFile("leaderboard.go")
	if err != nil {
		t.Fatalf("read leaderboard.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func sendAndManageLeaderboardPinSync(")
	if start < 0 {
		t.Fatal("sendAndManageLeaderboardPinSync missing")
	}
	block := text[start:]
	for _, want := range []string{
		"旧榜单置顶状态读取失败",
		"旧榜单置顶消息ID解析失败",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"新一期 [%s] 榜单发送失败",
		"榜单交接顺利完成",
		"formatPlainError(err)",
		"formatPlainError(parseErr)",
		"formatPlainError(err))",
		"formatPlainValue(timeframe), formatTelegramSendError(err)",
		"formatPlainValue(timeframe), formatPlainValue(newValue)",
		"setSystemConfigStringChecked(configKey, newValue)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("leaderboard pin state read guard missing %q", want)
		}
	}
	if strings.Contains(block, `DB.Where("key = ?", configKey).First(&cfg).Error == nil && cfg.Value != ""`) {
		t.Fatal("leaderboard pin state read errors are still collapsed into no old pin")
	}
	for _, unsafe := range []string{
		`timeframe, formatTelegramSendError(err)`,
		`timeframe, newValue`,
		`DB.Clauses(clause.OnConflict`,
		`Create(&SystemConfig{`,
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("leaderboard pin diagnostics still use raw dynamic fields: %s", unsafe)
		}
	}
	rawErrFormat := string([]byte{'e', 'r', 'r', '=', '%', 'v'})
	if strings.Contains(block, rawErrFormat) {
		t.Fatal("leaderboard pin diagnostics should use formatPlainError instead of a raw printf error directive")
	}
}
