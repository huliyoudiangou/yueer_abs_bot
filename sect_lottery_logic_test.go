package main

import (
	"os"
	"strings"
	"testing"
	"time"

	"gorm.io/gorm"
)

func TestNormalizeSectLotteryTitle(t *testing.T) {
	if got, ok := normalizeSectLotteryTitle("  宗门福利  "); !ok || got != "宗门福利" {
		t.Fatalf("normalizeSectLotteryTitle() = %q, %v", got, ok)
	}
	if _, ok := normalizeSectLotteryTitle("短"); ok {
		t.Fatal("one-rune title should be rejected")
	}
	if _, ok := normalizeSectLotteryTitle("宗门\n福利"); ok {
		t.Fatal("title with newline should be rejected")
	}
}

func TestParseSectLotterySecrets(t *testing.T) {
	secrets, err := parseSectLotterySecrets("  alpha  \n\n beta \n")
	if err != nil {
		t.Fatalf("parseSectLotterySecrets() err = %v", err)
	}
	if len(secrets) != 2 || secrets[0] != "alpha" || secrets[1] != "beta" {
		t.Fatalf("parseSectLotterySecrets() = %#v", secrets)
	}

	if _, err := parseSectLotterySecrets("same\nsame"); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("duplicate secrets should be rejected, err=%v", err)
	}
	if _, err := parseSectLotterySecrets(""); err == nil {
		t.Fatal("empty secret list should be rejected")
	}
	if _, err := parseSectLotterySecrets(strings.Repeat("a", sectLotterySecretMaxLen+1)); err == nil {
		t.Fatal("too long secret should be rejected")
	}
}

func TestSectLotterySessionParsersFailClosed(t *testing.T) {
	session := &SessionState{}

	session.SetTemp("sect_lottery_limit", "bad")
	if _, err := parseSectLotterySessionInt(session, "sect_lottery_limit", 1, 100000); err == nil {
		t.Fatal("invalid sect lottery limit should fail closed")
	}

	session.SetTemp("sect_lottery_limit", "0")
	if _, err := parseSectLotterySessionInt(session, "sect_lottery_limit", 1, 100000); err == nil {
		t.Fatal("out-of-range sect lottery limit should fail closed")
	}

	session.SetTemp("sect_lottery_limit", "3")
	if got, err := parseSectLotterySessionInt(session, "sect_lottery_limit", 1, 100000); err != nil || got != 3 {
		t.Fatalf("valid sect lottery limit parse failed: got=%d err=%v", got, err)
	}

	session.SetTemp("sect_lottery_sect_id", "bad")
	if _, err := parseSectLotterySessionInt64(session, "sect_lottery_sect_id"); err == nil {
		t.Fatal("invalid sect lottery sect id should fail closed")
	}

	session.SetTemp("sect_lottery_sect_id", "0")
	if _, err := parseSectLotterySessionInt64(session, "sect_lottery_sect_id"); err == nil {
		t.Fatal("non-positive sect lottery sect id should fail closed")
	}

	now := time.Unix(1000, 0)
	session.SetTemp("sect_lottery_draw_at", "bad")
	if _, err := parseSectLotterySessionUnixTime(session, "sect_lottery_draw_at", now); err == nil {
		t.Fatal("invalid sect lottery draw time should fail closed")
	}

	session.SetTemp("sect_lottery_draw_at", "1000")
	if _, err := parseSectLotterySessionUnixTime(session, "sect_lottery_draw_at", now); err == nil {
		t.Fatal("non-future sect lottery draw time should fail closed")
	}

	session.SetTemp("sect_lottery_draw_at", "1001")
	if got, err := parseSectLotterySessionUnixTime(session, "sect_lottery_draw_at", now); err != nil || !got.Equal(time.Unix(1001, 0)) {
		t.Fatalf("valid sect lottery draw time parse failed: got=%s err=%v", got, err)
	}
}

