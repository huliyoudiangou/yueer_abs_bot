package main

// ==========================================
// 藏经阁 · 宗门功法（宗门声望拓展）
//
// 玩法（设计锁定，见 docs/agent/sect_prestige_expansion_planning.md）：
// - 藏经阁升级：消耗宗门声望，仅宗主/长老（canUpgradeSectAsset），1-4 级，逐级解锁功法品阶。
// - 功法解锁：消耗宗门声望（公共资产），仅宗主/长老；解锁后全宗成员可用。
// - 观法（入门）/ 修习（升层）：消耗个人声望（个人资产），全员可操作。
// - 挂载粒度：灵侍级——每只灵侍只能修习一门功法（unique servant_id），与装备/升星逐只调性一致。
// - 品阶：四等 黄/玄/地/天，灵侍品阶门槛 不限/灵/地/天。
// - 战力加成：单一加法点，第 n 层 = 入门×n 成本、累计深度×每层加成 %，由战力引擎统一应用。
//
// 资产规则（AGENTS 资产安全）：
// - 宗门声望变动一律事务 + 条件更新（funds/prestige >= cost）+ RowsAffected + 流水（SectLibraryLog / SectManualUnlock）。
// - 个人声望变动一律事务 + 条件更新（personal_prestige >= cost）+ RowsAffected + PrestigeSpent 累计流水。
// ==========================================

