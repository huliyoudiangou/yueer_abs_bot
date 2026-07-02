package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const (
	paiGowMinBet           = 1
	paiGowMaxBet           = 5
	paiGowBetDuration      = 60 * time.Second
	paiGowCooldownDuration = 1 * time.Minute
	paiGowMaxPlayers       = 20
)

type PaiGowPlayerBet struct {
	UserName string
	Points   int
	BetAt    time.Time
}

type PaiGowState struct {
	GameID     string
	IsActive   bool
	IsDealing  bool
	Bets       map[int64]*PaiGowPlayerBet
	TotalPool  int
	Mu         sync.Mutex
	LastGameAt time.Time
}

type PaiGowCard struct {
	Suit  string
	Rank  string
	Point int
}

type PaiGowDealtPlayer struct {
	UserID      int64
	UserName    string
	Points      int
	Hand        []PaiGowCard
	PlayerPoint int
	Won         bool
	Result      string
	Payout      int
}

var GroupPaiGows sync.Map

func getPaiGowState(chatID int64) *PaiGowState {
	val, _ := GroupPaiGows.LoadOrStore(chatID, &PaiGowState{
		Bets: make(map[int64]*PaiGowPlayerBet),
	})
	return val.(*PaiGowState)
}

func isPaiGowBetCommand(text string) bool {
	parts := strings.Fields(strings.TrimSpace(text))
	if len(parts) != 2 || parts[0] != "押" {
		return false
	}
	_, err := strconv.Atoi(parts[1])
	return err == nil
}

func isPaiGowOpenTime(now time.Time) bool {
	loc := time.FixedZone("CST", 8*3600)
	local := now.In(loc)
	minutes := local.Hour()*60 + local.Minute()
	return minutes >= 18*60 && minutes < 19*60+55
}

func createPaiGowBetInTx(tx *gorm.DB, bet *PaiGowBet) error {
	res := tx.Create(bet)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("牌九下注记录创建未命中")
	}
	return nil
}

