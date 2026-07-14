package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestValidLotteryTitleRejectsUnsafeText(t *testing.T) {
	if !validLotteryTitle("端午福泽抽奖") {
		t.Fatal("valid lottery title should be accepted")
	}
	if validLotteryTitle("短") {
		t.Fatal("short lottery title should be rejected")
	}
	if validLotteryTitle(strings.Repeat("长", 61)) {
		t.Fatal("overlong lottery title should be rejected")
	}
	for _, title := range []string{
		"活动\n标题",
		"活动\t标题",
		"活动\x00标题",
		"活动\u2028标题",
		"活动\u2029标题",
	} {
		if validLotteryTitle(title) {
			t.Fatalf("unsafe lottery title accepted: %q", title)
		}
	}
}

func TestValidLotteryClaimCodeRejectsUnsafeText(t *testing.T) {
	if !validLotteryClaimCode("天道福泽888") {
		t.Fatal("valid claim code should be accepted")
	}
	if validLotteryClaimCode("短") {
		t.Fatal("short claim code should be rejected")
	}
	if validLotteryClaimCode(strings.Repeat("长", 41)) {
		t.Fatal("overlong claim code should be rejected")
	}
	for _, code := range []string{
		"暗号\n一",
		"暗号\t一",
		"暗号\x00一",
		"暗号\u2028一",
		"暗号\u2029一",
	} {
		if validLotteryClaimCode(code) {
			t.Fatalf("unsafe claim code accepted: %q", code)
		}
	}
}

func TestParseLotteryPrizeSpecsParsesSupportedPrizeTypes(t *testing.T) {
	specs, err := parseLotteryPrizeSpecs(strings.Join([]string{
		"积分 100 3",
		"续期 30 2",
		"邀请码 1",
		"丹药 聚灵丹 4",
	}, "\n"))
	if err != nil {
		t.Fatalf("parseLotteryPrizeSpecs() err = %v", err)
	}
	if len(specs) != 4 {
		t.Fatalf("spec count = %d, want 4", len(specs))
	}
	tests := []struct {
		idx         int
		prizeType   string
		amount      int
		quantity    int
		displayName string
	}{
		{idx: 0, prizeType: lotteryPrizePoints, amount: 100, quantity: 3, displayName: "100 积分"},
		{idx: 1, prizeType: lotteryPrizeRenew, amount: 30, quantity: 2, displayName: "30 天续期卡"},
		{idx: 2, prizeType: lotteryPrizeInvite, amount: 1, quantity: 1, displayName: "邀请码"},
		{idx: 3, prizeType: lotteryPrizePill, amount: 1, quantity: 4, displayName: "聚灵丹"},
	}
	for _, tt := range tests {
		got := specs[tt.idx]
		if got.PrizeType != tt.prizeType || got.Amount != tt.amount || got.Quantity != tt.quantity || got.DisplayName != tt.displayName {
			t.Fatalf("spec[%d] = %#v, want type=%s amount=%d quantity=%d display=%q", tt.idx, got, tt.prizeType, tt.amount, tt.quantity, tt.displayName)
		}
	}
}

func TestParseLotteryPrizeSpecsRejectsInvalidPrizes(t *testing.T) {
	tests := []string{
		"",
		"积分 0 1",
		"积分 100 0",
		"续期 0 1",
		"邀请码 101",
		"丹药 不存在的丹 1",
		"丹药 聚灵丹 0",
		"法宝 1 1",
	}
	for _, raw := range tests {
		if _, err := parseLotteryPrizeSpecs(raw); err == nil {
			t.Fatalf("parseLotteryPrizeSpecs(%q) expected error", raw)
		}
	}
}

