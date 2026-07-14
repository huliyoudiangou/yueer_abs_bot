package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestSectShopRenewMonthlyLimitByLevel(t *testing.T) {
	tests := []struct {
		level int
		want  int
	}{
		{level: -1, want: 2},
		{level: 1, want: 2},
		{level: 2, want: 3},
		{level: 3, want: 5},
		{level: 4, want: 7},
		{level: 5, want: 10},
		{level: 6, want: 13},
		{level: 7, want: 16},
		{level: 8, want: 20},
		{level: 9, want: 24},
		{level: 10, want: 30},
		{level: 99, want: 30},
	}

	for _, tt := range tests {
		if got := sectShopRenewMonthlyLimit(tt.level); got != tt.want {
			t.Fatalf("sectShopRenewMonthlyLimit(%d) = %d, want %d", tt.level, got, tt.want)
		}
	}
}

func TestSectShopRenewExpireLimit(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))
	expired := now.AddDate(0, 0, -1)
	remaining38 := now.AddDate(0, 0, 38)
	remaining39 := now.AddDate(0, 0, 39)

	if got := sectShopRenewNextExpireAt(&expired, now); !got.Equal(now.AddDate(0, 0, sectShopRenewDays)) {
		t.Fatalf("expired account renews from now, got %s", got)
	}
	if !sectShopRenewAllowedByExpireLimit(&remaining38, now) {
		t.Fatalf("remaining 38 days should allow a 7-day renew under 45-day cap")
	}
	if sectShopRenewAllowedByExpireLimit(&remaining39, now) {
		t.Fatalf("remaining 39 days should exceed the 45-day cap after 7-day renew")
	}
}

func TestSectShopRenewJoinedLongEnough(t *testing.T) {
	now := time.Date(2026, 6, 17, 12, 0, 0, 0, time.FixedZone("CST", 8*3600))

	if sectShopRenewJoinedLongEnough(now.AddDate(0, 0, -6), now) {
		t.Fatalf("joined for 6 days should not be eligible")
	}
	if !sectShopRenewJoinedLongEnough(now.AddDate(0, 0, -7), now) {
		t.Fatalf("joined for 7 days should be eligible")
	}
}

func TestSectShopMonthKeyUsesBeijingTime(t *testing.T) {
	utc := time.FixedZone("UTC", 0)
	if got := sectShopMonthKey(time.Date(2026, 5, 31, 16, 30, 0, 0, utc)); got != "202606" {
		t.Fatalf("sectShopMonthKey() = %s, want 202606", got)
	}
}

func TestSectShopRenewClaimCountDiagnosticsAreReadable(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func countSectShopRenewClaims(")
	if start < 0 {
		t.Fatal("countSectShopRenewClaims missing")
	}
	end := strings.Index(text[start:], "func countSectShopRenewClaimsTx(")
	if end < 0 {
		t.Fatal("countSectShopRenewClaims boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"宗门七日续期名额统计失败",
		"formatPlainValue(monthKey)",
		"formatPlainError(err)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect shop renew claim count diagnostic guard missing %q", want)
		}
	}
	if strings.Contains(block, "log.Printf(\"\u95c1") {
		t.Fatal("sect shop renew claim count diagnostics contain mojibake")
	}
	if strings.Contains(block, "userID, monthKey, formatPlainError(err)") {
		t.Fatal("sect shop renew claim count logs raw month key")
	}
}

func TestSectPointDescriptionNameSanitizesText(t *testing.T) {
	got := sectPointDescriptionName("  alpha\nbeta\tgamma  ")
	if got != "alpha beta gamma" {
		t.Fatalf("sectPointDescriptionName() = %q", got)
	}
	if got := sectPointDescriptionName("\n\t"); got != "-" {
		t.Fatalf("empty sect point description name fallback = %q", got)
	}

	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	for _, unsafe := range []string{
		`fmt.Sprintf("创建宗门 %s，消耗 %d 积分", name, sectCreateCost)`,
		`fmt.Sprintf("加入宗门 %s，消耗 %d 积分", sect.Name, sectJoinCost)`,
		`fmt.Sprintf("捐献宗门 %s %d 积分", sect.Name, amount)`,
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("sect point transaction description should sanitize sect name: %s", unsafe)
		}
	}
	for _, want := range []string{
		"sectPointDescriptionName(name), sectCreateCost",
		"sectPointDescriptionName(sect.Name), sectJoinCost",
		"sectPointDescriptionName(sect.Name), amount",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect point transaction description missing sanitized use %q", want)
		}
	}
	if strings.Contains(text, "oldName, newName, sectRenameCost") {
		t.Fatal("sect rename contribution log should not persist raw sect names")
	}
	if !strings.Contains(text, "sectPointDescriptionName(oldName), sectPointDescriptionName(newName), sectRenameCost") {
		t.Fatal("sect rename contribution log should sanitize old and new sect names")
	}
}

func TestSectContributionLogReasonUsesPlainValue(t *testing.T) {
	got := sectContributionLogReason("  alpha\nbeta\tgamma  ")
	if strings.ContainsAny(got, "\n\t") || !strings.Contains(got, "alpha") || !strings.Contains(got, "beta") || !strings.Contains(got, "gamma") {
		t.Fatalf("sectContributionLogReason() did not normalize text: %q", got)
	}

	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func awardSectContributionTx(")
	if start < 0 {
		t.Fatal("awardSectContributionTx missing")
	}
	end := strings.Index(text[start:], "func awardSectListeningContribution(")
	if end < 0 {
		t.Fatal("awardSectContributionTx boundary missing")
	}
	block := text[start : start+end]
	if strings.Contains(block, "Reason:       reason") {
		t.Fatal("awardSectContributionTx should not persist raw contribution log reason")
	}
	if !strings.Contains(block, "Reason:       sectContributionLogReason(reason)") {
		t.Fatal("awardSectContributionTx should sanitize contribution log reason")
	}
}

func TestSectContributionLogReasonsAreReadable(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)

	checks := []struct {
		name  string
		start string
		end   string
		want  []string
	}{
		{
			name:  "handleDonateSect",
			start: "func handleDonateSect(",
			end:   "func handleSectShop(",
			want:  []string{`Reason:       "宗门捐献"`},
		},
		{
			name:  "handleExchangeSectContributionForPrestige",
			start: "func handleExchangeSectContributionForPrestige(",
			end:   "func handleExchangeSectRenew(",
			want:  []string{"贡献兑换宗门声望，消耗 %d 贡献，声望 +%d"},
		},
		{
			name:  "handleSectShopRenew",
			start: `case text == "确认宗门七日续期":`,
			end:   "func handleSectRenewError(",
			want:  []string{"宗门七日续期，续期 %d 天"},
		},
		{
			name:  "awardSectListeningContribution",
			start: "func awardSectListeningContribution(",
			end:   "type sectDailyTaskStatus struct",
			want:  []string{"听书增长奖励 %d 贡献"},
		},
	}

	for _, tc := range checks {
		start := strings.Index(text, tc.start)
		if start < 0 {
			t.Fatalf("%s missing", tc.name)
		}
		end := strings.Index(text[start:], tc.end)
		if end < 0 {
			t.Fatalf("%s boundary missing", tc.name)
		}
		block := text[start : start+end]
		for _, want := range tc.want {
			if !strings.Contains(block, want) {
				t.Fatalf("%s readable contribution log reason missing %q", tc.name, want)
			}
		}
		for _, forbidden := range []string{"Reason:       \"\u95c1", "Reason:       fmt.Sprintf(\"\u95c1"} {
			if strings.Contains(block, forbidden) {
				t.Fatalf("%s contribution log reason contains mojibake", tc.name)
			}
		}
	}
}

func TestCreateSectContributionLogInTxChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createSectContributionLogInTx(")
	if start < 0 {
		t.Fatal("createSectContributionLogInTx missing")
	}
	end := strings.Index(text[start:], "func HandleSectCommand(")
	if end < 0 {
		t.Fatal("createSectContributionLogInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"entry := *logEntry",
		"entry.Reason = sectContributionLogReason(entry.Reason)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SECT_CONTRIBUTION_LOG_CREATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect contribution log helper guard missing %q", want)
		}
	}

	for _, file := range []string{"sect.go", "world_boss.go", "sect_secret_realm.go"} {
		fileSource, err := os.ReadFile(file)
		if err != nil {
			t.Fatalf("read %s err = %v", file, err)
		}
		if strings.Contains(string(fileSource), "tx.Create(&SectContributionLog{") {
			t.Fatalf("%s should use createSectContributionLogInTx for contribution logs", file)
		}
	}
}

func TestSectAndMemberCreatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)

	helperChecks := []struct {
		name string
		next string
		want []string
	}{
		{
			name: "createSectInTx",
			next: "func createSectMemberInTx(",
			want: []string{
				"res := tx.Create(&entry)",
				"res.Error != nil",
				"isUniqueConstraintError(res.Error)",
				"errSectNameExists",
				"res.RowsAffected == 0",
				"SECT_CREATE_MISSED",
			},
		},
		{
			name: "createSectMemberInTx",
			next: "type SectContributionLog struct",
			want: []string{
				"res := tx.Create(&entry)",
				"res.Error != nil",
				"isUniqueConstraintError(res.Error)",
				"errAlreadyInSect",
				"res.RowsAffected == 0",
				"SECT_MEMBER_CREATE_MISSED",
			},
		},
	}
	for _, tc := range helperChecks {
		start := strings.Index(text, "func "+tc.name+"(")
		if start < 0 {
			t.Fatalf("%s missing", tc.name)
		}
		end := strings.Index(text[start:], tc.next)
		if end < 0 {
			t.Fatalf("%s boundary missing", tc.name)
		}
		block := text[start : start+end]
		for _, want := range tc.want {
			if !strings.Contains(block, want) {
				t.Fatalf("%s guard missing %q", tc.name, want)
			}
		}
	}

	createStart := strings.Index(text, "func handleCreateSect(")
	if createStart < 0 {
		t.Fatal("handleCreateSect missing")
	}
	createEnd := strings.Index(text[createStart:], "func handleRenameSect(")
	if createEnd < 0 {
		t.Fatal("handleCreateSect boundary missing")
	}
	createBlock := text[createStart : createStart+createEnd]
	for _, want := range []string{
		"createSectInTx(tx, &sect)",
		"createSectMemberInTx(tx, &member)",
	} {
		if !strings.Contains(createBlock, want) {
			t.Fatalf("handleCreateSect missing helper call %q", want)
		}
	}
	for _, unsafe := range []string{
		"tx.Create(&sect).Error",
		"tx.Create(&SectMember{",
	} {
		if strings.Contains(createBlock, unsafe) {
			t.Fatalf("handleCreateSect still creates without RowsAffected guard: %q", unsafe)
		}
	}

	joinStart := strings.Index(text, "func handleConfirmJoinSect(")
	if joinStart < 0 {
		t.Fatal("handleConfirmJoinSect missing")
	}
	joinEnd := strings.Index(text[joinStart:], "func handleMySect(")
	if joinEnd < 0 {
		t.Fatal("handleConfirmJoinSect boundary missing")
	}
	joinBlock := text[joinStart : joinStart+joinEnd]
	if !strings.Contains(joinBlock, "createSectMemberInTx(tx, &member)") {
		t.Fatal("handleConfirmJoinSect should create member through RowsAffected helper")
	}
	if strings.Contains(joinBlock, "tx.Create(&SectMember{") {
		t.Fatal("handleConfirmJoinSect still creates member without RowsAffected guard")
	}
}

func TestSectNameMigrationReplacesFullUniqueIndex(t *testing.T) {
	sectSource, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go: %v", err)
	}
	sectText := string(sectSource)
	modelStart := strings.Index(sectText, "type Sect struct {")
	if modelStart < 0 {
		t.Fatal("Sect model missing")
	}
	modelEnd := strings.Index(sectText[modelStart:], "func (Sect) TableName() string")
	if modelEnd < 0 {
		t.Fatal("Sect model boundary missing")
	}
	modelBlock := sectText[modelStart : modelStart+modelEnd]
	if strings.Contains(modelBlock, "Name        string `gorm:\"uniqueIndex;not null\"`") {
		t.Fatal("Sect.Name must not use GORM full unique index")
	}
	if !strings.Contains(modelBlock, "Name        string `gorm:\"index;not null\"`") {
		t.Fatal("Sect.Name should keep a normal index tag")
	}

	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("sects(name)"`)
	if start < 0 {
		t.Fatal("sect name migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_members(user_id)"`)
	if end < 0 {
		t.Fatal("sect name migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM sects",
		"WHERE deleted_at IS NULL",
		"ensureSectNamePartialUniqueIndex(DB)",
		"sect name unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect name migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSectNamePartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSectNamePartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureSectMemberUserPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sect name partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sects_name",
		"ON sects(name)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect name partial index helper missing %q", want)
		}
	}
}

func TestSectMemberUserMigrationReplacesFullUniqueIndex(t *testing.T) {
	sectSource, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go: %v", err)
	}
	sectText := string(sectSource)
	modelStart := strings.Index(sectText, "type SectMember struct {")
	if modelStart < 0 {
		t.Fatal("SectMember model missing")
	}
	modelEnd := strings.Index(sectText[modelStart:], "func (SectMember) TableName() string")
	if modelEnd < 0 {
		t.Fatal("SectMember model boundary missing")
	}
	modelBlock := sectText[modelStart : modelStart+modelEnd]
	if strings.Contains(modelBlock, "UserID int64 `gorm:\"uniqueIndex;not null\"`") {
		t.Fatal("SectMember.UserID must not use GORM full unique index")
	}
	if !strings.Contains(modelBlock, "UserID int64 `gorm:\"index;not null\"`") {
		t.Fatal("SectMember.UserID should keep a normal index tag")
	}

	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("sect_members(user_id)"`)
	if start < 0 {
		t.Fatal("sect member user migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_daily_task_claims(user_id, day_key)"`)
	if end < 0 {
		t.Fatal("sect member user migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM sect_members",
		"WHERE deleted_at IS NULL",
		"ensureSectMemberUserPartialUniqueIndex(DB)",
		"sect member user unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect member user migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSectMemberUserPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSectMemberUserPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureGardenSeedPurchasePartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sect member user partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sect_members_user_id",
		"ON sect_members(user_id)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect member user partial index helper missing %q", want)
		}
	}
}

func TestSectMemberReadsDistinguishNotFoundFromDatabaseErrors(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)

	helperChecks := []struct {
		name string
		next string
		want []string
	}{
		{
			name: "loadSectMemberByUserInTx",
			next: "func loadTargetSectMemberByUserInTx(",
			want: []string{
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"return errNotInSect",
				"return err",
			},
		},
		{
			name: "loadTargetSectMemberByUserInTx",
			next: "type SectDailyTaskClaim struct",
			want: []string{
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"return errTargetNotInSect",
				"return err",
			},
		},
	}
	for _, tc := range helperChecks {
		start := strings.Index(text, "func "+tc.name+"(")
		if start < 0 {
			t.Fatalf("%s missing", tc.name)
		}
		end := strings.Index(text[start:], tc.next)
		if end < 0 {
			t.Fatalf("%s boundary missing", tc.name)
		}
		block := text[start : start+end]
		for _, want := range tc.want {
			if !strings.Contains(block, want) {
				t.Fatalf("%s missing read-error guard %q", tc.name, want)
			}
		}
	}

	replyStart := strings.Index(text, "func replySectMemberReadError(")
	if replyStart < 0 {
		t.Fatal("replySectMemberReadError missing")
	}
	replyEnd := strings.Index(text[replyStart:], "type SectDailyTaskClaim struct")
	if replyEnd < 0 {
		t.Fatal("replySectMemberReadError boundary missing")
	}
	replyBlock := text[replyStart : replyStart+replyEnd]
	for _, want := range []string{
		"errors.Is(err, errNotInSect)",
		"formatPlainValue(logLabel)",
		"formatPlainError(err)",
		"宗门成员档案读取失败，请稍后再试。",
	} {
		if !strings.Contains(replyBlock, want) {
			t.Fatalf("replySectMemberReadError guard missing %q", want)
		}
	}

	if count := strings.Count(text, "return errNotInSect"); count != 1 {
		t.Fatalf("errNotInSect should only be returned by loadSectMemberByUserInTx, got %d returns", count)
	}
	if count := strings.Count(text, "return errTargetNotInSect"); count != 1 {
		t.Fatalf("errTargetNotInSect should only be returned by loadTargetSectMemberByUserInTx, got %d returns", count)
	}

	callChecks := []struct {
		name string
		end  string
		want []string
	}{
		{
			name: "handleMySect",
			end:  "func handleSectMembers(",
			want: []string{"loadSectMemberByUserInTx(DB, msg.From.ID, &member, false)"},
		},
		{
			name: "handleSectMembers",
			end:  "func handleSectMemberPageCallback(",
			want: []string{"loadSectMemberByUserInTx(DB, msg.From.ID, &myMember, false)"},
		},
		{
			name: "handleSectMemberPageCallback",
			end:  "func loadSectMemberListPage(",
			want: []string{"loadSectMemberByUserInTx(DB, cb.From.ID, &myMember, false)"},
		},
		{
			name: "handleConfirmRenameSect",
			end:  "func handleConfirmJoinSect(",
			want: []string{"loadSectMemberByUserInTx(tx, userID, &member, false)"},
		},
		{
			name: "handleUpgradeSect",
			end:  "func handleAppointSectRole(",
			want: []string{"loadSectMemberByUserInTx(tx, userID, &member, false)"},
		},
		{
			name: "handleAppointSectRole",
			end:  "func handleExitSect(",
			want: []string{
				"loadSectMemberByUserInTx(tx, userID, &operator, false)",
				"loadTargetSectMemberByUserInTx(tx, targetID, operator.SectID, &target)",
			},
		},
		{
			name: "handleDonateSect",
			end:  "func handleSectShop(",
			want: []string{"loadSectMemberByUserInTx(tx, userID, &member, false)"},
		},
		{
			name: "handleSectShop",
			end:  "func handleExchangeSectContributionForPrestige(",
			want: []string{"loadSectMemberByUserInTx(DB, userID, &member, false)"},
		},
		{
			name: "handleExchangeSectContributionForPrestige",
			end:  "func handleExchangeSectRenew(",
			want: []string{"loadSectMemberByUserInTx(tx, userID, &member, true)"},
		},
		{
			name: "handleExchangeSectRenew",
			end:  "func handleSectRenewError(",
			want: []string{"loadSectMemberByUserInTx(DB, userID, &member, false)"},
		},
		{
			name: "handleSectContributionRank",
			end:  "func querySectContributionRankRows(",
			want: []string{"loadSectMemberByUserInTx(DB, userID, &myMember, false)"},
		},
		{
			name: "handleSectCave",
			end:  "func handleUnlockSectCave(",
			want: []string{"loadSectMemberByUserInTx(DB, userID, &member, false)"},
		},
		{
			name: "handleUnlockSectCave",
			end:  "func handleStartPersonalSectCaveRetreat(",
			want: []string{
				"loadSectMemberByUserInTx(DB, userID, &member, false)",
				"loadSectMemberByUserInTx(tx, userID, &lockedMember, true)",
			},
		},
		{
			name: "handleStartPersonalSectCaveRetreat",
			end:  "func handleStartSectCaveRetreat(",
			want: []string{
				"loadSectMemberByUserInTx(DB, userID, &member, false)",
				"loadSectMemberByUserInTx(tx, userID, &lockedMember, true)",
			},
		},
		{
			name: "handleStartSectCaveRetreat",
			end:  "func awardSectContribution(",
			want: []string{
				"loadSectMemberByUserInTx(DB, userID, &member, false)",
				"loadSectMemberByUserInTx(tx, userID, &lockedOperator, true)",
			},
		},
		{
			name: "handleClaimSectTaskReward",
			end:  "func handleSettleSectWeeklyTaskReward(",
			want: []string{"loadSectMemberByUserInTx(tx, userID, &member, false)"},
		},
		{
			name: "handleSectTasks",
			end:  "func handleClaimSectTaskReward(",
			want: []string{"loadSectMemberByUserInTx(DB, userID, &member, false)"},
		},
		{
			name: "settleSectWeeklyTaskReward",
			end:  "func sectWeeklyTaskManualSettlementTargetTx(",
			want: []string{"loadSectMemberByUserInTx(tx, actorID, &member, false)"},
		},
	}
	for _, tc := range callChecks {
		start := strings.Index(text, "func "+tc.name+"(")
		if start < 0 {
			t.Fatalf("%s missing", tc.name)
		}
		end := strings.Index(text[start:], tc.end)
		if end < 0 {
			t.Fatalf("%s boundary missing", tc.name)
		}
		block := text[start : start+end]
		for _, want := range tc.want {
			if !strings.Contains(block, want) {
				t.Fatalf("%s should read sect member through helper %q", tc.name, want)
			}
		}
	}
}

