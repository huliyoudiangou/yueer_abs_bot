package main

import (
	"os"
	"strings"
	"testing"

	"gorm.io/gorm/clause"
)

func TestDailyLifecycleReadErrorsAreVisibleAndDoNotMarkSuccess(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, `dailyLifecycleLastErrorKey`) {
		t.Fatal("daily lifecycle should persist the latest error")
	}

	start := strings.Index(text, "func runDailyLifecycleIfNeeded(")
	if start < 0 {
		t.Fatal("runDailyLifecycleIfNeeded missing")
	}
	end := strings.Index(text[start:], "func runAutoBackupIfNeeded(")
	if end < 0 {
		t.Fatal("runDailyLifecycleIfNeeded boundary missing")
	}
	scheduler := text[start : start+end]
	for _, want := range []string{
		"getSystemConfigStringChecked(dailyLifecycleLastSuccessDateKey)",
		"recordDailyLifecycleStateReadFailure(todayKey, dailyLifecycleLastSuccessDateKey, err)",
		"每日用户生命周期巡检状态读取失败，已跳过本轮巡检",
	} {
		if !strings.Contains(scheduler, want) && !strings.Contains(text, want) {
			t.Fatalf("daily lifecycle scheduler state read fail-closed guard missing %q", want)
		}
	}
	if strings.Contains(scheduler, "getSystemConfigString(dailyLifecycleLastSuccessDateKey) == todayKey") {
		t.Fatal("daily lifecycle scheduler still treats unreadable success date as empty")
	}
	errIdx := strings.Index(scheduler, "if err := runDailyLifecycleOperations(bot); err != nil")
	successIdx := strings.Index(scheduler, "setSystemConfigStringChecked(dailyLifecycleLastSuccessDateKey, todayKey)")
	if errIdx < 0 {
		t.Fatal("daily lifecycle scheduler must handle operation errors")
	}
	if successIdx < 0 {
		t.Fatal("daily lifecycle scheduler must still mark success after a clean run")
	}
	if errIdx > successIdx {
		t.Fatal("daily lifecycle errors must be handled before marking success")
	}
	errorBlock := scheduler[errIdx:successIdx]
	if !strings.Contains(errorBlock, "setSystemConfigError(dailyLifecycleLastErrorKey, err)") ||
		!strings.Contains(errorBlock, "return") {
		t.Fatal("daily lifecycle errors must be persisted and stop success marking")
	}
	for _, unsafe := range []string{
		"setSystemConfigString(dailyLifecycleLastSuccessDateKey, todayKey)",
		"setSystemConfigString(dailyLifecycleLastErrorKey, \"\")",
	} {
		if strings.Contains(scheduler, unsafe) {
			t.Fatalf("daily lifecycle success state still uses unchecked write: %s", unsafe)
		}
	}
	for _, want := range []string{
		"setSystemConfigStringChecked(dailyLifecycleLastSuccessDateKey, todayKey)",
		"recordDailyLifecycleStateWriteFailure(bot, todayKey, dailyLifecycleLastSuccessDateKey, err)",
		"setSystemConfigStringChecked(dailyLifecycleLastErrorKey, \"\")",
		"recordDailyLifecycleStateWriteFailure(bot, todayKey, dailyLifecycleLastErrorKey, err)",
		"func recordDailyLifecycleStateWriteFailure(bot *tgbotapi.BotAPI, todayKey string, key string, err error)",
		"每日用户生命周期巡检状态写入失败",
		"每日用户生命周期巡检已执行完成，但状态写入失败",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("daily lifecycle success state checked write missing %q", want)
		}
	}

	start = strings.Index(text, "func runDailyLifecycleOperations(")
	if start < 0 {
		t.Fatal("runDailyLifecycleOperations missing")
	}
	operation := text[start:]
	for _, want := range []string{
		"func runDailyLifecycleOperations(bot *tgbotapi.BotAPI) error",
		"Find(&users).Error",
		"notifySuperAdminsPlain(bot",
		"return err",
		"return nil",
	} {
		if !strings.Contains(operation, want) {
			t.Fatalf("daily lifecycle operation guard missing %q", want)
		}
	}
	if strings.Contains(operation, "DB.Where(\"is_whitelist = ? AND abs_user_id != ?\", false, \"\").Find(&users)\n") ||
		strings.Contains(operation, "DB.Where(\"is_whitelist = ? AND abs_user_id != ?\", false, \"\").Find(&users)\r\n") {
		t.Fatal("daily lifecycle user query still ignores DB errors")
	}

	start = strings.Index(text, "func formatBackgroundStatusReport(")
	if start < 0 {
		t.Fatal("formatBackgroundStatusReport missing")
	}
	statusReport := text[start:]
	if !strings.Contains(statusReport, "getSystemConfigStringForStatus(dailyLifecycleLastErrorKey") ||
		!strings.Contains(statusReport, "formatSystemConfigErrorForMarkdown(lifecycleError)") {
		t.Fatal("background status must expose daily lifecycle read errors safely")
	}
}

