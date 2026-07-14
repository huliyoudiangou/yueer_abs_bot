package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	sectLotteryModeCount = "count"
	sectLotteryModeTime  = "time"

	sectLotteryStatusActive    = "active"
	sectLotteryStatusDrawing   = "drawing"
	sectLotteryStatusDrawn     = "drawn"
	sectLotteryStatusCancelled = "cancelled"

	sectLotteryPrizePending   = "pending"
	sectLotteryPrizeAssigned  = "assigned"
	sectLotteryPrizeDelivered = "delivered"
	sectLotteryPrizeFailed    = "failed"

	sectLotteryWinnerAssigned  = "assigned"
	sectLotteryWinnerDelivered = "delivered"
	sectLotteryWinnerFailed    = "failed"

	sectLotteryReminderDelivered = "delivered"
	sectLotteryReminderFailed    = "failed"

	sectLotteryTitleMinRunes        = 2
	sectLotteryTitleMaxRunes        = 60
	sectLotterySecretMaxLen         = 200
	sectLotteryMaxSecrets           = 100
	sectLotteryMinContribution      = 100
	sectLotterySchedulerInterval    = time.Minute
	sectLotteryMaxScheduledDraws    = 20
	sectLotteryCreateConfirmCommand = "确认创建宗门抽奖"
)

type SectLottery struct {
	gorm.Model

	SectID      int64  `gorm:"index;not null"`
	SectName    string `gorm:"index"`
	CreatorID   int64  `gorm:"index;not null"`
	CreatorName string

	Title  string `gorm:"not null"`
	Status string `gorm:"index;not null"`
	Mode   string `gorm:"index;not null"`

	TargetEntryCount int
	DrawAt           *time.Time `gorm:"index"`
	DrawnAt          *time.Time `gorm:"index"`

	PrizeCount  int
	EntryCount  int
	WinnerCount int
	ResultNote  string
}

func (SectLottery) TableName() string { return "sect_lotteries" }

type SectLotteryEntry struct {
	gorm.Model

	LotteryID uint  `gorm:"index;not null"`
	SectID    int64 `gorm:"index;not null"`
	UserID    int64 `gorm:"index;not null"`
	UserName  string

	HistoricalContribution int
	JoinedAt               time.Time `gorm:"index;not null"`
}

func (SectLotteryEntry) TableName() string { return "sect_lottery_entries" }

type SectLotteryPrize struct {
	gorm.Model

	LotteryID uint   `gorm:"index;not null"`
	CodeEnc   string `gorm:"not null"`
	Preview   string
	Status    string `gorm:"index;not null"`

	AssignedUserID int64 `gorm:"index"`
	DeliveredAt    *time.Time
	DeliveryError  string
}

func (SectLotteryPrize) TableName() string { return "sect_lottery_prizes" }

type SectLotteryWinner struct {
	gorm.Model

	LotteryID uint  `gorm:"index;not null"`
	SectID    int64 `gorm:"index;not null"`
	PrizeID   uint  `gorm:"index;not null"`
	UserID    int64 `gorm:"index;not null"`
	UserName  string
	Status    string `gorm:"index;not null"`

	DeliveryError string
	DeliveredAt   *time.Time
}

func (SectLotteryWinner) TableName() string { return "sect_lottery_winners" }

type SectLotteryReminder struct {
	ID        uint `gorm:"primaryKey"`
	CreatedAt time.Time
	UpdatedAt time.Time

	LotteryID uint  `gorm:"index;not null"`
	SectID    int64 `gorm:"index;not null"`
	UserID    int64 `gorm:"index;not null"`
	UserName  string
	Status    string `gorm:"index;not null"`

	LastError string
	SentAt    *time.Time
}

func (SectLotteryReminder) TableName() string { return "sect_lottery_reminders" }

func upsertSectLotteryReminderRecord(db *gorm.DB, reminder *SectLotteryReminder) error {
	if db == nil || reminder == nil {
		return fmt.Errorf("SECT_LOTTERY_REMINDER_INVALID")
	}
	entry := *reminder
	entry.UserName = formatPlainValue(entry.UserName)
	entry.Status = formatPlainValue(entry.Status)
	entry.LastError = formatPlainValue(entry.LastError)
	res := db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "lottery_id"}, {Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"sect_id":    entry.SectID,
			"user_name":  entry.UserName,
			"status":     entry.Status,
			"last_error": entry.LastError,
			"sent_at":    entry.SentAt,
			"updated_at": entry.UpdatedAt,
		}),
	}).Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SECT_LOTTERY_REMINDER_UPSERT_MISSED")
	}
	*reminder = entry
	return nil
}

func createSectLotteryInTx(tx *gorm.DB, lot *SectLottery) error {
	if tx == nil || lot == nil {
		return fmt.Errorf("SECT_LOTTERY_INVALID")
	}
	lot.SectName = formatPlainValue(lot.SectName)
	lot.CreatorName = formatPlainValue(lot.CreatorName)
	lot.Title = formatPlainValue(lot.Title)
	lot.Status = formatPlainValue(lot.Status)
	lot.Mode = formatPlainValue(lot.Mode)
	res := tx.Create(lot)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SECT_LOTTERY_CREATE_MISSED")
	}
	return nil
}

func createSectLotteryPrizeInTx(tx *gorm.DB, prize *SectLotteryPrize) error {
	if tx == nil || prize == nil {
		return fmt.Errorf("SECT_LOTTERY_PRIZE_INVALID")
	}
	prize.Preview = formatPlainValue(prize.Preview)
	prize.Status = formatPlainValue(prize.Status)
	res := tx.Create(prize)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SECT_LOTTERY_PRIZE_CREATE_MISSED")
	}
	return nil
}

func createSectLotteryEntryInTx(tx *gorm.DB, entry *SectLotteryEntry) error {
	if tx == nil || entry == nil {
		return fmt.Errorf("SECT_LOTTERY_ENTRY_INVALID")
	}
	entry.UserName = formatPlainValue(entry.UserName)
	res := tx.Create(entry)
	if res.Error != nil {
		if isUniqueConstraintError(res.Error) {
			return errLotteryAlreadyJoined
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SECT_LOTTERY_ENTRY_CREATE_MISSED")
	}
	return nil
}

func createSectLotteryWinnerInTx(tx *gorm.DB, winner *SectLotteryWinner) (bool, error) {
	if tx == nil || winner == nil {
		return false, fmt.Errorf("SECT_LOTTERY_WINNER_INVALID")
	}
	winner.UserName = formatPlainValue(winner.UserName)
	winner.Status = formatPlainValue(winner.Status)
	res := tx.Create(winner)
	if res.Error != nil {
		if isUniqueConstraintError(res.Error) {
			return false, nil
		}
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, fmt.Errorf("SECT_LOTTERY_WINNER_CREATE_MISSED")
	}
	return true, nil
}

func HandleSectLotteryCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState) bool {
	if msg == nil || msg.From == nil || msg.Chat == nil {
		return false
	}
	text = strings.TrimSpace(text)
	if session == nil {
		session = getSession(msg.From.ID)
	}

	if strings.HasPrefix(session.GetStep(), "WAITING_SECT_LOTTERY_") {
		handleSectLotteryCreateStep(bot, msg, text, session)
		return true
	}

	switch {
	case text == "创建宗门抽奖":
		startSectLotteryCreateWizard(bot, msg, session)
		return true
	case text == "宗门抽奖":
		showSectLotteryList(bot, msg)
		return true
	case strings.HasPrefix(text, "参加宗门抽奖 "):
		handleJoinSectLotteryCommand(bot, msg, strings.TrimSpace(strings.TrimPrefix(text, "参加宗门抽奖 ")))
		return true
	case strings.HasPrefix(text, "查看宗门抽奖 "):
		handleSectLotteryDetailCommand(bot, msg, strings.TrimSpace(strings.TrimPrefix(text, "查看宗门抽奖 ")))
		return true
	case strings.HasPrefix(text, "重发宗门抽奖 "):
		handleRetrySectLotteryDeliveryCommand(bot, msg, strings.TrimSpace(strings.TrimPrefix(text, "重发宗门抽奖 ")))
		return true
	case strings.HasPrefix(text, "提醒宗门抽奖 "):
		handleRemindSectLotteryMembersCommand(bot, msg, strings.TrimSpace(strings.TrimPrefix(text, "提醒宗门抽奖 ")))
		return true
	case strings.HasPrefix(text, "补发宗门抽奖提醒 "):
		handleRemindSectLotteryMembersCommand(bot, msg, strings.TrimSpace(strings.TrimPrefix(text, "补发宗门抽奖提醒 ")))
		return true
	case strings.HasPrefix(text, "取消宗门抽奖 "):
		handleCancelSectLotteryCommand(bot, msg, strings.TrimSpace(strings.TrimPrefix(text, "取消宗门抽奖 ")))
		return true
	default:
		return false
	}
}

