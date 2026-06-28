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

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	lotteryModeCount = "count"
	lotteryModeTime  = "time"

	lotteryStatusActive  = "active"
	lotteryStatusDrawing = "drawing"
	lotteryStatusDrawn   = "drawn"
	lotteryStatusClosed  = "closed"

	lotteryWinnerPending = "pending"
	lotteryWinnerClaimed = "claimed"
	lotteryWinnerExpired = "expired"

	lotteryPrizePoints = "points"
	lotteryPrizeInvite = "invite"
	lotteryPrizeRenew  = "renew"
	lotteryPrizePill   = "pill"

	lotteryClaimAttemptPurpose = "lottery_claim"
	lotteryClaimMaxFailures    = 5
	lotteryClaimLockDuration   = 30 * time.Minute
	lotteryClaimValidDuration  = 2 * time.Hour

	lotteryTitleRequirementText     = "2-60 个字，且不能包含换行、制表符或其他控制/分隔字符"
	lotteryClaimCodeRequirementText = "3-40 个字，且不能包含换行、制表符或其他控制/分隔字符"
)

var lotteryDrawTimeLocation = time.FixedZone("CST", 8*3600)

type LotteryActivity struct {
	gorm.Model

	Title          string `gorm:"index;not null"`
	Mode           string `gorm:"index;not null"`
	Status         string `gorm:"index;not null;default:'active'"`
	CreatedByID    int64  `gorm:"index;not null"`
	CreatedBy      string
	AnnounceChatID int64 `gorm:"index"`

	IntroMessageID  int
	ResultMessageID int
	IntroPinned     bool `gorm:"default:false"`
	ResultPinned    bool `gorm:"default:false"`
	PinsUnpinned    bool `gorm:"default:false;index"`

	EntryCost         int
	TotalEntryCost    int
	TotalRefundPoints int
	FusionPoolPoints  int

	ParticipantLimit int
	DrawAt           *time.Time `gorm:"index"`
	DrawnAt          *time.Time `gorm:"index"`

	ClaimCodeHash      string `gorm:"index;not null"`
	ClaimCodeEncrypted string
	ClaimCodePreview   string
	ClaimHours         int
	ClaimDeadlineAt    *time.Time `gorm:"index"`

	WinnersCount int
	ResultNote   string
}

func (LotteryActivity) TableName() string {
	return "lottery_activities"
}

type LotteryPrize struct {
	gorm.Model

	ActivityID  uint   `gorm:"index;not null"`
	PrizeType   string `gorm:"index;not null"`
	Amount      int
	Quantity    int
	DisplayName string
}

func (LotteryPrize) TableName() string {
	return "lottery_prizes"
}

type LotteryParticipant struct {
	gorm.Model

	ActivityID   uint  `gorm:"index;not null"`
	UserID       int64 `gorm:"index;not null"`
	UserName     string
	JoinedAt     time.Time `gorm:"index;not null"`
	EntryCost    int
	RefundPoints int
	IsRefunded   bool `gorm:"default:false"`
	RefundedAt   *time.Time
}

func (LotteryParticipant) TableName() string {
	return "lottery_participants"
}

type LotteryWinner struct {
	gorm.Model

	ActivityID uint  `gorm:"index;not null"`
	UserID     int64 `gorm:"index;not null"`
	UserName   string

	PrizeID          uint
	PrizeType        string `gorm:"index;not null"`
	Amount           int
	Status           string `gorm:"index;not null;default:'pending'"`
	ClaimedAt        *time.Time
	PrizeCodePreview string
}

func (LotteryWinner) TableName() string {
	return "lottery_winners"
}

type LotteryClaimLog struct {
	gorm.Model

	ActivityID uint  `gorm:"index;not null"`
	WinnerID   uint  `gorm:"index"`
	UserID     int64 `gorm:"index;not null"`
	Action     string
	Detail     string
}

func (LotteryClaimLog) TableName() string {
	return "lottery_claim_logs"
}

func createLotteryClaimLogInTx(tx *gorm.DB, logEntry *LotteryClaimLog) error {
	if tx == nil || logEntry == nil {
		return fmt.Errorf("LOTTERY_CLAIM_LOG_INVALID")
	}
	entry := *logEntry
	entry.Action = formatPlainValue(entry.Action)
	entry.Detail = formatPlainValue(entry.Detail)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("LOTTERY_CLAIM_LOG_CREATE_MISSED")
	}
	return nil
}

func createLotteryActivityInTx(tx *gorm.DB, activity *LotteryActivity) error {
	if tx == nil || activity == nil {
		return fmt.Errorf("LOTTERY_ACTIVITY_INVALID")
	}
	entry := *activity
	entry.Title = lotteryDisplayText(entry.Title, 80, "-")
	entry.Mode = formatPlainValue(entry.Mode)
	entry.Status = formatPlainValue(entry.Status)
	entry.CreatedBy = formatPlainValue(entry.CreatedBy)
	entry.ClaimCodePreview = formatPlainValue(entry.ClaimCodePreview)
	entry.ResultNote = formatPlainValue(entry.ResultNote)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("LOTTERY_ACTIVITY_CREATE_MISSED")
	}
	*activity = entry
	return nil
}

func createLotteryPrizeInTx(tx *gorm.DB, prize *LotteryPrize) error {
	if tx == nil || prize == nil {
		return fmt.Errorf("LOTTERY_PRIZE_INVALID")
	}
	entry := *prize
	entry.PrizeType = formatPlainValue(entry.PrizeType)
	entry.DisplayName = lotteryDisplayText(entry.DisplayName, 80, "")
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("LOTTERY_PRIZE_CREATE_MISSED")
	}
	*prize = entry
	return nil
}

func createLotteryParticipantInTx(tx *gorm.DB, participant *LotteryParticipant) error {
	if tx == nil || participant == nil {
		return fmt.Errorf("LOTTERY_PARTICIPANT_INVALID")
	}
	entry := *participant
	entry.UserName = formatPlainValue(entry.UserName)
	res := tx.Create(&entry)
	if res.Error != nil {
		if isUniqueConstraintError(res.Error) {
			return errLotteryAlreadyJoined
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("LOTTERY_PARTICIPANT_CREATE_MISSED")
	}
	*participant = entry
	return nil
}

func createLotteryWinnerInTx(tx *gorm.DB, winner *LotteryWinner) (bool, error) {
	if tx == nil || winner == nil {
		return false, fmt.Errorf("LOTTERY_WINNER_INVALID")
	}
	entry := *winner
	entry.UserName = formatPlainValue(entry.UserName)
	entry.PrizeType = formatPlainValue(entry.PrizeType)
	entry.Status = formatPlainValue(entry.Status)
	entry.PrizeCodePreview = formatPlainValue(entry.PrizeCodePreview)
	res := tx.Create(&entry)
	if res.Error != nil {
		if isUniqueConstraintError(res.Error) {
			return false, nil
		}
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, fmt.Errorf("LOTTERY_WINNER_CREATE_MISSED")
	}
	*winner = entry
	return true, nil
}

func createLotteryLocalUserIfMissingInTx(tx *gorm.DB, user *User) error {
	if tx == nil || user == nil {
		return fmt.Errorf("LOTTERY_LOCAL_USER_INVALID")
	}
	entry := *user
	entry.Username = formatPlainValue(entry.Username)
	if entry.TelegramID == 0 {
		return fmt.Errorf("LOTTERY_LOCAL_USER_INVALID")
	}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	*user = entry
	return nil
}

type lotteryPrizeSpec struct {
	PrizeType   string
	Amount      int
	Quantity    int
	DisplayName string
}

type lotteryDrawWinnerInfo struct {
	UserID      int64
	UserName    string
	PrizeType   string
	Amount      int
	DisplayName string
}

func HandleLotteryCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState, role string) bool {
	if bot == nil || msg == nil || msg.From == nil || msg.Chat == nil || session == nil {
		return false
	}

	step := session.GetStep()
	if strings.HasPrefix(step, "WAITING_LOTTERY_") {
		handleLotteryCreateStep(bot, msg, text, session, role)
		return true
	}

	userID := msg.From.ID
	chatID := msg.Chat.ID
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	if !msg.Chat.IsPrivate() {
		switch {
		case trimmed == "/lottery" || trimmed == "积分抽奖" || trimmed == "抽奖列表":
			registerIncomingGroupCommandForAutoDelete(msg)
			showActiveLotteries(bot, chatID)
			return true
		case strings.HasPrefix(trimmed, "参加抽奖") || strings.HasPrefix(trimmed, "/join_lottery"):
			registerIncomingGroupCommandForAutoDelete(msg)
			handleJoinLotteryCommand(bot, msg, trimmed)
			return true
		case strings.HasPrefix(trimmed, "抽奖领奖"):
			registerIncomingGroupCommandForAutoDelete(msg)
			sendLotteryGroupPlainText(bot, chatID, "🎲 领奖请私聊 Bot 发送“抽奖领奖 暗号”，群内不会处理领奖暗号。")
			return true
		}
		return false
	}

	if role == "super_admin" {
		switch {
		case trimmed == "🎲 积分抽奖" || trimmed == "抽奖管理":
			showLotteryAdminHelp(bot, chatID)
			return true
		case trimmed == "创建积分抽奖" || trimmed == "创建抽奖":
			startLotteryCreateWizard(bot, msg, session)
			return true
		case strings.HasPrefix(trimmed, "抽奖详情"):
			handleLotteryDetailCommand(bot, chatID, trimmed)
			return true
		case strings.HasPrefix(trimmed, "强制开奖"):
			handleForceDrawLotteryCommand(bot, chatID, userID, trimmed)
			return true
		case strings.HasPrefix(trimmed, "取消抽奖"):
			handleCancelLotteryCommand(bot, chatID, userID, trimmed)
			return true
		}
	}

	switch {
	case trimmed == "/lottery" || trimmed == "积分抽奖" || trimmed == "抽奖列表":
		showActiveLotteries(bot, chatID)
		return true
	case strings.HasPrefix(trimmed, "参加抽奖") || strings.HasPrefix(trimmed, "/join_lottery"):
		handleJoinLotteryCommand(bot, msg, trimmed)
		return true
	case strings.HasPrefix(trimmed, "抽奖领奖"):
		code := strings.TrimSpace(strings.TrimPrefix(trimmed, "抽奖领奖"))
		if code == "" {
			sendPlainText(bot, chatID, "请按格式发送：抽奖领奖 暗号")
			return true
		}
		claimLotteryPrizeByCode(bot, chatID, userID, code, false)
		return true
	default:
		return claimLotteryPrizeByCode(bot, chatID, userID, trimmed, true)
	}
}

func startLotteryCreateWizard(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, session *SessionState) {
	if !requireSuperAdmin(bot, msg.Chat.ID, msg.From.ID) {
		return
	}
	session.SetStep("WAITING_LOTTERY_TITLE")
	sendPlainText(bot, msg.Chat.ID, "🎲 创建积分抽奖\n\n第一步：请发送活动名称，"+lotteryTitleRequirementText+"。\n\n发送“取消”可退出。")
	UserSessions.Store(msg.From.ID, session)
}

