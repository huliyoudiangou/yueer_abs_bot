package main

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

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

func calculateHorseRaceBetRange(_ float64) (int, int) {
	// 赛马下注限额固定为 3-15 积分，不随全服平均积分变化。
	return 3, 15
}

// ==========================================
// 🎲 三界骰局核心引擎与动画渲染
// ==========================================
