package main

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const (
	githubBenefitEnabledKey = "github_benefit_enabled"
	githubBenefitQuotaKey   = "github_benefit_quota"

	githubBenefitStatusPending = "pending"
	githubBenefitStatusExpired = "expired"
	githubBenefitStatusClaimed = "claimed"

	githubBenefitVerifyTTL           = time.Hour
	githubBenefitMinAccountAge       = 5
	githubBenefitRenewDays           = 150
	githubBenefitUserAgent           = "abs-bot-github-benefit"
	githubBenefitHTTPTimeout         = 8 * time.Second
	githubBenefitMaxAPIResponseBytes = 1 << 20
	githubBenefitConfirmCommand      = "确认执行github福利"

	githubBenefitRewardInvite = "invite"
	githubBenefitRewardRenew  = "renew_150d"
)

var githubLoginPattern = regexp.MustCompile(`^[A-Za-z0-9](?:[A-Za-z0-9-]{0,37}[A-Za-z0-9])?$`)

var (
	errGithubBenefitDisabled       = errors.New("GITHUB_BENEFIT_DISABLED")
	errGithubBenefitQuotaExhausted = errors.New("GITHUB_BENEFIT_QUOTA_EXHAUSTED")
	errGithubBenefitAlreadyTG      = errors.New("GITHUB_BENEFIT_ALREADY_TG")
	errGithubBenefitAlreadyGithub  = errors.New("GITHUB_BENEFIT_ALREADY_GITHUB")
	errGithubBenefitPendingMissing = errors.New("GITHUB_BENEFIT_PENDING_MISSING")
	errGithubBenefitPendingExpired = errors.New("GITHUB_BENEFIT_PENDING_EXPIRED")
	errGithubBenefitInvalidLogin   = errors.New("GITHUB_BENEFIT_INVALID_LOGIN")
	errGithubBenefitNotOldEnough   = errors.New("GITHUB_BENEFIT_NOT_OLD_ENOUGH")
	errGithubBenefitBioMismatch    = errors.New("GITHUB_BENEFIT_BIO_MISMATCH")
)

func createGithubBenefitPendingClaimInTx(tx *gorm.DB, claim *GithubBenefitClaim) error {
	if tx == nil || claim == nil {
		return fmt.Errorf("GITHUB_BENEFIT_PENDING_CLAIM_INVALID")
	}
	res := tx.Create(claim)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("GITHUB_BENEFIT_PENDING_CLAIM_CREATE_MISSED")
	}
	return nil
}

type githubAPIUser struct {
	ID        int64     `json:"id"`
	Login     string    `json:"login"`
	Bio       *string   `json:"bio"`
	CreatedAt time.Time `json:"created_at"`
	Message   string    `json:"message"`
}

type githubBenefitRewardResult struct {
	Type string
	Code string
	Days int
}

func HandleGithubBenefitCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState) bool {
	text = strings.TrimSpace(text)
	if msg == nil || msg.From == nil || text == "" {
		return isGithubBenefitCommand(text)
	}

	if session == nil {
		session = getSession(msg.From.ID)
	}

	pendingAdmin := session.GetStep() == "WAITING_GITHUB_BENEFIT_ADMIN_REASON" ||
		session.GetStep() == "WAITING_GITHUB_BENEFIT_ADMIN_QUOTA" ||
		session.GetStep() == "WAITING_CONFIRM_GITHUB_BENEFIT_ADMIN"
	if !pendingAdmin && !isGithubBenefitCommand(text) {
		return false
	}

	if pendingAdmin || isGithubBenefitAdminCommand(text) {
		return handleGithubBenefitAdminCommand(bot, msg, text, session, pendingAdmin)
	}

	if !msg.Chat.IsPrivate() {
		registerIncomingGroupCommandForAutoDelete(msg)
		sendGroupAutoDeleteMessage(bot, msg.Chat.ID, "GitHub 福利需要私聊校验。请私聊 Bot 发送 `github福利` 开始领取。")
		return true
	}

	if isGithubBenefitVerifyCommand(text) {
		handleGithubBenefitVerify(bot, msg, text)
		return true
	}

	if strings.EqualFold(text, "github福利") {
		handleGithubBenefitEntry(bot, msg)
		return true
	}

	sendPlainText(bot, msg.Chat.ID, "GitHub 福利指令格式：github福利，或 校验github福利 GitHub用户名。")
	return true
}

func isGithubBenefitCommand(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	lower := strings.ToLower(text)
	return lower == "github福利" ||
		strings.HasPrefix(lower, "github福利 ") ||
		strings.HasPrefix(lower, "校验github福利 ") ||
		isGithubBenefitAdminCommand(text)
}

func isGithubBenefitVerifyCommand(text string) bool {
	lower := strings.ToLower(strings.TrimSpace(text))
	return strings.HasPrefix(lower, "校验github福利 ") ||
		strings.HasPrefix(lower, "github福利 ")
}

