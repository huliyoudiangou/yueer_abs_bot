package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type WorldBossEvent struct {
	gorm.Model
	BossID string `gorm:"index;not null"`
	Name   string
	ChatID int64 `gorm:"index"`

	Status  string    `gorm:"index"` // active / settling / settled
	StartAt time.Time `gorm:"index"`
	EndAt   time.Time `gorm:"index"`

	MaxHP     int
	CurrentHP float64
	IsKilled  bool

	ParticipantCount int
	SettledAt        *time.Time

	BoardChatID    int64
	BoardMessageID int
}

func (WorldBossEvent) TableName() string { return "world_boss_events" }

func createWorldBossEventRecord(db *gorm.DB, event *WorldBossEvent) error {
	if db == nil || event == nil {
		return fmt.Errorf("WORLD_BOSS_EVENT_INVALID")
	}
	event.BossID = formatPlainValue(event.BossID)
	event.Name = formatPlainValue(event.Name)
	event.Status = formatPlainValue(event.Status)
	res := db.Create(event)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("WORLD_BOSS_EVENT_CREATE_MISSED")
	}
	return nil
}

type WorldBossParticipant struct {
	gorm.Model
	BossID   string `gorm:"index;not null"`
	UserID   int64  `gorm:"index;not null"`
	UserName string

	BaseHours  float64
	FinalHours float64
	DeltaHours float64

	BaseDamage float64
	Multiplier float64
	Damage     float64

	RewardPoints       int
	RewardContribution int
	RewardPrestige     int
	IsRewarded         bool `gorm:"default:false"`
}

func (WorldBossParticipant) TableName() string { return "world_boss_participants" }

func createWorldBossParticipantInTx(db *gorm.DB, participant *WorldBossParticipant) (bool, error) {
	if db == nil || participant == nil {
		return false, fmt.Errorf("WORLD_BOSS_PARTICIPANT_INVALID")
	}
	entry := *participant
	entry.BossID = formatPlainValue(entry.BossID)
	entry.UserName = formatPlainValue(entry.UserName)
	res := db.Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(&entry)
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	*participant = entry
	return true, nil
}

const (
	worldBossName      = "域外心魔"
	worldBossStartHour = 21
	worldBossEndHour   = 22

	worldBossBaseHP           = 100
	worldBossMinHP            = 120
	worldBossMaxHP            = 450
	worldBossHPPerParticipant = 12

	worldBossActualDamagePerHour      = 30.0
	worldBossValidDamage              = 3.0
	worldBossCultivationBonusPerStage = 0.01
	worldBossCultivationBonusCap      = 0.25
	worldBossLiveRefreshInterval      = 2 * time.Minute
	worldBossJoinCloseBeforeEnd       = 15 * time.Minute
	worldBossSettlementStaleAfter     = 30 * time.Minute
	worldBossRedPacketTotalPoints     = 30
	worldBossRedPacketCount           = 10
)

func worldBossPointDescriptionName(name string) string {
	return lotteryDisplayText(name, 80, "-")
}

var (
	lastWorldBossStartKey string
	worldBossRankOrder    = "damage DESC, delta_hours DESC, created_at ASC"
)

func isWorldBossOpenDay(weekday time.Weekday) bool {
	return weekday == time.Saturday || weekday == time.Sunday
}

func StartWorldBossScheduler(bot *tgbotapi.BotAPI) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for now := range ticker.C {
			if AppConfig.NoticeGroupID == 0 {
				continue
			}

			local := now.In(time.FixedZone("CST", 8*3600))
			dayKey := local.Format("2006-01-02")

			if isWorldBossOpenDay(local.Weekday()) && local.Hour() == worldBossStartHour && local.Minute() == 0 && lastWorldBossStartKey != dayKey {
				lastWorldBossStartKey = dayKey
				go startWorldBossEvent(bot, AppConfig.NoticeGroupID, local)
			}

			go refreshActiveWorldBosses(bot, local)
			go settleExpiredWorldBosses(bot, local)
		}
	}()

	log.Println("✅ 世界Boss调度器已启动：每周六、周日 21:00-22:00")
}

func HandleWorldBossCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) bool {
	if msg == nil || msg.From == nil {
		return false
	}

	text = strings.TrimSpace(text)
	if text != "世界Boss" && text != "Boss状态" && text != "参加Boss" && text != "Boss排行" {
		return false
	}

	registerIncomingGroupCommandForAutoDelete(msg)

	switch text {
	case "世界Boss", "Boss状态":
		handleWorldBossStatus(bot, msg)
	case "参加Boss":
		handleJoinWorldBoss(bot, msg)
	case "Boss排行":
		handleWorldBossRank(bot, msg)
	}

	return true
}

func startWorldBossEvent(bot *tgbotapi.BotAPI, chatID int64, now time.Time) {
	startAt := time.Date(now.Year(), now.Month(), now.Day(), worldBossStartHour, 0, 0, 0, now.Location())
	endAt := time.Date(now.Year(), now.Month(), now.Day(), worldBossEndHour, 0, 0, 0, now.Location())
	bossID := "WB-" + startAt.Format("20060102")

	exists, err := worldBossEventExists(bossID)
	if err != nil {
		log.Printf("❌ 查询世界Boss是否已存在失败: boss=%s err=%s", formatPlainValue(bossID), formatPlainError(err))
		return
	}
	if exists {
		return
	}

	maxHP := calculateWorldBossMaxHP(0)

	event := WorldBossEvent{
		BossID:    bossID,
		Name:      worldBossName,
		ChatID:    chatID,
		Status:    "active",
		StartAt:   startAt,
		EndAt:     endAt,
		MaxHP:     maxHP,
		CurrentHP: float64(maxHP),
	}

	if err := createWorldBossEventRecord(DB, &event); err != nil {
		log.Printf("❌ 创建世界Boss失败: %s", formatPlainError(err))
		return
	}

	notice := fmt.Sprintf(
		"🌑 **【世界Boss降临】** 🌑\n\n"+
			"域外心魔撕裂天幕，诸位道友请共同听书镇压！\n\n"+
			"Boss：**%s**\n"+
			"时间：`21:00 - 22:00`\n"+
			"血量：`%.2f/%d`（基础 `%d` + 每名参与者 `%d`，最低 `%d`，最高 `%d`）\n\n"+
			"发送 `参加Boss` 记录当前实际听书时长。\n"+
			"最后 `%d` 分钟停止新道友加入。\n"+
			"结算伤害 = 参与后 Boss 时段实际听书小时 × `%.0f` ×（1 + 修为加成 + 宗门科技）。\n"+
			"修为加成：炼气初期 `+1%%`，每小段 `+1%%`，最高 `+25%%`。\n\n"+
			"发送 `Boss状态` 查看进度，`Boss排行` 查看伤害榜。",
		escapeMarkdown(event.Name),
		event.CurrentHP,
		event.MaxHP,
		worldBossBaseHP,
		worldBossHPPerParticipant,
		worldBossMinHP,
		worldBossMaxHP,
		int(worldBossJoinCloseBeforeEnd.Minutes()),
		worldBossActualDamagePerHour,
	)

	sendGroupAutoDeleteMessage(bot, chatID, notice)
	ensureWorldBossLiveBoard(bot, event)
}

