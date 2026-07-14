package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ==========================================
// 📿 悦耳修仙传专属数据表
// ==========================================
type Cultivation struct {
	ID               uint      `gorm:"primaryKey"`
	UserID           int64     `gorm:"index"`     // 绑定 TelegramID
	TotalAudioTime   float64   `gorm:"default:0"` // 🎧 累计听书时长（小时）- 纯闭关苦修
	PillAudioTime    float64   `gorm:"default:0"` // 💊 累计丹药时长（小时）- 氪金药力加成
	MajorRealm       int       `gorm:"default:0"` // 大境界 (0:凡人, 1:炼气, 2:筑基, 3:结丹, 4:元婴, 5:化神)
	MinorRealm       int       `gorm:"default:1"` // 小段位 (1:初期, 2:中期, 3:后期, 4:圆满)
	TribulationFails int       `gorm:"default:0"` // 渡劫失败次数计数器（暗藏天道保底）
	ConsolidateUntil time.Time // 境界巩固期（防止老玩家光速吃药连升的冷却时间）
}

func cultivationErrorCode(err error) string {
	switch {
	case errors.Is(err, errCultivationNotFound):
		return "CULTIVATION_NOT_FOUND"
	case errors.Is(err, errMaxRealmReached):
		return "MAX_REALM_REACHED"
	case errors.Is(err, errConsolidating):
		return "CONSOLIDATING"
	case errors.Is(err, errBreakthroughNotReady):
		return "NOT_READY"
	case errors.Is(err, errInsufficientCultivation):
		return "INSUFFICIENT_CULTIVATION"
	case errors.Is(err, errInsufficientPoints):
		return "INSUFFICIENT_POINTS"
	case errors.Is(err, errNoBreakthroughPill):
		return "NO_PILL"
	case errors.Is(err, errInvalidBreakthroughMode):
		return "INVALID_BREAKTHROUGH_MODE"
	case errors.Is(err, errCultivationStateChanged):
		return "CULTIVATION_STATE_CHANGED"
	case errors.Is(err, errRandomFailed):
		return "RANDOM_FAILED"
	case errors.Is(err, errUserNotFound):
		return "USER_NOT_FOUND"
	case err != nil:
		return fallbackBusinessErrorCode(err)
	default:
		return ""
	}
}

// ==========================================
// 🔮 修仙系统核心逻辑引擎
// ==========================================

// GetOrCreateCultivation 获取或初始化用户的仙籍
func GetOrCreateCultivation(userID int64) *Cultivation {
	var cul Cultivation

	err := DB.Where("user_id = ?", userID).First(&cul).Error
	if err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("⚠️ 读取修仙档案失败: user=%d err=%s", userID, formatPlainError(err))
			return nil
		}

		created, err := createCultivationIfMissing(userID)
		if err != nil {
			log.Printf("⚠️ 创建修仙档案失败: user=%d err=%s", userID, formatPlainError(err))
			return nil
		}
		cul = *created
	}

	// 每次读取时，根据当前累计听书时长同步当前大境界内的最高小段位
	SyncCultivationRealm(&cul)
	return &cul
}

func createCultivationIfMissing(userID int64) (*Cultivation, error) {
	if userID == 0 {
		return nil, fmt.Errorf("CULTIVATION_CREATE_INVALID")
	}
	cul := Cultivation{
		UserID:           userID,
		TotalAudioTime:   0,
		PillAudioTime:    0,
		MajorRealm:       0,
		MinorRealm:       1,
		TribulationFails: 0,
		ConsolidateUntil: time.Now().Add(-24 * time.Hour),
	}
	res := DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&cul)
	if res.Error != nil {
		return nil, res.Error
	}
	if res.RowsAffected == 0 {
		var existing Cultivation
		if err := DB.Where("user_id = ?", userID).First(&existing).Error; err != nil {
			return nil, err
		}
		return &existing, nil
	}
	return &cul, nil
}

// GetRealmName 获取文本名称，例如 【结丹中期】
func GetRealmName(cul *Cultivation) string {
	if cul == nil {
		return "【未知境界】"
	}

	realmTitle := getCultivationRealmTitle(cul.MajorRealm)
	if realmTitle == "" {
		majors := []string{"一介凡人", "炼气", "筑基", "结丹", "元婴", "化神"}
		if cul.MajorRealm == 0 {
			return "【平平无奇的凡人】"
		}
		if cul.MajorRealm >= len(majors) {
			return "【震古烁今大能】"
		}
		realmTitle = majors[cul.MajorRealm]
	}

	if cul.MajorRealm == 0 {
		return fmt.Sprintf("【%s】", realmTitle)
	}

	minorName := getCultivationMinorName(cul.MajorRealm, cul.MinorRealm)
	if minorName == "" {
		minors := []string{"", "初期", "中期", "后期", "圆满"}
		if cul.MinorRealm > 0 && cul.MinorRealm < len(minors) {
			minorName = minors[cul.MinorRealm]
		}
	}

	return fmt.Sprintf("【%s%s】", realmTitle, minorName)
}

