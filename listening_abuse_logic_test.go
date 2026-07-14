package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestListeningAbuseScanDayKeyUsesBeijingYesterday(t *testing.T) {
	now := time.Date(2026, 6, 15, 20, 30, 0, 0, time.UTC)
	if got := listeningAbuseScanDayKey(now); got != "2026-06-15" {
		t.Fatalf("expected Beijing yesterday to be 2026-06-15, got %s", got)
	}

	now = time.Date(2026, 6, 15, 18, 30, 0, 0, time.UTC)
	if got := listeningAbuseScanDayKey(now); got != "2026-06-15" {
		t.Fatalf("expected Beijing yesterday to stay 2026-06-15 after local midnight, got %s", got)
	}
}

func TestPreviousDayKey(t *testing.T) {
	got, ok := previousDayKey("2026-03-01")
	if !ok || got != "2026-02-28" {
		t.Fatalf("expected previous day 2026-02-28, got %s ok=%t", got, ok)
	}

	if got, ok := previousDayKey("bad-day"); ok || got != "" {
		t.Fatalf("invalid day should fail, got %s ok=%t", got, ok)
	}
}

func TestListeningAbuseDayKeyValidationAndOrdering(t *testing.T) {
	if !isListeningAbuseDayKey("2026-06-15") {
		t.Fatal("valid date key should be accepted")
	}
	if isListeningAbuseDayKey("2026-6-15") {
		t.Fatal("non-normalized date key should be rejected")
	}
	if !listeningAbuseDayBefore("2026-06-14", "2026-06-15") {
		t.Fatal("previous normalized day should be before effective start")
	}
	if listeningAbuseDayBefore("2026-06-15", "2026-06-15") {
		t.Fatal("same day should not be before effective start")
	}
	if listeningAbuseDayBefore("bad-day", "2026-06-15") {
		t.Fatal("invalid day should not compare as before")
	}
}

func TestListeningAbuseThresholds(t *testing.T) {
	if listeningAbuseShouldWarn(15.0) {
		t.Fatal("exactly 15 hours should not warn")
	}
	if !listeningAbuseShouldWarn(15.01) {
		t.Fatal("more than 15 hours should warn")
	}
	if listeningAbuseShouldFreeze(15.01, 15.0) {
		t.Fatal("previous day must also be more than 15 hours")
	}
	if !listeningAbuseShouldFreeze(15.01, 15.02) {
		t.Fatal("two consecutive days over 15 hours should freeze")
	}
}