func githubBenefitCommandArg(text string, prefix string) string {
	trimmed := strings.TrimSpace(text)
	if len([]rune(trimmed)) <= len([]rune(prefix)) {
		return ""
	}
	return strings.TrimSpace(string([]rune(trimmed)[len([]rune(prefix)):]))
}

func handleGithubBenefitEntry(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	claim, quota, err := createGithubBenefitPendingClaim(msg.From.ID)
	if err != nil {
		sendPlainText(bot, msg.Chat.ID, githubBenefitUserErrorText(err))
		return
	}

	replyText(bot, msg.Chat.ID, fmt.Sprintf(
		"🎁 **GitHub 福利**\n\n"+
			"领取条件：GitHub 账号注册满 `5` 年，并能修改该账号 Bio。\n"+
			"奖励规则：未绑定 ABS 账号发 `1` 个邀请码；已绑定 ABS 账号发 `150` 天续期卡。\n"+
			"剩余名额：`%d`\n\n"+
			"请把下面这段校验码临时放进 GitHub 个人简介 Bio：\n"+
			"`%s`\n\n"+
			"放好后回复：`校验github福利 你的GitHub用户名`\n"+
			"校验码有效期至：`%s`",
		quota,
		claim.VerifyCode,
		claim.ExpiresAt.Format("2006-01-02 15:04:05"),
	))
}

func handleGithubBenefitVerify(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) {
	login := ""
	lower := strings.ToLower(strings.TrimSpace(text))
	if strings.HasPrefix(lower, "校验github福利 ") {
		login = githubBenefitCommandArg(text, "校验github福利")
	} else if strings.HasPrefix(lower, "github福利 ") {
		login = githubBenefitCommandArg(text, "github福利")
	}
	login, err := normalizeGithubLogin(login)
	if err != nil {
		sendPlainText(bot, msg.Chat.ID, "GitHub 用户名格式不正确。请确认只填写用户名，不要带链接或 @。")
		return
	}

	pending, err := getActiveGithubBenefitPendingClaim(msg.From.ID, time.Now())
	if err != nil {
		sendPlainText(bot, msg.Chat.ID, githubBenefitUserErrorText(err))
		return
	}

	sendPlainText(bot, msg.Chat.ID, "正在校验 GitHub 账号，请稍候。")

	ghUser, err := fetchGithubUser(login)
	if err != nil {
		log.Printf("GitHub 福利 API 校验失败: user=%d login=%s err=%s", msg.From.ID, formatPlainValue(login), formatPlainError(err))
		sendPlainText(bot, msg.Chat.ID, "GitHub 校验失败，请稍后重试。若 GitHub API 频率受限，也可能短时间内无法校验。")
		return
	}

	if err := validateGithubBenefitEligibility(ghUser, pending.VerifyCode, time.Now()); err != nil {
		sendPlainText(bot, msg.Chat.ID, githubBenefitUserErrorText(err))
		return
	}

	reward, err := claimGithubBenefitReward(msg.From.ID, pending.ID, ghUser)
	if err != nil {
		log.Printf("GitHub 福利发放失败: user=%d github_id=%d login=%s err=%s", msg.From.ID, ghUser.ID, formatPlainValue(ghUser.Login), formatPlainError(err))
		sendPlainText(bot, msg.Chat.ID, githubBenefitUserErrorText(err))
		return
	}

	if reward.Type == githubBenefitRewardRenew {
		replyText(bot, msg.Chat.ID, fmt.Sprintf(
			"🎉 **GitHub 福利领取成功**\n\n"+
				"GitHub：`%s`\n"+
				"注册时间：`%s`\n"+
				"奖励：`%d` 天续期卡\n"+
				"专属续期卡：`%s`\n\n"+
				"续期卡仅此处展示，请妥善保存。",
			escapeMarkdown(ghUser.Login),
			ghUser.CreatedAt.Format("2006-01-02"),
			reward.Days,
			reward.Code,
		))
		return
	}

	replyText(bot, msg.Chat.ID, fmt.Sprintf(
		"🎉 **GitHub 福利领取成功**\n\n"+
			"GitHub：`%s`\n"+
			"注册时间：`%s`\n"+
			"奖励：邀请码 `1` 个\n"+
			"专属邀请码：`%s`\n\n"+
			"邀请码仅此处展示，请妥善保存。",
		escapeMarkdown(ghUser.Login),
		ghUser.CreatedAt.Format("2006-01-02"),
		reward.Code,
	))
}