func worldBossEventExists(bossID string) (bool, error) {
	bossID = strings.TrimSpace(bossID)
	if bossID == "" {
		return false, fmt.Errorf("INVALID_WORLD_BOSS_ID")
	}
	var existing WorldBossEvent
	if err := DB.Where("boss_id = ?", bossID).First(&existing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

func calculateWorldBossMaxHP(participantCount int) int {
	if participantCount < 0 {
		participantCount = 0
	}

	maxHP := worldBossBaseHP + participantCount*worldBossHPPerParticipant
	if maxHP < worldBossMinHP {
		maxHP = worldBossMinHP
	}
	if maxHP > worldBossMaxHP {
		maxHP = worldBossMaxHP
	}
	return maxHP
}

func worldBossJoinDeadline(event WorldBossEvent) time.Time {
	return event.EndAt.Add(-worldBossJoinCloseBeforeEnd)
}

func canJoinWorldBossAt(event WorldBossEvent, now time.Time) bool {
	if event.BossID == "" || event.Status != "active" {
		return false
	}
	if now.Before(event.StartAt) || !now.Before(event.EndAt) {
		return false
	}
	return now.Before(worldBossJoinDeadline(event))
}

func refreshWorldBossStoredHPByParticipants(bossID string) (WorldBossEvent, error) {
	var event WorldBossEvent
	if err := DB.Where("boss_id = ? AND status = ?", bossID, "active").First(&event).Error; err != nil {
		return event, err
	}

	var participantCount int64
	if err := DB.Model(&WorldBossParticipant{}).
		Where("boss_id = ?", bossID).
		Count(&participantCount).Error; err != nil {
		return event, err
	}

	var totalDamage float64
	if err := DB.Model(&WorldBossParticipant{}).
		Where("boss_id = ?", bossID).
		Select("COALESCE(SUM(damage), 0)").
		Scan(&totalDamage).Error; err != nil {
		return event, err
	}
	totalDamage = roundWorldBossDamage(totalDamage)

	maxHP := calculateWorldBossMaxHP(int(participantCount))
	currentHP := float64(maxHP) - totalDamage
	if currentHP < 0 {
		currentHP = 0
	} else {
		currentHP = roundWorldBossDamage(currentHP)
	}
	isKilled := totalDamage >= float64(maxHP)

	res := DB.Model(&WorldBossEvent{}).
		Where("boss_id = ? AND status = ?", bossID, "active").
		Updates(map[string]interface{}{
			"max_hp":            maxHP,
			"current_hp":        currentHP,
			"is_killed":         isKilled,
			"participant_count": int(participantCount),
		})
	if res.Error != nil {
		return event, res.Error
	}
	if res.RowsAffected == 0 {
		return event, fmt.Errorf("WORLD_BOSS_ACTIVE_STATE_CHANGED")
	}

	event.MaxHP = maxHP
	event.CurrentHP = currentHP
	event.IsKilled = isKilled
	event.ParticipantCount = int(participantCount)
	return event, nil
}

func settleExpiredWorldBosses(bot *tgbotapi.BotAPI, now time.Time) {
	recoverStaleWorldBossSettlements(now)

	var events []WorldBossEvent
	if err := DB.Where("status = ? AND end_at <= ?", "active", now).Find(&events).Error; err != nil {
		log.Printf("⚠️ 查询到期世界Boss失败，已跳过本轮结算扫描: now=%s err=%s", formatPlainValue(now.Format(time.RFC3339)), formatPlainError(err))
		return
	}

	for _, event := range events {
		settleWorldBoss(bot, event.BossID)
	}
}

func refreshActiveWorldBosses(bot *tgbotapi.BotAPI, now time.Time) {
	recoverStaleWorldBossSettlements(now)

	var events []WorldBossEvent
	if err := DB.Where("status = ? AND start_at <= ? AND end_at > ?", "active", now, now).Find(&events).Error; err != nil {
		log.Printf("⚠️ 查询进行中世界Boss失败，已跳过本轮实时刷新: now=%s err=%s", formatPlainValue(now.Format(time.RFC3339)), formatPlainError(err))
		return
	}

	for _, event := range events {
		if time.Since(event.UpdatedAt) < worldBossLiveRefreshInterval {
			continue
		}
		refreshed, _, err := refreshWorldBossLiveDamage(event)
		if err != nil {
			log.Printf("⚠️ 世界Boss实时伤害刷新失败: boss=%s err=%s", formatPlainValue(event.BossID), formatPlainError(err))
			continue
		}
		ensureWorldBossLiveBoard(bot, refreshed)
		if refreshed.IsKilled {
			settleWorldBoss(bot, refreshed.BossID)
		}
	}
}

func recoverStaleWorldBossSettlements(now time.Time) {
	if DB == nil {
		return
	}
	cutoff := now.Add(-worldBossSettlementStaleAfter)
	res := DB.Model(&WorldBossEvent{}).
		Where("status = ? AND updated_at < ? AND settled_at IS NULL", "settling", cutoff).
		Update("status", "active")
	if res.Error != nil {
		log.Printf("世界Boss settling 恢复扫描失败: cutoff=%s err=%s", formatPlainValue(cutoff.Format(time.RFC3339)), formatPlainError(res.Error))
		return
	}
	if res.RowsAffected > 0 {
		log.Printf("世界Boss settling 已恢复为 active: count=%d cutoff=%s", res.RowsAffected, formatPlainValue(cutoff.Format(time.RFC3339)))
	}
}

func refreshWorldBossLiveDamage(event WorldBossEvent) (WorldBossEvent, float64, error) {
	if event.BossID == "" || event.Status != "active" {
		return event, 0, nil
	}

	var participants []WorldBossParticipant
	if err := DB.Where("boss_id = ? AND deleted_at IS NULL", event.BossID).Find(&participants).Error; err != nil {
		return event, 0, err
	}

	totalDamage := 0.0
	maxHP := calculateWorldBossMaxHP(len(participants))
	for i := range participants {
		p := &participants[i]

		var u User
		if err := DB.Where("telegram_id = ?", p.UserID).First(&u).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 世界Boss实时读取本地档案失败: boss=%s user=%d err=%s", formatPlainValue(event.BossID), p.UserID, formatPlainError(err))
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := clearWorldBossParticipantComputedDamage(*p, "user_not_found"); err != nil {
					return event, totalDamage, err
				}
			} else {
				totalDamage += p.Damage
			}
			continue
		}
		if u.AbsUserID == "" {
			if err := clearWorldBossParticipantComputedDamage(*p, "abs_unbound"); err != nil {
				return event, totalDamage, err
			}
			continue
		}

		finalHours, err := getWorldBossRawListeningHours(u.AbsUserID)
		if err != nil {
			log.Printf("⚠️ 世界Boss实时读取ABS失败: boss=%s user=%d err=%s", formatPlainValue(event.BossID), p.UserID, formatPlainError(err))
			totalDamage += p.Damage
			continue
		}

		maxDeltaHours := calculateWorldBossDamageWindowHours(*p, event, time.Now())
		damage, baseDamage, multiplier := calculateWorldBossDamage(p.BaseHours, finalHours, maxDeltaHours)
		damage, multiplier, err = applyWorldBossDamageBonusesChecked(p.UserID, baseDamage)
		if err != nil {
			log.Printf("⚠️ 世界Boss实时读取伤害加成失败: boss=%s user=%d err=%s", formatPlainValue(event.BossID), p.UserID, formatPlainError(err))
			totalDamage += p.Damage
			continue
		}

		deltaHours := math.Max(0, finalHours-p.BaseHours)
		if deltaHours > maxDeltaHours {
			deltaHours = maxDeltaHours
		}
		totalDamage += damage

		damageRes := DB.Model(&WorldBossParticipant{}).
			Where("id = ? AND boss_id = ? AND user_id = ?", p.ID, event.BossID, p.UserID).
			Updates(map[string]interface{}{
				"final_hours": finalHours,
				"delta_hours": deltaHours,
				"base_damage": baseDamage,
				"multiplier":  multiplier,
				"damage":      damage,
			})
		if damageRes.Error != nil {
			log.Printf("⚠️ 世界Boss实时伤害写入失败: boss=%s user=%d err=%s", formatPlainValue(event.BossID), p.UserID, formatPlainError(damageRes.Error))
		} else if damageRes.RowsAffected == 0 {
			log.Printf("⚠️ 世界Boss实时伤害写入未命中: boss=%s user=%d participant=%d", formatPlainValue(event.BossID), p.UserID, p.ID)
		}
	}
	totalDamage = roundWorldBossDamage(totalDamage)

	currentHP := float64(maxHP) - totalDamage
	if currentHP < 0 {
		currentHP = 0
	} else {
		currentHP = roundWorldBossDamage(currentHP)
	}
	isKilled := totalDamage >= float64(maxHP)

	event.MaxHP = maxHP
	event.CurrentHP = currentHP
	event.IsKilled = isKilled
	event.ParticipantCount = len(participants)

	eventRes := DB.Model(&WorldBossEvent{}).
		Where("boss_id = ? AND status = ?", event.BossID, "active").
		Updates(map[string]interface{}{
			"max_hp":            maxHP,
			"current_hp":        currentHP,
			"is_killed":         isKilled,
			"participant_count": len(participants),
		})
	if eventRes.Error != nil {
		return event, totalDamage, eventRes.Error
	}
	if eventRes.RowsAffected == 0 {
		return event, totalDamage, fmt.Errorf("WORLD_BOSS_ACTIVE_STATE_CHANGED")
	}

	return event, totalDamage, nil
}