func showLotteryAdminHelp(bot *tgbotapi.BotAPI, chatID int64) {
	sendPlainText(bot, chatID,
		"🎲 积分抽奖管理\n\n"+
			"可用命令：\n"+
			"创建积分抽奖\n"+
			"抽奖列表\n"+
			"抽奖详情 1\n"+
			"强制开奖 1\n"+
			"取消抽奖 1\n\n"+
			"用户命令：\n"+
			"积分抽奖\n"+
			"参加抽奖 1\n"+
			"抽奖领奖 暗号")
}

func handleLotteryCreateStep(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState, role string) {
	chatID := msg.Chat.ID
	userID := msg.From.ID
	text = strings.TrimSpace(text)

	if text == "取消" {
		clearSession(userID)
		sendMenu(bot, chatID, "已取消创建积分抽奖。", SuperAdminMenu)
		return
	}

	if role != "super_admin" {
		clearSession(userID)
		sendPlainText(bot, chatID, "❌ 权限不足：创建抽奖仅限超级管理员。")
		return
	}

	switch session.GetStep() {
	case "WAITING_LOTTERY_TITLE":
		if !validLotteryTitle(text) {
			sendPlainText(bot, chatID, "活动名称需为"+lotteryTitleRequirementText+"，请重新发送：")
			return
		}
		session.SetTemp("lottery_title", text)
		session.SetStep("WAITING_LOTTERY_MODE")
		sendPlainText(bot, chatID, "第二步：请选择开奖模式。\n\n发送：人数\n或发送：时间")

	case "WAITING_LOTTERY_MODE":
		if text == "人数" || text == "1" {
			session.SetTemp("lottery_mode", lotteryModeCount)
			session.SetStep("WAITING_LOTTERY_LIMIT")
			sendPlainText(bot, chatID, "第三步：请输入满多少人开奖，范围 2-100000：")
			return
		}
		if text == "时间" || text == "2" {
			session.SetTemp("lottery_mode", lotteryModeTime)
			session.SetStep("WAITING_LOTTERY_DRAW_MINUTES")
			sendPlainText(bot, chatID, "第三步：请输入开奖时间。\n\n支持两种格式：\n1. 分钟数，例如：60\n2. 固定时间，例如：2026-06-07 20:00:00")
			return
		}
		sendPlainText(bot, chatID, "模式不识别，请发送“人数”或“时间”。")

	case "WAITING_LOTTERY_LIMIT":
		limit, err := strconv.Atoi(text)
		if err != nil || limit < 2 || limit > 100000 {
			sendPlainText(bot, chatID, "人数限制需为 2-100000 的整数，请重新输入：")
			return
		}
		session.SetTemp("lottery_limit", strconv.Itoa(limit))
		session.SetStep("WAITING_LOTTERY_ENTRY_COST")
		sendPlainText(bot, chatID, "第四步：请输入参与消耗积分，0 表示免费，范围 0-10000：")

	case "WAITING_LOTTERY_DRAW_MINUTES":
		drawAt, drawDesc, err := parseLotteryDrawTimeInput(text, time.Now())
		if err != nil {
			sendPlainText(bot, chatID, formatPlainError(err))
			return
		}
		session.SetTemp("lottery_draw_at", strconv.FormatInt(drawAt.Unix(), 10))
		session.SetTemp("lottery_draw_desc", drawDesc)
		session.SetStep("WAITING_LOTTERY_ENTRY_COST")
		sendPlainText(bot, chatID, "第四步：请输入参与消耗积分，0 表示免费，范围 0-10000：")

	case "WAITING_LOTTERY_ENTRY_COST":
		entryCost, err := strconv.Atoi(text)
		if err != nil || entryCost < 0 || entryCost > 10000 {
			sendPlainText(bot, chatID, "参与消耗积分需为 0-10000 的整数，请重新输入：")
			return
		}
		session.SetTemp("lottery_entry_cost", strconv.Itoa(entryCost))
		session.SetStep("WAITING_LOTTERY_CLAIM_CODE")
		sendPlainText(bot, chatID, "第五步：请设置领奖暗号，"+lotteryClaimCodeRequirementText+"。\n\n暗号不会明文保存到数据库。")

	case "WAITING_LOTTERY_CLAIM_CODE":
		if !validLotteryClaimCode(text) {
			sendPlainText(bot, chatID, "领奖暗号需为"+lotteryClaimCodeRequirementText+"，请重新发送：")
			return
		}
		session.SetTemp("lottery_claim_code", text)
		session.SetTemp("lottery_claim_hours", "2")
		session.SetStep("WAITING_LOTTERY_PRIZES")
		sendPlainText(bot, chatID,
			"第六步：请发送奖品配置，每行一个奖品。\n\n"+
				"格式示例：\n"+
				"积分 100 3\n"+
				"续期 30 2\n"+
				"邀请码 1\n"+
				"丹药 聚灵丹 1\n\n"+
				"说明：\n"+
				"积分 100 3 = 100 积分奖品 3 份\n"+
				"续期 30 2 = 30 天续期卡 2 份\n"+
				"邀请码 1 = 邀请码 1 份\n"+
				"丹药 聚灵丹 1 = 聚灵丹 1 份")

	case "WAITING_LOTTERY_PRIZES":
		specs, err := parseLotteryPrizeSpecs(text)
		if err != nil {
			sendPlainText(bot, chatID, "奖品配置有误："+formatPlainError(err)+"\n\n请重新发送奖品配置。")
			return
		}
		session.SetTemp("lottery_prizes", text)
		session.SetStep("WAITING_LOTTERY_CONFIRM")
		sendPlainText(bot, chatID, formatLotteryCreateConfirm(session, specs))

	case "WAITING_LOTTERY_CONFIRM":
		if text != "确认创建抽奖" {
			clearSession(userID)
			sendPlainText(bot, chatID, "已取消创建积分抽奖。")
			return
		}
		activityID, err := createLotteryActivityFromSession(msg, session)
		if err != nil {
			clearSession(userID)
			sendPlainText(bot, chatID, "❌ 创建失败："+formatPlainError(err))
			return
		}
		clearSession(userID)
		sendPlainText(bot, chatID, fmt.Sprintf("✅ 积分抽奖创建成功，活动 ID：%d\n\n用户可私聊发送：参加抽奖 %d", activityID, activityID))
		announceLotteryCreated(bot, chatID, activityID)
	}
}

func validLotteryTitle(title string) bool {
	title = strings.TrimSpace(title)
	titleLen := len([]rune(title))
	if titleLen < 2 || titleLen > 60 {
		return false
	}
	return !containsDisallowedControl(title, false)
}

func validLotteryClaimCode(code string) bool {
	code = strings.TrimSpace(code)
	codeLen := len([]rune(code))
	if codeLen < 3 || codeLen > 40 {
		return false
	}
	return !containsDisallowedControl(code, false)
}

func parseLotteryPrizeSpecs(raw string) ([]lotteryPrizeSpec, error) {
	lines := strings.Split(strings.TrimSpace(raw), "\n")
	specs := make([]lotteryPrizeSpec, 0, len(lines))
	totalQuantity := 0

	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("每行至少需要奖品类型和数量")
		}

		switch fields[0] {
		case "积分":
			if len(fields) != 3 {
				return nil, fmt.Errorf("积分格式应为：积分 100 3")
			}
			amount, err := strconv.Atoi(fields[1])
			if err != nil || amount < 1 || amount > 100000 {
				return nil, fmt.Errorf("积分奖品数量需为 1-100000")
			}
			qty, err := strconv.Atoi(fields[2])
			if err != nil || qty < 1 || qty > 100 {
				return nil, fmt.Errorf("积分奖品份数需为 1-100")
			}
			specs = append(specs, lotteryPrizeSpec{
				PrizeType:   lotteryPrizePoints,
				Amount:      amount,
				Quantity:    qty,
				DisplayName: fmt.Sprintf("%d 积分", amount),
			})
			totalQuantity += qty

		case "续期":
			if len(fields) != 3 {
				return nil, fmt.Errorf("续期格式应为：续期 30 2")
			}
			days, err := strconv.Atoi(fields[1])
			if err != nil || days < 1 || days > 365 {
				return nil, fmt.Errorf("续期天数需为 1-365")
			}
			qty, err := strconv.Atoi(fields[2])
			if err != nil || qty < 1 || qty > 100 {
				return nil, fmt.Errorf("续期卡份数需为 1-100")
			}
			specs = append(specs, lotteryPrizeSpec{
				PrizeType:   lotteryPrizeRenew,
				Amount:      days,
				Quantity:    qty,
				DisplayName: fmt.Sprintf("%d 天续期卡", days),
			})
			totalQuantity += qty

		case "邀请码":
			if len(fields) != 2 {
				return nil, fmt.Errorf("邀请码格式应为：邀请码 1")
			}
			qty, err := strconv.Atoi(fields[1])
			if err != nil || qty < 1 || qty > 100 {
				return nil, fmt.Errorf("邀请码份数需为 1-100")
			}
			specs = append(specs, lotteryPrizeSpec{
				PrizeType:   lotteryPrizeInvite,
				Amount:      1,
				Quantity:    qty,
				DisplayName: "邀请码",
			})
			totalQuantity += qty

		case "丹药":
			if len(fields) != 3 {
				return nil, fmt.Errorf("丹药格式应为：丹药 聚灵丹 1")
			}
			itemName := strings.TrimSpace(fields[1])
			if !isLotteryPillPrizeName(itemName) {
				return nil, fmt.Errorf("不支持的丹药：%s", itemName)
			}
			qty, err := strconv.Atoi(fields[2])
			if err != nil || qty < 1 || qty > 100 {
				return nil, fmt.Errorf("丹药份数需为 1-100")
			}
			specs = append(specs, lotteryPrizeSpec{
				PrizeType:   lotteryPrizePill,
				Amount:      1,
				Quantity:    qty,
				DisplayName: itemName,
			})
			totalQuantity += qty

		default:
			return nil, fmt.Errorf("未知奖品类型：%s", fields[0])
		}
	}

	if len(specs) == 0 {
		return nil, fmt.Errorf("至少配置一个奖品")
	}
	if totalQuantity > 100 {
		return nil, fmt.Errorf("第一版单个活动总奖品份数最多 100")
	}
	return specs, nil
}

func isLotteryPillPrizeName(itemName string) bool {
	switch strings.TrimSpace(itemName) {
	case "聚灵丹", "九转造化丹", "万年仙玉髓",
		"引灵入体", "筑基丹", "降尘丹", "九曲灵参丹", "补天丹":
		return true
	default:
		return false
	}
}

