package main

import (
	"fmt"
	"log"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ==========================================
// 灵晶钱包系统（Phase 1）
// 所有灵侍消耗必须经过 SpendLingjing/EarnLingjing
// ==========================================

// DailyMaxExchangePoints 每日兑换积分上限
const DailyMaxExchangePoints = 1000

// ExchangePointsToLingjing 积分兑换灵晶（单向，永不逆反）
// 1积分 = 10灵晶，每日上限 1000积分 = 10000灵晶
func ExchangePointsToLingjing(tx *gorm.DB, userID int64, pointsAmount int) (int, error) {
	if pointsAmount <= 0 {
		return 0, fmt.Errorf("兑换积分必须大于0")
	}
	if pointsAmount%100 != 0 || pointsAmount > DailyMaxExchangePoints {
		return 0, fmt.Errorf("兑换积分必须是100的整数倍，且不超过每日上限%d积分", DailyMaxExchangePoints)
	}
	today := time.Now().Format("20060102")
	var lingjing int
	err := tx.Transaction(func(ttx *gorm.DB) error {
		// 扣积分（安全条件更新）
		result := ttx.Model(&User{}).Where("telegram_id = ? AND points >= ?", userID, pointsAmount).
			Update("points", gorm.Expr("points - ?", pointsAmount))
		if result.Error != nil {
			return fmt.Errorf("积分扣除失败")
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("积分不足")
		}

		// 检查并记录每日配额
		var quota DailyLingjingQuota
		findErr := ttx.Where("user_id = ? AND day_key = ?", userID, today).First(&quota).Error
		if findErr == gorm.ErrRecordNotFound {
			quota = DailyLingjingQuota{UserID: userID, DayKey: today, Spent: pointsAmount}
			if err := ttx.Create(&quota).Error; err != nil {
				return fmt.Errorf("无法创建当日配额: %w", err)
			}
		} else if findErr == nil {
			newSpent := quota.Spent + pointsAmount
			if newSpent > DailyMaxExchangePoints {
				remain := DailyMaxExchangePoints - quota.Spent
				return fmt.Errorf("今日兑换已超上限%d积分，剩余可兑换:%d", DailyMaxExchangePoints, remain)
			}
			quota.Spent = newSpent
			if err := ttx.Save(&quota).Error; err != nil {
				return fmt.Errorf("更新当日配额失败: %w", err)
			}
		} else {
			return fmt.Errorf("查询当日配额失败: %w", findErr)
		}

		// 更新钱包余额（不存在则创建）
		lingjing = pointsAmount * LingjingExchangeRate()
		var balance UserLingjingBalance
		balErr := ttx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).First(&balance).Error
		if balErr == gorm.ErrRecordNotFound {
			newBalance := UserLingjingBalance{
				UserID:      userID,
				Lingjing:    lingjing,
				Lingchen:    0,
				TotalEarned: lingjing,
				TotalSpent:  0,
			}
			if err := ttx.Create(&newBalance).Error; err != nil {
				return fmt.Errorf("无法创建灵晶钱包: %w", err)
			}
		} else if balErr == nil {
			before := balance.Lingjing
			balance.Lingjing += lingjing
			balance.TotalEarned += lingjing
			if err := ttx.Save(&balance).Error; err != nil {
				return fmt.Errorf("无法更新灵晶余额: %w", err)
			}
			_ = before
		} else {
			return fmt.Errorf("查询灵晶余额失败: %w", balErr)
		}

		// 记录灵晶交易流水
		transaction := LingjingTransaction{
			UserID:      userID,
			Delta:       lingjing,
			Type:        "exchange_from_points",
			Description: fmt.Sprintf("积分%d转换为灵晶%d", pointsAmount, lingjing),
			RefType:     "exchange",
			RefID:       fmt.Sprintf("%d:%d", userID, pointsAmount),
		}
		if err := ttx.Create(&transaction).Error; err != nil {
			return fmt.Errorf("写灵晶交易失败: %w", err)
		}
		return nil
	})
	return lingjing, err
}

