package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	dailyOperationsStartHour     = 3
	dailyFusionPoolCollectHour   = 12
	sectWeeklyTaskAutoSettleHour = 9

	autoBackupLastSuccessDateKey = "auto_backup_last_success_date"
	autoBackupLastSuccessAtKey   = "auto_backup_last_success_at"
	autoBackupLastMessageIDKey   = "auto_backup_last_message_id"
	autoBackupLastAttemptAtKey   = "auto_backup_last_attempt_at"
	autoBackupLastErrorKey       = "auto_backup_last_error"
	autoBackupRetryCountKey      = "auto_backup_retry_count"
	backupLastPinnedMessageIDKey = "backup_last_pinned_message_id"
	backupLastPinErrorKey        = "backup_last_pin_error"

	dailyLifecycleLastSuccessDateKey    = "daily_lifecycle_last_success_date"
	dailyLifecycleLastErrorKey          = "daily_lifecycle_last_error"
	dailyListeningRefreshLastAtKey      = "daily_listening_refresh_last_at"
	dailyListeningRefreshLastSuccessKey = "daily_listening_refresh_last_success"
	dailyListeningRefreshLastTotalKey   = "daily_listening_refresh_last_total"
	dailyListeningRefreshLastSkippedKey = "daily_listening_refresh_last_skipped"
	dailyListeningRefreshLastErrorKey   = "daily_listening_refresh_last_error"
	dailyFusionPoolLastSuccessDateKey   = "daily_fusion_pool_last_success_date"
	dailyFusionPoolLastSuccessAtKey     = "daily_fusion_pool_last_success_at"
	dailyFusionPoolLastAmountKey        = "daily_fusion_pool_last_amount"
	dailyFusionPoolLastErrorKey         = "daily_fusion_pool_last_error"
	sectWeeklyTaskAutoSettleLastWeekKey = "sect_weekly_task_auto_settle_last_week"

	autoBackupMaxRetryCount           = 5
	dailyListeningRefreshInterval     = 30 * time.Minute
	dailyListeningRefreshFreshTTL     = 5 * time.Minute
	dailyListeningRefreshRequestDelay = 80 * time.Millisecond
)

var autoBackupMu sync.Mutex
var dailyLifecycleMu sync.Mutex
var dailyListeningRefreshMu sync.Mutex
var dailyFusionPoolMu sync.Mutex
var sectWeeklyTaskAutoSettleMu sync.Mutex
var dailyOperationsLocation = time.FixedZone("CST", 8*3600)
var dailyFusionPoolLocation = dailyOperationsLocation

func startBackgroundJobs(bot *tgbotapi.BotAPI) {
	go func() {
		for {
			runBackgroundSchedulerTickSafely(bot)
			time.Sleep(1 * time.Minute)
		}
	}()

	log.Println("✅ 后台任务调度器已启动：每日 03:00 后自动巡检、补跑备份与生命周期任务。")
}

func runBackgroundSchedulerTickSafely(bot *tgbotapi.BotAPI) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("⚠️ 后台任务调度器发生 panic，已恢复: panic=%s", formatPlainValue(r))
			notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 后台任务调度器发生异常，系统已自动恢复。\n\npanic: %s", formatPlainValue(r)))
		}
	}()

	if bot == nil || DB == nil || AppConfig == nil {
		return
	}

	now := time.Now()
	if !isDailyOperationsWindowOpen(now) {
		return
	}

	runDailyLifecycleIfNeeded(bot, now)
	runDailyListeningRefreshIfNeeded(bot, now)
	runListeningAbuseMonitorIfNeeded(bot, now)
	runSectWeeklyTaskAutoSettlementIfNeeded(bot, now)
	runDailyFusionPoolCollectIfNeeded(bot, now)
	runAutoBackupIfNeeded(bot, now)
}

func runDailyFusionPoolCollectIfNeeded(bot *tgbotapi.BotAPI, now time.Time) {
	now, todayKey, due := dailyFusionPoolSchedule(now)
	if !due {
		return
	}

	lastSuccessDate, err := getSystemConfigStringChecked(dailyFusionPoolLastSuccessDateKey)
	if err != nil {
		recordDailyFusionPoolStateReadFailure(bot, todayKey, dailyFusionPoolLastSuccessDateKey, err)
		return
	}
	if lastSuccessDate == todayKey {
		return
	}

	if !dailyFusionPoolMu.TryLock() {
		return
	}
	defer dailyFusionPoolMu.Unlock()

	lastSuccessDate, err = getSystemConfigStringChecked(dailyFusionPoolLastSuccessDateKey)
	if err != nil {
		recordDailyFusionPoolStateReadFailure(bot, todayKey, dailyFusionPoolLastSuccessDateKey, err)
		return
	}
	if lastSuccessDate == todayKey {
		return
	}

	amount, err := randomDailyFusionPoolAmount()
	if err != nil {
		setSystemConfigError(dailyFusionPoolLastErrorKey, err)
		log.Printf("⚠️ 每日天道灵气随机数生成失败: date=%s err=%s", formatPlainValue(todayKey), formatPlainError(err))
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 每日天道灵气收集失败\n\n日期: %s\n错误: %s", formatPlainValue(todayKey), formatPlainError(err)))
		return
	}

	currentPool := 0
	isBurst := false
	didInject := false
	err = runFusionPoolLockedTransaction(func(tx *gorm.DB) error {
		claimed, err := claimDailyFusionPoolCollectionInTx(tx, todayKey)
		if err != nil {
			return err
		}
		if !claimed {
			return nil
		}

		currentPool, isBurst, err = addPointsToFusionPoolInTx(tx, amount)
		if err != nil {
			return err
		}
		didInject = true

		nowText := time.Now().Format(time.RFC3339)
		if err := upsertSystemConfigValueInTx(tx, dailyFusionPoolLastSuccessAtKey, nowText); err != nil {
			return err
		}
		if err := upsertSystemConfigValueInTx(tx, dailyFusionPoolLastAmountKey, strconv.Itoa(amount)); err != nil {
			return err
		}
		return upsertSystemConfigValueInTx(tx, dailyFusionPoolLastErrorKey, "")
	})
	if err != nil {
		setSystemConfigError(dailyFusionPoolLastErrorKey, err)
		log.Printf("⚠️ 每日天道灵气注入失败: date=%s amount=%d err=%s", formatPlainValue(todayKey), amount, formatPlainError(err))
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 每日天道灵气注入失败\n\n日期: %s\n灵气: %d\n错误: %s", formatPlainValue(todayKey), amount, formatPlainError(err)))
		return
	}

	if !didInject {
		return
	}

	log.Printf("✅ 每日天道灵气已注入奖池: date=%s amount=%d pool=%d/300", formatPlainValue(todayKey), amount, currentPool)
	notifyDailyFusionPoolInjection(bot, todayKey, amount, currentPool, isBurst)
	if isBurst {
		notifyFusionPoolBurst(bot, AppConfig.NoticeGroupID, "每日天道灵气自然汇聚")
	}
}

func recordDailyFusionPoolStateReadFailure(bot *tgbotapi.BotAPI, todayKey string, key string, err error) {
	setSystemConfigError(dailyFusionPoolLastErrorKey, err)
	log.Printf("每日天道灵气收集状态读取失败，已跳过本轮注入: date=%s key=%s err=%s", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("每日天道灵气收集状态读取失败，已跳过本轮注入。\n\n日期：%s\n配置：%s\n错误：%s", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err)))
}

