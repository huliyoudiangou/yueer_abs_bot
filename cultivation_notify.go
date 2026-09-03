package main

import (
	"fmt"
	"log"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

// ==========================================
// 📿 小阶段自动突破检测与双端通知引擎
// ==========================================
func fetchReportAndCheckUpgrade(bot *tgbotapi.BotAPI, userID int64, absUserID string) string {
	if absUserID == "" {
		return ""
	}

	oldCul := GetOrCreateCultivation(userID)
	oldRealm := GetRealmName(oldCul)

	reportStr := absClient.GetPersonalReport(absUserID)

	newCul := GetOrCreateCultivation(userID)
	if oldCul == nil || newCul == nil {
		log.Printf("⚠️ 境界变化检查修仙档案读取失败: user=%d old_nil=%t new_nil=%t", userID, oldCul == nil, newCul == nil)
		return reportStr
	}
	newRealm := GetRealmName(newCul)

	if oldRealm != newRealm {
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 境界变化公告读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
		}

		displayName := escapeMarkdown(u.Username)
		if displayName == "" {
			displayName = "神秘道友"
		}

		if chat, err := bot.GetChat(tgbotapi.ChatInfoConfig{ChatConfig: tgbotapi.ChatConfig{ChatID: userID}}); err == nil {
			if chat.UserName != "" {
				displayName = telegramUsernameMentionMarkdown(chat.UserName)
			}
		}

		announce := fmt.Sprintf("🎉 **【仙途精进·修为突破】** 🎉\n\n恭喜道友 %s 闭关苦修，厚积薄发！\n\n✨ 成功从 **【%s】** 突破至全新的 **【%s】** 境界！\n\n*(大道漫漫，望道友继续潜心听书，早日登临绝顶！)*", displayName, oldRealm, newRealm)

		if _, err := sendAutoDelete(bot, tgbotapi.NewMessage(userID, announce)); err != nil {
			log.Printf("发送修为突破私聊通知失败: user=%d err=%s", userID, formatTelegramSendError(err))
		}

		if AppConfig.NoticeGroupID != 0 {
			// 如果是大境界跨越，只发突破通报（奖池逻辑已在雷劫里处理）
			if oldCul.MajorRealm != newCul.MajorRealm {
				sendGroupAutoDeleteMessage(bot, AppConfig.NoticeGroupID, announce)
			} else {
				// 🚨 小阶段福利：调用全局安全引擎注入奖池
				smallPts := randomIntRange(5, 10)

				currentPool, isBurst := addPointsToFusionPool(smallPts)

				progressText := ""
				if !isBurst {
					progressText = fmt.Sprintf("\n\n*(💧 此番精进引动天地共鸣，为天道奖池注入了 `%d` 积分，当前进度 `%d/300`)*", smallPts, currentPool)
					sendGroupAutoDeleteMessage(bot, AppConfig.NoticeGroupID, announce+progressText)
				} else {
					progressText = fmt.Sprintf("\n\n*(💧 此番精进成为了压轴造化，为天道奖池注入了最后 `%d` 积分！)*", smallPts)
					sendGroupAutoDeleteMessage(bot, AppConfig.NoticeGroupID, announce+progressText)

					notifyFusionPoolBurst(bot, AppConfig.NoticeGroupID, "众道友接连突破引动天地异象")
				}
			}
		}
	}

	return reportStr
}

