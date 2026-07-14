package main

import (
	"os"
	"strings"
	"testing"
)

func TestSectHornDiagnosticsUseSanitizedErrors(t *testing.T) {
	source, err := os.ReadFile("sect_horn.go")
	if err != nil {
		t.Fatalf("read sect_horn.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainError(err)",
		"formatPlainError(res.Error)",
		"formatPlainError(updateErr)",
		"formatTelegramSendError(err)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect horn diagnostics missing %q", want)
		}
	}
	rawErrFormat := string([]byte{'e', 'r', 'r', '=', '%', 'v'})
	if strings.Contains(text, rawErrFormat) {
		t.Fatal("sect horn diagnostics should not log raw error values")
	}
}

func TestSectHornDeliveryStatusUpdatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect_horn.go")
	if err != nil {
		t.Fatalf("read sect_horn.go err = %v", err)
	}
	text := string(source)
	for _, marker := range []string{
		"SECT_HORN_DELIVERY_FAILED_STATE_CHANGED",
		"SECT_HORN_DELIVERY_SENT_STATE_CHANGED",
	} {
		idx := strings.Index(text, marker)
		if idx < 0 {
			t.Fatalf("missing delivery status state-change marker %s", marker)
		}
		start := idx - 220
		if start < 0 {
			start = 0
		}
		block := text[start:minInt(len(text), idx+len(marker)+80)]
		for _, want := range []string{
			"updateRes.Error == nil",
			"updateRes.RowsAffected == 0",
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("delivery status guard %s missing %q: %s", marker, want, block)
			}
		}
	}
}

func TestSectHornBroadcastStatusUpdatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect_horn.go")
	if err != nil {
		t.Fatalf("read sect_horn.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		marker  string
		resName string
		where   string
	}{
		{
			marker:  "SECT_HORN_BROADCAST_COMPLETE_STATE_CHANGED",
			resName: "completeRes",
			where:   `Where("horn_id = ? AND status = ?", hornID, sectHornStatusSending)`,
		},
		{
			marker:  "SECT_HORN_BROADCAST_FAILED_STATE_CHANGED",
			resName: "failRes",
			where:   `Where("horn_id = ? AND status <> ?", hornID, sectHornStatusCompleted)`,
		},
	}
	for _, tt := range tests {
		idx := strings.Index(text, tt.marker)
		if idx < 0 {
			t.Fatalf("missing broadcast status state-change marker %s", tt.marker)
		}
		start := idx - 520
		if start < 0 {
			start = 0
		}
		block := text[start:minInt(len(text), idx+len(tt.marker)+80)]
		for _, want := range []string{
			tt.resName + ".Error != nil",
			tt.resName + ".RowsAffected == 0",
			tt.where,
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("broadcast status guard %s missing %q: %s", tt.marker, want, block)
			}
		}
	}
}

func TestSectHornCompletionReceiptReadFailureLogsError(t *testing.T) {
	source, err := os.ReadFile("sect_horn.go")
	if err != nil {
		t.Fatalf("read sect_horn.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func completeSectHornBroadcast(")
	if start < 0 {
		t.Fatal("completeSectHornBroadcast missing")
	}
	end := strings.Index(text[start:], "func markSectHornBroadcastFailed(")
	if end < 0 {
		t.Fatal("completeSectHornBroadcast boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"喇叭完成回执读取广播失败",
		"formatPlainValue(hornID)",
		"formatPlainError(err)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect horn completion receipt read failure should log sanitized diagnostics, missing %q", want)
		}
	}
	if strings.Contains(block, "if err := DB.Where(\"horn_id = ?\", hornID).First(&broadcast).Error; err != nil {\n\t\treturn\n\t}") {
		t.Fatal("sect horn completion receipt read failure still returns silently")
	}
}