func parseLotteryDrawTimeInput(raw string, now time.Time) (time.Time, string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, "", fmt.Errorf("开奖时间不能为空，请输入分钟数或固定时间，例如：2026-06-07 20:00:00")
	}

	if minutes, err := strconv.Atoi(raw); err == nil {
		if minutes < 1 || minutes > 10080 {
			return time.Time{}, "", fmt.Errorf("分钟数需为 1-10080，请重新输入：")
		}
		drawAt := now.Add(time.Duration(minutes) * time.Minute)
		return drawAt, fmt.Sprintf("%d 分钟后开奖（%s）", minutes, drawAt.Format("2006-01-02 15:04:05")), nil
	}

	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
	}
	for _, layout := range layouts {
		drawAt, err := time.ParseInLocation(layout, raw, lotteryDrawTimeLocation)
		if err != nil {
			continue
		}
		if !drawAt.After(now) {
			return time.Time{}, "", fmt.Errorf("开奖时间必须晚于当前时间。当前时间：%s", now.Format("2006-01-02 15:04:05"))
		}
		return drawAt, "固定时间开奖：" + drawAt.Format("2006-01-02 15:04:05"), nil
	}

	return time.Time{}, "", fmt.Errorf("时间格式不正确。请发送分钟数，或固定时间格式：YYYY-MM-DD HH:MM:SS，例如：2026-06-07 20:00:00")
}

func parseLotterySessionInt(session *SessionState, key string, minValue, maxValue int) (int, error) {
	raw := strings.TrimSpace(session.GetTemp(key))
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid lottery session integer: key=%s value=%s err=%w", formatPlainValue(key), formatPlainValue(raw), err)
	}
	if value < minValue || value > maxValue {
		return 0, fmt.Errorf("lottery session integer out of range: key=%s value=%d range=%d-%d", formatPlainValue(key), value, minValue, maxValue)
	}
	return value, nil
}

func parseLotterySessionUnixTime(session *SessionState, key string, now time.Time) (time.Time, error) {
	raw := strings.TrimSpace(session.GetTemp(key))
	unixValue, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid lottery session time: key=%s value=%s err=%w", formatPlainValue(key), formatPlainValue(raw), err)
	}
	drawAt := time.Unix(unixValue, 0)
	if !drawAt.After(now) {
		return time.Time{}, fmt.Errorf("lottery session time is not in future: key=%s value=%s", formatPlainValue(key), formatPlainValue(raw))
	}
	return drawAt, nil
}

func formatLotteryCreateConfirm(session *SessionState, specs []lotteryPrizeSpec) string {
	mode := session.GetTemp("lottery_mode")
	modeText := "满人开奖"
	ruleText := fmt.Sprintf("满 %s 人开奖", session.GetTemp("lottery_limit"))
	if mode == lotteryModeTime {
		modeText = "定时开奖"
		ruleText = session.GetTemp("lottery_draw_desc")
	}

	var b strings.Builder
	b.WriteString("请核对抽奖活动：\n\n")
	b.WriteString("名称：" + session.GetTemp("lottery_title") + "\n")
	b.WriteString("模式：" + modeText + "\n")
	b.WriteString("规则：" + ruleText + "\n")
	entryCost, entryCostErr := parseLotterySessionInt(session, "lottery_entry_cost", 0, 10000)
	if entryCostErr != nil {
		b.WriteString("参与消耗：读取失败\n")
	} else {
		b.WriteString(fmt.Sprintf("参与消耗：%d 积分\n", entryCost))
	}
	if entryCostErr == nil && entryCost > 0 {
		b.WriteString("天道奖池：按总参与积分的 10% 注入\n")
	}
	b.WriteString("领奖期限：开奖后 2 小时\n")
	b.WriteString("暗号预览：" + maskSecret(session.GetTemp("lottery_claim_code")) + "\n\n")
	b.WriteString("奖品：\n")
	for _, spec := range specs {
		b.WriteString(fmt.Sprintf("- %s x %d\n", spec.DisplayName, spec.Quantity))
	}
	b.WriteString("\n确认创建请回复：确认创建抽奖\n取消请回复：取消")
	return b.String()
}

func createLotteryActivityFromSession(msg *tgbotapi.Message, session *SessionState) (uint, error) {
	specs, err := parseLotteryPrizeSpecs(session.GetTemp("lottery_prizes"))
	if err != nil {
		return 0, err
	}

	claimCode := strings.TrimSpace(session.GetTemp("lottery_claim_code"))
	claimHash := hashSensitiveToken(claimCode)
	if claimHash == "" {
		return 0, fmt.Errorf("系统安全密钥未配置，无法保存领奖暗号")
	}
	claimEncrypted, err := encryptLotteryClaimCode(claimCode)
	if err != nil {
		return 0, fmt.Errorf("领奖暗号加密失败")
	}

	mode := session.GetTemp("lottery_mode")
	entryCost, err := parseLotterySessionInt(session, "lottery_entry_cost", 0, 10000)
	if err != nil {
		return 0, err
	}

	activity := LotteryActivity{
		Title:              session.GetTemp("lottery_title"),
		Mode:               mode,
		Status:             lotteryStatusActive,
		CreatedByID:        msg.From.ID,
		CreatedBy:          getTelegramDisplayName(msg.From),
		ClaimCodeHash:      claimHash,
		ClaimCodeEncrypted: claimEncrypted,
		ClaimCodePreview:   maskSecret(claimCode),
		ClaimHours:         2,
		EntryCost:          entryCost,
	}

	if mode == lotteryModeCount {
		limit, err := parseLotterySessionInt(session, "lottery_limit", 2, 100000)
		if err != nil {
			return 0, err
		}
		activity.ParticipantLimit = limit
	} else if mode == lotteryModeTime {
		drawAt, err := parseLotterySessionUnixTime(session, "lottery_draw_at", time.Now())
		if err != nil {
			return 0, err
		}
		activity.DrawAt = &drawAt
	} else {
		return 0, fmt.Errorf("开奖模式异常")
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		if err := createLotteryActivityInTx(tx, &activity); err != nil {
			return err
		}
		for _, spec := range specs {
			if err := createLotteryPrizeInTx(tx, &LotteryPrize{
				ActivityID:  activity.ID,
				PrizeType:   spec.PrizeType,
				Amount:      spec.Amount,
				Quantity:    spec.Quantity,
				DisplayName: spec.DisplayName,
			}); err != nil {
				return err
			}
		}
		if err := writeAuditLogInTx(tx, msg.From.ID, "CREATE_LOTTERY", fmt.Sprintf("%d", activity.ID), 0, fmt.Sprintf("超级管理员创建积分抽奖活动，标题：%s", formatPlainValue(activity.Title))); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return activity.ID, nil
}

func showActiveLotteries(bot *tgbotapi.BotAPI, chatID int64) {
	var activities []LotteryActivity
	if err := DB.Where("status IN ?", []string{lotteryStatusActive, lotteryStatusDrawn}).
		Order("id desc").
		Limit(20).
		Find(&activities).Error; err != nil {
		sendPlainText(bot, chatID, "❌ 查询抽奖活动失败，请稍后再试。")
		return
	}
	if len(activities) == 0 {
		sendPlainText(bot, chatID, "当前没有可参与或可领奖的积分抽奖。")
		return
	}

	var b strings.Builder
	b.WriteString("🎲 积分抽奖活动\n\n")
	for _, activity := range activities {
		participantCount, participantErr := countLotteryParticipants(activity.ID)
		statusText := lotteryStatusText(activity.Status)
		b.WriteString(fmt.Sprintf("#%d %s\n", activity.ID, lotteryDisplayText(activity.Title, 80, "-")))
		b.WriteString(fmt.Sprintf("状态：%s\n", statusText))
		if activity.Status == lotteryStatusActive {
			if activity.Mode == lotteryModeCount {
				if participantErr != nil {
					b.WriteString("进度：读取失败\n")
				} else {
					b.WriteString(fmt.Sprintf("进度：%d/%d 人\n", participantCount, activity.ParticipantLimit))
				}
			} else if activity.DrawAt != nil {
				b.WriteString(fmt.Sprintf("开奖时间：%s\n", activity.DrawAt.Format("2006-01-02 15:04")))
			}
			b.WriteString(fmt.Sprintf("参与：参加抽奖 %d\n\n", activity.ID))
		} else {
			b.WriteString("已开奖：中奖者请发送“抽奖领奖 暗号”领取。\n\n")
		}
	}
	sendPlainText(bot, chatID, b.String())
}

func announceLotteryCreated(bot *tgbotapi.BotAPI, adminChatID int64, activityID uint) {
	if AppConfig.NoticeGroupID == 0 {
		sendPlainText(bot, adminChatID, "⚠️ 抽奖已创建，但未配置 NOTICE_GROUP_ID，无法自动发布到群内。")
		return
	}

	var activity LotteryActivity
	if err := DB.First(&activity, activityID).Error; err != nil {
		sendPlainText(bot, adminChatID, "⚠️ 抽奖已创建，但读取活动信息失败，无法自动发布到群内。")
		return
	}

	var prizes []LotteryPrize
	if err := DB.Where("activity_id = ? AND deleted_at IS NULL", activityID).Order("id asc").Find(&prizes).Error; err != nil {
		sendPlainText(bot, adminChatID, "⚠️ 抽奖已创建，但读取奖品信息失败，无法自动发布到群内。")
		return
	}

	ruleText := ""
	if activity.Mode == lotteryModeCount {
		ruleText = fmt.Sprintf("满 %d 人自动开奖", activity.ParticipantLimit)
	} else if activity.DrawAt != nil {
		ruleText = "开奖时间：" + activity.DrawAt.Format("2006-01-02 15:04:05")
	} else {
		ruleText = "定时开奖"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🎲 积分抽奖开启：%s\n\n", lotteryDisplayText(activity.Title, 80, "-")))
	b.WriteString("规则：" + ruleText + "\n")
	b.WriteString(fmt.Sprintf("参与消耗：%d 积分\n", activity.EntryCost))
	if activity.EntryCost > 0 {
		b.WriteString("天道奖池：本期总参与积分的 10%\n")
	}
	b.WriteString("领奖期限：开奖后 2 小时\n\n")
	b.WriteString("奖品：\n")
	for _, prize := range prizes {
		b.WriteString(fmt.Sprintf("- %s x %d\n", lotteryDisplayText(prize.DisplayName, 80, "-"), prize.Quantity))
	}
	b.WriteString(fmt.Sprintf("\n参与方式：发送 参加抽奖 %d", activity.ID))

	text := b.String()
	if enqueueTelegramAsync(telegramAsyncJob{
		Kind:        "lottery_intro_announce",
		DedupeKey:   fmt.Sprintf("lottery_intro:%d", activityID),
		Priority:    telegramAsyncPriorityNormal,
		MaxAttempts: 1,
		Send: func() error {
			return sendLotteryIntroAnnouncementSync(bot, adminChatID, activityID, AppConfig.NoticeGroupID, text)
		},
	}) {
		return
	}

	log.Printf("⚠️ 抽奖创建群内公告异步入队失败，改为同步发送: activity=%d chat=%d", activityID, AppConfig.NoticeGroupID)
	if err := sendLotteryIntroAnnouncementSync(bot, adminChatID, activityID, AppConfig.NoticeGroupID, text); err != nil {
		log.Printf("⚠️ 抽奖创建群内公告同步发送失败: activity=%d chat=%d err=%s", activityID, AppConfig.NoticeGroupID, formatTelegramSendError(err))
	}
}

func sendLotteryIntroAnnouncementSync(bot *tgbotapi.BotAPI, adminChatID int64, activityID uint, targetChatID int64, text string) error {
	sentMsg, err := sendLotteryGroupPersistentText(bot, targetChatID, text)
	if err != nil {
		log.Printf("⚠️ 抽奖创建群内公告发送失败: activity=%d chat=%d err=%s", activityID, targetChatID, formatTelegramSendError(err))
		if adminChatID != 0 {
			sendPlainText(bot, adminChatID, "⚠️ 抽奖已创建，但群内公告发送失败，请检查 Bot 群权限。")
		}
		return err
	}

	introPinned := pinLotteryMessage(bot, targetChatID, sentMsg.MessageID, activityID, "intro")
	res := DB.Model(&LotteryActivity{}).
		Where("id = ?", activityID).
		Updates(map[string]interface{}{
			"announce_chat_id": targetChatID,
			"intro_message_id": sentMsg.MessageID,
			"intro_pinned":     introPinned,
		})
	if res.Error != nil {
		err := res.Error
		log.Printf("⚠️ 抽奖公告群记录失败: activity=%d chat=%d err=%s", activityID, targetChatID, formatPlainError(err))
	}
	if res.Error == nil && res.RowsAffected == 0 {
		log.Printf("lottery intro announcement record update missed: activity=%d chat=%d message=%d", activityID, targetChatID, sentMsg.MessageID)
	}
	return nil
}

func handleJoinLotteryCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) {
	parts := strings.Fields(text)
	if len(parts) < 2 {
		sendPlainText(bot, msg.Chat.ID, "请按格式发送：参加抽奖 活动ID")
		return
	}
	id, err := strconv.ParseUint(parts[len(parts)-1], 10, 64)
	if err != nil || id == 0 {
		sendPlainText(bot, msg.Chat.ID, "活动 ID 格式不正确。")
		return
	}

	sourceChatID := int64(0)
	if !msg.Chat.IsPrivate() {
		sourceChatID = msg.Chat.ID
	}
	joinedCount, shouldDraw, err := joinLotteryActivity(uint(id), msg.From, sourceChatID)
	if err != nil {
		sendPlainText(bot, msg.Chat.ID, lotteryJoinErrorText(err))
		return
	}

	successText := fmt.Sprintf("✅ 已成功参与抽奖 #%d，当前已成功参与 %d 人。", id, joinedCount)
	if !msg.Chat.IsPrivate() {
		sendLotteryReplyPlainText(bot, msg, successText)
	} else {
		sendPlainText(bot, msg.Chat.ID, successText)
	}
	if shouldDraw {
		go func() {
			if err := drawLotteryActivity(bot, uint(id), "participant_limit", 0); err != nil {
				log.Printf("⚠️ 满人抽奖自动开奖失败: activity=%d err=%s", id, formatPlainError(err))
				notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 满人抽奖 #%d 自动开奖失败：%s", id, formatPlainError(err)))
			}
		}()
	}
}

func joinLotteryActivity(activityID uint, tgUser *tgbotapi.User, sourceChatID int64) (int64, bool, error) {
	if tgUser == nil {
		return 0, false, errUserNotFound
	}

	now := time.Now()
	userID := tgUser.ID
	userName := getTelegramDisplayName(tgUser)
	var joinedCount int64
	shouldDraw := false

	err := DB.Transaction(func(tx *gorm.DB) error {
		var txJoinedCount int64
		txShouldDraw := false
		var activity LotteryActivity
		if err := tx.Where("id = ? AND status = ?", activityID, lotteryStatusActive).First(&activity).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errLotteryNotActive
			}
			return err
		}

		if activity.Mode == lotteryModeTime && activity.DrawAt != nil && now.After(*activity.DrawAt) {
			return errLotteryWaitingDraw
		}

		if sourceChatID != 0 && activity.AnnounceChatID == 0 {
			res := tx.Model(&LotteryActivity{}).
				Where("id = ? AND status = ? AND announce_chat_id = 0", activityID, lotteryStatusActive).
				Update("announce_chat_id", sourceChatID)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				var current LotteryActivity
				if err := tx.Select("id", "status", "announce_chat_id").First(&current, activityID).Error; err != nil {
					return err
				}
				if current.Status != lotteryStatusActive {
					return errLotteryNotActive
				}
				if current.AnnounceChatID == 0 {
					return fmt.Errorf("LOTTERY_JOIN_ANNOUNCE_CHAT_UPDATE_MISSED")
				}
			}
		}

		var existingUser User
		if err := tx.Where("telegram_id = ?", userID).First(&existingUser).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			existingUser = User{
				TelegramID: userID,
				Username:   lotteryUniqueUsername(tgUser),
				AbsUserID:  "",
				Points:     0,
			}
			if err := createLotteryLocalUserIfMissingInTx(tx, &existingUser); err != nil {
				return err
			}
		}

		if activity.Mode == lotteryModeCount {
			var currentCount int64
			if err := tx.Model(&LotteryParticipant{}).
				Where("activity_id = ? AND deleted_at IS NULL", activityID).
				Count(&currentCount).Error; err != nil {
				return err
			}
			if currentCount >= int64(activity.ParticipantLimit) {
				return errLotteryFull
			}
		}

		participant := LotteryParticipant{
			ActivityID: activityID,
			UserID:     userID,
			UserName:   userName,
			JoinedAt:   now,
			EntryCost:  activity.EntryCost,
		}
		if err := createLotteryParticipantInTx(tx, &participant); err != nil {
			return err
		}

		if activity.EntryCost > 0 {
			if err := applyPointDeltaInTx(
				tx,
				userID,
				-activity.EntryCost,
				"lottery_entry_cost",
				fmt.Sprintf("参与积分抽奖 #%d《%s》，消耗 %d 积分", activity.ID, lotteryPointDescriptionTitle(activity.Title), activity.EntryCost),
				"lottery",
				fmt.Sprintf("%d", activity.ID),
			); err != nil {
				return err
			}
		}

		if err := tx.Model(&LotteryParticipant{}).
			Where("activity_id = ? AND deleted_at IS NULL", activityID).
			Count(&txJoinedCount).Error; err != nil {
			return err
		}
		if activity.Mode == lotteryModeCount && txJoinedCount > int64(activity.ParticipantLimit) {
			return errLotteryFull
		}

		txShouldDraw = activity.Mode == lotteryModeCount && txJoinedCount >= int64(activity.ParticipantLimit)
		joinedCount = txJoinedCount
		shouldDraw = txShouldDraw
		return nil
	})

	if err != nil {
		return 0, false, err
	}
	return joinedCount, shouldDraw, nil
}