func TestBackgroundStatusReportConfigReadsAreChecked(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func formatBackgroundStatusReport(")
	if start < 0 {
		t.Fatal("formatBackgroundStatusReport missing")
	}
	end := strings.Index(text[start:], "func runDailyOperations(")
	if end < 0 {
		t.Fatal("formatBackgroundStatusReport boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"getSystemConfigStringForStatus(dailyLifecycleLastSuccessDateKey",
		"getSystemConfigStringForStatus(dailyLifecycleLastErrorKey",
		"lifecycleStateAvailable := lifecycleDateAvailable && lifecycleErrorAvailable",
		"!lifecycleStateAvailable",
		"formatSystemConfigTimeForStatus(dailyListeningRefreshLastAtKey)",
		"getSystemConfigStringForStatus(dailyListeningRefreshLastSuccessKey",
		"getSystemConfigStringForStatus(dailyListeningRefreshLastTotalKey",
		"getSystemConfigStringForStatus(dailyListeningRefreshLastSkippedKey",
		"getSystemConfigStringForStatus(dailyListeningRefreshLastErrorKey",
		"refreshStateAvailable := refreshAtAvailable && refreshSuccessAvailable && refreshTotalAvailable && refreshSkippedAvailable && refreshErrorAvailable",
		`refreshAt = "状态暂不可用"`,
		"getSystemConfigStringForStatus(autoBackupLastSuccessDateKey",
		"getSystemConfigStringForStatus(autoBackupLastErrorKey",
		"backupStateAvailable := backupDateAvailable && backupRetryCountAvailable && backupErrorAvailable",
		"!backupStateAvailable",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("background status checked config read missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"getSystemConfigString(dailyLifecycleLastSuccessDateKey)",
		"getSystemConfigString(dailyLifecycleLastErrorKey)",
		"formatSystemConfigTimeForReport(dailyListeningRefreshLastAtKey)",
		"getSystemConfigString(dailyListeningRefreshLastSuccessKey)",
		"getSystemConfigString(dailyListeningRefreshLastTotalKey)",
		"getSystemConfigString(dailyListeningRefreshLastSkippedKey)",
		"getSystemConfigString(dailyListeningRefreshLastErrorKey)",
		"getSystemConfigString(autoBackupLastSuccessDateKey)",
		"getSystemConfigString(autoBackupLastErrorKey)",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("background status still uses unchecked config read %q", unsafe)
		}
	}
}

func TestNoUncheckedSystemConfigTimeFallbackHelpers(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	for _, unsafe := range []string{
		"func getSystemConfigTime(key string) time.Time",
		"func formatSystemConfigTimeForReport(key string) string",
		"按空值处理",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("unchecked system config time fallback helper still exists: %q", unsafe)
		}
	}
}

func TestNotifierDiagnosticsUseSanitizedErrors(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainError(err)",
		"formatPlainValue(key)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("notifier diagnostics missing %q", want)
		}
	}
	rawErrFormat := string([]byte{'e', 'r', 'r', '=', '%', 'v'})
	if strings.Contains(text, rawErrFormat) {
		t.Fatal("notifier diagnostics should not log raw error values")
	}
}

func TestSystemConfigWritesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)

	claimStart := strings.Index(text, "func claimDailyFusionPoolCollectionInTx(")
	if claimStart < 0 {
		t.Fatal("claimDailyFusionPoolCollectionInTx missing")
	}
	claimEnd := strings.Index(text[claimStart:], "func upsertSystemConfigValueInTx(")
	if claimEnd < 0 {
		t.Fatal("claimDailyFusionPoolCollectionInTx boundary missing")
	}
	claimBlock := text[claimStart : claimStart+claimEnd]
	for _, want := range []string{
		"createRes := tx.Clauses(systemConfigKeyDoNothingClause()).Create(&cfg)",
		"createRes.Error != nil",
		"createRes.RowsAffected > 0",
		"res.RowsAffected > 0",
	} {
		if !strings.Contains(claimBlock, want) {
			t.Fatalf("daily fusion pool claim write guard missing %q", want)
		}
	}
	if strings.Contains(claimBlock, ".Create(&cfg).Error") || strings.Contains(claimBlock, "cfg.ID > 0") {
		t.Fatal("daily fusion pool claim still relies on unchecked create or GORM ID backfill")
	}

	helpers := []struct {
		name string
		next string
	}{
		{name: "upsertSystemConfigValueInTx", next: "func randomDailyFusionPoolAmount("},
		{name: "setSystemConfigStringChecked", next: "func setSystemConfigError("},
	}
	for _, helper := range helpers {
		start := strings.Index(text, "func "+helper.name+"(")
		if start < 0 {
			t.Fatalf("%s missing", helper.name)
		}
		end := strings.Index(text[start:], helper.next)
		if end < 0 {
			t.Fatalf("%s boundary missing", helper.name)
		}
		block := text[start : start+end]
		for _, want := range []string{
			"clause.OnConflict{",
			"TargetWhere: systemConfigKeyConflictTarget()",
			"res := ",
			"res.Error != nil",
			"res.RowsAffected == 0",
			"SYSTEM_CONFIG_UPSERT_MISSED",
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s write guard missing %q", helper.name, want)
			}
		}
		if strings.Contains(block, "}).Error") {
			t.Fatalf("%s still returns only upsert error", helper.name)
		}
	}
}

func TestSystemConfigConflictClausesTargetPartialUniqueIndex(t *testing.T) {
	checkTarget := func(name string, onConflict clause.OnConflict) {
		t.Helper()
		if len(onConflict.Columns) != 1 || onConflict.Columns[0].Name != "key" {
			t.Fatalf("%s columns = %#v", name, onConflict.Columns)
		}
		if len(onConflict.TargetWhere.Exprs) != 1 {
			t.Fatalf("%s target where = %#v", name, onConflict.TargetWhere.Exprs)
		}
		eq, ok := onConflict.TargetWhere.Exprs[0].(clause.Eq)
		if !ok {
			t.Fatalf("%s target where should use clause.Eq, got %#v", name, onConflict.TargetWhere.Exprs[0])
		}
		col, ok := eq.Column.(clause.Column)
		if !ok || col.Name != "deleted_at" || eq.Value != nil {
			t.Fatalf("%s target where should be deleted_at IS NULL, got %#v", name, eq)
		}
	}

	doNothing := systemConfigKeyDoNothingClause()
	checkTarget("system config do-nothing", doNothing)
	if !doNothing.DoNothing {
		t.Fatal("system config do-nothing clause must use DoNothing")
	}
}

func TestNotifierDateAndWeekDiagnosticsUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"每日天道灵气收集失败\\n\\n日期: %s\\n错误: %s\", formatPlainValue(todayKey), formatPlainError(err)",
		"每日天道灵气注入失败\\n\\n日期: %s\\n灵气: %d\\n错误: %s\", formatPlainValue(todayKey), amount, formatPlainError(err)",
		"每日天道灵气已注入奖池: date=%s amount=%d pool=%d/300\", formatPlainValue(todayKey)",
		"宗门周目标自动结算完成: week=%s settled=%d failed=%d\", formatPlainValue(targetWeekKey)",
		"formatPlainValue(sectWeekKey(targetWeek)), len(results), failed",
		"每日用户生命周期巡检完成: date=%s\", formatPlainValue(todayKey)",
		"开始执行每日自动加密备份: date=%s attempt=%d/%d\", formatPlainValue(todayKey)",
		"每日自动加密备份失败: date=%s attempt=%d/%d err=%s\", formatPlainValue(todayKey)",
		"每日自动加密备份成功: date=%s message_id=%d\", formatPlainValue(todayKey)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("notifier date/week diagnostic missing sanitized pattern %q", want)
		}
	}
	for _, unsafe := range []string{
		"日期: %s\\n错误: %s\", todayKey",
		"日期: %s\\n灵气: %d\\n错误: %s\", todayKey",
		"周次：%s\\n配置：%s\\n错误：%s\", targetWeekKey",
		"sectWeekKey(targetWeek), len(results), failed",
		"日期：%s\\n配置：%s\\n错误：%s\", todayKey",
		"date=%s amount=%d pool=%d/300\", todayKey",
		"date=%s attempt=%d/%d\", todayKey",
		"date=%s message_id=%d\", todayKey",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("notifier date/week diagnostic still uses raw dynamic field %q", unsafe)
		}
	}
}

func TestLifecycleSuccessLogsUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		`log.Printf("⛔ 已封禁过期用户: %s (TG: %d)", formatPlainValue(u.Username), u.TelegramID)`,
		`log.Printf("🗑 已彻底销毁逾期不续费用户: %s (TG: %d)", formatPlainValue(u.Username), u.TelegramID)`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("lifecycle success log should sanitize username, missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`log.Printf("⛔ 已封禁过期用户: %s (TG: %d)", u.Username, u.TelegramID)`,
		`log.Printf("🗑 已彻底销毁逾期不续费用户: %s (TG: %d)", u.Username, u.TelegramID)`,
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("lifecycle success log still uses raw username: %q", unsafe)
		}
	}
}

func TestDailyListeningRefreshStateReadErrorsSkipRefresh(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func runDailyListeningRefreshIfNeeded(")
	if start < 0 {
		t.Fatal("runDailyListeningRefreshIfNeeded missing")
	}
	end := strings.Index(text[start:], "func recordDailyListeningRefreshStateReadFailure(")
	if end < 0 {
		t.Fatal("runDailyListeningRefreshIfNeeded boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"lastAt, err := getSystemConfigTimeChecked(dailyListeningRefreshLastAtKey)",
		"recordDailyListeningRefreshStateReadFailure(bot, dailyListeningRefreshLastAtKey, err)",
		"lastAt, err = getSystemConfigTimeChecked(dailyListeningRefreshLastAtKey)",
		"return",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("daily listening refresh state read fail-closed guard missing %q", want)
		}
	}
	if strings.Contains(block, "getSystemConfigTime(dailyListeningRefreshLastAtKey)") {
		t.Fatal("daily listening refresh scheduler still treats unreadable last refresh time as zero")
	}
	for _, want := range []string{
		"setSystemConfigError(dailyListeningRefreshLastErrorKey, err)",
		"每日听书缓存刷新状态读取失败，已跳过本轮刷新",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
		"formatPlainError(err)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("daily listening refresh state read diagnostics missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"setSystemConfigString(dailyListeningRefreshLastAtKey, time.Now().Format(time.RFC3339))",
		"setSystemConfigString(dailyListeningRefreshLastSuccessKey, strconv.Itoa(success))",
		"setSystemConfigString(dailyListeningRefreshLastTotalKey, strconv.Itoa(total))",
		"setSystemConfigString(dailyListeningRefreshLastSkippedKey, strconv.Itoa(skipped))",
		"setSystemConfigString(dailyListeningRefreshLastErrorKey, \"\")",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("daily listening refresh success state still uses unchecked write: %s", unsafe)
		}
	}
	for _, want := range []string{
		"setSystemConfigStringChecked(dailyListeningRefreshLastAtKey, time.Now().Format(time.RFC3339))",
		"recordDailyListeningRefreshStateWriteFailure(bot, dailyListeningRefreshLastAtKey, err)",
		"setSystemConfigStringChecked(dailyListeningRefreshLastSuccessKey, strconv.Itoa(success))",
		"recordDailyListeningRefreshStateWriteFailure(bot, dailyListeningRefreshLastSuccessKey, err)",
		"setSystemConfigStringChecked(dailyListeningRefreshLastTotalKey, strconv.Itoa(total))",
		"recordDailyListeningRefreshStateWriteFailure(bot, dailyListeningRefreshLastTotalKey, err)",
		"setSystemConfigStringChecked(dailyListeningRefreshLastSkippedKey, strconv.Itoa(skipped))",
		"recordDailyListeningRefreshStateWriteFailure(bot, dailyListeningRefreshLastSkippedKey, err)",
		"setSystemConfigStringChecked(dailyListeningRefreshLastErrorKey, \"\")",
		"recordDailyListeningRefreshStateWriteFailure(bot, dailyListeningRefreshLastErrorKey, err)",
		"func recordDailyListeningRefreshStateWriteFailure(bot *tgbotapi.BotAPI, key string, err error)",
		"每日听书缓存刷新状态写入失败",
		"每日听书缓存刷新已执行完成，但状态写入失败",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("daily listening refresh success state checked write missing %q", want)
		}
	}
}

