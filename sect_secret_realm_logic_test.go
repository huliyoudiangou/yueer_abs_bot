package main

import (
	"math"
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestSectSecretRealmPointDescriptionNameSanitizesText(t *testing.T) {
	got := sectSecretRealmPointDescriptionName("  realm\nalpha\tbeta  ")
	if got != "realm alpha beta" {
		t.Fatalf("sectSecretRealmPointDescriptionName() = %q", got)
	}
	if got := sectSecretRealmPointDescriptionName("\n\t"); got != "-" {
		t.Fatalf("empty sect secret realm description name fallback = %q", got)
	}

	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	if strings.Contains(text, "event.Name, p.DeltaHours") {
		t.Fatal("sect secret realm point reward description should not persist raw event name")
	}
	if !strings.Contains(text, "sectSecretRealmPointDescriptionName(event.Name), p.DeltaHours") {
		t.Fatal("sect secret realm point reward description should sanitize event name")
	}
	if strings.Contains(text, `fmt.Sprintf("开启宗门秘境【%s】，消耗宗门资金 %d", profile.Name, cost)`) {
		t.Fatal("sect secret realm open contribution log should not persist raw profile name")
	}
	if !strings.Contains(text, `fmt.Sprintf("开启宗门秘境【%s】，消耗宗门资金 %d", sectSecretRealmPointDescriptionName(profile.Name), cost)`) {
		t.Fatal("sect secret realm open contribution log should sanitize profile name")
	}
}

func TestCanJoinSectSecretRealmAtRequiresActiveWindow(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	start := time.Date(2026, 6, 13, 20, 0, 0, 0, loc)
	event := SectSecretRealmEvent{
		RealmID: "SR-20260613-001",
		Status:  "active",
		StartAt: start,
		EndAt:   start.Add(2 * time.Hour),
	}

	tests := []struct {
		name  string
		event SectSecretRealmEvent
		now   time.Time
		want  bool
	}{
		{name: "active at start", event: event, now: start, want: true},
		{name: "active before end", event: event, now: event.EndAt.Add(-time.Second), want: true},
		{name: "before start", event: event, now: start.Add(-time.Second), want: false},
		{name: "at end", event: event, now: event.EndAt, want: false},
		{name: "settled", event: SectSecretRealmEvent{RealmID: event.RealmID, Status: "settled", StartAt: event.StartAt, EndAt: event.EndAt}, now: start.Add(time.Hour), want: false},
		{name: "missing realm id", event: SectSecretRealmEvent{Status: "active", StartAt: event.StartAt, EndAt: event.EndAt}, now: start.Add(time.Hour), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canJoinSectSecretRealmAt(tt.event, tt.now); got != tt.want {
				t.Fatalf("canJoinSectSecretRealmAt(%s, %s) = %v, want %v", tt.event.Status, tt.now.Format(time.RFC3339), got, tt.want)
			}
		})
	}
}

func TestSectSecretRealmActiveQueriesDistinguishReadErrors(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"func getActiveSectSecretRealmTxChecked(",
		"return SectSecretRealmEvent{}, errSectSecretRealmNotActive",
		"return SectSecretRealmEvent{}, err",
		"func getActiveOrLatestSectSecretRealmChecked(",
		"getActiveSectSecretRealmTxChecked(DB, sectID, now)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect secret realm active checked helper missing %q", want)
		}
	}

	blocks := map[string]string{
		"status": "func handleSectSecretRealmStatus(",
		"open":   "func handleOpenSectSecretRealm(",
		"join":   "func handleJoinSectSecretRealm(",
		"rank":   "func handleSectSecretRealmRank(",
		"detail": "func handleSectSecretRealmDetail(",
	}
	extract := func(name string, startMarker string) string {
		start := strings.Index(text, startMarker)
		if start < 0 {
			t.Fatalf("%s block missing", name)
		}
		next := strings.Index(text[start+len(startMarker):], "\nfunc ")
		if next < 0 {
			t.Fatalf("%s block boundary missing", name)
		}
		return text[start : start+len(startMarker)+next]
	}

	statusBlock := extract("status", blocks["status"])
	for _, want := range []string{
		"getActiveOrLatestSectSecretRealmChecked(member.SectID, now)",
		"宗门秘境状态读取失败",
		"formatPlainError(realmErr)",
	} {
		if !strings.Contains(statusBlock, want) {
			t.Fatalf("sect secret realm status active read guard missing %q", want)
		}
	}

	openBlock := extract("open", blocks["open"])
	for _, want := range []string{
		"loadSectMemberByUserInTx(tx, userID, &txMember, false)",
		"touchRes := tx.Model(&Sect{})",
		"touchRes.RowsAffected == 0",
		"SECT_SECRET_REALM_SECT_TOUCH_MISSED",
		"getActiveSectSecretRealmTxChecked(tx, txMember.SectID, now)",
		"return activeErr",
		"宗门秘境开启后活动重读失败",
	} {
		if !strings.Contains(openBlock, want) {
			t.Fatalf("sect secret realm open active read guard missing %q", want)
		}
	}
	if strings.Contains(openBlock, "getActiveSectSecretRealmTx(tx, txMember.SectID, now)") {
		t.Fatal("sect secret realm open still treats active read errors as inactive")
	}
	if strings.Contains(openBlock, "return errNotInSect") {
		t.Fatal("sect secret realm open transaction still maps all member read errors to not-in-sect")
	}

	joinBlock := extract("join", blocks["join"])
	for _, want := range []string{
		"getActiveSectSecretRealmTxChecked(DB, member.SectID, time.Now())",
		"宗门秘境进入活动读取失败",
		"getActiveSectSecretRealmTxChecked(tx, member.SectID, time.Now())",
		"return activeErr",
	} {
		if !strings.Contains(joinBlock, want) {
			t.Fatalf("sect secret realm join active read guard missing %q", want)
		}
	}
	if strings.Contains(joinBlock, "getActiveSectSecretRealmTx(DB, member.SectID, time.Now())") ||
		strings.Contains(joinBlock, "getActiveSectSecretRealmTx(tx, member.SectID, time.Now())") {
		t.Fatal("sect secret realm join still treats active read errors as inactive")
	}

	for name, wantLog := range map[string]string{
		"rank":   "宗门秘境排行活动读取失败",
		"detail": "宗门秘境明细活动读取失败",
	} {
		block := extract(name, blocks[name])
		if !strings.Contains(block, "getActiveOrLatestSectSecretRealmChecked(member.SectID, time.Now())") ||
			!strings.Contains(block, wantLog) ||
			!strings.Contains(block, "formatPlainError(realmErr)") {
			t.Fatalf("sect secret realm %s active/latest read guard missing", name)
		}
		if strings.Contains(block, "event, ok := getActiveOrLatestSectSecretRealm(member.SectID, time.Now())") {
			t.Fatalf("sect secret realm %s still treats active/latest read errors as missing records", name)
		}
	}
}

func TestSectSecretRealmEventCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createSectSecretRealmEventInTx(")
	if start < 0 {
		t.Fatal("createSectSecretRealmEventInTx missing")
	}
	end := strings.Index(text[start:], "func sectSecretRealmPointDescriptionName(")
	if end < 0 {
		t.Fatal("createSectSecretRealmEventInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SECT_SECRET_REALM_EVENT_CREATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm event create guard missing %q", want)
		}
	}

	openStart := strings.Index(text, "func handleOpenSectSecretRealm(")
	if openStart < 0 {
		t.Fatal("handleOpenSectSecretRealm missing")
	}
	openEnd := strings.Index(text[openStart:], "func handleJoinSectSecretRealm(")
	if openEnd < 0 {
		t.Fatal("handleOpenSectSecretRealm boundary missing")
	}
	openBlock := text[openStart : openStart+openEnd]
	if !strings.Contains(openBlock, "createSectSecretRealmEventInTx(tx, &event)") {
		t.Fatal("open secret realm should create event through RowsAffected helper")
	}
	if strings.Contains(openBlock, "tx.Create(&event).Error") {
		t.Fatal("open secret realm still creates event without RowsAffected guard")
	}
}

func TestSectSecretRealmOpenReturnValuesOnlyAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleOpenSectSecretRealm(")
	if start < 0 {
		t.Fatal("handleOpenSectSecretRealm missing")
	}
	end := strings.Index(text[start:], "func handleJoinSectSecretRealm(")
	if end < 0 {
		t.Fatal("handleOpenSectSecretRealm boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"txWeeklyUsedAfter := weeklyUsed + 1",
		"txRealmID := fmt.Sprintf(",
		"txSectName := txSect.Name",
		"RefID:        txRealmID",
		"realmID = txRealmID",
		"sectName = txSectName",
		"weeklyUsedAfter = txWeeklyUsedAfter",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("secret realm open return guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"weeklyUsedAfter = weeklyUsed + 1",
		"realmID = fmt.Sprintf(",
		"sectName = txSect.Name",
		"RefID:        realmID",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("secret realm open still exposes transactional intermediate value: %s", unsafe)
		}
	}
}

func TestSectSecretRealmParticipantCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createSectSecretRealmParticipantIfMissingInTx(")
	if start < 0 {
		t.Fatal("createSectSecretRealmParticipantIfMissingInTx missing")
	}
	end := strings.Index(text[start:], "const (")
	if end < 0 {
		t.Fatal("createSectSecretRealmParticipantIfMissingInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"SECT_SECRET_REALM_PARTICIPANT_INVALID",
		"entry := *participant",
		"entry.RealmID = formatPlainValue(entry.RealmID)",
		"entry.UserName = formatPlainValue(entry.UserName)",
		"res := tx.Clauses(sectSecretRealmParticipantOnConflict()).Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"return nil",
		"*participant = entry",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm participant helper guard missing %q", want)
		}
	}
	if strings.Contains(block, "}).Error") {
		t.Fatal("sect secret realm participant helper still checks only create error")
	}

	joinStart := strings.Index(text, "func handleJoinSectSecretRealm(")
	if joinStart < 0 {
		t.Fatal("handleJoinSectSecretRealm missing")
	}
	joinEnd := strings.Index(text[joinStart:], "func sectSecretRealmParticipantOnConflict(")
	if joinEnd < 0 {
		t.Fatal("handleJoinSectSecretRealm boundary missing")
	}
	joinBlock := text[joinStart : joinStart+joinEnd]
	if !strings.Contains(joinBlock, "createSectSecretRealmParticipantIfMissingInTx(tx, &participant)") {
		t.Fatal("join secret realm should create participant through helper")
	}
	if strings.Contains(joinBlock, "Create(&SectSecretRealmParticipant{") {
		t.Fatal("join secret realm still creates participant without RowsAffected guard")
	}
}

func TestSectSecretRealmParticipantMigrationReplacesFullUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("sect_secret_realm_participants(realm_id, user_id)"`)
	if start < 0 {
		t.Fatal("sect secret realm participant migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_horn_deliveries(horn_id, user_id)"`)
	if end < 0 {
		t.Fatal("sect secret realm participant migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM sect_secret_realm_participants",
		"WHERE deleted_at IS NULL",
		"ensureSectSecretRealmParticipantPartialUniqueIndex(DB)",
		"sect secret realm participant unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm participant migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSectSecretRealmParticipantPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSectSecretRealmParticipantPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureSectHornDeliveryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sect secret realm participant partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sect_secret_realm_participants_realm_user_unique",
		"ON sect_secret_realm_participants(realm_id, user_id)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect secret realm participant partial index helper missing %q", want)
		}
	}
}

func TestSectSecretRealmEventIDMigrationReplacesFullUniqueIndex(t *testing.T) {
	modelData, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go: %v", err)
	}
	modelText := string(modelData)
	if !strings.Contains(modelText, "RealmID string `gorm:\"index;not null\"`") {
		t.Fatal("SectSecretRealmEvent.RealmID should use a plain model index; startup migration owns partial uniqueness")
	}
	if strings.Contains(modelText, "RealmID string `gorm:\"uniqueIndex;not null\"`") {
		t.Fatal("SectSecretRealmEvent.RealmID still declares a full unique index")
	}

	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("sect_secret_realm_events(realm_id)"`)
	if start < 0 {
		t.Fatal("sect secret realm event id migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_secret_realm_events(active sect_id)"`)
	if end < 0 {
		t.Fatal("sect secret realm event id migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM sect_secret_realm_events",
		"WHERE deleted_at IS NULL",
		"ensureSectSecretRealmEventIDPartialUniqueIndex(DB)",
		"sect secret realm event id unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm event id migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSectSecretRealmEventIDPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSectSecretRealmEventIDPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureSectSecretRealmActiveUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sect secret realm event id partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sect_secret_realm_events_realm_id",
		"ON sect_secret_realm_events(realm_id)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect secret realm event id partial index helper missing %q", want)
		}
	}
}

func TestSectSecretRealmActiveMigrationReplacesFullUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("sect_secret_realm_events(active sect_id)"`)
	if start < 0 {
		t.Fatal("sect secret realm active migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_secret_realm_participants(realm_id, user_id)"`)
	if end < 0 {
		t.Fatal("sect secret realm active migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM sect_secret_realm_events",
		"WHERE status = 'active' AND deleted_at IS NULL",
		"ensureSectSecretRealmActiveUniqueIndex(DB)",
		"sect secret realm active unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm active migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSectSecretRealmActiveUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSectSecretRealmActiveUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureSectListeningDailyProgressPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sect secret realm active unique index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sect_secret_realm_events_active_sect_unique",
		"ON sect_secret_realm_events(sect_id)",
		"WHERE status = 'active' AND deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect secret realm active unique index helper missing %q", want)
		}
	}
}

