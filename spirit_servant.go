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

// StarUpgrade 升星（三段制祭品 + 道具替代）：
// - 段1（升至 ≤3 星）：祭品同品阶 + 同属性（不限星级）
// - 段2（升至 4-6 星）：同品阶 + 同属性 + 同星级（祭品星级=目标当前星级）
// - 段3（升至 7-9 星）：同名 + 同星级（祭品星级=目标当前星级）
// 道具替代：
//   - 灵魄（useItem=itemTypeSoul）：段1-2，无需灵侍祭品，消耗 1 灵魄
//   - 万能真身碎片（useItem=itemTypeShard）：段3，替代「同名」要求——
//     祭品仅需同品质 + 同星级（不限属性/名称），消耗 1 碎片
//
// 祭品不能是自己/已锁定/出战中；成功后等级重置为 1，祭品被消耗（软删除）
func StarUpgrade(tx *gorm.DB, userID int64, targetServantID uint, sacrificeIDs []uint, useItem string) error {
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

	nextStar := target.Star + 1
	stage := StarUpStage(nextStar)

	// 道具消耗校验（在事务内条件更新，失败整体回滚）
	switch useItem {
	case itemTypeSoul:
		if stage > 2 {
			return fmt.Errorf("灵魄仅可用于升至 6 星及以下的升星")
		}
		if len(sacrificeIDs) != 0 {
			return fmt.Errorf("灵魄升星无需提供祭品灵侍")
		}
		if err := spendSpiritItemTx(tx, userID, itemTypeSoul, 1); err != nil {
			return err
		}
	case itemTypeShard:
		if stage != 3 {
			return fmt.Errorf("真身碎片仅可用于升至 7 星及以上的升星")
		}
		if len(sacrificeIDs) != 1 {
			return fmt.Errorf("碎片升星需且仅需 1 只祭品灵侍")
		}
		if err := spendSpiritItemTx(tx, userID, itemTypeShard, 1); err != nil {
			return err
		}
	case "":
		if len(sacrificeIDs) < 1 {
			return fmt.Errorf("至少需要1个祭品")
		}
	default:
		return fmt.Errorf("未知升星道具")
	}

	if len(sacrificeIDs) > 0 {
		seen := map[uint]bool{}
		for _, sid := range sacrificeIDs {
			if seen[sid] {
				return fmt.Errorf("祭品重复")
			}
			seen[sid] = true
		}

		for _, sid := range sacrificeIDs {
			if sid == targetServantID {
				return fmt.Errorf("不能用自己作祭品")
			}
			var sacrifice UserSpiritServant
			if err := tx.Where("id = ? AND user_id = ?", sid, userID).First(&sacrifice).Error; err != nil {
				return fmt.Errorf("祭品无效：%d", sid)
			}
			if sacrifice.IsLocked {
				return fmt.Errorf("祭品已锁定不能消耗")
			}
			if sacrifice.IsDeployed {
				return fmt.Errorf("出战中的祭品不能消耗")
			}
			if useItem == itemTypeShard {
				// 碎片路径：同品质 + 同星级（不限属性/名称）
				if sacrifice.Quality != target.Quality {
					return fmt.Errorf("祭品品阶不匹配：需%s品阶", target.Quality)
				}
				if sacrifice.Star != target.Star {
					return fmt.Errorf("祭品星级不匹配：需%d星", target.Star)
				}
				continue
			}
			switch {
			case nextStar <= 3: // 段1：同品阶 + 同属性
				if sacrifice.Quality != target.Quality {
					return fmt.Errorf("祭品品阶不匹配：需%s品阶", target.Quality)
				}
				if sacrifice.Attribute != target.Attribute {
					return fmt.Errorf("祭品属性不匹配：需%s属性", target.Attribute)
				}
			case nextStar <= 6: // 段2：同品阶 + 同属性 + 同星级
				if sacrifice.Quality != target.Quality {
					return fmt.Errorf("祭品品阶不匹配：需%s品阶", target.Quality)
				}
				if sacrifice.Attribute != target.Attribute {
					return fmt.Errorf("祭品属性不匹配：需%s属性", target.Attribute)
				}
				if sacrifice.Star != target.Star {
					return fmt.Errorf("祭品星级不匹配：需%d星", target.Star)
				}
			default: // 段3：同名 + 同星级
				if sacrifice.Name != target.Name {
					return fmt.Errorf("祭品名称不匹配：需%s", target.Name)
				}
				if sacrifice.Star != target.Star {
					return fmt.Errorf("祭品星级不匹配：需%d星", target.Star)
				}
			}
		}

		// 消耗祭品
		for _, sid := range sacrificeIDs {
			if err := tx.Where("id = ? AND user_id = ?", sid, userID).Delete(&UserSpiritServant{}).Error; err != nil {
				return fmt.Errorf("删除祭品失败")
			}
		}
	}

	// 升星日志
	var firstSacrificeID uint
	if len(sacrificeIDs) > 0 {
		firstSacrificeID = sacrificeIDs[0]
	}
	remark := fmt.Sprintf("星级提升:%d->%d, 消耗祭品%d件", target.Star, nextStar, len(sacrificeIDs))
	if useItem == itemTypeSoul {
		remark = fmt.Sprintf("星级提升:%d->%d, 消耗灵魄1个", target.Star, nextStar)
	} else if useItem == itemTypeShard {
		remark = fmt.Sprintf("星级提升:%d->%d, 消耗真身碎片1个+祭品1件", target.Star, nextStar)
	}
	logRecord := ServantStarUpLog{
		UserID:             userID,
		ServantID:          targetServantID,
		NewStar:            nextStar,
		SacrificeQuality:   target.Quality,
		SacrificeAttribute: target.Attribute,
		SacrificeID:        firstSacrificeID,
		SpiritCost:         0,
		Remark:             remark,
	}
	if err := tx.Create(&logRecord).Error; err != nil {
		return fmt.Errorf("升星日志写入失败")
	}

	target.Star = nextStar
	target.Level = 1
	if err := tx.Save(&target).Error; err != nil {
		return fmt.Errorf("升星保存失败")
	}
	log.Printf("[灵侍] 升星成功 user=%d name=%s new_star=%d sacrifices=%d item=%s",
		userID, target.Name, nextStar, len(sacrificeIDs), useItem)
	return nil
}