func lotteryUniqueUsername(tgUser *tgbotapi.User) string {
	if tgUser == nil {
		return "tg_unknown"
	}
	if strings.TrimSpace(tgUser.UserName) != "" {
		return strings.TrimSpace(tgUser.UserName)
	}
	return fmt.Sprintf("tg_%d", tgUser.ID)
}

func lotteryJoinErrorCode(err error) string {
	switch {
	case errors.Is(err, errLotteryNotActive):
		return "LOTTERY_NOT_ACTIVE"
	case errors.Is(err, errLotteryWaitingDraw):
		return "LOTTERY_WAITING_DRAW"
	case errors.Is(err, errLotteryFull):
		return "LOTTERY_FULL"
	case errors.Is(err, errLotteryAlreadyJoined):
		return "ALREADY_JOINED"
	case errors.Is(err, errPointsNotEnough):
		return "POINTS_NOT_ENOUGH"
	case errors.Is(err, errUserNotFound):
		return "USER_NOT_FOUND"
	case err != nil:
		return fallbackBusinessErrorCode(err)
	default:
		return ""
	}
}

func lotteryJoinErrorText(err error) string {
	if err == nil {
		return ""
	}
	switch lotteryJoinErrorCode(err) {
	case "LOTTERY_NOT_ACTIVE":
		return "❌ 该抽奖不存在或已不可参与。"
	case "LOTTERY_WAITING_DRAW":
		return "⏳ 该抽奖已到开奖时间，正在等待系统开奖。"
	case "LOTTERY_FULL":
		return "❌ 该抽奖人数已满，正在等待开奖。"
	case "ALREADY_JOINED":
		return "你已经参与过这个抽奖了。"
	case "POINTS_NOT_ENOUGH":
		return "❌ 你的积分不足，无法参与本次抽奖。"
	default:
		log.Printf("⚠️ 参与抽奖失败: err=%s", formatPlainError(err))
		return "❌ 参与抽奖失败，请稍后再试。"
	}
}

