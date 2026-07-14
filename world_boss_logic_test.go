package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestWorldBossRedPacketConfig(t *testing.T) {
	if worldBossRedPacketTotalPoints != 30 || worldBossRedPacketCount != 10 {
		t.Fatalf("world boss red packet = %d/%d, want 30/10", worldBossRedPacketTotalPoints, worldBossRedPacketCount)
	}
}

func TestWorldBossPointDescriptionNameSanitizesText(t *testing.T) {
	got := worldBossPointDescriptionName("  boss\nalpha\tbeta  ")
	if got != "boss alpha beta" {
		t.Fatalf("worldBossPointDescriptionName() = %q", got)
	}
	if got := worldBossPointDescriptionName("\n\t"); got != "-" {
		t.Fatalf("empty world boss description name fallback = %q", got)
	}

	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	if strings.Contains(text, "event.Name, p.Damage") {
		t.Fatal("world boss point reward description should not persist raw event name")
	}
	if !strings.Contains(text, "worldBossPointDescriptionName(event.Name), p.Damage") {
		t.Fatal("world boss point reward description should sanitize event name")
	}
}

func TestWorldBossMarkdownNamesAreEscaped(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"结算伤害 = 参与后 Boss 时段实际听书小时 × `%.0f` ×（1 + 修为加成 + 宗门科技）",
		"伤害 = 参与后 Boss 时段实际听书小时 × `%.0f` ×（1 + 修为加成 + 宗门科技）",
		"规则：实听小时 × `%.0f` ×（1 + 修为加成 + 宗门科技）",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("world boss damage rule text missing %q", want)
		}
	}

	tests := []struct {
		name      string
		startFunc string
		endFunc   string
	}{
		{"start notice", "func startWorldBossEvent(", "func worldBossEventExists("},
		{"join reply", "func handleJoinWorldBoss(", "func handleWorldBossStatus("},
		{"status reply", "func handleWorldBossStatus(", "func handleWorldBossRank("},
		{"rank reply", "func handleWorldBossRank(", "func sendWorldBossSettlement("},
		{"settlement notice", "func sendWorldBossSettlement(", "func renderWorldBossLiveBoard("},
		{"live board", "func renderWorldBossLiveBoard(", "func worldBossTopParticipants("},
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
		if !strings.Contains(block, "escapeMarkdown(event.Name)") {
			t.Fatalf("%s should escape world boss event name in Markdown", tt.name)
		}
		if strings.Contains(block, "\n\t\tevent.Name,") {
			t.Fatalf("%s still passes raw event.Name into Markdown fmt", tt.name)
		}
	}
}

func TestCanJoinWorldBossAtClosesLastFifteenMinutes(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	start := time.Date(2026, 6, 13, 21, 0, 0, 0, loc)
	event := WorldBossEvent{
		BossID:  "WB-20260613",
		Status:  "active",
		StartAt: start,
		EndAt:   start.Add(time.Hour),
	}

	tests := []struct {
		name string
		now  time.Time
		want bool
	}{
		{name: "before start", now: start.Add(-time.Second), want: false},
		{name: "at start", now: start, want: true},
		{name: "before deadline", now: worldBossJoinDeadline(event).Add(-time.Second), want: true},
		{name: "at deadline", now: worldBossJoinDeadline(event), want: false},
		{name: "before end after deadline", now: event.EndAt.Add(-time.Second), want: false},
		{name: "at end", now: event.EndAt, want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := canJoinWorldBossAt(event, tt.now); got != tt.want {
				t.Fatalf("canJoinWorldBossAt(%s) = %v, want %v", tt.now.Format(time.RFC3339), got, tt.want)
			}
		})
	}
}

