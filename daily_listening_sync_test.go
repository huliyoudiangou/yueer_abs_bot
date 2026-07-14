package main

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm/clause"
)

func TestPersonalReportUsesUnifiedDailyListeningSync(t *testing.T) {
	body, err := os.ReadFile("abs_client.go")
	if err != nil {
		t.Fatalf("read abs_client.go: %v", err)
	}

	src := string(body)
	fnStart := strings.Index(src, "func (c *AbsClient) GetPersonalReport")
	if fnStart < 0 {
		t.Fatalf("GetPersonalReport not found")
	}
	fnEnd := strings.Index(src[fnStart:], "\nfunc (c *AbsClient) GetServerStats")
	if fnEnd < 0 {
		t.Fatalf("GetPersonalReport end not found")
	}
	fn := src[fnStart : fnStart+fnEnd]

	if !strings.Contains(fn, "syncCultivationFromDailyListeningStatsAt(u.TelegramID, now)") {
		t.Fatalf("GetPersonalReport must use unified daily listening sync helper")
	}
	if !strings.Contains(fn, "if syncedEffectiveTotalHours, ok :=") {
		t.Fatalf("GetPersonalReport must check unified daily listening sync result before using it")
	}
	for _, want := range []string{
		"statsBody, statsCode, statsErr := c.sendRequest",
		"听书报告 ABS 统计读取失败",
		"听书报告 ABS 统计状态异常",
		"听书报告 ABS 统计解析失败",
		"听书统计暂时读取失败，请稍后再试。",
		"听书报告 ABS 书籍进度读取失败",
		"听书报告 ABS 书籍进度状态异常",
		"听书报告 ABS 书籍进度解析失败",
		"finishedCountText := \"读取失败\"",
		"听书报告本地用户读取失败，跳过净修为同步",
		"听书报告每日统计写入失败，使用本次 ABS 数据降级展示",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"formatPlainValue(absUserID)",
		"formatPlainError(err)",
		"if statsDays == nil",
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("GetPersonalReport local user read guard missing %q", want)
		}
	}
	if strings.Contains(fn, "if len(statsDays) == 0") {
		t.Fatalf("GetPersonalReport must distinguish a missing days field from a present empty days object")
	}
	if strings.Contains(fn, "persistCultivationAudioTime(u.TelegramID") {
		t.Fatalf("GetPersonalReport must not directly persist total_audio_time; use unified sync helper")
	}
	if strings.Contains(fn, `if DB.Where("abs_user_id = ?", absUserID).First(&u).Error == nil`) {
		t.Fatalf("GetPersonalReport must not silently skip local user read errors")
	}
	if strings.Contains(fn, "statsBody, statsCode, _ := c.sendRequest") {
		t.Fatalf("GetPersonalReport must not ignore ABS stats request errors")
	}
	if strings.Contains(fn, "userBody, userCode, _ := c.sendRequest") {
		t.Fatalf("GetPersonalReport must not ignore ABS user progress request errors")
	}
}