func drawLotteryActivity(bot *tgbotapi.BotAPI, activityID uint, reason string, actorID int64) error {
	var activity LotteryActivity
	var participantCount int
	var winnerCount int
	var drawWinners []lotteryDrawWinnerInfo
	totalEntryCost := 0
	fusionPoolPoints := 0
	fusionBurst := false

	err := runFusionPoolLockedTransaction(func(tx *gorm.DB) error {
		res := tx.Model(&LotteryActivity{}).
			Where("id = ? AND status = ?", activityID, lotteryStatusActive).
			Update("status", lotteryStatusDrawing)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errLotteryNotActive
		}

		if err := tx.First(&activity, activityID).Error; err != nil {
			return err
		}

		var participants []LotteryParticipant
		if err := tx.Where("activity_id = ? AND deleted_at IS NULL", activityID).
			Order("id asc").
			Find(&participants).Error; err != nil {
			return err
		}
		participantCount = len(participants)
		for _, participant := range participants {
			if participant.EntryCost > 0 {
				totalEntryCost += participant.EntryCost
			}
		}

		now := time.Now()
		claimHours := int(lotteryClaimDuration(activity) / time.Hour)
		claimDeadline := lotteryClaimDeadline(activity, now)
		activity.DrawnAt = &now
		activity.ClaimHours = claimHours
		activity.ClaimDeadlineAt = &claimDeadline
		activity.Status = lotteryStatusDrawn

		if len(participants) == 0 {
			activity.Status = lotteryStatusClosed
			closeRes := tx.Model(&LotteryActivity{}).
				Where("id = ?", activityID).
				Updates(map[string]interface{}{
					"status":            lotteryStatusClosed,
					"drawn_at":          &now,
					"claim_hours":       claimHours,
					"claim_deadline_at": &claimDeadline,
					"result_note":       "无人参与，活动自动关闭",
				})
			if closeRes.Error != nil {
				return closeRes.Error
			}
			if closeRes.RowsAffected == 0 {
				return fmt.Errorf("LOTTERY_DRAW_CLOSE_UPDATE_MISSED")
			}
			if reason == "manual" && actorID != 0 {
				if err := writeAuditLogInTx(tx, actorID, "FORCE_DRAW_LOTTERY", fmt.Sprintf("%d", activityID), 0, "超级管理员手动触发抽奖开奖，活动无人参与并关闭"); err != nil {
					return err
				}
			}
			return nil
		}

		var prizes []LotteryPrize
		if err := tx.Where("activity_id = ? AND deleted_at IS NULL", activityID).
			Order("id asc").
			Find(&prizes).Error; err != nil {
			return err
		}

		prizeSlots := make([]LotteryPrize, 0)
		for _, prize := range prizes {
			for i := 0; i < prize.Quantity; i++ {
				prizeSlots = append(prizeSlots, prize)
			}
		}
		if len(prizeSlots) == 0 {
			return fmt.Errorf("NO_PRIZES")
		}

		shuffleLotteryParticipants(participants)
		shuffleLotteryPrizes(prizeSlots)

		winnerCount = len(prizeSlots)
		if len(participants) < winnerCount {
			winnerCount = len(participants)
		}

		for i := 0; i < winnerCount; i++ {
			p := participants[i]
			prize := prizeSlots[i]
			winner := LotteryWinner{
				ActivityID: activityID,
				UserID:     p.UserID,
				UserName:   p.UserName,
				PrizeID:    prize.ID,
				PrizeType:  prize.PrizeType,
				Amount:     prize.Amount,
				Status:     lotteryWinnerPending,
			}
			created, err := createLotteryWinnerInTx(tx, &winner)
			if err != nil {
				return err
			}
			if !created {
				continue
			}
			drawWinners = append(drawWinners, lotteryDrawWinnerInfo{
				UserID:      p.UserID,
				UserName:    p.UserName,
				PrizeType:   prize.PrizeType,
				Amount:      prize.Amount,
				DisplayName: prize.DisplayName,
			})
		}

		fusionPoolPoints = totalEntryCost / 10
		if fusionPoolPoints > 0 {
			var err error
			_, fusionBurst, err = addPointsToFusionPoolInTx(tx, fusionPoolPoints)
			if err != nil {
				return err
			}
		}
		activity.TotalEntryCost = totalEntryCost
		activity.TotalRefundPoints = 0
		activity.FusionPoolPoints = fusionPoolPoints

		drawRes := tx.Model(&LotteryActivity{}).
			Where("id = ?", activityID).
			Updates(map[string]interface{}{
				"status":              lotteryStatusDrawn,
				"drawn_at":            &now,
				"claim_hours":         claimHours,
				"claim_deadline_at":   &claimDeadline,
				"winners_count":       winnerCount,
				"total_entry_cost":    totalEntryCost,
				"total_refund_points": 0,
				"fusion_pool_points":  fusionPoolPoints,
				"result_note":         fmt.Sprintf("reason=%s participants=%d winners=%d", formatPlainValue(reason), participantCount, winnerCount),
			})
		if drawRes.Error != nil {
			return drawRes.Error
		}
		if drawRes.RowsAffected == 0 {
			return fmt.Errorf("LOTTERY_DRAW_RESULT_UPDATE_MISSED")
		}
		if reason == "manual" && actorID != 0 {
			if err := writeAuditLogInTx(tx, actorID, "FORCE_DRAW_LOTTERY", fmt.Sprintf("%d", activityID), 0, fmt.Sprintf("超级管理员手动触发抽奖开奖，参与=%d 中奖=%d", participantCount, winnerCount)); err != nil {
				return err
			}
		}
		return nil
	})

	if err != nil {
		return err
	}

	notifyLotteryDrawn(bot, activity, participantCount, winnerCount, drawWinners)
	if fusionBurst {
		notifyFusionPoolBurst(bot, activity.AnnounceChatID, "积分抽奖福泽回流")
	}
	return nil
}

func lotteryClaimDuration(_ LotteryActivity) time.Duration {
	return lotteryClaimValidDuration
}

func lotteryClaimDeadline(activity LotteryActivity, now time.Time) time.Time {
	return now.Add(lotteryClaimDuration(activity))
}

func notifyLotteryDrawn(bot *tgbotapi.BotAPI, activity LotteryActivity, participantCount int, winnerCount int, winners []lotteryDrawWinnerInfo) {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("🎲 积分抽奖 #%d《%s》已开奖\n\n", activity.ID, lotteryDisplayText(activity.Title, 80, "-")))
	b.WriteString(fmt.Sprintf("参与人数：%d\n", participantCount))
	b.WriteString(fmt.Sprintf("中奖名额：%d\n", winnerCount))
	if activity.TotalEntryCost > 0 {
		b.WriteString(fmt.Sprintf("总参与积分：%d\n", activity.TotalEntryCost))
		b.WriteString(fmt.Sprintf("注入天道奖池：%d\n", activity.FusionPoolPoints))
	}
	if activity.ClaimDeadlineAt != nil {
		b.WriteString("领奖截止：" + activity.ClaimDeadlineAt.Format("2006-01-02 15:04") + "\n")
	}
	if len(winners) > 0 {
		b.WriteString("\n中奖者：\n")
		for i, winner := range winners {
			b.WriteString(fmt.Sprintf("%d. %s - %s\n", i+1, lotteryDisplayText(winner.UserName, 80, "-"), lotteryPrizeDisplayText(winner.PrizeType, winner.Amount, winner.DisplayName)))
		}
		b.WriteString("\nBot 已私聊中奖者发送领奖暗号。")
	} else {
		b.WriteString("\n本期无人中奖。")
	}

	text := b.String()
	targetChatID := activity.AnnounceChatID
	if targetChatID == 0 {
		targetChatID = AppConfig.NoticeGroupID
	}
	if targetChatID != 0 {
		activityID := activity.ID
		if !enqueueTelegramAsync(telegramAsyncJob{
			Kind:        "lottery_result_announce",
			DedupeKey:   fmt.Sprintf("lottery_result:%d", activityID),
			Priority:    telegramAsyncPriorityNormal,
			MaxAttempts: 1,
			Send: func() error {
				return sendLotteryResultAnnouncementSync(bot, activityID, targetChatID, text)
			},
		}) {
			log.Printf("⚠️ 抽奖开奖结果公告异步入队失败，改为同步发送: activity=%d chat=%d", activityID, targetChatID)
			if err := sendLotteryResultAnnouncementSync(bot, activityID, targetChatID, text); err != nil {
				log.Printf("⚠️ 抽奖开奖结果公告同步发送失败: activity=%d chat=%d err=%s", activityID, targetChatID, formatTelegramSendError(err))
			}
		}
	}
	notifySuperAdminsPlain(bot, text)
	notifyLotteryWinnersPrivately(bot, activity, winners)
}

func sendLotteryResultAnnouncementSync(bot *tgbotapi.BotAPI, activityID uint, targetChatID int64, text string) error {
	sentMsg, err := sendLotteryGroupPersistentText(bot, targetChatID, text)
	if err != nil {
		log.Printf("⚠️ 抽奖开奖群内公告发送失败: activity=%d chat=%d err=%s", activityID, targetChatID, formatTelegramSendError(err))
		return err
	}

	resultPinned := pinLotteryMessage(bot, targetChatID, sentMsg.MessageID, activityID, "result")
	res := DB.Model(&LotteryActivity{}).
		Where("id = ?", activityID).
		Updates(map[string]interface{}{
			"announce_chat_id":  targetChatID,
			"result_message_id": sentMsg.MessageID,
			"result_pinned":     resultPinned,
		})
	if res.Error != nil {
		err := res.Error
		log.Printf("⚠️ 抽奖结果公告记录失败: activity=%d chat=%d message=%d err=%s", activityID, targetChatID, sentMsg.MessageID, formatPlainError(err))
	}
	if res.Error == nil && res.RowsAffected == 0 {
		log.Printf("lottery result announcement record update missed: activity=%d chat=%d message=%d", activityID, targetChatID, sentMsg.MessageID)
	}
	return nil
}

func notifyLotteryWinnersPrivately(bot *tgbotapi.BotAPI, activity LotteryActivity, winners []lotteryDrawWinnerInfo) {
	if len(winners) == 0 {
		return
	}

	claimCode, err := decryptLotteryClaimCode(activity.ClaimCodeEncrypted)
	if err != nil {
		log.Printf("⚠️ 抽奖暗号解密失败，无法私聊中奖者: activity=%d err=%s", activity.ID, formatPlainError(err))
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 抽奖 #%d 已开奖，但领奖暗号解密失败，无法自动私聊中奖者。", activity.ID))
		return
	}

	deadline := "开奖后 2 小时内"
	if activity.ClaimDeadlineAt != nil {
		deadline = activity.ClaimDeadlineAt.Format("2006-01-02 15:04") + " 前"
	}

	for _, winner := range winners {
		text := fmt.Sprintf(
			"🎲 恭喜中奖！\n\n活动：%s\n奖品：%s\n领奖暗号：%s\n\n请在 %s 私聊发送：抽奖领奖 %s\n逾期未领视为作废。",
			lotteryDisplayText(activity.Title, 80, "-"),
			lotteryPrizeDisplayText(winner.PrizeType, winner.Amount, winner.DisplayName),
			claimCode,
			deadline,
			claimCode,
		)
		if err := sendLotteryPlainText(bot, winner.UserID, text); err != nil {
			log.Printf("⚠️ 抽奖中奖私聊提醒失败: activity=%d user=%d err=%s", activity.ID, winner.UserID, formatTelegramSendError(err))
			notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 抽奖 #%d 中奖用户 %d 私聊提醒失败，请人工通知。", activity.ID, winner.UserID))
		}
	}
}

func createFusionPoolConfigIfMissingInTx(tx *gorm.DB) error {
	if tx == nil {
		return fmt.Errorf("FUSION_POOL_CONFIG_INVALID")
	}
	cfg := SystemConfig{Key: "fusion_pool_points", Value: "0"}
	res := tx.Clauses(systemConfigKeyDoNothingClause()).Create(&cfg)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	return nil
}

// addPointsToFusionPoolInTx assumes the caller already holds fusionPoolMutex.
func addPointsToFusionPoolInTx(tx *gorm.DB, pointsToAdd int) (int, bool, error) {
	if tx == nil {
		return 0, false, fmt.Errorf("DB_TX_EMPTY")
	}
	if pointsToAdd <= 0 {
		return 0, false, nil
	}

	var poolCfg SystemConfig
	err := tx.Where("key = ?", "fusion_pool_points").First(&poolCfg).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if err := createFusionPoolConfigIfMissingInTx(tx); err != nil {
				return 0, false, err
			}
			if err := tx.Where("key = ?", "fusion_pool_points").First(&poolCfg).Error; err != nil {
				return 0, false, err
			}
		} else {
			return 0, false, err
		}
	}

	currentPool, err := strconv.Atoi(strings.TrimSpace(poolCfg.Value))
	if err != nil {
		return 0, false, fmt.Errorf("invalid fusion pool points value=%s: %w", formatPlainValue(poolCfg.Value), err)
	}

	currentPool += pointsToAdd
	isBurst := false
	for currentPool >= 300 {
		currentPool -= 300
		isBurst = true

		redID := "FS-" + generateRandomCode(6)
		packet := RedPacket{
			ID:          redID,
			SenderID:    0,
			SenderName:  "✨ 天道灵气",
			TotalPoints: 300,
			Count:       30,
			LeftCount:   30,
			LeftPoints:  300,
			CreatedAt:   time.Now(),
		}
		if err := createRedPacketInTx(tx, &packet); err != nil {
			return 0, false, err
		}
	}

	poolRes := tx.Model(&SystemConfig{}).
		Where("id = ? AND key = ?", poolCfg.ID, "fusion_pool_points").
		Update("value", fmt.Sprintf("%d", currentPool))
	if poolRes.Error != nil {
		return 0, false, poolRes.Error
	}
	if poolRes.RowsAffected == 0 {
		return 0, false, fmt.Errorf("FUSION_POOL_STATE_CHANGED")
	}

	return currentPool, isBurst, nil
}