func settleWorldBoss(bot *tgbotapi.BotAPI, bossID string) {
	res := DB.Model(&WorldBossEvent{}).
		Where("boss_id = ? AND status = ?", bossID, "active").
		Update("status", "settling")
	if res.Error != nil || res.RowsAffected == 0 {
		return
	}

	var event WorldBossEvent
	if err := DB.Where("boss_id = ?", bossID).First(&event).Error; err != nil {
		resetWorldBossToActive(bossID, err)
		return
	}

	var participants []WorldBossParticipant
	if err := DB.Where("boss_id = ?", bossID).Find(&participants).Error; err != nil {
		resetWorldBossToActive(bossID, err)
		return
	}
	event.MaxHP = calculateWorldBossMaxHP(len(participants))

	totalDamage := 0.0
	rewardParticipants := make([]WorldBossParticipant, 0, len(participants))
	for i := range participants {
		p := &participants[i]

		var u User
		if err := DB.Where("telegram_id = ?", p.UserID).First(&u).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := clearWorldBossParticipantComputedDamage(*p, "user_not_found"); err != nil {
					resetWorldBossToActive(bossID, err)
					return
				}
				continue
			}
			resetWorldBossToActive(bossID, err)
			return
		}
		if u.AbsUserID == "" {
			if err := clearWorldBossParticipantComputedDamage(*p, "abs_unbound"); err != nil {
				resetWorldBossToActive(bossID, err)
				return
			}
			continue
		}

		finalHours, err := getWorldBossRawListeningHours(u.AbsUserID)
		if err != nil {
			// ABS can be temporarily unavailable during settlement; keep the last live damage so rewards are not dropped.
			log.Printf("world boss settlement ABS read failed, keeping recorded damage: boss=%s user=%d damage=%.2f err=%s", formatPlainValue(bossID), p.UserID, p.Damage, formatPlainError(err))
			totalDamage += p.Damage
			rewardParticipants = append(rewardParticipants, *p)
			continue
		}

		// Use current wall-clock time; the helper clamps the window to the event end time.
		maxDeltaHours := calculateWorldBossDamageWindowHours(*p, event, time.Now())
		damage, baseDamage, multiplier := calculateWorldBossDamage(p.BaseHours, finalHours, maxDeltaHours)
		damage, multiplier, err = applyWorldBossDamageBonusesChecked(p.UserID, baseDamage)
		if err != nil {
			resetWorldBossToActive(bossID, err)
			return
		}

		p.FinalHours = finalHours
		p.DeltaHours = math.Max(0, finalHours-p.BaseHours)
		if p.DeltaHours > maxDeltaHours {
			p.DeltaHours = maxDeltaHours
		}
		p.BaseDamage = baseDamage
		p.Multiplier = multiplier
		p.Damage = damage

		totalDamage += damage
		damageRes := DB.Model(&WorldBossParticipant{}).
			Where("id = ? AND boss_id = ? AND user_id = ?", p.ID, bossID, p.UserID).
			Updates(map[string]interface{}{
				"final_hours": finalHours,
				"delta_hours": p.DeltaHours,
				"base_damage": baseDamage,
				"multiplier":  multiplier,
				"damage":      damage,
			})
		if damageRes.Error != nil {
			resetWorldBossToActive(bossID, damageRes.Error)
			return
		}
		if damageRes.RowsAffected == 0 {
			resetWorldBossToActive(bossID, fmt.Errorf("WORLD_BOSS_PARTICIPANT_DAMAGE_UPDATE_MISSED"))
			return
		}
		rewardParticipants = append(rewardParticipants, *p)
	}
	totalDamage = roundWorldBossDamage(totalDamage)

	killed := totalDamage >= float64(event.MaxHP)
	currentHP := float64(event.MaxHP) - totalDamage
	if currentHP < 0 {
		currentHP = 0
	} else {
		currentHP = roundWorldBossDamage(currentHP)
	}

	if err := grantWorldBossRewards(event, rewardParticipants, killed, currentHP); err != nil {
		log.Printf("❌ 世界Boss奖励结算失败: boss=%s err=%s", formatPlainValue(bossID), formatPlainError(err))
		resetWorldBossToActive(bossID, err)
		return
	}

	event.Status = "settled"
	event.CurrentHP = currentHP
	event.IsKilled = killed
	event.ParticipantCount = len(participants)

	if err := DB.Where("boss_id = ?", bossID).First(&event).Error; err == nil {
		ensureWorldBossLiveBoard(bot, event)
	} else {
		log.Printf("⚠️ 世界Boss结算后事件重读失败: boss=%s err=%s", formatPlainValue(bossID), formatPlainError(err))
	}
	sendWorldBossSettlement(bot, event, killed, totalDamage)
}

