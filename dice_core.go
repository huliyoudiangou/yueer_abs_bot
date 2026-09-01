package main

import (
	"crypto/rand"
	"fmt"
	"log"
	"math/big"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
)

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
	// 三界骰局已改为全天开放。
	return true
}

func isRaceOpenTime(now time.Time) bool {
	// 赛马已改为全天开放。
	return true
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
