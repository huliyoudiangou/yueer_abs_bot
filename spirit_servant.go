package main

import (
	"fmt"
	"log"
	"math/rand"

	"gorm.io/gorm"
)

// ==========================================
// 灵侍养成骨架：创建、喂养、升星、编队
// Phase 1 骨架，战斗与推图在后续模块实现
// ==========================================

// CreateSpiritServant 按区域品阶概率创建一只灵侍
// zone.SpawnRates 为万分率，与 SpiritQualityNames 顺序对齐（凡/灵/玄/地/天/圣）
func CreateSpiritServant(userID int64, zone SpiritZone) (*UserSpiritServant, error) {
	roll := rand.Intn(10000)
	sum := 0
	chosenQuality := "凡"
	for i, rate := range zone.SpawnRates {
		if rate <= 0 || i >= len(SpiritQualityNames) {
			continue
		}
		sum += rate
		if roll < sum {
			chosenQuality = SpiritQualityNames[i]
			break
		}
	}
	return createServantRecord(db, userID, chosenQuality, zone)
}

// CreateSpiritServantWithTx 在事务内创建一只灵侍（品阶已由调用方决定）
func CreateSpiritServantWithTx(tx *gorm.DB, userID int64, quality string, zone SpiritZone) (*UserSpiritServant, error) {
	return createServantRecord(tx, userID, quality, zone)
}

// createServantRecord 通用创建逻辑：随机名+属性+基础属性，落库
func createServantRecord(tx *gorm.DB, userID int64, chosenQuality string, zone SpiritZone) (*UserSpiritServant, error) {

	// 同名重抽（默认最多 30 次）
	name := "灵侍"
	for retry := 0; retry < 30; retry++ {
		pool := ServantNamePool[chosenQuality]
		if len(pool) == 0 {
			break
		}
		name = pool[rand.Intn(len(pool))].Name
		if !CheckDuplicateName(userID, name) {
			break
		}
	}

	ser := &UserSpiritServant{
		UserID:    userID,
		Name:      name,
		Quality:   chosenQuality,
		Attribute: pickServantAttribute(chosenQuality),
		Level:     1,
		Star:      1,
		HP:        applyBaseStat(chosenQuality, QualityBasePower[chosenQuality]),
		ATK:       applyBaseStat(chosenQuality, 20),
		DEF:       applyBaseStat(chosenQuality, 15),
		SPD:       applyBaseStat(chosenQuality, 10),
		MAG:       applyBaseStat(chosenQuality, 12),
	}
	if err := db.Create(ser).Error; err != nil {
		return nil, err
	}
	log.Printf("[灵侍] 生成 user=%d zone=%s quality=%s name=%s", userID, zone.Key, chosenQuality, name)
	return ser, nil
}

// pickServantAttribute 随机属性；阴/阳仅地阶及以上可能出现
func pickServantAttribute(quality string) string {
	idx := QualityIndex(quality)
	pool := SpiritAttributes[:5] // 凡/灵/玄阶仅五行
	if idx >= QualityIndex("地") {
		pool = SpiritAttributes // 地阶及以上含阴阳
	}
	return pool[rand.Intn(len(pool))]
}

func applyBaseStat(quality string, base int) int {
	return base * int(QualityGrowth[quality])
}

func GetBattlePower(s *UserSpiritServant) int {
	return int(float64(s.HP+s.ATK+s.DEF+s.SPD+s.MAG) * QualityGrowth[s.Quality])
}

func GetLevelUpRequirement(s *UserSpiritServant) int {
	return 10 + s.Level*15
}

