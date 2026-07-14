package main

import (
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

func TestNormalizeGithubLogin(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
		ok   bool
	}{
		{name: "plain", raw: "octocat", want: "octocat", ok: true},
		{name: "mention", raw: "@octocat", want: "octocat", ok: true},
		{name: "url", raw: "https://github.com/octocat", want: "octocat", ok: true},
		{name: "repo url uses owner", raw: "https://github.com/octocat/Hello-World", want: "octocat", ok: true},
		{name: "http url rejected", raw: "http://github.com/octocat", ok: false},
		{name: "userinfo url rejected", raw: "https://user@github.com/octocat", ok: false},
		{name: "lookalike host rejected", raw: "https://github.com.evil.example/octocat", ok: false},
		{name: "trailing hyphen", raw: "octocat-", ok: false},
		{name: "double hyphen", raw: "octo--cat", ok: false},
		{name: "too long", raw: "abcdefghijklmnopqrstuvwxyzabcdefghijklmn", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := normalizeGithubLogin(tt.raw)
			if tt.ok {
				if err != nil {
					t.Fatalf("normalizeGithubLogin() err = %v", err)
				}
				if got != tt.want {
					t.Fatalf("normalizeGithubLogin() = %s, want %s", got, tt.want)
				}
				return
			}
			if !errors.Is(err, errGithubBenefitInvalidLogin) {
				t.Fatalf("normalizeGithubLogin() err = %v, want invalid login", err)
			}
		})
	}
}

func TestGithubAPIErrorMessageSanitizesExternalText(t *testing.T) {
	got := formatGithubAPIErrorMessage("rate limited\npass\u202eword=gh-pass-123 to\u202eken=gh-token-123 Bearer gh-bearer-123")
	for _, unsafe := range []string{
		"gh-pass-123",
		"gh-token-123",
		"gh-bearer-123",
		"\n",
		"\r",
		"\u202e",
	} {
		if strings.Contains(got, unsafe) {
			t.Fatalf("formatGithubAPIErrorMessage leaked unsafe text %q in %q", unsafe, got)
		}
	}
	if !strings.Contains(got, "***") {
		t.Fatalf("formatGithubAPIErrorMessage should mark redacted fields, got %q", got)
	}

	long := formatGithubAPIErrorMessage(strings.Repeat("界", 200))
	if gotRunes := len([]rune(long)); gotRunes > 163 {
		t.Fatalf("formatGithubAPIErrorMessage length = %d runes, want <= 163", gotRunes)
	}
	if !strings.HasSuffix(long, "...") {
		t.Fatalf("formatGithubAPIErrorMessage should append truncation marker, got %q", long)
	}
}

func TestReadGithubAPIResponseBodyRejectsOversize(t *testing.T) {
	body, err := readGithubAPIResponseBody(strings.NewReader(`{"login":"octocat"}`))
	if err != nil {
		t.Fatalf("readGithubAPIResponseBody small err = %v", err)
	}
	if string(body) != `{"login":"octocat"}` {
		t.Fatalf("readGithubAPIResponseBody small body = %q", body)
	}

	_, err = readGithubAPIResponseBody(strings.NewReader(strings.Repeat("a", githubBenefitMaxAPIResponseBytes+1)))
	if err == nil || !strings.Contains(err.Error(), "github_api_response_too_large") {
		t.Fatalf("readGithubAPIResponseBody oversize err = %v, want too large", err)
	}
}

func TestValidateGithubBenefitEligibility(t *testing.T) {
	now := time.Date(2026, 6, 14, 12, 0, 0, 0, time.UTC)
	code := "absbot-gh-ABCDEFGH"
	bio := "hello " + code

	oldEnough := githubAPIUser{
		ID:        1,
		Login:     "octocat",
		Bio:       &bio,
		CreatedAt: now.AddDate(-5, 0, 0),
	}
	if err := validateGithubBenefitEligibility(oldEnough, code, now); err != nil {
		t.Fatalf("eligible user err = %v", err)
	}

	young := oldEnough
	young.CreatedAt = now.AddDate(-4, -11, -29)
	if err := validateGithubBenefitEligibility(young, code, now); !errors.Is(err, errGithubBenefitNotOldEnough) {
		t.Fatalf("young user err = %v, want not old enough", err)
	}

	missingCode := oldEnough
	otherBio := "hello"
	missingCode.Bio = &otherBio
	if err := validateGithubBenefitEligibility(missingCode, code, now); !errors.Is(err, errGithubBenefitBioMismatch) {
		t.Fatalf("missing code err = %v, want bio mismatch", err)
	}
}