func startSectLotteryCreateWizard(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, session *SessionState) {
	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "宗门抽奖创建请私聊 Bot 执行。")
		return
	}
	member, sect, err := sectLotteryCreatorContext(msg.From.ID)
	if err != nil {
		sendPlainText(bot, msg.Chat.ID, sectLotteryActionErrorText(err))
		return
	}
	if !canUpgradeSectAsset(member.Role) {
		sendPlainText(bot, msg.Chat.ID, "❌ 只有宗主或长老可以创建宗门抽奖。")
		return
	}

	clearSession(msg.From.ID)
	session = getSession(msg.From.ID)
	session.SetTemp("sect_lottery_sect_id", strconv.FormatInt(int64(sect.ID), 10))
	session.SetTemp("sect_lottery_sect_name", sect.Name)
	session.SetStep("WAITING_SECT_LOTTERY_TITLE")
	UserSessions.Store(msg.From.ID, session)
	sendPlainText(bot, msg.Chat.ID, "🎁 创建宗门抽奖\n\n第一步：请输入活动标题，2-60 个字，不能包含换行、制表符或控制字符。\n发送“取消”可退出。")
}

func handleSectLotteryCreateStep(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState) {
	userID := msg.From.ID
	chatID := msg.Chat.ID
	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, chatID, "宗门抽奖创建请私聊 Bot 执行。")
		return
	}
	if text == "取消" {
		clearSession(userID)
		sendPlainText(bot, chatID, "已取消宗门抽奖创建。")
		return
	}

	switch session.GetStep() {
	case "WAITING_SECT_LOTTERY_TITLE":
		title, ok := normalizeSectLotteryTitle(text)
		if !ok {
			sendPlainText(bot, chatID, "活动标题需为 2-60 个字，且不能包含换行、制表符或控制字符，请重新发送：")
			return
		}
		session.SetTemp("sect_lottery_title", title)
		session.SetStep("WAITING_SECT_LOTTERY_MODE")
		UserSessions.Store(userID, session)
		sendPlainText(bot, chatID, "第二步：请选择开奖模式。\n\n发送：人数\n或发送：时间")
	case "WAITING_SECT_LOTTERY_MODE":
		switch text {
		case "人数":
			session.SetTemp("sect_lottery_mode", sectLotteryModeCount)
			session.SetStep("WAITING_SECT_LOTTERY_LIMIT")
			UserSessions.Store(userID, session)
			sendPlainText(bot, chatID, "第三步：请输入满多少人开奖，范围 1-100000：")
		case "时间":
			session.SetTemp("sect_lottery_mode", sectLotteryModeTime)
			session.SetStep("WAITING_SECT_LOTTERY_DRAW_TIME")
			UserSessions.Store(userID, session)
			sendPlainText(bot, chatID, "第三步：请输入开奖时间。\n\n支持两种格式：\n1. 分钟数，例如：60\n2. 固定时间，例如：2026-06-07 20:00:00")
		default:
			sendPlainText(bot, chatID, "模式不识别，请发送“人数”或“时间”。")
		}
	case "WAITING_SECT_LOTTERY_LIMIT":
		limit, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || limit < 1 || limit > 100000 {
			sendPlainText(bot, chatID, "人数限制需为 1-100000 的整数，请重新输入：")
			return
		}
		session.SetTemp("sect_lottery_limit", strconv.Itoa(limit))
		session.SetStep("WAITING_SECT_LOTTERY_SECRETS")
		UserSessions.Store(userID, session)
		sendPlainText(bot, chatID, fmt.Sprintf("第四步：请逐行发送卡密，最多 %d 条，单条最多 %d 字。\n\n重复卡密会拒绝创建，空行会忽略。", sectLotteryMaxSecrets, sectLotterySecretMaxLen))
	case "WAITING_SECT_LOTTERY_DRAW_TIME":
		drawAt, drawDesc, err := parseLotteryDrawTimeInput(text, time.Now())
		if err != nil {
			sendPlainText(bot, chatID, formatPlainError(err))
			return
		}
		session.SetTemp("sect_lottery_draw_at", strconv.FormatInt(drawAt.Unix(), 10))
		session.SetTemp("sect_lottery_draw_desc", drawDesc)
		session.SetStep("WAITING_SECT_LOTTERY_SECRETS")
		UserSessions.Store(userID, session)
		sendPlainText(bot, chatID, fmt.Sprintf("第四步：请逐行发送卡密，最多 %d 条，单条最多 %d 字。\n\n重复卡密会拒绝创建，空行会忽略。", sectLotteryMaxSecrets, sectLotterySecretMaxLen))
	case "WAITING_SECT_LOTTERY_SECRETS":
		secrets, err := parseSectLotterySecrets(text)
		if err != nil {
			sendPlainText(bot, chatID, "卡密格式有误："+formatPlainError(err)+"\n\n请重新逐行发送卡密。")
			return
		}
		session.SetTemp("sect_lottery_secrets", strings.Join(secrets, "\n"))
		session.SetStep("WAITING_SECT_LOTTERY_CONFIRM")
		UserSessions.Store(userID, session)
		sendPlainText(bot, chatID, formatSectLotteryCreateConfirm(session, len(secrets)))
	case "WAITING_SECT_LOTTERY_CONFIRM":
		if text != sectLotteryCreateConfirmCommand {
			clearSession(userID)
			sendPlainText(bot, chatID, "已取消宗门抽奖创建。")
			return
		}
		lotteryID, err := createSectLotteryFromSession(msg, session)
		if err != nil {
			sendPlainText(bot, chatID, "❌ 创建宗门抽奖失败："+formatPlainError(err))
			return
		}
		clearSession(userID)
		success, failed := remindSectLotteryMembers(bot, lotteryID)
		sendPlainText(bot, chatID, fmt.Sprintf("✅ 宗门抽奖已创建\n\n活动ID：%d\n\n成员私聊发送：\n参加宗门抽奖 %d\n\n成员提醒：成功 %d，失败 %d", lotteryID, lotteryID, success, failed))
	default:
		clearSession(userID)
		sendPlainText(bot, chatID, "宗门抽奖创建会话异常，已中止。")
	}
}