func TestSectLotteryCreateSessionValuesDoNotIgnoreParseErrors(t *testing.T) {
	source, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createSectLotteryFromSession(")
	if start < 0 {
		t.Fatal("createSectLotteryFromSession missing")
	}
	end := strings.Index(text[start:], "func handleJoinSectLotteryCommand(")
	if end < 0 {
		t.Fatal("createSectLotteryFromSession boundary missing")
	}
	fn := text[start : start+end]
	for _, want := range []string{
		`parseSectLotterySessionInt64(session, "sect_lottery_sect_id")`,
		`parseSectLotterySessionInt(session, "sect_lottery_limit", 1, 100000)`,
		`parseSectLotterySessionUnixTime(session, "sect_lottery_draw_at", time.Now())`,
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("sect lottery session parse guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`sectID, _ := strconv.ParseInt(session.GetTemp("sect_lottery_sect_id"), 10, 64)`,
		`limit, _ := strconv.Atoi(session.GetTemp("sect_lottery_limit"))`,
		`drawUnix, _ := strconv.ParseInt(session.GetTemp("sect_lottery_draw_at"), 10, 64)`,
	} {
		if strings.Contains(fn, unsafe) {
			t.Fatalf("sect lottery session value still ignores parse errors: %q", unsafe)
		}
	}
}

func TestSectLotteryCreatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		signature string
		end       string
		markers   []string
	}{
		{
			name:      "lottery",
			signature: "func createSectLotteryInTx(",
			end:       "func createSectLotteryPrizeInTx(",
			markers: []string{
				"lot.SectName = formatPlainValue(lot.SectName)",
				"lot.CreatorName = formatPlainValue(lot.CreatorName)",
				"lot.Title = formatPlainValue(lot.Title)",
				"res := tx.Create(lot)",
				"res.Error != nil",
				"res.RowsAffected == 0",
				"SECT_LOTTERY_CREATE_MISSED",
			},
		},
		{
			name:      "prize",
			signature: "func createSectLotteryPrizeInTx(",
			end:       "func createSectLotteryEntryInTx(",
			markers: []string{
				"prize.Preview = formatPlainValue(prize.Preview)",
				"prize.Status = formatPlainValue(prize.Status)",
				"res := tx.Create(prize)",
				"res.Error != nil",
				"res.RowsAffected == 0",
				"SECT_LOTTERY_PRIZE_CREATE_MISSED",
			},
		},
		{
			name:      "entry",
			signature: "func createSectLotteryEntryInTx(",
			end:       "func createSectLotteryWinnerInTx(",
			markers: []string{
				"entry.UserName = formatPlainValue(entry.UserName)",
				"res := tx.Create(entry)",
				"res.Error != nil",
				"isUniqueConstraintError(res.Error)",
				"errLotteryAlreadyJoined",
				"res.RowsAffected == 0",
				"SECT_LOTTERY_ENTRY_CREATE_MISSED",
			},
		},
		{
			name:      "winner",
			signature: "func createSectLotteryWinnerInTx(",
			end:       "func HandleSectLotteryCommand(",
			markers: []string{
				"winner.UserName = formatPlainValue(winner.UserName)",
				"winner.Status = formatPlainValue(winner.Status)",
				"res := tx.Create(winner)",
				"res.Error != nil",
				"isUniqueConstraintError(res.Error)",
				"return false, nil",
				"res.RowsAffected == 0",
				"SECT_LOTTERY_WINNER_CREATE_MISSED",
			},
		},
	}
	for _, tt := range tests {
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

	createStart := strings.Index(text, "func createSectLotteryFromSession(")
	if createStart < 0 {
		t.Fatal("createSectLotteryFromSession missing")
	}
	createEnd := strings.Index(text[createStart:], "func handleJoinSectLotteryCommand(")
	if createEnd < 0 {
		t.Fatal("createSectLotteryFromSession boundary missing")
	}
	createBlock := text[createStart : createStart+createEnd]
	for _, want := range []string{
		"createSectLotteryInTx(tx, &lot)",
		"createSectLotteryPrizeInTx(tx, &SectLotteryPrize{",
	} {
		if !strings.Contains(createBlock, want) {
			t.Fatalf("sect lottery create path missing helper %q", want)
		}
	}

	joinStart := strings.Index(text, "func joinSectLottery(")
	if joinStart < 0 {
		t.Fatal("joinSectLottery missing")
	}
	joinEnd := strings.Index(text[joinStart:], "func drawSectLottery(")
	if joinEnd < 0 {
		t.Fatal("joinSectLottery boundary missing")
	}
	joinBlock := text[joinStart : joinStart+joinEnd]
	if !strings.Contains(joinBlock, "createSectLotteryEntryInTx(tx, &entry)") {
		t.Fatal("sect lottery join path should use createSectLotteryEntryInTx")
	}

	drawStart := strings.Index(text, "func drawSectLottery(")
	if drawStart < 0 {
		t.Fatal("drawSectLottery missing")
	}
	drawEnd := strings.Index(text[drawStart:], "func deliverSectLotteryWinners(")
	if drawEnd < 0 {
		t.Fatal("drawSectLottery boundary missing")
	}
	drawBlock := text[drawStart : drawStart+drawEnd]
	for _, want := range []string{
		"created, err := createSectLotteryWinnerInTx(tx, &winner)",
		"if !created",
	} {
		if !strings.Contains(drawBlock, want) {
			t.Fatalf("sect lottery draw path missing helper behavior %q", want)
		}
	}

	for _, unsafe := range []string{
		"tx.Create(&lot).Error",
		"tx.Create(&SectLotteryPrize{",
		"tx.Create(&entry).Error",
		"tx.Create(&winner).Error",
	} {
		if strings.Contains(createBlock, unsafe) || strings.Contains(joinBlock, unsafe) || strings.Contains(drawBlock, unsafe) {
			t.Fatalf("sect lottery create path still ignores RowsAffected: %s", unsafe)
		}
	}
}

func TestSectLotteryMigrationsReplaceFullUniqueIndexes(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func runSectLotteryMigrations()")
	if start < 0 {
		t.Fatal("runSectLotteryMigrations missing")
	}
	end := strings.Index(text[start:], "type duplicateGroup struct")
	if end < 0 {
		t.Fatal("runSectLotteryMigrations boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`runOneTimeMigration("20260618_sect_lottery_indexes"`,
		`runOneTimeMigration("20260623_sect_lottery_partial_unique_indexes"`,
		`assertNoDuplicateGroups("sect_lottery_entries(lottery_id, user_id)"`,
		`assertNoDuplicateGroups("sect_lottery_winners(lottery_id, user_id)"`,
		`assertNoDuplicateGroups("sect_lottery_winners(lottery_id, prize_id)"`,
		"ensureSectLotteryPartialUniqueIndexes(DB)",
		"sect lottery unique index migration failed",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect lottery migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSectLotteryPartialUniqueIndexes(")
	if helperStart < 0 {
		t.Fatal("ensureSectLotteryPartialUniqueIndexes missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureGardenPlotPartialUniqueIndexes(")
	if helperEnd < 0 {
		t.Fatal("sect lottery partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sect_lottery_entries_lottery_user_unique",
		"ON sect_lottery_entries(lottery_id, user_id)",
		"idx_sect_lottery_winners_lottery_user_unique",
		"ON sect_lottery_winners(lottery_id, user_id)",
		"idx_sect_lottery_winners_lottery_prize_unique",
		"ON sect_lottery_winners(lottery_id, prize_id)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect lottery partial index helper missing %q", want)
		}
	}
}

func TestSectLotterySecretEncryptionRoundTrip(t *testing.T) {
	oldConfig := AppConfig
	AppConfig = &Config{SecurityPepper: strings.Repeat("p", 64)}
	t.Cleanup(func() { AppConfig = oldConfig })

	enc, err := encryptSectLotterySecret("card-secret-001")
	if err != nil {
		t.Fatalf("encryptSectLotterySecret() err = %v", err)
	}
	if strings.Contains(enc, "card-secret-001") || !strings.HasPrefix(enc, "gcm$") {
		t.Fatalf("encrypted secret not properly protected: %q", enc)
	}
	plain, err := decryptSectLotterySecret(enc)
	if err != nil {
		t.Fatalf("decryptSectLotterySecret() err = %v", err)
	}
	if plain != "card-secret-001" {
		t.Fatalf("decryptSectLotterySecret() = %q", plain)
	}
}

func TestSectLotteryUserEligibilityRequiresFormalActiveAccount(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	future := now.AddDate(0, 0, 1)
	if !sectLotteryUserEligibleAt(User{Status: "active", AccountType: accountTypeFormal, ExpireAt: &future}, now) {
		t.Fatal("formal active account should be eligible")
	}
	if sectLotteryUserEligibleAt(User{Status: "active", AccountType: accountTypeTrial, ExpireAt: &future}, now) {
		t.Fatal("trial account should be ineligible")
	}
	if sectLotteryUserEligibleAt(User{Status: "active", AccountType: accountTypeFormal, IsSuspended: true, ExpireAt: &future}, now) {
		t.Fatal("suspended account should be ineligible")
	}
	if sectLotteryUserEligibleAt(User{Status: "active", AccountType: accountTypeFormal, ExpireAt: &now}, now) {
		t.Fatal("expired account should be ineligible")
	}
}

func TestSectLotterySchedulerIsStarted(t *testing.T) {
	data, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	if !strings.Contains(string(data), "StartSectLotteryScheduler(bot)") {
		t.Fatal("main.go should start sect lottery scheduler")
	}
}

func TestSectLotteryReminderText(t *testing.T) {
	drawAt := time.Date(2026, 6, 18, 22, 0, 0, 0, time.FixedZone("CST", 8*3600))
	text := sectLotteryReminderText(SectLottery{
		Model:      gorm.Model{ID: 88},
		SectName:   "青云宗",
		Title:      "月度福利",
		Mode:       sectLotteryModeTime,
		DrawAt:     &drawAt,
		PrizeCount: 3,
	})
	for _, want := range []string{"青云宗", "月度福利", "活动ID：88", "历史贡献 >= 100", "参加宗门抽奖 88"} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect lottery reminder missing %q: %s", want, text)
		}
	}
}

func TestSectLotteryCreatorSummaryTextIncludesWinners(t *testing.T) {
	text := sectLotteryCreatorSummaryText(SectLottery{
		Model:      gorm.Model{ID: 9},
		Title:      "月度福利",
		EntryCount: 5,
		PrizeCount: 2,
	}, []SectLotteryWinner{
		{UserID: 1001, UserName: "alice", Status: sectLotteryWinnerDelivered},
		{UserID: 1002, UserName: "bob", Status: sectLotteryWinnerFailed},
	}, 1, 1, 0)
	for _, want := range []string{"宗门抽奖已开奖", "月度福利", "中奖名单", "alice(1001) - 已发送", "bob(1002) - 发送失败", "摘要不展示卡密明文"} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect lottery creator summary missing %q: %s", want, text)
		}
	}
}

