package main

import (
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	listeningAbuseDailyRawHoursThreshold = 15.0
	listeningAbuseFreezeHours            = 24

	listeningAbuseActionWarning = "warning"
	listeningAbuseActionFreeze  = "freeze"

	listeningAbuseStatusWarningSent    = "warning_sent"
	listeningAbuseStatusActive         = "active"
	listeningAbuseStatusReleased       = "released"
	listeningAbuseStatusAmnestied      = "amnestied"
	listeningAbuseStatusReleaseBlocked = "release_blocked"

	listeningAbuseEffectiveStartDayKey = "listening_abuse_effective_start_day"
	listeningAbuseLastScanDayKey       = "listening_abuse_last_scan_day"
	listeningAbuseLastScanAtKey        = "listening_abuse_last_scan_at"
	listeningAbuseLastErrorKey         = "listening_abuse_last_error"
)

var listeningAbuseMu sync.Mutex

var errListeningAbuseEffectiveStartDayInit = errors.New("LISTENING_ABUSE_EFFECTIVE_START_DAY_INIT_FAILED")

type ListeningAbuseRecord struct {
	gorm.Model

	UserID    int64  `gorm:"index;not null"`
	AbsUserID string `gorm:"index;not null"`

	DayKey         string `gorm:"index;not null"`
	PreviousDayKey string `gorm:"index"`

	RawHours         float64
	PreviousRawHours float64

	Action string `gorm:"index;not null"` // warning / freeze
	Status string `gorm:"index;not null"` // warning_sent / active / released / amnestied / release_blocked

	FreezeStartAt *time.Time `gorm:"index"`
	FreezeEndAt   *time.Time `gorm:"index"`
	ReleasedAt    *time.Time `gorm:"index"`

	NoticeError  string
	ReleaseError string
}

func (ListeningAbuseRecord) TableName() string {
	return "listening_abuse_records"
}

func runListeningAbuseMonitorIfNeeded(bot *tgbotapi.BotAPI, now time.Time) {
	if !listeningAbuseMu.TryLock() {
		return
	}
	defer listeningAbuseMu.Unlock()

	effectiveStartDay, err := ensureListeningAbuseEffectiveStartDayChecked(now)
	if err != nil {
		if errors.Is(err, errListeningAbuseEffectiveStartDayInit) {
			recordListeningAbuseStateWriteFailure(bot, listeningAbuseEffectiveStartDayKey, err)
		} else {
			recordListeningAbuseStateReadFailure(bot, listeningAbuseEffectiveStartDayKey, err)
		}
		return
	}
	amnestyPreEffectiveListeningAbuseFreezes(bot, now, effectiveStartDay)
	releaseDueListeningAbuseFreezes(bot, now)

	targetDay := listeningAbuseScanDayKey(now)
	if targetDay != "" && listeningAbuseDayBefore(targetDay, effectiveStartDay) {
		return
	}
	if targetDay == "" {
		return
	}
	lastScanDay, err := getSystemConfigStringChecked(listeningAbuseLastScanDayKey)
	if err != nil {
		recordListeningAbuseStateReadFailure(bot, listeningAbuseLastScanDayKey, err)
		return
	}
	if lastScanDay == targetDay {
		return
	}

	warnings, freezes, skipped, err := scanListeningAbuseForDay(bot, targetDay, effectiveStartDay, now)
	if err != nil {
		setSystemConfigError(listeningAbuseLastErrorKey, err)
		log.Printf("listening abuse scan failed: day=%s err=%s", formatPlainValue(targetDay), formatPlainError(err))
		notifySuperAdminsPlain(bot, fmt.Sprintf("播放异常巡检失败\n\n日期: %s\n错误: %s", formatPlainValue(targetDay), formatPlainError(err)))
		return
	}

	if err := setSystemConfigStringChecked(listeningAbuseLastScanDayKey, targetDay); err != nil {
		recordListeningAbuseStateWriteFailure(bot, listeningAbuseLastScanDayKey, err)
		return
	}
	if err := setSystemConfigStringChecked(listeningAbuseLastScanAtKey, time.Now().Format(time.RFC3339)); err != nil {
		recordListeningAbuseStateWriteFailure(bot, listeningAbuseLastScanAtKey, err)
		return
	}
	if err := setSystemConfigStringChecked(listeningAbuseLastErrorKey, ""); err != nil {
		recordListeningAbuseStateWriteFailure(bot, listeningAbuseLastErrorKey, err)
		return
	}
	log.Printf("listening abuse scan finished: day=%s warnings=%d freezes=%d skipped=%d", formatPlainValue(targetDay), warnings, freezes, skipped)
}

