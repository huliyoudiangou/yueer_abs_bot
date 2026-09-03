package main

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

func handleHorseRace(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if AppConfig.NoticeGroupID != 0 && !isUserInGroupFresh(bot, msg.From.ID, AppConfig.NoticeGroupID) {
		return
	}

	globalRace := getRaceState(msg.Chat.ID)
	userID := msg.From.ID
	chatID := msg.Chat.ID

	text := strings.TrimSpace(msg.Text)
	userName := msg.From.UserName
	if userName == "" {
		userName = msg.From.FirstName
	}
	safeName := escapeMarkdown(userName)

	// 发起赛马
	if text == "发起赛马" {
		if !isRaceOpenTime(time.Now()) {
			sendGroupAutoDeleteMessage(bot, chatID, "⏳ **赛马场关门啦！**\n\n赛马现已全天开放，请稍后重试。")
			return
		}

		casualUnlock := lockCasualGameChat(chatID)
		defer casualUnlock()
		if other, ok := casualGameActiveInChat(chatID, casualGameRace); ok {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("⚠️ 当前已有 **%s** 正在进行，请等它结束后再发起赛马！", casualGameDisplayName(other)))
			return
		}

		globalRace.Mu.Lock()
		if globalRace.IsActive {
			globalRace.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, "⚠️ 赛马场正在营业中，本局还未结束，请勿重复发起！")
			return
		}
		cdDuration := 1 * time.Minute
		if time.Since(globalRace.LastRaceAt) < cdDuration {
			cdLeft := int(cdDuration.Seconds() - time.Since(globalRace.LastRaceAt).Seconds())
			globalRace.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("🧹 赛马场正在打扫场地...\n\n🐎 马匹需要休息，请等待 **%d 秒** 后再发起下一局！", cdLeft))
			return
		}

		minBet, maxBet := calculateHorseRaceBetRange(0)

		globalRace.RaceID = fmt.Sprintf("RACE-%d-%s", chatID, generateRandomCode(8))
		globalRace.IsActive = true
		globalRace.IsRacing = false
		globalRace.Bets = make(map[int64]*PlayerBet)
		globalRace.TotalPool = 0
		globalRace.MinBet = minBet
		globalRace.MaxBet = maxBet
		globalRace.Mu.Unlock()

		notice := fmt.Sprintf("🏇 **皇家赛马场已开放！** 🏇\n\n💰 **本局限额**：`%d` - `%d` 积分\n📊 **奖池规则**：玩家筹码组成奖池，押中冠军者按本金比例瓜分\n⏱ **下注时间**：60 秒\n\n👇 **请在群内回复下注，如：** `押 1 10`\n\n1️⃣号: 🔴红影\n2️⃣号: 🔵蓝电\n3️⃣号: 🟡金光\n4️⃣号: 🟢绿风\n5️⃣号: 🟣紫幻", minBet, maxBet)
		sendGroupAutoDeleteMessage(bot, chatID, notice)

		go runHorseRaceRoutine(bot, chatID)
		return
	}

	// 🛡️ 下注环节：落库 + 原子扣费 + 唯一索引防重复下注
	if strings.HasPrefix(text, "押 ") {
		globalRace.Mu.Lock()
		if !globalRace.IsActive {
			globalRace.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 当前没有进行中的赛马，全天开放，请发送 `发起赛马` 开启新一局！", safeName))
			return
		}
		if globalRace.IsRacing {
			globalRace.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 比赛已经开始了，买定离手，下局请早！", safeName))
			return
		}
		if _, exists := globalRace.Bets[userID]; exists {
			globalRace.Mu.Unlock()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经下过注了，一人只能买一匹马！", safeName))
			return
		}

		raceID := globalRace.RaceID
		minBet := globalRace.MinBet
		maxBet := globalRace.MaxBet
		globalRace.Mu.Unlock()

		parts := strings.Split(text, " ")
		if len(parts) != 3 {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 格式错误，正确格式如：`押 1 10`", safeName))
			return
		}

		horseNum, err1 := strconv.Atoi(parts[1])
		points, err2 := strconv.Atoi(parts[2])

		if err1 != nil || err2 != nil || horseNum < 1 || horseNum > 5 {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 马号只能是 1-5，金额必须是纯数字！", safeName))
			return
		}
		if points < minBet {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 本局最低下注额为 **%d** 积分！", safeName, minBet))
			return
		}
		if points > maxBet {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 老板为了防止您倾家荡产，本局最高限额 **%d** 积分！", safeName, maxBet))
			return
		}

		// 第一步：下注落库 + 扣费，在同一个事务内完成。
		err := DB.Transaction(func(tx *gorm.DB) error {
			if _, _, err := ensureUserWalletInTx(tx, msg.From); err != nil {
				return err
			}

			// 先创建下注记录。
			// race_id + user_id 有唯一索引，所以同一局同一人只能成功插入一次。
			if err := createRaceBetInTx(tx, &RaceBet{
				RaceID:   raceID,
				ChatID:   chatID,
				UserID:   userID,
				UserName: safeName,
				HorseNum: horseNum,
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
				"race_bet",
				fmt.Sprintf("赛马下注：%d号马，消耗 %d 积分", horseNum, points),
				"race",
				raceID,
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
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经下过注了，一人只能买一匹马！", safeName))
			} else if errors.Is(err, errInsufficientPoints) {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 您的钱包可用积分不足！", safeName))
			} else {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 下注失败，系统繁忙，请稍后重试！", safeName))
			}
			return
		}

		refundRaceBet := func() {
			err := DB.Transaction(func(tx *gorm.DB) error {
				claimed, err := updateRaceBetStatusCAS(tx, raceID, userID, RaceBetStatusActive, map[string]interface{}{
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
					"race_refund",
					fmt.Sprintf("赛马异常退款，返还 %d 积分", points),
					"race",
					raceID,
				); err != nil {
					return err
				}
				return nil
			})

			if err != nil {
				log.Printf("⚠️ 赛马单人退款失败: race_id=%s user_id=%d points=%d err=%s", formatPlainValue(raceID), userID, points, formatPlainError(err))
				return
			}
		}

		// 第二步：扣费成功后，再次检查赛马状态。
		// 如果比赛刚好开跑，就退款并删除下注记录。
		globalRace.Mu.Lock()
		if !globalRace.IsActive || globalRace.IsRacing || globalRace.RaceID != raceID {
			globalRace.Mu.Unlock()
			refundRaceBet()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 买定离手，比赛已发车，您的资金已原路退回！", safeName))
			return
		}

		if _, exists := globalRace.Bets[userID]; exists {
			globalRace.Mu.Unlock()
			refundRaceBet()
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经下过注了，本次重复请求已退款。", safeName))
			return
		}

		globalRace.Bets[userID] = &PlayerBet{
			UserName: safeName,
			HorseNum: horseNum,
			Points:   points,
		}
		globalRace.TotalPool += points
		globalRace.Mu.Unlock()

		horseIcons := []string{"", "🔴", "🔵", "🟡", "🟢", "🟣"}
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✅ @%s 成功买入 **%d** 积分押注 %s%d号马！", safeName, points, horseIcons[horseNum], horseNum))
	}
}

// ------------------------------------
// 阶段三：跑马转播与智能彩池结算
// ------------------------------------
func runHorseRaceRoutine(bot *tgbotapi.BotAPI, chatID int64) {
	globalRace := getRaceState(chatID)

	raceID := ""
	settled := false

	globalRace.Mu.Lock()
	raceID = globalRace.RaceID
	globalRace.Mu.Unlock()

	// 全生命周期兜底：
	// 只要本函数中途 return、panic、Telegram API 发送失败，且比赛没有完成结算，
	// 就会自动退还本局所有 active 下注。
	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ 赛马协程 panic，准备退款: race_id=%s panic=%s", formatPlainValue(raceID), formatPlainValue(r))
		}

		if raceID != "" && !settled {
			count, points, err := refundRaceBetsByRaceID(raceID, "race routine aborted")
			if err == nil && count > 0 {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf(
					"↩️ **本局赛马异常中止**\n\n系统已自动退还 `%d` 名玩家共 `%d` 积分。",
					count,
					points,
				))
			}
		}

		globalRace.Mu.Lock()
		if globalRace.RaceID == raceID {
			globalRace.IsActive = false
			globalRace.IsRacing = false
			globalRace.LastRaceAt = time.Now()
		}
		globalRace.Mu.Unlock()
	}()

	if raceID == "" {
		return
	}

	time.Sleep(45 * time.Second)

	globalRace.Mu.Lock()
	if !globalRace.IsActive || globalRace.RaceID != raceID {
		globalRace.Mu.Unlock()
		return
	}
	globalRace.Mu.Unlock()

	sendGroupAutoDeleteMessage(bot, chatID, "⏱ **赛马场还有 15 秒停止下注！** 还没买的大佬抓紧最后机会！")

	time.Sleep(15 * time.Second)

	globalRace.Mu.Lock()
	if !globalRace.IsActive || globalRace.RaceID != raceID {
		globalRace.Mu.Unlock()
		return
	}

	globalRace.IsRacing = true
	totalPlayers := len(globalRace.Bets)
	globalRace.Mu.Unlock()

	if totalPlayers == 0 {
		refundRaceBetsByRaceID(raceID, "no players")
		settled = true
		sendGroupAutoDeleteMessage(bot, chatID, "🍂 由于本局无人下注，比赛已自动取消。")
		return
	}

	icons := []string{"🔴", "🔵", "🟡", "🟢", "🟣"}
	positions := []int{0, 0, 0, 0, 0}
	trackLen := 20
	startTime := time.Now()

	buildTrack := func(pos []int, elapsed float64) string {
		res := "🏇 **比赛激烈进行中...** 🏇\n\n"
		for i := 0; i < 5; i++ {
			track := ""
			for j := 0; j < trackLen; j++ {
				if j == pos[i] {
					track += "🐎"
				} else if j == trackLen-1 {
					track += "🏁"
				} else {
					track += "-"
				}
			}

			speed := 0.0
			if elapsed > 0 {
				speed = float64(pos[i]) / elapsed
			}

			res += fmt.Sprintf("%d号 %s [%s] ⏱ %.2f 格/秒\n", i+1, icons[i], track, speed)
		}
		return res
	}

	msg := tgbotapi.NewMessage(chatID, buildTrack(positions, 0))
	sentMsg, err := sendAutoDelete(bot, msg)
	if err != nil {
		log.Printf("⚠️ 赛马动画初始消息发送失败，准备退款: race_id=%s err=%s", formatPlainValue(raceID), formatTelegramSendError(err))
		return
	}

	winner := -1

	for winner == -1 {
		time.Sleep(2 * time.Second)

		var crossers []int

		for i := 0; i < 5; i++ {
			positions[i] += randomIntRange(1, 3)

			if positions[i] >= trackLen-1 {
				positions[i] = trackLen - 1
				crossers = append(crossers, i+1)
			}
		}

		if len(crossers) > 0 {
			if len(crossers) == 1 {
				winner = crossers[0]
			} else {
				winner = crossers[randomIntRange(0, len(crossers)-1)]
			}
		}

		elapsedSeconds := time.Since(startTime).Seconds()
		editMsg := tgbotapi.NewEditMessageText(chatID, sentMsg.MessageID, buildTrack(positions, elapsedSeconds))
		_, apiErr := bot.Request(editMsg)

		// Telegram 频控只跳过动画帧，不影响比赛结算。
		if apiErr != nil {
			if tgErr, ok := apiErr.(*tgbotapi.Error); ok && tgErr.Code == 429 {
				time.Sleep(time.Duration(tgErr.RetryAfter) * time.Second)
			}
		}
	}

	time.Sleep(1 * time.Second)
	finalTime := time.Since(startTime).Seconds()

	globalRace.Mu.Lock()
	if globalRace.RaceID != raceID {
		globalRace.Mu.Unlock()
		return
	}

	memoryUserPool := globalRace.TotalPool
	globalRace.Mu.Unlock()

	betsSnapshot, userPool, err := loadActiveRaceBetsSnapshot(raceID)
	if err != nil {
		log.Printf("鈿狅笍 璧涢┈缁撶畻璇诲彇鏈夋晥涓嬫敞澶辫触: race_id=%s err=%s", formatPlainValue(raceID), formatPlainError(err))
		return
	}
	if userPool != memoryUserPool {
		log.Printf("race settlement db snapshot differs from memory: race_id=%s memory_pool=%d db_pool=%d", formatPlainValue(raceID), memoryUserPool, userPool)
	}
	totalPrizePool := userPool

	winnerPool := 0
	for _, bet := range betsSnapshot {
		if bet.HorseNum == winner {
			winnerPool += bet.Points
		}
	}

	prizeRemainder := 0
	if winnerPool > 0 {
		prizeRemainder = totalPrizePool
		for _, bet := range betsSnapshot {
			if bet.HorseNum == winner {
				prizeRemainder -= (bet.Points * totalPrizePool) / winnerPool
			}
		}
		if prizeRemainder < 0 {
			prizeRemainder = 0
		}
	}

	if winnerPool > 0 {
		winList := ""
		poolAfter := 0
		isBurst := false
		expectedWinnerCount := 0
		expectedLoserCount := 0
		for _, bet := range betsSnapshot {
			if bet.HorseNum == winner {
				expectedWinnerCount++
			} else {
				expectedLoserCount++
			}
		}
		err := func() error {
			return runFusionPoolLockedTransaction(func(tx *gorm.DB) error {
				claimedWinnerCount := 0
				for uid, bet := range betsSnapshot {
					if bet.HorseNum == winner {
						winPts := (bet.Points * totalPrizePool) / winnerPool

						claimed, err := updateRaceBetStatusCAS(tx, raceID, uid, RaceBetStatusActive, map[string]interface{}{
							"status": RaceBetStatusSettled,
						})
						if err != nil {
							return err
						}
						if !claimed {
							continue
						}
						claimedWinnerCount++

						if err := applyPointDeltaInTx(
							tx,
							uid,
							winPts,
							"race_win",
							fmt.Sprintf("赛马中奖，获得 %d 积分", winPts),
							"race",
							raceID,
						); err != nil {
							return err
						}

						winList += fmt.Sprintf("👑 @%s : 喜提 `%d` 积分\n", escapeMarkdownPreservingEscapes(bet.UserName), winPts)
					}
				}

				loserRes := tx.Model(&RaceBet{}).
					Where("race_id = ? AND status = ?", raceID, RaceBetStatusActive).
					Update("status", RaceBetStatusSettled)
				if loserRes.Error != nil {
					return loserRes.Error
				}
				if loserRes.RowsAffected != int64(expectedLoserCount) {
					return fmt.Errorf("RACE_LOSER_SETTLEMENT_MISSED")
				}

				if claimedWinnerCount != expectedWinnerCount {
					return fmt.Errorf("RACE_WINNER_SETTLEMENT_MISSED")
				}
				if claimedWinnerCount == 0 {
					prizeRemainder = 0
				}

				if prizeRemainder > 0 {
					var err error
					poolAfter, isBurst, err = addPointsToFusionPoolInTx(tx, prizeRemainder)
					if err != nil {
						return err
					}
				}

				return nil
			})
		}()

		if err != nil {
			log.Printf("⚠️ 赛马结算失败，准备退款: race_id=%s err=%s", formatPlainValue(raceID), formatPlainError(err))
			return
		}
		settled = true
		poolNotice := ""
		if prizeRemainder > 0 {
			poolNotice = fmt.Sprintf(
				"\n🌊 奖池瓜分余数 `%d` 积分已注入天道奖池，当前水位：`%d/300`。",
				prizeRemainder,
				poolAfter,
			)

			if isBurst {
				poolNotice += "\n🎁 天道奖池已满，系统自动生成 `300` 积分灵气红包！"
				notifyFusionPoolBurst(bot, chatID, "皇家赛马场结算余数归入天道")
			}
		}

		finalAnnounce := fmt.Sprintf(
			"🏆 **比赛结束！** 🏆\n\n"+
				"🎉 恭喜 **%d号马 (%s)** 历时 **%.2f 秒** 勇夺冠军！\n\n"+
				"💰 玩家总筹码: `%d` 积分\n"+
				"📊 玩家奖池: `%d` 积分\n\n"+
				"**🤑 获胜名单 (按比例瓜分)：**\n%s%s",
			winner,
			icons[winner-1],
			finalTime,
			userPool,
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
			res := tx.Model(&RaceBet{}).
				Where("race_id = ? AND status = ?", raceID, RaceBetStatusActive).
				Update("status", RaceBetStatusSettled)
			if res.Error != nil {
				return res.Error
			}
			if userPool > 0 && res.RowsAffected == 0 {
				return fmt.Errorf("RACE_BET_SETTLEMENT_MISSED")
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
		log.Printf("⚠️ 赛马庄家通吃结算失败，准备退款: race_id=%s err=%s", formatPlainValue(raceID), formatPlainError(err))
		return
	}

	settled = true

	poolNotice := "🌊 本局无有效玩家筹码可注入天道奖池。"
	if userPool > 0 {
		poolNotice = fmt.Sprintf("🌊 已自动注入天道奖池：`+%d` 积分，当前水位：`%d/300`。", userPool, poolAfter)
		if isBurst {
			poolNotice += "\n🎁 天道奖池已满，系统自动生成 `300` 积分灵气红包！"
			notifyFusionPoolBurst(bot, chatID, "皇家赛马场无人押中，系统通吃筹码注入天道")
		}
	}

	finalAnnounce := fmt.Sprintf(
		"🏆 **比赛结束！** 🏆\n\n"+
			"🎉 冠军是冷门黑马 **%d号 (%s)**，历时 **%.2f 秒**！\n\n"+
			"🍂 **系统通吃**：本局无人押中冠军，玩家下注的 `%d` 积分由系统赢得。\n"+
			"%s",
		winner,
		icons[winner-1],
		finalTime,
		userPool,
		poolNotice,
	)

	sendGroupAutoDeleteMessage(bot, chatID, finalAnnounce)
}
