package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMessageSweeperLogsQueueAndDeleteErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func startMessageSweeper(")
	if start < 0 {
		t.Fatal("startMessageSweeper missing")
	}
	end := strings.Index(text[start:], "// 2. 内存清道夫任务")
	if end < 0 {
		t.Fatal("startMessageSweeper queue section boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"自动删消息队列读取失败",
		"自动删消息记录清理失败",
		"自动删除 Telegram 消息失败",
		"formatPlainError(err)",
		"formatPlainError(deleteErr)",
		"formatTelegramSendError(err)",
		"res := DB.Delete(&m)",
		"res.RowsAffected == 0",
		"AUTO_DELETE_MSG_DELETE_MISSED",
		"自动删消息记录清理未命中",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("message sweeper diagnostics missing %q", want)
		}
	}
	if strings.Contains(block, "Find(&msgs).Error == nil") {
		t.Fatal("message sweeper still silently ignores queue read errors")
	}
	if strings.Contains(block, "DB.Delete(&m).Error") {
		t.Fatal("message sweeper still checks only delete error")
	}
	if strings.Contains(block, "\u923f") || strings.Contains(block, "\u9477\ue041\u934a\u59e9\u9352\u72b3") {
		t.Fatal("message sweeper diagnostics contain mojibake")
	}
}

func TestAuditRoleReadErrorsAreNotSilentlyDefaulted(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	roleStart := strings.Index(text, "func getUserRoleFromDBChecked(")
	if roleStart < 0 {
		t.Fatal("getUserRoleFromDBChecked missing")
	}
	roleEnd := strings.Index(text[roleStart:], "func getUserRoleFromDB(")
	if roleEnd < 0 {
		t.Fatal("getUserRoleFromDBChecked boundary missing")
	}
	roleBlock := text[roleStart : roleStart+roleEnd]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"return \"user\", err",
		"return \"super_admin\", nil",
	} {
		if !strings.Contains(roleBlock, want) {
			t.Fatalf("checked role reader missing %q", want)
		}
	}
	if strings.Contains(roleBlock, ".First(&u).Error == nil") {
		t.Fatal("checked role reader still collapses read errors into default role")
	}

	compatStart := strings.Index(text, "func getUserRoleFromDB(db *gorm.DB, userID int64) string")
	if compatStart < 0 {
		t.Fatal("getUserRoleFromDB compatibility helper missing")
	}
	compatEnd := strings.Index(text[compatStart:], "func getUserRole(userID int64) string")
	if compatEnd < 0 {
		t.Fatal("getUserRoleFromDB compatibility helper boundary missing")
	}
	compatBlock := text[compatStart : compatStart+compatEnd]
	for _, want := range []string{
		"getUserRoleFromDBChecked(db, userID)",
		"formatPlainError(err)",
		"按普通用户处理",
	} {
		if !strings.Contains(compatBlock, want) {
			t.Fatalf("compat role reader missing %q", want)
		}
	}

	auditRoleStart := strings.Index(text, "func getAuditActorRoleInTx(")
	if auditRoleStart < 0 {
		t.Fatal("getAuditActorRoleInTx missing")
	}
	auditRoleEnd := strings.Index(text[auditRoleStart:], "func isSuperAdmin(")
	if auditRoleEnd < 0 {
		t.Fatal("getAuditActorRoleInTx boundary missing")
	}
	auditRoleBlock := text[auditRoleStart : auditRoleStart+auditRoleEnd]
	if !strings.Contains(auditRoleBlock, "(string, error)") ||
		!strings.Contains(auditRoleBlock, "return getUserRoleFromDBChecked(tx, actorID)") {
		t.Fatal("audit actor role must propagate checked role read errors")
	}

	auditStart := strings.Index(text, "func writeAuditLogInTx(")
	if auditStart < 0 {
		t.Fatal("writeAuditLogInTx missing")
	}
	auditEnd := strings.Index(text[auditStart:], "const (")
	if auditEnd < 0 {
		t.Fatal("writeAuditLogInTx boundary missing")
	}
	auditBlock := text[auditStart : auditStart+auditEnd]
	for _, want := range []string{
		"role, err := getAuditActorRoleInTx(tx, actorID)",
		"if err != nil",
		"return err",
	} {
		if !strings.Contains(auditBlock, want) {
			t.Fatalf("writeAuditLogInTx must stop on role read errors, missing %q", want)
		}
	}
}