func TestSectHornBroadcastFailureReasonUsesPlainValue(t *testing.T) {
	source, err := os.ReadFile("sect_horn.go")
	if err != nil {
		t.Fatalf("read sect_horn.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func markSectHornBroadcastFailed(")
	if start < 0 {
		t.Fatal("markSectHornBroadcastFailed missing")
	}
	end := strings.Index(text[start:], "func createSectHornBroadcast(")
	if end < 0 {
		t.Fatal("markSectHornBroadcastFailed boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"reason = formatPlainValue(reason)",
		`"last_error":   reason`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect horn broadcast failure reason guard missing %q", want)
		}
	}
	if strings.Contains(block, `truncateRunes(reason, 500)`) {
		t.Fatal("sect horn broadcast failure reason should not only be truncated without diagnostic sanitization")
	}
}

func TestSectHornCreateRecordsCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect_horn.go")
	if err != nil {
		t.Fatalf("read sect_horn.go err = %v", err)
	}
	text := string(source)
	helperTests := []struct {
		name      string
		signature string
		end       string
		markers   []string
	}{
		{
			name:      "broadcast",
			signature: "func createSectHornBroadcastInTx(",
			end:       "func createSectHornDeliveriesInTx(",
			markers: []string{
				"broadcast.HornID = formatPlainValue(broadcast.HornID)",
				"broadcast.SectName = formatPlainValue(broadcast.SectName)",
				"broadcast.SenderName = formatPlainValue(broadcast.SenderName)",
				"res := tx.Create(broadcast)",
				"res.Error != nil",
				"res.RowsAffected == 0",
				"SECT_HORN_BROADCAST_CREATE_MISSED",
			},
		},
		{
			name:      "deliveries",
			signature: "func createSectHornDeliveriesInTx(",
			end:       "type sectHornRecipient struct",
			markers: []string{
				"deliveries[i].HornID = formatPlainValue(deliveries[i].HornID)",
				"deliveries[i].UserName = formatPlainValue(deliveries[i].UserName)",
				"res := tx.CreateInBatches(&deliveries, 500)",
				"res.Error != nil",
				"res.RowsAffected != int64(len(deliveries))",
				"SECT_HORN_DELIVERIES_CREATE_MISSED",
			},
		},
	}
	for _, tt := range helperTests {
		start := strings.Index(text, tt.signature)
		if start < 0 {
			t.Fatalf("%s helper missing", tt.name)
		}
		end := strings.Index(text[start:], tt.end)
		if end < 0 {
			t.Fatalf("%s helper boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range tt.markers {
			if !strings.Contains(block, want) {
				t.Fatalf("%s helper guard missing %q", tt.name, want)
			}
		}
	}

	start := strings.Index(text, "func createSectHornBroadcast(")
	if start < 0 {
		t.Fatal("createSectHornBroadcast missing")
	}
	end := strings.Index(text[start:], "var (")
	if end < 0 {
		t.Fatal("createSectHornBroadcast boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"createSectHornBroadcastInTx(tx, &broadcast)",
		"createSectHornDeliveriesInTx(tx, deliveries)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect horn create path missing helper %q", want)
		}
	}
	for _, unsafe := range []string{
		"tx.Create(&broadcast).Error",
		"tx.CreateInBatches(&deliveries, 500).Error",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("sect horn create path still ignores RowsAffected: %s", unsafe)
		}
	}
}

func TestSectHornBroadcastIDMigrationReplacesFullUniqueIndex(t *testing.T) {
	modelData, err := os.ReadFile("sect_horn.go")
	if err != nil {
		t.Fatalf("read sect_horn.go: %v", err)
	}
	modelText := string(modelData)
	if !strings.Contains(modelText, "HornID string `gorm:\"index;not null\"`") {
		t.Fatal("SectHornBroadcast.HornID should use a plain model index; startup migration owns partial uniqueness")
	}
	if strings.Contains(modelText, "HornID string `gorm:\"uniqueIndex;not null\"`") {
		t.Fatal("SectHornBroadcast.HornID still declares a full unique index")
	}

	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("sect_horn_broadcasts(horn_id)"`)
	if start < 0 {
		t.Fatal("sect horn broadcast migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_horn_deliveries(horn_id, user_id)"`)
	if end < 0 {
		t.Fatal("sect horn broadcast migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM sect_horn_broadcasts",
		"WHERE deleted_at IS NULL",
		"ensureSectHornBroadcastIDPartialUniqueIndex(DB)",
		"sect horn broadcast id unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect horn broadcast migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSectHornBroadcastIDPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSectHornBroadcastIDPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureSectHornDeliveryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sect horn broadcast partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sect_horn_broadcasts_horn_id",
		"ON sect_horn_broadcasts(horn_id)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect horn broadcast partial index helper missing %q", want)
		}
	}
}

func TestSectHornDeliveryMigrationReplacesFullUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("sect_horn_deliveries(horn_id, user_id)"`)
	if start < 0 {
		t.Fatal("sect horn delivery migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sign_in_logs(user_id, sign_date)"`)
	if end < 0 {
		t.Fatal("sect horn delivery migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM sect_horn_deliveries",
		"WHERE deleted_at IS NULL",
		"ensureSectHornDeliveryPartialUniqueIndex(DB)",
		"sect horn delivery unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect horn delivery migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSectHornDeliveryPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSectHornDeliveryPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureWorldBossParticipantPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sect horn delivery partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sect_horn_deliveries_horn_user_unique",
		"ON sect_horn_deliveries(horn_id, user_id)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect horn delivery partial index helper missing %q", want)
		}
	}
}

