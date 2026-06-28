package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"math"
	"math/big"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ==========================================
// 🛡️ 状态机底层：高并发安全封装与防泄漏
// ==========================================
type SessionState struct {
	step      string            // 私有化，强制走方法
	tempData  map[string]string // 私有化，强制走方法
	mu        sync.RWMutex      // 升级为读写锁，提升超高频读取时的并发性能
	updatedAt time.Time         // 🛡️ 新增：最后活跃时间，供清道夫协程识别僵尸会话
}

var callbackAckStates sync.Map // map[string]*atomic.Bool

// SetStep 安全写入当前步骤
func (s *SessionState) SetStep(step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.step = step
	s.updatedAt = time.Now()
}

// GetStep 安全读取当前步骤
func (s *SessionState) GetStep() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.step
}

// SetTemp 安全写入临时数据
func (s *SessionState) SetTemp(key, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tempData == nil {
		s.tempData = make(map[string]string)
	}
	s.tempData[key] = val
	s.updatedAt = time.Now()
}

// GetTemp 安全读取临时数据
func (s *SessionState) GetTemp(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.tempData == nil {
		return ""
	}
	return s.tempData[key]
}

type AutoDeleteMsg struct {
	ID        uint  `gorm:"primaryKey"`
	ChatID    int64 `gorm:"index"`
	MessageID int
	DeleteAt  time.Time `gorm:"index"`
}

var UserSessions sync.Map
var absClient *AbsClient
var sweeperOnce sync.Once
var groupMemberCache sync.Map  // 缓存群成员状态，防 TG 接口频控
var fusionPoolMutex sync.Mutex // 🌊 新增：天道奖池独立并发锁

const (
	groupMemberPositiveTTL = 5 * time.Minute
	groupMemberNegativeTTL = 1 * time.Minute
	groupMemberFreshTTL    = 30 * time.Second
	blindBoxCost           = 20
)

func getSession(userID int64) *SessionState {
	val, _ := UserSessions.LoadOrStore(userID, &SessionState{
		step:      "IDLE",
		tempData:  make(map[string]string),
		updatedAt: time.Now(),
	})
	return val.(*SessionState)
}

func clearSession(userID int64) {
	UserSessions.Delete(userID)
}

func generateRandomCode(length int) string {
	if length <= 0 {
		length = 16
	}

	bytes := make([]byte, (length+1)/2)
	if _, err := rand.Read(bytes); err != nil {
		log.Printf("❌ 生成随机码失败: %s", formatPlainError(err))
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}

	code := hex.EncodeToString(bytes)
	if len(code) > length {
		code = code[:length]
	}
	return code
}

func getManualPillUsageConfig(itemName string, t time.Time) (periodStart time.Time, periodKey string, maxCount int, cycleName string, addHours float64, ok bool) {
	loc := time.FixedZone("CST", 8*3600)
	now := t.In(loc)

	switch itemName {
	case "聚灵丹":
		maxCount = 3
		cycleName = "本周"
		addHours = 1.0
	case "九转造化丹":
		maxCount = 2
		cycleName = "本周"
		addHours = 3.0
	case "万年仙玉髓":
		maxCount = 1
		cycleName = "本月"
		addHours = 10.0
	default:
		return time.Time{}, "", 0, "", 0, false
	}

	if itemName == "万年仙玉髓" {
		periodStart = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		periodKey = fmt.Sprintf("%04d-%02d", now.Year(), int(now.Month()))
	} else {
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		todayZero := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		periodStart = todayZero.AddDate(0, 0, -(weekday - 1))
		isoYear, isoWeek := now.ISOWeek()
		periodKey = fmt.Sprintf("%04d-W%02d", isoYear, isoWeek)
	}

	return periodStart, periodKey, maxCount, cycleName, addHours, true
}

func countManualPillUsage(userID int64, itemName string, periodStart time.Time) (int64, error) {
	var usedCount int64
	err := DB.Model(&ItemUsageLog{}).
		Where("user_id = ? AND item_name = ? AND used_at >= ?", userID, itemName, periodStart).
		Count(&usedCount).Error
	return usedCount, err
}

func manualPillUsageCountText(usedCount int64, maxCount int, available bool) string {
	if !available {
		return fmt.Sprintf("读取失败/%d", maxCount)
	}
	return fmt.Sprintf("%d/%d", usedCount, maxCount)
}

// ==========================================
// 🌊 天道奖池注水引擎 (核心级并发防护)
// ==========================================

// 返回值：currentPool(当前进度), isBurst(是否触发了300分爆包)
func addPointsToFusionPool(pointsToAdd int) (int, bool) {
	currentPool, isBurst, err := addPointsToFusionPoolWithError(pointsToAdd)
	if err != nil {
		log.Printf("⚠️ 天道奖池注水失败: points=%d err=%s", pointsToAdd, formatPlainError(err))
		return 0, false
	}

	return currentPool, isBurst
}

func addPointsToFusionPoolWithError(pointsToAdd int) (int, bool, error) {
	currentPool := 0
	isBurst := false
	err := runFusionPoolLockedTransaction(func(tx *gorm.DB) error {
		if pointsToAdd <= 0 {
			var poolCfg SystemConfig
			if err := tx.Where("key = ?", "fusion_pool_points").First(&poolCfg).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return nil
				}
				return err
			}
			parsedPool, err := strconv.Atoi(strings.TrimSpace(poolCfg.Value))
			if err != nil {
				return err
			}
			currentPool = parsedPool
			return nil
		}

		var err error
		currentPool, isBurst, err = addPointsToFusionPoolInTx(tx, pointsToAdd)
		return err
	})
	if err != nil {
		return 0, false, err
	}

	return currentPool, isBurst, nil
}

func runFusionPoolLockedTransaction(fn func(tx *gorm.DB) error) error {
	if fn == nil {
		return fmt.Errorf("FUSION_POOL_TX_EMPTY")
	}

	fusionPoolMutex.Lock()
	defer fusionPoolMutex.Unlock()

	return DB.Transaction(fn)
}

func notifyFusionPoolBurst(bot *tgbotapi.BotAPI, fallbackChatID int64, reason string) {
	if bot == nil {
		return
	}

	targetChatID := AppConfig.NoticeGroupID
	if targetChatID == 0 {
		targetChatID = fallbackChatID
	}
	if targetChatID == 0 {
		return
	}

	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "众道友引动天地异象"
	}

	// 当前 reason 都是代码内固定文案；这里额外转义，防止未来误传用户输入导致 Markdown 格式注入。
	reason = escapeMarkdown(reason)

	announce := fmt.Sprintf(
		"🌈 **【天降甘霖·仙气化雨】** 🌈\n"+
			"%s，天道奖池已蓄满并自动爆开！\n\n"+
			"💰 降下红包: `300` 积分\n"+
			"📦 福泽份数: `30` 份\n\n"+
			"👇 众修士快回复关键字 【`沾仙气`】 汲取天地造化！",
		reason,
	)

	go sendGroupAutoDeleteMessage(bot, targetChatID, announce)
}

// ==========================================
// 🛡️ 核心基建：强健的权限与发卡防抖组件
// ==========================================
type groupMemberCacheEntry struct {
	inGroup  bool
	expireAt time.Time
}

func isUserInGroup(bot *tgbotapi.BotAPI, userID int64, groupID int64) bool {
	if groupID == 0 {
		return true
	}

	cacheKey := fmt.Sprintf("%d:%d", groupID, userID)

	if cached, ok := groupMemberCache.Load(cacheKey); ok {
		if entry, ok := cached.(*groupMemberCacheEntry); ok && time.Now().Before(entry.expireAt) {
			return entry.inGroup
		}
	}

	member, err := bot.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: groupID,
			UserID: userID,
		},
	})
	if err != nil {
		groupMemberCache.Store(cacheKey, &groupMemberCacheEntry{
			inGroup:  false,
			expireAt: time.Now().Add(groupMemberNegativeTTL),
		})
		return false
	}

	inGroup := member.Status == "member" ||
		member.Status == "creator" ||
		member.Status == "administrator" ||
		member.Status == "restricted"

	ttl := groupMemberNegativeTTL
	if inGroup {
		ttl = groupMemberPositiveTTL
	}

	groupMemberCache.Store(cacheKey, &groupMemberCacheEntry{
		inGroup:  inGroup,
		expireAt: time.Now().Add(ttl),
	})

	return inGroup
}

func isUserInGroupFresh(bot *tgbotapi.BotAPI, userID int64, groupID int64) bool {
	if groupID == 0 {
		return true
	}

	cacheKey := fmt.Sprintf("%d:%d", groupID, userID)

	if cached, ok := groupMemberCache.Load(cacheKey); ok {
		if entry, ok := cached.(*groupMemberCacheEntry); ok && time.Now().Before(entry.expireAt) {
			return entry.inGroup
		}
	}

	member, err := bot.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: groupID,
			UserID: userID,
		},
	})
	if err != nil {
		log.Printf("⚠️ 实时群成员校验失败: user=%d group=%d err=%s", userID, groupID, formatTelegramSendError(err))
		groupMemberCache.Store(cacheKey, &groupMemberCacheEntry{
			inGroup:  false,
			expireAt: time.Now().Add(groupMemberNegativeTTL),
		})
		return false
	}

	inGroup := member.Status == "member" ||
		member.Status == "creator" ||
		member.Status == "administrator" ||
		member.Status == "restricted"

	groupMemberCache.Store(cacheKey, &groupMemberCacheEntry{
		inGroup:  inGroup,
		expireAt: time.Now().Add(groupMemberFreshTTL),
	})

	return inGroup
}

func isMessageFromNoticeGroup(msg *tgbotapi.Message) bool {
	return AppConfig != nil &&
		AppConfig.NoticeGroupID != 0 &&
		msg != nil &&
		msg.Chat != nil &&
		msg.Chat.ID == AppConfig.NoticeGroupID
}

func createRenewCodeRecord(tx *gorm.DB, rawCode string, days int) error {
	if tx == nil {
		tx = DB
	}

	codeHash := hashSensitiveToken(rawCode)
	if codeHash == "" {
		return errSecurityPepperNotConfigured
	}

	res := tx.Create(&RenewCode{
		Code:        "internal-renew-" + generateRandomCode(16),
		CodeHash:    codeHash,
		CodePreview: maskSecret(rawCode),
		Days:        days,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("RENEW_CODE_CREATE_MISSED")
	}
	return nil
}

func createInviteCodeRecord(tx *gorm.DB, rawCode string) error {
	if tx == nil {
		tx = DB
	}

	codeHash := hashSensitiveToken(rawCode)
	if codeHash == "" {
		return errSecurityPepperNotConfigured
	}

	res := tx.Create(&InviteCode{
		Code:        "internal-invite-" + generateRandomCode(16),
		CodeHash:    codeHash,
		CodePreview: maskSecret(rawCode),
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("INVITE_CODE_CREATE_MISSED")
	}
	return nil
}

func getUserRoleFromDBChecked(db *gorm.DB, userID int64) (string, error) {
	if AppConfig != nil && AppConfig.AdminIDs != nil && AppConfig.AdminIDs[userID] {
		return "super_admin", nil
	}
	if db == nil {
		return "user", fmt.Errorf("ROLE_DB_EMPTY")
	}
	var u User
	err := db.Where("telegram_id = ?", userID).First(&u).Error
	if err == nil {
		if strings.TrimSpace(u.Role) == "" {
			return "user", nil
		}
		return u.Role, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "user", nil
	}
	return "user", err
}

func getUserRoleFromDB(db *gorm.DB, userID int64) string {
	role, err := getUserRoleFromDBChecked(db, userID)
	if err != nil {
		log.Printf("⚠️ 用户角色读取失败，按普通用户处理: user=%d err=%s", userID, formatPlainError(err))
	}
	return role
}

func getUserRole(userID int64) string {
	return getUserRoleFromDB(DB, userID)
}

func getAuditActorRoleInTx(tx *gorm.DB, actorID int64) (string, error) {
	if actorID == 0 {
		return "system", nil
	}
	return getUserRoleFromDBChecked(tx, actorID)
}

func isSuperAdmin(userID int64) bool {
	return getUserRole(userID) == "super_admin"
}

func requireSuperAdmin(bot *tgbotapi.BotAPI, chatID int64, userID int64) bool {
	if !isSuperAdmin(userID) {
		replyText(bot, chatID, "❌ 权限不足：该操作仅限超级管理员。")
		return false
	}
	return true
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func adminAdjustDailyLimitExceeded(todayTotal int, delta int) bool {
	return todayTotal+absInt(delta) > 20000
}

func ghostWalletUsername(userID int64) string {
	return fmt.Sprintf("ghost_tg_%d", userID)
}

func ensureUserWalletInTx(tx *gorm.DB, tgUser *tgbotapi.User) (User, string, error) {
	if tgUser == nil {
		return User{}, "", errTelegramUserMissing
	}

	displayName := getTelegramDisplayName(tgUser)
	var u User
	err := tx.Where("telegram_id = ?", tgUser.ID).First(&u).Error
	if err == nil {
		return u, displayName, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return User{}, displayName, err
	}

	u = User{
		TelegramID: tgUser.ID,
		Username:   ghostWalletUsername(tgUser.ID),
		Points:     0,
	}
	res := tx.Create(&u)
	if res.Error != nil {
		if isUniqueConstraintError(res.Error) {
			if retryErr := tx.Where("telegram_id = ?", tgUser.ID).First(&u).Error; retryErr == nil {
				return u, displayName, nil
			}
		}
		return User{}, displayName, res.Error
	}
	if res.RowsAffected == 0 {
		return User{}, displayName, fmt.Errorf("USER_WALLET_CREATE_MISSED")
	}

	return u, displayName, nil
}

func ensureUserWallet(tgUser *tgbotapi.User) (User, string, error) {
	var u User
	var displayName string
	err := DB.Transaction(func(tx *gorm.DB) error {
		txUser, txDisplayName, innerErr := ensureUserWalletInTx(tx, tgUser)
		if innerErr != nil {
			return innerErr
		}
		u = txUser
		displayName = txDisplayName
		return nil
	})
	if err != nil {
		return User{}, "", err
	}
	return u, displayName, nil
}

func executeBlindBoxOpen(tgUser *tgbotapi.User) (string, string, error) {
	if tgUser == nil {
		return "", "", errTelegramUserMissing
	}

	userID := tgUser.ID
	safeName := escapeMarkdown(tgUser.FirstName)
	resultPrefix := fmt.Sprintf("📦 盲盒开启中...\n💰 已扣除 `%d` 积分。\n\n", blindBoxCost)

	var txReplyMsg, txBroadcastMsg string
	blindBoxRefID := fmt.Sprintf("blind_box:%d:%s", userID, generateRandomCode(8))
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := applyPointDeltaInTx(
			tx,
			userID,
			-blindBoxCost,
			"blind_box_cost",
			fmt.Sprintf("开启积分盲盒，消耗 %d 积分", blindBoxCost),
			"blind_box",
			blindBoxRefID,
		); err != nil {
			if errors.Is(err, errPointsNotEnough) {
				return errPointsNotEnough
			}
			return err
		}

		nBig, err := rand.Int(rand.Reader, big.NewInt(100))
		if err != nil {
			return err
		}
		roll := int(nBig.Int64()) + 1

		switch {
		case roll <= 50:
			txReplyMsg = resultPrefix + "💨 噗~ 里面空空如也。\n**【谢谢惠顾】**\n\n别灰心，垫子已经铺好，下发必出金！"
		case roll <= 89:
			code := fmt.Sprintf("R%d-%s", 3, generateRandomCode(16))
			if err := createRenewCodeRecord(tx, code, 3); err != nil {
				return err
			}
			txReplyMsg = resultPrefix + fmt.Sprintf("🎉 恭喜获得保底小奖：**【3天续期卡】**！\n💳 专属卡密：`%s`\n(卡密已升级为16位安全密钥，请在此发送充值)", code)
		case roll <= 94:
			code := generateRandomCode(16)
			if err := createInviteCodeRecord(tx, code); err != nil {
				return err
			}
			txReplyMsg = resultPrefix + fmt.Sprintf("🎉 运气不错！恭喜获得：**【专属邀请码】**！\n🎫 邀请码：`%s`\n(可直接用于开户)", code)
		case roll <= 99:
			code := fmt.Sprintf("R%d-%s", 30, generateRandomCode(16))
			if err := createRenewCodeRecord(tx, code, 30); err != nil {
				return err
			}
			txReplyMsg = resultPrefix + fmt.Sprintf("🎊 运气爆棚！获得大奖：**【30天续期月卡】**！\n💳 专属卡密：`%s`", code)
			txBroadcastMsg = fmt.Sprintf("🎰 **欧皇降临！**\n\n恭喜 @%s 在积分盲盒中单抽入魂，斩获大奖 **【💳 30天续期月卡】**！", safeName)
		case roll <= 100:
			code := fmt.Sprintf("R%d-%s", 365, generateRandomCode(16))
			if err := createRenewCodeRecord(tx, code, 365); err != nil {
				return err
			}
			txReplyMsg = resultPrefix + fmt.Sprintf("👑 欧皇附体！！！获得终极大奖：**【365天尊享年卡】**！！！\n💳 专属卡密：`%s`", code)
			txBroadcastMsg = fmt.Sprintf("👑👑 **全服通报：终极欧皇诞生！** 👑👑\n\n天呐！@%s 爆发了惊人气运，直接抽中了终极大奖 **【👑 365天尊享年卡】**！！！", safeName)
		}
		return nil
	})

	if err != nil {
		return "", "", err
	}

	return txReplyMsg, txBroadcastMsg, nil
}

func writeAuditLog(actorID int64, action string, target string, detail string) {
	writeAuditLogWithDelta(actorID, action, target, 0, detail)
}

func writeAuditLogWithDelta(actorID int64, action string, target string, delta int, detail string) {
	if err := writeAuditLogInTx(DB, actorID, action, target, delta, detail); err != nil {
		log.Printf("⚠️ 写入审计日志失败: actor=%d action=%s target=%s err=%s", actorID, formatPlainValue(action), formatPlainValue(target), formatPlainError(err))
	}
}

func writeAuditLogInTx(tx *gorm.DB, actorID int64, action string, target string, delta int, detail string) error {
	if tx == nil {
		return fmt.Errorf("AUDIT_TX_EMPTY")
	}
	role, err := getAuditActorRoleInTx(tx, actorID)
	if err != nil {
		return err
	}

	target = formatAuditTextForStorage(target, auditTargetMaxRunes)
	detail = formatAuditTextForStorage(detail, auditDetailMaxRunes)

	res := tx.Create(&AuditLog{
		ActorID:   actorID,
		ActorRole: role,
		Action:    action,
		Target:    target,
		Delta:     delta,
		Detail:    detail,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("AUDIT_LOG_CREATE_MISSED")
	}
	return nil
}

const (
	auditTargetMaxRunes = 200
	auditDetailMaxRunes = 1000
)

func formatAuditTextForStorage(text string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	formatted := formatAuditTextForDisplay(text)
	runes := []rune(formatted)
	if len(runes) <= maxRunes {
		return formatted
	}
	if maxRunes <= 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}

func formatAuditTextForDisplay(text string) string {
	return formatDiagnosticTextForDisplay(text)
}

func normalizeAuditTextForReadability(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	lastSpace := false
	for _, r := range text {
		if unicode.Is(unicode.Cf, r) {
			continue
		}
		if r <= ' ' || r == 0x7f || r == '\u2028' || r == '\u2029' {
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
			continue
		}
		b.WriteRune(r)
		lastSpace = false
	}
	return strings.TrimSpace(b.String())
}

func formatDiagnosticTextForDisplay(text string) string {
	return normalizeAuditTextForReadability(redactSensitiveAuditText(text))
}

func redactSensitiveAuditText(text string) string {
	if text == "" {
		return ""
	}
	text = stripUnicodeFormatControls(text)

	patterns := []struct {
		re   *regexp.Regexp
		repl string
	}{
		{
			re:   regexp.MustCompile("(?i)(\"?(?:password|token|api[_-]?key|authorization|secret|security[_-]?code|backup_encrypt_key|security_pepper|telegram_bot_token)\"?\\s*[:=]\\s*)(\"[^\"]*\"|`[^`]*`|bearer\\s+[A-Za-z0-9._~+/=-]+|[^\\s&,，;；}]+)"),
			repl: "${1}***",
		},
		{
			re:   regexp.MustCompile("((?:密码|安全码|卡密|邀请码|续期卡|备份密钥|安全密钥)\\s*[:：=]\\s*)(`[^`]*`|[^\\s,，;；。]+)"),
			repl: "${1}***",
		},
		{
			re:   regexp.MustCompile("(?i)\\b(TELEGRAM_BOT_TOKEN|ABS_API_KEY|BACKUP_ENCRYPT_KEY|SECURITY_PEPPER)\\s*[:=]\\s*[^\\s&,，;；]+"),
			repl: "${1}=***",
		},
		{
			re:   regexp.MustCompile("(?i)\\b(token|api[_-]?key|password|secret|authorization|security[_-]?code|backup_encrypt_key|security_pepper|telegram_bot_token)=([^&\\s\"'`,;]+)"),
			repl: "${1}=***",
		},
		{
			re:   regexp.MustCompile(`(?i)(https://api\.telegram\.org/)bot[^/\s]+/`),
			repl: "${1}bot***:***/",
		},
		{
			re:   regexp.MustCompile("(?i)(https?://)([^\\s/@:]+):([^\\s/@]+)@"),
			repl: "${1}***:***@",
		},
		{
			re:   regexp.MustCompile("(?i)bearer\\s+[A-Za-z0-9._~+/=-]+"),
			repl: "Bearer ***",
		},
	}

	redacted := text
	for _, pattern := range patterns {
		redacted = pattern.re.ReplaceAllString(redacted, pattern.repl)
	}
	return redacted
}

func stripUnicodeFormatControls(text string) string {
	if text == "" {
		return ""
	}
	var b strings.Builder
	changed := false
	for _, r := range text {
		if unicode.Is(unicode.Cf, r) {
			changed = true
			continue
		}
		b.WriteRune(r)
	}
	if !changed {
		return text
	}
	return b.String()
}

func formatMarkdownError(err error) string {
	if err == nil {
		return ""
	}
	return escapeMarkdown(truncateRunes(formatDiagnosticTextForDisplay(err.Error()), 500))
}

func formatPlainError(err error) string {
	if err == nil {
		return ""
	}
	return formatPlainValue(err)
}

func formatPlainValue(value any) string {
	return truncateRunes(formatDiagnosticTextForDisplay(fmt.Sprint(value)), 500)
}

func formatTelegramSendError(err error) string {
	return formatPlainError(err)
}

func formatSystemConfigErrorForMarkdown(text string) string {
	if strings.TrimSpace(text) == "" {
		return "无"
	}
	return escapeMarkdown(truncateRunes(formatDiagnosticTextForDisplay(text), 500))
}

func pointTransactionTypeText(txType string) string {
	switch txType {
	case "sign_in":
		return "每日签到"
	case "sign_streak_bonus":
		return "连签奖励"
	case "blind_box_cost":
		return "盲盒消费"
	case "exchange_invite":
		return "兑换邀请码"
	case "exchange_renew":
		return "兑换续期卡"
	case "redpacket_send":
		return "发红包"
	case "redpacket_grab":
		return "抢红包"
	case "admin_adjust":
		return "管理员调账"
	case "race_bet":
		return "赛马下注"
	case "race_refund":
		return "赛马退款"
	case "race_win":
		return "赛马中奖"
	case "dice_bet":
		return "骰子下注"
	case "dice_refund":
		return "骰子退款"
	case "dice_win":
		return "骰子中奖"
	case "breakthrough_auto_buy":
		return "突破代购"
	case "breakthrough_refund":
		return "突破返还"
	case "breakthrough_fail_penalty":
		return "突破失败惩罚"
	case "breakthrough_splash_penalty":
		return "雷劫外溢惩罚"
	case "sect_create":
		return "创建宗门"
	case "sect_join":
		return "加入宗门"
	case "sect_donate":
		return "宗门捐献"
	case "sect_shop_points":
		return "宗门商店"
	case "sect_secret_realm_reward":
		return "宗门秘境"
	case "sect_horn":
		return "宗门喇叭"
	case "world_horn":
		return "世界喇叭"
	case "shop_buy_item":
		return "聚宝斋购买"
	case "marketplace_buy":
		return "交易行购买"
	case "marketplace_sell":
		return "交易行售出"
	case "marketplace_fee":
		return "交易行手续费"
	case "garden_plot_open":
		return "药园开垦"
	case "garden_seed_buy":
		return "购买种子"
	case "garden_herb_sell":
		return "药铺回收"
	case "garden_recipe_unlock":
		return "参悟丹方"
	case "garden_alchemy_cost":
		return "炼丹炉火"
	case "legacy_compensation":
		return "历史补偿"
	case "world_boss_reward":
		return "世界Boss奖励"
	case "lottery_reward":
		return "积分抽奖奖励"
	case "lottery_entry_cost":
		return "积分抽奖参与"
	case "lottery_loser_refund":
		return "抽奖未中奖返还"
	case "lottery_cancel_refund":
		return "抽奖取消退款"
	case "referral_reward":
		return "邀请拉新奖励"
	default:
		return txType
	}
}

func pointTransactionTypeMarkdown(txType string) string {
	return escapeMarkdown(pointTransactionTypeText(txType))
}

func pointTransactionDescriptionMarkdown(description string) string {
	description = strings.TrimSpace(formatDiagnosticTextForDisplay(description))
	if description == "" {
		return "-"
	}
	return escapeMarkdownPreservingEscapes(description)
}

func signInMonthKey(t time.Time) string {
	loc := time.FixedZone("CST", 8*3600)
	return t.In(loc).Format("200601")
}

func signInDateKey(t time.Time) string {
	loc := time.FixedZone("CST", 8*3600)
	return t.In(loc).Format("2006-01-02")
}

func daysInMonth(t time.Time) int {
	firstOfNextMonth := time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
	lastOfThisMonth := firstOfNextMonth.AddDate(0, 0, -1)
	return lastOfThisMonth.Day()
}

func randomIntRange(min int, max int) int {
	if max <= min {
		return min
	}

	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	if err != nil {
		return min
	}

	return int(nBig.Int64()) + min
}

func calculateSignStreakReward(streak *MonthlySignInStreak, now time.Time) (int, string) {
	fullDays := daysInMonth(now)

	switch {
	case streak.StreakDays == 3 && !streak.Rewarded3Days:
		streak.Rewarded3Days = true
		return 1, "连续签到3天奖励"

	case streak.StreakDays == 7 && !streak.Rewarded7Days:
		streak.Rewarded7Days = true
		return 2, "连续签到7天奖励"

	case streak.StreakDays == 14 && !streak.Rewarded14Days:
		streak.Rewarded14Days = true
		return randomIntRange(3, 5), "连续签到14天奖励"

	case streak.StreakDays == 21 && !streak.Rewarded21Days:
		streak.Rewarded21Days = true
		return randomIntRange(5, 7), "连续签到21天奖励"

	case streak.StreakDays == fullDays && !streak.RewardedFull:
		streak.RewardedFull = true
		return randomIntRange(8, 15), "本月全勤奖励"
	}

	return 0, ""
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}

	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "unique") ||
		strings.Contains(errText, "constraint failed") ||
		strings.Contains(errText, "duplicate")
}

func applyPillAudioTimeInTx(tx *gorm.DB, userID int64, addHours float64) error {
	if tx == nil || userID == 0 || addHours <= 0 {
		return fmt.Errorf("INVALID_PILL_AUDIO_TIME")
	}
	now := time.Now()
	// 注意：cultivations 表没有 updated_at 列（Cultivation 未嵌入 gorm.Model），
	// 之前的 DoUpdates 误写了 "updated_at"，会导致 SQLite 报 "no such column: updated_at"，
	// 进而让整个吞服事务回滚，用户看到“系统繁忙”。这里只更新真实存在的列。
	res := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"pill_audio_time": gorm.Expr("pill_audio_time + ?", addHours),
		}),
	}).Create(&Cultivation{
		UserID:           userID,
		PillAudioTime:    addHours,
		MajorRealm:       0,
		MinorRealm:       1,
		TribulationFails: 0,
		ConsolidateUntil: now.Add(-24 * time.Hour),
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("PILL_AUDIO_TIME_GRANT_MISSED")
	}
	return nil
}

func createItemUsageLogInTx(tx *gorm.DB, logEntry *ItemUsageLog) error {
	if tx == nil || logEntry == nil {
		return fmt.Errorf("ITEM_USAGE_LOG_INVALID")
	}
	entry := *logEntry
	entry.ItemName = strings.TrimSpace(entry.ItemName)
	if entry.UserID == 0 || entry.ItemName == "" {
		return fmt.Errorf("ITEM_USAGE_LOG_INVALID")
	}
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("ITEM_USAGE_LOG_CREATE_MISSED")
	}
	*logEntry = entry
	return nil
}

func createItemUsageQuotaIfMissingInTx(tx *gorm.DB, quota *ItemUsageQuota) error {
	if tx == nil || quota == nil {
		return fmt.Errorf("ITEM_USAGE_QUOTA_INVALID")
	}
	entry := *quota
	entry.ItemName = strings.TrimSpace(entry.ItemName)
	entry.PeriodKey = formatPlainValue(entry.PeriodKey)
	if entry.UserID == 0 || entry.ItemName == "" || entry.PeriodKey == "" {
		return fmt.Errorf("ITEM_USAGE_QUOTA_INVALID")
	}
	res := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "item_name"},
			{Name: "period_key"},
		},
		TargetWhere: clause.Where{Exprs: []clause.Expression{
			clause.Eq{Column: clause.Column{Name: "deleted_at"}, Value: nil},
		}},
		DoNothing: true,
	}).Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	*quota = entry
	return nil
}

func calculateCycleSignReward(dayInCycle int) (int, string) {
	switch dayInCycle {
	case 3:
		return 1, "连续签到3天奖励"
	case 7:
		return 2, "连续签到7天奖励"
	case 14:
		return randomIntRange(3, 5), "连续签到14天奖励"
	case 21:
		return randomIntRange(5, 7), "连续签到21天奖励"
	case 30:
		return randomIntRange(8, 15), "连续签到30天奖励"
	default:
		return 0, ""
	}
}

func signInDayInCycle(streakDays int) int {
	if streakDays <= 0 {
		return 1
	}
	return ((streakDays - 1) % 30) + 1
}

func createSignInLogInTx(tx *gorm.DB, logEntry *SignInLog) error {
	if tx == nil || logEntry == nil {
		return fmt.Errorf("SIGN_IN_LOG_INVALID")
	}
	res := tx.Create(logEntry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SIGN_IN_LOG_CREATE_MISSED")
	}
	return nil
}

func createSignInRewardClaimInTx(tx *gorm.DB, claim *SignInRewardClaim) error {
	if tx == nil || claim == nil {
		return fmt.Errorf("SIGN_IN_REWARD_CLAIM_INVALID")
	}
	entry := *claim
	entry.Description = formatPlainValue(entry.Description)
	entry.RefID = formatPlainValue(entry.RefID)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SIGN_IN_REWARD_CLAIM_CREATE_MISSED")
	}
	return nil
}

func createSignInStreakInTx(tx *gorm.DB, streak *SignInStreak) error {
	if tx == nil || streak == nil {
		return fmt.Errorf("SIGN_IN_STREAK_INVALID")
	}
	entry := *streak
	res := tx.Create(&entry)
	if res.Error != nil {
		if isUniqueConstraintError(res.Error) {
			return errConcurrentSignInRetry
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SIGN_IN_STREAK_CREATE_MISSED")
	}
	*streak = entry
	return nil
}

type signInResult struct {
	UserName string

	BaseBonus        int
	StreakBonus      int
	StreakRewardDesc string

	CurrentStreakDays int
	CycleSeq          int
	DayInCycle        int

	BalanceBeforeBase   int
	BalanceAfterBase    int
	BalanceAfterAll     int
	StreakRewardRefID   string
	StreakRewardGranted bool

	WasBroken bool
}

// handleUserSignIn 执行新版签到逻辑：
// 1. 不按月份统计；
// 2. 只按连续签到天数计算；
// 3. 30 天一轮，3/7/14/21/30 天发奖励；
// 4. 断签后下一次签到从 1 重新计算；
// 5. 使用 sign_in_logs 防重复签到，sign_in_reward_claims 防重复奖励。
func handleUserSignIn(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.From == nil {
		return
	}

	userID := msg.From.ID
	chatID := msg.Chat.ID

	now := time.Now()
	todayKey := signInDateKey(now)
	yesterdayKey := signInDateKey(now.AddDate(0, 0, -1))

	baseBonus := randomIntRange(5, 10)

	var result signInResult

	err := DB.Transaction(func(tx *gorm.DB) error {
		_, displayName, walletErr := ensureUserWalletInTx(tx, msg.From)
		if walletErr != nil {
			return walletErr
		}

		result.UserName = displayName
		if strings.TrimSpace(result.UserName) == "" {
			result.UserName = fmt.Sprintf("%d", userID)
		}

		// 先用签到日志唯一索引兜底防止并发重复签到。
		// 这里先不 Create，等计算出 streak 后再写完整日志。
		var existingLog SignInLog
		if err := tx.Where("user_id = ? AND sign_date = ?", userID, todayKey).First(&existingLog).Error; err == nil {
			return errAlreadySigned
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var streak SignInStreak
		err := tx.Where("user_id = ?", userID).First(&streak).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				streak = SignInStreak{
					UserID:            userID,
					CurrentStreakDays: 0,
					LongestStreakDays: 0,
					TotalSignDays:     0,
					LastSignDate:      "",
					CycleSeq:          1,
					BreakCount:        0,
				}
				if err := createSignInStreakInTx(tx, &streak); err != nil {
					return err
				}
			} else {
				return err
			}
		}

		oldStoredCycleSeq := streak.CycleSeq
		normalizedOldCycleSeq := oldStoredCycleSeq
		if normalizedOldCycleSeq <= 0 {
			normalizedOldCycleSeq = 1
		}
		oldLastSignDate := streak.LastSignDate
		oldCurrentStreakDays := streak.CurrentStreakDays
		oldTotalSignDays := streak.TotalSignDays
		oldBreakCount := streak.BreakCount

		newStreakDays := 1
		newCycleSeq := normalizedOldCycleSeq
		newBreakCount := oldBreakCount
		wasBroken := false

		switch {
		case streak.LastSignDate == "":
			newStreakDays = 1

		case streak.LastSignDate == todayKey:
			return errAlreadySigned

		case streak.LastSignDate == yesterdayKey:
			newStreakDays = streak.CurrentStreakDays + 1
			if newStreakDays <= 0 {
				newStreakDays = 1
			}

			// 连续签到跨过 30 天周期边界时，进入下一轮。
			// 例如第 31 天为新一轮第 1 天，第 33 天触发新一轮 3 天奖励。
			if newStreakDays > 1 && (newStreakDays-1)%30 == 0 {
				newCycleSeq++
			}

		case streak.LastSignDate < yesterdayKey:
			newStreakDays = 1
			newCycleSeq++
			wasBroken = true
			newBreakCount++

		default:
			// last_sign_date 大于 today，说明系统时间或数据异常，禁止继续发奖。
			return errSignDateInFuture
		}

		if newCycleSeq <= 0 {
			newCycleSeq = 1
		}

		dayInCycle := signInDayInCycle(newStreakDays)

		streak.CurrentStreakDays = newStreakDays
		if newStreakDays > streak.LongestStreakDays {
			streak.LongestStreakDays = newStreakDays
		}
		streak.TotalSignDays = oldTotalSignDays + 1
		streak.LastSignDate = todayKey
		streak.LastSignAt = &now
		streak.CycleSeq = newCycleSeq
		streak.BreakCount = newBreakCount

		streakRes := tx.Model(&SignInStreak{}).
			Where("id = ? AND user_id = ? AND last_sign_date = ? AND current_streak_days = ? AND total_sign_days = ? AND cycle_seq = ? AND break_count = ?",
				streak.ID, userID, oldLastSignDate, oldCurrentStreakDays, oldTotalSignDays, oldStoredCycleSeq, oldBreakCount).
			Updates(map[string]interface{}{
				"current_streak_days": streak.CurrentStreakDays,
				"longest_streak_days": streak.LongestStreakDays,
				"total_sign_days":     streak.TotalSignDays,
				"last_sign_date":      streak.LastSignDate,
				"last_sign_at":        streak.LastSignAt,
				"cycle_seq":           streak.CycleSeq,
				"break_count":         streak.BreakCount,
			})
		if streakRes.Error != nil {
			return streakRes.Error
		}
		if streakRes.RowsAffected == 0 {
			return errConcurrentSignInRetry
		}

		// 写签到日志。唯一索引 user_id + sign_date 是最终防线。
		if err := createSignInLogInTx(tx, &SignInLog{
			UserID:          userID,
			SignDate:        todayKey,
			SignAt:          now,
			BasePoints:      baseBonus,
			StreakDaysAfter: newStreakDays,
			CycleSeq:        newCycleSeq,
			DayInCycle:      dayInCycle,
		}); err != nil {
			if isUniqueConstraintError(err) {
				return errAlreadySigned
			}
			return err
		}

		streakBonus, streakDesc := calculateCycleSignReward(dayInCycle)

		rewardGranted := false
		rewardRefID := ""

		if streakBonus > 0 {
			rewardRefID = fmt.Sprintf("sign_cycle:%d:%d:%d", userID, newCycleSeq, dayInCycle)

			claim := SignInRewardClaim{
				UserID:        userID,
				RewardType:    "cycle_streak",
				CycleSeq:      newCycleSeq,
				MilestoneDays: dayInCycle,
				Points:        streakBonus,
				Description:   streakDesc,
				RefID:         rewardRefID,
				ClaimedAt:     now,
			}

			if err := createSignInRewardClaimInTx(tx, &claim); err != nil {
				if isUniqueConstraintError(err) {
					// 理论上今天只能签到一次，不应走到这里。
					// 若并发或历史脏数据触发，则跳过奖励，避免重复发放。
					streakBonus = 0
					streakDesc = ""
				} else {
					return err
				}
			} else {
				rewardGranted = true
			}
		}

		if err := applyPointDeltaInTx(
			tx,
			userID,
			baseBonus,
			"sign_in",
			fmt.Sprintf("每日签到获得 %d 积分", baseBonus),
			"sign_in",
			todayKey,
		); err != nil {
			return err
		}

		var afterBaseUser User
		if err := tx.Select("telegram_id", "username", "points").
			Where("telegram_id = ?", userID).
			First(&afterBaseUser).Error; err != nil {
			return err
		}

		result.BalanceBeforeBase = afterBaseUser.Points - baseBonus
		result.BalanceAfterBase = afterBaseUser.Points
		result.BalanceAfterAll = afterBaseUser.Points

		if streakBonus > 0 && rewardGranted {
			if err := applyPointDeltaInTx(
				tx,
				userID,
				streakBonus,
				"sign_streak_bonus",
				fmt.Sprintf("%s，额外获得 %d 积分", streakDesc, streakBonus),
				"sign_cycle",
				rewardRefID,
			); err != nil {
				return err
			}

			var afterAllUser User
			if err := tx.Select("telegram_id", "username", "points").
				Where("telegram_id = ?", userID).
				First(&afterAllUser).Error; err != nil {
				return err
			}

			result.BalanceAfterAll = afterAllUser.Points
		}

		userSignRes := tx.Model(&User{}).
			Where("telegram_id = ?", userID).
			Update("last_sign_at", &now)
		if userSignRes.Error != nil {
			return userSignRes.Error
		}
		if userSignRes.RowsAffected == 0 {
			return fmt.Errorf("SIGN_USER_LAST_SIGN_UPDATE_MISSED")
		}

		result.BaseBonus = baseBonus
		result.StreakBonus = streakBonus
		result.StreakRewardDesc = streakDesc
		result.CurrentStreakDays = newStreakDays
		result.CycleSeq = newCycleSeq
		result.DayInCycle = dayInCycle
		result.StreakRewardRefID = rewardRefID
		result.StreakRewardGranted = rewardGranted
		result.WasBroken = wasBroken

		return nil
	})

	if err != nil {
		switch signInErrorCode(err) {
		case "ALREADY_SIGNED":
			replyText(bot, chatID, "⚠️ 您今天已经打过卡啦，明天再来吧！")
		case "SIGN_DATE_IN_FUTURE":
			log.Printf("⚠️ 签到状态异常，last_sign_date 超前: user=%d today=%s", userID, formatPlainValue(todayKey))
			replyText(bot, chatID, "❌ 签到状态异常，请联系管理员处理。")
		case "CONCURRENT_SIGN_IN_RETRY":
			replyText(bot, chatID, "⚠️ 签到请求过于频繁，请稍后再试。")
		default:
			log.Printf("❌ 签到失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 签到失败，请稍后重试。")
		}
		return
	}

	awardSectContribution(userID, 1, "每日签到", "sign_in", todayKey)
	var u User
	if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		log.Printf("⚠️ 签到成功后本地档案读取失败: user=%d err=%s", userID, formatPlainError(err))
	} else if u.AbsUserID != "" {
		fetchReportAndCheckUpgrade(bot, userID, u.AbsUserID)
		checkAndCompensateLegacyUser(bot, userID)
	}

	cul := GetOrCreateCultivation(userID)
	realmStr := GetRealmName(cul)
	if cul == nil {
		replyText(bot, chatID, "⚠️ 签到成功，但修仙档案暂时读取失败，请稍后查看我的信息。")
		return
	}
	safeName := result.UserName
	if safeName == "" {
		safeName = "神秘道友"
	}

	streakText := fmt.Sprintf(
		"\n🔥 连续签到：`%d` 天\n🔄 当前周期：第 `%d` 轮，第 `%d/30` 天\n",
		result.CurrentStreakDays,
		result.CycleSeq,
		result.DayInCycle,
	)

	if result.WasBroken {
		streakText += "⚠️ 昨日未签到，本轮连签已重新开始。\n"
	}

	if result.StreakBonus > 0 && result.StreakRewardGranted {
		streakText += fmt.Sprintf("🎊 连签奖励：`%d` 积分\n", result.StreakBonus)
	}

	reply := fmt.Sprintf("🎉 **签到成功！**\n\n"+
		"👤 道友：`%s`\n"+
		"📿 境界：%s\n"+
		"⏱ 闭关：`%.1f` 小时\n"+
		"🎁 获得灵石：`%d` 积分%s"+
		"🪪 当前余额：`%d` 积分\n\n"+
		"*(💡 连续签到奖励：3天+1，7天+2，14天随机3~5，21天随机5~7，30天随机8~15；30天一轮，断签重置)*\n"+
		"*(💡 发送 `修仙榜` 查看全服排名)*",
		safeName,
		realmStr,
		cul.TotalAudioTime+cul.PillAudioTime,
		result.BaseBonus,
		streakText,
		result.BalanceAfterAll,
	)

	replyText(bot, chatID, reply)
}

func showPointTransactions(bot *tgbotapi.BotAPI, chatID int64, requesterID int64, targetID int64, days int) {
	role := getUserRole(requesterID)

	if requesterID != targetID {
		if role != "admin" && role != "super_admin" {
			replyText(bot, chatID, "❌ 你只能查询自己的积分流水。")
			return
		}
	}

	maxDays := 1
	limit := 30

	if role == "admin" {
		maxDays = 7
		limit = 100
	} else if role == "super_admin" {
		maxDays = 30
		limit = 100
	}

	if requesterID == targetID && role != "admin" && role != "super_admin" {
		days = 1
	}

	if days <= 0 {
		days = 1
	}
	if days > maxDays {
		replyText(bot, chatID, fmt.Sprintf("❌ 当前权限最多只能查询最近 %d 天积分流水。", maxDays))
		return
	}

	start := time.Now().AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())

	var logs []PointTransaction
	if err := DB.Where("user_id = ? AND created_at >= ?", targetID, start).
		Order("created_at DESC").
		Limit(limit).
		Find(&logs).Error; err != nil {
		replyText(bot, chatID, "❌ 查询积分流水失败，请稍后再试。")
		return
	}

	if len(logs) == 0 {
		if requesterID == targetID {
			replyText(bot, chatID, "📒 你今天还没有积分流水。")
		} else {
			replyText(bot, chatID, "📒 该用户在查询范围内没有积分流水。")
		}
		return
	}

	title := "📒 我的今日积分流水"
	if requesterID != targetID {
		title = fmt.Sprintf("📒 用户 `%d` 最近 %d 天积分流水", targetID, days)
	} else if days > 1 {
		title = fmt.Sprintf("📒 我的最近 %d 天积分流水", days)
	}

	var builder strings.Builder
	builder.WriteString(title)
	builder.WriteString("\n\n")

	for _, item := range logs {
		sign := "+"
		if item.Delta < 0 {
			sign = ""
		}

		builder.WriteString(fmt.Sprintf(
			"%s  %s%d  %s\n余额：%d → %d\n%s\n\n",
			item.CreatedAt.Format("01-02 15:04"),
			sign,
			item.Delta,
			pointTransactionTypeMarkdown(item.Type),
			item.BalanceBefore,
			item.BalanceAfter,
			pointTransactionDescriptionMarkdown(item.Description),
		))
	}

	if len(logs) == limit {
		builder.WriteString(fmt.Sprintf("仅显示最近 %d 条。", limit))
	}

	replyText(bot, chatID, builder.String())
}

func handlePointTransactionQuery(bot *tgbotapi.BotAPI, chatID int64, requesterID int64, text string) {
	text = strings.TrimSpace(text)

	if text == "我的流水" || text == "积分流水" || text == "📒 我的流水" {
		showPointTransactions(bot, chatID, requesterID, requesterID, 1)
		return
	}

	role := getUserRole(requesterID)
	if role != "admin" && role != "super_admin" {
		replyText(bot, chatID, "❌ 权限不足。")
		return
	}

	parts := strings.Fields(text)
	if len(parts) < 2 {
		replyText(bot, chatID, "用法：查流水 用户ID [天数]\n例如：查流水 123456789 7")
		return
	}

	targetID, err := strconv.ParseInt(parts[1], 10, 64)
	if err != nil || targetID <= 0 {
		replyText(bot, chatID, "❌ 用户ID格式错误，请输入纯数字 Telegram ID。")
		return
	}

	days := 1
	if len(parts) >= 3 {
		rawDays := strings.TrimSuffix(parts[2], "天")
		parsedDays, err := strconv.Atoi(rawDays)
		if err != nil || parsedDays <= 0 {
			replyText(bot, chatID, "❌ 天数格式错误，例如：查流水 123456789 7")
			return
		}
		days = parsedDays
	}

	showPointTransactions(bot, chatID, requesterID, targetID, days)
}

func handleAuditLogQuery(bot *tgbotapi.BotAPI, chatID int64, requesterID int64, text string) {
	if getUserRole(requesterID) != "super_admin" {
		replyText(bot, chatID, "❌ 审计日志仅限超级管理员查看。")
		return
	}

	filter := ""
	days := 1
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) >= 2 {
		if parsedDays, ok := parseAuditQueryDays(parts[1]); ok {
			days = parsedDays
		} else {
			filter = strings.TrimSpace(parts[1])
		}
	}
	if len(parts) >= 3 {
		if parsedDays, ok := parseAuditQueryDays(parts[2]); ok {
			days = parsedDays
		} else if filter == "" {
			filter = strings.TrimSpace(parts[2])
		}
	}

	if days <= 0 {
		days = 1
	}
	if days > 30 {
		replyText(bot, chatID, "❌ 审计日志最多查询最近 30 天。")
		return
	}

	start := time.Now().AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())

	query := DB.Where("created_at >= ?", start)
	filterText := "全部"
	if filter != "" {
		if actorID, err := strconv.ParseInt(filter, 10, 64); err == nil {
			query = query.Where("actor_id = ? OR target = ?", actorID, filter)
			filterText = fmt.Sprintf("用户/目标 %d", actorID)
		} else {
			action := strings.ToUpper(filter)
			query = query.Where("action = ?", action)
			filterText = action
		}
	}

	const limit = 20
	var logs []AuditLog
	if err := query.Order("created_at DESC").Limit(limit).Find(&logs).Error; err != nil {
		replyText(bot, chatID, "❌ 查询审计日志失败，请稍后再试。")
		return
	}

	if len(logs) == 0 {
		replyText(bot, chatID, "📋 查询范围内没有审计日志。")
		writeAuditLog(requesterID, "VIEW_AUDIT_LOGS", filterText, fmt.Sprintf("查看审计日志无结果，范围=%d天", days))
		return
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("📋 **审计日志**\n范围：最近 `%d` 天\n过滤：`%s`\n\n", days, escapeMarkdown(filterText)))
	for _, item := range logs {
		deltaText := ""
		if item.Delta != 0 {
			deltaText = fmt.Sprintf(" Δ`%+d`", item.Delta)
		}
		detail := truncateRunes(strings.TrimSpace(formatAuditTextForDisplay(item.Detail)), 120)
		if detail == "" {
			detail = "无详情"
		}
		target := truncateRunes(strings.TrimSpace(formatAuditTextForDisplay(item.Target)), 80)
		builder.WriteString(fmt.Sprintf(
			"%s  `%s`%s\n操作者：`%d` / `%s`\n目标：`%s`\n%s\n\n",
			item.CreatedAt.Format("01-02 15:04"),
			escapeMarkdown(item.Action),
			deltaText,
			item.ActorID,
			escapeMarkdown(item.ActorRole),
			escapeMarkdown(target),
			escapeMarkdown(detail),
		))
	}
	if len(logs) == limit {
		builder.WriteString(fmt.Sprintf("仅显示最近 `%d` 条。", limit))
	}

	replyText(bot, chatID, builder.String())
	writeAuditLog(requesterID, "VIEW_AUDIT_LOGS", filterText, fmt.Sprintf("查看审计日志，范围=%d天，返回=%d条", days, len(logs)))
}

func handleAuditSummaryQuery(bot *tgbotapi.BotAPI, chatID int64, requesterID int64, text string) {
	if getUserRole(requesterID) != "super_admin" {
		replyText(bot, chatID, "❌ 审计概览仅限超级管理员查看。")
		return
	}

	days := 1
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) >= 2 {
		if parsedDays, ok := parseAuditQueryDays(parts[1]); ok {
			days = parsedDays
		}
	}
	if days <= 0 {
		days = 1
	}
	if days > 30 {
		replyText(bot, chatID, "❌ 审计概览最多统计最近 30 天。")
		return
	}

	start := time.Now().AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, start.Location())

	total, failed, highRisk, topActions, topActors, err := loadAuditSummary(start)
	if err != nil {
		replyText(bot, chatID, "❌ 统计审计概览失败，请稍后再试。")
		return
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("📊 **审计概览**\n范围：最近 `%d` 天\n\n", days))
	builder.WriteString(fmt.Sprintf("总记录：`%d`\n失败/异常：`%d`\n高危操作：`%d`\n\n", total, failed, highRisk))
	builder.WriteString("Top 操作类型：\n")
	appendAuditSummaryRows(&builder, topActions, "action")
	builder.WriteString("\nTop 操作者：\n")
	appendAuditSummaryRows(&builder, topActors, "actor")

	replyText(bot, chatID, builder.String())
	writeAuditLog(requesterID, "VIEW_AUDIT_SUMMARY", "audit_logs", fmt.Sprintf("查看审计概览，范围=%d天，总记录=%d，失败=%d，高危=%d", days, total, failed, highRisk))
}

type auditSummaryRow struct {
	Label string
	Count int64
}

func loadAuditSummary(start time.Time) (int64, int64, int64, []auditSummaryRow, []auditSummaryRow, error) {
	var total int64
	if err := DB.Model(&AuditLog{}).Where("created_at >= ?", start).Count(&total).Error; err != nil {
		return 0, 0, 0, nil, nil, err
	}

	var failed int64
	if err := DB.Model(&AuditLog{}).
		Where("created_at >= ? AND (action LIKE ? OR action LIKE ? OR detail LIKE ? OR detail LIKE ?)", start, "%FAILED%", "%FAIL%", "%失败%", "%异常%").
		Count(&failed).Error; err != nil {
		return 0, 0, 0, nil, nil, err
	}

	var highRisk int64
	highRisk, err := countHighRiskAuditLogs(start)
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}

	topActions, err := loadAuditSummaryRows(start, "action")
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}
	topActors, err := loadAuditSummaryRows(start, "actor_id")
	if err != nil {
		return 0, 0, 0, nil, nil, err
	}

	return total, failed, highRisk, topActions, topActors, nil
}

func loadAuditSummaryRows(start time.Time, column string) ([]auditSummaryRow, error) {
	if column != "action" && column != "actor_id" {
		return nil, fmt.Errorf("unsupported audit summary column: %s", column)
	}

	var rows []auditSummaryRow
	if err := DB.Model(&AuditLog{}).
		Select(fmt.Sprintf("%s AS label, COUNT(*) AS count", column)).
		Where("created_at >= ?", start).
		Group(column).
		Order("count DESC").
		Limit(5).
		Scan(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func appendAuditSummaryRows(builder *strings.Builder, rows []auditSummaryRow, labelKind string) {
	if len(rows) == 0 {
		builder.WriteString("无记录\n")
		return
	}
	for _, row := range rows {
		label := strings.TrimSpace(formatAuditTextForDisplay(row.Label))
		if label == "" {
			label = "unknown"
		}
		if labelKind == "actor" && label == "0" {
			label = "0(system)"
		}
		builder.WriteString(fmt.Sprintf("- `%s`：`%d`\n", escapeMarkdown(label), row.Count))
	}
}

func countHighRiskAuditLogs(start time.Time) (int64, error) {
	var rows []auditSummaryRow
	if err := DB.Model(&AuditLog{}).
		Select("action AS label, COUNT(*) AS count").
		Where("created_at >= ?", start).
		Group("action").
		Scan(&rows).Error; err != nil {
		return 0, err
	}

	var total int64
	for _, row := range rows {
		if isHighRiskAuditAction(row.Label) {
			total += row.Count
		}
	}
	return total, nil
}

var highRiskAuditActionSet = map[string]struct{}{
	"ADJUST_POINTS":                          {},
	"PROMOTE_ADMIN":                          {},
	"SET_WHITELIST":                          {},
	"SET_SERVER_LINES":                       {},
	"SET_INVITE_PRICE":                       {},
	"SET_RENEW_PRICE":                        {},
	"GENERATE_INVITE_CODES":                  {},
	"GENERATE_RENEW_CODES":                   {},
	"RESERVE_INVITE_CODE":                    {},
	"RELEASE_INVITE_CODE":                    {},
	"USE_INVITE_CODE":                        {},
	"USE_RENEW_CODE":                         {},
	"REFERRAL_TRIAL_REGISTER":                {},
	"TRIAL_CONVERT_FORMAL":                   {},
	"REFERRAL_TRIAL_TASK_CLAIM":              {},
	"BIND_USER":                              {},
	"REBIND_USER":                            {},
	"UNBIND_USER":                            {},
	"MANUAL_BACKUP":                          {},
	"MANUAL_BACKUP_FAILED":                   {},
	"SIMULATE_EXPIRE":                        {},
	"SUSPEND_USER":                           {},
	"UNSUSPEND_USER":                         {},
	"AUTO_SUSPEND_EXPIRED_USER":              {},
	"AUTO_SUSPEND_EXPIRED_USER_LOCAL_FAILED": {},
	"LISTENING_ABUSE_FREEZE":                 {},
	"LISTENING_ABUSE_FREEZE_FAILED":          {},
	"LISTENING_ABUSE_RELEASE":                {},
	"LISTENING_ABUSE_RELEASE_FAILED":         {},
	"LISTENING_ABUSE_RELEASE_BLOCKED":        {},
	"LISTENING_ABUSE_AMNESTY":                {},
	"LISTENING_ABUSE_AMNESTY_FAILED":         {},
	"RENEW_REACTIVATE_USER":                  {},
	"RENEW_REACTIVATE_USER_FAILED":           {},
	"RENEW_REACTIVATE_USER_LOCAL_FAILED":     {},
	"SECT_SHOP_RENEW":                        {},
	"SECT_SHOP_RENEW_REACTIVATE":             {},
	"SELF_DELETE_USER":                       {},
	"AUTO_DELETE_EXPIRED_USER":               {},
	"FORCE_DELETE_USER":                      {},
	"CLEAN_WIDOWS":                           {},
	"CREATE_LOTTERY":                         {},
	"FORCE_DRAW_LOTTERY":                     {},
	"CANCEL_LOTTERY":                         {},
	"CREATE_SECT_LOTTERY":                    {},
	"DRAW_SECT_LOTTERY":                      {},
	"CANCEL_SECT_LOTTERY":                    {},
	"RELOAD_CULTIVATION_RULES":               {},
	"UPDATE_BREAKTHROUGH_CONFIG":             {},
	"UPDATE_REALM_THRESHOLD":                 {},
	"UPDATE_MINOR_REALM_THRESHOLD":           {},
	"UPDATE_SECT_SECRET_REALM_CONFIG":        {},
	"CLAIM_GITHUB_BENEFIT_INVITE":            {},
	"CLAIM_GITHUB_BENEFIT_RENEW":             {},
	"SET_GITHUB_BENEFIT_ENABLED":             {},
	"SET_GITHUB_BENEFIT_QUOTA":               {},
}

func highRiskAuditActions() []string {
	actions := make([]string, 0, len(highRiskAuditActionSet))
	for action := range highRiskAuditActionSet {
		actions = append(actions, action)
	}
	return actions
}

func isHighRiskAuditAction(action string) bool {
	_, ok := highRiskAuditActionSet[normalizeHighRiskAuditAction(action)]
	return ok
}

func normalizeHighRiskAuditAction(action string) string {
	action = strings.ToUpper(strings.TrimSpace(action))
	for {
		switch {
		case strings.HasSuffix(action, "_LOCAL_FAILED"):
			action = strings.TrimSuffix(action, "_LOCAL_FAILED")
		case strings.HasSuffix(action, "_FAILED"):
			action = strings.TrimSuffix(action, "_FAILED")
		default:
			return action
		}
	}
}

func parseAuditQueryDays(raw string) (int, bool) {
	raw = strings.TrimSpace(raw)
	raw = strings.TrimSpace(strings.TrimSuffix(raw, "天"))
	if raw == "" {
		return 0, false
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days <= 0 {
		return 0, false
	}
	return days, true
}

func auditDayRange(t time.Time) (time.Time, time.Time) {
	loc := time.FixedZone("CST", 8*3600)
	local := t.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1)
}

func getTodayAuditDeltaTotal(actorID int64, action string) (int, error) {
	startOfDay, endOfDay := auditDayRange(time.Now())

	var total int
	if err := DB.Model(&AuditLog{}).
		Where("actor_id = ? AND action = ? AND created_at >= ? AND created_at < ?", actorID, action, startOfDay, endOfDay).
		Select("COALESCE(SUM(ABS(delta)), 0)").
		Scan(&total).Error; err != nil {
		return 0, err
	}

	return total, nil
}

const (
	bookRequestStatusPending   = "pending"
	bookRequestStatusClaimed   = "claimed"
	bookRequestStatusNeedInfo  = "need_info"
	bookRequestStatusUploaded  = "uploaded"
	bookRequestStatusCompleted = "completed" // 兼容历史旧状态
	bookRequestStatusRejected  = "rejected"
	bookRequestStatusCancelled = "cancelled"

	bookRequestDailyLimit   = 3
	bookRequestPendingLimit = 5
	bookRequestNoteMaxLen   = 300
	bookRequestLinkMaxLen   = 500

	bookRequestLinkRequirementText = "必须以 https:// 开头，仅支持 ximalaya.com / www.ximalaya.com / m.ximalaya.com / xima.tv，路径不能为首页，且不能包含空格、换行、制表符、URL 账号密码信息或其他控制/分隔字符"
	bookRequestNoteInvalidText     = "内容不符合要求，请输入最多 300 字、可换行且不含制表符或其他控制字符的说明。"
)

func registrationExpireAtForExistingUser(existingExpireAt *time.Time, defaultExpireAt *time.Time) (*time.Time, bool) {
	if defaultExpireAt == nil {
		return nil, existingExpireAt != nil
	}
	if existingExpireAt == nil || existingExpireAt.Before(*defaultExpireAt) {
		return defaultExpireAt, true
	}
	return nil, false
}

func createRegisteredUserInTx(tx *gorm.DB, user *User) error {
	if tx == nil || user == nil {
		return fmt.Errorf("REGISTERED_USER_INVALID")
	}
	entry := *user
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("REGISTERED_USER_CREATE_MISSED")
	}
	*user = entry
	return nil
}

func hasActiveAbsAccount(userID int64) bool {
	var u User
	if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		return false
	}

	ok, err := userHasUsableLocalAbsAccountAt(u, time.Now())
	if err != nil {
		log.Printf("⚠️ 有效 ABS 账号状态读取失败: user=%d abs=%s err=%s", userID, formatPlainValue(u.AbsUserID), formatPlainError(err))
		return false
	}
	return ok
}

const botMessageAutoDeleteDelay = 10 * time.Minute

func createAutoDeleteMessageRecord(chatID int64, messageID int, deleteAt time.Time) error {
	if DB == nil || chatID == 0 || messageID == 0 {
		return nil
	}

	res := DB.Create(&AutoDeleteMsg{
		ChatID:    chatID,
		MessageID: messageID,
		DeleteAt:  deleteAt,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("AUTO_DELETE_MSG_CREATE_MISSED")
	}
	return nil
}

func registerAutoDeleteMessage(chatID int64, messageID int) {
	if chatID == 0 || messageID == 0 {
		return
	}

	if err := createAutoDeleteMessageRecord(chatID, messageID, time.Now().Add(botMessageAutoDeleteDelay)); err != nil {
		log.Printf("⚠️ 登记自动删除消息失败: chat=%d message=%d err=%s", chatID, messageID, formatPlainError(err))
	}
}

func registerIncomingGroupCommandForAutoDelete(msg *tgbotapi.Message) {
	if msg == nil || msg.Chat == nil {
		return
	}

	// 私聊消息不删除
	if msg.Chat.IsPrivate() {
		return
	}

	registerAutoDeleteMessage(msg.Chat.ID, msg.MessageID)
}

func sendAutoDelete(bot *tgbotapi.BotAPI, chattable tgbotapi.Chattable) (tgbotapi.Message, error) {
	sentMsg, err := bot.Send(chattable)
	if err == nil && sentMsg.Chat.ID < 0 {
		registerAutoDeleteMessage(sentMsg.Chat.ID, sentMsg.MessageID)
	}
	return sentMsg, err
}

func sendNoAutoDelete(bot *tgbotapi.BotAPI, chattable tgbotapi.Chattable) (tgbotapi.Message, error) {
	return bot.Send(chattable)
}

func enqueueAutoDelete(bot *tgbotapi.BotAPI, chattable tgbotapi.Chattable, kind string, priority telegramAsyncPriority, dedupeKey string) bool {
	return enqueueTelegramAsync(telegramAsyncJob{
		Kind:      kind,
		DedupeKey: dedupeKey,
		Priority:  priority,
		Send: func() error {
			_, err := sendAutoDelete(bot, chattable)
			return err
		},
	})
}

func enqueueNoAutoDelete(bot *tgbotapi.BotAPI, chattable tgbotapi.Chattable, kind string, priority telegramAsyncPriority, dedupeKey string) bool {
	return enqueueTelegramAsync(telegramAsyncJob{
		Kind:      kind,
		DedupeKey: dedupeKey,
		Priority:  priority,
		Send: func() error {
			_, err := sendNoAutoDelete(bot, chattable)
			return err
		},
	})
}

func sendPlainText(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("发送 Telegram 文本消息失败: %s", formatTelegramSendError(err))
	}
}

func isTelegramMessageNotModifiedError(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "message is not modified")
}

func isTerminalTelegramDeleteError(err error) bool {
	if err == nil {
		return false
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "message to delete not found") ||
		strings.Contains(errText, "message can't be deleted") ||
		strings.Contains(errText, "not enough rights") ||
		strings.Contains(errText, "not enough rights to delete messages")
}

func isTerminalTelegramUnpinError(err error) bool {
	if err == nil {
		return false
	}
	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "message to unpin not found") ||
		strings.Contains(errText, "message not found") ||
		strings.Contains(errText, "message can't be unpinned") ||
		strings.Contains(errText, "message is not pinned") ||
		strings.Contains(errText, "not enough rights") ||
		strings.Contains(errText, "not enough rights to manage pinned messages")
}

func getTelegramDisplayName(user *tgbotapi.User) string {
	if user == nil {
		return "未知用户"
	}
	if user.UserName != "" {
		return "@" + user.UserName
	}
	if strings.TrimSpace(user.FirstName) != "" {
		return strings.TrimSpace(user.FirstName)
	}
	return fmt.Sprintf("%d", user.ID)
}

func validateXmlyLink(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false
	}
	for _, r := range raw {
		if unicode.IsControl(r) || r == '\u2028' || r == '\u2029' {
			return "", false
		}
	}

	if len(raw) < 10 || len(raw) > bookRequestLinkMaxLen {
		return "", false
	}

	if strings.ContainsAny(raw, " \r\n\t") {
		return "", false
	}

	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}

	if u.Scheme != "https" {
		return "", false
	}
	if u.User != nil {
		return "", false
	}

	host := strings.ToLower(u.Hostname())
	allowedHosts := map[string]bool{
		"ximalaya.com":     true,
		"www.ximalaya.com": true,
		"m.ximalaya.com":   true,
		"xima.tv":          true,
	}

	if !allowedHosts[host] {
		return "", false
	}

	if u.Path == "" || u.Path == "/" {
		return "", false
	}

	return raw, true
}

func containsDisallowedControl(text string, allowNewline bool) bool {
	for _, r := range text {
		if r == '\n' && allowNewline {
			continue
		}
		if r < 0x20 || r == 0x7f || r == '\u2028' || r == '\u2029' {
			return true
		}
	}
	return false
}

func validateBookRequestNote(raw string) (string, bool) {
	note := strings.TrimSpace(raw)
	if len([]rune(note)) > bookRequestNoteMaxLen {
		return "", false
	}
	if containsDisallowedControl(note, true) {
		return "", false
	}
	return note, true
}

func markBookRequestUserReplied(db *gorm.DB, req BookRequest, actorName string, replyNote string, now time.Time) (bool, error) {
	if db == nil {
		return false, fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}
	newNote := strings.TrimSpace(req.UserNote + "\n\u8865\u5145\uff1a" + replyNote)
	updated := false
	err := db.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&BookRequest{}).
			Where("id = ? AND user_id = ? AND status = ?", req.ID, req.UserID, bookRequestStatusNeedInfo).
			Updates(map[string]interface{}{
				"status":         bookRequestStatusClaimed,
				"user_note":      newNote,
				"last_action_at": &now,
			})
		if res.Error != nil {
			return fmt.Errorf("book request user reply update failed: %s", formatPlainError(res.Error))
		}
		if res.RowsAffected == 0 {
			return nil
		}
		updated = true
		return createBookRequestLogInTx(tx, req.ID, req.UserID, actorName, "user_reply", req.Status, bookRequestStatusClaimed, replyNote)
	})
	return updated, err
}

func reloadBookRequestAfterClaim(db *gorm.DB, req *BookRequest, reqID uint, adminID int64, adminName string, now time.Time) error {
	if req != nil {
		req.Status = bookRequestStatusClaimed
		req.AssigneeID = adminID
		req.AssigneeName = adminName
		req.AdminID = adminID
		req.AdminName = adminName
		req.ClaimedAt = &now
		req.LastActionAt = &now
	}
	if db == nil {
		return fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}
	return db.Where("id = ?", reqID).First(req).Error
}

func reloadBookRequestAfterFinish(db *gorm.DB, req *BookRequest, reqID uint, status string, adminID int64, adminName string, now time.Time) error {
	if req != nil {
		req.Status = status
		req.AdminID = adminID
		req.AdminName = adminName
		req.LastActionAt = &now
		req.CompletedAt = &now
	}
	if db == nil {
		return fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}
	return db.Where("id = ?", reqID).First(req).Error
}

func reloadBookRequestAfterNeedInfo(db *gorm.DB, req *BookRequest, reqID uint, note string, adminID int64, adminName string, now time.Time) error {
	if req != nil {
		req.Status = bookRequestStatusNeedInfo
		req.AdminNote = note
		req.AdminID = adminID
		req.AdminName = adminName
		req.LastActionAt = &now
	}
	if db == nil {
		return fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}
	return db.Where("id = ?", reqID).First(req).Error
}

func reloadBookRequestAfterAdminNote(db *gorm.DB, req *BookRequest, reqID uint, note string, adminID int64, adminName string, now time.Time) error {
	if req != nil {
		req.AdminNote = note
		req.AdminID = adminID
		req.AdminName = adminName
		req.LastActionAt = &now
	}
	if db == nil {
		return fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}
	return db.Where("id = ?", reqID).First(req).Error
}

func recordBookRequestAdminMessageID(db *gorm.DB, req *BookRequest, chatID int64, messageID int) error {
	if db == nil || req == nil || req.ID == 0 || chatID == 0 || messageID == 0 {
		return fmt.Errorf("BOOK_REQUEST_ADMIN_MESSAGE_ID_INVALID")
	}
	res := db.Model(&BookRequest{}).
		Where("id = ?", req.ID).
		Updates(map[string]interface{}{
			"admin_chat_id":    chatID,
			"admin_message_id": messageID,
		})
	if res.Error != nil {
		return fmt.Errorf("book request admin message id update failed: %s", formatPlainError(res.Error))
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("book request admin message id update missed: req=%d", req.ID)
	}
	req.AdminChatID = chatID
	req.AdminMessageID = messageID
	return nil
}

func createBookRequestLog(requestID uint, actorID int64, actorName string, action string, oldStatus string, newStatus string, note string) {
	if DB == nil || requestID == 0 {
		return
	}

	res := DB.Create(&BookRequestLog{
		RequestID: requestID,
		ActorID:   actorID,
		ActorName: actorName,
		Action:    action,
		OldStatus: oldStatus,
		NewStatus: newStatus,
		Note:      note,
	})
	if res.Error != nil {
		err := res.Error
		log.Printf("⚠️ 写入求书工单日志失败: req=%d actor=%d action=%s err=%s", requestID, actorID, formatPlainValue(action), formatPlainError(err))
	}
	if res.Error == nil && res.RowsAffected == 0 {
		err := fmt.Errorf("BOOK_REQUEST_LOG_CREATE_MISSED")
		log.Printf("⚠️ 写入求书工单日志未命中: req=%d actor=%d action=%s err=%s", requestID, actorID, formatPlainValue(action), formatPlainError(err))
	}
}

func createBookRequestLogInTx(tx *gorm.DB, requestID uint, actorID int64, actorName string, action string, oldStatus string, newStatus string, note string) error {
	if tx == nil {
		return fmt.Errorf("BOOK_REQUEST_LOG_DB_EMPTY")
	}
	if requestID == 0 {
		return fmt.Errorf("BOOK_REQUEST_LOG_REQUEST_EMPTY")
	}

	res := tx.Create(&BookRequestLog{
		RequestID: requestID,
		ActorID:   actorID,
		ActorName: actorName,
		Action:    action,
		OldStatus: oldStatus,
		NewStatus: newStatus,
		Note:      note,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("BOOK_REQUEST_LOG_CREATE_MISSED")
	}
	return nil
}

func isMenuLikeBookRequestReply(text string) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return true
	}

	menuWords := []string{
		"/start", "/admin",
		"注册", "绑定", "签到", "兑换", "邀请码", "续期卡",
		"求书", "我的求书", "待处理求书", "我的处理工单",
		"我的信息", "听书报告", "取消", "返回",
	}

	for _, word := range menuWords {
		if strings.Contains(text, word) {
			return true
		}
	}

	return false
}

func canOperateBookRequest(req BookRequest, actorID int64) bool {
	if isSuperAdmin(actorID) {
		return true
	}

	return req.AssigneeID != 0 && req.AssigneeID == actorID
}

func bookRequestStatusText(status string) string {
	switch status {
	case bookRequestStatusPending:
		return "待接单"
	case bookRequestStatusClaimed:
		return "处理中"
	case bookRequestStatusNeedInfo:
		return "需补充信息"
	case bookRequestStatusUploaded, bookRequestStatusCompleted:
		return "已上传"
	case bookRequestStatusRejected:
		return "暂无资源"
	case bookRequestStatusCancelled:
		return "已取消"
	default:
		return "未知"
	}
}

func formatBookRequestTime(t *time.Time) string {
	if t == nil {
		return "未处理"
	}
	return t.Format("2006-01-02 15:04")
}

func displayBookRequestText(v string, fallback string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return fallback
	}
	return v
}

func bookRequestDayRange(t time.Time) (time.Time, time.Time) {
	loc := time.FixedZone("CST", 8*3600)
	local := t.In(loc)
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	return start, start.AddDate(0, 0, 1)
}

func bookRequestLimitMessageFromCounts(todayCount int64, pendingCount int64) string {
	if todayCount >= bookRequestDailyLimit {
		return fmt.Sprintf("你今天已经提交了 %d 条求书，请明天再试。", bookRequestDailyLimit)
	}

	if pendingCount >= bookRequestPendingLimit {
		return fmt.Sprintf("你当前已有 %d 条待处理求书，请等待管理员处理后再提交新的求书。", bookRequestPendingLimit)
	}

	return ""
}

func queryBookRequestLimitCounts(db *gorm.DB, userID int64, now time.Time) (int64, int64, error) {
	if db == nil || userID == 0 {
		return 0, 0, fmt.Errorf("BOOK_REQUEST_LIMIT_DB_EMPTY")
	}

	startOfDay, endOfDay := bookRequestDayRange(now)
	var todayCount int64
	if err := db.Model(&BookRequest{}).
		Where("user_id = ? AND created_at >= ? AND created_at < ?", userID, startOfDay, endOfDay).
		Count(&todayCount).Error; err != nil {
		return 0, 0, err
	}

	var pendingCount int64
	if err := db.Model(&BookRequest{}).
		Where("user_id = ? AND status = ?", userID, bookRequestStatusPending).
		Count(&pendingCount).Error; err != nil {
		return 0, 0, err
	}

	return todayCount, pendingCount, nil
}

func checkBookRequestLimitsWithDB(db *gorm.DB, userID int64, now time.Time) (string, error) {
	todayCount, pendingCount, err := queryBookRequestLimitCounts(db, userID, now)
	if err != nil {
		return "", err
	}
	return bookRequestLimitMessageFromCounts(todayCount, pendingCount), nil
}

func checkBookRequestLimits(userID int64) string {
	limitMsg, err := checkBookRequestLimitsWithDB(DB, userID, time.Now())
	if err != nil {
		log.Printf("⚠️ 求书限额检查失败: user=%d err=%s", userID, formatPlainError(err))
		return "求书限额检查失败，请稍后再试。"
	}
	return limitMsg
}

func createBookRequestWithinLimits(req *BookRequest, now time.Time) (string, error) {
	if req == nil {
		return "", fmt.Errorf("BOOK_REQUEST_EMPTY")
	}
	if DB == nil {
		return "", fmt.Errorf("BOOK_REQUEST_DB_EMPTY")
	}

	var limitMsg string
	err := DB.Transaction(func(tx *gorm.DB) error {
		msg, err := checkBookRequestLimitsWithDB(tx, req.UserID, now)
		if err != nil {
			return err
		}
		if msg != "" {
			limitMsg = msg
			return nil
		}
		res := tx.Create(req)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("BOOK_REQUEST_CREATE_MISSED")
		}
		if err := createBookRequestLogInTx(tx, req.ID, req.UserID, req.UserName, "create", "", bookRequestStatusPending, "user created book request"); err != nil {
			return err
		}
		return writeAuditLogInTx(tx, req.UserID, "CREATE_BOOK_REQUEST", fmt.Sprintf("%d", req.ID), 0, "user created book request")
	})
	return limitMsg, err
}

func formatBookRequestAdminText(req BookRequest) string {
	lastActionText := "暂无"
	if req.LastActionAt != nil {
		lastActionText = req.LastActionAt.Format("2006-01-02 15:04")
	} else if !req.UpdatedAt.IsZero() {
		lastActionText = req.UpdatedAt.Format("2006-01-02 15:04")
	}

	assigneeText := "未接单"
	if strings.TrimSpace(req.AssigneeName) != "" {
		assigneeText = req.AssigneeName
	}

	text := fmt.Sprintf(
		"📚 求书工单 #%d\n\n"+
			"状态：%s\n"+
			"接单人：%s\n"+
			"最近更新：%s\n\n"+
			"用户：%s\n"+
			"用户ID：%d\n\n"+
			"喜马拉雅链接：\n%s\n\n"+
			"用户备注：\n%s\n\n"+
			"管理员备注：\n%s",
		req.ID,
		bookRequestStatusText(req.Status),
		displayBookRequestText(assigneeText, "未接单"),
		lastActionText,
		displayBookRequestText(req.UserName, "未知用户"),
		req.UserID,
		req.XmlyLink,
		displayBookRequestText(req.UserNote, "无"),
		displayBookRequestText(req.AdminNote, "无"),
	)

	if req.Status == bookRequestStatusUploaded ||
		req.Status == bookRequestStatusCompleted ||
		req.Status == bookRequestStatusRejected ||
		req.Status == bookRequestStatusCancelled {
		text += fmt.Sprintf(
			"\n\n处理人：%s\n处理时间：%s",
			displayBookRequestText(req.AdminName, "未知管理员"),
			formatBookRequestTime(req.CompletedAt),
		)
	}

	return text
}

func editBookRequestAdminMessage(bot *tgbotapi.BotAPI, chatID int64, messageID int, req BookRequest, removeButtons bool) {
	if bot == nil || chatID == 0 || messageID == 0 {
		return
	}
	edit := tgbotapi.NewEditMessageText(chatID, messageID, formatBookRequestAdminText(req))
	edit.DisableWebPagePreview = true
	if removeButtons {
		emptyMarkup := tgbotapi.NewInlineKeyboardMarkup()
		edit.ReplyMarkup = &emptyMarkup
	} else {
		var keyboard tgbotapi.InlineKeyboardMarkup

		if req.Status == bookRequestStatusPending {
			keyboard = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("🤝 接单", fmt.Sprintf("br_claim_%d", req.ID)),
				),
			)
		} else if req.Status == bookRequestStatusClaimed || req.Status == bookRequestStatusNeedInfo {
			keyboard = tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("✅ 已上传", fmt.Sprintf("br_done_%d", req.ID)),
					tgbotapi.NewInlineKeyboardButtonData("❌ 暂无资源", fmt.Sprintf("br_reject_%d", req.ID)),
				),
				tgbotapi.NewInlineKeyboardRow(
					tgbotapi.NewInlineKeyboardButtonData("📝 管理员备注", fmt.Sprintf("br_note_%d", req.ID)),
					tgbotapi.NewInlineKeyboardButtonData("❓ 需补充信息", fmt.Sprintf("br_need_info_%d", req.ID)),
				),
			)
		}

		if len(keyboard.InlineKeyboard) > 0 {
			edit.ReplyMarkup = &keyboard
		}
	}

	if _, err := bot.Request(edit); err != nil {
		log.Printf("⚠️ 刷新求书工单管理员消息失败: req=%d chat=%d msg=%d err=%s", req.ID, chatID, messageID, formatTelegramSendError(err))
	}
}

func refreshStoredBookRequestAdminMessage(bot *tgbotapi.BotAPI, req BookRequest, removeButtons bool, skipChatID int64, skipMessageID int) {
	if req.AdminChatID == 0 || req.AdminMessageID == 0 {
		return
	}

	if req.AdminChatID == skipChatID && req.AdminMessageID == skipMessageID {
		return
	}

	editBookRequestAdminMessage(bot, req.AdminChatID, req.AdminMessageID, req, removeButtons)
}

func formatBookRequestUserResultText(req BookRequest) string {
	if req.Status == bookRequestStatusUploaded || req.Status == bookRequestStatusCompleted {
		return fmt.Sprintf(
			"✅ 你提交的求书已处理完成。\n\n"+
				"喜马拉雅链接：\n%s\n\n"+
				"管理员备注：\n%s\n\n"+
				"请前往 ABS 搜索查看。",
			req.XmlyLink,
			displayBookRequestText(req.AdminNote, "无"),
		)
	}

	if req.Status == bookRequestStatusNeedInfo {
		return fmt.Sprintf(
			"❓ 你的求书 #%d 需要补充信息：\n\n%s\n\n请直接回复补充内容。",
			req.ID,
			displayBookRequestText(req.AdminNote, "请补充更详细的信息"),
		)
	}

	if req.Status == bookRequestStatusRejected {
		return fmt.Sprintf(
			"📚 你提交的求书暂时无法处理。\n\n"+
				"喜马拉雅链接：\n%s\n\n"+
				"管理员备注：\n%s",
			req.XmlyLink,
			displayBookRequestText(req.AdminNote, "无"),
		)
	}

	return fmt.Sprintf(
		"📚 你的求书状态已更新。\n\n"+
			"喜马拉雅链接：\n%s\n\n"+
			"当前状态：%s\n\n"+
			"管理员备注：\n%s",
		req.XmlyLink,
		bookRequestStatusText(req.Status),
		displayBookRequestText(req.AdminNote, "无"),
	)
}

func notifyBookRequestAdmins(bot *tgbotapi.BotAPI, req BookRequest) {
	adminIDs := make(map[int64]bool)

	for id := range AppConfig.AdminIDs {
		adminIDs[id] = true
	}

	var dbAdmins []User
	if err := DB.Where("role IN ?", []string{"admin", "super_admin"}).Find(&dbAdmins).Error; err != nil {
		log.Printf("⚠️ 求书工单通知管理员列表读取失败: req=%d err=%s", req.ID, formatPlainError(err))
	} else {
		for _, admin := range dbAdmins {
			adminIDs[admin.TelegramID] = true
		}
	}

	if len(adminIDs) == 0 {
		log.Printf("⚠️ 新求书工单 #%d 创建成功，但没有找到可通知的管理员", req.ID)
		return
	}

	adminText := formatBookRequestAdminText(req)

	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🤝 接单", fmt.Sprintf("br_claim_%d", req.ID)),
		),
	)

	for adminID := range adminIDs {
		msg := tgbotapi.NewMessage(adminID, adminText)
		msg.DisableWebPagePreview = true
		msg.ReplyMarkup = keyboard

		sentMsg, err := sendAutoDelete(bot, msg)
		if err != nil {
			log.Printf("⚠️ 求书工单通知管理员失败: req=%d admin=%d err=%s", req.ID, adminID, formatTelegramSendError(err))
			continue
		}

		// 保存第一条成功发送的管理员工单消息
		if req.AdminChatID == 0 && req.AdminMessageID == 0 {
			if err := recordBookRequestAdminMessageID(DB, &req, sentMsg.Chat.ID, sentMsg.MessageID); err != nil {
				log.Printf("⚠️ 求书工单管理员消息ID记录失败: req=%d chat=%d msg=%d err=%s", req.ID, sentMsg.Chat.ID, sentMsg.MessageID, formatPlainError(err))
			}
		}
	}
}

func handleBookRequestStart(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, session *SessionState) {
	if msg == nil || msg.From == nil {
		return
	}

	if !hasActiveAbsAccount(msg.From.ID) {
		sendPlainText(bot, msg.Chat.ID,
			"📚 求书功能仅限当前有效的 ABS 用户使用。\n\n"+
				"请先注册 / 绑定 ABS 账号，或完成续期后再提交求书。",
		)
		return
	}

	limitMsg := checkBookRequestLimits(msg.From.ID)
	if limitMsg != "" {
		sendPlainText(bot, msg.Chat.ID, "⚠️ "+limitMsg)
		return
	}

	session.SetStep("WAITING_BOOK_LINK")
	UserSessions.Store(msg.From.ID, session)

	sendPlainText(bot, msg.Chat.ID,
		"📚 求书提交\n\n"+
			"请发送喜马拉雅链接。\n\n"+
			"要求："+bookRequestLinkRequirementText+"。\n\n"+
			"发送“取消”可退出。",
	)
}

func showMyBookRequests(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	if !hasActiveAbsAccount(userID) {
		sendPlainText(bot, chatID,
			"📋 我的求书功能仅限当前有效的 ABS 用户使用。\n\n"+
				"请先注册 / 绑定 ABS 账号，或完成续期后再查看求书记录。",
		)
		return
	}

	var reqs []BookRequest
	if err := DB.Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(5).
		Find(&reqs).Error; err != nil {
		sendPlainText(bot, chatID, "❌ 查询求书记录失败，请稍后再试。")
		return
	}

	if len(reqs) == 0 {
		sendPlainText(bot, chatID, "📋 你还没有提交过求书。")
		return
	}
	var b strings.Builder
	b.WriteString("📋 我的求书记录\n")
	b.WriteString("以下显示最近 5 条：\n\n")

	for _, req := range reqs {
		lastActionText := req.UpdatedAt.Format("2006-01-02 15:04")
		if req.LastActionAt != nil {
			lastActionText = req.LastActionAt.Format("2006-01-02 15:04")
		}

		assigneeText := "未接单"
		if strings.TrimSpace(req.AssigneeName) != "" {
			assigneeText = req.AssigneeName
		}

		b.WriteString(fmt.Sprintf("#%d  %s\n", req.ID, bookRequestStatusText(req.Status)))
		b.WriteString(fmt.Sprintf("接单人：%s\n", assigneeText))
		b.WriteString(fmt.Sprintf("最近更新：%s\n", lastActionText))
		b.WriteString("喜马拉雅链接：\n")
		b.WriteString(req.XmlyLink)
		b.WriteString("\n")
		b.WriteString("用户备注：\n")
		b.WriteString(displayBookRequestText(req.UserNote, "无"))
		b.WriteString("\n")
		b.WriteString("管理员备注：\n")
		b.WriteString(displayBookRequestText(req.AdminNote, "无"))
		b.WriteString("\n\n")
	}

	sendPlainText(bot, chatID, b.String())
}

func showPendingBookRequests(bot *tgbotapi.BotAPI, chatID int64) {
	var reqs []BookRequest

	if err := DB.
		Where("status = ?", bookRequestStatusPending).
		Order("created_at ASC").
		Limit(20).
		Find(&reqs).Error; err != nil {
		sendPlainText(bot, chatID, "❌ 查询待处理求书失败。")
		return
	}

	if len(reqs) == 0 {
		sendPlainText(bot, chatID, "📚 当前没有待处理求书工单。")
		return
	}

	var builder strings.Builder
	builder.WriteString("📚 待接单求书工单\n\n")

	rows := make([][]tgbotapi.InlineKeyboardButton, 0)

	for _, req := range reqs {
		builder.WriteString(fmt.Sprintf(
			"#%d\n用户：%s\n提交时间：%s\n\n",
			req.ID,
			displayBookRequestText(req.UserName, "未知用户"),
			req.CreatedAt.Format("2006-01-02 15:04"),
		))

		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("查看 / 处理 #%d", req.ID),
				fmt.Sprintf("br_view_%d", req.ID),
			),
		))
	}

	builder.WriteString(fmt.Sprintf("\n共 %d 条待处理工单。", len(reqs)))

	msg := tgbotapi.NewMessage(chatID, builder.String())
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)

	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("⚠️ 发送待处理求书列表失败: err=%s", formatTelegramSendError(err))
	}
}

func showMyClaimedBookRequests(bot *tgbotapi.BotAPI, chatID int64, adminID int64) {
	var reqs []BookRequest

	if err := DB.
		Where("assignee_id = ? AND status IN ?", adminID, []string{
			bookRequestStatusClaimed,
			bookRequestStatusNeedInfo,
		}).
		Order("last_action_at DESC").
		Limit(20).
		Find(&reqs).Error; err != nil {
		sendPlainText(bot, chatID, "❌ 查询我的处理工单失败。")
		return
	}

	if len(reqs) == 0 {
		sendPlainText(bot, chatID, "📚 你当前没有正在处理的求书工单。")
		return
	}

	var builder strings.Builder
	builder.WriteString("📚 我的处理工单\n\n")

	rows := make([][]tgbotapi.InlineKeyboardButton, 0)

	for _, req := range reqs {
		lastActionText := req.UpdatedAt.Format("2006-01-02 15:04")
		if req.LastActionAt != nil {
			lastActionText = req.LastActionAt.Format("2006-01-02 15:04")
		}

		builder.WriteString(fmt.Sprintf(
			"#%d  %s\n用户：%s\n最近更新：%s\n\n",
			req.ID,
			bookRequestStatusText(req.Status),
			displayBookRequestText(req.UserName, "未知用户"),
			lastActionText,
		))

		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(
				fmt.Sprintf("查看 / 处理 #%d", req.ID),
				fmt.Sprintf("br_view_%d", req.ID),
			),
		))
	}

	msg := tgbotapi.NewMessage(chatID, builder.String())
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(rows...)

	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("⚠️ 发送我的处理工单失败: admin=%d err=%s", adminID, formatTelegramSendError(err))
	}
}

func sendBookRequestDetail(bot *tgbotapi.BotAPI, chatID int64, req BookRequest) {
	msg := tgbotapi.NewMessage(chatID, formatBookRequestAdminText(req))
	msg.DisableWebPagePreview = true
	if req.Status == bookRequestStatusPending {
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🤝 接单", fmt.Sprintf("br_claim_%d", req.ID)),
			),
		)
	} else if req.Status == bookRequestStatusClaimed || req.Status == bookRequestStatusNeedInfo {
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("✅ 已上传", fmt.Sprintf("br_done_%d", req.ID)),
				tgbotapi.NewInlineKeyboardButtonData("❌ 暂无资源", fmt.Sprintf("br_reject_%d", req.ID)),
			),
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("📝 管理员备注", fmt.Sprintf("br_note_%d", req.ID)),
				tgbotapi.NewInlineKeyboardButtonData("❓ 需补充信息", fmt.Sprintf("br_need_info_%d", req.ID)),
			),
		)
	}

	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("⚠️ 发送求书详情失败: req=%d err=%s", req.ID, formatTelegramSendError(err))
	}
}

func submitBookRequest(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, session *SessionState) {
	if msg == nil || msg.From == nil {
		return
	}

	if !hasActiveAbsAccount(msg.From.ID) {
		sendPlainText(bot, msg.Chat.ID,
			"📚 求书提交失败。\n\n"+
				"该功能仅限当前有效的 ABS 用户使用，请先注册 / 绑定 ABS 账号，或完成续期后再提交。",
		)
		clearSession(msg.From.ID)
		return
	}

	limitMsg := checkBookRequestLimits(msg.From.ID)
	if limitMsg != "" {
		sendPlainText(bot, msg.Chat.ID, "⚠️ "+limitMsg)
		clearSession(msg.From.ID)
		return
	}

	xmlyLink := session.GetTemp("book_xmly_link")
	userNote := session.GetTemp("book_user_note")
	userName := getTelegramDisplayName(msg.From)

	now := time.Now()
	req := BookRequest{
		UserID:       msg.From.ID,
		UserName:     userName,
		XmlyLink:     xmlyLink,
		UserNote:     userNote,
		Status:       bookRequestStatusPending,
		LastActionAt: &now,
	}

	limitMsg, err := createBookRequestWithinLimits(&req, now)
	if limitMsg != "" {
		sendPlainText(bot, msg.Chat.ID, "⚠️ "+limitMsg)
		clearSession(msg.From.ID)
		return
	}
	if err != nil {
		log.Printf("❌ 创建求书工单失败: user=%d err=%s", msg.From.ID, formatPlainError(err))
		sendPlainText(bot, msg.Chat.ID, "❌ 提交失败，请稍后再试。")
		clearSession(msg.From.ID)
		return
	}

	notifyBookRequestAdmins(bot, req)

	sendPlainText(bot, msg.Chat.ID,
		fmt.Sprintf(
			"✅ 求书已提交，工单编号：#%d\n\n管理员处理后，你会收到通知。",
			req.ID,
		),
	)

	clearSession(msg.From.ID)
}

func isBookRequestAdmin(userID int64) bool {
	role := getUserRole(userID)
	return role == "super_admin" || role == "admin"
}

func parseBookRequestCallbackID(data string, prefix string) (uint, bool) {
	if !strings.HasPrefix(data, prefix) {
		return 0, false
	}

	rawID := strings.TrimPrefix(data, prefix)
	id64, err := strconv.ParseUint(rawID, 10, 64)
	if err != nil || id64 == 0 {
		return 0, false
	}
	if id64 > uint64(^uint(0)) {
		return 0, false
	}

	return uint(id64), true
}

func loadBookRequestByID(db *gorm.DB, reqID uint, context string) (BookRequest, bool, error) {
	var req BookRequest
	if err := db.Where("id = ?", reqID).First(&req).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return req, false, nil
		}
		log.Printf("⚠️ 求书工单读取失败: context=%s req=%d err=%s", formatPlainValue(context), reqID, formatPlainError(err))
		return req, false, err
	}
	return req, true, nil
}

const callbackAlertTextMaxRunes = 200

func formatCallbackAlertText(text string) string {
	formatted := formatDiagnosticTextForDisplay(text)
	if formatted == "" {
		return "操作已处理"
	}
	runes := []rune(formatted)
	if len(runes) <= callbackAlertTextMaxRunes {
		return formatted
	}
	if callbackAlertTextMaxRunes <= 3 {
		return string(runes[:callbackAlertTextMaxRunes])
	}
	return string(runes[:callbackAlertTextMaxRunes-3]) + "..."
}

func answerCallback(bot *tgbotapi.BotAPI, callbackID string, text string) {
	markCallbackAnswered(callbackID)
	cb := tgbotapi.NewCallback(callbackID, formatCallbackAlertText(text))
	if _, err := bot.Request(cb); err != nil && !isOldTelegramCallbackError(err) {
		log.Printf("⚠️ 回答 callback 失败: err=%s", formatTelegramSendError(err))
	}
}

func startDelayedCallbackAck(bot *tgbotapi.BotAPI, callbackID string) {
	if bot == nil || strings.TrimSpace(callbackID) == "" {
		return
	}

	stateValue, _ := callbackAckStates.LoadOrStore(callbackID, &atomic.Bool{})
	state, ok := stateValue.(*atomic.Bool)
	if !ok {
		callbackAckStates.Delete(callbackID)
		return
	}

	go func() {
		time.Sleep(callbackFastAckDelay)
		defer callbackAckStates.Delete(callbackID)
		if !state.CompareAndSwap(false, true) {
			return
		}
		recordCallbackFastAck()
		cb := tgbotapi.NewCallback(callbackID, formatCallbackAlertText("操作处理中"))
		if _, err := bot.Request(cb); err != nil && !isOldTelegramCallbackError(err) {
			log.Printf("⚠️ 快速确认 callback 失败: err=%s", formatTelegramSendError(err))
		}
	}()
}

func markCallbackAnswered(callbackID string) {
	if strings.TrimSpace(callbackID) == "" {
		return
	}
	stateValue, ok := callbackAckStates.Load(callbackID)
	if !ok {
		return
	}
	if state, ok := stateValue.(*atomic.Bool); ok {
		state.Store(true)
	}
}

func isOldTelegramCallbackError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "query is too old") ||
		strings.Contains(text, "response timeout expired") ||
		strings.Contains(text, "query id is invalid")
}

func handleBookRequestCallback(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery) {
	if cb == nil || cb.From == nil {
		return
	}

	data := cb.Data
	if !strings.HasPrefix(data, "br_") {
		answerCallback(bot, cb.ID, "未知操作")
		return
	}

	if !isBookRequestAdmin(cb.From.ID) {
		answerCallback(bot, cb.ID, "无权操作该求书工单")
		return
	}

	if reqID, ok := parseBookRequestCallbackID(data, "br_view_"); ok {
		req, found, err := loadBookRequestByID(DB, reqID, "callback view")
		if err != nil {
			answerCallback(bot, cb.ID, "查询工单失败，请稍后重试")
			return
		}
		if !found {
			answerCallback(bot, cb.ID, "工单不存在")
			return
		}

		if cb.Message != nil {
			sendBookRequestDetail(bot, cb.Message.Chat.ID, req)
		}

		answerCallback(bot, cb.ID, "已打开工单详情")
		return
	}

	if reqID, ok := parseBookRequestCallbackID(data, "br_claim_"); ok {
		req, found, err := loadBookRequestByID(DB, reqID, "callback claim")
		if err != nil {
			answerCallback(bot, cb.ID, "查询工单失败，请稍后重试")
			return
		}
		if !found {
			answerCallback(bot, cb.ID, "工单不存在")
			return
		}

		if req.Status != bookRequestStatusPending {
			if cb.Message != nil {
				editBookRequestAdminMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, req, true)
			}
			answerCallback(bot, cb.ID, "该工单已被接单或已处理")
			return
		}

		now := time.Now()
		adminName := getTelegramDisplayName(cb.From)

		claimed := false
		err = DB.Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&BookRequest{}).
				Where("id = ? AND status = ?", reqID, bookRequestStatusPending).
				Updates(map[string]interface{}{
					"status":         bookRequestStatusClaimed,
					"assignee_id":    cb.From.ID,
					"assignee_name":  adminName,
					"admin_id":       cb.From.ID,
					"admin_name":     adminName,
					"claimed_at":     &now,
					"last_action_at": &now,
				})
			if res.Error != nil {
				return fmt.Errorf("book request claim update failed: %s", formatPlainError(res.Error))
			}
			if res.RowsAffected == 0 {
				return nil
			}
			claimed = true
			if err := createBookRequestLogInTx(tx, reqID, cb.From.ID, adminName, "claim", bookRequestStatusPending, bookRequestStatusClaimed, "admin claimed book request"); err != nil {
				return err
			}
			return writeAuditLogInTx(tx, cb.From.ID, "CLAIM_BOOK_REQUEST", fmt.Sprintf("%d", reqID), 0, "admin claimed book request")
		})
		if err != nil {
			log.Printf("book request claim failed: req=%d admin=%d err=%s", reqID, cb.From.ID, formatPlainError(err))
			answerCallback(bot, cb.ID, "\u63a5\u5355\u5931\u8d25")
			return
		}

		if !claimed {
			answerCallback(bot, cb.ID, "\u8be5\u5de5\u5355\u521a\u521a\u5df2\u88ab\u522b\u4eba\u63a5\u5355")
			return
		}

		if err := reloadBookRequestAfterClaim(DB, &req, reqID, cb.From.ID, adminName, now); err != nil {
			log.Printf("book request claim reload failed: req=%d admin=%d err=%s", reqID, cb.From.ID, formatPlainError(err))
		}

		if cb.Message != nil {
			if req.AdminChatID == 0 || req.AdminMessageID == 0 {
				if err := recordBookRequestAdminMessageID(DB, &req, cb.Message.Chat.ID, cb.Message.MessageID); err != nil {
					log.Printf("⚠️ 求书工单管理员消息ID记录失败: req=%d chat=%d msg=%d err=%s", req.ID, cb.Message.Chat.ID, cb.Message.MessageID, formatPlainError(err))
				}
			}

			editBookRequestAdminMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, req, false)
		}

		sendPlainText(bot, req.UserID, fmt.Sprintf(
			"📚 你的求书 #%d 已由管理员接单。\n\n当前状态：处理中",
			req.ID,
		))

		answerCallback(bot, cb.ID, "已接单")
		return
	}

	if reqID, ok := parseBookRequestCallbackID(data, "br_need_info_"); ok {
		req, found, err := loadBookRequestByID(DB, reqID, "callback need info")
		if err != nil {
			answerCallback(bot, cb.ID, "查询工单失败，请稍后重试")
			return
		}
		if !found {
			answerCallback(bot, cb.ID, "工单不存在")
			return
		}

		if req.Status != bookRequestStatusClaimed && req.Status != bookRequestStatusNeedInfo {
			answerCallback(bot, cb.ID, "该工单当前不能要求补充信息")
			return
		}

		if !canOperateBookRequest(req, cb.From.ID) {
			answerCallback(bot, cb.ID, "只有接单人或超级管理员可以操作")
			return
		}

		session := getSession(cb.From.ID)
		session.SetStep("WAITING_BOOK_NEED_INFO_NOTE")
		session.SetTemp("book_need_info_req_id", fmt.Sprintf("%d", reqID))

		if cb.Message != nil {
			session.SetTemp("book_need_info_chat_id", fmt.Sprintf("%d", cb.Message.Chat.ID))
			session.SetTemp("book_need_info_message_id", fmt.Sprintf("%d", cb.Message.MessageID))
		}

		UserSessions.Store(cb.From.ID, session)

		answerCallback(bot, cb.ID, "请发送需要用户补充的内容")
		sendPlainText(bot, cb.From.ID, fmt.Sprintf("❓ 请发送求书工单 #%d 需要用户补充的信息。\n\n例如：请说明缺少哪几集 / 想要哪个主播版本。\n发送“取消”可退出。", reqID))
		return
	}

	if reqID, ok := parseBookRequestCallbackID(data, "br_note_"); ok {
		req, found, err := loadBookRequestByID(DB, reqID, "callback note")
		if err != nil {
			answerCallback(bot, cb.ID, "查询工单失败，请稍后重试")
			return
		}
		if !found {
			answerCallback(bot, cb.ID, "工单不存在")
			return
		}
		if req.Status != bookRequestStatusClaimed && req.Status != bookRequestStatusNeedInfo {
			if cb.Message != nil {
				if req.AdminChatID == 0 || req.AdminMessageID == 0 {
					if err := recordBookRequestAdminMessageID(DB, &req, cb.Message.Chat.ID, cb.Message.MessageID); err != nil {
						log.Printf("⚠️ 求书工单管理员消息ID记录失败: req=%d chat=%d msg=%d err=%s", req.ID, cb.Message.Chat.ID, cb.Message.MessageID, formatPlainError(err))
					}
				}

				editBookRequestAdminMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, req, true)
			}

			answerCallback(bot, cb.ID, "该工单已处理，已刷新状态")
			return
		}
		if !canOperateBookRequest(req, cb.From.ID) {
			answerCallback(bot, cb.ID, "只有接单人或超级管理员可以备注")
			return
		}
		session := getSession(cb.From.ID)
		session.SetStep("WAITING_BOOK_ADMIN_NOTE")
		session.SetTemp("book_admin_note_req_id", fmt.Sprintf("%d", reqID))

		if cb.Message != nil {
			session.SetTemp("book_admin_note_chat_id", fmt.Sprintf("%d", cb.Message.Chat.ID))
			session.SetTemp("book_admin_note_message_id", fmt.Sprintf("%d", cb.Message.MessageID))

			if req.AdminChatID == 0 || req.AdminMessageID == 0 {
				if err := recordBookRequestAdminMessageID(DB, &req, cb.Message.Chat.ID, cb.Message.MessageID); err != nil {
					log.Printf("⚠️ 求书工单管理员消息ID记录失败: req=%d chat=%d msg=%d err=%s", req.ID, cb.Message.Chat.ID, cb.Message.MessageID, formatPlainError(err))
				}
			}
		}

		UserSessions.Store(cb.From.ID, session)

		answerCallback(bot, cb.ID, "请发送管理员备注")
		sendPlainText(bot, cb.From.ID, fmt.Sprintf("📝 请发送求书工单 #%d 的管理员备注。\n\n最多 %d 字。\n发送“取消”可退出。", reqID, bookRequestNoteMaxLen))
		return
	}

	status := ""

	if reqID, ok := parseBookRequestCallbackID(data, "br_done_"); ok {
		status = bookRequestStatusUploaded
		data = fmt.Sprintf("%d", reqID)
	} else if reqID, ok := parseBookRequestCallbackID(data, "br_reject_"); ok {
		status = bookRequestStatusRejected
		data = fmt.Sprintf("%d", reqID)
	} else {
		answerCallback(bot, cb.ID, "未知操作")
		return
	}

	reqID64, parseErr := strconv.ParseUint(data, 10, 64)
	if parseErr != nil || reqID64 == 0 {
		answerCallback(bot, cb.ID, "工单编号异常")
		return
	}
	reqID := uint(reqID64)

	req, found, err := loadBookRequestByID(DB, reqID, "callback finish")
	if err != nil {
		answerCallback(bot, cb.ID, "查询工单失败，请稍后重试")
		return
	}
	if !found {
		answerCallback(bot, cb.ID, "工单不存在")
		return
	}

	if req.Status != bookRequestStatusClaimed && req.Status != bookRequestStatusNeedInfo {
		if cb.Message != nil {
			if req.AdminChatID == 0 || req.AdminMessageID == 0 {
				if err := recordBookRequestAdminMessageID(DB, &req, cb.Message.Chat.ID, cb.Message.MessageID); err != nil {
					log.Printf("⚠️ 求书工单管理员消息ID记录失败: req=%d chat=%d msg=%d err=%s", req.ID, cb.Message.Chat.ID, cb.Message.MessageID, formatPlainError(err))
				}
			}

			editBookRequestAdminMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, req, true)
		}

		answerCallback(bot, cb.ID, "该工单已处理，已刷新状态")
		return
	}
	if !canOperateBookRequest(req, cb.From.ID) {
		answerCallback(bot, cb.ID, "只有接单人或超级管理员可以处理")
		return
	}
	oldStatus := req.Status
	now := time.Now()
	adminName := getTelegramDisplayName(cb.From)

	finished := false
	err = DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&BookRequest{}).
			Where("id = ? AND status IN ?", reqID, []string{bookRequestStatusClaimed, bookRequestStatusNeedInfo}).
			Updates(map[string]interface{}{
				"status":         status,
				"admin_id":       cb.From.ID,
				"admin_name":     adminName,
				"last_action_at": &now,
				"completed_at":   &now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		finished = true
		note := fmt.Sprintf("admin finished book request; status=%s", status)
		if err := createBookRequestLogInTx(tx, reqID, cb.From.ID, adminName, "finish", oldStatus, status, note); err != nil {
			return err
		}
		return writeAuditLogInTx(tx, cb.From.ID, "HANDLE_BOOK_REQUEST", fmt.Sprintf("%d", reqID), 0, note)
	})
	if err != nil {
		log.Printf("book request finish failed: req=%d err=%s", reqID, formatPlainError(err))
		answerCallback(bot, cb.ID, "\u5904\u7406\u5931\u8d25")
		return
	}

	if !finished {
		answerCallback(bot, cb.ID, "\u8be5\u5de5\u5355\u5df2\u5904\u7406")
		return
	}

	if err := reloadBookRequestAfterFinish(DB, &req, reqID, status, cb.From.ID, adminName, now); err != nil {
		log.Printf("book request finish reload failed: req=%d err=%s", reqID, formatPlainError(err))
	}

	sendPlainText(bot, req.UserID, formatBookRequestUserResultText(req))

	currentChatID := int64(0)
	currentMessageID := 0

	if cb.Message != nil {
		currentChatID = cb.Message.Chat.ID
		currentMessageID = cb.Message.MessageID

		if req.AdminChatID == 0 || req.AdminMessageID == 0 {
			if err := recordBookRequestAdminMessageID(DB, &req, cb.Message.Chat.ID, cb.Message.MessageID); err != nil {
				log.Printf("⚠️ 求书工单管理员消息ID记录失败: req=%d chat=%d msg=%d err=%s", req.ID, cb.Message.Chat.ID, cb.Message.MessageID, formatPlainError(err))
			}
		}

		editBookRequestAdminMessage(bot, cb.Message.Chat.ID, cb.Message.MessageID, req, true)
	}

	refreshStoredBookRequestAdminMessage(bot, req, true, currentChatID, currentMessageID)

	answerCallback(bot, cb.ID, "已处理")
}

func getTodayAuditDeltaTotalTx(tx *gorm.DB, actorID int64, action string) (int, error) {
	startOfDay, endOfDay := auditDayRange(time.Now())

	var total int
	if err := tx.Model(&AuditLog{}).
		Where("actor_id = ? AND action = ? AND created_at >= ? AND created_at < ?", actorID, action, startOfDay, endOfDay).
		Select("COALESCE(SUM(ABS(delta)), 0)").
		Scan(&total).Error; err != nil {
		return 0, err
	}

	return total, nil
}

func validateAdminReason(text string) (string, bool) {
	reason := strings.TrimSpace(text)
	if len([]rune(reason)) < 5 {
		return "", false
	}
	for _, r := range reason {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	if containsDisallowedControl(reason, false) {
		return "", false
	}
	if len([]rune(reason)) > 200 {
		reasonRunes := []rune(reason)
		reason = string(reasonRunes[:200])
	}
	return reason, true
}

const (
	adminReasonRequirementText = "至少 5 个字，且不能包含换行、制表符或其他控制/分隔字符"
	adminReasonInvalidText     = "原因不符合要求，请输入" + adminReasonRequirementText + "。"

	serverLinesMaxLen          = 4000
	serverLinesRequirementText = "1-4000 字，可换行，不能包含制表符或其他控制/分隔字符"
	serverLinesInvalidText     = "线路配置内容不符合要求，请输入" + serverLinesRequirementText + "。"

	securityCodeAttemptPurpose = "security_code"
	securityCodeMaxFailures    = 5
	securityCodeLockDuration   = 10 * time.Minute
)

type securityAttemptFailureState struct {
	FailCount   int
	LockedUntil *time.Time
	Message     string
}

func securityAttemptRemainingMinutes(lockedUntil time.Time, now time.Time) int {
	remaining := int(math.Ceil(lockedUntil.Sub(now).Minutes()))
	if remaining < 1 {
		return 1
	}
	return remaining
}

func nextSecurityAttemptFailureState(currentFailCount int, currentLockedUntil *time.Time, maxFailures int, lockDuration time.Duration, now time.Time, remainingFormat string, lockMessage string) (securityAttemptFailureState, error) {
	if maxFailures <= 0 || lockDuration <= 0 || remainingFormat == "" || lockMessage == "" {
		return securityAttemptFailureState{}, fmt.Errorf("SECURITY_ATTEMPT_INVALID")
	}
	if currentFailCount < 0 {
		currentFailCount = 0
	}

	if currentLockedUntil != nil && currentLockedUntil.After(now) {
		return securityAttemptFailureState{
			FailCount:   maxFailures,
			LockedUntil: currentLockedUntil,
			Message:     lockMessage,
		}, nil
	}

	failCount := currentFailCount + 1
	if currentLockedUntil != nil {
		failCount = 1
	}

	if failCount >= maxFailures {
		lockedUntil := now.Add(lockDuration)
		return securityAttemptFailureState{
			FailCount:   maxFailures,
			LockedUntil: &lockedUntil,
			Message:     lockMessage,
		}, nil
	}

	return securityAttemptFailureState{
		FailCount: failCount,
		Message:   fmt.Sprintf(remainingFormat, maxFailures-failCount),
	}, nil
}

func validateServerLinesContent(raw string) (string, bool) {
	content := strings.ReplaceAll(raw, "\r\n", "\n")
	content = strings.ReplaceAll(content, "\r", "\n")
	content = strings.TrimSpace(content)
	if content == "" {
		return "", false
	}
	if len([]rune(content)) > serverLinesMaxLen {
		return "", false
	}
	for _, r := range content {
		if r != '\n' && unicode.IsControl(r) {
			return "", false
		}
	}
	if containsDisallowedControl(content, true) {
		return "", false
	}
	return content, true
}

func serverLinesMarkdownBody(raw string) string {
	content, ok := validateServerLinesContent(raw)
	if !ok {
		return "⚠️ 线路配置异常，请联系管理员更新。"
	}
	return escapeMarkdown(content)
}

func recordSecurityAttemptFailureInTx(tx *gorm.DB, userID int64, purpose string, maxFailures int, lockDuration time.Duration, now time.Time, remainingFormat string, lockMessage string) (string, error) {
	if tx == nil || userID == 0 || strings.TrimSpace(purpose) == "" || maxFailures <= 0 || lockDuration <= 0 {
		return "", fmt.Errorf("SECURITY_ATTEMPT_INVALID")
	}

	purpose = strings.TrimSpace(purpose)
	var attempt SecurityAttemptLock
	err := tx.Where("user_id = ? AND purpose = ?", userID, purpose).First(&attempt).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", err
	}

	if err == nil {
		return updateSecurityAttemptFailureInTx(tx, &attempt, maxFailures, lockDuration, now, remainingFormat, lockMessage)
	}

	state, err := nextSecurityAttemptFailureState(0, nil, maxFailures, lockDuration, now, remainingFormat, lockMessage)
	if err != nil {
		return "", err
	}

	create := SecurityAttemptLock{
		UserID:      userID,
		Purpose:     purpose,
		FailCount:   state.FailCount,
		LockedUntil: state.LockedUntil,
		LastFailAt:  &now,
	}
	res := tx.Create(&create)
	if res.Error != nil {
		if isUniqueConstraintError(res.Error) {
			var existing SecurityAttemptLock
			if readErr := tx.Where("user_id = ? AND purpose = ?", userID, purpose).First(&existing).Error; readErr != nil {
				return "", readErr
			}
			return updateSecurityAttemptFailureInTx(tx, &existing, maxFailures, lockDuration, now, remainingFormat, lockMessage)
		}
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "", fmt.Errorf("SECURITY_ATTEMPT_CREATE_MISSED")
	}
	return state.Message, nil
}

func updateSecurityAttemptFailureInTx(tx *gorm.DB, attempt *SecurityAttemptLock, maxFailures int, lockDuration time.Duration, now time.Time, remainingFormat string, lockMessage string) (string, error) {
	if tx == nil || attempt == nil {
		return "", fmt.Errorf("SECURITY_ATTEMPT_INVALID")
	}

	state, err := nextSecurityAttemptFailureState(attempt.FailCount, attempt.LockedUntil, maxFailures, lockDuration, now, remainingFormat, lockMessage)
	if err != nil {
		return "", err
	}

	res := tx.Model(&SecurityAttemptLock{}).
		Where("id = ? AND user_id = ? AND purpose = ?", attempt.ID, attempt.UserID, attempt.Purpose).
		Updates(map[string]interface{}{
			"fail_count":   state.FailCount,
			"locked_until": state.LockedUntil,
			"last_fail_at": &now,
		})
	if res.Error != nil {
		return "", res.Error
	}
	if res.RowsAffected == 0 {
		return "", fmt.Errorf("SECURITY_ATTEMPT_STATE_CHANGED")
	}
	return state.Message, nil
}

func verifyUserSecurityCodeWithCooldown(userID int64, input string, stored string) (bool, string) {
	return verifySensitiveTokenWithPersistentCooldown(userID, securityCodeAttemptPurpose, input, stored)
}

func verifySensitiveTokenWithPersistentCooldown(userID int64, purpose string, input string, stored string) (bool, string) {
	now := time.Now()

	// 数据库尚未初始化时保留极简兜底，避免异常路径 panic。
	if DB == nil {
		if verifySensitiveToken(input, stored) {
			return true, ""
		}
		return false, "❌ 安全码错误。"
	}

	ok := false
	message := ""

	err := DB.Transaction(func(tx *gorm.DB) error {
		var attempt SecurityAttemptLock
		err := tx.Where("user_id = ? AND purpose = ?", userID, purpose).First(&attempt).Error

		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		hasAttempt := err == nil

		if hasAttempt && attempt.LockedUntil != nil && attempt.LockedUntil.After(now) {
			remaining := securityAttemptRemainingMinutes(*attempt.LockedUntil, now)
			message = fmt.Sprintf("⏳ 安全码错误次数过多，请 %d 分钟后再试。", remaining)
			return nil
		}

		if verifySensitiveToken(input, stored) {
			ok = true

			if hasAttempt && (attempt.FailCount > 0 || attempt.LockedUntil != nil || attempt.LastFailAt != nil) {
				res := tx.Model(&SecurityAttemptLock{}).
					Where("id = ? AND user_id = ? AND purpose = ?", attempt.ID, attempt.UserID, attempt.Purpose).
					Updates(map[string]interface{}{
						"fail_count":   0,
						"locked_until": nil,
						"last_fail_at": nil,
					})
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					return nil
				}
				return nil
			}

			return nil
		}

		var recordErr error
		message, recordErr = recordSecurityAttemptFailureInTx(
			tx,
			userID,
			purpose,
			securityCodeMaxFailures,
			securityCodeLockDuration,
			now,
			"❌ 安全码错误。剩余尝试次数：%d",
			"⏳ 安全码错误次数过多，请 10 分钟后再试。",
		)
		return recordErr
	})

	if err != nil {
		log.Printf("⚠️ 安全码失败次数持久化异常: user=%d purpose=%s err=%s", userID, formatPlainValue(purpose), formatPlainError(err))

		// 数据库锁表异常时不放大故障：正确安全码仍允许通过，错误安全码拒绝。
		if verifySensitiveToken(input, stored) {
			return true, ""
		}
		return false, "❌ 安全码校验失败，请稍后重试。"
	}

	return ok, message
}

func escapeMarkdown(text string) string {
	text = strings.ReplaceAll(text, "_", "\\_")
	text = strings.ReplaceAll(text, "*", "\\*")
	text = strings.ReplaceAll(text, "[", "\\[")
	text = strings.ReplaceAll(text, "`", "\\`")
	return text
}

func escapeMarkdownPreservingEscapes(text string) string {
	var b strings.Builder
	prevBackslash := false
	for _, r := range text {
		if (r == '_' || r == '*' || r == '[' || r == '`') && !prevBackslash {
			b.WriteRune('\\')
		}
		b.WriteRune(r)
		prevBackslash = r == '\\'
	}
	return b.String()
}

func telegramUsernameMentionMarkdown(username string) string {
	username = strings.TrimSpace(username)
	username = strings.TrimPrefix(username, "@")
	username = strings.TrimSpace(username)
	if username == "" {
		return ""
	}
	return "@" + escapeMarkdown(username)
}

func inventoryItemMarkdownName(name string) string {
	name = strings.Map(func(r rune) rune {
		if containsDisallowedControl(string(r), false) {
			return ' '
		}
		return r
	}, name)
	name = strings.Join(strings.Fields(name), " ")
	if name == "" {
		name = "-"
	}
	return escapeMarkdown(name)
}

func truncateRunes(text string, max int) string {
	runes := []rune(text)
	if len(runes) <= max {
		return text
	}
	return string(runes[:max]) + "..."
}

func getConfigIntFromDBChecked(db *gorm.DB, key string, defaultVal int) (int, error) {
	if db == nil {
		return defaultVal, nil
	}

	var cfg SystemConfig
	err := db.Where("key = ?", key).First(&cfg).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || (err == nil && strings.TrimSpace(cfg.Value) == "") {
		return defaultVal, nil
	}
	if err != nil {
		return defaultVal, err
	}
	val, err := strconv.Atoi(strings.TrimSpace(cfg.Value))
	if err != nil {
		return defaultVal, fmt.Errorf("invalid integer config %s=%q: %w", key, cfg.Value, err)
	}
	return val, nil
}

func getConfigIntChecked(key string, defaultVal int) (int, error) {
	return getConfigIntFromDBChecked(DB, key, defaultVal)
}

func upsertSystemConfigValue(key string, value string) error {
	return upsertSystemConfigValueInTx(DB, key, value)
}

// 🛡️ 核心重构：后台巡警协程，集成了消息安全清理与内存垃圾回收
func startMessageSweeper(bot *tgbotapi.BotAPI) {
	// 1. Cron 定时清理消息任务
	go func() {
		for {
			time.Sleep(30 * time.Second)

			var msgs []AutoDeleteMsg
			if err := DB.Where("delete_at <= ?", time.Now()).Find(&msgs).Error; err != nil {
				log.Printf("⚠️ 自动删消息队列读取失败: err=%s", formatPlainError(err))
				continue
			}
			for _, m := range msgs {
				_, err := bot.Request(tgbotapi.NewDeleteMessage(m.ChatID, m.MessageID))

				// 仅在成功删除，或确认该消息已被提前手动删除/不可删除时，才清除数据库记录
				if err == nil || isTerminalTelegramDeleteError(err) {
					res := DB.Delete(&m)
					if deleteErr := res.Error; deleteErr != nil {
						log.Printf("⚠️ 自动删消息记录清理失败: id=%d chat=%d message=%d err=%s", m.ID, m.ChatID, m.MessageID, formatPlainError(deleteErr))
					} else if res.RowsAffected == 0 {
						deleteErr := fmt.Errorf("AUTO_DELETE_MSG_DELETE_MISSED")
						log.Printf("⚠️ 自动删消息记录清理未命中: id=%d chat=%d message=%d err=%s", m.ID, m.ChatID, m.MessageID, formatPlainError(deleteErr))
					}
				} else {
					log.Printf("⚠️ 自动删除 Telegram 消息失败: id=%d chat=%d message=%d err=%s", m.ID, m.ChatID, m.MessageID, formatTelegramSendError(err))
				}

				time.Sleep(50 * time.Millisecond)
			}
		}
	}()

	// 2. 内存清道夫任务
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			now := time.Now()

			// 清理过期的群成员鉴权缓存
			groupMemberCache.Range(func(key, value interface{}) bool {
				if entry, ok := value.(*groupMemberCacheEntry); ok {
					if now.After(entry.expireAt) {
						groupMemberCache.Delete(key)
					}
				} else {
					// 兼容旧缓存格式或异常数据
					groupMemberCache.Delete(key)
				}
				return true
			})

			// 清理超过 2 小时处于游离状态的僵尸会话
			UserSessions.Range(func(key, value interface{}) bool {
				if session, ok := value.(*SessionState); ok {
					session.mu.RLock()
					lastActive := session.updatedAt
					session.mu.RUnlock()

					if now.Sub(lastActive) > 2*time.Hour {
						UserSessions.Delete(key)
					}
				}
				return true
			})

			// 清理超过 6 小时未使用且当前无人持有的用户锁，防止 userLocks 无限增长。
			// 不能删除 inUse > 0 的锁，否则同一用户可能被分配到两把锁，破坏状态机串行保证。
			userLocks.Range(func(key, value interface{}) bool {
				entry, ok := value.(*userLockEntry)
				if !ok {
					userLocks.Delete(key)
					return true
				}

				entry.metaMu.Lock()
				idleTooLong := now.Sub(entry.lastUsed) > 6*time.Hour
				inUse := entry.inUse
				entry.metaMu.Unlock()

				if idleTooLong && inUse == 0 {
					userLocks.Delete(key)
				}

				return true
			})
		}
	}()
}

func sendGroupAutoDeleteMessage(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("发送 Telegram 自动删除消息失败: %s", formatTelegramSendError(err))
	}
}

func sendGroupAutoDeleteMessageAsync(bot *tgbotapi.BotAPI, chatID int64, text string, kind string, dedupeKey string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if !enqueueAutoDelete(bot, msg, kind, telegramAsyncPriorityNormal, dedupeKey) {
		log.Printf("⚠️ Telegram 群通知异步入队失败: chat=%d kind=%s", chatID, formatPlainValue(kind))
	}
}

func isTelegramCommandText(text string, command string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}

	head := strings.ToLower(fields[0])
	command = strings.ToLower(command)
	return head == command || strings.HasPrefix(head, command+"@")
}

// ==========================================
// 📿 小阶段自动突破检测与双端通知引擎
// ==========================================
func fetchReportAndCheckUpgrade(bot *tgbotapi.BotAPI, userID int64, absUserID string) string {
	if absUserID == "" {
		return ""
	}

	oldCul := GetOrCreateCultivation(userID)
	oldRealm := GetRealmName(oldCul)

	reportStr := absClient.GetPersonalReport(absUserID)

	newCul := GetOrCreateCultivation(userID)
	if oldCul == nil || newCul == nil {
		log.Printf("⚠️ 境界变化检查修仙档案读取失败: user=%d old_nil=%t new_nil=%t", userID, oldCul == nil, newCul == nil)
		return reportStr
	}
	newRealm := GetRealmName(newCul)

	if oldRealm != newRealm {
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 境界变化公告读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
		}

		displayName := escapeMarkdown(u.Username)
		if displayName == "" {
			displayName = "神秘道友"
		}

		if chat, err := bot.GetChat(tgbotapi.ChatInfoConfig{ChatConfig: tgbotapi.ChatConfig{ChatID: userID}}); err == nil {
			if chat.UserName != "" {
				displayName = telegramUsernameMentionMarkdown(chat.UserName)
			}
		}

		announce := fmt.Sprintf("🎉 **【仙途精进·修为突破】** 🎉\n\n恭喜道友 %s 闭关苦修，厚积薄发！\n\n✨ 成功从 **【%s】** 突破至全新的 **【%s】** 境界！\n\n*(大道漫漫，望道友继续潜心听书，早日登临绝顶！)*", displayName, oldRealm, newRealm)

		if _, err := sendAutoDelete(bot, tgbotapi.NewMessage(userID, announce)); err != nil {
			log.Printf("发送修为突破私聊通知失败: user=%d err=%s", userID, formatTelegramSendError(err))
		}

		if AppConfig.NoticeGroupID != 0 {
			// 如果是大境界跨越，只发突破通报（奖池逻辑已在雷劫里处理）
			if oldCul.MajorRealm != newCul.MajorRealm {
				sendGroupAutoDeleteMessage(bot, AppConfig.NoticeGroupID, announce)
			} else {
				// 🚨 小阶段福利：调用全局安全引擎注入奖池
				smallPts := randomIntRange(5, 10)

				currentPool, isBurst := addPointsToFusionPool(smallPts)

				progressText := ""
				if !isBurst {
					progressText = fmt.Sprintf("\n\n*(💧 此番精进引动天地共鸣，为天道奖池注入了 `%d` 积分，当前进度 `%d/300`)*", smallPts, currentPool)
					sendGroupAutoDeleteMessage(bot, AppConfig.NoticeGroupID, announce+progressText)
				} else {
					progressText = fmt.Sprintf("\n\n*(💧 此番精进成为了压轴造化，为天道奖池注入了最后 `%d` 积分！)*", smallPts)
					sendGroupAutoDeleteMessage(bot, AppConfig.NoticeGroupID, announce+progressText)

					notifyFusionPoolBurst(bot, AppConfig.NoticeGroupID, "众道友接连突破引动天地异象")
				}
			}
		}
	}

	return reportStr
}

// ==========================================
// 📜 上古老玩家历史破境功勋安全退税对账组件
// ==========================================
func checkAndCompensateLegacyUser(bot *tgbotapi.BotAPI, userID int64) {
	cul := GetOrCreateCultivation(userID)
	if cul == nil {
		log.Printf("⚠️ 历史补偿检查修仙档案读取失败: user=%d", userID)
		return
	}
	if cul.MajorRealm < 2 {
		return
	}

	var pointsToAdd int
	var codes []string
	var poolInjectedPts int
	var needNotify bool

	err := DB.Transaction(func(tx *gorm.DB) error {
		var txU User
		if err := tx.Where("telegram_id = ?", userID).First(&txU).Error; err != nil {
			return err
		}

		// 原子抢占补偿资格。
		// 只有 is_compensated = false 的时候才能更新成功。
		// 并发情况下，只有一个请求 RowsAffected == 1。
		res := tx.Model(&User{}).
			Where("telegram_id = ? AND is_compensated = ?", userID, false).
			Update("is_compensated", true)

		if res.Error != nil {
			return res.Error
		}

		if res.RowsAffected == 0 {
			return nil
		}

		calcPoints := 0
		calcCodes := []string{}

		if cul.MajorRealm >= 2 {
			calcPoints += 20
		}
		if cul.MajorRealm >= 3 {
			calcPoints += 40
		}
		if cul.MajorRealm >= 4 {
			calcPoints += 100

			c, err := createLegacyInviteCodeInTx(tx)
			if err != nil {
				return err
			}
			calcCodes = append(calcCodes, c)
		}
		if cul.MajorRealm >= 5 {
			calcPoints += 200

			c, err := createLegacyInviteCodeInTx(tx)
			if err != nil {
				return err
			}
			calcCodes = append(calcCodes, c)
		}

		if calcPoints > 0 {
			if err := applyPointDeltaInTx(
				tx,
				userID,
				calcPoints,
				"legacy_compensation",
				fmt.Sprintf("老玩家历史突破补偿，获得 %d 积分", calcPoints),
				"legacy_compensation",
				fmt.Sprintf("realm:%d:%d", cul.MajorRealm, cul.MinorRealm),
			); err != nil {
				return err
			}
		}

		missedUpgrades := 0
		if cul.MajorRealm > 0 {
			missedUpgrades = (cul.MajorRealm-1)*3 + (cul.MinorRealm - 1)
		}

		calcPoolInjectedPts := 0
		for i := 0; i < missedUpgrades; i++ {
			calcPoolInjectedPts += randomIntRange(5, 10)
		}

		pointsToAdd = calcPoints
		codes = calcCodes
		poolInjectedPts = calcPoolInjectedPts
		needNotify = true

		return nil
	})

	if err != nil {
		log.Printf("⚠️ 老玩家补偿失败: user_id=%d err=%s", userID, formatPlainError(err))
		return
	}

	// 没抢到补偿资格，说明已经补偿过了。
	if !needNotify {
		return
	}

	codeStr := ""
	if len(codes) > 0 {
		codeStr = "\n🎁 **附赠大道拉新机缘**：\n"
		for i, code := range codes {
			codeStr += fmt.Sprintf("🎫 专属裂变邀请码 %d：`%s`\n", i+1, code)
		}
	}

	poolMsg := ""
	if poolInjectedPts > 0 {
		_, isBurst := addPointsToFusionPool(poolInjectedPts)
		poolMsg = fmt.Sprintf("\n\n🌊 **天道补全**：系统已追溯您历次小境界的突破造化，共将 `%d` 积分厚礼代为您注入全服【天道融合大奖池】！", poolInjectedPts)

		if isBurst {
			notifyFusionPoolBurst(bot, AppConfig.NoticeGroupID, "上古大能复苏查账，引动浩瀚天地异象")
		}
	}

	msg := fmt.Sprintf(
		"📜 **【天道密卷·历史破境功勋大对账】** 📜\n\n"+
			"检测到您作为本界资深修士，实力出众，特此跨越时空为您下发退税大补帖：\n\n"+
			"💰 **历史突破仙石退税**：`+%d` 积分\n%s%s\n\n"+
			"📈 灵石资产已注入您的乾坤袋，功勋标记已入册，愿道友仙途永昌！",
		pointsToAdd,
		codeStr,
		poolMsg,
	)

	if _, err := sendAutoDelete(bot, tgbotapi.NewMessage(userID, msg)); err != nil {
		log.Printf("发送红包领取私聊通知失败: user=%d err=%s", userID, formatTelegramSendError(err))
	}
}

func createLegacyInviteCodeInTx(tx *gorm.DB) (string, error) {
	for i := 0; i < 5; i++ {
		code := "YQM-" + generateRandomCode(12)
		if err := createInviteCodeRecord(tx, code); err == nil {
			return code, nil
		}
	}

	return "", fmt.Errorf("生成历史补偿邀请码失败")
}

func handleBreakthroughConfirmation(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, session *SessionState, text string) {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return
	}
	if session == nil {
		session = getSession(msg.From.ID)
	}

	if !msg.Chat.IsPrivate() {
		registerIncomingGroupCommandForAutoDelete(msg)
	}

	mode := session.GetTemp("bt_mode")
	if (mode == "USE_INVENTORY" && text == "确认渡劫") || (mode == "AUTO_BUY" && text == "确认代购并渡劫") {
		// 将控制权移交给底层渡劫处决引擎
		ExecuteBreakthrough(bot, msg, mode)
	} else {
		replyText(bot, msg.Chat.ID, "🛑 您已压制体内翻涌的气血，取消了本次渡劫。")
	}
	clearSession(msg.From.ID)
}

// ==========================================
// 🚀 机器人交互核心枢纽
// ==========================================
func handleInteractiveMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return
	}

	sweeperOnce.Do(func() {
		DB.AutoMigrate(&AutoDeleteMsg{})
		startMessageSweeper(bot)
	})

	userID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	if msg.Chat.IsPrivate() {
		if isTelegramCommandText(text, "/start") {
			clearSession(userID)
			if code, ok := parseReferralStartPayload(text); ok {
				if err := validateReferralCodeForStart(code, userID); err != nil {
					if errors.Is(err, errReferralSelfInvite) {
						replyText(bot, chatID, "❌ 不能使用自己的邀请链接注册新人体验。")
					} else if errors.Is(err, errReferralInvalidCode) || errors.Is(err, errReferralInviterNotEligible) {
						replyText(bot, chatID, "❌ 邀请链接无效或已停用。")
					} else {
						log.Printf("⚠️ 邀请链接预校验失败: user=%d err=%s", userID, formatPlainError(err))
						replyText(bot, chatID, "❌ 邀请链接暂时读取失败，请稍后再试。")
					}
					return
				}
				var u User
				if err := DB.Where("telegram_id = ? AND abs_user_id <> ?", userID, "").First(&u).Error; err == nil {
					replyText(bot, chatID, "⚠️ 您已经拥有听书账号，不能重复领取新人体验。")
					return
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					log.Printf("⚠️ 邀请链接注册读取本地正式账号失败: user=%d err=%s", userID, formatPlainError(err))
					replyText(bot, chatID, "❌ 本地档案读取失败，请稍后再试。")
					return
				}
				session := getSession(userID)
				session.SetTemp("referral_code", code)
				session.SetStep("WAITING_REG_USER")
				replyText(bot, chatID, "🎧 欢迎领取新人体验。\n\n通过邀请链接注册可获得 `7` 天体验权限；体验期内听书满 `10` 小时，可领取 `7` 天体验延期。\n\n第一步：请输入您想要的用户名\n仅限 3-20 位字母、数字或下划线。")
				UserSessions.Store(userID, session)
				return
			}
			sendUserMainMenu(bot, chatID, "👋 欢迎使用【悦耳声阅】用户管理系统：")
			return
		}

		if isTelegramCommandText(text, "/admin") {
			clearSession(userID)
			role := getUserRole(userID)
			if role == "super_admin" {
				sendMenu(bot, chatID, "👑 欢迎进入超级管理员控制台：", SuperAdminMenu)
			} else if role == "admin" {
				sendMenu(bot, chatID, "🛠️ 欢迎进入普通管理员控制台：", NormalAdminMenu)
			} else {
				replyText(bot, chatID, "❌ 拒绝访问：您没有管理员权限。")
			}
			return
		}

		if text == "取消" || strings.Contains(text, "返回") {
			clearSession(userID)
			sendUserMainMenu(bot, chatID, "✅ 已为您切换至主菜单：")
			return
		}
	}

	if locked, lockMessage := getLotteryClaimLockMessage(userID); locked {
		if msg.Chat.IsPrivate() {
			sendPlainText(bot, chatID, lockMessage)
		} else {
			registerIncomingGroupCommandForAutoDelete(msg)
			sendLotteryGroupPlainText(bot, chatID, lockMessage)
		}
		return
	}

	if AppConfig.NoticeGroupID != 0 && !isMessageFromNoticeGroup(msg) && !isUserInGroup(bot, userID, AppConfig.NoticeGroupID) {
		if msg.Chat.IsPrivate() {
			replyText(bot, chatID, "⚠️ **访问受限：您尚未加入官方交流群！**\n\n为了保障社群的健康生态，本机器人系统仅对官方群成员开放。\n👉 您必须先加入我们的官方大群，才能解锁各项功能。")
		}
		return
	}

	if HandleCultivationAdminReadOnlyCommand(bot, msg, text) {
		return
	}

	if HandleCultivationAdminWriteCommand(bot, msg, text) {
		return
	}

	if HandleSectSecretRealmAdminCommand(bot, msg, text) {
		return
	}

	if HandleGithubBenefitCommand(bot, msg, text, nil) {
		return
	}

	if HandleWorldBossCommand(bot, msg, text) {
		return
	}

	if HandleSectSecretRealmCommand(bot, msg, text) {
		return
	}

	session := getSession(userID)

	if HandleSectLotteryCommand(bot, msg, text, session) {
		return
	}

	if HandleSectCommand(bot, msg, text) {
		return
	}

	role := getUserRole(userID)

	if HandleReferralCommand(bot, msg, text) {
		return
	}

	if HandleMarketplaceCommand(bot, msg, text, session) {
		return
	}

	if handleDailyListeningStatCommand(bot, msg, text, role) {
		return
	}

	if HandleLotteryCommand(bot, msg, text, session, role) {
		return
	}

	if !msg.Chat.IsPrivate() && session.GetStep() == "WAITING_CONFIRM_BREAKTHROUGH" {
		handleBreakthroughConfirmation(bot, msg, session, text)
		return
	}

	if session.GetStep() == "IDLE" && msg.Chat.IsPrivate() && text != "" && text != "取消" {
		var needInfoReq BookRequest
		if err := DB.
			Where("user_id = ? AND status = ?", userID, bookRequestStatusNeedInfo).
			Order("last_action_at DESC").
			First(&needInfoReq).Error; err == nil {

			if !isMenuLikeBookRequestReply(text) {
				now := time.Now()
				replyNote, ok := validateBookRequestNote(text)
				if !ok {
					sendPlainText(bot, chatID, bookRequestNoteInvalidText)
					return
				}

				actorName := getTelegramDisplayName(msg.From)
				updated, err := markBookRequestUserReplied(DB, needInfoReq, actorName, replyNote, now)
				if err != nil {
					log.Printf("⚠️ 求书用户补充信息写入失败: req=%d user=%d err=%s", needInfoReq.ID, userID, formatPlainError(err))
					sendPlainText(bot, chatID, "❌ 补充信息提交失败，请稍后再试。")
					return
				}
				if !updated {
					sendPlainText(bot, chatID, "⚠️ 该求书工单状态已变化，请稍后查看最新状态。")
					return
				}

				sendPlainText(bot, chatID, fmt.Sprintf("✅ 已收到你对求书 #%d 的补充信息，已通知接单管理员。", needInfoReq.ID))

				if needInfoReq.AssigneeID != 0 {
					sendPlainText(bot, needInfoReq.AssigneeID, fmt.Sprintf(
						"📚 求书 #%d 用户已补充信息：\n\n%s\n\n工单已回到处理中。",
						needInfoReq.ID,
						replyNote,
					))
				}

				return
			}
		}
	}

	if text == "抢" || text == "沾仙气" {
		registerIncomingGroupCommandForAutoDelete(msg)
		handleGrabRedPacket(bot, msg)
		return
	}

	if text == "发起骰子" || isDiceBetCommand(text) {
		registerIncomingGroupCommandForAutoDelete(msg)
		handleDiceGame(bot, msg)
		return
	}
	if text == "发起赛马" || strings.HasPrefix(text, "押 ") {
		registerIncomingGroupCommandForAutoDelete(msg)
		handleHorseRace(bot, msg)
		return
	}

	// 🌊 核心新增：主动勘测天道大水池进度
	if text == "🌊 天道奖池" || text == "天道奖池" {
		registerIncomingGroupCommandForAutoDelete(msg)

		var poolCfg SystemConfig
		currentPool := 0
		poolAvailable := true
		if err := DB.Where("key = ?", "fusion_pool_points").First(&poolCfg).Error; err == nil {
			if points, parseErr := strconv.Atoi(poolCfg.Value); parseErr == nil {
				currentPool = points
			} else {
				log.Printf("⚠️ 天道奖池配置解析失败: err=%s", formatPlainError(parseErr))
				poolAvailable = false
			}
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			currentPool = 0
		} else {
			log.Printf("⚠️ 天道奖池读取失败: err=%s", formatPlainError(err))
			poolAvailable = false
		}

		progress := float64(currentPool) / 300.0 * 10.0
		bar := ""
		for i := 0; i < 10; i++ {
			if float64(i) < progress {
				bar += "█"
			} else {
				bar += "░"
			}
		}

		progressText := fmt.Sprintf("`[%s]` **%d/300**", bar, currentPool)
		if !poolAvailable {
			progressText = "`读取失败`"
		}
		reply := fmt.Sprintf("🌊 **【天道大奖池·实时勘测】** 🌊\n\n当前天地灵气汇聚进度：\n%s\n\n💡 *当进度达到 300 时，天道将自动降下 30 份全服大红包！*\n👉 赶紧去听书精进，或者呼唤老怪出关吧！", progressText)

		if msg.Chat.IsPrivate() {
			if _, err := sendAutoDelete(bot, tgbotapi.NewMessage(chatID, reply)); err != nil {
				log.Printf("发送天道奖池私聊状态失败: chat=%d err=%s", chatID, formatTelegramSendError(err))
			}
		} else {
			sendGroupAutoDeleteMessage(bot, chatID, reply)
		}
		return
	}

	if text == "突破" {
		registerIncomingGroupCommandForAutoDelete(msg)
		// 🚨 调用全新的天道扫描预检引擎
		HandleBreakthroughRequest(bot, msg)
		return
	}
	if text == "修仙榜" || text == "仙道榜" || text == "修仙排行榜" {
		registerIncomingGroupCommandForAutoDelete(msg)

		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err == nil && u.AbsUserID != "" {
			fetchReportAndCheckUpgrade(bot, userID, u.AbsUserID)
			checkAndCompensateLegacyUser(bot, userID)
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("⚠️ 修仙榜刷新用户档案读取失败: user=%d err=%s", userID, formatPlainError(err))
		}

		handleCultivationRank(bot, msg.Chat.ID)
		return
	}

	if text == userMenuGardenText || text == userMenuGardenMiniAppText || text == "药园" {
		registerIncomingGroupCommandForAutoDelete(msg)
		handleGardenEntry(bot, msg)
		return
	}

	if handleGardenSellCommand(bot, msg, text) {
		return
	}

	if handleMenuEntry(bot, msg, text) {
		return
	}

	if handleAdminMenuEntry(bot, msg, text) {
		return
	}

	if !msg.Chat.IsPrivate() {
		if strings.HasPrefix(text, "/") {
			safeName := escapeMarkdown(msg.From.FirstName)
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("⚠️ @%s 为了保持群内整洁，请**私聊我**执行各项操作指令哦！", safeName))
			registerIncomingGroupCommandForAutoDelete(msg)
		}
		return
	}

	menuKeywords := []string{
		"注册", "绑定", "签到", "兑换", "邀请码", "续期卡",
		"线路", "报告", "我的信息", "安全", "删号", "注销",
		"修改密码", "修改用户名", "红包", "解绑", "修改本地",
		"监控", "操控", "生成", "授权", "白名单", "设置",
		"模拟过期", "备份", "备份状态", "后台状态", "审计概览", "审计日志", "查审计", "价格", "盲盒", "清理遗孀", "财富榜", "积分榜",
		"查询", "封禁", "暂停", "删除用户", "查码", "抽奖", "积分抽奖",
		"突破", "修仙榜", "仙道榜", "修仙排行榜",
		"查看修仙配置", "查看突破配置", "查看境界配置",
		"重载修仙配置", "设置突破成功率", "设置突破消耗", "设置突破冷却", "设置突破最低修为",
		"设置境界门槛", "设置小境界门槛",
		"查看秘境配置", "设置秘境档位", "设置秘境倍率", "设置秘境掉落",
		"发起赛马", "押", "天道奖池", "乾坤袋", "药园", "回收灵草", "求书", "我的求书", "待处理求书", "我的处理工单",
		"交易行", "交易行帮助", "上架商品", "购买商品", "下架商品", "我的交易行", "我的购买", "我的订单", "交易行订单", "查交易订单", "举报订单",
		"创建宗门", "加入宗门", "退出宗门", "我的宗门", "宗门排行", "宗门成员", "捐献宗门",
		"升级宗门", "宗门改名", "确认宗门改名", "修改宗门名称", "确认修改宗门名称", "任命长老", "任命成员", "踢出宗门", "转让宗主", "宗门贡献榜", "宗门周榜",
		"宗门任务", "领取宗门任务奖励", "结算宗门周目标", "宗门商店", "贡献换声望", "宗门七日续期", "确认宗门七日续期", "洞府", "解锁洞府", "闭关", "宗门闭关",
		"创建宗门抽奖", "宗门抽奖", "参加宗门抽奖", "查看宗门抽奖", "重发宗门抽奖", "提醒宗门抽奖", "补发宗门抽奖提醒", "取消宗门抽奖",
		"宗门秘境", "开启宗门秘境", "确认开启宗门秘境", "开启普通宗门秘境", "确认开启普通宗门秘境", "开启高阶宗门秘境", "确认开启高阶宗门秘境", "开启限时宗门秘境", "确认开启限时宗门秘境", "进入宗门秘境", "结算宗门秘境", "宗门秘境排行", "宗门秘境明细",
		"宗门喇叭", "世界喇叭", "确认宗门喇叭", "确认世界喇叭",
		"世界Boss", "Boss状态", "参加Boss", "Boss排行", "宗门科技", "升级科技", "确认升级科技",
		"流水", "我的流水", "查流水",
		"刷新我的今日净修为", "刷新宗门今日净修为", "刷新全服今日净修为", "查看每日净修为",
	}

	isMenuCommand := false
	for _, kw := range menuKeywords {
		if strings.Contains(text, kw) {
			isMenuCommand = true
			break
		}
	}

	if text == "确认注销" {
		isMenuCommand = false
	}

	if isMenuCommand && session.GetStep() == "IDLE" {
		clearSession(userID)
		session = getSession(userID)

		if strings.Contains(text, "药园") {
			handleGardenEntry(bot, msg)
			return
		}

		if text == "📚 待处理求书" {
			if !isBookRequestAdmin(userID) {
				sendPlainText(bot, chatID, "❌ 权限不足。")
				return
			}

			showPendingBookRequests(bot, chatID)
			return
		}

		if text == "📚 我的处理工单" || text == "我的处理工单" {
			if !isBookRequestAdmin(userID) {
				sendPlainText(bot, chatID, "❌ 权限不足。")
				return
			}

			showMyClaimedBookRequests(bot, chatID, userID)
			return
		}

		if text == "📋 我的求书" {
			showMyBookRequests(bot, chatID, userID)
			return
		}

		if text == "📚 求书" {
			handleBookRequestStart(bot, msg, session)
			return
		}

		if text == "我的流水" || text == "积分流水" || text == "📒 我的流水" || strings.HasPrefix(text, "查流水") {
			handlePointTransactionQuery(bot, chatID, userID, text)
			return
		}

		if text == "审计概览" || strings.HasPrefix(text, "查审计概览") {
			handleAuditSummaryQuery(bot, chatID, userID, text)
			return
		}

		if text == "审计日志" || strings.HasPrefix(text, "查审计") {
			handleAuditLogQuery(bot, chatID, userID, text)
			return
		}

		if strings.Contains(text, "获取线路") || (strings.Contains(text, "获取") && strings.Contains(text, "线路")) {
			var cfg SystemConfig
			lines := "⚠️ 管理员暂未配置任何线路，请稍后再试。"
			if err := DB.Where("key = ?", "server_lines").First(&cfg).Error; err == nil && cfg.Value != "" {
				lines = serverLinesMarkdownBody(cfg.Value)
			} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 用户获取线路读取配置失败: user=%d err=%s", userID, formatPlainError(err))
				lines = "⚠️ 线路配置暂时读取失败，请稍后再试。"
			}
			replyText(bot, chatID, "🗺️ **服务器实时线路**\n\n"+lines)
			return
		}

		if strings.Contains(text, "我的信息") {
			var u User
			userErr := DB.Where("telegram_id = ?", userID).First(&u).Error
			if userErr == nil {
				statusText := resolveUserAccountStatusDisplay(u, time.Now(), accountStatusDisplaySelf, true).Text

				if u.AbsUserID != "" {
					fetchReportAndCheckUpgrade(bot, userID, u.AbsUserID)
					checkAndCompensateLegacyUser(bot, userID)
				}
				cul := GetOrCreateCultivation(userID)
				if cul == nil {
					replyText(bot, chatID, "⚠️ 修仙档案暂时读取失败，请稍后重试。")
					return
				}
				realmStr := GetRealmName(cul)
				todayEffectiveHours := 0.0
				todayEffectiveHoursText := "`0.00`"
				if todayStat, ok, err := getTodayDailyListeningStatChecked(userID, time.Now()); err != nil {
					log.Printf("⚠️ 我的信息今日净修为读取失败: user=%d err=%s", userID, formatPlainError(err))
					todayEffectiveHoursText = "`读取失败`"
				} else if ok {
					todayEffectiveHours = todayStat.EffectiveHours + activeSectCaveRetreatBonusHours(userID, time.Now())
					todayEffectiveHoursText = fmt.Sprintf("`%.2f`", todayEffectiveHours)
				}

				// 🩸 核心新增：天道时间时区计算与周/月度丹毒沉淀勘测
				loc := time.FixedZone("CST", 8*3600)
				now := time.Now().In(loc)

				// 精确计算本周一 00:00:00 的时间节点
				offset := int(now.Weekday()) - 1
				if offset == -1 {
					offset = 6 // 适配周日逻辑
				}
				thisMonday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -offset)

				// 精确计算本月 1 号 00:00:00 的时间节点
				thisMonthFirst := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)

				// 高并发查表：读取该道友周期内的嗑药计数
				countJuLing, errJuLing := countManualPillUsage(userID, "聚灵丹", thisMonday)
				countJiuZhuan, errJiuZhuan := countManualPillUsage(userID, "九转造化丹", thisMonday)
				countXianYu, errXianYu := countManualPillUsage(userID, "万年仙玉髓", thisMonthFirst)
				if errJuLing != nil {
					log.Printf("⚠️ 查询聚灵丹丹毒计数失败: user=%d err=%s", userID, formatPlainError(errJuLing))
				}
				if errJiuZhuan != nil {
					log.Printf("⚠️ 查询九转造化丹丹毒计数失败: user=%d err=%s", userID, formatPlainError(errJiuZhuan))
				}
				if errXianYu != nil {
					log.Printf("⚠️ 查询万年仙玉髓丹毒计数失败: user=%d err=%s", userID, formatPlainError(errXianYu))
				}

				// 渲染全新的多维档案面板
				info := fmt.Sprintf("👤 **我的账户档案**\n\n"+
					"🏷️ **当前名称**: `%s`\n"+
					"🆔 **TG 绑定ID**: `%d`\n"+
					"🌍 **当前积分**: `%d`\n"+
					"⏳ **账号状态**: %s\n"+
					"──────────────\n"+
					"📿 **修仙境界**: %s\n"+
					"⏱ **总计修为**: `%.1f` 小时\n"+
					" ├ 🎧 **闭关苦修**: `%.1f` 小时\n"+
					" └ 💊 **丹药药力**: `%.1f` 小时\n"+
					"🌅 **今日净修为**: %s 小时\n"+
					"🪵 **渡劫气运**: `%d` 次失败累积\n"+
					"──────────────\n"+
					"🩸 **【体内丹毒沉淀】** *(每周一零点重置)*\n"+
					"🍵 聚灵丹: `%s` 次\n"+
					"💊 九转造化丹: `%s` 次\n"+
					"🍎 万年仙玉髓: `%s` 次 *(每月限额)*",
					escapeMarkdown(u.Username), userID, u.Points, statusText,
					realmStr, cul.TotalAudioTime+cul.PillAudioTime, cul.TotalAudioTime, cul.PillAudioTime, todayEffectiveHoursText, cul.TribulationFails,
					manualPillUsageCountText(countJuLing, 3, errJuLing == nil),
					manualPillUsageCountText(countJiuZhuan, 2, errJiuZhuan == nil),
					manualPillUsageCountText(countXianYu, 1, errXianYu == nil))

				replyText(bot, chatID, info)
			} else if errors.Is(userErr, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "⚠️ 您还没有任何资产记录。")
			} else {
				log.Printf("⚠️ 我的信息读取本地档案失败: user=%d err=%s", userID, formatPlainError(userErr))
				replyText(bot, chatID, "⚠️ 账户档案暂时读取失败，请稍后重试。")
			}
			return
		}

		if strings.Contains(text, "签到") {
			handleUserSignIn(bot, msg)
			return
		}

		if strings.Contains(text, "财富榜") || strings.Contains(text, "积分榜") {
			var topUsers []User
			var envAdminIDs []int64
			for id := range AppConfig.AdminIDs {
				envAdminIDs = append(envAdminIDs, id)
			}
			query := DB.Where("role != ? AND role != ?", "super_admin", "admin")
			if len(envAdminIDs) > 0 {
				query = query.Where("telegram_id NOT IN ?", envAdminIDs)
			}
			if err := query.Order("points desc").Limit(20).Find(&topUsers).Error; err != nil {
				replyText(bot, chatID, "❌ 获取积分排行榜失败，请稍后重试。")
				return
			}
			if len(topUsers) == 0 {
				replyText(bot, chatID, "🫙 当前全服还没有任何平民玩家拥有积分记录。")
				return
			}
			msgText := "🏆 **全服积分财富榜 Top 20**\n\n"
			for i, u := range topUsers {
				medal := "▪️"
				if i == 0 {
					medal = "🥇"
				} else if i == 1 {
					medal = "🥈"
				} else if i == 2 {
					medal = "🥉"
				}
				safeName := escapeMarkdown(u.Username)
				msgText += fmt.Sprintf("%s 第%d名 **%s** : `%d` 积分\n", medal, i+1, safeName, u.Points)
			}
			msgText += "\n💡 *提示：每天点击【📆 每日签到】或参与群内抢红包可快速积攒财富！*"
			replyText(bot, chatID, msgText)
			return
		}

		if strings.Contains(text, "兑换") {
			u, _, walletErr := ensureUserWallet(msg.From)
			if walletErr != nil {
				log.Printf("❌ 创建幽灵钱包失败: user=%d err=%s", userID, formatPlainError(walletErr))
				replyText(bot, chatID, "❌ 钱包初始化失败，请稍后重试。")
				return
			}
			invPrice, invPriceErr := getConfigIntChecked("invite_price", 300)
			renPrice, renPriceErr := getConfigIntChecked("renew_price", 150)
			if invPriceErr != nil || renPriceErr != nil {
				log.Printf("⚠️ 兑换商城价格配置读取失败: user=%d invite_err=%s renew_err=%s", userID, formatPlainError(invPriceErr), formatPlainError(renPriceErr))
				replyText(bot, chatID, "❌ 价格配置暂时读取失败，请稍后重试。")
				return
			}
			session.SetStep("WAITING_EXCHANGE_CHOICE")
			replyText(bot, chatID, fmt.Sprintf("🪙 **欢迎光临积分福利商城**\n您的可用资产: `%d` 积分\n\n请回复对应的数字进行操作：\n[1] 消耗 **%d** 积分 -> 兑换【邀请码】\n[2] 消耗 **%d** 积分 -> 兑换【30天续期卡】\n\n丹药与奇珍请从一级菜单进入【🏪 聚宝斋】。\n输入 `取消` 退出。", u.Points, invPrice, renPrice))
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "乾坤袋") {
			var items []Inventory
			if err := DB.Where("user_id = ? AND quantity > 0", userID).Find(&items).Error; err != nil {
				log.Printf("⚠️ 乾坤袋读取失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "🎒 乾坤袋暂时读取失败，请稍后重试。")
				return
			}

			if len(items) == 0 {
				replyText(bot, chatID, "🎒 **【我的乾坤袋】**\n\n🫙 里面空空如也...\n👉 请从一级菜单进入【🏪 聚宝斋】选购天地奇珍。")
				return
			}

			msgText := "🎒 **【我的乾坤袋】**\n\n"
			for i, item := range items {
				msgText += fmt.Sprintf("**[%d]** %s - 拥有数量: `%d`\n", i+1, inventoryItemMarkdownName(item.ItemName), item.Quantity)
				if effectLine := pillEffectMarkdownLine(item.ItemName); effectLine != "" {
					msgText += "  " + effectLine + "\n"
				}
				// 将序号与物品名称绑定，存入缓存供下一步读取
				session.SetTemp(fmt.Sprintf("inv_item_%d", i+1), item.ItemName)
			}
			msgText += "\n👉 **请输入你要使用的物品序号 (如 `1`)，或输入 `取消` 退出。**"

			// 🚨🚨🚨 核心修复：加上这一行，把拼接好的背包界面发出来！
			replyText(bot, chatID, msgText)

			session.SetStep("WAITING_INVENTORY_ACTION")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "盲盒") {
			u, _, walletErr := ensureUserWallet(msg.From)
			if walletErr != nil {
				log.Printf("❌ 创建幽灵钱包失败: user=%d err=%s", userID, formatPlainError(walletErr))
				replyText(bot, chatID, "❌ 钱包初始化失败，请稍后重试。")
				return
			}
			if u.Points < blindBoxCost {
				replyText(bot, chatID, fmt.Sprintf("🎁 开启盲盒需要 %d 积分，您当前余额为 %d 积分。\n\n💡 **新人指引**：赶紧去点击面板上的【📆 每日签到】白嫖第一桶金吧！", blindBoxCost, u.Points))
				return
			}

			session.SetStep("WAITING_CONFIRM_BLIND_BOX")
			replyText(bot, chatID, fmt.Sprintf("🎁 **积分盲盒确认**\n\n开启一次将扣除 `%d` 积分。\n当前余额：`%d` 积分\n扣除后余额：`%d` 积分\n\n确认开启请回复：`确认开启盲盒`\n取消请回复：`取消`。", blindBoxCost, u.Points, u.Points-blindBoxCost))
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "报告") {
			var u User
			userErr := DB.Where("telegram_id = ?", userID).First(&u).Error
			if userErr == nil && u.AbsUserID != "" {
				replyText(bot, chatID, "🔍 正在提取您的战绩...")
				reportStr := fetchReportAndCheckUpgrade(bot, userID, u.AbsUserID)
				checkAndCompensateLegacyUser(bot, userID)
				replyText(bot, chatID, reportStr)
			} else if userErr == nil || errors.Is(userErr, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "⚠️ 您只有幽灵钱包，请先注册/绑定真实听书账号。")
			} else {
				log.Printf("⚠️ 听书报告入口读取本地档案失败: user=%d err=%s", userID, formatPlainError(userErr))
				replyText(bot, chatID, "❌ 本地档案读取失败，请稍后再试。")
			}
			return
		}

		if strings.Contains(text, "注册") || (strings.Contains(text, "邀请码") && !strings.Contains(text, "生成") && !strings.Contains(text, "价格")) {
			var u User
			err := DB.Where("telegram_id = ? AND abs_user_id != ?", userID, "").First(&u).Error
			if err == nil {
				if isTrialAccount(u) {
					session.SetStep("WAITING_TRIAL_FORMAL_INVITE")
					replyText(bot, chatID, "🎫 当前为新人体验账号。\n\n请发送正式邀请码完成转正；转正后即可使用普通续期卡。")
					UserSessions.Store(userID, session)
					return
				}
				replyText(bot, chatID, "⚠️ 您已经拥有正式账号了，无需重复注册。")
				return
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 注册入口读取本地正式账号失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 本地档案读取失败，请稍后再试。")
				return
			}
			session.SetStep("WAITING_REG_USER")
			replyText(bot, chatID, "📝 **第一步：请输入您想要的用户名**\n(⚠️ 仅限 3-20 位字母、数字、下划线)")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "绑定") {
			var u User
			err := DB.Where("telegram_id = ? AND abs_user_id != ?", userID, "").First(&u).Error
			if err == nil {
				replyText(bot, chatID, "⚠️ 您当前已经绑定过正式账号了。")
				return
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 绑定入口读取本地正式账号失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 本地档案读取失败，请稍后再试。")
				return
			}
			session.SetStep("WAITING_BIND_USER")
			replyText(bot, chatID, "🔗 **请输入您在有声书服的已有用户名：**")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "安全") {
			var u User
			if err := DB.Where("telegram_id = ?", userID).First(&u).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "⚠️ 您还未绑定账户。")
				return
			} else if err != nil {
				log.Printf("⚠️ 账户安全入口读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
				return
			}
			textOut, markup := renderFeatureMenu("security", userID)
			sendMenuPanel(bot, chatID, textOut, markup)
			return
		}

		if strings.Contains(text, "修改用户名") {
			var u User
			if err := DB.Where("telegram_id = ?", userID).First(&u).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "⚠️ 您还未绑定账户。")
				return
			} else if err != nil {
				log.Printf("⚠️ 修改用户名入口读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
				return
			}
			if strings.TrimSpace(u.AbsUserID) == "" {
				replyText(bot, chatID, "⚠️ 您只有幽灵钱包，请先注册或绑定真实听书账号后再修改用户名。")
				return
			}
			session.SetStep("WAITING_USERNAME_AUTH")
			replyText(bot, chatID, "🔒 **请输入您的安全码(PIN)以验证所有权：**")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "密码") {
			session.SetStep("WAITING_SAFETY_AUTH")
			replyText(bot, chatID, "🔒 **请输入您的安全码(PIN)以验证所有权：**")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "解绑") {
			session.SetStep("WAITING_UNBIND_AUTH")
			replyText(bot, chatID, "🔒 **请输入您的安全码(PIN)确认解绑：**")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "红包") {
			session.SetStep("WAITING_RED_POINTS")
			replyText(bot, chatID, "🧧 **欢迎发起社群积分红包**\n请输入红包的 **总积分金额** (最少10)：")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "续期卡") && !strings.Contains(text, "生成") && !strings.Contains(text, "价格") {
			session.SetStep("WAITING_RENEW_CODE")
			replyText(bot, chatID, "💳 请发送您的**续期卡密**：")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "注销") || strings.Contains(text, "删号") {
			session.SetStep("WAITING_DELETE_AUTH")
			replyText(bot, chatID, "⚠️ **高危操作警告**：此操作将硬删除本地记录及有声书服务端资产！\n\n请先输入您的安全码(PIN)验证身份。")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "监控") || strings.Contains(text, "操控") || strings.Contains(text, "生成") || strings.Contains(text, "模拟过期") || strings.Contains(text, "价格") || strings.Contains(text, "查询") || strings.Contains(text, "封禁") || strings.Contains(text, "暂停") || strings.Contains(text, "删除用户") || strings.Contains(text, "查码") {

			if role != "super_admin" && role != "admin" {
				replyText(bot, chatID, "❌ 权限不足，拒绝越权访问。")
				return
			}

			// 普通管理员只允许监控和查询。
			// 其余所有写操作、高危操作、卡密查看操作均限制为超级管理员。
			if strings.Contains(text, "查询") {
				session.SetStep("WAITING_QUERY_USER")
				replyText(bot, chatID, "🔍 请输入要查询的 **Telegram ID**、**带 @ 的 TG 用户名** 或 **ABS 用户名**：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "监控") {
				replyText(bot, chatID, absClient.GetServerStats())
				writeAuditLog(userID, "VIEW_SERVER_STATS", "ABS", "管理员查看系统监控")
				return
			}

			// 以下全部是超级管理员专属。
			if role != "super_admin" {
				replyText(bot, chatID, "❌ 权限不足：普通管理员只能使用【系统监控】和【查询用户】。")
				return
			}

			if strings.Contains(text, "暂停") || strings.Contains(text, "封禁") {
				session.SetStep("WAITING_SUSPEND_USER")
				replyText(bot, chatID, "🛑 请输入要封禁/解封的用户 **Telegram ID**：\n系统会自动反转当前封禁状态。")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "删除用户") {
				session.SetStep("WAITING_FORCE_DELETE_USER")
				replyText(bot, chatID, "⚠️ **高危操作：物理删号**\n请输入要彻底抹除的用户 **Telegram ID**：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "查码") {
				session.SetStep("WAITING_QUERY_CODE")
				replyText(bot, chatID, "🔍 请发送需要追溯的 **邀请码** 或 **续期卡密**：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "模拟过期") {
				session.SetStep("WAITING_SIMULATE_EXPIRE")
				replyText(bot, chatID, "⏱️ 请输入要强制过期的用户 TG ID：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "操控") {
				session.SetStep("WAITING_MANAGE_POINTS_ID")
				replyText(bot, chatID, "🎛️ 请输入需要人工调账的用户 **Telegram ID**：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "生成") && strings.Contains(text, "邀请") {
				session.SetStep("WAITING_GEN_INVITE_COUNT")
				replyText(bot, chatID, "🔢 请输入生成【邀请码】的数量，建议一次不超过 100 张：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "生成") && strings.Contains(text, "续期") {
				session.SetStep("WAITING_GEN_RENEW_DAYS")
				replyText(bot, chatID, "📅 请输入需要生成的续期卡天数，允许范围 1-365：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "邀请码价格") {
				price, err := getConfigIntChecked("invite_price", 300)
				if err != nil {
					log.Printf("⚠️ 邀请码价格配置读取失败: actor=%d err=%s", userID, formatPlainError(err))
					replyText(bot, chatID, "❌ 邀请码价格配置暂时读取失败，请稍后重试。")
					return
				}
				session.SetStep("WAITING_SET_INVITE_PRICE")
				replyText(bot, chatID, fmt.Sprintf("🔢 当前售价为 `%d` 积分。请输入新的售卖积分：", price))
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "续期卡价格") {
				price, err := getConfigIntChecked("renew_price", 150)
				if err != nil {
					log.Printf("⚠️ 续期卡价格配置读取失败: actor=%d err=%s", userID, formatPlainError(err))
					replyText(bot, chatID, "❌ 续期卡价格配置暂时读取失败，请稍后重试。")
					return
				}
				session.SetStep("WAITING_SET_RENEW_PRICE")
				replyText(bot, chatID, fmt.Sprintf("🔢 当前售价为 `%d` 积分。请输入新的售卖积分：", price))
				UserSessions.Store(userID, session)
				return
			}
		}

		if strings.Contains(text, "授权") || strings.Contains(text, "白名单") || (strings.Contains(text, "设置") && strings.Contains(text, "线路")) || strings.Contains(text, "备份") || strings.Contains(text, "后台状态") {
			if role != "super_admin" {
				replyText(bot, chatID, "❌ 此为超级管理员专属功能。")
				return
			}
			if strings.Contains(text, "后台状态") {
				replyText(bot, chatID, formatBackgroundStatusReport())
				writeAuditLog(userID, "VIEW_BACKGROUND_STATUS", "background_jobs", "超级管理员查看后台任务状态")
				return
			}
			if strings.Contains(text, "备份状态") {
				replyText(bot, chatID, formatBackupStatusReport())
				writeAuditLog(userID, "VIEW_BACKUP_STATUS", "database_backup", "超级管理员查看数据库备份状态")
				return
			}
			if strings.Contains(text, "备份") {
				if AppConfig == nil || AppConfig.BackupGroupID == 0 {
					replyText(bot, chatID, "⚠️ 系统环境变量中尚未配置 `BACKUP_GROUP_ID`，无法发送。")
					return
				}
				session.SetStep("WAITING_BACKUP_REASON")
				replyText(bot, chatID, "📝 手动数据库备份会生成加密备份并发送到备份群组。\n请输入本次备份原因，"+adminReasonRequirementText+"：")
				UserSessions.Store(userID, session)
				return
			}
			if strings.Contains(text, "授权") {
				session.SetStep("WAITING_PROMOTE_ID")
				replyText(bot, chatID, "👤 请输入准备提拔的用户的 **Telegram ID**：")
				UserSessions.Store(userID, session)
				return
			}
			if strings.Contains(text, "白名单") {
				session.SetStep("WAITING_WHITELIST_ID")
				replyText(bot, chatID, "🏳️ 请输入要免除保号惩罚的用户 **Telegram ID**：")
				UserSessions.Store(userID, session)
				return
			}
			if strings.Contains(text, "设置") && strings.Contains(text, "线路") {
				session.SetStep("WAITING_SET_SERVER_LINES")
				replyText(bot, chatID, "🗺️ **请发送全新的服务器线路配置内容**：")
				UserSessions.Store(userID, session)
				return
			}
		}

		if text == "📚 待处理求书" {

			if !isBookRequestAdmin(userID) {
				sendPlainText(bot, chatID, "❌ 权限不足。")
				return
			}

			showPendingBookRequests(bot, chatID)
			return
		}

		if strings.Contains(text, "清理遗孀") {
			if role != "super_admin" {
				replyText(bot, chatID, "❌ 此为超级管理员专属高危功能。")
				return
			}

			replyText(bot, chatID, "⏳ 正在拉取服务端数据进行全局对账，请稍候...")

			absUsers, err := absClient.GetAllUsers()
			if err != nil {
				replyText(bot, chatID, "❌ 无法连接到 ABS 服务端获取用户列表。")
				return
			}

			var localUsers []User
			if err := DB.Where("abs_user_id != ''").Find(&localUsers).Error; err != nil {
				replyText(bot, chatID, "❌ 本地数据库读取异常，为防止误删全服，对账协议已强行中止！")
				return
			}

			localMap := make(map[string]bool)
			for _, lu := range localUsers {
				localMap[lu.AbsUserID] = true
			}

			var widowIDs []string
			var widowNames []string
			for _, au := range absUsers {
				if au.Type == "root" || au.Type == "admin" {
					continue
				}
				if !localMap[au.ID] {
					widowIDs = append(widowIDs, au.ID)
					widowNames = append(widowNames, au.Username)
				}
			}

			if len(widowIDs) == 0 {
				replyText(bot, chatID, "🎉 **全局对账完毕！**\n\nABS 服务端的所有常规账号均已完美绑定，不存在任何遗孀账号。")
				clearSession(userID)
				return
			}

			session.SetTemp("widow_ids", strings.Join(widowIDs, ","))
			session.SetStep("WAITING_CLEAN_WIDOWS_REASON")

			replyText(bot, chatID, fmt.Sprintf("⚠️ **警告：发现 %d 个未绑定 Bot 的遗孀账号**\n\n正在为您全量导出名单，请仔细核对：", len(widowIDs)))

			batchSize := 100
			for i := 0; i < len(widowNames); i += batchSize {
				end := i + batchSize
				if end > len(widowNames) {
					end = len(widowNames)
				}
				batch := widowNames[i:end]

				batchMsg := fmt.Sprintf("📦 **遗孀名单分组 (%d-%d)：**\n`%s`", i+1, end, strings.Join(batch, ", "))
				replyText(bot, chatID, batchMsg)

				time.Sleep(150 * time.Millisecond)
			}

			confirmMsg := "🚨 **以上为当前服务端检测出的全量遗孀名单**\n\n⚠️ *此操作将硬删除 ABS 服务端数据，不可逆！*\n\n请先输入本次清理原因，" + adminReasonRequirementText + "。"
			replyText(bot, chatID, confirmMsg)

			UserSessions.Store(userID, session)
			return
		}
	}

	switch session.GetStep() {
	case "WAITING_CONFIRM_SECT_HORN":
		handleSectHornSession(bot, msg, session, text)

	case "WAITING_BOOK_LINK":
		xmlyLink, ok := validateXmlyLink(text)
		if !ok {
			sendPlainText(bot, chatID,
				"❌ 链接格式不正确。\n\n"+
					"请发送以 https:// 开头的喜马拉雅链接，仅支持：\n"+
					"ximalaya.com\n"+
					"www.ximalaya.com\n"+
					"m.ximalaya.com\n"+
					"xima.tv",
			)
			return
		}

		session.SetTemp("book_xmly_link", xmlyLink)
		session.SetStep("WAITING_BOOK_USER_NOTE")
		UserSessions.Store(userID, session)

		sendPlainText(bot, chatID,
			fmt.Sprintf(
				"✅ 链接已记录。\n\n喜马拉雅链接：\n%s\n\n是否需要填写备注？\n例如：想要全集、缺少某几集、指定版本等。\n\n没有备注请发送：跳过",
				xmlyLink,
			),
		)

	case "WAITING_BOOK_USER_NOTE":
		userNote := strings.TrimSpace(text)
		if userNote == "跳过" || userNote == "无" || userNote == "没有" {
			userNote = ""
		}

		if normalizedNote, ok := validateBookRequestNote(userNote); !ok {
			sendPlainText(bot, chatID, bookRequestNoteInvalidText)
			return
		} else {
			userNote = normalizedNote
		}

		session.SetTemp("book_user_note", userNote)
		session.SetStep("WAITING_BOOK_CONFIRM")
		UserSessions.Store(userID, session)

		xmlyLink := session.GetTemp("book_xmly_link")

		sendPlainText(bot, chatID,
			fmt.Sprintf(
				"📚 请确认求书信息：\n\n"+
					"喜马拉雅链接：\n%s\n\n"+
					"用户备注：\n%s\n\n"+
					"确认提交请回复：确认提交\n"+
					"重新填写请回复：重新填写\n"+
					"取消请回复：取消",
				xmlyLink,
				displayBookRequestText(userNote, "无"),
			),
		)

	case "WAITING_BOOK_CONFIRM":
		if text == "确认提交" {
			submitBookRequest(bot, msg, session)
			return
		}

		if text == "重新填写" {
			session.SetStep("WAITING_BOOK_LINK")
			session.SetTemp("book_xmly_link", "")
			session.SetTemp("book_user_note", "")
			UserSessions.Store(userID, session)

			sendPlainText(bot, chatID,
				"📚 请重新发送喜马拉雅链接。\n\n"+
					"要求："+bookRequestLinkRequirementText+"。",
			)
			return
		}

		sendPlainText(bot, chatID, "请回复：确认提交、重新填写，或取消。")

	case "WAITING_BOOK_ADMIN_NOTE":
		if !isBookRequestAdmin(userID) {
			sendPlainText(bot, chatID, "❌ 权限不足。")
			clearSession(userID)
			return
		}

		adminNote, ok := validateBookRequestNote(text)
		if !ok {
			sendPlainText(bot, chatID, bookRequestNoteInvalidText)
			return
		}
		if adminNote == "" {
			sendPlainText(bot, chatID, "❌ 管理员备注不能为空，请重新发送，或发送“取消”退出。")
			return
		}

		reqIDRaw := session.GetTemp("book_admin_note_req_id")
		reqID64, err := strconv.ParseUint(reqIDRaw, 10, 64)
		if err != nil || reqID64 == 0 {
			sendPlainText(bot, chatID, "❌ 工单编号异常，请重新操作。")
			clearSession(userID)
			return
		}

		reqID := uint(reqID64)
		adminName := getTelegramDisplayName(msg.From)

		currentReq, found, err := loadBookRequestByID(DB, reqID, "admin note input")
		if err != nil {
			sendPlainText(bot, chatID, "❌ 查询工单失败，请稍后再试。")
			return
		}
		if !found {
			sendPlainText(bot, chatID, "❌ 工单不存在，请重新操作。")
			clearSession(userID)
			return
		}

		if currentReq.Status != bookRequestStatusClaimed && currentReq.Status != bookRequestStatusNeedInfo {
			sendPlainText(bot, chatID, "⚠️ 该工单当前不能添加备注。")
			clearSession(userID)
			return
		}

		if !canOperateBookRequest(currentReq, userID) {
			sendPlainText(bot, chatID, "❌ 只有接单人或超级管理员可以备注该工单。")
			clearSession(userID)
			return
		}

		now := time.Now()

		noteSaved := false
		err = DB.Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&BookRequest{}).
				Where("id = ? AND status IN ?", reqID, []string{bookRequestStatusClaimed, bookRequestStatusNeedInfo}).
				Updates(map[string]interface{}{
					"admin_note":     adminNote,
					"admin_id":       userID,
					"admin_name":     adminName,
					"last_action_at": &now,
				})
			if res.Error != nil {
				return fmt.Errorf("book request admin note update failed: %s", formatPlainError(res.Error))
			}
			if res.RowsAffected == 0 {
				return nil
			}
			noteSaved = true
			if err := createBookRequestLogInTx(tx, reqID, userID, adminName, "admin_note", currentReq.Status, currentReq.Status, adminNote); err != nil {
				return err
			}
			return writeAuditLogInTx(tx, userID, "BOOK_REQUEST_ADMIN_NOTE", fmt.Sprintf("%d", reqID), 0, "admin added book request note")
		})
		if err != nil {
			log.Printf("book request admin note failed: req=%d admin=%d err=%s", reqID, userID, formatPlainError(err))
			sendPlainText(bot, chatID, "\u274c \u4fdd\u5b58\u5907\u6ce8\u5931\u8d25\uff0c\u8bf7\u7a0d\u540e\u518d\u8bd5\u3002")
			return
		}

		if !noteSaved {
			sendPlainText(bot, chatID, "\u26a0\ufe0f \u8be5\u5de5\u5355\u4e0d\u5b58\u5728\u6216\u5df2\u5904\u7406\uff0c\u65e0\u6cd5\u7ee7\u7eed\u6dfb\u52a0\u5907\u6ce8\u3002")
			clearSession(userID)
			return
		}

		var updatedReq BookRequest
		if err := reloadBookRequestAfterAdminNote(DB, &updatedReq, reqID, adminNote, userID, adminName, now); err == nil {
			adminMsgChatIDRaw := session.GetTemp("book_admin_note_chat_id")
			adminMsgIDRaw := session.GetTemp("book_admin_note_message_id")

			adminMsgChatID, chatParseErr := strconv.ParseInt(adminMsgChatIDRaw, 10, 64)
			adminMsgID64, msgParseErr := strconv.ParseInt(adminMsgIDRaw, 10, 64)
			if chatParseErr != nil || msgParseErr != nil || adminMsgChatID == 0 || adminMsgID64 == 0 {
				adminMsgChatID = 0
				adminMsgID64 = 0
			}
			currentMsgID := int(adminMsgID64)

			if adminMsgChatID != 0 && currentMsgID != 0 {
				editBookRequestAdminMessage(bot, adminMsgChatID, currentMsgID, updatedReq, false)
			}

			refreshStoredBookRequestAdminMessage(bot, updatedReq, false, adminMsgChatID, currentMsgID)
		}

		sendPlainText(bot, chatID, fmt.Sprintf("✅ 已保存求书工单 #%d 的管理员备注，原工单消息已刷新。", reqID))
		clearSession(userID)

	case "WAITING_BOOK_NEED_INFO_NOTE":
		if !isBookRequestAdmin(userID) {
			sendPlainText(bot, chatID, "❌ 权限不足。")
			clearSession(userID)
			return
		}

		needInfoNote, ok := validateBookRequestNote(text)
		if !ok {
			sendPlainText(bot, chatID, bookRequestNoteInvalidText)
			return
		}
		if needInfoNote == "" {
			sendPlainText(bot, chatID, "❌ 补充信息说明不能为空，请重新发送，或发送“取消”退出。")
			return
		}

		reqIDRaw := session.GetTemp("book_need_info_req_id")
		reqID64, err := strconv.ParseUint(reqIDRaw, 10, 64)
		if err != nil || reqID64 == 0 {
			sendPlainText(bot, chatID, "❌ 工单编号异常，请重新操作。")
			clearSession(userID)
			return
		}

		reqID := uint(reqID64)

		currentReq, found, err := loadBookRequestByID(DB, reqID, "need info input")
		if err != nil {
			sendPlainText(bot, chatID, "❌ 查询工单失败，请稍后再试。")
			return
		}
		if !found {
			sendPlainText(bot, chatID, "❌ 工单不存在，请重新操作。")
			clearSession(userID)
			return
		}

		if currentReq.Status != bookRequestStatusClaimed && currentReq.Status != bookRequestStatusNeedInfo {
			sendPlainText(bot, chatID, "⚠️ 该工单当前不能要求补充信息。")
			clearSession(userID)
			return
		}

		if !canOperateBookRequest(currentReq, userID) {
			sendPlainText(bot, chatID, "❌ 只有接单人或超级管理员可以操作该工单。")
			clearSession(userID)
			return
		}

		now := time.Now()
		adminName := getTelegramDisplayName(msg.From)
		needInfoSaved := false
		err = DB.Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&BookRequest{}).
				Where("id = ? AND status IN ?", reqID, []string{bookRequestStatusClaimed, bookRequestStatusNeedInfo}).
				Updates(map[string]interface{}{
					"status":         bookRequestStatusNeedInfo,
					"admin_note":     needInfoNote,
					"admin_id":       userID,
					"admin_name":     adminName,
					"last_action_at": &now,
				})
			if res.Error != nil {
				return fmt.Errorf("book request need info update failed: %s", formatPlainError(res.Error))
			}
			if res.RowsAffected == 0 {
				return nil
			}
			needInfoSaved = true
			if err := createBookRequestLogInTx(tx, reqID, userID, adminName, "need_info", currentReq.Status, bookRequestStatusNeedInfo, needInfoNote); err != nil {
				return err
			}
			return writeAuditLogInTx(tx, userID, "BOOK_REQUEST_NEED_INFO", fmt.Sprintf("%d", reqID), 0, "admin requested book request info")
		})
		if err != nil {
			log.Printf("book request need info failed: req=%d admin=%d err=%s", reqID, userID, formatPlainError(err))
			sendPlainText(bot, chatID, "\u274c \u8bbe\u7f6e\u8865\u5145\u4fe1\u606f\u5931\u8d25\uff0c\u8bf7\u7a0d\u540e\u518d\u8bd5\u3002")
			return
		}
		if !needInfoSaved {
			sendPlainText(bot, chatID, "\u26a0\ufe0f \u8be5\u5de5\u5355\u72b6\u6001\u5df2\u53d8\u5316\uff0c\u8bf7\u7a0d\u540e\u67e5\u770b\u6700\u65b0\u72b6\u6001\u3002")
			clearSession(userID)
			return
		}

		var updatedReq BookRequest
		if err := reloadBookRequestAfterNeedInfo(DB, &updatedReq, reqID, needInfoNote, userID, adminName, now); err == nil {
			adminMsgChatIDRaw := session.GetTemp("book_need_info_chat_id")
			adminMsgIDRaw := session.GetTemp("book_need_info_message_id")

			adminMsgChatID, chatParseErr := strconv.ParseInt(adminMsgChatIDRaw, 10, 64)
			adminMsgID64, msgParseErr := strconv.ParseInt(adminMsgIDRaw, 10, 64)
			if chatParseErr != nil || msgParseErr != nil || adminMsgChatID == 0 || adminMsgID64 == 0 {
				adminMsgChatID = 0
				adminMsgID64 = 0
			}
			currentMsgID := int(adminMsgID64)

			if adminMsgChatID != 0 && currentMsgID != 0 {
				editBookRequestAdminMessage(bot, adminMsgChatID, currentMsgID, updatedReq, false)
			}

			refreshStoredBookRequestAdminMessage(bot, updatedReq, false, adminMsgChatID, currentMsgID)

			sendPlainText(bot, updatedReq.UserID, fmt.Sprintf(
				"❓ 你的求书 #%d 需要补充信息：\n\n%s\n\n请直接回复补充内容，系统会通知接单管理员。",
				updatedReq.ID,
				needInfoNote,
			))
		}

		sendPlainText(bot, chatID, fmt.Sprintf("✅ 已将求书工单 #%d 设置为需要用户补充信息。", reqID))
		clearSession(userID)

	case "WAITING_SET_INVITE_PRICE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		price, err := strconv.Atoi(text)
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 金额格式错误，请输入 0-100000 之间的整数：")
			return
		}

		oldPrice, err := getConfigIntChecked("invite_price", 300)
		if err != nil {
			log.Printf("⚠️ 邀请码价格配置读取失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 邀请码价格配置暂时读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		session.SetTemp("invite_price_new", strconv.Itoa(price))
		session.SetTemp("invite_price_old", strconv.Itoa(oldPrice))
		session.SetStep("WAITING_SET_INVITE_PRICE_REASON")
		replyText(bot, chatID, fmt.Sprintf("📝 邀请码售价将从 `%d` 调整为 `%d` 积分。\n请输入本次调价原因，%s：", oldPrice, price, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_SET_INVITE_PRICE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		price, err := strconv.Atoi(session.GetTemp("invite_price_new"))
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 价格会话状态异常，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}

		oldPrice, err := getConfigIntChecked("invite_price", 300)
		if err != nil {
			log.Printf("⚠️ 邀请码价格二次确认读取配置失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 邀请码价格配置暂时读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		session.SetTemp("invite_price_reason", reason)
		session.SetStep("WAITING_CONFIRM_SET_INVITE_PRICE")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **邀请码价格二次确认**\n\n当前售价：`%d` 积分\n新售价：`%d` 积分\n原因：`%s`\n\n确认更新请回复：`确认设置邀请码价格`\n取消请回复：`取消`",
			oldPrice,
			price,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SET_INVITE_PRICE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认设置邀请码价格" {
			replyText(bot, chatID, "🛑 已取消设置邀请码价格。")
			clearSession(userID)
			return
		}

		price, err := strconv.Atoi(session.GetTemp("invite_price_new"))
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 价格会话状态异常，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("invite_price_reason")

		if _, err := setConfigIntWithAudit(userID, "invite_price", price, 300, "SET_INVITE_PRICE", "invite_price", reason); err != nil {
			log.Printf("⚠️ 设置邀请码价格失败: actor=%d price=%d err=%s", userID, price, formatPlainError(err))
			replyText(bot, chatID, "❌ 邀请码价格更新失败，请稍后重试。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("✅ **管理面板配置成功：邀请码售价已变更为 %d 积分！**", price))
		clearSession(userID)

	case "WAITING_CONFIRM_BREAKTHROUGH":
		handleBreakthroughConfirmation(bot, msg, session, text)

	case "WAITING_CONFIRM_BLIND_BOX":
		if text == "确认开启盲盒" {
			replyMsg, broadcastMsg, err := executeBlindBoxOpen(msg.From)
			if err != nil {
				if errors.Is(err, errPointsNotEnough) {
					replyText(bot, chatID, "❌ 您的积分储备余额不足，开盒失败。")
				} else {
					log.Printf("❌ 积分盲盒事务失败: user=%d cost=%d err=%s", userID, blindBoxCost, formatPlainError(err))
					replyText(bot, chatID, "❌ 奖品生成触发底层碰撞保护，为您中止交易。\n💰 本次操作未扣除您的任何积分。")
				}
				clearSession(userID)
				return
			}

			replyText(bot, chatID, replyMsg)
			if broadcastMsg != "" && AppConfig.NoticeGroupID != 0 {
				sendGroupAutoDeleteMessage(bot, AppConfig.NoticeGroupID, broadcastMsg)
			}
		} else {
			replyText(bot, chatID, "🛑 已取消开启积分盲盒。")
		}
		clearSession(userID)

	case "WAITING_SHOP_BUY":
		item, exists := treasureShopItemFromText(text)
		if !exists {
			replyText(bot, chatID, "❌ 输入序号有误，请重新输入或发送 `取消`：")
			return
		}
		session.SetTemp("buy_item_name", item.Name)
		session.SetTemp("buy_item_price", strconv.Itoa(item.Price))
		session.SetStep("WAITING_CONFIRM_SHOP_BUY")
		replyText(bot, chatID, treasureShopBuyConfirmMarkdownText(item)+"\n👉 请回复 `确认购买` 或 `取消`。")
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SHOP_BUY":
		if text == "确认购买" {
			shopItemName := session.GetTemp("buy_item_name")
			price, err := strconv.Atoi(session.GetTemp("buy_item_price"))
			if err != nil || price <= 0 {
				replyText(bot, chatID, "❌ 聚宝斋购买会话异常，已中止。请重新发起购买。")
				clearSession(userID)
				return
			}

			if err := purchaseTreasureShopItem(userID, shopItemName, price); err != nil {
				if errors.Is(err, errInsufficientPoints) {
					replyText(bot, chatID, "❌ 您的积分不足，购买失败。")
				} else {
					replyText(bot, chatID, "❌ 交易异常，购买失败，请稍后重试。")
				}
			} else {
				replyText(bot, chatID, treasureShopBuySuccessMarkdownText(shopItemName))
			}
		} else {
			replyText(bot, chatID, "🛑 已取消购买。")
		}
		clearSession(userID)

	case "WAITING_INVENTORY_ACTION":
		itemName := session.GetTemp(fmt.Sprintf("inv_item_%s", text))
		if itemName == "" {
			replyText(bot, chatID, "❌ 输入序号有误，请重新输入或发送 `取消`：")
			return
		}

		// 🛡️ 防呆机制：拦截破境丹，提醒自动消耗
		if strings.Contains(itemName, "丹") && itemName != "聚灵丹" && itemName != "九转造化丹" {
			replyText(bot, chatID, fmt.Sprintf("⚠️ **【%s】** 是渡劫破阶专用丹药。\n👉 *无需手动吞服*，请在达到境界大圆满时直接发送 `突破` 指令，天道雷劫降临时系统会自动吞服！", inventoryItemMarkdownName(itemName)))
			clearSession(userID)
			return
		}

		// 🧪 经验丹丹毒测算拦截。
		if periodStart, _, maxCount, cycleName, addHours, ok := getManualPillUsageConfig(itemName, time.Now()); ok {
			usedCount, err := countManualPillUsage(userID, itemName, periodStart)
			if err != nil {
				log.Printf("⚠️ 查询丹药服用额度失败: user=%d item=%s err=%s", userID, formatPlainValue(itemName), formatPlainError(err))
				replyText(bot, chatID, "❌ 丹毒沉淀读取失败，暂不能吞服丹药，请稍后重试。")
				clearSession(userID)
				return
			}

			if int(usedCount) >= maxCount {
				replyText(bot, chatID, fmt.Sprintf("🩸 **丹毒警告**：您%s服用【%s】已达上限 (%d/%d次)，继续吞服恐会爆体而亡！", cycleName, inventoryItemMarkdownName(itemName), usedCount, maxCount))
				clearSession(userID)
				return
			}

			session.SetTemp("use_item_name", itemName)
			session.SetTemp("use_item_hours", fmt.Sprintf("%.1f", addHours))
			session.SetTemp("use_item_cycle", cycleName)
			session.SetTemp("use_item_count", fmt.Sprintf("%d", usedCount))
			session.SetTemp("use_item_max", fmt.Sprintf("%d", maxCount))

			session.SetStep("WAITING_CONFIRM_USE_ITEM")
			replyText(bot, chatID, fmt.Sprintf("🔮 **使用确认**：您正准备吞服 **【%s】**。\n⚠️ *%s已服药 (%d/%d) 次，吞服后修为将暴涨 `%.1f` 小时！*\n👉 请回复 `确认使用` 或 `取消`。", inventoryItemMarkdownName(itemName), cycleName, usedCount, maxCount, addHours))
			UserSessions.Store(userID, session)
		}

	case "WAITING_CONFIRM_USE_ITEM":
		if text == "确认使用" {
			itemName := session.GetTemp("use_item_name")

			now := time.Now()
			periodStart, periodKey, maxCount, cycleName, addHours, ok := getManualPillUsageConfig(itemName, now)
			if !ok {
				replyText(bot, chatID, "❌ 该物品不可手动吞服。")
				clearSession(userID)
				return
			}

			// 🛡️ 高并发原子更新消耗引擎：
			// 1. 初始化本周期额度记录。
			// 2. used_count < maxCount 时才能 +1。
			// 3. 扣背包库存。
			// 4. 写使用日志。
			// 5. 增加丹药修为加成。
			// 这些动作在同一个事务里，任何一步失败都会回滚。
			err := DB.Transaction(func(tx *gorm.DB) error {
				var historicalUsed int64
				if err := tx.Model(&ItemUsageLog{}).
					Where("user_id = ? AND item_name = ? AND used_at >= ?", userID, itemName, periodStart).
					Count(&historicalUsed).Error; err != nil {
					return err
				}

				initialUsed := int(historicalUsed)
				if initialUsed > maxCount {
					initialUsed = maxCount
				}

				// 如果额度记录不存在，就按照历史日志初始化。
				// 如果已经存在，则不覆盖。
				if err := createItemUsageQuotaIfMissingInTx(tx, &ItemUsageQuota{
					UserID:    userID,
					ItemName:  itemName,
					PeriodKey: periodKey,
					UsedCount: initialUsed,
				}); err != nil {
					return err
				}

				quotaRes := tx.Model(&ItemUsageQuota{}).
					Where("user_id = ? AND item_name = ? AND period_key = ? AND used_count < ?", userID, itemName, periodKey, maxCount).
					UpdateColumn("used_count", gorm.Expr("used_count + 1"))

				if quotaRes.Error != nil {
					return quotaRes.Error
				}

				if quotaRes.RowsAffected == 0 {
					return errUsageLimitReached
				}

				res := tx.Model(&Inventory{}).
					Where("user_id = ? AND item_name = ? AND quantity > 0", userID, itemName).
					UpdateColumn("quantity", gorm.Expr("quantity - 1"))

				if res.Error != nil {
					return res.Error
				}

				if res.RowsAffected == 0 {
					return errItemNotEnough
				}

				usageLog := ItemUsageLog{
					UserID:   userID,
					ItemName: itemName,
					UsedAt:   now,
				}
				if err := createItemUsageLogInTx(tx, &usageLog); err != nil {
					return err
				}

				return applyPillAudioTimeInTx(tx, userID, addHours)
			})

			if err != nil {
				if errors.Is(err, errUsageLimitReached) {
					replyText(bot, chatID, fmt.Sprintf("🩸 **丹毒警告**：您%s服用【%s】已达上限，不能继续吞服。", cycleName, inventoryItemMarkdownName(itemName)))
				} else if errors.Is(err, errItemNotEnough) {
					replyText(bot, chatID, "❌ 吞服失败，乾坤袋内该物品余量不足。")
				} else {
					log.Printf("⚠️ 吞服丹药事务失败: user=%d item=%s err=%s", userID, formatPlainValue(itemName), formatPlainError(err))
					replyText(bot, chatID, "❌ 吞服失败，系统繁忙，请稍后重试。")
				}
			} else {
				cul := GetOrCreateCultivation(userID)
				newRealm := "`读取失败`"
				if cul != nil {
					SyncCultivationRealm(cul)
					newRealm = GetRealmName(cul)
				} else {
					log.Printf("⚠️ 吞服丹药成功后修仙档案读取失败: user=%d item=%s", userID, formatPlainValue(itemName))
				}
				replyText(bot, chatID, fmt.Sprintf("✨ **吞服成功！**\n\n磅礴的药力在体内化开，您的总修为凭空增加了 `%.1f` 小时！\n📿 当前境界：**%s**", addHours, newRealm))
			}
		} else {
			replyText(bot, chatID, "🛑 已取消吞服。")
		}
		clearSession(userID)

	case "WAITING_SET_RENEW_PRICE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		price, err := strconv.Atoi(text)
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 金额格式错误，请输入 0-100000 之间的整数：")
			return
		}

		oldPrice, err := getConfigIntChecked("renew_price", 150)
		if err != nil {
			log.Printf("⚠️ 续期卡价格配置读取失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 续期卡价格配置暂时读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		session.SetTemp("renew_price_new", strconv.Itoa(price))
		session.SetTemp("renew_price_old", strconv.Itoa(oldPrice))
		session.SetStep("WAITING_SET_RENEW_PRICE_REASON")
		replyText(bot, chatID, fmt.Sprintf("📝 续期卡售价将从 `%d` 调整为 `%d` 积分。\n请输入本次调价原因，%s：", oldPrice, price, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_SET_RENEW_PRICE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		price, err := strconv.Atoi(session.GetTemp("renew_price_new"))
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 价格会话状态异常，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}

		oldPrice, err := getConfigIntChecked("renew_price", 150)
		if err != nil {
			log.Printf("⚠️ 续期卡价格二次确认读取配置失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 续期卡价格配置暂时读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		session.SetTemp("renew_price_reason", reason)
		session.SetStep("WAITING_CONFIRM_SET_RENEW_PRICE")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **续期卡价格二次确认**\n\n当前售价：`%d` 积分\n新售价：`%d` 积分\n原因：`%s`\n\n确认更新请回复：`确认设置续期卡价格`\n取消请回复：`取消`",
			oldPrice,
			price,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SET_RENEW_PRICE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认设置续期卡价格" {
			replyText(bot, chatID, "🛑 已取消设置续期卡价格。")
			clearSession(userID)
			return
		}

		price, err := strconv.Atoi(session.GetTemp("renew_price_new"))
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 价格会话状态异常，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("renew_price_reason")

		if _, err := setConfigIntWithAudit(userID, "renew_price", price, 150, "SET_RENEW_PRICE", "renew_price", reason); err != nil {
			log.Printf("⚠️ 设置续期卡价格失败: actor=%d price=%d err=%s", userID, price, formatPlainError(err))
			replyText(bot, chatID, "❌ 续期卡价格更新失败，请稍后重试。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("✅ **管理面板配置成功：续期卡售价已变更为 %d 积分！**", price))
		clearSession(userID)

	case "WAITING_EXCHANGE_CHOICE":
		if text == "1" {
			invPrice, err := getConfigIntChecked("invite_price", 300)
			if err != nil {
				log.Printf("⚠️ 兑换邀请码价格配置读取失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 邀请码价格配置暂时读取失败，本次交易未扣除积分，请稍后重试。")
				clearSession(userID)
				return
			}

			var code string
			var txCode string

			err = DB.Transaction(func(tx *gorm.DB) error {
				txCode = ""
				// 1. 原子扣除兑换所需积分并写入流水。
				if err := applyPointDeltaInTx(
					tx,
					userID,
					-invPrice,
					"exchange_invite",
					fmt.Sprintf("兑换邀请码，消耗 %d 积分", invPrice),
					"exchange",
					"invite",
				); err != nil {
					if errors.Is(err, errPointsNotEnough) {
						return errInsufficientPoints
					}
					return err
				}

				// 2. 生成邀请码并写入数据库。失败则事务回滚，积分不会被扣。
				for i := 0; i < 5; i++ {
					candidateCode := generateRandomCode(16)

					if err := createInviteCodeRecord(tx, candidateCode); err == nil {
						txCode = candidateCode
						break
					} else {
						if isUniqueConstraintError(err) {
							continue
						}
						return err
					}
				}

				if txCode == "" {
					return errCreateInviteCodeFailed
				}

				return nil
			})
			if err == nil {
				code = txCode
			}

			if err != nil {
				switch assetCreationErrorCode(err) {
				case "USER_NOT_FOUND":
					replyText(bot, chatID, "❌ 未检测到您的积分账户，请先完成注册、绑定或签到初始化账户。")
				case "INSUFFICIENT_POINTS":
					replyText(bot, chatID, "❌ 您的积分储备余额不足，兑换失败。")
				case "CREATE_INVITE_CODE_FAILED":
					replyText(bot, chatID, "❌ 邀请码生成失败，本次交易未扣除积分，请稍后重试。")
				case "SECURITY_PEPPER_NOT_CONFIGURED":
					replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
				default:
					log.Printf("❌ 兑换邀请码事务失败: user=%d price=%d err=%s", userID, invPrice, formatPlainError(err))
					replyText(bot, chatID, "❌ 兑换失败，本次交易未扣除积分，请稍后重试。")
				}

				clearSession(userID)
				return
			}

			replyText(bot, chatID, fmt.Sprintf("🎉 **兑换成功！扣除 %d 积分**\n🎁 你的专属邀请码为：`%s`", invPrice, code))
			clearSession(userID)
			return

		} else if text == "2" {
			renPrice, err := getConfigIntChecked("renew_price", 150)
			if err != nil {
				log.Printf("⚠️ 兑换续期卡价格配置读取失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 续期卡价格配置暂时读取失败，本次交易未扣除积分，请稍后重试。")
				clearSession(userID)
				return
			}

			var code string
			var txCode string
			const renewDays = 30

			err = DB.Transaction(func(tx *gorm.DB) error {
				txCode = ""
				// 1. 原子扣除兑换所需积分并写入流水。
				if err := applyPointDeltaInTx(
					tx,
					userID,
					-renPrice,
					"exchange_renew",
					fmt.Sprintf("兑换 %d 天续期卡，消耗 %d 积分", renewDays, renPrice),
					"exchange",
					"renew",
				); err != nil {
					if errors.Is(err, errPointsNotEnough) {
						return errInsufficientPoints
					}
					return err
				}

				// 2. 生成续期卡并写入数据库。失败则事务回滚，积分不会被扣。
				for i := 0; i < 5; i++ {
					candidateCode := fmt.Sprintf("R%d-%s", renewDays, generateRandomCode(16))

					if err := createRenewCodeRecord(tx, candidateCode, renewDays); err == nil {
						txCode = candidateCode
						break
					} else {
						if isUniqueConstraintError(err) {
							continue
						}
						return err
					}
				}

				if txCode == "" {
					return errCreateRenewCodeFailed
				}

				return nil
			})
			if err == nil {
				code = txCode
			}

			if err != nil {
				switch assetCreationErrorCode(err) {
				case "USER_NOT_FOUND":
					replyText(bot, chatID, "❌ 未检测到您的积分账户，请先完成注册、绑定或签到初始化账户。")
				case "INSUFFICIENT_POINTS":
					replyText(bot, chatID, "❌ 您的积分储备余额不足，兑换失败。")
				case "CREATE_RENEW_CODE_FAILED":
					replyText(bot, chatID, "❌ 续期卡生成失败，本次交易未扣除积分，请稍后重试。")
				case "SECURITY_PEPPER_NOT_CONFIGURED":
					replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
				default:
					log.Printf("❌ 兑换续期卡事务失败: user=%d price=%d err=%s", userID, renPrice, formatPlainError(err))
					replyText(bot, chatID, "❌ 兑换失败，本次交易未扣除积分，请稍后重试。")
				}

				clearSession(userID)
				return
			}

			replyText(bot, chatID, fmt.Sprintf("🎉 **兑换成功！扣除 %d 积分**\n🎁 30天续期卡密为：`%s`", renPrice, code))
			clearSession(userID)
			return

		} else if text == "3" {
			clearSession(userID)
			showTreasureShopHome(bot, msg.From, chatID)
			return

		} else {
			replyText(bot, chatID, "⚠️ 输入不识别。请输入数字 1 或 2，或发送 `取消`。")
			return
		}

	case "WAITING_RED_POINTS":
		pts, err := strconv.Atoi(text)
		if err != nil || pts < 10 {
			replyText(bot, chatID, "❌ 金额不规范，最少 10 积分：")
			return
		}
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 发红包前钱包读取失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 钱包读取失败，请稍后重新输入红包金额。")
			return
		}
		if u.Points < pts {
			replyText(bot, chatID, "❌ 您钱包里的可用积分不足。")
			clearSession(userID)
			return
		}
		session.SetTemp("red_points", text)
		session.SetStep("WAITING_RED_COUNT")
		replyText(bot, chatID, "🔢 请输入裂变分发的 **红包总个数** (2-30)：")
		UserSessions.Store(userID, session)

	case "WAITING_RED_COUNT":
		count, err := strconv.Atoi(text)
		if err != nil || count <= 2 || count > 30 {
			replyText(bot, chatID, "❌ 个数限制在 2 ~ 30 个之间：")
			return
		}

		pts, err := strconv.Atoi(session.GetTemp("red_points"))
		if err != nil || pts < 10 {
			replyText(bot, chatID, "❌ 红包金额异常，请重新发起红包。")
			clearSession(userID)
			return
		}
		if pts < count {
			replyText(bot, chatID, "❌ 红包总积分不能小于红包个数，每个红包至少需要 1 积分。")
			return
		}

		cleanSender := escapeMarkdown(msg.From.FirstName + " " + msg.From.LastName)

		err = DB.Transaction(func(tx *gorm.DB) error {
			txRedID := ""
			// 1. 创建红包。ID 碰撞时重试；后续扣分失败会回滚红包记录。
			for i := 0; i < 5; i++ {
				candidateID := "HB-" + generateRandomCode(10)

				packet := RedPacket{
					ID:          candidateID,
					SenderID:    userID,
					SenderName:  cleanSender,
					TotalPoints: pts,
					Count:       count,
					LeftCount:   count,
					LeftPoints:  pts,
					CreatedAt:   time.Now(),
				}
				err := createRedPacketInTx(tx, &packet)

				if err == nil {
					txRedID = candidateID
					break
				}

				if isUniqueConstraintError(err) {
					continue
				}

				return err
			}

			if txRedID == "" {
				return errCreateRedPacketFailed
			}

			// 2. 原子扣除发包人积分并写入流水。流水和红包创建同事务，避免账实不一致。
			if err := applyPointDeltaInTx(
				tx,
				userID,
				-pts,
				"redpacket_send",
				fmt.Sprintf("发放积分红包，消耗 %d 积分", pts),
				"redpacket",
				txRedID,
			); err != nil {
				if errors.Is(err, errPointsNotEnough) {
					return errInsufficientPoints
				}
				return err
			}

			return nil
		})

		if err != nil {
			switch assetCreationErrorCode(err) {
			case "USER_NOT_FOUND":
				replyText(bot, chatID, "❌ 未检测到您的积分账户，请先完成注册、绑定或签到初始化账户。")
			case "INSUFFICIENT_POINTS":
				replyText(bot, chatID, "❌ 发包过程中积分不足。")
			case "CREATE_REDPACKET_FAILED":
				replyText(bot, chatID, "❌ 红包编号生成失败，本次交易未扣除积分，请稍后重试。")
			default:
				log.Printf("❌ 发红包事务失败: user=%d points=%d count=%d err=%s", userID, pts, count, formatPlainError(err))
				replyText(bot, chatID, "❌ 红包创建失败，本次交易未扣除积分，请稍后重试。")
			}

			clearSession(userID)
			return
		}

		replyText(bot, chatID, "🚀 **红包打包成功！**\n📢 机器人已在大群同步发放。")

		if AppConfig.NoticeGroupID != 0 {
			群信息 := fmt.Sprintf(
				"🧧 **%s 发放了一个拼手气积分红包！**\n\n"+
					"💰 红包总额: `%d` 积分\n"+
					"📦 红包份数: `%d` 份\n\n"+
					"👇 快在群内回复关键字 【`抢`】 拼手气吧！",
				cleanSender,
				pts,
				count,
			)
			sendGroupAutoDeleteMessage(bot, AppConfig.NoticeGroupID, 群信息)
		}

		clearSession(userID)

	case "WAITING_PROMOTE_ID":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "请输入纯数字ID：")
			return
		}
		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止授权自己为管理员。")
			clearSession(userID)
			return
		}
		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已是超级管理员，无需授权。")
			clearSession(userID)
			return
		}
		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 本地查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 授权管理员目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		if tUser.Role == "admin" {
			replyText(bot, chatID, "ℹ️ 目标用户已经是管理员，无需重复授权。")
			clearSession(userID)
			return
		}

		session.SetTemp("promote_tgt_uid", strconv.FormatInt(tgtID, 10))
		session.SetTemp("promote_tgt_username", tUser.Username)
		session.SetStep("WAITING_PROMOTE_REASON")
		replyText(bot, chatID, fmt.Sprintf("📝 即将授权用户 `%s` / `%d` 为管理员。\n请输入授权原因，%s：", escapeMarkdown(tUser.Username), tgtID, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_PROMOTE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tgtID := session.GetTemp("promote_tgt_uid")
		username := session.GetTemp("promote_tgt_username")
		session.SetTemp("promote_reason", reason)
		session.SetStep("WAITING_CONFIRM_PROMOTE")

		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **授权管理员二次确认**\n\n目标用户：`%s`\nTG ID：`%s`\n原因：`%s`\n\n确认授权请回复：`确认授权管理员`\n取消请回复：`取消`",
			escapeMarkdown(username),
			tgtID,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_PROMOTE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认授权管理员" {
			replyText(bot, chatID, "🛑 已取消授权管理员。")
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(session.GetTemp("promote_tgt_uid"), 10, 64)
		if err != nil || tgtID == 0 {
			replyText(bot, chatID, "❌ 授权会话状态异常，已中止。请重新发起授权流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("promote_reason")

		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止授权自己为管理员。")
			clearSession(userID)
			return
		}
		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已是超级管理员，无需授权。")
			clearSession(userID)
			return
		}

		status, err := promoteAdminWithAudit(userID, tgtID, reason)
		if err != nil {
			log.Printf("⚠️ 授权管理员失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 授权失败，请稍后重试。")
			clearSession(userID)
			return
		}
		switch status {
		case adminMutationSelf:
			replyText(bot, chatID, "❌ 禁止授权自己为管理员。")
			clearSession(userID)
			return
		case adminMutationNotFound:
			replyText(bot, chatID, "❌ 本地查无此人。")
			clearSession(userID)
			return
		case adminMutationTargetSuperAdmin:
			replyText(bot, chatID, "❌ 目标用户已是超级管理员，无需授权。")
			clearSession(userID)
			return
		case adminMutationAlreadyAdmin:
			replyText(bot, chatID, "ℹ️ 目标用户已经是管理员，无需重复授权。")
			clearSession(userID)
			return
		case adminMutationTargetStateChanged:
			replyText(bot, chatID, "⚠️ 目标用户状态已变化，请重新发起授权流程。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("👑 晋升成功！用户 `%d` 已成为【管理员】。", tgtID))
		clearSession(userID)

	case "WAITING_WHITELIST_ID":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "请输入纯数字：")
			return
		}
		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止将自己加入白名单。")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 白名单目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		if tUser.Role == "super_admin" {
			replyText(bot, chatID, "❌ 禁止将超级管理员加入白名单。")
			clearSession(userID)
			return
		}

		if tUser.IsWhitelist {
			replyText(bot, chatID, "ℹ️ 目标用户已经在白名单中，无需重复设置。")
			clearSession(userID)
			return
		}

		session.SetTemp("whitelist_tgt_uid", strconv.FormatInt(tgtID, 10))
		session.SetTemp("whitelist_tgt_username", tUser.Username)
		session.SetStep("WAITING_WHITELIST_REASON")
		replyText(bot, chatID, fmt.Sprintf("📝 即将将用户 `%s` / `%d` 加入白名单。\n请输入设置原因，%s：", escapeMarkdown(tUser.Username), tgtID, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_WHITELIST_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tgtID := session.GetTemp("whitelist_tgt_uid")
		username := session.GetTemp("whitelist_tgt_username")
		session.SetTemp("whitelist_reason", reason)
		session.SetStep("WAITING_CONFIRM_WHITELIST")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **白名单二次确认**\n\n目标用户：`%s`\nTG ID：`%s`\n原因：`%s`\n\n加入白名单后，该用户将跳过账号生命周期封禁和自动清理。\n确认设置请回复：`确认设置白名单`\n取消请回复：`取消`",
			escapeMarkdown(username),
			tgtID,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_WHITELIST":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认设置白名单" {
			replyText(bot, chatID, "🛑 已取消设置白名单。")
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(session.GetTemp("whitelist_tgt_uid"), 10, 64)
		if err != nil || tgtID == 0 {
			replyText(bot, chatID, "❌ 白名单会话状态异常，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("whitelist_reason")

		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止将自己加入白名单。")
			clearSession(userID)
			return
		}

		status, err := setWhitelistWithAudit(userID, tgtID, reason)
		if err != nil {
			log.Printf("⚠️ 设置白名单失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 设置白名单失败，请稍后重试。")
			clearSession(userID)
			return
		}
		switch status {
		case adminMutationSelf:
			replyText(bot, chatID, "❌ 禁止将自己加入白名单。")
			clearSession(userID)
			return
		case adminMutationNotFound:
			replyText(bot, chatID, "❌ 查无此人。")
			clearSession(userID)
			return
		case adminMutationTargetSuperAdmin:
			replyText(bot, chatID, "❌ 禁止将超级管理员加入白名单。")
			clearSession(userID)
			return
		case adminMutationAlreadyWhitelisted:
			replyText(bot, chatID, "ℹ️ 目标用户已经在白名单中，无需重复设置。")
			clearSession(userID)
			return
		case adminMutationTargetStateChanged:
			replyText(bot, chatID, "⚠️ 目标用户状态已变化，请重新发起白名单设置流程。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("🏳️ 用户 `%d` 已进入圣光白名单。", tgtID))
		clearSession(userID)

	case "WAITING_SET_SERVER_LINES":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if len([]rune(text)) > 4000 {
			replyText(bot, chatID, "❌ 线路配置内容过长，请控制在 4000 字以内。")
			return
		}

		session.SetTemp("server_lines_content", text)
		session.SetStep("WAITING_SET_SERVER_LINES_REASON")
		replyText(bot, chatID, fmt.Sprintf(
			"📝 **线路配置预览**\n\n%s\n\n请输入本次更新原因，"+adminReasonRequirementText+"：",
			escapeMarkdown(truncateRunes(text, 800)),
		))
		UserSessions.Store(userID, session)

	case "WAITING_SET_SERVER_LINES_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		lines := session.GetTemp("server_lines_content")
		if len([]rune(lines)) > 4000 {
			replyText(bot, chatID, "❌ 线路配置会话状态异常或内容过长，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}

		session.SetTemp("server_lines_reason", reason)
		session.SetStep("WAITING_CONFIRM_SET_SERVER_LINES")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **线路配置二次确认**\n\n内容长度：`%d` 字\n原因：`%s`\n\n确认更新请回复：`确认设置线路`\n取消请回复：`取消`",
			len([]rune(lines)),
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SET_SERVER_LINES":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认设置线路" {
			replyText(bot, chatID, "🛑 已取消设置线路。")
			clearSession(userID)
			return
		}

		lines := session.GetTemp("server_lines_content")
		reason := session.GetTemp("server_lines_reason")
		if len([]rune(lines)) > 4000 {
			replyText(bot, chatID, "❌ 线路配置会话状态异常或内容过长，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}

		validatedLines, ok := validateServerLinesContent(lines)
		if !ok {
			replyText(bot, chatID, "❌ 线路配置会话状态异常或内容不符合要求，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}
		lines = validatedLines

		if _, _, err := setServerLinesWithAudit(userID, lines, reason); err != nil {
			log.Printf("⚠️ 更新线路配置失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 线路配置更新失败，请稍后重试。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "✅ **线路配置已成功更新！**")
		clearSession(userID)

	case "WAITING_BACKUP_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}
		if AppConfig == nil || AppConfig.BackupGroupID == 0 {
			replyText(bot, chatID, "⚠️ 系统环境变量中尚未配置 `BACKUP_GROUP_ID`，无法发送。")
			clearSession(userID)
			return
		}

		session.SetTemp("backup_reason", reason)
		session.SetStep("WAITING_CONFIRM_BACKUP")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **手动备份二次确认**\n\n目标：备份群组\n原因：`%s`\n\n备份文件会使用 AES-GCM 加密后发送，请确认备份密钥已妥善保管。\n确认执行请回复：`确认备份`\n取消请回复：`取消`",
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_BACKUP":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认备份" {
			replyText(bot, chatID, "🛑 已取消手动备份。")
			clearSession(userID)
			return
		}

		reason := session.GetTemp("backup_reason")
		if _, ok := validateAdminReason(reason); !ok {
			replyText(bot, chatID, "❌ 备份会话状态异常，已中止。请重新发起备份流程。")
			clearSession(userID)
			return
		}
		if AppConfig == nil || AppConfig.BackupGroupID == 0 {
			replyText(bot, chatID, "⚠️ 系统环境变量中尚未配置 `BACKUP_GROUP_ID`，无法发送。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "⏳ 正在打包加密数据库备份并发送到备份群组...")
		go backupDatabaseToTelegram(bot, userID, reason)
		clearSession(userID)

	case "WAITING_MANAGE_POINTS_ID":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "格式错误，请输入纯数字 TG ID：")
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 目标未查到记录。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 调账目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		session.SetTemp("tgt_uid", text)
		session.SetTemp("tgt_username", tUser.Username)
		session.SetStep("WAITING_MANAGE_POINTS_VAL")
		replyText(bot, chatID, "🔢 请输入增减的积分数值。\n\n限制：\n- 单次最多 `5000` 积分\n- 每个超级管理员每日累计最多 `20000` 积分\n\n增加输入正数，如 `100`；扣除输入负数，如 `-50`。")
		UserSessions.Store(userID, session)

	case "WAITING_MANAGE_POINTS_VAL":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		val, err := strconv.Atoi(text)
		if err != nil || val == 0 {
			replyText(bot, chatID, "格式错误，请输入非 0 整数：")
			return
		}

		if absInt(val) > 5000 {
			replyText(bot, chatID, "❌ 单次调账不能超过 5000 积分。")
			return
		}

		todayTotal, err := getTodayAuditDeltaTotal(userID, "ADJUST_POINTS")
		if err != nil {
			log.Printf("⚠️ 调账额度查询失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 调账额度暂时无法读取，请稍后再试。")
			clearSession(userID)
			return
		}
		if adminAdjustDailyLimitExceeded(todayTotal, val) {
			replyText(bot, chatID, fmt.Sprintf("❌ 今日调账额度不足。\n\n今日已累计调整：`%d`\n本次申请：`%d`\n每日上限：`20000`", todayTotal, absInt(val)))
			clearSession(userID)
			return
		}

		session.SetTemp("points_delta", strconv.Itoa(val))
		session.SetStep("WAITING_MANAGE_POINTS_REASON")
		replyText(bot, chatID, "📝 请输入本次调账原因，"+adminReasonRequirementText+"。\n例如：`活动奖励补发`、`异常积分回滚`。")
		UserSessions.Store(userID, session)

	case "WAITING_MANAGE_POINTS_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tID, err := strconv.ParseInt(session.GetTemp("tgt_uid"), 10, 64)
		if err != nil || tID == 0 {
			replyText(bot, chatID, "❌ 调账会话状态异常，已中止。请重新发起调账流程。")
			clearSession(userID)
			return
		}
		val, err := strconv.Atoi(session.GetTemp("points_delta"))
		if err != nil || val == 0 {
			replyText(bot, chatID, "❌ 调账数值状态异常，已中止。请重新发起调账流程。")
			clearSession(userID)
			return
		}

		session.SetTemp("points_reason", reason)
		session.SetStep("WAITING_CONFIRM_MANAGE_POINTS")

		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **调账二次确认**\n\n目标用户：`%d`\n变动积分：`%+d`\n原因：`%s`\n\n确认执行请回复：`确认调账`\n取消请回复：`取消`",
			tID,
			val,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_MANAGE_POINTS":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认调账" {
			replyText(bot, chatID, "🛑 已取消调账。")
			clearSession(userID)
			return
		}

		tID, err := strconv.ParseInt(session.GetTemp("tgt_uid"), 10, 64)
		if err != nil || tID == 0 {
			replyText(bot, chatID, "❌ 调账会话状态异常，已中止。请重新发起调账流程。")
			clearSession(userID)
			return
		}

		val, err := strconv.Atoi(session.GetTemp("points_delta"))
		if err != nil || val == 0 {
			replyText(bot, chatID, "❌ 调账数值状态异常，已中止。请重新发起调账流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("points_reason")

		var beforePoints int
		var afterPoints int
		var actualDelta int
		var targetName string

		err = DB.Transaction(func(tx *gorm.DB) error {
			todayTotal, err := getTodayAuditDeltaTotalTx(tx, userID, "ADJUST_POINTS")
			if err != nil {
				return err
			}
			if adminAdjustDailyLimitExceeded(todayTotal, val) {
				return fmt.Errorf("%w:%d", errDailyAdjustLimitExceeded, todayTotal)
			}

			var tUser User
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("telegram_id = ?", tID).
				First(&tUser).Error; err != nil {
				return err
			}

			beforePoints = tUser.Points
			targetName = tUser.Username

			afterPoints = beforePoints + val
			if afterPoints < 0 {
				afterPoints = 0
			}

			actualDelta = afterPoints - beforePoints
			if actualDelta == 0 {
				return errAdjustNoEffect
			}

			if err := applyPointDeltaInTx(
				tx,
				tID,
				actualDelta,
				"admin_adjust",
				fmt.Sprintf("管理员调账：%s", formatPlainValue(reason)),
				"admin_adjust",
				fmt.Sprintf("%d", userID),
			); err != nil {
				return err
			}

			return writeAuditLogInTx(
				tx,
				userID,
				"ADJUST_POINTS",
				fmt.Sprintf("%d", tID),
				actualDelta,
				fmt.Sprintf("用户 %s(%d) 积分从 %d 调整为 %d，申请变动 %+d，实际变动 %+d，原因：%s", formatPlainValue(targetName), tID, beforePoints, afterPoints, val, actualDelta, formatPlainValue(reason)),
			)
		})

		if err != nil {
			if errors.Is(err, errDailyAdjustLimitExceeded) {
				replyText(bot, chatID, "❌ 今日调账额度不足，每个超级管理员每日累计最多 `20000` 积分。")
			} else if errors.Is(err, errAdjustNoEffect) {
				replyText(bot, chatID, "❌ 本次调账不会产生实际积分变化。")
			} else {
				replyText(bot, chatID, "❌ 调账失败，目标用户可能不存在。")
			}
			clearSession(userID)
			return
		}
		replyText(bot, chatID, fmt.Sprintf("🛠️ **调账成功！**\n用户 `%d` 积分从 `%d` 变更为 `%d`。\n实际变动：`%+d`。", tID, beforePoints, afterPoints, actualDelta))
		replyText(bot, tID, fmt.Sprintf("🔔 **系统账务通知**\n管理员调整了您的积分。\n\n积分变动：`%+d`\n变动后余额：`%d`\n原因：`%s`", actualDelta, afterPoints, escapeMarkdown(reason)))
		clearSession(userID)

	case "WAITING_REG_USER":
		valid, _ := regexp.MatchString(`^[a-zA-Z0-9_]{3,20}$`, text)
		if !valid {
			replyText(bot, chatID, "❌ 用户名格式不合规！只允许 3-20 位的字母、数字或下划线：")
			return
		}

		session.SetTemp("username", text)

		if session.GetTemp("referral_code") != "" {
			session.SetStep("WAITING_REG_SEC_CODE")
			replyText(bot, chatID, "🛡️ 第二步：请设置安全码(PIN)，至少 6 位。")
			UserSessions.Store(userID, session)
			return
		}

		if AppConfig.InviteRequired {
			session.SetStep("WAITING_REG_INVITE")
			replyText(bot, chatID, "🎫 **第二步：请输入您的邀请码**")
		} else {
			session.SetStep("WAITING_REG_SEC_CODE")
			replyText(bot, chatID, "🛡️ **第二步：请设置安全码(PIN)**")
		}

		UserSessions.Store(userID, session)

	case "WAITING_REG_INVITE":
		inviteHash := hashSensitiveToken(text)
		if inviteHash == "" {
			replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			clearSession(userID)
			return
		}

		var invite InviteCode
		if err := DB.Where("code_hash = ? AND is_used = ?", inviteHash, false).First(&invite).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("注册邀请码预校验读取失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 邀请码暂时读取失败，请稍后重试。")
				return
			}
			replyText(bot, chatID, "❌ 邀请码无效，请重新输入或发送 `取消`：")
			return
		}

		session.SetTemp("invite_hash", inviteHash)
		session.SetTemp("invite_preview", maskSecret(text))
		session.SetStep("WAITING_REG_SEC_CODE")
		replyText(bot, chatID, "🛡️ **第三步：请设置安全码(PIN)**")
		UserSessions.Store(userID, session)

	case "WAITING_TRIAL_FORMAL_INVITE":
		inviteHash := hashSensitiveToken(text)
		if inviteHash == "" {
			replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			clearSession(userID)
			return
		}
		nextExpireAt, err := convertTrialToFormalWithInviteCode(userID, inviteHash)
		if err != nil {
			if errors.Is(err, errInvalidInviteCode) {
				replyText(bot, chatID, "❌ 邀请码无效或已被使用，请重新输入或发送 `取消`。")
				return
			}
			if errors.Is(err, errTrialFormalInviteOnly) {
				replyText(bot, chatID, "⚠️ 当前账号不是新人体验账号，无需转正。")
				clearSession(userID)
				return
			}
			log.Printf("trial formal conversion failed: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 转正失败，请稍后重试。")
			return
		}
		replyText(bot, chatID, fmt.Sprintf("✅ 转正成功。\n\n账号已转为正式用户，普通续期卡现已可用。\n当前到期时间：`%s`", nextExpireAt.Format("2006-01-02")))
		clearSession(userID)

	case "WAITING_REG_SEC_CODE":
		if len(text) < 6 {
			replyText(bot, chatID, "❌ 安全码过短，请至少设置 6 位：")
			return
		}

		secCodeHash := hashSensitiveToken(text)
		if secCodeHash == "" {
			replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			clearSession(userID)
			return
		}

		session.SetTemp("security_code_hash", secCodeHash)
		session.SetStep("WAITING_REG_PASS")
		replyText(bot, chatID, "🔐 **最后一步：请输入您的 ABS 登录密码**\n\n密码只会用于本次开户，不会写入机器人本地会话。")
		UserSessions.Store(userID, session)

	case "WAITING_REG_PASS":
		if len(text) < 6 {
			replyText(bot, chatID, "❌ 密码太短，请至少 6 位：")
			return
		}

		username := session.GetTemp("username")
		password := text
		inviteHash := session.GetTemp("invite_hash")
		referralCode := session.GetTemp("referral_code")
		secCodeHash := session.GetTemp("security_code_hash")

		if username == "" || secCodeHash == "" {
			replyText(bot, chatID, "❌ 注册会话已失效，请重新开始注册。")
			clearSession(userID)
			return
		}

		var reservedInvite InviteCode
		if inviteHash != "" {
			var reserveErr error
			reservedInvite, reserveErr = reserveInviteCodeForRegistrationWithAudit(userID, inviteHash)
			if reserveErr != nil {
				replyText(bot, chatID, "❌ 哎呀手慢了！这个邀请码刚刚已被他人抢先使用。")
				clearSession(userID)
				return
			}
		}

		replyText(bot, chatID, "⏳ 正在连接服务器为您开户...")
		id, err := absClient.RegisterUser(username, password)
		if err != nil {
			var releaseErr error
			if inviteHash != "" {
				releaseErr = releaseInviteCodeReservationWithAudit(userID, inviteHash, "abs_register_failed")
				if releaseErr != nil {
					log.Printf("⚠️ ABS 开户失败后邀请码退回失败: user=%d invite_id=%d err=%s release_err=%s",
						userID, reservedInvite.ID, formatPlainError(err), formatPlainError(releaseErr))
					replyText(bot, chatID, "❌ 开户失败，且邀请码退回失败。系统已记录异常，请联系管理员核查后再重试。")
					clearSession(userID)
					return
				}
			}

			retryHint := "请稍后重试。"
			if inviteHash != "" {
				retryHint = "🔄 邀请码已退回，请稍后重试。"
			}
			replyText(bot, chatID, "❌ 开户失败: "+formatMarkdownError(err)+"\n"+retryHint)
			return
		}

		var expPtr *time.Time
		if AppConfig.AccountValidDays > 0 {
			exp := time.Now().AddDate(0, 0, AppConfig.AccountValidDays)
			expPtr = &exp
		}

		dbErr := DB.Transaction(func(tx *gorm.DB) error {
			if referralCode != "" {
				_, err := createReferralTrialAccountInTx(tx, userID, username, id, secCodeHash, referralCode, time.Now())
				return err
			}

			var existU User
			err := tx.Where("telegram_id = ?", userID).First(&existU).Error

			if err == nil {
				updates := map[string]interface{}{
					"username":      username,
					"abs_user_id":   id,
					"security_code": secCodeHash,
					"status":        "active",
					"is_suspended":  false,
					"account_type":  accountTypeFormal,
				}

				if nextExpireAt, shouldUpdateExpireAt := registrationExpireAtForExistingUser(existU.ExpireAt, expPtr); shouldUpdateExpireAt {
					if nextExpireAt == nil {
						updates["expire_at"] = nil
					} else {
						updates["expire_at"] = nextExpireAt
					}
				}

				userRes := tx.Model(&User{}).
					Where("id = ? AND telegram_id = ?", existU.ID, userID).
					Updates(updates)
				if userRes.Error != nil {
					return userRes.Error
				}
				if userRes.RowsAffected == 0 {
					return fmt.Errorf("REGISTRATION_USER_STATE_CHANGED")
				}
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				user := User{
					TelegramID:   userID,
					Username:     username,
					AbsUserID:    id,
					SecurityCode: secCodeHash,
					ExpireAt:     expPtr,
					Status:       "active",
					IsSuspended:  false,
					AccountType:  accountTypeFormal,
				}
				if err := createRegisteredUserInTx(tx, &user); err != nil {
					return err
				}
			} else {
				return err
			}

			if reservedInvite.ID != 0 {
				return writeAuditLogInTx(
					tx,
					userID,
					"USE_INVITE_CODE",
					fmt.Sprintf("invite_code_id=%d", reservedInvite.ID),
					0,
					fmt.Sprintf("user %s(%d) completed registration with invite code %s abs_user_id=%s",
						formatPlainValue(username), userID, formatPlainValue(reservedInvite.CodePreview), formatPlainValue(id)),
				)
			}
			return nil
		})

		if dbErr != nil {
			rollbackErr := absClient.DeleteUser(id)

			if rollbackErr != nil && !IsAbsNotFoundError(rollbackErr) {
				replyText(bot, chatID, fmt.Sprintf(
					"❌ **注册中止！**\n\nABS 已创建账号，但本地安全档案写入失败：%s\n\n⚠️ 系统尝试回滚 ABS 账号也失败：%s\n请立刻联系管理员处理，避免产生遗孀账号。",
					formatMarkdownError(dbErr),
					formatMarkdownError(rollbackErr),
				))
				return
			}

			if referralCode != "" {
				switch {
				case errors.Is(dbErr, errReferralDailyLimit):
					replyText(bot, chatID, "⚠️ 该邀请链接今日新人体验名额已满，请明天再试。")
				case errors.Is(dbErr, errReferralAlreadyTried):
					replyText(bot, chatID, "⚠️ 您已经领取过新人体验，不能重复领取。")
				case errors.Is(dbErr, errReferralSelfInvite):
					replyText(bot, chatID, "❌ 不能使用自己的邀请链接注册新人体验。")
				case errors.Is(dbErr, errReferralInvalidCode), errors.Is(dbErr, errReferralInviterNotEligible):
					replyText(bot, chatID, "❌ 邀请链接无效、已停用，或邀请者暂不具备邀请资格。")
				default:
					log.Printf("⚠️ 邀请链接注册本地归因失败: user=%d err=%s", userID, formatPlainError(dbErr))
					replyText(bot, chatID, "❌ 新人体验注册失败，本次 ABS 账号已回滚，请稍后重试。")
				}
				clearSession(userID)
				return
			}

			if inviteHash != "" {
				if releaseErr := releaseInviteCodeReservationWithAudit(userID, inviteHash, "local_registration_failed"); releaseErr != nil {
					log.Printf("⚠️ 本地注册失败后邀请码退回失败: user=%d invite_id=%d err=%s",
						userID, reservedInvite.ID, formatPlainError(releaseErr))
					replyText(bot, chatID, fmt.Sprintf(
						"❌ 注册失败：本地安全档案写入失败，系统已回滚 ABS 账号，但邀请码退回失败。\n\n错误详情：%s\n请联系管理员核查后再重试。",
						formatMarkdownError(dbErr),
					))
					return
				}
			}

			retryHint := "请稍后重试。"
			if inviteHash != "" {
				retryHint = "🔄 邀请码已退回，请稍后重试。"
			}
			replyText(bot, chatID, fmt.Sprintf(
				"❌ 注册失败：本地安全档案写入失败，系统已自动回滚 ABS 账号。\n\n错误详情：%s\n%s",
				formatMarkdownError(dbErr),
				retryHint,
			))
			return
		}

		if referralCode != "" {
			replyText(bot, chatID, "🎉 新人体验注册成功。\n\n已获得 `7` 天听书体验权限。体验期内累计听书满 `10` 小时后，发送 `新人任务` 可领取 `7` 天体验延期。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "🎉 注册成功！")

		clearSession(userID)

	case "WAITING_BIND_USER":
		session.SetTemp("username", text)
		session.SetStep("WAITING_BIND_PASS")
		replyText(bot, chatID, "🔐 **请输入密码验权：**")
		UserSessions.Store(userID, session)

	case "WAITING_BIND_PASS":
		username := session.GetTemp("username")
		password := text
		replyText(bot, chatID, "⏳ 正在校验身份...")
		go func() {
			absID, err := absClient.VerifyUser(username, password)
			if err != nil {
				replyText(bot, chatID, "❌ 验证失败: "+formatMarkdownError(err))
				return
			}
			var existingUser User
			existingErr := DB.Where("username = ? AND abs_user_id != ?", username, "").First(&existingUser).Error
			if existingErr == nil {
				session.SetTemp("abs_id", absID)
				session.SetTemp("username", username)
				session.SetStep("WAITING_REBIND_SEC_AUTH")
				replyText(bot, chatID, "🔔 **检测到该资产已被绑定**\n请输入原先设置的 **安全码** 强制迁移：")
				UserSessions.Store(userID, session)
				return
			} else if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 绑定校验后读取既有绑定失败: user=%d username=%s err=%s", userID, formatPlainValue(username), formatPlainError(existingErr))
				replyText(bot, chatID, "❌ 本地绑定状态读取失败，请稍后重试。")
				return
			}
			session.SetTemp("abs_id", absID)
			session.SetTemp("username", username)
			session.SetStep("WAITING_BIND_CREATE_SEC")
			replyText(bot, chatID, "🛡️ 检测到首次接入，**请初始化一个安全码：**")
			UserSessions.Store(userID, session)
		}()

	case "WAITING_REBIND_SEC_AUTH":
		username := session.GetTemp("username")
		absID := session.GetTemp("abs_id")
		var oldUser User

		if err := DB.Where("username = ?", username).First(&oldUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 未找到可迁移的本地档案。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 换绑安全码校验读取旧档案失败: user=%d username=%s err=%s", userID, formatPlainValue(username), formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		if ok, errMsg := verifyUserSecurityCodeWithCooldown(userID, text, oldUser.SecurityCode); !ok {
			replyText(bot, chatID, errMsg)
			return
		}

		if err := rebindLocalUserWithAudit(userID, oldUser.ID, absID); err != nil {
			log.Printf("⚠️ 换绑本地档案或审计写入失败: user=%d target_id=%d abs=%s err=%s", userID, oldUser.ID, formatPlainValue(absID), formatPlainError(err))
			replyText(bot, chatID, "❌ 换绑失败，请稍后重试。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "🎉 **数据安全迁移成功！换绑完成。**\n\n原有效期、积分和资产已原样恢复。")
		clearSession(userID)

	case "WAITING_BIND_CREATE_SEC":
		if len(text) < 6 {
			replyText(bot, chatID, "❌ 安全码过短，请至少设置 6 位：")
			return
		}

		secCodeHash := hashSensitiveToken(text)
		if secCodeHash == "" {
			replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			clearSession(userID)
			return
		}

		absID := session.GetTemp("abs_id")
		username := session.GetTemp("username")

		var expPtr *time.Time
		if AppConfig.AccountValidDays > 0 {
			exp := time.Now().AddDate(0, 0, AppConfig.AccountValidDays)
			expPtr = &exp
		}

		if err := bindLocalUserWithAudit(userID, username, absID, secCodeHash, expPtr); err != nil {
			log.Printf("⚠️ 绑定本地档案或审计写入失败: user=%d username=%s abs=%s err=%s",
				userID, formatPlainValue(username), formatPlainValue(absID), formatPlainError(err))
			replyText(bot, chatID, "❌ 绑定失败，该账号可能已存在本地档案，请尝试走换绑流程。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "🎉 **挂载且安全档案构建成功！资产已同步合并。**")
		clearSession(userID)

	case "WAITING_SAFETY_AUTH":
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 安全码验证读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重新输入安全码。")
			return
		}
		if ok, errMsg := verifyUserSecurityCodeWithCooldown(userID, text, u.SecurityCode); !ok {
			replyText(bot, chatID, errMsg)
			clearSession(userID)
			return
		}
		session.SetStep("WAITING_NEW_PASSWORD")
		replyText(bot, chatID, "🔓 验证通过！**请输入新密码：**")
		UserSessions.Store(userID, session)

	case "WAITING_NEW_PASSWORD":
		if len(text) < 6 {
			replyText(bot, chatID, "❌ 密码太短：")
			return
		}
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 修改密码读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重新输入新密码。")
			return
		}
		if strings.TrimSpace(u.AbsUserID) == "" {
			log.Printf("⚠️ 修改密码缺少 ABS 用户ID: user=%d", userID)
			replyText(bot, chatID, "❌ 本地档案缺少 ABS 账号信息，请重新绑定后再试。")
			clearSession(userID)
			return
		}
		replyText(bot, chatID, "⏳ 正在同步密码...")
		go func() {
			if err := absClient.UpdateAbsPassword(u.AbsUserID, text); err != nil {
				replyText(bot, chatID, "❌ 服务端密码更改失败: "+formatMarkdownError(err))
				return
			}
			replyText(bot, chatID, "✅ **服务端密码已修改！**")
		}()
		clearSession(userID)

	case "WAITING_USERNAME_AUTH":
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 修改用户名安全码校验读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重新输入安全码。")
			clearSession(userID)
			return
		}
		if ok, errMsg := verifyUserSecurityCodeWithCooldown(userID, text, u.SecurityCode); !ok {
			replyText(bot, chatID, errMsg)
			clearSession(userID)
			return
		}
		session.SetStep("WAITING_USERNAME_PASSWORD")
		replyText(bot, chatID, "🔓 安全码验证通过！\n\n🔑 **请输入您当前的登录密码以进一步验证身份：**")
		UserSessions.Store(userID, session)

	case "WAITING_USERNAME_PASSWORD":
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 修改用户名密码校验读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		if strings.TrimSpace(u.AbsUserID) == "" {
			replyText(bot, chatID, "❌ 本地档案缺少 ABS 账号信息，请重新绑定后再试。")
			clearSession(userID)
			return
		}
		replyText(bot, chatID, "⏳ 正在校验密码...")
		if _, err := absClient.VerifyUser(u.Username, text); err != nil {
			log.Printf("⚠️ 修改用户名密码校验失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 密码错误，身份校验未通过。修改用户名已取消。")
			clearSession(userID)
			return
		}
		session.SetStep("WAITING_NEW_USERNAME")
		replyText(bot, chatID, "✅ 密码验证通过！\n\n📝 **请输入新的用户名**\n(⚠️ 仅限 3-20 位字母、数字、下划线)")
		UserSessions.Store(userID, session)

	case "WAITING_NEW_USERNAME":
		newUsername := strings.TrimSpace(text)
		if valid, _ := regexp.MatchString(`^[a-zA-Z0-9_]{3,20}$`, newUsername); !valid {
			replyText(bot, chatID, "❌ 用户名格式不合规！只允许 3-20 位的字母、数字或下划线，请重新输入：")
			return
		}

		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 修改用户名读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重新输入新用户名。")
			return
		}
		if strings.TrimSpace(u.AbsUserID) == "" {
			replyText(bot, chatID, "❌ 本地档案缺少 ABS 账号信息，请重新绑定后再试。")
			clearSession(userID)
			return
		}
		oldUsername := u.Username
		if newUsername == oldUsername {
			replyText(bot, chatID, "⚠️ 新用户名与当前用户名相同，请输入一个不同的用户名：")
			return
		}

		// 改名前先做一次本地占用预检，便于快速失败、避免无谓的 ABS 调用。
		var conflictCount int64
		if err := DB.Model(&User{}).
			Where("username = ? AND telegram_id <> ?", newUsername, userID).
			Count(&conflictCount).Error; err != nil {
			log.Printf("⚠️ 修改用户名占用预检失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 用户名校验失败，请稍后重试。")
			return
		}
		if conflictCount > 0 {
			replyText(bot, chatID, "❌ 该用户名已被占用，请换一个再试：")
			return
		}

		replyText(bot, chatID, "⏳ 正在同步用户名到服务端...")

		if err := absClient.UpdateAbsUsername(u.AbsUserID, newUsername); err != nil {
			log.Printf("⚠️ 修改用户名服务端写入失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 服务端用户名修改失败: "+formatMarkdownError(err))
			clearSession(userID)
			return
		}

		if err := renameLocalUsernameWithAudit(userID, oldUsername, newUsername, u.AbsUserID); err != nil {
			// 本地写入失败：尝试把 ABS 用户名回滚到旧值，避免本地与服务端不一致。
			rollbackErr := absClient.UpdateAbsUsername(u.AbsUserID, oldUsername)
			if rollbackErr != nil {
				log.Printf("❌ 修改用户名本地写入失败且 ABS 回滚失败: user=%d new=%s err=%s rollback_err=%s",
					userID, formatPlainValue(newUsername), formatPlainError(err), formatPlainError(rollbackErr))
				replyText(bot, chatID, fmt.Sprintf(
					"❌ **修改中止！**\n\nABS 已改名，但本地档案写入失败：%s\n\n⚠️ 系统尝试回滚 ABS 用户名也失败：%s\n请立刻联系管理员处理。",
					formatMarkdownError(err),
					formatMarkdownError(rollbackErr),
				))
				clearSession(userID)
				return
			}

			if errors.Is(err, errUsernameTaken) {
				replyText(bot, chatID, "❌ 该用户名刚刚已被他人抢先占用，本次修改已回滚，请换一个再试。")
			} else if errors.Is(err, errUsernameUnchanged) {
				replyText(bot, chatID, "⚠️ 新用户名与当前用户名相同，本次未做修改。")
			} else {
				log.Printf("⚠️ 修改用户名本地写入失败（ABS 已回滚）: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 本地档案写入失败，本次修改已回滚，请稍后重试。")
			}
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("✅ **用户名修改成功！**\n\n新用户名：`%s`\n下次登录有声书请使用新用户名。", escapeMarkdown(newUsername)))
		clearSession(userID)

	case "WAITING_DELETE_AUTH":
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "⚠️ 未检测到有效账户。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 注销安全码校验读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		if ok, errMsg := verifyUserSecurityCodeWithCooldown(userID, text, u.SecurityCode); !ok {
			replyText(bot, chatID, errMsg)
			clearSession(userID)
			return
		}

		session.SetStep("WAITING_CONFIRM_DELETE")
		replyText(bot, chatID, "⚠️ **最终确认**：此操作不可逆，将永久删除 ABS 账号和本地档案。\n\n确认请回复：`确认注销`")
		UserSessions.Store(userID, session)

	case "WAITING_UNBIND_AUTH":
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "⚠️ 未检测到有效账户。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 解绑安全码校验读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		if ok, errMsg := verifyUserSecurityCodeWithCooldown(userID, text, u.SecurityCode); !ok {
			replyText(bot, chatID, errMsg)
			clearSession(userID)
			return
		}

		if err := unbindLocalUserWithAudit(userID, u.AbsUserID); err != nil {
			log.Printf("⚠️ 解绑本地档案或审计写入失败: user=%d abs=%s err=%s", userID, formatPlainValue(u.AbsUserID), formatPlainError(err))
			replyText(bot, chatID, "❌ 解绑失败，请稍后重试。")
			clearSession(userID)
			return
		}

		sendUserMainMenu(bot, chatID, "🔄 **本地安全解除挂载成功！**\n\n您的资产档案已冻结保留，重新绑定时不会重新赠送有效期。")
		clearSession(userID)

	case "WAITING_RENEW_CODE":
		if AppConfig.NoticeGroupID != 0 && !isUserInGroupFresh(bot, userID, AppConfig.NoticeGroupID) {
			replyText(bot, chatID, "🚫 检测到您当前不在指定群组内，无法使用续期卡。")
			clearSession(userID)
			return
		}

		var days int
		var newExpireAt time.Time
		var absUserID string
		var needReactivate bool

		err := DB.Transaction(func(tx *gorm.DB) error {
			renewHash := hashSensitiveToken(text)
			if renewHash == "" {
				return errSecurityPepperNotConfigured
			}

			var rCode RenewCode
			if err := tx.Where("code_hash = ? AND is_used = ?", renewHash, false).First(&rCode).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errInvalidRenewCode
				}
				return err
			}

			var u User
			if err := tx.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errUserNotFound
				}
				return err
			}
			if isTrialAccount(u) {
				return errTrialCannotUseRenewCode
			}

			res := tx.Model(&RenewCode{}).
				Where("id = ? AND is_used = ?", rCode.ID, false).
				Updates(map[string]interface{}{
					"is_used":    true,
					"used_by_id": userID,
				})

			if res.Error != nil {
				return res.Error
			}

			if res.RowsAffected == 0 {
				return errInvalidRenewCode
			}

			now := time.Now()
			if u.ExpireAt == nil || u.ExpireAt.Before(now) {
				newExpireAt = now.AddDate(0, 0, rCode.Days)
			} else {
				newExpireAt = u.ExpireAt.AddDate(0, 0, rCode.Days)
			}

			userRes := tx.Model(&User{}).
				Where("id = ? AND telegram_id = ? AND account_type <> ?", u.ID, userID, accountTypeTrial).
				Update("expire_at", newExpireAt)
			if userRes.Error != nil {
				return userRes.Error
			}
			if userRes.RowsAffected == 0 {
				return fmt.Errorf("RENEW_USER_STATE_CHANGED")
			}

			if err := writeAuditLogInTx(
				tx,
				userID,
				"USE_RENEW_CODE",
				fmt.Sprintf("renew_code_id=%d", rCode.ID),
				0,
				fmt.Sprintf("user %s(%d) used renew code %s for %d days; expire_at=%s; need_reactivate=%t",
					formatPlainValue(u.Username), userID, formatPlainValue(rCode.CodePreview), rCode.Days, newExpireAt.Format(time.RFC3339), u.IsSuspended && u.AbsUserID != ""),
			); err != nil {
				return err
			}

			days = rCode.Days
			absUserID = u.AbsUserID
			needReactivate = u.IsSuspended && u.AbsUserID != ""

			return nil
		})

		if err != nil {
			switch renewRedeemErrorCode(err) {
			case "INVALID_RENEW_CODE":
				replyText(bot, chatID, "❌ 卡密无效或已被消费。")
			case "USER_NOT_FOUND":
				replyText(bot, chatID, "⚠️ 未检测到有效账户。")
			case "TRIAL_CANNOT_USE_RENEW_CODE":
				replyText(bot, chatID, "⚠️ 当前为新人体验账号，仅支持新人体验延期。普通续期卡需使用正式邀请码转正后才能使用。")
			case "SECURITY_PEPPER_NOT_CONFIGURED":
				replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			default:
				replyText(bot, chatID, "❌ 续期失败，请稍后重试。")
			}
			return
		}

		if needReactivate {
			if err := absClient.SetUserActiveStatus(absUserID, true); err != nil {
				log.Printf("⚠️ 续期后 ABS 解封失败: user=%d abs=%s err=%s", userID, formatPlainValue(absUserID), formatPlainError(err))
				auditErr := writeAuditLogInTx(DB, userID, "RENEW_REACTIVATE_USER_FAILED", fmt.Sprintf("%d", userID), 0,
					fmt.Sprintf("renew card extended account but ABS reactivation failed: tg=%d abs_user_id=%s expire_at=%s days=%d error=%s",
						userID, formatPlainValue(absUserID), newExpireAt.Format(time.RFC3339), days, formatPlainError(err)))
				if auditErr != nil {
					log.Printf("⚠️ 续期 ABS 解封失败审计写入失败: user=%d abs=%s err=%s", userID, formatPlainValue(absUserID), formatPlainError(auditErr))
					notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 用户续期已到账，但 ABS 恢复失败，且失败审计写入失败。\n用户：%d\nABS：%s\n到期：%s\n天数：%d\nABS错误：%s\n审计错误：%s", userID, formatPlainValue(absUserID), newExpireAt.Format(time.RFC3339), days, formatPlainError(err), formatPlainError(auditErr)))
				}

				replyText(bot, chatID, fmt.Sprintf(
					"⚠️ 续期已到账，新的到期时间为 `%s`。\n\nABS 解封暂时失败，系统已记录异常，请联系管理员处理。",
					newExpireAt.Format("2006-01-02"),
				))
				clearSession(userID)
				return
			}

			if err := applyRenewReactivateLocalStatusWithAudit(userID, absUserID, newExpireAt, days); err != nil {
				log.Printf("⚠️ ABS 已解封，但本地解除封禁状态或审计写入失败: user=%d abs=%s err=%s", userID, formatPlainValue(absUserID), formatPlainError(err))
				auditErr := writeAuditLogInTx(DB, userID, "RENEW_REACTIVATE_USER_LOCAL_FAILED", fmt.Sprintf("%d", userID), 0,
					fmt.Sprintf("renew card reactivated ABS but local state/audit failed: tg=%d abs_user_id=%s expire_at=%s days=%d error=%s",
						userID, formatPlainValue(absUserID), newExpireAt.Format(time.RFC3339), days, formatPlainError(err)))
				if auditErr != nil {
					log.Printf("⚠️ 续期 ABS 已解封，但本地失败审计写入失败: user=%d abs=%s err=%s", userID, formatPlainValue(absUserID), formatPlainError(auditErr))
					notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 用户续期已到账且 ABS 已恢复，但本地权限状态或成功审计失败，且本地失败审计写入失败。\n用户：%d\nABS：%s\n到期：%s\n天数：%d\n本地错误：%s\n审计错误：%s\n请立即人工核查。", userID, formatPlainValue(absUserID), newExpireAt.Format(time.RFC3339), days, formatPlainError(err), formatPlainError(auditErr)))
					replyText(bot, chatID, fmt.Sprintf(
						"⚠️ 续期已到账，新的到期时间为 `%s`。\n\nABS 已恢复，但本地权限状态和失败审计写入均异常，已通知管理员人工核查。",
						newExpireAt.Format("2006-01-02"),
					))
				} else {
					replyText(bot, chatID, fmt.Sprintf(
						"⚠️ 续期已到账，新的到期时间为 `%s`。\n\nABS 已恢复，但本地权限状态或审计写入失败，请联系管理员处理。",
						newExpireAt.Format("2006-01-02"),
					))
				}
				clearSession(userID)
				return
			}
		}

		replyText(bot, chatID, fmt.Sprintf(
			"🎉 续费成功！延长 `%d` 天。\n📅 新到期时间：`%s`",
			days,
			newExpireAt.Format("2006-01-02"),
		))
		clearSession(userID)

	case "WAITING_CONFIRM_DELETE":
		if text == "确认注销" {
			if isSuperAdmin(userID) {
				replyText(bot, chatID, "❌ 超级管理员账号禁止通过自助注销物理删除。请先完成权限交接并移出超级管理员名单后再处理。")
				clearSession(userID)
				return
			}

			var u User
			userErr := DB.Where("telegram_id = ?", userID).First(&u).Error
			if userErr == nil {
				if u.AbsUserID != "" {
					if err := absClient.DeleteUser(u.AbsUserID); err != nil && !IsAbsNotFoundError(err) {
						replyText(bot, chatID, fmt.Sprintf(
							"❌ **注销中止！**\n\nABS 服务端删除失败：%s\n\n为了避免服务端账号残留，本地档案暂时保留。请稍后重试或联系管理员。",
							formatMarkdownError(err),
						))
						clearSession(userID)
						return
					}
				}

				if err := deleteLocalUserWithAudit(userID, userID, u.AbsUserID, "SELF_DELETE_USER", func(deleted User) string {
					return fmt.Sprintf("用户自助注销并物理删除本地档案：username=%s tg=%d abs_user_id=%s", formatPlainValue(deleted.Username), userID, formatPlainValue(deleted.AbsUserID))
				}); err != nil {
					log.Printf("⚠️ 用户自助注销本地档案或审计写入失败: user=%d abs=%s err=%s", userID, formatPlainValue(u.AbsUserID), formatPlainError(err))
					replyText(bot, chatID, "⚠️ ABS 账号已删除，但本地档案或审计写入失败，请立即联系管理员人工核查。")
					clearSession(userID)
					return
				}
				replyText(bot, chatID, "🗑 账户和本地安全档案已连根抹除。")
			} else if errors.Is(userErr, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "⚠️ 未找到本地账户档案，注销无需执行。")
			} else {
				log.Printf("⚠️ 自助注销读取本地档案失败: user=%d err=%s", userID, formatPlainError(userErr))
				replyText(bot, chatID, "❌ 本地档案读取失败，注销未执行，请稍后重试。")
			}
		} else {
			replyText(bot, chatID, "注销中止。")
		}
		clearSession(userID)

	case "WAITING_GEN_INVITE_COUNT":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		count, err := strconv.Atoi(text)
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 请输入有效数量，范围 1-100：")
			return
		}

		session.SetTemp("invite_count", strconv.Itoa(count))
		session.SetStep("WAITING_GEN_INVITE_REASON")
		replyText(bot, chatID, "📝 请输入本次批量生成邀请码的原因，"+adminReasonRequirementText+"：")
		UserSessions.Store(userID, session)

	case "WAITING_GEN_INVITE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		count, err := strconv.Atoi(session.GetTemp("invite_count"))
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 邀请码生成数量状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}
		session.SetTemp("invite_reason", reason)
		session.SetStep("WAITING_CONFIRM_GEN_INVITE")

		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **批量生成邀请码二次确认**\n\n数量：`%d`\n原因：`%s`\n\n确认生成请回复：`确认生成邀请码`\n取消请回复：`取消`",
			count,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_GEN_INVITE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认生成邀请码" {
			replyText(bot, chatID, "🛑 已取消生成邀请码。")
			clearSession(userID)
			return
		}

		count, err := strconv.Atoi(session.GetTemp("invite_count"))
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 邀请码生成数量状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("invite_reason")

		res := "✅ **成功生成邀请码：**\n\n"
		codes, err := generateInviteCodesWithAudit(userID, count, reason)
		if err != nil {
			log.Printf("⚠️ 批量生成邀请码失败: actor=%d count=%d err=%s", userID, count, formatPlainError(err))
			replyText(bot, chatID, "❌ 邀请码生成失败，未创建任何新卡密，请稍后重试。")
			clearSession(userID)
			return
		}
		for _, c := range codes {
			res += fmt.Sprintf("`%s`\n", c)
		}

		replyText(bot, chatID, res)
		clearSession(userID)

	case "WAITING_GEN_RENEW_DAYS":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		days, err := strconv.Atoi(text)
		if err != nil || days <= 0 || days > 365 {
			replyText(bot, chatID, "❌ 天数输入错误，允许范围 1-365：")
			return
		}

		session.SetTemp("days", strconv.Itoa(days))
		session.SetStep("WAITING_GEN_RENEW_COUNT")
		replyText(bot, chatID, fmt.Sprintf("🔢 确认面额为 `%d` 天。请输入生成的卡密张数，范围 1-100：", days))
		UserSessions.Store(userID, session)

	case "WAITING_GEN_RENEW_COUNT":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		count, err := strconv.Atoi(text)
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 张数限制 1-100：")
			return
		}

		session.SetTemp("renew_count", strconv.Itoa(count))
		session.SetStep("WAITING_GEN_RENEW_REASON")
		replyText(bot, chatID, "📝 请输入本次批量生成续期卡的原因，"+adminReasonRequirementText+"：")
		UserSessions.Store(userID, session)

	case "WAITING_GEN_RENEW_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		days, err := strconv.Atoi(session.GetTemp("days"))
		if err != nil || days <= 0 || days > 365 {
			replyText(bot, chatID, "❌ 续期卡天数状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}
		count, err := strconv.Atoi(session.GetTemp("renew_count"))
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 续期卡生成数量状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}

		session.SetTemp("renew_reason", reason)
		session.SetStep("WAITING_CONFIRM_GEN_RENEW")

		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **批量生成续期卡二次确认**\n\n面额：`%d` 天\n数量：`%d` 张\n原因：`%s`\n\n确认生成请回复：`确认生成续期卡`\n取消请回复：`取消`",
			days,
			count,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_GEN_RENEW":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认生成续期卡" {
			replyText(bot, chatID, "🛑 已取消生成续期卡。")
			clearSession(userID)
			return
		}

		days, err := strconv.Atoi(session.GetTemp("days"))
		if err != nil || days <= 0 || days > 365 {
			replyText(bot, chatID, "❌ 续期卡天数状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}
		count, err := strconv.Atoi(session.GetTemp("renew_count"))
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 续期卡生成数量状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("renew_reason")

		res := fmt.Sprintf("✅ **成功生成 %d 天续期卡密：**\n\n", days)
		codes, err := generateRenewCodesWithAudit(userID, days, count, reason)
		if err != nil {
			log.Printf("⚠️ 批量生成续期卡失败: actor=%d days=%d count=%d err=%s", userID, days, count, formatPlainError(err))
			replyText(bot, chatID, "❌ 续期卡生成失败，未创建任何新卡密，请稍后重试。")
			clearSession(userID)
			return
		}
		for _, c := range codes {
			res += fmt.Sprintf("`%s`\n", c)
		}

		replyText(bot, chatID, res)
		clearSession(userID)

	case "WAITING_SIMULATE_EXPIRE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "❌ 格式错误，请输入纯数字 TG ID。")
			return
		}
		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止将自己强制设为过期。")
			clearSession(userID)
			return
		}
		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户是超级管理员，禁止模拟过期。")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 模拟过期目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		session.SetTemp("simulate_expire_tgt_uid", strconv.FormatInt(tgtID, 10))
		session.SetTemp("simulate_expire_tgt_username", tUser.Username)
		session.SetStep("WAITING_SIMULATE_EXPIRE_REASON")
		replyText(bot, chatID, fmt.Sprintf("📝 即将将用户 `%s` / `%d` 强制设为已过期。\n请输入操作原因，%s：", escapeMarkdown(tUser.Username), tgtID, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_SIMULATE_EXPIRE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tgtID := session.GetTemp("simulate_expire_tgt_uid")
		username := session.GetTemp("simulate_expire_tgt_username")
		session.SetTemp("simulate_expire_reason", reason)
		session.SetStep("WAITING_CONFIRM_SIMULATE_EXPIRE")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **模拟过期二次确认**\n\n目标用户：`%s`\nTG ID：`%s`\n原因：`%s`\n\n此操作会把用户到期时间改为昨天，并解除本地封禁状态，后续生命周期巡检会按过期账号处理。\n确认执行请回复：`确认模拟过期`\n取消请回复：`取消`",
			escapeMarkdown(username),
			tgtID,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SIMULATE_EXPIRE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认模拟过期" {
			replyText(bot, chatID, "🛑 已取消模拟过期。")
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(session.GetTemp("simulate_expire_tgt_uid"), 10, 64)
		if err != nil || tgtID == 0 {
			replyText(bot, chatID, "❌ 模拟过期会话状态异常，已中止。请重新发起流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("simulate_expire_reason")

		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止将自己强制设为过期。")
			clearSession(userID)
			return
		}
		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		status, _, err := simulateExpireWithAudit(userID, tgtID, reason)
		if err != nil {
			log.Printf("⚠️ 模拟过期失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 模拟过期写入失败，请稍后重试。")
			clearSession(userID)
			return
		}
		switch status {
		case adminMutationSelf:
			replyText(bot, chatID, "❌ 禁止将自己强制设为过期。")
			clearSession(userID)
			return
		case adminMutationNotFound:
			replyText(bot, chatID, "❌ 查无此人。")
			clearSession(userID)
			return
		case adminMutationTargetSuperAdmin:
			replyText(bot, chatID, "❌ 目标用户是超级管理员，操作中止。")
			clearSession(userID)
			return
		case adminMutationTargetStateChanged:
			replyText(bot, chatID, "⚠️ 目标用户状态已变化，请重新发起模拟过期流程。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("⏱️ 用户 `%d` 已被设置为过期。", tgtID))
		clearSession(userID)

	case "WAITING_CLEAN_WIDOWS_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		idsStr := session.GetTemp("widow_ids")
		ids := strings.Split(idsStr, ",")

		session.SetTemp("widow_reason", reason)
		session.SetStep("WAITING_CONFIRM_CLEAN_WIDOWS")

		replyText(bot, chatID, fmt.Sprintf(
			"🚨 **清理遗孀二次确认**\n\n待清理数量：`%d`\n原因：`%s`\n\n此操作会硬删除 ABS 服务端账号，不可逆。\n确认执行请回复：`确认清理`\n取消请回复：`取消`",
			len(ids),
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_CLEAN_WIDOWS":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text == "确认清理" {
			processingMsg, sendErr := sendAutoDelete(bot, tgbotapi.NewMessage(chatID, "💥 正在执行物理清除，请勿操作机器人...\n由于账号可能较多，这可能需要几分钟时间。"))
			if sendErr != nil {
				log.Printf("发送遗孀清理进度消息失败: chat=%d err=%s", chatID, formatTelegramSendError(sendErr))
			}

			idsStr := session.GetTemp("widow_ids")
			reason := session.GetTemp("widow_reason")
			ids := strings.Split(idsStr, ",")

			go func(targetIDs []string, msgID int, reason string) {
				successCount := 0
				failCount := 0

				for _, id := range targetIDs {
					if id != "" {
						if err := absClient.DeleteUser(id); err == nil || IsAbsNotFoundError(err) {
							successCount++
						} else {
							failCount++
						}
						time.Sleep(150 * time.Millisecond)
					}
				}

				auditErr := writeAuditLogInTx(DB, userID, "CLEAN_WIDOWS", "ABS", 0, fmt.Sprintf("清理遗孀账号，目标 %d 个，成功 %d 个，失败 %d 个，原因：%s", len(targetIDs), successCount, failCount, formatPlainValue(reason)))
				if auditErr != nil {
					log.Printf("⚠️ 遗孀清理审计写入失败: actor=%d targets=%d success=%d fail=%d err=%s", userID, len(targetIDs), successCount, failCount, formatPlainError(auditErr))
					notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 遗孀清理已执行，但 CLEAN_WIDOWS 审计写入失败。\n执行人：%d\n目标：%d\n成功：%d\n失败：%d\n错误：%s\n请立即人工核查。", userID, len(targetIDs), successCount, failCount, formatPlainError(auditErr)))
				}

				finalText := fmt.Sprintf("✅ **大清洗完成！**\n成功抹除了 `%d` 个遗孀账号。\n失败：`%d` 个。", successCount, failCount)
				if auditErr != nil {
					finalText += "\n\n⚠️ 审计写入失败，已通知超级管理员人工核查。"
				}
				editMsg := tgbotapi.NewEditMessageText(chatID, msgID, finalText)
				editMsg.ParseMode = "Markdown"
				if _, err := bot.Request(editMsg); err != nil {
					log.Printf("编辑遗孀清理进度消息失败: chat=%d message=%d err=%s", chatID, msgID, formatTelegramSendError(err))
				}
			}(ids, processingMsg.MessageID, reason)

		} else {
			replyText(bot, chatID, "🛑 已中止清理任务，遗孀们松了一口气。")
		}

		clearSession(userID)

	case "WAITING_QUERY_USER":
		var targetUser User
		foundUser := false

		cleanQuery := strings.TrimSpace(strings.TrimPrefix(text, "@"))

		if tgID, parseErr := strconv.ParseInt(cleanQuery, 10, 64); parseErr == nil {
			err := DB.Where("telegram_id = ?", tgID).First(&targetUser).Error
			if err == nil {
				foundUser = true
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				err = DB.Where("username = ?", cleanQuery).First(&targetUser).Error
				if err == nil {
					foundUser = true
				} else if errors.Is(err, gorm.ErrRecordNotFound) {
					foundUser = false
				} else {
					log.Printf("⚠️ 查询用户用户名回退读取失败: actor=%d query=%s err=%s", userID, formatPlainValue(cleanQuery), formatPlainError(err))
					replyText(bot, chatID, "❌ 用户档案读取失败，请稍后重试。")
					clearSession(userID)
					return
				}
			} else {
				log.Printf("⚠️ 查询用户 TG ID 读取失败: actor=%d query=%s err=%s", userID, formatPlainValue(cleanQuery), formatPlainError(err))
				replyText(bot, chatID, "❌ 用户档案读取失败，请稍后重试。")
				clearSession(userID)
				return
			}
		} else {
			err := DB.Where("username = ?", cleanQuery).First(&targetUser).Error
			if err == nil {
				foundUser = true
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				foundUser = false
			} else {
				log.Printf("⚠️ 查询用户用户名读取失败: actor=%d query=%s err=%s", userID, formatPlainValue(cleanQuery), formatPlainError(err))
				replyText(bot, chatID, "❌ 用户档案读取失败，请稍后重试。")
				clearSession(userID)
				return
			}
		}

		if !foundUser {
			replyText(bot, chatID, "❌ 数据库中未查找到该用户。")
			clearSession(userID)
			return
		}

		status := resolveUserAccountStatusDisplay(targetUser, time.Now(), accountStatusDisplayAdmin, true).Text

		expText := "永久有效"
		if targetUser.IsWhitelist {
			expText = "🏳️ 白名单 (永久免保号清理)"
		} else if targetUser.ExpireAt != nil {
			expText = targetUser.ExpireAt.Format("2006-01-02 15:04:05")
		}

		realRole := getUserRole(targetUser.TelegramID)
		roleDisplay := "👤 普通用户"
		if realRole == "super_admin" {
			roleDisplay = "👑 超级管理员"
		} else if realRole == "admin" {
			roleDisplay = "🛠️ 管理员"
		}

		targetCul := GetOrCreateCultivation(targetUser.TelegramID)
		targetRealm := GetRealmName(targetCul)
		targetCultivationHoursText := "`读取失败`"
		targetTribulationFailsText := "`读取失败`"
		if targetCul != nil {
			targetCultivationHoursText = fmt.Sprintf("`%.1f`", targetCul.TotalAudioTime)
			targetTribulationFailsText = fmt.Sprintf("%d", targetCul.TribulationFails)
		} else {
			targetRealm = "`读取失败`"
		}

		info := fmt.Sprintf("📊 **用户档案查询结果**\n\n"+
			"👤 **名称 (TG/ABS)**: `%s`\n"+
			"🆔 **TG 绑定 ID**: `%d`\n"+
			"🔑 **ABS 库 ID**: `%s`\n"+
			"🪪 **当前积分**: `%d`\n"+
			"🎖️ **系统角色**: %s\n"+
			"⏳ **到期时间**: %s\n"+
			"🛡️ **当前状态**: %s\n"+
			"──────────────\n"+
			"📿 **修仙境界**: %s\n"+
			"⏱ **闭关时长**: %s 小时 (失败: %s)",
			escapeMarkdown(targetUser.Username), targetUser.TelegramID, escapeMarkdown(targetUser.AbsUserID),
			targetUser.Points, roleDisplay, expText, status,
			targetRealm, targetCultivationHoursText, targetTribulationFailsText)

		writeAuditLog(
			userID,
			"QUERY_USER_PROFILE",
			fmt.Sprintf("%d", targetUser.TelegramID),
			fmt.Sprintf("管理员查询用户档案：username=%s abs_user_id=%s", formatPlainValue(targetUser.Username), formatPlainValue(targetUser.AbsUserID)),
		)
		replyText(bot, chatID, info)
		clearSession(userID)

	case "WAITING_SUSPEND_USER":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "❌ 格式错误，请输入纯数字 TG ID：")
			return
		}

		if tgtID == userID {
			replyText(bot, chatID, "❌ 警告：系统禁止对自己执行封禁操作！")
			clearSession(userID)
			return
		}

		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 警告：免死金牌生效，无法对超级管理员执行封禁操作！")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 本地数据库查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 封禁入口目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		if tUser.AbsUserID == "" {
			replyText(bot, chatID, "⚠️ 该用户为幽灵账户（未绑定 ABS），无需封禁。")
			clearSession(userID)
			return
		}

		newSuspendStatus := !tUser.IsSuspended
		actionText := "封禁/暂停"
		confirmText := "确认封禁"
		if !newSuspendStatus {
			actionText = "解封/恢复"
			confirmText = "确认解封"
		}

		session.SetTemp("suspend_tgt_uid", strconv.FormatInt(tgtID, 10))
		session.SetTemp("suspend_tgt_username", tUser.Username)
		session.SetTemp("suspend_new_status", fmt.Sprintf("%t", newSuspendStatus))
		session.SetTemp("suspend_confirm_text", confirmText)
		session.SetTemp("suspend_action_text", actionText)
		session.SetStep("WAITING_SUSPEND_REASON")

		replyText(bot, chatID, fmt.Sprintf("📝 即将对用户 `%d` 执行【%s】。\n请输入操作原因，%s：", tgtID, actionText, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_SUSPEND_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tgtID := session.GetTemp("suspend_tgt_uid")
		username := session.GetTemp("suspend_tgt_username")
		actionText := session.GetTemp("suspend_action_text")
		confirmText := session.GetTemp("suspend_confirm_text")

		session.SetTemp("suspend_reason", reason)
		session.SetStep("WAITING_CONFIRM_SUSPEND_USER")

		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **封禁/解封二次确认**\n\n目标用户：`%s`\nTG ID：`%s`\n操作：`%s`\n原因：`%s`\n\n确认执行请回复：`%s`\n取消请回复：`取消`",
			escapeMarkdown(username),
			tgtID,
			escapeMarkdown(actionText),
			escapeMarkdown(reason),
			confirmText,
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SUSPEND_USER":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		confirmText := session.GetTemp("suspend_confirm_text")
		if text != confirmText {
			replyText(bot, chatID, "🛑 已取消封禁/解封操作。")
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(session.GetTemp("suspend_tgt_uid"), 10, 64)
		if err != nil || tgtID == 0 {
			replyText(bot, chatID, "❌ 封禁/解封会话状态异常，已中止。请重新发起流程。")
			clearSession(userID)
			return
		}
		reason, ok := validateAdminReason(session.GetTemp("suspend_reason"))
		if !ok {
			replyText(bot, chatID, "❌ 封禁/解封原因异常，已中止。请重新发起流程。")
			clearSession(userID)
			return
		}
		newSuspendRaw := session.GetTemp("suspend_new_status")
		if newSuspendRaw != "true" && newSuspendRaw != "false" {
			replyText(bot, chatID, "❌ 封禁/解封目标状态异常，已中止。请重新发起流程。")
			clearSession(userID)
			return
		}
		newSuspendStatus := newSuspendRaw == "true"

		if tgtID == userID {
			replyText(bot, chatID, "❌ 警告：系统禁止对自己执行封禁操作！")
			clearSession(userID)
			return
		}
		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 本地数据库查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 封禁确认目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		if tUser.Role == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已经是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		if tUser.AbsUserID == "" {
			replyText(bot, chatID, "⚠️ 该用户为幽灵账户（未绑定 ABS），无法同步服务端封禁状态。")
			clearSession(userID)
			return
		}

		actionText := "封禁/暂停"
		auditAction := "SUSPEND_USER"
		if !newSuspendStatus {
			actionText = "解封/恢复"
			auditAction = "UNSUSPEND_USER"
		}

		apiErr := absClient.SetUserActiveStatus(tUser.AbsUserID, !newSuspendStatus)
		if apiErr != nil {
			auditErr := writeAuditLogInTx(DB, userID, auditAction+"_FAILED", fmt.Sprintf("%d", tgtID), 0, fmt.Sprintf("用户 %s(%d) 执行%s时 ABS 服务端状态更新失败，原因：%s，错误：%s", formatPlainValue(tUser.Username), tgtID, actionText, formatPlainValue(reason), formatPlainError(apiErr)))
			if auditErr != nil {
				log.Printf("⚠️ ABS 状态更新失败审计写入失败: actor=%d target=%d action=%s err=%s", userID, tgtID, formatPlainValue(auditAction+"_FAILED"), formatPlainError(auditErr))
				notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ %s 失败，且失败审计写入失败。\n执行人：%d\n目标：%d\nABS错误：%s\n审计错误：%s", actionText, userID, tgtID, formatPlainError(apiErr), formatPlainError(auditErr)))
			}
			replyText(bot, chatID, fmt.Sprintf("❌ ABS 服务端状态更新失败: %s", formatMarkdownError(apiErr)))
			clearSession(userID)
			return
		}

		if err := applySuspendLocalStatusWithAudit(userID, tgtID, tUser.AbsUserID, newSuspendStatus, auditAction, reason); err != nil {
			log.Printf("⚠️ ABS 状态已更新，但本地封禁状态或审计写入失败: user=%d err=%s", tgtID, formatPlainError(err))
			auditErr := writeAuditLogInTx(DB, userID, auditAction+"_LOCAL_FAILED", fmt.Sprintf("%d", tgtID), 0, fmt.Sprintf("用户 %s(%d) 执行%s时 ABS 已更新但本地状态或审计写入失败，原因：%s，错误：%s", formatPlainValue(tUser.Username), tgtID, actionText, formatPlainValue(reason), formatPlainError(err)))
			if auditErr != nil {
				log.Printf("⚠️ ABS 状态已更新，但本地失败审计写入失败: actor=%d target=%d action=%s err=%s", userID, tgtID, formatPlainValue(auditAction+"_LOCAL_FAILED"), formatPlainError(auditErr))
				notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ ABS 已执行 %s，但本地状态或成功审计失败，且本地失败审计写入失败。\n执行人：%d\n目标：%d\n本地错误：%s\n审计错误：%s\n请立即人工核查。", actionText, userID, tgtID, formatPlainError(err), formatPlainError(auditErr)))
				replyText(bot, chatID, "⚠️ ABS 状态已更新，但本地状态和失败审计写入均异常，已通知超级管理员人工核查。")
			} else {
				replyText(bot, chatID, "⚠️ ABS 状态已更新，但本地状态或审计写入失败，请管理员手动核查。")
			}
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("✅ **%s 成功！** 用户 `%d` 的服务端权限已同步更新。", actionText, tgtID))

		clearSession(userID)

	case "WAITING_FORCE_DELETE_USER":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "❌ 格式错误，请输入纯数字 TG ID：")
			return
		}

		if tgtID == userID {
			replyText(bot, chatID, "❌ 警告：系统禁止执行物理自毁操作！")
			clearSession(userID)
			return
		}

		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 警告：免死金牌生效，无法抹除超级管理员！")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 本地数据库查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 物理删号目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		if tUser.Role == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已经是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		session.SetTemp("delete_tgt_uid", strconv.FormatInt(tgtID, 10))
		session.SetTemp("delete_tgt_username", tUser.Username)
		session.SetStep("WAITING_FORCE_DELETE_REASON")

		replyText(bot, chatID, fmt.Sprintf("📝 即将物理删除用户 `%s` / `%d`。\n请输入删除原因，%s：", escapeMarkdown(tUser.Username), tgtID, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_FORCE_DELETE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tgtID := session.GetTemp("delete_tgt_uid")
		username := session.GetTemp("delete_tgt_username")

		session.SetTemp("delete_reason", reason)
		session.SetStep("WAITING_CONFIRM_FORCE_DELETE")

		replyText(bot, chatID, fmt.Sprintf(
			"🚨 **物理删号二次确认**\n\n目标用户：`%s`\nTG ID：`%s`\n原因：`%s`\n\n此操作将删除 ABS 账号和本地资产，不可逆。\n确认执行请回复：`确认删除`\n取消请回复：`取消`",
			escapeMarkdown(username),
			tgtID,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_FORCE_DELETE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认删除" {
			replyText(bot, chatID, "🛑 已取消物理删号。")
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(session.GetTemp("delete_tgt_uid"), 10, 64)
		if err != nil || tgtID == 0 {
			replyText(bot, chatID, "❌ 删号会话状态异常，已中止。请重新发起物理删号流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("delete_reason")

		if tgtID == userID {
			replyText(bot, chatID, "❌ 警告：系统禁止执行物理自毁操作！")
			clearSession(userID)
			return
		}

		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 本地数据库查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 物理删号确认目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "⏳ 正在执行跨端抹除协议...")

		if tUser.Role == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已经是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		if tUser.AbsUserID != "" {
			apiErr := absClient.DeleteUser(tUser.AbsUserID)
			if apiErr != nil && !IsAbsNotFoundError(apiErr) {
				replyText(bot, chatID, fmt.Sprintf("❌ **删号行动中止！**\nABS 服务端无响应或拒绝删除: %s\n\n⚠️ 为防止该用户变为不受监管的账号，系统已保留其本地档案。请检查 ABS 服务器状态后重试！", formatMarkdownError(apiErr)))
				clearSession(userID)
				return
			}
		}

		if err := deleteLocalUserWithAudit(userID, tgtID, tUser.AbsUserID, "FORCE_DELETE_USER", func(deleted User) string {
			return fmt.Sprintf("物理删除用户 %s(%d)，ABS ID=%s，原因：%s", formatPlainValue(deleted.Username), tgtID, formatPlainValue(deleted.AbsUserID), formatPlainValue(reason))
		}); err != nil {
			log.Printf("⚠️ ABS 已删除，但本地用户删除或审计写入失败: user=%d abs=%s err=%s", tgtID, formatPlainValue(tUser.AbsUserID), formatPlainError(err))
			replyText(bot, chatID, "⚠️ ABS 账号已删除，但本地档案或审计写入失败，请立即人工核查数据库。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("🗑️ **处决完成**。用户 `%d` 的 ABS 账号及本地资产已被删除。", tgtID))

		clearSession(userID)

	case "WAITING_QUERY_CODE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}
		queryCode := strings.TrimSpace(text)
		queryHash := hashSensitiveToken(queryCode)
		if queryHash == "" {
			replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			clearSession(userID)
			return
		}

		var foundType string
		var isUsed bool
		var usedByID int64
		displayCode := maskSecret(queryCode)

		var invCode InviteCode
		inviteErr := DB.Where("code_hash = ?", queryHash).First(&invCode).Error
		if inviteErr == nil {
			foundType = "🎫 专属邀请码"
			isUsed = invCode.IsUsed
			usedByID = invCode.UsedByID
			if invCode.CodePreview != "" {
				displayCode = invCode.CodePreview
			}
		} else if !errors.Is(inviteErr, gorm.ErrRecordNotFound) {
			log.Printf("⚠️ 卡密溯源邀请码读取失败: user=%d err=%s", userID, formatPlainError(inviteErr))
			replyText(bot, chatID, "❌ 卡密查询失败，请稍后重试。")
			clearSession(userID)
			return
		} else {
			var renCode RenewCode
			renewErr := DB.Where("code_hash = ?", queryHash).First(&renCode).Error
			if renewErr == nil {
				foundType = fmt.Sprintf("💳 %d天续期卡", renCode.Days)
				isUsed = renCode.IsUsed
				usedByID = renCode.UsedByID
				if renCode.CodePreview != "" {
					displayCode = renCode.CodePreview
				}
			} else if errors.Is(renewErr, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "❌ 查无此码。请确认卡密输入正确，且是由本系统生成的。")
				clearSession(userID)
				return
			} else {
				log.Printf("⚠️ 卡密溯源续期卡读取失败: user=%d err=%s", userID, formatPlainError(renewErr))
				replyText(bot, chatID, "❌ 卡密查询失败，请稍后重试。")
				clearSession(userID)
				return
			}
		}

		statusText := "🟢 **未使用** (可正常分发或使用)"
		useInfo := ""

		if isUsed {
			statusText = "🔴 **已使用/已核销**"
			var user User
			userErr := DB.Where("telegram_id = ?", usedByID).First(&user).Error
			if userErr == nil {
				safeName := escapeMarkdown(user.Username)
				useInfo = fmt.Sprintf("\n👤 **使用者名称**: `%s`\n🆔 **使用者 TG ID**: `%d`", safeName, usedByID)
			} else if errors.Is(userErr, gorm.ErrRecordNotFound) {
				useInfo = fmt.Sprintf("\n👤 **使用者 TG ID**: `%d` (该用户可能已注销或退群)", usedByID)
			} else {
				log.Printf("⚠️ 卡密溯源使用者档案读取失败: admin=%d target=%d err=%s", userID, usedByID, formatPlainError(userErr))
				useInfo = fmt.Sprintf("\n👤 **使用者 TG ID**: `%d`\n👤 **使用者档案**: `读取失败`", usedByID)
			}
		}

		info := fmt.Sprintf("🔍 **卡密溯源档案**\n\n"+
			"🏷️ **资产类型**: %s\n"+
			"🔑 **卡密内容**: `%s`\n"+
			"🛡️ **当前状态**: %s%s",
			foundType, displayCode, statusText, useInfo)

		writeAuditLog(userID, "QUERY_CODE", foundType, fmt.Sprintf("查询卡密状态，类型：%s，是否使用：%t，使用者：%d", foundType, isUsed, usedByID))
		replyText(bot, chatID, info)
		clearSession(userID)
	}
}

// ==========================================
// 🧧 抢包引擎与后台运维 (物理环境区分)
// ==========================================

const redPacketGrabMaxAttempts = 8

type redPacketGrabResult struct {
	Packet RedPacket
	Points int
}

func applyRedPacketClaimScopeFilter(query *gorm.DB, userID int64) *gorm.DB {
	if query == nil {
		return query
	}
	return query.Where(
		"(COALESCE(red_packets.claim_scope, '') = '' OR (red_packets.claim_scope = ? AND EXISTS (SELECT 1 FROM world_boss_participants WHERE world_boss_participants.boss_id = red_packets.ref_id AND world_boss_participants.user_id = ? AND world_boss_participants.deleted_at IS NULL)))",
		redPacketClaimScopeWorldBossParticipant,
		userID,
	)
}

func hasActiveIneligibleWorldBossRedPacketTx(tx *gorm.DB, userID int64, prefix string) (bool, error) {
	if tx == nil || userID == 0 {
		return false, nil
	}

	var count int64
	err := tx.Model(&RedPacket{}).
		Where("left_count > ? AND is_finished = ? AND id LIKE ?", 0, false, prefix+"%").
		Where("claim_scope = ?", redPacketClaimScopeWorldBossParticipant).
		Where("id NOT IN (?)", tx.Model(&RedPacketGrab{}).
			Select("packet_id").
			Where("user_id = ?", userID)).
		Where("NOT EXISTS (SELECT 1 FROM world_boss_participants WHERE world_boss_participants.boss_id = red_packets.ref_id AND world_boss_participants.user_id = ? AND world_boss_participants.deleted_at IS NULL)", userID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func hasActiveIneligibleWorldBossRedPacket(userID int64, prefix string) (bool, error) {
	return hasActiveIneligibleWorldBossRedPacketTx(DB, userID, prefix)
}

func claimableRedPacketQuery(tx *gorm.DB, userID int64, prefix string) *gorm.DB {
	if tx == nil {
		return tx
	}
	query := tx.
		Where("left_count > ? AND is_finished = ? AND id LIKE ?", 0, false, prefix+"%").
		Where("id NOT IN (?)", tx.Model(&RedPacketGrab{}).
			Select("packet_id").
			Where("user_id = ?", userID)).
		Order("created_at asc")
	return applyRedPacketClaimScopeFilter(query, userID)
}

func executeRedPacketGrabWithRetry(userID int64, safeName string, prefix string) (redPacketGrabResult, error) {
	var result redPacketGrabResult
	var lastErr error

	for attempt := 1; attempt <= redPacketGrabMaxAttempts; attempt++ {
		var attemptResult redPacketGrabResult
		err := DB.Transaction(func(tx *gorm.DB) error {
			packet, points, err := grabRedPacketInTx(tx, userID, safeName, prefix)
			if err != nil {
				return err
			}

			attemptResult = redPacketGrabResult{
				Packet: packet,
				Points: points,
			}
			return nil
		})
		if err == nil {
			attemptResult.Packet.LeftCount--
			attemptResult.Packet.LeftPoints -= attemptResult.Points
			if attemptResult.Packet.LeftCount <= 0 {
				attemptResult.Packet.LeftCount = 0
				attemptResult.Packet.IsFinished = true
			}
			return attemptResult, nil
		}

		lastErr = err
		if !isRetryableRedPacketGrabError(err) {
			return result, err
		}
		if attempt < redPacketGrabMaxAttempts {
			time.Sleep(redPacketGrabRetryDelay(attempt))
		}
	}

	if lastErr == nil {
		lastErr = errConcurrentRedPacketGrabRetry
	}
	return result, fmt.Errorf("%w: %s", errConcurrentRedPacketGrabRetry, formatPlainError(lastErr))
}

func createRedPacketGrabInTx(tx *gorm.DB, grab *RedPacketGrab) error {
	if tx == nil || grab == nil {
		return fmt.Errorf("RED_PACKET_GRAB_INVALID")
	}
	entry := *grab
	entry.GrabberName = formatPlainValue(entry.GrabberName)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("RED_PACKET_GRAB_CREATE_MISSED")
	}
	return nil
}

func createRedPacketInTx(tx *gorm.DB, packet *RedPacket) error {
	if tx == nil || packet == nil {
		return fmt.Errorf("RED_PACKET_INVALID")
	}
	entry := *packet
	entry.ID = formatPlainValue(entry.ID)
	entry.SenderName = formatPlainValue(entry.SenderName)
	entry.RefType = formatPlainValue(entry.RefType)
	entry.RefID = formatPlainValue(entry.RefID)
	entry.ClaimScope = formatPlainValue(entry.ClaimScope)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("RED_PACKET_CREATE_MISSED")
	}
	*packet = entry
	return nil
}

func grabRedPacketInTx(tx *gorm.DB, userID int64, safeName string, prefix string) (RedPacket, int, error) {
	var packet RedPacket
	// 选取最早一个“本用户尚未领取”的有效红包。
	// 之前固定取最早红包再判重，会导致同时存在多个红包时，
	// 已领过最早那个的用户被判 errAlreadyGrabbed 而无法领取较新的红包，
	// 直到最早的红包被抢空。这里用子查询排除已领取的红包修复该锁死。
	query := claimableRedPacketQuery(tx, userID, prefix)
	if err := query.First(&packet).Error; err != nil {
		return RedPacket{}, 0, err
	}

	var grabRecord RedPacketGrab
	if err := tx.Where("packet_id = ? AND user_id = ?", packet.ID, userID).First(&grabRecord).Error; err == nil {
		return RedPacket{}, 0, errAlreadyGrabbed
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return RedPacket{}, 0, err
	}

	if packet.LeftCount <= 0 || packet.LeftPoints <= 0 || packet.LeftPoints < packet.LeftCount {
		return RedPacket{}, 0, fmt.Errorf("red packet balance inconsistent: packet=%s left_count=%d left_points=%d", packet.ID, packet.LeftCount, packet.LeftPoints)
	}

	grabPoints := 0
	if packet.LeftCount == 1 {
		grabPoints = packet.LeftPoints
	} else {
		max := (packet.LeftPoints / packet.LeftCount) * 2
		if max <= 1 {
			max = 2
		}
		nBig, err := rand.Int(rand.Reader, big.NewInt(int64(max-1)))
		if err != nil {
			return RedPacket{}, 0, errRandomFailed
		}
		grabPoints = int(nBig.Int64()) + 1
		if grabPoints >= packet.LeftPoints {
			grabPoints = packet.LeftPoints - packet.LeftCount + 1
		}
	}
	if grabPoints <= 0 {
		return RedPacket{}, 0, fmt.Errorf("red packet grab points invalid: packet=%s points=%d", packet.ID, grabPoints)
	}

	updateData := map[string]interface{}{
		"left_count":  gorm.Expr("left_count - 1"),
		"left_points": gorm.Expr("left_points - ?", grabPoints),
	}

	if packet.LeftCount == 1 {
		updateData["is_finished"] = true
	}

	// CAS 条件同时检查 left_count、left_points 和 is_finished。
	// 只要红包状态被其他并发请求改过，本次事务回滚，由外层重新选包重试。
	res := tx.Model(&RedPacket{}).
		Where("id = ? AND left_count = ? AND left_points = ? AND is_finished = ?", packet.ID, packet.LeftCount, packet.LeftPoints, false).
		Updates(updateData)

	if res.Error != nil {
		return RedPacket{}, 0, res.Error
	}

	if res.RowsAffected == 0 {
		return RedPacket{}, 0, errConcurrentRedPacketGrabRetry
	}

	if err := createRedPacketGrabInTx(tx, &RedPacketGrab{
		PacketID:    packet.ID,
		UserID:      userID,
		GrabberName: safeName,
		Points:      grabPoints,
		GrabbedAt:   time.Now(),
	}); err != nil {
		// 如果唯一索引触发，说明用户已经抢过。
		// 返回 ALREADY_GRABBED，事务会自动回滚前面的红包扣减。
		if isUniqueConstraintError(err) {
			return RedPacket{}, 0, errAlreadyGrabbed
		}
		return RedPacket{}, 0, err
	}

	if err := applyPointDeltaInTx(
		tx,
		userID,
		grabPoints,
		"redpacket_grab",
		fmt.Sprintf("抢到红包 %s，获得 %d 积分", packet.ID, grabPoints),
		"redpacket",
		packet.ID,
	); err != nil {
		return RedPacket{}, 0, err
	}

	return packet, grabPoints, nil
}

func isRetryableRedPacketGrabError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errConcurrentRedPacketGrabRetry) {
		return true
	}

	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "database is locked") ||
		strings.Contains(errText, "database table is locked") ||
		strings.Contains(errText, "sqlite_busy")
}

func redPacketGrabRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	delay := time.Duration(attempt*15) * time.Millisecond
	if nBig, err := rand.Int(rand.Reader, big.NewInt(20)); err == nil {
		delay += time.Duration(nBig.Int64()) * time.Millisecond
	}
	return delay
}

func userAlreadyGrabbedAllActiveRedPackets(userID int64, prefix string) (bool, error) {
	if DB == nil || userID == 0 {
		return false, nil
	}

	activeQuery := applyRedPacketClaimScopeFilter(DB.Model(&RedPacket{}).
		Where("left_count > ? AND is_finished = ? AND id LIKE ?", 0, false, prefix+"%"), userID)

	var activeCount int64
	if err := activeQuery.Count(&activeCount).Error; err != nil {
		return false, err
	}
	if activeCount == 0 {
		return false, nil
	}

	var eligibleCount int64
	eligibleQuery := applyRedPacketClaimScopeFilter(DB.Model(&RedPacket{}).
		Where("left_count > ? AND is_finished = ? AND id LIKE ?", 0, false, prefix+"%").
		Where("id NOT IN (?)", DB.Model(&RedPacketGrab{}).
			Select("packet_id").
			Where("user_id = ?", userID)), userID)
	if err := eligibleQuery.Count(&eligibleCount).Error; err != nil {
		return false, err
	}
	return eligibleCount == 0, nil
}

func handleGrabRedPacket(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if AppConfig.NoticeGroupID != 0 && !isMessageFromNoticeGroup(msg) && !isUserInGroupFresh(bot, msg.From.ID, AppConfig.NoticeGroupID) {
		if msg.Chat.IsPrivate() {
			replyText(bot, msg.Chat.ID, "⚠️ **访问受限：您尚未加入官方群组！**\n👉 请先加群后再参与抢红包。")
		}
		return
	}

	// 🚨 彻底拆除全局排队大锁 grabMutex，全面释放并发吞吐率

	userID := msg.From.ID
	chatID := msg.Chat.ID
	userName := msg.From.UserName
	if userName == "" {
		userName = msg.From.FirstName
	}
	safeName := escapeMarkdown(userName)

	var u User
	if walletUser, _, err := ensureUserWallet(msg.From); err != nil {
		log.Printf("❌ 创建幽灵钱包失败: user=%d err=%s", userID, formatPlainError(err))
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 钱包初始化失败，请稍后重试。", safeName))
		return
	} else {
		u = walletUser
	}

	prefix := "HB-"
	actionWord := "抢到"
	if msg.Text == "沾仙气" {
		prefix = "FS-"
		actionWord = "沾到"
	}

	var grabPoints int
	var packet RedPacket

	// 🛡️ 采用乐观锁 CAS + 唯一索引双保险，杜绝重复领取。
	result, err := executeRedPacketGrabWithRetry(userID, safeName, prefix)
	if err == nil {
		packet = result.Packet
		grabPoints = result.Points
	}

	if err != nil {
		alreadyGrabbedAll := false
		ineligibleWorldBossPacket := false
		var redPacketStateErr error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			alreadyGrabbedAll, redPacketStateErr = userAlreadyGrabbedAllActiveRedPackets(userID, prefix)
			if redPacketStateErr == nil && !alreadyGrabbedAll {
				ineligibleWorldBossPacket, redPacketStateErr = hasActiveIneligibleWorldBossRedPacket(userID, prefix)
			}
		}
		if redPacketStateErr != nil {
			log.Printf("⚠️ 红包领取状态读取失败: user=%d prefix=%s err=%s", userID, formatPlainValue(prefix), formatPlainError(redPacketStateErr))
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("⚠️ @%s 红包状态暂时读取失败，请稍后再试。", safeName))
		} else if errors.Is(err, errAlreadyGrabbed) || (errors.Is(err, gorm.ErrRecordNotFound) && alreadyGrabbedAll) {
			if prefix == "FS-" {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 您已经在这场机缘中沾过仙气了，不可多贪！", safeName))
			} else {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经抢过这个红包啦！", safeName))
			}
		} else if errors.Is(err, gorm.ErrRecordNotFound) && ineligibleWorldBossPacket {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("⚔️ @%s 这份 Boss 红包仅限本期参加 Boss 的道友领取。", safeName))
		} else if errors.Is(err, errConcurrentRedPacketGrabRetry) {
			log.Printf("⚠️ 红包领取并发重试耗尽: user=%d prefix=%s err=%s", userID, formatPlainValue(prefix), formatPlainError(err))
			if prefix == "FS-" {
				sendGroupAutoDeleteMessage(bot, chatID, "⏳ 当前仙缘争抢太激烈，请再发送 `沾仙气` 试一次。")
			} else {
				sendGroupAutoDeleteMessage(bot, chatID, "⏳ 当前红包争抢太激烈，请再抢一次。")
			}
		} else {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 红包领取失败: user=%d prefix=%s err=%s", userID, formatPlainValue(prefix), formatPlainError(err))
			}
			if prefix == "FS-" {
				sendGroupAutoDeleteMessage(bot, chatID, "🫙 哎呀手慢了，当前天地福泽已被瓜分完毕，静待下一位大能飞升吧！")
			} else {
				sendGroupAutoDeleteMessage(bot, chatID, "🫙 哎呀手慢了，当前没有正在发放的红包，或者已经被抢光啦！")
			}
		}
		return
	}

	balanceText := fmt.Sprintf("`%d` 积分", u.Points)
	if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		log.Printf("⚠️ 红包领取后余额读取失败: user=%d packet=%s err=%s", userID, formatPlainValue(packet.ID), formatPlainError(err))
		balanceText = "`读取失败`"
	} else {
		balanceText = fmt.Sprintf("`%d` 积分", u.Points)
	}
	if packet.LeftCount == 0 {
		var grabs []RedPacketGrab
		grabsErr := DB.Where("packet_id = ?", packet.ID).Order("points desc").Find(&grabs).Error
		if grabsErr != nil {
			log.Printf("⚠️ 红包抢空榜读取失败: packet=%s user=%d err=%s", formatPlainValue(packet.ID), userID, formatPlainError(grabsErr))
		}

		var title, senderTitle string
		if prefix == "FS-" {
			title = "💥 **本次天地福泽已被大家吸收完毕！**"
			senderTitle = "天道赐福"
		} else {
			title = "💥 **该红包已被抢空！**"
			senderTitle = "发起人"
		}

		summary := fmt.Sprintf("\n\n%s\n\n🧧 %s: %s\n💰 总积分: %d\n📦 总份数: %d\n\n**📊 气运争夺风云榜：**\n", title, senderTitle, escapeMarkdownPreservingEscapes(packet.SenderName), packet.TotalPoints, packet.Count)
		bestPoints, bestUser := 0, ""
		if grabsErr != nil {
			summary += "\n⚠️ 气运榜暂时读取失败，请稍后查看。\n"
		} else if len(grabs) > 0 {
			bestPoints, bestUser = grabs[0].Points, grabs[0].GrabberName
		}
		for i, g := range grabs {
			medal := "▪️"
			if i == 0 {
				medal = "🥇"
			} else if i == 1 {
				medal = "🥈"
			} else if i == 2 {
				medal = "🥉"
			}
			summary += fmt.Sprintf("%s @%s : `%d` 积分\n", medal, escapeMarkdownPreservingEscapes(g.GrabberName), g.Points)
		}
		if bestUser != "" {
			summary += fmt.Sprintf("\n👑 **手气最佳** 👑\n🏆 恭喜 @%s 狂揽 `%d` 积分！", escapeMarkdownPreservingEscapes(bestUser), bestPoints)
		}

		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("🎉 恭喜 @%s %s了最后一份 **%d** 积分！\n🪙 当前总资产: %s%s", safeName, actionWord, grabPoints, balanceText, summary))
	} else {
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("🎉 恭喜 @%s %s了 **%d** 积分！\n🪙 当前总资产: %s\n\n📦 剩余份数: `%d` 份", safeName, actionWord, grabPoints, balanceText, packet.LeftCount))
	}
}

func backupDatabaseToTelegram(bot *tgbotapi.BotAPI, actorID int64, reason string) {
	messageID, err := sendEncryptedBackupToTelegram(bot, "manual")
	if err != nil {
		log.Printf("⚠️ 手动加密备份失败: actor=%d err=%s", actorID, formatPlainError(err))
		auditErr := writeAuditLogInTx(DB, actorID, "MANUAL_BACKUP_FAILED", "database_backup", 0, fmt.Sprintf("手动触发加密数据库备份失败，原因：%s，错误：%s", formatPlainValue(reason), formatPlainError(err)))
		if auditErr != nil {
			log.Printf("⚠️ 手动加密备份失败审计写入失败: actor=%d err=%s", actorID, formatPlainError(auditErr))
			notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 手动加密备份失败，且失败审计写入失败。\n\n备份错误: %s\n审计错误: %s", formatPlainError(err), formatPlainError(auditErr)))
		}
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 手动加密备份失败\n\n错误: %s", formatPlainError(err)))
		return
	}

	if auditErr := writeAuditLogInTx(DB, actorID, "MANUAL_BACKUP", "database_backup", 0, fmt.Sprintf("手动触发加密数据库备份成功，message_id=%d，原因：%s", messageID, formatPlainValue(reason))); auditErr != nil {
		log.Printf("⚠️ 手动加密备份成功审计写入失败: actor=%d message_id=%d err=%s", actorID, messageID, formatPlainError(auditErr))
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 手动加密备份已发送，但成功审计写入失败，请人工核查。\n\nmessage_id: %d\n审计错误: %s", messageID, formatPlainError(auditErr)))
	}
	log.Printf("✅ 手动加密备份发送成功: actor=%d message_id=%d", actorID, messageID)
}

// ==========================================
// 🏇 赛马场全局状态与结构体
// ==========================================
type PlayerBet struct {
	UserName string
	HorseNum int
	Points   int
}

type RaceState struct {
	RaceID     string
	IsActive   bool
	IsRacing   bool
	Bets       map[int64]*PlayerBet
	TotalPool  int
	Mu         sync.Mutex
	MinBet     int
	MaxBet     int
	LastRaceAt time.Time
}

const (
	RaceBetStatusActive   = "active"
	RaceBetStatusSettled  = "settled"
	RaceBetStatusRefunded = "refunded"

	// 赛马系统补贴比例：系统额外注入玩家总筹码的该比例作为奖池配资。
	raceSystemSubsidyRate    = 0.10
	raceSystemSubsidyPercent = 10
)

func createDiceBetInTx(tx *gorm.DB, bet *DiceBet) error {
	if tx == nil || bet == nil {
		return fmt.Errorf("DICE_BET_INVALID")
	}
	entry := *bet
	entry.UserName = formatPlainValue(entry.UserName)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("DICE_BET_CREATE_MISSED")
	}
	return nil
}

func createRaceBetInTx(tx *gorm.DB, bet *RaceBet) error {
	if tx == nil || bet == nil {
		return fmt.Errorf("RACE_BET_INVALID")
	}
	entry := *bet
	entry.UserName = formatPlainValue(entry.UserName)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("RACE_BET_CREATE_MISSED")
	}
	return nil
}

func upsertDiceDailyProfitDeltaInTx(tx *gorm.DB, userID int64, dayKey string, delta int) error {
	if tx == nil || userID == 0 || strings.TrimSpace(dayKey) == "" {
		return fmt.Errorf("DICE_DAILY_PROFIT_INVALID")
	}
	res := tx.Clauses(diceDailyProfitDeltaOnConflict(delta)).
		Create(&DiceDailyProfit{UserID: userID, DayKey: dayKey, NetProfit: delta})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("DICE_DAILY_PROFIT_UPSERT_MISSED")
	}
	return nil
}

func diceDailyProfitDeltaOnConflict(delta int) clause.OnConflict {
	return clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}, {Name: "day_key"}},
		TargetWhere: clause.Where{Exprs: []clause.Expression{
			clause.Eq{Column: clause.Column{Name: "deleted_at"}, Value: nil},
		}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"net_profit": gorm.Expr("net_profit + ?", delta),
		}),
	}
}

func createDiceDailyProfitInTx(tx *gorm.DB, stat *DiceDailyProfit) error {
	if tx == nil || stat == nil || stat.UserID == 0 || strings.TrimSpace(stat.DayKey) == "" {
		return fmt.Errorf("DICE_DAILY_PROFIT_INVALID")
	}
	res := tx.Create(stat)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("DICE_DAILY_PROFIT_CREATE_MISSED")
	}
	return nil
}

func updateDiceDailyProfitDeltaInTx(tx *gorm.DB, statID uint, delta int) error {
	if tx == nil || statID == 0 {
		return fmt.Errorf("DICE_DAILY_PROFIT_INVALID")
	}
	res := tx.Model(&DiceDailyProfit{}).
		Where("id = ?", statID).
		UpdateColumn("net_profit", gorm.Expr("net_profit + ?", delta))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("DICE_DAILY_PROFIT_UPDATE_MISSED")
	}
	return nil
}

var GroupRaces sync.Map

func getRaceState(chatID int64) *RaceState {
	val, _ := GroupRaces.LoadOrStore(chatID, &RaceState{
		Bets: make(map[int64]*PlayerBet),
	})
	return val.(*RaceState)
}

func refundRaceBetsByRaceID(raceID string, reason string) (int, int, error) {
	if raceID == "" {
		return 0, 0, nil
	}

	refundCount := 0
	refundPoints := 0

	err := DB.Transaction(func(tx *gorm.DB) error {
		txRefundCount := 0
		txRefundPoints := 0
		var bets []RaceBet
		if err := tx.Where("race_id = ? AND status = ?", raceID, RaceBetStatusActive).Find(&bets).Error; err != nil {
			return err
		}

		for _, bet := range bets {
			res := tx.Model(&RaceBet{}).
				Where("id = ? AND status = ?", bet.ID, RaceBetStatusActive).
				Update("status", RaceBetStatusRefunded)

			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				continue
			}

			if err := applyPointDeltaInTx(
				tx,
				bet.UserID,
				bet.Points,
				"race_refund",
				fmt.Sprintf("赛马异常退款，返还 %d 积分", bet.Points),
				"race",
				raceID,
			); err != nil {
				return err
			}

			txRefundCount++
			txRefundPoints += bet.Points
		}
		refundCount = txRefundCount
		refundPoints = txRefundPoints
		return nil
	})

	if err != nil {
		log.Printf("⚠️ 赛马退款失败: race_id=%s reason=%s err=%s", formatPlainValue(raceID), formatPlainValue(reason), formatPlainError(err))
		return 0, 0, err
	}

	if refundCount > 0 {
		log.Printf("↩️ 赛马异常退款完成: race_id=%s count=%d points=%d reason=%s", formatPlainValue(raceID), refundCount, refundPoints, formatPlainValue(reason))
	}
	return refundCount, refundPoints, nil
}

func recoverActiveRaceBetsOnStartup() {
	var raceIDs []string

	if err := DB.Model(&RaceBet{}).
		Where("status = ?", RaceBetStatusActive).
		Distinct("race_id").
		Pluck("race_id", &raceIDs).Error; err != nil {
		log.Printf("⚠️ 启动时扫描未结算赛马下注失败: %s", formatPlainError(err))
		return
	}

	if len(raceIDs) == 0 {
		log.Println("✅ 启动检查：没有发现未结算赛马下注")
		return
	}

	log.Printf("⚠️ 启动检查：发现 %d 局未结算赛马，开始自动退款", len(raceIDs))

	totalCount := 0
	totalPoints := 0

	for _, raceID := range raceIDs {
		count, points, err := refundRaceBetsByRaceID(raceID, "startup recovery")
		if err != nil {
			continue
		}
		totalCount += count
		totalPoints += points
	}

	log.Printf("✅ 启动赛马兜底退款完成：退款人数=%d，总积分=%d", totalCount, totalPoints)
}

func updateRaceBetStatusCAS(tx *gorm.DB, raceID string, userID int64, fromStatus string, values map[string]interface{}) (bool, error) {
	res := tx.Model(&RaceBet{}).
		Where("race_id = ? AND user_id = ? AND status = ?", raceID, userID, fromStatus).
		Updates(values)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func loadActiveRaceBetsSnapshot(raceID string) (map[int64]*PlayerBet, int, error) {
	if raceID == "" {
		return map[int64]*PlayerBet{}, 0, nil
	}
	var bets []RaceBet
	if err := DB.Where("race_id = ? AND status = ?", raceID, RaceBetStatusActive).Find(&bets).Error; err != nil {
		return nil, 0, err
	}
	snapshot := make(map[int64]*PlayerBet, len(bets))
	totalPool := 0
	for _, bet := range bets {
		snapshot[bet.UserID] = &PlayerBet{
			UserName: bet.UserName,
			HorseNum: bet.HorseNum,
			Points:   bet.Points,
		}
		totalPool += bet.Points
	}
	return snapshot, totalPool, nil
}

func calculateHorseRaceBetRange(avgPoints float64) (int, int) {
	minBet := int(avgPoints * 0.03)
	maxBet := int(avgPoints * 0.15)

	if minBet < 3 {
		minBet = 3
	}
	if maxBet < 15 {
		maxBet = 15
	}
	if maxBet > 500 {
		maxBet = 500
	}
	return minBet, maxBet
}

// ==========================================
// 🎲 三界骰局核心引擎与动画渲染
// ==========================================

type DicePlayerBet struct {
	UserName string
	Choice   string
	Points   int
}

type DiceState struct {
	DiceID     string
	IsActive   bool
	IsRolling  bool
	Bets       map[int64]*DicePlayerBet
	TotalPool  int
	Mu         sync.Mutex
	MinBet     int
	MaxBet     int
	LastDiceAt time.Time
}

var GroupDices sync.Map

func getDiceState(chatID int64) *DiceState {
	val, _ := GroupDices.LoadOrStore(chatID, &DiceState{
		Bets: make(map[int64]*DicePlayerBet),
	})
	return val.(*DiceState)
}

func isDiceBetCommand(text string) bool {
	parts := strings.Fields(text)
	if len(parts) != 3 || parts[0] != "押" {
		return false
	}
	choice := parts[1]
	return choice == "大" || choice == "小" || choice == "豹子"
}

func isDiceOpenTime(now time.Time) bool {
	loc := time.FixedZone("CST", 8*3600)
	local := now.In(loc)
	minutes := local.Hour()*60 + local.Minute()
	return (minutes >= 8*60 && minutes < 19*60+55) || (minutes >= 22*60+5 && minutes < 24*60)
}

func diceDayKey(t time.Time) string {
	loc := time.FixedZone("CST", 8*3600)
	return t.In(loc).Format("2006-01-02")
}

func diceResultType(dice []int) string {
	if len(dice) == 3 && dice[0] == dice[1] && dice[1] == dice[2] {
		return "豹子"
	}
	sum := 0
	for _, v := range dice {
		sum += v
	}
	if sum >= 11 && sum <= 17 {
		return "大"
	}
	return "小"
}

func diceFaces(dice []int) string {
	parts := make([]string, 0, len(dice))
	for _, v := range dice {
		if v < 1 || v > 6 {
			parts = append(parts, "🎲 ?点")
			continue
		}
		parts = append(parts, fmt.Sprintf("🎲 %d点", v))
	}
	return strings.Join(parts, "　")
}

func rollThreeDice() ([]int, error) {
	dice := make([]int, 3)
	for i := 0; i < 3; i++ {
		nBig, err := rand.Int(rand.Reader, big.NewInt(6))
		if err != nil {
			return nil, err
		}
		dice[i] = int(nBig.Int64()) + 1
	}
	return dice, nil
}

func refundDiceBetsByDiceID(diceID string, reason string) (int, int, error) {
	if diceID == "" {
		return 0, 0, nil
	}

	refundCount := 0
	refundPoints := 0

	err := DB.Transaction(func(tx *gorm.DB) error {
		txRefundCount := 0
		txRefundPoints := 0
		var bets []DiceBet
		if err := tx.Where("dice_id = ? AND status = ?", diceID, RaceBetStatusActive).Find(&bets).Error; err != nil {
			return err
		}

		for _, bet := range bets {
			res := tx.Model(&DiceBet{}).
				Where("id = ? AND status = ?", bet.ID, RaceBetStatusActive).
				Update("status", RaceBetStatusRefunded)

			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				continue
			}

			if err := applyPointDeltaInTx(
				tx,
				bet.UserID,
				bet.Points,
				"dice_refund",
				fmt.Sprintf("骰子异常退款，返还 %d 积分", bet.Points),
				"dice",
				diceID,
			); err != nil {
				return err
			}

			txRefundCount++
			txRefundPoints += bet.Points
		}
		refundCount = txRefundCount
		refundPoints = txRefundPoints
		return nil
	})

	if err != nil {
		log.Printf("⚠️ 骰子退款失败: dice_id=%s reason=%s err=%s", formatPlainValue(diceID), formatPlainValue(reason), formatPlainError(err))
		return 0, 0, err
	}

	if refundCount > 0 {
		log.Printf("↩️ 骰子异常退款完成: dice_id=%s count=%d points=%d reason=%s", formatPlainValue(diceID), refundCount, refundPoints, formatPlainValue(reason))
	}
	return refundCount, refundPoints, nil
}

func recoverActiveDiceBetsOnStartup() {
	var diceIDs []string
	if err := DB.Model(&DiceBet{}).
		Where("status = ?", RaceBetStatusActive).
		Distinct("dice_id").
		Pluck("dice_id", &diceIDs).Error; err != nil {
		log.Printf("⚠️ 启动时扫描未结算骰子下注失败: %s", formatPlainError(err))
		return
	}

	if len(diceIDs) == 0 {
		log.Println("✅ 启动检查：没有发现未结算骰子下注")
		return
	}

	log.Printf("⚠️ 启动检查：发现 %d 局未结算骰子，开始自动退款", len(diceIDs))
	totalCount := 0
	totalPoints := 0
	for _, diceID := range diceIDs {
		count, points, err := refundDiceBetsByDiceID(diceID, "startup recovery")
		if err != nil {
			continue
		}
		totalCount += count
		totalPoints += points
	}
	log.Printf("✅ 启动骰子兜底退款完成：退款人数=%d，总积分=%d", totalCount, totalPoints)
}

func updateDiceBetStatusCAS(tx *gorm.DB, diceID string, userID int64, fromStatus string, values map[string]interface{}) (bool, error) {
	res := tx.Model(&DiceBet{}).
		Where("dice_id = ? AND user_id = ? AND status = ?", diceID, userID, fromStatus).
		Updates(values)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func loadActiveDiceBetsSnapshot(diceID string) (map[int64]*DicePlayerBet, int, error) {
	if diceID == "" {
		return map[int64]*DicePlayerBet{}, 0, nil
	}
	var bets []DiceBet
	if err := DB.Where("dice_id = ? AND status = ?", diceID, RaceBetStatusActive).Find(&bets).Error; err != nil {
		return nil, 0, err
	}
	snapshot := make(map[int64]*DicePlayerBet, len(bets))
	totalPool := 0
	for _, bet := range bets {
		snapshot[bet.UserID] = &DicePlayerBet{
			UserName: bet.UserName,
			Choice:   bet.Choice,
			Points:   bet.Points,
		}
		totalPool += bet.Points
	}
	return snapshot, totalPool, nil
}

func handleDiceGame(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if AppConfig.NoticeGroupID != 0 && !isUserInGroupFresh(bot, msg.From.ID, AppConfig.NoticeGroupID) {
		return
	}

	globalDice := getDiceState(msg.Chat.ID)
	userID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)
	userName := msg.From.UserName
	if userName == "" {
		userName = msg.From.FirstName
	}
	safeName := escapeMarkdown(userName)

	if text == "发起骰子" {
		if !isDiceOpenTime(time.Now()) {
			sendGroupAutoDeleteMessage(bot, chatID, "⏳ **三界骰局尚未开放！**\n\n开放时间为 **08:00-19:55**、**22:05-24:00**，赛马黄金档前后各预留 5 分钟缓冲。")
			return
		}

		globalDice.Mu.Lock()
		if globalDice.IsActive {
			globalDice.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, "⚠️ 三界骰局正在进行中，本局还未结束，请勿重复发起！")
			return
		}
		cdDuration := 20 * time.Second
		if time.Since(globalDice.LastDiceAt) < cdDuration {
			cdLeft := int(cdDuration.Seconds() - time.Since(globalDice.LastDiceAt).Seconds())
			globalDice.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("🎲 灵骰正在回气，请等待 **%d 秒** 后再发起下一局！", cdLeft))
			return
		}

		globalDice.DiceID = fmt.Sprintf("DICE-%d-%s", chatID, generateRandomCode(8))
		globalDice.IsActive = true
		globalDice.IsRolling = false
		globalDice.Bets = make(map[int64]*DicePlayerBet)
		globalDice.TotalPool = 0
		globalDice.MinBet = 1
		globalDice.MaxBet = 10
		globalDice.Mu.Unlock()

		notice := "🎲 **三界骰局已开启！** 🎲\n\n" +
			"💰 **本局限额**：`1` - `10` 积分\n" +
			"⏱ **下注时间**：30 秒\n" +
			"🤖 **系统补贴**：多人局总筹码大于 `50` 且有人中奖时，补贴 `10%`\n" +
			"🐉 **豹子奖励**：押中豹子额外获得 `本金 × 3`，再按本金比例瓜分奖池\n" +
			"⚖️ **每日上限**：骰子每日净盈利最多 `200` 积分\n\n" +
			"👇 **下注格式**：`押 大 3` / `押 小 3` / `押 豹子 3`\n\n" +
			"大：11-17，小：4-10，豹子：三个骰子点数相同，大小通杀。"
		sendGroupAutoDeleteMessage(bot, chatID, notice)

		go runDiceRoutine(bot, chatID)
		return
	}

	if isDiceBetCommand(text) {
		globalDice.Mu.Lock()
		if !globalDice.IsActive {
			globalDice.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 当前没有开放中的骰局，请发送 `发起骰子` 开启新一局！", safeName))
			return
		}
		if globalDice.IsRolling {
			globalDice.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 骰子已经开始转动了，买定离手，下局请早！", safeName))
			return
		}
		if _, exists := globalDice.Bets[userID]; exists {
			globalDice.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经下过注了，一局只能押一次！", safeName))
			return
		}

		diceID := globalDice.DiceID
		minBet := globalDice.MinBet
		maxBet := globalDice.MaxBet
		globalDice.Mu.Unlock()

		parts := strings.Fields(text)
		choice := parts[1]
		points, err := strconv.Atoi(parts[2])
		if err != nil {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 金额必须是纯数字，格式如：`押 大 3`", safeName))
			return
		}
		if points < minBet || points > maxBet {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 本局下注范围为 **%d-%d** 积分。", safeName, minBet, maxBet))
			return
		}

		err = DB.Transaction(func(tx *gorm.DB) error {
			if _, _, err := ensureUserWalletInTx(tx, msg.From); err != nil {
				return err
			}

			if err := createDiceBetInTx(tx, &DiceBet{
				DiceID:   diceID,
				ChatID:   chatID,
				UserID:   userID,
				UserName: safeName,
				Choice:   choice,
				Points:   points,
				Status:   RaceBetStatusActive,
			}); err != nil {
				if isUniqueConstraintError(err) {
					return errAlreadyBet
				}
				return err
			}

			if err := applyPointDeltaInTx(
				tx,
				userID,
				-points,
				"dice_bet",
				fmt.Sprintf("骰子下注：押 %s，消耗 %d 积分", choice, points),
				"dice",
				diceID,
			); err != nil {
				if errors.Is(err, errPointsNotEnough) {
					return errInsufficientPoints
				}
				return err
			}

			return nil
		})

		if err != nil {
			if errors.Is(err, errAlreadyBet) {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经下过注了，一局只能押一次！", safeName))
			} else if errors.Is(err, errInsufficientPoints) {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 您的钱包可用积分不足！", safeName))
			} else {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 下注失败，系统繁忙，请稍后重试！", safeName))
			}
			return
		}

		refundDiceBet := func() {
			err := DB.Transaction(func(tx *gorm.DB) error {
				claimed, err := updateDiceBetStatusCAS(tx, diceID, userID, RaceBetStatusActive, map[string]interface{}{
					"status": RaceBetStatusRefunded,
				})
				if err != nil {
					return err
				}
				if !claimed {
					return nil
				}

				if err := applyPointDeltaInTx(
					tx,
					userID,
					points,
					"dice_refund",
					fmt.Sprintf("骰子异常退款，返还 %d 积分", points),
					"dice",
					diceID,
				); err != nil {
					return err
				}
				return nil
			})
			if err != nil {
				log.Printf("⚠️ 骰子单人退款失败: dice_id=%s user_id=%d points=%d err=%s", formatPlainValue(diceID), userID, points, formatPlainError(err))
				return
			}
		}

		globalDice.Mu.Lock()
		if !globalDice.IsActive || globalDice.IsRolling || globalDice.DiceID != diceID {
			globalDice.Mu.Unlock()
			refundDiceBet()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 买定离手，骰子已开转，您的资金已原路退回！", safeName))
			return
		}
		if _, exists := globalDice.Bets[userID]; exists {
			globalDice.Mu.Unlock()
			refundDiceBet()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经下过注了，本次重复请求已退款。", safeName))
			return
		}
		globalDice.Bets[userID] = &DicePlayerBet{UserName: safeName, Choice: choice, Points: points}
		globalDice.TotalPool += points
		globalDice.Mu.Unlock()

		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✅ @%s 成功买入 **%d** 积分押注 **%s**！", safeName, points, choice))
	}
}

func runDiceRoutine(bot *tgbotapi.BotAPI, chatID int64) {
	globalDice := getDiceState(chatID)
	diceID := ""
	settled := false

	globalDice.Mu.Lock()
	diceID = globalDice.DiceID
	globalDice.Mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ 骰子协程 panic，准备退款: dice_id=%s panic=%s", formatPlainValue(diceID), formatPlainValue(r))
		}
		if diceID != "" && !settled {
			count, points, err := refundDiceBetsByDiceID(diceID, "dice routine aborted")
			if err == nil && count > 0 {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("↩️ **本局骰子异常中止**\n\n系统已自动退还 `%d` 名玩家共 `%d` 积分。", count, points))
			}
		}

		globalDice.Mu.Lock()
		if globalDice.DiceID == diceID {
			globalDice.IsActive = false
			globalDice.IsRolling = false
			globalDice.LastDiceAt = time.Now()
		}
		globalDice.Mu.Unlock()
	}()

	if diceID == "" {
		return
	}

	time.Sleep(30 * time.Second)

	globalDice.Mu.Lock()
	if !globalDice.IsActive || globalDice.DiceID != diceID {
		globalDice.Mu.Unlock()
		return
	}
	globalDice.IsRolling = true
	totalPlayers := len(globalDice.Bets)
	globalDice.Mu.Unlock()

	if totalPlayers == 0 {
		refundDiceBetsByDiceID(diceID, "no players")
		settled = true
		sendGroupAutoDeleteMessage(bot, chatID, "🍂 由于本局无人下注，三界骰局已自动取消。")
		return
	}

	sendGroupAutoDeleteMessage(bot, chatID, "⏱ **买定离手！** 灵骰即将揭晓！")

	finalDice, err := rollThreeDice()
	if err != nil {
		log.Printf("⚠️ 骰子真实点数生成失败，准备退款: dice_id=%s err=%s", formatPlainValue(diceID), formatPlainError(err))
		return
	}
	resultType := diceResultType(finalDice)
	sum := finalDice[0] + finalDice[1] + finalDice[2]

	msg := tgbotapi.NewMessage(chatID, "🎲 **三界骰局开奖中...**\n\n🎲 转动中　🎲 转动中　🎲 转动中")
	sentMsg, err := sendAutoDelete(bot, msg)
	if err != nil {
		log.Printf("⚠️ 骰子动画初始消息发送失败，准备退款: dice_id=%s err=%s", formatPlainValue(diceID), formatTelegramSendError(err))
		return
	}

	for i := 0; i < 5; i++ {
		frameDice, frameErr := rollThreeDice()
		if frameErr != nil || i == 4 {
			frameDice = finalDice
		}
		frameText := fmt.Sprintf("🎲 **三界骰局开奖中...**\n\n%s", diceFaces(frameDice))
		if i == 4 {
			frameText = fmt.Sprintf("🎲 **三界骰局开奖结果**\n\n%s\n\n点数合计：`%d`\n结果：**%s**", diceFaces(frameDice), sum, resultType)
		}
		editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, frameText)
		_, apiErr := bot.Request(editMsg)
		if apiErr != nil {
			if tgErr, ok := apiErr.(*tgbotapi.Error); ok && tgErr.Code == 429 {
				time.Sleep(time.Duration(tgErr.RetryAfter) * time.Second)
			}
		}
		time.Sleep(700 * time.Millisecond)
	}
	time.Sleep(1 * time.Second)

	globalDice.Mu.Lock()
	if globalDice.DiceID != diceID {
		globalDice.Mu.Unlock()
		return
	}
	memoryUserPool := globalDice.TotalPool
	globalDice.Mu.Unlock()

	betsSnapshot, userPool, err := loadActiveDiceBetsSnapshot(diceID)
	if err != nil {
		log.Printf("鈿狅笍 楠板瓙缁撶畻璇诲彇鏈夋晥涓嬫敞澶辫触: dice_id=%s err=%s", formatPlainValue(diceID), formatPlainError(err))
		return
	}
	if userPool != memoryUserPool {
		log.Printf("dice settlement db snapshot differs from memory: dice_id=%s memory_pool=%d db_pool=%d", formatPlainValue(diceID), memoryUserPool, userPool)
	}

	winnerPool := 0
	for _, bet := range betsSnapshot {
		if bet.Choice == resultType {
			winnerPool += bet.Points
		}
	}

	if winnerPool > 0 {
		systemSubsidy := 0
		subsidyDesc := "无"
		if totalPlayers > 1 && userPool > 50 {
			systemSubsidy = int(float64(userPool) * 0.10)
			subsidyDesc = "多人局总筹码大于50，补贴10%"
		}
		totalPrizePool := userPool + systemSubsidy
		withheldTotal := 0
		winList := ""
		dayKey := diceDayKey(time.Now())

		// 计算按比例瓜分奖池后，因为整数除法产生的余数。
		// 例如奖池 101 分，赢家比例瓜分后只发出 100 分，剩余 1 分进入天道奖池。
		prizeRemainder := totalPrizePool
		for _, bet := range betsSnapshot {
			if bet.Choice == resultType {
				prizeRemainder -= (bet.Points * totalPrizePool) / winnerPool
			}
		}
		if prizeRemainder < 0 {
			prizeRemainder = 0
		}

		poolAfter := 0
		isBurst := false
		expectedWinnerCount := 0
		expectedLoserCount := 0
		for _, bet := range betsSnapshot {
			if bet.Choice == resultType {
				expectedWinnerCount++
			} else {
				expectedLoserCount++
			}
		}
		err := func() error {
			return runFusionPoolLockedTransaction(func(tx *gorm.DB) error {
				claimedWinnerCount := 0
				claimedLoserCount := 0
				for uid, bet := range betsSnapshot {
					if bet.Choice != resultType {
						claimed, err := updateDiceBetStatusCAS(tx, diceID, uid, RaceBetStatusActive, map[string]interface{}{
							"status": RaceBetStatusSettled,
							"result": resultType,
						})
						if err != nil {
							return err
						}
						if !claimed {
							continue
						}
						claimedLoserCount++

						if err := upsertDiceDailyProfitDeltaInTx(tx, uid, dayKey, -bet.Points); err != nil {
							return err
						}
						continue
					}

					poolShare := (bet.Points * totalPrizePool) / winnerPool
					bonus := 0
					if resultType == "豹子" {
						bonus = bet.Points * 3
					}
					expectedPayout := poolShare + bonus

					var stat DiceDailyProfit
					if err := tx.Where("user_id = ? AND day_key = ?", uid, dayKey).First(&stat).Error; err != nil {
						if errors.Is(err, gorm.ErrRecordNotFound) {
							stat = DiceDailyProfit{UserID: uid, DayKey: dayKey, NetProfit: 0}
							if err := createDiceDailyProfitInTx(tx, &stat); err != nil {
								return err
							}
						} else {
							return err
						}
					}

					maxPayout := expectedPayout
					remainingProfit := 200 - stat.NetProfit
					if remainingProfit < 0 {
						remainingProfit = 0
					}
					capPayout := bet.Points + remainingProfit
					if maxPayout > capPayout {
						maxPayout = capPayout
					}
					if maxPayout < 0 {
						maxPayout = 0
					}

					withheld := expectedPayout - maxPayout
					if withheld < 0 {
						withheld = 0
					}

					claimed, err := updateDiceBetStatusCAS(tx, diceID, uid, RaceBetStatusActive, map[string]interface{}{
						"status":     RaceBetStatusSettled,
						"result":     resultType,
						"payout":     maxPayout,
						"pool_share": poolShare,
						"bonus":      bonus,
						"withheld":   withheld,
					})
					if err != nil {
						return err
					}
					if !claimed {
						continue
					}
					claimedWinnerCount++
					withheldTotal += withheld

					if maxPayout > 0 {
						if err := applyPointDeltaInTx(
							tx,
							uid,
							maxPayout,
							"dice_win",
							fmt.Sprintf("骰子中奖，获得 %d 积分", maxPayout),
							"dice",
							diceID,
						); err != nil {
							return err
						}
					}

					delta := maxPayout - bet.Points
					if err := updateDiceDailyProfitDeltaInTx(tx, stat.ID, delta); err != nil {
						return err
					}

					if withheld > 0 {
						winList += fmt.Sprintf("👑 @%s : 到账 `%d` 积分（天道上限回收 `%d`）\n", escapeMarkdownPreservingEscapes(bet.UserName), maxPayout, withheld)
					} else {
						winList += fmt.Sprintf("👑 @%s : 到账 `%d` 积分\n", escapeMarkdownPreservingEscapes(bet.UserName), maxPayout)
					}
				}

				if claimedWinnerCount != expectedWinnerCount {
					return fmt.Errorf("DICE_WINNER_SETTLEMENT_MISSED")
				}
				if claimedLoserCount != expectedLoserCount {
					return fmt.Errorf("DICE_LOSER_SETTLEMENT_MISSED")
				}
				if claimedWinnerCount == 0 {
					prizeRemainder = 0
				}

				poolInjectTotal := withheldTotal + prizeRemainder
				if poolInjectTotal > 0 {
					var err error
					poolAfter, isBurst, err = addPointsToFusionPoolInTx(tx, poolInjectTotal)
					if err != nil {
						return err
					}
				}

				return nil
			})
		}()

		if err != nil {
			log.Printf("⚠️ 骰子结算失败，准备退款: dice_id=%s err=%s", formatPlainValue(diceID), formatPlainError(err))
			return
		}

		poolNotice := ""
		poolInjectTotal := withheldTotal + prizeRemainder
		if poolInjectTotal > 0 {
			injectDetails := make([]string, 0, 2)

			if withheldTotal > 0 {
				injectDetails = append(injectDetails, fmt.Sprintf("天道上限回收 `%d` 积分", withheldTotal))
			}
			if prizeRemainder > 0 {
				injectDetails = append(injectDetails, fmt.Sprintf("奖池瓜分余数 `%d` 积分", prizeRemainder))
			}

			poolNotice = fmt.Sprintf(
				"\n🌊 %s已注入天道奖池，当前水位：`%d/300`。",
				strings.Join(injectDetails, "，"),
				poolAfter,
			)

			if isBurst {
				poolNotice += "\n🎁 天道奖池已满，系统自动生成 `300` 积分灵气红包！"
				notifyFusionPoolBurst(bot, chatID, "三界骰局结算引动天地灵气")
			}
		}

		settled = true
		finalAnnounce := fmt.Sprintf(
			"🎲 **三界骰局结算完成！** 🎲\n\n"+
				"开奖结果：%s，合计 `%d` 点，判定为 **%s**\n\n"+
				"💰 玩家总筹码：`%d` 积分\n"+
				"🤖 系统补贴：`+%d` 积分（%s）\n"+
				"📊 最终奖池：`%d` 积分\n\n"+
				"**🤑 获胜名单（按本金比例瓜分）：**\n%s%s",
			diceFaces(finalDice),
			sum,
			resultType,
			userPool,
			systemSubsidy,
			subsidyDesc,
			totalPrizePool,
			winList,
			poolNotice,
		)
		sendGroupAutoDeleteMessage(bot, chatID, finalAnnounce)
		return
	}

	poolAfter := 0
	isBurst := false
	err = func() error {
		return runFusionPoolLockedTransaction(func(tx *gorm.DB) error {
			dayKey := diceDayKey(time.Now())
			for uid, bet := range betsSnapshot {
				if err := upsertDiceDailyProfitDeltaInTx(tx, uid, dayKey, -bet.Points); err != nil {
					return err
				}
			}

			res := tx.Model(&DiceBet{}).
				Where("dice_id = ? AND status = ?", diceID, RaceBetStatusActive).
				Updates(map[string]interface{}{"status": RaceBetStatusSettled, "result": resultType})
			if res.Error != nil {
				return res.Error
			}
			if userPool > 0 && res.RowsAffected == 0 {
				return fmt.Errorf("DICE_BET_SETTLEMENT_MISSED")
			}

			if userPool > 0 {
				var err error
				poolAfter, isBurst, err = addPointsToFusionPoolInTx(tx, userPool)
				if err != nil {
					return err
				}
			}

			return nil
		})
	}()
	if err != nil {
		log.Printf("⚠️ 骰子系统通吃结算失败，准备退款: dice_id=%s err=%s", formatPlainValue(diceID), formatPlainError(err))
		return
	}

	poolNotice := "🌊 本局无有效玩家筹码可注入天道奖池。"
	if userPool > 0 {
		poolNotice = fmt.Sprintf("🌊 已自动注入天道奖池：`+%d` 积分，当前水位：`%d/300`。", userPool, poolAfter)
		if isBurst {
			poolNotice += "\n🎁 天道奖池已满，系统自动生成 `300` 积分灵气红包！"
			notifyFusionPoolBurst(bot, chatID, "三界骰局无人押中，系统通吃筹码注入天道")
		}
	}

	settled = true
	finalAnnounce := fmt.Sprintf(
		"🎲 **三界骰局结算完成！** 🎲\n\n"+
			"开奖结果：%s，合计 `%d` 点，判定为 **%s**\n\n"+
			"🍂 **系统通吃**：本局无人押中，玩家下注的 `%d` 积分由系统赢得。\n"+
			"🤖 本局无赢家，系统补贴不生成、不注入天道奖池。\n"+
			"%s",
		diceFaces(finalDice),
		sum,
		resultType,
		userPool,
		poolNotice,
	)
	sendGroupAutoDeleteMessage(bot, chatID, finalAnnounce)
}

// ==========================================
// 🏇 赛马系统核心引擎与动画渲染 (无锁防爆版)
// ==========================================

func handleHorseRace(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if AppConfig.NoticeGroupID != 0 && !isUserInGroupFresh(bot, msg.From.ID, AppConfig.NoticeGroupID) {
		return
	}

	globalRace := getRaceState(msg.Chat.ID)
	userID := msg.From.ID
	chatID := msg.Chat.ID

	text := strings.TrimSpace(msg.Text)
	userName := msg.From.UserName
	if userName == "" {
		userName = msg.From.FirstName
	}
	safeName := escapeMarkdown(userName)

	// 发起赛马
	if text == "发起赛马" {
		loc := time.FixedZone("CST", 8*3600)
		nowHour := time.Now().In(loc).Hour()
		if nowHour < 20 || nowHour >= 22 {
			sendGroupAutoDeleteMessage(bot, chatID, "⏳ **赛马场关门啦！**\n\n营业时间为每晚 **20:00 - 22:00**，请在黄金档再来哦！")
			return
		}

		globalRace.Mu.Lock()
		if globalRace.IsActive {
			globalRace.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, "⚠️ 赛马场正在营业中，本局还未结束，请勿重复发起！")
			return
		}
		cdDuration := 1 * time.Minute
		if time.Since(globalRace.LastRaceAt) < cdDuration {
			cdLeft := int(cdDuration.Seconds() - time.Since(globalRace.LastRaceAt).Seconds())
			globalRace.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("🧹 赛马场正在打扫场地...\n\n🐎 马匹需要休息，请等待 **%d 秒** 后再发起下一局！", cdLeft))
			return
		}

		var avgPoints float64
		if err := DB.Model(&User{}).Where("points > 0").Select("avg(points)").Scan(&avgPoints).Error; err != nil {
			globalRace.Mu.Unlock()
			log.Printf("⚠️ 赛马平均积分查询失败: chat=%d err=%s", chatID, formatPlainError(err))
			sendGroupAutoDeleteMessage(bot, chatID, "❌ 赛马场暂时无法计算下注范围，请稍后重试。")
			return
		}
		minBet, maxBet := calculateHorseRaceBetRange(avgPoints)

		globalRace.RaceID = fmt.Sprintf("RACE-%d-%s", chatID, generateRandomCode(8))
		globalRace.IsActive = true
		globalRace.IsRacing = false
		globalRace.Bets = make(map[int64]*PlayerBet)
		globalRace.TotalPool = 0
		globalRace.MinBet = minBet
		globalRace.MaxBet = maxBet
		globalRace.Mu.Unlock()

		notice := fmt.Sprintf("🏇 **皇家赛马场已开放！** 🏇\n\n💰 **本局限额**：`%d` - `%d` 积分\n🌟 **福利补贴**：系统将额外注入总奖池的 **%d%%**！\n⏱ **下注时间**：60 秒\n\n👇 **请在群内回复下注，如：** `押 1 10`\n\n1️⃣号: 🔴红影\n2️⃣号: 🔵蓝电\n3️⃣号: 🟡金光\n4️⃣号: 🟢绿风\n5️⃣号: 🟣紫幻", minBet, maxBet, raceSystemSubsidyPercent)
		sendGroupAutoDeleteMessage(bot, chatID, notice)

		go runHorseRaceRoutine(bot, chatID)
		return
	}

	// 🛡️ 下注环节：落库 + 原子扣费 + 唯一索引防重复下注
	if strings.HasPrefix(text, "押 ") {
		globalRace.Mu.Lock()
		if !globalRace.IsActive {
			globalRace.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 赛马场当前未开放，请发送 `发起赛马` 开启新一局！", safeName))
			return
		}
		if globalRace.IsRacing {
			globalRace.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 比赛已经开始了，买定离手，下局请早！", safeName))
			return
		}
		if _, exists := globalRace.Bets[userID]; exists {
			globalRace.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经下过注了，一人只能买一匹马！", safeName))
			return
		}

		raceID := globalRace.RaceID
		minBet := globalRace.MinBet
		maxBet := globalRace.MaxBet
		globalRace.Mu.Unlock()

		parts := strings.Split(text, " ")
		if len(parts) != 3 {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 格式错误，正确格式如：`押 1 10`", safeName))
			return
		}

		horseNum, err1 := strconv.Atoi(parts[1])
		points, err2 := strconv.Atoi(parts[2])

		if err1 != nil || err2 != nil || horseNum < 1 || horseNum > 5 {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 马号只能是 1-5，金额必须是纯数字！", safeName))
			return
		}
		if points < minBet {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 根据汇率，最低下注额为 **%d** 积分哦！", safeName, minBet))
			return
		}
		if points > maxBet {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 老板为了防止您倾家荡产，本局最高限额 **%d** 积分！", safeName, maxBet))
			return
		}

		// 第一步：下注落库 + 扣费，在同一个事务内完成。
		err := DB.Transaction(func(tx *gorm.DB) error {
			if _, _, err := ensureUserWalletInTx(tx, msg.From); err != nil {
				return err
			}

			// 先创建下注记录。
			// race_id + user_id 有唯一索引，所以同一局同一人只能成功插入一次。
			if err := createRaceBetInTx(tx, &RaceBet{
				RaceID:   raceID,
				ChatID:   chatID,
				UserID:   userID,
				UserName: safeName,
				HorseNum: horseNum,
				Points:   points,
				Status:   RaceBetStatusActive,
			}); err != nil {
				if isUniqueConstraintError(err) {
					return errAlreadyBet
				}
				return err
			}

			if err := applyPointDeltaInTx(
				tx,
				userID,
				-points,
				"race_bet",
				fmt.Sprintf("赛马下注：%d号马，消耗 %d 积分", horseNum, points),
				"race",
				raceID,
			); err != nil {
				if errors.Is(err, errPointsNotEnough) {
					return errInsufficientPoints
				}
				return err
			}

			return nil
		})

		if err != nil {
			if errors.Is(err, errAlreadyBet) {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经下过注了，一人只能买一匹马！", safeName))
			} else if errors.Is(err, errInsufficientPoints) {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 您的钱包可用积分不足！", safeName))
			} else {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 下注失败，系统繁忙，请稍后重试！", safeName))
			}
			return
		}

		refundRaceBet := func() {
			err := DB.Transaction(func(tx *gorm.DB) error {
				claimed, err := updateRaceBetStatusCAS(tx, raceID, userID, RaceBetStatusActive, map[string]interface{}{
					"status": RaceBetStatusRefunded,
				})
				if err != nil {
					return err
				}
				if !claimed {
					return nil
				}

				if err := applyPointDeltaInTx(
					tx,
					userID,
					points,
					"race_refund",
					fmt.Sprintf("赛马异常退款，返还 %d 积分", points),
					"race",
					raceID,
				); err != nil {
					return err
				}
				return nil
			})

			if err != nil {
				log.Printf("⚠️ 赛马单人退款失败: race_id=%s user_id=%d points=%d err=%s", formatPlainValue(raceID), userID, points, formatPlainError(err))
				return
			}
		}

		// 第二步：扣费成功后，再次检查赛马状态。
		// 如果比赛刚好开跑，就退款并删除下注记录。
		globalRace.Mu.Lock()
		if !globalRace.IsActive || globalRace.IsRacing || globalRace.RaceID != raceID {
			globalRace.Mu.Unlock()
			refundRaceBet()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 买定离手，比赛已发车，您的资金已原路退回！", safeName))
			return
		}

		if _, exists := globalRace.Bets[userID]; exists {
			globalRace.Mu.Unlock()
			refundRaceBet()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经下过注了，本次重复请求已退款。", safeName))
			return
		}

		globalRace.Bets[userID] = &PlayerBet{
			UserName: safeName,
			HorseNum: horseNum,
			Points:   points,
		}
		globalRace.TotalPool += points
		globalRace.Mu.Unlock()

		horseIcons := []string{"", "🔴", "🔵", "🟡", "🟢", "🟣"}
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✅ @%s 成功买入 **%d** 积分押注 %s%d号马！", safeName, points, horseIcons[horseNum], horseNum))
	}
}

// ------------------------------------
// 阶段三：跑马转播与智能彩池结算
// ------------------------------------
func runHorseRaceRoutine(bot *tgbotapi.BotAPI, chatID int64) {
	globalRace := getRaceState(chatID)

	raceID := ""
	settled := false

	globalRace.Mu.Lock()
	raceID = globalRace.RaceID
	globalRace.Mu.Unlock()

	// 全生命周期兜底：
	// 只要本函数中途 return、panic、Telegram API 发送失败，且比赛没有完成结算，
	// 就会自动退还本局所有 active 下注。
	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ 赛马协程 panic，准备退款: race_id=%s panic=%s", formatPlainValue(raceID), formatPlainValue(r))
		}

		if raceID != "" && !settled {
			count, points, err := refundRaceBetsByRaceID(raceID, "race routine aborted")
			if err == nil && count > 0 {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf(
					"↩️ **本局赛马异常中止**\n\n系统已自动退还 `%d` 名玩家共 `%d` 积分。",
					count,
					points,
				))
			}
		}

		globalRace.Mu.Lock()
		if globalRace.RaceID == raceID {
			globalRace.IsActive = false
			globalRace.IsRacing = false
			globalRace.LastRaceAt = time.Now()
		}
		globalRace.Mu.Unlock()
	}()

	if raceID == "" {
		return
	}

	time.Sleep(45 * time.Second)

	globalRace.Mu.Lock()
	if !globalRace.IsActive || globalRace.RaceID != raceID {
		globalRace.Mu.Unlock()
		return
	}
	globalRace.Mu.Unlock()

	sendGroupAutoDeleteMessage(bot, chatID, "⏱ **赛马场还有 15 秒停止下注！** 还没买的大佬抓紧最后机会！")

	time.Sleep(15 * time.Second)

	globalRace.Mu.Lock()
	if !globalRace.IsActive || globalRace.RaceID != raceID {
		globalRace.Mu.Unlock()
		return
	}

	globalRace.IsRacing = true
	totalPlayers := len(globalRace.Bets)
	globalRace.Mu.Unlock()

	if totalPlayers == 0 {
		refundRaceBetsByRaceID(raceID, "no players")
		settled = true
		sendGroupAutoDeleteMessage(bot, chatID, "🍂 由于本局无人下注，比赛已自动取消。")
		return
	}

	icons := []string{"🔴", "🔵", "🟡", "🟢", "🟣"}
	positions := []int{0, 0, 0, 0, 0}
	trackLen := 20
	startTime := time.Now()

	buildTrack := func(pos []int, elapsed float64) string {
		res := "🏇 **比赛激烈进行中...** 🏇\n\n"
		for i := 0; i < 5; i++ {
			track := ""
			for j := 0; j < trackLen; j++ {
				if j == pos[i] {
					track += "🐎"
				} else if j == trackLen-1 {
					track += "🏁"
				} else {
					track += "-"
				}
			}

			speed := 0.0
			if elapsed > 0 {
				speed = float64(pos[i]) / elapsed
			}

			res += fmt.Sprintf("%d号 %s [%s] ⏱ %.2f 格/秒\n", i+1, icons[i], track, speed)
		}
		return res
	}

	msg := tgbotapi.NewMessage(chatID, buildTrack(positions, 0))
	sentMsg, err := sendAutoDelete(bot, msg)
	if err != nil {
		log.Printf("⚠️ 赛马动画初始消息发送失败，准备退款: race_id=%s err=%s", formatPlainValue(raceID), formatTelegramSendError(err))
		return
	}

	winner := -1

	for winner == -1 {
		time.Sleep(2 * time.Second)

		var crossers []int

		for i := 0; i < 5; i++ {
			positions[i] += randomIntRange(1, 3)

			if positions[i] >= trackLen-1 {
				positions[i] = trackLen - 1
				crossers = append(crossers, i+1)
			}
		}

		if len(crossers) > 0 {
			if len(crossers) == 1 {
				winner = crossers[0]
			} else {
				winner = crossers[randomIntRange(0, len(crossers)-1)]
			}
		}

		elapsedSeconds := time.Since(startTime).Seconds()
		editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, buildTrack(positions, elapsedSeconds))
		_, apiErr := bot.Request(editMsg)

		// Telegram 频控只跳过动画帧，不影响比赛结算。
		if apiErr != nil {
			if tgErr, ok := apiErr.(*tgbotapi.Error); ok && tgErr.Code == 429 {
				time.Sleep(time.Duration(tgErr.RetryAfter) * time.Second)
			}
		}
	}

	time.Sleep(1 * time.Second)
	finalTime := time.Since(startTime).Seconds()

	globalRace.Mu.Lock()
	if globalRace.RaceID != raceID {
		globalRace.Mu.Unlock()
		return
	}

	memoryUserPool := globalRace.TotalPool
	globalRace.Mu.Unlock()

	betsSnapshot, userPool, err := loadActiveRaceBetsSnapshot(raceID)
	if err != nil {
		log.Printf("鈿狅笍 璧涢┈缁撶畻璇诲彇鏈夋晥涓嬫敞澶辫触: race_id=%s err=%s", formatPlainValue(raceID), formatPlainError(err))
		return
	}
	if userPool != memoryUserPool {
		log.Printf("race settlement db snapshot differs from memory: race_id=%s memory_pool=%d db_pool=%d", formatPlainValue(raceID), memoryUserPool, userPool)
	}
	systemSubsidy := int(float64(userPool) * raceSystemSubsidyRate)
	totalPrizePool := userPool + systemSubsidy

	winnerPool := 0
	for _, bet := range betsSnapshot {
		if bet.HorseNum == winner {
			winnerPool += bet.Points
		}
	}

	prizeRemainder := 0
	if winnerPool > 0 {
		prizeRemainder = totalPrizePool
		for _, bet := range betsSnapshot {
			if bet.HorseNum == winner {
				prizeRemainder -= (bet.Points * totalPrizePool) / winnerPool
			}
		}
		if prizeRemainder < 0 {
			prizeRemainder = 0
		}
	}

	if winnerPool > 0 {
		winList := ""
		poolAfter := 0
		isBurst := false
		expectedWinnerCount := 0
		expectedLoserCount := 0
		for _, bet := range betsSnapshot {
			if bet.HorseNum == winner {
				expectedWinnerCount++
			} else {
				expectedLoserCount++
			}
		}
		err := func() error {
			return runFusionPoolLockedTransaction(func(tx *gorm.DB) error {
				claimedWinnerCount := 0
				for uid, bet := range betsSnapshot {
					if bet.HorseNum == winner {
						winPts := (bet.Points * totalPrizePool) / winnerPool

						claimed, err := updateRaceBetStatusCAS(tx, raceID, uid, RaceBetStatusActive, map[string]interface{}{
							"status": RaceBetStatusSettled,
						})
						if err != nil {
							return err
						}
						if !claimed {
							continue
						}
						claimedWinnerCount++

						if err := applyPointDeltaInTx(
							tx,
							uid,
							winPts,
							"race_win",
							fmt.Sprintf("赛马中奖，获得 %d 积分", winPts),
							"race",
							raceID,
						); err != nil {
							return err
						}

						winList += fmt.Sprintf("👑 @%s : 喜提 `%d` 积分\n", escapeMarkdownPreservingEscapes(bet.UserName), winPts)
					}
				}

				loserRes := tx.Model(&RaceBet{}).
					Where("race_id = ? AND status = ?", raceID, RaceBetStatusActive).
					Update("status", RaceBetStatusSettled)
				if loserRes.Error != nil {
					return loserRes.Error
				}
				if loserRes.RowsAffected != int64(expectedLoserCount) {
					return fmt.Errorf("RACE_LOSER_SETTLEMENT_MISSED")
				}

				if claimedWinnerCount != expectedWinnerCount {
					return fmt.Errorf("RACE_WINNER_SETTLEMENT_MISSED")
				}
				if claimedWinnerCount == 0 {
					prizeRemainder = 0
				}

				if prizeRemainder > 0 {
					var err error
					poolAfter, isBurst, err = addPointsToFusionPoolInTx(tx, prizeRemainder)
					if err != nil {
						return err
					}
				}

				return nil
			})
		}()

		if err != nil {
			log.Printf("⚠️ 赛马结算失败，准备退款: race_id=%s err=%s", formatPlainValue(raceID), formatPlainError(err))
			return
		}
		settled = true
		poolNotice := ""
		if prizeRemainder > 0 {
			poolNotice = fmt.Sprintf(
				"\n🌊 奖池瓜分余数 `%d` 积分已注入天道奖池，当前水位：`%d/300`。",
				prizeRemainder,
				poolAfter,
			)

			if isBurst {
				poolNotice += "\n🎁 天道奖池已满，系统自动生成 `300` 积分灵气红包！"
				notifyFusionPoolBurst(bot, chatID, "皇家赛马场结算余数归入天道")
			}
		}

		finalAnnounce := fmt.Sprintf(
			"🏆 **比赛结束！** 🏆\n\n"+
				"🎉 恭喜 **%d号马 (%s)** 历时 **%.2f 秒** 勇夺冠军！\n\n"+
				"💰 玩家总筹码: `%d` 积分\n"+
				"🤖 庄家配资(%d%%): `+%d` 积分\n"+
				"📊 最终大奖池: `%d` 积分\n\n"+
				"**🤑 获胜名单 (按比例瓜分)：**\n%s%s",
			winner,
			icons[winner-1],
			finalTime,
			userPool,
			raceSystemSubsidyPercent,
			systemSubsidy,
			totalPrizePool,
			winList,
			poolNotice,
		)

		sendGroupAutoDeleteMessage(bot, chatID, finalAnnounce)
		return
	}

	poolAfter := 0
	isBurst := false
	err = func() error {
		return runFusionPoolLockedTransaction(func(tx *gorm.DB) error {
			res := tx.Model(&RaceBet{}).
				Where("race_id = ? AND status = ?", raceID, RaceBetStatusActive).
				Update("status", RaceBetStatusSettled)
			if res.Error != nil {
				return res.Error
			}
			if userPool > 0 && res.RowsAffected == 0 {
				return fmt.Errorf("RACE_BET_SETTLEMENT_MISSED")
			}

			if userPool > 0 {
				var err error
				poolAfter, isBurst, err = addPointsToFusionPoolInTx(tx, userPool)
				if err != nil {
					return err
				}
			}

			return nil
		})
	}()

	if err != nil {
		log.Printf("⚠️ 赛马庄家通吃结算失败，准备退款: race_id=%s err=%s", formatPlainValue(raceID), formatPlainError(err))
		return
	}

	settled = true

	poolNotice := "🌊 本局无有效玩家筹码可注入天道奖池。"
	if userPool > 0 {
		poolNotice = fmt.Sprintf("🌊 已自动注入天道奖池：`+%d` 积分，当前水位：`%d/300`。", userPool, poolAfter)
		if isBurst {
			poolNotice += "\n🎁 天道奖池已满，系统自动生成 `300` 积分灵气红包！"
			notifyFusionPoolBurst(bot, chatID, "皇家赛马场无人押中，系统通吃筹码注入天道")
		}
	}

	finalAnnounce := fmt.Sprintf(
		"🏆 **比赛结束！** 🏆\n\n"+
			"🎉 冠军是冷门黑马 **%d号 (%s)**，历时 **%.2f 秒**！\n\n"+
			"🍂 **系统通吃**：本局无人押中冠军，玩家下注的 `%d` 积分由系统赢得。\n"+
			"%s",
		winner,
		icons[winner-1],
		finalTime,
		userPool,
		poolNotice,
	)

	sendGroupAutoDeleteMessage(bot, chatID, finalAnnounce)
}