func updatePaiGowBetStatusCAS(tx *gorm.DB, gameID string, userID int64, fromStatus string, values map[string]interface{}) (bool, error) {
	res := tx.Model(&PaiGowBet{}).
		Where("game_id = ? AND user_id = ? AND status = ?", gameID, userID, fromStatus).
		Updates(values)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func loadActivePaiGowBetsSnapshot(gameID string) (map[int64]*PaiGowPlayerBet, int, error) {
	if gameID == "" {
		return map[int64]*PaiGowPlayerBet{}, 0, nil
	}
	var bets []PaiGowBet
	if err := DB.Where("game_id = ? AND status = ?", gameID, RaceBetStatusActive).Find(&bets).Error; err != nil {
		return nil, 0, err
	}
	snapshot := make(map[int64]*PaiGowPlayerBet, len(bets))
	totalPool := 0
	for _, bet := range bets {
		snapshot[bet.UserID] = &PaiGowPlayerBet{
			UserName: bet.UserName,
			Points:   bet.Points,
			BetAt:    bet.CreatedAt,
		}
		totalPool += bet.Points
	}
	return snapshot, totalPool, nil
}

func refundPaiGowBetsByGameID(gameID string, reason string) (int, int, error) {
	if gameID == "" {
		return 0, 0, nil
	}

	refundCount := 0
	refundPoints := 0

	err := DB.Transaction(func(tx *gorm.DB) error {
		txRefundCount := 0
		txRefundPoints := 0
		var bets []PaiGowBet
		if err := tx.Where("game_id = ? AND status = ?", gameID, RaceBetStatusActive).Find(&bets).Error; err != nil {
			return err
		}

		for _, bet := range bets {
			res := tx.Model(&PaiGowBet{}).
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
				"pai_gow_refund",
				fmt.Sprintf("牌九异常退款，返还 %d 积分", bet.Points),
				"pai_gow",
				gameID,
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
		log.Printf("⚠️ 牌九退款失败: game_id=%s reason=%s err=%s", formatPlainValue(gameID), formatPlainValue(reason), formatPlainError(err))
		return 0, 0, err
	}

	if refundCount > 0 {
		log.Printf("↩️ 牌九异常退款完成: game_id=%s count=%d points=%d reason=%s", formatPlainValue(gameID), refundCount, refundPoints, formatPlainValue(reason))
	}
	return refundCount, refundPoints, nil
}

func recoverActivePaiGowBetsOnStartup() {
	var gameIDs []string
	if err := DB.Model(&PaiGowBet{}).
		Where("status = ?", RaceBetStatusActive).
		Distinct("game_id").
		Pluck("game_id", &gameIDs).Error; err != nil {
		log.Printf("⚠️ 启动时扫描未结算牌九下注失败: %s", formatPlainError(err))
		return
	}

	if len(gameIDs) == 0 {
		log.Println("✅ 启动检查：没有发现未结算牌九下注")
		return
	}

	log.Printf("⚠️ 启动检查：发现 %d 局未结算牌九，开始自动退款", len(gameIDs))
	totalCount := 0
	totalPoints := 0
	for _, gameID := range gameIDs {
		count, points, err := refundPaiGowBetsByGameID(gameID, "startup recovery")
		if err != nil {
			continue
		}
		totalCount += count
		totalPoints += points
	}
	log.Printf("✅ 启动牌九兜底退款完成：退款人数=%d，总积分=%d", totalCount, totalPoints)
}

func handlePaiGowGame(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.From == nil || msg.Chat == nil {
		return
	}
	if AppConfig.NoticeGroupID != 0 && !isUserInGroupFresh(bot, msg.From.ID, AppConfig.NoticeGroupID) {
		return
	}

	state := getPaiGowState(msg.Chat.ID)
	userID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)
	userName := msg.From.UserName
	if userName == "" {
		userName = msg.From.FirstName
	}
	safeName := escapeMarkdown(userName)

	switch {
	case text == "发起牌九":
		handlePaiGowStart(bot, chatID, state)
	case text == "牌九状态":
		handlePaiGowStatus(bot, chatID, state)
	case text == "取消牌九":
		handlePaiGowCancel(bot, chatID, userID, state)
	case isPaiGowBetCommand(text):
		handlePaiGowBet(bot, msg, chatID, userID, safeName, text, state)
	}
}

func handlePaiGowStart(bot *tgbotapi.BotAPI, chatID int64, state *PaiGowState) {
	if !isPaiGowOpenTime(time.Now()) {
		sendGroupAutoDeleteMessage(bot, chatID, "⏳ **推牌九尚未开放！**\n\n开放时间为每日 **18:00 - 19:55**，赛马黄金档前预留 5 分钟缓冲。")
		return
	}

	state.Mu.Lock()
	if state.IsActive {
		state.Mu.Unlock()
		sendGroupAutoDeleteMessage(bot, chatID, "⚠️ 推牌九正在进行中，本局还未结束，请勿重复发起！")
		return
	}
	if time.Since(state.LastGameAt) < paiGowCooldownDuration {
		cdLeft := int(paiGowCooldownDuration.Seconds() - time.Since(state.LastGameAt).Seconds())
		state.Mu.Unlock()
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("🃏 牌桌正在收拾，请等待 **%d 秒** 后再发起下一局！", cdLeft))
		return
	}

	state.GameID = fmt.Sprintf("PAIGOW-%d-%s", chatID, generateRandomCode(8))
	state.IsActive = true
	state.IsDealing = false
	state.Bets = make(map[int64]*PaiGowPlayerBet)
	state.TotalPool = 0
	state.Mu.Unlock()

	notice := "🃏 **推牌九开局**\n\n" +
		"庄家：天机阁\n" +
		"⏱ **下注时间**：60 秒\n" +
		"💰 **下注范围**：`1` - `5` 积分\n" +
		"🕕 **开放时段**：18:00 - 19:55\n\n" +
		"👇 **下注格式**：`押 3`\n\n" +
		"规则：A=1，2-9按牌面，10/J/Q/K=0；每人两张取个位，9点最大，同点庄家大半点。"
	sendGroupAutoDeleteMessage(bot, chatID, notice)

	go runPaiGowRoutine(bot, chatID)
}

func handlePaiGowStatus(bot *tgbotapi.BotAPI, chatID int64, state *PaiGowState) {
	state.Mu.Lock()
	defer state.Mu.Unlock()
	if !state.IsActive {
		sendGroupAutoDeleteMessage(bot, chatID, "🃏 当前没有进行中的推牌九。开放时间：每日 **18:00 - 19:55**。")
		return
	}
	status := "下注中"
	if state.IsDealing {
		status = "开奖中"
	}
	sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("🃏 **推牌九状态**\n\n当前状态：%s\n参与人数：`%d/%d`\n总下注：`%d` 积分", status, len(state.Bets), paiGowMaxPlayers, state.TotalPool))
}