func TestSectSecretRealmConfigReadsFailClosed(t *testing.T) {
	realmSource, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	configSource, err := os.ReadFile("sect_secret_realm_config.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm_config.go err = %v", err)
	}
	realmText := string(realmSource)
	configText := string(configSource)

	loaderStart := strings.Index(configText, "func loadSectSecretRealmConfigChecked(")
	if loaderStart < 0 {
		t.Fatal("loadSectSecretRealmConfigChecked missing")
	}
	loaderEnd := strings.Index(configText[loaderStart:], "func (cfg *SectSecretRealmConfig) ensureDefaults(")
	if loaderEnd < 0 {
		t.Fatal("loadSectSecretRealmConfigChecked boundary missing")
	}
	loaderBlock := configText[loaderStart : loaderStart+loaderEnd]
	for _, want := range []string{
		"getSystemConfigStringChecked(sectSecretRealmConfigKey)",
		"解析宗门秘境配置失败",
		"return cfg, err",
	} {
		if !strings.Contains(loaderBlock, want) {
			t.Fatalf("sect secret realm checked config loader missing %q", want)
		}
	}

	openStart := strings.Index(realmText, "func handleOpenSectSecretRealm(")
	if openStart < 0 {
		t.Fatal("handleOpenSectSecretRealm missing")
	}
	openEnd := strings.Index(realmText[openStart:], "func handleJoinSectSecretRealm(")
	if openEnd < 0 {
		t.Fatal("handleOpenSectSecretRealm boundary missing")
	}
	openBlock := realmText[openStart : openStart+openEnd]
	for _, want := range []string{
		"loadSectSecretRealmConfigChecked()",
		"宗门秘境开启配置读取失败",
		"宗门秘境配置暂时读取失败，请稍后重试。",
		"return",
	} {
		if !strings.Contains(openBlock, want) {
			t.Fatalf("open secret realm config fail-closed guard missing %q", want)
		}
	}
	if strings.Contains(openBlock, "cfg := loadSectSecretRealmConfig()") {
		t.Fatal("open secret realm still uses default config on read failure")
	}

	adminStart := strings.Index(configText, "func HandleSectSecretRealmAdminCommand(")
	if adminStart < 0 {
		t.Fatal("HandleSectSecretRealmAdminCommand missing")
	}
	adminEnd := strings.Index(configText[adminStart:], "func handleSectSecretRealmAdminSession(")
	if adminEnd < 0 {
		t.Fatal("HandleSectSecretRealmAdminCommand boundary missing")
	}
	adminBlock := configText[adminStart : adminStart+adminEnd]
	for _, want := range []string{
		"loadSectSecretRealmConfigChecked()",
		"查看宗门秘境配置读取失败",
		"宗门秘境配置暂时读取失败，请稍后重试。",
	} {
		if !strings.Contains(adminBlock, want) {
			t.Fatalf("admin config view fail-closed guard missing %q", want)
		}
	}

	execStart := strings.Index(configText, "func executeSectSecretRealmAdminWriteCommand(")
	if execStart < 0 {
		t.Fatal("executeSectSecretRealmAdminWriteCommand missing")
	}
	execEnd := strings.Index(configText[execStart:], "func sectSecretRealmConfigProfileByKey(")
	if execEnd < 0 {
		t.Fatal("executeSectSecretRealmAdminWriteCommand boundary missing")
	}
	execBlock := configText[execStart : execStart+execEnd]
	for _, want := range []string{
		"loadSectSecretRealmConfigChecked()",
		"宗门秘境配置读取失败，请稍后重试",
	} {
		if !strings.Contains(execBlock, want) {
			t.Fatalf("admin config write fail-closed guard missing %q", want)
		}
	}
	if strings.Contains(execBlock, "cfg := loadSectSecretRealmConfig()") {
		t.Fatal("admin config write still uses default config on read failure")
	}
}

func TestSectSecretRealmConfigAuditDetailUsesPlainValue(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm_config.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm_config.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func saveSectSecretRealmConfig(")
	if start < 0 {
		t.Fatal("saveSectSecretRealmConfig missing")
	}
	end := strings.Index(text[start:], "func formatSectSecretRealmConfig(")
	if end < 0 {
		t.Fatal("saveSectSecretRealmConfig boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`writeAuditLogInTx(tx, actorID, "UPDATE_SECT_SECRET_REALM_CONFIG"`,
		`fmt.Sprintf("%s，原因：%s", detail, formatPlainValue(reason))`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm config audit detail guard missing %q", want)
		}
	}
	if strings.Contains(block, `fmt.Sprintf("%s，原因：%s", detail, reason)`) {
		t.Fatal("sect secret realm config audit detail still uses raw reason")
	}
}

func TestSectSecretRealmConfigWriteDetailsUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm_config.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm_config.go err = %v", err)
	}
	text := string(source)

	execStart := strings.Index(text, "func executeSectSecretRealmAdminWriteCommand(")
	if execStart < 0 {
		t.Fatal("executeSectSecretRealmAdminWriteCommand missing")
	}
	execEnd := strings.Index(text[execStart:], "func sectSecretRealmConfigProfileByKey(")
	if execEnd < 0 {
		t.Fatal("executeSectSecretRealmAdminWriteCommand boundary missing")
	}
	execBlock := text[execStart : execStart+execEnd]
	for _, want := range []string{
		"formatPlainValue(profile.Key), minMajor, pointPercent",
		"formatPlainValue(profile.Key), minMajor, formatPlainValue(itemName)",
	} {
		if !strings.Contains(execBlock, want) {
			t.Fatalf("sect secret realm config write detail guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"profile.Key, minMajor, pointPercent",
		"profile.Key, minMajor, itemName",
	} {
		if strings.Contains(execBlock, unsafe) {
			t.Fatalf("sect secret realm config write detail uses raw value: %s", unsafe)
		}
	}

	fieldStart := strings.Index(text, "func updateSectSecretRealmProfileField(")
	if fieldStart < 0 {
		t.Fatal("updateSectSecretRealmProfileField missing")
	}
	fieldEnd := strings.Index(text[fieldStart:], "func parseSectSecretRealmBool(")
	if fieldEnd < 0 {
		t.Fatal("updateSectSecretRealmProfileField boundary missing")
	}
	fieldBlock := text[fieldStart : fieldStart+fieldEnd]
	if got := strings.Count(fieldBlock, "formatPlainValue(profile.Key)"); got != 13 {
		t.Fatalf("profile detail key formatPlainValue count = %d, want 13", got)
	}
	if !strings.Contains(fieldBlock, "formatPlainValue(profile.Key), formatPlainValue(name)") {
		t.Fatal("profile name detail should format key and name")
	}
	for _, unsafe := range []string{
		"profile.Key, name",
		"profile.Key, v",
		"profile.Key, enabled",
	} {
		if strings.Contains(fieldBlock, unsafe) {
			t.Fatalf("profile detail uses raw value: %s", unsafe)
		}
	}
}

func TestSectSecretRealmConfigAdminPlainRepliesUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm_config.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm_config.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainValue(normalizedText), adminReasonRequirementText",
		"formatPlainValue(pendingCommand), formatPlainValue(reason)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect secret realm config admin plain reply guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"normalizedText, adminReasonRequirementText",
		", pendingCommand, reason))",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("sect secret realm config admin plain reply still uses raw value %q", unsafe)
		}
	}
}

func TestSectSecretRealmRewardAssetUpdatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	for _, marker := range []string{
		"SECT_SECRET_REALM_CONTRIBUTION_REWARD_MISSED",
		"SECT_SECRET_REALM_PRESTIGE_REWARD_MISSED",
	} {
		idx := strings.Index(text, marker)
		if idx < 0 {
			t.Fatalf("missing sect secret realm reward guard: %s", marker)
		}
		start := idx - 260
		if start < 0 {
			start = 0
		}
		block := text[start:minInt(len(text), idx+len(marker)+80)]
		if !strings.Contains(block, "res.RowsAffected == 0") {
			t.Fatalf("reward guard %s should check RowsAffected: %s", marker, block)
		}
	}
}

func TestSectSecretRealmSettlementCriticalWritesAreChecked(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"func rollbackSectSecretRealmSettlement(realmID string, reason error)",
		"rollbackSectSecretRealmSettlement(realmID, err)",
		"rollbackSectSecretRealmSettlement(realmID, res.Error)",
		"SECT_SECRET_REALM_PARTICIPANT_FINAL_UPDATE_MISSED",
		"SECT_SECRET_REALM_EVENT_SETTLE_MISSED",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("settlement guard missing %q", want)
		}
	}
	if strings.Contains(text, "DB.Model(p).Updates(") {
		t.Fatalf("settlement participant final update must not ignore DB update result")
	}
	if strings.Contains(text, "DB.Where(\"realm_id = ?\", realmID).Find(&participants)\n") {
		t.Fatalf("settlement participant query must check DB error")
	}
}

func TestSectSecretRealmLiveProgressEventUpdateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func refreshSectSecretRealmLiveProgress(")
	if start < 0 {
		t.Fatal("refreshSectSecretRealmLiveProgress missing")
	}
	end := strings.Index(text[start:], "func sectSecretRealmCultivationSnapshot(")
	if end < 0 {
		t.Fatal("refreshSectSecretRealmLiveProgress boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := DB.Model(&SectSecretRealmEvent{})",
		`Where("realm_id = ? AND status = ?", event.RealmID, "active")`,
		"res.Error",
		"res.RowsAffected == 0",
		"SECT_SECRET_REALM_EVENT_REFRESH_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm live progress event update guard missing %q", want)
		}
	}
	if strings.Contains(block, `}).Error; err != nil`) {
		t.Fatal("sect secret realm live progress event update still ignores RowsAffected")
	}
}

func TestSectSecretRealmRankQueriesHandleReadErrors(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"func sectSecretRealmTopParticipants(realmID string, limit int) ([]SectSecretRealmParticipant, error)",
		"宗门秘境排行暂时读取失败",
		"排行读取失败，请稍后发送 `宗门秘境排行` 查看。",
		"宗门秘境实时榜排行读取失败",
		"宗门秘境结算排行读取失败",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect secret realm rank read guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"DB.Where(\"realm_id = ?\", event.RealmID).\n\t\tOrder(sectSecretRealmRankOrder).\n\t\tLimit(10).\n\t\tFind(&participants)",
		"DB.Where(\"realm_id = ?\", event.RealmID).\r\n\t\tOrder(sectSecretRealmRankOrder).\r\n\t\tLimit(10).\r\n\t\tFind(&participants)",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("sect secret realm rank query still ignores DB errors")
		}
	}
}

func TestSectSecretRealmJoinParticipantReloadHandlesReadError(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"宗门秘境参与记录读取失败",
		"realmText = \"读取失败\"",
		"baseHoursText = \"`读取失败`\"",
		"当前基线净修为：%s",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect secret realm join participant reload guard missing %q", want)
		}
	}
	if strings.Contains(text, "DB.Where(\"realm_id = ? AND user_id = ?\", event.RealmID, userID).First(&participant)\n") ||
		strings.Contains(text, "DB.Where(\"realm_id = ? AND user_id = ?\", event.RealmID, userID).First(&participant)\r\n") {
		t.Fatal("sect secret realm join participant reload still ignores DB errors")
	}
}

func TestSectSecretRealmJoinUserReadErrorsAreNotTreatedAsUnbound(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleJoinSectSecretRealm(")
	if start < 0 {
		t.Fatal("handleJoinSectSecretRealm missing")
	}
	end := strings.Index(text[start:], "func sectSecretRealmParticipantOnConflict(")
	if end < 0 {
		t.Fatal("handleJoinSectSecretRealm boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"进入宗门秘境读取本地档案失败",
		"formatPlainError(err)",
		"进入宗门秘境读取本地档案失败，请稍后重试",
		`if strings.TrimSpace(u.AbsUserID) == ""`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm join user read guard missing %q", want)
		}
	}
	if strings.Contains(block, `err != nil || strings.TrimSpace(u.AbsUserID) == ""`) {
		t.Fatal("sect secret realm join still treats DB read errors as unbound users")
	}
}

func TestSectSecretRealmCommandReadErrorsAreDistinguished(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)

	for _, want := range []string{
		"func replySectSecretRealmMemberReadFailure(",
		"func replySectSecretRealmSectReadFailure(",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"宗门成员档案读取失败，请稍后重试。",
		"宗门档案读取失败，请稍后重试。",
		"formatPlainError(err)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect secret realm command read failure helper missing %q", want)
		}
	}

	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
		forbidden []string
	}{
		{
			name:      "status",
			startFunc: "func handleSectSecretRealmStatus(",
			endFunc:   "func handleOpenSectSecretRealm(",
			markers: []string{
				`replySectSecretRealmMemberReadFailure(bot, chatID, userID, "状态", err)`,
				`replySectSecretRealmSectReadFailure(bot, chatID, userID, member.SectID, "状态", err)`,
			},
			forbidden: []string{
				`if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		replyText(bot, chatID, "❌ 您当前没有加入宗门。")
		return
	}`,
				`if err := DB.Where("id = ?", member.SectID).First(&sect).Error; err != nil {
		replyText(bot, chatID, "❌ 宗门档案异常。")
		return
	}`,
			},
		},
		{
			name:      "open",
			startFunc: "func handleOpenSectSecretRealm(",
			endFunc:   "func handleJoinSectSecretRealm(",
			markers: []string{
				`replySectSecretRealmMemberReadFailure(bot, chatID, userID, "开启", err)`,
				`replySectSecretRealmSectReadFailure(bot, chatID, userID, member.SectID, "开启", err)`,
			},
		},
		{
			name:      "join",
			startFunc: "func handleJoinSectSecretRealm(",
			endFunc:   "func sectSecretRealmParticipantOnConflict(",
			markers: []string{
				`replySectSecretRealmMemberReadFailure(bot, chatID, userID, "进入", err)`,
			},
		},
		{
			name:      "manual settlement",
			startFunc: "func handleSettleSectSecretRealmCommand(",
			endFunc:   "func settleExpiredSectSecretRealms(",
			markers: []string{
				`replySectSecretRealmMemberReadFailure(bot, chatID, userID, "结算", err)`,
				"宗门秘境手动结算活动读取失败",
				"宗门秘境结算状态暂时读取失败，请稍后重试。",
			},
			forbidden: []string{
				`First(&event).Error; err != nil {
		replyText(bot, chatID, "📜 当前没有可结算的宗门秘境。")
		return
	}`,
			},
		},
		{
			name:      "rank",
			startFunc: "func handleSectSecretRealmRank(",
			endFunc:   "func handleSectSecretRealmDetail(",
			markers: []string{
				`replySectSecretRealmMemberReadFailure(bot, chatID, userID, "排行", err)`,
			},
		},
		{
			name:      "detail",
			startFunc: "func handleSectSecretRealmDetail(",
			endFunc:   "func getSectSecretRealmEffectiveListeningHours(",
			markers: []string{
				`replySectSecretRealmMemberReadFailure(bot, chatID, userID, "明细", err)`,
				"宗门秘境明细参与记录读取失败",
				"宗门秘境明细暂时读取失败，请稍后重试。",
			},
			forbidden: []string{
				`if err := DB.Where("realm_id = ? AND user_id = ?", event.RealmID, targetUserID).First(&participant).Error; err != nil {
		replyText(bot, chatID, "📜 未找到该道友在本次秘境中的参与记录。")
		return
	}`,
			},
		},
	}

	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s start missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endFunc)
		if end < 0 {
			t.Fatalf("%s boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range tt.markers {
			if !strings.Contains(block, want) {
				t.Fatalf("%s read error guard missing %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still treats database errors as business absence", tt.name)
			}
		}
	}
}