func clearWorldBossParticipantComputedDamage(p WorldBossParticipant, reason string) error {
	if p.ID == 0 || p.BossID == "" {
		return fmt.Errorf("WORLD_BOSS_PARTICIPANT_INVALID")
	}
	res := DB.Model(&WorldBossParticipant{}).
		Where("id = ? AND boss_id = ? AND user_id = ?", p.ID, p.BossID, p.UserID).
		Updates(map[string]interface{}{
			"final_hours": p.BaseHours,
			"delta_hours": 0,
			"base_damage": 0,
			"multiplier":  0,
			"damage":      0,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("WORLD_BOSS_PARTICIPANT_CLEAR_DAMAGE_MISSED")
	}
	log.Printf("world boss participant computed damage cleared: boss=%s user=%d reason=%s", formatPlainValue(p.BossID), p.UserID, formatPlainValue(reason))
	return nil
}

func resetWorldBossToActive(bossID string, reason error) {
	if reason != nil {
		log.Printf("⚠️ world boss settlement rolling back to active: boss=%s err=%s", formatPlainValue(bossID), formatPlainError(reason))
	}
	res := DB.Model(&WorldBossEvent{}).
		Where("boss_id = ? AND status = ?", bossID, "settling").
		Update("status", "active")
	if res.Error != nil {
		log.Printf("⚠️ world boss settlement rollback failed: boss=%s err=%s", formatPlainValue(bossID), formatPlainError(res.Error))
		return
	}
	if res.RowsAffected == 0 {
		log.Printf("⚠️ world boss settlement rollback missed active reset: boss=%s", formatPlainValue(bossID))
	}
}
func preciseWorldBossDamage(p WorldBossParticipant) float64 {
	if p.Damage < 0 {
		return 0
	}
	return p.Damage
}

func worldBossParticipantRankLess(left WorldBossParticipant, right WorldBossParticipant) bool {
	if left.Damage != right.Damage {
		return left.Damage > right.Damage
	}
	leftDamage := preciseWorldBossDamage(left)
	rightDamage := preciseWorldBossDamage(right)
	if leftDamage != rightDamage {
		return leftDamage > rightDamage
	}
	if left.DeltaHours != right.DeltaHours {
		return left.DeltaHours > right.DeltaHours
	}
	return left.CreatedAt.Before(right.CreatedAt)
}

func newWorldBossRedPacket(packetID string, event WorldBossEvent, now time.Time) RedPacket {
	return RedPacket{
		ID:          packetID,
		SenderID:    0,
		SenderName:  "世界Boss",
		TotalPoints: worldBossRedPacketTotalPoints,
		Count:       worldBossRedPacketCount,
		LeftCount:   worldBossRedPacketCount,
		LeftPoints:  worldBossRedPacketTotalPoints,
		CreatedAt:   now,
		RefType:     "world_boss",
		RefID:       event.BossID,
		ClaimScope:  redPacketClaimScopeWorldBossParticipant,
	}
}

func createWorldBossRedPacketInTx(tx *gorm.DB, packet *RedPacket) error {
	if tx == nil || packet == nil {
		return fmt.Errorf("WORLD_BOSS_REDPACKET_INVALID")
	}
	packet.ID = formatPlainValue(packet.ID)
	packet.SenderName = formatPlainValue(packet.SenderName)
	packet.RefType = formatPlainValue(packet.RefType)
	packet.RefID = formatPlainValue(packet.RefID)
	packet.ClaimScope = formatPlainValue(packet.ClaimScope)
	res := tx.Create(packet)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("WORLD_BOSS_REDPACKET_CREATE_MISSED")
	}
	return nil
}