func TestAwardSectContributionTxChecksSectPrestigeRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func awardSectContributionTx(")
	if start < 0 {
		t.Fatal("awardSectContributionTx missing")
	}
	end := strings.Index(text[start:], "func awardSectListeningContribution(")
	if end < 0 {
		t.Fatal("awardSectContributionTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"sectUpdate := tx.Model(&Sect{})",
		"sectUpdate.Error != nil",
		"sectUpdate.RowsAffected == 0",
		"SECT_CONTRIBUTION_SECT_PRESTIGE_UPDATE_MISSED",
		"memberUpdate.RowsAffected == 0",
		"SECT_MEMBER_CONTRIBUTION_UPDATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("awardSectContributionTx asset rows affected guard missing %q", want)
		}
	}
	if strings.Contains(block, `}).Error; err != nil`) {
		t.Fatal("awardSectContributionTx should not check only update error for asset writes")
	}
}

func TestAwardSectContributionDiagnosticsAreReadable(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func awardSectContribution(")
	if start < 0 {
		t.Fatal("awardSectContribution missing")
	}
	end := strings.Index(text[start:], "func awardSectContributionTx(")
	if end < 0 {
		t.Fatal("awardSectContribution boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"宗门贡献奖励失败",
		"formatPlainValue(reason)",
		"formatPlainError(err)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("awardSectContribution diagnostic guard missing %q", want)
		}
	}
	if strings.Contains(block, "log.Printf(\"\u95c1") || strings.Contains(block, "log.Printf(\"\u95c2") {
		t.Fatal("awardSectContribution diagnostics contain mojibake")
	}
}

func TestFormatSectMemberListPageUsesThirtyPerPage(t *testing.T) {
	members := make([]SectMember, 52)
	for i := range members {
		members[i] = SectMember{
			UserName:     "member_" + strconv.Itoa(i+1),
			Role:         sectRoleMember,
			Contribution: i + 1,
		}
	}
	members[0].Role = sectRoleOwner

	first := formatSectMemberListPage(Sect{Name: "天机阁", Level: 5}, members[:sectMemberListPageSize], len(members), 1)
	if !strings.Contains(first, "成员：`52/60`") || !strings.Contains(first, "页码：`1/2`") {
		t.Fatalf("member list should show total member count, capacity and page, got: %s", first)
	}
	if !strings.Contains(first, "1. `member\\_1` [宗主]") {
		t.Fatalf("member list missing owner row: %s", first)
	}
	if strings.Contains(first, "31. `member\\_31`") {
		t.Fatalf("first page should contain only 30 members, got: %s", first)
	}

	second := formatSectMemberListPage(Sect{Name: "天机阁", Level: 5}, members[sectMemberListPageSize:], len(members), 2)
	if !strings.Contains(second, "页码：`2/2`") {
		t.Fatalf("second page should show page 2/2, got: %s", second)
	}
	if !strings.Contains(second, "31. `member\\_31` [成员]") || !strings.Contains(second, "52. `member\\_52` [成员]") {
		t.Fatalf("second page should include members 31-52, got: %s", second)
	}

	markup := sectMemberListPageMarkup(1, sectMemberListTotalPages(len(members)))
	if len(markup.InlineKeyboard) != 1 || len(markup.InlineKeyboard[0]) != 1 || markup.InlineKeyboard[0][0].Text != "下一页" {
		t.Fatalf("first page should only expose next-page button, got: %#v", markup.InlineKeyboard)
	}

}

func TestSectTaskPanelDoesNotTreatQueryErrorAsNormalStatus(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	for _, helper := range []string{
		"func sectClaimExistsForDay(dayKey string, userID int64) (bool, error)",
		"func sectWeeklySettlementExists(sectID int64, weekKey string) (bool, error)",
	} {
		if !strings.Contains(text, helper) {
			t.Fatalf("missing status helper: %s", helper)
		}
	}
	for _, unsafePattern := range []string{
		`claimedToday := DB.Where("user_id = ? AND day_key = ?", userID, dayKey).First(&todayClaim).Error == nil`,
		`weeklySettled := DB.Where("sect_id = ? AND week_key = ?", member.SectID, weeklyStats.WeekKey).First(&weeklySettlement).Error == nil`,
	} {
		if strings.Contains(text, unsafePattern) {
			t.Fatalf("sect task panel still ignores query error: %s", unsafePattern)
		}
	}
	if !strings.Contains(text, "状态暂不可用") || !strings.Contains(text, "本周结算状态暂不可用") {
		t.Fatalf("sect task panel should expose unavailable status when query fails")
	}
}

func TestSectWeeklyTaskStatsErrorsAreNotTreatedAsZeroProgress(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "func querySectWeeklyTaskStatsTx(tx *gorm.DB, sectID int64, now time.Time) (sectWeeklyTaskStats, error)") {
		t.Fatalf("weekly task stats query must return an error")
	}
	if !strings.Contains(text, "func querySectWeeklyTaskStats(sectID int64, now time.Time) (sectWeeklyTaskStats, error)") {
		t.Fatalf("weekly task stats wrapper must return an error")
	}
	if strings.Contains(text, "stats := querySectWeeklyTaskStatsTx(tx, sectID, now)\n\treward := calculateSectWeeklyTaskReward") {
		t.Fatalf("settlement still calculates reward from stats without checking query error")
	}
	if !strings.Contains(text, "宗门周目标统计状态暂不可用") {
		t.Fatalf("sect task panel should expose unavailable weekly stats status")
	}
}

func TestSectWeeklyManualSettlementTargetReadErrorsAbort(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"func sectWeeklyTaskManualSettlementTargetTx(tx *gorm.DB, sectID int64, now time.Time) (time.Time, error)",
		"targetTime, err := sectWeeklyTaskManualSettlementTargetTx(tx, member.SectID, now)",
		"return err",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"return time.Time{}, err",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("weekly manual settlement target read guard missing %q", want)
		}
	}
	if strings.Contains(text, "func sectWeeklyTaskManualSettlementTargetTx(tx *gorm.DB, sectID int64, now time.Time) time.Time") ||
		strings.Contains(text, "targetTime := sectWeeklyTaskManualSettlementTargetTx(tx, member.SectID, now)") {
		t.Fatal("weekly manual settlement target still ignores settlement lookup errors")
	}
}

func TestSectDailyTaskStatusesReturnErrors(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "func getSectDailyTaskStatuses(userID int64, sectID int64, now time.Time) ([]sectDailyTaskStatus, error)") {
		t.Fatalf("daily task status helper must return an error")
	}
	if !strings.Contains(text, "func getSectDailyTaskStatusesTx(") || !strings.Contains(text, "([]sectDailyTaskStatus, error)") {
		t.Fatalf("daily task status tx helper must return an error")
	}
	if strings.Contains(text, "tasks := getSectDailyTaskStatuses(userID, member.SectID, now)") {
		t.Fatalf("task panel still ignores daily task status error")
	}
	if strings.Contains(text, "tasks := getSectDailyTaskStatusesTx(tx, userID, member.SectID, now, listenHoursOverride)") {
		t.Fatalf("claim flow still ignores daily task status error")
	}
	if !strings.Contains(text, "查询宗门每日任务状态失败") {
		t.Fatalf("daily task status query failure should be logged")
	}
	if !strings.Contains(text, "个人任务状态暂不可用") {
		t.Fatalf("daily task panel should expose unavailable status")
	}
	for _, want := range []string{
		`signStatus := "未完成"`,
		`signStatus = "已完成"`,
		`Name:         "今日签到"`,
		`Name:         "今日净修为 +1 小时"`,
		`fmt.Sprintf("今日捐献 %d 积分", sectDailyTaskDonateTarget)`,
		`return fmt.Sprintf("超额 +%d%%", excess)`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("daily task readable copy missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"signStatus := \"\u95c1",
		"signStatus = \"\u943e",
		"Name:         \"\u6fde",
		"fmt.Sprintf(\"\u6fde",
		"return fmt.Sprintf(\"\u95c1",
	} {
		if strings.Contains(text, forbidden) {
			t.Fatal("daily task status copy contains mojibake")
		}
	}
}