func TestSectLotteryNonWinnerNoticeText(t *testing.T) {
	text := sectLotteryNonWinnerNoticeText(SectLottery{
		SectName: "青云宗",
		Title:    "月度福利",
	})
	for _, want := range []string{"宗门福利抽奖已开奖", "青云宗", "月度福利", "未中奖", "后续宗门福泽"} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect lottery non-winner notice missing %q: %s", want, text)
		}
	}
}
func TestSectLotteryNoticeTextsSanitizeDynamicNames(t *testing.T) {
	lot := SectLottery{
		SectName: "青\n云\t宗",
		Title:    "月\n度\t福利",
	}
	for name, text := range map[string]string{
		"reminder":  sectLotteryReminderText(SectLottery{Model: gorm.Model{ID: 88}, SectName: lot.SectName, Title: lot.Title, PrizeCount: 1, TargetEntryCount: 2}),
		"nonWinner": sectLotteryNonWinnerNoticeText(lot),
		"summary":   sectLotteryCreatorSummaryText(SectLottery{Title: lot.Title}, nil, 0, 0, 0),
	} {
		if strings.Contains(text, "青\n云") || strings.Contains(text, "月\n度") ||
			strings.Contains(text, "青\t云") || strings.Contains(text, "度\t福利") {
			t.Fatalf("%s text should sanitize dynamic names: %q", name, text)
		}
		if strings.Contains(text, "青 云 宗") && name == "summary" {
			t.Fatalf("%s should not contain sect name: %q", name, text)
		}
	}

	data, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go: %v", err)
	}
	if got := strings.Count(string(data), `lotteryDisplayText(lot.SectName, 80, "-"), lotteryDisplayText(lot.Title, 80, "-")`); got < 3 {
		t.Fatalf("sect lottery private notices using sanitized sect/title = %d, want at least 3", got)
	}
	if strings.Contains(string(data), `lot.ID, lot.Title, sectLotteryStatusText(lot.Status)`) {
		t.Fatal("sect lottery list/detail should not render raw lottery title")
	}
	if got := strings.Count(string(data), `lot.ID, lotteryDisplayText(lot.Title, 80, "-"), sectLotteryStatusText(lot.Status)`); got < 2 {
		t.Fatalf("sect lottery list/detail sanitized title count = %d, want at least 2", got)
	}
}

