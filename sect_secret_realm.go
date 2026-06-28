package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"math"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type SectSecretRealmEvent struct {
	gorm.Model

	RealmID string `gorm:"index;not null"`
	SectID  int64  `gorm:"index;not null"`
	ChatID  int64  `gorm:"index"`

	Name           string
	ProfileKey     string `gorm:"index"`
	ProfileName    string
	ConfigSnapshot string
	Status         string `gorm:"index;not null"` // active / settling / settled

	OpenedByID   int64 `gorm:"index;not null"`
	OpenedByName string

	StartAt time.Time `gorm:"index;not null"`
	EndAt   time.Time `gorm:"index;not null"`

	FundsCost int

	ParticipantCount        int
	TotalDeltaHours         float64
	TotalRewardPoints       int
	TotalRewardContribution int
	TotalRewardPrestige     int
	TotalRewardDrops        int

	GuardianUserID       int64
	GuardianName         string
	GuardianMajorRealm   int
	GuardianMinorRealm   int
	GuardianBonusPercent int

	SettledAt *time.Time

	BoardChatID    int64
	BoardMessageID int
}

func (SectSecretRealmEvent) TableName() string {
	return "sect_secret_realm_events"
}

func createSectSecretRealmEventInTx(tx *gorm.DB, event *SectSecretRealmEvent) error {
	if tx == nil || event == nil {
		return fmt.Errorf("SECT_SECRET_REALM_EVENT_INVALID")
	}
	entry := *event
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SECT_SECRET_REALM_EVENT_CREATE_MISSED")
	}
	*event = entry
	return nil
}

func sectSecretRealmPointDescriptionName(name string) string {
	return lotteryDisplayText(name, 80, "-")
}

type SectSecretRealmParticipant struct {
	gorm.Model

	RealmID string `gorm:"index;not null"`
	SectID  int64  `gorm:"index;not null"`
	UserID  int64  `gorm:"index;not null"`

	UserName   string
	MajorRealm int
	MinorRealm int

	BaseHours               float64
	FinalHours              float64
	DeltaHours              float64
	BaseRawSeconds          float64
	FinalRawSeconds         float64
	RawDeltaSeconds         float64
	ObservedRawDeltaSeconds float64
	WallClockCapSeconds     float64
	SuppressedHours         float64
	RawCapped               bool

	RewardPoints             int
	RewardContribution       int
	RewardPrestige           int
	PointBonusPercent        int
	ContributionBonusPercent int
	PrestigeBonusPercent     int
	GuardianBonusPercent     int
	RewardDropItem           string
	RewardDropQuantity       int

	IsRewarded bool `gorm:"default:false"`
}

func (SectSecretRealmParticipant) TableName() string {
	return "sect_secret_realm_participants"
}

func createSectSecretRealmParticipantIfMissingInTx(tx *gorm.DB, participant *SectSecretRealmParticipant) error {
	if tx == nil || participant == nil {
		return fmt.Errorf("SECT_SECRET_REALM_PARTICIPANT_INVALID")
	}
	entry := *participant
	entry.RealmID = formatPlainValue(entry.RealmID)
	entry.UserName = formatPlainValue(entry.UserName)
	if entry.RealmID == "" || entry.SectID == 0 || entry.UserID == 0 {
		return fmt.Errorf("SECT_SECRET_REALM_PARTICIPANT_INVALID")
	}
	res := tx.Clauses(sectSecretRealmParticipantOnConflict()).Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	*participant = entry
	return nil
}

const (
	sectSecretRealmName = "听潮秘境"

	sectSecretRealmDuration = 2 * time.Hour

	sectSecretRealmBaseCost     = 100
	sectSecretRealmCostPerLevel = 30

	sectSecretRealmMinDeltaHours          = 0.5
	sectSecretRealmHighMinDeltaHours      = 0.75
	sectSecretRealmLimitedMinDeltaHours   = 0.5
	sectSecretRealmMaxRewardPoints        = 18
	sectSecretRealmHighMaxRewardPoints    = 30
	sectSecretRealmLimitedMaxRewardPoints = 20
	sectSecretRealmMaxRewardContribution  = 12
	sectSecretRealmMaxRewardPrestige      = 10

	sectSecretRealmWeeklyOpenLimit = 2
	sectSecretRealmNormalPointRate = 6.0
	sectSecretRealmHighPointRate   = 7.0

	sectSecretRealmPressureFullHours    = 2.0
	sectSecretRealmPressureAfterRate    = 0.5
	sectSecretRealmLiveRefreshInterval  = 2 * time.Minute
	sectSecretRealmSettlementStaleAfter = 30 * time.Minute

	sectSecretRealmTokenItemName = "秘境信物"
)

var sectSecretRealmRankOrder = "delta_hours DESC, raw_delta_seconds DESC, created_at ASC"

type sectSecretRealmListeningSnapshot struct {
	EffectiveHours float64
	RawSeconds     float64
}

type sectSecretRealmRewardMultiplier struct {
	PointPercent        int
	ContributionPercent int
	PrestigePercent     int
}

type sectSecretRealmDropRule struct {
	ItemName      string
	Quantity      int
	ChancePercent int
}

type sectSecretRealmRawDeltaDetail struct {
	ObservedDeltaSeconds float64
	WallClockCapSeconds  float64
	DeltaSeconds         float64
	WasCapped            bool
}

func HandleSectSecretRealmCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) bool {
	if msg == nil || msg.From == nil {
		return false
	}

	text = strings.TrimSpace(text)
	action, profileKey, args, ok := parseSectSecretRealmCommand(text)
	if !ok {
		return false
	}

	registerIncomingGroupCommandForAutoDelete(msg)
	settleExpiredSectSecretRealms(bot, time.Now())

	switch action {
	case "status":
		handleSectSecretRealmStatus(bot, msg)
	case "open":
		handleOpenSectSecretRealm(bot, msg, profileKey, false)
	case "confirm_open":
		handleOpenSectSecretRealm(bot, msg, profileKey, true)
	case "join":
		handleJoinSectSecretRealm(bot, msg)
	case "settle":
		handleSettleSectSecretRealmCommand(bot, msg)
	case "rank":
		handleSectSecretRealmRank(bot, msg)
	case "detail":
		handleSectSecretRealmDetail(bot, msg, args)
	}

	return true
}

func parseSectSecretRealmCommand(text string) (action string, profileKey string, args []string, ok bool) {
	text = strings.TrimSpace(text)
	switch text {
	case "宗门秘境":
		return "status", sectSecretRealmProfileNormal, nil, true
	case "开启宗门秘境", "开启普通宗门秘境":
		return "open", sectSecretRealmProfileNormal, nil, true
	case "确认开启宗门秘境", "确认开启普通宗门秘境":
		return "confirm_open", sectSecretRealmProfileNormal, nil, true
	case "开启高阶宗门秘境":
		return "open", sectSecretRealmProfileHigh, nil, true
	case "确认开启高阶宗门秘境":
		return "confirm_open", sectSecretRealmProfileHigh, nil, true
	case "开启限时宗门秘境":
		return "open", sectSecretRealmProfileLimited, nil, true
	case "确认开启限时宗门秘境":
		return "confirm_open", sectSecretRealmProfileLimited, nil, true
	case "进入宗门秘境":
		return "join", sectSecretRealmProfileNormal, nil, true
	case "结算宗门秘境":
		return "settle", sectSecretRealmProfileNormal, nil, true
	case "宗门秘境排行":
		return "rank", sectSecretRealmProfileNormal, nil, true
	}
	if strings.HasPrefix(text, "宗门秘境明细") {
		return "detail", sectSecretRealmProfileNormal, strings.Fields(strings.TrimSpace(strings.TrimPrefix(text, "宗门秘境明细"))), true
	}
	return "", "", nil, false
}

func StartSectSecretRealmScheduler(bot *tgbotapi.BotAPI) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for now := range ticker.C {
			go refreshActiveSectSecretRealms(bot, now)
			settleExpiredSectSecretRealms(bot, now)
		}
	}()

	log.Println("✅ 宗门秘境调度器已启动：每分钟巡检过期秘境")
}

func getSectSecretRealmCost(level int) int {
	profile, _ := defaultSectSecretRealmConfig().profile(sectSecretRealmProfileNormal)
	return getSectSecretRealmProfileCost(profile, level)
}

func getSectSecretRealmProfileCost(profile SectSecretRealmProfileConfig, level int) int {
	if level < 1 {
		level = 1
	}
	cost := profile.BaseCost + level*profile.CostPerLevel
	if cost < 0 {
		return 0
	}
	return cost
}

func sectSecretRealmProfileDuration(profile SectSecretRealmProfileConfig) time.Duration {
	if profile.DurationMinutes <= 0 {
		return sectSecretRealmDuration
	}
	return time.Duration(profile.DurationMinutes) * time.Minute
}

func canOperateSectSecretRealm(role string) bool {
	return role == sectRoleOwner || role == sectRoleElder
}

func sumSectSecretRealmRawListeningSeconds(days map[string]float64) float64 {
	total := 0.0
	for _, seconds := range days {
		if seconds > 0 {
			total += seconds
		}
	}
	if total < 0 {
		return 0
	}
	return total
}

func sectSecretRealmCapEnd(eventEnd time.Time, settledAt time.Time) time.Time {
	if eventEnd.IsZero() {
		return settledAt
	}
	if settledAt.IsZero() || eventEnd.Before(settledAt) {
		return eventEnd
	}
	return settledAt
}

func sectSecretRealmWallClockCapSeconds(joinedAt time.Time, eventStart time.Time, eventEnd time.Time, settledAt time.Time) float64 {
	start := joinedAt
	if start.IsZero() || start.Before(eventStart) {
		start = eventStart
	}
	end := sectSecretRealmCapEnd(eventEnd, settledAt)
	if end.IsZero() || !end.After(start) {
		return 0
	}
	return end.Sub(start).Seconds()
}

func calculateSectSecretRealmRawDeltaSeconds(baseRawSeconds float64, finalRawSeconds float64, joinedAt time.Time, eventStart time.Time, eventEnd time.Time, settledAt time.Time) float64 {
	return calculateSectSecretRealmRawDeltaDetail(baseRawSeconds, finalRawSeconds, joinedAt, eventStart, eventEnd, settledAt).DeltaSeconds
}

func calculateSectSecretRealmRawDeltaDetail(baseRawSeconds float64, finalRawSeconds float64, joinedAt time.Time, eventStart time.Time, eventEnd time.Time, settledAt time.Time) sectSecretRealmRawDeltaDetail {
	if baseRawSeconds < 0 {
		baseRawSeconds = 0
	}
	if finalRawSeconds < 0 {
		finalRawSeconds = 0
	}
	delta := finalRawSeconds - baseRawSeconds
	capSeconds := sectSecretRealmWallClockCapSeconds(joinedAt, eventStart, eventEnd, settledAt)
	detail := sectSecretRealmRawDeltaDetail{
		ObservedDeltaSeconds: delta,
		WallClockCapSeconds:  capSeconds,
	}
	if delta <= 0 {
		return detail
	}
	if delta > capSeconds {
		detail.WasCapped = true
		if capSeconds > 0 {
			detail.DeltaSeconds = capSeconds
		}
		return detail
	}
	detail.DeltaSeconds = delta
	return detail
}

func calculateSectSecretRealmSuppressedHours(rawDeltaSeconds float64) float64 {
	profile, _ := defaultSectSecretRealmConfig().profile(sectSecretRealmProfileNormal)
	return calculateSectSecretRealmSuppressedHoursForProfile(rawDeltaSeconds, profile)
}