func createGithubBenefitPendingClaim(tgID int64) (GithubBenefitClaim, int, error) {
	now := time.Now()
	code, err := generateGithubBenefitVerifyCode()
	if err != nil {
		return GithubBenefitClaim{}, 0, err
	}

	var claim GithubBenefitClaim
	quota := 0
	err = DB.Transaction(func(tx *gorm.DB) error {
		hasClaimedTG, err := githubBenefitHasClaimedTelegramInTx(tx, tgID)
		if err != nil {
			return err
		}
		if hasClaimedTG {
			return errGithubBenefitAlreadyTG
		}
		enabled, err := githubBenefitEnabledInTxChecked(tx)
		if err != nil {
			return err
		}
		if !enabled {
			return errGithubBenefitDisabled
		}
		txQuota, err := githubBenefitQuotaInTxChecked(tx)
		if err != nil {
			return err
		}
		if txQuota <= 0 {
			return errGithubBenefitQuotaExhausted
		}
		expireRes := tx.Model(&GithubBenefitClaim{}).
			Where("telegram_id = ? AND status = ?", tgID, githubBenefitStatusPending).
			Update("status", githubBenefitStatusExpired)
		if expireRes.Error != nil {
			return expireRes.Error
		}

		txClaim := GithubBenefitClaim{
			TelegramID: tgID,
			VerifyCode: code,
			Status:     githubBenefitStatusPending,
			ExpiresAt:  now.Add(githubBenefitVerifyTTL),
		}
		if err := createGithubBenefitPendingClaimInTx(tx, &txClaim); err != nil {
			return err
		}
		claim = txClaim
		quota = txQuota
		return nil
	})
	if err != nil {
		return GithubBenefitClaim{}, 0, err
	}
	return claim, quota, nil
}

func getActiveGithubBenefitPendingClaim(tgID int64, now time.Time) (GithubBenefitClaim, error) {
	var claim GithubBenefitClaim
	err := DB.Where("telegram_id = ? AND status = ?", tgID, githubBenefitStatusPending).
		Order("created_at DESC").
		First(&claim).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return GithubBenefitClaim{}, errGithubBenefitPendingMissing
		}
		return GithubBenefitClaim{}, err
	}
	if !claim.ExpiresAt.After(now) {
		res := DB.Model(&GithubBenefitClaim{}).
			Where("id = ? AND status = ?", claim.ID, githubBenefitStatusPending).
			Update("status", githubBenefitStatusExpired)
		if res.Error != nil {
			log.Printf("GitHub 福利待校验过期标记失败: claim=%d user=%d err=%s", claim.ID, tgID, formatPlainError(res.Error))
			return GithubBenefitClaim{}, res.Error
		}
		if res.RowsAffected == 0 {
			log.Printf("GitHub 福利待校验过期标记未命中: claim=%d user=%d", claim.ID, tgID)
			return GithubBenefitClaim{}, errGithubBenefitPendingMissing
		}
		return GithubBenefitClaim{}, errGithubBenefitPendingExpired
	}
	return claim, nil
}

func claimGithubBenefitReward(tgID int64, pendingID uint, ghUser githubAPIUser) (githubBenefitRewardResult, error) {
	now := time.Now()
	reward := githubBenefitRewardResult{}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var pending GithubBenefitClaim
		if err := tx.Where("id = ? AND telegram_id = ? AND status = ?", pendingID, tgID, githubBenefitStatusPending).First(&pending).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errGithubBenefitPendingMissing
			}
			return err
		}
		if !pending.ExpiresAt.After(now) {
			res := tx.Model(&GithubBenefitClaim{}).Where("id = ? AND status = ?", pending.ID, githubBenefitStatusPending).Update("status", githubBenefitStatusExpired)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errGithubBenefitPendingMissing
			}
			return errGithubBenefitPendingExpired
		}
		hasClaimedTG, err := githubBenefitHasClaimedTelegramInTx(tx, tgID)
		if err != nil {
			return err
		}
		if hasClaimedTG {
			return errGithubBenefitAlreadyTG
		}
		hasClaimedGithub, err := githubBenefitHasClaimedGithubInTx(tx, ghUser.ID)
		if err != nil {
			return err
		}
		if hasClaimedGithub {
			return errGithubBenefitAlreadyGithub
		}
		enabled, err := githubBenefitEnabledInTxChecked(tx)
		if err != nil {
			return err
		}
		if !enabled {
			return errGithubBenefitDisabled
		}
		hasBoundABS, err := githubBenefitHasBoundABSAccountInTx(tx, tgID)
		if err != nil {
			return err
		}
		rewardType, rewardDays := githubBenefitRewardForBoundABSAccount(hasBoundABS)

		res := tx.Model(&SystemConfig{}).
			Where("key = ? AND CAST(value AS INTEGER) > 0", githubBenefitQuotaKey).
			Update("value", gorm.Expr("CAST(value AS INTEGER) - 1"))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errGithubBenefitQuotaExhausted
		}

		createdAt := ghUser.CreatedAt
		claimedAt := now

		updates := map[string]interface{}{
			"github_id":         ghUser.ID,
			"github_login":      ghUser.Login,
			"github_created_at": &createdAt,
			"status":            githubBenefitStatusClaimed,
			"claimed_at":        &claimedAt,
		}
		auditAction := "CLAIM_GITHUB_BENEFIT_INVITE"
		auditDetail := ""
		txReward := githubBenefitRewardResult{}
		if rewardType == githubBenefitRewardRenew {
			renew, createdRenewCode, err := createGithubBenefitRenewCodeInTx(tx, rewardDays)
			if err != nil {
				return err
			}
			txReward = githubBenefitRewardResult{
				Type: githubBenefitRewardRenew,
				Code: createdRenewCode,
				Days: rewardDays,
			}
			updates["renew_code_id"] = renew.ID
			updates["renew_code_preview"] = renew.CodePreview
			updates["reward_type"] = githubBenefitRewardRenew
			updates["reward_days"] = rewardDays
			auditAction = "CLAIM_GITHUB_BENEFIT_RENEW"
			auditDetail = fmt.Sprintf("GitHub benefit renew claimed: tg=%d github_login=%s github_created_at=%s renew_code_id=%d renew_preview=%s days=%d reward_type=%s",
				tgID, formatPlainValue(ghUser.Login), ghUser.CreatedAt.Format(time.RFC3339), renew.ID, formatPlainValue(renew.CodePreview), rewardDays, githubBenefitRewardRenew)
		} else {
			invite, createdInviteCode, err := createGithubBenefitInviteCodeInTx(tx)
			if err != nil {
				return err
			}
			txReward = githubBenefitRewardResult{
				Type: githubBenefitRewardInvite,
				Code: createdInviteCode,
			}
			updates["invite_code_id"] = invite.ID
			updates["invite_code_preview"] = invite.CodePreview
			updates["reward_type"] = githubBenefitRewardInvite
			auditDetail = fmt.Sprintf("GitHub benefit invite claimed: tg=%d github_login=%s github_created_at=%s invite_code_id=%d invite_preview=%s reward_type=%s",
				tgID, formatPlainValue(ghUser.Login), ghUser.CreatedAt.Format(time.RFC3339), invite.ID, formatPlainValue(invite.CodePreview), githubBenefitRewardInvite)
		}
		res = tx.Model(&GithubBenefitClaim{}).
			Where("id = ? AND telegram_id = ? AND status = ?", pending.ID, tgID, githubBenefitStatusPending).
			Updates(updates)
		if res.Error != nil {
			err := res.Error
			if isUniqueConstraintError(err) {
				return errGithubBenefitAlreadyGithub
			}
			return err
		}
		if res.RowsAffected == 0 {
			return errGithubBenefitPendingMissing
		}

		if err := writeAuditLogInTx(
			tx,
			tgID,
			auditAction,
			fmt.Sprintf("github_id=%d", ghUser.ID),
			0,
			auditDetail,
		); err != nil {
			return err
		}
		reward = txReward
		return nil
	})
	if err != nil {
		return githubBenefitRewardResult{}, err
	}
	return reward, nil
}

