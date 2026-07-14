package main

import (
	"encoding/json"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const (
	sectSecretRealmConfigKey = "sect_secret_realm_config_v1"

	sectSecretRealmProfileNormal  = "normal"
	sectSecretRealmProfileHigh    = "high"
	sectSecretRealmProfileLimited = "limited"
)

type SectSecretRealmConfig struct {
	Profiles []SectSecretRealmProfileConfig `json:"profiles"`
}

type SectSecretRealmProfileConfig struct {
	Key               string                         `json:"key"`
	Name              string                         `json:"name"`
	Enabled           bool                           `json:"enabled"`
	MinSectLevel      int                            `json:"min_sect_level"`
	MinMajorRealm     int                            `json:"min_major_realm"`
	BaseCost          int                            `json:"base_cost"`
	CostPerLevel      int                            `json:"cost_per_level"`
	DurationMinutes   int                            `json:"duration_minutes"`
	MinDeltaHours     float64                        `json:"min_delta_hours"`
	MaxRewardPoints   int                            `json:"max_reward_points"`
	MaxContribution   int                            `json:"max_contribution"`
	MaxPrestige       int                            `json:"max_prestige"`
	PressureFullHours float64                        `json:"pressure_full_hours"`
	PressureAfterRate float64                        `json:"pressure_after_rate"`
	Multipliers       []SectSecretRealmMultiplierCfg `json:"multipliers"`
	GuardianBonuses   []SectSecretRealmGuardianCfg   `json:"guardian_bonuses"`
	DropRules         []SectSecretRealmDropCfg       `json:"drop_rules"`
}

type SectSecretRealmMultiplierCfg struct {
	MinMajorRealm       int `json:"min_major_realm"`
	PointPercent        int `json:"point_percent"`
	ContributionPercent int `json:"contribution_percent"`
	PrestigePercent     int `json:"prestige_percent"`
}

type SectSecretRealmGuardianCfg struct {
	MinMajorRealm int `json:"min_major_realm"`
	BonusPercent  int `json:"bonus_percent"`
}

type SectSecretRealmDropCfg struct {
	MinMajorRealm int    `json:"min_major_realm"`
	ItemName      string `json:"item_name"`
	Quantity      int    `json:"quantity"`
	ChancePercent int    `json:"chance_percent"`
}

func defaultSectSecretRealmConfig() SectSecretRealmConfig {
	return SectSecretRealmConfig{Profiles: []SectSecretRealmProfileConfig{
		{
			Key:               sectSecretRealmProfileNormal,
			Name:              sectSecretRealmName,
			Enabled:           true,
			MinSectLevel:      1,
			MinMajorRealm:     0,
			BaseCost:          sectSecretRealmBaseCost,
			CostPerLevel:      sectSecretRealmCostPerLevel,
			DurationMinutes:   int(sectSecretRealmDuration.Minutes()),
			MinDeltaHours:     sectSecretRealmMinDeltaHours,
			MaxRewardPoints:   sectSecretRealmMaxRewardPoints,
			MaxContribution:   sectSecretRealmMaxRewardContribution,
			MaxPrestige:       sectSecretRealmMaxRewardPrestige,
			PressureFullHours: sectSecretRealmPressureFullHours,
			PressureAfterRate: sectSecretRealmPressureAfterRate,
			Multipliers: []SectSecretRealmMultiplierCfg{
				{MinMajorRealm: 0, PointPercent: 100, ContributionPercent: 100, PrestigePercent: 100},
				{MinMajorRealm: 2, PointPercent: 105, ContributionPercent: 115, PrestigePercent: 115},
				{MinMajorRealm: 3, PointPercent: 110, ContributionPercent: 125, PrestigePercent: 125},
				{MinMajorRealm: 4, PointPercent: 115, ContributionPercent: 140, PrestigePercent: 140},
				{MinMajorRealm: 5, PointPercent: 120, ContributionPercent: 150, PrestigePercent: 150},
			},
			GuardianBonuses: []SectSecretRealmGuardianCfg{
				{MinMajorRealm: 2, BonusPercent: 3},
				{MinMajorRealm: 3, BonusPercent: 6},
				{MinMajorRealm: 4, BonusPercent: 10},
			},
			DropRules: []SectSecretRealmDropCfg{
				{MinMajorRealm: 1, ItemName: "凝露草种子", Quantity: 1, ChancePercent: 30},
				{MinMajorRealm: 2, ItemName: "龙血果种子", Quantity: 1, ChancePercent: 25},
				{MinMajorRealm: 3, ItemName: "天心莲种子", Quantity: 1, ChancePercent: 25},
				{MinMajorRealm: 4, ItemName: sectSecretRealmTokenItemName, Quantity: 1, ChancePercent: 35},
			},
		},
		{
			Key:               sectSecretRealmProfileHigh,
			Name:              "玄阶秘境",
			Enabled:           true,
			MinSectLevel:      3,
			MinMajorRealm:     2,
			BaseCost:          180,
			CostPerLevel:      45,
			DurationMinutes:   180,
			MinDeltaHours:     sectSecretRealmHighMinDeltaHours,
			MaxRewardPoints:   sectSecretRealmHighMaxRewardPoints,
			MaxContribution:   22,
			MaxPrestige:       18,
			PressureFullHours: 3,
			PressureAfterRate: 0.6,
			Multipliers: []SectSecretRealmMultiplierCfg{
				{MinMajorRealm: 0, PointPercent: 100, ContributionPercent: 100, PrestigePercent: 100},
				{MinMajorRealm: 2, PointPercent: 115, ContributionPercent: 130, PrestigePercent: 130},
				{MinMajorRealm: 3, PointPercent: 125, ContributionPercent: 145, PrestigePercent: 145},
				{MinMajorRealm: 4, PointPercent: 135, ContributionPercent: 165, PrestigePercent: 165},
				{MinMajorRealm: 5, PointPercent: 150, ContributionPercent: 190, PrestigePercent: 190},
			},
			GuardianBonuses: []SectSecretRealmGuardianCfg{
				{MinMajorRealm: 2, BonusPercent: 5},
				{MinMajorRealm: 3, BonusPercent: 8},
				{MinMajorRealm: 4, BonusPercent: 12},
			},
			DropRules: []SectSecretRealmDropCfg{
				{MinMajorRealm: 2, ItemName: "龙血果种子", Quantity: 1, ChancePercent: 35},
				{MinMajorRealm: 3, ItemName: "天心莲种子", Quantity: 1, ChancePercent: 35},
				{MinMajorRealm: 4, ItemName: sectSecretRealmTokenItemName, Quantity: 1, ChancePercent: 45},
			},
		},
		{
			Key:               sectSecretRealmProfileLimited,
			Name:              "限时秘境",
			Enabled:           false,
			MinSectLevel:      1,
			MinMajorRealm:     0,
			BaseCost:          120,
			CostPerLevel:      25,
			DurationMinutes:   120,
			MinDeltaHours:     sectSecretRealmLimitedMinDeltaHours,
			MaxRewardPoints:   sectSecretRealmLimitedMaxRewardPoints,
			MaxContribution:   16,
			MaxPrestige:       12,
			PressureFullHours: 2,
			PressureAfterRate: 0.5,
			Multipliers: []SectSecretRealmMultiplierCfg{
				{MinMajorRealm: 0, PointPercent: 100, ContributionPercent: 100, PrestigePercent: 100},
				{MinMajorRealm: 2, PointPercent: 110, ContributionPercent: 125, PrestigePercent: 125},
				{MinMajorRealm: 3, PointPercent: 120, ContributionPercent: 145, PrestigePercent: 145},
				{MinMajorRealm: 4, PointPercent: 130, ContributionPercent: 170, PrestigePercent: 170},
			},
			GuardianBonuses: []SectSecretRealmGuardianCfg{
				{MinMajorRealm: 2, BonusPercent: 4},
				{MinMajorRealm: 3, BonusPercent: 7},
				{MinMajorRealm: 4, BonusPercent: 10},
			},
			DropRules: []SectSecretRealmDropCfg{
				{MinMajorRealm: 1, ItemName: "凝露草种子", Quantity: 1, ChancePercent: 35},
				{MinMajorRealm: 2, ItemName: "龙血果种子", Quantity: 1, ChancePercent: 30},
				{MinMajorRealm: 3, ItemName: "天心莲种子", Quantity: 1, ChancePercent: 30},
				{MinMajorRealm: 4, ItemName: sectSecretRealmTokenItemName, Quantity: 1, ChancePercent: 40},
			},
		},
	}}
}

func loadSectSecretRealmConfig() SectSecretRealmConfig {
	cfg, err := loadSectSecretRealmConfigChecked()
	if err != nil {
		return defaultSectSecretRealmConfig()
	}
	return cfg
}

func loadSectSecretRealmConfigChecked() (SectSecretRealmConfig, error) {
	cfg := defaultSectSecretRealmConfig()
	raw, err := getSystemConfigStringChecked(sectSecretRealmConfigKey)
	if err != nil {
		return cfg, err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return cfg, nil
	}
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		return SectSecretRealmConfig{}, fmt.Errorf("解析宗门秘境配置失败: %w", err)
	}
	cfg.ensureDefaults()
	return cfg, nil
}