func grantWorldBossRewards(event WorldBossEvent, participants []WorldBossParticipant, killed bool, currentHP float64) error {
	sort.Slice(participants, func(i, j int) bool {
		return worldBossParticipantRankLess(participants[i], participants[j])
	})

	// 击杀时的天道奖池 +10 注入必须与结算同事务、受 status='settling'→'settled'
	// 状态闸门保护，避免“已记结算但奖池注入因进程崩溃丢失”或后续重发结算
	// 消息导致重复注入。按项目锁序要求：先持 fusionPoolMutex，再开事务。
	return runFusionPoolLockedTransaction(func(tx *gorm.DB) error {
		if killed {
			packetID := "HB-WB-" + generateRandomCode(8)
			packet := newWorldBossRedPacket(packetID, event, time.Now())
			if err := createWorldBossRedPacketInTx(tx, &packet); err != nil {
				return err
			}
		}

		for idx, p := range participants {
			if p.Damage < worldBossValidDamage || p.IsRewarded {
				continue
			}

			points := 0
			contribution := 0
			prestige := 0

			if killed {
				points += 1
				contribution += 3

				switch idx {
				case 0:
					points += 6
					contribution += 10
					prestige += 3
				case 1:
					points += 4
					contribution += 7
					prestige += 2
				case 2:
					points += 2
					contribution += 5
					prestige += 1
				default:
					if idx < 10 {
						contribution += 3
					}
				}
			} else {
				contribution += 1
				if idx == 0 {
					contribution += 3
				} else if idx == 1 {
					contribution += 2
				} else if idx == 2 {
					contribution += 1
				}
			}

			if points > 0 {
				if err := applyPointDeltaInTx(
					tx,
					p.UserID,
					points,
					"world_boss_reward",
					fmt.Sprintf("世界Boss【%s】奖励，伤害 %.2f", worldBossPointDescriptionName(event.Name), p.Damage),
					"world_boss",
					event.BossID,
				); err != nil {
					return err
				}
			}

			if contribution > 0 || prestige > 0 {
				if err := awardWorldBossSectRewardTx(tx, p.UserID, contribution, prestige, event.BossID); err != nil {
					return err
				}
			}

			res := tx.Model(&WorldBossParticipant{}).
				Where("id = ? AND is_rewarded = ?", p.ID, false).
				Updates(map[string]interface{}{
					"reward_points":       points,
					"reward_contribution": contribution,
					"reward_prestige":     prestige,
					"is_rewarded":         true,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return fmt.Errorf("WORLD_BOSS_PARTICIPANT_REWARD_MARK_MISSED")
			}
		}

		settledAt := time.Now()
		res := tx.Model(&WorldBossEvent{}).
			Where("boss_id = ? AND status = ?", event.BossID, "settling").
			Updates(map[string]interface{}{
				"status":            "settled",
				"max_hp":            event.MaxHP,
				"current_hp":        currentHP,
				"is_killed":         killed,
				"participant_count": len(participants),
				"settled_at":        &settledAt,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("WORLD_BOSS_STATUS_CHANGED")
		}

		// 击杀奖励：天道奖池 +10，与结算同事务提交，受状态闸门保护。
		if killed {
			if _, _, err := addPointsToFusionPoolInTx(tx, 10); err != nil {
				return err
			}
		}

		return nil
	})
}

func awardWorldBossSectRewardTx(tx *gorm.DB, userID int64, contribution int, prestige int, bossID string) error {
	var member SectMember
	if err := tx.Where("user_id = ?", userID).First(&member).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}

	if contribution > 0 {
		res := tx.Model(&SectMember{}).
			Where("id = ?", member.ID).
			Updates(map[string]interface{}{
				"contribution":        gorm.Expr("contribution + ?", contribution),
				"weekly_contribution": gorm.Expr("weekly_contribution + ?", contribution),
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("WORLD_BOSS_SECT_MEMBER_REWARD_MISSED")
		}
	}

	if prestige > 0 {
		res := tx.Model(&Sect{}).
			Where("id = ?", member.SectID).
			UpdateColumn("prestige", gorm.Expr("prestige + ?", prestige))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("WORLD_BOSS_SECT_PRESTIGE_REWARD_MISSED")
		}
	}

	delta := contribution
	if delta == 0 {
		delta = prestige
	}

	var updatedMember SectMember
	if err := tx.Select("id", "contribution").
		Where("id = ?", member.ID).
		First(&updatedMember).Error; err != nil {
		return err
	}

	return createSectContributionLogInTx(tx, &SectContributionLog{
		SectID:       member.SectID,
		UserID:       userID,
		Delta:        delta,
		Reason:       fmt.Sprintf("世界Boss奖励：贡献 +%d，声望 +%d", contribution, prestige),
		RefType:      "world_boss",
		RefID:        bossID,
		BalanceAfter: updatedMember.Contribution,
	})
}

func handleJoinWorldBoss(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	event, eventErr := getActiveWorldBossChecked()
	if errors.Is(eventErr, gorm.ErrRecordNotFound) {
		replyText(bot, msg.Chat.ID, "📜 当前没有开放中的世界Boss。\n\n开放时间：每周六、周日 `21:00 - 22:00`。")
		return
	}
	if eventErr != nil {
		log.Printf("⚠️ 世界Boss参加活动读取失败: user=%d err=%s", msg.From.ID, formatPlainError(eventErr))
		replyText(bot, msg.Chat.ID, "❌ 世界Boss状态读取失败，请稍后重试。")
		return
	}
	now := time.Now()
	if !canJoinWorldBossAt(event, now) {
		replyText(bot, msg.Chat.ID, fmt.Sprintf(
			"⚠️ 本期世界Boss已进入最后 `%d` 分钟冲刺阶段，停止新道友加入。\n\n请等待下一场 Boss 降临。",
			int(worldBossJoinCloseBeforeEnd.Minutes()),
		))
		return
	}

	var u User
	if err := DB.Where("telegram_id = ?", msg.From.ID).First(&u).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("⚠️ 世界Boss参加读取本地档案失败: user=%d err=%s", msg.From.ID, formatPlainError(err))
			replyText(bot, msg.Chat.ID, "❌ 参加世界Boss读取本地档案失败，请稍后重试。")
			return
		}
		replyText(bot, msg.Chat.ID, "❌ 参加世界Boss需要先绑定有效 ABS 听书账号。")
		return
	}
	if u.AbsUserID == "" {
		replyText(bot, msg.Chat.ID, "❌ 参加世界Boss需要先绑定有效 ABS 听书账号。")
		return
	}
	usable, statusErr := userHasUsableLocalAbsAccountAt(u, now)
	if statusErr != nil {
		log.Printf("⚠️ 世界Boss参加状态读取失败: user=%d abs=%s err=%s", msg.From.ID, formatPlainValue(u.AbsUserID), formatPlainError(statusErr))
		replyText(bot, msg.Chat.ID, "❌ 账号状态读取失败，请稍后重试。")
		return
	}
	if !usable {
		replyText(bot, msg.Chat.ID, "❌ 参加世界Boss需要当前有效且未暂停的 ABS 听书账号。")
		return
	}

	baseHours, err := getWorldBossRawListeningHours(u.AbsUserID)
	if err != nil {
		replyText(bot, msg.Chat.ID, "❌ 读取当前实际听书时长失败，请稍后重试。")
		return
	}

	userName := getTelegramDisplayName(msg.From)
	_, err = createWorldBossParticipantInTx(DB, &WorldBossParticipant{
		BossID:    event.BossID,
		UserID:    msg.From.ID,
		UserName:  userName,
		BaseHours: baseHours,
	})
	if err != nil {
		replyText(bot, msg.Chat.ID, "❌ 参加失败，请稍后重试。")
		return
	}

	var p WorldBossParticipant
	baseHoursText := fmt.Sprintf("`%.1f` 小时", baseHours)
	if err := DB.Where("boss_id = ? AND user_id = ?", event.BossID, msg.From.ID).First(&p).Error; err != nil {
		log.Printf("⚠️ 世界Boss参与记录读取失败: boss=%s user=%d err=%s", formatPlainValue(event.BossID), msg.From.ID, formatPlainError(err))
		baseHoursText = "`读取失败`"
	} else {
		baseHoursText = fmt.Sprintf("`%.1f` 小时", p.BaseHours)
	}
	if refreshed, err := refreshWorldBossStoredHPByParticipants(event.BossID); err == nil {
		event = refreshed
	} else {
		log.Printf("⚠️ 世界Boss参与后血量刷新失败: boss=%s user=%d err=%s", formatPlainValue(event.BossID), msg.From.ID, formatPlainError(err))
	}

	ensureWorldBossLiveBoard(bot, event)

	replyText(bot, msg.Chat.ID, fmt.Sprintf(
		"✅ 已加入世界Boss讨伐。\n\nBoss：**%s**\n当前基线实际听书：%s\n当前血量：`%.2f/%d`\n参与人数：`%d`\n\n请在 `22:00` 前实际听书，结算时仅按参与后的 Boss 时段实际听书时长造成伤害。",
		escapeMarkdown(event.Name),
		baseHoursText,
		event.CurrentHP,
		event.MaxHP,
		event.ParticipantCount,
	))
}