func calculateSectSecretRealmSuppressedHoursForProfile(rawDeltaSeconds float64, profile SectSecretRealmProfileConfig) float64 {
	if rawDeltaSeconds <= 0 {
		return 0
	}
	rawHours := rawDeltaSeconds / 3600.0
	if rawHours <= profile.PressureFullHours {
		return rawHours
	}
	return profile.PressureFullHours + (rawHours-profile.PressureFullHours)*profile.PressureAfterRate
}

func sectSecretRealmGuardianBonusPercentForMajor(major int) int {
	profile, _ := defaultSectSecretRealmConfig().profile(sectSecretRealmProfileNormal)
	return sectSecretRealmGuardianBonusPercentForProfile(major, profile)
}

func sectSecretRealmGuardianBonusPercentForProfile(major int, profile SectSecretRealmProfileConfig) int {
	bonus := 0
	for _, rule := range profile.GuardianBonuses {
		if major >= rule.MinMajorRealm && rule.BonusPercent > bonus {
			bonus = rule.BonusPercent
		}
	}
	return bonus
}

func applySectSecretRealmHourBonus(hours float64, bonusPercent int) float64 {
	if hours <= 0 {
		return 0
	}
	if bonusPercent <= 0 {
		return hours
	}
	return hours * float64(100+bonusPercent) / 100.0
}

func sectSecretRealmGuardian(participants []SectSecretRealmParticipant) (SectSecretRealmParticipant, int) {
	profile, _ := defaultSectSecretRealmConfig().profile(sectSecretRealmProfileNormal)
	return sectSecretRealmGuardianForProfile(participants, profile)
}

func sectSecretRealmGuardianForProfile(participants []SectSecretRealmParticipant, profile SectSecretRealmProfileConfig) (SectSecretRealmParticipant, int) {
	var guardian SectSecretRealmParticipant
	for _, p := range participants {
		if sectSecretRealmGuardianBonusPercentForProfile(p.MajorRealm, profile) <= 0 {
			continue
		}
		if guardian.UserID == 0 ||
			p.MajorRealm > guardian.MajorRealm ||
			(p.MajorRealm == guardian.MajorRealm && p.MinorRealm > guardian.MinorRealm) ||
			(p.MajorRealm == guardian.MajorRealm && p.MinorRealm == guardian.MinorRealm && p.CreatedAt.Before(guardian.CreatedAt)) {
			guardian = p
		}
	}
	if guardian.UserID == 0 {
		return SectSecretRealmParticipant{}, 0
	}
	return guardian, sectSecretRealmGuardianBonusPercentForProfile(guardian.MajorRealm, profile)
}

func sectSecretRealmRewardMultiplierForMajor(major int) sectSecretRealmRewardMultiplier {
	profile, _ := defaultSectSecretRealmConfig().profile(sectSecretRealmProfileNormal)
	return sectSecretRealmRewardMultiplierForProfile(major, profile)
}

func sectSecretRealmRewardMultiplierForProfile(major int, profile SectSecretRealmProfileConfig) sectSecretRealmRewardMultiplier {
	multiplier := sectSecretRealmRewardMultiplier{PointPercent: 100, ContributionPercent: 100, PrestigePercent: 100}
	for _, rule := range profile.Multipliers {
		if major >= rule.MinMajorRealm {
			multiplier = sectSecretRealmRewardMultiplier{
				PointPercent:        rule.PointPercent,
				ContributionPercent: rule.ContributionPercent,
				PrestigePercent:     rule.PrestigePercent,
			}
		}
	}
	return multiplier
}

func sectSecretRealmDropRuleForMajor(major int) (sectSecretRealmDropRule, bool) {
	profile, _ := defaultSectSecretRealmConfig().profile(sectSecretRealmProfileNormal)
	return sectSecretRealmDropRuleForProfile(major, profile)
}

func sectSecretRealmDropRuleForProfile(major int, profile SectSecretRealmProfileConfig) (sectSecretRealmDropRule, bool) {
	var selected sectSecretRealmDropRule
	ok := false
	for _, rule := range profile.DropRules {
		if major >= rule.MinMajorRealm {
			selected = sectSecretRealmDropRule{
				ItemName:      rule.ItemName,
				Quantity:      rule.Quantity,
				ChancePercent: rule.ChancePercent,
			}
			ok = true
		}
	}
	if !ok || selected.ItemName == "" || selected.Quantity <= 0 || selected.ChancePercent <= 0 {
		return sectSecretRealmDropRule{}, false
	}
	return selected, true
}

func sectSecretRealmStableDropScore(realmID string, userID int64, major int) int {
	h := fnv.New32a()
	_, _ = h.Write([]byte(fmt.Sprintf("%s:%d:%d", strings.TrimSpace(realmID), userID, major)))
	return int(h.Sum32() % 100)
}

func sectSecretRealmDropForScore(major int, deltaHours float64, score int) (string, int) {
	profile, _ := defaultSectSecretRealmConfig().profile(sectSecretRealmProfileNormal)
	return sectSecretRealmDropForScoreWithProfile(major, deltaHours, score, profile)
}

func sectSecretRealmDropForScoreWithProfile(major int, deltaHours float64, score int, profile SectSecretRealmProfileConfig) (string, int) {
	if deltaHours+0.000001 < profile.MinDeltaHours {
		return "", 0
	}
	rule, ok := sectSecretRealmDropRuleForProfile(major, profile)
	if !ok || rule.ItemName == "" || rule.Quantity <= 0 || rule.ChancePercent <= 0 {
		return "", 0
	}
	if score < 0 {
		score = 0
	}
	score = score % 100
	if score >= rule.ChancePercent {
		return "", 0
	}
	return rule.ItemName, rule.Quantity
}

func sectSecretRealmDropForParticipant(realmID string, userID int64, major int, deltaHours float64) (string, int) {
	profile, _ := defaultSectSecretRealmConfig().profile(sectSecretRealmProfileNormal)
	return sectSecretRealmDropForParticipantWithProfile(realmID, userID, major, deltaHours, profile)
}

func sectSecretRealmDropForParticipantWithProfile(realmID string, userID int64, major int, deltaHours float64, profile SectSecretRealmProfileConfig) (string, int) {
	score := sectSecretRealmStableDropScore(realmID, userID, major)
	return sectSecretRealmDropForScoreWithProfile(major, deltaHours, score, profile)
}

func sectSecretRealmRealmMarkdown(major int, minor int) string {
	return escapeMarkdown(GetRealmName(&Cultivation{MajorRealm: major, MinorRealm: minor}))
}

func sectSecretRealmDropMarkdown(itemName string, quantity int) string {
	itemName = strings.TrimSpace(itemName)
	if itemName == "" || quantity <= 0 {
		return "无"
	}
	return fmt.Sprintf("%s x`%d`", inventoryItemMarkdownName(itemName), quantity)
}

func sectSecretRealmGuardianSummaryMarkdown(event SectSecretRealmEvent) string {
	if event.GuardianUserID == 0 || event.GuardianBonusPercent <= 0 {
		return "护道者：无\n"
	}
	return fmt.Sprintf(
		"护道者：`%s`（%s，加持 +`%d%%`）\n",
		escapeMarkdown(event.GuardianName),
		sectSecretRealmRealmMarkdown(event.GuardianMajorRealm, event.GuardianMinorRealm),
		event.GuardianBonusPercent,
	)
}

func applySectSecretRealmRewardMultiplier(base int, percent int) int {
	if base <= 0 {
		return 0
	}
	if percent <= 0 {
		percent = 100
	}
	return int(math.Floor(float64(base) * float64(percent) / 100.0))
}