func (cfg *SectSecretRealmConfig) ensureDefaults() {
	defaults := defaultSectSecretRealmConfig()
	for _, def := range defaults.Profiles {
		if _, ok := cfg.profile(def.Key); !ok {
			cfg.Profiles = append(cfg.Profiles, def)
		}
	}
	for i := range cfg.Profiles {
		normalizeSectSecretRealmProfile(&cfg.Profiles[i])
	}
}

func (cfg SectSecretRealmConfig) profile(key string) (SectSecretRealmProfileConfig, bool) {
	key = normalizeSectSecretRealmProfileKey(key)
	for _, profile := range cfg.Profiles {
		if profile.Key == key {
			return profile, true
		}
	}
	return SectSecretRealmProfileConfig{}, false
}

func normalizeSectSecretRealmProfile(profile *SectSecretRealmProfileConfig) {
	profile.Key = normalizeSectSecretRealmProfileKey(profile.Key)
	if profile.Key == "" {
		profile.Key = sectSecretRealmProfileNormal
	}
	if strings.TrimSpace(profile.Name) == "" {
		profile.Name = sectSecretRealmName
	}
	if profile.MinSectLevel < 1 {
		profile.MinSectLevel = 1
	}
	if profile.DurationMinutes < 1 {
		profile.DurationMinutes = int(sectSecretRealmDuration.Minutes())
	}
	normalizeSectSecretRealmPointCap(profile)
	normalizeSectSecretRealmDefaultEconomy(profile)
	sort.Slice(profile.Multipliers, func(i, j int) bool {
		return profile.Multipliers[i].MinMajorRealm < profile.Multipliers[j].MinMajorRealm
	})
	sort.Slice(profile.GuardianBonuses, func(i, j int) bool {
		return profile.GuardianBonuses[i].MinMajorRealm < profile.GuardianBonuses[j].MinMajorRealm
	})
	sort.Slice(profile.DropRules, func(i, j int) bool {
		return profile.DropRules[i].MinMajorRealm < profile.DropRules[j].MinMajorRealm
	})
}