func TestSectCopyDoesNotContainMojibake(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	for _, bad := range []string{
		"\u95c1",
		"\u9498",
		"\u7039",
		"\u59b7",
		"\u9289",
		"\u940e",
		"\u95bb",
		"\u59ab",
	} {
		if strings.Contains(text, bad) {
			t.Fatalf("sect copy contains mojibake marker %q", bad)
		}
	}
	for _, want := range []string{
		"暂无成员。",
		"目标用户 ID 格式不正确。",
		"不能任命自己，请指定本宗门其他成员。",
		"宗门职位任命失败，请稍后再试。",
		"你尚未加入宗门，无法查看宗门商店。",
		"你尚未加入宗门，无法使用宗门七日续期。",
		"本地档案读取失败，无法续期，请稍后再试。",
		"宗门七日续期已到账，新的到期时间",
		"宗门七日续期已到账且 ABS 账号已恢复",
		"宗门今日净修为 ABS 当日数据缺失，使用本地缓存",
		"宗门今日净修为 ABS 当日数据命中",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect readable copy missing %q", want)
		}
	}
}

func TestSectDailyTaskIncompleteTextListsMissingItems(t *testing.T) {
	tasks := []sectDailyTaskStatus{
		{Name: "今日签到", Completed: true, ProgressText: "已完成"},
		{Name: "今日净修为 +1 小时", Completed: false, ProgressText: "0.25/1.00"},
		{Name: "今日捐献 1 积分", Completed: false, ProgressText: "0/1"},
	}
	incomplete := getIncompleteSectDailyTaskSummaries(tasks)
	text := sectDailyTaskIncompleteText(incomplete)
	for _, want := range []string{"尚未完成", "今日净修为 +1 小时（0.25/1.00）", "今日捐献 1 积分（0/1）"} {
		if !strings.Contains(text, want) {
			t.Fatalf("incomplete task text missing %q: %s", want, text)
		}
	}
	if strings.Contains(text, "今日签到（已完成）") {
		t.Fatalf("completed task should not be listed: %s", text)
	}
}

func TestSectErrorCodeMapsDailyTaskClaimStates(t *testing.T) {
	tests := map[string]string{
		"SECT_DAILY_TASK_NOT_ALL_COMPLETED": "SECT_DAILY_TASK_NOT_ALL_COMPLETED",
		"ALREADY_CLAIMED":                   "ALREADY_CLAIMED",
	}
	for input, want := range tests {
		if got := sectErrorCode(fmt.Errorf(input)); got != want {
			t.Fatalf("sectErrorCode(%s) = %s, want %s", input, got, want)
		}
	}
}

func TestSectMemberCountDecrementChecksRowsAffected(t *testing.T) {
	data, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(data)
	if got := strings.Count(text, "SECT_MEMBER_COUNT_CHANGED"); got != 2 {
		t.Fatalf("member count decrement guard count = %d, want 2", got)
	}
	pos := 0
	for i := 0; i < 2; i++ {
		idx := strings.Index(text[pos:], `UpdateColumn("member_count", gorm.Expr("member_count - 1"))`)
		if idx < 0 {
			t.Fatalf("missing member_count decrement #%d", i+1)
		}
		start := pos + idx
		block := text[start:minInt(len(text), start+220)]
		if !strings.Contains(block, "res.RowsAffected == 0") {
			t.Fatalf("member_count decrement #%d does not check RowsAffected: %s", i+1, block)
		}
		pos = start + 1
	}
}

func TestSectMemberDeleteChecksRowsAffectedBeforeCountDecrement(t *testing.T) {
	data, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(data)
	for _, fn := range []string{"func handleExitSect(", "func handleKickSectMember("} {
		start := strings.Index(text, fn)
		if start < 0 {
			t.Fatalf("%s missing", fn)
		}
		end := strings.Index(text[start:], "res := tx.Model(&Sect{})")
		if end < 0 {
			t.Fatalf("%s member count update boundary missing", fn)
		}
		block := text[start : start+end]
		for _, want := range []string{
			"deleteRes := tx.Unscoped()",
			"Delete(&SectMember{})",
			"deleteRes.Error",
			"deleteRes.RowsAffected == 0",
			"SECT_MEMBER_DELETE_MISSED",
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s delete guard missing %q", fn, want)
			}
		}
		if strings.Contains(block, "tx.Unscoped()."+"Delete(&member).Error") ||
			strings.Contains(block, "tx.Unscoped()."+"Delete(&target).Error") ||
			strings.Contains(block, "tx.Unscoped()."+"Delete(&member)") ||
			strings.Contains(block, "tx.Unscoped()."+"Delete(&target)") {
			t.Fatalf("%s still deletes a loaded member without scoped conditions", fn)
		}
	}
	exitStart := strings.Index(text, "func handleExitSect(")
	if exitStart < 0 {
		t.Fatal("exit sect block missing")
	}
	exitEnd := strings.Index(text[exitStart:], "func handleKickSectMember(")
	if exitEnd < 0 {
		t.Fatal("exit sect block boundary missing")
	}
	exitBlock := text[exitStart : exitStart+exitEnd]
	for _, want := range []string{
		`Where("id = ? AND user_id = ? AND role <> ?", member.ID, userID, sectRoleOwner)`,
		"Delete(&SectMember{})",
	} {
		if !strings.Contains(exitBlock, want) {
			t.Fatalf("exit sect conditional delete guard missing %q", want)
		}
	}

	kickStart := strings.Index(text, "func handleKickSectMember(")
	if kickStart < 0 {
		t.Fatal("kick sect block missing")
	}
	kickEnd := strings.Index(text[kickStart:], "func handleTransferSectOwner(")
	if kickEnd < 0 {
		t.Fatal("kick sect block boundary missing")
	}
	kickBlock := text[kickStart : kickStart+kickEnd]
	for _, want := range []string{
		`Where("id = ? AND user_id = ? AND sect_id = ? AND role = ?", target.ID, targetID, operator.SectID, target.Role)`,
		`EXISTS (SELECT 1 FROM sect_members op WHERE op.id = ? AND op.user_id = ? AND op.sect_id = ? AND op.role = ? AND op.deleted_at IS NULL)`,
		"Delete(&SectMember{})",
		"目标用户 ID 格式错误。",
		"不能将自己踢出宗门。",
		"你尚未加入宗门，无法踢出成员。",
		"目标成员不在你的宗门中。",
		"只有宗主或长老可以踢出普通成员。",
		"宗门踢出成员失败",
		"踢出宗门成员失败，请稍后再试。",
		"已将 `%s` 踢出宗门。",
		"formatPlainError(err)",
	} {
		if !strings.Contains(kickBlock, want) {
			t.Fatalf("kick sect conditional delete guard missing %q", want)
		}
	}
	if strings.Contains(kickBlock, "log.Printf(\"\u95c1") {
		t.Fatal("kick sect diagnostics contain mojibake")
	}
	if strings.Contains(kickBlock, "replyText(bot, chatID, \"\u95c1") ||
		strings.Contains(kickBlock, "replyText(bot, chatID, \"\u95c2") {
		t.Fatal("kick sect copy contains mojibake")
	}
}

func TestSectUpgradeSuccessReloadHandlesReadError(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"宗门升级后成员上限读取失败",
		"maxMembersText := \"读取失败\"",
		"成员上限提升至：`%s` 人",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect upgrade reload guard missing %q", want)
		}
	}
	if strings.Contains(text, "DB.Where(\"name = ?\", sectName).First(&upgradedSect)") {
		t.Fatal("sect upgrade success reload still ignores DB errors")
	}
}

func TestSectUpgradeSuccessReloadChecksTechnologyReadError(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleUpgradeSect(")
	if start < 0 {
		t.Fatal("handleUpgradeSect missing")
	}
	end := strings.Index(text[start:], "func handleAppointSectRole(")
	if end < 0 {
		t.Fatal("handleUpgradeSect boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"maxMembersText := \"读取失败\"",
		"maxMembers, err := getSectMaxMembersWithTechTxChecked(DB, upgradedSect)",
		"道友尚未加入宗门，无法升级宗门。",
		"只有宗主或长老可以升级宗门。",
		"宗门已达最高等级",
		"宗门资金不足，升级需要",
		"宗门声望不足，升级需要",
		"宗门资源已变化，请稍后重试。",
		"宗门升级失败，请稍后再试。",
		"宗门升级失败",
		"宗门升级后成员上限科技读取失败",
		"maxMembersText = strconv.Itoa(maxMembers)",
		"formatPlainError(err)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect upgrade technology reload guard missing %q", want)
		}
	}
	if strings.Contains(block, "getSectMaxMembersWithTech(upgradedSect)") {
		t.Fatal("sect upgrade success reload still ignores technology read errors")
	}
	if strings.Contains(block, "log.Printf(\"\u95c1") || strings.Contains(block, "log.Printf(\"\u95c2") ||
		strings.Contains(block, "replyText(bot, chatID, \"\u95c1") || strings.Contains(block, "replyText(bot, chatID, \"\u95c2") {
		t.Fatal("sect upgrade diagnostics contain mojibake")
	}
}