func refreshSectSecretRealmLiveProgress(event SectSecretRealmEvent) (SectSecretRealmEvent, error) {
	if event.RealmID == "" || event.Status != "active" {
		return event, nil
	}

	profile, err := sectSecretRealmProfileFromSnapshotChecked(event.ProfileKey, event.ConfigSnapshot)
	if err != nil {
		return event, err
	}
	var participants []SectSecretRealmParticipant
	if err := DB.Where("realm_id = ?", event.RealmID).Find(&participants).Error; err != nil {
		return event, err
	}

	refreshAt := time.Now()
	processed := make(map[uint]bool, len(participants))
	for i := range participants {
		p := &participants[i]

		var u User
		if err := DB.Where("telegram_id = ?", p.UserID).First(&u).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 宗门秘境实时刷新读取本地档案失败: realm=%s user=%d err=%s", formatPlainValue(event.RealmID), p.UserID, formatPlainError(err))
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				if err := clearSectSecretRealmParticipantComputedReward(*p, "user_not_found"); err != nil {
					return event, err
				}
			}
			continue
		}
		if strings.TrimSpace(u.AbsUserID) == "" {
			if err := clearSectSecretRealmParticipantComputedReward(*p, "abs_unbound"); err != nil {
				return event, err
			}
			continue
		}

		finalSnapshot, err := getSectSecretRealmListeningSnapshot(u.AbsUserID)
		if err != nil {
			log.Printf("⚠️ 宗门秘境实时刷新读取 ABS 失败: realm=%s user=%d err=%s", formatPlainValue(event.RealmID), p.UserID, formatPlainError(err))
			continue
		}

		updateCultivationAudioHours(p.UserID, finalSnapshot.EffectiveHours)
		p.MajorRealm, p.MinorRealm = sectSecretRealmCultivationSnapshot(p.UserID)
		p.FinalHours = finalSnapshot.EffectiveHours
		p.FinalRawSeconds = finalSnapshot.RawSeconds
		if p.BaseRawSeconds > 0 || p.BaseHours == 0 {
			detail := calculateSectSecretRealmRawDeltaDetail(p.BaseRawSeconds, finalSnapshot.RawSeconds, p.CreatedAt, event.StartAt, event.EndAt, refreshAt)
			p.ObservedRawDeltaSeconds = detail.ObservedDeltaSeconds
			p.WallClockCapSeconds = detail.WallClockCapSeconds
			p.RawDeltaSeconds = detail.DeltaSeconds
			p.RawCapped = detail.WasCapped
		} else {
			legacyDeltaHours := math.Max(0, p.FinalHours-p.BaseHours)
			p.ObservedRawDeltaSeconds = legacyDeltaHours * 3600
			p.WallClockCapSeconds = sectSecretRealmWallClockCapSeconds(p.CreatedAt, event.StartAt, event.EndAt, refreshAt)
			p.RawDeltaSeconds = p.ObservedRawDeltaSeconds
			if p.WallClockCapSeconds > 0 && p.RawDeltaSeconds > p.WallClockCapSeconds {
				p.RawDeltaSeconds = p.WallClockCapSeconds
				p.RawCapped = true
			}
		}
		processed[p.ID] = true
	}

	eligibleParticipants := make([]SectSecretRealmParticipant, 0, len(participants))
	for i := range participants {
		if processed[participants[i].ID] {
			eligibleParticipants = append(eligibleParticipants, participants[i])
		}
	}

	guardian, guardianBonusPercent := sectSecretRealmGuardianForProfile(eligibleParticipants, profile)
	event.GuardianBonusPercent = guardianBonusPercent
	if guardian.UserID != 0 {
		event.GuardianUserID = guardian.UserID
		event.GuardianName = guardian.UserName
		event.GuardianMajorRealm = guardian.MajorRealm
		event.GuardianMinorRealm = guardian.MinorRealm
	} else {
		event.GuardianUserID = 0
		event.GuardianName = ""
		event.GuardianMajorRealm = 0
		event.GuardianMinorRealm = 0
	}

	totalDeltaHours := 0.0
	totalPoints := 0
	totalContribution := 0
	totalPrestige := 0
	totalDrops := 0
	for i := range participants {
		p := &participants[i]
		if !processed[p.ID] {
			continue
		}
		if p.BaseRawSeconds > 0 || p.BaseHours == 0 {
			p.SuppressedHours = calculateSectSecretRealmSuppressedHoursForProfile(p.RawDeltaSeconds, profile)
		} else {
			p.SuppressedHours = p.RawDeltaSeconds / 3600
		}
		p.DeltaHours = applySectSecretRealmHourBonus(p.SuppressedHours, guardianBonusPercent)
		p.GuardianBonusPercent = guardianBonusPercent
		multiplier := sectSecretRealmRewardMultiplierForProfile(p.MajorRealm, profile)
		p.PointBonusPercent = sectSecretRealmPointRealmBonusForProfile(p.MajorRealm, profile)
		p.ContributionBonusPercent = multiplier.ContributionPercent - 100
		p.PrestigeBonusPercent = multiplier.PrestigePercent - 100
		p.RewardPoints, p.RewardContribution, p.RewardPrestige = calculateSectSecretRealmRewardsForProfile(p.DeltaHours, p.MajorRealm, profile)
		p.RewardDropItem, p.RewardDropQuantity = sectSecretRealmDropForParticipantWithProfile(event.RealmID, p.UserID, p.MajorRealm, p.DeltaHours, profile)

		res := DB.Model(&SectSecretRealmParticipant{}).
			Where("id = ? AND EXISTS (SELECT 1 FROM sect_secret_realm_events WHERE realm_id = ? AND status = ?)", p.ID, event.RealmID, "active").
			Updates(map[string]interface{}{
				"major_realm":                p.MajorRealm,
				"minor_realm":                p.MinorRealm,
				"final_hours":                p.FinalHours,
				"final_raw_seconds":          p.FinalRawSeconds,
				"observed_raw_delta_seconds": p.ObservedRawDeltaSeconds,
				"wall_clock_cap_seconds":     p.WallClockCapSeconds,
				"raw_delta_seconds":          p.RawDeltaSeconds,
				"suppressed_hours":           p.SuppressedHours,
				"delta_hours":                p.DeltaHours,
				"raw_capped":                 p.RawCapped,
				"reward_points":              p.RewardPoints,
				"reward_contribution":        p.RewardContribution,
				"reward_prestige":            p.RewardPrestige,
				"point_bonus_percent":        p.PointBonusPercent,
				"contribution_bonus_percent": p.ContributionBonusPercent,
				"prestige_bonus_percent":     p.PrestigeBonusPercent,
				"guardian_bonus_percent":     p.GuardianBonusPercent,
				"reward_drop_item":           p.RewardDropItem,
				"reward_drop_quantity":       p.RewardDropQuantity,
			})
		if res.Error != nil {
			log.Printf("⚠️ 宗门秘境实时进度写入失败: realm=%s user=%d err=%s", formatPlainValue(event.RealmID), p.UserID, formatPlainError(res.Error))
			continue
		}
		if res.RowsAffected == 0 {
			continue
		}

		totalDeltaHours += p.DeltaHours
		totalPoints += p.RewardPoints
		totalContribution += p.RewardContribution
		totalPrestige += p.RewardPrestige
		totalDrops += p.RewardDropQuantity
	}

	event.ParticipantCount = len(participants)
	event.TotalDeltaHours = totalDeltaHours
	event.TotalRewardPoints = totalPoints
	event.TotalRewardContribution = totalContribution
	event.TotalRewardPrestige = totalPrestige
	event.TotalRewardDrops = totalDrops

	res := DB.Model(&SectSecretRealmEvent{}).
		Where("realm_id = ? AND status = ?", event.RealmID, "active").
		Updates(map[string]interface{}{
			"participant_count":         event.ParticipantCount,
			"total_delta_hours":         event.TotalDeltaHours,
			"total_reward_points":       event.TotalRewardPoints,
			"total_reward_contribution": event.TotalRewardContribution,
			"total_reward_prestige":     event.TotalRewardPrestige,
			"total_reward_drops":        event.TotalRewardDrops,
			"guardian_user_id":          event.GuardianUserID,
			"guardian_name":             event.GuardianName,
			"guardian_major_realm":      event.GuardianMajorRealm,
			"guardian_minor_realm":      event.GuardianMinorRealm,
			"guardian_bonus_percent":    event.GuardianBonusPercent,
		})
	if res.Error != nil {
		return event, res.Error
	}
	if res.RowsAffected == 0 {
		return event, fmt.Errorf("SECT_SECRET_REALM_EVENT_REFRESH_MISSED")
	}

	return event, nil
}

func sectSecretRealmCultivationSnapshot(userID int64) (int, int) {
	cul := GetOrCreateCultivation(userID)
	if cul == nil {
		return 0, 1
	}
	return cul.MajorRealm, cul.MinorRealm
}

func sectSecretRealmOpenErrorCode(err error) string {
	switch {
	case errors.Is(err, errNotInSect):
		return "NOT_IN_SECT"
	case errors.Is(err, errSectNoPermission):
		return "NO_PERMISSION"
	case errors.Is(err, errSectSecretRealmAlreadyActive):
		return "REALM_ALREADY_ACTIVE"
	case errors.Is(err, errSectSecretRealmWeeklyLimit):
		return "REALM_WEEKLY_LIMIT"
	case errors.Is(err, errSectFundsNotEnough):
		return "FUNDS_NOT_ENOUGH"
	case err != nil:
		return fallbackBusinessErrorCode(err)
	default:
		return ""
	}
}

func sectSecretRealmWeeklyOpenWindow(now time.Time) (time.Time, time.Time) {
	start := sectWeekStart(now)
	return start, start.AddDate(0, 0, 7)
}

func countSectSecretRealmWeeklyOpenTx(tx *gorm.DB, sectID int64, now time.Time) (int64, error) {
	if tx == nil {
		tx = DB
	}
	if tx == nil || sectID == 0 {
		return 0, nil
	}

	start, end := sectSecretRealmWeeklyOpenWindow(now)
	var count int64
	err := tx.Model(&SectSecretRealmEvent{}).
		Where("sect_id = ? AND created_at >= ? AND created_at < ?", sectID, start, end).
		Count(&count).Error
	return count, err
}

func getActiveSectSecretRealmTx(tx *gorm.DB, sectID int64, now time.Time) (SectSecretRealmEvent, bool) {
	event, err := getActiveSectSecretRealmTxChecked(tx, sectID, now)
	return event, err == nil
}

func getActiveSectSecretRealmTxChecked(tx *gorm.DB, sectID int64, now time.Time) (SectSecretRealmEvent, error) {
	if tx == nil {
		tx = DB
	}
	if tx == nil {
		return SectSecretRealmEvent{}, fmt.Errorf("SECT_SECRET_REALM_DB_UNAVAILABLE")
	}

	var event SectSecretRealmEvent
	err := tx.Where("sect_id = ? AND status = ? AND start_at <= ? AND end_at > ?", sectID, "active", now, now).
		Order("start_at DESC").
		First(&event).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return SectSecretRealmEvent{}, errSectSecretRealmNotActive
		}
		return SectSecretRealmEvent{}, err
	}

	return event, nil
}

func canJoinSectSecretRealmAt(event SectSecretRealmEvent, now time.Time) bool {
	if event.RealmID == "" || event.Status != "active" {
		return false
	}
	return !now.Before(event.StartAt) && now.Before(event.EndAt)
}

func getActiveOrLatestSectSecretRealm(sectID int64, now time.Time) (SectSecretRealmEvent, bool) {
	event, err := getActiveOrLatestSectSecretRealmChecked(sectID, now)
	return event, err == nil
}

func getActiveOrLatestSectSecretRealmChecked(sectID int64, now time.Time) (SectSecretRealmEvent, error) {
	if event, err := getActiveSectSecretRealmTxChecked(DB, sectID, now); err == nil {
		return event, nil
	} else if !errors.Is(err, errSectSecretRealmNotActive) {
		return SectSecretRealmEvent{}, err
	}
	if DB == nil {
		return SectSecretRealmEvent{}, fmt.Errorf("SECT_SECRET_REALM_DB_UNAVAILABLE")
	}

	var event SectSecretRealmEvent
	err := DB.Where("sect_id = ?", sectID).
		Order("start_at DESC").
		First(&event).Error
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return SectSecretRealmEvent{}, errSectSecretRealmNotActive
		}
		return SectSecretRealmEvent{}, err
	}

	return event, nil
}

func refreshActiveSectSecretRealms(bot *tgbotapi.BotAPI, now time.Time) {
	var events []SectSecretRealmEvent
	if err := DB.Where("status = ? AND start_at <= ? AND end_at > ?", "active", now, now).Find(&events).Error; err != nil {
		log.Printf("⚠️ 查询进行中宗门秘境失败，已跳过本轮实时刷新: now=%s err=%s", formatPlainValue(now.Format(time.RFC3339)), formatPlainError(err))
		return
	}

	for _, event := range events {
		if time.Since(event.UpdatedAt) < sectSecretRealmLiveRefreshInterval {
			continue
		}
		refreshed, err := refreshSectSecretRealmLiveProgress(event)
		if err != nil {
			log.Printf("⚠️ 宗门秘境实时刷新失败: realm=%s err=%s", formatPlainValue(event.RealmID), formatPlainError(err))
			continue
		}
		ensureSectSecretRealmLiveBoard(bot, refreshed)
	}
}

func replySectSecretRealmMemberReadFailure(bot *tgbotapi.BotAPI, chatID int64, userID int64, action string, err error) {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		replyText(bot, chatID, "❌ 您当前没有加入宗门。")
		return
	}
	log.Printf("⚠️ 宗门秘境%s成员档案读取失败: user=%d err=%s", formatPlainValue(action), userID, formatPlainError(err))
	replyText(bot, chatID, "❌ 宗门成员档案读取失败，请稍后重试。")
}

func replySectSecretRealmSectReadFailure(bot *tgbotapi.BotAPI, chatID int64, userID int64, sectID int64, action string, err error) {
	log.Printf("⚠️ 宗门秘境%s宗门档案读取失败: sect=%d user=%d err=%s", formatPlainValue(action), sectID, userID, formatPlainError(err))
	if errors.Is(err, gorm.ErrRecordNotFound) {
		replyText(bot, chatID, "❌ 宗门档案异常。")
		return
	}
	replyText(bot, chatID, "❌ 宗门档案读取失败，请稍后重试。")
}