func TestDailyListeningStatsRecordChecksRowsAffected(t *testing.T) {
	body, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go: %v", err)
	}
	src := string(body)

	start := strings.Index(src, "func recordDailyListeningStatsFromABSDays(")
	if start < 0 {
		t.Fatalf("recordDailyListeningStatsFromABSDays not found")
	}
	end := strings.Index(src[start:], "func dailyListeningStatOnConflict(")
	if end < 0 {
		t.Fatalf("recordDailyListeningStatsFromABSDays end not found")
	}
	recordBlock := src[start : start+end]
	for _, want := range []string{
		") error",
		"res := DB.Clauses(dailyListeningStatOnConflict(time.Now())).Create(&records)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"DAILY_LISTENING_STATS_UPSERT_MISSED",
		"每日听书统计写入失败",
		"每日听书统计写入未命中",
		"formatPlainError(err)",
		"return nil",
	} {
		if !strings.Contains(recordBlock, want) {
			t.Fatalf("daily listening stat record guard missing %q", want)
		}
	}
	if strings.Contains(recordBlock, "Create(&records).Error") {
		t.Fatalf("recordDailyListeningStatsFromABSDays still checks only create error")
	}
	for _, forbidden := range []string{"log.Printf(\"\u95c1", "log.Printf(\"\u95c2"} {
		if strings.Contains(recordBlock, forbidden) {
			t.Fatalf("recordDailyListeningStatsFromABSDays diagnostics contain mojibake: %q", forbidden)
		}
	}

	refreshStart := strings.Index(src, "func refreshDailyListeningStatsFromABS(")
	if refreshStart < 0 {
		t.Fatalf("refreshDailyListeningStatsFromABS not found")
	}
	refreshEnd := strings.Index(src[refreshStart:], "func refreshSectMembersDailyListeningStats(")
	if refreshEnd < 0 {
		t.Fatalf("refreshDailyListeningStatsFromABS end not found")
	}
	refreshBlock := src[refreshStart : refreshStart+refreshEnd]
	for _, want := range []string{
		"if err := recordDailyListeningStatsFromABSDays",
		"每日听书 ABS 统计读取失败",
		"每日听书 ABS 统计解析失败",
		"formatPlainValue(absUserID)",
		"formatPlainError(err)",
		"return nil, false",
		"return stats.Days, true",
	} {
		if !strings.Contains(refreshBlock, want) {
			t.Fatalf("daily listening refresh persistence failure guard missing %q", want)
		}
	}
	for _, forbidden := range []string{"log.Printf(\"\u95c1", "log.Printf(\"\u95c2"} {
		if strings.Contains(refreshBlock, forbidden) {
			t.Fatalf("refreshDailyListeningStatsFromABS diagnostics contain mojibake: %q", forbidden)
		}
	}

	memberRefreshStart := strings.Index(src, "func refreshSectMembersDailyListeningStats(")
	if memberRefreshStart < 0 {
		t.Fatalf("refreshSectMembersDailyListeningStats not found")
	}
	memberRefreshEnd := strings.Index(src[memberRefreshStart:], "func getTodaySectListeningHoursFromABS(")
	if memberRefreshEnd < 0 {
		t.Fatalf("refreshSectMembersDailyListeningStats end not found")
	}
	memberRefreshBlock := src[memberRefreshStart : memberRefreshStart+memberRefreshEnd]
	for _, want := range []string{
		"宗门成员每日听书统计刷新名单读取失败",
		"formatPlainError(err)",
	} {
		if !strings.Contains(memberRefreshBlock, want) {
			t.Fatalf("sect member daily listening refresh diagnostic guard missing %q", want)
		}
	}
	for _, forbidden := range []string{"log.Printf(\"\u95c1", "log.Printf(\"\u95c2"} {
		if strings.Contains(memberRefreshBlock, forbidden) {
			t.Fatalf("refreshSectMembersDailyListeningStats diagnostics contain mojibake: %q", forbidden)
		}
	}
}

func TestDailyListeningStatOnConflictTargetsPartialUniqueIndex(t *testing.T) {
	onConflict := dailyListeningStatOnConflict(time.Now())
	if len(onConflict.Columns) != 2 ||
		onConflict.Columns[0].Name != "user_id" ||
		onConflict.Columns[1].Name != "day_key" {
		t.Fatalf("daily listening upsert columns = %#v", onConflict.Columns)
	}
	if len(onConflict.TargetWhere.Exprs) != 1 {
		t.Fatalf("daily listening upsert target where = %#v", onConflict.TargetWhere.Exprs)
	}
	eq, ok := onConflict.TargetWhere.Exprs[0].(clause.Eq)
	if !ok {
		t.Fatalf("daily listening upsert target where should use clause.Eq, got %#v", onConflict.TargetWhere.Exprs[0])
	}
	column, ok := eq.Column.(clause.Column)
	if !ok || column.Name != "deleted_at" || eq.Value != nil {
		t.Fatalf("daily listening upsert target should constrain deleted_at IS NULL, got %#v", eq)
	}
	assignmentsText := fmt.Sprintf("%#v", onConflict.DoUpdates)
	for _, want := range []string{
		"raw_seconds",
		"capped_seconds",
		"effective_hours",
		"last_fetched_at",
		"updated_at",
		"deleted_at",
	} {
		if !strings.Contains(assignmentsText, want) {
			t.Fatalf("daily listening upsert updates missing %q: %#v", want, onConflict.DoUpdates)
		}
	}
}