func TestParseLotteryPrizeSpecsParsesManualAndCustomCode(t *testing.T) {
	specs, err := parseLotteryPrizeSpecs(strings.Join([]string{
		"人工 实体 纪念品 2",
		"卡密 Steam 礼品卡|AAAA-BBBB-CCCC",
		"自定义卡密 Steam 礼品卡|DDDD-EEEE-FFFF",
	}, "\n"))
	if err != nil {
		t.Fatalf("parseLotteryPrizeSpecs() err = %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("spec count = %d, want 3", len(specs))
	}
	manual := specs[0]
	if manual.PrizeType != lotteryPrizeManual || manual.Quantity != 2 || manual.DisplayName != "实体 纪念品" || manual.SecretValue != "" {
		t.Fatalf("manual spec = %#v", manual)
	}
	for i, wantCode := range []string{"AAAA-BBBB-CCCC", "DDDD-EEEE-FFFF"} {
		got := specs[i+1]
		if got.PrizeType != lotteryPrizeCustomCode || got.Quantity != 1 || got.DisplayName != "Steam 礼品卡" || got.SecretValue != wantCode {
			t.Fatalf("custom code spec[%d] = %#v", i, got)
		}
		if got.SecretPreview == "" || got.SecretPreview == wantCode {
			t.Fatalf("custom code preview[%d] is unsafe: %q", i, got.SecretPreview)
		}
	}
}

func TestParseLotteryPrizeSpecsRejectsUnsafeCustomPrizes(t *testing.T) {
	tests := []string{
		"人工 1",
		"人工 特殊奖品 0",
		"人工 特殊\n奖品 1",
		"卡密 Steam礼品卡",
		"卡密 |AAAA-BBBB",
		"卡密 Steam礼品卡|",
		"卡密 Steam礼品卡|AAAA\tBBBB",
		"卡密 Steam礼品卡|AAAA\n卡密 Steam礼品卡|AAAA",
		"卡密 Steam礼品卡|" + strings.Repeat("A", lotteryCustomCodeMaxRunes+1),
	}
	for _, raw := range tests {
		if _, err := parseLotteryPrizeSpecs(raw); err == nil {
			t.Fatalf("parseLotteryPrizeSpecs(%q) expected error", raw)
		}
	}
}

func TestLotteryCustomCodeEncryptionRoundTrip(t *testing.T) {
	oldConfig := AppConfig
	AppConfig = &Config{SecurityPepper: strings.Repeat("p", 64)}
	defer func() { AppConfig = oldConfig }()

	plain := "EXTERNAL-CODE-1234"
	encrypted, err := encryptLotteryCustomCode(plain)
	if err != nil {
		t.Fatalf("encryptLotteryCustomCode() err = %v", err)
	}
	if encrypted == plain || strings.Contains(encrypted, plain) || !strings.HasPrefix(encrypted, "gcm$") {
		t.Fatalf("encrypted custom code is unsafe: %q", encrypted)
	}
	got, err := decryptLotteryCustomCode(encrypted)
	if err != nil {
		t.Fatalf("decryptLotteryCustomCode() err = %v", err)
	}
	if got != plain {
		t.Fatalf("decryptLotteryCustomCode() = %q, want %q", got, plain)
	}
	if _, err := decryptLotteryClaimCode(encrypted); err == nil {
		t.Fatal("custom code ciphertext must not decrypt with claim-code domain key")
	}
}

func TestLotteryCreateConfirmDoesNotExposeCustomCode(t *testing.T) {
	session := &SessionState{}
	session.SetTemp("lottery_mode", lotteryModeCount)
	session.SetTemp("lottery_limit", "10")
	session.SetTemp("lottery_entry_cost", "0")
	session.SetTemp("lottery_claim_code", "claim-code")

	text := formatLotteryCreateConfirm(session, []lotteryPrizeSpec{{
		PrizeType:     lotteryPrizeCustomCode,
		Quantity:      1,
		DisplayName:   "External gift card",
		SecretValue:   "SECRET-123",
		SecretPreview: "SE***23",
	}})
	if strings.Contains(text, "SECRET-123") {
		t.Fatal("lottery create confirmation must not expose a custom code")
	}
}

func TestLotteryManualAndCustomPrizeDisplayText(t *testing.T) {
	if got := lotteryPrizeDisplayText(lotteryPrizeManual, 1, "实体\n纪念品"); got != "人工奖品【实体 纪念品】" {
		t.Fatalf("manual prize display = %q", got)
	}
	if got := lotteryPrizeDisplayText(lotteryPrizeCustomCode, 1, "Steam\t礼品卡"); got != "自定义卡密【Steam 礼品卡】" {
		t.Fatalf("custom code display = %q", got)
	}
}
func TestLotteryDisplayTextNormalizesUnsafeSeparators(t *testing.T) {
	got := lotteryDisplayText("  活动\n名\tA\u2028B\u2029C\x00D  ", 20, "-")
	if got != "活动 名 A B C D" {
		t.Fatalf("lotteryDisplayText normalized = %q", got)
	}
	if got := lotteryDisplayText("\x00\u2028\t", 20, "-"); got != "-" {
		t.Fatalf("lotteryDisplayText empty fallback = %q", got)
	}
}

func TestLotteryPointDescriptionTitleIsSingleLine(t *testing.T) {
	got := lotteryPointDescriptionTitle("  活动\n名\tA\u2028B\u2029C\x00D  ")
	if got != "活动 名 A B C D" {
		t.Fatalf("lotteryPointDescriptionTitle = %q", got)
	}
}

func TestLotteryPrizeDisplayTextSanitizesPillDisplayName(t *testing.T) {
	got := lotteryPrizeDisplayText(lotteryPrizePill, 1, "聚灵丹\nA\u2028B")
	if got != "丹药【聚灵丹 A B】" {
		t.Fatalf("lotteryPrizeDisplayText pill = %q", got)
	}
	if got := lotteryPrizeDisplayText(lotteryPrizePill, 1, "\x00"); got != "丹药【丹药】" {
		t.Fatalf("lotteryPrizeDisplayText empty pill = %q", got)
	}
}

func TestLotteryStatusTextUsesStableFallback(t *testing.T) {
	if got := lotteryStatusText("unexpected\nstatus"); got != "未知状态" {
		t.Fatalf("lotteryStatusText unknown = %q", got)
	}
}

func TestLotteryCustomPrizeSourceGuards(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	for _, required := range []string{
		"SecretEncrypted string",
		"specs[i].SecretEncrypted, err = encryptLotteryCustomCode(specs[i].SecretValue)",
		"specs[i].SecretValue = \"\"",
		"SecretEncrypted: spec.SecretEncrypted",
		"code, err := decryptLotteryCustomCode(prize.SecretEncrypted)",
		"该奖品由管理员人工派发，请联系管理员领奖。",
		"LOTTERY_CUSTOM_CODE_PRIZE_INVALID",
		"LOTTERY_PRIZE_UNEXPECTED_SECRET",
	} {
		if !strings.Contains(text, required) {
			t.Fatalf("custom prize guard missing %q", required)
		}
	}
}
func TestLotteryParticipantCountErrorsAreVisible(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	if !strings.Contains(text, "func countLotteryParticipants(activityID uint) (int64, error)") {
		t.Fatalf("participant count helper must return an error")
	}
	if strings.Contains(text, "fmt.Sprintf(\"参与人数：%d\\n\", countLotteryParticipants(activity.ID))") {
		t.Fatalf("lottery detail still prints unchecked participant count")
	}
	if !strings.Contains(text, "参与人数：读取失败") || !strings.Contains(text, "进度：读取失败") {
		t.Fatalf("lottery views should expose participant count read failures")
	}
}

func TestLotteryDetailPrizeAndWinnerQueriesHandleReadErrors(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleLotteryDetailCommand(")
	if start < 0 {
		t.Fatal("handleLotteryDetailCommand missing")
	}
	end := strings.Index(text[start:], "func handleForceDrawLotteryCommand(")
	if end < 0 {
		t.Fatal("handleLotteryDetailCommand boundary missing")
	}
	fn := text[start : start+end]
	for _, want := range []string{
		"DB.First(&activity, uint(id)).Error",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"lottery detail activity read failed",
		"formatPlainError(err)",
		"Find(&prizes).Error",
		"Find(&winners).Error",
		"抽奖详情读取奖品失败",
		"抽奖详情读取中奖记录失败",
		"奖品记录不可用",
		"中奖记录不可用",
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("lottery detail read guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"DB.Where(\"activity_id = ? AND deleted_at IS NULL\", activity.ID).Find(&prizes)\n",
		"DB.Where(\"activity_id = ? AND deleted_at IS NULL\", activity.ID).Order(\"id asc\").Find(&winners)\n",
	} {
		if strings.Contains(fn, unsafe) {
			t.Fatalf("lottery detail query still ignores DB errors")
		}
	}
}

func TestClaimLotteryWinnerClaimsStatusBeforeReward(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func claimLotteryWinner(")
	if start < 0 {
		t.Fatal("claimLotteryWinner missing")
	}
	end := strings.Index(text[start:], "func getLotteryPrizeDisplayNameInTx(")
	if end < 0 {
		t.Fatal("claimLotteryWinner boundary missing")
	}
	fn := text[start : start+end]
	if strings.Contains(fn, "tx.Save(&current)") {
		t.Fatal("claimLotteryWinner should not use full-row Save for claim status")
	}
	markIdx := strings.Index(fn, "LOTTERY_WINNER_CLAIM_MARK_MISSED")
	rewardIdx := strings.Index(fn, "applyPointDeltaInTx(")
	if markIdx < 0 {
		t.Fatal("claim status update must check RowsAffected")
	}
	if rewardIdx < 0 {
		t.Fatal("point reward path missing")
	}
	if markIdx > rewardIdx {
		t.Fatal("claim status should be conditionally marked before rewards are issued")
	}
	if !strings.Contains(fn, "LOTTERY_WINNER_PREVIEW_UPDATE_MISSED") {
		t.Fatal("code prize preview update must check RowsAffected")
	}
}

func TestClaimLotteryPrizeByCodeContinuesPastHandledWinners(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func claimLotteryPrizeByCode(")
	if start < 0 {
		t.Fatal("claimLotteryPrizeByCode missing")
	}
	end := strings.Index(text[start:], "func claimLotteryWinner(")
	if end < 0 {
		t.Fatal("claimLotteryPrizeByCode boundary missing")
	}
	fn := text[start : start+end]
	for _, want := range []string{
		`fallbackClaimMessage := ""`,
		`if fallbackClaimMessage != ""`,
		`sendPlainText(bot, chatID, fallbackClaimMessage)`,
		`reply, err := claimLotteryWinner(activity, winner)`,
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("claim by code fallback guard missing %q", want)
		}
	}

	branches := []struct {
		name  string
		start string
		end   string
	}{
		{
			name:  "claimed",
			start: "if winner.Status == lotteryWinnerClaimed {",
			end:   "if winner.Status != lotteryWinnerPending {",
		},
		{
			name:  "not pending",
			start: "if winner.Status != lotteryWinnerPending {",
			end:   "if lotteryWinnerShouldExpire(activity, winner, time.Now()) {",
		},
		{
			name:  "expired",
			start: "if lotteryWinnerShouldExpire(activity, winner, time.Now()) {",
			end:   "reply, err := claimLotteryWinner(activity, winner)",
		},
	}
	for _, branch := range branches {
		branchStart := strings.Index(fn, branch.start)
		if branchStart < 0 {
			t.Fatalf("%s branch missing", branch.name)
		}
		branchEnd := strings.Index(fn[branchStart:], branch.end)
		if branchEnd < 0 {
			t.Fatalf("%s branch boundary missing", branch.name)
		}
		block := fn[branchStart : branchStart+branchEnd]
		if !strings.Contains(block, "fallbackClaimMessage") || !strings.Contains(block, "continue") {
			t.Fatalf("%s branch should record fallback and continue scanning matching activities", branch.name)
		}
		if strings.Contains(block, "return true") {
			t.Fatalf("%s branch should not return before later matching activities are checked", branch.name)
		}
	}
}

func TestLotteryClaimLogCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createLotteryClaimLogInTx(")
	if start < 0 {
		t.Fatal("createLotteryClaimLogInTx missing")
	}
	end := strings.Index(text[start:], "type lotteryPrizeSpec struct")
	if end < 0 {
		t.Fatal("createLotteryClaimLogInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"entry := *logEntry",
		"entry.Action = formatPlainValue(entry.Action)",
		"entry.Detail = formatPlainValue(entry.Detail)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"LOTTERY_CLAIM_LOG_CREATE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("lottery claim log helper guard missing %q", want)
		}
	}
	if strings.Contains(text, "tx.Create(&LotteryClaimLog{") {
		t.Fatal("lottery claim logs should use createLotteryClaimLogInTx")
	}
	for _, want := range []string{
		"return createLotteryClaimLogInTx(tx, &LotteryClaimLog{",
		"return true, createLotteryClaimLogInTx(tx, &LotteryClaimLog{",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("lottery claim log helper call missing %q", want)
		}
	}
}

func TestLotteryClaimLockReadErrorsFailClosed(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func getLotteryClaimLockMessage(")
	if start < 0 {
		t.Fatal("getLotteryClaimLockMessage missing")
	}
	end := strings.Index(text[start:], "func recordLotteryClaimFailure(")
	if end < 0 {
		t.Fatal("getLotteryClaimLockMessage boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"lottery claim lock read failed",
		"formatPlainError(err)",
		`return true, "抽奖领奖安全状态读取失败，请稍后再试。"`,
		"return false, \"\"",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("lottery claim lock read guard missing %q", want)
		}
	}
	if strings.Contains(block, `if err := DB.Where("user_id = ? AND purpose = ?", userID, lotteryClaimAttemptPurpose).First(&attempt).Error; err != nil {
		return false, ""
	}`) {
		t.Fatal("lottery claim lock read errors are still treated as no lock")
	}
}

func TestLotteryCoreCreatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		signature string
		end       string
		markers   []string
	}{
		{
			name:      "activity",
			signature: "func createLotteryActivityInTx(",
			end:       "func createLotteryPrizeInTx(",
			markers: []string{
				"entry := *activity",
				"entry.Title = lotteryDisplayText(entry.Title, 80, \"-\")",
				"entry.Mode = formatPlainValue(entry.Mode)",
				"entry.Status = formatPlainValue(entry.Status)",
				"entry.CreatedBy = formatPlainValue(entry.CreatedBy)",
				"entry.ClaimCodePreview = formatPlainValue(entry.ClaimCodePreview)",
				"res := tx.Create(&entry)",
				"res.Error != nil",
				"res.RowsAffected == 0",
				"LOTTERY_ACTIVITY_CREATE_MISSED",
				"*activity = entry",
			},
		},
		{
			name:      "prize",
			signature: "func createLotteryPrizeInTx(",
			end:       "func createLotteryParticipantInTx(",
			markers: []string{
				"entry := *prize",
				"entry.PrizeType = formatPlainValue(entry.PrizeType)",
				"entry.DisplayName = lotteryDisplayText(entry.DisplayName, 80, \"\")",
				"res := tx.Create(&entry)",
				"res.Error != nil",
				"res.RowsAffected == 0",
				"LOTTERY_PRIZE_CREATE_MISSED",
				"*prize = entry",
			},
		},
		{
			name:      "participant",
			signature: "func createLotteryParticipantInTx(",
			end:       "func createLotteryWinnerInTx(",
			markers: []string{
				"entry := *participant",
				"entry.UserName = formatPlainValue(entry.UserName)",
				"res := tx.Create(&entry)",
				"res.Error != nil",
				"isUniqueConstraintError(res.Error)",
				"errLotteryAlreadyJoined",
				"res.RowsAffected == 0",
				"LOTTERY_PARTICIPANT_CREATE_MISSED",
				"*participant = entry",
			},
		},
		{
			name:      "winner",
			signature: "func createLotteryWinnerInTx(",
			end:       "type lotteryPrizeSpec struct",
			markers: []string{
				"entry := *winner",
				"entry.UserName = formatPlainValue(entry.UserName)",
				"entry.PrizeType = formatPlainValue(entry.PrizeType)",
				"entry.Status = formatPlainValue(entry.Status)",
				"res := tx.Create(&entry)",
				"res.Error != nil",
				"isUniqueConstraintError(res.Error)",
				"return false, nil",
				"res.RowsAffected == 0",
				"LOTTERY_WINNER_CREATE_MISSED",
				"*winner = entry",
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

	createStart := strings.Index(text, "func createLotteryActivityFromSession(")
	if createStart < 0 {
		t.Fatal("createLotteryActivityFromSession missing")
	}
	createEnd := strings.Index(text[createStart:], "func showActiveLotteries(")
	if createEnd < 0 {
		t.Fatal("createLotteryActivityFromSession boundary missing")
	}
	createBlock := text[createStart : createStart+createEnd]
	for _, want := range []string{
		"createLotteryActivityInTx(tx, &activity)",
		"createLotteryPrizeInTx(tx, &LotteryPrize{",
	} {
		if !strings.Contains(createBlock, want) {
			t.Fatalf("lottery create path missing helper %q", want)
		}
	}

	joinStart := strings.Index(text, "func joinLotteryActivity(")
	if joinStart < 0 {
		t.Fatal("joinLotteryActivity missing")
	}
	joinEnd := strings.Index(text[joinStart:], "func drawLotteryActivity(")
	if joinEnd < 0 {
		t.Fatal("joinLotteryActivity boundary missing")
	}
	joinBlock := text[joinStart : joinStart+joinEnd]
	if !strings.Contains(joinBlock, "createLotteryParticipantInTx(tx, &participant)") {
		t.Fatal("lottery join path should use createLotteryParticipantInTx")
	}

	drawStart := strings.Index(text, "func drawLotteryActivity(")
	if drawStart < 0 {
		t.Fatal("drawLotteryActivity missing")
	}
	drawEnd := strings.Index(text[drawStart:], "func lotteryClaimDuration(")
	if drawEnd < 0 {
		t.Fatal("drawLotteryActivity boundary missing")
	}
	drawBlock := text[drawStart : drawStart+drawEnd]
	for _, want := range []string{
		"created, err := createLotteryWinnerInTx(tx, &winner)",
		"if !created",
	} {
		if !strings.Contains(drawBlock, want) {
			t.Fatalf("lottery draw path missing helper behavior %q", want)
		}
	}

	for _, unsafe := range []string{
		"tx.Create(&activity).Error",
		"tx.Create(&LotteryPrize{",
		"tx.Create(&LotteryParticipant{",
		"tx.Create(&LotteryWinner{",
	} {
		if strings.Contains(createBlock, unsafe) || strings.Contains(joinBlock, unsafe) || strings.Contains(drawBlock, unsafe) {
			t.Fatalf("lottery core create still ignores RowsAffected: %s", unsafe)
		}
	}
}

func TestLotteryEntryMigrationsReplaceFullUniqueIndexes(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("lottery_participants(activity_id, user_id)"`)
	if start < 0 {
		t.Fatal("lottery entry migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("garden_plots(user_id, plot_no)"`)
	if end < 0 {
		t.Fatal("lottery entry migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM lottery_participants",
		"FROM lottery_winners",
		"WHERE deleted_at IS NULL",
		"ensureLotteryEntryPartialUniqueIndexes(DB)",
		"lottery entry unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("lottery entry migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureLotteryEntryPartialUniqueIndexes(")
	if helperStart < 0 {
		t.Fatal("ensureLotteryEntryPartialUniqueIndexes missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureInventoryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("lottery entry partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_lottery_participants_activity_user_unique",
		"ON lottery_participants(activity_id, user_id)",
		"idx_lottery_winners_activity_user_unique",
		"ON lottery_winners(activity_id, user_id)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("lottery entry partial index helper missing %q", want)
		}
	}
}

func TestLotteryLocalUserCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)

	start := strings.Index(text, "func createLotteryLocalUserIfMissingInTx(")
	if start < 0 {
		t.Fatal("createLotteryLocalUserIfMissingInTx missing")
	}
	end := strings.Index(text[start:], "type lotteryPrizeSpec struct")
	if end < 0 {
		t.Fatal("createLotteryLocalUserIfMissingInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"LOTTERY_LOCAL_USER_INVALID",
		"entry := *user",
		"entry.Username = formatPlainValue(entry.Username)",
		"res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"return nil",
		"*user = entry",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("lottery local user helper guard missing %q", want)
		}
	}
	if strings.Contains(block, "}).Error") {
		t.Fatal("lottery local user helper still checks only create error")
	}

	joinStart := strings.Index(text, "func joinLotteryActivity(")
	if joinStart < 0 {
		t.Fatal("joinLotteryActivity missing")
	}
	joinEnd := strings.Index(text[joinStart:], "func drawLotteryActivity(")
	if joinEnd < 0 {
		t.Fatal("joinLotteryActivity boundary missing")
	}
	joinBlock := text[joinStart : joinStart+joinEnd]
	if !strings.Contains(joinBlock, "createLotteryLocalUserIfMissingInTx(tx, &existingUser)") {
		t.Fatal("lottery join should create local user through helper")
	}
	if strings.Contains(joinBlock, "tx.Create(&existingUser).Error") {
		t.Fatal("lottery join still creates local user without RowsAffected guard")
	}
}

func TestLotteryInventoryGrantChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func grantLotteryInventoryItemInTx(")
	if start < 0 {
		t.Fatal("grantLotteryInventoryItemInTx missing")
	}
	end := strings.Index(text[start:], "func sendLotteryPlainText(")
	if end < 0 {
		t.Fatal("grantLotteryInventoryItemInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := tx.Clauses(inventoryQuantityUpsertClause(quantity)).Create(&Inventory{",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"LOTTERY_INVENTORY_GRANT_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("lottery inventory grant guard missing %q", want)
		}
	}
	if strings.Contains(block, "}).Error") {
		t.Fatal("lottery inventory grant still checks only create error")
	}
}

func TestCreateLotteryAuditIsTransactional(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createLotteryActivityFromSession(")
	if start < 0 {
		t.Fatal("createLotteryActivityFromSession missing")
	}
	end := strings.Index(text[start:], "func showActiveLotteries(")
	if end < 0 {
		t.Fatal("createLotteryActivityFromSession boundary missing")
	}
	fn := text[start : start+end]
	for _, want := range []string{
		"err = DB.Transaction(func(tx *gorm.DB) error {",
		"createLotteryActivityInTx(tx, &activity)",
		"createLotteryPrizeInTx(tx, &LotteryPrize{",
		`writeAuditLogInTx(tx, msg.From.ID, "CREATE_LOTTERY"`,
		"formatPlainValue(activity.Title)",
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("create lottery transactional audit guard missing %q", want)
		}
	}
	if strings.Contains(fn, `writeAuditLog(msg.From.ID, "CREATE_LOTTERY"`) {
		t.Fatal("create lottery audit still uses unchecked helper")
	}
	if strings.Contains(fn, "，标题：%s\", activity.Title") {
		t.Fatal("create lottery audit detail still uses raw activity title")
	}

	confirmStart := strings.Index(text, `case "WAITING_LOTTERY_CONFIRM":`)
	if confirmStart < 0 {
		t.Fatal("lottery confirm state missing")
	}
	confirmEnd := strings.Index(text[confirmStart:], "func validLotteryTitle(")
	if confirmEnd < 0 {
		t.Fatal("lottery confirm boundary missing")
	}
	confirmBlock := text[confirmStart : confirmStart+confirmEnd]
	if strings.Contains(confirmBlock, `writeAuditLog(userID, "CREATE_LOTTERY"`) {
		t.Fatal("create lottery confirm path writes audit after transaction")
	}
}

func TestLotterySessionNumericParsersFailClosed(t *testing.T) {
	session := &SessionState{}

	session.SetTemp("lottery_entry_cost", "abc")
	if _, err := parseLotterySessionInt(session, "lottery_entry_cost", 0, 10000); err == nil {
		t.Fatal("invalid lottery entry cost should fail closed")
	}

	session.SetTemp("lottery_entry_cost", "10001")
	if _, err := parseLotterySessionInt(session, "lottery_entry_cost", 0, 10000); err == nil {
		t.Fatal("out-of-range lottery entry cost should fail closed")
	}

	session.SetTemp("lottery_entry_cost", "100")
	if got, err := parseLotterySessionInt(session, "lottery_entry_cost", 0, 10000); err != nil || got != 100 {
		t.Fatalf("valid lottery entry cost parse failed: got=%d err=%v", got, err)
	}

	now := time.Unix(1000, 0)
	session.SetTemp("lottery_draw_at", "bad")
	if _, err := parseLotterySessionUnixTime(session, "lottery_draw_at", now); err == nil {
		t.Fatal("invalid lottery draw time should fail closed")
	}

	session.SetTemp("lottery_draw_at", "1000")
	if _, err := parseLotterySessionUnixTime(session, "lottery_draw_at", now); err == nil {
		t.Fatal("non-future lottery draw time should fail closed")
	}

	session.SetTemp("lottery_draw_at", "1001")
	if got, err := parseLotterySessionUnixTime(session, "lottery_draw_at", now); err != nil || !got.Equal(time.Unix(1001, 0)) {
		t.Fatalf("valid lottery draw time parse failed: got=%s err=%v", got, err)
	}
}

func TestLotteryCreateSessionValuesDoNotIgnoreParseErrors(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func createLotteryActivityFromSession(")
	if start < 0 {
		t.Fatal("createLotteryActivityFromSession missing")
	}
	end := strings.Index(text[start:], "func showActiveLotteries(")
	if end < 0 {
		t.Fatal("createLotteryActivityFromSession boundary missing")
	}
	fn := text[start : start+end]
	for _, want := range []string{
		`parseLotterySessionInt(session, "lottery_entry_cost", 0, 10000)`,
		`parseLotterySessionInt(session, "lottery_limit", 2, 100000)`,
		`parseLotterySessionUnixTime(session, "lottery_draw_at", time.Now())`,
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("create lottery session parse guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`entryCost, _ := strconv.Atoi(session.GetTemp("lottery_entry_cost"))`,
		`limit, _ := strconv.Atoi(session.GetTemp("lottery_limit"))`,
		`drawAtUnix, _ := strconv.ParseInt(session.GetTemp("lottery_draw_at"), 10, 64)`,
	} {
		if strings.Contains(fn, unsafe) {
			t.Fatalf("create lottery session value still ignores parse errors: %q", unsafe)
		}
	}
}

func TestForceDrawLotteryAuditIsTransactional(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func drawLotteryActivity(")
	if start < 0 {
		t.Fatal("drawLotteryActivity missing")
	}
	end := strings.Index(text[start:], "func lotteryClaimDuration(")
	if end < 0 {
		t.Fatal("drawLotteryActivity boundary missing")
	}
	fn := text[start : start+end]
	for _, want := range []string{
		"func drawLotteryActivity(bot *tgbotapi.BotAPI, activityID uint, reason string, actorID int64)",
		"LOTTERY_DRAW_CLOSE_UPDATE_MISSED",
		"LOTTERY_DRAW_RESULT_UPDATE_MISSED",
		"formatPlainValue(reason), participantCount, winnerCount",
		`reason == "manual" && actorID != 0`,
		`writeAuditLogInTx(tx, actorID, "FORCE_DRAW_LOTTERY"`,
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("force draw transactional audit guard missing %q", want)
		}
	}
	if strings.Contains(fn, "reason, participantCount, winnerCount") {
		t.Fatal("lottery draw result note should not persist raw reason")
	}

	handlerStart := strings.Index(text, "func handleForceDrawLotteryCommand(")
	if handlerStart < 0 {
		t.Fatal("handleForceDrawLotteryCommand missing")
	}
	handlerEnd := strings.Index(text[handlerStart:], "func handleCancelLotteryCommand(")
	if handlerEnd < 0 {
		t.Fatal("handleForceDrawLotteryCommand boundary missing")
	}
	handlerBlock := text[handlerStart : handlerStart+handlerEnd]
	for _, want := range []string{
		`drawLotteryActivity(bot, uint(id), "manual", actorID)`,
	} {
		if !strings.Contains(handlerBlock, want) {
			t.Fatalf("force draw handler guard missing %q", want)
		}
	}
	if strings.Contains(handlerBlock, `writeAuditLog(actorID, "FORCE_DRAW_LOTTERY"`) {
		t.Fatal("force draw handler still writes audit after draw transaction")
	}
}

func TestCancelLotteryRefundMarksParticipantBeforeRefund(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func cancelLotteryActivityWithFullRefund(")
	if start < 0 {
		t.Fatal("cancelLotteryActivityWithFullRefund missing")
	}
	end := strings.Index(text[start:], "func parseTrailingUint(")
	if end < 0 {
		t.Fatal("cancelLotteryActivityWithFullRefund boundary missing")
	}
	fn := text[start : start+end]
	markIdx := strings.Index(fn, "LOTTERY_CANCEL_REFUND_MARK_MISSED")
	refundIdx := strings.Index(fn, "\"lottery_cancel_refund\"")
	if markIdx < 0 {
		t.Fatal("refund participant update must check RowsAffected")
	}
	if refundIdx < 0 {
		t.Fatal("refund point transaction path missing")
	}
	if markIdx > refundIdx {
		t.Fatal("participant refund marker should be conditionally claimed before points are refunded")
	}
	if !strings.Contains(fn, "LOTTERY_CANCEL_TOTAL_REFUND_UPDATE_MISSED") {
		t.Fatal("activity total refund update must check RowsAffected")
	}
	for _, want := range []string{
		"func cancelLotteryActivityWithFullRefund(activityID uint, actorID int64)",
		"txRefundTotal := 0",
		"txRefundTotal = 0",
		"txRefundTotal += participant.EntryCost",
		`"total_refund_points": txRefundTotal`,
		`writeAuditLogInTx(tx, actorID, "CANCEL_LOTTERY"`,
		"txRefundTotal)); err != nil",
		"return txRefundTotal, nil",
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("cancel lottery transactional audit guard missing %q", want)
		}
	}
	for _, forbidden := range []string{
		`writeAuditLog(actorID, "CANCEL_LOTTERY"`,
		"refundTotal += participant.EntryCost",
		`"total_refund_points": refundTotal`,
		"return refundTotal, nil",
	} {
		if strings.Contains(fn, forbidden) {
			t.Fatalf("cancel lottery still exposes uncommitted refund result or unchecked audit %q", forbidden)
		}
	}
}

func TestLotteryJoinAnnounceChatUpdateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func joinLotteryActivity(")
	if start < 0 {
		t.Fatal("joinLotteryActivity missing")
	}
	end := strings.Index(text[start:], "func lotteryUniqueUsername(")
	if end < 0 {
		t.Fatal("joinLotteryActivity boundary missing")
	}
	fn := text[start : start+end]
	for _, want := range []string{
		`Where("id = ? AND status = ? AND announce_chat_id = 0", activityID, lotteryStatusActive)`,
		"res.RowsAffected == 0",
		`tx.Select("id", "status", "announce_chat_id").First(&current, activityID).Error`,
		"current.Status != lotteryStatusActive",
		"current.AnnounceChatID == 0",
		"LOTTERY_JOIN_ANNOUNCE_CHAT_UPDATE_MISSED",
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("lottery join announce chat update guard missing %q", want)
		}
	}
	if strings.Contains(fn, `Update("announce_chat_id", sourceChatID).Error`) {
		t.Fatal("lottery join announce chat update still ignores RowsAffected")
	}
}

func TestLotteryJoinTransactionalReturnValuesOnlyAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func joinLotteryActivity(")
	if start < 0 {
		t.Fatal("joinLotteryActivity missing")
	}
	end := strings.Index(text[start:], "func lotteryUniqueUsername(")
	if end < 0 {
		t.Fatal("joinLotteryActivity boundary missing")
	}
	fn := text[start : start+end]
	for _, want := range []string{
		"var txJoinedCount int64",
		"txShouldDraw := false",
		"Count(&currentCount).Error",
		"Count(&txJoinedCount).Error",
		"txShouldDraw = activity.Mode == lotteryModeCount && txJoinedCount >= int64(activity.ParticipantLimit)",
		"joinedCount = txJoinedCount",
		"shouldDraw = txShouldDraw",
		"if err != nil {\n\t\treturn 0, false, err\n\t}",
		"return joinedCount, shouldDraw, nil",
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("lottery join transactional return guard missing %q", want)
		}
	}
	if strings.Contains(fn, "return joinedCount, shouldDraw, err") {
		t.Fatal("lottery join still returns possibly rolled-back count/draw flag")
	}
}

func TestLotteryPinCleanupMarkerChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func unpinExpiredLotteryMessages(")
	if start < 0 {
		t.Fatal("unpinExpiredLotteryMessages missing")
	}
	fn := text[start:]
	for _, want := range []string{
		"res := DB.Model(&LotteryActivity{})",
		`Where("id = ? AND status IN ? AND claim_deadline_at IS NOT NULL AND claim_deadline_at <= ? AND pins_unpinned = ?", activity.ID, []string{lotteryStatusDrawn, lotteryStatusClosed}, now, false)`,
		`Update("pins_unpinned", true)`,
		"res.Error",
		"res.RowsAffected == 0",
		"抽奖置顶清理完成标记未命中",
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("lottery pin cleanup marker guard missing %q", want)
		}
	}
	if strings.Contains(fn, `Update("pins_unpinned", true).Error`) {
		t.Fatal("lottery pin cleanup marker still ignores RowsAffected")
	}
}

func TestLotteryAnnouncementMessageIDUpdatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
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
			name:      "intro announcement",
			startFunc: "func sendLotteryIntroAnnouncementSync(",
			endFunc:   "func handleJoinLotteryCommand(",
			markers: []string{
				"res := DB.Model(&LotteryActivity{})",
				`"intro_message_id": sentMsg.MessageID`,
				"res.Error != nil",
				"res.RowsAffected == 0",
				"lottery intro announcement record update missed",
			},
			forbidden: []string{
				`Updates(map[string]interface{}{
			"announce_chat_id": targetChatID,
			"intro_message_id": sentMsg.MessageID,
			"intro_pinned":     introPinned,
		}).Error`,
			},
		},
		{
			name:      "result announcement",
			startFunc: "func sendLotteryResultAnnouncementSync(",
			endFunc:   "func notifyLotteryWinnersPrivately(",
			markers: []string{
				"res := DB.Model(&LotteryActivity{})",
				`"result_message_id": sentMsg.MessageID`,
				"res.Error != nil",
				"res.RowsAffected == 0",
				"lottery result announcement record update missed",
			},
			forbidden: []string{
				`Updates(map[string]interface{}{
			"announce_chat_id":  targetChatID,
			"result_message_id": sentMsg.MessageID,
			"result_pinned":     resultPinned,
		}).Error`,
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
				t.Fatalf("%s still ignores RowsAffected", tt.name)
			}
		}
	}
}

func TestFusionPoolUpdateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	helperStart := strings.Index(text, "func createFusionPoolConfigIfMissingInTx(")
	if helperStart < 0 {
		t.Fatal("createFusionPoolConfigIfMissingInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func addPointsTo"+"FusionPoolInTx(")
	if helperEnd < 0 {
		t.Fatal("createFusionPoolConfigIfMissingInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"FUSION_POOL_CONFIG_INVALID",
		`cfg := SystemConfig{Key: "fusion_pool_points", Value: "0"}`,
		"res := tx.Clauses(systemConfigKeyDoNothingClause()).Create(&cfg)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"return nil",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("fusion pool config helper guard missing %q", want)
		}
	}
	if strings.Contains(helperBlock, "}).Error") {
		t.Fatal("fusion pool config helper still checks only create error")
	}

	start := strings.Index(text, "func addPointsTo"+"FusionPoolInTx(")
	if start < 0 {
		t.Fatal("addPointsToFusionPoolInTx missing")
	}
	end := strings.Index(text[start:], "func lotteryPrizeText(")
	if end < 0 {
		t.Fatal("addPointsToFusionPoolInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"createFusionPoolConfigIfMissingInTx(tx)",
		"poolRes := tx.Model(&SystemConfig{})",
		`Where("id = ? AND key = ?", poolCfg.ID, "fusion_pool_points")`,
		"poolRes.RowsAffected == 0",
		"FUSION_POOL_STATE_CHANGED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("fusion pool update rows affected guard missing %q", want)
		}
	}
	if strings.Contains(block, `tx.Model(&poolCfg).Update("value", fmt.Sprintf("%d", currentPool)).Error`) {
		t.Fatal("fusion pool update still ignores RowsAffected")
	}
	if strings.Contains(block, `Create(&SystemConfig{Key: "fusion_pool_points", Value: "0"}).Error`) {
		t.Fatal("fusion pool initialization still creates config without RowsAffected guard")
	}
}

func TestFusionPoolInvalidStoredValueFailsClosed(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func addPointsTo"+"FusionPoolInTx(")
	if start < 0 {
		t.Fatal("addPointsToFusionPoolInTx missing")
	}
	end := strings.Index(text[start:], "func lotteryPrizeText(")
	if end < 0 {
		t.Fatal("addPointsToFusionPoolInTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"currentPool, err := strconv.Atoi(strings.TrimSpace(poolCfg.Value))",
		`return 0, false, fmt.Errorf("invalid fusion pool points value=%s: %w", formatPlainValue(poolCfg.Value), err)`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("fusion pool invalid stored value fail-closed guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"已按 0 处理",
		"currentPool = 0",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("fusion pool invalid stored value still falls back to zero: %q", unsafe)
		}
	}
}

func TestLotteryDiagnosticsUseSanitizedErrors(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainError(err)",
		"formatPlainValue(poolCfg.Value)",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("lottery diagnostics missing %q", want)
		}
	}
	rawErrFormat := string([]byte{'e', 'r', 'r', '=', '%', 'v'})
	if strings.Contains(text, rawErrFormat) {
		t.Fatal("lottery diagnostics should not log raw error values")
	}
}

func TestLotteryPinDiagnosticsFormatLabel(t *testing.T) {
	source, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	text := string(source)
	for _, fn := range []string{"func pinLotteryMessage(", "func unpinLotteryMessage("} {
		start := strings.Index(text, fn)
		if start < 0 {
			t.Fatalf("%s missing", fn)
		}
		end := strings.Index(text[start+len(fn):], "\n}\n")
		if end < 0 {
			t.Fatalf("%s boundary missing", fn)
		}
		block := text[start : start+len(fn)+end]
		for _, want := range []string{
			"safeLabel := formatPlainValue(label)",
			"activityID, safeLabel, chatID, messageID",
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s should format label in diagnostics, missing %q", fn, want)
			}
		}
		for _, unsafe := range []string{
			"activityID, label, chatID, messageID",
			"activityID, label, chatID, messageID, formatPlainError(err)",
		} {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still uses raw label in diagnostics: %q", fn, unsafe)
			}
		}
	}
}
