package main

import (
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type CultivationRealmConfig struct {
	gorm.Model

	MajorRealm int    `gorm:"index;not null"`
	Name       string `gorm:"not null"`
	TitleName  string

	MinTotalHours float64 `gorm:"default:0"`
	MaxTotalHours float64 `gorm:"default:0"`

	IsMaxRealm bool `gorm:"default:false"`
	Enabled    bool `gorm:"default:true"`
}

func (CultivationRealmConfig) TableName() string {
	return "cultivation_realm_configs"
}

type CultivationMinorRealmConfig struct {
	gorm.Model

	MajorRealm int `gorm:"index;not null"`
	MinorRealm int `gorm:"not null"`

	Name          string  `gorm:"not null"`
	RequiredHours float64 `gorm:"not null"`

	Enabled bool `gorm:"default:true"`
}

func (CultivationMinorRealmConfig) TableName() string {
	return "cultivation_minor_realm_configs"
}

type BreakthroughConfig struct {
	gorm.Model

	FromMajorRealm int `gorm:"index;not null"`
	ToMajorRealm   int `gorm:"not null"`

	PillName   string
	PointsCost int

	MinTotalHours float64 `gorm:"not null"`
	SuccessRate   float64 `gorm:"not null"`

	CooldownHours int `gorm:"default:0"`

	GuaranteeFailCount int     `gorm:"default:3"`
	RefundRate         float64 `gorm:"default:0.2"`
	FailPenaltyPoints  int     `gorm:"default:50"`

	SplashMinMajorRealm int `gorm:"default:2"`
	SplashVictimCount   int `gorm:"default:3"`
	SplashPenaltyPoints int `gorm:"default:10"`

	Enabled bool `gorm:"default:true"`
}

func (BreakthroughConfig) TableName() string {
	return "breakthrough_configs"
}

type CultivationRuleSet struct {
	Realms        map[int]CultivationRealmConfig
	MinorRealms   map[int][]CultivationMinorRealmConfig
	Breakthroughs map[int]BreakthroughConfig
	LoadedAt      time.Time
	Source        string
}

var cultivationRuleCache = struct {
	mu    sync.RWMutex
	rules *CultivationRuleSet
}{}

func InitCultivationRuleCache() {
	if err := ReloadCultivationRules(); err != nil {
		log.Printf("⚠️ 修仙配置首次加载失败，使用内置默认规则兜底: %s", formatPlainError(err))

		cultivationRuleCache.mu.Lock()
		cultivationRuleCache.rules = buildDefaultCultivationRuleSet("builtin_fallback")
		cultivationRuleCache.mu.Unlock()
	}

	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			if err := ReloadCultivationRules(); err != nil {
				log.Printf("⚠️ 修仙配置自动刷新失败，继续使用上一版缓存: %s", formatPlainError(err))
			}
		}
	}()
}

func GetCultivationRules() *CultivationRuleSet {
	cultivationRuleCache.mu.RLock()
	defer cultivationRuleCache.mu.RUnlock()

	if cultivationRuleCache.rules == nil {
		return buildDefaultCultivationRuleSet("builtin_fallback")
	}

	return cloneCultivationRuleSet(cultivationRuleCache.rules)
}

func ReloadCultivationRules() error {
	rules, err := loadCultivationRulesFromDB()
	if err != nil {
		return err
	}

	if err := validateCultivationRuleSet(rules); err != nil {
		return err
	}

	cultivationRuleCache.mu.Lock()
	cultivationRuleCache.rules = rules
	cultivationRuleCache.mu.Unlock()

	log.Printf("✅ 修仙配置缓存已刷新: realms=%d breakthroughs=%d source=%s",
		len(rules.Realms),
		len(rules.Breakthroughs),
		formatPlainValue(rules.Source),
	)

	return nil
}

func loadCultivationRulesFromDB() (*CultivationRuleSet, error) {
	if DB == nil {
		return nil, errors.New("database is not initialized")
	}

	return loadCultivationRulesFromDBHandle(DB)
}

