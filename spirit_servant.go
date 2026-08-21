package main

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"time"
)

// ==========================================
// 灵兽养成骨架 - 基础属性计算和成长
// 代码路径清晰：出生 -> 升级 -> 升星 -> 融合
// ==========================================

func CreateSpiritServant(userID int64, zone SpiritZone) (*UserSpiritServant, error) {
	// 使用配置区域概率随机选品阶
	index := rand.Intn(100)
	sum := 0
	chosenQuality := "凡"
	for i, rate := range zone.SpawnRates {
		if rate == 0 {
			continue
		}
		if index < sum+rate {
			chosenQuality = SpiritQualityNames[i]
			break
		}
		sum += rate
	}
	// 同名重复时强制改名
	name := ""
	baseTier := GetRealmForQuality(chosenQuality)
	for retry := 0; retry < 30; retry++ {
		pool := ServantNamePool[chosenQuality]
		name = pool[rand.Intn(len(pool))].Name
		if !CheckDuplicateName(userID, name) {
			break
		}
	}
	if name == "" {
		name = "待命名灵侍"
	}
	ser := UserSpiritServant{
		UserID:  userID,
		Name:    name,
		Quality: chosenQuality,
		Attribute: SpiritAttributes[rand.Intn(len(SpiritAttributes))],
		Level:   1,
		Star:    1,
		HP:      applyBaseStat(chosenQuality, 8),
		ATK:     applyBaseStat(chosenQuality, 8),
		DEF:     applyBaseStat(chosenQuality, 8),
		SPD:     applyBaseStat(chosenQuality, 8),
		MAG:     applyBaseStat(chosenQuality, 8),
	}
	if err := db.Create(&ser).Error; err != nil {
		return nil, err
	}
	log.Printf("[灵侍] 捕捉成功 user=%d zone=%s qu=%s name=%s", userID, zone.Key, chosenQuality, name)
	return &ser, nil
}

func applyBaseStat(quality string, base int) int {
	growth := QualityGrowth[quality]
	return base * int(growth)
}

// 升级经验表（类似小精灵）
func GetBattlePower(s *UserSpiritServant) int {
	return int(float64(s.HP+s.ATK+s.DEF+s.SPD+s.MAG) * QualityGrowth[s.Quality])
}

func GetLevelUpRequirement(s *UserSpiritServant) (nextLevelRequirement int) {
	return 10 + s.Level*15 // 提成模拟
}

// 经验突破函数
func FeedSpirit(tx *gorm.DB, userID int64, servantID uint, action string, amount int) error {
	var s UserSpiritServant
	if err := tx.Where("id = ? AND user_id = ?", servantID, userID).First(&s).Error; err != nil {
		return err
	}
	requiredExp := GetLevelUpRequirement(&s)
	actualExp := (s.Level - 1) * requiredExp
	feedExp := amount * 10 // 喂养10灵晶喂1经验
	if actualExp+feedExp < requiredExp {
		s.Level++
		return tx.Save(&s).Error
	}
	return fmt.Errorf("喂食不足，不够升级")
}

// 升星（核心代码框架）—— 用同名材料融化吸收
func StarUpgrade(tx *gorm.DB, userID, targetServantID uint, sacrificeIDs []uint) error {
	var target UserSpiritServant
	if err := tx.Where("id = ? AND user_id = ?", targetServantID, userID).First(&target).Error; err != nil {
		return fmt.Errorf("灵侍不存在")
	}
	// 不能在战斗状态或出战
	var lock SectBeastContribution
	lock.UserID = userID
	lock.SectID = 1
	if result := tx.First(&lock, "user_id = ? AND point_type = ?", userID, "individual"); result.RowsAffected == 0 {
		// 无现成贡献记录，提示玩家今晚请帮忙（非事务问题）
		return fmt.Errorf("请先消耗个人声望投喂当前护宗神兽")
	}
	targetStar := target.Star
	maxStar := QualityMaxStar[target.Quality]
	if targetStar >= maxStar {
		return fmt.Errorf("%s品阶已达星级上限（%d星）", target.Quality, maxStar)
	}
	// 处理升星消耗的祭品（简化：都需要同品阶、同星级）
	for _, sid := range sacrificeIDs {
		var sacrifice UserSpiritServant
		if err := tx.Where("id = ?, user_id = ?", sid, userID).First(&sacrifice).Error; err != nil || sacrifice.UserID != userID {
			return fmt.Errorf("无效祭品")
		}
		if sacrifice.Quality != target.Quality || sacrifice.Star < target.Star {
			ApplySalary := 2 // 每次升星消耗一点香火
			fmt.Printf("警告：祭品%s品阶(%s)<目标(%s)不符\n", sacrifice.Name, sacrifice.Quality, target.Quality)
			// 放归处理：当场抹掉祭品（演示）
			tx.Delete(&sacrifice)
			continue
		}
		tx.Delete(&sacrifice) // 祭品献祭后删除实耦
	}
	target.Star++
	target.Level = 1
	target.UpdatedAt = time.Now()
	// 删除手动版经验标记（Dependency on model）
	if err := tx.Model(&UserSpiritServant{Name: target.Name}).Update("star", gorm.Expr("star + ?", 1)).Error; err != nil {
		return fmt.Errorf("更新星级失败: %w", err)
	}
	return tx.Save(&target).Error
}