func TestSectLotteryDeliveryFailureWriteErrorsNotIgnored(t *testing.T) {
	data, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go: %v", err)
	}
	text := string(data)
	signature := "func markSectLotteryDeliveryFailed(winner SectLotteryWinner, reason string) error"
	start := strings.Index(text, signature)
	if start < 0 {
		t.Fatalf("missing error-returning delivery failure marker: %s", signature)
	}
	end := strings.Index(text[start:], "func showSectLotteryList")
	if end < 0 {
		t.Fatal("missing showSectLotteryList after delivery failure marker")
	}
	body := text[start : start+end]
	if strings.Contains(body, "_ = DB.Transaction") {
		t.Fatalf("delivery failure marker still ignores database errors: %s", body)
	}
	if !strings.Contains(body, "reason = formatPlainValue(reason)") {
		t.Fatal("delivery failure reason should be sanitized before persistence")
	}
	if strings.Contains(body, "reason = truncateRunes(reason, 500)") {
		t.Fatal("delivery failure reason should not only be truncated without diagnostic sanitization")
	}
	if !strings.Contains(text, "if markErr := markSectLotteryDeliveryFailed") {
		t.Fatal("delivery failure marker errors should be logged by callers")
	}
}

func TestSectLotteryReminderFailureReasonUsesPlainValue(t *testing.T) {
	data, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go: %v", err)
	}
	text := string(data)
	signature := "func markSectLotteryReminder(lot SectLottery, member SectMember, status string, reason string) error"
	start := strings.Index(text, signature)
	if start < 0 {
		t.Fatalf("missing reminder marker: %s", signature)
	}
	end := strings.Index(text[start:], "func sectLotteryReminderText(")
	if end < 0 {
		t.Fatal("missing sectLotteryReminderText after reminder marker")
	}
	body := text[start : start+end]
	if !strings.Contains(body, "reason = formatPlainValue(reason)") {
		t.Fatal("reminder failure reason should be sanitized before persistence")
	}
	if strings.Contains(body, "reason = truncateRunes(reason, 500)") {
		t.Fatal("reminder failure reason should not only be truncated without diagnostic sanitization")
	}
	for _, want := range []string{
		"LastError: reason",
		"return upsertSectLotteryReminderRecord(DB, &reminder)",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("reminder failure persistence missing %q", want)
		}
	}
	helperStart := strings.Index(text, "func upsertSectLotteryReminderRecord(")
	if helperStart < 0 {
		t.Fatal("upsertSectLotteryReminderRecord missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func createSectLotteryInTx(")
	if helperEnd < 0 {
		t.Fatal("upsertSectLotteryReminderRecord boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	if !strings.Contains(helperBlock, `"last_error": entry.LastError`) {
		t.Fatal("reminder helper should persist sanitized last_error")
	}
}

func TestSectLotteryReminderUpsertChecksRowsAffected(t *testing.T) {
	data, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go: %v", err)
	}
	text := string(data)
	helperStart := strings.Index(text, "func upsertSectLotteryReminderRecord(")
	if helperStart < 0 {
		t.Fatal("upsertSectLotteryReminderRecord missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func createSectLotteryInTx(")
	if helperEnd < 0 {
		t.Fatal("upsertSectLotteryReminderRecord boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"entry := *reminder",
		"entry.UserName = formatPlainValue(entry.UserName)",
		"entry.Status = formatPlainValue(entry.Status)",
		"entry.LastError = formatPlainValue(entry.LastError)",
		"res := db.Clauses(clause.OnConflict{",
		`Columns: []clause.Column{{Name: "lottery_id"}, {Name: "user_id"}}`,
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SECT_LOTTERY_REMINDER_UPSERT_MISSED",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect lottery reminder upsert guard missing %q", want)
		}
	}

	markerStart := strings.Index(text, "func markSectLotteryReminder(")
	if markerStart < 0 {
		t.Fatal("markSectLotteryReminder missing")
	}
	markerEnd := strings.Index(text[markerStart:], "func sectLotteryReminderText(")
	if markerEnd < 0 {
		t.Fatal("markSectLotteryReminder boundary missing")
	}
	markerBlock := text[markerStart : markerStart+markerEnd]
	if !strings.Contains(markerBlock, "return upsertSectLotteryReminderRecord(DB, &reminder)") {
		t.Fatal("markSectLotteryReminder should use upsertSectLotteryReminderRecord")
	}
	if strings.Contains(markerBlock, "}).Create(&reminder).Error") {
		t.Fatal("markSectLotteryReminder still upserts reminder without checking RowsAffected")
	}
}