func TestGithubBenefitAdminWriteCommandRecognition(t *testing.T) {
	valid := []string{
		"开启github福利",
		"关闭github福利",
		"设置github福利名额 20",
		"确认设置github福利名额 20",
	}
	for _, text := range valid {
		if !isGithubBenefitAdminWriteCommand(text) {
			t.Fatalf("command %q should be recognized", text)
		}
	}

	invalid := []string{
		"查看github福利",
		"设置github福利名额",
		"设置 github 福利名额 20",
	}
	for _, text := range invalid {
		if isGithubBenefitAdminWriteCommand(text) {
			t.Fatalf("command %q should not be recognized as write command", text)
		}
	}
}

func TestGithubBenefitAdminCommandRecognition(t *testing.T) {
	valid := []string{
		"查看github福利",
		"设置github福利名额",
		"设置github福利名额 20",
	}
	for _, text := range valid {
		if !isGithubBenefitAdminCommand(text) {
			t.Fatalf("command %q should be recognized as admin command", text)
		}
	}
}

func TestGithubBenefitRewardForBoundABSAccount(t *testing.T) {
	rewardType, days := githubBenefitRewardForBoundABSAccount(false)
	if rewardType != githubBenefitRewardInvite || days != 0 {
		t.Fatalf("unbound reward = %s/%d, want %s/0", rewardType, days, githubBenefitRewardInvite)
	}

	rewardType, days = githubBenefitRewardForBoundABSAccount(true)
	if rewardType != githubBenefitRewardRenew || days != githubBenefitRenewDays {
		t.Fatalf("bound reward = %s/%d, want %s/%d", rewardType, days, githubBenefitRewardRenew, githubBenefitRenewDays)
	}
}

func TestGithubBenefitAdminCountText(t *testing.T) {
	if got := githubBenefitAdminCountText(12, true); got != "12" {
		t.Fatalf("githubBenefitAdminCountText available = %q", got)
	}
	if got := githubBenefitAdminCountText(12, false); got != "读取失败" {
		t.Fatalf("githubBenefitAdminCountText unavailable = %q", got)
	}
}

func TestGithubBenefitClaimedChecksReturnErrors(t *testing.T) {
	source, err := os.ReadFile("github_benefit.go")
	if err != nil {
		t.Fatalf("read github_benefit.go err = %v", err)
	}
	text := string(source)
	for _, signature := range []string{
		"func githubBenefitHasClaimedTelegramInTx(tx *gorm.DB, tgID int64) (bool, error)",
		"func githubBenefitHasClaimedGithubInTx(tx *gorm.DB, githubID int64) (bool, error)",
	} {
		if !strings.Contains(text, signature) {
			t.Fatalf("missing error-returning signature: %s", signature)
		}
	}
	for _, unsafePattern := range []string{
		"if githubBenefitHasClaimedTelegramInTx(tx, tgID) {",
		"if githubBenefitHasClaimedGithubInTx(tx, ghUser.ID) {",
	} {
		if strings.Contains(text, unsafePattern) {
			t.Fatalf("claimed check ignores database error: %s", unsafePattern)
		}
	}
}

func TestGithubBenefitExpiredPendingUpdateChecksError(t *testing.T) {
	source, err := os.ReadFile("github_benefit.go")
	if err != nil {
		t.Fatalf("read github_benefit.go err = %v", err)
	}
	text := string(source)
	if strings.Contains(text, `_ = DB.Model(&GithubBenefitClaim{}).Where("id = ? AND status = ?", claim.ID, githubBenefitStatusPending).Update("status", githubBenefitStatusExpired).Error`) {
		t.Fatal("expired pending claim update still ignores database errors")
	}
	for _, want := range []string{
		"GitHub 福利待校验过期标记失败",
		"return GithubBenefitClaim{}, err",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("missing expired pending update error guard %q", want)
		}
	}
}

func TestGithubBenefitExpiredPendingUpdateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("github_benefit.go")
	if err != nil {
		t.Fatalf("read github_benefit.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
	}{
		{
			name:      "active pending lookup",
			startFunc: "func getActiveGithubBenefitPendingClaim(",
			endFunc:   "func claimGithubBenefitReward(",
		},
		{
			name:      "reward claim transaction",
			startFunc: "func claimGithubBenefitReward(",
			endFunc:   "func createGithubBenefitInviteCodeInTx(",
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
		for _, want := range []string{
			"res.Error != nil",
			"res.RowsAffected == 0",
			"errGithubBenefitPendingMissing",
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s missing expired pending update guard %q", tt.name, want)
			}
		}
		if strings.Contains(block, `.Update("status", githubBenefitStatusExpired).Error`) {
			t.Fatalf("%s still checks only update error", tt.name)
		}
	}
}

func TestGithubBenefitPendingClaimCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("github_benefit.go")
	if err != nil {
		t.Fatalf("read github_benefit.go err = %v", err)
	}
	text := string(source)
	helperStart := strings.Index(text, "func createGithubBenefitPendingClaimInTx(")
	if helperStart < 0 {
		t.Fatal("createGithubBenefitPendingClaimInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "type githubAPIUser struct")
	if helperEnd < 0 {
		t.Fatal("createGithubBenefitPendingClaimInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"res := tx.Create(claim)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"GITHUB_BENEFIT_PENDING_CLAIM_CREATE_MISSED",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("github benefit pending claim helper guard missing %q", want)
		}
	}

	createStart := strings.Index(text, "func createGithubBenefitPendingClaim(")
	if createStart < 0 {
		t.Fatal("createGithubBenefitPendingClaim missing")
	}
	createEnd := strings.Index(text[createStart:], "func getActiveGithubBenefitPendingClaim(")
	if createEnd < 0 {
		t.Fatal("createGithubBenefitPendingClaim boundary missing")
	}
	createBlock := text[createStart : createStart+createEnd]
	if !strings.Contains(createBlock, "createGithubBenefitPendingClaimInTx(tx, &txClaim)") {
		t.Fatal("createGithubBenefitPendingClaim should use createGithubBenefitPendingClaimInTx")
	}
	if strings.Contains(createBlock, "tx.Create(&claim).Error") {
		t.Fatal("github benefit pending claim create still ignores RowsAffected")
	}
}

func TestGithubBenefitTransactionalReturnValuesOnlyAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("github_benefit.go")
	if err != nil {
		t.Fatalf("read github_benefit.go err = %v", err)
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
			name:      "pending claim",
			startFunc: "func createGithubBenefitPendingClaim(",
			endFunc:   "func getActiveGithubBenefitPendingClaim(",
			wants: []string{
				"txQuota, err := githubBenefitQuotaInTxChecked(tx)",
				"txClaim := GithubBenefitClaim{",
				"claim = txClaim",
				"quota = txQuota",
				"return GithubBenefitClaim{}, 0, err",
				"return claim, quota, nil",
			},
			forbidden: []string{
				"return claim, quota, err",
				"quota, err = githubBenefitQuotaInTxChecked(tx)",
			},
		},
		{
			name:      "reward",
			startFunc: "func claimGithubBenefitReward(",
			endFunc:   "func createGithubBenefitInviteCodeInTx(",
			wants: []string{
				"txReward := githubBenefitRewardResult{}",
				"reward = txReward",
				"return githubBenefitRewardResult{}, err",
				"return reward, nil",
			},
			forbidden: []string{
				"return reward, err",
				"reward = githubBenefitRewardResult{",
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

func TestGithubBenefitConfigReturnValuesOnlyAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("github_benefit.go")
	if err != nil {
		t.Fatalf("read github_benefit.go err = %v", err)
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
			name:      "enabled",
			startFunc: "func setGithubBenefitEnabledWithAudit(",
			endFunc:   "func setGithubBenefitQuotaWithAudit(",
			wants: []string{
				"txOldEnabled, err := githubBenefitEnabledInTxChecked(tx)",
				"oldEnabled = txOldEnabled",
				"return false, err",
				"return oldEnabled, nil",
			},
			forbidden: []string{
				"oldEnabled, err = githubBenefitEnabledInTxChecked(tx)",
				"return oldEnabled, err",
			},
		},
		{
			name:      "quota",
			startFunc: "func setGithubBenefitQuotaWithAudit(",
			endFunc:   "func formatGithubBenefitAdminStatus(",
			wants: []string{
				"txOldQuota, err := githubBenefitQuotaInTxChecked(tx)",
				"oldQuota = txOldQuota",
				"return 0, err",
				"return oldQuota, nil",
			},
			forbidden: []string{
				"oldQuota, err = githubBenefitQuotaInTxChecked(tx)",
				"return oldQuota, err",
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
				t.Fatalf("%s missing post-transaction config return guard %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still exposes transactional intermediate value: %s", tt.name, unsafe)
			}
		}
	}
}

func TestGithubBenefitRewardCodeCreatesCheckRowsAffected(t *testing.T) {
	source, err := os.ReadFile("github_benefit.go")
	if err != nil {
		t.Fatalf("read github_benefit.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		missedErr string
		unsafe    string
	}{
		{
			name:      "invite",
			startFunc: "func createGithubBenefitInviteCodeInTx(",
			endFunc:   "func createGithubBenefitRenewCodeInTx(",
			missedErr: "errCreateInviteCodeFailed",
			unsafe:    "tx.Create(&invite).Error",
		},
		{
			name:      "renew",
			startFunc: "func createGithubBenefitRenewCodeInTx(",
			endFunc:   "func githubBenefitHasClaimedTelegramInTx(",
			missedErr: "errCreateRenewCodeFailed",
			unsafe:    "tx.Create(&renew).Error",
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
		for _, want := range []string{
			"res := tx.Create(&",
			"res.Error == nil && res.RowsAffected > 0",
			"res.Error == nil",
			tt.missedErr,
			"isUniqueConstraintError(res.Error)",
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s reward code create guard missing %q", tt.name, want)
			}
		}
		if strings.Contains(block, tt.unsafe) {
			t.Fatalf("%s reward code create still checks only Error", tt.name)
		}
	}
}

func TestGithubBenefitConfigReadsAreChecked(t *testing.T) {
	source, err := os.ReadFile("github_benefit.go")
	if err != nil {
		t.Fatalf("read github_benefit.go err = %v", err)
	}
	text := string(source)
	for _, signature := range []string{
		"func githubBenefitEnabledInTxChecked(tx *gorm.DB) (bool, error)",
		"func githubBenefitQuotaInTxChecked(tx *gorm.DB) (int, error)",
		"func githubBenefitEnabledChecked() (bool, error)",
		"func githubBenefitQuotaChecked() (int, error)",
	} {
		if !strings.Contains(text, signature) {
			t.Fatalf("missing checked config helper: %s", signature)
		}
	}

	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		wants     []string
		unsafe    []string
	}{
		{
			name:      "create pending claim",
			startFunc: "func createGithubBenefitPendingClaim(",
			endFunc:   "func getActiveGithubBenefitPendingClaim(",
			wants: []string{
				"githubBenefitEnabledInTxChecked(tx)",
				"githubBenefitQuotaInTxChecked(tx)",
				"return err",
			},
			unsafe: []string{
				"if !githubBenefitEnabledInTx(tx)",
				"quota = githubBenefitQuotaInTx(tx)",
			},
		},
		{
			name:      "claim reward",
			startFunc: "func claimGithubBenefitReward(",
			endFunc:   "func createGithubBenefitInviteCodeInTx(",
			wants: []string{
				"githubBenefitEnabledInTxChecked(tx)",
				"return err",
			},
			unsafe: []string{
				"if !githubBenefitEnabledInTx(tx)",
			},
		},
		{
			name:      "enabled audit",
			startFunc: "func setGithubBenefitEnabledWithAudit(",
			endFunc:   "func setGithubBenefitQuotaWithAudit(",
			wants: []string{
				"githubBenefitEnabledInTxChecked(tx)",
				"return err",
				"SET_GITHUB_BENEFIT_ENABLED",
				"formatPlainValue(reason)",
			},
			unsafe: []string{
				"oldEnabled = githubBenefitEnabledInTx(tx)",
				"oldEnabled, enabled, reason",
			},
		},
		{
			name:      "quota audit",
			startFunc: "func setGithubBenefitQuotaWithAudit(",
			endFunc:   "func formatGithubBenefitAdminStatus(",
			wants: []string{
				"githubBenefitQuotaInTxChecked(tx)",
				"return err",
				"SET_GITHUB_BENEFIT_QUOTA",
				"formatPlainValue(reason)",
			},
			unsafe: []string{
				"oldQuota = githubBenefitQuotaInTx(tx)",
				"oldQuota, quota, reason",
			},
		},
		{
			name:      "admin status",
			startFunc: "func formatGithubBenefitAdminStatus(",
			endFunc:   "",
			wants: []string{
				"githubBenefitEnabledChecked()",
				"githubBenefitQuotaChecked()",
				"status = \"读取失败\"",
				"quotaText := \"读取失败\"",
			},
			unsafe: []string{
				"if githubBenefitEnabled()",
				"githubBenefitQuota(),",
			},
		},
	}
	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s start missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endFunc)
		if tt.endFunc == "" {
			end = len(text) - start
		} else if end < 0 {
			t.Fatalf("%s boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range tt.wants {
			if !strings.Contains(block, want) {
				t.Fatalf("%s missing checked config guard %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.unsafe {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still uses unchecked config read %q", tt.name, unsafe)
			}
		}
	}
}