func handleSectSecretRealmStatus(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	var member SectMember
	if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		replySectSecretRealmMemberReadFailure(bot, chatID, userID, "状态", err)
		return
	}

	var sect Sect
	if err := DB.Where("id = ?", member.SectID).First(&sect).Error; err != nil {
		replySectSecretRealmSectReadFailure(bot, chatID, userID, member.SectID, "状态", err)
		return
	}

	now := time.Now()
	event, realmErr := getActiveOrLatestSectSecretRealmChecked(member.SectID, now)

	if errors.Is(realmErr, errSectSecretRealmNotActive) {
		cfg := loadSectSecretRealmConfig()
		normalProfile, _ := cfg.profile(sectSecretRealmProfileNormal)
		cost := getSectSecretRealmProfileCost(normalProfile, sect.Level)
		replyText(bot, chatID, fmt.Sprintf(
			"🏯 **%s · 宗门秘境**\n\n"+
				"当前没有秘境记录。\n\n"+
				"普通秘境消耗：宗门资金 `%d`\n"+
				"普通秘境持续：`%d` 分钟\n\n"+
				"宗主或长老可发送：`开启宗门秘境` / `开启高阶宗门秘境`",
			escapeMarkdown(sect.Name),
			cost,
			normalProfile.DurationMinutes,
		))
		return
	}
	if realmErr != nil {
		log.Printf("⚠️ 宗门秘境状态读取失败: sect=%d user=%d err=%s", member.SectID, userID, formatPlainError(realmErr))
		replyText(bot, chatID, "📜 宗门秘境状态暂时读取失败，请稍后重试。")
		return
	}
	if !canJoinSectSecretRealmAt(event, now) {
		replyText(bot, chatID, fmt.Sprintf(
			"🏯 **%s · 宗门秘境**\n\n"+
				"当前没有可参加的宗门秘境。\n\n"+
				"宗主或长老可发送：`开启宗门秘境` / `开启高阶宗门秘境`\n"+
				"已结束秘境可发送：`宗门秘境排行` 查看最近一次榜单。",
			escapeMarkdown(sect.Name),
		))
		return
	}

	if event.Status == "active" {
		if refreshed, err := refreshSectSecretRealmLiveProgress(event); err == nil {
			event = refreshed
			ensureSectSecretRealmLiveBoard(bot, event)
		} else {
			log.Printf("⚠️ 宗门秘境状态实时刷新失败: realm=%s err=%s", formatPlainValue(event.RealmID), formatPlainError(err))
		}
	}
	profile := sectSecretRealmProfileFromSnapshot(event.ProfileKey, event.ConfigSnapshot)

	statusText := "进行中"
	if event.Status == "settled" {
		statusText = "已结算"
	} else if event.Status == "settling" {
		statusText = "结算中"
	}

	timeText := ""
	if event.Status == "active" {
		remaining := int(time.Until(event.EndAt).Minutes())
		if remaining < 0 {
			remaining = 0
		}
		timeText = fmt.Sprintf("剩余时间：`%d` 分钟\n", remaining)
	}
	guardianText := ""
	if event.Status == "settled" || event.GuardianBonusPercent > 0 {
		guardianText = sectSecretRealmGuardianSummaryMarkdown(event)
	}

	replyText(bot, chatID, fmt.Sprintf(
		"🏯 **%s · 宗门秘境**\n\n"+
			"秘境：**%s**\n"+
			"状态：`%s`\n"+
			"%s"+
			"%s"+
			"档位：`%s`\n"+
			"秘境口径：原始听书增量按墙钟封顶，前 `%.1f` 小时全额计入，之后按 `%.0f%%` 计入。\n"+
			"参与人数：`%d`\n"+
			"累计新增净修为：`%.1f` 小时\n"+
			"累计积分奖励：`%d`\n"+
			"累计贡献奖励：`%d`\n"+
			"累计声望奖励：`%d`\n"+
			"累计掉落：`%d`\n\n"+
			"可用指令：`进入宗门秘境` / `宗门秘境排行`",
		escapeMarkdown(sect.Name),
		escapeMarkdown(event.Name),
		statusText,
		timeText,
		guardianText,
		escapeMarkdown(profile.Name),
		profile.PressureFullHours,
		profile.PressureAfterRate*100,
		event.ParticipantCount,
		event.TotalDeltaHours,
		event.TotalRewardPoints,
		event.TotalRewardContribution,
		event.TotalRewardPrestige,
		event.TotalRewardDrops,
	))
}

func handleOpenSectSecretRealm(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, profileKey string, confirmed bool) {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	var member SectMember
	if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		replySectSecretRealmMemberReadFailure(bot, chatID, userID, "开启", err)
		return
	}

	if !canOperateSectSecretRealm(member.Role) {
		replyText(bot, chatID, "❌ 只有宗主或长老可以开启宗门秘境。")
		return
	}

	var sect Sect
	if err := DB.Where("id = ?", member.SectID).First(&sect).Error; err != nil {
		replySectSecretRealmSectReadFailure(bot, chatID, userID, member.SectID, "开启", err)
		return
	}

	cfg, cfgErr := loadSectSecretRealmConfigChecked()
	if cfgErr != nil {
		log.Printf("⚠️ 宗门秘境开启配置读取失败: sect=%d user=%d err=%s", member.SectID, userID, formatPlainError(cfgErr))
		replyText(bot, chatID, "❌ 宗门秘境配置暂时读取失败，请稍后重试。")
		return
	}
	profile, ok := cfg.profile(profileKey)
	if !ok || !profile.Enabled {
		replyText(bot, chatID, "❌ 该秘境档位尚未开放。")
		return
	}
	if sect.Level < profile.MinSectLevel {
		replyText(bot, chatID, fmt.Sprintf("❌ 开启%s需要宗门等级达到 `%d`。", escapeMarkdown(profile.Name), profile.MinSectLevel))
		return
	}

	cost := getSectSecretRealmProfileCost(profile, sect.Level)
	weeklyUsageText := ""
	if weeklyUsed, err := countSectSecretRealmWeeklyOpenTx(DB, member.SectID, time.Now()); err == nil {
		weeklyUsageText = fmt.Sprintf("本周开启次数：`%d/%d`\n", weeklyUsed, sectSecretRealmWeeklyOpenLimit)
	}
	confirmCommand := "确认开启宗门秘境"
	switch profile.Key {
	case sectSecretRealmProfileHigh:
		confirmCommand = "确认开启高阶宗门秘境"
	case sectSecretRealmProfileLimited:
		confirmCommand = "确认开启限时宗门秘境"
	}

	if !confirmed {
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **开启宗门秘境确认**\n\n"+
				"宗门：**%s**\n"+
				"秘境：**%s**\n"+
				"档位：`%s`\n"+
				"持续时间：`%d` 分钟\n"+
				"消耗宗门资金：`%d`\n"+
				"当前宗门资金：`%d`\n"+
				"%s\n"+
				"参与境界门槛：%s\n"+
				"结算口径：秘境期间原始听书增量按墙钟封顶，前 `%.1f` 小时全额计入；高境界道友有奖励加成，符合条件者可为全体护道。\n\n"+
				"确认开启请发送：`%s`",
			escapeMarkdown(sect.Name),
			escapeMarkdown(profile.Name),
			escapeMarkdown(profile.Key),
			profile.DurationMinutes,
			cost,
			sect.Funds,
			weeklyUsageText,
			sectSecretRealmRealmMarkdown(profile.MinMajorRealm, 0),
			profile.PressureFullHours,
			confirmCommand,
		))
		return
	}

	now := time.Now()
	var realmID string
	var sectName string
	var weeklyUsedAfter int64
	profileSnapshot := sectSecretRealmProfileSnapshot(profile)

	err := DB.Transaction(func(tx *gorm.DB) error {
		var txMember SectMember
		if err := loadSectMemberByUserInTx(tx, userID, &txMember, false); err != nil {
			return err
		}

		if !canOperateSectSecretRealm(txMember.Role) {
			return errSectNoPermission
		}

		var txSect Sect
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", txMember.SectID).
			First(&txSect).Error; err != nil {
			return err
		}
		touchRes := tx.Model(&Sect{}).
			Where("id = ?", txSect.ID).
			UpdateColumn("updated_at", gorm.Expr("updated_at"))
		if touchRes.Error != nil {
			return touchRes.Error
		}
		if touchRes.RowsAffected == 0 {
			return fmt.Errorf("SECT_SECRET_REALM_SECT_TOUCH_MISSED")
		}

		if _, activeErr := getActiveSectSecretRealmTxChecked(tx, txMember.SectID, now); activeErr == nil {
			return errSectSecretRealmAlreadyActive
		} else if !errors.Is(activeErr, errSectSecretRealmNotActive) {
			return activeErr
		}
		if txSect.Level < profile.MinSectLevel {
			return errSectNoPermission
		}
		weeklyUsed, err := countSectSecretRealmWeeklyOpenTx(tx, txMember.SectID, now)
		if err != nil {
			return err
		}
		if weeklyUsed >= sectSecretRealmWeeklyOpenLimit {
			return errSectSecretRealmWeeklyLimit
		}
		txWeeklyUsedAfter := weeklyUsed + 1

		cost = getSectSecretRealmProfileCost(profile, txSect.Level)

		res := tx.Model(&Sect{}).
			Where("id = ? AND funds >= ?", txSect.ID, cost).
			UpdateColumn("funds", gorm.Expr("funds - ?", cost))

		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errSectFundsNotEnough
		}

		txRealmID := fmt.Sprintf("SR-%d-%s", txSect.ID, generateRandomCode(10))
		txSectName := txSect.Name

		event := SectSecretRealmEvent{
			Model:          gorm.Model{CreatedAt: now, UpdatedAt: now},
			RealmID:        txRealmID,
			SectID:         int64(txSect.ID),
			ChatID:         chatID,
			Name:           profile.Name,
			ProfileKey:     profile.Key,
			ProfileName:    profile.Name,
			ConfigSnapshot: profileSnapshot,
			Status:         "active",
			OpenedByID:     userID,
			OpenedByName:   getTelegramDisplayName(msg.From),
			StartAt:        now,
			EndAt:          now.Add(sectSecretRealmProfileDuration(profile)),
			FundsCost:      cost,
		}

		if err := createSectSecretRealmEventInTx(tx, &event); err != nil {
			return err
		}

		fundsAfter, err := readSectFundsInTx(tx, int64(txSect.ID))
		if err != nil {
			return err
		}

		if err := createSectContributionLogInTx(tx, &SectContributionLog{
			SectID:       int64(txSect.ID),
			UserID:       userID,
			Delta:        -cost,
			Reason:       fmt.Sprintf("开启宗门秘境【%s】，消耗宗门资金 %d", sectSecretRealmPointDescriptionName(profile.Name), cost),
			RefType:      "sect_secret_realm_open",
			RefID:        txRealmID,
			BalanceAfter: fundsAfter,
		}); err != nil {
			return err
		}
		realmID = txRealmID
		sectName = txSectName
		weeklyUsedAfter = txWeeklyUsedAfter
		return nil
	})

	if err != nil {
		switch sectSecretRealmOpenErrorCode(err) {
		case "NOT_IN_SECT":
			replyText(bot, chatID, "❌ 您当前没有加入宗门。")
		case "NO_PERMISSION":
			replyText(bot, chatID, "❌ 只有宗主或长老可以开启宗门秘境，且宗门等级需满足该档位要求。")
		case "REALM_ALREADY_ACTIVE":
			replyText(bot, chatID, "⚠️ 当前宗门已有进行中的秘境，请勿重复开启。")
		case "REALM_WEEKLY_LIMIT":
			replyText(bot, chatID, fmt.Sprintf("⚠️ 本宗门本周秘境开启次数已达上限 `%d/%d`，普通、高阶和限时秘境合并计算，请下周再开。", sectSecretRealmWeeklyOpenLimit, sectSecretRealmWeeklyOpenLimit))
		case "FUNDS_NOT_ENOUGH":
			replyText(bot, chatID, fmt.Sprintf("❌ 宗门资金不足，开启秘境需要 `%d` 资金。", cost))
		default:
			log.Printf("❌ 开启宗门秘境失败: user=%d sect=%d err=%s", userID, member.SectID, formatPlainError(err))
			replyText(bot, chatID, "❌ 开启宗门秘境失败，请稍后重试。")
		}
		return
	}

	replyText(bot, chatID, fmt.Sprintf(
		"🏯 **宗门秘境已开启！**\n\n"+
			"宗门：**%s**\n"+
			"秘境：**%s**\n"+
			"档位：`%s`\n"+
			"持续时间：`%d` 分钟\n"+
			"消耗宗门资金：`%d`\n\n"+
			"本周开启次数：`%d/%d`\n"+
			"秘境期间听书将按秘境专属口径结算，高境界道友可获得额外奖励并为全体护道。\n"+
			"宗门成员可发送：`进入宗门秘境`\n"+
			"秘境编号：`%s`",
		escapeMarkdown(sectName),
		escapeMarkdown(profile.Name),
		escapeMarkdown(profile.Key),
		profile.DurationMinutes,
		cost,
		weeklyUsedAfter,
		sectSecretRealmWeeklyOpenLimit,
		realmID,
	))

	if event, latestErr := getActiveOrLatestSectSecretRealmChecked(member.SectID, time.Now()); latestErr == nil && event.RealmID == realmID {
		ensureSectSecretRealmLiveBoard(bot, event)
	} else if latestErr != nil && !errors.Is(latestErr, errSectSecretRealmNotActive) {
		log.Printf("⚠️ 宗门秘境开启后活动重读失败: realm=%s sect=%d err=%s", formatPlainValue(realmID), member.SectID, formatPlainError(latestErr))
	}
}