// SyncCultivationRealm 根据总修为（苦修+药力），平滑自动晋升当前大境界内的“小段位”
func SyncCultivationRealm(cul *Cultivation) {
	if cul == nil {
		return
	}

	h := cul.TotalAudioTime + cul.PillAudioTime
	oldMajor := cul.MajorRealm
	oldMinor := cul.MinorRealm

	rules := GetCultivationRules()
	if rules == nil {
		return
	}

	// 凡人阶段只允许按配置从 0 进入第一个突破目标，不自动跨越后续大境界。
	if cul.MajorRealm == 0 {
		if req, ok := getBreakthroughSettingFromRules(0); ok && h >= req.MinHours {
			cul.MajorRealm = req.ToMajorRealm
			cul.MinorRealm = 1
		}
	}

	// 小境界完全由 cultivation_minor_realm_configs.required_hours 驱动。
	if cul.MajorRealm > 0 {
		minors := rules.MinorRealms[cul.MajorRealm]
		if len(minors) > 0 {
			bestMinor := 1
			for _, minor := range minors {
				if h >= minor.RequiredHours && minor.MinorRealm > bestMinor {
					bestMinor = minor.MinorRealm
				}
			}
			cul.MinorRealm = bestMinor
		}
	}

	if cul.MinorRealm < 1 {
		cul.MinorRealm = 1
	}
	if cul.MinorRealm > 4 {
		cul.MinorRealm = 4
	}

	if oldMajor != cul.MajorRealm || oldMinor != cul.MinorRealm {
		// 只更新本函数负责的列（小段位，以及凡人自动进阶时的大境界），
		// 并以读取时的大境界为条件做乐观锁。
		// 后台批量同步（notifier/宗门刷新/世界Boss加成）不持有用户级锁，
		// 若沿用全行 DB.Save(cul)，会用陈旧快照覆盖用户刚突破写入的
		// major_realm / consolidate_until / tribulation_fails，
		// 造成“扣了资源却退回原境界”的资产损失；这里改为定向条件更新规避。
		updates := map[string]interface{}{"minor_realm": cul.MinorRealm}
		if oldMajor != cul.MajorRealm {
			updates["major_realm"] = cul.MajorRealm
		}
		res := DB.Model(&Cultivation{}).
			Where("user_id = ? AND major_realm = ?", cul.UserID, oldMajor).
			Updates(updates)
		if res.Error != nil {
			log.Printf("⚠️ 同步修仙段位失败: user=%d err=%s", cul.UserID, formatPlainError(res.Error))
		} else if res.RowsAffected == 0 {
			log.Printf("⚠️ 同步修仙段位未命中: user=%d old_major=%d new_major=%d new_minor=%d", cul.UserID, oldMajor, cul.MajorRealm, cul.MinorRealm)
		}
	}
}

// persistCultivationAudioTime 只更新 total_audio_time 列。
// 听书时长同步多由后台批量任务触发（不持用户级锁），必须避免全行
// DB.Save(cul) 用陈旧快照覆盖用户并发突破写入的 major_realm /
// minor_realm / consolidate_until / tribulation_fails / pill_audio_time。
func persistCultivationAudioTime(userID int64, totalAudioTime float64) {
	if userID == 0 {
		return
	}
	if totalAudioTime < 0 {
		totalAudioTime = 0
	}
	res := DB.Model(&Cultivation{}).
		Where("user_id = ?", userID).
		UpdateColumn("total_audio_time", totalAudioTime)
	if res.Error != nil {
		log.Printf("⚠️ 更新累计听书时长失败: user=%d err=%s", userID, formatPlainError(res.Error))
	} else if res.RowsAffected == 0 {
		log.Printf("⚠️ 更新累计听书时长未命中: user=%d total_audio_time=%.2f", userID, totalAudioTime)
	}
}

// ==========================================
// ⚡️ 逆天而行：渡劫核心网关 (全智能自动化版)
// ==========================================

type BreakthroughSetting struct {
	FromMajorRealm int
	ToMajorRealm   int

	PillName    string
	PointsCost  int
	MinHours    float64
	SuccessRate float64
	Cooldown    time.Duration

	GuaranteeFailCount int
	RefundRate         float64
	FailPenaltyPoints  int

	SplashMinMajorRealm int
	SplashVictimCount   int
	SplashPenaltyPoints int
}

func getBreakthroughSettingFromRules(fromMajor int) (BreakthroughSetting, bool) {
	rules := GetCultivationRules()
	if rules == nil {
		return BreakthroughSetting{}, false
	}

	cfg, ok := rules.Breakthroughs[fromMajor]
	if !ok || !cfg.Enabled {
		return BreakthroughSetting{}, false
	}

	return BreakthroughSetting{
		FromMajorRealm:      cfg.FromMajorRealm,
		ToMajorRealm:        cfg.ToMajorRealm,
		PillName:            cfg.PillName,
		PointsCost:          cfg.PointsCost,
		MinHours:            cfg.MinTotalHours,
		SuccessRate:         cfg.SuccessRate,
		Cooldown:            time.Duration(cfg.CooldownHours) * time.Hour,
		GuaranteeFailCount:  cfg.GuaranteeFailCount,
		RefundRate:          cfg.RefundRate,
		FailPenaltyPoints:   cfg.FailPenaltyPoints,
		SplashMinMajorRealm: cfg.SplashMinMajorRealm,
		SplashVictimCount:   cfg.SplashVictimCount,
		SplashPenaltyPoints: cfg.SplashPenaltyPoints,
	}, true
}