func TestSQLiteIndexDefinitionComparisonRequiresExactPredicate(t *testing.T) {
	desired := `
		CREATE UNIQUE INDEX IF NOT EXISTS idx_sect_secret_realm_events_active_sect_unique
		ON sect_secret_realm_events(sect_id)
		WHERE status = 'active' AND deleted_at IS NULL;
	`
	existing := `
		CREATE UNIQUE INDEX idx_sect_secret_realm_events_active_sect_unique
		ON sect_secret_realm_events(sect_id)
		WHERE status = 'active' AND deleted_at IS NULL
	`
	if !sqliteIndexDefinitionsEqual(existing, desired) {
		t.Fatal("equivalent index definitions should match despite IF NOT EXISTS or whitespace")
	}

	for _, tc := range []struct {
		name     string
		existing string
		desired  string
	}{
		{
			name: "missing active predicate",
			existing: `
				CREATE UNIQUE INDEX idx_sect_secret_realm_events_active_sect_unique
				ON sect_secret_realm_events(sect_id)
				WHERE deleted_at IS NULL
			`,
			desired: desired,
		},
		{
			name: "missing code hash nonempty predicate",
			existing: `
				CREATE UNIQUE INDEX idx_invite_codes_code_hash_unique
				ON invite_codes(code_hash)
				WHERE deleted_at IS NULL
			`,
			desired: `
				CREATE UNIQUE INDEX IF NOT EXISTS idx_invite_codes_code_hash_unique
				ON invite_codes(code_hash)
				WHERE code_hash <> '' AND deleted_at IS NULL;
			`,
		},
		{
			name: "wrong indexed columns",
			existing: `
				CREATE UNIQUE INDEX idx_lottery_participants_activity_user_unique
				ON lottery_participants(activity_id)
				WHERE deleted_at IS NULL
			`,
			desired: `
				CREATE UNIQUE INDEX IF NOT EXISTS idx_lottery_participants_activity_user_unique
				ON lottery_participants(activity_id, user_id)
				WHERE deleted_at IS NULL;
			`,
		},
	} {
		if sqliteIndexDefinitionsEqual(tc.existing, tc.desired) {
			t.Fatalf("%s should not match desired index definition", tc.name)
		}
	}
}

