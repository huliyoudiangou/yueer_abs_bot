package main

import (
	"errors"
	"fmt"
	"math"
	"time"

	"gorm.io/gorm"
)

// ==========================================
// 灵晶钱包 - Phase 1 经济核心
// 积分→灵晶单向桥，绝对不回退
// 所有灵晶消耗必须经过 SpendLingjing或 EarnLingjing
// ==========================================

var DailyMaxExchangePoints = 1000 // 每日最多兑换1000积分（10000灵晶）

// 兑换积分换灵晶（单向、仅限特定、仅写流水）
func ExchangePointsToLingjing(tx *gorm.DB, userID int64, pointsAmount int) (int, error) {
	if pointsAmount <= 0 {
		return 0, errors.New("兑换积分必须大于0")
	}
	if pointsAmount%100 != 0 || pointsAmount > 1000 {
		return 0, errors.New("兑换积分必须是100的整数倍，且最多1000")
	}

	today := time.Now().Format("20060102")
	var totalExchanged int
	err := tx.Transaction(func(ttx *gorm.DB) error {
		// 1. 确保用户有足够积分
		res := ttx.Model(&User{}).
			Where("id = ? AND points >= ?", userID, pointsAmount).
			Update("points", gorm.Expr("points - ?", pointsAmount))
		if res.Error != nil || res.RowsAffected == 0 {
			return errors.New("积分扣除失败或积分不足")
		}

		// 2. 查询今日已兑换额度（兜底判断）
		var quota DailyLingjingQuota
		findErr := ttx.Where("user_id = ? AND day_key = ?", userID, today).First(&quota).Error
		if errors.Is(findErr, gorm.ErrRecordNotFound) {
			quota = DailyLingjingQuota{UserID: userID, DayKey: today, Spent: pointsAmount, Exchanged: pointsAmount * 10}
			if err := ttx.Create(&quota).Error; err != nil {
				return fmt.Errorf("无法创建当日配额: %w", err)
			}
		} else if findErr != nil {
			return fmt.Errorf("查询当日配额失败: %w", findErr)
		} else {
			if quota.Spent+pointsAmount > DailyMaxExchangePoints {
				return fmt.Errorf("今日兑换已超限%d积分，剩余:%d", DailyMaxExchangePoints, DailyMaxExchangePoints-quota.Spent)
			}
			// 更新额度记录
			newQuota := DailyLingjingQuota{UserID: userID, DayKey: today, Spent: quota.Spent + pointsAmount, Exchanged: quota.Exchanged + pointsAmount*10}
			if err := ttx.Save(&newQuota).Error; err != nil {
				return err
			}
		}

		// 3. 创建灵晶到余额（写流水）
		lingjing := pointsAmount * 10
		if err := ttx.Model(&UserLingjingBalance{}).
			Where("user_id = ?", userID).
			Update("lingjing", gorm.Expr("lingjing + ?", lingjing)).Error; err != nil {
			return fmt.Errorf("无法更新灵晶余额: %w", err)
		}

		lt := LingjingTransaction{
			UserID:    userID,
			Delta:     lingjing,
			Type:      "exchange_to_lingjing",
			Description: fmt.Sprintf("积分%d转换为灵晶%d", pointsAmount, lingjing),
		}
		if err := ttx.Create(&lt).Error; err != nil {
			return fmt.Errorf("写积分交易失败: %w", err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	totalExchanged = pointsAmount * 10
	return totalExchanged, nil
}

// 消耗灵晶（喂养/强化/升星）
func SpendLingjing(tx *gorm.DB, userID int64, amount int, expenseType, description string) error {
	if amount <= 0 {
		return errors.New("invalid spend amount")
	}
	var bal UserLingjingBalance
	err := tx.Transaction(func(ttx *gorm.DB) error {
		if err := ttx.Where("user_id = ?", userID).First(&bal).Error; err != nil || bal.Lingjing < amount {
			return errors.New("灵晶不足")
		}
		// 直接扣减余额
		if err := ttx.Model(&UserLingjingBalance{}).
			Where("user_id = ?", userID).
			Update("lingjing", gorm.Expr("lingjing - ?", amount)).Error; err != nil {
			return fmt.Errorf("扣减灵晶失败: %w", err)
		}
		// 写流水（无论正负都有审计）
		lt := LingjingTransaction{
			UserID:    userID,
			Delta:     -amount,
			Type:      expenseType,
			Description: description,
		}
		if err := ttx.Create(&lt).Error; err != nil {
			return fmt.Errorf("写积分交易失败: %w", err)
		}
		return nil
	})
	return err
}

// 掉落/获得灵晶（为非求神通增加）
func EarnLingjing(tx *gorm.DB, userID int64, amount int, rewardType, description string) error {
	if amount <= 0 {
		return errors.New("invalid earn amount")
	}
	// 制成后置入
	if err := tx.Model(&UserLingjingBalance{}).
		Where("user_id = ?", userID).
		Update("lingjing", gorm.Expr("lingjing + ?", amount)).Error; err != nil {
		return fmt.Errorf("加灵晶失败: %w", err)
	}
	// 流水
	lt := LingjingTransaction{
		UserID:    userID,
		Delta:     amount,
		Type:      rewardType,
		Description: description,
	}
	if err := tx.Create(&lt).Error; err != nil {
		return fmt.Errorf("写积分事务失败: %w", err)
	}
	return nil
}

// 查询灵晶余额
func GetUserWalletBalance(userID int64) (int, error) {
	var bal UserLingjingBalance
	err := db.Where("user_id = ?", userID).First(&bal).Error
	if err != nil {
		return 0, err
	}
	return bal.Lingjing, nil
}

// 交易后的同步功能
func SyncUserBalance(userID int64) (int, error) {
	var latestTx LingjingTransaction
	err := db.Where("user_id = ?", userID).Order("created_at DESC").First(&latestTx).Error
	if err != nil {
		return 0, nil // 交易可能不存在，返回0不影响
	}
	return int(latestTx.BalanceAfter), nil
}