func handleJoinSectSecretRealm(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	var member SectMember
	if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		replySectSecretRealmMemberReadFailure(bot, chatID, userID, "进入", err)
		return
	}

	event, activeErr := getActiveSectSecretRealmTxChecked(DB, member.SectID, time.Now())
	if errors.Is(activeErr, errSectSecretRealmNotActive) {
		replyText(bot, chatID, "📜 当前宗门没有开放中的秘境。")
		return
	}

	if activeErr != nil {
		log.Printf("⚠️ 宗门秘境进入活动读取失败: sect=%d user=%d err=%s", member.SectID, userID, formatPlainError(activeErr))
		replyText(bot, chatID, "❌ 宗门秘境状态读取失败，请稍后重试。")
		return
	}

	var u User
	if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("⚠️ 进入宗门秘境读取本地档案失败: user=%d realm=%s err=%s", userID, formatPlainValue(event.RealmID), formatPlainError(err))
			replyText(bot, chatID, "❌ 进入宗门秘境读取本地档案失败，请稍后重试。")
			return
		}
		replyText(bot, chatID, "❌ 进入宗门秘境需要先绑定有效 ABS 听书账号。")
		return
	}
	if strings.TrimSpace(u.AbsUserID) == "" {
		replyText(bot, chatID, "❌ 进入宗门秘境需要先绑定有效 ABS 听书账号。")
		return
	}

	baseSnapshot, err := getSectSecretRealmListeningSnapshot(u.AbsUserID)
	if err != nil {
		log.Printf("⚠️ 进入宗门秘境读取 ABS 失败: user=%d realm=%s err=%s", userID, formatPlainValue(event.RealmID), formatPlainError(err))
		replyText(bot, chatID, "❌ 读取当前净修为失败，请稍后重试。")
		return
	}

	updateCultivationAudioHours(userID, baseSnapshot.EffectiveHours)
	majorRealm, minorRealm := sectSecretRealmCultivationSnapshot(userID)
	profile, err := sectSecretRealmProfileFromSnapshotChecked(event.ProfileKey, event.ConfigSnapshot)
	if err != nil {
		log.Printf("⚠️ 进入宗门秘境读取配置快照失败: realm=%s user=%d err=%s", formatPlainValue(event.RealmID), userID, formatPlainError(err))
		replyText(bot, chatID, "❌ 宗门秘境配置快照读取失败，请稍后重试。")
		return
	}
	if majorRealm < profile.MinMajorRealm {
		replyText(bot, chatID, fmt.Sprintf("❌ 进入%s需要境界达到 %s。", escapeMarkdown(profile.Name), sectSecretRealmRealmMarkdown(profile.MinMajorRealm, 0)))
		return
	}

	userName := getTelegramDisplayName(msg.From)

	err = DB.Transaction(func(tx *gorm.DB) error {
		if _, activeErr := getActiveSectSecretRealmTxChecked(tx, member.SectID, time.Now()); errors.Is(activeErr, errSectSecretRealmNotActive) {
			return errSectSecretRealmNotActive
		} else if activeErr != nil {
			return activeErr
		}

		participant := SectSecretRealmParticipant{
			RealmID:        event.RealmID,
			SectID:         member.SectID,
			UserID:         userID,
			UserName:       userName,
			MajorRealm:     majorRealm,
			MinorRealm:     minorRealm,
			BaseHours:      baseSnapshot.EffectiveHours,
			BaseRawSeconds: baseSnapshot.RawSeconds,
		}
		if err := createSectSecretRealmParticipantIfMissingInTx(tx, &participant); err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		if errors.Is(err, errSectSecretRealmNotActive) {
			replyText(bot, chatID, "📜 秘境刚刚关闭，请等待下次开启。")
		} else {
			replyText(bot, chatID, "❌ 进入秘境失败，请稍后重试。")
		}
		return
	}

	var participant SectSecretRealmParticipant
	realmText := sectSecretRealmRealmMarkdown(majorRealm, minorRealm)
	baseHoursText := fmt.Sprintf("`%.1f` 小时", baseSnapshot.EffectiveHours)
	if err := DB.Where("realm_id = ? AND user_id = ?", event.RealmID, userID).First(&participant).Error; err != nil {
		log.Printf("⚠️ 宗门秘境参与记录读取失败: realm=%s user=%d err=%s", formatPlainValue(event.RealmID), userID, formatPlainError(err))
		realmText = "读取失败"
		baseHoursText = "`读取失败`"
	} else {
		realmText = sectSecretRealmRealmMarkdown(participant.MajorRealm, participant.MinorRealm)
		baseHoursText = fmt.Sprintf("`%.1f` 小时", participant.BaseHours)
	}

	replyText(bot, chatID, fmt.Sprintf(
		"✅ 已进入宗门秘境【%s】。\n\n"+
			"档位：`%s`\n"+
			"当前境界：%s\n"+
			"当前基线净修为：%s\n"+
			"秘境结束前继续听书，结算时按秘境期间原始听书增量发放奖励。\n"+
			"前 `%.1f` 小时全额计入，并受护道者和自身境界加成影响。",
		escapeMarkdown(event.Name),
		escapeMarkdown(profile.Name),
		realmText,
		baseHoursText,
		profile.PressureFullHours,
	))

	if refreshed, err := refreshSectSecretRealmLiveProgress(event); err == nil {
		ensureSectSecretRealmLiveBoard(bot, refreshed)
	} else {
		log.Printf("⚠️ 宗门秘境进入后实时刷新失败: realm=%s user=%d err=%s", formatPlainValue(event.RealmID), userID, formatPlainError(err))
	}
}

func sectSecretRealmParticipantOnConflict() clause.OnConflict {
	// SQLite cannot match a partial unique index when conflict columns are named
	// without the index predicate, so use plain ON CONFLICT DO NOTHING.
	return clause.OnConflict{DoNothing: true}
}

func handleSettleSectSecretRealmCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	var member SectMember
	if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		replySectSecretRealmMemberReadFailure(bot, chatID, userID, "结算", err)
		return
	}

	if !canOperateSectSecretRealm(member.Role) {
		replyText(bot, chatID, "❌ 只有宗主或长老可以结算宗门秘境。")
		return
	}

	var event SectSecretRealmEvent
	if err := DB.Where("sect_id = ? AND status = ?", member.SectID, "active").
		Order("start_at DESC").
		First(&event).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "📜 当前没有可结算的宗门秘境。")
		} else {
			log.Printf("⚠️ 宗门秘境手动结算活动读取失败: sect=%d user=%d err=%s", member.SectID, userID, formatPlainError(err))
			replyText(bot, chatID, "📜 宗门秘境结算状态暂时读取失败，请稍后重试。")
		}
		return
	}

	if time.Now().Before(event.EndAt) {
		remaining := int(time.Until(event.EndAt).Minutes())
		if remaining < 1 {
			remaining = 1
		}
		replyText(bot, chatID, fmt.Sprintf("⏳ 秘境尚未关闭，约 `%d` 分钟后可结算。", remaining))
		return
	}

	settleSectSecretRealm(bot, event.RealmID, chatID)
}

func settleExpiredSectSecretRealms(bot *tgbotapi.BotAPI, now time.Time) {
	recoverStaleSectSecretRealmSettlements(now)

	var events []SectSecretRealmEvent
	if err := DB.Where("status = ? AND end_at <= ?", "active", now).Find(&events).Error; err != nil {
		log.Printf("⚠️ 查询到期宗门秘境失败，已跳过本轮结算扫描: now=%s err=%s", formatPlainValue(now.Format(time.RFC3339)), formatPlainError(err))
		return
	}

	for _, event := range events {
		settleSectSecretRealm(bot, event.RealmID, 0)
	}
}

func recoverStaleSectSecretRealmSettlements(now time.Time) {
	if DB == nil {
		return
	}
	cutoff := now.Add(-sectSecretRealmSettlementStaleAfter)
	res := DB.Model(&SectSecretRealmEvent{}).
		Where("status = ? AND updated_at < ? AND settled_at IS NULL", "settling", cutoff).
		Update("status", "active")
	if res.Error != nil {
		log.Printf("宗门秘境 settling 恢复扫描失败: cutoff=%s err=%s", formatPlainValue(cutoff.Format(time.RFC3339)), formatPlainError(res.Error))
		return
	}
	if res.RowsAffected > 0 {
		log.Printf("宗门秘境 settling 已恢复为 active: count=%d cutoff=%s", res.RowsAffected, formatPlainValue(cutoff.Format(time.RFC3339)))
	}
}