func TestSectLotteryReminderDeliveryFailsWhenStateWriteFails(t *testing.T) {
	data, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go: %v", err)
	}
	text := string(data)

	deliverStart := strings.Index(text, "func deliverSectLotteryReminder(")
	if deliverStart < 0 {
		t.Fatal("deliverSectLotteryReminder missing")
	}
	deliverEnd := strings.Index(text[deliverStart:], "func markSectLotteryReminder(")
	if deliverEnd < 0 {
		t.Fatal("deliverSectLotteryReminder boundary missing")
	}
	deliverBlock := text[deliverStart : deliverStart+deliverEnd]
	for _, want := range []string{
		"if markErr := markSectLotteryReminder(lot, member, sectLotteryReminderFailed, formatTelegramSendError(err)); markErr != nil",
		"formatPlainError(markErr)",
		"if err := markSectLotteryReminder(lot, member, sectLotteryReminderDelivered, \"\"); err != nil",
		"后续可能重复补发",
		"return false",
	} {
		if !strings.Contains(deliverBlock, want) {
			t.Fatalf("reminder delivery should surface state write failure, missing %q", want)
		}
	}

	markerStart := strings.Index(text, "func markSectLotteryReminder(lot SectLottery, member SectMember, status string, reason string) error")
	if markerStart < 0 {
		t.Fatal("markSectLotteryReminder should return error")
	}
	markerEnd := strings.Index(text[markerStart:], "func sectLotteryReminderText(")
	if markerEnd < 0 {
		t.Fatal("markSectLotteryReminder boundary missing")
	}
	markerBlock := text[markerStart : markerStart+markerEnd]
	if !strings.Contains(markerBlock, "return upsertSectLotteryReminderRecord(DB, &reminder)") {
		t.Fatal("markSectLotteryReminder should return reminder upsert error")
	}
}