func TestListeningAbuseDailyStatReadErrorsAreLoggedAndSkipped(t *testing.T) {
	source, err := os.ReadFile("listening_abuse.go")
	if err != nil {
		t.Fatalf("read listening_abuse.go err = %v", err)
	}
	text := string(source)

	helperStart := strings.Index(text, "func dailyRawListeningHours(")
	if helperStart < 0 {
		t.Fatal("dailyRawListeningHours missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func isUserExpiredAt(")
	if helperEnd < 0 {
		t.Fatal("dailyRawListeningHours boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"func dailyRawListeningHours(userID int64, dayKey string) (float64, bool, error)",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"return 0, false, nil",
		"return 0, false, err",
		"stat.FetchStatus == dailyListeningLiveProvisionalStatus",
		"stat.Source == dailyListeningLiveCheckpointSource",
		"stat.FetchStatus == dailyListeningCrossDayStatus",
		"stat.Source == dailyListeningCrossDaySource",
		"stat.OfficialRawSeconds / 3600",
		"return stat.RawSeconds / 3600, true",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("daily raw listening stat read guard missing %q", want)
		}
	}

	scanStart := strings.Index(text, "func scanListeningAbuseForDay(")
	if scanStart < 0 {
		t.Fatal("scanListeningAbuseForDay missing")
	}
	scanEnd := strings.Index(text[scanStart:], "func dailyRawListeningHours(")
	if scanEnd < 0 {
		t.Fatal("scanListeningAbuseForDay boundary missing")
	}
	scanBlock := text[scanStart : scanStart+scanEnd]
	for _, want := range []string{
		"rawHours, hasToday, err := dailyRawListeningHours(u.TelegramID, dayKey)",
		"listening abuse daily stat read failed",
		"previousRawHours, hasPrevious, err = dailyRawListeningHours(u.TelegramID, previousKey)",
		"listening abuse previous daily stat read failed",
		"formatPlainValue(dayKey)",
		"formatPlainValue(previousKey)",
		"formatPlainError(err)",
		"skipped++",
	} {
		if !strings.Contains(scanBlock, want) {
			t.Fatalf("listening abuse stat read failure handling missing %q", want)
		}
	}
	if strings.Contains(scanBlock, "previousRawHours, _ = dailyRawListeningHours") {
		t.Fatal("previous-day listening stat read errors are still ignored")
	}
}

func TestListeningAbuseUsesOnlyOfficialValuesForCrossDayCorrections(t *testing.T) {
	hours, ok := listeningAbuseRawHoursFromStat(DailyListeningStat{
		RawSeconds:         8 * 3600,
		OfficialRawSeconds: 0,
		Source:             dailyListeningCrossDaySource,
		FetchStatus:        dailyListeningCrossDayStatus,
	})
	if ok || hours != 0 {
		t.Fatalf("cross-day-only correction should be excluded, got hours=%.2f ok=%v", hours, ok)
	}

	hours, ok = listeningAbuseRawHoursFromStat(DailyListeningStat{
		RawSeconds:         10 * 3600,
		OfficialRawSeconds: 17 * 3600,
		Source:             dailyListeningCrossDaySource,
		FetchStatus:        dailyListeningCrossDayStatus,
	})
	if !ok || hours != 17 {
		t.Fatalf("cross-day correction should retain official abuse value, got hours=%.2f ok=%v", hours, ok)
	}
}

func TestIsUserExpiredAt(t *testing.T) {
	now := time.Date(2026, 6, 15, 3, 0, 0, 0, time.UTC)
	if isUserExpiredAt(User{}, now) {
		t.Fatal("nil expiry should not be expired")
	}

	future := now.Add(time.Second)
	if isUserExpiredAt(User{ExpireAt: &future}, now) {
		t.Fatal("future expiry should not be expired")
	}

	exact := now
	if !isUserExpiredAt(User{ExpireAt: &exact}, now) {
		t.Fatal("expiry at now should be expired")
	}
}

func TestListeningAbuseReleaseErrorIsSanitizedAndChecked(t *testing.T) {
	source, err := os.ReadFile("listening_abuse.go")
	if err != nil {
		t.Fatalf("read listening_abuse.go err = %v", err)
	}
	text := string(source)
	if strings.Contains(text, `Update("release_error", err.Error())`) {
		t.Fatal("release_error must not persist raw err.Error()")
	}
	for _, want := range []string{
		"func recordListeningAbuseReleaseError(recordID uint, userID int64, releaseErr error)",
		`Update("release_error", formatPlainError(releaseErr))`,
		"res.Error != nil",
		"res.RowsAffected == 0",
		"listening abuse release error persistence failed",
		"listening abuse release error persistence missed record",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing sanitized release error guard %q", want)
		}
	}
}

func TestListeningAbuseNoticeErrorIsSanitizedAndChecked(t *testing.T) {
	source, err := os.ReadFile("listening_abuse.go")
	if err != nil {
		t.Fatalf("read listening_abuse.go err = %v", err)
	}
	text := string(source)
	for _, unsafe := range []string{
		`Update("notice_error", noticeErr.Error())`,
		`Update("notice_error", fmt.Sprintf("private=%s group=%s", formatPlainError(noticeErr), formatPlainError(groupErr)))`,
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("notice_error still uses unsafe unchecked persistence: %s", unsafe)
		}
	}
	for _, want := range []string{
		"func recordListeningAbuseNoticeError(recordID uint, userID int64, noticeText string)",
		"formatPlainValue(noticeText)",
		`Update("notice_error", noticeText)`,
		"res.Error != nil",
		"res.RowsAffected == 0",
		"listening abuse notice error persistence failed",
		"listening abuse notice error persistence missed record",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing sanitized notice error guard %q", want)
		}
	}
}