func createGithubBenefitInviteCodeInTx(tx *gorm.DB) (InviteCode, string, error) {
	for i := 0; i < 5; i++ {
		code := generateRandomCode(16)
		codeHash := hashSensitiveToken(code)
		if codeHash == "" {
			return InviteCode{}, "", errSecurityPepperNotConfigured
		}
		invite := InviteCode{
			Code:        "internal-invite-" + generateRandomCode(16),
			CodeHash:    codeHash,
			CodePreview: maskSecret(code),
		}
		res := tx.Create(&invite)
		if res.Error == nil && res.RowsAffected > 0 {
			return invite, code, nil
		}
		if res.Error == nil {
			return InviteCode{}, "", errCreateInviteCodeFailed
		}
		if !isUniqueConstraintError(res.Error) {
			return InviteCode{}, "", res.Error
		}
	}
	return InviteCode{}, "", errCreateInviteCodeFailed
}

func createGithubBenefitRenewCodeInTx(tx *gorm.DB, days int) (RenewCode, string, error) {
	for i := 0; i < 5; i++ {
		code := fmt.Sprintf("R%d-%s", days, generateRandomCode(16))
		codeHash := hashSensitiveToken(code)
		if codeHash == "" {
			return RenewCode{}, "", errSecurityPepperNotConfigured
		}
		renew := RenewCode{
			Code:        "internal-renew-" + generateRandomCode(16),
			CodeHash:    codeHash,
			CodePreview: maskSecret(code),
			Days:        days,
		}
		res := tx.Create(&renew)
		if res.Error == nil && res.RowsAffected > 0 {
			return renew, code, nil
		}
		if res.Error == nil {
			return RenewCode{}, "", errCreateRenewCodeFailed
		}
		if !isUniqueConstraintError(res.Error) {
			return RenewCode{}, "", res.Error
		}
	}
	return RenewCode{}, "", errCreateRenewCodeFailed
}

func githubBenefitHasClaimedTelegramInTx(tx *gorm.DB, tgID int64) (bool, error) {
	var count int64
	err := tx.Model(&GithubBenefitClaim{}).
		Where("telegram_id = ? AND status = ?", tgID, githubBenefitStatusClaimed).
		Count(&count).Error
	return count > 0, err
}

func githubBenefitHasClaimedGithubInTx(tx *gorm.DB, githubID int64) (bool, error) {
	if githubID <= 0 {
		return false, nil
	}
	var count int64
	err := tx.Model(&GithubBenefitClaim{}).
		Where("github_id = ? AND status = ?", githubID, githubBenefitStatusClaimed).
		Count(&count).Error
	return count > 0, err
}