func TestDailyFusionPoolStateReadErrorsSkipInjection(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func runDailyFusionPoolCollectIfNeeded(")
	if start < 0 {
		t.Fatal("runDailyFusionPoolCollectIfNeeded missing")
	}
	end := strings.Index(text[start:], "func recordDailyFusionPoolStateReadFailure(")
	if end < 0 {
		t.Fatal("runDailyFusionPoolCollectIfNeeded boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"lastSuccessDate, err := getSystemConfigStringChecked(dailyFusionPoolLastSuccessDateKey)",
		"recordDailyFusionPoolStateReadFailure(bot, todayKey, dailyFusionPoolLastSuccessDateKey, err)",
		"lastSuccessDate, err = getSystemConfigStringChecked(dailyFusionPoolLastSuccessDateKey)",
		"return",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("daily fusion pool state read fail-closed guard missing %q", want)
		}
	}
	if strings.Contains(block, "getSystemConfigString(dailyFusionPoolLastSuccessDateKey) == todayKey") {
		t.Fatal("daily fusion pool scheduler still treats unreadable success date as uncollected")
	}
	for _, want := range []string{
		"setSystemConfigError(dailyFusionPoolLastErrorKey, err)",
		"每日天道灵气收集状态读取失败，已跳过本轮注入",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
		"日期：%s\\n配置：%s\\n错误：%s\", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err)",
		"formatPlainError(err)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("daily fusion pool state read diagnostics missing %q", want)
		}
	}
	if strings.Contains(text, "日期：%s\\n配置：%s\\n错误：%s\", todayKey, key, formatPlainError(err)") {
		t.Fatal("daily fusion pool state read notification still exposes raw date/key")
	}
}

func TestAutoBackupStateReadErrorsSkipBackupSend(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func runAutoBackupIfNeeded(")
	if start < 0 {
		t.Fatal("runAutoBackupIfNeeded missing")
	}
	end := strings.Index(text[start:], "func runAutoBackupAttempt(")
	if end < 0 {
		t.Fatal("runAutoBackupIfNeeded boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"getSystemConfigStringChecked(autoBackupLastSuccessDateKey)",
		"getTodayAutoBackupRetryCountChecked(todayKey)",
		"getSystemConfigTimeChecked(autoBackupLastAttemptAtKey)",
		"recordAutoBackupStateReadFailure(todayKey",
		"return",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("auto backup state read fail-closed guard missing %q", want)
		}
	}
	if strings.Contains(block, "getSystemConfigString(autoBackupLastSuccessDateKey) == todayKey") ||
		strings.Contains(block, "retryCount := getTodayAutoBackupRetryCount(todayKey)") ||
		strings.Contains(block, "lastAttemptAt := getSystemConfigTime(autoBackupLastAttemptAtKey)") {
		t.Fatal("auto backup scheduler still treats unreadable state as empty/default")
	}
	for _, want := range []string{
		"func getSystemConfigStringChecked(",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"func getTodayAutoBackupRetryCountChecked(",
		"invalid auto backup retry count",
		"自动备份状态读取失败，已跳过本轮备份",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("auto backup checked state helper missing %q", want)
		}
	}
}

func TestAutoBackupRetryCountStatusReadErrorsAreVisible(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)

	helperStart := strings.Index(text, "func getTodayAutoBackupRetryCountForStatus(")
	if helperStart < 0 {
		t.Fatal("getTodayAutoBackupRetryCountForStatus missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func getTodayAutoBackupRetryCountChecked(")
	if helperEnd < 0 {
		t.Fatal("getTodayAutoBackupRetryCountForStatus boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"getTodayAutoBackupRetryCountChecked(todayKey)",
		"状态暂不可用",
		`return 0, "读取失败", false`,
		"return count, strconv.Itoa(count), true",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("auto backup retry count status helper missing %q", want)
		}
	}
	if strings.Contains(helperBlock, "按 0 展示") || strings.Contains(helperBlock, "return 0\n") {
		t.Fatal("auto backup retry count status helper still collapses read errors into 0")
	}

	backupStart := strings.Index(text, "func formatBackupStatusReport(")
	if backupStart < 0 {
		t.Fatal("formatBackupStatusReport missing")
	}
	backupEnd := strings.Index(text[backupStart:], "func formatBackgroundStatusReport(")
	if backupEnd < 0 {
		t.Fatal("formatBackupStatusReport boundary missing")
	}
	backupBlock := text[backupStart : backupStart+backupEnd]
	for _, want := range []string{
		"retryCount, retryCountText, retryCountAvailable := getTodayAutoBackupRetryCountForStatus(todayKey)",
		"!backupStateAvailable",
		"自动备份状态暂不可用",
		"今日重试：`%s/%d`",
		"escapeMarkdown(retryCountText)",
	} {
		if !strings.Contains(backupBlock, want) {
			t.Fatalf("backup status retry count read-error display missing %q", want)
		}
	}

	backgroundStart := strings.Index(text, "func formatBackgroundStatusReport(")
	if backgroundStart < 0 {
		t.Fatal("formatBackgroundStatusReport missing")
	}
	backgroundBlock := text[backgroundStart:]
	for _, want := range []string{
		"backupRetryCount, backupRetryCountText, backupRetryCountAvailable := getTodayAutoBackupRetryCountForStatus(todayKey)",
		"!backupStateAvailable",
		"自动备份状态暂不可用",
		"今日备份重试：`%s/%d`",
		"escapeMarkdown(backupRetryCountText)",
	} {
		if !strings.Contains(backgroundBlock, want) {
			t.Fatalf("background status retry count read-error display missing %q", want)
		}
	}
	if strings.Contains(text, "getTodayAutoBackupRetryCount(todayKey)") {
		t.Fatal("status reports still use unchecked auto backup retry count helper")
	}
}

