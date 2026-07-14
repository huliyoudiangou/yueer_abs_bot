package main

import (
	"os"
	"strings"
	"testing"
)

func TestCultivationRankDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		username string
		sectName string
		want     string
	}{
		{name: "plain username", username: "makizhang", want: "makizhang"},
		{name: "fallback username", username: " ", want: "神秘道友"},
		{name: "with sect", username: "makizhang", sectName: "青云宗", want: "makizhang【青云宗】"},
		{name: "escape markdown", username: "a_b*c[`", sectName: "宗_门", want: "a\\_b\\*c\\[\\`【宗\\_门】"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cultivationRankDisplayName(tt.username, tt.sectName); got != tt.want {
				t.Fatalf("cultivationRankDisplayName() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestBreakthroughWalletReadErrorDoesNotLookInsufficient(t *testing.T) {
	source, err := os.ReadFile("cultivation.go")
	if err != nil {
		t.Fatalf("read cultivation.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"突破前钱包读取失败",
		"钱包读取失败，请稍后再尝试突破。",
		"First(&u).Error",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("breakthrough wallet read guard missing %q", want)
		}
	}
	if strings.Contains(text, "DB.Where(\"telegram_id = ?\", userID).First(&u)\n") ||
		strings.Contains(text, "DB.Where(\"telegram_id = ?\", userID).First(&u)\r\n") {
		t.Fatal("breakthrough flow still treats wallet read errors as zero points")
	}
}

func TestBreakthroughInventoryReadErrorDoesNotAutoBuy(t *testing.T) {
	source, err := os.ReadFile("cultivation.go")
	if err != nil {
		t.Fatalf("read cultivation.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"突破前丹药库存读取失败",
		"乾坤袋读取失败，请稍后再尝试突破。",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"formatPlainValue(req.PillName)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("breakthrough inventory read guard missing %q", want)
		}
	}
	if strings.Contains(text, `DB.Where("user_id = ? AND item_name = ?", userID, req.PillName).First(&inv).Error == nil && inv.Quantity > 0`) {
		t.Fatal("breakthrough flow still treats inventory read errors as missing pills")
	}
}

func TestBreakthroughCultivationReadErrorsFailClosed(t *testing.T) {
	source, err := os.ReadFile("cultivation.go")
	if err != nil {
		t.Fatalf("read cultivation.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func ExecuteBreakthrough(")
	if start < 0 {
		t.Fatal("ExecuteBreakthrough missing")
	}
	end := strings.Index(text[start:], "func handleCultivationRank(")
	if end < 0 {
		t.Fatal("ExecuteBreakthrough boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`tx.Where("user_id = ?", userID).First(&cul).Error`,
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"return errCultivationNotFound",
		"return err",
		"大境界突破成功：%d -> %d",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("breakthrough cultivation read guard missing %q", want)
		}
	}
	if strings.Contains(block, `if err := tx.Where("user_id = ?", userID).First(&cul).Error; err != nil {
			return errCultivationNotFound
		}`) {
		t.Fatal("breakthrough transaction still maps all cultivation read errors to not found")
	}
	if strings.Contains(block, "澶у") || strings.Contains(block, "鐣岀") {
		t.Fatal("breakthrough contribution reason contains mojibake")
	}
}

func TestBreakthroughAutoBuyPointDescriptionSanitizesPillName(t *testing.T) {
	if got := cultivationPointDescriptionName("  pill\nalpha\tbeta  "); got != "pill alpha beta" {
		t.Fatalf("cultivationPointDescriptionName() = %q", got)
	}
	if got := cultivationPointDescriptionName("\n\t"); got != "-" {
		t.Fatalf("empty cultivation point description name fallback = %q", got)
	}

	source, err := os.ReadFile("cultivation.go")
	if err != nil {
		t.Fatalf("read cultivation.go err = %v", err)
	}
	text := string(source)
	if strings.Contains(text, `fmt.Sprintf("突破自动代购【%s】，消耗 %d 积分", req.PillName, req.PointsCost)`) {
		t.Fatal("breakthrough auto-buy point description should not persist raw pill name")
	}
	if !strings.Contains(text, `fmt.Sprintf("突破自动代购【%s】，消耗 %d 积分", cultivationPointDescriptionName(req.PillName), req.PointsCost)`) {
		t.Fatal("breakthrough auto-buy point description should sanitize pill name")
	}
}

func TestCultivationDiagnosticsUseSanitizedErrors(t *testing.T) {
	source, err := os.ReadFile("cultivation.go")
	if err != nil {
		t.Fatalf("read cultivation.go err = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "formatPlainError(err)") {
		t.Fatal("cultivation diagnostics should use formatPlainError")
	}
	rawErrFormat := string([]byte{'e', 'r', 'r', '=', '%', 'v'})
	if strings.Contains(text, rawErrFormat) {
		t.Fatal("cultivation diagnostics should not log raw error values")
	}
}

func TestGetOrCreateCultivationCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("cultivation.go")
	if err != nil {
		t.Fatalf("read cultivation.go err = %v", err)
	}
	text := string(source)

	getStart := strings.Index(text, "func GetOrCreateCultivation(")
	if getStart < 0 {
		t.Fatal("GetOrCreateCultivation missing")
	}
	getEnd := strings.Index(text[getStart:], "func createCultivationIfMissing(")
	if getEnd < 0 {
		t.Fatal("GetOrCreateCultivation boundary missing")
	}
	getBlock := text[getStart : getStart+getEnd]
	if !strings.Contains(getBlock, "createCultivationIfMissing(userID)") {
		t.Fatal("GetOrCreateCultivation should create through helper")
	}
	if strings.Contains(getBlock, "DB.Create(&cul)") {
		t.Fatal("GetOrCreateCultivation still creates cultivation without RowsAffected guard")
	}

	createStart := strings.Index(text, "func createCultivationIfMissing(")
	if createStart < 0 {
		t.Fatal("createCultivationIfMissing missing")
	}
	createEnd := strings.Index(text[createStart:], "func GetRealmName(")
	if createEnd < 0 {
		t.Fatal("createCultivationIfMissing boundary missing")
	}
	createBlock := text[createStart : createStart+createEnd]
	for _, want := range []string{
		"CULTIVATION_CREATE_INVALID",
		"res := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&cul)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		`DB.Where("user_id = ?", userID).First(&existing).Error`,
	} {
		if !strings.Contains(createBlock, want) {
			t.Fatalf("cultivation create helper guard missing %q", want)
		}
	}
	if strings.Contains(createBlock, "}).Error") {
		t.Fatal("cultivation create helper still checks only create error")
	}
}

func TestCultivationNilCallersFailClosed(t *testing.T) {
	files := map[string]string{}
	for _, name := range []string{"cultivation.go", "daily_listening_commands.go", "abs_client.go", "world_boss.go", "state_machine.go"} {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s err = %v", name, err)
		}
		files[name] = string(data)
	}

	checks := []struct {
		name string
		file string
		want []string
	}{
		{
			name: "breakthrough precheck",
			file: "cultivation.go",
			want: []string{
				"突破前修仙档案读取失败",
				"修仙档案读取失败，请稍后再尝试突破。",
				"if cul == nil {",
			},
		},
		{
			name: "daily listening sync",
			file: "daily_listening_commands.go",
			want: []string{
				"每日净修为同步读取修仙档案失败",
				"if cul == nil {",
				"return 0, false",
			},
		},
		{
			name: "personal report fallback",
			file: "abs_client.go",
			want: []string{
				"if cul := GetOrCreateCultivation(u.TelegramID); cul != nil",
				"effectiveTotalHours = cul.TotalAudioTime",
			},
		},
		{
			name: "world boss cultivation refresh",
			file: "world_boss.go",
			want: []string{
				"世界 Boss 刷新修仙档案读取失败",
				"if cul == nil {",
				"return",
			},
		},
		{
			name: "my info and admin query",
			file: "state_machine.go",
			want: []string{
				"修仙档案暂时读取失败，请稍后重试。",
				"历史补偿检查修仙档案读取失败",
				"境界变化检查修仙档案读取失败",
				"吞服丹药成功后修仙档案读取失败",
				"newRealm := \"`读取失败`\"",
				"targetCultivationHoursText := \"`读取失败`\"",
				"targetRealm = \"`读取失败`\"",
			},
		},
	}
	for _, tc := range checks {
		for _, want := range tc.want {
			if !strings.Contains(files[tc.file], want) {
				t.Fatalf("%s missing nil cultivation guard marker %q", tc.name, want)
			}
		}
	}
	for _, unsafe := range []string{
		"effectiveTotalHours = GetOrCreateCultivation(u.TelegramID).TotalAudioTime",
		"targetRealm, targetCul.TotalAudioTime, targetCul.TribulationFails",
	} {
		for file, text := range files {
			if strings.Contains(text, unsafe) {
				t.Fatalf("%s still contains unsafe cultivation dereference %q", file, unsafe)
			}
		}
	}
}

func TestCultivationRealmSyncChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("cultivation.go")
	if err != nil {
		t.Fatalf("read cultivation.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func SyncCultivationRealm(")
	if start < 0 {
		t.Fatal("SyncCultivationRealm missing")
	}
	end := strings.Index(text[start:], "func persistCultivationAudioTime(")
	if end < 0 {
		t.Fatal("SyncCultivationRealm boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`Where("user_id = ? AND major_realm = ?", cul.UserID, oldMajor)`,
		"res.Error",
		"res.RowsAffected == 0",
		"同步修仙段位未命中",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("cultivation realm sync rows affected guard missing %q", want)
		}
	}
	if strings.Contains(block, "Updates(updates).Error") {
		t.Fatal("cultivation realm sync still ignores RowsAffected")
	}
}

func TestApplyPointDeltaCreatesTransactionChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("cultivation.go")
	if err != nil {
		t.Fatalf("read cultivation.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func applyPointDeltaInTx(")
	if start < 0 {
		t.Fatal("applyPointDeltaInTx missing")
	}
	end := strings.Index(text[start:], "func ExecuteBreakthrough(")
	if end < 0 {
		t.Fatal("applyPointDeltaInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := updateQuery.UpdateColumn",
		"res.RowsAffected == 0",
		"txRes := tx.Create(&PointTransaction{",
		"txRes.Error != nil",
		"txRes.RowsAffected == 0",
		"POINT_TRANSACTION_CREATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("applyPointDeltaInTx guard missing %q", want)
		}
	}
	if strings.Contains(block, "}).Error") {
		t.Fatal("applyPointDeltaInTx still checks only point transaction create error")
	}
}

func TestBreakthroughAttemptCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("cultivation.go")
	if err != nil {
		t.Fatalf("read cultivation.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createBreakthroughAttemptInTx(")
	if start < 0 {
		t.Fatal("createBreakthroughAttemptInTx missing")
	}
	end := strings.Index(text[start:], "func HandleBreakthroughRequest(")
	if end < 0 {
		t.Fatal("createBreakthroughAttemptInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"BREAKTHROUGH_ATTEMPT_CREATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("breakthrough attempt create guard missing %q", want)
		}
	}

	executeStart := strings.Index(text, "func ExecuteBreakthrough(")
	if executeStart < 0 {
		t.Fatal("ExecuteBreakthrough missing")
	}
	executeEnd := strings.Index(text[executeStart:], "func handleCultivationRank(")
	if executeEnd < 0 {
		t.Fatal("ExecuteBreakthrough boundary missing")
	}
	executeBlock := text[executeStart : executeStart+executeEnd]
	if got := strings.Count(executeBlock, "createBreakthroughAttemptInTx(tx, &attempt)"); got != 2 {
		t.Fatalf("breakthrough attempt helper call count = %d, want 2", got)
	}
	if strings.Contains(executeBlock, "tx.Create(&attempt).Error") {
		t.Fatal("ExecuteBreakthrough still creates attempt without RowsAffected guard")
	}
}

func TestPillAudioTimeGrantChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func applyPillAudioTimeInTx(")
	if start < 0 {
		t.Fatal("applyPillAudioTimeInTx missing")
	}
	end := strings.Index(text[start:], "func calculateCycleSignReward(")
	if end < 0 {
		t.Fatal("applyPillAudioTimeInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := tx.Clauses(clause.OnConflict{",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"PILL_AUDIO_TIME_GRANT_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("pill audio time grant guard missing %q", want)
		}
	}
	if strings.Contains(block, "}).Error") {
		t.Fatal("pill audio time grant still checks only create error")
	}
}

func TestPillUsageLogCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createItemUsageLogInTx(")
	if start < 0 {
		t.Fatal("createItemUsageLogInTx missing")
	}
	end := strings.Index(text[start:], "func calculateCycleSignReward(")
	if end < 0 {
		t.Fatal("createItemUsageLogInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"entry := *logEntry",
		"entry.ItemName = strings.TrimSpace(entry.ItemName)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"ITEM_USAGE_LOG_CREATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("item usage log helper guard missing %q", want)
		}
	}

	waitStart := strings.Index(text, `case "WAITING_CONFIRM_USE_ITEM":`)
	if waitStart < 0 {
		t.Fatal("WAITING_CONFIRM_USE_ITEM missing")
	}
	waitEnd := strings.Index(text[waitStart:], `case "WAITING_SET_RENEW_PRICE":`)
	if waitEnd < 0 {
		t.Fatal("WAITING_CONFIRM_USE_ITEM boundary missing")
	}
	waitBlock := text[waitStart : waitStart+waitEnd]
	if !strings.Contains(waitBlock, "createItemUsageLogInTx(tx, &usageLog)") {
		t.Fatal("pill usage transaction should create usage log through helper")
	}
	if strings.Contains(waitBlock, "tx.Create(&ItemUsageLog{") {
		t.Fatal("pill usage transaction still creates usage log without RowsAffected guard")
	}
}

func TestPillUsageQuotaCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createItemUsageQuotaIfMissingInTx(")
	if start < 0 {
		t.Fatal("createItemUsageQuotaIfMissingInTx missing")
	}
	end := strings.Index(text[start:], "func calculateCycleSignReward(")
	if end < 0 {
		t.Fatal("createItemUsageQuotaIfMissingInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"entry := *quota",
		"entry.ItemName = strings.TrimSpace(entry.ItemName)",
		"entry.PeriodKey = formatPlainValue(entry.PeriodKey)",
		"res := tx.Clauses(clause.OnConflict{",
		"TargetWhere: clause.Where{Exprs: []clause.Expression{",
		`clause.Eq{Column: clause.Column{Name: "deleted_at"}, Value: nil}`,
		"DoNothing: true",
		"res.Error != nil",
		"res.RowsAffected == 0",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("item usage quota helper guard missing %q", want)
		}
	}

	waitStart := strings.Index(text, `case "WAITING_CONFIRM_USE_ITEM":`)
	if waitStart < 0 {
		t.Fatal("WAITING_CONFIRM_USE_ITEM missing")
	}
	waitEnd := strings.Index(text[waitStart:], `case "WAITING_SET_RENEW_PRICE":`)
	if waitEnd < 0 {
		t.Fatal("WAITING_CONFIRM_USE_ITEM boundary missing")
	}
	waitBlock := text[waitStart : waitStart+waitEnd]
	if !strings.Contains(waitBlock, "createItemUsageQuotaIfMissingInTx(tx, &ItemUsageQuota{") {
		t.Fatal("pill usage transaction should initialize quota through helper")
	}
	if strings.Contains(waitBlock, "}).Create(&ItemUsageQuota{") {
		t.Fatal("pill usage transaction still creates quota directly")
	}
}

func TestItemUsageQuotaMigrationCreatesPartialUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("item_usage_quotas(user_id, item_name, period_key)"`)
	if start < 0 {
		t.Fatal("item usage quota migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("monthly_sign_in_streaks(user_id, month_key)"`)
	if end < 0 {
		t.Fatal("item usage quota migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM item_usage_quotas",
		"WHERE deleted_at IS NULL",
		"ensureItemUsageQuotaPartialUniqueIndex(DB)",
		"item usage quota unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("item usage quota migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureItemUsageQuotaPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureItemUsageQuotaPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureInventoryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("item usage quota partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_item_usage_quotas_unique",
		"ON item_usage_quotas(user_id, item_name, period_key)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("item usage quota partial index helper missing %q", want)
		}
	}
}

func TestPersistCultivationAudioTimeLogsRowsAffectedMiss(t *testing.T) {
	source, err := os.ReadFile("cultivation.go")
	if err != nil {
		t.Fatalf("read cultivation.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func persistCultivationAudioTime(")
	if start < 0 {
		t.Fatal("persistCultivationAudioTime missing")
	}
	end := strings.Index(text[start:], "type BreakthroughSetting struct")
	if end < 0 {
		t.Fatal("persistCultivationAudioTime boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := DB.Model(&Cultivation{})",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"更新累计听书时长未命中",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("persist cultivation audio time rows affected guard missing %q", want)
		}
	}
	if strings.Contains(block, `UpdateColumn("total_audio_time", totalAudioTime).Error`) {
		t.Fatal("persistCultivationAudioTime still checks only update error")
	}
}