func githubBenefitHasBoundABSAccountInTx(tx *gorm.DB, tgID int64) (bool, error) {
	var count int64
	err := tx.Model(&User{}).
		Where("telegram_id = ? AND TRIM(abs_user_id) <> ''", tgID).
		Count(&count).Error
	return count > 0, err
}

func githubBenefitRewardForBoundABSAccount(hasBoundABS bool) (string, int) {
	if hasBoundABS {
		return githubBenefitRewardRenew, githubBenefitRenewDays
	}
	return githubBenefitRewardInvite, 0
}

func generateGithubBenefitVerifyCode() (string, error) {
	const letters = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	var b strings.Builder
	b.WriteString("absbot-gh-")
	for i := 0; i < 8; i++ {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		if err != nil {
			return "", err
		}
		b.WriteByte(letters[n.Int64()])
	}
	return b.String(), nil
}

func normalizeGithubLogin(login string) (string, error) {
	login = strings.TrimSpace(login)
	login = strings.TrimPrefix(login, "@")
	if strings.Contains(login, "://") {
		u, err := url.Parse(login)
		if err != nil {
			return "", errGithubBenefitInvalidLogin
		}
		if u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "github.com") || u.User != nil {
			return "", errGithubBenefitInvalidLogin
		}
		login = strings.Trim(strings.TrimSpace(u.Path), "/")
		if strings.Contains(login, "/") {
			login = strings.Split(login, "/")[0]
		}
	}
	if !githubLoginPattern.MatchString(login) || strings.Contains(login, "--") {
		return "", errGithubBenefitInvalidLogin
	}
	return login, nil
}

