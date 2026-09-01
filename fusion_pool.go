package main

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

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