func listeningAbuseScanDayKey(now time.Time) string {
	localNow := dailyOperationsLocalTime(now)
	return localNow.AddDate(0, 0, -1).Format("2006-01-02")
}

func ensureListeningAbuseEffectiveStartDayChecked(now time.Time) (string, error) {
	raw, err := getSystemConfigStringChecked(listeningAbuseEffectiveStartDayKey)
	if err != nil {
		return "", err
	}
	dayKey := strings.TrimSpace(raw)
	if isListeningAbuseDayKey(dayKey) {
		return dayKey, nil
	}

	dayKey = dailyOperationsDateKey(now)
	if err := setSystemConfigStringChecked(listeningAbuseEffectiveStartDayKey, dayKey); err != nil {
		return "", fmt.Errorf("%w: %w", errListeningAbuseEffectiveStartDayInit, err)
	}
	log.Printf("listening abuse effective start day initialized: day=%s", formatPlainValue(dayKey))
	return dayKey, nil
}

func recordListeningAbuseStateReadFailure(bot *tgbotapi.BotAPI, key string, err error) {
	setSystemConfigError(listeningAbuseLastErrorKey, err)
	log.Printf("播放异常风控状态读取失败，已跳过本轮巡检: key=%s err=%s", formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("播放异常风控状态读取失败，已跳过本轮巡检。\n\n配置：%s\n错误：%s", formatPlainValue(key), formatPlainError(err)))
}

func recordListeningAbuseStateWriteFailure(bot *tgbotapi.BotAPI, key string, err error) {
	if key != listeningAbuseLastErrorKey {
		setSystemConfigError(listeningAbuseLastErrorKey, err)
	}
	log.Printf("播放异常风控状态写入失败: key=%s err=%s", formatPlainValue(key), formatPlainError(err))
	notifySuperAdminsPlain(bot, fmt.Sprintf("播放异常风控状态写入失败，请人工核查；相关巡检已中止或完成状态未确认。\n\n配置：%s\n错误：%s", formatPlainValue(key), formatPlainError(err)))
}

func isListeningAbuseDayKey(dayKey string) bool {
	if dayKey == "" {
		return false
	}
	_, err := time.ParseInLocation("2006-01-02", dayKey, dailyOperationsLocation)
	return err == nil
}

func listeningAbuseDayBefore(dayKey string, otherDayKey string) bool {
	return isListeningAbuseDayKey(dayKey) && isListeningAbuseDayKey(otherDayKey) && dayKey < otherDayKey
}

func previousDayKey(dayKey string) (string, bool) {
	day, err := time.ParseInLocation("2006-01-02", dayKey, dailyOperationsLocation)
	if err != nil {
		return "", false
	}
	return day.AddDate(0, 0, -1).Format("2006-01-02"), true
}

func listeningAbuseShouldWarn(rawHours float64) bool {
	return rawHours > listeningAbuseDailyRawHoursThreshold
}

func listeningAbuseShouldFreeze(rawHours float64, previousRawHours float64) bool {
	return rawHours > listeningAbuseDailyRawHoursThreshold && previousRawHours > listeningAbuseDailyRawHoursThreshold
}