func isMaxCultivationRealm(major int) bool {
	rules := GetCultivationRules()
	if rules == nil {
		return major >= 5
	}

	realm, ok := rules.Realms[major]
	return ok && realm.IsMaxRealm
}

func getCultivationRealmTitle(major int) string {
	rules := GetCultivationRules()
	if rules == nil {
		return ""
	}

	realm, ok := rules.Realms[major]
	if !ok {
		return ""
	}

	if strings.TrimSpace(realm.TitleName) != "" {
		return realm.TitleName
	}

	return realm.Name
}

func getCultivationMinorName(major int, minor int) string {
	rules := GetCultivationRules()
	if rules == nil {
		return ""
	}

	for _, cfg := range rules.MinorRealms[major] {
		if cfg.MinorRealm == minor {
			return cfg.Name
		}
	}

	return ""
}

// BreakthroughAttempt 记录每一次突破尝试。
// 作用：
// 1. 审计随机 roll、成功率、是否保底；
// 2. 记录消耗的积分 / 丹药、失败惩罚、成功返还；
// 3. 方便用户争议和运维排障。
type BreakthroughAttempt struct {
	gorm.Model

	UserID int64 `gorm:"index;not null"`

	FromMajor int
	FromMinor int
	ToMajor   int
	ToMinor   int

	Mode     string `gorm:"index"` // USE_INVENTORY / AUTO_BUY
	PillName string `gorm:"index"`

	PointsCost   int
	PillConsumed bool

	SuccessRate  float64
	Roll         int
	IsGuaranteed bool

	Result string `gorm:"index"` // success / failed
	Status string `gorm:"index"` // settled

	ResourceCostPoints int
	FailPenaltyPoints  int
	RefundPoints       int

	Detail string
}

func (BreakthroughAttempt) TableName() string {
	return "breakthrough_attempts"
}

func createBreakthroughAttemptInTx(tx *gorm.DB, attempt *BreakthroughAttempt) error {
	if tx == nil || attempt == nil {
		return fmt.Errorf("BREAKTHROUGH_ATTEMPT_INVALID")
	}
	entry := *attempt
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("BREAKTHROUGH_ATTEMPT_CREATE_MISSED")
	}
	*attempt = entry
	return nil
}

// 1. 渡劫前置扫描预检
func HandleBreakthroughRequest(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	userID := msg.From.ID
	chatID := msg.Chat.ID
	userName := msg.From.UserName
	if userName == "" {
		userName = msg.From.FirstName
	}
	safeName := escapeMarkdown(userName)

	cul := GetOrCreateCultivation(userID)
	if cul == nil {
		log.Printf("⚠️ 突破前修仙档案读取失败: user=%d", userID)
		replyText(bot, chatID, "❌ 修仙档案读取失败，请稍后再尝试突破。")
		return
	}

	if time.Now().Before(cul.ConsolidateUntil) {
		loc := time.FixedZone("CST", 8*3600)
		unlockTime := cul.ConsolidateUntil.In(loc).Format("2006-01-02 15:04")
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("⚠️ @%s 您刚刚突破境界，气血翻涌、根基不稳！\n\n请静心打坐巩固修为，**%s** 之后方可再次引动雷劫！", safeName, unlockTime))
		return
	}

	if cul.MinorRealm != 4 && cul.MajorRealm != 0 {
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 您的修为尚未达到当前境界的【大圆满】瓶颈，盲目渡劫必会爆体身亡！请继续闭关听书打磨气血。", safeName))
		return
	}

	req, exists := getBreakthroughSettingFromRules(cul.MajorRealm)
	if !exists || isMaxCultivationRealm(cul.MajorRealm) {
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("⚡️ @%s 您已达到本界天道天花板，无法继续在人界突破！", safeName))
		return
	}

	// 🚨 判定标准升级：苦修 + 药力 双轨总修为
	totalH := cul.TotalAudioTime + cul.PillAudioTime
	if totalH < req.MinHours {
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 突破需要总修为满 `%.1f` 小时！您当前总计仅有 `%.1f` 小时，强行渡劫必遭反噬！", safeName, req.MinHours, totalH))
		return
	}

	var u User
	if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		log.Printf("⚠️ 突破前钱包读取失败: user=%d err=%s", userID, formatPlainError(err))
		replyText(bot, chatID, "❌ 钱包读取失败，请稍后再尝试突破。")
		return
	}

	hasPill := false
	if req.PointsCost > 0 {
		var inv Inventory
		err := DB.Where("user_id = ? AND item_name = ?", userID, req.PillName).First(&inv).Error
		if err == nil && inv.Quantity > 0 {
			hasPill = true
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("⚠️ 突破前丹药库存读取失败: user=%d item=%s err=%s", userID, formatPlainValue(req.PillName), formatPlainError(err))
			replyText(bot, chatID, "❌ 乾坤袋读取失败，请稍后再尝试突破。")
			return
		}
	}

	session := getSession(userID)

	if req.PointsCost == 0 {
		// 凡人引灵入体
		session.SetTemp("bt_mode", "USE_INVENTORY")
		session.SetStep("WAITING_CONFIRM_BREAKTHROUGH")
		replyText(bot, chatID, "⚡️ 检测到您闭关苦修已感知天地灵气，是否立即【引灵入体】，正式踏上仙途？\n👉 请回复 `确认渡劫` 或 `取消`。")
		UserSessions.Store(userID, session)
	} else if hasPill {
		// 背包有药
		session.SetTemp("bt_mode", "USE_INVENTORY")
		session.SetStep("WAITING_CONFIRM_BREAKTHROUGH")
		replyText(bot, chatID, fmt.Sprintf("⚡️ 检测到乾坤袋中备有**【%s】**，是否立即吞服并引动雷劫？\n👉 请回复 `确认渡劫` 或 `取消`。", inventoryItemMarkdownName(req.PillName)))
		UserSessions.Store(userID, session)
	} else if u.Points >= req.PointsCost {
		// 背包无药，但钱够代购
		session.SetTemp("bt_mode", "AUTO_BUY")
		session.SetStep("WAITING_CONFIRM_BREAKTHROUGH")
		replyText(bot, chatID, fmt.Sprintf("⚡️ 您乾坤袋中暂无【%s】。\n💰 您的积分充足，是否授权天道商行自动扣除 `%d` 积分代购**【%s】**并立即开始渡劫？\n👉 请回复 `确认代购并渡劫` 或 `取消`。", inventoryItemMarkdownName(req.PillName), req.PointsCost, inventoryItemMarkdownName(req.PillName)))
		UserSessions.Store(userID, session)
	} else {
		// 没钱没药，无情驳回
		replyText(bot, chatID, fmt.Sprintf("❌ 突破失败。您既无**【%s】**，也缺少积分在聚宝斋购买（当前仅有 `%d` 积分，需要 `%d` 积分）。请努力积攒灵石！", inventoryItemMarkdownName(req.PillName), u.Points, req.PointsCost))
	}
}