func notifyDailyFusionPoolInjection(bot *tgbotapi.BotAPI, dateKey string, amount int, currentPool int, isBurst bool) {
	if bot == nil || AppConfig == nil || AppConfig.NoticeGroupID == 0 {
		return
	}

	progressText := fmt.Sprintf("当前水位：`%d/300`。", currentPool)
	if isBurst {
		progressText = fmt.Sprintf("本次注入后奖池蓄满，已降下天道机缘红包；新水位：`%d/300`。", currentPool)
	}

	notice := fmt.Sprintf(
		"🌊 **【天地灵气·自然汇聚】** 🌊\n\n"+
			"%s 天道巡游收集到 `%d` 分天地灵气，已注入全服【天道奖池】。\n"+
			"%s",
		dateKey,
		amount,
		progressText,
	)

	msg := tgbotapi.NewMessage(AppConfig.NoticeGroupID, notice)
	msg.ParseMode = "Markdown"
	if !enqueueAutoDelete(bot, msg, "daily_fusion_pool_notice", telegramAsyncPriorityNormal, "daily_fusion_pool_notice:"+dateKey) {
		log.Printf("⚠️ 每日天道灵气群提醒入队失败: chat=%d date=%s", AppConfig.NoticeGroupID, formatPlainValue(dateKey))
	}
}

func claimDailyFusionPoolCollectionInTx(tx *gorm.DB, todayKey string) (bool, error) {
	if tx == nil || strings.TrimSpace(todayKey) == "" {
		return false, fmt.Errorf("DAILY_FUSION_POOL_CLAIM_INVALID")
	}

	cfg := SystemConfig{Key: dailyFusionPoolLastSuccessDateKey, Value: todayKey}
	createRes := tx.Clauses(systemConfigKeyDoNothingClause()).Create(&cfg)
	if createRes.Error != nil {
		return false, createRes.Error
	}
	if createRes.RowsAffected > 0 {
		return true, nil
	}

	res := tx.Model(&SystemConfig{}).
		Where("key = ? AND value <> ?", dailyFusionPoolLastSuccessDateKey, todayKey).
		Update("value", todayKey)
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected > 0, nil
}

func systemConfigKeyConflictTarget() clause.Where {
	return clause.Where{Exprs: []clause.Expression{
		clause.Eq{Column: clause.Column{Name: "deleted_at"}, Value: nil},
	}}
}

func systemConfigKeyDoNothingClause() clause.OnConflict {
	return clause.OnConflict{
		Columns:     []clause.Column{{Name: "key"}},
		TargetWhere: systemConfigKeyConflictTarget(),
		DoNothing:   true,
	}
}

func upsertSystemConfigValueInTx(tx *gorm.DB, key string, value string) error {
	if tx == nil || strings.TrimSpace(key) == "" {
		return fmt.Errorf("SYSTEM_CONFIG_UPSERT_INVALID")
	}

	res := tx.Clauses(clause.OnConflict{
		Columns:     []clause.Column{{Name: "key"}},
		TargetWhere: systemConfigKeyConflictTarget(),
		DoUpdates: clause.Assignments(map[string]interface{}{
			"value":      value,
			"updated_at": time.Now(),
		}),
	}).Create(&SystemConfig{
		Key:   key,
		Value: value,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SYSTEM_CONFIG_UPSERT_MISSED")
	}
	return nil
}

func randomDailyFusionPoolAmount() (int, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(6))
	if err != nil {
		return 0, err
	}
	return int(n.Int64()) + 5, nil
}

func dailyFusionPoolSchedule(now time.Time) (time.Time, string, bool) {
	localNow := now.In(dailyFusionPoolLocation)
	return localNow, localNow.Format("2006-01-02"), localNow.Hour() >= dailyFusionPoolCollectHour
}

func dailyOperationsLocalTime(now time.Time) time.Time {
	return now.In(dailyOperationsLocation)
}

func dailyOperationsDateKey(now time.Time) string {
	return dailyOperationsLocalTime(now).Format("2006-01-02")
}

func isDailyOperationsWindowOpen(now time.Time) bool {
	return dailyOperationsLocalTime(now).Hour() >= dailyOperationsStartHour
}

func runDailyListeningRefreshIfNeeded(bot *tgbotapi.BotAPI, now time.Time) {
	lastAt, err := getSystemConfigTimeChecked(dailyListeningRefreshLastAtKey)
	if err != nil {
		recordDailyListeningRefreshStateReadFailure(bot, dailyListeningRefreshLastAtKey, err)
		return
	}
	if !lastAt.IsZero() && now.Sub(lastAt) < dailyListeningRefreshInterval {
		return
	}

	if !dailyListeningRefreshMu.TryLock() {
		return
	}
	defer dailyListeningRefreshMu.Unlock()

	lastAt, err = getSystemConfigTimeChecked(dailyListeningRefreshLastAtKey)
	if err != nil {
		recordDailyListeningRefreshStateReadFailure(bot, dailyListeningRefreshLastAtKey, err)
		return
	}
	if !lastAt.IsZero() && now.Sub(lastAt) < dailyListeningRefreshInterval {
		return
	}

	success, total, skipped := refreshAllDailyListeningStatsWithOptions(false)
	if err := setSystemConfigStringChecked(dailyListeningRefreshLastAtKey, time.Now().Format(time.RFC3339)); err != nil {
		recordDailyListeningRefreshStateWriteFailure(bot, dailyListeningRefreshLastAtKey, err)
		return
	}
	if err := setSystemConfigStringChecked(dailyListeningRefreshLastSuccessKey, strconv.Itoa(success)); err != nil {
		recordDailyListeningRefreshStateWriteFailure(bot, dailyListeningRefreshLastSuccessKey, err)
		return
	}
	if err := setSystemConfigStringChecked(dailyListeningRefreshLastTotalKey, strconv.Itoa(total)); err != nil {
		recordDailyListeningRefreshStateWriteFailure(bot, dailyListeningRefreshLastTotalKey, err)
		return
	}
	if err := setSystemConfigStringChecked(dailyListeningRefreshLastSkippedKey, strconv.Itoa(skipped)); err != nil {
		recordDailyListeningRefreshStateWriteFailure(bot, dailyListeningRefreshLastSkippedKey, err)
		return
	}
	if err := setSystemConfigStringChecked(dailyListeningRefreshLastErrorKey, ""); err != nil {
		recordDailyListeningRefreshStateWriteFailure(bot, dailyListeningRefreshLastErrorKey, err)
		return
	}
	log.Printf("✅ 后台每日净修为刷新完成: success=%d total=%d skipped=%d", success, total, skipped)
}

func recordDailyListeningRefreshStateReadFailure(bot *tgbotapi.BotAPI, key string, err error) {
	setSystemConfigError(dailyListeningRefreshLastErrorKey, err)
	log.Printf("每日听书缓存刷新状态读取失败，已跳过本轮刷新: key=%s err=%s", formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("每日听书缓存刷新状态读取失败，已跳过本轮刷新。\n\n配置：%s\n错误：%s", formatPlainValue(key), formatPlainError(err)))
}

func recordDailyListeningRefreshStateWriteFailure(bot *tgbotapi.BotAPI, key string, err error) {
	if key != dailyListeningRefreshLastErrorKey {
		setSystemConfigError(dailyListeningRefreshLastErrorKey, err)
	}
	log.Printf("每日听书缓存刷新状态写入失败: key=%s err=%s", formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("每日听书缓存刷新已执行完成，但状态写入失败，请人工核查，避免重复批量刷新 ABS。\n\n配置：%s\n错误：%s", formatPlainValue(key), formatPlainError(err)))
}