import (
	"errors"
	"fmt"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// ==========================================
// 数据模型
// ==========================================

// SectLibrary 宗门藏经阁（一宗门一行，level 0=未建）
type SectLibrary struct {
	gorm.Model
	SectID int64 `gorm:"uniqueIndex;not null"`
	Level  int   `gorm:"default:0"`
}

func (SectLibrary) TableName() string { return "sect_libraries" }

// SectLibraryLog 藏经阁升级流水（宗门声望消耗审计）
type SectLibraryLog struct {
	gorm.Model
	SectID       int64 `gorm:"index;not null"`
	UserID       int64 `gorm:"not null"`
	UserName     string
	OldLevel     int
	NewLevel     int
	PrestigeCost int
}

func (SectLibraryLog) TableName() string { return "sect_library_logs" }

// SectManualUnlock 宗门功法解锁流水（公共资产审计 + 解锁状态判断）
type SectManualUnlock struct {
	gorm.Model
	SectID             int64  `gorm:"index;not null"`
	ManualCode         string `gorm:"index;not null"`
	UnlockedBy         int64  `gorm:"not null"`
	UnlockedByName     string
	UnlockPrestigeCost int
}

func (SectManualUnlock) TableName() string { return "sect_manual_unlocks" }

// ServantManualStudy 灵侍功法修习进度（灵侍级挂载，一灵侍一门）
type ServantManualStudy struct {
	gorm.Model
	UserID        int64  `gorm:"index;not null"`
	ServantID     uint   `gorm:"uniqueIndex;not null"`
	ManualCode    string `gorm:"not null"`
	Depth         int    `gorm:"default:1"`
	PrestigeSpent int    `gorm:"default:0"`
}

func (ServantManualStudy) TableName() string { return "servant_manual_studies" }

// ==========================================
// 配置（默认值，进配置可调）
// ==========================================

const (
	sectLibraryMaxLevel = 4

	manualCodeYellow = "huang" // 黄
	manualCodeXuan   = "xuan"  // 玄
	manualCodeEarth  = "di"    // 地
	manualCodeHeaven = "tian"  // 天
)

// sectLibraryLevelCosts 藏经阁升到该级所需宗门声望（index=目标等级，0=未建累计 0）
var sectLibraryLevelCosts = [sectLibraryMaxLevel + 1]int{0, 200, 500, 1000, 2000}

// sectManualConfig 一门功法的静态配置
type sectManualConfig struct {
	Code                 string
	Name                 string
	Tier                 string
	RequiredLibraryLevel int    // 所需藏经阁等级
	UnlockPrestige       int    // 解锁宗门声望
	EntryCost            int    // 观法入门个人声望
	MaxDepth             int    // 层数上限
	BonusPerLayer        int    // 每层战力加成 %
	RequiredQuality      string // 灵侍品阶门槛（""=不限）
}

var sectManualCatalog = []sectManualConfig{
	{manualCodeYellow, "黄阶·引气诀", "黄", 1, 100, 3, 3, 2, ""},
	{manualCodeXuan, "玄阶·观澜诀", "玄", 2, 300, 8, 5, 3, "灵"},
	{manualCodeEarth, "地阶·撼岳诀", "地", 3, 800, 20, 7, 4, "地"},
	{manualCodeHeaven, "天阶·天罡诀", "天", 4, 2000, 50, 9, 5, "天"},
}

func sectManualConfigByCode(code string) (sectManualConfig, bool) {
	for _, c := range sectManualCatalog {
		if c.Code == code {
			return c, true
		}
	}
	return sectManualConfig{}, false
}

func sectManualName(code string) string {
	if c, ok := sectManualConfigByCode(code); ok {
		return c.Name
	}
	return code
}

// manualBonusPercent 功法战力加成百分比（深度 × 每层加成）
func manualBonusPercent(code string, depth int) int {
	if c, ok := sectManualConfigByCode(code); ok {
		if depth > c.MaxDepth {
			depth = c.MaxDepth
		}
		return depth * c.BonusPerLayer
	}
	return 0
}

// ==========================================
// 藏经阁等级
// ==========================================

// ensureSectLibraryTx 读取宗门藏经阁（不存在则建 level=0 行）。
// 唯一索引 sect_id 兜底并发：Create 撞唯一约束时回退读取。
func ensureSectLibraryTx(tx *gorm.DB, sectID int64) (*SectLibrary, error) {
	if tx == nil || sectID <= 0 {
		return nil, fmt.Errorf("SECT_LIBRARY_INVALID")
	}
	var lib SectLibrary
	if err := tx.Where("sect_id = ?", sectID).First(&lib).Error; err == nil {
		return &lib, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	lib = SectLibrary{SectID: sectID, Level: 0}
	if err := tx.Create(&lib).Error; err != nil {
		if isUniqueConstraintError(err) {
			if err2 := tx.Where("sect_id = ?", sectID).First(&lib).Error; err2 != nil {
				return nil, err2
			}
			return &lib, nil
		}
		return nil, err
	}
	if lib.ID == 0 {
		return nil, fmt.Errorf("SECT_LIBRARY_CREATE_MISSED")
	}
	return &lib, nil
}

// getSectLibraryLevelTxChecked 读取藏经阁等级（含不存在时建行兜底）
func getSectLibraryLevelTxChecked(tx *gorm.DB, sectID int64) (int, error) {
	lib, err := ensureSectLibraryTx(tx, sectID)
	if err != nil {
		return 0, err
	}
	return lib.Level, nil
}

// sectManualUnlockedTx 宗门是否已解锁某功法
func sectManualUnlockedTx(tx *gorm.DB, sectID int64, manualCode string) (bool, error) {
	var n int64
	if err := tx.Model(&SectManualUnlock{}).
		Where("sect_id = ? AND manual_code = ?", sectID, manualCode).Count(&n).Error; err != nil {
		return false, err
	}
	return n > 0, nil
}

// ==========================================
// 藏经阁升级（宗门声望）
// ==========================================

// upgradeSectLibrary 升级宗门藏经阁一级（宗主/长老）。
// 返回旧等级、新等级；错误经 sectErrorCode 映射。
func upgradeSectLibrary(userID int64, operatorName string) (int, int, error) {
	var oldLevel, newLevel int
	err := DB.Transaction(func(tx *gorm.DB) error {
		var member SectMember
		if err := tx.Where("user_id = ?", userID).First(&member).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return errNotInSect
		}
		if !canUpgradeSectAsset(member.Role) {
			return errSectOnlyOwner
		}
		var sect Sect
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", member.SectID).First(&sect).Error; err != nil {
			return err
		}
		lib, err := ensureSectLibraryTx(tx, int64(sect.ID))
		if err != nil {
			return err
		}
		if lib.Level >= sectLibraryMaxLevel {
			return errSectMaxLevel
		}
		oldLevel = lib.Level
		newLevel = lib.Level + 1
		cost := sectLibraryLevelCosts[newLevel]

		res := tx.Model(&Sect{}).
			Where("id = ? AND prestige >= ?", sect.ID, cost).
			UpdateColumn("prestige", gorm.Expr("prestige - ?", cost))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errSectPrestigeNotEnough
		}

		upRes := tx.Model(&SectLibrary{}).
			Where("id = ? AND level = ?", lib.ID, oldLevel).
			Update("level", newLevel)
		if upRes.Error != nil {
			return upRes.Error
		}
		if upRes.RowsAffected == 0 {
			return errSectResourceNotEnough
		}

		if err := tx.Create(&SectLibraryLog{
			SectID:       int64(sect.ID),
			UserID:       userID,
			UserName:     operatorName,
			OldLevel:     oldLevel,
			NewLevel:     newLevel,
			PrestigeCost: cost,
		}).Error; err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return oldLevel, newLevel, errNotInSect
		}
	}
	return oldLevel, newLevel, err
}