func TestDailyListeningStatsMigrationCreatesPartialUniqueIndex(t *testing.T) {
	body, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	src := string(body)

	legacyStart := strings.Index(src, "assertNoDuplicateGroups(\"sect_listening_daily_progresses(user_id, day_key)\"")
	if legacyStart < 0 {
		t.Fatal("sect listening daily progress migration block not found")
	}
	legacyEnd := strings.Index(src[legacyStart:], "assertNoDuplicateGroups(\"daily_listening_stats(user_id, day_key)\"")
	if legacyEnd < 0 {
		t.Fatal("sect listening daily progress migration block boundary missing")
	}
	legacyBlock := src[legacyStart : legacyStart+legacyEnd]
	for _, want := range []string{
		"ensureSectListeningDailyProgressPartialUniqueIndex(DB)",
		"sect listening daily progress unique index migration failed; startup blocked",
	} {
		if !strings.Contains(legacyBlock, want) {
			t.Fatalf("sect listening daily progress partial index migration missing %q", want)
		}
	}

	legacyHelperStart := strings.Index(src, "func ensureSectListeningDailyProgressPartialUniqueIndex(")
	if legacyHelperStart < 0 {
		t.Fatal("ensureSectListeningDailyProgressPartialUniqueIndex missing")
	}
	legacyHelperEnd := strings.Index(src[legacyHelperStart:], "func ensureDailyListeningStatsPartialUniqueIndex(")
	if legacyHelperEnd < 0 {
		t.Fatal("ensureSectListeningDailyProgressPartialUniqueIndex boundary missing")
	}
	legacyHelperBlock := src[legacyHelperStart : legacyHelperStart+legacyHelperEnd]
	for _, want := range []string{
		"FROM sqlite_master",
		"idx_sect_listening_daily_progresses_user_day_unique",
		"DROP INDEX IF EXISTS idx_sect_listening_daily_progresses_user_day_unique",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_sect_listening_daily_progresses_user_day_unique",
		"ON sect_listening_daily_progresses(user_id, day_key)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(legacyHelperBlock, want) {
			t.Fatalf("sect listening daily progress partial index helper missing %q", want)
		}
	}

	start := strings.Index(src, "assertNoDuplicateGroups(\"daily_listening_stats(user_id, day_key)\"")
	if start < 0 {
		t.Fatal("daily listening stats migration block not found")
	}
	end := strings.Index(src[start:], "CREATE UNIQUE INDEX IF NOT EXISTS idx_referral_codes_user_unique")
	if end < 0 {
		t.Fatal("daily listening stats migration block boundary missing")
	}
	block := src[start : start+end]
	for _, want := range []string{
		"ensureDailyListeningStatsPartialUniqueIndex(DB)",
		"daily listening stats unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("daily listening stats partial index migration missing %q", want)
		}
	}

	helperStart := strings.Index(src, "func ensureDailyListeningStatsPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureDailyListeningStatsPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(src[helperStart:], "func ensureMarketplaceOpenDisputeUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("ensureDailyListeningStatsPartialUniqueIndex boundary missing")
	}
	helperBlock := src[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"FROM sqlite_master",
		"idx_daily_listening_stats_user_day_unique",
		"DROP INDEX IF EXISTS idx_daily_listening_stats_user_day_unique",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_daily_listening_stats_user_day_unique",
		"ON daily_listening_stats(user_id, day_key)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("daily listening stats partial index helper missing %q", want)
		}
	}
}