func TestSectSecretRealmLiveRefreshUserReadErrorsAreLogged(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func refreshSectSecretRealmLiveProgress(")
	if start < 0 {
		t.Fatal("refreshSectSecretRealmLiveProgress missing")
	}
	end := strings.Index(text[start:], "func countSectSecretRealmWeeklyOpenTx(")
	if end < 0 {
		t.Fatal("refreshSectSecretRealmLiveProgress boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"sectSecretRealmProfileFromSnapshotChecked(event.ProfileKey, event.ConfigSnapshot)",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"宗门秘境实时刷新读取本地档案失败",
		"formatPlainError(err)",
		`clearSectSecretRealmParticipantComputedReward(*p, "user_not_found")`,
		`if strings.TrimSpace(u.AbsUserID) == ""`,
		`clearSectSecretRealmParticipantComputedReward(*p, "abs_unbound")`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm live refresh user read guard missing %q", want)
		}
	}
	if strings.Contains(block, `err != nil || strings.TrimSpace(u.AbsUserID) == ""`) {
		t.Fatal("sect secret realm live refresh still treats DB read errors as unbound users")
	}
	if strings.Contains(block, "\u923f") || strings.Contains(block, "\u7019\u6945\u68ec") {
		t.Fatal("sect secret realm live refresh diagnostics contain mojibake")
	}
}

func TestSectSecretRealmSettlementUserReadErrorsRollback(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func settleSectSecretRealm(")
	if start < 0 {
		t.Fatal("settleSectSecretRealm missing")
	}
	end := strings.Index(text[start:], "func rollbackSectSecretRealmSettlement(")
	if end < 0 {
		t.Fatal("settleSectSecretRealm boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"sectSecretRealmProfileFromSnapshotChecked(event.ProfileKey, event.ConfigSnapshot)",
		"rollbackSectSecretRealmSettlement(realmID, err)",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		`rollbackSectSecretRealmSettlement(realmID, fmt.Errorf("SECT_SECRET_REALM_USER_READ_FAILED user=%d: %w", p.UserID, err))`,
		`if strings.TrimSpace(u.AbsUserID) == ""`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm settlement user read guard missing %q", want)
		}
	}
	if strings.Contains(block, `err != nil || strings.TrimSpace(u.AbsUserID) == ""`) {
		t.Fatal("sect secret realm settlement still treats DB read errors as unbound users")
	}
	if strings.Contains(block, "profile := sectSecretRealmProfileFromSnapshot(event.ProfileKey, event.ConfigSnapshot)") {
		t.Fatal("sect secret realm settlement should not fall back to current config when snapshot is invalid")
	}
}

func TestSectSecretRealmSettlementRewardParticipantsMatchEligibleSet(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func settleSectSecretRealm(")
	if start < 0 {
		t.Fatal("settleSectSecretRealm missing")
	}
	end := strings.Index(text[start:], "func clearSectSecretRealmParticipantComputedReward(")
	if end < 0 {
		t.Fatal("settleSectSecretRealm reward participant boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`clearSectSecretRealmParticipantComputedReward(*p, "user_not_found")`,
		`clearSectSecretRealmParticipantComputedReward(*p, "abs_unbound")`,
		"eligibleParticipants := make([]SectSecretRealmParticipant, 0, len(participants))",
		"sectSecretRealmGuardianForProfile(eligibleParticipants, profile)",
		"rewardParticipants := make([]SectSecretRealmParticipant, 0, len(eligibleParticipants))",
		"rewardParticipants = append(rewardParticipants, *p)",
		"event.ParticipantCount = len(participants)",
		"grantSectSecretRealmRewards(event, rewardParticipants)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm settlement reward participant guard missing %q", want)
		}
	}
	if strings.Contains(block, "grantSectSecretRealmRewards(event, participants)") {
		t.Fatal("sect secret realm settlement still rewards the unfiltered participant set")
	}

	start = strings.Index(text, "func clearSectSecretRealmParticipantComputedReward(")
	if start < 0 {
		t.Fatal("clearSectSecretRealmParticipantComputedReward missing")
	}
	end = strings.Index(text[start:], "func rollbackSectSecretRealmSettlement(")
	if end < 0 {
		t.Fatal("clearSectSecretRealmParticipantComputedReward boundary missing")
	}
	block = text[start : start+end]
	for _, want := range []string{
		`Where("id = ? AND realm_id = ? AND user_id = ?", p.ID, p.RealmID, p.UserID)`,
		`"final_hours":                p.BaseHours`,
		`"reward_points":              0`,
		`"reward_drop_item":           ""`,
		"res.Error",
		"res.RowsAffected == 0",
		"SECT_SECRET_REALM_PARTICIPANT_CLEAR_REWARD_MISSED",
		"formatPlainValue(reason)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm participant reward clearing guard missing %q", want)
		}
	}

	start = strings.Index(text, "func grantSectSecretRealmRewards(")
	if start < 0 {
		t.Fatal("grantSectSecretRealmRewards missing")
	}
	end = strings.Index(text[start:], "func sendSectSecretRealmSettlement(")
	if end < 0 {
		t.Fatal("grantSectSecretRealmRewards boundary missing")
	}
	block = text[start : start+end]
	if !strings.Contains(block, `"participant_count":         event.ParticipantCount`) {
		t.Fatal("sect secret realm settlement should persist explicit participant count")
	}
	if strings.Contains(block, `"participant_count":         len(participants)`) {
		t.Fatal("sect secret realm settlement participant count depends on filtered reward slice")
	}
}