// FeedSpirit 喂养升级骨架：按次数提升等级，受等级上限约束
func FeedSpirit(tx *gorm.DB, userID int64, servantID uint, amount int) error {
	var s UserSpiritServant
	if err := tx.Where("id = ? AND user_id = ?", servantID, userID).First(&s).Error; err != nil {
		return err
	}
	maxLevel := MaxLevelByStar(s.Star)
	if s.Level >= maxLevel {
		return fmt.Errorf("已达当前星级等级上限：%d级", maxLevel)
	}
	if amount <= 0 {
		return fmt.Errorf("喂养次数必须大于0")
	}
	s.Level += amount
	if s.Level > maxLevel {
		s.Level = maxLevel
	}
	return tx.Save(&s).Error
}

// StarUpgrade 升星：祭品需与目标同品阶同星级，且不能使用自己/锁定/出战中灵侍
func StarUpgrade(tx *gorm.DB, userID int64, targetServantID uint, sacrificeIDs []uint) error {
	var target UserSpiritServant
	if err := tx.Where("id = ? AND user_id = ?", targetServantID, userID).First(&target).Error; err != nil {
		return fmt.Errorf("灵侍不存在")
	}
	maxStar := QualityMaxStar[target.Quality]
	if target.Star >= maxStar {
		return fmt.Errorf("%s品阶已达星级上限：%d星", target.Quality, maxStar)
	}
	if target.IsDeployed {
		return fmt.Errorf("出战中的灵侍不能升星")
	}
	if len(sacrificeIDs) < 1 {
		return fmt.Errorf("至少需要1个祭品")
	}

	for _, sid := range sacrificeIDs {
		if sid == targetServantID {
			return fmt.Errorf("不能用自己作祭品")
		}
		var sacrifice UserSpiritServant
		if err := tx.Where("id = ? AND user_id = ?", sid, userID).First(&sacrifice).Error; err != nil {
			return fmt.Errorf("祭品无效：%d", sid)
		}
		if sacrifice.Quality != target.Quality {
			return fmt.Errorf("祭品品阶不匹配：需%s品阶", target.Quality)
		}
		if sacrifice.Star != target.Star {
			return fmt.Errorf("祭品星级不匹配：需%d星", target.Star)
		}
		if sacrifice.IsLocked {
			return fmt.Errorf("祭品已锁定不能消耗")
		}
		if sacrifice.IsDeployed {
			return fmt.Errorf("出战中的祭品不能消耗")
		}
	}

	// 消耗祭品
	for _, sid := range sacrificeIDs {
		if err := tx.Where("id = ? AND user_id = ?", sid, userID).Delete(&UserSpiritServant{}).Error; err != nil {
			return fmt.Errorf("删除祭品失败")
		}
	}

	// 升星日志
	newStar := target.Star + 1
	var firstSacrificeID uint
	if len(sacrificeIDs) > 0 {
		firstSacrificeID = sacrificeIDs[0]
	}
	logRecord := ServantStarUpLog{
		UserID:             userID,
		ServantID:          targetServantID,
		NewStar:            newStar,
		SacrificeQuality:   target.Quality,
		SacrificeAttribute: target.Attribute,
		SacrificeID:        firstSacrificeID,
		SpiritCost:         0,
		Remark:             fmt.Sprintf("星级提升:%d->%d, 消耗祭品%d件", target.Star, newStar, len(sacrificeIDs)),
	}
	if err := tx.Create(&logRecord).Error; err != nil {
		return fmt.Errorf("升星日志写入失败")
	}

	target.Star = newStar
	target.Level = 1
	if err := tx.Save(&target).Error; err != nil {
		return fmt.Errorf("升星保存失败")
	}
	log.Printf("[灵侍] 升星成功 user=%d name=%s new_star=%d sacrifices=%d", userID, target.Name, newStar, len(sacrificeIDs))
	return nil
}

// TeamDeploy 上阵/下阵
func TeamDeploy(tx *gorm.DB, userID int64, servantID uint, deployed bool) error {
	var s UserSpiritServant
	if err := tx.Where("id = ? AND user_id = ?", servantID, userID).First(&s).Error; err != nil {
		return err
	}
	s.IsDeployed = deployed
	return tx.Save(&s).Error
}