func TestGithubBenefitNoUncheckedConfigFallbackHelpers(t *testing.T) {
	source, err := os.ReadFile("github_benefit.go")
	if err != nil {
		t.Fatalf("read github_benefit.go err = %v", err)
	}
	text := string(source)
	for _, unsafe := range []string{
		"func githubBenefitEnabledInTx(tx *gorm.DB) bool",
		"func githubBenefitQuotaInTx(tx *gorm.DB) int",
		"func githubBenefitEnabled() bool",
		"func githubBenefitQuota() int",
		"按关闭处理",
		"按关闭展示",
		"按 0 处理",
		"按 0 展示",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("github benefit config read must not use unchecked fallback helper %q", unsafe)
		}
	}
}

func TestGithubBenefitClaimAuditDetailsUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("github_benefit.go")
	if err != nil {
		t.Fatalf("read github_benefit.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func claimGithubBenefitReward(")
	if start < 0 {
		t.Fatal("claimGithubBenefitReward missing")
	}
	end := strings.Index(text[start:], "func createGithubBenefitInviteCodeInTx(")
	if end < 0 {
		t.Fatal("claimGithubBenefitReward boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"CLAIM_GITHUB_BENEFIT_RENEW",
		"CLAIM_GITHUB_BENEFIT_INVITE",
		"formatPlainValue(ghUser.Login)",
		"formatPlainValue(renew.CodePreview)",
		"formatPlainValue(invite.CodePreview)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("github benefit claim audit detail guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"tgID, ghUser.Login, ghUser.CreatedAt",
		"renew.ID, renew.CodePreview",
		"invite.ID, invite.CodePreview",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("github benefit claim audit detail still uses raw dynamic field %q", unsafe)
		}
	}
}