func TestBackupStatusReportConfigReadsAreChecked(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)

	helperStart := strings.Index(text, "func getSystemConfigStringForStatus(")
	if helperStart < 0 {
		t.Fatal("getSystemConfigStringForStatus missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func recordAutoBackupStateReadFailure(")
	if helperEnd < 0 {
		t.Fatal("status config helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"getSystemConfigStringChecked(key)",
		"formatPlainValue(key), formatPlainError(err)",
		`return "读取失败", false`,
		"getSystemConfigTimeChecked(key)",
		`return "无", true`,
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("backup status config read helper missing %q", want)
		}
	}

	backupStart := strings.Index(text, "func formatBackupStatusReport(")
	if backupStart < 0 {
		t.Fatal("formatBackupStatusReport missing")
	}
	backupEnd := strings.Index(text[backupStart:], "func formatBackgroundStatusReport(")
	if backupEnd < 0 {
		t.Fatal("formatBackupStatusReport boundary missing")
	}
	backupBlock := text[backupStart : backupStart+backupEnd]
	for _, want := range []string{
		"getSystemConfigStringForStatus(autoBackupLastSuccessDateKey",
		"formatSystemConfigTimeForStatus(autoBackupLastSuccessAtKey)",
		"formatSystemConfigTimeForStatus(autoBackupLastAttemptAtKey)",
		"getSystemConfigStringForStatus(autoBackupLastMessageIDKey",
		"getSystemConfigStringForStatus(autoBackupLastErrorKey",
		"getSystemConfigStringForStatus(backupLastPinnedMessageIDKey",
		"getSystemConfigStringForStatus(backupLastPinErrorKey",
		"backupStateAvailable := lastSuccessDateAvailable",
		"!backupStateAvailable",
	} {
		if !strings.Contains(backupBlock, want) {
			t.Fatalf("backup status checked read missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"getSystemConfigString(autoBackupLastSuccessDateKey)",
		"formatSystemConfigTimeForReport(autoBackupLastSuccessAtKey)",
		"formatSystemConfigTimeForReport(autoBackupLastAttemptAtKey)",
		"getSystemConfigString(autoBackupLastMessageIDKey)",
		"getSystemConfigString(autoBackupLastErrorKey)",
		"getSystemConfigString(backupLastPinnedMessageIDKey)",
		"getSystemConfigString(backupLastPinErrorKey)",
	} {
		if strings.Contains(backupBlock, unsafe) {
			t.Fatalf("backup status report still uses unchecked config read %q", unsafe)
		}
	}
}

func TestAutoBackupSuccessStateWritesAreChecked(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func runAutoBackupAttempt(")
	if start < 0 {
		t.Fatal("runAutoBackupAttempt missing")
	}
	end := strings.Index(text[start:], "func autoBackupRetryDelay(")
	if end < 0 {
		t.Fatal("runAutoBackupAttempt boundary missing")
	}
	block := text[start : start+end]
	for _, unsafe := range []string{
		"setSystemConfigString(autoBackupLastAttemptAtKey, now.Format(time.RFC3339))",
		"setSystemConfigString(autoBackupRetryCountKey, strconv.Itoa(attemptNo))",
		"setSystemConfigError(autoBackupLastErrorKey, err)",
		"setSystemConfigString(autoBackupLastSuccessDateKey, todayKey)",
		"setSystemConfigString(autoBackupLastSuccessAtKey, time.Now().Format(time.RFC3339))",
		"setSystemConfigString(autoBackupLastMessageIDKey, strconv.Itoa(messageID))",
		"setSystemConfigString(autoBackupRetryCountKey, \"0\")",
		"setSystemConfigString(autoBackupLastErrorKey, \"\")",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("auto backup success state still uses unchecked write: %s", unsafe)
		}
	}
	for _, want := range []string{
		"setSystemConfigStringChecked(autoBackupLastAttemptAtKey, now.Format(time.RFC3339))",
		"recordAutoBackupAttemptStateWriteFailure(bot, todayKey, autoBackupLastAttemptAtKey, err)",
		"setSystemConfigStringChecked(autoBackupRetryCountKey, strconv.Itoa(attemptNo))",
		"recordAutoBackupAttemptStateWriteFailure(bot, todayKey, autoBackupRetryCountKey, writeErr)",
		"setSystemConfigStringChecked(autoBackupLastErrorKey, formatPlainError(err))",
		"recordAutoBackupAttemptStateWriteFailure(bot, todayKey, autoBackupLastErrorKey, writeErr)",
		"setSystemConfigStringChecked(autoBackupLastSuccessDateKey, todayKey)",
		"recordAutoBackupStateWriteFailure(bot, todayKey, autoBackupLastSuccessDateKey, err)",
		"setSystemConfigStringChecked(autoBackupLastSuccessAtKey, time.Now().Format(time.RFC3339))",
		"recordAutoBackupStateWriteFailure(bot, todayKey, autoBackupLastSuccessAtKey, err)",
		"setSystemConfigStringChecked(autoBackupLastMessageIDKey, strconv.Itoa(messageID))",
		"recordAutoBackupStateWriteFailure(bot, todayKey, autoBackupLastMessageIDKey, err)",
		"setSystemConfigStringChecked(autoBackupRetryCountKey, \"0\")",
		"recordAutoBackupStateWriteFailure(bot, todayKey, autoBackupRetryCountKey, err)",
		"setSystemConfigStringChecked(autoBackupLastErrorKey, \"\")",
		"recordAutoBackupStateWriteFailure(bot, todayKey, autoBackupLastErrorKey, err)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("auto backup success state checked write missing %q", want)
		}
	}
	for _, want := range []string{
		"func recordAutoBackupStateWriteFailure(bot *tgbotapi.BotAPI, todayKey string, key string, err error)",
		"setSystemConfigError(autoBackupLastErrorKey, err)",
		"自动备份状态写入失败",
		"每日自动加密备份已发送成功，但状态写入失败",
		"func recordAutoBackupAttemptStateWriteFailure(bot *tgbotapi.BotAPI, todayKey string, key string, err error)",
		"自动备份尝试状态写入失败",
		"避免重复外发备份或重试次数失真",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("auto backup success state write diagnostics missing %q", want)
		}
	}
}

