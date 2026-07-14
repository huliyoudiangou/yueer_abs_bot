package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestPaiGowHandPointRules(t *testing.T) {
	tests := []struct {
		name string
		hand []PaiGowCard
		want int
	}{
		{
			name: "nine is max",
			hand: []PaiGowCard{{Rank: "K", Point: 0}, {Rank: "9", Point: 9}},
			want: 9,
		},
		{
			name: "sum takes ones digit",
			hand: []PaiGowCard{{Rank: "8", Point: 8}, {Rank: "7", Point: 7}},
			want: 5,
		},
		{
			name: "face cards are zero",
			hand: []PaiGowCard{{Rank: "J", Point: 0}, {Rank: "Q", Point: 0}},
			want: 0,
		},
		{
			name: "ace is one",
			hand: []PaiGowCard{{Rank: "A", Point: 1}, {Rank: "9", Point: 9}},
			want: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := paiGowHandPoint(tt.hand); got != tt.want {
				t.Fatalf("paiGowHandPoint() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestPaiGowOpenTimeAndBetCommand(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	open := time.Date(2026, 7, 2, 18, 0, 0, 0, loc)
	if !isPaiGowOpenTime(open) {
		t.Fatal("18:00 should be pai gow open time")
	}
	if !isPaiGowOpenTime(time.Date(2026, 7, 2, 19, 54, 0, 0, loc)) {
		t.Fatal("19:54 should be pai gow open time")
	}
	if isPaiGowOpenTime(time.Date(2026, 7, 2, 19, 55, 0, 0, loc)) {
		t.Fatal("19:55 should be pai gow closed buffer")
	}
	if isPaiGowOpenTime(time.Date(2026, 7, 2, 17, 59, 0, 0, loc)) {
		t.Fatal("17:59 should be pai gow closed")
	}

	if !isPaiGowBetCommand("押 3") {
		t.Fatal("押 3 should be pai gow bet command")
	}
	for _, text := range []string{"押 大 3", "押 1 10", "押 三", "押"} {
		if isPaiGowBetCommand(text) {
			t.Fatalf("%q should not be pai gow bet command", text)
		}
	}
}

func TestDiceOpenTimeExcludesPaiGowAndRaceWindows(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	if !isDiceOpenTime(time.Date(2026, 7, 2, 17, 54, 0, 0, loc)) {
		t.Fatal("17:54 should be dice open time")
	}
	for _, closed := range []time.Time{
		time.Date(2026, 7, 2, 17, 55, 0, 0, loc),
		time.Date(2026, 7, 2, 18, 30, 0, 0, loc),
		time.Date(2026, 7, 2, 20, 30, 0, 0, loc),
	} {
		if isDiceOpenTime(closed) {
			t.Fatalf("%s should be dice closed time", closed)
		}
	}
	if !isDiceOpenTime(time.Date(2026, 7, 2, 22, 5, 0, 0, loc)) {
		t.Fatal("22:05 should be dice open time")
	}
}

func TestPaiGowSourceGuards(t *testing.T) {
	source, err := os.ReadFile("pai_gow.go")
	if err != nil {
		t.Fatalf("read pai_gow.go err = %v", err)
	}
	text := string(source)

	for _, want := range []string{
		"paiGowMinBet           = 1",
		"paiGowMaxBet           = 5",
		"paiGowBetDuration      = 60 * time.Second",
		"paiGowCooldownDuration = 1 * time.Minute",
		"paiGowMaxPlayers       = 20",
		`"pai_gow_bet"`,
		`"pai_gow_refund"`,
		`"pai_gow_win"`,
		"StartedAt  time.Time",
		"本局开奖结果会发布到群内，按群消息规则定时清理，不置顶。",
		"剩余下注",
		"本局当前",
		"createPaiGowBetInTx",
		"updatePaiGowBetStatusCAS",
		"loadActivePaiGowBetsSnapshot",
		"refundPaiGowBetsByGameID",
		"recoverActivePaiGowBetsOnStartup",
		"runFusionPoolLockedTransaction",
		"addPointsToFusionPoolInTx" + "(tx, botWinTotal)",
		"notifyFusionPoolBurst(bot, chatID, \"推牌九庄家通吃筹码注入天道\")",
		"sendAutoDelete(bot, msg)",
		"sort.Slice(userIDs",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("pai gow source guard missing %q", want)
		}
	}

	if strings.Contains(text, "sendNoAutoDelete(bot, msg)") {
		t.Fatal("pai gow final announcement should use auto-delete sender")
	}
}

func TestPaiGowDatabaseAndDispatchGuards(t *testing.T) {
	dbSource, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go err = %v", err)
	}
	dbText := string(dbSource)
	for _, want := range []string{
		"type PaiGowBet struct",
		"&PaiGowBet{}",
		"idx_pai_gow_bets_game_user_unique",
		"ON pai_gow_bets(game_id, user_id)",
	} {
		if !strings.Contains(dbText, want) {
			t.Fatalf("pai gow db guard missing %q", want)
		}
	}

	mainSource, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go err = %v", err)
	}
	if !strings.Contains(string(mainSource), "recoverActivePaiGowBetsOnStartup()") {
		t.Fatal("main startup should recover active pai gow bets")
	}

	stateSource, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	stateText := string(stateSource)
	for _, want := range []string{
		`text == "发起牌九"`,
		`text == "牌九状态"`,
		`text == "取消牌九"`,
		"isPaiGowBetCommand(text)",
		"handlePaiGowGame(bot, msg)",
		"minutes < 17*60+55 || (minutes >= 22*60+5 && minutes < 24*60)",
	} {
		if !strings.Contains(stateText, want) {
			t.Fatalf("pai gow dispatch/time guard missing %q", want)
		}
	}
}