func loadCultivationRulesFromDBHandle(db *gorm.DB) (*CultivationRuleSet, error) {
	if db == nil {
		return nil, errors.New("database is not initialized")
	}

	var realms []CultivationRealmConfig
	if err := db.Where("enabled = ?", true).Order("major_realm ASC").Find(&realms).Error; err != nil {
		return nil, fmt.Errorf("load realm configs failed: %w", err)
	}

	var minorRealms []CultivationMinorRealmConfig
	if err := db.Where("enabled = ?", true).Order("major_realm ASC, minor_realm ASC").Find(&minorRealms).Error; err != nil {
		return nil, fmt.Errorf("load minor realm configs failed: %w", err)
	}

	var breakthroughs []BreakthroughConfig
	if err := db.Where("enabled = ?", true).Order("from_major_realm ASC").Find(&breakthroughs).Error; err != nil {
		return nil, fmt.Errorf("load breakthrough configs failed: %w", err)
	}

	rules := &CultivationRuleSet{
		Realms:        make(map[int]CultivationRealmConfig),
		MinorRealms:   make(map[int][]CultivationMinorRealmConfig),
		Breakthroughs: make(map[int]BreakthroughConfig),
		LoadedAt:      time.Now(),
		Source:        "database",
	}

	for _, realm := range realms {
		rules.Realms[realm.MajorRealm] = realm
	}

	for _, minor := range minorRealms {
		rules.MinorRealms[minor.MajorRealm] = append(rules.MinorRealms[minor.MajorRealm], minor)
	}

	for major := range rules.MinorRealms {
		sort.Slice(rules.MinorRealms[major], func(i, j int) bool {
			return rules.MinorRealms[major][i].MinorRealm < rules.MinorRealms[major][j].MinorRealm
		})
	}

	for _, breakthrough := range breakthroughs {
		rules.Breakthroughs[breakthrough.FromMajorRealm] = breakthrough
	}

	return rules, nil
}

func isInvalidRuleFloat(value float64) bool {
	return math.IsNaN(value) || math.IsInf(value, 0)
}

func validateCultivationRuleSet(rules *CultivationRuleSet) error {
	if rules == nil {
		return errors.New("cultivation rules are nil")
	}

	if len(rules.Realms) == 0 {
		return errors.New("realm configs are empty")
	}

	maxRealmCount := 0
	for major, realm := range rules.Realms {
		if major != realm.MajorRealm {
			return fmt.Errorf("realm map key mismatch: key=%d value=%d", major, realm.MajorRealm)
		}

		if realm.Name == "" {
			return fmt.Errorf("realm name is empty: major=%d", major)
		}

		if isInvalidRuleFloat(realm.MinTotalHours) || isInvalidRuleFloat(realm.MaxTotalHours) {
			return fmt.Errorf("realm hours must be finite numbers: major=%d", major)
		}

		if realm.MinTotalHours < 0 || realm.MaxTotalHours < 0 {
			return fmt.Errorf("realm hours cannot be negative: major=%d", major)
		}

		if realm.MaxTotalHours > 0 && realm.MaxTotalHours < realm.MinTotalHours {
			return fmt.Errorf("realm max hours smaller than min hours: major=%d", major)
		}

		if realm.IsMaxRealm {
			maxRealmCount++
		}
	}

	if maxRealmCount != 1 {
		return fmt.Errorf("there must be exactly one max realm, got %d", maxRealmCount)
	}

	for major, minors := range rules.MinorRealms {
		if _, ok := rules.Realms[major]; !ok {
			return fmt.Errorf("minor realm references missing major realm: major=%d", major)
		}

		seen := make(map[int]bool)
		lastHours := -1.0

		for _, minor := range minors {
			if minor.MinorRealm < 1 || minor.MinorRealm > 4 {
				return fmt.Errorf("minor realm must be 1-4: major=%d minor=%d", major, minor.MinorRealm)
			}

			if seen[minor.MinorRealm] {
				return fmt.Errorf("duplicate minor realm: major=%d minor=%d", major, minor.MinorRealm)
			}
			seen[minor.MinorRealm] = true

			if minor.Name == "" {
				return fmt.Errorf("minor realm name is empty: major=%d minor=%d", major, minor.MinorRealm)
			}

			if isInvalidRuleFloat(minor.RequiredHours) {
				return fmt.Errorf("minor realm required hours must be finite number: major=%d minor=%d", major, minor.MinorRealm)
			}

			if minor.RequiredHours < 0 {
				return fmt.Errorf("minor realm required hours cannot be negative: major=%d minor=%d", major, minor.MinorRealm)
			}

			if lastHours >= 0 && minor.RequiredHours < lastHours {
				return fmt.Errorf("minor realm required hours must be ascending: major=%d minor=%d", major, minor.MinorRealm)
			}
			lastHours = minor.RequiredHours
		}
	}

	for fromMajor, breakthrough := range rules.Breakthroughs {
		if fromMajor != breakthrough.FromMajorRealm {
			return fmt.Errorf("breakthrough map key mismatch: key=%d value=%d", fromMajor, breakthrough.FromMajorRealm)
		}

		if _, ok := rules.Realms[breakthrough.FromMajorRealm]; !ok {
			return fmt.Errorf("breakthrough references missing from realm: from=%d", breakthrough.FromMajorRealm)
		}

		if _, ok := rules.Realms[breakthrough.ToMajorRealm]; !ok {
			return fmt.Errorf("breakthrough references missing to realm: to=%d", breakthrough.ToMajorRealm)
		}

		if breakthrough.ToMajorRealm <= breakthrough.FromMajorRealm {
			return fmt.Errorf("breakthrough target must be greater than source: from=%d to=%d", breakthrough.FromMajorRealm, breakthrough.ToMajorRealm)
		}

		if breakthrough.PointsCost < 0 {
			return fmt.Errorf("breakthrough points cost cannot be negative: from=%d", breakthrough.FromMajorRealm)
		}

		if isInvalidRuleFloat(breakthrough.MinTotalHours) {
			return fmt.Errorf("breakthrough min hours must be finite number: from=%d", breakthrough.FromMajorRealm)
		}

		if breakthrough.MinTotalHours < 0 {
			return fmt.Errorf("breakthrough min hours cannot be negative: from=%d", breakthrough.FromMajorRealm)
		}

		if isInvalidRuleFloat(breakthrough.SuccessRate) {
			return fmt.Errorf("breakthrough success rate must be finite number: from=%d", breakthrough.FromMajorRealm)
		}

		if breakthrough.SuccessRate < 0 || breakthrough.SuccessRate > 1 {
			return fmt.Errorf("breakthrough success rate must be between 0 and 1: from=%d", breakthrough.FromMajorRealm)
		}

		if isInvalidRuleFloat(breakthrough.RefundRate) {
			return fmt.Errorf("breakthrough refund rate must be finite number: from=%d", breakthrough.FromMajorRealm)
		}

		if breakthrough.CooldownHours < 0 {
			return fmt.Errorf("breakthrough cooldown cannot be negative: from=%d", breakthrough.FromMajorRealm)
		}

		if breakthrough.GuaranteeFailCount < 0 {
			return fmt.Errorf("breakthrough guarantee fail count cannot be negative: from=%d", breakthrough.FromMajorRealm)
		}

		if breakthrough.RefundRate < 0 || breakthrough.RefundRate > 1 {
			return fmt.Errorf("breakthrough refund rate must be between 0 and 1: from=%d", breakthrough.FromMajorRealm)
		}

		if breakthrough.FailPenaltyPoints < 0 ||
			breakthrough.SplashMinMajorRealm < 0 ||
			breakthrough.SplashVictimCount < 0 ||
			breakthrough.SplashPenaltyPoints < 0 {
			return fmt.Errorf("breakthrough penalty settings cannot be negative: from=%d", breakthrough.FromMajorRealm)
		}
	}

	return nil
}