func TestSectSecretRealmStaleSettlingRecoveryIsWired(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := strings.ReplaceAll(string(source), "\r\n", "\n")
	start := strings.Index(text, "func recoverStaleSectSecretRealmSettlements(")
	if start < 0 {
		t.Fatal("recoverStaleSectSecretRealmSettlements missing")
	}
	end := strings.Index(text[start:], "func settleSectSecretRealm(")
	if end < 0 {
		t.Fatal("recoverStaleSectSecretRealmSettlements boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"sectSecretRealmSettlementStaleAfter",
		`Where("status = ? AND updated_at < ? AND settled_at IS NULL", "settling", cutoff)`,
		`Update("status", "active")`,
		"formatPlainError(res.Error)",
		"res.RowsAffected > 0",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm stale settling recovery missing %q", want)
		}
	}
	if !strings.Contains(text, "func settleExpiredSectSecretRealms(bot *tgbotapi.BotAPI, now time.Time) {\n\trecoverStaleSectSecretRealmSettlements(now)") {
		t.Fatal("sect secret realm expired scanner does not call stale settling recovery")
	}
}

func TestSectSecretRealmJoinUsesCheckedProfileSnapshot(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleJoinSectSecretRealm(")
	if start < 0 {
		t.Fatal("handleJoinSectSecretRealm missing")
	}
	end := strings.Index(text[start:], "func sectSecretRealmParticipantOnConflict(")
	if end < 0 {
		t.Fatal("handleJoinSectSecretRealm boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"sectSecretRealmProfileFromSnapshotChecked(event.ProfileKey, event.ConfigSnapshot)",
		"进入宗门秘境读取配置快照失败",
		"宗门秘境配置快照读取失败，请稍后重试。",
		"formatPlainError(err)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm join snapshot guard missing %q", want)
		}
	}
	if strings.Contains(block, "profile := sectSecretRealmProfileFromSnapshot(event.ProfileKey, event.ConfigSnapshot)") {
		t.Fatal("sect secret realm join should not fall back to current config when snapshot is invalid")
	}
}

func TestSectSecretRealmDiagnosticsUseSanitizedErrors(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainError(err)",
		"formatPlainError(res.Error)",
		"formatPlainError(reason)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect secret realm diagnostics missing %q", want)
		}
	}
	rawErrFormat := string([]byte{'e', 'r', 'r', '=', '%', 'v'})
	if strings.Contains(text, rawErrFormat) {
		t.Fatal("sect secret realm diagnostics should not log raw error values")
	}
}

func TestSectSecretRealmSchedulerQueryFailuresAreLogged(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
	}{
		{
			name:      "refresh active",
			startFunc: "func refreshActiveSectSecretRealms(",
			endFunc:   "func handleSectSecretRealmStatus(",
			markers: []string{
				"查询进行中宗门秘境失败",
				"已跳过本轮实时刷新",
			},
		},
		{
			name:      "settle expired",
			startFunc: "func settleExpiredSectSecretRealms(",
			endFunc:   "func settleSectSecretRealm(",
			markers: []string{
				"查询到期宗门秘境失败",
				"已跳过本轮结算扫描",
			},
		},
	}
	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s scheduler function missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endFunc)
		if end < 0 {
			t.Fatalf("%s scheduler function boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range append(tt.markers, "formatPlainValue(now.Format(time.RFC3339))", "formatPlainError(err)") {
			if !strings.Contains(block, want) {
				t.Fatalf("%s scheduler query failure log missing %q", tt.name, want)
			}
		}
		if strings.Contains(block, "if err := DB.Where") && strings.Contains(block, "Find(&events).Error; err != nil {\n\t\treturn\n\t}") {
			t.Fatalf("%s scheduler query failure still returns silently", tt.name)
		}
	}
}

func TestSectSecretRealmLiveBoardMessageIDUpdateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect_secret_realm.go")
	if err != nil {
		t.Fatalf("read sect_secret_realm.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func ensureSectSecretRealmLiveBoardSync(")
	if start < 0 {
		t.Fatal("ensureSectSecretRealmLiveBoardSync missing")
	}
	end := strings.Index(text[start:], "func handleSectSecretRealmRank(")
	if end < 0 {
		t.Fatal("ensureSectSecretRealmLiveBoardSync boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := DB.Model(&SectSecretRealmEvent{})",
		`Where("realm_id = ?", event.RealmID)`,
		"res.Error",
		"formatPlainError(err)",
		"res.RowsAffected == 0",
		"sect secret realm live board message id record missed",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect secret realm live board message id guard missing %q", want)
		}
	}
	if strings.Contains(block, "Updates(map[string]interface{}{\n\t\t\t\"board_chat_id\":    sentMsg.Chat.ID,\n\t\t\t\"board_message_id\": sentMsg.MessageID,\n\t\t}).Error; err != nil") {
		t.Fatal("sect secret realm live board message id update still ignores RowsAffected")
	}
}

func TestCalculateSectSecretRealmRewardsCurrentRules(t *testing.T) {
	tests := []struct {
		name         string
		deltaHours   float64
		points       int
		contribution int
		prestige     int
	}{
		{name: "old threshold now below reward", deltaHours: 0.2, points: 0, contribution: 0, prestige: 0},
		{name: "below threshold", deltaHours: 0.49, points: 0, contribution: 0, prestige: 0},
		{name: "half hour reward", deltaHours: 0.5, points: 3, contribution: 1, prestige: 0},
		{name: "one hour", deltaHours: 1.0, points: 6, contribution: 3, prestige: 1},
		{name: "capped reward", deltaHours: 20.0, points: 18, contribution: 12, prestige: 10},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			points, contribution, prestige := calculateSectSecretRealmRewards(tt.deltaHours)
			if points != tt.points || contribution != tt.contribution || prestige != tt.prestige {
				t.Fatalf("rewards = (%d, %d, %d), want (%d, %d, %d)", points, contribution, prestige, tt.points, tt.contribution, tt.prestige)
			}
		})
	}
}

func TestSectSecretRealmRewardMultiplierForMajor(t *testing.T) {
	tests := []struct {
		major               int
		pointPercent        int
		contributionPercent int
		prestigePercent     int
	}{
		{major: 0, pointPercent: 100, contributionPercent: 100, prestigePercent: 100},
		{major: 1, pointPercent: 100, contributionPercent: 100, prestigePercent: 100},
		{major: 2, pointPercent: 105, contributionPercent: 115, prestigePercent: 115},
		{major: 3, pointPercent: 110, contributionPercent: 125, prestigePercent: 125},
		{major: 4, pointPercent: 115, contributionPercent: 140, prestigePercent: 140},
		{major: 5, pointPercent: 120, contributionPercent: 150, prestigePercent: 150},
		{major: 9, pointPercent: 120, contributionPercent: 150, prestigePercent: 150},
	}

	for _, tt := range tests {
		got := sectSecretRealmRewardMultiplierForMajor(tt.major)
		if got.PointPercent != tt.pointPercent ||
			got.ContributionPercent != tt.contributionPercent ||
			got.PrestigePercent != tt.prestigePercent {
			t.Fatalf("major %d multiplier = (%d, %d, %d), want (%d, %d, %d)",
				tt.major,
				got.PointPercent,
				got.ContributionPercent,
				got.PrestigePercent,
				tt.pointPercent,
				tt.contributionPercent,
				tt.prestigePercent)
		}
	}
}

