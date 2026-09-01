package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

// ==========================================
// 🧧 抢包引擎与后台运维 (物理环境区分)
// ==========================================

const redPacketGrabMaxAttempts = 8

type redPacketGrabResult struct {
	Packet RedPacket
	Points int
}

func validRedPacketCount(count int) bool {
	return count >= 3 && count <= 100
}
func applyRedPacketClaimScopeFilter(query *gorm.DB, userID int64) *gorm.DB {
	if query == nil {
		return query
	}
	return query.Where(
		"(COALESCE(red_packets.claim_scope, '') = '' OR (red_packets.claim_scope = ? AND EXISTS (SELECT 1 FROM world_boss_participants WHERE world_boss_participants.boss_id = red_packets.ref_id AND world_boss_participants.user_id = ? AND world_boss_participants.deleted_at IS NULL)))",
		redPacketClaimScopeWorldBossParticipant,
		userID,
	)
}

func hasActiveIneligibleWorldBossRedPacketTx(tx *gorm.DB, userID int64, prefix string) (bool, error) {
	if tx == nil || userID == 0 {
		return false, nil
	}

	var count int64
	err := tx.Model(&RedPacket{}).
		Where("left_count > ? AND is_finished = ? AND id LIKE ?", 0, false, prefix+"%").
		Where("claim_scope = ?", redPacketClaimScopeWorldBossParticipant).
		Where("id NOT IN (?)", tx.Model(&RedPacketGrab{}).
			Select("packet_id").
			Where("user_id = ?", userID)).
		Where("NOT EXISTS (SELECT 1 FROM world_boss_participants WHERE world_boss_participants.boss_id = red_packets.ref_id AND world_boss_participants.user_id = ? AND world_boss_participants.deleted_at IS NULL)", userID).
		Count(&count).Error
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

func hasActiveIneligibleWorldBossRedPacket(userID int64, prefix string) (bool, error) {
	return hasActiveIneligibleWorldBossRedPacketTx(DB, userID, prefix)
}

func claimableRedPacketQuery(tx *gorm.DB, userID int64, prefix string) *gorm.DB {
	if tx == nil {
		return tx
	}
	query := tx.
		Where("left_count > ? AND is_finished = ? AND id LIKE ?", 0, false, prefix+"%").
		Where("id NOT IN (?)", tx.Model(&RedPacketGrab{}).
			Select("packet_id").
			Where("user_id = ?", userID)).
		Order("created_at asc")
	return applyRedPacketClaimScopeFilter(query, userID)
}

func executeRedPacketGrabWithRetry(userID int64, safeName string, prefix string) (redPacketGrabResult, error) {
	var result redPacketGrabResult
	var lastErr error

	for attempt := 1; attempt <= redPacketGrabMaxAttempts; attempt++ {
		var attemptResult redPacketGrabResult
		err := DB.Transaction(func(tx *gorm.DB) error {
			packet, points, err := grabRedPacketInTx(tx, userID, safeName, prefix)
			if err != nil {
				return err
			}

			attemptResult = redPacketGrabResult{
				Packet: packet,
				Points: points,
			}
			return nil
		})
		if err == nil {
			attemptResult.Packet.LeftCount--
			attemptResult.Packet.LeftPoints -= attemptResult.Points
			if attemptResult.Packet.LeftCount <= 0 {
				attemptResult.Packet.LeftCount = 0
				attemptResult.Packet.IsFinished = true
			}
			return attemptResult, nil
		}

		lastErr = err
		if !isRetryableRedPacketGrabError(err) {
			return result, err
		}
		if attempt < redPacketGrabMaxAttempts {
			time.Sleep(redPacketGrabRetryDelay(attempt))
		}
	}

	if lastErr == nil {
		lastErr = errConcurrentRedPacketGrabRetry
	}
	return result, fmt.Errorf("%w: %s", errConcurrentRedPacketGrabRetry, formatPlainError(lastErr))
}

func createRedPacketGrabInTx(tx *gorm.DB, grab *RedPacketGrab) error {
	if tx == nil || grab == nil {
		return fmt.Errorf("RED_PACKET_GRAB_INVALID")
	}
	entry := *grab
	entry.GrabberName = formatPlainValue(entry.GrabberName)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("RED_PACKET_GRAB_CREATE_MISSED")
	}
	return nil
}

func createRedPacketInTx(tx *gorm.DB, packet *RedPacket) error {
	if tx == nil || packet == nil {
		return fmt.Errorf("RED_PACKET_INVALID")
	}
	entry := *packet
	entry.ID = formatPlainValue(entry.ID)
	entry.SenderName = formatPlainValue(entry.SenderName)
	entry.RefType = formatPlainValue(entry.RefType)
	entry.RefID = formatPlainValue(entry.RefID)
	entry.ClaimScope = formatPlainValue(entry.ClaimScope)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("RED_PACKET_CREATE_MISSED")
	}
	*packet = entry
	return nil
}