func scanListeningAbuseForDay(bot *tgbotapi.BotAPI, dayKey string, effectiveStartDay string, now time.Time) (int, int, int, error) {
	if listeningAbuseDayBefore(dayKey, effectiveStartDay) {
		return 0, 0, 0, nil
	}

	previousKey, ok := previousDayKey(dayKey)
	if !ok {
		return 0, 0, 0, fmt.Errorf("invalid_day_key")
	}

	var users []User
	if err := DB.Select("telegram_id", "username", "abs_user_id", "expire_at", "is_suspended", "is_whitelist", "role", "status").
		Where("abs_user_id <> '' AND is_whitelist = ? AND role <> ?", false, "super_admin").
		Where("(status IS NULL OR status = '' OR status = ?)", "active").
		Find(&users).Error; err != nil {
		return 0, 0, 0, err
	}

	warnings := 0
	freezes := 0
	skipped := 0
	for _, u := range users {
		if u.IsSuspended || isUserExpiredAt(u, now) {
			skipped++
			continue
		}

		rawHours, hasToday, err := dailyRawListeningHours(u.TelegramID, dayKey)
		if err != nil {
			log.Printf("listening abuse daily stat read failed: user=%d day=%s err=%s", u.TelegramID, formatPlainValue(dayKey), formatPlainError(err))
			skipped++
			continue
		}
		if !hasToday || !listeningAbuseShouldWarn(rawHours) {
			continue
		}
		previousRawHours := 0.0
		if !listeningAbuseDayBefore(previousKey, effectiveStartDay) {
			var hasPrevious bool
			previousRawHours, hasPrevious, err = dailyRawListeningHours(u.TelegramID, previousKey)
			if err != nil {
				log.Printf("listening abuse previous daily stat read failed: user=%d day=%s previous_day=%s err=%s", u.TelegramID, formatPlainValue(dayKey), formatPlainValue(previousKey), formatPlainError(err))
				skipped++
				continue
			}
			if !hasPrevious {
				previousRawHours = 0
			}
		}
		if listeningAbuseShouldFreeze(rawHours, previousRawHours) {
			started, err := startListeningAbuseFreeze(bot, u, dayKey, previousKey, rawHours, previousRawHours, now)
			if err != nil {
				log.Printf("listening abuse freeze failed: user=%d day=%s err=%s", u.TelegramID, formatPlainValue(dayKey), formatPlainError(err))
				writeListeningAbuseFailureAudit(bot, "LISTENING_ABUSE_FREEZE_FAILED", u.TelegramID, "播放异常自动冻结失败",
					fmt.Sprintf("failed to freeze abnormal listening user; day=%s raw_hours=%.2f previous_day=%s previous_raw_hours=%.2f error=%s",
						formatPlainValue(dayKey), rawHours, formatPlainValue(previousKey), previousRawHours, formatPlainError(err)))
				continue
			}
			if started {
				freezes++
			}
			continue
		}

		warned, err := createListeningAbuseWarning(bot, u, dayKey, rawHours, now)
		if err != nil {
			log.Printf("listening abuse warning failed: user=%d day=%s err=%s", u.TelegramID, formatPlainValue(dayKey), formatPlainError(err))
			continue
		}
		if warned {
			warnings++
		}
	}

	return warnings, freezes, skipped, nil
}

func dailyRawListeningHours(userID int64, dayKey string) (float64, bool, error) {
	var stat DailyListeningStat
	if err := DB.Where("user_id = ? AND day_key = ?", userID, dayKey).First(&stat).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, false, nil
		}
		return 0, false, err
	}
	rawHours, ok := listeningAbuseRawHoursFromStat(stat)
	return rawHours, ok, nil
}

func listeningAbuseRawHoursFromStat(stat DailyListeningStat) (float64, bool) {
	if stat.FetchStatus == dailyListeningLiveProvisionalStatus || stat.Source == dailyListeningLiveCheckpointSource {
		return 0, false
	}
	if stat.FetchStatus == "mixed" || stat.Source == "mixed" ||
		stat.FetchStatus == dailyListeningCrossDayStatus || stat.Source == dailyListeningCrossDaySource {
		if stat.OfficialRawSeconds <= 0 {
			return 0, false
		}
		return stat.OfficialRawSeconds / 3600, true
	}
	if stat.OfficialRawSeconds > 0 {
		return stat.OfficialRawSeconds / 3600, true
	}
	return stat.RawSeconds / 3600, true
}