func refreshAllDailyListeningStats() (int, int) {
	success, total, _ := refreshAllDailyListeningStatsWithOptions(true)
	return success, total
}

func refreshAllDailyListeningStatsWithOptions(force bool) (int, int, int) {
	var users []User
	if err := DB.Select("telegram_id", "abs_user_id").Where("abs_user_id <> ''").Find(&users).Error; err != nil {
		log.Printf("⚠️ 查询每日净修为刷新用户失败: err=%s", formatPlainError(err))
		setSystemConfigError(dailyListeningRefreshLastErrorKey, err)
		return 0, 0, 0
	}

	success := 0
	skipped := 0
	now := time.Now()
	for _, u := range users {
		if !force {
			if stat, ok := getTodayDailyListeningStat(u.TelegramID, now); ok && shouldSkipDailyListeningRefresh(stat, now, force) {
				skipped++
				continue
			}
		}
		if _, ok := refreshDailyListeningStatsFromABS(u.TelegramID, u.AbsUserID); ok {
			if _, syncOK := syncCultivationFromDailyListeningStats(u.TelegramID); !syncOK {
				setSystemConfigError(dailyListeningRefreshLastErrorKey, errDailyListeningStatsReadFailed)
				continue
			}
			success++
		}
		time.Sleep(dailyListeningRefreshRequestDelay)
	}

	return success, len(users), skipped
}

func shouldSkipDailyListeningRefresh(stat DailyListeningStat, now time.Time, force bool) bool {
	if force || stat.LastFetchedAt.IsZero() {
		return false
	}
	age := now.Sub(stat.LastFetchedAt)
	return age >= 0 && age < dailyListeningRefreshFreshTTL
}

func runSectWeeklyTaskAutoSettlementIfNeeded(bot *tgbotapi.BotAPI, now time.Time) {
	targetWeek, due := sectWeeklyTaskAutoSettlementTargetWeek(now)
	if !due {
		return
	}

	targetWeekKey := targetWeek.Format("2006-01-02")
	lastSettledWeek, err := getSystemConfigStringChecked(sectWeeklyTaskAutoSettleLastWeekKey)
	if err != nil {
		recordSectWeeklyTaskAutoSettleStateReadFailure(bot, targetWeekKey, sectWeeklyTaskAutoSettleLastWeekKey, err)
		return
	}
	if strings.TrimSpace(lastSettledWeek) == targetWeekKey {
		return
	}

	if !sectWeeklyTaskAutoSettleMu.TryLock() {
		return
	}
	defer sectWeeklyTaskAutoSettleMu.Unlock()

	lastSettledWeek, err = getSystemConfigStringChecked(sectWeeklyTaskAutoSettleLastWeekKey)
	if err != nil {
		recordSectWeeklyTaskAutoSettleStateReadFailure(bot, targetWeekKey, sectWeeklyTaskAutoSettleLastWeekKey, err)
		return
	}
	if strings.TrimSpace(lastSettledWeek) == targetWeekKey {
		return
	}

	results, failed := autoSettleSectWeeklyTaskRewards(bot, targetWeek)
	if failed == 0 {
		if err := setSystemConfigStringChecked(sectWeeklyTaskAutoSettleLastWeekKey, targetWeekKey); err != nil {
			recordSectWeeklyTaskAutoSettleStateWriteFailure(bot, targetWeekKey, sectWeeklyTaskAutoSettleLastWeekKey, err)
			return
		}
	}
	if len(results) > 0 {
		log.Printf("✅ 宗门周目标自动结算完成: week=%s settled=%d failed=%d", formatPlainValue(targetWeekKey), len(results), failed)
	}
}