func TestSectWeeklyAutoSettlementStateReadErrorsSkipSettlement(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func runSectWeeklyTaskAutoSettlementIfNeeded(")
	if start < 0 {
		t.Fatal("runSectWeeklyTaskAutoSettlementIfNeeded missing")
	}
	end := strings.Index(text[start:], "func recordSectWeeklyTaskAutoSettleStateReadFailure(")
	if end < 0 {
		t.Fatal("runSectWeeklyTaskAutoSettlementIfNeeded boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"getSystemConfigStringChecked(sectWeeklyTaskAutoSettleLastWeekKey)",
		"recordSectWeeklyTaskAutoSettleStateReadFailure(bot, targetWeekKey, sectWeeklyTaskAutoSettleLastWeekKey, err)",
		"return",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect weekly auto settlement state read fail-closed guard missing %q", want)
		}
	}
	if strings.Contains(block, "getSystemConfigString(sectWeeklyTaskAutoSettleLastWeekKey) == targetWeekKey") {
		t.Fatal("sect weekly auto settlement still treats unreadable state as unsettled")
	}
	if strings.Contains(block, "setSystemConfigString(sectWeeklyTaskAutoSettleLastWeekKey, targetWeekKey)") {
		t.Fatal("sect weekly auto settlement success state still uses unchecked write")
	}
	for _, want := range []string{
		"setSystemConfigStringChecked(sectWeeklyTaskAutoSettleLastWeekKey, targetWeekKey)",
		"recordSectWeeklyTaskAutoSettleStateWriteFailure(bot, targetWeekKey, sectWeeklyTaskAutoSettleLastWeekKey, err)",
		"func recordSectWeeklyTaskAutoSettleStateWriteFailure(bot *tgbotapi.BotAPI, targetWeekKey string, key string, err error)",
		"宗门周目标自动结算状态写入失败",
		"宗门周目标自动结算已执行完成，但状态写入失败",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect weekly auto settlement state write guard missing %q", want)
		}
	}
	for _, want := range []string{
		"宗门周目标自动结算状态读取失败，已跳过本轮结算",
		"notifySuperAdminsPlain(bot",
		"周次：%s\\n配置：%s\\n错误：%s\", formatPlainValue(targetWeekKey), formatPlainValue(key), formatPlainError(err)",
		"formatPlainError(err)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect weekly auto settlement state read diagnostics missing %q", want)
		}
	}
	if strings.Contains(text, "周次：%s\\n配置：%s\\n错误：%s\", targetWeekKey, key, formatPlainError(err)") {
		t.Fatal("sect weekly auto settlement state read notification still exposes raw week/key")
	}
}