func TestCalculateSectSecretRealmRewardsForRealm(t *testing.T) {
	points, contribution, prestige := calculateSectSecretRealmRewardsForRealm(3.0, 5)
	if points != 18 || contribution != 12 || prestige != 4 {
		t.Fatalf("high realm rewards = (%d, %d, %d), want (18, 12, 4)", points, contribution, prestige)
	}

	points, contribution, prestige = calculateSectSecretRealmRewardsForRealm(0.2, 5)
	if points != 0 || contribution != 0 || prestige != 0 {
		t.Fatalf("old minimum high realm rewards = (%d, %d, %d), want (0, 0, 0)", points, contribution, prestige)
	}

	points, contribution, prestige = calculateSectSecretRealmRewardsForRealm(0.5, 5)
	if points != 8 || contribution != 1 || prestige != 0 {
		t.Fatalf("minimum high realm rewards = (%d, %d, %d), want (8, 1, 0)", points, contribution, prestige)
	}
}

func TestCalculateSectSecretRealmRewardsForUserExample(t *testing.T) {
	points, contribution, prestige := calculateSectSecretRealmRewardsForRealm(0.84, 3)
	if points != 8 || contribution != 2 || prestige != 0 {
		t.Fatalf("core realm 0.84h rewards = (%d, %d, %d), want (8, 2, 0)", points, contribution, prestige)
	}
}

func TestSectSecretRealmDropForScore(t *testing.T) {
	tests := []struct {
		name       string
		major      int
		deltaHours float64
		score      int
		item       string
		quantity   int
	}{
		{name: "below threshold", major: 5, deltaHours: 0.19, score: 0, item: "", quantity: 0},
		{name: "mortal no drop pool", major: 0, deltaHours: 1, score: 0, item: "", quantity: 0},
		{name: "qi refining seed", major: 1, deltaHours: 1, score: 29, item: "凝露草种子", quantity: 1},
		{name: "foundation seed", major: 2, deltaHours: 1, score: 24, item: "龙血果种子", quantity: 1},
		{name: "core seed", major: 3, deltaHours: 1, score: 24, item: "天心莲种子", quantity: 1},
		{name: "nascent token", major: 4, deltaHours: 1, score: 34, item: sectSecretRealmTokenItemName, quantity: 1},
		{name: "miss chance", major: 4, deltaHours: 1, score: 35, item: "", quantity: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			item, quantity := sectSecretRealmDropForScore(tt.major, tt.deltaHours, tt.score)
			if item != tt.item || quantity != tt.quantity {
				t.Fatalf("drop = (%q, %d), want (%q, %d)", item, quantity, tt.item, tt.quantity)
			}
		})
	}
}

func TestSectSecretRealmRawListeningHelpers(t *testing.T) {
	days := map[string]float64{
		"2026-06-12": 3600,
		"2026-06-13": 7200,
		"invalid":    -500,
	}
	if got := sumSectSecretRealmRawListeningSeconds(days); got != 10800 {
		t.Fatalf("raw seconds = %.0f, want 10800", got)
	}
}

func TestSectSecretRealmRawDeltaUsesWallClockCap(t *testing.T) {
	start := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	joined := start.Add(30 * time.Minute)
	settled := end.Add(10 * time.Minute)

	got := calculateSectSecretRealmRawDeltaSeconds(1000, 20000, joined, start, end, settled)
	want := 90 * 60.0
	if got != want {
		t.Fatalf("raw delta = %.0f, want %.0f", got, want)
	}
}

func TestSectSecretRealmRawDeltaReturnsZeroWithoutWallClockWindow(t *testing.T) {
	start := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	end := start.Add(2 * time.Hour)
	joined := end.Add(time.Minute)
	settled := end

	got := calculateSectSecretRealmRawDeltaSeconds(1000, 20000, joined, start, end, settled)
	if got != 0 {
		t.Fatalf("raw delta = %.0f, want 0", got)
	}
}

func TestSectSecretRealmSuppressedHoursUsesRealmCurve(t *testing.T) {
	tests := []struct {
		name       string
		rawSeconds float64
		wantHours  float64
	}{
		{name: "inside full-rate window", rawSeconds: 90 * 60, wantHours: 1.5},
		{name: "after full-rate window", rawSeconds: 3 * 3600, wantHours: 2.5},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateSectSecretRealmSuppressedHours(tt.rawSeconds)
			if math.Abs(got-tt.wantHours) > 0.000001 {
				t.Fatalf("suppressed hours = %.6f, want %.6f", got, tt.wantHours)
			}
		})
	}
}

func TestSectSecretRealmSuppressedHoursUsesProfileCurve(t *testing.T) {
	profile := SectSecretRealmProfileConfig{
		PressureFullHours: 3,
		PressureAfterRate: 0.6,
	}
	got := calculateSectSecretRealmSuppressedHoursForProfile(4*3600, profile)
	if math.Abs(got-3.6) > 0.000001 {
		t.Fatalf("profile suppressed hours = %.6f, want 3.6", got)
	}
}

func TestSectSecretRealmGuardianBonusPercentForMajor(t *testing.T) {
	tests := []struct {
		major int
		want  int
	}{
		{major: 0, want: 0},
		{major: 1, want: 0},
		{major: 2, want: 3},
		{major: 3, want: 6},
		{major: 4, want: 10},
		{major: 9, want: 10},
	}

	for _, tt := range tests {
		if got := sectSecretRealmGuardianBonusPercentForMajor(tt.major); got != tt.want {
			t.Fatalf("major %d guardian bonus = %d, want %d", tt.major, got, tt.want)
		}
	}
}

func TestSectSecretRealmProfileRules(t *testing.T) {
	cfg := defaultSectSecretRealmConfig()
	high, ok := cfg.profile(sectSecretRealmProfileHigh)
	if !ok {
		t.Fatal("high profile missing")
	}
	if !high.Enabled || high.MinSectLevel != 3 || high.MinMajorRealm != 2 {
		t.Fatalf("unexpected high profile gates: enabled=%t sect=%d major=%d", high.Enabled, high.MinSectLevel, high.MinMajorRealm)
	}
	if math.Abs(high.MinDeltaHours-sectSecretRealmHighMinDeltaHours) > 0.000001 {
		t.Fatalf("high profile min delta = %.2f, want %.2f", high.MinDeltaHours, sectSecretRealmHighMinDeltaHours)
	}
	if cost := getSectSecretRealmProfileCost(high, 3); cost != 315 {
		t.Fatalf("high profile cost = %d, want 315", cost)
	}

	points, contribution, prestige := calculateSectSecretRealmRewardsForProfile(4, 5, high)
	if points != 30 || contribution != 22 || prestige != 7 {
		t.Fatalf("high profile rewards = (%d, %d, %d), want (30, 22, 7)", points, contribution, prestige)
	}

	item, quantity := sectSecretRealmDropForScoreWithProfile(4, 1, 44, high)
	if item != sectSecretRealmTokenItemName || quantity != 1 {
		t.Fatalf("high profile drop = (%q, %d), want token x1", item, quantity)
	}
}