func normalizeSectSecretRealmPointCap(profile *SectSecretRealmProfileConfig) {
	if profile == nil {
		return
	}
	switch profile.Key {
	case sectSecretRealmProfileNormal:
		if profile.MaxRewardPoints == 8 {
			profile.MaxRewardPoints = sectSecretRealmMaxRewardPoints
		}
	case sectSecretRealmProfileHigh:
		if profile.MaxRewardPoints == 14 {
			profile.MaxRewardPoints = sectSecretRealmHighMaxRewardPoints
		}
	case sectSecretRealmProfileLimited:
		if profile.MaxRewardPoints == 10 {
			profile.MaxRewardPoints = sectSecretRealmLimitedMaxRewardPoints
		}
	}
}

func normalizeSectSecretRealmDefaultEconomy(profile *SectSecretRealmProfileConfig) {
	if profile == nil {
		return
	}
	switch profile.Key {
	case sectSecretRealmProfileNormal:
		if profile.BaseCost == 80 && profile.CostPerLevel == 20 {
			profile.BaseCost = sectSecretRealmBaseCost
			profile.CostPerLevel = sectSecretRealmCostPerLevel
		}
		if sectSecretRealmFloatClose(profile.MinDeltaHours, 0.2) {
			profile.MinDeltaHours = sectSecretRealmMinDeltaHours
		}
	case sectSecretRealmProfileHigh:
		if sectSecretRealmFloatClose(profile.MinDeltaHours, 0.3) {
			profile.MinDeltaHours = sectSecretRealmHighMinDeltaHours
		}
	case sectSecretRealmProfileLimited:
		if sectSecretRealmFloatClose(profile.MinDeltaHours, 0.2) {
			profile.MinDeltaHours = sectSecretRealmLimitedMinDeltaHours
		}
	}
}

func sectSecretRealmFloatClose(left float64, right float64) bool {
	diff := left - right
	if diff < 0 {
		diff = -diff
	}
	return diff < 0.000001
}