func fetchGithubUser(login string) (githubAPIUser, error) {
	normalized, err := normalizeGithubLogin(login)
	if err != nil {
		return githubAPIUser{}, err
	}
	req, err := http.NewRequest(http.MethodGet, "https://api.github.com/users/"+url.PathEscape(normalized), nil)
	if err != nil {
		return githubAPIUser{}, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", githubBenefitUserAgent)
	applyGithubBenefitAuthHeader(req)

	client := &http.Client{Timeout: githubBenefitHTTPTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return githubAPIUser{}, err
	}
	defer resp.Body.Close()

	body, err := readGithubAPIResponseBody(resp.Body)
	if err != nil {
		return githubAPIUser{}, err
	}
	var ghUser githubAPIUser
	if err := json.Unmarshal(body, &ghUser); err != nil {
		return githubAPIUser{}, err
	}
	if resp.StatusCode == http.StatusNotFound {
		return githubAPIUser{}, errGithubBenefitInvalidLogin
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if msg := formatGithubAPIErrorMessage(ghUser.Message); msg != "" {
			return githubAPIUser{}, fmt.Errorf("github_api_status_%d:%s", resp.StatusCode, msg)
		}
		return githubAPIUser{}, fmt.Errorf("github_api_status_%d", resp.StatusCode)
	}
	if ghUser.ID <= 0 || ghUser.Login == "" || ghUser.CreatedAt.IsZero() {
		return githubAPIUser{}, fmt.Errorf("github_api_invalid_response")
	}
	return ghUser, nil
}

func readGithubAPIResponseBody(r io.Reader) ([]byte, error) {
	if r == nil {
		return nil, fmt.Errorf("github_api_response_body_missing")
	}
	body, err := io.ReadAll(io.LimitReader(r, githubBenefitMaxAPIResponseBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > githubBenefitMaxAPIResponseBytes {
		return nil, fmt.Errorf("github_api_response_too_large")
	}
	return body, nil
}

func formatGithubAPIErrorMessage(message string) string {
	message = formatDiagnosticTextForDisplay(message)
	if message == "" {
		return ""
	}
	return truncateRunes(message, 160)
}

func applyGithubBenefitAuthHeader(req *http.Request) {
	if req == nil {
		return
	}
	token := githubBenefitAPIToken()
	if token == "" {
		return
	}
	req.Header.Set("Authorization", "Bearer "+token)
}

func githubBenefitAPIToken() string {
	if AppConfig == nil {
		return ""
	}
	return strings.TrimSpace(AppConfig.GithubAPIToken)
}

func validateGithubBenefitEligibility(ghUser githubAPIUser, verifyCode string, now time.Time) error {
	if ghUser.CreatedAt.AddDate(githubBenefitMinAccountAge, 0, 0).After(now) {
		return errGithubBenefitNotOldEnough
	}
	bio := ""
	if ghUser.Bio != nil {
		bio = *ghUser.Bio
	}
	if !strings.Contains(bio, verifyCode) {
		return errGithubBenefitBioMismatch
	}
	return nil
}

func githubBenefitEnabledInTxChecked(tx *gorm.DB) (bool, error) {
	var cfg SystemConfig
	if err := tx.Where("key = ?", githubBenefitEnabledKey).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(cfg.Value), "true"), nil
}

func githubBenefitQuotaInTxChecked(tx *gorm.DB) (int, error) {
	var cfg SystemConfig
	if err := tx.Where("key = ?", githubBenefitQuotaKey).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	quota, err := strconv.Atoi(strings.TrimSpace(cfg.Value))
	if err != nil {
		return 0, fmt.Errorf("invalid github benefit quota %s=%q: %w", githubBenefitQuotaKey, cfg.Value, err)
	}
	if quota < 0 {
		return 0, fmt.Errorf("invalid github benefit quota %s=%q", githubBenefitQuotaKey, cfg.Value)
	}
	return quota, nil
}

func githubBenefitQuotaChecked() (int, error) {
	raw, err := getSystemConfigStringChecked(githubBenefitQuotaKey)
	if err != nil {
		return 0, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}
	quota, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid github benefit quota %s=%q: %w", githubBenefitQuotaKey, raw, err)
	}
	if quota < 0 {
		return 0, fmt.Errorf("invalid github benefit quota %s=%q", githubBenefitQuotaKey, raw)
	}
	return quota, nil
}

func githubBenefitEnabledChecked() (bool, error) {
	raw, err := getSystemConfigStringChecked(githubBenefitEnabledKey)
	if err != nil {
		return false, err
	}
	return strings.EqualFold(strings.TrimSpace(raw), "true"), nil
}

type githubBenefitAdminStats struct {
	ClaimedCount int64
	PendingCount int64
}

func loadGithubBenefitAdminStats(now time.Time) (githubBenefitAdminStats, error) {
	var stats githubBenefitAdminStats
	if err := DB.Model(&GithubBenefitClaim{}).
		Where("status = ?", githubBenefitStatusClaimed).
		Count(&stats.ClaimedCount).Error; err != nil {
		return stats, err
	}
	if err := DB.Model(&GithubBenefitClaim{}).
		Where("status = ? AND expires_at > ?", githubBenefitStatusPending, now).
		Count(&stats.PendingCount).Error; err != nil {
		return stats, err
	}
	return stats, nil
}

func githubBenefitAdminCountText(value int64, available bool) string {
	if !available {
		return "读取失败"
	}
	return strconv.FormatInt(value, 10)
}

func githubBenefitUserErrorText(err error) string {
	switch {
	case errors.Is(err, errGithubBenefitDisabled):
		return "GitHub 福利暂未开启，请等待管理员开放。"
	case errors.Is(err, errGithubBenefitQuotaExhausted):
		return "本期 GitHub 福利名额已发完，请等待下次开放。"
	case errors.Is(err, errGithubBenefitAlreadyTG):
		return "你的 TG 账号已经领取过 GitHub 福利，不能重复领取。"
	case errors.Is(err, errGithubBenefitAlreadyGithub):
		return "该 GitHub 账号已经领取过福利，不能重复领取。"
	case errors.Is(err, errGithubBenefitPendingMissing):
		return "请先发送 `github福利` 获取本次专属校验码。"
	case errors.Is(err, errGithubBenefitPendingExpired):
		return "本次 GitHub 校验码已过期，请重新发送 `github福利` 获取新校验码。"
	case errors.Is(err, errGithubBenefitInvalidLogin):
		return "未找到该 GitHub 用户，或用户名格式不正确。"
	case errors.Is(err, errGithubBenefitNotOldEnough):
		return "该 GitHub 账号注册未满 5 年，暂不符合领取条件。"
	case errors.Is(err, errGithubBenefitBioMismatch):
		return "未在该 GitHub 账号 Bio 中找到本次校验码。请确认已保存 Bio 后再重试。"
	case errors.Is(err, errSecurityPepperNotConfigured), errors.Is(err, errCreateInviteCodeFailed), errors.Is(err, errCreateRenewCodeFailed):
		return "卡密生成失败，本次领取未消耗名额，请稍后重试。"
	default:
		return "GitHub 福利处理失败，请稍后重试。"
	}
}

func handleGithubBenefitAdminCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState, pendingAdmin bool) bool {
	if !isSuperAdmin(msg.From.ID) {
		sendPlainText(bot, msg.Chat.ID, "权限不足：GitHub 福利管理仅限超级管理员。")
		if pendingAdmin {
			clearSession(msg.From.ID)
		}
		return true
	}
	if !msg.Chat.IsPrivate() {
		registerIncomingGroupCommandForAutoDelete(msg)
		sendPlainText(bot, msg.Chat.ID, "GitHub 福利管理命令必须私聊 Bot 执行。")
		if pendingAdmin {
			clearSession(msg.From.ID)
		}
		return true
	}
	if pendingAdmin {
		return handleGithubBenefitAdminSession(bot, msg, text, session)
	}
	if text == "查看github福利" {
		sendPlainText(bot, msg.Chat.ID, formatGithubBenefitAdminStatus())
		writeAuditLog(msg.From.ID, "VIEW_GITHUB_BENEFIT", "github_benefit", "超级管理员查看 GitHub 福利状态")
		return true
	}
	if text == "设置github福利名额" {
		session.SetStep("WAITING_GITHUB_BENEFIT_ADMIN_QUOTA")
		UserSessions.Store(msg.From.ID, session)
		quotaText := "读取失败"
		if quota, err := githubBenefitQuotaChecked(); err == nil {
			quotaText = strconv.Itoa(quota)
		} else {
			log.Printf("GitHub 福利名额读取失败: actor=%d err=%s", msg.From.ID, formatPlainError(err))
		}
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("当前 GitHub 福利剩余名额：%s。\n\n请输入新的剩余名额，范围 0-10000。", quotaText))
		return true
	}
	if !isGithubBenefitAdminWriteCommand(text) {
		sendPlainText(bot, msg.Chat.ID, "GitHub 福利管理指令格式错误。")
		return true
	}

	normalized := normalizeGithubBenefitAdminWriteCommand(text)
	session.SetTemp("github_benefit_admin_command", normalized)
	session.SetStep("WAITING_GITHUB_BENEFIT_ADMIN_REASON")
	UserSessions.Store(msg.From.ID, session)
	sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("高危操作：该指令会修改 GitHub 福利开放状态或名额。\n\n待执行指令：\n%s\n\n请输入本次变更原因，%s：", formatPlainValue(normalized), adminReasonRequirementText))
	return true
}