func cloneCultivationRuleSet(src *CultivationRuleSet) *CultivationRuleSet {
	if src == nil {
		return nil
	}

	dst := &CultivationRuleSet{
		Realms:        make(map[int]CultivationRealmConfig, len(src.Realms)),
		MinorRealms:   make(map[int][]CultivationMinorRealmConfig, len(src.MinorRealms)),
		Breakthroughs: make(map[int]BreakthroughConfig, len(src.Breakthroughs)),
		LoadedAt:      src.LoadedAt,
		Source:        src.Source,
	}

	for k, v := range src.Realms {
		dst.Realms[k] = v
	}

	for k, v := range src.MinorRealms {
		copied := make([]CultivationMinorRealmConfig, len(v))
		copy(copied, v)
		dst.MinorRealms[k] = copied
	}

	for k, v := range src.Breakthroughs {
		dst.Breakthroughs[k] = v
	}

	return dst
}

func buildDefaultCultivationRuleSet(source string) *CultivationRuleSet {
	rules := &CultivationRuleSet{
		Realms:        make(map[int]CultivationRealmConfig),
		MinorRealms:   make(map[int][]CultivationMinorRealmConfig),
		Breakthroughs: make(map[int]BreakthroughConfig),
		LoadedAt:      time.Now(),
		Source:        source,
	}

	for _, realm := range defaultCultivationRealmConfigs() {
		rules.Realms[realm.MajorRealm] = realm
	}

	for _, minor := range defaultCultivationMinorRealmConfigs() {
		rules.MinorRealms[minor.MajorRealm] = append(rules.MinorRealms[minor.MajorRealm], minor)
	}

	for major := range rules.MinorRealms {
		sort.Slice(rules.MinorRealms[major], func(i, j int) bool {
			return rules.MinorRealms[major][i].MinorRealm < rules.MinorRealms[major][j].MinorRealm
		})
	}

	for _, breakthrough := range defaultBreakthroughConfigs() {
		rules.Breakthroughs[breakthrough.FromMajorRealm] = breakthrough
	}

	return rules
}

func seedDefaultCultivationConfigs() error {
	return seedDefaultCultivationConfigsWithHandle(DB)
}