func TestSectLotteryReminderDedupeReadFailureSkipsDelivery(t *testing.T) {
	data, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go: %v", err)
	}
	text := string(data)

	remindStart := strings.Index(text, "func remindSectLotteryMembers(")
	if remindStart < 0 {
		t.Fatal("remindSectLotteryMembers missing")
	}
	remindEnd := strings.Index(text[remindStart:], "func sectLotteryReminderAlreadyDelivered(")
	if remindEnd < 0 {
		t.Fatal("remindSectLotteryMembers boundary missing")
	}
	remindBlock := text[remindStart : remindStart+remindEnd]
	for _, want := range []string{
		"alreadyDelivered, err := sectLotteryReminderAlreadyDelivered(lotteryID, member.UserID)",
		"if err != nil",
		"failed++",
		"continue",
		"if alreadyDelivered",
	} {
		if !strings.Contains(remindBlock, want) {
			t.Fatalf("reminder delivery loop should skip on dedupe read failure, missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func sectLotteryReminderAlreadyDelivered(lotteryID uint, userID int64) (bool, error)")
	if helperStart < 0 {
		t.Fatal("sectLotteryReminderAlreadyDelivered should return (bool, error)")
	}
	helperEnd := strings.Index(text[helperStart:], "func deliverSectLotteryReminder(")
	if helperEnd < 0 {
		t.Fatal("sectLotteryReminderAlreadyDelivered boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"return false, err",
		"return count > 0, nil",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("reminder dedupe helper error handling missing %q", want)
		}
	}
	if strings.Contains(helperBlock, "return false\n") {
		t.Fatal("reminder dedupe helper must not treat query failure as not delivered")
	}
}

func TestSectLotteryDeliveryStatusUpdatesCheckRowsAffected(t *testing.T) {
	data, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go: %v", err)
	}
	text := string(data)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
	}{
		{
			name:      "delivery success",
			startFunc: "func deliverSectLotteryWinner(",
			endFunc:   "func notifySectLotteryNonWinners(",
			markers: []string{
				"宗门抽奖发奖读取活动失败",
				"宗门抽奖发奖读取奖品失败",
				"formatPlainError(err)",
				"SECT_LOTTERY_WINNER_DELIVERY_STATE_CHANGED",
				"SECT_LOTTERY_PRIZE_DELIVERY_STATE_CHANGED",
			},
		},
		{
			name:      "delivery failure",
			startFunc: "func markSectLotteryDeliveryFailed(",
			endFunc:   "func showSectLotteryList(",
			markers: []string{
				"SECT_LOTTERY_WINNER_FAILURE_STATE_CHANGED",
				"SECT_LOTTERY_PRIZE_FAILURE_STATE_CHANGED",
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
			t.Fatalf("%s end missing", tt.name)
		}
		block := text[start : start+end]
		if !strings.Contains(block, "RowsAffected == 0") {
			t.Fatalf("%s should check RowsAffected: %s", tt.name, block)
		}
		for _, marker := range tt.markers {
			if !strings.Contains(block, marker) {
				t.Fatalf("%s missing marker %s", tt.name, marker)
			}
		}
	}
}