func handlePaiGowCancel(bot *tgbotapi.BotAPI, chatID int64, userID int64, state *PaiGowState) {
	if !isBookRequestAdmin(userID) {
		sendGroupAutoDeleteMessage(bot, chatID, "❌ 只有管理员可以取消推牌九。")
		return
	}

	state.Mu.Lock()
	if !state.IsActive {
		state.Mu.Unlock()
		sendGroupAutoDeleteMessage(bot, chatID, "🃏 当前没有进行中的推牌九。")
		return
	}
	if state.IsDealing {
		state.Mu.Unlock()
		sendGroupAutoDeleteMessage(bot, chatID, "⚠️ 推牌九已经开始发牌，无法取消。")
		return
	}
	gameID := state.GameID
	state.IsActive = false
	state.IsDealing = true
	state.Mu.Unlock()

	count, points, err := refundPaiGowBetsByGameID(gameID, "admin cancel")
	if err != nil {
		state.Mu.Lock()
		if state.GameID == gameID {
			state.IsActive = true
			state.IsDealing = false
		}
		state.Mu.Unlock()
		sendGroupAutoDeleteMessage(bot, chatID, "❌ 取消牌九失败，请稍后重试或联系超级管理员查看日志。")
		return
	}

	state.Mu.Lock()
	state.IsActive = false
	state.IsDealing = false
	state.Bets = make(map[int64]*PaiGowPlayerBet)
	state.TotalPool = 0
	state.LastGameAt = time.Now()
	state.Mu.Unlock()

	sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("↩️ **本局推牌九已取消**\n\n已退还 `%d` 名玩家共 `%d` 积分。", count, points))
}