func handleWorldBossStatus(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	event, eventErr := getActiveOrLatestWorldBossChecked()
	if errors.Is(eventErr, gorm.ErrRecordNotFound) {
		replyText(bot, msg.Chat.ID, "📜 暂无世界Boss记录。\n\n开放时间：每周六、周日 `21:00 - 22:00`。")
		return
	}
	if eventErr != nil {
		log.Printf("⚠️ 世界Boss状态活动读取失败: user=%d err=%s", msg.From.ID, formatPlainError(eventErr))
		replyText(bot, msg.Chat.ID, "📜 世界Boss状态暂时读取失败，请稍后重试。")
		return
	}
	if event.Status == "active" {
		if refreshed, _, err := refreshWorldBossLiveDamage(event); err == nil {
			event = refreshed
			ensureWorldBossLiveBoard(bot, event)
			if event.IsKilled {
				settleWorldBoss(bot, event.BossID)
				if latest, latestErr := getActiveOrLatestWorldBossChecked(); latestErr == nil {
					event = latest
				} else if latestErr != nil && !errors.Is(latestErr, gorm.ErrRecordNotFound) {
					log.Printf("⚠️ 世界Boss状态结算后活动重读失败: boss=%s user=%d err=%s", formatPlainValue(event.BossID), msg.From.ID, formatPlainError(latestErr))
				}
			}
		} else {
			log.Printf("⚠️ 世界Boss状态实时刷新失败: boss=%s err=%s", formatPlainValue(event.BossID), formatPlainError(err))
		}
	}

	statusText := "进行中"
	if event.Status == "active" && event.IsKilled {
		statusText = "已击杀，待结算"
	} else if event.Status == "settled" {
		if event.IsKilled {
			statusText = "已击杀"
		} else {
			statusText = "未击杀"
		}
	}

	replyText(bot, msg.Chat.ID, fmt.Sprintf(
		"🌑 **世界Boss状态**\n\nBoss：**%s**\n状态：`%s`\n血量：`%.2f/%d`\n有效门槛：最终伤害 `>= %.2f`\n参与人数：`%d`\n\n血量按实际参与人数动态调整：基础 `%d`，每人 `%d`，最低 `%d`，最高 `%d`。\n伤害 = 参与后 Boss 时段实际听书小时 × `%.0f` ×（1 + 修为加成 + 宗门科技），不计算白天净修为，无单人伤害上限。\n修为加成：炼气初期 `+1%%`，每小段 `+1%%`，最高 `+25%%`。\n开放时间：每周六、周日 `21:00 - 22:00`，最后 `%d` 分钟停止新道友加入。\n发送 `参加Boss` 加入，`Boss排行` 查看榜单。",
		escapeMarkdown(event.Name),
		statusText,
		event.CurrentHP,
		event.MaxHP,
		worldBossValidDamage,
		event.ParticipantCount,
		worldBossBaseHP,
		worldBossHPPerParticipant,
		worldBossMinHP,
		worldBossMaxHP,
		worldBossActualDamagePerHour,
		int(worldBossJoinCloseBeforeEnd.Minutes()),
	))
}

func handleWorldBossRank(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	event, eventErr := getActiveOrLatestWorldBossChecked()
	if eventErr != nil && !errors.Is(eventErr, gorm.ErrRecordNotFound) {
		log.Printf("⚠️ 世界Boss排行活动读取失败: user=%d err=%s", msg.From.ID, formatPlainError(eventErr))
		replyText(bot, msg.Chat.ID, "📜 世界Boss排行暂时读取失败，请稍后重试。")
		return
	}
	if errors.Is(eventErr, gorm.ErrRecordNotFound) {
		replyText(bot, msg.Chat.ID, "📜 暂无世界Boss记录。")
		return
	}
	if event.Status == "active" {
		if refreshed, _, err := refreshWorldBossLiveDamage(event); err == nil {
			event = refreshed
			ensureWorldBossLiveBoard(bot, event)
			if event.IsKilled {
				settleWorldBoss(bot, event.BossID)
				if latest, latestErr := getActiveOrLatestWorldBossChecked(); latestErr == nil {
					event = latest
				} else if latestErr != nil && !errors.Is(latestErr, gorm.ErrRecordNotFound) {
					log.Printf("⚠️ 世界Boss排行结算后活动重读失败: boss=%s user=%d err=%s", formatPlainValue(event.BossID), msg.From.ID, formatPlainError(latestErr))
				}
			}
		} else {
			log.Printf("⚠️ 世界Boss排行实时刷新失败: boss=%s err=%s", formatPlainValue(event.BossID), formatPlainError(err))
		}
	}

	participants, err := worldBossTopParticipants(event.BossID, 10)
	if err != nil {
		log.Printf("⚠️ 世界Boss排行读取失败: boss=%s err=%s", formatPlainValue(event.BossID), formatPlainError(err))
		replyText(bot, msg.Chat.ID, "📜 世界Boss排行暂时读取失败，请稍后重试。")
		return
	}

	if len(participants) == 0 {
		replyText(bot, msg.Chat.ID, "📜 当前世界Boss暂无参与者。")
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🏆 **世界Boss伤害榜 · %s**\n\n", escapeMarkdown(event.Name)))

	for i, p := range participants {
		preciseDamage := preciseWorldBossDamage(p)
		b.WriteString(fmt.Sprintf(
			"%d. `%s` 伤害：`%.2f`  实听：`%.0f`分  倍率：`%.2f`\n",
			i+1,
			escapeMarkdown(p.UserName),
			preciseDamage,
			p.DeltaHours*60,
			p.Multiplier,
		))
	}

	replyText(bot, msg.Chat.ID, b.String())
}

func sendWorldBossSettlement(bot *tgbotapi.BotAPI, event WorldBossEvent, killed bool, totalDamage float64) {
	var rankText strings.Builder
	participants, err := worldBossTopParticipants(event.BossID, 10)
	if err != nil {
		log.Printf("⚠️ 世界Boss结算排行读取失败: boss=%s err=%s", formatPlainValue(event.BossID), formatPlainError(err))
		rankText.WriteString("排行读取失败，请稍后发送 `Boss排行` 查看。\n")
	} else {
		for i, p := range participants {
			rankText.WriteString(fmt.Sprintf("%d. `%s` 伤害 `%.2f`\n", i+1, escapeMarkdown(p.UserName), p.Damage))
		}
		if rankText.Len() == 0 {
			rankText.WriteString("本期暂无有效参与记录。\n")
		}
	}

	resultText := "未能击杀"
	rewardText := "未击杀奖励：不发积分、不发红包、不注入天道奖池；有效参与者宗门贡献 +1，前三名额外贡献 +3/+2/+1。"
	if killed {
		resultText = "击杀成功"
		// 天道奖池 +10 已在 grantWorldBossRewards 的结算事务内完成注入，
		// 此处不再重复注入，避免重发结算消息时二次注水。
		rewardText = fmt.Sprintf("击杀奖励：Boss 红包 `%d` 积分 / `%d` 份，仅本期参与修士可抢，发送 `抢` 领取；天道奖池 `+10`；有效参与者积分 +1、宗门贡献 +3。", worldBossRedPacketTotalPoints, worldBossRedPacketCount)
	}

	notice := fmt.Sprintf(
		"🌑 **【世界Boss结算】** 🌑\n\n"+
			"Boss：**%s**\n"+
			"结果：**%s**\n"+
			"总伤害：`%.2f/%d`\n\n"+
			"本期伤害仅按参与后的 Boss 时段实际听书时长计算，不计白天净修为。\n\n"+
			"%s\n\n"+
			"🏆 **伤害排行 Top 10**\n%s\n"+
			"发送 `Boss排行` 可查看本期榜单。",
		escapeMarkdown(event.Name),
		resultText,
		totalDamage,
		event.MaxHP,
		rewardText,
		rankText.String(),
	)

	targetChatID := event.ChatID
	if targetChatID == 0 {
		targetChatID = AppConfig.NoticeGroupID
	}
	if targetChatID != 0 {
		sendGroupAutoDeleteMessage(bot, targetChatID, notice)
	}
}

func renderWorldBossLiveBoard(event WorldBossEvent) string {
	participants, participantsErr := worldBossTopParticipants(event.BossID, 10)
	if participantsErr != nil {
		log.Printf("⚠️ 世界Boss实时战榜排行读取失败: boss=%s err=%s", formatPlainValue(event.BossID), formatPlainError(participantsErr))
	}

	statusText := "进行中"
	if event.Status == "settling" {
		statusText = "结算中"
	} else if event.Status == "settled" {
		if event.IsKilled {
			statusText = "已击杀"
		} else {
			statusText = "未击杀"
		}
	} else if event.IsKilled {
		statusText = "已击杀，结算中"
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"🌑 **世界Boss实时战榜**\n\nBoss：**%s**\n状态：`%s`\n血量：`%.2f/%d`\n参与人数：`%d`\n规则：实听小时 × `%.0f` ×（1 + 修为加成 + 宗门科技），无单人伤害上限\n\n",
		escapeMarkdown(event.Name),
		statusText,
		event.CurrentHP,
		event.MaxHP,
		event.ParticipantCount,
		worldBossActualDamagePerHour,
	))

	if participantsErr != nil {
		b.WriteString("排行读取失败，请稍后发送 `Boss排行` 查看。\n")
		return b.String()
	}
	if len(participants) == 0 {
		b.WriteString("暂无道友参战。\n")
		return b.String()
	}

	for i, p := range participants {
		preciseDamage := preciseWorldBossDamage(p)
		b.WriteString(fmt.Sprintf(
			"%d. `%s` 伤害：`%.2f`  实听：`%.0f`分\n",
			i+1,
			escapeMarkdown(p.UserName),
			preciseDamage,
			p.DeltaHours*60,
		))
	}

	b.WriteString("\n每 2 分钟自动刷新；发送 `Boss排行` 可手动刷新。")
	return b.String()
}

