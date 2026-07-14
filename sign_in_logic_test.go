package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestSignInDateKeyUsesBeijingDayBoundary(t *testing.T) {
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{
			name: "before beijing midnight",
			t:    time.Date(2026, 6, 17, 15, 59, 59, 0, time.UTC),
			want: "2026-06-17",
		},
		{
			name: "at beijing midnight",
			t:    time.Date(2026, 6, 17, 16, 0, 0, 0, time.UTC),
			want: "2026-06-18",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := signInDateKey(tt.t); got != tt.want {
				t.Fatalf("signInDateKey() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSignInMonthKeyUsesBeijingMonthBoundary(t *testing.T) {
	tests := []struct {
		name string
		t    time.Time
		want string
	}{
		{
			name: "before beijing month boundary",
			t:    time.Date(2026, 5, 31, 15, 59, 59, 0, time.UTC),
			want: "202605",
		},
		{
			name: "at beijing month boundary",
			t:    time.Date(2026, 5, 31, 16, 0, 0, 0, time.UTC),
			want: "202606",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := signInMonthKey(tt.t); got != tt.want {
				t.Fatalf("signInMonthKey() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestSignInDayInCycleWrapsEveryThirtyDays(t *testing.T) {
	tests := map[int]int{
		0:  1,
		1:  1,
		3:  3,
		30: 30,
		31: 1,
		33: 3,
		60: 30,
		61: 1,
	}

	for streakDays, want := range tests {
		if got := signInDayInCycle(streakDays); got != want {
			t.Fatalf("signInDayInCycle(%d) = %d, want %d", streakDays, got, want)
		}
	}
}

func TestCalculateCycleSignRewardMilestones(t *testing.T) {
	tests := []struct {
		day      int
		min      int
		max      int
		descPart string
	}{
		{day: 3, min: 1, max: 1, descPart: "3天"},
		{day: 7, min: 2, max: 2, descPart: "7天"},
		{day: 14, min: 3, max: 5, descPart: "14天"},
		{day: 21, min: 5, max: 7, descPart: "21天"},
		{day: 30, min: 8, max: 15, descPart: "30天"},
	}

	for _, tt := range tests {
		t.Run(tt.descPart, func(t *testing.T) {
			for i := 0; i < 20; i++ {
				got, desc := calculateCycleSignReward(tt.day)
				if got < tt.min || got > tt.max {
					t.Fatalf("calculateCycleSignReward(%d) = %d, want %d..%d", tt.day, got, tt.min, tt.max)
				}
				if !strings.Contains(desc, tt.descPart) {
					t.Fatalf("calculateCycleSignReward(%d) desc = %q, want contains %q", tt.day, desc, tt.descPart)
				}
			}
		})
	}

	for _, day := range []int{1, 2, 4, 6, 8, 13, 15, 22, 29, 31} {
		if got, desc := calculateCycleSignReward(day); got != 0 || desc != "" {
			t.Fatalf("calculateCycleSignReward(%d) = %d, %q; want no reward", day, got, desc)
		}
	}
}

func TestCalculateSignStreakRewardSetsMilestoneFlags(t *testing.T) {
	now := time.Date(2026, 6, 30, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	streak := &MonthlySignInStreak{StreakDays: 3}

	points, desc := calculateSignStreakReward(streak, now)
	if points != 1 || !strings.Contains(desc, "3天") || !streak.Rewarded3Days {
		t.Fatalf("3-day reward = %d, %q, rewarded=%t", points, desc, streak.Rewarded3Days)
	}

	points, desc = calculateSignStreakReward(streak, now)
	if points != 0 || desc != "" {
		t.Fatalf("duplicate 3-day reward = %d, %q; want no reward", points, desc)
	}
}

func TestCalculateSignStreakRewardFullMonthUsesMonthLength(t *testing.T) {
	feb := time.Date(2026, 2, 28, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	streak := &MonthlySignInStreak{StreakDays: 28}

	for i := 0; i < 20; i++ {
		points, desc := calculateSignStreakReward(streak, feb)
		if points < 8 || points > 15 || !strings.Contains(desc, "全勤") || !streak.RewardedFull {
			t.Fatalf("full-month reward = %d, %q, rewarded=%t; want 8..15 full attendance", points, desc, streak.RewardedFull)
		}
		streak.RewardedFull = false
	}
}

func TestSignInStreakUpdateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleUserSignIn(")
	if start < 0 {
		t.Fatal("handleUserSignIn missing")
	}
	end := strings.Index(text[start:], "func showPointTransactions(")
	if end < 0 {
		t.Fatal("handleUserSignIn boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"oldStoredCycleSeq := streak.CycleSeq",
		"normalizedOldCycleSeq := oldStoredCycleSeq",
		"oldLastSignDate := streak.LastSignDate",
		"oldCurrentStreakDays := streak.CurrentStreakDays",
		"oldTotalSignDays := streak.TotalSignDays",
		"oldBreakCount := streak.BreakCount",
		"newCycleSeq := normalizedOldCycleSeq",
		"streakRes := tx.Model(&SignInStreak{})",
		`Where("id = ? AND user_id = ? AND last_sign_date = ? AND current_streak_days = ? AND total_sign_days = ? AND cycle_seq = ? AND break_count = ?"`,
		"oldStoredCycleSeq",
		"streakRes.RowsAffected == 0",
		"errConcurrentSignInRetry",
		"userSignRes := tx.Model(&User{})",
		`Update("last_sign_at", &now)`,
		"userSignRes.RowsAffected == 0",
		"SIGN_USER_LAST_SIGN_UPDATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sign-in streak update guard missing %q", want)
		}
	}
	if strings.Contains(block, "tx.Save(&streak)") {
		t.Fatal("sign-in streak update still uses full-row Save")
	}
}

func TestSignInCreateLogsCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	signLogStart := strings.Index(text, "func createSignInLogInTx(")
	if signLogStart < 0 {
		t.Fatal("createSignInLogInTx missing")
	}
	signLogEnd := strings.Index(text[signLogStart:], "func createSignInRewardClaimInTx(")
	if signLogEnd < 0 {
		t.Fatal("createSignInLogInTx boundary missing")
	}
	signLogBlock := text[signLogStart : signLogStart+signLogEnd]
	for _, want := range []string{
		"res := tx.Create(logEntry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SIGN_IN_LOG_CREATE_MISSED",
	} {
		if !strings.Contains(signLogBlock, want) {
			t.Fatalf("sign-in log helper guard missing %q", want)
		}
	}

	claimStart := strings.Index(text, "func createSignInRewardClaimInTx(")
	if claimStart < 0 {
		t.Fatal("createSignInRewardClaimInTx missing")
	}
	claimEnd := strings.Index(text[claimStart:], "type signInResult struct")
	if claimEnd < 0 {
		t.Fatal("createSignInRewardClaimInTx boundary missing")
	}
	claimBlock := text[claimStart : claimStart+claimEnd]
	for _, want := range []string{
		"entry := *claim",
		"entry.Description = formatPlainValue(entry.Description)",
		"entry.RefID = formatPlainValue(entry.RefID)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SIGN_IN_REWARD_CLAIM_CREATE_MISSED",
	} {
		if !strings.Contains(claimBlock, want) {
			t.Fatalf("sign-in reward claim helper guard missing %q", want)
		}
	}

	streakStart := strings.Index(text, "func createSignInStreakInTx(")
	if streakStart < 0 {
		t.Fatal("createSignInStreakInTx missing")
	}
	streakEnd := strings.Index(text[streakStart:], "type signInResult struct")
	if streakEnd < 0 {
		t.Fatal("createSignInStreakInTx boundary missing")
	}
	streakBlock := text[streakStart : streakStart+streakEnd]
	for _, want := range []string{
		"entry := *streak",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"isUniqueConstraintError(res.Error)",
		"errConcurrentSignInRetry",
		"res.RowsAffected == 0",
		"SIGN_IN_STREAK_CREATE_MISSED",
		"*streak = entry",
	} {
		if !strings.Contains(streakBlock, want) {
			t.Fatalf("sign-in streak helper guard missing %q", want)
		}
	}

	handleStart := strings.Index(text, "func handleUserSignIn(")
	if handleStart < 0 {
		t.Fatal("handleUserSignIn missing")
	}
	handleEnd := strings.Index(text[handleStart:], "func showPointTransactions(")
	if handleEnd < 0 {
		t.Fatal("handleUserSignIn boundary missing")
	}
	handleBlock := text[handleStart : handleStart+handleEnd]
	for _, want := range []string{
		"createSignInStreakInTx(tx, &streak)",
		"createSignInLogInTx(tx, &SignInLog{",
		"createSignInRewardClaimInTx(tx, &claim)",
	} {
		if !strings.Contains(handleBlock, want) {
			t.Fatalf("sign-in create helper call missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"tx.Create(&streak).Error",
		"tx.Create(&SignInLog{",
		"tx.Create(&claim).Error",
	} {
		if strings.Contains(handleBlock, unsafe) {
			t.Fatalf("sign-in create still ignores RowsAffected: %s", unsafe)
		}
	}
}

func TestSignInMigrationsReplaceFullUniqueIndexes(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("monthly_sign_in_streaks(user_id, month_key)"`)
	if start < 0 {
		t.Fatal("monthly sign-in migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("github_benefit_claims(claimed telegram_id)"`)
	if end < 0 {
		t.Fatal("sign-in migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM monthly_sign_in_streaks",
		"FROM sign_in_streaks",
		"FROM sign_in_logs",
		"FROM sign_in_reward_claims",
		"WHERE deleted_at IS NULL",
		"ensureSignInPartialUniqueIndexes(DB)",
		"sign-in unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sign-in migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSignInPartialUniqueIndexes(")
	if helperStart < 0 {
		t.Fatal("ensureSignInPartialUniqueIndexes missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureInventoryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sign-in partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_monthly_sign_in_streaks_user_month_unique",
		"ON monthly_sign_in_streaks(user_id, month_key)",
		"idx_sign_in_streaks_user_unique",
		"ON sign_in_streaks(user_id)",
		"idx_sign_in_logs_user_date_unique",
		"ON sign_in_logs(user_id, sign_date)",
		"idx_sign_in_reward_claims_ref_unique",
		"ON sign_in_reward_claims(ref_id)",
		"WHERE ref_id <> '' AND deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sign-in partial index helper missing %q", want)
		}
	}
}

func TestSignInFutureDateDiagnosticUsesPlainValue(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "SIGN_DATE_IN_FUTURE":`)
	if start < 0 {
		t.Fatal("SIGN_DATE_IN_FUTURE branch missing")
	}
	end := strings.Index(text[start:], `case "CONCURRENT_SIGN_IN_RETRY":`)
	if end < 0 {
		t.Fatal("SIGN_DATE_IN_FUTURE branch boundary missing")
	}
	block := text[start : start+end]
	if !strings.Contains(block, "userID, formatPlainValue(todayKey)") {
		t.Fatal("sign-in future date diagnostic should format todayKey")
	}
	if strings.Contains(block, "userID, todayKey") {
		t.Fatal("sign-in future date diagnostic should not log raw todayKey")
	}
}