func settleSectSecretRealm(bot *tgbotapi.BotAPI, realmID string, fallbackChatID int64) {
	res := DB.Model(&SectSecretRealmEvent{}).
		Where("realm_id = ? AND status = ?", realmID, "active").
		Update("status", "settling")

	if res.Error != nil || res.RowsAffected == 0 {
		return
	}

	var event SectSecretRealmEvent
	if err := DB.Where("realm_id = ?", realmID).First(&event).Error; err != nil {
		rollbackSectSecretRealmSettlement(realmID, err)
		return
	}
	profile, err := sectSecretRealmProfileFromSnapshotChecked(event.ProfileKey, event.ConfigSnapshot)
	if err != nil {
		rollbackSectSecretRealmSettlement(realmID, err)
		return
	}

	var participants []SectSecretRealmParticipant
	if err := DB.Where("realm_id = ?", realmID).Find(&participants).Error; err != nil {
		rollbackSectSecretRealmSettlement(realmID, err)
		return
	}

	settledAt := time.Now()
	processed := make(map[uint]bool, len(participants))
	for i := range participants {
		p := &participants[i]

		var u User
		if err := DB.Where("telegram_id = ?", p.UserID).First(&u).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				rollbackSectSecretRealmSettlement(realmID, fmt.Errorf("SECT_SECRET_REALM_USER_READ_FAILED user=%d: %w", p.UserID, err))
				return
			}
			if err := clearSectSecretRealmParticipantComputedReward(*p, "user_not_found"); err != nil {
				rollbackSectSecretRealmSettlement(realmID, err)
				return
			}
			continue
		}
		if strings.TrimSpace(u.AbsUserID) == "" {
			if err := clearSectSecretRealmParticipantComputedReward(*p, "abs_unbound"); err != nil {
				rollbackSectSecretRealmSettlement(realmID, err)
				return
			}
			continue
		}

		finalSnapshot, err := getSectSecretRealmListeningSnapshot(u.AbsUserID)
		if err != nil {
			log.Printf("⚠️ 宗门秘境结算读取 ABS 失败: realm=%s user=%d err=%s", formatPlainValue(realmID), p.UserID, formatPlainError(err))
			finalSnapshot = sectSecretRealmListeningSnapshot{
				EffectiveHours: p.BaseHours,
				RawSeconds:     p.BaseRawSeconds,
			}
		}

		updateCultivationAudioHours(p.UserID, finalSnapshot.EffectiveHours)
		p.MajorRealm, p.MinorRealm = sectSecretRealmCultivationSnapshot(p.UserID)
		p.FinalHours = finalSnapshot.EffectiveHours
		p.FinalRawSeconds = finalSnapshot.RawSeconds
		if p.BaseRawSeconds > 0 || p.BaseHours == 0 {
			detail := calculateSectSecretRealmRawDeltaDetail(p.BaseRawSeconds, finalSnapshot.RawSeconds, p.CreatedAt, event.StartAt, event.EndAt, settledAt)
			p.ObservedRawDeltaSeconds = detail.ObservedDeltaSeconds
			p.WallClockCapSeconds = detail.WallClockCapSeconds
			p.RawDeltaSeconds = detail.DeltaSeconds
			p.RawCapped = detail.WasCapped
			if detail.WasCapped && detail.ObservedDeltaSeconds > detail.DeltaSeconds {
				log.Printf("⚠️ 宗门秘境听书增量被墙钟封顶: realm=%s user=%d observed_seconds=%.0f cap_seconds=%.0f used_seconds=%.0f", formatPlainValue(realmID), p.UserID, detail.ObservedDeltaSeconds, detail.WallClockCapSeconds, detail.DeltaSeconds)
			}
		} else {
			p.RawDeltaSeconds = 0
		}
		processed[p.ID] = true
	}

	eligibleParticipants := make([]SectSecretRealmParticipant, 0, len(participants))
	for i := range participants {
		if processed[participants[i].ID] {
			eligibleParticipants = append(eligibleParticipants, participants[i])
		}
	}

	guardian, guardianBonusPercent := sectSecretRealmGuardianForProfile(eligibleParticipants, profile)
	event.GuardianBonusPercent = guardianBonusPercent
	if guardian.UserID != 0 {
		event.GuardianUserID = guardian.UserID
		event.GuardianName = guardian.UserName
		event.GuardianMajorRealm = guardian.MajorRealm
		event.GuardianMinorRealm = guardian.MinorRealm
	}

	rewardParticipants := make([]SectSecretRealmParticipant, 0, len(eligibleParticipants))
	for i := range participants {
		p := &participants[i]
		if !processed[p.ID] {
			continue
		}
		if p.BaseRawSeconds > 0 || p.BaseHours == 0 {
			p.SuppressedHours = calculateSectSecretRealmSuppressedHoursForProfile(p.RawDeltaSeconds, profile)
			p.DeltaHours = applySectSecretRealmHourBonus(p.SuppressedHours, guardianBonusPercent)
		} else {
			legacyDeltaHours := math.Max(0, p.FinalHours-p.BaseHours)
			p.SuppressedHours = legacyDeltaHours
			p.DeltaHours = applySectSecretRealmHourBonus(legacyDeltaHours, guardianBonusPercent)
		}
		p.GuardianBonusPercent = guardianBonusPercent
		multiplier := sectSecretRealmRewardMultiplierForProfile(p.MajorRealm, profile)
		p.PointBonusPercent = sectSecretRealmPointRealmBonusForProfile(p.MajorRealm, profile)
		p.ContributionBonusPercent = multiplier.ContributionPercent - 100
		p.PrestigeBonusPercent = multiplier.PrestigePercent - 100
		p.RewardPoints, p.RewardContribution, p.RewardPrestige = calculateSectSecretRealmRewardsForProfile(p.DeltaHours, p.MajorRealm, profile)
		p.RewardDropItem, p.RewardDropQuantity = sectSecretRealmDropForParticipantWithProfile(event.RealmID, p.UserID, p.MajorRealm, p.DeltaHours, profile)

		res := DB.Model(&SectSecretRealmParticipant{}).
			Where("id = ?", p.ID).
			Updates(map[string]interface{}{
				"major_realm":                p.MajorRealm,
				"minor_realm":                p.MinorRealm,
				"final_hours":                p.FinalHours,
				"final_raw_seconds":          p.FinalRawSeconds,
				"observed_raw_delta_seconds": p.ObservedRawDeltaSeconds,
				"wall_clock_cap_seconds":     p.WallClockCapSeconds,
				"raw_delta_seconds":          p.RawDeltaSeconds,
				"suppressed_hours":           p.SuppressedHours,
				"delta_hours":                p.DeltaHours,
				"raw_capped":                 p.RawCapped,
				"reward_points":              p.RewardPoints,
				"reward_contribution":        p.RewardContribution,
				"reward_prestige":            p.RewardPrestige,
				"point_bonus_percent":        p.PointBonusPercent,
				"contribution_bonus_percent": p.ContributionBonusPercent,
				"prestige_bonus_percent":     p.PrestigeBonusPercent,
				"guardian_bonus_percent":     p.GuardianBonusPercent,
				"reward_drop_item":           p.RewardDropItem,
				"reward_drop_quantity":       p.RewardDropQuantity,
			})
		if res.Error != nil {
			rollbackSectSecretRealmSettlement(realmID, res.Error)
			return
		}
		if res.RowsAffected == 0 {
			rollbackSectSecretRealmSettlement(realmID, fmt.Errorf("SECT_SECRET_REALM_PARTICIPANT_FINAL_UPDATE_MISSED"))
			return
		}
		rewardParticipants = append(rewardParticipants, *p)
	}

	event.ParticipantCount = len(participants)
	if err := grantSectSecretRealmRewards(event, rewardParticipants); err != nil {
		log.Printf("❌ 宗门秘境奖励结算失败: realm=%s err=%s", formatPlainValue(realmID), formatPlainError(err))
		rollbackSectSecretRealmSettlement(realmID, err)
		return
	}

	sendSectSecretRealmSettlement(bot, event, fallbackChatID)
}

func clearSectSecretRealmParticipantComputedReward(p SectSecretRealmParticipant, reason string) error {
	if p.ID == 0 || p.RealmID == "" {
		return fmt.Errorf("SECT_SECRET_REALM_PARTICIPANT_INVALID")
	}
	res := DB.Model(&SectSecretRealmParticipant{}).
		Where("id = ? AND realm_id = ? AND user_id = ?", p.ID, p.RealmID, p.UserID).
		Updates(map[string]interface{}{
			"final_hours":                p.BaseHours,
			"final_raw_seconds":          p.BaseRawSeconds,
			"observed_raw_delta_seconds": 0,
			"wall_clock_cap_seconds":     0,
			"raw_delta_seconds":          0,
			"suppressed_hours":           0,
			"delta_hours":                0,
			"raw_capped":                 false,
			"reward_points":              0,
			"reward_contribution":        0,
			"reward_prestige":            0,
			"point_bonus_percent":        0,
			"contribution_bonus_percent": 0,
			"prestige_bonus_percent":     0,
			"guardian_bonus_percent":     0,
			"reward_drop_item":           "",
			"reward_drop_quantity":       0,
		})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("SECT_SECRET_REALM_PARTICIPANT_CLEAR_REWARD_MISSED")
	}
	log.Printf("sect secret realm settlement excluded participant: realm=%s user=%d reason=%s", formatPlainValue(p.RealmID), p.UserID, formatPlainValue(reason))
	return nil
}

func rollbackSectSecretRealmSettlement(realmID string, reason error) {
	res := DB.Model(&SectSecretRealmEvent{}).
		Where("realm_id = ? AND status = ?", realmID, "settling").
		Update("status", "active")
	if res.Error != nil {
		log.Printf("⚠️ 宗门秘境结算回滚 active 失败: realm=%s reason=%s err=%s", formatPlainValue(realmID), formatPlainValue(formatPlainError(reason)), formatPlainError(res.Error))
		return
	}
	if res.RowsAffected == 0 {
		log.Printf("⚠️ 宗门秘境结算回滚 active 未命中: realm=%s reason=%s", formatPlainValue(realmID), formatPlainValue(formatPlainError(reason)))
		return
	}
	log.Printf("↩️ 宗门秘境结算已回滚 active: realm=%s reason=%s", formatPlainValue(realmID), formatPlainValue(formatPlainError(reason)))
}

func calculateSectSecretRealmRewards(deltaHours float64) (points int, contribution int, prestige int) {
	return calculateSectSecretRealmRewardsForRealm(deltaHours, 0)
}

func calculateSectSecretRealmRewardsForRealm(deltaHours float64, majorRealm int) (points int, contribution int, prestige int) {
	profile, _ := defaultSectSecretRealmConfig().profile(sectSecretRealmProfileNormal)
	return calculateSectSecretRealmRewardsForProfile(deltaHours, majorRealm, profile)
}

func sectSecretRealmPointRateForProfile(profile SectSecretRealmProfileConfig) float64 {
	if normalizeSectSecretRealmProfileKey(profile.Key) == sectSecretRealmProfileHigh {
		return sectSecretRealmHighPointRate
	}
	return sectSecretRealmNormalPointRate
}

func sectSecretRealmPointRealmBonusForProfile(majorRealm int, profile SectSecretRealmProfileConfig) int {
	if majorRealm < 0 {
		majorRealm = 0
	}

	if normalizeSectSecretRealmProfileKey(profile.Key) == sectSecretRealmProfileHigh {
		switch {
		case majorRealm >= 5:
			return 9
		case majorRealm >= 4:
			return 7
		case majorRealm >= 3:
			return 5
		case majorRealm >= 2:
			return 3
		default:
			return 0
		}
	}

	switch {
	case majorRealm >= 5:
		return 5
	case majorRealm >= 4:
		return 4
	case majorRealm >= 3:
		return 3
	case majorRealm >= 2:
		return 2
	case majorRealm >= 1:
		return 1
	default:
		return 0
	}
}

func calculateSectSecretRealmRewardPointsForProfile(deltaHours float64, majorRealm int, profile SectSecretRealmProfileConfig) int {
	if deltaHours+0.000001 < profile.MinDeltaHours {
		return 0
	}

	points := int(math.Floor(deltaHours*sectSecretRealmPointRateForProfile(profile))) +
		sectSecretRealmPointRealmBonusForProfile(majorRealm, profile)
	if points < 1 {
		points = 1
	}
	if points > profile.MaxRewardPoints {
		points = profile.MaxRewardPoints
	}
	return points
}

func calculateSectSecretRealmRewardsForProfile(deltaHours float64, majorRealm int, profile SectSecretRealmProfileConfig) (points int, contribution int, prestige int) {
	if deltaHours+0.000001 < profile.MinDeltaHours {
		return 0, 0, 0
	}

	multiplier := sectSecretRealmRewardMultiplierForProfile(majorRealm, profile)
	points = calculateSectSecretRealmRewardPointsForProfile(deltaHours, majorRealm, profile)
	contribution = applySectSecretRealmRewardMultiplier(int(math.Floor(deltaHours*3)), multiplier.ContributionPercent)
	prestige = applySectSecretRealmRewardMultiplier(int(math.Floor(deltaHours)), multiplier.PrestigePercent)

	if contribution < 1 {
		contribution = 1
	}

	if contribution > profile.MaxContribution {
		contribution = profile.MaxContribution
	}
	if prestige > profile.MaxPrestige {
		prestige = profile.MaxPrestige
	}

	return points, contribution, prestige
}