func TestSectHornMemberReadsDistinguishNotInSectFromDBErrors(t *testing.T) {
	source, err := os.ReadFile("sect_horn.go")
	if err != nil {
		t.Fatalf("read sect_horn.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
		forbidden string
	}{
		{
			name:      "create",
			startFunc: "func createSectHornBroadcast(",
			endFunc:   "var (",
			markers: []string{
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"return errNotInSect",
				"return err",
			},
			forbidden: `if err := tx.Where("user_id = ?", tgUser.ID).First(&member).Error; err != nil {
			return errNotInSect
		}`,
		},
		{
			name:      "preview",
			startFunc: "func buildSectHornPreview(",
			endFunc:   "func checkSectHornCooldownTx(",
			markers: []string{
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"return sectHornPreview{}, errNotInSect",
				"return sectHornPreview{}, err",
			},
			forbidden: `if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		return sectHornPreview{}, errNotInSect
	}`,
		},
	}
	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s path missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endFunc)
		if end < 0 {
			t.Fatalf("%s path boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range tt.markers {
			if !strings.Contains(block, want) {
				t.Fatalf("%s member read guard missing %q", tt.name, want)
			}
		}
		if strings.Contains(block, tt.forbidden) {
			t.Fatalf("%s path still maps all member read errors to not-in-sect", tt.name)
		}
	}
}

func TestSectHornCreateReturnValueOnlyAfterTransactionSuccess(t *testing.T) {
	source, err := os.ReadFile("sect_horn.go")
	if err != nil {
		t.Fatalf("read sect_horn.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createSectHornBroadcast(")
	if start < 0 {
		t.Fatal("createSectHornBroadcast missing")
	}
	end := strings.Index(text[start:], "var (")
	if end < 0 {
		t.Fatal("createSectHornBroadcast boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"txResult := sectHornCreateResult{",
		"result = txResult",
		"return sectHornCreateResult{}, err",
		"return result, nil",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect horn create return guard missing %q", want)
		}
	}
	if strings.Contains(block, "return result, err") {
		t.Fatal("sect horn create still returns transactional intermediate result on error")
	}
}

func TestSectHornPointDescriptionSectNameSanitizesText(t *testing.T) {
	got := sectHornPointDescriptionSectName("  alpha\nbeta\tgamma  ")
	if got != "alpha beta gamma" {
		t.Fatalf("sectHornPointDescriptionSectName() = %q", got)
	}
	if got := sectHornPointDescriptionSectName("\n\t"); got != "-" {
		t.Fatalf("empty sect horn description name fallback = %q", got)
	}

	source, err := os.ReadFile("sect_horn.go")
	if err != nil {
		t.Fatalf("read sect_horn.go err = %v", err)
	}
	text := string(source)
	if strings.Contains(text, `fmt.Sprintf("在宗门【%s】使用宗门喇叭，消耗 %d 积分", sect.Name, cost)`) ||
		strings.Contains(text, `fmt.Sprintf("代表宗门【%s】使用世界喇叭，消耗 %d 积分", sect.Name, cost)`) {
		t.Fatal("sect horn point transaction descriptions should sanitize sect name")
	}
	if got := strings.Count(text, "sectHornPointDescriptionSectName(sect.Name)"); got < 2 {
		t.Fatalf("sect horn sanitized sect name use count = %d, want at least 2", got)
	}
}

// TestSectHornConfirmNotRoutedAsEmptyContent 防止回归：
// 「确认宗门喇叭 / 确认世界喇叭」绝不能在 HandleSectCommand 中以空正文重启喇叭流程，
// 否则会抢在会话分支 WAITING_CONFIRM_SECT_HORN 之前执行，导致「正文不能为空」误报。
// 确认文案必须落到 state_machine 的会话分支，由 handleSectHornSession 读取暂存正文。
func TestSectHornConfirmNotRoutedAsEmptyContent(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	for _, bad := range []string{
		`handleSectHornStart(bot, msg, sectHornScopeSect, "")`,
		`handleSectHornStart(bot, msg, sectHornScopeWorld, "")`,
		`case text == "确认宗门喇叭":`,
		`case text == "确认世界喇叭":`,
	} {
		if strings.Contains(text, bad) {
			t.Fatalf("HandleSectCommand should not route horn confirmation as empty content; found %q", bad)
		}
	}

	machine, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	if !strings.Contains(string(machine), `case "WAITING_CONFIRM_SECT_HORN":`) {
		t.Fatal("state_machine.go missing WAITING_CONFIRM_SECT_HORN session branch for horn confirmation")
	}
}