func lotteryPrizeText(prizeType string, amount int) string {
	switch prizeType {
	case lotteryPrizePoints:
		return fmt.Sprintf("%d 积分", amount)
	case lotteryPrizeInvite:
		return "邀请码"
	case lotteryPrizeRenew:
		return fmt.Sprintf("%d 天续期卡", amount)
	case lotteryPrizePill:
		return "丹药"
	default:
		return prizeType
	}
}

func lotteryPrizeDisplayText(prizeType string, amount int, displayName string) string {
	if prizeType == lotteryPrizePill && strings.TrimSpace(displayName) != "" {
		return fmt.Sprintf("丹药【%s】", lotteryDisplayText(displayName, 80, "丹药"))
	}
	return lotteryPrizeText(prizeType, amount)
}

func lotteryDisplayText(text string, maxLen int, fallback string) string {
	text = strings.Map(func(r rune) rune {
		if containsDisallowedControl(string(r), false) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return fallback
	}
	return truncateRunes(text, maxLen)
}

func lotteryPointDescriptionTitle(title string) string {
	return lotteryDisplayText(title, 80, "-")
}

func claimLotteryPrizeByCode(bot *tgbotapi.BotAPI, chatID int64, userID int64, rawCode string, silent bool) bool {
	rawCode = strings.TrimSpace(rawCode)
	if rawCode == "" {
		return false
	}

	if locked, message := getLotteryClaimLockMessage(userID); locked {
		// 锁定校验对显式和静默路径一律生效：静默路径是私聊兜底入口，
		// 若不校验，被锁用户仍可用裸消息持续探测暗号，绕过 5 次错误锁定。
		if !silent {
			sendPlainText(bot, chatID, message)
		}
		return true
	}

	codeHash := hashSensitiveToken(rawCode)
	if codeHash == "" {
		if !silent {
			sendPlainText(bot, chatID, "❌ 系统安全密钥未配置，暂时无法校验领奖暗号。")
		}
		return !silent
	}

	var activities []LotteryActivity
	if err := DB.Where("claim_code_hash = ? AND status = ?", codeHash, lotteryStatusDrawn).
		Order("id desc").
		Find(&activities).Error; err != nil {
		if !silent {
			sendPlainText(bot, chatID, "❌ 查询领奖信息失败，请稍后再试。")
		}
		return !silent
	}
	if len(activities) == 0 {
		if !silent {
			sendPlainText(bot, chatID, recordLotteryClaimFailure(userID))
		}
		return false
	}

	fallbackClaimMessage := ""
	for _, activity := range activities {
		var winner LotteryWinner
		err := DB.Where("activity_id = ? AND user_id = ?", activity.ID, userID).First(&winner).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			continue
		}
		if err != nil {
			if !silent {
				sendPlainText(bot, chatID, "❌ 查询中奖记录失败，请稍后再试。")
			}
			return true
		}

		if winner.Status == lotteryWinnerClaimed {
			if fallbackClaimMessage == "" {
				fallbackClaimMessage = fmt.Sprintf("你已领取过抽奖 #%d 的奖品，不能重复领取。", activity.ID)
			}
			continue
		}
		if winner.Status != lotteryWinnerPending {
			if fallbackClaimMessage == "" {
				fallbackClaimMessage = fmt.Sprintf("抽奖 #%d 的奖品当前状态为：%s。", activity.ID, winner.Status)
			}
			continue
		}

		if lotteryWinnerShouldExpire(activity, winner, time.Now()) {
			markLotteryWinnerExpired(activity.ID, winner.ID, userID)
			if fallbackClaimMessage == "" {
				fallbackClaimMessage = fmt.Sprintf("⏳ 抽奖 #%d 的领奖期限已过，奖品已作废。", activity.ID)
			}
			continue
		}

		reply, err := claimLotteryWinner(activity, winner)
		if err != nil {
			if errors.Is(err, errLotteryClaimExpired) {
				sendPlainText(bot, chatID, fmt.Sprintf("⏳ 抽奖 #%d 的领奖期限已过，奖品已作废。", activity.ID))
				return true
			}
			log.Printf("⚠️ 抽奖领奖失败: activity=%d winner=%d user=%d err=%s", activity.ID, winner.ID, userID, formatPlainError(err))
			sendPlainText(bot, chatID, "❌ 领奖失败，请稍后再试。系统已保留你的中奖资格。")
			return true
		}
		resetLotteryClaimFailures(userID)
		if err := sendLotteryPlainText(bot, chatID, reply); err != nil {
			log.Printf("⚠️ 抽奖奖品通知发送失败: activity=%d winner=%d user=%d err=%s", activity.ID, winner.ID, userID, formatTelegramSendError(err))
			notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 抽奖 #%d 用户 %d 已领取成功，但奖品私聊发送失败，请人工核查补发。", activity.ID, userID))
		}
		return true
	}

	if fallbackClaimMessage != "" {
		sendPlainText(bot, chatID, fallbackClaimMessage)
		return true
	}

	// 走到这里说明暗号哈希命中了某个已开奖活动，但当前账号不在其中奖名单。
	// 这是明确的领奖尝试信号（普通闲聊不会 HMAC 命中真实暗号），
	// 无论显式还是静默路径都计入失败次数，确保 5 次错误锁定不被裸消息绕过。
	// 注意：暗号哈希未命中的兜底路径（len(activities)==0）属于普通私聊消息，
	// 绝不能在此计失败，否则会误锁正常聊天用户。
	failureMessage := recordLotteryClaimFailure(userID)
	if !silent {
		sendPlainText(bot, chatID, failureMessage)
	}
	return !silent
}

func claimLotteryWinner(activity LotteryActivity, winner LotteryWinner) (string, error) {
	var reply string
	now := time.Now()
	expiredInTx := false

	err := DB.Transaction(func(tx *gorm.DB) error {
		var current LotteryWinner
		if err := tx.Where("id = ? AND activity_id = ? AND user_id = ? AND status = ?", winner.ID, activity.ID, winner.UserID, lotteryWinnerPending).First(&current).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("WINNER_NOT_PENDING")
			}
			return err
		}

		if lotteryWinnerShouldExpire(activity, current, now) {
			expired, err := expireLotteryWinnerInTx(tx, activity.ID, current.ID, current.UserID, "claim deadline exceeded in claim tx")
			if err != nil {
				return err
			}
			expiredInTx = expired
			return nil
		}

		markRes := tx.Model(&LotteryWinner{}).
			Where("id = ? AND activity_id = ? AND user_id = ? AND status = ?", current.ID, activity.ID, current.UserID, lotteryWinnerPending).
			Updates(map[string]interface{}{
				"status":     lotteryWinnerClaimed,
				"claimed_at": &now,
			})
		if markRes.Error != nil {
			return markRes.Error
		}
		if markRes.RowsAffected == 0 {
			return fmt.Errorf("LOTTERY_WINNER_CLAIM_MARK_MISSED")
		}
		current.Status = lotteryWinnerClaimed
		current.ClaimedAt = &now

		switch current.PrizeType {
		case lotteryPrizePoints:
			if err := applyPointDeltaInTx(
				tx,
				current.UserID,
				current.Amount,
				"lottery_reward",
				fmt.Sprintf("积分抽奖 #%d《%s》中奖奖励", activity.ID, lotteryPointDescriptionTitle(activity.Title)),
				"lottery",
				fmt.Sprintf("%d", activity.ID),
			); err != nil {
				return err
			}
			reply = fmt.Sprintf("🎲 恭喜中奖！\n\n活动：%s\n奖品：%d 积分\n\n灵石已自动入账。", lotteryDisplayText(activity.Title, 80, "-"), current.Amount)

		case lotteryPrizeInvite:
			code, err := createLotteryInviteCodeInTx(tx)
			if err != nil {
				return err
			}
			current.PrizeCodePreview = maskSecret(code)
			reply = fmt.Sprintf("🎲 恭喜中奖！\n\n活动：%s\n奖品：邀请码\n\n你的专属邀请码：%s", lotteryDisplayText(activity.Title, 80, "-"), code)

		case lotteryPrizeRenew:
			code, err := createLotteryRenewCodeInTx(tx, current.Amount)
			if err != nil {
				return err
			}
			current.PrizeCodePreview = maskSecret(code)
			reply = fmt.Sprintf("🎲 恭喜中奖！\n\n活动：%s\n奖品：%d 天续期卡\n\n你的专属续期卡：%s", lotteryDisplayText(activity.Title, 80, "-"), current.Amount, code)

		case lotteryPrizePill:
			itemName, err := getLotteryPrizeDisplayNameInTx(tx, activity.ID, current)
			if err != nil {
				return err
			}
			if !isLotteryPillPrizeName(itemName) {
				return fmt.Errorf("INVALID_PILL_PRIZE")
			}
			if err := grantLotteryInventoryItemInTx(tx, current.UserID, itemName, 1); err != nil {
				return err
			}
			reply = fmt.Sprintf("🎲 恭喜中奖！\n\n活动：%s\n奖品：丹药【%s】 x1\n\n丹药已放入乾坤袋。", lotteryDisplayText(activity.Title, 80, "-"), lotteryDisplayText(itemName, 80, "丹药"))

		default:
			return fmt.Errorf("UNKNOWN_PRIZE_TYPE")
		}

		if current.PrizeCodePreview != "" {
			previewRes := tx.Model(&LotteryWinner{}).
				Where("id = ? AND activity_id = ? AND user_id = ? AND status = ?", current.ID, activity.ID, current.UserID, lotteryWinnerClaimed).
				Update("prize_code_preview", current.PrizeCodePreview)
			if previewRes.Error != nil {
				return previewRes.Error
			}
			if previewRes.RowsAffected == 0 {
				return fmt.Errorf("LOTTERY_WINNER_PREVIEW_UPDATE_MISSED")
			}
		}

		return createLotteryClaimLogInTx(tx, &LotteryClaimLog{
			ActivityID: activity.ID,
			WinnerID:   current.ID,
			UserID:     current.UserID,
			Action:     "claimed",
			Detail:     fmt.Sprintf("prize_type=%s amount=%d", current.PrizeType, current.Amount),
		})
	})

	if err != nil {
		return "", err
	}
	if expiredInTx {
		return "", errLotteryClaimExpired
	}
	return reply, nil
}

