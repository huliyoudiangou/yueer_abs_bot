package main

import (
	"os"
	"strings"
	"testing"
)

func TestCultivationDefaultConfigCreatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("cultivation_config.go")
	if err != nil {
		t.Fatalf("read cultivation_config.go err = %v", err)
	}
	text := string(source)

	helpers := []struct {
		name string
		next string
		want []string
	}{
		{
			name: "createDefaultCultivationRealmConfigIfMissingInTx",
			next: "func defaultCultivationRealmConfigs(",
			want: []string{
				"CULTIVATION_REALM_CONFIG_INVALID",
				"entry := *cfg",
				"res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)",
				"res.Error != nil",
				"res.RowsAffected == 0",
				"return nil",
				"*cfg = entry",
			},
		},
		{
			name: "createDefaultCultivationMinorRealmConfigIfMissingInTx",
			next: "func defaultCultivationMinorRealmConfigs(",
			want: []string{
				"CULTIVATION_MINOR_REALM_CONFIG_INVALID",
				"entry := *cfg",
				"res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)",
				"res.Error != nil",
				"res.RowsAffected == 0",
				"return nil",
				"*cfg = entry",
			},
		},
		{
			name: "createDefaultBreakthroughConfigIfMissingInTx",
			next: "func defaultBreakthroughConfigs(",
			want: []string{
				"BREAKTHROUGH_CONFIG_INVALID",
				"entry := *cfg",
				"res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)",
				"res.Error != nil",
				"res.RowsAffected == 0",
				"return nil",
				"*cfg = entry",
			},
		},
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
		for _, want := range helper.want {
			if !strings.Contains(block, want) {
				t.Fatalf("%s guard missing %q", helper.name, want)
			}
		}
		if strings.Contains(block, "}).Error") {
			t.Fatalf("%s still checks only create error", helper.name)
		}
	}

	seedStart := strings.Index(text, "func seedDefaultRealmConfigs(")
	if seedStart < 0 {
		t.Fatal("seedDefaultRealmConfigs missing")
	}
	seedEnd := strings.Index(text[seedStart:], "func defaultBreakthroughConfigs(")
	if seedEnd < 0 {
		t.Fatal("seed default config boundary missing")
	}
	seedBlock := text[seedStart : seedStart+seedEnd]
	for _, want := range []string{
		"createDefaultCultivationRealmConfigIfMissingInTx(tx, &cfg)",
		"createDefaultCultivationMinorRealmConfigIfMissingInTx(tx, &cfg)",
		"createDefaultBreakthroughConfigIfMissingInTx(tx, &cfg)",
	} {
		if !strings.Contains(seedBlock, want) {
			t.Fatalf("default config seed missing helper call %q", want)
		}
	}
	if strings.Contains(seedBlock, "tx.Create(&cfg).Error") {
		t.Fatal("default cultivation config seed still creates config without RowsAffected guard")
	}
}

func TestCultivationMinorRealmConfigMigrationReplacesFullUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("cultivation_minor_realm_configs(major_realm, minor_realm)"`)
	if start < 0 {
		t.Fatal("cultivation minor realm config migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_daily_task_claims(user_id, day_key)"`)
	if end < 0 {
		t.Fatal("cultivation minor realm config migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM cultivation_minor_realm_configs",
		"WHERE deleted_at IS NULL",
		"ensureCultivationMinorRealmConfigPartialUniqueIndex(DB)",
		"cultivation minor realm config unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("cultivation minor realm config migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureCultivationMinorRealmConfigPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureCultivationMinorRealmConfigPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureUsersAbsUserIDPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("cultivation minor realm config partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_cultivation_minor_realm_configs_unique",
		"ON cultivation_minor_realm_configs(major_realm, minor_realm)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("cultivation minor realm config partial index helper missing %q", want)
		}
	}
}

func TestCultivationConfigMigrationsReplaceFullUniqueIndexes(t *testing.T) {
	configSource, err := os.ReadFile("cultivation_config.go")
	if err != nil {
		t.Fatalf("read cultivation_config.go: %v", err)
	}
	configText := string(configSource)
	for _, unsafe := range []string{
		"MajorRealm int    `gorm:\"uniqueIndex;not null\"`",
		"FromMajorRealm int `gorm:\"uniqueIndex;not null\"`",
	} {
		if strings.Contains(configText, unsafe) {
			t.Fatalf("cultivation config model must not use GORM full unique index: %s", unsafe)
		}
	}
	for _, want := range []string{
		"MajorRealm int    `gorm:\"index;not null\"`",
		"FromMajorRealm int `gorm:\"index;not null\"`",
	} {
		if !strings.Contains(configText, want) {
			t.Fatalf("cultivation config model missing normal index tag %q", want)
		}
	}

	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("cultivation_realm_configs(major_realm)"`)
	if start < 0 {
		t.Fatal("cultivation config migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_members(user_id)"`)
	if end < 0 {
		t.Fatal("cultivation config migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM cultivation_realm_configs",
		"FROM cultivation_minor_realm_configs",
		"FROM breakthrough_configs",
		"WHERE deleted_at IS NULL",
		"ensureCultivationMinorRealmConfigPartialUniqueIndex(DB)",
		"ensureCultivationConfigPartialUniqueIndexes(DB)",
		"seedDefaultCultivationConfigs()",
		"cultivation config seed after index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("cultivation config migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureCultivationConfigPartialUniqueIndexes(")
	if helperStart < 0 {
		t.Fatal("ensureCultivationConfigPartialUniqueIndexes missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureUsersAbsUserIDPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("cultivation config partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_cultivation_realm_configs_major_realm",
		"ON cultivation_realm_configs(major_realm)",
		"idx_breakthrough_configs_from_major_realm",
		"ON breakthrough_configs(from_major_realm)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("cultivation config partial index helper missing %q", want)
		}
	}
}

func TestCultivationConfigRefreshLogFormatsSource(t *testing.T) {
	source, err := os.ReadFile("cultivation_config.go")
	if err != nil {
		t.Fatalf("read cultivation_config.go err = %v", err)
	}
	text := string(source)

	start := strings.Index(text, "func ReloadCultivationRules() error {")
	if start < 0 {
		t.Fatal("ReloadCultivationRules missing")
	}
	end := strings.Index(text[start:], "func loadCultivationRulesFromDB()")
	if end < 0 {
		t.Fatal("ReloadCultivationRules boundary missing")
	}
	block := text[start : start+end]

	if !strings.Contains(block, "formatPlainValue(rules.Source)") {
		t.Fatal("cultivation config refresh log must format dynamic source")
	}
	if strings.Contains(block, "\n\t\trules.Source,\n") {
		t.Fatal("cultivation config refresh log still outputs raw source")
	}
}