func parseSectLotterySessionInt(session *SessionState, key string, minValue, maxValue int) (int, error) {
	raw := strings.TrimSpace(session.GetTemp(key))
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid sect lottery session integer: key=%s value=%s err=%w", formatPlainValue(key), formatPlainValue(raw), err)
	}
	if value < minValue || value > maxValue {
		return 0, fmt.Errorf("sect lottery session integer out of range: key=%s value=%d range=%d-%d", formatPlainValue(key), value, minValue, maxValue)
	}
	return value, nil
}

func parseSectLotterySessionInt64(session *SessionState, key string) (int64, error) {
	raw := strings.TrimSpace(session.GetTemp(key))
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid sect lottery session integer: key=%s value=%s err=%w", formatPlainValue(key), formatPlainValue(raw), err)
	}
	if value <= 0 {
		return 0, fmt.Errorf("sect lottery session integer out of range: key=%s value=%d", formatPlainValue(key), value)
	}
	return value, nil
}

func parseSectLotterySessionUnixTime(session *SessionState, key string, now time.Time) (time.Time, error) {
	raw := strings.TrimSpace(session.GetTemp(key))
	unixValue, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid sect lottery session time: key=%s value=%s err=%w", formatPlainValue(key), formatPlainValue(raw), err)
	}
	drawAt := time.Unix(unixValue, 0)
	if !drawAt.After(now) {
		return time.Time{}, fmt.Errorf("sect lottery session time is not in future: key=%s value=%s", formatPlainValue(key), formatPlainValue(raw))
	}
	return drawAt, nil
}

func createSectLotteryFromSession(msg *tgbotapi.Message, session *SessionState) (uint, error) {
	if msg == nil || msg.From == nil {
		return 0, errTelegramUserMissing
	}
	member, sect, err := sectLotteryCreatorContext(msg.From.ID)
	if err != nil {
		return 0, err
	}
	if !canUpgradeSectAsset(member.Role) {
		return 0, errSectNoPermission
	}
	sectID, err := parseSectLotterySessionInt64(session, "sect_lottery_sect_id")
	if err != nil {
		return 0, err
	}
	if sectID != int64(sect.ID) {
		return 0, errSectNotFound
	}
	title, ok := normalizeSectLotteryTitle(session.GetTemp("sect_lottery_title"))
	if !ok {
		return 0, fmt.Errorf("INVALID_TITLE")
	}
	mode := session.GetTemp("sect_lottery_mode")
	secrets, err := parseSectLotterySecrets(session.GetTemp("sect_lottery_secrets"))
	if err != nil {
		return 0, err
	}

	lot := SectLottery{
		SectID:      int64(sect.ID),
		SectName:    sect.Name,
		CreatorID:   msg.From.ID,
		CreatorName: getTelegramDisplayName(msg.From),
		Title:       title,
		Status:      sectLotteryStatusActive,
		Mode:        mode,
		PrizeCount:  len(secrets),
	}
	if mode == sectLotteryModeCount {
		limit, err := parseSectLotterySessionInt(session, "sect_lottery_limit", 1, 100000)
		if err != nil {
			return 0, err
		}
		lot.TargetEntryCount = limit
	} else if mode == sectLotteryModeTime {
		drawAt, err := parseSectLotterySessionUnixTime(session, "sect_lottery_draw_at", time.Now())
		if err != nil {
			return 0, err
		}
		lot.DrawAt = &drawAt
	} else {
		return 0, fmt.Errorf("INVALID_MODE")
	}

	var lotteryID uint
	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := createSectLotteryInTx(tx, &lot); err != nil {
			return err
		}
		for _, secret := range secrets {
			enc, err := encryptSectLotterySecret(secret)
			if err != nil {
				return err
			}
			if err := createSectLotteryPrizeInTx(tx, &SectLotteryPrize{
				LotteryID: lot.ID,
				CodeEnc:   enc,
				Preview:   maskSecret(secret),
				Status:    sectLotteryPrizePending,
			}); err != nil {
				return err
			}
		}
		return writeAuditLogInTx(tx, msg.From.ID, "CREATE_SECT_LOTTERY", fmt.Sprintf("%d", lot.ID), 0,
			fmt.Sprintf("创建宗门抽奖 sect=%d title=%s mode=%s prizes=%d", sect.ID, formatPlainValue(title), formatPlainValue(mode), len(secrets)))
	})
	if err != nil {
		return 0, err
	}
	lotteryID = lot.ID
	return lotteryID, nil
}

func handleJoinSectLotteryCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, rawID string) {
	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "宗门抽奖参与请私聊 Bot 执行。")
		return
	}
	id, err := strconv.ParseUint(strings.TrimSpace(rawID), 10, 64)
	if err != nil || id == 0 {
		sendPlainText(bot, msg.Chat.ID, "用法：参加宗门抽奖 活动ID")
		return
	}
	joined, target, shouldDraw, err := joinSectLottery(uint(id), msg.From)
	if err != nil {
		sendPlainText(bot, msg.Chat.ID, sectLotteryActionErrorText(err))
		return
	}
	if target > 0 {
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("✅ 已参与宗门福利抽奖 #%d\n当前参与：%d/%d", id, joined, target))
	} else {
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("✅ 已参与宗门福利抽奖 #%d\n当前参与：%d", id, joined))
	}
	if shouldDraw {
		if err := drawSectLottery(bot, uint(id), "participant_limit"); err != nil && !errors.Is(err, errLotteryNotActive) {
			log.Printf("⚠️ 满人宗门抽奖开奖失败: lottery=%d err=%s", id, formatPlainError(err))
			notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 宗门抽奖 #%d 满人开奖失败：%s", id, formatPlainError(err)))
		}
	}
}

func joinSectLottery(lotteryID uint, tgUser *tgbotapi.User) (int, int, bool, error) {
	if tgUser == nil {
		return 0, 0, false, errTelegramUserMissing
	}
	joined := 0
	target := 0
	shouldDraw := false
	err := DB.Transaction(func(tx *gorm.DB) error {
		txJoined := 0
		txTarget := 0
		txShouldDraw := false
		var lot SectLottery
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND status = ?", lotteryID, sectLotteryStatusActive).First(&lot).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return errLotteryNotActive
		}
		if lot.Mode == sectLotteryModeTime && lot.DrawAt != nil && !time.Now().Before(*lot.DrawAt) {
			return errLotteryWaitingDraw
		}

		var member SectMember
		if err := tx.Where("sect_id = ? AND user_id = ?", lot.SectID, tgUser.ID).First(&member).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return errNotInSect
		}
		if err := validateSectLotteryUserEligibleTx(tx, member); err != nil {
			return err
		}
		totalContribution, err := sectShopMemberTotalContributionTx(tx, member)
		if err != nil {
			return err
		}
		if totalContribution < sectLotteryMinContribution {
			return errSectLotteryContributionTooLow
		}

		entry := SectLotteryEntry{
			LotteryID:              lotteryID,
			SectID:                 lot.SectID,
			UserID:                 tgUser.ID,
			UserName:               getTelegramDisplayName(tgUser),
			HistoricalContribution: totalContribution,
			JoinedAt:               time.Now(),
		}
		if err := createSectLotteryEntryInTx(tx, &entry); err != nil {
			return err
		}

		var count int64
		if err := tx.Model(&SectLotteryEntry{}).Where("lottery_id = ?", lotteryID).Count(&count).Error; err != nil {
			return err
		}
		txJoined = int(count)
		txTarget = lot.TargetEntryCount
		entryRes := tx.Model(&SectLottery{}).
			Where("id = ? AND status = ?", lotteryID, sectLotteryStatusActive).
			Update("entry_count", txJoined)
		if entryRes.Error != nil {
			return entryRes.Error
		}
		if entryRes.RowsAffected == 0 {
			return fmt.Errorf("SECT_LOTTERY_ENTRY_COUNT_STATE_CHANGED")
		}
		txShouldDraw = lot.Mode == sectLotteryModeCount && lot.TargetEntryCount > 0 && txJoined >= lot.TargetEntryCount
		joined = txJoined
		target = txTarget
		shouldDraw = txShouldDraw
		return nil
	})
	if err != nil {
		return 0, 0, false, err
	}
	return joined, target, shouldDraw, nil
}