// ==========================================
// 功法解锁（宗门声望，宗主/长老）
// ==========================================

// unlockSectManual 宗门解锁一门功法（宗主/长老），消耗宗门声望。
func unlockSectManual(userID int64, operatorName, manualCode string) error {
	cfg, ok := sectManualConfigByCode(manualCode)
	if !ok {
		return errSectManualNotFound
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		var member SectMember
		if err := tx.Where("user_id = ?", userID).First(&member).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			return errNotInSect
		}
		if !canUpgradeSectAsset(member.Role) {
			return errSectOnlyOwner
		}
		var sect Sect
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ?", member.SectID).First(&sect).Error; err != nil {
			return err
		}
		lib, err := ensureSectLibraryTx(tx, int64(sect.ID))
		if err != nil {
			return err
		}
		if lib.Level < cfg.RequiredLibraryLevel {
			return errSectManualLibraryLevelLow
		}
		unlocked, err := sectManualUnlockedTx(tx, int64(sect.ID), manualCode)
		if err != nil {
			return err
		}
		if unlocked {
			return errSectManualAlreadyUnlocked
		}

		res := tx.Model(&Sect{}).
			Where("id = ? AND prestige >= ?", sect.ID, cfg.UnlockPrestige).
			UpdateColumn("prestige", gorm.Expr("prestige - ?", cfg.UnlockPrestige))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errSectPrestigeNotEnough
		}

		if err := tx.Create(&SectManualUnlock{
			SectID:             int64(sect.ID),
			ManualCode:         manualCode,
			UnlockedBy:         userID,
			UnlockedByName:     operatorName,
			UnlockPrestigeCost: cfg.UnlockPrestige,
		}).Error; err != nil {
			if isUniqueConstraintError(err) {
				return errSectManualAlreadyUnlocked
			}
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errNotInSect
		}
	}
	return err
}

// ==========================================
// 观法（入门） / 修习（升层）——个人声望
// ==========================================

// loadMemberAndLibraryForStudy 观法/修习公共前置：锁成员行 + 读藏经阁等级 + 校验解锁状态。
func loadMemberAndLibraryForStudy(tx *gorm.DB, userID int64, manualCode string, cfg sectManualConfig) (*SectMember, error) {
	var member SectMember
	if err := tx.Where("user_id = ?", userID).First(&member).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
		return nil, errNotInSect
	}
	libLevel, err := getSectLibraryLevelTxChecked(tx, member.SectID)
	if err != nil {
		return nil, err
	}
	if libLevel < cfg.RequiredLibraryLevel {
		return nil, errSectManualLibraryLevelLow
	}
	unlocked, err := sectManualUnlockedTx(tx, member.SectID, manualCode)
	if err != nil {
		return nil, err
	}
	if !unlocked {
		return nil, errSectManualNotUnlocked
	}
	return &member, nil
}

// loadOwnedServantTx 读取属于该用户的灵侍；不存在/不属于返回对应错误。
func loadOwnedServantTx(tx *gorm.DB, userID int64, servantID uint) (*UserSpiritServant, error) {
	var s UserSpiritServant
	if err := tx.Where("id = ? AND user_id = ?", servantID, userID).First(&s).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errSectManualServantNotOwned
		}
		return nil, err
	}
	return &s, nil
}

// checkServantManualQuality 校验灵侍品阶达到功法门槛
func checkServantManualQuality(servant *UserSpiritServant, cfg sectManualConfig) error {
	if cfg.RequiredQuality == "" {
		return nil
	}
	if QualityIndex(servant.Quality) < QualityIndex(cfg.RequiredQuality) {
		return errSectManualServantQualityLow
	}
	return nil
}