func recordSectWeeklyTaskAutoSettleStateReadFailure(bot *tgbotapi.BotAPI, targetWeekKey string, key string, err error) {
	log.Printf("⚠️ 宗门周目标自动结算状态读取失败，已跳过本轮结算: week=%s key=%s err=%s", formatPlainValue(targetWeekKey), formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 宗门周目标自动结算状态读取失败，已跳过本轮结算。\n\n周次：%s\n配置：%s\n错误：%s", formatPlainValue(targetWeekKey), formatPlainValue(key), formatPlainError(err)))
}

func recordSectWeeklyTaskAutoSettleStateWriteFailure(bot *tgbotapi.BotAPI, targetWeekKey string, key string, err error) {
	log.Printf("宗门周目标自动结算状态写入失败: week=%s key=%s err=%s", formatPlainValue(targetWeekKey), formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("宗门周目标自动结算已执行完成，但状态写入失败，请人工核查，避免重复扫描。\n\n周次：%s\n配置：%s\n错误：%s", formatPlainValue(targetWeekKey), formatPlainValue(key), formatPlainError(err)))
}

func sectWeeklyTaskAutoSettlementTargetWeek(now time.Time) (time.Time, bool) {
	localNow := dailyOperationsLocalTime(now)
	if localNow.Weekday() != time.Monday || localNow.Hour() < sectWeeklyTaskAutoSettleHour {
		return time.Time{}, false
	}
	return sectWeekStart(localNow.AddDate(0, 0, -7)), true
}

func autoSettleSectWeeklyTaskRewards(bot *tgbotapi.BotAPI, targetWeek time.Time) ([]sectWeeklyTaskSettlementResult, int) {
	var sects []Sect
	if err := DB.Select("id", "name").Find(&sects).Error; err != nil {
		log.Printf("⚠️ 查询宗门周目标自动结算列表失败: err=%s", formatPlainError(err))
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 宗门周目标自动结算失败：查询宗门列表失败。\n\n错误：%s", formatPlainError(err)))
		return nil, 1
	}

	results := make([]sectWeeklyTaskSettlementResult, 0, len(sects))
	failed := 0
	for _, sect := range sects {
		var result sectWeeklyTaskSettlementResult
		err := DB.Transaction(func(tx *gorm.DB) error {
			return settleSectWeeklyTaskRewardForSectTx(tx, int64(sect.ID), targetWeek, 0, "系统自动结算", &result)
		})
		if err != nil {
			switch sectErrorCode(err) {
			case "SECT_WEEKLY_TASK_NOT_ACHIEVED", "SECT_WEEKLY_TASK_ALREADY_SETTLED":
				continue
			default:
				failed++
				log.Printf("⚠️ 宗门周目标自动结算失败: sect=%d week=%s err=%s", sect.ID, formatPlainValue(sectWeekKey(targetWeek)), formatPlainError(err))
				continue
			}
		}
		results = append(results, result)
		notifySectWeeklyTaskSettlementLeaders(bot, result)
	}

	if failed > 0 {
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 宗门周目标自动结算部分失败\n\n周次：%s\n成功：%d\n失败：%d\n请查看日志并必要时手动补结算。", formatPlainValue(sectWeekKey(targetWeek)), len(results), failed))
	}
	return results, failed
}

func notifySectWeeklyTaskSettlementLeaders(bot *tgbotapi.BotAPI, result sectWeeklyTaskSettlementResult) {
	if bot == nil || result.SectID == 0 {
		return
	}

	var leaders []SectMember
	if err := DB.Select("user_id", "role").
		Where("sect_id = ? AND role IN ?", result.SectID, []string{sectRoleOwner, sectRoleElder}).
		Find(&leaders).Error; err != nil {
		log.Printf("⚠️ 查询宗门周目标通知对象失败: sect=%d err=%s", result.SectID, formatPlainError(err))
		return
	}

	text := formatSectWeeklyTaskSettlementNotice(result)
	sent := make(map[int64]bool, len(leaders))
	for _, leader := range leaders {
		if leader.UserID == 0 || sent[leader.UserID] {
			continue
		}
		sent[leader.UserID] = true
		msg := tgbotapi.NewMessage(leader.UserID, text)
		msg.ParseMode = "Markdown"
		if _, err := sendNoAutoDelete(bot, msg); err != nil {
			log.Printf("⚠️ 宗门周目标结算私信提醒失败: sect=%d user=%d err=%s", result.SectID, leader.UserID, formatTelegramSendError(err))
		}
	}
}

func formatSectWeeklyTaskSettlementNotice(result sectWeeklyTaskSettlementResult) string {
	return fmt.Sprintf(
		"📋 **宗门周目标已自动结算**\n\n"+
			"宗门：**%s**\n"+
			"周次：`%s`\n"+
			"达成目标：`%d/3`\n"+
			"全宗累计签到：`%d/%d`\n"+
			"全宗净修为增长：`%.1f/%d` 小时\n"+
			"全宗完成个人任务：`%d/%d`\n\n"+
			"基础奖励：资金 +`%d`，声望 +`%d`\n"+
			"超额合计：`%d%%`\n"+
			"超额奖励：资金 +`%d`，声望 +`%d`\n"+
			"最终发放：资金 +`%d`，声望 +`%d`",
		escapeMarkdown(result.SectName),
		result.Stats.WeekKey,
		result.Reward.AchievedCount,
		result.Stats.SignCount,
		sectWeeklySignTarget,
		result.Stats.ListenHours,
		sectWeeklyListenHourTarget,
		result.Stats.TaskClaimCount,
		sectWeeklyTaskTarget,
		result.Reward.BaseFunds,
		result.Reward.BasePrestige,
		result.Reward.ExcessPercentTotal,
		result.Reward.ExcessFunds,
		result.Reward.ExcessPrestige,
		result.Reward.Funds,
		result.Reward.Prestige,
	)
}

func runDailyLifecycleIfNeeded(bot *tgbotapi.BotAPI, now time.Time) {
	todayKey := dailyOperationsDateKey(now)

	lastSuccessDate, err := getSystemConfigStringChecked(dailyLifecycleLastSuccessDateKey)
	if err != nil {
		recordDailyLifecycleStateReadFailure(todayKey, dailyLifecycleLastSuccessDateKey, err)
		return
	}
	if strings.TrimSpace(lastSuccessDate) == todayKey {
		return
	}

	if !dailyLifecycleMu.TryLock() {
		return
	}
	defer dailyLifecycleMu.Unlock()

	lastSuccessDate, err = getSystemConfigStringChecked(dailyLifecycleLastSuccessDateKey)
	if err != nil {
		recordDailyLifecycleStateReadFailure(todayKey, dailyLifecycleLastSuccessDateKey, err)
		return
	}
	if strings.TrimSpace(lastSuccessDate) == todayKey {
		return
	}

	log.Println("🕵️ 开始执行每日用户生命周期巡检...")
	if err := runDailyLifecycleOperations(bot); err != nil {
		setSystemConfigError(dailyLifecycleLastErrorKey, err)
		log.Printf("⚠️ 每日用户生命周期巡检失败: date=%s err=%s", formatPlainValue(todayKey), formatPlainError(err))
		return
	}

	if err := setSystemConfigStringChecked(dailyLifecycleLastSuccessDateKey, todayKey); err != nil {
		recordDailyLifecycleStateWriteFailure(bot, todayKey, dailyLifecycleLastSuccessDateKey, err)
		return
	}
	if err := setSystemConfigStringChecked(dailyLifecycleLastErrorKey, ""); err != nil {
		recordDailyLifecycleStateWriteFailure(bot, todayKey, dailyLifecycleLastErrorKey, err)
		return
	}
	log.Printf("✅ 每日用户生命周期巡检完成: date=%s", formatPlainValue(todayKey))
}

func recordDailyLifecycleStateReadFailure(todayKey string, key string, err error) {
	setSystemConfigError(dailyLifecycleLastErrorKey, err)
	log.Printf("⚠️ 每日用户生命周期巡检状态读取失败，已跳过本轮巡检: date=%s key=%s err=%s", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err))
}

func recordDailyLifecycleStateWriteFailure(bot *tgbotapi.BotAPI, todayKey string, key string, err error) {
	if key != dailyLifecycleLastErrorKey {
		setSystemConfigError(dailyLifecycleLastErrorKey, err)
	}
	log.Printf("每日用户生命周期巡检状态写入失败: date=%s key=%s err=%s", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("每日用户生命周期巡检已执行完成，但状态写入失败，请人工核查，避免重复巡检。\n\n日期：%s\n配置：%s\n错误：%s", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err)))
}

func runAutoBackupIfNeeded(bot *tgbotapi.BotAPI, now time.Time) {
	if AppConfig.BackupGroupID == 0 {
		return
	}

	todayKey := dailyOperationsDateKey(now)

	lastSuccessDate, err := getSystemConfigStringChecked(autoBackupLastSuccessDateKey)
	if err != nil {
		recordAutoBackupStateReadFailure(todayKey, autoBackupLastSuccessDateKey, err)
		return
	}
	if strings.TrimSpace(lastSuccessDate) == todayKey {
		return
	}

	retryCount, err := getTodayAutoBackupRetryCountChecked(todayKey)
	if err != nil {
		recordAutoBackupStateReadFailure(todayKey, autoBackupRetryCountKey, err)
		return
	}
	if retryCount >= autoBackupMaxRetryCount {
		return
	}

	lastAttemptAt, err := getSystemConfigTimeChecked(autoBackupLastAttemptAtKey)
	if err != nil {
		recordAutoBackupStateReadFailure(todayKey, autoBackupLastAttemptAtKey, err)
		return
	}
	if !lastAttemptAt.IsZero() && dailyOperationsDateKey(lastAttemptAt) == todayKey {
		delay := autoBackupRetryDelay(retryCount)
		if time.Since(lastAttemptAt) < delay {
			return
		}
	}

	if !autoBackupMu.TryLock() {
		return
	}
	defer autoBackupMu.Unlock()

	lastSuccessDate, err = getSystemConfigStringChecked(autoBackupLastSuccessDateKey)
	if err != nil {
		recordAutoBackupStateReadFailure(todayKey, autoBackupLastSuccessDateKey, err)
		return
	}
	if strings.TrimSpace(lastSuccessDate) == todayKey {
		return
	}

	retryCount, err = getTodayAutoBackupRetryCountChecked(todayKey)
	if err != nil {
		recordAutoBackupStateReadFailure(todayKey, autoBackupRetryCountKey, err)
		return
	}
	if retryCount >= autoBackupMaxRetryCount {
		return
	}

	runAutoBackupAttempt(bot, now, retryCount)
}