func normalizeSectSecretRealmProfileKey(key string) string {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "", "普通", "普通秘境", "normal":
		return sectSecretRealmProfileNormal
	case "高阶", "高阶秘境", "玄阶", "玄阶秘境", "high":
		return sectSecretRealmProfileHigh
	case "限时", "限时秘境", "limited":
		return sectSecretRealmProfileLimited
	default:
		return strings.ToLower(strings.TrimSpace(key))
	}
}

func sectSecretRealmProfileSnapshot(profile SectSecretRealmProfileConfig) string {
	normalizeSectSecretRealmProfile(&profile)
	body, err := json.Marshal(profile)
	if err != nil {
		return ""
	}
	return string(body)
}

func sectSecretRealmProfileFromSnapshotChecked(profileKey string, snapshot string) (SectSecretRealmProfileConfig, error) {
	if strings.TrimSpace(snapshot) == "" {
		return SectSecretRealmProfileConfig{}, fmt.Errorf("宗门秘境配置快照为空: profile=%s", formatPlainValue(profileKey))
	}
	var profile SectSecretRealmProfileConfig
	if err := json.Unmarshal([]byte(snapshot), &profile); err != nil {
		return SectSecretRealmProfileConfig{}, fmt.Errorf("解析宗门秘境配置快照失败: profile=%s: %w", formatPlainValue(profileKey), err)
	}
	normalizeSectSecretRealmProfile(&profile)
	return profile, nil
}

func sectSecretRealmProfileFromSnapshot(profileKey string, snapshot string) SectSecretRealmProfileConfig {
	if profile, err := sectSecretRealmProfileFromSnapshotChecked(profileKey, snapshot); err == nil {
		return profile
	} else {
		log.Printf("⚠️ 宗门秘境配置快照读取失败，降级使用当前配置展示: profile=%s err=%s", formatPlainValue(profileKey), formatPlainError(err))
	}
	cfg := loadSectSecretRealmConfig()
	if profile, ok := cfg.profile(profileKey); ok {
		return profile
	}
	profile, _ := defaultSectSecretRealmConfig().profile(sectSecretRealmProfileNormal)
	return profile
}

func saveSectSecretRealmConfig(actorID int64, cfg SectSecretRealmConfig, reason string, detail string) error {
	cfg.ensureDefaults()
	body, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	return DB.Transaction(func(tx *gorm.DB) error {
		if err := upsertSystemConfigValueInTx(tx, sectSecretRealmConfigKey, string(body)); err != nil {
			return err
		}
		return writeAuditLogInTx(tx, actorID, "UPDATE_SECT_SECRET_REALM_CONFIG", sectSecretRealmConfigKey, 0, fmt.Sprintf("%s，原因：%s", detail, formatPlainValue(reason)))
	})
}

func formatSectSecretRealmConfig(cfg SectSecretRealmConfig) string {
	cfg.ensureDefaults()
	var b strings.Builder
	b.WriteString("【宗门秘境配置】\n\n")
	for _, profile := range cfg.Profiles {
		b.WriteString(fmt.Sprintf(
			"%s（%s）\n状态：%s\n宗门等级门槛：%d\n参与境界门槛：%s\n持续：%d 分钟\n消耗：%d + 宗门等级 x %d 资金\n有效门槛：%.2f 小时\n压制：前 %.1f 小时全额，之后 %.0f%%\n奖励封顶：积分 %d / 贡献 %d / 声望 %d\n",
			profile.Name,
			profile.Key,
			formatEnabledText(profile.Enabled),
			profile.MinSectLevel,
			sectSecretRealmRealmMarkdown(profile.MinMajorRealm, 0),
			profile.DurationMinutes,
			profile.BaseCost,
			profile.CostPerLevel,
			profile.MinDeltaHours,
			profile.PressureFullHours,
			profile.PressureAfterRate*100,
			profile.MaxRewardPoints,
			profile.MaxContribution,
			profile.MaxPrestige,
		))
		b.WriteString("积分：普通/限时 floor(小时*6)+固定境界加成；高阶 floor(小时*7)+固定境界加成。\n")
		b.WriteString("倍率：")
		for i, rule := range profile.Multipliers {
			if i > 0 {
				b.WriteString("；")
			}
			b.WriteString(fmt.Sprintf("境界>=%d 积分字段%d%%(兼容保留) 贡献%d%% 声望%d%%", rule.MinMajorRealm, rule.PointPercent, rule.ContributionPercent, rule.PrestigePercent))
		}
		b.WriteString("\n掉落：")
		for i, rule := range profile.DropRules {
			if i > 0 {
				b.WriteString("；")
			}
			b.WriteString(fmt.Sprintf("境界>=%d %s x%d %d%%", rule.MinMajorRealm, rule.ItemName, rule.Quantity, rule.ChancePercent))
		}
		b.WriteString("\n\n")
	}
	b.WriteString("写入命令：\n")
	b.WriteString("设置秘境档位 档位 字段 值\n")
	b.WriteString("设置秘境倍率 档位 大境界 积分% 贡献% 声望%（积分%为兼容字段，当前积分按固定公式）\n")
	b.WriteString("设置秘境掉落 档位 大境界 物品名 数量 概率%\n")
	return b.String()
}