func handleGithubBenefitAdminSession(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState) bool {
	switch session.GetStep() {
	case "WAITING_GITHUB_BENEFIT_ADMIN_QUOTA":
		quota, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || quota < 0 || quota > 10000 {
			sendPlainText(bot, msg.Chat.ID, "名额必须是 0-10000 的整数，请重新输入。")
			return true
		}
		command := fmt.Sprintf("设置github福利名额 %d", quota)
		session.SetTemp("github_benefit_admin_command", command)
		session.SetStep("WAITING_GITHUB_BENEFIT_ADMIN_REASON")
		UserSessions.Store(msg.From.ID, session)
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("高危操作：该指令会修改 GitHub 福利名额。\n\n待执行指令：\n%s\n\n请输入本次变更原因，%s：", formatPlainValue(command), adminReasonRequirementText))
		return true

	case "WAITING_GITHUB_BENEFIT_ADMIN_REASON":
		reason, ok := validateAdminReason(text)
		if !ok {
			sendPlainText(bot, msg.Chat.ID, adminReasonInvalidText)
			return true
		}
		command := session.GetTemp("github_benefit_admin_command")
		if !isGithubBenefitAdminWriteCommand(command) {
			sendPlainText(bot, msg.Chat.ID, "GitHub 福利管理会话异常，已中止。")
			clearSession(msg.From.ID)
			return true
		}
		session.SetTemp("github_benefit_admin_reason", reason)
		session.SetStep("WAITING_CONFIRM_GITHUB_BENEFIT_ADMIN")
		UserSessions.Store(msg.From.ID, session)
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("高危操作二次确认：\n\n待执行指令：\n%s\n\n原因：%s\n\n确认执行请回复：%s", formatPlainValue(command), formatPlainValue(reason), githubBenefitConfirmCommand))
		return true

	case "WAITING_CONFIRM_GITHUB_BENEFIT_ADMIN":
		if text != githubBenefitConfirmCommand {
			sendPlainText(bot, msg.Chat.ID, "已取消 GitHub 福利管理操作。")
			clearSession(msg.From.ID)
			return true
		}
		command := session.GetTemp("github_benefit_admin_command")
		reason, ok := validateAdminReason(session.GetTemp("github_benefit_admin_reason"))
		if !ok || !isGithubBenefitAdminWriteCommand(command) {
			sendPlainText(bot, msg.Chat.ID, "GitHub 福利管理会话异常，已中止。")
			clearSession(msg.From.ID)
			return true
		}
		reply, err := executeGithubBenefitAdminWriteCommand(msg.From.ID, command, reason)
		if err != nil {
			log.Printf("GitHub 福利管理写入失败: actor=%d command=%s err=%s", msg.From.ID, formatPlainValue(command), formatPlainError(err))
			sendPlainText(bot, msg.Chat.ID, "GitHub 福利配置更新失败，请稍后重试。")
			clearSession(msg.From.ID)
			return true
		}
		sendPlainText(bot, msg.Chat.ID, reply)
		clearSession(msg.From.ID)
		return true
	}
	clearSession(msg.From.ID)
	sendPlainText(bot, msg.Chat.ID, "GitHub 福利管理会话异常，已中止。")
	return true
}

func isGithubBenefitAdminCommand(text string) bool {
	text = strings.TrimSpace(text)
	return text == "查看github福利" || text == "设置github福利名额" || isGithubBenefitAdminWriteCommand(text)
}

func isGithubBenefitAdminWriteCommand(text string) bool {
	text = normalizeGithubBenefitAdminWriteCommand(text)
	return text == "开启github福利" ||
		text == "关闭github福利" ||
		strings.HasPrefix(text, "设置github福利名额 ")
}