func runAutoBackupAttempt(bot *tgbotapi.BotAPI, now time.Time, retryCount int) {
	todayKey := dailyOperationsDateKey(now)
	attemptNo := retryCount + 1

	if err := setSystemConfigStringChecked(autoBackupLastAttemptAtKey, now.Format(time.RFC3339)); err != nil {
		recordAutoBackupAttemptStateWriteFailure(bot, todayKey, autoBackupLastAttemptAtKey, err)
		return
	}

	log.Printf("📦 开始执行每日自动加密备份: date=%s attempt=%d/%d", formatPlainValue(todayKey), attemptNo, autoBackupMaxRetryCount)

	messageID, err := sendEncryptedBackupToTelegram(bot, "daily_auto")
	if err != nil {
		if writeErr := setSystemConfigStringChecked(autoBackupRetryCountKey, strconv.Itoa(attemptNo)); writeErr != nil {
			recordAutoBackupAttemptStateWriteFailure(bot, todayKey, autoBackupRetryCountKey, writeErr)
		}
		if writeErr := setSystemConfigStringChecked(autoBackupLastErrorKey, formatPlainError(err)); writeErr != nil {
			recordAutoBackupAttemptStateWriteFailure(bot, todayKey, autoBackupLastErrorKey, writeErr)
		}

		log.Printf("⚠️ 每日自动加密备份失败: date=%s attempt=%d/%d err=%s", formatPlainValue(todayKey), attemptNo, autoBackupMaxRetryCount, formatPlainError(err))
		notifyAutoBackupFailure(bot, todayKey, attemptNo, err)
		return
	}

	if err := setSystemConfigStringChecked(autoBackupLastSuccessDateKey, todayKey); err != nil {
		recordAutoBackupStateWriteFailure(bot, todayKey, autoBackupLastSuccessDateKey, err)
		return
	}
	if err := setSystemConfigStringChecked(autoBackupLastSuccessAtKey, time.Now().Format(time.RFC3339)); err != nil {
		recordAutoBackupStateWriteFailure(bot, todayKey, autoBackupLastSuccessAtKey, err)
		return
	}
	if err := setSystemConfigStringChecked(autoBackupLastMessageIDKey, strconv.Itoa(messageID)); err != nil {
		recordAutoBackupStateWriteFailure(bot, todayKey, autoBackupLastMessageIDKey, err)
		return
	}
	if err := setSystemConfigStringChecked(autoBackupRetryCountKey, "0"); err != nil {
		recordAutoBackupStateWriteFailure(bot, todayKey, autoBackupRetryCountKey, err)
		return
	}
	if err := setSystemConfigStringChecked(autoBackupLastErrorKey, ""); err != nil {
		recordAutoBackupStateWriteFailure(bot, todayKey, autoBackupLastErrorKey, err)
		return
	}

	log.Printf("✅ 每日自动加密备份成功: date=%s message_id=%d", formatPlainValue(todayKey), messageID)
}

func autoBackupRetryDelay(retryCount int) time.Duration {
	switch retryCount {
	case 0:
		return 0
	case 1:
		return 5 * time.Minute
	case 2:
		return 15 * time.Minute
	case 3:
		return 30 * time.Minute
	default:
		return 60 * time.Minute
	}
}

func getTodayAutoBackupRetryCountForStatus(todayKey string) (int, string, bool) {
	count, err := getTodayAutoBackupRetryCountChecked(todayKey)
	if err != nil {
		log.Printf("⚠️ 自动备份重试次数读取失败，状态暂不可用: date=%s err=%s", formatPlainValue(todayKey), formatPlainError(err))
		return 0, "读取失败", false
	}
	return count, strconv.Itoa(count), true
}

func getTodayAutoBackupRetryCountChecked(todayKey string) (int, error) {
	lastAttemptAt, err := getSystemConfigTimeChecked(autoBackupLastAttemptAtKey)
	if err != nil {
		return 0, err
	}
	if lastAttemptAt.IsZero() || dailyOperationsDateKey(lastAttemptAt) != todayKey {
		return 0, nil
	}

	raw, err := getSystemConfigStringChecked(autoBackupRetryCountKey)
	if err != nil {
		return 0, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, nil
	}

	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid auto backup retry count %s=%q: %w", autoBackupRetryCountKey, raw, err)
	}
	if n < 0 {
		return 0, fmt.Errorf("invalid auto backup retry count %s=%q", autoBackupRetryCountKey, raw)
	}

	return n, nil
}

func getSystemConfigStringForStatus(key, emptyText string) (string, bool) {
	value, err := getSystemConfigStringChecked(key)
	if err != nil {
		log.Printf("⚠️ 系统配置状态读取失败: key=%s err=%s", formatPlainValue(key), formatPlainError(err))
		return "读取失败", false
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return emptyText, true
	}
	return value, true
}

func formatSystemConfigTimeForStatus(key string) (string, bool) {
	t, err := getSystemConfigTimeChecked(key)
	if err != nil {
		log.Printf("⚠️ 系统配置时间状态读取失败: key=%s err=%s", formatPlainValue(key), formatPlainError(err))
		return "读取失败", false
	}
	if t.IsZero() {
		return "无", true
	}
	return dailyOperationsLocalTime(t).Format("2006-01-02 15:04:05"), true
}

func recordAutoBackupStateReadFailure(todayKey string, key string, err error) {
	setSystemConfigError(autoBackupLastErrorKey, err)
	log.Printf("⚠️ 自动备份状态读取失败，已跳过本轮备份: date=%s key=%s err=%s", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err))
}

func recordAutoBackupStateWriteFailure(bot *tgbotapi.BotAPI, todayKey string, key string, err error) {
	if key != autoBackupLastErrorKey {
		setSystemConfigError(autoBackupLastErrorKey, err)
	}
	log.Printf("自动备份状态写入失败: date=%s key=%s err=%s", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("每日自动加密备份已发送成功，但状态写入失败，请人工核查，避免重复外发备份。\n\n日期：%s\n配置：%s\n错误：%s", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err)))
}

func recordAutoBackupAttemptStateWriteFailure(bot *tgbotapi.BotAPI, todayKey string, key string, err error) {
	if key != autoBackupLastErrorKey {
		if writeErr := setSystemConfigStringChecked(autoBackupLastErrorKey, formatPlainError(err)); writeErr != nil {
			log.Printf("自动备份尝试状态写入失败，且最近错误写入也失败: date=%s key=%s err=%s last_error_write_err=%s", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err), formatPlainError(writeErr))
		}
	}
	log.Printf("自动备份尝试状态写入失败: date=%s key=%s err=%s", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("自动备份尝试状态写入失败，请人工核查，避免重复外发备份或重试次数失真。\n\n日期：%s\n配置：%s\n错误：%s", formatPlainValue(todayKey), formatPlainValue(key), formatPlainError(err)))
}

func notifyAutoBackupFailure(bot *tgbotapi.BotAPI, todayKey string, attemptNo int, err error) {
	statusText := "系统会继续按重试策略自动补跑。"
	if attemptNo >= autoBackupMaxRetryCount {
		statusText = "今日自动备份已达到最大重试次数，请尽快人工检查。"
	}

	notifySuperAdminsPlain(bot, fmt.Sprintf(
		"⚠️ 每日自动备份失败\n\n日期: %s\n重试: %d/%d\n错误: %s\n\n%s",
		formatPlainValue(todayKey),
		attemptNo,
		autoBackupMaxRetryCount,
		formatPlainError(err),
		statusText,
	))
}