func drawSectLottery(bot *tgbotapi.BotAPI, lotteryID uint, reason string) error {
	now := time.Now()
	var lot SectLottery
	var creatorID int64
	var winners []SectLotteryWinner
	var delivered int
	var failed int
	var unassigned int

	err := DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&SectLottery{}).
			Where("id = ? AND status = ?", lotteryID, sectLotteryStatusActive).
			Update("status", sectLotteryStatusDrawing)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errLotteryNotActive
		}
		if err := tx.Where("id = ?", lotteryID).First(&lot).Error; err != nil {
			return err
		}
		creatorID = lot.CreatorID

		var entries []SectLotteryEntry
		if err := tx.Where("lottery_id = ?", lotteryID).Order("id asc").Find(&entries).Error; err != nil {
			return err
		}
		var prizes []SectLotteryPrize
		if err := tx.Where("lottery_id = ? AND status = ?", lotteryID, sectLotteryPrizePending).Order("id asc").Find(&prizes).Error; err != nil {
			return err
		}
		if len(entries) == 0 || len(prizes) == 0 {
			unassigned = len(prizes)
			lot.EntryCount = len(entries)
			lot.WinnerCount = 0
			drawRes := tx.Model(&SectLottery{}).Where("id = ? AND status = ?", lotteryID, sectLotteryStatusDrawing).Updates(map[string]interface{}{
				"status":       sectLotteryStatusDrawn,
				"drawn_at":     &now,
				"entry_count":  len(entries),
				"winner_count": 0,
				"result_note":  fmt.Sprintf("reason=%s entries=%d prizes=%d", formatPlainValue(reason), len(entries), len(prizes)),
			})
			if drawRes.Error != nil {
				return drawRes.Error
			}
			if drawRes.RowsAffected == 0 {
				return fmt.Errorf("SECT_LOTTERY_DRAW_STATE_CHANGED")
			}
			return writeAuditLogInTx(tx, 0, "DRAW_SECT_LOTTERY", fmt.Sprintf("%d", lotteryID), 0,
				fmt.Sprintf("宗门抽奖开奖 sect=%d entries=%d prizes=%d winners=0 reason=%s", lot.SectID, len(entries), len(prizes), formatPlainValue(reason)))
		}

		shuffleSectLotteryEntries(entries)
		shuffleSectLotteryPrizes(prizes)
		prizeIndex := 0
		for _, entry := range entries {
			if prizeIndex >= len(prizes) {
				break
			}
			stillEligible, eligibilityErr := sectLotteryEntryStillEligibleTx(tx, lot.SectID, entry.UserID)
			if eligibilityErr != nil {
				return eligibilityErr
			}
			if !stillEligible {
				continue
			}
			prize := prizes[prizeIndex]
			winner := SectLotteryWinner{
				LotteryID: lotteryID,
				SectID:    lot.SectID,
				PrizeID:   prize.ID,
				UserID:    entry.UserID,
				UserName:  entry.UserName,
				Status:    sectLotteryWinnerAssigned,
			}
			created, err := createSectLotteryWinnerInTx(tx, &winner)
			if err != nil {
				return err
			}
			if !created {
				continue
			}
			res := tx.Model(&SectLotteryPrize{}).Where("id = ? AND status = ?", prize.ID, sectLotteryPrizePending).Updates(map[string]interface{}{
				"status":           sectLotteryPrizeAssigned,
				"assigned_user_id": entry.UserID,
			})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return fmt.Errorf("SECT_LOTTERY_PRIZE_STATE_CHANGED")
			}
			winners = append(winners, winner)
			prizeIndex++
		}

		unassigned = len(prizes) - len(winners)
		drawRes := tx.Model(&SectLottery{}).Where("id = ? AND status = ?", lotteryID, sectLotteryStatusDrawing).Updates(map[string]interface{}{
			"status":       sectLotteryStatusDrawn,
			"drawn_at":     &now,
			"entry_count":  len(entries),
			"winner_count": len(winners),
			"result_note":  fmt.Sprintf("reason=%s entries=%d prizes=%d winners=%d unassigned=%d", formatPlainValue(reason), len(entries), len(prizes), len(winners), unassigned),
		})
		if drawRes.Error != nil {
			return drawRes.Error
		}
		if drawRes.RowsAffected == 0 {
			return fmt.Errorf("SECT_LOTTERY_FINAL_STATE_CHANGED")
		}
		lot.EntryCount = len(entries)
		lot.WinnerCount = len(winners)
		return writeAuditLogInTx(tx, 0, "DRAW_SECT_LOTTERY", fmt.Sprintf("%d", lotteryID), 0,
			fmt.Sprintf("宗门抽奖开奖 sect=%d entries=%d prizes=%d winners=%d reason=%s", lot.SectID, len(entries), len(prizes), len(winners), formatPlainValue(reason)))
	})
	if err != nil {
		return err
	}

	delivered, failed = deliverSectLotteryWinners(bot, lotteryID)
	notifySectLotteryNonWinners(bot, lotteryID)
	notifySectLotteryCreatorSummary(bot, creatorID, lot, delivered, failed, unassigned)
	return nil
}

func deliverSectLotteryWinners(bot *tgbotapi.BotAPI, lotteryID uint) (int, int) {
	var winners []SectLotteryWinner
	if err := DB.Where("lottery_id = ? AND status IN ?", lotteryID, []string{sectLotteryWinnerAssigned, sectLotteryWinnerFailed}).Find(&winners).Error; err != nil {
		log.Printf("⚠️ 查询宗门抽奖待发奖中奖者失败: lottery=%d err=%s", lotteryID, formatPlainError(err))
		return 0, 0
	}
	delivered := 0
	failed := 0
	for _, winner := range winners {
		ok := deliverSectLotteryWinner(bot, winner)
		if ok {
			delivered++
		} else {
			failed++
		}
	}
	return delivered, failed
}