func grantSectSecretRealmRewards(event SectSecretRealmEvent, participants []SectSecretRealmParticipant) error {
	totalDeltaHours := 0.0
	totalPoints := 0
	totalContribution := 0
	totalPrestige := 0
	totalDrops := 0
	rewardedCount := 0

	return DB.Transaction(func(tx *gorm.DB) error {
		for _, p := range participants {
			if p.IsRewarded {
				continue
			}

			if p.DeltaHours <= 0 && p.RewardPoints <= 0 && p.RewardContribution <= 0 && p.RewardPrestige <= 0 && p.RewardDropQuantity <= 0 {
				continue
			}

			res := tx.Model(&SectSecretRealmParticipant{}).
				Where("id = ? AND is_rewarded = ?", p.ID, false).
				Update("is_rewarded", true)

			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				continue
			}

			if p.RewardPoints > 0 {
				if err := applyPointDeltaInTx(
					tx,
					p.UserID,
					p.RewardPoints,
					"sect_secret_realm_reward",
					fmt.Sprintf("宗门秘境【%s】奖励，新增净修为 %.1f 小时", sectSecretRealmPointDescriptionName(event.Name), p.DeltaHours),
					"sect_secret_realm",
					event.RealmID,
				); err != nil {
					return err
				}
			}

			if p.RewardContribution > 0 {
				res := tx.Model(&SectMember{}).
					Where("sect_id = ? AND user_id = ?", event.SectID, p.UserID).
					Updates(map[string]interface{}{
						"contribution":        gorm.Expr("contribution + ?", p.RewardContribution),
						"weekly_contribution": gorm.Expr("weekly_contribution + ?", p.RewardContribution),
					})
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					return fmt.Errorf("SECT_SECRET_REALM_CONTRIBUTION_REWARD_MISSED")
				}
			}

			if p.RewardPrestige > 0 {
				res := tx.Model(&Sect{}).
					Where("id = ?", event.SectID).
					UpdateColumn("prestige", gorm.Expr("prestige + ?", p.RewardPrestige))
				if res.Error != nil {
					return res.Error
				}
				if res.RowsAffected == 0 {
					return fmt.Errorf("SECT_SECRET_REALM_PRESTIGE_REWARD_MISSED")
				}
			}

			if strings.TrimSpace(p.RewardDropItem) != "" && p.RewardDropQuantity > 0 {
				if err := gardenGrantInventoryInTx(tx, p.UserID, p.RewardDropItem, p.RewardDropQuantity); err != nil {
					return err
				}
			}

			var updatedMember SectMember
			if err := tx.Select("id", "contribution").
				Where("sect_id = ? AND user_id = ?", event.SectID, p.UserID).
				First(&updatedMember).Error; err != nil {
				if !errors.Is(err, gorm.ErrRecordNotFound) {
					return err
				}
			}

			if p.RewardContribution > 0 || p.RewardPrestige > 0 {
				if err := createSectContributionLogInTx(tx, &SectContributionLog{
					SectID:       event.SectID,
					UserID:       p.UserID,
					Delta:        p.RewardContribution,
					Reason:       fmt.Sprintf("宗门秘境奖励：贡献 +%d，声望 +%d", p.RewardContribution, p.RewardPrestige),
					RefType:      "sect_secret_realm",
					RefID:        event.RealmID,
					BalanceAfter: updatedMember.Contribution,
				}); err != nil {
					return err
				}
			}

			totalDeltaHours += p.DeltaHours
			totalPoints += p.RewardPoints
			totalContribution += p.RewardContribution
			totalPrestige += p.RewardPrestige
			totalDrops += p.RewardDropQuantity
			rewardedCount++
		}

		settledAt := time.Now()

		res := tx.Model(&SectSecretRealmEvent{}).
			Where("realm_id = ?", event.RealmID).
			Updates(map[string]interface{}{
				"status":                    "settled",
				"participant_count":         event.ParticipantCount,
				"total_delta_hours":         totalDeltaHours,
				"total_reward_points":       totalPoints,
				"total_reward_contribution": totalContribution,
				"total_reward_prestige":     totalPrestige,
				"total_reward_drops":        totalDrops,
				"guardian_user_id":          event.GuardianUserID,
				"guardian_name":             event.GuardianName,
				"guardian_major_realm":      event.GuardianMajorRealm,
				"guardian_minor_realm":      event.GuardianMinorRealm,
				"guardian_bonus_percent":    event.GuardianBonusPercent,
				"settled_at":                &settledAt,
			})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("SECT_SECRET_REALM_EVENT_SETTLE_MISSED")
		}
		return nil
	})
}

func sendSectSecretRealmSettlement(bot *tgbotapi.BotAPI, event SectSecretRealmEvent, fallbackChatID int64) {
	var sect Sect
	if err := DB.Where("id = ?", event.SectID).First(&sect).Error; err != nil {
		log.Printf("⚠️ 宗门秘境结算公告读取宗门失败: realm=%s sect=%d err=%s", formatPlainValue(event.RealmID), event.SectID, formatPlainError(err))
	}

	participants, participantsErr := sectSecretRealmTopParticipants(event.RealmID, 10)
	if participantsErr != nil {
		log.Printf("⚠️ 宗门秘境结算排行读取失败: realm=%s err=%s", formatPlainValue(event.RealmID), formatPlainError(participantsErr))
	}

	var rankText strings.Builder
	if participantsErr != nil {
		rankText.WriteString("排行读取失败，请稍后发送 `宗门秘境排行` 查看。\n")
	} else if len(participants) == 0 {
		rankText.WriteString("本次暂无有效参与记录。\n")
	} else {
		for i, p := range participants {
			dropText := sectSecretRealmDropMarkdown(p.RewardDropItem, p.RewardDropQuantity)
			rankText.WriteString(fmt.Sprintf(
				"%d. `%s` %s 新增 `%.1f` 小时，积分 +`%d`，贡献 +`%d`，声望 +`%d`，境界加成：积分 +`%d`，贡献/声望 +`%d/%d%%`，护道 +`%d%%`，掉落 %s\n",
				i+1,
				escapeMarkdown(p.UserName),
				sectSecretRealmRealmMarkdown(p.MajorRealm, p.MinorRealm),
				p.DeltaHours,
				p.RewardPoints,
				p.RewardContribution,
				p.RewardPrestige,
				p.PointBonusPercent,
				p.ContributionBonusPercent,
				p.PrestigeBonusPercent,
				p.GuardianBonusPercent,
				dropText,
			))
		}
	}

	var updatedEvent SectSecretRealmEvent
	if err := DB.Where("realm_id = ?", event.RealmID).First(&updatedEvent).Error; err != nil {
		log.Printf("⚠️ 宗门秘境结算公告读取事件失败: realm=%s err=%s", formatPlainValue(event.RealmID), formatPlainError(err))
		updatedEvent = event
	}
	profile := sectSecretRealmProfileFromSnapshot(updatedEvent.ProfileKey, updatedEvent.ConfigSnapshot)
	guardianText := sectSecretRealmGuardianSummaryMarkdown(updatedEvent)

	notice := fmt.Sprintf(
		"🏯 **【宗门秘境结算】** 🏯\n\n"+
			"宗门：**%s**\n"+
			"秘境：**%s**\n"+
			"档位：`%s`\n"+
			"%s"+
			"参与人数：`%d`\n"+
			"累计新增净修为：`%.1f` 小时\n\n"+
			"发放积分：`%d`\n"+
			"发放贡献：`%d`\n"+
			"发放声望：`%d`\n"+
			"发放掉落：`%d` 件\n\n"+
			"🏆 **秘境排行 Top 10**\n%s",
		escapeMarkdown(sect.Name),
		escapeMarkdown(updatedEvent.Name),
		escapeMarkdown(profile.Name),
		guardianText,
		updatedEvent.ParticipantCount,
		updatedEvent.TotalDeltaHours,
		updatedEvent.TotalRewardPoints,
		updatedEvent.TotalRewardContribution,
		updatedEvent.TotalRewardPrestige,
		updatedEvent.TotalRewardDrops,
		rankText.String(),
	)

	targetChatID := AppConfig.NoticeGroupID
	if targetChatID == 0 {
		targetChatID = fallbackChatID
	}
	if targetChatID != 0 {
		sendGroupAutoDeleteMessage(bot, targetChatID, notice)
	}
}

func renderSectSecretRealmLiveBoard(event SectSecretRealmEvent) string {
	var sect Sect
	if err := DB.Where("id = ?", event.SectID).First(&sect).Error; err != nil {
		log.Printf("⚠️ 宗门秘境实时榜读取宗门失败: realm=%s sect=%d err=%s", formatPlainValue(event.RealmID), event.SectID, formatPlainError(err))
	}

	participants, participantsErr := sectSecretRealmTopParticipants(event.RealmID, 10)
	if participantsErr != nil {
		log.Printf("⚠️ 宗门秘境实时榜排行读取失败: realm=%s err=%s", formatPlainValue(event.RealmID), formatPlainError(participantsErr))
	}

	profile := sectSecretRealmProfileFromSnapshot(event.ProfileKey, event.ConfigSnapshot)
	statusText := "进行中"
	if event.Status == "settling" {
		statusText = "结算中"
	} else if event.Status == "settled" {
		statusText = "已结算"
	}
	remaining := int(time.Until(event.EndAt).Minutes())
	if remaining < 0 {
		remaining = 0
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(
		"🏯 **宗门秘境实时榜**\n\n宗门：**%s**\n秘境：**%s**\n档位：`%s`\n状态：`%s`\n剩余：`%d` 分钟\n参与人数：`%d`\n累计秘境净修为：`%.2f` 小时\n预计奖励：积分 +`%d` / 贡献 +`%d` / 声望 +`%d`\n%s\n",
		escapeMarkdown(sect.Name),
		escapeMarkdown(event.Name),
		escapeMarkdown(profile.Name),
		statusText,
		remaining,
		event.ParticipantCount,
		event.TotalDeltaHours,
		event.TotalRewardPoints,
		event.TotalRewardContribution,
		event.TotalRewardPrestige,
		sectSecretRealmGuardianSummaryMarkdown(event),
	))

	if participantsErr != nil {
		b.WriteString("排行读取失败，请稍后发送 `宗门秘境排行` 查看。\n")
	} else if len(participants) == 0 {
		b.WriteString("暂无道友进入秘境。\n")
	} else {
		b.WriteString("🏆 **实时排行 Top 10**\n")
		for i, p := range participants {
			b.WriteString(fmt.Sprintf(
				"%d. `%s` %s 秘境净修为 `%.2f`h，实听 `%.0f`分，预计积分 +`%d`\n",
				i+1,
				escapeMarkdown(p.UserName),
				sectSecretRealmRealmMarkdown(p.MajorRealm, p.MinorRealm),
				p.DeltaHours,
				p.RawDeltaSeconds/60,
				p.RewardPoints,
			))
		}
	}

	b.WriteString("\n每 2 分钟自动刷新；发送 `宗门秘境排行` 可手动刷新。")
	return b.String()
}

func sectSecretRealmTopParticipants(realmID string, limit int) ([]SectSecretRealmParticipant, error) {
	var participants []SectSecretRealmParticipant
	if strings.TrimSpace(realmID) == "" {
		return participants, fmt.Errorf("SECT_SECRET_REALM_ID_EMPTY")
	}
	query := DB.Where("realm_id = ?", realmID).Order(sectSecretRealmRankOrder)
	if limit > 0 {
		query = query.Limit(limit)
	}
	if err := query.Find(&participants).Error; err != nil {
		return nil, err
	}
	return participants, nil
}

func ensureSectSecretRealmLiveBoard(bot *tgbotapi.BotAPI, event SectSecretRealmEvent) {
	if bot == nil || event.RealmID == "" {
		return
	}
	realmID := event.RealmID
	if enqueueTelegramAsync(telegramAsyncJob{
		Kind:      "sect_secret_realm_live_board",
		DedupeKey: "sect_secret_realm_live_board:" + realmID,
		Priority:  telegramAsyncPriorityLow,
		Send: func() error {
			return ensureSectSecretRealmLiveBoardSync(bot, realmID)
		},
	}) {
		return
	}

	log.Printf("⚠️ 宗门秘境实时榜异步入队失败，改为同步刷新: realm=%s", formatPlainValue(realmID))
	if err := ensureSectSecretRealmLiveBoardSync(bot, realmID); err != nil {
		log.Printf("⚠️ 宗门秘境实时榜同步刷新失败: realm=%s err=%s", formatPlainValue(realmID), formatTelegramSendError(err))
	}
}