func TestGithubBenefitClaimedMigrationsReplaceFullUniqueIndexes(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("github_benefit_claims(claimed telegram_id)"`)
	if start < 0 {
		t.Fatal("github benefit claimed migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("users(abs_user_id)"`)
	if end < 0 {
		t.Fatal("github benefit claimed migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM github_benefit_claims",
		"WHERE status = 'claimed' AND deleted_at IS NULL",
		"WHERE github_id > 0 AND status = 'claimed' AND deleted_at IS NULL",
		"ensureGithubBenefitClaimedPartialUniqueIndexes(DB)",
		"github benefit claimed unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("github benefit claimed migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureGithubBenefitClaimedPartialUniqueIndexes(")
	if helperStart < 0 {
		t.Fatal("ensureGithubBenefitClaimedPartialUniqueIndexes missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureInventoryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("github benefit claimed partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_github_benefit_claims_tg_claimed_unique",
		"ON github_benefit_claims(telegram_id)",
		"idx_github_benefit_claims_github_claimed_unique",
		"ON github_benefit_claims(github_id)",
		"WHERE status = 'claimed' AND deleted_at IS NULL",
		"WHERE github_id > 0 AND status = 'claimed' AND deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("github benefit claimed partial index helper missing %q", want)
		}
	}
}

func TestGithubBenefitAdminPlainRepliesUsePlainValue(t *testing.T) {
	source, err := os.ReadFile("github_benefit.go")
	if err != nil {
		t.Fatalf("read github_benefit.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"formatPlainValue(normalized), adminReasonRequirementText",
		"formatPlainValue(command), adminReasonRequirementText",
		"formatPlainValue(command), formatPlainValue(reason), githubBenefitConfirmCommand",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("github benefit admin plain reply guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"normalized, adminReasonRequirementText",
		"command, adminReasonRequirementText",
		"command, reason, githubBenefitConfirmCommand",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("github benefit admin plain reply still uses raw value %q", unsafe)
		}
	}
}

func TestApplyGithubBenefitAuthHeader(t *testing.T) {
	oldConfig := AppConfig
	defer func() { AppConfig = oldConfig }()

	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/users/octocat", nil)
	if err != nil {
		t.Fatalf("new request err = %v", err)
	}
	AppConfig = &Config{}
	applyGithubBenefitAuthHeader(req)
	if got := req.Header.Get("Authorization"); got != "" {
		t.Fatalf("Authorization without token = %q, want empty", got)
	}

	req, err = http.NewRequest(http.MethodGet, "https://api.github.com/users/octocat", nil)
	if err != nil {
		t.Fatalf("new request err = %v", err)
	}
	AppConfig = &Config{GithubAPIToken: "  github_pat_test  "}
	applyGithubBenefitAuthHeader(req)
	if got := req.Header.Get("Authorization"); got != "Bearer github_pat_test" {
		t.Fatalf("Authorization = %q, want bearer token", got)
	}
}