func deliverSectLotteryWinner(bot *tgbotapi.BotAPI, winner SectLotteryWinner) bool {
	var lot SectLottery
	var prize SectLotteryPrize
	if err := DB.Where("id = ?", winner.LotteryID).First(&lot).Error; err != nil {
		log.Printf("⚠️ 宗门抽奖发奖读取活动失败: lottery=%d winner=%d user=%d err=%s", winner.LotteryID, winner.ID, winner.UserID, formatPlainError(err))
		return false
	}
	if err := DB.Where("id = ? AND lottery_id = ?", winner.PrizeID, winner.LotteryID).First(&prize).Error; err != nil {
		log.Printf("⚠️ 宗门抽奖发奖读取奖品失败: lottery=%d prize=%d winner=%d user=%d err=%s", winner.LotteryID, winner.PrizeID, winner.ID, winner.UserID, formatPlainError(err))
		return false
	}
	plain, err := decryptSectLotterySecret(prize.CodeEnc)
	if err != nil {
		if markErr := markSectLotteryDeliveryFailed(winner, "卡密解密失败"); markErr != nil {
			log.Printf("⚠️ 宗门抽奖发奖失败状态写入失败: lottery=%d winner=%d user=%d err=%s", winner.LotteryID, winner.ID, winner.UserID, formatPlainError(markErr))
		}
		return false
	}
	text := fmt.Sprintf("🎁 宗门福泽降临\n\n你在【%s】的宗门福利抽奖中中奖：\n%s\n\n卡密：\n%s\n\n请妥善保存，Bot 不会在群内公开此内容。", lotteryDisplayText(lot.SectName, 80, "-"), lotteryDisplayText(lot.Title, 80, "-"), plain)
	if err := sendNoAutoDeletePlainText(bot, winner.UserID, text); err != nil {
		if markErr := markSectLotteryDeliveryFailed(winner, formatTelegramSendError(err)); markErr != nil {
			log.Printf("⚠️ 宗门抽奖发奖失败状态写入失败: lottery=%d winner=%d user=%d err=%s", winner.LotteryID, winner.ID, winner.UserID, formatPlainError(markErr))
		}
		return false
	}
	now := time.Now()
	err = DB.Transaction(func(tx *gorm.DB) error {
		winnerRes := tx.Model(&SectLotteryWinner{}).
			Where("id = ? AND lottery_id = ? AND prize_id = ? AND user_id = ? AND status IN ?", winner.ID, winner.LotteryID, winner.PrizeID, winner.UserID, []string{sectLotteryWinnerAssigned, sectLotteryWinnerFailed}).
			Updates(map[string]interface{}{
				"status":         sectLotteryWinnerDelivered,
				"delivery_error": "",
				"delivered_at":   &now,
			})
		if winnerRes.Error != nil {
			return winnerRes.Error
		}
		if winnerRes.RowsAffected == 0 {
			return fmt.Errorf("SECT_LOTTERY_WINNER_DELIVERY_STATE_CHANGED")
		}
		prizeRes := tx.Model(&SectLotteryPrize{}).
			Where("id = ? AND lottery_id = ? AND assigned_user_id = ? AND status IN ?", prize.ID, winner.LotteryID, winner.UserID, []string{sectLotteryPrizeAssigned, sectLotteryPrizeFailed}).
			Updates(map[string]interface{}{
				"status":         sectLotteryPrizeDelivered,
				"delivery_error": "",
				"delivered_at":   &now,
			})
		if prizeRes.Error != nil {
			return prizeRes.Error
		}
		if prizeRes.RowsAffected == 0 {
			return fmt.Errorf("SECT_LOTTERY_PRIZE_DELIVERY_STATE_CHANGED")
		}
		return nil
	})
	if err != nil {
		log.Printf("sect lottery delivery status write failed: lottery=%d winner=%d user=%d err=%s", winner.LotteryID, winner.ID, winner.UserID, formatPlainError(err))
		notifySuperAdminsPlain(bot, fmt.Sprintf("Sect lottery #%d user %d has received the private secret, but delivery status write failed. Please verify before resending.", winner.LotteryID, winner.UserID))
		return false
	}
	return true
}

func notifySectLotteryNonWinners(bot *tgbotapi.BotAPI, lotteryID uint) {
	var lot SectLottery
	if err := DB.Where("id = ?", lotteryID).First(&lot).Error; err != nil {
		log.Printf("⚠️ 查询宗门抽奖未中奖通知活动失败: lottery=%d err=%s", lotteryID, formatPlainError(err))
		return
	}
	var entries []SectLotteryEntry
	if err := DB.Where("lottery_id = ?", lotteryID).Order("id asc").Find(&entries).Error; err != nil {
		log.Printf("⚠️ 查询宗门抽奖未中奖参与者失败: lottery=%d err=%s", lotteryID, formatPlainError(err))
		return
	}
	if len(entries) == 0 {
		return
	}
	var winners []SectLotteryWinner
	if err := DB.Select("user_id").Where("lottery_id = ?", lotteryID).Find(&winners).Error; err != nil {
		log.Printf("⚠️ 查询宗门抽奖中奖者用于未中奖通知失败: lottery=%d err=%s", lotteryID, formatPlainError(err))
		return
	}
	winnerIDs := make(map[int64]struct{}, len(winners))
	for _, winner := range winners {
		winnerIDs[winner.UserID] = struct{}{}
	}

	sent := 0
	failed := 0
	for _, entry := range entries {
		if _, ok := winnerIDs[entry.UserID]; ok {
			continue
		}
		text := sectLotteryNonWinnerNoticeText(lot)
		if err := sendNoAutoDeletePlainText(bot, entry.UserID, text); err != nil {
			failed++
			log.Printf("⚠️ 宗门抽奖未中奖通知发送失败: lottery=%d user=%d err=%s", lotteryID, entry.UserID, formatTelegramSendError(err))
			continue
		}
		sent++
	}
	if sent > 0 || failed > 0 {
		log.Printf("✅ 宗门抽奖未中奖通知完成: lottery=%d sent=%d failed=%d", lotteryID, sent, failed)
	}
}

func sectLotteryNonWinnerNoticeText(lot SectLottery) string {
	return fmt.Sprintf("🎁 宗门福利抽奖已开奖\n\n宗门：%s\n活动：%s\n\n很遗憾，本次抽奖您未中奖。\n道友莫灰心，后续宗门福泽还会继续开放。", lotteryDisplayText(lot.SectName, 80, "-"), lotteryDisplayText(lot.Title, 80, "-"))
}
func markSectLotteryDeliveryFailed(winner SectLotteryWinner, reason string) error {
	reason = formatPlainValue(reason)
	return DB.Transaction(func(tx *gorm.DB) error {
		winnerRes := tx.Model(&SectLotteryWinner{}).
			Where("id = ? AND lottery_id = ? AND prize_id = ? AND user_id = ? AND status IN ?", winner.ID, winner.LotteryID, winner.PrizeID, winner.UserID, []string{sectLotteryWinnerAssigned, sectLotteryWinnerFailed}).
			Updates(map[string]interface{}{
				"status":         sectLotteryWinnerFailed,
				"delivery_error": reason,
			})
		if winnerRes.Error != nil {
			return winnerRes.Error
		}
		if winnerRes.RowsAffected == 0 {
			return fmt.Errorf("SECT_LOTTERY_WINNER_FAILURE_STATE_CHANGED")
		}
		prizeRes := tx.Model(&SectLotteryPrize{}).
			Where("id = ? AND lottery_id = ? AND assigned_user_id = ? AND status IN ?", winner.PrizeID, winner.LotteryID, winner.UserID, []string{sectLotteryPrizeAssigned, sectLotteryPrizeFailed}).
			Updates(map[string]interface{}{
				"status":         sectLotteryPrizeFailed,
				"delivery_error": reason,
			})
		if prizeRes.Error != nil {
			return prizeRes.Error
		}
		if prizeRes.RowsAffected == 0 {
			return fmt.Errorf("SECT_LOTTERY_PRIZE_FAILURE_STATE_CHANGED")
		}
		return nil
	})
}