func TestSectMaxMemberDisplaysCheckTechnologyReadError(t *testing.T) {
	sectSource, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	sectText := string(sectSource)
	for _, want := range []string{
		"func sectMaxMembersDisplayText(",
		"getSectMaxMembersWithTechTxChecked(DB, sect)",
		"sect max members display read failed",
		"sectMaxMembersDisplayText(sect, \"my_sect\", msg.From.ID)",
		"sectMaxMembersDisplayText(sect, \"sect_rank\", msg.From.ID)",
		"sectMaxMembersDisplayText(sect, \"sect_member_list\", 0)",
		"formatSectMemberListPageWithMax(",
		"成员：`%d/%s`",
	} {
		if !strings.Contains(sectText, want) {
			t.Fatalf("sect max member display guard missing %q", want)
		}
	}

	techSource, err := os.ReadFile("sect_technology.go")
	if err != nil {
		t.Fatalf("read sect_technology.go err = %v", err)
	}
	techText := string(techSource)
	for _, want := range []string{
		"当前上限 %s 人",
		"sectMaxMembersDisplayText(sect, \"sect_technology_effect\", 0)",
	} {
		if !strings.Contains(techText, want) {
			t.Fatalf("sect technology max member display guard missing %q", want)
		}
	}

	for _, unsafe := range []string{
		"maxMembers := getSectMaxMembersWithTech(sect)",
		"getSectMaxMembersWithTech(sect),",
		"当前上限 %d 人",
	} {
		if strings.Contains(sectText, unsafe) || strings.Contains(techText, unsafe) {
			t.Fatalf("sect max member display still ignores technology read errors: %s", unsafe)
		}
	}
}

func TestDonateSectChecksAssetUpdateRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleDonateSect(")
	if start < 0 {
		t.Fatal("handleDonateSect missing")
	}
	end := strings.Index(text[start:], "func handleSectShop(")
	if end < 0 {
		t.Fatal("handleDonateSect boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"fundRes := tx.Model(&Sect{})",
		"fundRes.RowsAffected == 0",
		"SECT_DONATE_FUNDS_UPDATE_MISSED",
		"memberRes := tx.Model(&SectMember{})",
		"memberRes.RowsAffected == 0",
		"SECT_DONATE_MEMBER_CONTRIBUTION_UPDATE_MISSED",
		"捐献数量必须是 1-100000 的整数。",
		"你尚未加入宗门，无法捐献。",
		"积分不足，无法完成宗门捐献。",
		"宗门捐献失败",
		"宗门捐献失败，请稍后再试。",
		"formatPlainError(err)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("donate sect asset rows affected guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`UpdateColumn("funds", gorm.Expr("funds + ?", amount)).Error`,
		`if err := tx.Model(&SectMember{}).`,
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("donate sect still ignores RowsAffected: %s", unsafe)
		}
	}
	if strings.Contains(block, "log.Printf(\"\u95c1") ||
		strings.Contains(block, "replyText(bot, chatID, \"\u95c1") ||
		strings.Contains(block, "replyText(bot, chatID, \"\u95c2") {
		t.Fatal("donate sect diagnostics or copy contain mojibake")
	}
}

func TestExchangeSectContributionForPrestigeChecksAssetUpdateRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleExchangeSectContributionForPrestige(")
	if start < 0 {
		t.Fatal("handleExchangeSectContributionForPrestige missing")
	}
	end := strings.Index(text[start:], "func handleExchangeSectRenew(")
	if end < 0 {
		t.Fatal("handleExchangeSectContributionForPrestige boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"sectPrestigeRes := tx.Model(&Sect{})",
		"sectPrestigeRes.RowsAffected == 0",
		"SECT_SHOP_PRESTIGE_SECT_UPDATE_MISSED",
		"memberPrestigeRes := tx.Model(&SectMember{})",
		"memberPrestigeRes.RowsAffected == 0",
		"SECT_SHOP_PRESTIGE_MEMBER_UPDATE_MISSED",
		"兑换数量格式错误。",
		"你尚未加入宗门，无法兑换宗门声望。",
		"个人贡献不足，本次兑换需要",
		"宗门贡献兑换声望失败",
		"贡献兑换宗门声望失败，请稍后再试。",
		"formatPlainError(err)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect shop prestige rows affected guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`UpdateColumn("prestige", gorm.Expr("prestige + ?", rewardPrestige)).Error`,
		`UpdateColumn("personal_prestige", gorm.Expr("personal_prestige + ?", rewardPrestige)).Error`,
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("sect shop prestige still ignores RowsAffected: %s", unsafe)
		}
	}
	if strings.Contains(block, "log.Printf(\"\u95c1") ||
		strings.Contains(block, "replyText(bot, chatID, \"\u95c1") ||
		strings.Contains(block, "replyText(bot, chatID, \"\u95c2") {
		t.Fatal("sect shop prestige diagnostics or copy contain mojibake")
	}

	disabledStart := strings.Index(text, "func handleDisabledSectContributionForPoints(")
	if disabledStart < 0 {
		t.Fatal("handleDisabledSectContributionForPoints missing")
	}
	disabledEnd := strings.Index(text[disabledStart:], "func handleExchangeSectContributionForPrestige(")
	if disabledEnd < 0 {
		t.Fatal("handleDisabledSectContributionForPoints boundary missing")
	}
	disabledBlock := text[disabledStart : disabledStart+disabledEnd]
	if !strings.Contains(disabledBlock, "贡献换积分已关闭。") ||
		!strings.Contains(disabledBlock, "贡献换声望 数量") {
		t.Fatal("disabled contribution-for-points copy should explain the supported exchange path")
	}
	if strings.Contains(disabledBlock, "replyText(bot, msg.Chat.ID, \"\u95c1") ||
		strings.Contains(disabledBlock, "replyText(bot, msg.Chat.ID, \"\u95c2") {
		t.Fatal("disabled contribution-for-points copy contains mojibake")
	}
}

func TestSectShopPurchaseCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	helperStart := strings.Index(text, "func createSectShopPurchaseInTx(")
	if helperStart < 0 {
		t.Fatal("createSectShopPurchaseInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "type SectCaveRetreat struct")
	if helperEnd < 0 {
		t.Fatal("createSectShopPurchaseInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"purchase.ExchangeType = formatPlainValue(purchase.ExchangeType)",
		"purchase.DayKey = formatPlainValue(purchase.DayKey)",
		"res := tx.Create(purchase)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SECT_SHOP_PURCHASE_CREATE_MISSED",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect shop purchase helper guard missing %q", want)
		}
	}

	prestigeStart := strings.Index(text, "func handleExchangeSectContributionForPrestige(")
	if prestigeStart < 0 {
		t.Fatal("handleExchangeSectContributionForPrestige missing")
	}
	prestigeEnd := strings.Index(text[prestigeStart:], "func handleExchangeSectRenew(")
	if prestigeEnd < 0 {
		t.Fatal("handleExchangeSectContributionForPrestige boundary missing")
	}
	prestigeBlock := text[prestigeStart : prestigeStart+prestigeEnd]
	if !strings.Contains(prestigeBlock, "createSectShopPurchaseInTx(tx, &SectShopPurchase{") {
		t.Fatal("sect shop prestige purchase should use createSectShopPurchaseInTx")
	}

	renewStart := strings.Index(text, "func handleSectShop(")
	if renewStart < 0 {
		t.Fatal("handleSectShop missing")
	}
	renewEnd := strings.Index(text[renewStart:], "func handleSectRenewError(")
	if renewEnd < 0 {
		t.Fatal("handleSectShop renew boundary missing")
	}
	renewBlock := text[renewStart : renewStart+renewEnd]
	if !strings.Contains(renewBlock, "createSectShopPurchaseInTx(tx, &purchase)") {
		t.Fatal("sect shop renew purchase should use createSectShopPurchaseInTx")
	}

	for _, unsafe := range []string{
		"tx.Create(&SectShopPurchase{",
		"tx.Create(&purchase).Error",
	} {
		if strings.Contains(prestigeBlock, unsafe) || strings.Contains(renewBlock, unsafe) {
			t.Fatalf("sect shop purchase create still ignores RowsAffected: %s", unsafe)
		}
	}
}

func TestSectDailyTaskClaimChecksRewardRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	helperStart := strings.Index(text, "func createSectDailyTaskClaimInTx(")
	if helperStart < 0 {
		t.Fatal("createSectDailyTaskClaimInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "type SectWeeklyTaskSettlement struct")
	if helperEnd < 0 {
		t.Fatal("createSectDailyTaskClaimInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"res := tx.Create(claim)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SECT_DAILY_TASK_CLAIM_CREATE_MISSED",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("daily task claim helper guard missing %q", want)
		}
	}

	idx := strings.Index(text, "SECT_DAILY_TASK_SECT_REWARD_UPDATE_MISSED")
	if idx < 0 {
		t.Fatal("daily task sect reward guard missing")
	}
	start := idx - 900
	if start < 0 {
		start = 0
	}
	end := strings.Index(text[idx:], "SectContributionLog")
	if end < 0 {
		t.Fatal("daily task reward boundary missing")
	}
	block := text[start : idx+end]
	for _, want := range []string{
		"sectRewardRes := tx.Model(&Sect{})",
		"sectRewardRes.RowsAffected == 0",
		"SECT_DAILY_TASK_SECT_REWARD_UPDATE_MISSED",
		"memberRewardRes := tx.Model(&SectMember{})",
		"memberRewardRes.RowsAffected == 0",
		"SECT_DAILY_TASK_MEMBER_REWARD_UPDATE_MISSED",
		"createSectDailyTaskClaimInTx(tx, &claim)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("daily task reward rows affected guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`"funds":    gorm.Expr("funds + ?", sectDailyTaskRewardContribution),
				"prestige": gorm.Expr("prestige + ?", sectDailyTaskRewardPrestige),
			}).Error`,
		`"contribution":        gorm.Expr("contribution + ?", sectDailyTaskRewardContribution),
				"weekly_contribution": gorm.Expr("weekly_contribution + ?", sectDailyTaskRewardContribution),
			}).Error`,
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("daily task reward still ignores RowsAffected: %s", unsafe)
		}
	}
	if strings.Contains(block, "tx.Create(&claim).Error") {
		t.Fatal("daily task claim create still ignores RowsAffected")
	}
}

func TestSectTaskRewardMigrationsReplaceFullUniqueIndexes(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("sect_daily_task_claims(user_id, day_key)"`)
	if start < 0 {
		t.Fatal("sect task reward migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_listening_daily_progresses(user_id, day_key)"`)
	if end < 0 {
		t.Fatal("sect task reward migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM sect_daily_task_claims",
		"FROM sect_weekly_task_settlements",
		"WHERE deleted_at IS NULL",
		"ensureSectTaskRewardPartialUniqueIndexes(DB)",
		"sect task reward unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect task reward migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSectTaskRewardPartialUniqueIndexes(")
	if helperStart < 0 {
		t.Fatal("ensureSectTaskRewardPartialUniqueIndexes missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureInventoryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sect task reward partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sect_daily_task_claims_user_day_unique",
		"ON sect_daily_task_claims(user_id, day_key)",
		"idx_sect_weekly_task_settlements_sect_week_unique",
		"ON sect_weekly_task_settlements(sect_id, week_key)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect task reward partial index helper missing %q", want)
		}
	}
}

func TestSectTaskRewardDiagnosticsAreReadable(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)

	claimStart := strings.Index(text, "func handleClaimSectTaskReward(")
	if claimStart < 0 {
		t.Fatal("handleClaimSectTaskReward missing")
	}
	claimEnd := strings.Index(text[claimStart:], "func handleSettleSectWeeklyTaskReward(")
	if claimEnd < 0 {
		t.Fatal("handleClaimSectTaskReward boundary missing")
	}
	claimBlock := text[claimStart : claimStart+claimEnd]
	for _, want := range []string{
		"宗门每日任务奖励，完成 %d 项",
		"道友尚未加入宗门，无法领取宗门任务奖励。",
		"今日宗门任务奖励已经领取过了。",
		"宗门每日任务领奖失败",
		"宗门任务奖励领取失败，请稍后再试。",
		"formatPlainError(err)",
	} {
		if !strings.Contains(claimBlock, want) {
			t.Fatalf("sect daily task claim diagnostic guard missing %q", want)
		}
	}

	weeklyStart := strings.Index(text, "func handleSettleSectWeeklyTaskReward(")
	if weeklyStart < 0 {
		t.Fatal("handleSettleSectWeeklyTaskReward missing")
	}
	weeklyEnd := strings.Index(text[weeklyStart:], "func settleSectWeeklyTaskReward(")
	if weeklyEnd < 0 {
		t.Fatal("handleSettleSectWeeklyTaskReward boundary missing")
	}
	weeklyBlock := text[weeklyStart : weeklyStart+weeklyEnd]
	for _, want := range []string{
		"道友尚未加入宗门，无法结算宗门周目标。",
		"只有宗主或长老可以结算宗门周目标。",
		"本周宗门目标尚未达成，暂不可结算。",
		"本周宗门目标已经结算过了。",
		"宗门周目标结算失败",
		"宗门周目标结算失败，请稍后再试。",
		"formatPlainError(err)",
	} {
		if !strings.Contains(weeklyBlock, want) {
			t.Fatalf("sect weekly task settlement diagnostic guard missing %q", want)
		}
	}

	for name, block := range map[string]string{
		"handleClaimSectTaskReward":        claimBlock,
		"handleSettleSectWeeklyTaskReward": weeklyBlock,
	} {
		if strings.Contains(block, "log.Printf(\"\u95c1") || strings.Contains(block, "log.Printf(\"\u95c2") ||
			strings.Contains(block, "replyText(bot, chatID, \"\u95c1") || strings.Contains(block, "replyText(bot, chatID, \"\u95c2") {
			t.Fatalf("%s diagnostics contain mojibake", name)
		}
	}
}