func TestListeningAbuseFailureAuditsAreChecked(t *testing.T) {
	source, err := os.ReadFile("listening_abuse.go")
	if err != nil {
		t.Fatalf("read listening_abuse.go err = %v", err)
	}
	text := string(source)
	for _, unsafe := range []string{
		`writeAuditLog(0, "LISTENING_ABUSE_FREEZE_FAILED"`,
		`writeAuditLog(0, "LISTENING_ABUSE_AMNESTY_FAILED"`,
		`writeAuditLog(0, "LISTENING_ABUSE_RELEASE_FAILED"`,
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("failure audit still uses unchecked writeAuditLog: %s", unsafe)
		}
	}
	for _, want := range []string{
		"func writeListeningAbuseFailureAudit(bot *tgbotapi.BotAPI, action string, userID int64, label string, detail string)",
		"writeAuditLogInTx(DB, 0, action, strconv.FormatInt(userID, 10), 0, detail)",
		"listening abuse failure audit write failed",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
		"formatPlainValue(label), userID, formatPlainValue(action), formatPlainError(err)",
		`writeListeningAbuseFailureAudit(bot, "LISTENING_ABUSE_FREEZE_FAILED"`,
		`writeListeningAbuseFailureAudit(bot, "LISTENING_ABUSE_AMNESTY_FAILED"`,
		`writeListeningAbuseFailureAudit(bot, "LISTENING_ABUSE_RELEASE_FAILED"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing checked failure audit guard %q", want)
		}
	}
	if strings.Contains(text, "label, userID, formatPlainValue(action), formatPlainError(err)") {
		t.Fatal("failure audit notification should format label before notifying super admins")
	}
}

func TestListeningAbuseReleaseBlockedAuditUsesPlainValue(t *testing.T) {
	source, err := os.ReadFile("listening_abuse.go")
	if err != nil {
		t.Fatalf("read listening_abuse.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func markListeningAbuseFreezeReleaseBlocked(")
	if start < 0 {
		t.Fatal("markListeningAbuseFreezeReleaseBlocked missing")
	}
	end := strings.Index(text[start:], "func sendListeningAbusePrivateNotice(")
	if end < 0 {
		t.Fatal("markListeningAbuseFreezeReleaseBlocked boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`writeAuditLogInTx(tx, 0, "LISTENING_ABUSE_RELEASE_BLOCKED"`,
		`recordID, formatPlainValue(reason)`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("release blocked audit guard missing %q", want)
		}
	}
	if strings.Contains(block, "recordID, reason)") {
		t.Fatal("release blocked audit should not persist raw reason in audit detail")
	}
}

func TestListeningAbuseDateDiagnosticsUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("listening_abuse.go")
	if err != nil {
		t.Fatalf("read listening_abuse.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"listening abuse scan failed: day=%s err=%s\", formatPlainValue(targetDay), formatPlainError(err)",
		"播放异常巡检失败\\n\\n日期: %s\\n错误: %s\", formatPlainValue(targetDay), formatPlainError(err)",
		"listening abuse scan finished: day=%s warnings=%d freezes=%d skipped=%d\", formatPlainValue(targetDay)",
		"listening abuse effective start day initialized: day=%s\", formatPlainValue(dayKey)",
		"listening abuse freeze failed: user=%d day=%s err=%s\", u.TelegramID, formatPlainValue(dayKey), formatPlainError(err)",
		"formatPlainValue(dayKey), rawHours, formatPlainValue(previousKey), previousRawHours, formatPlainError(err)",
		"listening abuse warning failed: user=%d day=%s err=%s\", u.TelegramID, formatPlainValue(dayKey), formatPlainError(err)",
		"query pre-effective listening abuse freezes failed: effective_start=%s err=%s\", formatPlainValue(effectiveStartDay), formatPlainError(err)",
		"amnesty listening abuse freeze failed: record=%d user=%d effective_start=%s err=%s\", record.ID, record.UserID, formatPlainValue(effectiveStartDay), formatPlainError(err)",
		"record.ID, formatPlainValue(record.DayKey), formatPlainValue(effectiveStartDay)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("listening abuse date diagnostic missing sanitized pattern %q", want)
		}
	}
	for _, unsafe := range []string{
		"day=%s err=%s\", targetDay",
		"日期: %s\\n错误: %s\", targetDay",
		"day=%s warnings=%d freezes=%d skipped=%d\", targetDay",
		"day=%s\", dayKey",
		"day=%s err=%s\", u.TelegramID, dayKey",
		"dayKey, rawHours, previousKey, previousRawHours, formatPlainError(err)",
		"effective_start=%s err=%s\", effectiveStartDay",
		"effective_start=%s err=%s\", record.ID, record.UserID, effectiveStartDay",
		"record.ID, record.DayKey, effectiveStartDay",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("listening abuse date diagnostic still uses raw dynamic field %q", unsafe)
		}
	}
}

func TestListeningAbuseWarningAuditIsTransactional(t *testing.T) {
	source, err := os.ReadFile("listening_abuse.go")
	if err != nil {
		t.Fatalf("read listening_abuse.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createListeningAbuseWarning(")
	if start < 0 {
		t.Fatal("createListeningAbuseWarning missing")
	}
	end := strings.Index(text[start:], "func startListeningAbuseFreeze(")
	if end < 0 {
		t.Fatal("createListeningAbuseWarning boundary missing")
	}
	block := text[start : start+end]
	for _, unsafe := range []string{
		`writeAuditLog(0, "LISTENING_ABUSE_WARNING"`,
		`DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&record)`,
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("warning audit still uses unchecked or non-transactional path: %s", unsafe)
		}
	}
	for _, want := range []string{
		"if err := DB.Transaction(func(tx *gorm.DB) error",
		"created, err := createListeningAbuseRecordInTx(tx, &record)",
		"if !created",
		`writeAuditLogInTx(tx, 0, "LISTENING_ABUSE_WARNING"`,
		"record_id=%d day=%s raw_hours=%.2f threshold=%.2f",
		"sendListeningAbusePrivateNotice(bot, u.TelegramID, text)",
		"recordListeningAbuseNoticeError(record.ID, u.TelegramID, formatPlainError(noticeErr))",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("missing transactional warning audit guard %q", want)
		}
	}
	if strings.Index(block, "sendListeningAbusePrivateNotice(bot, u.TelegramID, text)") < strings.Index(block, "if err := DB.Transaction(func(tx *gorm.DB) error") {
		t.Fatal("warning private notice must remain after the DB transaction")
	}
}

func TestListeningAbuseRecordCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("listening_abuse.go")
	if err != nil {
		t.Fatalf("read listening_abuse.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createListeningAbuseRecordInTx(")
	if start < 0 {
		t.Fatal("createListeningAbuseRecordInTx missing")
	}
	end := strings.Index(text[start:], "func createListeningAbuseWarning(")
	if end < 0 {
		t.Fatal("createListeningAbuseRecordInTx boundary missing")
	}
	helperBlock := text[start : start+end]
	for _, want := range []string{
		"entry := *record",
		"res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"*record = entry",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("listening abuse record create helper guard missing %q", want)
		}
	}

	warningStart := strings.Index(text, "func createListeningAbuseWarning(")
	if warningStart < 0 {
		t.Fatal("createListeningAbuseWarning missing")
	}
	freezeStart := strings.Index(text, "func startListeningAbuseFreeze(")
	if freezeStart < 0 {
		t.Fatal("startListeningAbuseFreeze missing")
	}
	warningBlock := text[warningStart:freezeStart]
	freezeBlock := text[freezeStart:]
	for name, block := range map[string]string{
		"warning": warningBlock,
		"freeze":  freezeBlock,
	} {
		if !strings.Contains(block, "createListeningAbuseRecordInTx(tx, &record)") {
			t.Fatalf("%s path does not use createListeningAbuseRecordInTx", name)
		}
		if strings.Contains(block, "tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&record).Error") {
			t.Fatalf("%s path still creates listening abuse record directly", name)
		}
	}
}

func TestListeningAbuseRecordMigrationsReplaceFullUniqueIndexes(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("listening_abuse_records(user_id, day_key, action)"`)
	if start < 0 {
		t.Fatal("listening abuse record migration block missing")
	}
	end := strings.Index(text[start:], `INSERT OR IGNORE INTO daily_listening_stats`)
	if end < 0 {
		t.Fatal("listening abuse record migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM listening_abuse_records",
		"WHERE deleted_at IS NULL",
		"WHERE deleted_at IS NULL AND action = 'freeze' AND status = 'active'",
		"ensureListeningAbuseRecordPartialUniqueIndexes(DB)",
		"listening abuse record unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("listening abuse record migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureListeningAbuseRecordPartialUniqueIndexes(")
	if helperStart < 0 {
		t.Fatal("ensureListeningAbuseRecordPartialUniqueIndexes missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureGardenPlotPartialUniqueIndexes(")
	if helperEnd < 0 {
		t.Fatal("listening abuse record partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_listening_abuse_records_user_day_action_unique",
		"ON listening_abuse_records(user_id, day_key, action)",
		"idx_listening_abuse_records_active_user_unique",
		"ON listening_abuse_records(user_id)",
		"WHERE deleted_at IS NULL",
		"WHERE deleted_at IS NULL AND action = 'freeze' AND status = 'active'",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("listening abuse record partial index helper missing %q", want)
		}
	}
}

func TestListeningAbuseStateReadErrorsSkipScan(t *testing.T) {
	source, err := os.ReadFile("listening_abuse.go")
	if err != nil {
		t.Fatalf("read listening_abuse.go err = %v", err)
	}
	text := string(source)
	for _, unsafe := range []string{
		"ensureListeningAbuseEffectiveStartDay(now)",
		"getSystemConfigString(listeningAbuseLastScanDayKey) == targetDay",
		"getSystemConfigString(listeningAbuseEffectiveStartDayKey)",
		"setSystemConfigString(listeningAbuseEffectiveStartDayKey, dayKey)",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("listening abuse scheduler still uses defaulting state read: %s", unsafe)
		}
	}
	for _, want := range []string{
		"effectiveStartDay, err := ensureListeningAbuseEffectiveStartDayChecked(now)",
		"errors.Is(err, errListeningAbuseEffectiveStartDayInit)",
		"recordListeningAbuseStateWriteFailure(bot, listeningAbuseEffectiveStartDayKey, err)",
		"recordListeningAbuseStateReadFailure(bot, listeningAbuseEffectiveStartDayKey, err)",
		"lastScanDay, err := getSystemConfigStringChecked(listeningAbuseLastScanDayKey)",
		"recordListeningAbuseStateReadFailure(bot, listeningAbuseLastScanDayKey, err)",
		"errListeningAbuseEffectiveStartDayInit",
		"func ensureListeningAbuseEffectiveStartDayChecked(now time.Time) (string, error)",
		"getSystemConfigStringChecked(listeningAbuseEffectiveStartDayKey)",
		"setSystemConfigStringChecked(listeningAbuseEffectiveStartDayKey, dayKey)",
		"func recordListeningAbuseStateReadFailure(bot *tgbotapi.BotAPI, key string, err error)",
		"setSystemConfigError(listeningAbuseLastErrorKey, err)",
		"播放异常风控状态读取失败，已跳过本轮巡检",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
		"配置：%s\\n错误：%s\", formatPlainValue(key), formatPlainError(err)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing listening abuse state read fail-closed guard %q", want)
		}
	}
	if strings.Contains(text, "配置：%s\\n错误：%s\", key, formatPlainError(err)") {
		t.Fatal("listening abuse state read failure notification still exposes raw config key")
	}
}

func TestListeningAbuseScanSuccessStateWritesAreChecked(t *testing.T) {
	source, err := os.ReadFile("listening_abuse.go")
	if err != nil {
		t.Fatalf("read listening_abuse.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func runListeningAbuseMonitorIfNeeded(")
	if start < 0 {
		t.Fatal("runListeningAbuseMonitorIfNeeded missing")
	}
	end := strings.Index(text[start:], "func listeningAbuseScanDayKey(")
	if end < 0 {
		t.Fatal("runListeningAbuseMonitorIfNeeded boundary missing")
	}
	block := text[start : start+end]
	for _, unsafe := range []string{
		"setSystemConfigString(listeningAbuseLastScanDayKey, targetDay)",
		"setSystemConfigString(listeningAbuseLastScanAtKey, time.Now().Format(time.RFC3339))",
		"setSystemConfigString(listeningAbuseLastErrorKey, \"\")",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("listening abuse scan success state still uses unchecked write: %s", unsafe)
		}
	}
	for _, want := range []string{
		"setSystemConfigStringChecked(listeningAbuseLastScanDayKey, targetDay)",
		"recordListeningAbuseStateWriteFailure(bot, listeningAbuseLastScanDayKey, err)",
		"setSystemConfigStringChecked(listeningAbuseLastScanAtKey, time.Now().Format(time.RFC3339))",
		"recordListeningAbuseStateWriteFailure(bot, listeningAbuseLastScanAtKey, err)",
		"setSystemConfigStringChecked(listeningAbuseLastErrorKey, \"\")",
		"recordListeningAbuseStateWriteFailure(bot, listeningAbuseLastErrorKey, err)",
		"return",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("listening abuse scan success checked write missing %q", want)
		}
	}
	for _, want := range []string{
		"func recordListeningAbuseStateWriteFailure(bot *tgbotapi.BotAPI, key string, err error)",
		"setSystemConfigError(listeningAbuseLastErrorKey, err)",
		"播放异常风控状态写入失败",
		"相关巡检已中止或完成状态未确认",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("listening abuse scan success state diagnostics missing %q", want)
		}
	}
}
