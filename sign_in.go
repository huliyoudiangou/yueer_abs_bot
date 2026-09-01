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
	"gorm.io/gorm/clause"
)

func signInMonthKey(t time.Time) string {
	loc := time.FixedZone("CST", 8*3600)
	return t.In(loc).Format("200601")
}

func signInDateKey(t time.Time) string {
	loc := time.FixedZone("CST", 8*3600)
	return t.In(loc).Format("2006-01-02")
}

func daysInMonth(t time.Time) int {
	firstOfNextMonth := time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, t.Location())
	lastOfThisMonth := firstOfNextMonth.AddDate(0, 0, -1)
	return lastOfThisMonth.Day()
}

func randomIntRange(min int, max int) int {
	if max <= min {
		return min
	}

	nBig, err := rand.Int(rand.Reader, big.NewInt(int64(max-min+1)))
	if err != nil {
		return min
	}

	return int(nBig.Int64()) + min
}

func calculateSignStreakReward(streak *MonthlySignInStreak, now time.Time) (int, string) {
	fullDays := daysInMonth(now)

	switch {
	case streak.StreakDays == 3 && !streak.Rewarded3Days:
		streak.Rewarded3Days = true
		return 1, "连续签到3天奖励"

	case streak.StreakDays == 7 && !streak.Rewarded7Days:
		streak.Rewarded7Days = true
		return 2, "连续签到7天奖励"

	case streak.StreakDays == 14 && !streak.Rewarded14Days:
		streak.Rewarded14Days = true
		return randomIntRange(3, 5), "连续签到14天奖励"

	case streak.StreakDays == 21 && !streak.Rewarded21Days:
		streak.Rewarded21Days = true
		return randomIntRange(5, 7), "连续签到21天奖励"

	case streak.StreakDays == fullDays && !streak.RewardedFull:
		streak.RewardedFull = true
		return randomIntRange(8, 15), "本月全勤奖励"
	}

	return 0, ""
}

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}

	errText := strings.ToLower(err.Error())
	return strings.Contains(errText, "unique") ||
		strings.Contains(errText, "constraint failed") ||
		strings.Contains(errText, "duplicate")
}

func applyPillAudioTimeInTx(tx *gorm.DB, userID int64, addHours float64) error {
	if tx == nil || userID == 0 || addHours <= 0 {
		return fmt.Errorf("INVALID_PILL_AUDIO_TIME")
	}
	now := time.Now()
	// 注意：cultivations 表没有 updated_at 列（Cultivation 未嵌入 gorm.Model），
	// 之前的 DoUpdates 误写了 "updated_at"，会导致 SQLite 报 "no such column: updated_at"，
	// 进而让整个吞服事务回滚，用户看到“系统繁忙”。这里只更新真实存在的列。
	res := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"pill_audio_time": gorm.Expr("pill_audio_time + ?", addHours),
		}),
	}).Create(&Cultivation{
		UserID:           userID,
		PillAudioTime:    addHours,
		MajorRealm:       0,
		MinorRealm:       1,
		TribulationFails: 0,
		ConsolidateUntil: now.Add(-24 * time.Hour),
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("PILL_AUDIO_TIME_GRANT_MISSED")
	}
	return nil
}

func createItemUsageLogInTx(tx *gorm.DB, logEntry *ItemUsageLog) error {
	if tx == nil || logEntry == nil {
		return fmt.Errorf("ITEM_USAGE_LOG_INVALID")
	}
	entry := *logEntry
	entry.ItemName = strings.TrimSpace(entry.ItemName)
	if entry.UserID == 0 || entry.ItemName == "" {
		return fmt.Errorf("ITEM_USAGE_LOG_INVALID")
	}
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("ITEM_USAGE_LOG_CREATE_MISSED")
	}
	*logEntry = entry
	return nil
}

func createItemUsageQuotaIfMissingInTx(tx *gorm.DB, quota *ItemUsageQuota) error {
	if tx == nil || quota == nil {
		return fmt.Errorf("ITEM_USAGE_QUOTA_INVALID")
	}
	entry := *quota
	entry.ItemName = strings.TrimSpace(entry.ItemName)
	entry.PeriodKey = formatPlainValue(entry.PeriodKey)
	if entry.UserID == 0 || entry.ItemName == "" || entry.PeriodKey == "" {
		return fmt.Errorf("ITEM_USAGE_QUOTA_INVALID")
	}
	res := tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "item_name"},
			{Name: "period_key"},
		},
		TargetWhere: clause.Where{Exprs: []clause.Expression{
			clause.Eq{Column: clause.Column{Name: "deleted_at"}, Value: nil},
		}},
		DoNothing: true,
	}).Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	*quota = entry
	return nil
}