func applyPointDeltaInTx(
	tx *gorm.DB,
	userID int64,
	delta int,
	txType string,
	description string,
	refType string,
	refID string,
) error {
	if tx == nil {
		return fmt.Errorf("数据库事务为空")
	}

	if userID == 0 {
		return fmt.Errorf("用户ID为空")
	}

	if delta == 0 {
		return nil
	}

	updateQuery := tx.Model(&User{}).Where("telegram_id = ?", userID)

	// 扣积分时加上余额条件，避免扣成负数。
	if delta < 0 {
		updateQuery = updateQuery.Where("points >= ?", -delta)
	}

	res := updateQuery.UpdateColumn("points", gorm.Expr("points + ?", delta))
	if res.Error != nil {
		return res.Error
	}

	if res.RowsAffected == 0 {
		var exists int64
		if err := tx.Model(&User{}).
			Where("telegram_id = ?", userID).
			Count(&exists).Error; err != nil {
			return err
		}

		if exists == 0 {
			return errUserNotFound
		}

		if delta < 0 {
			return errPointsNotEnough
		}

		return fmt.Errorf("POINT_UPDATE_FAILED")
	}

	var afterUser User
	if err := tx.Select("telegram_id", "username", "points").
		Where("telegram_id = ?", userID).
		First(&afterUser).Error; err != nil {
		return err
	}

	afterPoints := afterUser.Points
	beforePoints := afterPoints - delta

	txRes := tx.Create(&PointTransaction{
		UserID:        userID,
		UserName:      afterUser.Username,
		Type:          txType,
		Delta:         delta,
		BalanceBefore: beforePoints,
		BalanceAfter:  afterPoints,
		Description:   description,
		RefType:       refType,
		RefID:         refID,
	})
	if txRes.Error != nil {
		return txRes.Error
	}
	if txRes.RowsAffected == 0 {
		return fmt.Errorf("POINT_TRANSACTION_CREATE_MISSED")
	}
	return nil
}