// ListShardSacrifices 列出真身碎片可用祭品（同品质+同星级，不限属性/名称；排除自己/锁定/出战中）
func ListShardSacrifices(userID int64, target *UserSpiritServant) []UserSpiritServant {
	var cands []UserSpiritServant
	if err := db.Where("user_id = ?", userID).
		Order("star desc, level desc").Find(&cands).Error; err != nil {
		return nil
	}
	var out []UserSpiritServant
	for _, c := range cands {
		if c.ID == target.ID || c.IsLocked || c.IsDeployed {
			continue
		}
		if c.Quality == target.Quality && c.Star == target.Star {
			out = append(out, c)
		}
	}
	return out
}

// StarUpStage 升至 nextStar 所属的段（1/2/3）
func StarUpStage(nextStar int) int {
	switch {
	case nextStar <= 3:
		return 1
	case nextStar <= 6:
		return 2
	default:
		return 3
	}
}

// StarUpRequirementText 下一次升星的祭品需求说明
func StarUpRequirementText(t *UserSpiritServant) string {
	nextStar := t.Star + 1
	switch {
	case nextStar <= 3:
		return fmt.Sprintf("祭品：1 只同品阶（%s）、同属性（%s）灵侍，不限星级", t.Quality, t.Attribute)
	case nextStar <= 6:
		return fmt.Sprintf("祭品：1 只同品阶（%s）、同属性（%s）、同星级（%d星）灵侍", t.Quality, t.Attribute, t.Star)
	default:
		return fmt.Sprintf("祭品：1 只同名（%s）、同星级（%d星）灵侍", t.Name, t.Star)
	}
}

// ListStarUpSacrifices 列出下一星可用的祭品（排除自己/锁定/出战中，按段规则过滤）
func ListStarUpSacrifices(userID int64, target *UserSpiritServant) []UserSpiritServant {
	var cands []UserSpiritServant
	if err := db.Where("user_id = ?", userID).
		Order("star desc, level desc").Find(&cands).Error; err != nil {
		return nil
	}
	nextStar := target.Star + 1
	var out []UserSpiritServant
	for _, c := range cands {
		if c.ID == target.ID || c.IsLocked || c.IsDeployed {
			continue
		}
		var ok bool
		switch {
		case nextStar <= 3:
			ok = c.Quality == target.Quality && c.Attribute == target.Attribute
		case nextStar <= 6:
			ok = c.Quality == target.Quality && c.Attribute == target.Attribute && c.Star == target.Star
		default:
			ok = c.Name == target.Name && c.Star == target.Star
		}
		if ok {
			out = append(out, c)
		}
	}
	return out
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