// seedDefaultCultivationConfigsWithHandle 幂等地补齐缺失的默认境界/小境界/突破配置行，
// 已存在的行一律不覆盖（避免覆盖超管自定义值）。迁移与启动播种共用。
func seedDefaultCultivationConfigsWithHandle(db *gorm.DB) error {
	if db == nil {
		return errors.New("database is not initialized")
	}

	return db.Transaction(func(tx *gorm.DB) error {
		if err := seedDefaultRealmConfigs(tx); err != nil {
			return err
		}

		if err := seedDefaultMinorRealmConfigs(tx); err != nil {
			return err
		}

		if err := seedDefaultBreakthroughConfigs(tx); err != nil {
			return err
		}

		return nil
	})
}

// migrateCultivationRealmExtension 把道祖境界扩展落地到已存在的老库：
//  1. 幂等补齐炼虚~道祖的境界/小境界/突破默认行；
//  2. 把化神从“最高境界”降为普通境界（MaxTotalHours=1500, IsMaxRealm=false），
//     只更新仍处于旧封顶状态的化神行，避免覆盖超管后续的自定义。
//
// 生产入口走一次性迁移 migrateCultivationRealmExtension（db.go 注册）。
func migrateCultivationRealmExtension(db *gorm.DB) error {
	if db == nil {
		return fmt.Errorf("MIGRATION_NIL_DB")
	}

	if err := seedDefaultCultivationConfigsWithHandle(db); err != nil {
		return err
	}

	res := db.Model(&CultivationRealmConfig{}).
		Where("major_realm = ? AND is_max_realm = ?", 5, true).
		Updates(map[string]interface{}{
			"max_total_hours": 1500,
			"is_max_realm":    false,
		})
	if res.Error != nil {
		return res.Error
	}

	// 化神→道祖的 8 个突破：渡劫费统一 1500、冷却统一 168h（对齐运营口径，幂等）。
	fee := db.Model(&BreakthroughConfig{}).
		Where("from_major_realm IN ?", []int{5, 6, 7, 8, 9, 10, 11, 12}).
		Updates(map[string]interface{}{
			"points_cost":    1500,
			"cooldown_hours": 168,
		})
	if fee.Error != nil {
		return fee.Error
	}

	return nil
}

// cultivationRealmDisplayName 返回大境界展示名（优先配置 TitleName），配置缺失时回退“境界%d阶”。
// 用于灵墟解锁提示等需要把 MajorRealm 数字转成人话的地方。
func cultivationRealmDisplayName(major int) string {
	rules := GetCultivationRules()
	if rules != nil {
		if realm, ok := rules.Realms[major]; ok {
			name := strings.TrimSpace(realm.TitleName)
			if name == "" {
				name = strings.TrimSpace(realm.Name)
			}
			if name != "" {
				return name
			}
		}
	}
	return fmt.Sprintf("境界%d阶", major)
}