// spendPersonalPrestigeTx 条件扣减个人声望并检查 RowsAffected
func spendPersonalPrestigeTx(tx *gorm.DB, member *SectMember, cost int) error {
	if cost <= 0 {
		return fmt.Errorf("SECT_MANUAL_INVALID_COST")
	}
	res := tx.Model(&SectMember{}).
		Where("id = ? AND personal_prestige >= ?", member.ID, cost).
		UpdateColumn("personal_prestige", gorm.Expr("personal_prestige - ?", cost))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errSectPersonalPrestigeNotEnough
	}
	return nil
}

// studySectManual 观法入门：某灵侍开始修习一门功法（消耗入门个人声望，depth=1）。
func studySectManual(userID int64, servantID uint, manualCode string) error {
	cfg, ok := sectManualConfigByCode(manualCode)
	if !ok {
		return errSectManualNotFound
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		member, err := loadMemberAndLibraryForStudy(tx, userID, manualCode, cfg)
		if err != nil {
			return err
		}
		servant, err := loadOwnedServantTx(tx, userID, servantID)
		if err != nil {
			return err
		}
		if err := checkServantManualQuality(servant, cfg); err != nil {
			return err
		}
		// 一灵侍一门功法：已有修习则拒绝
		var existing ServantManualStudy
		if err := tx.Where("servant_id = ?", servant.ID).First(&existing).Error; err == nil {
			return errSectManualAlreadyStudying
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}

		if err := spendPersonalPrestigeTx(tx, member, cfg.EntryCost); err != nil {
			return err
		}

		study := ServantManualStudy{
			UserID:        userID,
			ServantID:     servant.ID,
			ManualCode:    manualCode,
			Depth:         1,
			PrestigeSpent: cfg.EntryCost,
		}
		if err := tx.Create(&study).Error; err != nil {
			if isUniqueConstraintError(err) {
				return errSectManualAlreadyStudying
			}
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errNotInSect
		}
	}
	return err
}

// advanceSectManual 修习升层：第 n 层成本 = 入门 × n，depth+1。
func advanceSectManual(userID int64, servantID uint, manualCode string) error {
	cfg, ok := sectManualConfigByCode(manualCode)
	if !ok {
		return errSectManualNotFound
	}
	err := DB.Transaction(func(tx *gorm.DB) error {
		member, err := loadMemberAndLibraryForStudy(tx, userID, manualCode, cfg)
		if err != nil {
			return err
		}
		servant, err := loadOwnedServantTx(tx, userID, servantID)
		if err != nil {
			return err
		}
		var study ServantManualStudy
		if err := tx.Where("servant_id = ? AND manual_code = ?", servant.ID, manualCode).
			First(&study).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errSectManualNotStudied
			}
			return err
		}
		if study.Depth >= cfg.MaxDepth {
			return errSectManualMaxDepth
		}
		cost := cfg.EntryCost * (study.Depth + 1)
		if err := spendPersonalPrestigeTx(tx, member, cost); err != nil {
			return err
		}
		upRes := tx.Model(&ServantManualStudy{}).
			Where("id = ? AND depth = ?", study.ID, study.Depth).
			Updates(map[string]interface{}{
				"depth":          gorm.Expr("depth + 1"),
				"prestige_spent": gorm.Expr("prestige_spent + ?", cost),
			})
		if upRes.Error != nil {
			return upRes.Error
		}
		if upRes.RowsAffected == 0 {
			return errSectManualDepthChanged
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errNotInSect
		}
	}
	return err
}

// getServantManualStudy 查询某灵侍的修习进度（战力加成读取用，无事务）
func getServantManualStudy(servantID uint) *ServantManualStudy {
	var study ServantManualStudy
	if err := db.Where("servant_id = ?", servantID).First(&study).Error; err != nil {
		return nil
	}
	return &study
}

// servantManualBonusPctMap 批量查询用户全部灵侍的功法加成百分比（面板/战力引擎共用）
func servantManualBonusPctMap(q *gorm.DB, userID int64) map[uint]int {
	m := make(map[uint]int)
	var studies []ServantManualStudy
	if err := q.Where("user_id = ?", userID).Find(&studies).Error; err != nil {
		return m
	}
	for i := range studies {
		st := &studies[i]
		m[st.ServantID] = manualBonusPercent(st.ManualCode, st.Depth)
	}
	return m
}

// UserServantManualBonusPercent 用户面板入口便捷读取（无事务）
func UserServantManualBonusPercent(userID int64) map[uint]int {
	return servantManualBonusPctMap(db, userID)
}