func calculateCycleSignReward(dayInCycle int) (int, string) {
	switch dayInCycle {
	case 3:
		return 1, "连续签到3天奖励"
	case 7:
		return 2, "连续签到7天奖励"
	case 14:
		return randomIntRange(3, 5), "连续签到14天奖励"
	case 21:
		return randomIntRange(5, 7), "连续签到21天奖励"
	case 30:
		return randomIntRange(8, 15), "连续签到30天奖励"
	default:
		return 0, ""
	}
}

func signInDayInCycle(streakDays int) int {
	if streakDays <= 0 {
		return 1
	}
	return ((streakDays - 1) % 30) + 1
}

func createSignInLogInTx(tx *gorm.DB, logEntry *SignInLog) error {
	if tx == nil || logEntry == nil {
		return fmt.Errorf("SIGN_IN_LOG_INVALID")
	}
	res := tx.Create(logEntry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SIGN_IN_LOG_CREATE_MISSED")
	}
	return nil
}

func createSignInRewardClaimInTx(tx *gorm.DB, claim *SignInRewardClaim) error {
	if tx == nil || claim == nil {
		return fmt.Errorf("SIGN_IN_REWARD_CLAIM_INVALID")
	}
	entry := *claim
	entry.Description = formatPlainValue(entry.Description)
	entry.RefID = formatPlainValue(entry.RefID)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SIGN_IN_REWARD_CLAIM_CREATE_MISSED")
	}
	return nil
}

func createSignInStreakInTx(tx *gorm.DB, streak *SignInStreak) error {
	if tx == nil || streak == nil {
		return fmt.Errorf("SIGN_IN_STREAK_INVALID")
	}
	entry := *streak
	res := tx.Create(&entry)
	if res.Error != nil {
		if isUniqueConstraintError(res.Error) {
			return errConcurrentSignInRetry
		}
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SIGN_IN_STREAK_CREATE_MISSED")
	}
	*streak = entry
	return nil
}

type signInResult struct {
	UserName string

	BaseBonus        int
	StreakBonus      int
	StreakRewardDesc string

	CurrentStreakDays int
	CycleSeq          int
	DayInCycle        int

	BalanceBeforeBase   int
	BalanceAfterBase    int
	BalanceAfterAll     int
	StreakRewardRefID   string
	StreakRewardGranted bool

	WasBroken bool
}