func seedDefaultRealmConfigs(tx *gorm.DB) error {
	defaults := defaultCultivationRealmConfigs()
	for _, cfg := range defaults {
		var existing CultivationRealmConfig
		err := tx.Where("major_realm = ?", cfg.MajorRealm).First(&existing).Error
		if err == nil {
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := createDefaultCultivationRealmConfigIfMissingInTx(tx, &cfg); err != nil {
			return err
		}
	}

	return nil
}

func createDefaultCultivationRealmConfigIfMissingInTx(tx *gorm.DB, cfg *CultivationRealmConfig) error {
	if tx == nil || cfg == nil {
		return fmt.Errorf("CULTIVATION_REALM_CONFIG_INVALID")
	}
	entry := *cfg
	if entry.MajorRealm < 0 || strings.TrimSpace(entry.Name) == "" {
		return fmt.Errorf("CULTIVATION_REALM_CONFIG_INVALID")
	}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	*cfg = entry
	return nil
}

func defaultCultivationRealmConfigs() []CultivationRealmConfig {
	return []CultivationRealmConfig{
		{MajorRealm: 0, Name: "凡人", TitleName: "平平无奇的凡人", MinTotalHours: 0, MaxTotalHours: 10, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 1, Name: "炼气", TitleName: "炼气", MinTotalHours: 10, MaxTotalHours: 50, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 2, Name: "筑基", TitleName: "筑基", MinTotalHours: 50, MaxTotalHours: 150, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 3, Name: "结丹", TitleName: "结丹", MinTotalHours: 150, MaxTotalHours: 350, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 4, Name: "元婴", TitleName: "元婴", MinTotalHours: 350, MaxTotalHours: 750, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 5, Name: "化神", TitleName: "化神", MinTotalHours: 750, MaxTotalHours: 1500, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 6, Name: "炼虚", TitleName: "炼虚", MinTotalHours: 1500, MaxTotalHours: 2400, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 7, Name: "合体", TitleName: "合体", MinTotalHours: 2400, MaxTotalHours: 3700, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 8, Name: "大乘", TitleName: "大乘", MinTotalHours: 3700, MaxTotalHours: 5500, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 9, Name: "真仙", TitleName: "真仙", MinTotalHours: 5500, MaxTotalHours: 8100, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 10, Name: "金仙", TitleName: "金仙", MinTotalHours: 8100, MaxTotalHours: 11500, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 11, Name: "太乙", TitleName: "太乙", MinTotalHours: 11500, MaxTotalHours: 16100, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 12, Name: "大罗", TitleName: "大罗", MinTotalHours: 16100, MaxTotalHours: 22300, IsMaxRealm: false, Enabled: true},
		{MajorRealm: 13, Name: "道祖", TitleName: "道祖", MinTotalHours: 22300, MaxTotalHours: 0, IsMaxRealm: true, Enabled: true},
	}
}

func seedDefaultMinorRealmConfigs(tx *gorm.DB) error {
	defaults := defaultCultivationMinorRealmConfigs()

	for _, cfg := range defaults {
		var existing CultivationMinorRealmConfig
		err := tx.Where("major_realm = ? AND minor_realm = ?", cfg.MajorRealm, cfg.MinorRealm).First(&existing).Error
		if err == nil {
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := createDefaultCultivationMinorRealmConfigIfMissingInTx(tx, &cfg); err != nil {
			return err
		}
	}

	return nil
}

func createDefaultCultivationMinorRealmConfigIfMissingInTx(tx *gorm.DB, cfg *CultivationMinorRealmConfig) error {
	if tx == nil || cfg == nil {
		return fmt.Errorf("CULTIVATION_MINOR_REALM_CONFIG_INVALID")
	}
	entry := *cfg
	if entry.MajorRealm <= 0 || entry.MinorRealm <= 0 || strings.TrimSpace(entry.Name) == "" {
		return fmt.Errorf("CULTIVATION_MINOR_REALM_CONFIG_INVALID")
	}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	*cfg = entry
	return nil
}

func defaultCultivationMinorRealmConfigs() []CultivationMinorRealmConfig {
	return []CultivationMinorRealmConfig{
		{MajorRealm: 1, MinorRealm: 1, Name: "初期", RequiredHours: 10, Enabled: true},
		{MajorRealm: 1, MinorRealm: 2, Name: "中期", RequiredHours: 20, Enabled: true},
		{MajorRealm: 1, MinorRealm: 3, Name: "后期", RequiredHours: 30, Enabled: true},
		{MajorRealm: 1, MinorRealm: 4, Name: "圆满", RequiredHours: 40, Enabled: true},

		{MajorRealm: 2, MinorRealm: 1, Name: "初期", RequiredHours: 50, Enabled: true},
		{MajorRealm: 2, MinorRealm: 2, Name: "中期", RequiredHours: 75, Enabled: true},
		{MajorRealm: 2, MinorRealm: 3, Name: "后期", RequiredHours: 100, Enabled: true},
		{MajorRealm: 2, MinorRealm: 4, Name: "圆满", RequiredHours: 125, Enabled: true},

		{MajorRealm: 3, MinorRealm: 1, Name: "初期", RequiredHours: 150, Enabled: true},
		{MajorRealm: 3, MinorRealm: 2, Name: "中期", RequiredHours: 200, Enabled: true},
		{MajorRealm: 3, MinorRealm: 3, Name: "后期", RequiredHours: 250, Enabled: true},
		{MajorRealm: 3, MinorRealm: 4, Name: "圆满", RequiredHours: 300, Enabled: true},

		{MajorRealm: 4, MinorRealm: 1, Name: "初期", RequiredHours: 350, Enabled: true},
		{MajorRealm: 4, MinorRealm: 2, Name: "中期", RequiredHours: 450, Enabled: true},
		{MajorRealm: 4, MinorRealm: 3, Name: "后期", RequiredHours: 550, Enabled: true},
		{MajorRealm: 4, MinorRealm: 4, Name: "圆满", RequiredHours: 650, Enabled: true},

		{MajorRealm: 5, MinorRealm: 1, Name: "初期", RequiredHours: 750, Enabled: true},
		{MajorRealm: 5, MinorRealm: 2, Name: "中期", RequiredHours: 900, Enabled: true},
		{MajorRealm: 5, MinorRealm: 3, Name: "后期", RequiredHours: 1050, Enabled: true},
		{MajorRealm: 5, MinorRealm: 4, Name: "圆满", RequiredHours: 1200, Enabled: true},

		{MajorRealm: 6, MinorRealm: 1, Name: "初期", RequiredHours: 1500, Enabled: true},
		{MajorRealm: 6, MinorRealm: 2, Name: "中期", RequiredHours: 1700, Enabled: true},
		{MajorRealm: 6, MinorRealm: 3, Name: "后期", RequiredHours: 1900, Enabled: true},
		{MajorRealm: 6, MinorRealm: 4, Name: "圆满", RequiredHours: 2100, Enabled: true},

		{MajorRealm: 7, MinorRealm: 1, Name: "初期", RequiredHours: 2400, Enabled: true},
		{MajorRealm: 7, MinorRealm: 2, Name: "中期", RequiredHours: 2700, Enabled: true},
		{MajorRealm: 7, MinorRealm: 3, Name: "后期", RequiredHours: 3000, Enabled: true},
		{MajorRealm: 7, MinorRealm: 4, Name: "圆满", RequiredHours: 3300, Enabled: true},

		{MajorRealm: 8, MinorRealm: 1, Name: "初期", RequiredHours: 3700, Enabled: true},
		{MajorRealm: 8, MinorRealm: 2, Name: "中期", RequiredHours: 4100, Enabled: true},
		{MajorRealm: 8, MinorRealm: 3, Name: "后期", RequiredHours: 4500, Enabled: true},
		{MajorRealm: 8, MinorRealm: 4, Name: "圆满", RequiredHours: 5000, Enabled: true},

		{MajorRealm: 9, MinorRealm: 1, Name: "初期", RequiredHours: 5500, Enabled: true},
		{MajorRealm: 9, MinorRealm: 2, Name: "中期", RequiredHours: 6100, Enabled: true},
		{MajorRealm: 9, MinorRealm: 3, Name: "后期", RequiredHours: 6700, Enabled: true},
		{MajorRealm: 9, MinorRealm: 4, Name: "圆满", RequiredHours: 7400, Enabled: true},

		{MajorRealm: 10, MinorRealm: 1, Name: "初期", RequiredHours: 8100, Enabled: true},
		{MajorRealm: 10, MinorRealm: 2, Name: "中期", RequiredHours: 8900, Enabled: true},
		{MajorRealm: 10, MinorRealm: 3, Name: "后期", RequiredHours: 9700, Enabled: true},
		{MajorRealm: 10, MinorRealm: 4, Name: "圆满", RequiredHours: 10600, Enabled: true},

		{MajorRealm: 11, MinorRealm: 1, Name: "初期", RequiredHours: 11500, Enabled: true},
		{MajorRealm: 11, MinorRealm: 2, Name: "中期", RequiredHours: 12600, Enabled: true},
		{MajorRealm: 11, MinorRealm: 3, Name: "后期", RequiredHours: 13700, Enabled: true},
		{MajorRealm: 11, MinorRealm: 4, Name: "圆满", RequiredHours: 14900, Enabled: true},

		{MajorRealm: 12, MinorRealm: 1, Name: "初期", RequiredHours: 16100, Enabled: true},
		{MajorRealm: 12, MinorRealm: 2, Name: "中期", RequiredHours: 17500, Enabled: true},
		{MajorRealm: 12, MinorRealm: 3, Name: "后期", RequiredHours: 19000, Enabled: true},
		{MajorRealm: 12, MinorRealm: 4, Name: "圆满", RequiredHours: 20600, Enabled: true},

		{MajorRealm: 13, MinorRealm: 1, Name: "初期", RequiredHours: 22300, Enabled: true},
		{MajorRealm: 13, MinorRealm: 2, Name: "中期", RequiredHours: 24400, Enabled: true},
		{MajorRealm: 13, MinorRealm: 3, Name: "后期", RequiredHours: 27800, Enabled: true},
		{MajorRealm: 13, MinorRealm: 4, Name: "圆满", RequiredHours: 31700, Enabled: true},
	}
}

func seedDefaultBreakthroughConfigs(tx *gorm.DB) error {
	defaults := defaultBreakthroughConfigs()

	for _, cfg := range defaults {
		var existing BreakthroughConfig
		err := tx.Where("from_major_realm = ?", cfg.FromMajorRealm).First(&existing).Error
		if err == nil {
			continue
		}
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		if err := createDefaultBreakthroughConfigIfMissingInTx(tx, &cfg); err != nil {
			return err
		}
	}

	return nil
}

func createDefaultBreakthroughConfigIfMissingInTx(tx *gorm.DB, cfg *BreakthroughConfig) error {
	if tx == nil || cfg == nil {
		return fmt.Errorf("BREAKTHROUGH_CONFIG_INVALID")
	}
	entry := *cfg
	if entry.FromMajorRealm < 0 || entry.ToMajorRealm < 0 {
		return fmt.Errorf("BREAKTHROUGH_CONFIG_INVALID")
	}
	res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return nil
	}
	*cfg = entry
	return nil
}

func defaultBreakthroughConfigs() []BreakthroughConfig {
	return []BreakthroughConfig{
		{
			FromMajorRealm:      0,
			ToMajorRealm:        1,
			PillName:            "引灵入体",
			PointsCost:          0,
			MinTotalHours:       10,
			SuccessRate:         1.0,
			CooldownHours:       0,
			GuaranteeFailCount:  3,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		{
			FromMajorRealm:      1,
			ToMajorRealm:        2,
			PillName:            "筑基丹",
			PointsCost:          100,
			MinTotalHours:       50,
			SuccessRate:         0.80,
			CooldownHours:       24,
			GuaranteeFailCount:  3,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		{
			FromMajorRealm:      2,
			ToMajorRealm:        3,
			PillName:            "降尘丹",
			PointsCost:          200,
			MinTotalHours:       150,
			SuccessRate:         0.60,
			CooldownHours:       48,
			GuaranteeFailCount:  3,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		{
			FromMajorRealm:      3,
			ToMajorRealm:        4,
			PillName:            "九曲灵参丹",
			PointsCost:          500,
			MinTotalHours:       350,
			SuccessRate:         0.40,
			CooldownHours:       72,
			GuaranteeFailCount:  3,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		{
			FromMajorRealm:      4,
			ToMajorRealm:        5,
			PillName:            "补天丹",
			PointsCost:          1000,
			MinTotalHours:       750,
			SuccessRate:         0.20,
			CooldownHours:       168,
			GuaranteeFailCount:  5,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		// —— 化神之后：废除丹药代购，改为“至宝 + 积分”突破 ——
		// 成功率统一 10%，新境界保证连续失败 10 次后下次必过（GuaranteeFailCount=10）。
		// 至宝由前一境灵墟 Boss / 世界 Boss 掉落，禁止上架交易行；积分渡劫费统一 1500、冷却统一 168h。
		{
			FromMajorRealm:      5,
			ToMajorRealm:        6,
			PillName:            "虚空真髓",
			PointsCost:          1500,
			MinTotalHours:       1500,
			SuccessRate:         0.10,
			CooldownHours:       168,
			GuaranteeFailCount:  10,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		{
			FromMajorRealm:      6,
			ToMajorRealm:        7,
			PillName:            "合道天珠",
			PointsCost:          1500,
			MinTotalHours:       2400,
			SuccessRate:         0.10,
			CooldownHours:       168,
			GuaranteeFailCount:  10,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		{
			FromMajorRealm:      7,
			ToMajorRealm:        8,
			PillName:            "混元一气",
			PointsCost:          1500,
			MinTotalHours:       3700,
			SuccessRate:         0.10,
			CooldownHours:       168,
			GuaranteeFailCount:  10,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		{
			FromMajorRealm:      8,
			ToMajorRealm:        9,
			PillName:            "渡厄仙引",
			PointsCost:          1500,
			MinTotalHours:       5500,
			SuccessRate:         0.10,
			CooldownHours:       168,
			GuaranteeFailCount:  10,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		{
			FromMajorRealm:      9,
			ToMajorRealm:        10,
			PillName:            "赤金仙露",
			PointsCost:          1500,
			MinTotalHours:       8100,
			SuccessRate:         0.10,
			CooldownHours:       168,
			GuaranteeFailCount:  10,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		{
			FromMajorRealm:      10,
			ToMajorRealm:        11,
			PillName:            "太乙玄黄",
			PointsCost:          1500,
			MinTotalHours:       11500,
			SuccessRate:         0.10,
			CooldownHours:       168,
			GuaranteeFailCount:  10,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		{
			FromMajorRealm:      11,
			ToMajorRealm:        12,
			PillName:            "大罗道果",
			PointsCost:          1500,
			MinTotalHours:       16100,
			SuccessRate:         0.10,
			CooldownHours:       168,
			GuaranteeFailCount:  10,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
		{
			FromMajorRealm:      12,
			ToMajorRealm:        13,
			PillName:            "混沌道种",
			PointsCost:          1500,
			MinTotalHours:       22300,
			SuccessRate:         0.10,
			CooldownHours:       168,
			GuaranteeFailCount:  10,
			RefundRate:          0.2,
			FailPenaltyPoints:   50,
			SplashMinMajorRealm: 2,
			SplashVictimCount:   3,
			SplashPenaltyPoints: 10,
			Enabled:             true,
		},
	}
}

func GetCultivationRuleCacheStatus() string {
	rules := GetCultivationRules()
	if rules == nil {
		return "修仙配置缓存未初始化"
	}

	return fmt.Sprintf(
		"修仙配置缓存：source=%s realms=%d breakthroughs=%d loaded_at=%s",
		rules.Source,
		len(rules.Realms),
		len(rules.Breakthroughs),
		rules.LoadedAt.Format("2006-01-02 15:04:05"),
	)
}

func FormatCultivationConfigSummary() string {
	rules := GetCultivationRules()
	if rules == nil {
		return "修仙配置缓存未初始化"
	}

	minorCount := 0
	for _, minors := range rules.MinorRealms {
		minorCount += len(minors)
	}

	maxRealmName := "未配置"
	for _, realm := range rules.Realms {
		if realm.IsMaxRealm {
			maxRealmName = realm.Name
			break
		}
	}

	return fmt.Sprintf(
		"【修仙配置】\n\n来源：%s\n加载时间：%s\n大境界数量：%d\n小境界配置：%d\n突破配置：%d\n当前最高境界：%s",
		rules.Source,
		rules.LoadedAt.Format("2006-01-02 15:04:05"),
		len(rules.Realms),
		minorCount,
		len(rules.Breakthroughs),
		maxRealmName,
	)
}

func FormatCultivationRealmConfigs() string {
	rules := GetCultivationRules()
	if rules == nil {
		return "修仙配置缓存未初始化"
	}

	majors := make([]int, 0, len(rules.Realms))
	for major := range rules.Realms {
		majors = append(majors, major)
	}
	sort.Ints(majors)

	var builder strings.Builder
	builder.WriteString("【境界配置】\n")

	for _, major := range majors {
		realm := rules.Realms[major]

		builder.WriteString("\n")
		builder.WriteString(realm.Name)

		if realm.TitleName != "" && realm.TitleName != realm.Name {
			builder.WriteString("（")
			builder.WriteString(realm.TitleName)
			builder.WriteString("）")
		}

		if realm.IsMaxRealm {
			builder.WriteString("【当前最高】")
		}

		builder.WriteString("\n")
		builder.WriteString(fmt.Sprintf("最低修为：%.1f 小时\n", realm.MinTotalHours))

		if realm.MaxTotalHours > 0 {
			builder.WriteString(fmt.Sprintf("阶段上限：%.1f 小时\n", realm.MaxTotalHours))
		}

		minors := rules.MinorRealms[major]
		sort.Slice(minors, func(i, j int) bool {
			return minors[i].MinorRealm < minors[j].MinorRealm
		})

		for _, minor := range minors {
			builder.WriteString(fmt.Sprintf("%s：%.1f 小时\n", minor.Name, minor.RequiredHours))
		}
	}

	return builder.String()
}

func FormatBreakthroughConfigs() string {
	rules := GetCultivationRules()
	if rules == nil {
		return "修仙配置缓存未初始化"
	}

	fromMajors := make([]int, 0, len(rules.Breakthroughs))
	for fromMajor := range rules.Breakthroughs {
		fromMajors = append(fromMajors, fromMajor)
	}
	sort.Ints(fromMajors)

	var builder strings.Builder
	builder.WriteString("【突破配置】\n")

	for _, fromMajor := range fromMajors {
		cfg := rules.Breakthroughs[fromMajor]

		fromName := getRealmConfigName(rules, cfg.FromMajorRealm)
		toName := getRealmConfigName(rules, cfg.ToMajorRealm)

		builder.WriteString("\n")
		builder.WriteString(fmt.Sprintf("%s -> %s\n", fromName, toName))

		if cfg.PillName == "" {
			builder.WriteString("方式：无\n")
		} else if cfg.PointsCost == 0 {
			builder.WriteString(fmt.Sprintf("方式：%s\n", cfg.PillName))
		} else {
			builder.WriteString(fmt.Sprintf("丹药：%s\n", cfg.PillName))
		}

		builder.WriteString(fmt.Sprintf("最低修为：%.1f 小时\n", cfg.MinTotalHours))
		builder.WriteString(fmt.Sprintf("积分消耗：%d\n", cfg.PointsCost))
		builder.WriteString(fmt.Sprintf("成功率：%.0f%%\n", cfg.SuccessRate*100))
		builder.WriteString(fmt.Sprintf("冷却：%d 小时\n", cfg.CooldownHours))

		if cfg.GuaranteeFailCount > 0 {
			builder.WriteString(fmt.Sprintf("保底失败次数：%d\n", cfg.GuaranteeFailCount))
		}

		builder.WriteString(fmt.Sprintf("成功返还比例：%.0f%%\n", cfg.RefundRate*100))
		builder.WriteString(fmt.Sprintf("失败调养费：%d\n", cfg.FailPenaltyPoints))

		if cfg.SplashVictimCount > 0 && cfg.SplashPenaltyPoints > 0 {
			builder.WriteString(fmt.Sprintf(
				"雷劫外溢：%d 境及以上，影响 %d 人，每人扣 %d 积分\n",
				cfg.SplashMinMajorRealm,
				cfg.SplashVictimCount,
				cfg.SplashPenaltyPoints,
			))
		}
	}

	return builder.String()
}

func getRealmConfigName(rules *CultivationRuleSet, major int) string {
	if rules == nil {
		return fmt.Sprintf("境界%d", major)
	}

	realm, ok := rules.Realms[major]
	if !ok || realm.Name == "" {
		return fmt.Sprintf("境界%d", major)
	}

	return realm.Name
}