func TestWorldBossActiveQueriesDistinguishReadErrors(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"func getActiveWorldBossChecked(",
		"func getActiveOrLatestWorldBossChecked(",
		"getActiveWorldBossChecked()",
		"return WorldBossEvent{}, err",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("world boss active checked helper missing %q", want)
		}
	}

	extract := func(name string, startMarker string, endMarker string) string {
		start := strings.Index(text, startMarker)
		if start < 0 {
			t.Fatalf("%s block missing", name)
		}
		end := strings.Index(text[start:], endMarker)
		if end < 0 {
			t.Fatalf("%s block boundary missing", name)
		}
		return text[start : start+end]
	}

	joinBlock := extract("join", "func handleJoinWorldBoss(", "func handleWorldBossStatus(")
	for _, want := range []string{
		"event, eventErr := getActiveWorldBossChecked()",
		"世界Boss参加活动读取失败",
		"formatPlainError(eventErr)",
	} {
		if !strings.Contains(joinBlock, want) {
			t.Fatalf("world boss join active read guard missing %q", want)
		}
	}
	if strings.Contains(joinBlock, "event, ok := getActiveWorldBoss()") {
		t.Fatal("world boss join still treats active read errors as no active boss")
	}

	statusBlock := extract("status", "func handleWorldBossStatus(", "func handleWorldBossRank(")
	for _, want := range []string{
		"event, eventErr := getActiveOrLatestWorldBossChecked()",
		"世界Boss状态活动读取失败",
		"getActiveOrLatestWorldBossChecked(); latestErr == nil",
		"世界Boss状态结算后活动重读失败",
	} {
		if !strings.Contains(statusBlock, want) {
			t.Fatalf("world boss status active/latest read guard missing %q", want)
		}
	}
	if strings.Contains(statusBlock, "event, ok := getActiveOrLatestWorldBoss()") ||
		strings.Contains(statusBlock, "if latest, ok := getActiveOrLatestWorldBoss(); ok") {
		t.Fatal("world boss status still treats active/latest read errors as missing records")
	}

	rankBlock := extract("rank", "func handleWorldBossRank(", "func sendWorldBossSettlement(")
	for _, want := range []string{
		"event, eventErr := getActiveOrLatestWorldBossChecked()",
		"世界Boss排行活动读取失败",
		"getActiveOrLatestWorldBossChecked(); latestErr == nil",
		"世界Boss排行结算后活动重读失败",
	} {
		if !strings.Contains(rankBlock, want) {
			t.Fatalf("world boss rank active/latest read guard missing %q", want)
		}
	}
	if strings.Contains(rankBlock, "event, ok := getActiveOrLatestWorldBoss()") ||
		strings.Contains(rankBlock, "if latest, ok := getActiveOrLatestWorldBoss(); ok") {
		t.Fatal("world boss rank still treats active/latest read errors as missing records")
	}
}

func TestNewWorldBossRedPacketRestrictsClaimScope(t *testing.T) {
	now := time.Date(2026, 6, 13, 22, 0, 0, 0, time.UTC)
	event := WorldBossEvent{BossID: "WB-20260613"}

	packet := newWorldBossRedPacket("HB-WB-test", event, now)

	if packet.TotalPoints != 30 || packet.Count != 10 || packet.LeftPoints != 30 || packet.LeftCount != 10 {
		t.Fatalf("packet points/count = total %d count %d left %d/%d, want total 30 count 10 left 30/10",
			packet.TotalPoints, packet.Count, packet.LeftPoints, packet.LeftCount)
	}
	if packet.RefType != "world_boss" || packet.RefID != event.BossID {
		t.Fatalf("packet ref = %s/%s, want world_boss/%s", packet.RefType, packet.RefID, event.BossID)
	}
	if packet.ClaimScope != redPacketClaimScopeWorldBossParticipant {
		t.Fatalf("packet claim scope = %s, want %s", packet.ClaimScope, redPacketClaimScopeWorldBossParticipant)
	}
	if !packet.CreatedAt.Equal(now) {
		t.Fatalf("packet created_at = %s, want %s", packet.CreatedAt, now)
	}
}