func TestSystemConfigKeyMigrationReplacesFullUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "Key   string `gorm:\"index;not null\"`") {
		t.Fatal("SystemConfig.Key should use a plain model index; startup migration owns partial uniqueness")
	}
	if strings.Contains(text, "Key   string `gorm:\"uniqueIndex;not null\"`") {
		t.Fatal("SystemConfig.Key still declares a full unique index")
	}

	start := strings.Index(text, `assertNoDuplicateGroups("system_configs(key)"`)
	if start < 0 {
		t.Fatal("system config key migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_shop_renew_claims(sect_id, month_key, slot_no)"`)
	if end < 0 {
		t.Fatal("system config key migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM system_configs",
		"WHERE deleted_at IS NULL",
		"ensureSystemConfigKeyPartialUniqueIndex(DB)",
		"system config key unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("system config key migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSystemConfigKeyPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSystemConfigKeyPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureGardenSeedPurchasePartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("system config key partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_system_configs_key",
		"ON system_configs(key)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("system config key partial index helper missing %q", want)
		}
	}
}

func TestSensitiveDataMigrationsCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go err = %v", err)
	}
	text := string(source)

	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		model     string
		unsafe    string
	}{
		{
			name:      "user security code",
			startFunc: "func migrateUserSecurityCodesInBatches() error",
			endFunc:   "func migrateInviteCodesInBatches() error",
			model:     "res := tx.Model(&User{})",
			unsafe:    `Update("security_code", hashed).Error`,
		},
		{
			name:      "invite code",
			startFunc: "func migrateInviteCodesInBatches() error",
			endFunc:   "func migrateRenewCodesInBatches() error",
			model:     "res := tx.Model(&InviteCode{})",
			unsafe:    "Updates(updates).Error",
		},
		{
			name:      "renew code",
			startFunc: "func migrateRenewCodesInBatches() error",
			endFunc:   "",
			model:     "res := tx.Model(&RenewCode{})",
			unsafe:    "Updates(updates).Error",
		},
	}

	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s migration missing", tt.name)
		}
		end := len(text) - start
		if tt.endFunc != "" {
			end = strings.Index(text[start:], tt.endFunc)
			if end < 0 {
				t.Fatalf("%s migration boundary missing", tt.name)
			}
		}
		block := text[start : start+end]
		for _, want := range []string{
			tt.model,
			"res.Error != nil",
			"res.RowsAffected == 0",
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s migration missing rows guard %q", tt.name, want)
			}
		}
		if strings.Contains(block, tt.unsafe) {
			t.Fatalf("%s migration still checks only update error", tt.name)
		}
	}
}

func TestSensitiveCodeHashMigrationsUsePartialUniqueIndexes(t *testing.T) {
	source, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func runSensitiveDataMigrations()")
	if start < 0 {
		t.Fatal("runSensitiveDataMigrations missing")
	}
	end := strings.Index(text[start:], "func runSecurityAttemptLockMigration()")
	if end < 0 {
		t.Fatal("runSensitiveDataMigrations boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM invite_codes",
		"FROM renew_codes",
		"WHERE code_hash <> '' AND deleted_at IS NULL",
		"ensureSensitiveCodeHashPartialUniqueIndexes(DB)",
		"sensitive code hash unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sensitive code hash migration block missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"WHERE code_hash <> ''\n\t\tGROUP BY code_hash",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_invite_codes_code_hash_unique\n\t\tON invite_codes(code_hash)\n\t\tWHERE code_hash <> '';",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_renew_codes_code_hash_unique\n\t\tON renew_codes(code_hash)\n\t\tWHERE code_hash <> '';",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("sensitive code hash migration still uses non-partial unique index: %q", unsafe)
		}
	}

	helperStart := strings.Index(text, "func ensureSensitiveCodeHashPartialUniqueIndexes(")
	if helperStart < 0 {
		t.Fatal("ensureSensitiveCodeHashPartialUniqueIndexes missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureSecurityAttemptLockPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sensitive code hash partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_invite_codes_code_hash_unique",
		"ON invite_codes(code_hash)",
		"idx_renew_codes_code_hash_unique",
		"ON renew_codes(code_hash)",
		"WHERE code_hash <> '' AND deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sensitive code hash partial index helper missing %q", want)
		}
	}
}

func TestStateMachineAuditDetailsUsePlainValueForDynamicStrings(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	for _, want := range []string{
		"formatPlainValue(username), userID, formatPlainValue(reservedInvite.CodePreview), formatPlainValue(id)",
		"formatPlainValue(u.Username), userID, formatPlainValue(rCode.CodePreview)",
		"userID, formatPlainValue(result.AbsUserID), result.NewExpireAt.Format(time.RFC3339), result.Days, formatPlainError(err)",
		"formatPlainValue(deleted.Username), userID, formatPlainValue(deleted.AbsUserID)",
		"formatPlainValue(targetUser.Username), formatPlainValue(targetUser.AbsUserID)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("state machine audit detail missing sanitized dynamic field pattern %q", want)
		}
	}

	for _, unsafe := range []string{
		"username, userID, reservedInvite.CodePreview, id",
		"u.Username, userID, rCode.CodePreview",
		"userID, result.AbsUserID, result.NewExpireAt.Format(time.RFC3339)",
		"deleted.Username, userID, deleted.AbsUserID",
		"targetUser.Username, targetUser.AbsUserID",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("state machine audit detail still contains raw dynamic fields: %q", unsafe)
		}
	}
}

func TestAuditLogCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func writeAuditLogInTx(")
	if start < 0 {
		t.Fatal("writeAuditLogInTx missing")
	}
	end := strings.Index(text[start:], "func formatAuditTextForStorage(")
	if end < 0 {
		t.Fatal("writeAuditLogInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := tx.Create(&AuditLog{",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"AUDIT_LOG_CREATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("audit log create guard missing %q", want)
		}
	}
	if strings.Contains(block, "}).Error") {
		t.Fatal("writeAuditLogInTx still checks only create error")
	}
}

func TestHighRiskAuditActionsCoverAssetIssuance(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "var highRiskAuditActionSet = map[string]struct{}{")
	if start < 0 {
		t.Fatal("highRiskAuditActionSet missing")
	}
	end := strings.Index(text[start:], "func highRiskAuditActions()")
	if end < 0 {
		t.Fatal("highRiskAuditActionSet boundary missing")
	}
	block := text[start : start+end]
	for _, action := range []string{
		"CLAIM_GITHUB_BENEFIT_INVITE",
		"CLAIM_GITHUB_BENEFIT_RENEW",
		"CREATE_SECT_LOTTERY",
		"DRAW_SECT_LOTTERY",
		"CANCEL_SECT_LOTTERY",
		"REFERRAL_TRIAL_REGISTER",
		"TRIAL_CONVERT_FORMAL",
		"REFERRAL_TRIAL_TASK_CLAIM",
	} {
		if !strings.Contains(block, `"`+action+`":`) {
			t.Fatalf("asset issuance audit action missing from high-risk set: %s", action)
		}
		if !isHighRiskAuditAction(action) {
			t.Fatalf("asset issuance audit action not classified high risk: %s", action)
		}
		if !isHighRiskAuditAction(action + "_FAILED") {
			t.Fatalf("failed asset issuance audit action not classified high risk: %s", action)
		}
		if !isHighRiskAuditAction(action + "_LOCAL_FAILED") {
			t.Fatalf("local failed asset issuance audit action not classified high risk: %s", action)
		}
	}
}

func TestMigrationAppliedReadErrorsStopStartup(t *testing.T) {
	source, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func migrationAlreadyApplied(")
	if start < 0 {
		t.Fatal("migrationAlreadyApplied missing")
	}
	end := strings.Index(text[start:], "func markMigrationAppliedIfMissing(")
	if end < 0 {
		t.Fatal("migrationAlreadyApplied boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"log.Fatalf",
		"schema migration version lookup failed; startup blocked",
		"formatPlainError(err)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("migrationAlreadyApplied must stop on read errors, missing %q", want)
		}
	}
	if strings.Contains(block, ".First(&m).Error == nil") {
		t.Fatal("migrationAlreadyApplied still treats DB read errors as migration not applied")
	}

	markStart := strings.Index(text, "func markMigrationAppliedIfMissing(")
	if markStart < 0 {
		t.Fatal("markMigrationAppliedIfMissing missing")
	}
	markEnd := strings.Index(text[markStart:], "func runOneTimeMigration(")
	if markEnd < 0 {
		t.Fatal("markMigrationAppliedIfMissing boundary missing")
	}
	markBlock := text[markStart : markStart+markEnd]
	for _, want := range []string{
		"res := DB.Create(&SchemaMigration{",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SCHEMA_MIGRATION_CREATE_MISSED",
		"schema migration version record failed; startup blocked",
		"schema migration version record missed; startup blocked",
		"formatPlainError(err)",
	} {
		if !strings.Contains(markBlock, want) {
			t.Fatalf("migration mark create guard missing %q", want)
		}
	}
	if strings.Contains(markBlock, "}).Error") {
		t.Fatal("markMigrationAppliedIfMissing still checks only create error")
	}
}

func TestDBStartupMigrationLogsUseFormattedErrors(t *testing.T) {
	source, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"schema migration failed; startup blocked",
		"data consistency precheck failed; startup blocked",
		"critical database migration failed; startup blocked",
		"sect technology unique index migration failed; startup blocked",
		"sect secret realm active unique index migration failed; startup blocked",
		"marketplace open dispute unique index migration failed; startup blocked",
		"marketplace active secret hash unique index migration failed; startup blocked",
		"SECURITY_PEPPER is not configured; startup blocked",
		"user security code migration failed",
		"invite code migration failed",
		"renew code migration failed",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("db startup/migration diagnostics missing readable text %q", want)
		}
	}
	for lineNo, line := range strings.Split(text, "\n") {
		if !strings.Contains(line, "log.") {
			continue
		}
		if strings.Contains(line, "%v") && strings.Contains(line, "err") {
			t.Fatalf("db startup/migration log line %d must use formatPlainError instead of raw %%v: %s", lineNo+1, line)
		}
	}
}

func TestStateMachineDiagnosticsUseSanitizedErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainError(err)",
		"formatPlainError(res.Error)",
		"formatPlainError(walletErr)",
		"formatTelegramSendError(err)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("state machine diagnostics missing %q", want)
		}
	}
	rawErrFormat := string([]byte{'e', 'r', 'r', '=', '%', 'v'})
	if strings.Contains(text, rawErrFormat) {
		t.Fatal("state machine diagnostics should not log raw error values")
	}
}

func TestBusinessErrorLogsDoNotUseRawPercentV(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob go files: %v", err)
	}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		source, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s err = %v", file, err)
		}
		for lineNo, line := range strings.Split(string(source), "\n") {
			if !strings.Contains(line, "log.") || !strings.Contains(line, "%v") {
				continue
			}
			if strings.Contains(line, "err") || strings.Contains(line, "Error") {
				t.Fatalf("%s:%d must use formatPlainError or formatTelegramSendError instead of raw %%v: %s", file, lineNo+1, line)
			}
		}
	}
}