func sendEncryptedBackupToTelegram(bot *tgbotapi.BotAPI, source string) (int, error) {
	if AppConfig == nil || AppConfig.BackupGroupID == 0 {
		return 0, fmt.Errorf("未配置 BACKUP_GROUP_ID，无法发送备份")
	}

	backupPath, cleanup, err := createEncryptedDBBackup()
	if err != nil {
		return 0, fmt.Errorf("创建加密数据库备份失败: %w", err)
	}
	defer cleanup()

	sourceText := "手动触发"
	switch source {
	case "daily_auto":
		sourceText = "每日自动"
	case "manual":
		sourceText = "手动触发"
	case "retry_auto":
		sourceText = "自动重试"
	case "startup_catchup":
		sourceText = "启动补跑"
	}

	now := dailyOperationsLocalTime(time.Now())
	doc := tgbotapi.NewDocument(AppConfig.BackupGroupID, tgbotapi.FilePath(backupPath))
	doc.Caption = fmt.Sprintf(
		"📦 **%s加密数据库备份**\n⏰ 时间: `%s`\n🔐 文件已使用 AES-GCM 加密。\n⚠️ 请妥善保管 BACKUP_ENCRYPT_KEY，丢失后无法解密。",
		sourceText,
		now.Format("2006-01-02 15:04:05"),
	)
	doc.ParseMode = "Markdown"

	sentMsg, err := sendNoAutoDelete(bot, doc)
	if err != nil {
		return 0, fmt.Errorf("发送 Telegram 备份文件失败: %w", err)
	}

	pinLatestBackupMessage(bot, sentMsg.MessageID)

	return sentMsg.MessageID, nil
}

func pinLatestBackupMessage(bot *tgbotapi.BotAPI, messageID int) {
	if bot == nil || AppConfig == nil || AppConfig.BackupGroupID == 0 || messageID == 0 {
		return
	}

	rawOldMsgID, err := getSystemConfigStringChecked(backupLastPinnedMessageIDKey)
	if err != nil {
		recordBackupPinStateReadFailure(bot, backupLastPinnedMessageIDKey, err)
		return
	}

	oldMsgID := 0
	rawOldMsgID = strings.TrimSpace(rawOldMsgID)
	if rawOldMsgID != "" {
		parsedOldMsgID, parseErr := strconv.Atoi(rawOldMsgID)
		if parseErr != nil {
			recordBackupPinStateReadFailure(bot, backupLastPinnedMessageIDKey, fmt.Errorf("invalid backup pinned message id %s: %w", formatPlainValue(rawOldMsgID), parseErr))
			return
		}
		oldMsgID = parsedOldMsgID
	}
	if oldMsgID > 0 && oldMsgID != messageID {
		unpinCfg := tgbotapi.UnpinChatMessageConfig{
			ChatID:    AppConfig.BackupGroupID,
			MessageID: oldMsgID,
		}
		if _, err := bot.Request(unpinCfg); err != nil && !isTerminalTelegramUnpinError(err) {
			setSystemConfigError(backupLastPinErrorKey, err)
			log.Printf("⚠️ 备份消息发送成功，但取消旧备份置顶失败: old_message_id=%d new_message_id=%d err=%s",
				oldMsgID,
				messageID,
				formatTelegramSendError(err),
			)
		}
	}

	pinCfg := tgbotapi.PinChatMessageConfig{
		ChatID:              AppConfig.BackupGroupID,
		MessageID:           messageID,
		DisableNotification: true,
	}

	if _, err := bot.Request(pinCfg); err != nil {
		setSystemConfigError(backupLastPinErrorKey, err)
		log.Printf("⚠️ 备份消息发送成功，但置顶失败。请检查 Bot 是否拥有置顶消息权限: message_id=%d err=%s", messageID, formatTelegramSendError(err))
		notifySuperAdminsPlain(bot, fmt.Sprintf(
			"⚠️ 备份文件已发送成功，但置顶失败。\n\nMessageID: %d\n错误: %s\n\n请检查 Bot 是否是备份群管理员，并拥有置顶消息权限。",
			messageID,
			formatPlainError(err),
		))
		return
	}

	if err := setSystemConfigStringChecked(backupLastPinnedMessageIDKey, strconv.Itoa(messageID)); err != nil {
		recordBackupPinStateWriteFailure(bot, backupLastPinnedMessageIDKey, err)
		return
	}
	if err := setSystemConfigStringChecked(backupLastPinErrorKey, ""); err != nil {
		recordBackupPinStateWriteFailure(bot, backupLastPinErrorKey, err)
		return
	}
}

func recordBackupPinStateReadFailure(bot *tgbotapi.BotAPI, key string, err error) {
	setSystemConfigError(backupLastPinErrorKey, err)
	log.Printf("备份置顶状态读取失败，已跳过本次置顶交接: key=%s err=%s", formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("备份文件已发送成功，但置顶状态读取失败，已跳过本次置顶交接。\n\n配置：%s\n错误：%s", formatPlainValue(key), formatPlainError(err)))
}

func recordBackupPinStateWriteFailure(bot *tgbotapi.BotAPI, key string, err error) {
	if key != backupLastPinErrorKey {
		setSystemConfigError(backupLastPinErrorKey, err)
	}
	log.Printf("备份置顶状态写入失败: key=%s err=%s", formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("备份消息置顶已执行，但置顶状态写入失败，请人工核查。\n\n配置：%s\n错误：%s", formatPlainValue(key), formatPlainError(err)))
}

func notifySuperAdminsPlain(bot *tgbotapi.BotAPI, text string) {
	if bot == nil || AppConfig == nil {
		return
	}

	for adminID := range AppConfig.AdminIDs {
		msg := tgbotapi.NewMessage(adminID, text)
		if !enqueueNoAutoDelete(bot, msg, "super_admin_notice", telegramAsyncPriorityHigh, "") {
			log.Printf("⚠️ 通知管理员入队失败: admin=%d", adminID)
		}
	}
}

func getSystemConfigString(key string) string {
	value, err := getSystemConfigStringChecked(key)
	if err != nil {
		log.Printf("⚠️ 读取系统配置失败: key=%s err=%s", formatPlainValue(key), formatPlainError(err))
		return ""
	}
	return value
}

func getSystemConfigStringChecked(key string) (string, error) {
	if DB == nil || strings.TrimSpace(key) == "" {
		return "", nil
	}

	var cfg SystemConfig
	if err := DB.Where("key = ?", key).First(&cfg).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return "", nil
		}
		return "", err
	}

	return cfg.Value, nil
}

func setSystemConfigString(key string, value string) {
	if err := setSystemConfigStringChecked(key, value); err != nil {
		log.Printf("⚠️ 写入系统配置失败: key=%s err=%s", formatPlainValue(key), formatPlainError(err))
	}
}

func setSystemConfigStringChecked(key string, value string) error {
	if DB == nil || strings.TrimSpace(key) == "" {
		return nil
	}

	res := DB.Clauses(clause.OnConflict{
		Columns:     []clause.Column{{Name: "key"}},
		TargetWhere: systemConfigKeyConflictTarget(),
		DoUpdates: clause.Assignments(map[string]interface{}{
			"value":      value,
			"updated_at": time.Now(),
		}),
	}).Create(&SystemConfig{
		Key:   key,
		Value: value,
	})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SYSTEM_CONFIG_UPSERT_MISSED")
	}
	return nil
}

func setSystemConfigError(key string, err error) {
	if err == nil {
		setSystemConfigString(key, "")
		return
	}
	setSystemConfigString(key, formatPlainError(err))
}

func getSystemConfigTimeChecked(key string) (time.Time, error) {
	raw, err := getSystemConfigStringChecked(key)
	if err != nil {
		return time.Time{}, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}

	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid system config time %s=%q: %w", key, raw, err)
	}

	return t, nil
}