func grabRedPacketInTx(tx *gorm.DB, userID int64, safeName string, prefix string) (RedPacket, int, error) {
	var packet RedPacket
	// 选取最早一个“本用户尚未领取”的有效红包。
	// 之前固定取最早红包再判重，会导致同时存在多个红包时，
	// 已领过最早那个的用户被判 errAlreadyGrabbed 而无法领取较新的红包，
	// 直到最早的红包被抢空。这里用子查询排除已领取的红包修复该锁死。
	query := claimableRedPacketQuery(tx, userID, prefix)
	if err := query.First(&packet).Error; err != nil {
		return RedPacket{}, 0, err
	}

	var grabRecord RedPacketGrab
	if err := tx.Where("packet_id = ? AND user_id = ?", packet.ID, userID).First(&grabRecord).Error; err == nil {
		return RedPacket{}, 0, errAlreadyGrabbed
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return RedPacket{}, 0, err
	}

	if packet.LeftCount <= 0 || packet.LeftPoints <= 0 || packet.LeftPoints < packet.LeftCount {
		return RedPacket{}, 0, fmt.Errorf("red packet balance inconsistent: packet=%s left_count=%d left_points=%d", packet.ID, packet.LeftCount, packet.LeftPoints)
	}

	grabPoints := 0
	if packet.LeftCount == 1 {
		grabPoints = packet.LeftPoints
	} else {
		max := (packet.LeftPoints / packet.LeftCount) * 2
		if max <= 1 {
			max = 2
		}
		nBig, err := rand.Int(rand.Reader, big.NewInt(int64(max-1)))
		if err != nil {
			return RedPacket{}, 0, errRandomFailed
		}
		grabPoints = int(nBig.Int64()) + 1
		if grabPoints >= packet.LeftPoints {
			grabPoints = packet.LeftPoints - packet.LeftCount + 1
		}
	}
	if grabPoints <= 0 {
		return RedPacket{}, 0, fmt.Errorf("red packet grab points invalid: packet=%s points=%d", packet.ID, grabPoints)
	}

	updateData := map[string]interface{}{
		"left_count":  gorm.Expr("left_count - 1"),
		"left_points": gorm.Expr("left_points - ?", grabPoints),
	}

	if packet.LeftCount == 1 {
		updateData["is_finished"] = true
	}

	// CAS 条件同时检查 left_count、left_points 和 is_finished。
	// 只要红包状态被其他并发请求改过，本次事务回滚，由外层重新选包重试。
	res := tx.Model(&RedPacket{}).
		Where("id = ? AND left_count = ? AND left_points = ? AND is_finished = ?", packet.ID, packet.LeftCount, packet.LeftPoints, false).
		Updates(updateData)

	if res.Error != nil {
		return RedPacket{}, 0, res.Error
	}

	if res.RowsAffected == 0 {
		return RedPacket{}, 0, errConcurrentRedPacketGrabRetry
	}

	if err := createRedPacketGrabInTx(tx, &RedPacketGrab{
		PacketID:    packet.ID,
		UserID:      userID,
		GrabberName: safeName,
		Points:      grabPoints,
		GrabbedAt:   time.Now(),
	}); err != nil {
		// 如果唯一索引触发，说明用户已经抢过。
		// 返回 ALREADY_GRABBED，事务会自动回滚前面的红包扣减。
		if isUniqueConstraintError(err) {
			return RedPacket{}, 0, errAlreadyGrabbed
		}
		return RedPacket{}, 0, err
	}

	if err := applyPointDeltaInTx(
		tx,
		userID,
		grabPoints,
		"redpacket_grab",
		fmt.Sprintf("抢到红包 %s，获得 %d 积分", packet.ID, grabPoints),
		"redpacket",
		packet.ID,
	); err != nil {
		return RedPacket{}, 0, err
	}

	return packet, grabPoints, nil
}

func isRetryableRedPacketGrabError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, errConcurrentRedPacketGrabRetry) {
		return true
	}

	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "database is locked") ||
		strings.Contains(errText, "database table is locked") ||
		strings.Contains(errText, "sqlite_busy")
}

func redPacketGrabRetryDelay(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}

	delay := time.Duration(attempt*15) * time.Millisecond
	if nBig, err := rand.Int(rand.Reader, big.NewInt(20)); err == nil {
		delay += time.Duration(nBig.Int64()) * time.Millisecond
	}
	return delay
}