func getLotteryPrizeDisplayNameInTx(tx *gorm.DB, activityID uint, winner LotteryWinner) (string, error) {
	if tx == nil {
		return "", fmt.Errorf("DB_TX_EMPTY")
	}
	if activityID == 0 || winner.PrizeID == 0 {
		return "", fmt.Errorf("PRIZE_ID_EMPTY")
	}

	var prize LotteryPrize
	if err := tx.Select("id", "activity_id", "prize_type", "display_name").
		Where("id = ? AND activity_id = ? AND deleted_at IS NULL", winner.PrizeID, activityID).
		First(&prize).Error; err != nil {
		return "", err
	}
	if !lotteryPrizeMatchesWinner(prize, activityID, winner) {
		return "", fmt.Errorf("LOTTERY_PRIZE_MISMATCH")
	}

	itemName := strings.TrimSpace(prize.DisplayName)
	if itemName == "" {
		return "", fmt.Errorf("PRIZE_DISPLAY_EMPTY")
	}
	return itemName, nil
}

func lotteryPrizeMatchesWinner(prize LotteryPrize, activityID uint, winner LotteryWinner) bool {
	return activityID != 0 &&
		prize.ID == winner.PrizeID &&
		prize.ActivityID == activityID &&
		strings.TrimSpace(prize.PrizeType) == strings.TrimSpace(winner.PrizeType)
}

func grantLotteryInventoryItemInTx(tx *gorm.DB, userID int64, itemName string, quantity int) error {
	if tx == nil {
		return fmt.Errorf("DB_TX_EMPTY")
	}
	itemName = strings.TrimSpace(itemName)
	if userID == 0 || itemName == "" || quantity <= 0 {
		return fmt.Errorf("INVALID_INVENTORY_GRANT")
	}

	res := tx.Clauses(inventoryQuantityUpsertClause(quantity)).Create(&Inventory{
		UserID:   userID,
		ItemName: itemName,
		Quantity: quantity,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("LOTTERY_INVENTORY_GRANT_MISSED")
	}
	return nil
}

func sendLotteryPlainText(bot *tgbotapi.BotAPI, chatID int64, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	_, err := sendAutoDelete(bot, msg)
	return err
}

func sendLotteryGroupPlainText(bot *tgbotapi.BotAPI, chatID int64, text string) error {
	msg := tgbotapi.NewMessage(chatID, text)
	_, err := sendAutoDelete(bot, msg)
	return err
}

func sendLotteryReplyPlainText(bot *tgbotapi.BotAPI, sourceMsg *tgbotapi.Message, text string) {
	if bot == nil || sourceMsg == nil || sourceMsg.Chat == nil {
		return
	}

	msg := tgbotapi.NewMessage(sourceMsg.Chat.ID, text)
	msg.ReplyToMessageID = sourceMsg.MessageID
	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("⚠️ 抽奖回复消息发送失败: chat=%d message=%d err=%s", sourceMsg.Chat.ID, sourceMsg.MessageID, formatTelegramSendError(err))
		sendPlainText(bot, sourceMsg.Chat.ID, text)
	}
}

func sendLotteryGroupPersistentText(bot *tgbotapi.BotAPI, chatID int64, text string) (tgbotapi.Message, error) {
	msg := tgbotapi.NewMessage(chatID, text)
	return sendNoAutoDelete(bot, msg)
}

func pinLotteryMessage(bot *tgbotapi.BotAPI, chatID int64, messageID int, activityID uint, label string) bool {
	if bot == nil || chatID == 0 || messageID == 0 {
		return false
	}

	cfg := tgbotapi.PinChatMessageConfig{
		ChatID:              chatID,
		MessageID:           messageID,
		DisableNotification: true,
	}
	if _, err := bot.Request(cfg); err != nil {
		safeLabel := formatPlainValue(label)
		log.Printf("⚠️ 抽奖消息置顶失败: activity=%d label=%s chat=%d message=%d err=%s", activityID, safeLabel, chatID, messageID, formatTelegramSendError(err))
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 抽奖 #%d 的%s消息发送成功，但置顶失败。请检查 Bot 是否有群置顶权限。\n\nchat=%d message=%d err=%s", activityID, safeLabel, chatID, messageID, formatPlainError(err)))
		return false
	}
	return true
}

func unpinLotteryMessage(bot *tgbotapi.BotAPI, chatID int64, messageID int, activityID uint, label string) bool {
	if bot == nil || chatID == 0 || messageID == 0 {
		return true
	}

	cfg := tgbotapi.UnpinChatMessageConfig{
		ChatID:    chatID,
		MessageID: messageID,
	}
	if _, err := bot.Request(cfg); err != nil {
		safeLabel := formatPlainValue(label)
		log.Printf("⚠️ 抽奖消息取消置顶失败: activity=%d label=%s chat=%d message=%d err=%s", activityID, safeLabel, chatID, messageID, formatTelegramSendError(err))
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 抽奖 #%d 的%s消息取消置顶失败，请人工检查。\n\nchat=%d message=%d err=%s", activityID, safeLabel, chatID, messageID, formatPlainError(err)))
		return false
	}
	return true
}

func encryptLotteryClaimCode(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("EMPTY_CLAIM_CODE")
	}

	pepper := getSensitivePepper()
	if pepper == "" {
		return "", errSecurityPepperNotConfigured
	}

	key := sha256.Sum256([]byte("lottery-claim-code:" + pepper))
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

func decryptLotteryClaimCode(encrypted string) (string, error) {
	encrypted = strings.TrimSpace(encrypted)
	if !strings.HasPrefix(encrypted, "gcm$") {
		return "", fmt.Errorf("INVALID_CLAIM_CODE_CIPHER")
	}

	pepper := getSensitivePepper()
	if pepper == "" {
		return "", errSecurityPepperNotConfigured
	}

	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encrypted, "gcm$"))
	if err != nil {
		return "", err
	}

	key := sha256.Sum256([]byte("lottery-claim-code:" + pepper))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) <= gcm.NonceSize() {
		return "", fmt.Errorf("INVALID_CLAIM_CODE_PAYLOAD")
	}

	nonce := payload[:gcm.NonceSize()]
	ciphertext := payload[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}

func lotteryWinnerShouldExpire(activity LotteryActivity, winner LotteryWinner, now time.Time) bool {
	return winner.Status == lotteryWinnerPending &&
		activity.ClaimDeadlineAt != nil &&
		now.After(*activity.ClaimDeadlineAt)
}

func createLotteryInviteCodeInTx(tx *gorm.DB) (string, error) {
	for i := 0; i < 5; i++ {
		code := "YQM-" + generateRandomCode(12)
		if err := createInviteCodeRecord(tx, code); err == nil {
			return code, nil
		} else if !isUniqueConstraintError(err) {
			return "", err
		}
	}
	return "", errCreateInviteCodeFailed
}

func createLotteryRenewCodeInTx(tx *gorm.DB, days int) (string, error) {
	for i := 0; i < 5; i++ {
		code := fmt.Sprintf("R%d-%s", days, generateRandomCode(16))
		if err := createRenewCodeRecord(tx, code, days); err == nil {
			return code, nil
		} else if !isUniqueConstraintError(err) {
			return "", err
		}
	}
	return "", errCreateRenewCodeFailed
}

func markLotteryWinnerExpired(activityID uint, winnerID uint, userID int64) {
	err := DB.Transaction(func(tx *gorm.DB) error {
		_, err := expireLotteryWinnerInTx(tx, activityID, winnerID, userID, "claim deadline exceeded")
		return err
	})
	if err != nil {
		log.Printf("⚠️ 标记抽奖奖品过期失败: activity=%d winner=%d user=%d err=%s", activityID, winnerID, userID, formatPlainError(err))
	}
}

func expireLotteryWinnerInTx(tx *gorm.DB, activityID uint, winnerID uint, userID int64, detail string) (bool, error) {
	if tx == nil || activityID == 0 || winnerID == 0 || userID == 0 {
		return false, fmt.Errorf("LOTTERY_EXPIRE_INVALID")
	}
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "claim deadline exceeded"
	}

	res := tx.Model(&LotteryWinner{}).
		Where("id = ? AND activity_id = ? AND user_id = ? AND status = ?", winnerID, activityID, userID, lotteryWinnerPending).
		Update("status", lotteryWinnerExpired)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	return true, createLotteryClaimLogInTx(tx, &LotteryClaimLog{
		ActivityID: activityID,
		WinnerID:   winnerID,
		UserID:     userID,
		Action:     "expired",
		Detail:     detail,
	})
}

func getLotteryClaimLockMessage(userID int64) (bool, string) {
	if DB == nil || userID == 0 {
		return false, ""
	}

	var attempt SecurityAttemptLock
	if err := DB.Where("user_id = ? AND purpose = ?", userID, lotteryClaimAttemptPurpose).First(&attempt).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("lottery claim lock read failed: user=%d err=%s", userID, formatPlainError(err))
			return true, "抽奖领奖安全状态读取失败，请稍后再试。"
		}
		return false, ""
	}
	now := time.Now()
	if attempt.LockedUntil == nil || !attempt.LockedUntil.After(now) {
		return false, ""
	}

	remaining := securityAttemptRemainingMinutes(*attempt.LockedUntil, now)
	return true, fmt.Sprintf("⏳ 抽奖暗号错误次数过多，Bot 已暂停受理你的消息，请 %d 分钟后再试。", remaining)
}

func recordLotteryClaimFailure(userID int64) string {
	now := time.Now()
	message := "❌ 暗号无效，或活动尚未开奖。"

	if DB == nil || userID == 0 {
		return message
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		var err error
		message, err = recordSecurityAttemptFailureInTx(
			tx,
			userID,
			lotteryClaimAttemptPurpose,
			lotteryClaimMaxFailures,
			lotteryClaimLockDuration,
			now,
			"❌ 暗号无效，或活动尚未开奖。剩余尝试次数：%d",
			"⏳ 抽奖暗号错误已达 5 次，Bot 已暂停受理你的消息 30 分钟。",
		)
		return err
	}); err != nil {
		log.Printf("⚠️ 抽奖暗号错误次数记录失败: user=%d err=%s", userID, formatPlainError(err))
		return "❌ 暗号无效，或活动尚未开奖。"
	}

	return message
}

func resetLotteryClaimFailures(userID int64) {
	if DB == nil || userID == 0 {
		return
	}
	if err := DB.Model(&SecurityAttemptLock{}).
		Where("user_id = ? AND purpose = ?", userID, lotteryClaimAttemptPurpose).
		Updates(map[string]interface{}{
			"fail_count":   0,
			"locked_until": nil,
			"last_fail_at": nil,
		}).Error; err != nil {
		log.Printf("⚠️ 重置抽奖暗号错误次数失败: user=%d err=%s", userID, formatPlainError(err))
	}
}