func TestSectLotteryJoinAndDrawStateUpdatesCheckRowsAffected(t *testing.T) {
	data, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go: %v", err)
	}
	text := string(data)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
	}{
		{
			name:      "join entry count",
			startFunc: "func joinSectLottery(",
			endFunc:   "func drawSectLottery(",
			markers: []string{
				`Where("id = ? AND status = ?", lotteryID, sectLotteryStatusActive)`,
				"SECT_LOTTERY_ENTRY_COUNT_STATE_CHANGED",
			},
		},
		{
			name:      "draw final states",
			startFunc: "func drawSectLottery(",
			endFunc:   "func deliverSectLotteryWinners(",
			markers: []string{
				`Where("id = ? AND status = ?", lotteryID, sectLotteryStatusDrawing)`,
				"SECT_LOTTERY_DRAW_STATE_CHANGED",
				"SECT_LOTTERY_FINAL_STATE_CHANGED",
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
			t.Fatalf("%s end missing", tt.name)
		}
		block := text[start : start+end]
		if !strings.Contains(block, "RowsAffected == 0") {
			t.Fatalf("%s should check RowsAffected: %s", tt.name, block)
		}
		for _, marker := range tt.markers {
			if !strings.Contains(block, marker) {
				t.Fatalf("%s missing marker %s", tt.name, marker)
			}
		}
	}
}