// SpendLingjing 灵侍消耗灵晶
func SpendLingjing(tx *gorm.DB, userID int64, amount int, expenseType, description string) error {
	if amount <= 0 {
		return fmt.Errorf("invalid spend amount")
	}
	err := tx.Transaction(func(ttx *gorm.DB) error {
		result := ttx.Model(&UserLingjingBalance{}).Where("user_id = ? AND lingjing >= ?", userID, amount).
			Update("lingjing", gorm.Expr("lingjing - ?", amount))
		if result.Error != nil {
			return fmt.Errorf("灵晶扣除失败: %w", result.Error)
		}
		if result.RowsAffected == 0 {
			return fmt.Errorf("灵晶不足")
		}
		// 累加消耗统计
		ttx.Model(&UserLingjingBalance{}).Where("user_id = ?", userID).
			Update("total_spent", gorm.Expr("total_spent + ?", amount))

		transaction := LingjingTransaction{
			UserID:      userID,
			Delta:       -amount,
			Type:        expenseType, // "consume_capture", "consume_feed" 等
			Description: description,
			RefType:     "consume",
			RefID:       fmt.Sprintf("%d", userID),
		}
		if err := ttx.Create(&transaction).Error; err != nil {
			return fmt.Errorf("写灵晶交易失败: %w", err)
		}
		return nil
	})
	return err
}

// EarnLingjing 灵侍奖励灵晶
func EarnLingjing(tx *gorm.DB, userID int64, amount int, rewardType, description string) error {
	if amount <= 0 {
		return fmt.Errorf("invalid earn amount")
	}
	err := tx.Transaction(func(ttx *gorm.DB) error {
		var balance UserLingjingBalance
		findErr := ttx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("user_id = ?", userID).First(&balance).Error
		if findErr == gorm.ErrRecordNotFound {
			newBalance := UserLingjingBalance{UserID: userID, Lingjing: amount, TotalEarned: amount}
			if err := ttx.Create(&newBalance).Error; err != nil {
				return fmt.Errorf("无法创建灵晶钱包: %w", err)
			}
		} else if findErr == nil {
			if err := ttx.Model(&UserLingjingBalance{}).Where("user_id = ?", userID).
				Updates(map[string]interface{}{
					"lingjing":     gorm.Expr("lingjing + ?", amount),
					"total_earned": gorm.Expr("total_earned + ?", amount),
				}).Error; err != nil {
				return fmt.Errorf("加灵晶失败: %w", err)
			}
		} else {
			return fmt.Errorf("查询灵晶余额失败: %w", findErr)
		}

		transaction := LingjingTransaction{
			UserID:      userID,
			Delta:       amount,
			Type:        rewardType, // "battle_win", "battle_lose", "boss_drop" 等
			Description: description,
			RefType:     "reward",
			RefID:       fmt.Sprintf("%d", userID),
		}
		if err := ttx.Create(&transaction).Error; err != nil {
			return fmt.Errorf("写灵晶交易失败: %w", err)
		}
		return nil
	})
	return err
}

// GetUserWalletBalance 获取用户灵晶余额
func GetUserWalletBalance(tx *gorm.DB, userID int64) (int, error) {
	var balance UserLingjingBalance
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
		Where("user_id = ?", userID).First(&balance).Error
	if err == gorm.ErrRecordNotFound {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return balance.Lingjing, nil
}

// SyncUserBalance 余额对称核查（由交易流水重新汇总，仅记录日志）
func SyncUserBalance(tx *gorm.DB, userID int64) error {
	var txCount int64
	var totalAmount int64
	err := tx.Model(&LingjingTransaction{}).
		Where("user_id = ?", userID).
		Count(&txCount).Error
	if err != nil {
		return err
	}
	if txCount == 0 {
		return nil
	}
	err = tx.Model(&LingjingTransaction{}).
		Where("user_id = ?", userID).
		Select("COALESCE(SUM(delta), 0)").Scan(&totalAmount).Error
	if err != nil {
		return err
	}
	log.Printf("[灵晶] 余额对账核查 user=%d tx_count=%d total_delta=%d", userID, txCount, totalAmount)
	return nil
}