func worldBossTopParticipants(bossID string, limit int) ([]WorldBossParticipant, error) {
	var participants []WorldBossParticipant
	if strings.TrimSpace(bossID) == "" {
		return participants, fmt.Errorf("WORLD_BOSS_ID_EMPTY")
	}
	query := DB.Where("boss_id = ?", bossID).Order(worldBossRankOrder)
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&participants).Error; err != nil {
		return nil, err
	}
	return participants, nil
}

func ensureWorldBossLiveBoard(bot *tgbotapi.BotAPI, event WorldBossEvent) {
	if bot == nil || event.ChatID == 0 || event.BossID == "" {
		return
	}

	bossID := event.BossID
	if enqueueTelegramAsync(telegramAsyncJob{
		Kind:      "world_boss_live_board",
		DedupeKey: "world_boss_live_board:" + bossID,
		Priority:  telegramAsyncPriorityLow,
		Send: func() error {
			return ensureWorldBossLiveBoardSync(bot, bossID)
		},
	}) {
		return
	}

	log.Printf("⚠️ 世界Boss实时战榜异步入队失败，改为同步刷新: boss=%s", formatPlainValue(bossID))
	if err := ensureWorldBossLiveBoardSync(bot, bossID); err != nil {
		log.Printf("⚠️ 世界Boss实时战榜同步刷新失败: boss=%s err=%s", formatPlainValue(bossID), formatTelegramSendError(err))
	}
}

func ensureWorldBossLiveBoardSync(bot *tgbotapi.BotAPI, bossID string) error {
	if bot == nil || bossID == "" {
		return nil
	}

	var event WorldBossEvent
	if err := DB.Where("boss_id = ?", bossID).First(&event).Error; err != nil {
		log.Printf("⚠️ 世界Boss实时战榜读取事件失败: boss=%s err=%s", formatPlainValue(bossID), formatPlainError(err))
		return nil
	}
	if event.ChatID == 0 {
		return nil
	}

	text := renderWorldBossLiveBoard(event)
	if event.BoardChatID != 0 && event.BoardMessageID != 0 {
		edit := tgbotapi.NewEditMessageText(event.BoardChatID, event.BoardMessageID, text)
		edit.ParseMode = "Markdown"
		if _, err := bot.Send(edit); err == nil {
			return nil
		} else if isTelegramMessageNotModifiedError(err) {
			return nil
		} else {
			log.Printf("⚠️ 世界Boss实时战榜编辑失败，将重发: boss=%s chat=%d message=%d err=%s", formatPlainValue(event.BossID), event.BoardChatID, event.BoardMessageID, formatTelegramSendError(err))
		}
	}

	msg := tgbotapi.NewMessage(event.ChatID, text)
	msg.ParseMode = "Markdown"
	sentMsg, err := sendNoAutoDelete(bot, msg)
	if err != nil {
		log.Printf("⚠️ 世界Boss实时战榜发送失败: boss=%s chat=%d err=%s", formatPlainValue(event.BossID), event.ChatID, formatTelegramSendError(err))
		return err
	}

	res := DB.Model(&WorldBossEvent{}).
		Where("boss_id = ?", event.BossID).
		Updates(map[string]interface{}{
			"board_chat_id":    sentMsg.Chat.ID,
			"board_message_id": sentMsg.MessageID,
		})
	if res.Error != nil {
		log.Printf("⚠️ 世界Boss实时战榜消息ID记录失败: boss=%s chat=%d message=%d err=%s", formatPlainValue(event.BossID), sentMsg.Chat.ID, sentMsg.MessageID, formatPlainError(res.Error))
	} else if res.RowsAffected == 0 {
		log.Printf("⚠️ 世界Boss实时战榜消息ID记录未命中: boss=%s chat=%d message=%d", formatPlainValue(event.BossID), sentMsg.Chat.ID, sentMsg.MessageID)
	}
	return nil
}