func isUserExpiredAt(u User, now time.Time) bool {
	return u.ExpireAt != nil && !u.ExpireAt.After(now)
}

func createListeningAbuseRecordInTx(tx *gorm.DB, record *ListeningAbuseRecord) (bool, error) {
	if tx == nil || record == nil {
		return false, fmt.Errorf("LISTENING_ABUSE_RECORD_INVALID")
	}
	entry := *record
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	*record = entry
	return true, nil
}

func createListeningAbuseWarning(bot *tgbotapi.BotAPI, u User, dayKey string, rawHours float64, now time.Time) (bool, error) {
	record := ListeningAbuseRecord{
		UserID:    u.TelegramID,
		AbsUserID: u.AbsUserID,
		DayKey:    dayKey,
		RawHours:  rawHours,
		Action:    listeningAbuseActionWarning,
		Status:    listeningAbuseStatusWarningSent,
	}

	if err := DB.Transaction(func(tx *gorm.DB) error {
		created, err := createListeningAbuseRecordInTx(tx, &record)
		if err != nil {
			return err
		}
		if !created {
			return nil
		}
		return writeAuditLogInTx(tx, 0, "LISTENING_ABUSE_WARNING", strconv.FormatInt(u.TelegramID, 10), 0,
			fmt.Sprintf("abnormal listening warning; record_id=%d day=%s raw_hours=%.2f threshold=%.2f",
				record.ID, dayKey, rawHours, listeningAbuseDailyRawHoursThreshold))
	}); err != nil {
		return false, err
	}
	if record.ID == 0 {
		return false, nil
	}

	text := fmt.Sprintf("⚠️ **播放时长异常提醒**\n\n系统检测到您在北京时间 `%s` 的 ABS 原始播放时长为 `%.2f` 小时，已超过 `%g` 小时。\n\n若连续两个自然日超过该阈值，系统将自动暂停听书账号 `24` 小时。请确认播放器、自动播放或多设备播放状态是否正常。",
		dayKey, rawHours, listeningAbuseDailyRawHoursThreshold)
	noticeErr := sendListeningAbusePrivateNotice(bot, u.TelegramID, text)
	if noticeErr != nil {
		recordListeningAbuseNoticeError(record.ID, u.TelegramID, formatPlainError(noticeErr))
	}
	return true, nil
}