func showSectLotteryList(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "宗门抽奖请私聊 Bot 查看和管理。")
		return
	}
	member, _, err := sectLotteryCreatorContext(msg.From.ID)
	if err != nil {
		sendPlainText(bot, msg.Chat.ID, sectLotteryActionErrorText(err))
		return
	}
	var lots []SectLottery
	if err := DB.Where("sect_id = ?", member.SectID).Order("created_at desc").Limit(10).Find(&lots).Error; err != nil {
		sendPlainText(bot, msg.Chat.ID, "❌ 查询宗门抽奖失败，请稍后再试。")
		return
	}
	if len(lots) == 0 {
		sendPlainText(bot, msg.Chat.ID, "当前宗门暂无抽奖活动。")
		return
	}
	var b strings.Builder
	b.WriteString("🎁 宗门抽奖\n\n")
	for _, lot := range lots {
		b.WriteString(fmt.Sprintf("#%d %s\n状态：%s 参与：%d 奖品：%d\n", lot.ID, lotteryDisplayText(lot.Title, 80, "-"), sectLotteryStatusText(lot.Status), lot.EntryCount, lot.PrizeCount))
		if lot.Status == sectLotteryStatusActive {
			if lot.Mode == sectLotteryModeCount {
				b.WriteString(fmt.Sprintf("开奖：满 %d 人\n", lot.TargetEntryCount))
			} else if lot.DrawAt != nil {
				b.WriteString("开奖：" + lot.DrawAt.Format("2006-01-02 15:04") + "\n")
			}
			b.WriteString(fmt.Sprintf("参与：参加宗门抽奖 %d\n", lot.ID))
		}
		b.WriteString("\n")
	}
	sendPlainText(bot, msg.Chat.ID, strings.TrimSpace(b.String()))
}

func handleSectLotteryDetailCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, rawID string) {
	lot, ok := loadSectLotteryForOperator(bot, msg, rawID)
	if !ok {
		return
	}
	var entries int64
	var winners []SectLotteryWinner
	var prizes []SectLotteryPrize
	if err := DB.Model(&SectLotteryEntry{}).Where("lottery_id = ?", lot.ID).Count(&entries).Error; err != nil {
		log.Printf("⚠️ 查询宗门抽奖参与人数失败: lottery=%d err=%s", lot.ID, formatPlainError(err))
		sendPlainText(bot, msg.Chat.ID, "❌ 查询宗门抽奖失败，请稍后再试。")
		return
	}
	if err := DB.Where("lottery_id = ?", lot.ID).Order("id asc").Find(&winners).Error; err != nil {
		log.Printf("⚠️ 查询宗门抽奖中奖记录失败: lottery=%d err=%s", lot.ID, formatPlainError(err))
		sendPlainText(bot, msg.Chat.ID, "❌ 查询宗门抽奖失败，请稍后再试。")
		return
	}
	if err := DB.Where("lottery_id = ?", lot.ID).Order("id asc").Find(&prizes).Error; err != nil {
		log.Printf("⚠️ 查询宗门抽奖奖品记录失败: lottery=%d err=%s", lot.ID, formatPlainError(err))
		sendPlainText(bot, msg.Chat.ID, "❌ 查询宗门抽奖失败，请稍后再试。")
		return
	}
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🎁 宗门抽奖 #%d\n\n活动：%s\n状态：%s\n参与：%d\n奖品：%d\n", lot.ID, lotteryDisplayText(lot.Title, 80, "-"), sectLotteryStatusText(lot.Status), entries, len(prizes)))
	if lot.Mode == sectLotteryModeCount {
		b.WriteString(fmt.Sprintf("开奖：满 %d 人\n", lot.TargetEntryCount))
	} else if lot.DrawAt != nil {
		b.WriteString("开奖：" + lot.DrawAt.Format("2006-01-02 15:04") + "\n")
	}
	if len(winners) > 0 {
		b.WriteString("\n中奖记录：\n")
		for _, w := range winners {
			b.WriteString(fmt.Sprintf("- %s(%d) 状态:%s\n", lotteryDisplayText(w.UserName, 40, "-"), w.UserID, sectLotteryWinnerStatusText(w.Status)))
		}
	}
	sendPlainText(bot, msg.Chat.ID, strings.TrimSpace(b.String()))
}

func handleRetrySectLotteryDeliveryCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, rawID string) {
	lot, ok := loadSectLotteryForOperator(bot, msg, rawID)
	if !ok {
		return
	}
	if lot.Status != sectLotteryStatusDrawn {
		sendPlainText(bot, msg.Chat.ID, "❌ 只有已开奖的宗门抽奖可以重发失败奖品。")
		return
	}
	delivered, failed := deliverSectLotteryWinners(bot, lot.ID)
	sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("✅ 重发完成\n成功：%d\n失败：%d", delivered, failed))
}

func handleRemindSectLotteryMembersCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, rawID string) {
	lot, ok := loadSectLotteryForOperator(bot, msg, rawID)
	if !ok {
		return
	}
	if lot.Status != sectLotteryStatusActive {
		sendPlainText(bot, msg.Chat.ID, "❌ 只有可参与中的宗门抽奖可以补发成员提醒。")
		return
	}
	success, failed := remindSectLotteryMembers(bot, lot.ID)
	sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("✅ 宗门抽奖提醒已补发\n成功：%d\n失败：%d", success, failed))
}

func handleCancelSectLotteryCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, rawID string) {
	lot, ok := loadSectLotteryForOperator(bot, msg, rawID)
	if !ok {
		return
	}
	if lot.Status != sectLotteryStatusActive {
		sendPlainText(bot, msg.Chat.ID, "❌ 只有未开奖的宗门抽奖可以取消。")
		return
	}
	if err := cancelSectLottery(lot, msg.From.ID); err != nil {
		sendPlainText(bot, msg.Chat.ID, "❌ 取消宗门抽奖失败："+formatPlainError(err))
		return
	}
	sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("✅ 已取消宗门抽奖 #%d。", lot.ID))
}

func cancelSectLottery(lot SectLottery, actorID int64) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&SectLottery{}).Where("id = ? AND status = ?", lot.ID, sectLotteryStatusActive).Update("status", sectLotteryStatusCancelled)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errLotteryNotActive
		}
		return writeAuditLogInTx(tx, actorID, "CANCEL_SECT_LOTTERY", fmt.Sprintf("%d", lot.ID), 0, fmt.Sprintf("取消宗门抽奖 sect=%d title=%s", lot.SectID, formatPlainValue(lot.Title)))
	})
}

func remindSectLotteryMembers(bot *tgbotapi.BotAPI, lotteryID uint) (int, int) {
	var lot SectLottery
	if err := DB.Where("id = ?", lotteryID).First(&lot).Error; err != nil {
		log.Printf("⚠️ 查询宗门抽奖提醒活动失败: lottery=%d err=%s", lotteryID, formatPlainError(err))
		return 0, 0
	}
	if lot.Status != sectLotteryStatusActive {
		return 0, 0
	}

	var members []SectMember
	if err := DB.Where("sect_id = ?", lot.SectID).Order("joined_at asc, id asc").Find(&members).Error; err != nil {
		log.Printf("⚠️ 查询宗门抽奖提醒成员失败: lottery=%d sect=%d err=%s", lotteryID, lot.SectID, formatPlainError(err))
		return 0, 0
	}

	delivered := 0
	failed := 0
	for _, member := range members {
		alreadyDelivered, err := sectLotteryReminderAlreadyDelivered(lotteryID, member.UserID)
		if err != nil {
			log.Printf("⚠️ 查询宗门抽奖提醒去重状态失败，跳过本次投递: lottery=%d user=%d err=%s", lotteryID, member.UserID, formatPlainError(err))
			failed++
			continue
		}
		if alreadyDelivered {
			continue
		}
		if deliverSectLotteryReminder(bot, lot, member) {
			delivered++
		} else {
			failed++
		}
	}
	return delivered, failed
}