func userAlreadyGrabbedAllActiveRedPackets(userID int64, prefix string) (bool, error) {
	if DB == nil || userID == 0 {
		return false, nil
	}

	activeQuery := applyRedPacketClaimScopeFilter(DB.Model(&RedPacket{}).
		Where("left_count > ? AND is_finished = ? AND id LIKE ?", 0, false, prefix+"%"), userID)

	var activeCount int64
	if err := activeQuery.Count(&activeCount).Error; err != nil {
		return false, err
	}
	if activeCount == 0 {
		return false, nil
	}

	var eligibleCount int64
	eligibleQuery := applyRedPacketClaimScopeFilter(DB.Model(&RedPacket{}).
		Where("left_count > ? AND is_finished = ? AND id LIKE ?", 0, false, prefix+"%").
		Where("id NOT IN (?)", DB.Model(&RedPacketGrab{}).
			Select("packet_id").
			Where("user_id = ?", userID)), userID)
	if err := eligibleQuery.Count(&eligibleCount).Error; err != nil {
		return false, err
	}
	return eligibleCount == 0, nil
}

func handleGrabRedPacket(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if AppConfig.NoticeGroupID != 0 && !isMessageFromNoticeGroup(msg) && !isUserInGroupFresh(bot, msg.From.ID, AppConfig.NoticeGroupID) {
		if msg.Chat.IsPrivate() {
			replyText(bot, msg.Chat.ID, "⚠️ **访问受限：您尚未加入官方群组！**\n👉 请先加群后再参与抢红包。")
		}
		return
	}

	// 🚨 彻底拆除全局排队大锁 grabMutex，全面释放并发吞吐率

	userID := msg.From.ID
	chatID := msg.Chat.ID
	userName := msg.From.UserName
	if userName == "" {
		userName = msg.From.FirstName
	}
	safeName := escapeMarkdown(userName)

	var u User
	if walletUser, _, err := ensureUserWallet(msg.From); err != nil {
		log.Printf("❌ 创建幽灵钱包失败: user=%d err=%s", userID, formatPlainError(err))
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("❌ @%s 钱包初始化失败，请稍后重试。", safeName))
		return
	} else {
		u = walletUser
	}

	prefix := "HB-"
	actionWord := "抢到"
	if msg.Text == "沾仙气" {
		prefix = "FS-"
		actionWord = "沾到"
	}

	var grabPoints int
	var packet RedPacket

	// 🛡️ 采用乐观锁 CAS + 唯一索引双保险，杜绝重复领取。
	result, err := executeRedPacketGrabWithRetry(userID, safeName, prefix)
	if err == nil {
		packet = result.Packet
		grabPoints = result.Points
	}

	if err != nil {
		alreadyGrabbedAll := false
		ineligibleWorldBossPacket := false
		var redPacketStateErr error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			alreadyGrabbedAll, redPacketStateErr = userAlreadyGrabbedAllActiveRedPackets(userID, prefix)
			if redPacketStateErr == nil && !alreadyGrabbedAll {
				ineligibleWorldBossPacket, redPacketStateErr = hasActiveIneligibleWorldBossRedPacket(userID, prefix)
			}
		}
		if redPacketStateErr != nil {
			log.Printf("⚠️ 红包领取状态读取失败: user=%d prefix=%s err=%s", userID, formatPlainValue(prefix), formatPlainError(redPacketStateErr))
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("⚠️ @%s 红包状态暂时读取失败，请稍后再试。", safeName))
		} else if errors.Is(err, errAlreadyGrabbed) || (errors.Is(err, gorm.ErrRecordNotFound) && alreadyGrabbedAll) {
			if prefix == "FS-" {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 您已经在这场机缘中沾过仙气了，不可多贪！", safeName))
			} else {
				sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("✋ @%s 你已经抢过这个红包啦！", safeName))
			}
		} else if errors.Is(err, gorm.ErrRecordNotFound) && ineligibleWorldBossPacket {
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("⚔️ @%s 这份 Boss 红包仅限本期参加 Boss 的道友领取。", safeName))
		} else if errors.Is(err, errConcurrentRedPacketGrabRetry) {
			log.Printf("⚠️ 红包领取并发重试耗尽: user=%d prefix=%s err=%s", userID, formatPlainValue(prefix), formatPlainError(err))
			if prefix == "FS-" {
				sendGroupAutoDeleteMessage(bot, chatID, "⏳ 当前仙缘争抢太激烈，请再发送 `沾仙气` 试一次。")
			} else {
				sendGroupAutoDeleteMessage(bot, chatID, "⏳ 当前红包争抢太激烈，请再抢一次。")
			}
		} else {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 红包领取失败: user=%d prefix=%s err=%s", userID, formatPlainValue(prefix), formatPlainError(err))
			}
			if prefix == "FS-" {
				sendGroupAutoDeleteMessage(bot, chatID, "🫙 哎呀手慢了，当前天地福泽已被瓜分完毕，静待下一位大能飞升吧！")
			} else {
				sendGroupAutoDeleteMessage(bot, chatID, "🫙 哎呀手慢了，当前没有正在发放的红包，或者已经被抢光啦！")
			}
		}
		return
	}

	balanceText := fmt.Sprintf("`%d` 积分", u.Points)
	if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		log.Printf("⚠️ 红包领取后余额读取失败: user=%d packet=%s err=%s", userID, formatPlainValue(packet.ID), formatPlainError(err))
		balanceText = "`读取失败`"
	} else {
		balanceText = fmt.Sprintf("`%d` 积分", u.Points)
	}
	if packet.LeftCount == 0 {
		var grabs []RedPacketGrab
		grabsErr := DB.Where("packet_id = ?", packet.ID).Order("points desc").Find(&grabs).Error
		if grabsErr != nil {
			log.Printf("⚠️ 红包抢空榜读取失败: packet=%s user=%d err=%s", formatPlainValue(packet.ID), userID, formatPlainError(grabsErr))
		}

		var title, senderTitle string
		if prefix == "FS-" {
			title = "💥 **本次天地福泽已被大家吸收完毕！**"
			senderTitle = "天道赐福"
		} else {
			title = "💥 **该红包已被抢空！**"
			senderTitle = "发起人"
		}

		summary := fmt.Sprintf("\n\n%s\n\n🧧 %s: %s\n💰 总积分: %d\n📦 总份数: %d\n\n**📊 气运争夺风云榜：**\n", title, senderTitle, escapeMarkdownPreservingEscapes(packet.SenderName), packet.TotalPoints, packet.Count)
		bestPoints, bestUser := 0, ""
		if grabsErr != nil {
			summary += "\n⚠️ 气运榜暂时读取失败，请稍后查看。\n"
		} else if len(grabs) > 0 {
			bestPoints, bestUser = grabs[0].Points, grabs[0].GrabberName
		}
		for i, g := range grabs {
			medal := "▪️"
			if i == 0 {
				medal = "🥇"
			} else if i == 1 {
				medal = "🥈"
			} else if i == 2 {
				medal = "🥉"
			}
			summary += fmt.Sprintf("%s @%s : `%d` 积分\n", medal, escapeMarkdownPreservingEscapes(g.GrabberName), g.Points)
		}
		if bestUser != "" {
			summary += fmt.Sprintf("\n👑 **手气最佳** 👑\n🏆 恭喜 @%s 狂揽 `%d` 积分！", escapeMarkdownPreservingEscapes(bestUser), bestPoints)
		}

		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("🎉 恭喜 @%s %s了最后一份 **%d** 积分！\n🪙 当前总资产: %s%s", safeName, actionWord, grabPoints, balanceText, summary))
	} else {
		sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("🎉 恭喜 @%s %s了 **%d** 积分！\n🪙 当前总资产: %s\n\n📦 剩余份数: `%d` 份", safeName, actionWord, grabPoints, balanceText, packet.LeftCount))
	}
}