func handleLotteryDetailCommand(bot *tgbotapi.BotAPI, chatID int64, text string) {
	id, ok := parseTrailingUint(text)
	if !ok {
		sendPlainText(bot, chatID, "请按格式发送：抽奖详情 活动ID")
		return
	}

	var activity LotteryActivity
	if err := DB.First(&activity, uint(id)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendPlainText(bot, chatID, "抽奖活动不存在。")
		} else {
			log.Printf("lottery detail activity read failed: activity=%d err=%s", id, formatPlainError(err))
			sendPlainText(bot, chatID, "抽奖详情读取失败，请稍后再试。")
		}
		return
	}

	var prizes []LotteryPrize
	if err := DB.Where("activity_id = ? AND deleted_at IS NULL", activity.ID).Find(&prizes).Error; err != nil {
		log.Printf("⚠️ 抽奖详情读取奖品失败: activity=%d err=%s", activity.ID, formatPlainError(err))
		sendPlainText(bot, chatID, "抽奖详情暂时读取失败：奖品记录不可用，请稍后重试。")
		return
	}

	var winners []LotteryWinner
	if err := DB.Where("activity_id = ? AND deleted_at IS NULL", activity.ID).Order("id asc").Find(&winners).Error; err != nil {
		log.Printf("⚠️ 抽奖详情读取中奖记录失败: activity=%d err=%s", activity.ID, formatPlainError(err))
		sendPlainText(bot, chatID, "抽奖详情暂时读取失败：中奖记录不可用，请稍后重试。")
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("抽奖 #%d：%s\n", activity.ID, lotteryDisplayText(activity.Title, 80, "-")))
	b.WriteString("状态：" + lotteryStatusText(activity.Status) + "\n")
	if participantCount, err := countLotteryParticipants(activity.ID); err != nil {
		b.WriteString("参与人数：读取失败\n")
	} else {
		b.WriteString(fmt.Sprintf("参与人数：%d\n", participantCount))
	}
	if activity.Mode == lotteryModeCount {
		b.WriteString(fmt.Sprintf("开奖规则：满 %d 人\n", activity.ParticipantLimit))
	} else if activity.DrawAt != nil {
		b.WriteString("开奖时间：" + activity.DrawAt.Format("2006-01-02 15:04") + "\n")
	}
	if activity.ClaimDeadlineAt != nil {
		b.WriteString("领奖截止：" + activity.ClaimDeadlineAt.Format("2006-01-02 15:04") + "\n")
	}
	b.WriteString("\n奖品：\n")
	for _, p := range prizes {
		b.WriteString(fmt.Sprintf("- %s x %d\n", lotteryDisplayText(p.DisplayName, 80, "-"), p.Quantity))
	}
	if len(winners) > 0 {
		b.WriteString("\n中奖记录：\n")
		for _, w := range winners {
			b.WriteString(fmt.Sprintf("- %s(%d) %s %d 状态:%s\n", lotteryDisplayText(w.UserName, 80, "-"), w.UserID, lotteryPrizeText(w.PrizeType, w.Amount), w.Amount, lotteryStatusText(w.Status)))
		}
	}
	sendPlainText(bot, chatID, b.String())
}

func handleForceDrawLotteryCommand(bot *tgbotapi.BotAPI, chatID int64, actorID int64, text string) {
	id, ok := parseTrailingUint(text)
	if !ok {
		sendPlainText(bot, chatID, "请按格式发送：强制开奖 活动ID")
		return
	}
	if err := drawLotteryActivity(bot, uint(id), "manual", actorID); err != nil {
		sendPlainText(bot, chatID, "❌ 强制开奖失败："+formatPlainError(err))
		return
	}
	sendPlainText(bot, chatID, fmt.Sprintf("✅ 抽奖 #%d 已强制开奖。", id))
}

func handleCancelLotteryCommand(bot *tgbotapi.BotAPI, chatID int64, actorID int64, text string) {
	id, ok := parseTrailingUint(text)
	if !ok {
		sendPlainText(bot, chatID, "请按格式发送：取消抽奖 活动ID")
		return
	}

	refundTotal, err := cancelLotteryActivityWithFullRefund(uint(id), actorID)
	if err != nil {
		if errors.Is(err, errLotteryNotActive) {
			sendPlainText(bot, chatID, "该抽奖不存在或已不能取消。")
			return
		}
		log.Printf("⚠️ 取消抽奖失败: activity=%d actor=%d err=%s", id, actorID, formatPlainError(err))
		sendPlainText(bot, chatID, "❌ 取消抽奖失败，请稍后再试。")
		return
	}
	if refundTotal < 0 {
		sendPlainText(bot, chatID, "该抽奖不存在或已不能取消。")
		return
	}
	sendPlainText(bot, chatID, fmt.Sprintf("✅ 抽奖 #%d 已取消，已全额退还参与积分 %d。", id, refundTotal))
}

func cancelLotteryActivityWithFullRefund(activityID uint, actorID int64) (int, error) {
	txRefundTotal := 0
	now := time.Now()

	err := DB.Transaction(func(tx *gorm.DB) error {
		txRefundTotal = 0
		res := tx.Model(&LotteryActivity{}).
			Where("id = ? AND status = ?", activityID, lotteryStatusActive).
			Updates(map[string]interface{}{
				"status":            lotteryStatusClosed,
				"claim_deadline_at": &now,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errLotteryNotActive
		}

		var activity LotteryActivity
		if err := tx.First(&activity, activityID).Error; err != nil {
			return err
		}

		var participants []LotteryParticipant
		if err := tx.Where("activity_id = ? AND deleted_at IS NULL", activityID).Find(&participants).Error; err != nil {
			return err
		}

		for _, participant := range participants {
			if participant.EntryCost <= 0 || participant.IsRefunded {
				continue
			}

			refundRes := tx.Model(&LotteryParticipant{}).
				Where("id = ? AND activity_id = ? AND is_refunded = ?", participant.ID, activity.ID, false).
				Updates(map[string]interface{}{
					"refund_points": participant.EntryCost,
					"is_refunded":   true,
					"refunded_at":   &now,
				})
			if refundRes.Error != nil {
				return refundRes.Error
			}
			if refundRes.RowsAffected == 0 {
				return fmt.Errorf("LOTTERY_CANCEL_REFUND_MARK_MISSED")
			}

			if err := applyPointDeltaInTx(
				tx,
				participant.UserID,
				participant.EntryCost,
				"lottery_cancel_refund",
				fmt.Sprintf("积分抽奖 #%d《%s》取消，全额退还 %d 积分", activity.ID, lotteryPointDescriptionTitle(activity.Title), participant.EntryCost),
				"lottery",
				fmt.Sprintf("%d", activity.ID),
			); err != nil {
				return err
			}

			txRefundTotal += participant.EntryCost
		}

		totalRes := tx.Model(&LotteryActivity{}).
			Where("id = ?", activityID).
			Updates(map[string]interface{}{
				"total_refund_points": txRefundTotal,
			})
		if totalRes.Error != nil {
			return totalRes.Error
		}
		if totalRes.RowsAffected == 0 {
			return fmt.Errorf("LOTTERY_CANCEL_TOTAL_REFUND_UPDATE_MISSED")
		}
		if err := writeAuditLogInTx(tx, actorID, "CANCEL_LOTTERY", fmt.Sprintf("%d", activityID), 0, fmt.Sprintf("超级管理员取消积分抽奖活动，退还参与积分 %d", txRefundTotal)); err != nil {
			return err
		}
		return nil
	})

	if err != nil {
		return -1, err
	}
	return txRefundTotal, nil
}

func parseTrailingUint(text string) (uint64, bool) {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return 0, false
	}
	id, err := strconv.ParseUint(fields[len(fields)-1], 10, 64)
	return id, err == nil && id > 0
}

func countLotteryParticipants(activityID uint) (int64, error) {
	var count int64
	if err := DB.Model(&LotteryParticipant{}).
		Where("activity_id = ? AND deleted_at IS NULL", activityID).
		Count(&count).Error; err != nil {
		return 0, err
	}
	return count, nil
}

func lotteryStatusText(status string) string {
	switch status {
	case lotteryStatusActive:
		return "进行中"
	case lotteryStatusDrawing:
		return "开奖中"
	case lotteryStatusDrawn:
		return "已开奖"
	case lotteryStatusClosed:
		return "已关闭"
	default:
		return "未知状态"
	}
}

func shuffleLotteryParticipants(items []LotteryParticipant) {
	for i := len(items) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			continue
		}
		j := int(n.Int64())
		items[i], items[j] = items[j], items[i]
	}
}

func shuffleLotteryPrizes(items []LotteryPrize) {
	for i := len(items) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			continue
		}
		j := int(n.Int64())
		items[i], items[j] = items[j], items[i]
	}
}

func StartLotteryScheduler(bot *tgbotapi.BotAPI) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			runLotterySchedulerTick(bot)
		}
	}()
	log.Println("✅ 积分抽奖定时开奖调度器已启动。")
}

func runLotterySchedulerTick(bot *tgbotapi.BotAPI) {
	now := time.Now()
	var dueActivities []LotteryActivity
	if err := DB.Where("status = ? AND mode = ? AND draw_at IS NOT NULL AND draw_at <= ?", lotteryStatusActive, lotteryModeTime, now).
		Order("draw_at asc").
		Limit(20).
		Find(&dueActivities).Error; err != nil {
		log.Printf("⚠️ 查询到期抽奖失败: %s", formatPlainError(err))
		return
	}

	for _, activity := range dueActivities {
		if err := drawLotteryActivity(bot, activity.ID, "scheduled", 0); err != nil && !errors.Is(err, errLotteryNotActive) {
			log.Printf("⚠️ 定时抽奖开奖失败: activity=%d err=%s", activity.ID, formatPlainError(err))
			notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 定时抽奖 #%d 开奖失败：%s", activity.ID, formatPlainError(err)))
		}
	}

	unpinExpiredLotteryMessages(bot, now)
}

func unpinExpiredLotteryMessages(bot *tgbotapi.BotAPI, now time.Time) {
	var activities []LotteryActivity
	if err := DB.Where("status IN ? AND claim_deadline_at IS NOT NULL AND claim_deadline_at <= ? AND pins_unpinned = ?", []string{lotteryStatusDrawn, lotteryStatusClosed}, now, false).
		Order("claim_deadline_at asc").
		Limit(20).
		Find(&activities).Error; err != nil {
		log.Printf("⚠️ 查询待取消置顶抽奖失败: %s", formatPlainError(err))
		return
	}

	for _, activity := range activities {
		chatID := activity.AnnounceChatID
		if chatID == 0 {
			chatID = AppConfig.NoticeGroupID
		}
		if chatID != 0 {
			if activity.IntroMessageID > 0 && activity.IntroPinned {
				unpinLotteryMessage(bot, chatID, activity.IntroMessageID, activity.ID, "intro")
			}
			if activity.ResultMessageID > 0 && activity.ResultPinned {
				unpinLotteryMessage(bot, chatID, activity.ResultMessageID, activity.ID, "result")
			}
		}

		res := DB.Model(&LotteryActivity{}).
			Where("id = ? AND status IN ? AND claim_deadline_at IS NOT NULL AND claim_deadline_at <= ? AND pins_unpinned = ?", activity.ID, []string{lotteryStatusDrawn, lotteryStatusClosed}, now, false).
			Update("pins_unpinned", true)
		if res.Error != nil {
			log.Printf("⚠️ 标记抽奖置顶清理完成失败: activity=%d err=%s", activity.ID, formatPlainError(res.Error))
			continue
		}
		if res.RowsAffected == 0 {
			log.Printf("⚠️ 抽奖置顶清理完成标记未命中: activity=%d", activity.ID)
		}
	}
}