func TestSectSecretRealmProfileSnapshotCheckedFailsClosed(t *testing.T) {
	if _, err := sectSecretRealmProfileFromSnapshotChecked(sectSecretRealmProfileNormal, ""); err == nil {
		t.Fatal("empty profile snapshot should fail closed")
	}
	if _, err := sectSecretRealmProfileFromSnapshotChecked(sectSecretRealmProfileNormal, "{bad json"); err == nil {
		t.Fatal("invalid profile snapshot should fail closed")
	}

	cfg := defaultSectSecretRealmConfig()
	high, ok := cfg.profile(sectSecretRealmProfileHigh)
	if !ok {
		t.Fatal("high profile missing")
	}
	snapshot := sectSecretRealmProfileSnapshot(high)
	got, err := sectSecretRealmProfileFromSnapshotChecked(sectSecretRealmProfileHigh, snapshot)
	if err != nil {
		t.Fatalf("valid profile snapshot err = %v", err)
	}
	if got.Key != sectSecretRealmProfileHigh || got.Name != high.Name {
		t.Fatalf("profile from snapshot = (%q, %q), want (%q, %q)", got.Key, got.Name, sectSecretRealmProfileHigh, high.Name)
	}
}

func TestSectSecretRealmNormalProfileCostAndThreshold(t *testing.T) {
	cfg := defaultSectSecretRealmConfig()
	normal, ok := cfg.profile(sectSecretRealmProfileNormal)
	if !ok {
		t.Fatal("normal profile missing")
	}
	if normal.BaseCost != 100 || normal.CostPerLevel != 30 {
		t.Fatalf("normal profile cost formula = %d + level*%d, want 100 + level*30", normal.BaseCost, normal.CostPerLevel)
	}
	if cost := getSectSecretRealmProfileCost(normal, 1); cost != 130 {
		t.Fatalf("normal profile level 1 cost = %d, want 130", cost)
	}
	if math.Abs(normal.MinDeltaHours-0.5) > 0.000001 {
		t.Fatalf("normal profile min delta = %.2f, want 0.50", normal.MinDeltaHours)
	}
}

func TestNormalizeSectSecretRealmOldDefaultEconomy(t *testing.T) {
	normal := SectSecretRealmProfileConfig{
		Key:             sectSecretRealmProfileNormal,
		Name:            sectSecretRealmName,
		BaseCost:        80,
		CostPerLevel:    20,
		DurationMinutes: 120,
		MinDeltaHours:   0.2,
	}
	normalizeSectSecretRealmProfile(&normal)
	if normal.BaseCost != 100 || normal.CostPerLevel != 30 || math.Abs(normal.MinDeltaHours-0.5) > 0.000001 {
		t.Fatalf("normalized normal economy = %d + level*%d min %.2f, want 100 + level*30 min 0.50", normal.BaseCost, normal.CostPerLevel, normal.MinDeltaHours)
	}

	savedNormal := SectSecretRealmProfileConfig{
		Key:             sectSecretRealmProfileNormal,
		Name:            sectSecretRealmName,
		BaseCost:        100,
		CostPerLevel:    30,
		DurationMinutes: 120,
		MinDeltaHours:   0.5,
	}
	normalizeSectSecretRealmProfile(&savedNormal)
	if math.Abs(savedNormal.MinDeltaHours-0.5) > 0.000001 {
		t.Fatalf("normalized saved normal min delta = %.2f, want 0.50", savedNormal.MinDeltaHours)
	}

	high := SectSecretRealmProfileConfig{
		Key:             sectSecretRealmProfileHigh,
		Name:            "玄阶秘境",
		BaseCost:        180,
		CostPerLevel:    45,
		DurationMinutes: 180,
		MinDeltaHours:   0.3,
	}
	normalizeSectSecretRealmProfile(&high)
	if high.BaseCost != 180 || high.CostPerLevel != 45 || math.Abs(high.MinDeltaHours-0.75) > 0.000001 {
		t.Fatalf("normalized high economy = %d + level*%d min %.2f, want 180 + level*45 min 0.75", high.BaseCost, high.CostPerLevel, high.MinDeltaHours)
	}

	savedLimited := SectSecretRealmProfileConfig{
		Key:             sectSecretRealmProfileLimited,
		Name:            "闄愭椂绉樺",
		BaseCost:        120,
		CostPerLevel:    25,
		DurationMinutes: 120,
		MinDeltaHours:   0.5,
	}
	normalizeSectSecretRealmProfile(&savedLimited)
	if math.Abs(savedLimited.MinDeltaHours-0.5) > 0.000001 {
		t.Fatalf("normalized saved limited min delta = %.2f, want 0.50", savedLimited.MinDeltaHours)
	}
}

func TestSectSecretRealmWeeklyOpenWindowUsesBeijingWeek(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)
	start, end := sectSecretRealmWeeklyOpenWindow(now)
	loc := time.FixedZone("CST", 8*3600)
	wantStart := time.Date(2026, 6, 15, 0, 0, 0, 0, loc)
	wantEnd := time.Date(2026, 6, 22, 0, 0, 0, 0, loc)
	if !start.Equal(wantStart) || !end.Equal(wantEnd) {
		t.Fatalf("weekly window = %s - %s, want %s - %s", start, end, wantStart, wantEnd)
	}
}

func TestApplySectSecretRealmHourBonus(t *testing.T) {
	got := applySectSecretRealmHourBonus(2.0, 10)
	if math.Abs(got-2.2) > 0.000001 {
		t.Fatalf("bonus hours = %.6f, want 2.2", got)
	}
	if got := applySectSecretRealmHourBonus(2.0, 0); got != 2.0 {
		t.Fatalf("zero bonus hours = %.6f, want 2.0", got)
	}
}

func TestSectSecretRealmGuardianSelection(t *testing.T) {
	base := time.Date(2026, 6, 13, 10, 0, 0, 0, time.UTC)
	participants := []SectSecretRealmParticipant{
		{UserID: 1, UserName: "炼气", MajorRealm: 1, MinorRealm: 4, Model: gorm.Model{CreatedAt: base}},
		{UserID: 2, UserName: "结丹", MajorRealm: 3, MinorRealm: 1, Model: gorm.Model{CreatedAt: base.Add(time.Minute)}},
		{UserID: 3, UserName: "结丹后期", MajorRealm: 3, MinorRealm: 3, Model: gorm.Model{CreatedAt: base.Add(2 * time.Minute)}},
		{UserID: 4, UserName: "筑基", MajorRealm: 2, MinorRealm: 4, Model: gorm.Model{CreatedAt: base.Add(3 * time.Minute)}},
	}

	guardian, bonus := sectSecretRealmGuardian(participants)
	if guardian.UserID != 3 || bonus != 6 {
		t.Fatalf("guardian = user %d bonus %d, want user 3 bonus 6", guardian.UserID, bonus)
	}
}