func sectLotteryReminderAlreadyDelivered(lotteryID uint, userID int64) (bool, error) {
	var count int64
	if err := DB.Model(&SectLotteryReminder{}).
		Where("lottery_id = ? AND user_id = ? AND status = ?", lotteryID, userID, sectLotteryReminderDelivered).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func deliverSectLotteryReminder(bot *tgbotapi.BotAPI, lot SectLottery, member SectMember) bool {
	text := sectLotteryReminderText(lot)
	if err := sendNoAutoDeletePlainText(bot, member.UserID, text); err != nil {
		if markErr := markSectLotteryReminder(lot, member, sectLotteryReminderFailed, formatTelegramSendError(err)); markErr != nil {
			log.Printf("⚠️ 记录宗门抽奖提醒失败状态失败: lottery=%d user=%d err=%s", lot.ID, member.UserID, formatPlainError(markErr))
		}
		return false
	}
	if err := markSectLotteryReminder(lot, member, sectLotteryReminderDelivered, ""); err != nil {
		log.Printf("⚠️ 记录宗门抽奖提醒成功状态失败，后续可能重复补发: lottery=%d user=%d err=%s", lot.ID, member.UserID, formatPlainError(err))
		return false
	}
	return true
}

func markSectLotteryReminder(lot SectLottery, member SectMember, status string, reason string) error {
	reason = formatPlainValue(reason)
	now := time.Now()
	reminder := SectLotteryReminder{
		LotteryID: lot.ID,
		SectID:    lot.SectID,
		UserID:    member.UserID,
		UserName:  member.UserName,
		Status:    status,
		LastError: reason,
	}
	if status == sectLotteryReminderDelivered {
		reminder.SentAt = &now
	}
	reminder.UpdatedAt = now
	return upsertSectLotteryReminderRecord(DB, &reminder)
}

func sectLotteryReminderText(lot SectLottery) string {
	rule := ""
	if lot.Mode == sectLotteryModeCount {
		rule = fmt.Sprintf("满 %d 人开奖", lot.TargetEntryCount)
	} else if lot.DrawAt != nil {
		rule = "定时开奖：" + lot.DrawAt.Format("2006-01-02 15:04")
	} else {
		rule = "定时开奖"
	}
	return fmt.Sprintf("🎁 宗门福利抽奖已开启\n\n宗门：%s\n活动：%s\n活动ID：%d\n规则：%s\n奖品数量：%d\n参与门槛：历史贡献 >= %d\n\n如需参与，请私聊发送：\n参加宗门抽奖 %d", lotteryDisplayText(lot.SectName, 80, "-"), lotteryDisplayText(lot.Title, 80, "-"), lot.ID, rule, lot.PrizeCount, sectLotteryMinContribution, lot.ID)
}

func loadSectLotteryForOperator(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, rawID string) (SectLottery, bool) {
	var zero SectLottery
	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "宗门抽奖请私聊 Bot 查看和管理。")
		return zero, false
	}
	id, err := strconv.ParseUint(strings.TrimSpace(rawID), 10, 64)
	if err != nil || id == 0 {
		sendPlainText(bot, msg.Chat.ID, "用法：查看宗门抽奖 活动ID")
		return zero, false
	}
	member, _, err := sectLotteryCreatorContext(msg.From.ID)
	if err != nil {
		sendPlainText(bot, msg.Chat.ID, sectLotteryActionErrorText(err))
		return zero, false
	}
	if !canUpgradeSectAsset(member.Role) {
		sendPlainText(bot, msg.Chat.ID, "❌ 只有宗主或长老可以查看或管理宗门抽奖。")
		return zero, false
	}
	var lot SectLottery
	if err := DB.Where("id = ? AND sect_id = ?", uint(id), member.SectID).First(&lot).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendPlainText(bot, msg.Chat.ID, "❌ 宗门抽奖不存在。")
		} else {
			log.Printf("⚠️ 宗门抽奖详情读取失败: lottery=%d sect=%d user=%d err=%s", id, member.SectID, msg.From.ID, formatPlainError(err))
			sendPlainText(bot, msg.Chat.ID, "❌ 宗门抽奖读取失败，请稍后重试。")
		}
		return zero, false
	}
	return lot, true
}

func notifySectLotteryCreatorSummary(bot *tgbotapi.BotAPI, creatorID int64, lot SectLottery, delivered int, failed int, unassigned int) {
	if creatorID == 0 {
		return
	}
	var winners []SectLotteryWinner
	if err := DB.Where("lottery_id = ?", lot.ID).Order("id asc").Find(&winners).Error; err != nil {
		log.Printf("⚠️ 查询宗门抽奖创建者摘要中奖名单失败: lottery=%d creator=%d err=%s", lot.ID, creatorID, formatPlainError(err))
	}
	text := sectLotteryCreatorSummaryText(lot, winners, delivered, failed, unassigned)
	if err := sendNoAutoDeletePlainText(bot, creatorID, text); err != nil {
		log.Printf("⚠️ 宗门抽奖创建者摘要发送失败: lottery=%d creator=%d err=%s", lot.ID, creatorID, formatTelegramSendError(err))
	}
}

func sectLotteryCreatorSummaryText(lot SectLottery, winners []SectLotteryWinner, delivered int, failed int, unassigned int) string {
	var b strings.Builder
	b.WriteString("✅ 宗门抽奖已开奖\n\n")
	b.WriteString(fmt.Sprintf("活动：%s\n参与人数：%d\n奖品数量：%d\n成功发送：%d\n发送失败：%d\n未分配卡密：%d", lotteryDisplayText(lot.Title, 80, "-"), lot.EntryCount, lot.PrizeCount, delivered, failed, unassigned))
	if len(winners) == 0 {
		b.WriteString("\n\n中奖名单：无")
		return b.String()
	}
	b.WriteString("\n\n中奖名单：\n")
	for i, winner := range winners {
		name := lotteryDisplayText(winner.UserName, 40, "-")
		b.WriteString(fmt.Sprintf("%d. %s(%d) - %s\n", i+1, name, winner.UserID, sectLotteryWinnerStatusText(winner.Status)))
	}
	b.WriteString("\n卡密已仅私聊发送给中奖者，摘要不展示卡密明文。")
	return truncateRunes(strings.TrimSpace(b.String()), 3800)
}

func sectLotteryCreatorContext(userID int64) (SectMember, Sect, error) {
	var member SectMember
	if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return member, Sect{}, err
		}
		return member, Sect{}, errNotInSect
	}
	var sect Sect
	if err := DB.Where("id = ?", member.SectID).First(&sect).Error; err != nil {
		return member, sect, err
	}
	return member, sect, nil
}

func validateSectLotteryUserEligibleTx(tx *gorm.DB, member SectMember) error {
	var u User
	if err := tx.Where("telegram_id = ?", member.UserID).First(&u).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return errUserNotFound
	}
	now := time.Now()
	if !sectLotteryUserEligibleAt(u, now) {
		return errSectLotteryUserIneligible
	}
	usable, err := userHasUsableLocalAbsAccountTxAt(tx, u, now)
	if err != nil {
		return err
	}
	if !usable {
		return errSectLotteryUserIneligible
	}
	return nil
}

func sectLotteryUserEligibleAt(u User, now time.Time) bool {
	if u.IsSuspended || isUserExpiredAt(u, now) || (u.Status != "" && u.Status != "active") {
		return false
	}
	return isFormalAccount(u)
}