func formatEnabledText(enabled bool) string {
	if enabled {
		return "开启"
	}
	return "关闭"
}

func HandleSectSecretRealmAdminCommand(bot *tgbotapi.BotAPI, message *tgbotapi.Message, text string) bool {
	text = strings.TrimSpace(text)
	if message == nil || message.From == nil {
		return isSectSecretRealmAdminCommand(text)
	}

	session := getSession(message.From.ID)
	step := session.GetStep()
	pendingWrite := step == "WAITING_SECT_REALM_CONFIG_REASON" || step == "WAITING_CONFIRM_SECT_REALM_CONFIG"
	if !pendingWrite && !isSectSecretRealmAdminCommand(text) {
		return false
	}

	registerIncomingGroupCommandForAutoDelete(message)

	if !isSuperAdmin(message.From.ID) {
		sendPlainText(bot, message.Chat.ID, "权限不足：宗门秘境配置仅限超级管理员。")
		if pendingWrite {
			clearSession(message.From.ID)
		}
		return true
	}

	if text == "查看秘境配置" {
		cfg, err := loadSectSecretRealmConfigChecked()
		if err != nil {
			log.Printf("⚠️ 查看宗门秘境配置读取失败: user=%d err=%s", message.From.ID, formatPlainError(err))
			sendPlainText(bot, message.Chat.ID, "宗门秘境配置暂时读取失败，请稍后重试。")
			return true
		}
		sendLongPlainText(bot, message.Chat.ID, formatSectSecretRealmConfig(cfg))
		return true
	}

	if !message.Chat.IsPrivate() {
		sendPlainText(bot, message.Chat.ID, "宗门秘境配置写入属于高危操作，请私聊机器人执行。")
		if pendingWrite {
			clearSession(message.From.ID)
		}
		return true
	}

	if pendingWrite {
		return handleSectSecretRealmAdminSession(bot, message, text, session)
	}

	normalizedText := normalizeSectSecretRealmAdminWriteCommand(text)
	if normalizedText == "" || !isSectSecretRealmAdminWriteCommand(normalizedText) {
		sendPlainText(bot, message.Chat.ID, "秘境配置指令格式错误。")
		return true
	}

	session.SetTemp("sect_realm_config_command", normalizedText)
	session.SetStep("WAITING_SECT_REALM_CONFIG_REASON")
	UserSessions.Store(message.From.ID, session)
	sendPlainText(bot, message.Chat.ID, fmt.Sprintf("高危操作：该指令会修改全服宗门秘境配置。\n\n待执行指令：\n%s\n\n请输入本次变更原因，%s：", formatPlainValue(normalizedText), adminReasonRequirementText))
	return true
}