func startListeningAbuseFreeze(bot *tgbotapi.BotAPI, u User, dayKey string, previousDayKey string, rawHours float64, previousRawHours float64, now time.Time) (bool, error) {
	if err := absClient.SetUserActiveStatus(u.AbsUserID, false); err != nil {
		return false, err
	}

	freezeEnd := now.Add(time.Duration(listeningAbuseFreezeHours) * time.Hour)
	record := ListeningAbuseRecord{
		UserID:           u.TelegramID,
		AbsUserID:        u.AbsUserID,
		DayKey:           dayKey,
		PreviousDayKey:   previousDayKey,
		RawHours:         rawHours,
		PreviousRawHours: previousRawHours,
		Action:           listeningAbuseActionFreeze,
		Status:           listeningAbuseStatusActive,
		FreezeStartAt:    &now,
		FreezeEndAt:      &freezeEnd,
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		var activeCount int64
		if err := tx.Model(&ListeningAbuseRecord{}).
			Where("user_id = ? AND action = ? AND status = ? AND deleted_at IS NULL", u.TelegramID, listeningAbuseActionFreeze, listeningAbuseStatusActive).
			Count(&activeCount).Error; err != nil {
			return err
		}
		if activeCount > 0 {
			return nil
		}
		created, err := createListeningAbuseRecordInTx(tx, &record)
		if err != nil {
			return err
		}
		if !created {
			return nil
		}
		return writeAuditLogInTx(tx, 0, "LISTENING_ABUSE_FREEZE", strconv.FormatInt(u.TelegramID, 10), 0,
			fmt.Sprintf("auto freeze abnormal listening user; day=%s raw_hours=%.2f previous_day=%s previous_raw_hours=%.2f freeze_until=%s",
				dayKey, rawHours, previousDayKey, previousRawHours, freezeEnd.Format(time.RFC3339)))
	})
	if err != nil {
		if reactivateErr := absClient.SetUserActiveStatus(u.AbsUserID, true); reactivateErr != nil {
			log.Printf("listening abuse freeze rollback failed: user=%d abs=%s err=%s", u.TelegramID, formatPlainValue(u.AbsUserID), formatPlainError(reactivateErr))
		}
		return false, err
	}
	if record.ID == 0 {
		return false, nil
	}

	privateText := fmt.Sprintf("⛔ **账号已临时暂停**\n\n系统检测到您连续两个北京时间自然日播放时长超过 `%g` 小时：\n`%s`：`%.2f` 小时\n`%s`：`%.2f` 小时\n\n您的听书账号已暂停 `24` 小时，预计恢复时间：`%s`。\n\n本次仅暂停收听权限，不扣除积分、贡献或修为。请检查播放器自动播放、多设备播放或未关闭播放的问题。",
		listeningAbuseDailyRawHoursThreshold, previousDayKey, previousRawHours, dayKey, rawHours, dailyOperationsLocalTime(freezeEnd).Format("2006-01-02 15:04"))
	noticeErr := sendListeningAbusePrivateNotice(bot, u.TelegramID, privateText)
	groupErr := sendListeningAbuseGroupFreezeNotice(bot, u, dayKey, rawHours, previousDayKey, previousRawHours, freezeEnd)
	if noticeErr != nil || groupErr != nil {
		recordListeningAbuseNoticeError(record.ID, u.TelegramID, fmt.Sprintf("private=%s group=%s", formatPlainError(noticeErr), formatPlainError(groupErr)))
	}
	return true, nil
}

func releaseDueListeningAbuseFreezes(bot *tgbotapi.BotAPI, now time.Time) {
	var records []ListeningAbuseRecord
	if err := DB.Where("action = ? AND status = ? AND freeze_end_at IS NOT NULL AND freeze_end_at <= ?",
		listeningAbuseActionFreeze, listeningAbuseStatusActive, now).
		Find(&records).Error; err != nil {
		log.Printf("query due listening abuse freezes failed: err=%s", formatPlainError(err))
		return
	}

	for _, record := range records {
		if err := releaseListeningAbuseFreeze(bot, record, now); err != nil {
			log.Printf("release listening abuse freeze failed: record=%d user=%d err=%s", record.ID, record.UserID, formatPlainError(err))
		}
	}
}

func amnestyPreEffectiveListeningAbuseFreezes(bot *tgbotapi.BotAPI, now time.Time, effectiveStartDay string) {
	if !isListeningAbuseDayKey(effectiveStartDay) {
		return
	}

	var records []ListeningAbuseRecord
	if err := DB.Where("action = ? AND status = ? AND day_key < ?",
		listeningAbuseActionFreeze, listeningAbuseStatusActive, effectiveStartDay).
		Find(&records).Error; err != nil {
		log.Printf("query pre-effective listening abuse freezes failed: effective_start=%s err=%s", formatPlainValue(effectiveStartDay), formatPlainError(err))
		return
	}

	for _, record := range records {
		if err := amnestyListeningAbuseFreeze(bot, record, now, effectiveStartDay); err != nil {
			log.Printf("amnesty listening abuse freeze failed: record=%d user=%d effective_start=%s err=%s", record.ID, record.UserID, formatPlainValue(effectiveStartDay), formatPlainError(err))
		}
	}
}