func formatBackupStatusReport() string {
	now := time.Now()
	todayKey := dailyOperationsDateKey(now)

	backupGroupStatus := "未配置"
	if AppConfig != nil && AppConfig.BackupGroupID != 0 {
		backupGroupStatus = fmt.Sprintf("%d", AppConfig.BackupGroupID)
	}

	lastSuccessDate, lastSuccessDateAvailable := getSystemConfigStringForStatus(autoBackupLastSuccessDateKey, "从未成功")
	if lastSuccessDate == "" {
		lastSuccessDate = "从未成功"
	}

	lastSuccessAt, lastSuccessAtAvailable := formatSystemConfigTimeForStatus(autoBackupLastSuccessAtKey)
	lastAttemptAt, lastAttemptAtAvailable := formatSystemConfigTimeForStatus(autoBackupLastAttemptAtKey)
	lastMessageID, lastMessageIDAvailable := getSystemConfigStringForStatus(autoBackupLastMessageIDKey, "无")
	if lastMessageID == "" {
		lastMessageID = "无"
	}

	retryCount, retryCountText, retryCountAvailable := getTodayAutoBackupRetryCountForStatus(todayKey)
	lastError, lastErrorAvailable := getSystemConfigStringForStatus(autoBackupLastErrorKey, "无")
	if lastError == "" {
		lastError = "无"
	}

	pinnedMessageID, pinnedMessageIDAvailable := getSystemConfigStringForStatus(backupLastPinnedMessageIDKey, "无")
	if pinnedMessageID == "" {
		pinnedMessageID = "无"
	}
	pinError, pinErrorAvailable := getSystemConfigStringForStatus(backupLastPinErrorKey, "无")
	backupStateAvailable := lastSuccessDateAvailable && lastSuccessAtAvailable && lastAttemptAtAvailable &&
		lastMessageIDAvailable && retryCountAvailable && lastErrorAvailable && pinnedMessageIDAvailable && pinErrorAvailable
	if pinError == "" {
		pinError = "无"
	}

	status := "✅ 今日自动备份已成功"
	if AppConfig == nil || AppConfig.BackupGroupID == 0 {
		status = "⚠️ 未配置备份群组，自动备份不会发送"
	} else if !backupStateAvailable {
		status = "⚠️ 自动备份状态暂不可用"
	} else if lastSuccessDate != todayKey {
		if retryCount >= autoBackupMaxRetryCount {
			status = "❌ 今日自动备份已达到最大重试次数"
		} else if retryCount > 0 {
			status = "⚠️ 今日自动备份失败后等待重试"
		} else {
			status = "⏳ 今日自动备份尚未成功"
		}
	}

	return fmt.Sprintf(
		"📦 **数据库备份状态**\n\n"+
			"状态：%s\n"+
			"备份群组：`%s`\n"+
			"今日日期：`%s`\n"+
			"今日重试：`%s/%d`\n\n"+
			"最近成功日期：`%s`\n"+
			"最近成功时间：`%s`\n"+
			"最近尝试时间：`%s`\n"+
			"最近备份消息：`%s`\n"+
			"当前置顶消息：`%s`\n\n"+
			"最近备份错误：`%s`\n"+
			"最近置顶错误：`%s`",
		status,
		backupGroupStatus,
		todayKey,
		escapeMarkdown(retryCountText),
		autoBackupMaxRetryCount,
		escapeMarkdown(lastSuccessDate),
		escapeMarkdown(lastSuccessAt),
		escapeMarkdown(lastAttemptAt),
		escapeMarkdown(lastMessageID),
		escapeMarkdown(pinnedMessageID),
		formatSystemConfigErrorForMarkdown(lastError),
		formatSystemConfigErrorForMarkdown(pinError),
	)
}

func formatBackgroundStatusReport() string {
	now := time.Now()
	todayKey := dailyOperationsDateKey(now)

	lifecycleDate, lifecycleDateAvailable := getSystemConfigStringForStatus(dailyLifecycleLastSuccessDateKey, "从未完成")
	if lifecycleDate == "" {
		lifecycleDate = "从未完成"
	}
	lifecycleError, lifecycleErrorAvailable := getSystemConfigStringForStatus(dailyLifecycleLastErrorKey, "无")
	if lifecycleError == "" {
		lifecycleError = "无"
	}
	lifecycleStatus := "✅ 今日日常生命周期巡检已完成"
	lifecycleStateAvailable := lifecycleDateAvailable && lifecycleErrorAvailable
	if !lifecycleStateAvailable {
		lifecycleStatus = "⚠️ 生命周期巡检状态暂不可用"
	} else if lifecycleDate != todayKey {
		if !isDailyOperationsWindowOpen(now) {
			lifecycleStatus = "⏳ 今日调度窗口尚未开始"
		} else {
			lifecycleStatus = "⚠️ 今日日常生命周期巡检尚未完成"
		}
	}

	refreshAt, refreshAtAvailable := formatSystemConfigTimeForStatus(dailyListeningRefreshLastAtKey)
	refreshSuccess, refreshSuccessAvailable := getSystemConfigStringForStatus(dailyListeningRefreshLastSuccessKey, "0")
	if refreshSuccess == "" {
		refreshSuccess = "0"
	}
	refreshTotal, refreshTotalAvailable := getSystemConfigStringForStatus(dailyListeningRefreshLastTotalKey, "0")
	if refreshTotal == "" {
		refreshTotal = "0"
	}
	refreshSkipped, refreshSkippedAvailable := getSystemConfigStringForStatus(dailyListeningRefreshLastSkippedKey, "0")
	if refreshSkipped == "" {
		refreshSkipped = "0"
	}
	refreshError, refreshErrorAvailable := getSystemConfigStringForStatus(dailyListeningRefreshLastErrorKey, "无")
	if refreshError == "" {
		refreshError = "无"
	}

	refreshStateAvailable := refreshAtAvailable && refreshSuccessAvailable && refreshTotalAvailable && refreshSkippedAvailable && refreshErrorAvailable
	if !refreshStateAvailable {
		refreshAt = "状态暂不可用"
	}

	backupDate, backupDateAvailable := getSystemConfigStringForStatus(autoBackupLastSuccessDateKey, "从未成功")
	if backupDate == "" {
		backupDate = "从未成功"
	}
	backupRetryCount, backupRetryCountText, backupRetryCountAvailable := getTodayAutoBackupRetryCountForStatus(todayKey)
	backupError, backupErrorAvailable := getSystemConfigStringForStatus(autoBackupLastErrorKey, "无")
	if backupError == "" {
		backupError = "无"
	}

	backupStatus := "✅ 今日自动备份已成功"
	backupStateAvailable := backupDateAvailable && backupRetryCountAvailable && backupErrorAvailable
	if AppConfig == nil || AppConfig.BackupGroupID == 0 {
		backupStatus = "⚠️ 未配置备份群组"
	} else if !backupStateAvailable {
		backupStatus = "⚠️ 自动备份状态暂不可用"
	} else if backupDate != todayKey {
		if backupRetryCount >= autoBackupMaxRetryCount {
			backupStatus = "❌ 今日自动备份已达最大重试次数"
		} else if !isDailyOperationsWindowOpen(now) {
			backupStatus = "⏳ 今日调度窗口尚未开始"
		} else {
			backupStatus = "⚠️ 今日自动备份尚未成功"
		}
	}

	return fmt.Sprintf(
		"🛠 **后台任务状态**\n\n"+
			"当前日期：`%s`\n"+
			"调度窗口：每日 `%02d:00` 后，每分钟巡检一次\n\n"+
			"生命周期巡检：%s\n"+
			"最近完成日期：`%s`\n"+
			"生命周期错误：`%s`\n\n"+
			"每日听书刷新：`%s`\n"+
			"刷新结果：成功 `%s` / 总数 `%s` / 跳过 `%s`\n"+
			"刷新错误：`%s`\n\n"+
			"自动备份：%s\n"+
			"最近成功日期：`%s`\n"+
			"今日备份重试：`%s/%d`\n"+
			"备份错误：`%s`\n\n"+
			"%s",
		todayKey,
		dailyOperationsStartHour,
		lifecycleStatus,
		escapeMarkdown(lifecycleDate),
		formatSystemConfigErrorForMarkdown(lifecycleError),
		escapeMarkdown(refreshAt),
		escapeMarkdown(refreshSuccess),
		escapeMarkdown(refreshTotal),
		escapeMarkdown(refreshSkipped),
		formatSystemConfigErrorForMarkdown(refreshError),
		backupStatus,
		escapeMarkdown(backupDate),
		escapeMarkdown(backupRetryCountText),
		autoBackupMaxRetryCount,
		formatSystemConfigErrorForMarkdown(backupError),
		formatRuntimeMetricsReport(),
	)
}

