package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestWealthLeaderboardFormatUsesTopFiftyAndThirtyPerPage(t *testing.T) {
	users := []User{
		{Username: "user_31", Points: 70},
		{Username: "user32", Points: 60},
	}
	text := formatWealthLeaderboardPage(users, 50, 2)

	for _, want := range []string{
		"全服积分财富榜 Top 50",
		"页码：`2/2`",
		"第31名 **user\\_31** : `70` 积分",
		"第32名 **user32** : `60` 积分",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("wealth leaderboard text missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "Top 20") {
		t.Fatalf("wealth leaderboard should not render old Top 20 title: %s", text)
	}
}

func TestWealthLeaderboardPaginationButtonsOnly(t *testing.T) {
	if wealthLeaderboardLimit != 50 {
		t.Fatalf("wealthLeaderboardLimit = %d, want 50", wealthLeaderboardLimit)
	}
	if wealthLeaderboardPageSize != 30 {
		t.Fatalf("wealthLeaderboardPageSize = %d, want 30", wealthLeaderboardPageSize)
	}
	if pages := wealthLeaderboardTotalPages(50); pages != 2 {
		t.Fatalf("wealthLeaderboardTotalPages(50) = %d, want 2", pages)
	}

	first := wealthLeaderboardPageMarkup(1, 2)
	firstData, err := json.Marshal(first)
	if err != nil {
		t.Fatalf("marshal first page markup: %v", err)
	}
	firstRaw := string(firstData)
	if !strings.Contains(firstRaw, `"text":"下一页"`) || !strings.Contains(firstRaw, `"callback_data":"wealth_page:2"`) {
		t.Fatalf("first page next button missing: %s", firstRaw)
	}
	if strings.Contains(firstRaw, "上一页") {
		t.Fatalf("first page should not show previous button: %s", firstRaw)
	}

	second := wealthLeaderboardPageMarkup(2, 2)
	secondData, err := json.Marshal(second)
	if err != nil {
		t.Fatalf("marshal second page markup: %v", err)
	}
	secondRaw := string(secondData)
	if !strings.Contains(secondRaw, `"text":"上一页"`) || !strings.Contains(secondRaw, `"callback_data":"wealth_page:1"`) {
		t.Fatalf("second page previous button missing: %s", secondRaw)
	}
	if strings.Contains(secondRaw, "下一页") {
		t.Fatalf("second page should not show next button: %s", secondRaw)
	}
}

func TestWealthLeaderboardGroupEntryBeforePrivateReturn(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	menuStart := strings.Index(text, "menuKeywords := []string{")
	if menuStart < 0 {
		t.Fatal("menuKeywords block missing")
	}
	groupStart := strings.LastIndex(text[:menuStart], "if !msg.Chat.IsPrivate() {")
	if groupStart < 0 {
		t.Fatal("group return block before menuKeywords missing")
	}
	groupBlock := text[groupStart:menuStart]
	for _, want := range []string{
		"if isWealthLeaderboardCommand(text) {",
		"registerIncomingGroupCommandForAutoDelete(msg)",
		"handleWealthLeaderboardCommand(bot, msg)",
	} {
		if !strings.Contains(groupBlock, want) {
			t.Fatalf("wealth leaderboard group entry missing %q", want)
		}
	}
	if strings.Contains(groupBlock, `strings.Contains(text, "财富榜")`) {
		t.Fatal("group entry should use shared wealth command helper")
	}
}

func TestWealthLeaderboardCallbackIsDispatched(t *testing.T) {
	source, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "if update.CallbackQuery != nil {")
	if start < 0 {
		t.Fatal("callback dispatch block missing")
	}
	end := strings.Index(text[start:], "handlePrivateStartFastPath")
	if end < 0 {
		t.Fatal("callback dispatch block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"handleSectMemberPageCallback(bot, cb)",
		"handleWealthLeaderboardCallback(bot, cb)",
		"handleMenuCallback(bot, cb)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("callback dispatch missing %q", want)
		}
	}
	if strings.Index(block, "handleWealthLeaderboardCallback(bot, cb)") > strings.Index(block, "handleMenuCallback(bot, cb)") {
		t.Fatal("wealth leaderboard callback should be dispatched before menu callback")
	}
}