func amnestyListeningAbuseFreeze(bot *tgbotapi.BotAPI, record ListeningAbuseRecord, now time.Time, effectiveStartDay string) error {
	var u User
	if err := DB.Where("telegram_id = ?", record.UserID).First(&u).Error; err != nil {
		return err
	}

	if u.AbsUserID != record.AbsUserID || u.IsSuspended || isUserExpiredAt(u, now) {
		reason := fmt.Sprintf("pre-effective amnesty blocked; effective_start=%s abs_changed=%t suspended=%t expired=%t",
			formatPlainValue(effectiveStartDay), u.AbsUserID != record.AbsUserID, u.IsSuspended, isUserExpiredAt(u, now))
		return markListeningAbuseFreezeReleaseBlocked(record.ID, record.UserID, now, reason)
	}

	if err := absClient.SetUserActiveStatus(record.AbsUserID, true); err != nil {
		recordListeningAbuseReleaseError(record.ID, record.UserID, err)
		writeListeningAbuseFailureAudit(bot, "LISTENING_ABUSE_AMNESTY_FAILED", record.UserID, "播放异常既往不咎恢复失败",
			fmt.Sprintf("failed to restore pre-effective abnormal listening freeze; record_id=%d effective_start=%s error=%s",
				record.ID, formatPlainValue(effectiveStartDay), formatPlainError(err)))
		return err
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&ListeningAbuseRecord{}).
			Where("id = ? AND status = ?", record.ID, listeningAbuseStatusActive).
			Updates(map[string]interface{}{
				"status":        listeningAbuseStatusAmnestied,
				"released_at":   now,
				"release_error": "",
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		return writeAuditLogInTx(tx, 0, "LISTENING_ABUSE_AMNESTY", strconv.FormatInt(record.UserID, 10), 0,
			fmt.Sprintf("restored pre-effective abnormal listening freeze; record_id=%d day=%s effective_start=%s",
				record.ID, formatPlainValue(record.DayKey), formatPlainValue(effectiveStartDay)))
	})
	if err != nil {
		return err
	}

	_ = sendListeningAbusePrivateNotice(bot, record.UserID, fmt.Sprintf("✅ **账号暂停已解除**\n\n播放异常风控已改为自北京时间 `%s` 起计算，历史收听时长既往不咎。系统已恢复您的 ABS 听书权限，请保持正常收听。", effectiveStartDay))
	return nil
}

func releaseListeningAbuseFreeze(bot *tgbotapi.BotAPI, record ListeningAbuseRecord, now time.Time) error {
	var u User
	if err := DB.Where("telegram_id = ?", record.UserID).First(&u).Error; err != nil {
		return err
	}

	if u.AbsUserID != record.AbsUserID || u.IsSuspended || isUserExpiredAt(u, now) {
		reason := fmt.Sprintf("release blocked; abs_changed=%t suspended=%t expired=%t",
			u.AbsUserID != record.AbsUserID, u.IsSuspended, isUserExpiredAt(u, now))
		return markListeningAbuseFreezeReleaseBlocked(record.ID, record.UserID, now, reason)
	}

	if err := absClient.SetUserActiveStatus(record.AbsUserID, true); err != nil {
		recordListeningAbuseReleaseError(record.ID, record.UserID, err)
		writeListeningAbuseFailureAudit(bot, "LISTENING_ABUSE_RELEASE_FAILED", record.UserID, "播放异常临停到期恢复失败",
			fmt.Sprintf("failed to release abnormal listening freeze; record_id=%d error=%s", record.ID, formatPlainError(err)))
		return err
	}

	err := DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&ListeningAbuseRecord{}).
			Where("id = ? AND status = ?", record.ID, listeningAbuseStatusActive).
			Updates(map[string]interface{}{
				"status":        listeningAbuseStatusReleased,
				"released_at":   now,
				"release_error": "",
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		return writeAuditLogInTx(tx, 0, "LISTENING_ABUSE_RELEASE", strconv.FormatInt(record.UserID, 10), 0,
			fmt.Sprintf("released abnormal listening freeze; record_id=%d freeze_end=%s", record.ID, now.Format(time.RFC3339)))
	})
	if err != nil {
		return err
	}

	_ = sendListeningAbusePrivateNotice(bot, record.UserID, "✅ **账号暂停已解除**\n\n本次播放异常暂停已满 `24` 小时，系统已恢复您的 ABS 听书权限。请保持正常收听，避免连续长时间自动播放再次触发风控。")
	return nil
}