func sectLotteryEntryStillEligibleTx(tx *gorm.DB, sectID int64, userID int64) (bool, error) {
	var member SectMember
	if err := tx.Where("sect_id = ? AND user_id = ?", sectID, userID).First(&member).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return false, err
		}
		return false, nil
	}
	if err := validateSectLotteryUserEligibleTx(tx, member); err != nil {
		if errors.Is(err, errUserNotFound) || errors.Is(err, errSectLotteryUserIneligible) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func normalizeSectLotteryTitle(title string) (string, bool) {
	title = strings.TrimSpace(title)
	if len([]rune(title)) < sectLotteryTitleMinRunes || len([]rune(title)) > sectLotteryTitleMaxRunes {
		return "", false
	}
	for _, r := range title {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	if containsDisallowedControl(title, false) {
		return "", false
	}
	return title, true
}

func parseSectLotterySecrets(text string) ([]string, error) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	seen := make(map[string]bool)
	for _, line := range lines {
		secret := strings.TrimSpace(line)
		if secret == "" {
			continue
		}
		if len([]rune(secret)) > sectLotterySecretMaxLen || containsDisallowedControl(secret, false) {
			return nil, fmt.Errorf("卡密不能超过 %d 字，且不能包含控制字符", sectLotterySecretMaxLen)
		}
		if seen[secret] {
			return nil, fmt.Errorf("存在重复卡密：%s", maskSecret(secret))
		}
		seen[secret] = true
		out = append(out, secret)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("未识别到有效卡密")
	}
	if len(out) > sectLotteryMaxSecrets {
		return nil, fmt.Errorf("单次最多导入 %d 条卡密", sectLotteryMaxSecrets)
	}
	return out, nil
}

func formatSectLotteryCreateConfirm(session *SessionState, prizeCount int) string {
	mode := session.GetTemp("sect_lottery_mode")
	rule := ""
	if mode == sectLotteryModeCount {
		rule = fmt.Sprintf("满 %s 人开奖", session.GetTemp("sect_lottery_limit"))
	} else {
		rule = session.GetTemp("sect_lottery_draw_desc")
	}
	return fmt.Sprintf("请确认创建宗门抽奖：\n\n宗门：%s\n活动：%s\n开奖：%s\n奖品数量：%d\n参与门槛：历史贡献 >= %d\n\n确认创建请回复：%s\n取消请回复：取消", session.GetTemp("sect_lottery_sect_name"), session.GetTemp("sect_lottery_title"), rule, prizeCount, sectLotteryMinContribution, sectLotteryCreateConfirmCommand)
}

func sectLotteryActionErrorText(err error) string {
	switch {
	case errors.Is(err, errNotInSect):
		return "❌ 您当前没有加入宗门。"
	case errors.Is(err, errSectNoPermission):
		return "❌ 权限不足。"
	case errors.Is(err, errLotteryNotActive):
		return "❌ 该宗门抽奖不存在或已结束。"
	case errors.Is(err, errLotteryWaitingDraw):
		return "⏳ 该宗门抽奖已到开奖时间，正在等待系统开奖。"
	case errors.Is(err, errLotteryAlreadyJoined):
		return "❌ 您已经参与过该宗门抽奖。"
	case errors.Is(err, errSectLotteryContributionTooLow):
		return fmt.Sprintf("❌ 宗门历史贡献需达到 %d 才可参与。", sectLotteryMinContribution)
	case errors.Is(err, errSectLotteryUserIneligible):
		return "❌ 当前账号状态不可参与宗门抽奖。"
	default:
		return "❌ 操作失败：" + formatPlainError(err)
	}
}

func sectLotteryStatusText(status string) string {
	switch status {
	case sectLotteryStatusActive:
		return "可参与"
	case sectLotteryStatusDrawing:
		return "开奖中"
	case sectLotteryStatusDrawn:
		return "已开奖"
	case sectLotteryStatusCancelled:
		return "已取消"
	default:
		return "未知"
	}
}

func sectLotteryWinnerStatusText(status string) string {
	switch status {
	case sectLotteryWinnerAssigned:
		return "待发送"
	case sectLotteryWinnerDelivered:
		return "已发送"
	case sectLotteryWinnerFailed:
		return "发送失败"
	default:
		return "未知"
	}
}

func sendNoAutoDeletePlainText(bot *tgbotapi.BotAPI, chatID int64, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	_, err := sendNoAutoDelete(bot, msg)
	return err
}

func encryptSectLotterySecret(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("EMPTY_SECT_LOTTERY_SECRET")
	}
	pepper := getSensitivePepper()
	if pepper == "" {
		return "", errSecurityPepperNotConfigured
	}
	key := sha256.Sum256([]byte("sect-lottery-secret:" + pepper))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nil, nonce, []byte(raw), nil)
	payload := append(nonce, sealed...)
	return "gcm$" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func decryptSectLotterySecret(encrypted string) (string, error) {
	encrypted = strings.TrimSpace(encrypted)
	if !strings.HasPrefix(encrypted, "gcm$") {
		return "", fmt.Errorf("INVALID_SECT_LOTTERY_SECRET_CIPHER")
	}
	pepper := getSensitivePepper()
	if pepper == "" {
		return "", errSecurityPepperNotConfigured
	}
	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encrypted, "gcm$"))
	if err != nil {
		return "", err
	}
	key := sha256.Sum256([]byte("sect-lottery-secret:" + pepper))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) <= gcm.NonceSize() {
		return "", fmt.Errorf("INVALID_SECT_LOTTERY_SECRET_PAYLOAD")
	}
	nonce := payload[:gcm.NonceSize()]
	ciphertext := payload[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func shuffleSectLotteryEntries(items []SectLotteryEntry) {
	for i := len(items) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			continue
		}
		j := int(n.Int64())
		items[i], items[j] = items[j], items[i]
	}
}

func shuffleSectLotteryPrizes(items []SectLotteryPrize) {
	for i := len(items) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			continue
		}
		j := int(n.Int64())
		items[i], items[j] = items[j], items[i]
	}
}

func StartSectLotteryScheduler(bot *tgbotapi.BotAPI) {
	go func() {
		remindActiveSectLotteriesOnStartup(bot)
		ticker := time.NewTicker(sectLotterySchedulerInterval)
		defer ticker.Stop()
		for range ticker.C {
			runSectLotterySchedulerTick(bot)
		}
	}()
	log.Println("✅ 宗门抽奖定时开奖调度器已启动。")
}

func remindActiveSectLotteriesOnStartup(bot *tgbotapi.BotAPI) {
	var lots []SectLottery
	if err := DB.Where("status = ?", sectLotteryStatusActive).
		Order("created_at asc").
		Limit(sectLotteryMaxScheduledDraws).
		Find(&lots).Error; err != nil {
		log.Printf("⚠️ 查询待补发提醒宗门抽奖失败: %s", formatPlainError(err))
		return
	}
	for _, lot := range lots {
		success, failed := remindSectLotteryMembers(bot, lot.ID)
		if success > 0 || failed > 0 {
			log.Printf("✅ 宗门抽奖启动补发提醒完成: lottery=%d success=%d failed=%d", lot.ID, success, failed)
		}
	}
}

func runSectLotterySchedulerTick(bot *tgbotapi.BotAPI) {
	now := time.Now()
	var lots []SectLottery
	if err := DB.Where("status = ? AND mode = ? AND draw_at IS NOT NULL AND draw_at <= ?", sectLotteryStatusActive, sectLotteryModeTime, now).
		Order("draw_at asc").
		Limit(sectLotteryMaxScheduledDraws).
		Find(&lots).Error; err != nil {
		log.Printf("⚠️ 查询到期宗门抽奖失败: %s", formatPlainError(err))
		return
	}
	for _, lot := range lots {
		if err := drawSectLottery(bot, lot.ID, "scheduled"); err != nil && !errors.Is(err, errLotteryNotActive) {
			log.Printf("⚠️ 定时宗门抽奖开奖失败: lottery=%d err=%s", lot.ID, formatPlainError(err))
			notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 宗门抽奖 #%d 定时开奖失败：%s", lot.ID, formatPlainError(err)))
		}
	}
}