// 2. 渡劫底层处决引擎
func ExecuteBreakthrough(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, mode string) {
	if msg == nil || msg.From == nil {
		return
	}

	userID := msg.From.ID
	chatID := msg.Chat.ID

	userName := msg.From.UserName
	if userName == "" {
		userName = msg.From.FirstName
	}
	safeName := escapeMarkdown(userName)
	now := time.Now()

	var (
		newMajor            int
		newMinor            int
		pillName            string
		roll                int
		successRate         float64
		isGuaranteed        bool
		isSuccess           bool
		refundPoints        int
		failPenaltyPoints   int
		tribulationFails    int
		guaranteeFailCount  int
		splashPenaltyPoints int
		attemptID           uint
		victimNames         []string
		victimIDs           []int64
	)

	err := DB.Transaction(func(tx *gorm.DB) error {
		var cul Cultivation
		if err := tx.Where("user_id = ?", userID).First(&cul).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errCultivationNotFound
			}
			return err
		}

		req, exists := getBreakthroughSettingFromRules(cul.MajorRealm)
		if !exists || isMaxCultivationRealm(cul.MajorRealm) {
			return errMaxRealmReached
		}

		pillName = req.PillName
		successRate = req.SuccessRate
		guaranteeFailCount = req.GuaranteeFailCount
		splashPenaltyPoints = req.SplashPenaltyPoints

		// 事务内再次校验冷却，避免确认后状态变化。
		if now.Before(cul.ConsolidateUntil) {
			return errConsolidating
		}

		// 事务内再次校验是否大圆满。
		if cul.MinorRealm != 4 && cul.MajorRealm != 0 {
			return errBreakthroughNotReady
		}

		// 事务内再次校验总修为。
		totalH := cul.TotalAudioTime + cul.PillAudioTime
		if totalH < req.MinHours {
			return errInsufficientCultivation
		}

		resourceCostPoints := 0
		pillConsumed := false

		// 1. 扣突破资源：自动代购扣积分，背包模式扣丹药。
		if mode == "AUTO_BUY" {
			if req.PointsCost <= 0 {
				// 凡人引灵入体不需要代购。
				return errInvalidBreakthroughMode
			}

			if err := applyPointDeltaInTx(
				tx,
				userID,
				-req.PointsCost,
				"breakthrough_auto_buy",
				fmt.Sprintf("突破自动代购【%s】，消耗 %d 积分", cultivationPointDescriptionName(req.PillName), req.PointsCost),
				"breakthrough",
				fmt.Sprintf("major:%d", cul.MajorRealm),
			); err != nil {
				if errors.Is(err, errPointsNotEnough) {
					return errInsufficientPoints
				}
				return err
			}

			resourceCostPoints = req.PointsCost

		} else if mode == "USE_INVENTORY" && req.PointsCost > 0 {
			res := tx.Model(&Inventory{}).
				Where("user_id = ? AND item_name = ? AND quantity > 0", userID, req.PillName).
				UpdateColumn("quantity", gorm.Expr("quantity - 1"))

			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errNoBreakthroughPill
			}

			pillConsumed = true

		} else if mode != "USE_INVENTORY" {
			return errInvalidBreakthroughMode
		}

		// 2. 生成随机结果。随机结果在事务内落库，避免扣资源后无结果。
		isGuaranteed = req.GuaranteeFailCount > 0 && cul.TribulationFails >= req.GuaranteeFailCount
		finalRate := req.SuccessRate
		if isGuaranteed {
			finalRate = 1.0
		}

		nBig, err := rand.Int(rand.Reader, big.NewInt(100))
		if err != nil {
			return errRandomFailed
		}
		roll = int(nBig.Int64())

		isSuccess = isGuaranteed || roll < int(finalRate*100)

		if isSuccess {
			newMajor = req.ToMajorRealm
			newMinor = 1
			nextConsolidate := now.Add(req.Cooldown)

			// 3A. 成功：更新境界。
			res := tx.Model(&Cultivation{}).
				Where("user_id = ? AND major_realm = ? AND minor_realm = ?", userID, cul.MajorRealm, cul.MinorRealm).
				Updates(map[string]interface{}{
					"major_realm":       newMajor,
					"minor_realm":       newMinor,
					"tribulation_fails": 0,
					"consolidate_until": nextConsolidate,
				})

			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errCultivationStateChanged
			}

			// 成功返还 20% 代购成本。使用背包丹药时，按丹药标价返还。
			refundPoints = int(float64(req.PointsCost) * req.RefundRate)
			if refundPoints > 0 {
				if err := applyPointDeltaInTx(
					tx,
					userID,
					refundPoints,
					"breakthrough_refund",
					fmt.Sprintf("突破成功，天道返还 %d 积分", refundPoints),
					"breakthrough",
					fmt.Sprintf("major:%d->%d", cul.MajorRealm, newMajor),
				); err != nil {
					return err
				}
			}

			attempt := BreakthroughAttempt{
				UserID:             userID,
				FromMajor:          cul.MajorRealm,
				FromMinor:          cul.MinorRealm,
				ToMajor:            newMajor,
				ToMinor:            newMinor,
				Mode:               mode,
				PillName:           req.PillName,
				PointsCost:         req.PointsCost,
				PillConsumed:       pillConsumed,
				SuccessRate:        req.SuccessRate,
				Roll:               roll,
				IsGuaranteed:       isGuaranteed,
				Result:             "success",
				Status:             "settled",
				ResourceCostPoints: resourceCostPoints,
				FailPenaltyPoints:  0,
				RefundPoints:       refundPoints,
				Detail:             "突破成功",
			}

			if err := createBreakthroughAttemptInTx(tx, &attempt); err != nil {
				return err
			}

			attemptID = attempt.ID
			if err := awardSectContributionTx(
				tx,
				userID,
				20,
				fmt.Sprintf("大境界突破成功：%d -> %d", newMajor-1, newMajor),
				"breakthrough_success",
				fmt.Sprintf("%d", attemptID),
			); err != nil {
				return err
			}
			return nil
		}

		// 3B. 失败：增加失败次数。
		tribulationFails = cul.TribulationFails + 1

		res := tx.Model(&Cultivation{}).
			Where("user_id = ? AND major_realm = ? AND minor_realm = ?", userID, cul.MajorRealm, cul.MinorRealm).
			UpdateColumn("tribulation_fails", gorm.Expr("tribulation_fails + ?", 1))

		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errCultivationStateChanged
		}

		// 失败调养费：最多扣 50，余额不足则扣剩余全部，文案按实际扣款展示。
		var txUser User
		if err := tx.Select("telegram_id", "username", "points").
			Where("telegram_id = ?", userID).
			First(&txUser).Error; err != nil {
			return err
		}

		failPenaltyPoints = req.FailPenaltyPoints
		if txUser.Points < failPenaltyPoints {
			failPenaltyPoints = txUser.Points
		}
		if failPenaltyPoints < 0 {
			failPenaltyPoints = 0
		}

		if failPenaltyPoints > 0 {
			if err := applyPointDeltaInTx(
				tx,
				userID,
				-failPenaltyPoints,
				"breakthrough_fail_penalty",
				fmt.Sprintf("突破失败，扣除调养费 %d 积分", failPenaltyPoints),
				"breakthrough",
				fmt.Sprintf("major:%d", cul.MajorRealm),
			); err != nil {
				return err
			}
		}

		// 高境界失败时，雷劫外溢扣其他普通用户积分。
		if cul.MajorRealm >= req.SplashMinMajorRealm &&
			req.SplashVictimCount > 0 &&
			req.SplashPenaltyPoints > 0 {
			var victimCandidates []User
			// 避免在事务内 ORDER BY RANDOM() 全表排序。
			// 先取一批候选，再在内存中随机挑选，减少 SQLite 写锁持有时间。
			candidateLimit := req.SplashVictimCount * 10
			if candidateLimit < 20 {
				candidateLimit = 20
			}
			if candidateLimit > 200 {
				candidateLimit = 200
			}

			if err := tx.Where("telegram_id != ? AND points >= ? AND role = ?", userID, req.SplashPenaltyPoints, "user").
				Order("id DESC").
				Limit(candidateLimit).
				Find(&victimCandidates).Error; err != nil {
				return err
			}

			victims := make([]User, 0, req.SplashVictimCount)
			usedVictimIndex := make(map[int]bool)

			for len(victims) < req.SplashVictimCount && len(usedVictimIndex) < len(victimCandidates) {
				nBig, err := rand.Int(rand.Reader, big.NewInt(int64(len(victimCandidates))))
				if err != nil {
					return errRandomFailed
				}

				idx := int(nBig.Int64())
				if usedVictimIndex[idx] {
					continue
				}

				usedVictimIndex[idx] = true
				victims = append(victims, victimCandidates[idx])
			}

			for _, v := range victims {
				if err := applyPointDeltaInTx(
					tx,
					v.TelegramID,
					-req.SplashPenaltyPoints,
					"breakthrough_splash_penalty",
					fmt.Sprintf("雷劫外溢，被扣除 %d 积分医疗费", req.SplashPenaltyPoints),
					"breakthrough",
					fmt.Sprintf("source:%d", userID),
				); err != nil {
					// 雷劫外溢是对第三方的副作用，必须尽力而为：
					// 候选人余额在“选取→扣款”之间被并发消费而不足，
					// 或账号刚被删除时，跳过该受害者即可，绝不能因此
					// 回滚突破发起者已经结算成功的境界与扣费。
					// 仅当遇到真正的数据库错误时才中断事务。
					if errors.Is(err, errPointsNotEnough) || errors.Is(err, errUserNotFound) {
						continue
					}
					return err
				}

				victimIDs = append(victimIDs, v.TelegramID)
				victimNames = append(victimNames, escapeMarkdown(v.Username))
			}
		}

		attempt := BreakthroughAttempt{
			UserID:             userID,
			FromMajor:          cul.MajorRealm,
			FromMinor:          cul.MinorRealm,
			ToMajor:            cul.MajorRealm,
			ToMinor:            cul.MinorRealm,
			Mode:               mode,
			PillName:           req.PillName,
			PointsCost:         req.PointsCost,
			PillConsumed:       pillConsumed,
			SuccessRate:        req.SuccessRate,
			Roll:               roll,
			IsGuaranteed:       isGuaranteed,
			Result:             "failed",
			Status:             "settled",
			ResourceCostPoints: resourceCostPoints,
			FailPenaltyPoints:  failPenaltyPoints,
			RefundPoints:       0,
			Detail:             fmt.Sprintf("突破失败，失败次数变为 %d", tribulationFails),
		}

		if err := createBreakthroughAttemptInTx(tx, &attempt); err != nil {
			return err
		}

		attemptID = attempt.ID
		return nil
	})

	if err != nil {
		switch cultivationErrorCode(err) {
		case "CULTIVATION_NOT_FOUND":
			replyText(bot, chatID, "❌ 未找到您的修仙档案，请先发送 `听书报告` 或 `我的信息` 初始化档案。")
		case "MAX_REALM_REACHED":
			replyText(bot, chatID, "⚡️ 您已达到本界天道天花板，无法继续突破。")
		case "CONSOLIDATING":
			replyText(bot, chatID, "⚠️ 您仍处于境界巩固期，暂时无法再次渡劫。")
		case "NOT_READY":
			replyText(bot, chatID, "❌ 您尚未达到当前境界大圆满，无法引动雷劫。")
		case "INSUFFICIENT_CULTIVATION":
			replyText(bot, chatID, "❌ 当前总修为不足，无法突破。")
		case "INSUFFICIENT_POINTS":
			replyText(bot, chatID, "❌ 积分不足，无法自动代购突破丹药。")
		case "NO_PILL":
			replyText(bot, chatID, "❌ 乾坤袋内突破丹药不足，无法渡劫。")
		case "INVALID_BREAKTHROUGH_MODE":
			replyText(bot, chatID, "❌ 突破模式异常，请重新发送 `突破` 指令。")
		case "CULTIVATION_STATE_CHANGED":
			replyText(bot, chatID, "⚠️ 您的境界状态刚刚发生变化，请重新发送 `突破` 指令确认。")
		case "RANDOM_FAILED":
			replyText(bot, chatID, "❌ 天机骰盅异常，突破未执行，资源未扣除，请稍后重试。")
		default:
			log.Printf("❌ 突破事务失败: user=%d mode=%s err=%s", userID, formatPlainValue(mode), formatPlainError(err))
			replyText(bot, chatID, "❌ 渡劫执行失败，本次资源未扣除，请稍后重试。")
		}
		return
	}

	// 事务提交后再发 Telegram 消息、通知大群、注入天道奖池。
	// 这些都是外部动作，不能放进数据库事务内。
	if isSuccess {
		currentPool := 0
		isBurst := false
		poolInjected := true

		if refundPoints > 0 {
			var err error
			currentPool, isBurst, err = addPointsToFusionPoolWithError(refundPoints)
			if err != nil {
				poolInjected = false
				log.Printf("⚠️ 突破成功后天道奖池注入失败: user=%d attempt=%d points=%d err=%s", userID, attemptID, refundPoints, formatPlainError(err))
			}
		}

		progressText := ""
		if refundPoints > 0 && !poolInjected {
			progressText = "\n\n*(⚠️ 天道奖池注入暂未完成，系统已记录异常，请联系管理员核查。)*"
		} else if refundPoints > 0 && !isBurst {
			progressText = fmt.Sprintf(
				"\n\n*(📈 此时天道灵气池已汇聚 `%d/300` 积分，蓄满将降下全服大红包！)*",
				currentPool,
			)
		}

		newRealmName := GetRealmName(&Cultivation{
			MajorRealm: newMajor,
			MinorRealm: newMinor,
		})
		newRealmName = strings.TrimPrefix(strings.TrimSuffix(newRealmName, "】"), "【")

		announce := fmt.Sprintf(
			"⚡️💥 **【诸天异象·白日飞升】** 💥⚡️\n\n"+
				"恭喜道友 @%s 吞服【%s】，成功历经九重天劫！\n"+
				"✨ 恭迎阁下晋升至全新的 **【%s】** 境界！\n\n"+
				"🎲 天机判定：成功率 `%.0f%%`，本次掷点 `%d/100`，保底：`%t`\n"+
				"🧾 突破记录：`#%d`\n"+
				"💰 **天道恩赐**：晋升成功，返还本人 `%d` 积分！%s",
			safeName,
			pillName,
			newRealmName,
			successRate*100,
			roll,
			isGuaranteed,
			attemptID,
			refundPoints,
			progressText,
		)

		func() {
			msg := tgbotapi.NewMessage(chatID, announce)
			if chatID < 0 {
				if !enqueueAutoDelete(bot, msg, "cultivation_breakthrough_success", telegramAsyncPriorityNormal, fmt.Sprintf("cultivation_success:%d", attemptID)) {
					log.Printf("发送修仙突破成功公告入队失败: chat=%d user=%d attempt=%d", chatID, userID, attemptID)
				}
				return
			}
			if _, err := sendAutoDelete(bot, msg); err != nil {
				log.Printf("发送修仙突破成功公告失败: chat=%d user=%d attempt=%d err=%s", chatID, userID, attemptID, formatTelegramSendError(err))
			}
		}()

		if isBurst && AppConfig.NoticeGroupID != 0 {
			poolAnnounce := fmt.Sprintf(
				"%s\n\n"+
					"🌈 **【天降甘霖·仙气化雨】** 🌈\n"+
					"因道友突破引动天地异象，天道奖池已蓄满并自动爆开！\n\n"+
					"💰 降下红包: `300` 积分\n"+
					"📦 福泽份数: `30` 份\n\n"+
					"👇 众修士快回复关键字 【`沾仙气`】 汲取天地造化！",
				announce,
			)
			sendGroupAutoDeleteMessageAsync(bot, AppConfig.NoticeGroupID, poolAnnounce, "cultivation_pool_burst_notice", fmt.Sprintf("cultivation_pool_burst:%d", attemptID))
		} else {
			if AppConfig.NoticeGroupID != 0 && chatID != AppConfig.NoticeGroupID {
				sendGroupAutoDeleteMessageAsync(bot, AppConfig.NoticeGroupID, announce, "cultivation_breakthrough_notice", fmt.Sprintf("cultivation_notice:%d", attemptID))
			}
		}

		return
	}

	failText := fmt.Sprintf(
		"⚡️💔 **【渡劫失败·道行受损】** 💔⚡️\n\n"+
			"道友 @%s 在突破时遭遇九幽魔雷轰击，**【%s】在狂暴的天雷中化为飞灰！**\n"+
			"🎲 天机判定：成功率 `%.0f%%`，本次掷点 `%d/100`，保底：`%t`\n"+
			"🧾 突破记录：`#%d`\n"+
			"并且身受重伤，实际扣除 `%d` 积分调养费。\n\n"+
			"💡 天道感应：当前境界已连续失败 `%d/%d` 次，满%d次下次必定破阶成功！",
		safeName,
		pillName,
		successRate*100,
		roll,
		isGuaranteed,
		attemptID,
		failPenaltyPoints,
		tribulationFails,
		guaranteeFailCount,
		guaranteeFailCount,
	)

	if len(victimNames) > 0 {
		failText += "\n\n🌩 **【雷劫外溢·天道无常】** 🌩\n可怕的雷劫余波震荡了整个修仙界！以下无辜路人被雷劈中，强制扣除 `10` 积分医疗费："
		for _, name := range victimNames {
			if name == "" {
				name = "神秘道友"
			}
			failText += fmt.Sprintf("\n💥 @%s 痛失 %d 积分", name, splashPenaltyPoints)
		}
	}

	func() {
		msg := tgbotapi.NewMessage(chatID, failText)
		if chatID < 0 {
			if !enqueueAutoDelete(bot, msg, "cultivation_breakthrough_fail", telegramAsyncPriorityNormal, fmt.Sprintf("cultivation_fail:%d", attemptID)) {
				log.Printf("发送修仙突破失败公告入队失败: chat=%d user=%d attempt=%d", chatID, userID, attemptID)
			}
			return
		}
		if _, err := sendAutoDelete(bot, msg); err != nil {
			log.Printf("发送修仙突破失败公告失败: chat=%d user=%d attempt=%d err=%s", chatID, userID, attemptID, formatTelegramSendError(err))
		}
	}()

	// 避免编译器认为 victimIDs 未使用。保留该变量是为了后续需要私聊通知受害者时可直接使用。
	_ = victimIDs
}

