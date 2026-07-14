package main

import (
	"os"
	"strings"
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

func TestReferralInviterMeetsCultivationRequirement(t *testing.T) {
	tests := []struct {
		name  string
		major int
		minor int
		want  bool
	}{
		{name: "mortal", major: 0, minor: 1, want: false},
		{name: "qi refining early", major: 1, minor: 1, want: true},
		{name: "qi refining later", major: 1, minor: 2, want: true},
		{name: "foundation", major: 2, minor: 1, want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := referralInviterMeetsCultivationRequirement(tt.major, tt.minor)
			if got != tt.want {
				t.Fatalf("referralInviterMeetsCultivationRequirement(%d, %d) = %v, want %v", tt.major, tt.minor, got, tt.want)
			}
		})
	}
}

func TestReferralStatsText(t *testing.T) {
	got := referralStatsText(referralStats{
		Activated:   12,
		Effective:   7,
		MonthReward: 80,
	})
	if got != "累计激活：`12`\n有效新人：`7`\n本月奖励：`80/150` 积分" {
		t.Fatalf("referralStatsText = %q", got)
	}
}

func TestReferralStatsUnavailableText(t *testing.T) {
	got := referralStatsUnavailableText()
	if got != "累计激活：读取失败\n有效新人：读取失败\n本月奖励：读取失败/150 积分" {
		t.Fatalf("referralStatsUnavailableText = %q", got)
	}
}

func TestReferralLinkEscapesStartPayload(t *testing.T) {
	bot := &tgbotapi.BotAPI{Self: tgbotapi.User{UserName: "MyBot"}}
	got := referralLink(bot, "ABC 123&next=bad")
	want := "https://t.me/MyBot?start=ref_ABC+123%26next%3Dbad"
	if got != want {
		t.Fatalf("referralLink escaped = %q, want %q", got, want)
	}

	if got := referralLink(nil, "ABC 123&next=bad"); got != "/start ref_ABC 123&next=bad" {
		t.Fatalf("referralLink fallback = %q", got)
	}

	source, err := os.ReadFile("referral.go")
	if err != nil {
		t.Fatalf("read referral.go err = %v", err)
	}
	text := string(source)
	if strings.Contains(text, `fmt.Sprintf("https://t.me/%s?start=%s%s", botName, referralStartPrefix, code)`) {
		t.Fatal("referral t.me link should not concatenate raw start payload")
	}
	if !strings.Contains(text, `url.QueryEscape(referralStartPrefix+code)`) {
		t.Fatal("referral t.me link should query-escape start payload")
	}
}

func TestCapReferralTrialTaskSeconds(t *testing.T) {
	start := time.Date(2026, 6, 23, 20, 0, 0, 0, time.FixedZone("CST", 8*3600))
	end := start.AddDate(0, 0, referralTrialDays)

	tests := []struct {
		name    string
		seconds float64
		now     time.Time
		want    float64
	}{
		{
			name:    "caps same day historical daily aggregate to elapsed wall clock",
			seconds: 10 * 3600,
			now:     start.Add(2 * time.Hour),
			want:    2 * 3600,
		},
		{
			name:    "keeps lower aggregate",
			seconds: 90 * 60,
			now:     start.Add(2 * time.Hour),
			want:    90 * 60,
		},
		{
			name:    "uses trial end when checking after end",
			seconds: float64((referralTrialDays + 2) * 24 * 3600),
			now:     end.Add(6 * time.Hour),
			want:    end.Sub(start).Seconds(),
		},
		{
			name:    "zero before start",
			seconds: 3600,
			now:     start.Add(-time.Minute),
			want:    0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := capReferralTrialTaskSeconds(tt.seconds, start, end, tt.now)
			if got != tt.want {
				t.Fatalf("capReferralTrialTaskSeconds = %.0f, want %.0f", got, tt.want)
			}
		})
	}
}

func TestReferralStartExistingAccountReadErrorStopsRegistration(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "parseReferralStartPayload(text)")
	if start < 0 {
		t.Fatal("referral start payload branch missing")
	}
	end := strings.Index(text[start:], "sendUserMainMenu(")
	if end < 0 {
		t.Fatal("referral start payload branch boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"telegram_id = ? AND abs_user_id <> ?",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"邀请链接预校验失败",
		"邀请链接暂时读取失败，请稍后再试。",
		"邀请链接注册读取本地正式账号失败",
		"formatPlainError(err)",
		"本地档案读取失败，请稍后再试",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("referral start existing-account guard missing %q", want)
		}
	}
	if strings.Contains(block, "session := getSession(userID)") &&
		!strings.Contains(block, "} else if !errors.Is(err, gorm.ErrRecordNotFound) {") {
		t.Fatal("referral start still treats existing-account read failures as no account")
	}
}

func TestEnsureReferralCodeReenableChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("referral.go")
	if err != nil {
		t.Fatalf("read referral.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func ensureReferralCode(")
	if start < 0 {
		t.Fatal("ensureReferralCode missing")
	}
	end := strings.Index(text[start:], "func validateReferralCodeForStart(")
	if end < 0 {
		t.Fatal("ensureReferralCode boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := tx.Model(&ReferralCode{})",
		`Where("id = ? AND user_id = ? AND is_enabled = ?", txCode.ID, userID, false)`,
		`Update("is_enabled", true)`,
		"res.RowsAffected == 0",
		"REFERRAL_CODE_STATE_CHANGED",
		"res := tx.Create(&txCode)",
		"res.Error == nil && res.RowsAffected > 0",
		"CREATE_REFERRAL_CODE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("ensureReferralCode re-enable rows affected guard missing %q", want)
		}
	}
	if strings.Contains(block, `tx.Model(&code).Update("is_enabled", true).Error`) {
		t.Fatal("ensureReferralCode still re-enables referral code without checking RowsAffected")
	}
	if strings.Contains(block, `tx.Create(&txCode).Error`) {
		t.Fatal("ensureReferralCode still creates referral code without checking RowsAffected")
	}
}

func TestReferralTransactionalReturnValuesOnlyAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("referral.go")
	if err != nil {
		t.Fatalf("read referral.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		wants     []string
		forbidden []string
	}{
		{
			name:      "ensure referral code",
			startFunc: "func ensureReferralCode(",
			endFunc:   "func validateReferralCodeForStart(",
			wants: []string{
				"var txCode ReferralCode",
				"code = txCode",
				"return ReferralCode{}, err",
				"return code, nil",
			},
			forbidden: []string{
				"return code, err",
			},
		},
		{
			name:      "trial formal conversion",
			startFunc: "func convertTrialToFormalWithInviteCode(",
			endFunc:   "func sumReferralTrialRawSeconds(",
			wants: []string{
				"var txNextExpireAt time.Time",
				"nextExpireAt = txNextExpireAt",
				"return time.Time{}, err",
				"return nextExpireAt, nil",
			},
			forbidden: []string{
				"return nextExpireAt, err",
			},
		},
		{
			name:      "trial task claim",
			startFunc: "func claimReferralTrialTask(",
			endFunc:   "func showMyReferral(",
			wants: []string{
				"txNewExpireAt := base.AddDate(0, 0, referralTrialDays)",
				"txRewardPoints := 0",
				"txRewardGranted := false",
				"newExpireAt = txNewExpireAt",
				"rewardPoints = txRewardPoints",
				"rewardGranted = txRewardGranted",
				"return rawSeconds, time.Time{}, 0, false, err",
				"return rawSeconds, newExpireAt, rewardPoints, rewardGranted, nil",
			},
			forbidden: []string{
				"return rawSeconds, newExpireAt, rewardPoints, rewardGranted, err",
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
		for _, want := range tt.wants {
			if !strings.Contains(block, want) {
				t.Fatalf("%s missing post-transaction return guard %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still exposes transactional intermediate value: %s", tt.name, unsafe)
			}
		}
	}
}

func TestReferralBusinessErrorsOnlyForMissingRecords(t *testing.T) {
	source, err := os.ReadFile("referral.go")
	if err != nil {
		t.Fatalf("read referral.go err = %v", err)
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
			name:      "ensure code user read",
			startFunc: "func ensureReferralCode(",
			endFunc:   "func validateReferralCodeForStart(",
			markers: []string{
				`tx.Where("telegram_id = ?", userID).First(&u).Error`,
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"return errUserNotFound",
				"return err",
			},
			forbidden: []string{
				`if err := tx.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			return errUserNotFound
		}`,
			},
		},
		{
			name:      "start referral code validation",
			startFunc: "func validateReferralCodeForStart(",
			endFunc:   "func createReferralTrialUserInTx(",
			markers: []string{
				`DB.Where("code = ? AND is_enabled = ?", strings.TrimSpace(code), true).First(&ref).Error`,
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"return errReferralInvalidCode",
				"return err",
			},
			forbidden: []string{
				`if err := DB.Where("code = ? AND is_enabled = ?", strings.TrimSpace(code), true).First(&ref).Error; err != nil {
		return errReferralInvalidCode
	}`,
			},
		},
		{
			name:      "trial account referral code and inviter reads",
			startFunc: "func createReferralTrialAccountInTx(",
			endFunc:   "func convertTrialToFormalWithInviteCode(",
			markers: []string{
				`tx.Where("code = ? AND is_enabled = ?", referralCode, true).First(&code).Error`,
				`tx.Where("telegram_id = ?", code.UserID).First(&inviter).Error`,
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"return time.Time{}, errReferralInvalidCode",
				"return time.Time{}, errReferralInviterNotEligible",
				"return time.Time{}, err",
			},
			forbidden: []string{
				`if err := tx.Where("code = ? AND is_enabled = ?", referralCode, true).First(&code).Error; err != nil {
		return time.Time{}, errReferralInvalidCode
	}`,
				`if err := tx.Where("telegram_id = ?", code.UserID).First(&inviter).Error; err != nil {
		return time.Time{}, errReferralInviterNotEligible
	}`,
			},
		},
		{
			name:      "trial conversion user and invite reads",
			startFunc: "func convertTrialToFormalWithInviteCode(",
			endFunc:   "func sumReferralTrialRawSeconds(",
			markers: []string{
				`tx.Where("telegram_id = ?", userID).First(&u).Error`,
				`tx.Where("code_hash = ? AND is_used = ?", inviteHash, false).First(&invite).Error`,
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"return errUserNotFound",
				"return errInvalidInviteCode",
				"return err",
			},
			forbidden: []string{
				`if err := tx.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			return errUserNotFound
		}`,
				`if err := tx.Where("code_hash = ? AND is_used = ?", inviteHash, false).First(&invite).Error; err != nil {
			return errInvalidInviteCode
		}`,
			},
		},
		{
			name:      "trial task user activation and locked invitee reads",
			startFunc: "func claimReferralTrialTask(",
			endFunc:   "func showMyReferral(",
			markers: []string{
				`DB.Where("telegram_id = ?", userID).First(&u).Error`,
				`DB.Where("invitee_id = ?", userID).First(&activation).Error`,
				`tx.Where("telegram_id = ?", userID).First(&invitee).Error`,
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"return 0, time.Time{}, 0, false, errUserNotFound",
				"return 0, time.Time{}, 0, false, errReferralNoActivation",
				"return errUserNotFound",
				"return err",
			},
			forbidden: []string{
				`if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		return 0, time.Time{}, 0, false, errUserNotFound
	}`,
				`if err := DB.Where("invitee_id = ?", userID).First(&activation).Error; err != nil {
		return 0, time.Time{}, 0, false, errReferralNoActivation
	}`,
				`if err := tx.Where("telegram_id = ?", userID).First(&invitee).Error; err != nil {
			return errUserNotFound
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
				t.Fatalf("%s missing read error guard %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still maps all read errors to business error: %s", tt.name, unsafe)
			}
		}
	}
}

func TestReferralTrialTaskClaimChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("referral.go")
	if err != nil {
		t.Fatalf("read referral.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func claimReferralTrialTask(")
	if start < 0 {
		t.Fatal("claimReferralTrialTask missing")
	}
	end := strings.Index(text[start:], "func showMyReferral(")
	if end < 0 {
		t.Fatal("claimReferralTrialTask boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"inviteeRes := tx.Model(&User{})",
		`Where("id = ? AND account_type = ? AND abs_user_id = ?", invitee.ID, accountTypeTrial, invitee.AbsUserID)`,
		"inviteeRes.RowsAffected == 0",
		"REFERRAL_TRIAL_INVITEE_STATE_CHANGED",
		"activationRes := tx.Model(&ReferralActivation{})",
		`Where("id = ? AND invitee_id = ? AND status = ? AND effective_at IS NULL", locked.ID, userID, referralStatusActive)`,
		"activationRes.RowsAffected == 0",
		"REFERRAL_TRIAL_ACTIVATION_STATE_CHANGED",
		"rawSeconds = capReferralTrialTaskSeconds(rawSeconds, activation.TrialStartedAt, activation.TrialEndsAt, now)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("referral trial task claim rows affected guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"tx.Model(&invitee).Updates(",
		"tx.Model(&locked).Updates(updates).Error",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("referral trial task claim still ignores RowsAffected: %s", unsafe)
		}
	}
}

func TestReferralTrialRegisterAndConvertCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("referral.go")
	if err != nil {
		t.Fatalf("read referral.go err = %v", err)
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
			name:      "trial registration existing user",
			startFunc: "func createReferralTrialAccountInTx(",
			endFunc:   "func convertTrialToFormalWithInviteCode(",
			markers: []string{
				"userRes := tx.Model(&User{})",
				`Where("id = ? AND telegram_id = ? AND abs_user_id = ?", existingUser.ID, inviteeID, "")`,
				"userRes.RowsAffected == 0",
				"REFERRAL_TRIAL_USER_STATE_CHANGED",
				"createReferralTrialUserInTx(tx, &trialUser)",
				"createReferralActivationInTx(tx, &activation)",
			},
			forbidden: []string{
				"tx.Model(&existingUser).Updates(updates).Error",
				"tx.Create(&User{",
				"tx.Create(&activation).Error",
			},
		},
		{
			name:      "trial formal conversion",
			startFunc: "func convertTrialToFormalWithInviteCode(",
			endFunc:   "func sumReferralTrialRawSeconds(",
			markers: []string{
				"userRes := tx.Model(&User{})",
				`Where("id = ? AND telegram_id = ? AND account_type = ? AND abs_user_id = ?", u.ID, userID, accountTypeTrial, u.AbsUserID)`,
				"userRes.RowsAffected == 0",
				"REFERRAL_TRIAL_CONVERT_USER_STATE_CHANGED",
			},
			forbidden: []string{
				"tx.Model(&u).Updates(updates).Error",
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

func TestReferralTrialCreateHelpersCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("referral.go")
	if err != nil {
		t.Fatalf("read referral.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		wants     []string
	}{
		{
			name:      "trial user",
			startFunc: "func createReferralTrialUserInTx(",
			endFunc:   "func createReferralActivationInTx(",
			wants: []string{
				"res := tx.Create(&entry)",
				"res.Error != nil",
				"res.RowsAffected == 0",
				"REFERRAL_TRIAL_USER_CREATE_MISSED",
			},
		},
		{
			name:      "activation",
			startFunc: "func createReferralActivationInTx(",
			endFunc:   "func createReferralTrialAccountInTx(",
			wants: []string{
				"res := tx.Create(&entry)",
				"res.Error != nil",
				"isUniqueConstraintError(res.Error)",
				"errReferralAlreadyTried",
				"res.RowsAffected == 0",
				"REFERRAL_ACTIVATION_CREATE_MISSED",
			},
		},
	}
	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s helper missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endFunc)
		if end < 0 {
			t.Fatalf("%s helper boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range tt.wants {
			if !strings.Contains(block, want) {
				t.Fatalf("%s helper guard missing %q", tt.name, want)
			}
		}
	}
}

func TestReferralMigrationsReplaceFullUniqueIndexes(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("referral_codes(user_id)"`)
	if start < 0 {
		t.Fatal("referral migration block missing")
	}
	end := strings.Index(text[start:], `idx_referral_daily_activation_quotas_unique`)
	if end < 0 {
		t.Fatal("referral migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM referral_codes",
		"FROM referral_activations",
		"WHERE deleted_at IS NULL",
		`assertNoDuplicateGroups("referral_codes(code)"`,
		`assertNoDuplicateGroups("referral_activations(invitee_id)"`,
		"ensureReferralPartialUniqueIndexes(DB)",
		"referral unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("referral migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureReferralPartialUniqueIndexes(")
	if helperStart < 0 {
		t.Fatal("ensureReferralPartialUniqueIndexes missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureInventoryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("referral partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_referral_codes_user_unique",
		"ON referral_codes(user_id)",
		"idx_referral_codes_code_unique",
		"ON referral_codes(code)",
		"idx_referral_activations_invitee_unique",
		"ON referral_activations(invitee_id)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("referral partial index helper missing %q", want)
		}
	}
}

func TestInviteRegistrationAuditDetailsUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("invite_registration.go")
	if err != nil {
		t.Fatalf("read invite_registration.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainValue(invite.CodePreview)",
		"formatPlainValue(reason)",
		`"RESERVE_INVITE_CODE"`,
		`"RELEASE_INVITE_CODE"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("invite registration audit detail missing sanitized dynamic field pattern %q", want)
		}
	}
	for _, unsafe := range []string{
		"userID, invite.CodePreview",
		"invite.CodePreview, reason",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("invite registration audit detail still contains raw dynamic fields: %q", unsafe)
		}
	}
}

func TestReferralAuditDetailsUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("referral.go")
	if err != nil {
		t.Fatalf("read referral.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainValue(absUserID)",
		"formatPlainValue(u.Username)",
		"formatPlainValue(invite.CodePreview)",
		`"REFERRAL_TRIAL_REGISTER"`,
		`"TRIAL_CONVERT_FORMAL"`,
		`"USE_INVITE_CODE"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("referral audit detail missing sanitized dynamic field pattern %q", want)
		}
	}
	for _, unsafe := range []string{
		"code.ID, absUserID",
		"u.Username, userID, invite.CodePreview",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("referral audit detail still contains raw dynamic fields: %q", unsafe)
		}
	}
}
