package main

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

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
			sendGroupAutoDeleteMessage(bot, chatID, "⏳ **三界骰局尚未开放！**\n\n三界骰局现已全天开放，请稍后重试。")
			return
		}

		casualUnlock := lockCasualGameChat(chatID)
		defer casualUnlock()
		if other, ok := casualGameActiveInChat(chatID, casualGameDice); ok {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("⚠️ 当前已有 **%s** 正在进行，请等它结束后再发起三界骰局！", casualGameDisplayName(other)))
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
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 当前没有进行中的骰局，全天开放，请发送 `发起骰子` 开启新一局！", safeName))
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
