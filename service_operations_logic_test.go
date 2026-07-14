package main

import (
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

func TestRedPacketCountRangeAllowsUpToOneHundred(t *testing.T) {
	for _, count := range []int{3, 50, 100} {
		if !validRedPacketCount(count) {
			t.Fatalf("count %d should be accepted", count)
		}
	}
	for _, count := range []int{-1, 0, 2, 101} {
		if validRedPacketCount(count) {
			t.Fatalf("count %d should be rejected", count)
		}
	}
}

func TestOpenRegistrationCampaignAvailability(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	quota := OpenRegistrationCampaign{Mode: openRegistrationModeQuota, Quota: 2}
	if err := openRegistrationCampaignAvailability(quota, 1, now); err != nil {
		t.Fatalf("quota with one free slot err=%v", err)
	}
	if err := openRegistrationCampaignAvailability(quota, 2, now); !errors.Is(err, errOpenRegistrationFull) {
		t.Fatalf("full quota err=%v, want %v", err, errOpenRegistrationFull)
	}

	future := now.Add(time.Hour)
	duration := OpenRegistrationCampaign{Mode: openRegistrationModeDuration, EndsAt: &future}
	if err := openRegistrationCampaignAvailability(duration, 0, now); err != nil {
		t.Fatalf("active duration err=%v", err)
	}
	if err := openRegistrationCampaignAvailability(duration, 0, future); !errors.Is(err, errOpenRegistrationExpired) {
		t.Fatalf("expired duration err=%v, want %v", err, errOpenRegistrationExpired)
	}
}

func TestCompensationExpireAtRules(t *testing.T) {
	now := time.Date(2026, 7, 12, 12, 0, 0, 0, time.UTC)
	active := now.Add(48 * time.Hour)
	got, applied := compensationExpireAt(&active, now, 5, true)
	want := active.AddDate(0, 0, 5)
	if applied != 5 || got == nil || !got.Equal(want) {
		t.Fatalf("active expiry got=%v applied=%d, want=%v/5", got, applied, want)
	}

	expired := now.Add(-24 * time.Hour)
	got, applied = compensationExpireAt(&expired, now, 3, true)
	want = now.AddDate(0, 0, 3)
	if applied != 3 || got == nil || !got.Equal(want) {
		t.Fatalf("expired expiry got=%v applied=%d, want=%v/3", got, applied, want)
	}

	got, applied = compensationExpireAt(nil, now, 7, true)
	if got != nil || applied != 0 {
		t.Fatalf("unlimited account should remain unlimited, got=%v applied=%d", got, applied)
	}
	got, applied = compensationExpireAt(&active, now, 7, false)
	if applied != 0 || got == nil || !got.Equal(active) {
		t.Fatalf("account without ABS should not receive days, got=%v applied=%d", got, applied)
	}
}

func TestAuthoritativeABSListeningTotalPrefersOfficialTotal(t *testing.T) {
	days := map[string]float64{"2026-07-11": 100, "2026-07-12": 200, "bad": -50}
	if got := authoritativeABSListeningTotalSeconds(900, 800, days); got != 900 {
		t.Fatalf("official total=%v, want 900", got)
	}
	if got := authoritativeABSListeningTotalSeconds(0, 800, days); got != 300 {
		t.Fatalf("days fallback=%v, want 300", got)
	}
	if got := authoritativeABSListeningTotalSeconds(0, 800, nil); got != 800 {
		t.Fatalf("legacy fallback=%v, want 800", got)
	}
}

func TestLiveListeningClockFallbackRequiresReliablePlayingState(t *testing.T) {
	checkpoint := AbsLiveListeningCheckpoint{LastIsPlaying: true}
	playing := absLiveListeningSession{IsPlaying: true, HasPlayingState: true}
	if !canUseLiveClockFallback(playing, checkpoint) {
		t.Fatal("reliably playing session should allow clock fallback")
	}
	paused := absLiveListeningSession{IsPlaying: false, HasPlayingState: true}
	if canUseLiveClockFallback(paused, checkpoint) {
		t.Fatal("paused session must not allow clock fallback")
	}
	unknown := absLiveListeningSession{IsPlaying: true, HasPlayingState: false}
	if canUseLiveClockFallback(unknown, checkpoint) {
		t.Fatal("session without explicit playing state must not allow clock fallback")
	}
	checkpoint.LastIsPlaying = false
	if canUseLiveClockFallback(playing, checkpoint) {
		t.Fatal("previously paused checkpoint must not allow clock fallback")
	}
}

func TestAdminSystemConfigMenuContainsServiceOperations(t *testing.T) {
	_, configMenu := renderSuperAdminMenu("config")
	labels := make(map[string]string)
	for _, row := range configMenu.InlineKeyboard {
		for _, button := range row {
			callback := ""
			if button.CallbackData != nil {
				callback = *button.CallbackData
			}
			labels[button.Text] = callback
		}
	}
	if got := labels["🚪 开放注册"]; got != "admin:cmd:open_registration" {
		t.Fatalf("open registration menu callback=%q", got)
	}
	if got := labels["🛡 用户补偿"]; got != "admin:cmd:user_compensation" {
		t.Fatalf("user compensation menu callback=%q", got)
	}

	for key, want := range map[string]string{
		"open_registration": "开放注册",
		"user_compensation": "用户补偿",
	} {
		got, superOnly, ok := adminMenuCommandText(key)
		if !ok || !superOnly || got != want {
			t.Fatalf("admin command %s got=%q superOnly=%v ok=%v", key, got, superOnly, ok)
		}
	}

	for _, menuKey := range []string{"users", "assets"} {
		_, menu := renderSuperAdminMenu(menuKey)
		for _, row := range menu.InlineKeyboard {
			for _, button := range row {
				if button.Text == "🚪 开放注册" || button.Text == "🛡 用户补偿" {
					t.Fatalf("%s must live in system config, found in %s", button.Text, menuKey)
				}
			}
		}
	}
}
func TestServiceOperationsSourceGuards(t *testing.T) {
	adminSource, err := os.ReadFile("admin_service_operations.go")
	if err != nil {
		t.Fatalf("read admin_service_operations.go: %v", err)
	}
	adminText := string(adminSource)
	for _, want := range []string{
		"idx_open_registration_single_active",
		"used > int64(campaign.Quota)",
		"applyPointDeltaInTx" + "(tx, grant.UserID, grant.Points, \"service_" + "outage_compensation\"",
		"Where(\"id = ? AND status = ?\", grant.ID, compensationGrantPending)",
		"CREATE_USER_COMPENSATION",
		"COMPLETE_USER_COMPENSATION",
		"sendNoAutoDelete(bot, msg)",
		"StartCompensationDispatcher(bot *tgbotapi.BotAPI)",
	} {
		if !strings.Contains(adminText, want) {
			t.Fatalf("service operation guard missing %q", want)
		}
	}

	stateSource, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	stateText := string(stateSource)
	for _, want := range []string{
		"reserveOpenRegistrationSlot(userID, openCampaignID, time.Now())",
		"completeOpenRegistrationReservationInTx(tx, openReservation.ID, userID, time.Now())",
		"releaseOpenRegistrationReservation(openReservation.ID, userID, \"abs_register_failed\")",
		"红包总个数** (3-100)",
		"个数限制在 3 ~ 100 个之间",
	} {
		if !strings.Contains(stateText, want) {
			t.Fatalf("registration/red-packet guard missing %q", want)
		}
	}

	liveSource, err := os.ReadFile("daily_listening_live.go")
	if err != nil {
		t.Fatalf("read daily_listening_live.go: %v", err)
	}
	liveText := string(liveSource)
	start := strings.Index(liveText, "func consumeAbsLiveListeningSessionDelta(")
	if start < 0 {
		t.Fatal("consumeAbsLiveListeningSessionDelta missing")
	}
	end := strings.Index(liveText[start:], "func canUseLiveClockFallback(")
	if end < 0 {
		t.Fatal("live listening function boundary missing")
	}
	block := liveText[start : start+end]
	if strings.Contains(block, "session.StartedAt") {
		t.Fatal("first observation must not backfill from session.StartedAt")
	}
	if !strings.Contains(block, "首次观察只建立 checkpoint") {
		t.Fatal("first-observation checkpoint-only guard missing")
	}
}