func normalizeGithubBenefitAdminWriteCommand(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "确认") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "确认"))
	}
	return text
}

func executeGithubBenefitAdminWriteCommand(actorID int64, text string, reason string) (string, error) {
	text = normalizeGithubBenefitAdminWriteCommand(text)
	switch {
	case text == "开启github福利":
		oldEnabled, err := setGithubBenefitEnabledWithAudit(actorID, true, reason)
		if err != nil {
			return "", err
		}
		quotaText := "读取失败"
		if quota, err := githubBenefitQuotaChecked(); err == nil {
			quotaText = strconv.Itoa(quota)
		}
		return fmt.Sprintf("GitHub 福利已开启。原状态：%t，当前剩余名额：%s。", oldEnabled, quotaText), nil
	case text == "关闭github福利":
		oldEnabled, err := setGithubBenefitEnabledWithAudit(actorID, false, reason)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("GitHub 福利已关闭。原状态：%t。", oldEnabled), nil
	case strings.HasPrefix(text, "设置github福利名额 "):
		parts := strings.Fields(text)
		if len(parts) != 2 {
			return "", fmt.Errorf("INVALID_GITHUB_BENEFIT_QUOTA_COMMAND")
		}
		quota, err := strconv.Atoi(parts[1])
		if err != nil || quota < 0 || quota > 10000 {
			return "", fmt.Errorf("INVALID_GITHUB_BENEFIT_QUOTA")
		}
		oldQuota, err := setGithubBenefitQuotaWithAudit(actorID, quota, reason)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("GitHub 福利名额已更新：%d -> %d。", oldQuota, quota), nil
	default:
		return "", fmt.Errorf("UNKNOWN_GITHUB_BENEFIT_ADMIN_COMMAND")
	}
}

func setGithubBenefitEnabledWithAudit(actorID int64, enabled bool, reason string) (bool, error) {
	oldEnabled := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		txOldEnabled, err := githubBenefitEnabledInTxChecked(tx)
		if err != nil {
			return err
		}
		if err := upsertSystemConfigValueInTx(tx, githubBenefitEnabledKey, strconv.FormatBool(enabled)); err != nil {
			return err
		}
		if err := writeAuditLogInTx(
			tx,
			actorID,
			"SET_GITHUB_BENEFIT_ENABLED",
			githubBenefitEnabledKey,
			0,
			fmt.Sprintf("GitHub benefit enabled changed from %t to %t; reason: %s", txOldEnabled, enabled, formatPlainValue(reason)),
		); err != nil {
			return err
		}
		oldEnabled = txOldEnabled
		return nil
	})
	if err != nil {
		return false, err
	}
	return oldEnabled, nil
}

func setGithubBenefitQuotaWithAudit(actorID int64, quota int, reason string) (int, error) {
	oldQuota := 0
	err := DB.Transaction(func(tx *gorm.DB) error {
		txOldQuota, err := githubBenefitQuotaInTxChecked(tx)
		if err != nil {
			return err
		}
		if err := upsertSystemConfigValueInTx(tx, githubBenefitQuotaKey, strconv.Itoa(quota)); err != nil {
			return err
		}
		if err := writeAuditLogInTx(
			tx,
			actorID,
			"SET_GITHUB_BENEFIT_QUOTA",
			githubBenefitQuotaKey,
			quota-txOldQuota,
			fmt.Sprintf("GitHub benefit quota changed from %d to %d; reason: %s", txOldQuota, quota, formatPlainValue(reason)),
		); err != nil {
			return err
		}
		oldQuota = txOldQuota
		return nil
	})
	if err != nil {
		return 0, err
	}
	return oldQuota, nil
}

func formatGithubBenefitAdminStatus() string {
	status := "关闭"
	if enabled, err := githubBenefitEnabledChecked(); err != nil {
		log.Printf("load github benefit enabled failed: err=%s", formatPlainError(err))
		status = "读取失败"
	} else if enabled {
		status = "开启"
	}
	quotaText := "读取失败"
	if quota, err := githubBenefitQuotaChecked(); err != nil {
		log.Printf("load github benefit quota failed: err=%s", formatPlainError(err))
	} else {
		quotaText = strconv.Itoa(quota)
	}
	stats, statsErr := loadGithubBenefitAdminStats(time.Now())
	statsAvailable := statsErr == nil
	if statsErr != nil {
		log.Printf("load github benefit admin stats failed: err=%s", formatPlainError(statsErr))
	}
	return fmt.Sprintf(
		"GitHub 福利状态\n\n开放状态：%s\n剩余名额：%s\n已领取：%s\n有效待校验：%s\n\n用户入口：github福利\n校验方式：GitHub Bio\n账号年限：固定 %d 年\n奖励：未绑定 ABS 发邀请码 1 个；已绑定 ABS 发 150 天续期卡",
		status,
		quotaText,
		githubBenefitAdminCountText(stats.ClaimedCount, statsAvailable),
		githubBenefitAdminCountText(stats.PendingCount, statsAvailable),
		githubBenefitMinAccountAge,
	)
}