func TestSectCriticalTechnologyReadsReturnErrors(t *testing.T) {
	techSource, err := os.ReadFile("sect_technology.go")
	if err != nil {
		t.Fatalf("read sect_technology.go err = %v", err)
	}
	techText := string(techSource)
	for _, want := range []string{
		"func getSectTechnologyLevelTxChecked(",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"func getSectMaxMembersWithTechTxChecked(",
		"func getSectDailyTaskRewardsTxChecked(",
	} {
		if !strings.Contains(techText, want) {
			t.Fatalf("technology checked helper missing %q", want)
		}
	}

	sectSource, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	sectText := string(sectSource)
	for _, want := range []string{
		"maxMembers, err := getSectMaxMembersWithTechTxChecked(tx, sect)",
		"return err",
		"sectDailyTaskRewardContribution, sectDailyTaskRewardPrestige, rewardErr = getSectDailyTaskRewardsTxChecked(tx, member.SectID)",
		"return rewardErr",
	} {
		if !strings.Contains(sectText, want) {
			t.Fatalf("sect critical technology read guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"sect.MemberCount >= getSectMaxMembersWithTechTx(tx, sect)",
		"maxMembers := getSectMaxMembersWithTechTx(tx, sect)",
		"sectDailyTaskRewardContribution, sectDailyTaskRewardPrestige = getSectDailyTaskRewardsTx(tx, member.SectID)",
	} {
		if strings.Contains(sectText, unsafe) {
			t.Fatalf("sect critical path still swallows technology read errors: %s", unsafe)
		}
	}
}

func TestSectTechnologyPanelAndUpgradeCheckLevelReadErrors(t *testing.T) {
	source, err := os.ReadFile("sect_technology.go")
	if err != nil {
		t.Fatalf("read sect_technology.go err = %v", err)
	}
	text := string(source)

	panelStart := strings.Index(text, "func handleSectTechnology(")
	if panelStart < 0 {
		t.Fatal("handleSectTechnology missing")
	}
	panelEnd := strings.Index(text[panelStart:], "func handleUpgradeSectTechnology(")
	if panelEnd < 0 {
		t.Fatal("handleSectTechnology boundary missing")
	}
	panelBlock := text[panelStart : panelStart+panelEnd]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"宗门科技面板成员档案读取失败",
		"宗门成员档案读取失败，请稍后再试。",
		"宗门科技面板宗门档案读取失败",
		"宗门档案读取失败，请稍后再试。",
		"getSectTechnologyLevelTxChecked(DB, int64(sect.ID), def.Key)",
		"宗门科技面板等级读取失败",
		"Lv.读取失败/%d",
		"效果：读取失败",
		"formatPlainError(levelErr)",
	} {
		if !strings.Contains(panelBlock, want) {
			t.Fatalf("sect technology panel read failure guard missing %q", want)
		}
	}
	if strings.Contains(panelBlock, "level := getSectTechnologyLevel(int64(sect.ID), def.Key)") {
		t.Fatal("sect technology panel still swallows level read errors")
	}

	upgradeBlock := text[panelStart+panelEnd:]
	for _, want := range []string{
		"宗门科技升级确认成员档案读取失败",
		"宗门科技升级确认宗门档案读取失败",
		"currentLevel, levelErr := getSectTechnologyLevelTxChecked(DB, member.SectID, techKey)",
		"宗门科技升级确认等级读取失败",
		"宗门科技读取失败，请稍后重试。",
		"if !errors.Is(err, gorm.ErrRecordNotFound) {\n\t\t\t\treturn err\n\t\t\t}",
		"oldLevel, levelErr = getSectTechnologyLevelTxChecked(tx, int64(sect.ID), techKey)",
		"return levelErr",
		"宗门科技升级失败",
		"formatPlainError(err)",
	} {
		if !strings.Contains(upgradeBlock, want) {
			t.Fatalf("sect technology upgrade read failure guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"currentLevel := getSectTechnologyLevel(member.SectID, techKey)",
		"oldLevel = getSectTechnologyLevelTx(tx, int64(sect.ID), techKey)",
	} {
		if strings.Contains(upgradeBlock, unsafe) {
			t.Fatalf("sect technology upgrade still swallows level read errors: %s", unsafe)
		}
	}
}

func TestSectTechnologyLogCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect_technology.go")
	if err != nil {
		t.Fatalf("read sect_technology.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createSectTechnologyLogInTx(")
	if start < 0 {
		t.Fatal("createSectTechnologyLogInTx missing")
	}
	end := strings.Index(text[start:], "const (")
	if end < 0 {
		t.Fatal("createSectTechnologyLogInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"entry := *logEntry",
		"entry.UserName = formatPlainValue(entry.UserName)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SECT_TECHNOLOGY_LOG_CREATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect technology log helper guard missing %q", want)
		}
	}
	if strings.Contains(text, "tx.Create(&SectTechnologyLog{") {
		t.Fatal("sect technology logs should use createSectTechnologyLogInTx")
	}
}

func TestSectTechnologyCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect_technology.go")
	if err != nil {
		t.Fatalf("read sect_technology.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createSectTechnologyInTx(")
	if start < 0 {
		t.Fatal("createSectTechnologyInTx missing")
	}
	end := strings.Index(text[start:], "func createSectTechnologyLogInTx(")
	if end < 0 {
		t.Fatal("createSectTechnologyInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"entry := *technology",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SECT_TECHNOLOGY_CREATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect technology helper guard missing %q", want)
		}
	}

	upgradeStart := strings.Index(text, "func handleUpgradeSectTechnology(")
	if upgradeStart < 0 {
		t.Fatal("handleUpgradeSectTechnology missing")
	}
	upgradeBlock := text[upgradeStart:]
	if !strings.Contains(upgradeBlock, "createSectTechnologyInTx(tx, &tech)") {
		t.Fatal("sect technology upgrade does not use createSectTechnologyInTx")
	}
	if strings.Contains(upgradeBlock, "tx.Create(&tech).Error") {
		t.Fatal("sect technology upgrade still creates tech directly")
	}
}

func TestSectTechnologyMigrationReplacesFullUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("sect_technologies(sect_id, tech_key)"`)
	if start < 0 {
		t.Fatal("sect technology migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("sect_secret_realm_events(active sect_id)"`)
	if end < 0 {
		t.Fatal("sect technology migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM sect_technologies",
		"WHERE deleted_at IS NULL",
		"ensureSectTechnologyUniqueIndex(DB)",
		"sect technology unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect technology migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSectTechnologyUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSectTechnologyUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureSectSecretRealmActiveUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sect technology unique index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sect_technologies_sect_key_unique",
		"ON sect_technologies(sect_id, tech_key)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect technology unique index helper missing %q", want)
		}
	}
}