func handlePaiGowBet(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, chatID int64, userID int64, safeName string, text string, state *PaiGowState) {
	state.Mu.Lock()
	if !state.IsActive {
		state.Mu.Unlock()
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 当前没有开放中的推牌九，请在 18:00-19:55 发送 `发起牌九` 开启新一局！", safeName))
		return
	}
	if state.IsDealing {
		state.Mu.Unlock()
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 已经开始发牌了，买定离手，下局请早！", safeName))
		return
	}
	if _, exists := state.Bets[userID]; exists {
		state.Mu.Unlock()
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经下过注了，一局只能押一次！", safeName))
		return
	}
	if len(state.Bets) >= paiGowMaxPlayers {
		state.Mu.Unlock()
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 本局推牌九已满员，最多 `%d` 人参与。", safeName, paiGowMaxPlayers))
		return
	}
	gameID := state.GameID
	state.Mu.Unlock()

	parts := strings.Fields(text)
	points, err := strconv.Atoi(parts[1])
	if err != nil {
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 金额必须是纯数字，格式如：`押 3`", safeName))
		return
	}
	if points < paiGowMinBet || points > paiGowMaxBet {
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 推牌九下注范围为 **%d-%d** 积分。", safeName, paiGowMinBet, paiGowMaxBet))
		return
	}

	err = DB.Transaction(func(tx *gorm.DB) error {
		if _, _, err := ensureUserWalletInTx(tx, msg.From); err != nil {
			return err
		}
		if err := createPaiGowBetInTx(tx, &PaiGowBet{
			GameID:   gameID,
			ChatID:   chatID,
			UserID:   userID,
			UserName: safeName,
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
			"pai_gow_bet",
			fmt.Sprintf("牌九下注，消耗 %d 积分", points),
			"pai_gow",
			gameID,
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
			log.Printf("⚠️ 牌九下注失败: game_id=%s user=%d err=%s", formatPlainValue(gameID), userID, formatPlainError(err))
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 下注失败，系统繁忙，请稍后重试！", safeName))
		}
		return
	}

	refundPaiGowBet := func() {
		err := DB.Transaction(func(tx *gorm.DB) error {
			claimed, err := updatePaiGowBetStatusCAS(tx, gameID, userID, RaceBetStatusActive, map[string]interface{}{
				"status": RaceBetStatusRefunded,
			})
			if err != nil {
				return err
			}
			if !claimed {
				return nil
			}
			return applyPointDeltaInTx(
				tx,
				userID,
				points,
				"pai_gow_refund",
				fmt.Sprintf("牌九异常退款，返还 %d 积分", points),
				"pai_gow",
				gameID,
			)
		})
		if err != nil {
			log.Printf("⚠️ 牌九单人退款失败: game_id=%s user_id=%d points=%d err=%s", formatPlainValue(gameID), userID, points, formatPlainError(err))
		}
	}

	state.Mu.Lock()
	if !state.IsActive || state.IsDealing || state.GameID != gameID {
		state.Mu.Unlock()
		refundPaiGowBet()
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 买定离手，牌九已开局，您的资金已原路退回！", safeName))
		return
	}
	if _, exists := state.Bets[userID]; exists {
		state.Mu.Unlock()
		refundPaiGowBet()
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经下过注了，本次重复请求已退款。", safeName))
		return
	}
	if len(state.Bets) >= paiGowMaxPlayers {
		state.Mu.Unlock()
		refundPaiGowBet()
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 本局推牌九已满员，本次下注已退款。", safeName))
		return
	}
	state.Bets[userID] = &PaiGowPlayerBet{UserName: safeName, Points: points}
	state.TotalPool += points
	state.Mu.Unlock()

	sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✅ @%s 成功下注 **%d** 积分，等待天机阁开牌！", safeName, points))
}

func runPaiGowRoutine(bot *tgbotapi.BotAPI, chatID int64) {
	state := getPaiGowState(chatID)
	gameID := ""
	settled := false

	state.Mu.Lock()
	gameID = state.GameID
	state.Mu.Unlock()

	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ 牌九协程 panic，准备退款: game_id=%s panic=%s", formatPlainValue(gameID), formatPlainValue(r))
		}
		if gameID != "" && !settled {
			count, points, err := refundPaiGowBetsByGameID(gameID, "pai gow routine aborted")
			if err == nil && count > 0 {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("↩️ **本局推牌九异常中止**\n\n系统已自动退还 `%d` 名玩家共 `%d` 积分。", count, points))
			}
		}

		state.Mu.Lock()
		if state.GameID == gameID {
			state.IsActive = false
			state.IsDealing = false
			state.LastGameAt = time.Now()
		}
		state.Mu.Unlock()
	}()

	if gameID == "" {
		return
	}

	time.Sleep(paiGowBetDuration)

	state.Mu.Lock()
	if !state.IsActive || state.GameID != gameID {
		state.Mu.Unlock()
		return
	}
	state.IsDealing = true
	totalPlayers := len(state.Bets)
	memoryPool := state.TotalPool
	state.Mu.Unlock()

	if totalPlayers == 0 {
		refundPaiGowBetsByGameID(gameID, "no players")
		settled = true
		sendGroupAutoDeleteMessage(bot, chatID, "🍂 由于本局无人下注，推牌九已自动取消。")
		return
	}

	betsSnapshot, userPool, err := loadActivePaiGowBetsSnapshot(gameID)
	if err != nil {
		log.Printf("⚠️ 牌九结算读取有效下注失败: game_id=%s err=%s", formatPlainValue(gameID), formatPlainError(err))
		return
	}
	if userPool != memoryPool {
		log.Printf("pai gow settlement db snapshot differs from memory: game_id=%s memory_pool=%d db_pool=%d", formatPlainValue(gameID), memoryPool, userPool)
	}
	if len(betsSnapshot) == 0 {
		settled = true
		sendGroupAutoDeleteMessage(bot, chatID, "🍂 由于本局无人下注，推牌九已自动取消。")
		return
	}

	deck, err := shuffledPaiGowDeck()
	if err != nil {
		log.Printf("⚠️ 牌九洗牌失败，准备退款: game_id=%s err=%s", formatPlainValue(gameID), formatPlainError(err))
		return
	}
	if len(deck) < (len(betsSnapshot)+1)*2 {
		log.Printf("⚠️ 牌九牌堆不足，准备退款: game_id=%s players=%d", formatPlainValue(gameID), len(betsSnapshot))
		return
	}

	dealerHand := []PaiGowCard{deck[0], deck[1]}
	deck = deck[2:]
	dealerPoint := paiGowHandPoint(dealerHand)

	userIDs := make([]int64, 0, len(betsSnapshot))
	for uid := range betsSnapshot {
		userIDs = append(userIDs, uid)
	}
	sort.Slice(userIDs, func(i, j int) bool {
		left := betsSnapshot[userIDs[i]]
		right := betsSnapshot[userIDs[j]]
		if !left.BetAt.Equal(right.BetAt) {
			return left.BetAt.Before(right.BetAt)
		}
		return userIDs[i] < userIDs[j]
	})

	players := make([]PaiGowDealtPlayer, 0, len(userIDs))
	for _, uid := range userIDs {
		bet := betsSnapshot[uid]
		hand := []PaiGowCard{deck[0], deck[1]}
		deck = deck[2:]
		playerPoint := paiGowHandPoint(hand)
		won := playerPoint > dealerPoint
		result := "lose"
		payout := 0
		if won {
			result = "win"
			payout = bet.Points * 2
		}
		players = append(players, PaiGowDealtPlayer{
			UserID:      uid,
			UserName:    bet.UserName,
			Points:      bet.Points,
			Hand:        hand,
			PlayerPoint: playerPoint,
			Won:         won,
			Result:      result,
			Payout:      payout,
		})
	}

	dealerHandText := paiGowHandText(dealerHand)
	err = DB.Transaction(func(tx *gorm.DB) error {
		claimedCount := 0
		for _, player := range players {
			values := map[string]interface{}{
				"status":       RaceBetStatusSettled,
				"player_hand":  paiGowHandText(player.Hand),
				"dealer_hand":  dealerHandText,
				"player_point": player.PlayerPoint,
				"dealer_point": dealerPoint,
				"payout":       player.Payout,
				"result":       player.Result,
			}
			claimed, err := updatePaiGowBetStatusCAS(tx, gameID, player.UserID, RaceBetStatusActive, values)
			if err != nil {
				return err
			}
			if !claimed {
				continue
			}
			claimedCount++
			if player.Won {
				if err := applyPointDeltaInTx(
					tx,
					player.UserID,
					player.Payout,
					"pai_gow_win",
					fmt.Sprintf("牌九胜出，获得 %d 积分", player.Payout),
					"pai_gow",
					gameID,
				); err != nil {
					return err
				}
			}
		}
		if claimedCount != len(players) {
			return fmt.Errorf("PAI_GOW_SETTLEMENT_MISSED")
		}
		return nil
	})
	if err != nil {
		log.Printf("⚠️ 牌九结算失败，准备退款: game_id=%s err=%s", formatPlainValue(gameID), formatPlainError(err))
		return
	}
	settled = true

	sendPaiGowFinalAnnouncement(bot, chatID, dealerHand, dealerPoint, players)
}