func handleSectSecretRealmAdminSession(bot *tgbotapi.BotAPI, message *tgbotapi.Message, text string, session *SessionState) bool {
	switch session.GetStep() {
	case "WAITING_SECT_REALM_CONFIG_REASON":
		if text == "取消" {
			sendPlainText(bot, message.Chat.ID, "已取消宗门秘境配置写入。")
			clearSession(message.From.ID)
			return true
		}
		reason, ok := validateAdminReason(text)
		if !ok {
			sendPlainText(bot, message.Chat.ID, adminReasonInvalidText)
			return true
		}
		pendingCommand := session.GetTemp("sect_realm_config_command")
		if pendingCommand == "" || !isSectSecretRealmAdminWriteCommand(pendingCommand) {
			sendPlainText(bot, message.Chat.ID, "宗门秘境配置写入会话状态异常，已中止。")
			clearSession(message.From.ID)
			return true
		}
		session.SetTemp("sect_realm_config_reason", reason)
		session.SetStep("WAITING_CONFIRM_SECT_REALM_CONFIG")
		UserSessions.Store(message.From.ID, session)
		sendPlainText(bot, message.Chat.ID, fmt.Sprintf("高危操作二次确认：该指令会修改全服宗门秘境配置。\n\n待执行指令：\n%s\n\n原因：%s\n\n确认执行请回复：确认执行秘境配置\n取消请回复：取消", formatPlainValue(pendingCommand), formatPlainValue(reason)))
		return true

	case "WAITING_CONFIRM_SECT_REALM_CONFIG":
		if text != "确认执行秘境配置" {
			sendPlainText(bot, message.Chat.ID, "已取消宗门秘境配置写入。")
			clearSession(message.From.ID)
			return true
		}
		pendingCommand := session.GetTemp("sect_realm_config_command")
		reason, ok := validateAdminReason(session.GetTemp("sect_realm_config_reason"))
		if pendingCommand == "" || !isSectSecretRealmAdminWriteCommand(pendingCommand) || !ok {
			sendPlainText(bot, message.Chat.ID, "宗门秘境配置写入会话状态异常，已中止。")
			clearSession(message.From.ID)
			return true
		}
		reply, err := executeSectSecretRealmAdminWriteCommand(message.From.ID, pendingCommand, reason)
		if err != nil {
			sendPlainText(bot, message.Chat.ID, formatPlainError(err))
			clearSession(message.From.ID)
			return true
		}
		sendPlainText(bot, message.Chat.ID, reply)
		clearSession(message.From.ID)
		return true
	}
	clearSession(message.From.ID)
	sendPlainText(bot, message.Chat.ID, "宗门秘境配置写入会话状态异常，已中止。")
	return true
}

func isSectSecretRealmAdminCommand(text string) bool {
	text = strings.TrimSpace(text)
	return text == "查看秘境配置" || isSectSecretRealmAdminWriteCommand(text)
}

func isSectSecretRealmAdminWriteCommand(text string) bool {
	text = normalizeSectSecretRealmAdminWriteCommand(text)
	return strings.HasPrefix(text, "设置秘境档位 ") ||
		strings.HasPrefix(text, "设置秘境倍率 ") ||
		strings.HasPrefix(text, "设置秘境掉落 ")
}

func normalizeSectSecretRealmAdminWriteCommand(text string) string {
	text = strings.TrimSpace(text)
	if strings.HasPrefix(text, "确认") {
		text = strings.TrimSpace(strings.TrimPrefix(text, "确认"))
	}
	return text
}