func TestSectTaskPanelRewardDisplayChecksTechnologyReadError(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleSectTasks(")
	if start < 0 {
		t.Fatal("handleSectTasks missing")
	}
	end := strings.Index(text[start:], "func formatSectWeeklyTaskExcessText(")
	if end < 0 {
		t.Fatal("handleSectTasks boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"sectDailyTaskRewardContributionText := \"读取失败\"",
		"sectDailyTaskRewardPrestigeText := \"读取失败\"",
		"getSectDailyTaskRewardsTxChecked(DB, member.SectID)",
		"宗门任务页每日奖励读取失败",
		"宗门每日任务领取状态读取失败",
		"宗门周目标结算状态读取失败",
		"formatPlainValue(dayKey)",
		"formatPlainValue(weeklyStats.WeekKey)",
		"sectDailyTaskRewardContributionText = strconv.Itoa(sectDailyTaskRewardContribution)",
		"sectDailyTaskRewardPrestigeText = strconv.Itoa(sectDailyTaskRewardPrestige)",
		"每日奖励：个人贡献 +%s，本周贡献 +%s，宗门声望 +%s",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect task reward display guard missing %q", want)
		}
	}
	if strings.Contains(block, "getSectDailyTaskRewards(member.SectID)") {
		t.Fatal("sect task panel reward display still ignores technology read errors")
	}
	if strings.Contains(block, "log.Printf(\"\u95c1") || strings.Contains(block, "log.Printf(\"\u95c2") {
		t.Fatal("sect task panel diagnostics contain mojibake")
	}
}

func TestTransferSectOwnerChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleTransferSectOwner(")
	if start < 0 {
		t.Fatal("handleTransferSectOwner missing")
	}
	end := strings.Index(text[start:], "func handleSectContributionRank(")
	if end < 0 {
		t.Fatal("handleTransferSectOwner boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"ownerRes := tx.Model(&SectMember{})",
		"ownerRes.RowsAffected == 0",
		"SECT_TRANSFER_OWNER_ROLE_UPDATE_MISSED",
		"targetRes := tx.Model(&SectMember{})",
		"targetRes.RowsAffected == 0",
		"SECT_TRANSFER_TARGET_ROLE_UPDATE_MISSED",
		"sectRes := tx.Model(&Sect{})",
		"sectRes.RowsAffected == 0",
		"SECT_TRANSFER_SECT_OWNER_UPDATE_MISSED",
		"目标用户 ID 格式错误。",
		"不能将宗主之位转让给自己。",
		"你尚未加入宗门，无法转让宗主。",
		"只有宗主可以转让宗主之位。",
		"目标成员不在你的宗门中。",
		"宗门宗主转让失败",
		"宗主转让失败，请稍后再试。",
		"formatPlainError(err)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("transfer sect owner rows affected guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`tx.Model(&SectMember{}).Where("id = ?", owner.ID).Update("role", sectRoleMember).Error`,
		`tx.Model(&SectMember{}).Where("id = ?", target.ID).Update("role", sectRoleOwner).Error`,
		`return tx.Model(&Sect{}).`,
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("transfer sect owner still ignores RowsAffected: %s", unsafe)
		}
	}
	if strings.Contains(block, "log.Printf(\"\u95c1") {
		t.Fatal("transfer sect owner diagnostics contain mojibake")
	}
	if strings.Contains(block, "replyText(bot, chatID, \"\u95c1") ||
		strings.Contains(block, "replyText(bot, chatID, \"\u95c2") {
		t.Fatal("transfer sect owner copy contains mojibake")
	}
}

func TestAppointSectRoleChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleAppointSectRole(")
	if start < 0 {
		t.Fatal("handleAppointSectRole missing")
	}
	end := strings.Index(text[start:], "func handleTransferSectOwner(")
	if end < 0 {
		t.Fatal("handleAppointSectRole boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"if target.Role == targetRole",
		"Where(\"id = ? AND role = ?\", target.ID, target.Role)",
		"res.RowsAffected == 0",
		"SECT_APPOINT_ROLE_UPDATE_MISSED",
		"宗门职位任命失败",
		"formatPlainError(err)",
		"formatPlainValue(targetRole)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("appoint sect role rows affected guard missing %q", want)
		}
	}
	if strings.Contains(block, `Where("id = ?", target.ID).
			Update("role", targetRole).Error`) {
		t.Fatal("appoint sect role still ignores RowsAffected and concurrent role changes")
	}
	if strings.Contains(block, "log.Printf(\"\u95c1") {
		t.Fatal("appoint sect role diagnostics contain mojibake")
	}
}

func TestSectShopRenewChecksUserAndClaimRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleSectShop(")
	if start < 0 {
		t.Fatal("handleSectShop missing")
	}
	end := strings.Index(text[start:], "func handleSectRenewError(")
	if end < 0 {
		t.Fatal("handleSectShop renew boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"userRes := tx.Model(&User{})",
		`Where("id = ? AND abs_user_id = ? AND is_whitelist = ? AND expire_at IS NOT NULL"`,
		"userRes.RowsAffected == 0",
		"SECT_SHOP_RENEW_USER_STATE_CHANGED",
		"claimRes := tx.Model(&SectShopRenewClaim{})",
		`Where("id = ? AND purchase_id = ?", claim.ID, 0)`,
		"claimRes.RowsAffected == 0",
		"SECT_SHOP_RENEW_CLAIM_STATE_CHANGED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect shop renew rows affected guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`tx.Model(&lockedUser).Update("expire_at", newExpireAt).Error`,
		`UpdateColumn("purchase_id", purchase.ID).Error`,
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("sect shop renew still ignores RowsAffected: %s", unsafe)
		}
	}
}

func TestSectShopRenewClaimUsesPartialUniqueIndexes(t *testing.T) {
	dbSource, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go err = %v", err)
	}
	dbText := string(dbSource)

	modelStart := strings.Index(dbText, "type SectShopRenewClaim struct")
	if modelStart < 0 {
		t.Fatal("SectShopRenewClaim model missing")
	}
	modelEnd := strings.Index(dbText[modelStart:], "func (SectShopRenewClaim) TableName()")
	if modelEnd < 0 {
		t.Fatal("SectShopRenewClaim model boundary missing")
	}
	modelBlock := dbText[modelStart : modelStart+modelEnd]
	if strings.Contains(modelBlock, "uniqueIndex:idx_sect_shop_renew_claim") {
		t.Fatal("SectShopRenewClaim model should not create full unique indexes through AutoMigrate")
	}

	start := strings.Index(dbText, `assertNoDuplicateGroups("sect_shop_renew_claims(sect_id, month_key, slot_no)"`)
	if start < 0 {
		t.Fatal("sect shop renew claim migration block missing")
	}
	end := strings.Index(dbText[start:], `assertNoDuplicateGroups("red_packet_grabs(packet_id, user_id)"`)
	if end < 0 {
		t.Fatal("sect shop renew claim migration block boundary missing")
	}
	block := dbText[start : start+end]
	for _, want := range []string{
		"FROM sect_shop_renew_claims",
		"WHERE deleted_at IS NULL",
		`assertNoDuplicateGroups("sect_shop_renew_claims(user_id, month_key)"`,
		"ensureSectShopRenewClaimPartialUniqueIndexes(DB)",
		"sect shop renew claim unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect shop renew claim migration block missing %q", want)
		}
	}

	helperStart := strings.Index(dbText, "func ensureSectShopRenewClaimPartialUniqueIndexes(")
	if helperStart < 0 {
		t.Fatal("ensureSectShopRenewClaimPartialUniqueIndexes missing")
	}
	helperEnd := strings.Index(dbText[helperStart:], "func ensureInventoryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sect shop renew claim partial index helper boundary missing")
	}
	helperBlock := dbText[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sect_shop_renew_claim_slot",
		"ON sect_shop_renew_claims(sect_id, month_key, slot_no)",
		"idx_sect_shop_renew_claim_user_month",
		"ON sect_shop_renew_claims(user_id, month_key)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect shop renew claim partial index helper missing %q", want)
		}
	}

	sectSource, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	sectText := string(sectSource)
	reserveStart := strings.Index(sectText, "func reserveSectShopRenewClaimTx(")
	if reserveStart < 0 {
		t.Fatal("reserveSectShopRenewClaimTx missing")
	}
	reserveEnd := strings.Index(sectText[reserveStart:], "func sectShopMemberTotalContributionTx(")
	if reserveEnd < 0 {
		t.Fatal("reserveSectShopRenewClaimTx boundary missing")
	}
	reserveBlock := sectText[reserveStart : reserveStart+reserveEnd]
	if !strings.Contains(reserveBlock, "res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&claim)") {
		t.Fatal("sect shop renew claim reservation should use untargeted ON CONFLICT DO NOTHING for partial unique indexes")
	}
	if strings.Contains(reserveBlock, "Columns: []clause.Column") {
		t.Fatal("sect shop renew claim reservation must not specify conflict columns for partial unique indexes")
	}
}