func shuffledPaiGowDeck() ([]PaiGowCard, error) {
	suits := []string{"黑桃", "红桃", "梅花", "方块"}
	ranks := []struct {
		name  string
		point int
	}{
		{"A", 1},
		{"2", 2},
		{"3", 3},
		{"4", 4},
		{"5", 5},
		{"6", 6},
		{"7", 7},
		{"8", 8},
		{"9", 9},
		{"10", 0},
		{"J", 0},
		{"Q", 0},
		{"K", 0},
	}
	deck := make([]PaiGowCard, 0, 52)
	for _, suit := range suits {
		for _, rank := range ranks {
			deck = append(deck, PaiGowCard{Suit: suit, Rank: rank.name, Point: rank.point})
		}
	}
	for i := len(deck) - 1; i > 0; i-- {
		nBig, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return nil, err
		}
		j := int(nBig.Int64())
		deck[i], deck[j] = deck[j], deck[i]
	}
	return deck, nil
}

func paiGowHandPoint(hand []PaiGowCard) int {
	sum := 0
	for _, card := range hand {
		sum += card.Point
	}
	return sum % 10
}

func paiGowHandText(hand []PaiGowCard) string {
	parts := make([]string, 0, len(hand))
	for _, card := range hand {
		parts = append(parts, card.Suit+card.Rank)
	}
	return strings.Join(parts, "、")
}

func sendPaiGowFinalAnnouncement(bot *tgbotapi.BotAPI, chatID int64, dealerHand []PaiGowCard, dealerPoint int, players []PaiGowDealtPlayer) {
	var b strings.Builder
	b.WriteString("🃏 **推牌九开奖**\n\n")
	b.WriteString(fmt.Sprintf("庄家：%s｜`%d点`\n\n", escapeMarkdown(paiGowHandText(dealerHand)), dealerPoint))
	for _, player := range players {
		b.WriteString(fmt.Sprintf("@%s：%s｜`%d点`\n", escapeMarkdownPreservingEscapes(player.UserName), escapeMarkdown(paiGowHandText(player.Hand)), player.PlayerPoint))
		if player.Won {
			b.WriteString(fmt.Sprintf("压过庄家，赢得 `%d` 积分\n\n", player.Points))
		} else if player.PlayerPoint == dealerPoint {
			b.WriteString(fmt.Sprintf("同点庄家大半点，失去 `%d` 积分\n\n", player.Points))
		} else {
			b.WriteString(fmt.Sprintf("不敌庄家，失去 `%d` 积分\n\n", player.Points))
		}
	}

	sendPaiGowPermanentMarkdown(bot, chatID, strings.TrimSpace(b.String()))
}

func sendPaiGowPermanentMarkdown(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := sendNoAutoDelete(bot, msg); err != nil {
		log.Printf("⚠️ 发送牌九永久开奖结果失败: chat=%d err=%s", chatID, formatTelegramSendError(err))
	}
}