// ==========================================
// 📜 上古老玩家历史破境功勋安全退税对账组件
// ==========================================
func checkAndCompensateLegacyUser(bot *tgbotapi.BotAPI, userID int64) {
	cul := GetOrCreateCultivation(userID)
	if cul == nil {
		log.Printf("⚠️ 历史补偿检查修仙档案读取失败: user=%d", userID)
		return
	}
	if cul.MajorRealm < 2 {
		return
	}

	var pointsToAdd int
	var codes []string
	var poolInjectedPts int
	var needNotify bool

	err := DB.Transaction(func(tx *gorm.DB) error {
		var txU User
		if err := tx.Where("telegram_id = ?", userID).First(&txU).Error; err != nil {
			return err
		}

		// 原子抢占补偿资格。
		// 只有 is_compensated = false 的时候才能更新成功。
		// 并发情况下，只有一个请求 RowsAffected == 1。
		res := tx.Model(&User{}).
			Where("telegram_id = ? AND is_compensated = ?", userID, false).
			Update("is_compensated", true)

		if res.Error != nil {
			return res.Error
		}

		if res.RowsAffected == 0 {
			return nil
		}

		calcPoints := 0
		calcCodes := []string{}

		if cul.MajorRealm >= 2 {
			calcPoints += 20
		}
		if cul.MajorRealm >= 3 {
			calcPoints += 40
		}
		if cul.MajorRealm >= 4 {
			calcPoints += 100

			c, err := createLegacyInviteCodeInTx(tx)
			if err != nil {
				return err
			}
			calcCodes = append(calcCodes, c)
		}
		if cul.MajorRealm >= 5 {
			calcPoints += 200

			c, err := createLegacyInviteCodeInTx(tx)
			if err != nil {
				return err
			}
			calcCodes = append(calcCodes, c)
		}

		if calcPoints > 0 {
			if err := applyPointDeltaInTx(
				tx,
				userID,
				calcPoints,
				"legacy_compensation",
				fmt.Sprintf("老玩家历史突破补偿，获得 %d 积分", calcPoints),
				"legacy_compensation",
				fmt.Sprintf("realm:%d:%d", cul.MajorRealm, cul.MinorRealm),
			); err != nil {
				return err
			}
		}

		missedUpgrades := 0
		if cul.MajorRealm > 0 {
			missedUpgrades = (cul.MajorRealm-1)*3 + (cul.MinorRealm - 1)
		}

		calcPoolInjectedPts := 0
		for i := 0; i < missedUpgrades; i++ {
			calcPoolInjectedPts += randomIntRange(5, 10)
		}

		pointsToAdd = calcPoints
		codes = calcCodes
		poolInjectedPts = calcPoolInjectedPts
		needNotify = true

		return nil
	})

	if err != nil {
		log.Printf("⚠️ 老玩家补偿失败: user_id=%d err=%s", userID, formatPlainError(err))
		return
	}

	// 没抢到补偿资格，说明已经补偿过了。
	if !needNotify {
		return
	}

	codeStr := ""
	if len(codes) > 0 {
		codeStr = "\n🎁 **附赠大道拉新机缘**：\n"
		for i, code := range codes {
			codeStr += fmt.Sprintf("🎫 专属裂变邀请码 %d：`%s`\n", i+1, code)
		}
	}

	poolMsg := ""
	if poolInjectedPts > 0 {
		_, isBurst := addPointsToFusionPool(poolInjectedPts)
		poolMsg = fmt.Sprintf("\n\n🌊 **天道补全**：系统已追溯您历次小境界的突破造化，共将 `%d` 积分厚礼代为您注入全服【天道融合大奖池】！", poolInjectedPts)

		if isBurst {
			notifyFusionPoolBurst(bot, AppConfig.NoticeGroupID, "上古大能复苏查账，引动浩瀚天地异象")
		}
	}

	msg := fmt.Sprintf(
		"📜 **【天道密卷·历史破境功勋大对账】** 📜\n\n"+
			"检测到您作为本界资深修士，实力出众，特此跨越时空为您下发退税大补帖：\n\n"+
			"💰 **历史突破仙石退税**：`+%d` 积分\n%s%s\n\n"+
			"📈 灵石资产已注入您的乾坤袋，功勋标记已入册，愿道友仙途永昌！",
		pointsToAdd,
		codeStr,
		poolMsg,
	)

	if _, err := sendAutoDelete(bot, tgbotapi.NewMessage(userID, msg)); err != nil {
		log.Printf("发送红包领取私聊通知失败: user=%d err=%s", userID, formatTelegramSendError(err))
	}
}

func createLegacyInviteCodeInTx(tx *gorm.DB) (string, error) {
	for i := 0; i < 5; i++ {
		code := "YQM-" + generateRandomCode(12)
		if err := createInviteCodeRecord(tx, code); err == nil {
			return code, nil
		}
	}

	return "", fmt.Errorf("生成历史补偿邀请码失败")
}

func handleBreakthroughConfirmation(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, session *SessionState, text string) {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return
	}
	if session == nil {
		session = getSession(msg.From.ID)
	}

	if !msg.Chat.IsPrivate() {
		registerIncomingGroupCommandForAutoDelete(msg)
	}

	mode := session.GetTemp("bt_mode")
	if (mode == "USE_INVENTORY" && text == "确认渡劫") || (mode == "AUTO_BUY" && text == "确认代购并渡劫") {
		// 将控制权移交给底层渡劫处决引擎
		ExecuteBreakthrough(bot, msg, mode)
	} else {
		replyText(bot, msg.Chat.ID, "🛑 您已压制体内翻涌的气血，取消了本次渡劫。")
	}
	clearSession(msg.From.ID)
}

// ==========================================
// 🚀 机器人交互核心枢纽
// ==========================================