func ensureSectSecretRealmLiveBoardSync(bot *tgbotapi.BotAPI, realmID string) error {
	if bot == nil || realmID == "" {
		return nil
	}
	var event SectSecretRealmEvent
	if err := DB.Where("realm_id = ?", realmID).First(&event).Error; err != nil {
		log.Printf("⚠️ 宗门秘境实时榜读取事件失败: realm=%s err=%s", formatPlainValue(realmID), formatPlainError(err))
		return nil
	}
	targetChatID := event.ChatID
	if targetChatID == 0 {
		targetChatID = AppConfig.NoticeGroupID
	}
	if targetChatID == 0 {
		return nil
	}

	text := renderSectSecretRealmLiveBoard(event)
	if event.BoardChatID != 0 && event.BoardMessageID != 0 {
		edit := tgbotapi.NewEditMessageText(event.BoardChatID, event.BoardMessageID, text)
		edit.ParseMode = "Markdown"
		if _, err := bot.Send(edit); err == nil {
			return nil
		} else if isTelegramMessageNotModifiedError(err) {
			return nil
		} else {
			log.Printf("⚠️ 宗门秘境实时榜编辑失败，将重发: realm=%s chat=%d message=%d err=%s", formatPlainValue(event.RealmID), event.BoardChatID, event.BoardMessageID, formatTelegramSendError(err))
		}
	}

	msg := tgbotapi.NewMessage(targetChatID, text)
	msg.ParseMode = "Markdown"
	sentMsg, err := sendNoAutoDelete(bot, msg)
	if err != nil {
		log.Printf("⚠️ 宗门秘境实时榜发送失败: realm=%s chat=%d err=%s", formatPlainValue(event.RealmID), targetChatID, formatTelegramSendError(err))
		return err
	}

	res := DB.Model(&SectSecretRealmEvent{}).
		Where("realm_id = ?", event.RealmID).
		Updates(map[string]interface{}{
			"board_chat_id":    sentMsg.Chat.ID,
			"board_message_id": sentMsg.MessageID,
		})
	err = res.Error
	if res.Error != nil {
		log.Printf("⚠️ 宗门秘境实时榜消息ID记录失败: realm=%s chat=%d message=%d err=%s", formatPlainValue(event.RealmID), sentMsg.Chat.ID, sentMsg.MessageID, formatPlainError(err))
	}
	if res.Error == nil && res.RowsAffected == 0 {
		log.Printf("sect secret realm live board message id record missed: realm=%s chat=%d message=%d", formatPlainValue(event.RealmID), sentMsg.Chat.ID, sentMsg.MessageID)
	}
	return nil
}

func handleSectSecretRealmRank(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	var member SectMember
	if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		replySectSecretRealmMemberReadFailure(bot, chatID, userID, "排行", err)
		return
	}

	event, realmErr := getActiveOrLatestSectSecretRealmChecked(member.SectID, time.Now())
	if realmErr != nil && !errors.Is(realmErr, errSectSecretRealmNotActive) {
		log.Printf("⚠️ 宗门秘境排行活动读取失败: sect=%d user=%d err=%s", member.SectID, userID, formatPlainError(realmErr))
		replyText(bot, chatID, "📜 宗门秘境排行暂时读取失败，请稍后重试。")
		return
	}
	if errors.Is(realmErr, errSectSecretRealmNotActive) {
		replyText(bot, chatID, "📜 当前宗门暂无秘境记录。")
		return
	}

	if event.Status == "active" {
		if refreshed, err := refreshSectSecretRealmLiveProgress(event); err == nil {
			event = refreshed
			ensureSectSecretRealmLiveBoard(bot, event)
		} else {
			log.Printf("⚠️ 宗门秘境排行实时刷新失败: realm=%s err=%s", formatPlainValue(event.RealmID), formatPlainError(err))
		}
	}

	participants, err := sectSecretRealmTopParticipants(event.RealmID, 10)
	if err != nil {
		log.Printf("⚠️ 宗门秘境排行读取失败: realm=%s err=%s", formatPlainValue(event.RealmID), formatPlainError(err))
		replyText(bot, chatID, "📜 宗门秘境排行暂时读取失败，请稍后重试。")
		return
	}

	if len(participants) == 0 {
		replyText(bot, chatID, "📜 当前秘境暂无参与者。")
		return
	}

	var b strings.Builder
	profile := sectSecretRealmProfileFromSnapshot(event.ProfileKey, event.ConfigSnapshot)
	b.WriteString(fmt.Sprintf("🏆 **宗门秘境排行 · %s**\n档位：`%s`\n\n", escapeMarkdown(event.Name), escapeMarkdown(profile.Name)))

	for i, p := range participants {
		dropText := sectSecretRealmDropMarkdown(p.RewardDropItem, p.RewardDropQuantity)
		b.WriteString(fmt.Sprintf(
			"%d. `%s` %s 新增：`%.1f` 小时，积分 +`%d`，贡献 +`%d`，声望 +`%d`，掉落 %s\n",
			i+1,
			escapeMarkdown(p.UserName),
			sectSecretRealmRealmMarkdown(p.MajorRealm, p.MinorRealm),
			p.DeltaHours,
			p.RewardPoints,
			p.RewardContribution,
			p.RewardPrestige,
			dropText,
		))
	}

	replyText(bot, chatID, b.String())
}

func handleSectSecretRealmDetail(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, args []string) {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	var member SectMember
	if err := DB.Where("user_id = ?", userID).First(&member).Error; err != nil {
		replySectSecretRealmMemberReadFailure(bot, chatID, userID, "明细", err)
		return
	}

	targetUserID := userID
	if len(args) > 0 {
		if !canOperateSectSecretRealm(member.Role) && !isSuperAdmin(userID) {
			replyText(bot, chatID, "❌ 只有宗主、长老或超级管理员可以查看他人秘境明细。")
			return
		}
		parsed, err := strconv.ParseInt(strings.TrimSpace(args[0]), 10, 64)
		if err != nil || parsed <= 0 {
			replyText(bot, chatID, "❌ 用法：`宗门秘境明细` 或 `宗门秘境明细 用户ID`")
			return
		}
		targetUserID = parsed
	}

	event, realmErr := getActiveOrLatestSectSecretRealmChecked(member.SectID, time.Now())
	if realmErr != nil && !errors.Is(realmErr, errSectSecretRealmNotActive) {
		log.Printf("⚠️ 宗门秘境明细活动读取失败: sect=%d user=%d target=%d err=%s", member.SectID, userID, targetUserID, formatPlainError(realmErr))
		replyText(bot, chatID, "📜 宗门秘境明细暂时读取失败，请稍后重试。")
		return
	}
	if errors.Is(realmErr, errSectSecretRealmNotActive) {
		replyText(bot, chatID, "📜 当前宗门暂无秘境记录。")
		return
	}

	var participant SectSecretRealmParticipant
	if err := DB.Where("realm_id = ? AND user_id = ?", event.RealmID, targetUserID).First(&participant).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "📜 未找到该道友在本次秘境中的参与记录。")
		} else {
			log.Printf("⚠️ 宗门秘境明细参与记录读取失败: realm=%s user=%d target=%d err=%s", formatPlainValue(event.RealmID), userID, targetUserID, formatPlainError(err))
			replyText(bot, chatID, "📜 宗门秘境明细暂时读取失败，请稍后重试。")
		}
		return
	}

	profile := sectSecretRealmProfileFromSnapshot(event.ProfileKey, event.ConfigSnapshot)
	capText := "否"
	if participant.RawCapped {
		capText = "是"
	}
	dropText := sectSecretRealmDropMarkdown(participant.RewardDropItem, participant.RewardDropQuantity)
	replyText(bot, chatID, fmt.Sprintf(
		"📜 **宗门秘境明细 · %s**\n\n"+
			"道友：`%s`\n"+
			"境界：%s\n"+
			"档位：`%s`\n"+
			"状态：`%s`\n\n"+
			"进入基线原始听书：`%.2f` 小时\n"+
			"结算原始听书：`%.2f` 小时\n"+
			"观测增量：`%.2f` 小时\n"+
			"墙钟上限：`%.2f` 小时\n"+
			"是否封顶：`%s`\n"+
			"采用增量：`%.2f` 小时\n"+
			"秘境压制后：`%.2f` 小时\n"+
			"护道加成：`%d%%`\n"+
			"最终秘境净修为：`%.2f` 小时\n\n"+
			"积分 +`%d`，贡献 +`%d`，声望 +`%d`，掉落 %s",
		escapeMarkdown(event.Name),
		escapeMarkdown(participant.UserName),
		sectSecretRealmRealmMarkdown(participant.MajorRealm, participant.MinorRealm),
		escapeMarkdown(profile.Name),
		event.Status,
		participant.BaseRawSeconds/3600,
		participant.FinalRawSeconds/3600,
		participant.ObservedRawDeltaSeconds/3600,
		participant.WallClockCapSeconds/3600,
		capText,
		participant.RawDeltaSeconds/3600,
		participant.SuppressedHours,
		participant.GuardianBonusPercent,
		participant.DeltaHours,
		participant.RewardPoints,
		participant.RewardContribution,
		participant.RewardPrestige,
		dropText,
	))
}

func getSectSecretRealmEffectiveListeningHours(absUserID string) (float64, error) {
	snapshot, err := getSectSecretRealmListeningSnapshot(absUserID)
	if err != nil {
		return 0, err
	}
	return snapshot.EffectiveHours, nil
}

func getSectSecretRealmListeningSnapshot(absUserID string) (sectSecretRealmListeningSnapshot, error) {
	if absUserID == "" {
		return sectSecretRealmListeningSnapshot{}, errAbsUserIDEmpty
	}

	body, code, err := absClient.sendRequest("GET", absUserListeningStatsPath(absUserID), nil)
	if err != nil {
		return sectSecretRealmListeningSnapshot{}, err
	}
	if code != 200 {
		return sectSecretRealmListeningSnapshot{}, fmt.Errorf("ABS_STATUS_%d", code)
	}

	var stats struct {
		Days map[string]float64 `json:"days"`
	}
	if err := json.Unmarshal(body, &stats); err != nil {
		return sectSecretRealmListeningSnapshot{}, err
	}

	effectiveHours := calculateEffectiveCultivationHoursFromABSDays(stats.Days)
	rawSeconds := sumSectSecretRealmRawListeningSeconds(stats.Days)
	if DB != nil {
		var u User
		readErr := DB.Select("telegram_id", "abs_user_id").Where("abs_user_id = ?", absUserID).First(&u).Error
		if readErr == nil {
			if err := recordDailyListeningStatsFromABSDays(u.TelegramID, absUserID, stats.Days, time.Now()); err == nil {
				if syncedEffectiveHours, ok := sumDailyListeningEffectiveHours(u.TelegramID); ok {
					effectiveHours = syncedEffectiveHours
				}
			} else {
				log.Printf("⚠️ 宗门秘境每日统计写入失败，使用本次 ABS 数据降级计算: user=%d abs=%s err=%s",
					u.TelegramID, formatPlainValue(absUserID), formatPlainError(err))
			}
			return sectSecretRealmListeningSnapshot{
				EffectiveHours: effectiveHours,
				RawSeconds:     rawSeconds,
			}, nil
		}
		if !errors.Is(readErr, gorm.ErrRecordNotFound) {
			log.Printf("⚠️ 宗门秘境本地用户读取失败，使用本次 ABS 数据降级计算: abs=%s err=%s",
				formatPlainValue(absUserID), formatPlainError(readErr))
		}
	}

	return sectSecretRealmListeningSnapshot{
		EffectiveHours: effectiveHours,
		RawSeconds:     rawSeconds,
	}, nil
}