func runDailyOperations(bot *tgbotapi.BotAPI) {
	if _, err := sendEncryptedBackupToTelegram(bot, "daily_auto"); err != nil {
		log.Printf("⚠️ 每日加密备份失败: %s", formatPlainError(err))
	}

	if err := runDailyLifecycleOperations(bot); err != nil {
		setSystemConfigError(dailyLifecycleLastErrorKey, err)
		log.Printf("⚠️ 每日用户生命周期巡检失败: err=%s", formatPlainError(err))
	}
}

func runDailyLifecycleOperations(bot *tgbotapi.BotAPI) error {
	now := time.Now()

	var users []User
	// 排除白名单和未挂载 ABS 账号的幽灵用户
	if err := DB.Where("is_whitelist = ? AND abs_user_id != ?", false, "").Find(&users).Error; err != nil {
		log.Printf("⚠️ 生命周期巡检读取用户列表失败: err=%s", formatPlainError(err))
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 生命周期巡检失败：读取用户列表失败。\n\n错误：%s", formatPlainError(err)))
		return err
	}

	for _, u := range users {
		if u.ExpireAt == nil {
			continue // 永久有效账号跳过
		}

		daysLeft := int(u.ExpireAt.Sub(now).Hours() / 24)

		// 阶段 1：到期前 3 天每日预警
		if daysLeft >= 0 && daysLeft < AppConfig.NotifyBeforeDays {
			msg := fmt.Sprintf("⚠️ **账号到期预警**\n\n您的听书账号将在 `%d` 天后到期。\n💡 请及时使用【🪙 积分兑换中心】获取续期卡，以免影响收听！", daysLeft+1)
			replyText(bot, u.TelegramID, msg)
			continue
		}

		// 阶段 2：刚过期 -> 立即封禁，进入宽限期
		if daysLeft < 0 && !u.IsSuspended {
			err := absClient.SetUserActiveStatus(u.AbsUserID, false)
			if err != nil {
				log.Printf("auto suspend ABS status update failed: user=%s tg=%d abs=%s err=%s",
					formatPlainValue(u.Username), u.TelegramID, formatPlainValue(u.AbsUserID), formatPlainError(err))
				continue
			}
			if err := applySuspendLocalStatusWithAudit(0, u.TelegramID, u.AbsUserID, true, "AUTO_SUSPEND_EXPIRED_USER",
				fmt.Sprintf("expired lifecycle suspend; expire_at=%s grace_days=%d", u.ExpireAt.Format(time.RFC3339), AppConfig.AccountGraceDays)); err != nil {
				log.Printf("auto suspend local state or audit write failed: user=%s tg=%d abs=%s err=%s",
					formatPlainValue(u.Username), u.TelegramID, formatPlainValue(u.AbsUserID), formatPlainError(err))
				detail := fmt.Sprintf("expired lifecycle suspend ABS state updated but local state/audit failed: username=%s tg=%d abs_user_id=%s expire_at=%s grace_days=%d error=%s",
					formatPlainValue(u.Username), u.TelegramID, formatPlainValue(u.AbsUserID), u.ExpireAt.Format(time.RFC3339), AppConfig.AccountGraceDays, formatPlainError(err))
				if auditErr := writeAuditLogInTx(DB, 0, "AUTO_SUSPEND_EXPIRED_USER_LOCAL_FAILED", fmt.Sprintf("%d", u.TelegramID), 0, detail); auditErr != nil {
					log.Printf("auto suspend local failure audit write failed: user=%s tg=%d abs=%s err=%s",
						formatPlainValue(u.Username), u.TelegramID, formatPlainValue(u.AbsUserID), formatPlainError(auditErr))
					notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 生命周期自动封禁已更新 ABS，但本地状态或成功审计失败，且失败审计写入失败。\n用户：%d\nABS：%s\n本地错误：%s\n审计错误：%s\n请立即人工核查。", u.TelegramID, formatPlainValue(u.AbsUserID), formatPlainError(err), formatPlainError(auditErr)))
				}
				continue
			}
			graceDays := AppConfig.AccountGraceDays
			msg := fmt.Sprintf("⛔ **账号已暂停使用**\n\n您的听书账号已到期，系统已暂停您的收听权限。\n\n⏳ **数据保留期**：您的账户数据将为您保留 `%d` 天。宽限期到期后系统将**永久删除**您的账户及所有收听记录。\n\n💡 请尽快使用【💳 使用续期卡】恢复权限！", graceDays)
			replyText(bot, u.TelegramID, msg)
			log.Printf("⛔ 已封禁过期用户: %s (TG: %d)", formatPlainValue(u.Username), u.TelegramID)
			continue
		}

		// 阶段 3：宽限期结束 -> 物理删除、硬销毁
		if u.IsSuspended && daysLeft <= -AppConfig.AccountGraceDays {
			err := absClient.DeleteUser(u.AbsUserID)
			absDeleteResult := "success"

			if err != nil && !IsAbsNotFoundError(err) {
				log.Printf("⚠️ ABS 删除失败，已保留本地档案，避免遗孀账号: user=%s tg=%d abs=%s err=%s",
					formatPlainValue(u.Username), u.TelegramID, formatPlainValue(u.AbsUserID), formatPlainError(err))
				continue
			}

			if err != nil {
				absDeleteResult = "already_missing"
			}

			if err := deleteLocalUserWithAudit(0, u.TelegramID, u.AbsUserID, "AUTO_DELETE_EXPIRED_USER", func(deleted User) string {
				return fmt.Sprintf("生命周期后台任务物理删除过期用户：username=%s tg=%d abs_user_id=%s grace_days=%d abs_delete=%s",
					formatPlainValue(deleted.Username), deleted.TelegramID, formatPlainValue(deleted.AbsUserID), AppConfig.AccountGraceDays, formatPlainValue(absDeleteResult))
			}); err != nil {
				log.Printf("自动注销本地档案或审计写入失败: user=%s tg=%d abs=%s err=%s",
					formatPlainValue(u.Username), u.TelegramID, formatPlainValue(u.AbsUserID), formatPlainError(err))
				continue
			}
			msg := "🗑 **账户已注销**\n\n由于您的账号已超出最长保留期限，系统已自动将您的账户及所有听书进度**永久销毁**。\n\n期待与您的再次相遇！您可以随时重新注册。"
			replyText(bot, u.TelegramID, msg)
			log.Printf("🗑 已彻底销毁逾期不续费用户: %s (TG: %d)", formatPlainValue(u.Username), u.TelegramID)
		}
	}
	return nil
}