// handleUserSignIn 执行新版签到逻辑：
// 1. 不按月份统计；
// 2. 只按连续签到天数计算；
// 3. 30 天一轮，3/7/14/21/30 天发奖励；
// 4. 断签后下一次签到从 1 重新计算；
// 5. 使用 sign_in_logs 防重复签到，sign_in_reward_claims 防重复奖励。
func handleUserSignIn(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.From == nil {
		return
	}

	userID := msg.From.ID
	chatID := msg.Chat.ID

	now := time.Now()
	todayKey := signInDateKey(now)
	yesterdayKey := signInDateKey(now.AddDate(0, 0, -1))

	baseBonus := randomIntRange(5, 10)

	var result signInResult

	err := DB.Transaction(func(tx *gorm.DB) error {
		_, displayName, walletErr := ensureUserWalletInTx(tx, msg.From)
		if walletErr != nil {
			return walletErr
		}

		result.UserName = displayName
		if strings.TrimSpace(result.UserName) == "" {
			result.UserName = fmt.Sprintf("%d", userID)
		}

		// 先用签到日志唯一索引兜底防止并发重复签到。
		// 这里先不 Create，等计算出 streak 后再写完整日志。
		var existingLog SignInLog
		if err := tx.Where("user_id = ? AND sign_date = ?", userID, todayKey).First(&existingLog).Error; err == nil {
			return errAlreadySigned
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		var streak SignInStreak
		err := tx.Where("user_id = ?", userID).First(&streak).Error
		if err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				streak = SignInStreak{
					UserID:            userID,
					CurrentStreakDays: 0,
					LongestStreakDays: 0,
					TotalSignDays:     0,
					LastSignDate:      "",
					CycleSeq:          1,
					BreakCount:        0,
				}
				if err := createSignInStreakInTx(tx, &streak); err != nil {
					return err
				}
			} else {
				return err
			}
		}

		oldStoredCycleSeq := streak.CycleSeq
		normalizedOldCycleSeq := oldStoredCycleSeq
		if normalizedOldCycleSeq <= 0 {
			normalizedOldCycleSeq = 1
		}
		oldLastSignDate := streak.LastSignDate
		oldCurrentStreakDays := streak.CurrentStreakDays
		oldTotalSignDays := streak.TotalSignDays
		oldBreakCount := streak.BreakCount

		newStreakDays := 1
		newCycleSeq := normalizedOldCycleSeq
		newBreakCount := oldBreakCount
		wasBroken := false

		switch {
		case streak.LastSignDate == "":
			newStreakDays = 1

		case streak.LastSignDate == todayKey:
			return errAlreadySigned

		case streak.LastSignDate == yesterdayKey:
			newStreakDays = streak.CurrentStreakDays + 1
			if newStreakDays <= 0 {
				newStreakDays = 1
			}

			// 连续签到跨过 30 天周期边界时，进入下一轮。
			// 例如第 31 天为新一轮第 1 天，第 33 天触发新一轮 3 天奖励。
			if newStreakDays > 1 && (newStreakDays-1)%30 == 0 {
				newCycleSeq++
			}

		case streak.LastSignDate < yesterdayKey:
			newStreakDays = 1
			newCycleSeq++
			wasBroken = true
			newBreakCount++

		default:
			// last_sign_date 大于 today，说明系统时间或数据异常，禁止继续发奖。
			return errSignDateInFuture
		}

		if newCycleSeq <= 0 {
			newCycleSeq = 1
		}

		dayInCycle := signInDayInCycle(newStreakDays)

		streak.CurrentStreakDays = newStreakDays
		if newStreakDays > streak.LongestStreakDays {
			streak.LongestStreakDays = newStreakDays
		}
		streak.TotalSignDays = oldTotalSignDays + 1
		streak.LastSignDate = todayKey
		streak.LastSignAt = &now
		streak.CycleSeq = newCycleSeq
		streak.BreakCount = newBreakCount

		streakRes := tx.Model(&SignInStreak{}).
			Where("id = ? AND user_id = ? AND last_sign_date = ? AND current_streak_days = ? AND total_sign_days = ? AND cycle_seq = ? AND break_count = ?",
				streak.ID, userID, oldLastSignDate, oldCurrentStreakDays, oldTotalSignDays, oldStoredCycleSeq, oldBreakCount).
			Updates(map[string]interface{}{
				"current_streak_days": streak.CurrentStreakDays,
				"longest_streak_days": streak.LongestStreakDays,
				"total_sign_days":     streak.TotalSignDays,
				"last_sign_date":      streak.LastSignDate,
				"last_sign_at":        streak.LastSignAt,
				"cycle_seq":           streak.CycleSeq,
				"break_count":         streak.BreakCount,
			})
		if streakRes.Error != nil {
			return streakRes.Error
		}
		if streakRes.RowsAffected == 0 {
			return errConcurrentSignInRetry
		}

		// 写签到日志。唯一索引 user_id + sign_date 是最终防线。
		if err := createSignInLogInTx(tx, &SignInLog{
			UserID:          userID,
			SignDate:        todayKey,
			SignAt:          now,
			BasePoints:      baseBonus,
			StreakDaysAfter: newStreakDays,
			CycleSeq:        newCycleSeq,
			DayInCycle:      dayInCycle,
		}); err != nil {
			if isUniqueConstraintError(err) {
				return errAlreadySigned
			}
			return err
		}

		streakBonus, streakDesc := calculateCycleSignReward(dayInCycle)

		rewardGranted := false
		rewardRefID := ""

		if streakBonus > 0 {
			rewardRefID = fmt.Sprintf("sign_cycle:%d:%d:%d", userID, newCycleSeq, dayInCycle)

			claim := SignInRewardClaim{
				UserID:        userID,
				RewardType:    "cycle_streak",
				CycleSeq:      newCycleSeq,
				MilestoneDays: dayInCycle,
				Points:        streakBonus,
				Description:   streakDesc,
				RefID:         rewardRefID,
				ClaimedAt:     now,
			}

			if err := createSignInRewardClaimInTx(tx, &claim); err != nil {
				if isUniqueConstraintError(err) {
					// 理论上今天只能签到一次，不应走到这里。
					// 若并发或历史脏数据触发，则跳过奖励，避免重复发放。
					streakBonus = 0
					streakDesc = ""
				} else {
					return err
				}
			} else {
				rewardGranted = true
			}
		}

		if err := applyPointDeltaInTx(
			tx,
			userID,
			baseBonus,
			"sign_in",
			fmt.Sprintf("每日签到获得 %d 积分", baseBonus),
			"sign_in",
			todayKey,
		); err != nil {
			return err
		}

		var afterBaseUser User
		if err := tx.Select("telegram_id", "username", "points").
			Where("telegram_id = ?", userID).
			First(&afterBaseUser).Error; err != nil {
			return err
		}

		result.BalanceBeforeBase = afterBaseUser.Points - baseBonus
		result.BalanceAfterBase = afterBaseUser.Points
		result.BalanceAfterAll = afterBaseUser.Points

		if streakBonus > 0 && rewardGranted {
			if err := applyPointDeltaInTx(
				tx,
				userID,
				streakBonus,
				"sign_streak_bonus",
				fmt.Sprintf("%s，额外获得 %d 积分", streakDesc, streakBonus),
				"sign_cycle",
				rewardRefID,
			); err != nil {
				return err
			}

			var afterAllUser User
			if err := tx.Select("telegram_id", "username", "points").
				Where("telegram_id = ?", userID).
				First(&afterAllUser).Error; err != nil {
				return err
			}

			result.BalanceAfterAll = afterAllUser.Points
		}

		userSignRes := tx.Model(&User{}).
			Where("telegram_id = ?", userID).
			Update("last_sign_at", &now)
		if userSignRes.Error != nil {
			return userSignRes.Error
		}
		if userSignRes.RowsAffected == 0 {
			return fmt.Errorf("SIGN_USER_LAST_SIGN_UPDATE_MISSED")
		}

		result.BaseBonus = baseBonus
		result.StreakBonus = streakBonus
		result.StreakRewardDesc = streakDesc
		result.CurrentStreakDays = newStreakDays
		result.CycleSeq = newCycleSeq
		result.DayInCycle = dayInCycle
		result.StreakRewardRefID = rewardRefID
		result.StreakRewardGranted = rewardGranted
		result.WasBroken = wasBroken

		return nil
	})

	if err != nil {
		switch signInErrorCode(err) {
		case "ALREADY_SIGNED":
			replyText(bot, chatID, "⚠️ 您今天已经打过卡啦，明天再来吧！")
		case "SIGN_DATE_IN_FUTURE":
			log.Printf("⚠️ 签到状态异常，last_sign_date 超前: user=%d today=%s", userID, formatPlainValue(todayKey))
			replyText(bot, chatID, "❌ 签到状态异常，请联系管理员处理。")
		case "CONCURRENT_SIGN_IN_RETRY":
			replyText(bot, chatID, "⚠️ 签到请求过于频繁，请稍后再试。")
		default:
			log.Printf("❌ 签到失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 签到失败，请稍后重试。")
		}
		return
	}

	awardSectContribution(userID, 1, "每日签到", "sign_in", todayKey)
	var u User
	if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		log.Printf("⚠️ 签到成功后本地档案读取失败: user=%d err=%s", userID, formatPlainError(err))
	} else if u.AbsUserID != "" {
		fetchReportAndCheckUpgrade(bot, userID, u.AbsUserID)
		checkAndCompensateLegacyUser(bot, userID)
	}

	cul := GetOrCreateCultivation(userID)
	realmStr := GetRealmName(cul)
	if cul == nil {
		replyText(bot, chatID, "⚠️ 签到成功，但修仙档案暂时读取失败，请稍后查看我的信息。")
		return
	}
	safeName := result.UserName
	if safeName == "" {
		safeName = "神秘道友"
	}

	streakText := fmt.Sprintf(
		"\n🔥 连续签到：`%d` 天\n🔄 当前周期：第 `%d` 轮，第 `%d/30` 天\n",
		result.CurrentStreakDays,
		result.CycleSeq,
		result.DayInCycle,
	)

	if result.WasBroken {
		streakText += "⚠️ 昨日未签到，本轮连签已重新开始。\n"
	}

	if result.StreakBonus > 0 && result.StreakRewardGranted {
		streakText += fmt.Sprintf("🎊 连签奖励：`%d` 积分\n", result.StreakBonus)
	}

	reply := fmt.Sprintf("🎉 **签到成功！**\n\n"+
		"👤 道友：`%s`\n"+
		"📿 境界：%s\n"+
		"⏱ 闭关：`%.1f` 小时\n"+
		"🎁 获得灵石：`%d` 积分%s"+
		"🪪 当前余额：`%d` 积分\n\n"+
		"*(💡 连续签到奖励：3天+1，7天+2，14天随机3~5，21天随机5~7，30天随机8~15；30天一轮，断签重置)*\n"+
		"*(💡 发送 `修仙榜` 查看全服排名)*",
		safeName,
		realmStr,
		cul.TotalAudioTime+cul.PillAudioTime,
		result.BaseBonus,
		streakText,
		result.BalanceAfterAll,
	)

	replyText(bot, chatID, reply)
}