func TestSectLotteryJoinTransactionalReturnValuesOnlyAfterSuccess(t *testing.T) {
	data, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func joinSectLottery(")
	if start < 0 {
		t.Fatal("joinSectLottery missing")
	}
	end := strings.Index(text[start:], "func drawSectLottery(")
	if end < 0 {
		t.Fatal("joinSectLottery boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"txJoined := 0",
		"txTarget := 0",
		"txShouldDraw := false",
		"txJoined = int(count)",
		"txTarget = lot.TargetEntryCount",
		`Update("entry_count", txJoined)`,
		"txShouldDraw = lot.Mode == sectLotteryModeCount && lot.TargetEntryCount > 0 && txJoined >= lot.TargetEntryCount",
		"joined = txJoined",
		"target = txTarget",
		"shouldDraw = txShouldDraw",
		"if err != nil {\n\t\treturn 0, 0, false, err\n\t}",
		"return joined, target, shouldDraw, nil",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect lottery join transactional return guard missing %q", want)
		}
	}
	if strings.Contains(block, "return joined, target, shouldDraw, err") {
		t.Fatal("sect lottery join still returns possibly rolled-back count/draw flag")
	}
}

func TestSectLotteryEligibilityReadErrorsFailClosed(t *testing.T) {
	data, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go: %v", err)
	}
	text := string(data)

	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
		forbidden []string
	}{
		{
			name:      "join",
			startFunc: "func joinSectLottery(",
			endFunc:   "func drawSectLottery(",
			markers: []string{
				"if !errors.Is(err, gorm.ErrRecordNotFound) {\n\t\t\t\treturn err\n\t\t\t}",
				"return errLotteryNotActive",
				"return errNotInSect",
			},
			forbidden: []string{
				`if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", lotteryID, sectLotteryStatusActive).First(&lot).Error; err != nil {
			return errLotteryNotActive
		}`,
				`if err := tx.Where("sect_id = ? AND user_id = ?", lot.SectID, tgUser.ID).First(&member).Error; err != nil {
			return errNotInSect
		}`,
			},
		},
		{
			name:      "draw",
			startFunc: "func drawSectLottery(",
			endFunc:   "func deliverSectLotteryWinners(",
			markers: []string{
				"stillEligible, eligibilityErr := sectLotteryEntryStillEligibleTx(tx, lot.SectID, entry.UserID)",
				"if eligibilityErr != nil {\n\t\t\t\treturn eligibilityErr\n\t\t\t}",
			},
			forbidden: []string{
				"if !sectLotteryEntryStillEligibleTx(tx, lot.SectID, entry.UserID) {",
			},
		},
		{
			name:      "operator load",
			startFunc: "func loadSectLotteryForOperator(",
			endFunc:   "func notifySectLotteryCreatorSummary(",
			markers: []string{
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"宗门抽奖详情读取失败",
				"宗门抽奖读取失败，请稍后重试。",
			},
			forbidden: []string{
				`if err := DB.Where("id = ? AND sect_id = ?", uint(id), member.SectID).First(&lot).Error; err != nil {
		sendPlainText(bot, msg.Chat.ID, "❌ 宗门抽奖不存在。")
		return zero, false
	}`,
			},
		},
		{
			name:      "creator context",
			startFunc: "func sectLotteryCreatorContext(",
			endFunc:   "func validateSectLotteryUserEligibleTx(",
			markers: []string{
				"if !errors.Is(err, gorm.ErrRecordNotFound) {\n\t\t\treturn member, Sect{}, err\n\t\t}",
				"return member, Sect{}, errNotInSect",
			},
			forbidden: []string{
				`if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		return member, Sect{}, errNotInSect
	}`,
			},
		},
		{
			name:      "user eligibility",
			startFunc: "func validateSectLotteryUserEligibleTx(",
			endFunc:   "func sectLotteryUserEligibleAt(",
			markers: []string{
				"if !errors.Is(err, gorm.ErrRecordNotFound) {\n\t\t\treturn err\n\t\t}",
				"return errUserNotFound",
			},
			forbidden: []string{
				`if err := tx.Where("telegram_id = ?", member.UserID).First(&u).Error; err != nil {
		return errUserNotFound
	}`,
			},
		},
		{
			name:      "entry still eligible",
			startFunc: "func sectLotteryEntryStillEligibleTx(",
			endFunc:   "func normalizeSectLotteryTitle(",
			markers: []string{
				"func sectLotteryEntryStillEligibleTx(tx *gorm.DB, sectID int64, userID int64) (bool, error)",
				"return false, err",
				"return false, nil",
				"errors.Is(err, errUserNotFound) || errors.Is(err, errSectLotteryUserIneligible)",
				"return true, nil",
			},
			forbidden: []string{
				"func sectLotteryEntryStillEligibleTx(tx *gorm.DB, sectID int64, userID int64) bool",
				"return validateSectLotteryUserEligibleTx(tx, member) == nil",
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
				t.Fatalf("%s fail-closed read guard missing %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still swallows database read errors", tt.name)
			}
		}
	}
}

func TestSectLotteryAuditDetailsUsePlainValue(t *testing.T) {
	data, err := os.ReadFile("sect_lottery.go")
	if err != nil {
		t.Fatalf("read sect_lottery.go: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`formatPlainValue(title), formatPlainValue(mode)`,
		`formatPlainValue(lot.Title)`,
		`formatPlainValue(reason), len(entries), len(prizes)`,
		`formatPlainValue(reason), len(entries), len(prizes), len(winners), unassigned`,
		`len(entries), len(prizes), formatPlainValue(reason)`,
		`len(entries), len(prizes), len(winners), formatPlainValue(reason)`,
		`"CREATE_SECT_LOTTERY"`,
		`"DRAW_SECT_LOTTERY"`,
		`"CANCEL_SECT_LOTTERY"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect lottery audit detail missing sanitized dynamic field pattern %q", want)
		}
	}
	for _, unsafe := range []string{
		`sect.ID, title, mode, len(secrets)`,
		`lot.SectID, lot.Title`,
		`reason, len(entries), len(prizes)`,
		`reason, len(entries), len(prizes), len(winners), unassigned`,
		`len(entries), len(prizes), reason`,
		`len(entries), len(prizes), len(winners), reason`,
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("sect lottery audit detail still contains raw dynamic fields: %q", unsafe)
		}
	}
}