func getActiveWorldBoss() (WorldBossEvent, bool) {
	event, err := getActiveWorldBossChecked()
	return event, err == nil
}

func getActiveWorldBossChecked() (WorldBossEvent, error) {
	if DB == nil {
		return WorldBossEvent{}, fmt.Errorf("WORLD_BOSS_DB_UNAVAILABLE")
	}

	var event WorldBossEvent
	now := time.Now()
	err := DB.Where("status = ? AND start_at <= ? AND end_at > ?", "active", now, now).
		Order("start_at DESC").
		First(&event).Error
	if err != nil {
		return WorldBossEvent{}, err
	}
	return event, nil
}

func getActiveOrLatestWorldBoss() (WorldBossEvent, bool) {
	event, err := getActiveOrLatestWorldBossChecked()
	return event, err == nil
}

func getActiveOrLatestWorldBossChecked() (WorldBossEvent, error) {
	if event, err := getActiveWorldBossChecked(); err == nil {
		return event, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return WorldBossEvent{}, err
	}
	if DB == nil {
		return WorldBossEvent{}, fmt.Errorf("WORLD_BOSS_DB_UNAVAILABLE")
	}

	var event WorldBossEvent
	err := DB.Order("start_at DESC").First(&event).Error
	if err != nil {
		return WorldBossEvent{}, err
	}
	return event, nil
}

func calculateWorldBossDamage(baseHours float64, finalHours float64, maxDeltaHours float64) (float64, float64, float64) {
	deltaHours := finalHours - baseHours
	if deltaHours < 0 {
		deltaHours = 0
	}
	if maxDeltaHours < 0 {
		maxDeltaHours = 0
	}
	if deltaHours > maxDeltaHours {
		deltaHours = maxDeltaHours
	}

	baseDamage := deltaHours * worldBossActualDamagePerHour
	multiplier := 1.0
	damage := baseDamage
	if damage < 0 {
		damage = 0
	}

	return damage, baseDamage, multiplier
}

func getWorldBossCultivationDamageBonus(userID int64) float64 {
	bonus, err := getWorldBossCultivationDamageBonusChecked(userID)
	if err != nil {
		return 0
	}
	return bonus
}

func getWorldBossCultivationDamageBonusChecked(userID int64) (float64, error) {
	var cul Cultivation
	if err := DB.Where("user_id = ?", userID).First(&cul).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return 0, nil
		}
		return 0, err
	}
	SyncCultivationRealm(&cul)
	return calculateWorldBossCultivationDamageBonus(cul.MajorRealm, cul.MinorRealm), nil
}

func calculateWorldBossCultivationDamageBonus(major int, minor int) float64 {
	if major <= 0 {
		return 0
	}
	if minor < 1 {
		minor = 1
	}
	if minor > 4 {
		minor = 4
	}

	stage := (major-1)*4 + minor
	if stage < 1 {
		return 0
	}
	bonus := float64(stage) * worldBossCultivationBonusPerStage
	if bonus > worldBossCultivationBonusCap {
		return worldBossCultivationBonusCap
	}
	return bonus
}

func getWorldBossSectDamageBonus(userID int64) float64 {
	bonus, err := getWorldBossSectDamageBonusChecked(userID)
	if err != nil {
		return 0
	}
	return bonus
}

func getWorldBossSectDamageBonusChecked(userID int64) (float64, error) {
	level, err := getSectTechnologyLevelByUserChecked(userID, sectTechBossDamageBonus)
	if err != nil {
		return 0, err
	}
	if level <= 0 {
		return 0, nil
	}
	return float64(level) * 0.02, nil
}

func applyWorldBossDamageBonuses(userID int64, baseDamage float64) (float64, float64) {
	damage, multiplier, err := applyWorldBossDamageBonusesChecked(userID, baseDamage)
	if err != nil {
		return baseDamage, 1
	}
	return damage, multiplier
}

func applyWorldBossDamageBonusesChecked(userID int64, baseDamage float64) (float64, float64, error) {
	if baseDamage <= 0 {
		return 0, 1, nil
	}
	cultivationBonus, err := getWorldBossCultivationDamageBonusChecked(userID)
	if err != nil {
		return 0, 1, err
	}
	sectBonus, err := getWorldBossSectDamageBonusChecked(userID)
	if err != nil {
		return 0, 1, err
	}
	multiplier := 1 + cultivationBonus + sectBonus
	damage := baseDamage * multiplier
	if damage < 0 {
		return 0, multiplier, nil
	}
	return roundWorldBossDamage(damage), multiplier, nil
}

func roundWorldBossDamage(damage float64) float64 {
	return math.Round(damage*100) / 100
}

func calculateWorldBossDamageWindowHours(p WorldBossParticipant, event WorldBossEvent, now time.Time) float64 {
	start := p.CreatedAt
	if start.IsZero() || start.Before(event.StartAt) {
		start = event.StartAt
	}

	end := now
	if end.IsZero() || end.After(event.EndAt) {
		end = event.EndAt
	}
	if end.Before(start) {
		return 0
	}
	return end.Sub(start).Hours()
}

func getWorldBossRawListeningHours(absUserID string) (float64, error) {
	if absUserID == "" {
		return 0, errAbsUserIDEmpty
	}

	body, code, err := absClient.sendRequest("GET", absUserListeningStatsPath(absUserID), nil)
	if err != nil {
		return 0, err
	}
	if code != 200 {
		return 0, fmt.Errorf("ABS_STATUS_%d", code)
	}

	var stats struct {
		TotalTime     float64            `json:"totalTime"`
		TimeListening float64            `json:"timeListening"`
		Days          map[string]float64 `json:"days"`
	}
	if err := json.Unmarshal(body, &stats); err != nil {
		return 0, err
	}

	rawSeconds := stats.TotalTime
	if rawSeconds <= 0 {
		rawSeconds = stats.TimeListening
	}
	if rawSeconds <= 0 {
		for _, seconds := range stats.Days {
			if seconds > 0 {
				rawSeconds += seconds
			}
		}
	}
	if rawSeconds < 0 {
		rawSeconds = 0
	}
	return rawSeconds / 3600.0, nil
}

func updateCultivationAudioHours(userID int64, effectiveHours float64) {
	cul := GetOrCreateCultivation(userID)
	if cul == nil {
		log.Printf("⚠️ 世界 Boss 刷新修仙档案读取失败: user=%d", userID)
		return
	}
	if effectiveHours < 0 {
		effectiveHours = 0
	}

	oldHours := cul.TotalAudioTime
	cul.TotalAudioTime = effectiveHours
	persistCultivationAudioTime(userID, effectiveHours)
	SyncCultivationRealm(cul)

	awardSectListeningContribution(userID, oldHours, effectiveHours)
}