func TestDailyListeningSyncUsesFreshPersistedStatsOnly(t *testing.T) {
	reportBody, err := os.ReadFile("abs_client.go")
	if err != nil {
		t.Fatalf("read abs_client.go: %v", err)
	}
	reportSrc := string(reportBody)
	reportStart := strings.Index(reportSrc, "func (c *AbsClient) GetPersonalReport")
	if reportStart < 0 {
		t.Fatalf("GetPersonalReport not found")
	}
	reportEnd := strings.Index(reportSrc[reportStart:], "\nfunc (c *AbsClient) GetServerStats")
	if reportEnd < 0 {
		t.Fatalf("GetPersonalReport end not found")
	}
	reportBlock := reportSrc[reportStart : reportStart+reportEnd]
	if !strings.Contains(reportBlock, "if err := recordDailyListeningStatsFromABSDays(u.TelegramID, absUserID, statsDays, now); err == nil {") {
		t.Fatalf("personal report should only read persisted daily stats after successful write")
	}

	realmBody, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go: %v", err)
	}
	realmSrc := string(realmBody)
	realmStart := strings.Index(realmSrc, "func getSectSecretRealmListeningSnapshot(")
	if realmStart < 0 {
		t.Fatalf("getSectSecretRealmListeningSnapshot not found")
	}
	realmBlock := realmSrc[realmStart:]
	if !strings.Contains(realmBlock, "if err := recordDailyListeningStatsFromABSDays(u.TelegramID, absUserID, stats.Days, time.Now()); err == nil {") {
		t.Fatalf("secret realm snapshot should only read persisted daily stats after successful write")
	}
	for _, want := range []string{
		"宗门秘境每日统计写入失败，使用本次 ABS 数据降级计算",
		"宗门秘境本地用户读取失败，使用本次 ABS 数据降级计算",
		"errors.Is(readErr, gorm.ErrRecordNotFound)",
		"formatPlainValue(absUserID)",
		"formatPlainError(err)",
	} {
		if !strings.Contains(realmBlock, want) {
			t.Fatalf("secret realm daily listening fallback diagnostic missing %q", want)
		}
	}
}

func TestDailyListeningCommandsDistinguishReadErrors(t *testing.T) {
	body, err := os.ReadFile("daily_listening_commands.go")
	if err != nil {
		t.Fatalf("read daily_listening_commands.go: %v", err)
	}
	src := string(body)

	sectStart := strings.Index(src, "func handleRefreshSectDailyListeningStatsCommand(")
	if sectStart < 0 {
		t.Fatal("handleRefreshSectDailyListeningStatsCommand not found")
	}
	sectEnd := strings.Index(src[sectStart:], "func handleRefreshAllDailyListeningStats(")
	if sectEnd < 0 {
		t.Fatal("handleRefreshSectDailyListeningStatsCommand boundary missing")
	}
	sectBlock := src[sectStart : sectStart+sectEnd]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"刷新宗门每日净修为读取成员档案失败",
		"formatPlainError(err)",
		"宗门成员档案读取失败，请稍后再试。",
	} {
		if !strings.Contains(sectBlock, want) {
			t.Fatalf("sect daily listening refresh read-error guard missing %q", want)
		}
	}
	if strings.Contains(sectBlock, "if err := DB.Where(\"user_id = ?\", msg.From.ID).First(&member).Error; err != nil {\n\t\treplyText(bot, msg.Chat.ID, \"❌ 您当前没有加入宗门。\")") {
		t.Fatal("sect daily listening refresh still treats DB read errors as not in sect")
	}

	queryStart := strings.Index(src, "func handleQueryDailyListeningStat(")
	if queryStart < 0 {
		t.Fatal("handleQueryDailyListeningStat not found")
	}
	queryEnd := strings.Index(src[queryStart:], "func formatDailyListeningStat(")
	if queryEnd < 0 {
		t.Fatal("handleQueryDailyListeningStat boundary missing")
	}
	queryBlock := src[queryStart : queryStart+queryEnd]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"查询每日净修为记录失败",
		"formatPlainValue(dayKey)",
		"formatPlainError(err)",
		"每日净修为记录读取失败，请稍后再试。",
	} {
		if !strings.Contains(queryBlock, want) {
			t.Fatalf("daily listening query read-error guard missing %q", want)
		}
	}
	if strings.Contains(queryBlock, "if err := DB.Where(\"user_id = ? AND day_key = ?\", targetID, dayKey).First(&stat).Error; err != nil {\n\t\treplyText(bot, msg.Chat.ID, \"📭 未找到该用户该日期的每日净修为记录。\")") {
		t.Fatal("daily listening query still treats DB read errors as missing records")
	}
}