func executeSectSecretRealmAdminWriteCommand(actorID int64, text string, reason string) (string, error) {
	parts := strings.Fields(normalizeSectSecretRealmAdminWriteCommand(text))
	if len(parts) == 0 {
		return "", fmt.Errorf("秘境配置指令格式错误。")
	}
	cfg, err := loadSectSecretRealmConfigChecked()
	if err != nil {
		return "", fmt.Errorf("宗门秘境配置读取失败，请稍后重试: %w", err)
	}
	switch parts[0] {
	case "设置秘境档位":
		if len(parts) != 4 {
			return "", fmt.Errorf("用法：设置秘境档位 档位 字段 值")
		}
		profile, index, err := sectSecretRealmConfigProfileByKey(cfg, parts[1])
		if err != nil {
			return "", err
		}
		detail, err := updateSectSecretRealmProfileField(&profile, parts[2], parts[3])
		if err != nil {
			return "", err
		}
		cfg.Profiles[index] = profile
		if err := saveSectSecretRealmConfig(actorID, cfg, reason, detail); err != nil {
			return "", err
		}
		return "宗门秘境档位配置已更新。", nil

	case "设置秘境倍率":
		if len(parts) != 6 {
			return "", fmt.Errorf("用法：设置秘境倍率 档位 大境界 积分百分比 贡献百分比 声望百分比")
		}
		profile, index, err := sectSecretRealmConfigProfileByKey(cfg, parts[1])
		if err != nil {
			return "", err
		}
		minMajor, err := parseNonNegativeInt(parts[2], "大境界")
		if err != nil {
			return "", err
		}
		pointPercent, err := parseIntRange(parts[3], "积分百分比", 0, 500)
		if err != nil {
			return "", err
		}
		contributionPercent, err := parseIntRange(parts[4], "贡献百分比", 0, 500)
		if err != nil {
			return "", err
		}
		prestigePercent, err := parseIntRange(parts[5], "声望百分比", 0, 500)
		if err != nil {
			return "", err
		}
		upsertSectSecretRealmMultiplier(&profile, SectSecretRealmMultiplierCfg{
			MinMajorRealm:       minMajor,
			PointPercent:        pointPercent,
			ContributionPercent: contributionPercent,
			PrestigePercent:     prestigePercent,
		})
		cfg.Profiles[index] = profile
		detail := fmt.Sprintf("设置秘境倍率：%s 境界>=%d 积分%d%% 贡献%d%% 声望%d%%", formatPlainValue(profile.Key), minMajor, pointPercent, contributionPercent, prestigePercent)
		if err := saveSectSecretRealmConfig(actorID, cfg, reason, detail); err != nil {
			return "", err
		}
		return "宗门秘境倍率配置已更新。", nil

	case "设置秘境掉落":
		if len(parts) != 6 {
			return "", fmt.Errorf("用法：设置秘境掉落 档位 大境界 物品名 数量 概率百分比")
		}
		profile, index, err := sectSecretRealmConfigProfileByKey(cfg, parts[1])
		if err != nil {
			return "", err
		}
		minMajor, err := parseNonNegativeInt(parts[2], "大境界")
		if err != nil {
			return "", err
		}
		itemName := strings.TrimSpace(parts[3])
		quantity, err := parseIntRange(parts[4], "数量", 0, 999)
		if err != nil {
			return "", err
		}
		chance, err := parseIntRange(parts[5], "概率百分比", 0, 100)
		if err != nil {
			return "", err
		}
		if quantity > 0 && chance > 0 {
			if itemName == "" || len([]rune(itemName)) > 40 || strings.ContainsAny(itemName, "\r\n\t") {
				return "", fmt.Errorf("掉落物品名必须为 1-40 字且不能包含换行或制表符。")
			}
		}
		upsertSectSecretRealmDropRule(&profile, SectSecretRealmDropCfg{
			MinMajorRealm: minMajor,
			ItemName:      itemName,
			Quantity:      quantity,
			ChancePercent: chance,
		})
		cfg.Profiles[index] = profile
		detail := fmt.Sprintf("设置秘境掉落：%s 境界>=%d %s x%d %d%%", formatPlainValue(profile.Key), minMajor, formatPlainValue(itemName), quantity, chance)
		if err := saveSectSecretRealmConfig(actorID, cfg, reason, detail); err != nil {
			return "", err
		}
		return "宗门秘境掉落配置已更新。", nil
	}
	return "", fmt.Errorf("未知秘境配置指令。")
}

func sectSecretRealmConfigProfileByKey(cfg SectSecretRealmConfig, key string) (SectSecretRealmProfileConfig, int, error) {
	normalized := normalizeSectSecretRealmProfileKey(key)
	for i, profile := range cfg.Profiles {
		if profile.Key == normalized {
			return profile, i, nil
		}
	}
	return SectSecretRealmProfileConfig{}, -1, fmt.Errorf("未找到秘境档位：%s", key)
}