func handleCultivationRank(bot *tgbotapi.BotAPI, chatID int64) {
	type RankResult struct {
		Username       string
		SectName       string
		MajorRealm     int
		MinorRealm     int
		TotalAudioTime float64
		PillAudioTime  float64
	}

	var results []RankResult

	err := DB.Table("cultivations").
		Select("users.username, sects.name as sect_name, cultivations.major_realm, cultivations.minor_realm, cultivations.total_audio_time, cultivations.pill_audio_time").
		Joins("left join users on users.telegram_id = cultivations.user_id").
		Joins("left join sect_members on sect_members.user_id = cultivations.user_id and sect_members.deleted_at is null").
		Joins("left join sects on sects.id = sect_members.sect_id and sects.deleted_at is null").
		Order("cultivations.major_realm desc, cultivations.minor_realm desc, (cultivations.total_audio_time + cultivations.pill_audio_time) desc, cultivations.total_audio_time desc").
		Limit(10).
		Scan(&results).Error

	if err != nil {
		if _, sendErr := sendAutoDelete(bot, tgbotapi.NewMessage(chatID, "❌ 天机阁拉取榜单异常，请联系管理员查看后台日志！")); sendErr != nil {
			log.Printf("发送修仙榜单错误提示失败: chat=%d err=%s", chatID, formatTelegramSendError(sendErr))
		}
		log.Printf("⚠️ 修仙榜查询失败: chat=%d err=%s", chatID, formatPlainError(err))
		return
	}

	if len(results) == 0 {
		sendGroupAutoDeleteMessage(bot, chatID, "📿 **【仙道风云榜】**\n\n目前修仙界灵气稀薄，尚未有凡人踏上仙途。")
		return
	}

	reply := "📜 **【仙道风云榜·万界至尊】** 📜\n\n"
	medals := []string{"🥇", "🥈", "🥉", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟"}

	for i, r := range results {
		dummyCul := &Cultivation{MajorRealm: r.MajorRealm, MinorRealm: r.MinorRealm}
		realmName := GetRealmName(dummyCul)

		displayName := cultivationRankDisplayName(r.Username, r.SectName)

		totalShow := r.TotalAudioTime + r.PillAudioTime

		reply += fmt.Sprintf("%s **第%d名**: **%s**\n └ %s | 总修为 `%.1f` 小时\n   *(苦修: `%.1f` 小时 | 药力: `%.1f` 小时)*\n\n",
			medals[i], i+1, displayName, realmName, totalShow, r.TotalAudioTime, r.PillAudioTime)
	}

	reply += "*(💡 发送 `突破` 引动雷劫，提升境界排位！)*"
	msg := tgbotapi.NewMessage(chatID, reply)
	msg.ParseMode = "Markdown"
	if _, sendErr := sendAutoDelete(bot, msg); sendErr != nil {
		log.Printf("发送修仙榜单失败: chat=%d err=%s", chatID, formatTelegramSendError(sendErr))
	}
}

func cultivationRankDisplayName(username string, sectName string) string {
	name := strings.TrimSpace(username)
	if name == "" {
		name = "神秘道友"
	}
	name = escapeMarkdown(name)

	sectName = strings.TrimSpace(sectName)
	if sectName == "" {
		return name
	}
	return fmt.Sprintf("%s【%s】", name, escapeMarkdown(sectName))
}

func cultivationPointDescriptionName(name string) string {
	return lotteryDisplayText(name, 80, "-")
}