func TestMyInfoDailyListeningStatReadFailureDisplaysUnavailable(t *testing.T) {
	sectBody, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go: %v", err)
	}
	sectSrc := string(sectBody)
	helperStart := strings.Index(sectSrc, "func getTodayDailyListeningStatChecked(")
	if helperStart < 0 {
		t.Fatal("getTodayDailyListeningStatChecked missing")
	}
	helperEnd := strings.Index(sectSrc[helperStart:], "func getTodaySectListeningHoursFromCache(")
	if helperEnd < 0 {
		t.Fatal("getTodayDailyListeningStatChecked boundary missing")
	}
	helperBlock := sectSrc[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"return DailyListeningStat{}, false, nil",
		"return DailyListeningStat{}, false, err",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("checked today daily listening helper missing %q", want)
		}
	}

	stateBody, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	stateSrc := string(stateBody)
	start := strings.Index(stateSrc, `if strings.Contains(text, "我的信息") {`)
	if start < 0 {
		t.Fatal("my info branch missing")
	}
	end := strings.Index(stateSrc[start:], `if strings.Contains(text, "签到") {`)
	if end < 0 {
		t.Fatal("my info branch boundary missing")
	}
	block := stateSrc[start : start+end]
	for _, want := range []string{
		"getTodayDailyListeningStatChecked(userID, time.Now())",
		"我的信息今日净修为读取失败",
		"formatPlainError(err)",
		"todayEffectiveHoursText = \"`读取失败`\"",
		"🌅 **今日净修为**: %s 小时",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("my info daily listening read-failure guard missing %q", want)
		}
	}
	if strings.Contains(block, "if todayStat, ok := getTodayDailyListeningStat(userID, time.Now()); ok {") {
		t.Fatal("my info still uses unchecked today daily listening helper")
	}
}

func TestOfficialDailyListeningRefreshRepairsInflatedLiveToday(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	now := time.Date(2026, 7, 12, 9, 0, 0, 0, loc)

	// A present but empty days object means ABS reports no listening today. The
	// generated upsert row must overwrite any old provisional/live 24-hour value.
	records := buildOfficialDailyListeningRecords(1001, "abs-user", map[string]float64{}, now)
	if len(records) != 1 {
		t.Fatalf("records len = %d, want 1 authoritative today row", len(records))
	}
	repaired := records[0]
	if repaired.DayKey != "2026-07-12" {
		t.Fatalf("repaired day = %q, want 2026-07-12", repaired.DayKey)
	}
	if repaired.RawSeconds != 0 || repaired.CappedSeconds != 0 || repaired.EffectiveHours != 0 {
		t.Fatalf("repaired values = raw %.0f capped %.0f effective %.3f, want all zero", repaired.RawSeconds, repaired.CappedSeconds, repaired.EffectiveHours)
	}
	if repaired.OfficialRawSeconds != 0 || repaired.LiveRawSeconds != 0 {
		t.Fatalf("repaired source values = official %.0f live %.0f, want zero", repaired.OfficialRawSeconds, repaired.LiveRawSeconds)
	}
	if repaired.Source != "abs_days" || repaired.FetchStatus != "ok" || repaired.RefreshReason != "abs_refresh" {
		t.Fatalf("repaired metadata = %q/%q/%q", repaired.Source, repaired.FetchStatus, repaired.RefreshReason)
	}
}

func TestDailyListeningRecordDoesNotUseHistoricalSessionsAsLiveInput(t *testing.T) {
	body, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go: %v", err)
	}
	src := string(body)
	start := strings.Index(src, "func recordDailyListeningStatsFromABSDays(")
	end := strings.Index(src[start:], "func dailyListeningStatOnConflict(")
	if start < 0 || end < 0 {
		t.Fatal("daily listening record function boundary missing")
	}
	block := src[start : start+end]
	builderStart := strings.Index(src, "func buildOfficialDailyListeningRecords(")
	if builderStart < 0 {
		t.Fatal("official daily listening record builder missing")
	}
	builderBlock := src[builderStart : start+end]
	for _, forbidden := range []string{
		"collectDailyListeningSessionData(",
		"rebalanceABSDaysForCrossDaySessions(",
		"liveDeltas[",
	} {
		if strings.Contains(block+builderBlock, forbidden) {
			t.Fatalf("official daily cultivation must not consume historical session input %q", forbidden)
		}
	}
	for _, want := range []string{
		"if days != nil",
		"todayKey := sectDayKey(fetchedAt)",
		"LiveRawSeconds:        0",
		"Source:                \"abs_days\"",
	} {
		if !strings.Contains(builderBlock, want) {
			t.Fatalf("official daily repair guard missing %q", want)
		}
	}
}