func updateSectSecretRealmProfileField(profile *SectSecretRealmProfileConfig, field string, value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(field)) {
	case "enabled", "启用", "开启":
		enabled, err := parseSectSecretRealmBool(value)
		if err != nil {
			return "", err
		}
		profile.Enabled = enabled
		return fmt.Sprintf("设置秘境档位：%s enabled=%t", formatPlainValue(profile.Key), enabled), nil
	case "name", "名称":
		name := strings.TrimSpace(value)
		if name == "" || len([]rune(name)) > 30 || strings.ContainsAny(name, "\r\n\t") {
			return "", fmt.Errorf("秘境名称必须为 1-30 字且不能包含换行或制表符。")
		}
		profile.Name = name
		return fmt.Sprintf("设置秘境档位：%s name=%s", formatPlainValue(profile.Key), formatPlainValue(name)), nil
	case "min_sect_level", "宗门等级":
		v, err := parseIntRange(value, "宗门等级门槛", 1, 100)
		if err != nil {
			return "", err
		}
		profile.MinSectLevel = v
		return fmt.Sprintf("设置秘境档位：%s min_sect_level=%d", formatPlainValue(profile.Key), v), nil
	case "min_major_realm", "境界门槛":
		v, err := parseIntRange(value, "境界门槛", 0, 100)
		if err != nil {
			return "", err
		}
		profile.MinMajorRealm = v
		return fmt.Sprintf("设置秘境档位：%s min_major_realm=%d", formatPlainValue(profile.Key), v), nil
	case "base_cost", "基础消耗":
		v, err := parseIntRange(value, "基础消耗", 0, 1000000)
		if err != nil {
			return "", err
		}
		profile.BaseCost = v
		return fmt.Sprintf("设置秘境档位：%s base_cost=%d", formatPlainValue(profile.Key), v), nil
	case "cost_per_level", "等级消耗":
		v, err := parseIntRange(value, "等级消耗", 0, 1000000)
		if err != nil {
			return "", err
		}
		profile.CostPerLevel = v
		return fmt.Sprintf("设置秘境档位：%s cost_per_level=%d", formatPlainValue(profile.Key), v), nil
	case "duration_minutes", "持续分钟":
		v, err := parseIntRange(value, "持续分钟", 10, 1440)
		if err != nil {
			return "", err
		}
		profile.DurationMinutes = v
		return fmt.Sprintf("设置秘境档位：%s duration_minutes=%d", formatPlainValue(profile.Key), v), nil
	case "min_delta_hours", "有效门槛":
		v, err := parseFloatRange(value, "有效门槛", 0, 24)
		if err != nil {
			return "", err
		}
		profile.MinDeltaHours = v
		return fmt.Sprintf("设置秘境档位：%s min_delta_hours=%.2f", formatPlainValue(profile.Key), v), nil
	case "pressure_full_hours", "全额小时":
		v, err := parseFloatRange(value, "全额小时", 0, 24)
		if err != nil {
			return "", err
		}
		profile.PressureFullHours = v
		return fmt.Sprintf("设置秘境档位：%s pressure_full_hours=%.2f", formatPlainValue(profile.Key), v), nil
	case "pressure_after_rate", "后续比例":
		v, err := parseFloatRange(value, "后续比例", 0, 1)
		if err != nil {
			return "", err
		}
		profile.PressureAfterRate = v
		return fmt.Sprintf("设置秘境档位：%s pressure_after_rate=%.2f", formatPlainValue(profile.Key), v), nil
	case "max_points", "积分上限":
		v, err := parseIntRange(value, "积分上限", 0, 100000)
		if err != nil {
			return "", err
		}
		profile.MaxRewardPoints = v
		return fmt.Sprintf("设置秘境档位：%s max_points=%d", formatPlainValue(profile.Key), v), nil
	case "max_contribution", "贡献上限":
		v, err := parseIntRange(value, "贡献上限", 0, 100000)
		if err != nil {
			return "", err
		}
		profile.MaxContribution = v
		return fmt.Sprintf("设置秘境档位：%s max_contribution=%d", formatPlainValue(profile.Key), v), nil
	case "max_prestige", "声望上限":
		v, err := parseIntRange(value, "声望上限", 0, 100000)
		if err != nil {
			return "", err
		}
		profile.MaxPrestige = v
		return fmt.Sprintf("设置秘境档位：%s max_prestige=%d", formatPlainValue(profile.Key), v), nil
	default:
		return "", fmt.Errorf("不支持的秘境档位字段：%s", field)
	}
}

func parseSectSecretRealmBool(text string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(text)) {
	case "true", "1", "yes", "on", "开启", "启用":
		return true, nil
	case "false", "0", "no", "off", "关闭", "停用":
		return false, nil
	default:
		return false, fmt.Errorf("布尔值必须是 开启/关闭 或 true/false。")
	}
}

func parseIntRange(text string, label string, min int, max int) (int, error) {
	v, err := strconv.Atoi(strings.TrimSpace(text))
	if err != nil {
		return 0, fmt.Errorf("%s必须是整数。", label)
	}
	if v < min || v > max {
		return 0, fmt.Errorf("%s必须在 %d-%d 之间。", label, min, max)
	}
	return v, nil
}

func upsertSectSecretRealmMultiplier(profile *SectSecretRealmProfileConfig, rule SectSecretRealmMultiplierCfg) {
	for i := range profile.Multipliers {
		if profile.Multipliers[i].MinMajorRealm == rule.MinMajorRealm {
			profile.Multipliers[i] = rule
			normalizeSectSecretRealmProfile(profile)
			return
		}
	}
	profile.Multipliers = append(profile.Multipliers, rule)
	normalizeSectSecretRealmProfile(profile)
}

func upsertSectSecretRealmDropRule(profile *SectSecretRealmProfileConfig, rule SectSecretRealmDropCfg) {
	for i := range profile.DropRules {
		if profile.DropRules[i].MinMajorRealm == rule.MinMajorRealm {
			profile.DropRules[i] = rule
			normalizeSectSecretRealmProfile(profile)
			return
		}
	}
	profile.DropRules = append(profile.DropRules, rule)
	normalizeSectSecretRealmProfile(profile)
}