func TestBackupPinStateReadErrorsSkipPinHandoff(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func pinLatestBackupMessage(")
	if start < 0 {
		t.Fatal("pinLatestBackupMessage missing")
	}
	end := strings.Index(text[start:], "func recordBackupPinStateReadFailure(")
	if end < 0 {
		t.Fatal("pinLatestBackupMessage boundary missing")
	}
	block := text[start : start+end]
	for _, unsafe := range []string{
		"oldMsgID, _ := strconv.Atoi(getSystemConfigString(backupLastPinnedMessageIDKey))",
		"getSystemConfigString(backupLastPinnedMessageIDKey)",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("backup pin handoff still silently defaults unreadable state: %s", unsafe)
		}
	}
	for _, want := range []string{
		"rawOldMsgID, err := getSystemConfigStringChecked(backupLastPinnedMessageIDKey)",
		"recordBackupPinStateReadFailure(bot, backupLastPinnedMessageIDKey, err)",
		"parsedOldMsgID, parseErr := strconv.Atoi(rawOldMsgID)",
		"recordBackupPinStateReadFailure(bot, backupLastPinnedMessageIDKey, fmt.Errorf",
		"setSystemConfigStringChecked(backupLastPinnedMessageIDKey, strconv.Itoa(messageID))",
		"recordBackupPinStateWriteFailure(bot, backupLastPinnedMessageIDKey, err)",
		"setSystemConfigStringChecked(backupLastPinErrorKey, \"\")",
		"recordBackupPinStateWriteFailure(bot, backupLastPinErrorKey, err)",
		"return",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("backup pin state read fail-closed guard missing %q", want)
		}
	}
	for _, want := range []string{
		"setSystemConfigError(backupLastPinErrorKey, err)",
		"备份置顶状态读取失败，已跳过本次置顶交接",
		"func recordBackupPinStateWriteFailure(bot *tgbotapi.BotAPI, key string, err error)",
		"备份置顶状态写入失败",
		"备份消息置顶已执行，但置顶状态写入失败",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
		"formatPlainError(err)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("backup pin state read diagnostics missing %q", want)
		}
	}
}

func TestAutoSuspendLocalFailureAuditIsChecked(t *testing.T) {
	source, err := os.ReadFile("notifier.go")
	if err != nil {
		t.Fatalf("read notifier.go err = %v", err)
	}
	text := string(source)
	if strings.Contains(text, `writeAuditLog(0, "AUTO_SUSPEND_EXPIRED_USER_LOCAL_FAILED"`) {
		t.Fatal("auto suspend local failure audit must not use unchecked writeAuditLog")
	}
	for _, want := range []string{
		`writeAuditLogInTx(DB, 0, "AUTO_SUSPEND_EXPIRED_USER_LOCAL_FAILED"`,
		"formatPlainValue(u.Username), u.TelegramID, formatPlainValue(u.AbsUserID)",
		"auto suspend local failure audit write failed",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
		"生命周期自动封禁已更新 ABS",
		"请立即人工核查",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing checked auto suspend failure audit guard %q", want)
		}
	}
	for _, unsafe := range []string{
		"u.Username, u.TelegramID, u.AbsUserID, u.ExpireAt.Format(time.RFC3339)",
		"deleted.Username, deleted.TelegramID, deleted.AbsUserID, AppConfig.AccountGraceDays",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("lifecycle audit detail still contains raw dynamic fields: %q", unsafe)
		}
	}
	for _, want := range []string{
		`deleteLocalUserWithAudit(0, u.TelegramID, u.AbsUserID, "AUTO_DELETE_EXPIRED_USER"`,
		"formatPlainValue(deleted.Username), deleted.TelegramID, formatPlainValue(deleted.AbsUserID)",
		"formatPlainValue(absDeleteResult)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing auto delete audit detail guard %q", want)
		}
	}
}
