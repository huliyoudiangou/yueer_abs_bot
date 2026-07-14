package main

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm/clause"
)

func TestSplitDurationByBeijingDayAllocatesAcrossMidnight(t *testing.T) {
	loc := dailyOperationsLocation
	start := time.Date(2026, 6, 27, 23, 30, 0, 0, loc)
	end := time.Date(2026, 6, 28, 0, 30, 0, 0, loc)

	segments := splitDurationByBeijingDay(start, end)
	if got := segments["2026-06-27"]; got != 1800 {
		t.Fatalf("previous day seconds = %.0f, want 1800", got)
	}
	if got := segments["2026-06-28"]; got != 1800 {
		t.Fatalf("next day seconds = %.0f, want 1800", got)
	}
}

func TestParseABSLiveListeningSessionPayload(t *testing.T) {
	body := []byte(`{"sessions":[{"id":"s1","userId":"abs-user","libraryItemId":"book-1","currentTime":123.5,"timeListening":600,"startedAt":1782604800000,"isPlaying":true,"updatedAt":"2026-06-28T01:02:03Z"}]}`)
	sessions, err := parseABSSessionsPayload(body)
	if err != nil {
		t.Fatalf("parseABSSessionsPayload err = %v", err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions len = %d, want 1", len(sessions))
	}
	if !absSessionBelongsToUser(sessions[0], "abs-user") {
		t.Fatal("session should belong to abs-user")
	}
	session, ok := parseAbsLiveListeningSession(sessions[0], "abs-user")
	if !ok {
		t.Fatal("parseAbsLiveListeningSession should succeed")
	}
	if session.SessionKey != "s1" || session.ItemKey != "book-1" || !session.HasPosition || session.PositionSeconds != 123.5 || !session.HasListeningTime || session.ListeningSeconds != 600 || session.StartedAt.IsZero() || !session.IsPlaying {
		t.Fatalf("session parsed incorrectly: %#v", session)
	}
}

func TestParseABSLiveListeningSessionMissingStateIsNotPlaying(t *testing.T) {
	raw := map[string]interface{}{
		"id":            "historical-session",
		"userId":        "abs-user",
		"libraryItemId": "book-1",
		"currentTime":   7200.0,
		"timeListening": 1800.0,
		"startedAt":     "2026-07-11T10:00:00Z",
		"updatedAt":     "2026-07-11T10:30:00Z",
	}

	session, ok := parseAbsLiveListeningSession(raw, "abs-user")
	if !ok {
		t.Fatal("historical session should still parse for diagnostics")
	}
	if session.HasPlayingState {
		t.Fatal("missing playback state must remain unknown")
	}
	if session.IsPlaying {
		t.Fatal("historical session without explicit playback state must not be treated as playing")
	}
}

func TestAbsUserListeningSessionsPathUsesLatestUserPage(t *testing.T) {
	got := absUserListeningSessionsPath("user/with space")
	want := "/api/users/user%2Fwith%20space/listening-sessions?itemsPerPage=100&page=0"
	if got != want {
		t.Fatalf("absUserListeningSessionsPath() = %q, want %q", got, want)
	}
}

func TestParseABSLiveListeningSessionDoesNotTreatMediaStartTimeAsTimestamp(t *testing.T) {
	raw := map[string]interface{}{
		"id":            "s2",
		"userId":        "abs-user",
		"libraryItemId": "book-2",
		"currentTime":   7200.0,
		"startTime":     3600.0,
		"timeListening": 1800.0,
	}
	session, ok := parseAbsLiveListeningSession(raw, "abs-user")
	if !ok {
		t.Fatal("parseAbsLiveListeningSession should keep a session with playback position")
	}
	if !session.StartedAt.IsZero() {
		t.Fatalf("media startTime was parsed as wall-clock timestamp: %v", session.StartedAt)
	}
}

func TestRebalanceABSDaysSplitsContinuousSessionAcrossMidnight(t *testing.T) {
	loc := dailyOperationsLocation
	start := time.Date(2026, 7, 9, 22, 0, 0, 0, loc)
	sessions := []absLiveListeningSession{{
		StartedAt:        start,
		ListeningSeconds: 10 * 60 * 60,
		HasListeningTime: true,
	}}

	adjusted, corrected := rebalanceABSDaysForCrossDaySessions(map[string]float64{
		"2026-07-09": 12 * 60 * 60,
	}, sessions)

	if got := adjusted["2026-07-09"]; got != 4*60*60 {
		t.Fatalf("previous day seconds = %.0f, want 14400", got)
	}
	if got := adjusted["2026-07-10"]; got != 8*60*60 {
		t.Fatalf("next day seconds = %.0f, want 28800", got)
	}
	if !corrected["2026-07-09"] || !corrected["2026-07-10"] {
		t.Fatalf("corrected days = %#v, want both dates", corrected)
	}
	if got := adjusted["2026-07-09"] + adjusted["2026-07-10"]; got != 12*60*60 {
		t.Fatalf("rebalanced total seconds = %.0f, want 43200", got)
	}
}

func TestLivePositionDeltaAllowedUsesElapsedCap(t *testing.T) {
	now := time.Date(2026, 6, 28, 12, 0, 0, 0, time.UTC)
	last := now.Add(-10 * time.Minute)
	if !livePositionDeltaAllowed(1200, last, now) {
		t.Fatal("2x realtime plus grace should be allowed")
	}
	if livePositionDeltaAllowed(1300, last, now) {
		t.Fatal("position delta beyond cap should be rejected")
	}
}

func TestLiveClockFallbackAllowsShortCrossMidnightPlayback(t *testing.T) {
	loc := dailyOperationsLocation
	last := time.Date(2026, 7, 3, 23, 55, 0, 0, loc)
	now := time.Date(2026, 7, 4, 0, 5, 0, 0, loc)
	seconds, ok := liveClockFallbackSeconds(last, now)
	if !ok || seconds != 600 {
		t.Fatalf("liveClockFallbackSeconds() = %.0f,%v want 600,true", seconds, ok)
	}

	segments := make(map[string]float64)
	addAllocatedLiveSeconds(segments, last, now, seconds)
	if got := segments["2026-07-03"]; got != 300 {
		t.Fatalf("previous day fallback seconds = %.0f, want 300", got)
	}
	if got := segments["2026-07-04"]; got != 300 {
		t.Fatalf("next day fallback seconds = %.0f, want 300", got)
	}

	if _, ok := liveClockFallbackSeconds(last, last.Add(dailyListeningLiveClockFallbackMax+time.Second)); ok {
		t.Fatal("clock fallback beyond max window should be rejected")
	}
}

func TestAbsLiveListeningCheckpointOnConflictTargetsPartialUniqueIndex(t *testing.T) {
	onConflict := absLiveListeningCheckpointOnConflict(time.Now())
	if len(onConflict.Columns) != 3 ||
		onConflict.Columns[0].Name != "user_id" ||
		onConflict.Columns[1].Name != "session_key" ||
		onConflict.Columns[2].Name != "item_key" {
		t.Fatalf("checkpoint upsert columns = %#v", onConflict.Columns)
	}
	if len(onConflict.TargetWhere.Exprs) != 1 {
		t.Fatalf("checkpoint upsert target where = %#v", onConflict.TargetWhere.Exprs)
	}
	eq, ok := onConflict.TargetWhere.Exprs[0].(clause.Eq)
	if !ok {
		t.Fatalf("checkpoint target where should use clause.Eq, got %#v", onConflict.TargetWhere.Exprs[0])
	}
	column, ok := eq.Column.(clause.Column)
	if !ok || column.Name != "deleted_at" || eq.Value != nil {
		t.Fatalf("checkpoint target should constrain deleted_at IS NULL, got %#v", eq)
	}
	assignmentsText := fmt.Sprintf("%#v", onConflict.DoUpdates)
	for _, want := range []string{"last_observed_at", "last_position_seconds", "last_is_playing", "last_server_updated_at"} {
		if !strings.Contains(assignmentsText, want) {
			t.Fatalf("checkpoint upsert updates missing %q: %#v", want, onConflict.DoUpdates)
		}
	}
}