func backupDatabaseToTelegram(bot *tgbotapi.BotAPI, actorID int64, reason string) {
	messageID, err := sendEncryptedBackupToTelegram(bot, "manual")
	if err != nil {
		log.Printf("⚠️ 手动加密备份失败: actor=%d err=%s", actorID, formatPlainError(err))
		auditErr := writeAuditLogInTx(DB, actorID, "MANUAL_BACKUP_FAILED", "database_backup", 0, fmt.Sprintf("手动触发加密数据库备份失败，原因：%s，错误：%s", formatPlainValue(reason), formatPlainError(err)))
		if auditErr != nil {
			log.Printf("⚠️ 手动加密备份失败审计写入失败: actor=%d err=%s", actorID, formatPlainError(auditErr))
			notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 手动加密备份失败，且失败审计写入失败。\n\n备份错误: %s\n审计错误: %s", formatPlainError(err), formatPlainError(auditErr)))
		}
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 手动加密备份失败\n\n错误: %s", formatPlainError(err)))
		return
	}

	if auditErr := writeAuditLogInTx(DB, actorID, "MANUAL_BACKUP", "database_backup", 0, fmt.Sprintf("手动触发加密数据库备份成功，message_id=%d，原因：%s", messageID, formatPlainValue(reason))); auditErr != nil {
		log.Printf("⚠️ 手动加密备份成功审计写入失败: actor=%d message_id=%d err=%s", actorID, messageID, formatPlainError(auditErr))
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 手动加密备份已发送，但成功审计写入失败，请人工核查。\n\nmessage_id: %d\n审计错误: %s", messageID, formatPlainError(auditErr)))
	}
	log.Printf("✅ 手动加密备份发送成功: actor=%d message_id=%d", actorID, messageID)
}