func TestWorldBossStartDoesNotTreatQueryErrorAsMissingEvent(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "func worldBossEventExists(bossID string) (bool, error)") {
		t.Fatalf("missing world boss existence helper")
	}
	if !strings.Contains(text, "errors.Is(err, gorm.ErrRecordNotFound)") {
		t.Fatalf("world boss existence helper must distinguish record-not-found")
	}
	if strings.Contains(text, `if DB.Where("boss_id = ?", bossID).First(&existing).Error == nil`) {
		t.Fatalf("world boss start still treats any non-nil query error as missing event")
	}
	if !strings.Contains(text, "查询世界Boss是否已存在失败") {
		t.Fatalf("world boss start should log existence query failures")
	}
}

func TestWorldBossCreateRecordsCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	eventHelperStart := strings.Index(text, "func createWorldBossEventRecord(")
	if eventHelperStart < 0 {
		t.Fatal("createWorldBossEventRecord missing")
	}
	eventHelperEnd := strings.Index(text[eventHelperStart:], "type WorldBossParticipant struct")
	if eventHelperEnd < 0 {
		t.Fatal("createWorldBossEventRecord boundary missing")
	}
	eventHelperBlock := text[eventHelperStart : eventHelperStart+eventHelperEnd]
	for _, want := range []string{
		"event.BossID = formatPlainValue(event.BossID)",
		"event.Name = formatPlainValue(event.Name)",
		"event.Status = formatPlainValue(event.Status)",
		"res := db.Create(event)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"WORLD_BOSS_EVENT_CREATE_MISSED",
	} {
		if !strings.Contains(eventHelperBlock, want) {
			t.Fatalf("world boss event helper guard missing %q", want)
		}
	}

	packetHelperStart := strings.Index(text, "func createWorldBossRedPacketInTx(")
	if packetHelperStart < 0 {
		t.Fatal("createWorldBossRedPacketInTx missing")
	}
	packetHelperEnd := strings.Index(text[packetHelperStart:], "func grantWorldBossRewards(")
	if packetHelperEnd < 0 {
		t.Fatal("createWorldBossRedPacketInTx boundary missing")
	}
	packetHelperBlock := text[packetHelperStart : packetHelperStart+packetHelperEnd]
	for _, want := range []string{
		"packet.ID = formatPlainValue(packet.ID)",
		"packet.SenderName = formatPlainValue(packet.SenderName)",
		"packet.RefType = formatPlainValue(packet.RefType)",
		"packet.RefID = formatPlainValue(packet.RefID)",
		"packet.ClaimScope = formatPlainValue(packet.ClaimScope)",
		"res := tx.Create(packet)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"WORLD_BOSS_REDPACKET_CREATE_MISSED",
	} {
		if !strings.Contains(packetHelperBlock, want) {
			t.Fatalf("world boss red packet helper guard missing %q", want)
		}
	}

	start := strings.Index(text, "func startWorldBossEvent(")
	if start < 0 {
		t.Fatal("startWorldBossEvent missing")
	}
	startEnd := strings.Index(text[start:], "func worldBossEventExists(")
	if startEnd < 0 {
		t.Fatal("startWorldBossEvent boundary missing")
	}
	startBlock := text[start : start+startEnd]
	if !strings.Contains(startBlock, "createWorldBossEventRecord(DB, &event)") {
		t.Fatal("world boss start should use createWorldBossEventRecord")
	}
	if strings.Contains(startBlock, "DB.Create(&event).Error") {
		t.Fatal("world boss start event create still ignores RowsAffected")
	}

	grantStart := strings.Index(text, "func grantWorldBossRewards(")
	if grantStart < 0 {
		t.Fatal("grantWorldBossRewards missing")
	}
	grantEnd := strings.Index(text[grantStart:], "func awardWorldBossSectRewardTx(")
	if grantEnd < 0 {
		t.Fatal("grantWorldBossRewards boundary missing")
	}
	grantBlock := text[grantStart : grantStart+grantEnd]
	if !strings.Contains(grantBlock, "createWorldBossRedPacketInTx(tx, &packet)") {
		t.Fatal("world boss reward should use createWorldBossRedPacketInTx")
	}
	if strings.Contains(grantBlock, "tx.Create(&packet).Error") {
		t.Fatal("world boss red packet create still ignores RowsAffected")
	}
}

func TestWorldBossParticipantCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	helperStart := strings.Index(text, "func createWorldBossParticipantInTx(")
	if helperStart < 0 {
		t.Fatal("createWorldBossParticipantInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "const (")
	if helperEnd < 0 {
		t.Fatal("createWorldBossParticipantInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"entry := *participant",
		"entry.BossID = formatPlainValue(entry.BossID)",
		"entry.UserName = formatPlainValue(entry.UserName)",
		"res := db.Clauses(clause.OnConflict{",
		"DoNothing: true",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"*participant = entry",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("world boss participant helper guard missing %q", want)
		}
	}

	joinStart := strings.Index(text, "func handleJoinWorldBoss(")
	if joinStart < 0 {
		t.Fatal("handleJoinWorldBoss missing")
	}
	joinEnd := strings.Index(text[joinStart:], "func handleWorldBossStatus(")
	if joinEnd < 0 {
		t.Fatal("handleJoinWorldBoss boundary missing")
	}
	joinBlock := text[joinStart : joinStart+joinEnd]
	if !strings.Contains(joinBlock, "createWorldBossParticipantInTx(DB, &WorldBossParticipant{") {
		t.Fatal("world boss join should use createWorldBossParticipantInTx")
	}
	if strings.Contains(joinBlock, "}).Create(&WorldBossParticipant{") {
		t.Fatal("world boss join still creates participant directly")
	}
}

func TestWorldBossParticipantMigrationReplacesFullUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("world_boss_participants(boss_id, user_id)"`)
	if start < 0 {
		t.Fatal("world boss participant migration block missing")
	}
	end := strings.Index(text[start:], `idx_book_requests_status_last_action`)
	if end < 0 {
		t.Fatal("world boss participant migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM world_boss_participants",
		"WHERE deleted_at IS NULL",
		"ensureWorldBossParticipantPartialUniqueIndex(DB)",
		"world boss participant unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("world boss participant migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureWorldBossParticipantPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureWorldBossParticipantPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureInventoryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("world boss participant partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_world_boss_participants_boss_user_unique",
		"ON world_boss_participants(boss_id, user_id)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("world boss participant partial index helper missing %q", want)
		}
	}
}

func TestWorldBossEventIDMigrationReplacesFullUniqueIndex(t *testing.T) {
	modelData, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go: %v", err)
	}
	modelText := string(modelData)
	if !strings.Contains(modelText, "BossID string `gorm:\"index;not null\"`") {
		t.Fatal("WorldBossEvent.BossID should use a plain model index; startup migration owns partial uniqueness")
	}
	if strings.Contains(modelText, "BossID string `gorm:\"uniqueIndex;not null\"`") {
		t.Fatal("WorldBossEvent.BossID still declares a full unique index")
	}

	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("world_boss_events(boss_id)"`)
	if start < 0 {
		t.Fatal("world boss event id migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("world_boss_participants(boss_id, user_id)"`)
	if end < 0 {
		t.Fatal("world boss event id migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM world_boss_events",
		"WHERE deleted_at IS NULL",
		"ensureWorldBossEventIDPartialUniqueIndex(DB)",
		"world boss event id unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("world boss event id migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureWorldBossEventIDPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureWorldBossEventIDPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureWorldBossParticipantPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("world boss event id partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_world_boss_events_boss_id",
		"ON world_boss_events(boss_id)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("world boss event id partial index helper missing %q", want)
		}
	}
}

func TestWorldBossJoinUserReadErrorsAreNotTreatedAsUnbound(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleJoinWorldBoss(")
	if start < 0 {
		t.Fatal("handleJoinWorldBoss missing")
	}
	end := strings.Index(text[start:], "func handleWorldBossStatus(")
	if end < 0 {
		t.Fatal("handleJoinWorldBoss boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"世界Boss参加读取本地档案失败",
		"formatPlainError(err)",
		"参加世界Boss读取本地档案失败，请稍后重试",
		`if u.AbsUserID == ""`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("world boss join user read guard missing %q", want)
		}
	}
	if strings.Contains(block, `err != nil || u.AbsUserID == ""`) {
		t.Fatal("world boss join still treats DB read errors as unbound users")
	}
}

func TestWorldBossLiveRefreshUserReadErrorsAreLogged(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func refreshWorldBossLiveDamage(")
	if start < 0 {
		t.Fatal("refreshWorldBossLiveDamage missing")
	}
	end := strings.Index(text[start:], "func settleWorldBoss(")
	if end < 0 {
		t.Fatal("refreshWorldBossLiveDamage boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"世界Boss实时读取本地档案失败",
		"世界Boss实时读取伤害加成失败",
		"世界Boss实时伤害写入失败",
		"世界Boss实时伤害写入未命中",
		"formatPlainError(err)",
		`clearWorldBossParticipantComputedDamage(*p, "user_not_found")`,
		`if u.AbsUserID == ""`,
		`clearWorldBossParticipantComputedDamage(*p, "abs_unbound")`,
		"totalDamage += p.Damage",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("world boss live refresh user read guard missing %q", want)
		}
	}
	if strings.Contains(block, `err != nil || u.AbsUserID == ""`) {
		t.Fatal("world boss live refresh still treats DB read errors as unbound users")
	}
	if strings.Contains(block, "\u923f") || strings.Contains(block, "\u6d93\u682b\u6657") {
		t.Fatal("world boss live refresh diagnostics contain mojibake")
	}
}

func TestWorldBossDiagnosticsUseSanitizedErrors(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainError(err)",
		"formatPlainError(res.Error)",
		"formatPlainError(reason)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("world boss diagnostics missing %q", want)
		}
	}
	rawErrFormat := string([]byte{'e', 'r', 'r', '=', '%', 'v'})
	if strings.Contains(text, rawErrFormat) {
		t.Fatal("world boss diagnostics should not log raw error values")
	}
	for _, bad := range []string{
		"\u923f",
		"\u9252",
		"\u6d93\u682b\u6657",
	} {
		if strings.Contains(text, bad) {
			t.Fatalf("world boss diagnostics contain mojibake marker %q", bad)
		}
	}
}

func TestWorldBossSchedulerQueryFailuresAreLogged(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
	}{
		{
			name:      "settle expired",
			startFunc: "func settleExpiredWorldBosses(",
			endFunc:   "func refreshActiveWorldBosses(",
			markers: []string{
				"查询到期世界Boss失败",
				"已跳过本轮结算扫描",
			},
		},
		{
			name:      "refresh active",
			startFunc: "func refreshActiveWorldBosses(",
			endFunc:   "func refreshWorldBossLiveDamage(",
			markers: []string{
				"查询进行中世界Boss失败",
				"已跳过本轮实时刷新",
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

func TestWorldBossSectRewardUpdatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	for _, marker := range []string{
		"WORLD_BOSS_PARTICIPANT_REWARD_MARK_MISSED",
		"WORLD_BOSS_SECT_MEMBER_REWARD_MISSED",
		"WORLD_BOSS_SECT_PRESTIGE_REWARD_MISSED",
	} {
		idx := strings.Index(text, marker)
		if idx < 0 {
			t.Fatalf("missing world boss sect reward guard: %s", marker)
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

func TestWorldBossSettlementRollbackChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func resetWorldBossToActive(")
	if start < 0 {
		t.Fatal("resetWorldBossToActive missing")
	}
	end := strings.Index(text[start:], "func preciseWorldBossDamage(")
	if end < 0 {
		t.Fatal("resetWorldBossToActive boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := DB.Model(&WorldBossEvent{})",
		"res.Error",
		"res.RowsAffected == 0",
		"world boss settlement rollback missed active reset",
		"formatPlainError(reason)",
		"formatPlainError(res.Error)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("world boss rollback guard missing %q", want)
		}
	}
	if strings.Contains(block, `Update("status", "active").Error`) {
		t.Fatal("world boss rollback still ignores RowsAffected")
	}
}

func TestWorldBossSettlementRewardParticipantsMatchDamageTotal(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func settleWorldBoss(")
	if start < 0 {
		t.Fatal("settleWorldBoss missing")
	}
	end := strings.Index(text[start:], "func clearWorldBossParticipantComputedDamage(")
	if end < 0 {
		t.Fatal("settleWorldBoss reward participant boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"rewardParticipants := make([]WorldBossParticipant, 0, len(participants))",
		`clearWorldBossParticipantComputedDamage(*p, "user_not_found")`,
		`clearWorldBossParticipantComputedDamage(*p, "abs_unbound")`,
		"rewardParticipants = append(rewardParticipants, *p)",
		"grantWorldBossRewards(event, rewardParticipants, killed, currentHP)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("world boss settlement reward participant guard missing %q", want)
		}
	}
	if strings.Contains(block, "grantWorldBossRewards(event, participants, killed, currentHP)") {
		t.Fatal("world boss settlement still rewards the unfiltered participant set")
	}

	start = strings.Index(text, "func clearWorldBossParticipantComputedDamage(")
	if start < 0 {
		t.Fatal("clearWorldBossParticipantComputedDamage missing")
	}
	end = strings.Index(text[start:], "func resetWorldBossToActive(")
	if end < 0 {
		t.Fatal("clearWorldBossParticipantComputedDamage boundary missing")
	}
	block = text[start : start+end]
	for _, want := range []string{
		`Where("id = ? AND boss_id = ? AND user_id = ?", p.ID, p.BossID, p.UserID)`,
		`"final_hours": p.BaseHours`,
		`"damage":      0`,
		"res.Error",
		"res.RowsAffected == 0",
		"WORLD_BOSS_PARTICIPANT_CLEAR_DAMAGE_MISSED",
		"formatPlainValue(reason)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("world boss participant damage clearing guard missing %q", want)
		}
	}
}

func TestWorldBossStaleSettlingRecoveryIsWired(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	text = strings.ReplaceAll(text, "\r\n", "\n")
	start := strings.Index(text, "func recoverStaleWorldBossSettlements(")
	if start < 0 {
		t.Fatal("recoverStaleWorldBossSettlements missing")
	}
	end := strings.Index(text[start:], "func refreshWorldBossLiveDamage(")
	if end < 0 {
		t.Fatal("recoverStaleWorldBossSettlements boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"worldBossSettlementStaleAfter",
		`Where("status = ? AND updated_at < ? AND settled_at IS NULL", "settling", cutoff)`,
		`Update("status", "active")`,
		"formatPlainError(res.Error)",
		"res.RowsAffected > 0",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("world boss stale settling recovery missing %q", want)
		}
	}
	for _, want := range []string{
		"func settleExpiredWorldBosses(bot *tgbotapi.BotAPI, now time.Time) {\n\trecoverStaleWorldBossSettlements(now)",
		"func refreshActiveWorldBosses(bot *tgbotapi.BotAPI, now time.Time) {\n\trecoverStaleWorldBossSettlements(now)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("world boss scanner does not call stale settling recovery: %q", want)
		}
	}
}

func TestWorldBossSettlementReloadFailureIsLogged(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func settleWorldBoss(")
	if start < 0 {
		t.Fatal("settleWorldBoss missing")
	}
	end := strings.Index(text[start:], "func resetWorldBossToActive(")
	if end < 0 {
		t.Fatal("settleWorldBoss boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`event.Status = "settled"`,
		"event.CurrentHP = currentHP",
		"event.IsKilled = killed",
		"event.ParticipantCount = len(participants)",
		`DB.Where("boss_id = ?", bossID).First(&event).Error`,
		"世界Boss结算后事件重读失败",
		"formatPlainError(err)",
		"sendWorldBossSettlement(bot, event, killed, totalDamage)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("world boss settlement reload diagnostics missing %q", want)
		}
	}
	if strings.Contains(block, `if err := DB.Where("boss_id = ?", bossID).First(&event).Error; err == nil {
		ensureWorldBossLiveBoard(bot, event)
	}
	sendWorldBossSettlement`) {
		t.Fatal("world boss settlement reload still silently ignores read errors")
	}
}

func TestWorldBossStoredHPRefreshChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func refreshWorldBossStoredHPByParticipants(")
	if start < 0 {
		t.Fatal("refreshWorldBossStoredHPByParticipants missing")
	}
	end := strings.Index(text[start:], "func settleExpiredWorldBosses(")
	if end < 0 {
		t.Fatal("refreshWorldBossStoredHPByParticipants boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := DB.Model(&WorldBossEvent{})",
		`Where("boss_id = ? AND status = ?", bossID, "active")`,
		"res.Error",
		"res.RowsAffected == 0",
		"WORLD_BOSS_ACTIVE_STATE_CHANGED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("world boss stored HP refresh guard missing %q", want)
		}
	}
	if strings.Contains(block, "}).Error; err != nil") {
		t.Fatal("world boss stored HP refresh still ignores RowsAffected")
	}
}

func TestWorldBossDamageRefreshAndSettlementCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
		forbidden []string
	}{
		{
			name:      "live damage refresh",
			startFunc: "func refreshWorldBossLiveDamage(",
			endFunc:   "func settleWorldBoss(",
			markers: []string{
				"damageRes := DB.Model(&WorldBossParticipant{})",
				`Where("id = ? AND boss_id = ? AND user_id = ?", p.ID, event.BossID, p.UserID)`,
				"damageRes.RowsAffected == 0",
				"eventRes := DB.Model(&WorldBossEvent{})",
				"eventRes.RowsAffected == 0",
				"WORLD_BOSS_ACTIVE_STATE_CHANGED",
			},
			forbidden: []string{
				"DB.Model(p).Updates(",
				"}).Error; err != nil {\n\t\treturn event, totalDamage, err",
			},
		},
		{
			name:      "settlement damage update",
			startFunc: "func settleWorldBoss(",
			endFunc:   "func resetWorldBossToActive(",
			markers: []string{
				"damageRes := DB.Model(&WorldBossParticipant{})",
				`Where("id = ? AND boss_id = ? AND user_id = ?", p.ID, bossID, p.UserID)`,
				"damageRes.RowsAffected == 0",
				"WORLD_BOSS_PARTICIPANT_DAMAGE_UPDATE_MISSED",
			},
			forbidden: []string{
				"DB.Model(p).Updates(",
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
				t.Fatalf("%s rows affected guard missing %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still ignores RowsAffected: %s", tt.name, unsafe)
			}
		}
	}
}

func TestWorldBossDamageBonusReadsReturnErrors(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"func getWorldBossCultivationDamageBonusChecked(",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"func getWorldBossSectDamageBonusChecked(",
		"getSectTechnologyLevelByUserChecked(userID, sectTechBossDamageBonus)",
		"func applyWorldBossDamageBonusesChecked(",
		"cultivationBonus, err := getWorldBossCultivationDamageBonusChecked(userID)",
		"sectBonus, err := getWorldBossSectDamageBonusChecked(userID)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("world boss checked damage bonus guard missing %q", want)
		}
	}

	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
	}{
		{
			name:      "live damage refresh",
			startFunc: "func refreshWorldBossLiveDamage(",
			endFunc:   "func settleWorldBoss(",
			markers: []string{
				"damage, multiplier, err = applyWorldBossDamageBonusesChecked(p.UserID, baseDamage)",
				"totalDamage += p.Damage",
				"continue",
			},
		},
		{
			name:      "settlement damage update",
			startFunc: "func settleWorldBoss(",
			endFunc:   "func resetWorldBossToActive(",
			markers: []string{
				"damage, multiplier, err = applyWorldBossDamageBonusesChecked(p.UserID, baseDamage)",
				"resetWorldBossToActive(bossID, err)",
				"return",
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
				t.Fatalf("%s damage bonus error guard missing %q", tt.name, want)
			}
		}
		if strings.Contains(block, "damage, multiplier = applyWorldBossDamageBonuses(p.UserID, baseDamage)") {
			t.Fatalf("%s still ignores damage bonus read errors", tt.name)
		}
	}
}

func TestWorldBossRankQueriesHandleReadErrors(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"func worldBossTopParticipants(bossID string, limit int) ([]WorldBossParticipant, error)",
		"世界Boss排行暂时读取失败",
		"排行读取失败，请稍后发送 `Boss排行` 查看。",
		"世界Boss实时战榜排行读取失败",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("world boss rank read guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"DB.Where(\"boss_id = ?\", event.BossID).\n\t\tOrder(worldBossRankOrder).\n\t\tLimit(10).\n\t\tFind(&participants)",
		"DB.Where(\"boss_id = ?\", event.BossID).\r\n\t\tOrder(worldBossRankOrder).\r\n\t\tLimit(10).\r\n\t\tFind(&participants)",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("world boss rank query still ignores DB errors")
		}
	}
}

func TestWorldBossLiveBoardMessageIDUpdateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func ensureWorldBossLiveBoardSync(")
	if start < 0 {
		t.Fatal("ensureWorldBossLiveBoardSync missing")
	}
	end := strings.Index(text[start:], "func getActiveWorldBoss(")
	if end < 0 {
		t.Fatal("ensureWorldBossLiveBoardSync boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := DB.Model(&WorldBossEvent{})",
		`Where("boss_id = ?", event.BossID)`,
		"res.Error",
		"formatPlainError(res.Error)",
		"res.RowsAffected == 0",
		"世界Boss实时战榜消息ID记录未命中",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("world boss live board message id guard missing %q", want)
		}
	}
	if strings.Contains(block, "formatPlainError(err))\n\t} else if res.RowsAffected") ||
		strings.Contains(block, "Updates(map[string]interface{}{\n\t\t\t\"board_chat_id\":    sentMsg.Chat.ID,\n\t\t\t\"board_message_id\": sentMsg.MessageID,\n\t\t}).Error; err != nil") {
		t.Fatal("world boss live board message id update still ignores RowsAffected or logs stale err")
	}
}

func TestWorldBossJoinParticipantReloadHandlesReadError(t *testing.T) {
	source, err := os.ReadFile("world_boss.go")
	if err != nil {
		t.Fatalf("read world_boss.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"世界Boss参与记录读取失败",
		"baseHoursText = \"`读取失败`\"",
		"当前基线实际听书：%s",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("world boss join participant reload guard missing %q", want)
		}
	}
	if strings.Contains(text, "DB.Where(\"boss_id = ? AND user_id = ?\", event.BossID, msg.From.ID).First(&p)\n") ||
		strings.Contains(text, "DB.Where(\"boss_id = ? AND user_id = ?\", event.BossID, msg.From.ID).First(&p)\r\n") {
		t.Fatal("world boss join participant reload still ignores DB errors")
	}
}