func TestSectShopRenewReactivateFailureAuditsAreChecked(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case text == "确认宗门七日续期":`)
	if start < 0 {
		t.Fatal("sect shop renew confirm branch missing")
	}
	end := strings.Index(text[start:], "func handleSectRenewError(")
	if end < 0 {
		t.Fatal("sect shop renew confirm boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`writeAuditLogInTx(DB, userID, "SECT_SHOP_RENEW_REACTIVATE_FAILED"`,
		`writeAuditLogInTx(DB, userID, "SECT_SHOP_RENEW_REACTIVATE_LOCAL_FAILED"`,
		"宗门七日续期 ABS 解封失败",
		"宗门七日续期 ABS 已解封但本地状态同步失败",
		"formatPlainValue(absUserID)",
		"formatPlainError(auditErr)",
		"notifySuperAdminsPlain(bot, fmt.Sprintf",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect shop renew checked failure audit guard missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`writeAuditLog(userID, "SECT_SHOP_RENEW_REACTIVATE_FAILED"`,
		`writeAuditLog(userID, "SECT_SHOP_RENEW_REACTIVATE_LOCAL_FAILED"`,
		" userID, absUserID, newExpireAt.Format(time.RFC3339)",
	} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("sect shop renew failure audit still uses unchecked helper %q", forbidden)
		}
	}
	reactivateStart := strings.Index(block, "if needReactivate {")
	if reactivateStart < 0 {
		t.Fatal("sect shop renew reactivation branch missing")
	}
	reactivateEnd := strings.Index(block[reactivateStart:], "\n\treplyText(bot, chatID, fmt.Sprintf(\n\t\t\"宗门 **%s** 七日续期成功。")
	if reactivateEnd < 0 {
		t.Fatal("sect shop renew reactivation branch boundary missing")
	}
	reactivateBlock := block[reactivateStart : reactivateStart+reactivateEnd]
	if strings.Contains(reactivateBlock, "log.Printf(\"\u95c1") {
		t.Fatal("sect shop renew reactivation diagnostics contain mojibake")
	}
}

func TestSectShopRenewActionsAreHighRiskAudits(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "var highRiskAuditActionSet = map[string]struct{}{")
	if start < 0 {
		t.Fatal("highRiskAuditActionSet missing")
	}
	end := strings.Index(text[start:], "\n}\n\nfunc highRiskAuditActions(")
	if end < 0 {
		t.Fatal("highRiskAuditActionSet boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`"SECT_SHOP_RENEW":`,
		`"SECT_SHOP_RENEW_REACTIVATE":`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect shop renew high-risk audit action missing %q", want)
		}
	}
}

func TestSectDiagnosticsUseSanitizedErrors(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainError(err)",
		"formatPlainError(tasksErr)",
		"formatPlainError(statsErr)",
		"formatPlainError(claimErr)",
		"formatPlainError(settlementErr)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sect diagnostics missing %q", want)
		}
	}
	rawErrFormat := string([]byte{'e', 'r', 'r', '=', '%', 'v'})
	if strings.Contains(text, rawErrFormat) {
		t.Fatal("sect diagnostics should not log raw error values")
	}
}

func TestSectMemberListDiagnosticsAreReadable(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)

	pageStart := strings.Index(text, "func handleSectMemberPageCallback(")
	if pageStart < 0 {
		t.Fatal("handleSectMemberPageCallback missing")
	}
	pageEnd := strings.Index(text[pageStart:], "func loadSectMemberListPage(")
	if pageEnd < 0 {
		t.Fatal("handleSectMemberPageCallback boundary missing")
	}
	pageBlock := text[pageStart : pageStart+pageEnd]
	if !strings.Contains(pageBlock, "宗门成员列表分页消息编辑失败") ||
		!strings.Contains(pageBlock, "formatTelegramSendError(err)") {
		t.Fatal("sect member page edit diagnostic should be readable and sanitized")
	}
	if strings.Contains(pageBlock, "\u7f02\u509a\u5039") {
		t.Fatal("sect member page edit diagnostic still contains mojibake")
	}

	sendStart := strings.Index(text, "func sendSectMemberListPage(")
	if sendStart < 0 {
		t.Fatal("sendSectMemberListPage missing")
	}
	sendEnd := strings.Index(text[sendStart:], "func formatSectMemberListPage(")
	if sendEnd < 0 {
		t.Fatal("sendSectMemberListPage boundary missing")
	}
	sendBlock := text[sendStart : sendStart+sendEnd]
	if !strings.Contains(sendBlock, "宗门成员列表发送失败") ||
		!strings.Contains(sendBlock, "formatTelegramSendError(err)") {
		t.Fatal("sect member list send diagnostic should be readable and sanitized")
	}
	if strings.Contains(sendBlock, "\u95c1\u544a\u7466") {
		t.Fatal("sect member list send diagnostic still contains mojibake")
	}
}

func TestSectReadOnlyPanelCopyIsReadable(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)

	checks := []struct {
		name  string
		start string
		end   string
		want  []string
	}{
		{
			name:  "handleMySect",
			start: "func handleMySect(",
			end:   "func handleSectRank(",
			want: []string{
				"你尚未加入宗门。",
				"宗门档案读取失败，请稍后再试。",
				"已达最高等级",
				"已解锁",
				"可解锁",
				"未开放",
			},
		},
		{
			name:  "handleSectRank",
			start: "func handleSectRank(",
			end:   "func handleSectMembers(",
			want: []string{
				"宗门排行榜读取失败，请稍后再试。",
				"暂无宗门上榜。",
				"宗门排行榜 Top 10",
				`medals := []string{"1", "2", "3"`,
			},
		},
		{
			name:  "handleSectMembers",
			start: "func handleSectMembers(",
			end:   "func handleSectMemberPageCallback(",
			want: []string{
				"你尚未加入宗门。",
				"宗门成员列表读取失败，请稍后再试。",
			},
		},
		{
			name:  "handleSectMemberPageCallback",
			start: "func handleSectMemberPageCallback(",
			end:   "func loadSectMemberListPage(",
			want: []string{
				"页码无效",
				"你尚未加入宗门",
				"成员列表读取失败",
				"分页刷新失败",
				"第 %d 页",
			},
		},
	}

	for _, tc := range checks {
		start := strings.Index(text, tc.start)
		if start < 0 {
			t.Fatalf("%s missing", tc.name)
		}
		end := strings.Index(text[start:], tc.end)
		if end < 0 {
			t.Fatalf("%s boundary missing", tc.name)
		}
		block := text[start : start+end]
		for _, want := range tc.want {
			if !strings.Contains(block, want) {
				t.Fatalf("%s readable copy missing %q", tc.name, want)
			}
		}
		for _, forbidden := range []string{
			"replyText(bot, msg.Chat.ID, \"\u95c1",
			"replyText(bot, msg.Chat.ID, \"\u95c2",
			"answerCallback(bot, cb.ID, \"\u95c1",
			"answerCallback(bot, cb.ID, \"\u95c2",
			"b.WriteString(\"\u95c1",
			"upgradeText := \"\u95c1",
		} {
			if strings.Contains(block, forbidden) {
				t.Fatalf("%s readable copy contains mojibake", tc.name)
			}
		}
	}
}

func TestSectContributionRankCopyIsReadable(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleSectContributionRank(")
	if start < 0 {
		t.Fatal("handleSectContributionRank missing")
	}
	end := strings.Index(text[start:], "func querySectContributionRankRows(")
	if end < 0 {
		t.Fatal("handleSectContributionRank boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"你尚未加入宗门，无法查看宗门贡献排行。",
		"宗门档案读取失败，请稍后再试。",
		"总贡献排行",
		"本周贡献排行",
		"总贡献",
		"本周贡献",
		"宗门贡献排行读取失败，请稍后再试。",
		"暂无宗门贡献排行数据。",
		"`%s` [%s] %s `%d`",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect contribution rank readable copy missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"replyText(bot, chatID, \"\u95c1",
		"replyText(bot, chatID, \"\u95c2",
		"title := \"\u95c1",
		"valueName := \"\u95c1",
		"b.WriteString(fmt.Sprintf(\"\u95c1",
	} {
		if strings.Contains(block, forbidden) {
			t.Fatal("sect contribution rank copy contains mojibake")
		}
	}
}

func TestFormatSectMemberListLineShowsRealm(t *testing.T) {
	member := SectMember{
		UserName:     "alice",
		Role:         sectRoleOwner,
		Contribution: 42,
	}

	line := formatSectMemberListLine(1, member, "【结丹中期】")
	for _, want := range []string{"alice", "【结丹中期】", "贡献", "42"} {
		if !strings.Contains(line, want) {
			t.Fatalf("member list line = %q, missing %q", line, want)
		}
	}

	// 无修为档案时应给出占位文案，而不是出现空的境界字段。
	fallback := formatSectMemberListLine(2, member, "")
	if !strings.Contains(fallback, "【尚未踏入仙途】") {
		t.Fatalf("member list line fallback = %q, missing placeholder realm", fallback)
	}
	if strings.Contains(fallback, "[]") || strings.Contains(fallback, "  贡献") {
		t.Fatalf("member list line fallback has empty realm slot: %q", fallback)
	}
}

func TestSectMemberRealmBatchLookupAvoidsNPlusOne(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)

	if !strings.Contains(text, "func loadSectMemberRealmNames(members []SectMember) map[int64]string") {
		t.Fatal("loadSectMemberRealmNames helper missing")
	}
	start := strings.Index(text, "func loadSectMemberRealmNames(")
	end := strings.Index(text[start:], "\nfunc ")
	if end < 0 {
		t.Fatal("loadSectMemberRealmNames boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"user_id IN ?",
		"GetRealmName(",
		"realmNames[cul.UserID]",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("loadSectMemberRealmNames missing %q", want)
		}
	}
	// 必须批量查询，绝不能在循环里逐人查库。
	if strings.Contains(block, "GetOrCreateCultivation(") {
		t.Fatal("loadSectMemberRealmNames should batch query, not call GetOrCreateCultivation per member")
	}
}