func recordListeningAbuseReleaseError(recordID uint, userID int64, releaseErr error) {
	if releaseErr == nil {
		return
	}
	res := DB.Model(&ListeningAbuseRecord{}).
		Where("id = ?", recordID).
		Update("release_error", formatPlainError(releaseErr))
	if res.Error != nil {
		log.Printf("listening abuse release error persistence failed: record=%d user=%d err=%s", recordID, userID, formatPlainError(res.Error))
		return
	}
	if res.RowsAffected == 0 {
		log.Printf("listening abuse release error persistence missed record: record=%d user=%d", recordID, userID)
	}
}

func writeListeningAbuseFailureAudit(bot *tgbotapi.BotAPI, action string, userID int64, label string, detail string) {
	if err := writeAuditLogInTx(DB, 0, action, strconv.FormatInt(userID, 10), 0, detail); err != nil {
		log.Printf("listening abuse failure audit write failed: action=%s user=%d err=%s", formatPlainValue(action), userID, formatPlainError(err))
		notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ %s，且失败审计写入失败。\n用户：%d\n审计动作：%s\n审计错误：%s", formatPlainValue(label), userID, formatPlainValue(action), formatPlainError(err)))
	}
}

func recordListeningAbuseNoticeError(recordID uint, userID int64, noticeText string) {
	noticeText = strings.TrimSpace(formatPlainValue(noticeText))
	if noticeText == "" {
		return
	}
	res := DB.Model(&ListeningAbuseRecord{}).
		Where("id = ?", recordID).
		Update("notice_error", noticeText)
	if res.Error != nil {
		log.Printf("listening abuse notice error persistence failed: record=%d user=%d err=%s", recordID, userID, formatPlainError(res.Error))
		return
	}
	if res.RowsAffected == 0 {
		log.Printf("listening abuse notice error persistence missed record: record=%d user=%d", recordID, userID)
	}
}

func markListeningAbuseFreezeReleaseBlocked(recordID uint, userID int64, now time.Time, reason string) error {
	return DB.Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&ListeningAbuseRecord{}).
			Where("id = ? AND status = ?", recordID, listeningAbuseStatusActive).
			Updates(map[string]interface{}{
				"status":        listeningAbuseStatusReleaseBlocked,
				"released_at":   now,
				"release_error": reason,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return nil
		}
		return writeAuditLogInTx(tx, 0, "LISTENING_ABUSE_RELEASE_BLOCKED", strconv.FormatInt(userID, 10), 0,
			fmt.Sprintf("abnormal listening freeze release blocked; record_id=%d reason=%s", recordID, formatPlainValue(reason)))
	})
}

func sendListeningAbusePrivateNotice(bot *tgbotapi.BotAPI, chatID int64, text string) error {
	if bot == nil {
		return fmt.Errorf("bot_empty")
	}
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	_, err := sendNoAutoDelete(bot, msg)
	return err
}

func sendListeningAbuseGroupFreezeNotice(bot *tgbotapi.BotAPI, u User, dayKey string, rawHours float64, previousDayKey string, previousRawHours float64, freezeEnd time.Time) error {
	if bot == nil || AppConfig == nil || AppConfig.NoticeGroupID == 0 {
		return nil
	}

	name := escapeMarkdown(u.Username)
	if name == "" {
		name = strconv.FormatInt(u.TelegramID, 10)
	}
	text := fmt.Sprintf("⚠️ **播放异常风控**\n\n道友 `%s` 因连续两天播放时长异常，听书账号已临时暂停 `24` 小时。\n\n`%s`：`%.2f` 小时\n`%s`：`%.2f` 小时\n预计恢复：`%s`",
		name, previousDayKey, previousRawHours, dayKey, rawHours, dailyOperationsLocalTime(freezeEnd).Format("2006-01-02 15:04"))
	msg := tgbotapi.NewMessage(AppConfig.NoticeGroupID, text)
	msg.ParseMode = "Markdown"
	_, err := sendNoAutoDelete(bot, msg)
	return err
}
