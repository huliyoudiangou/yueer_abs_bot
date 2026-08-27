package main

// ==========================================
// 灵侍道具（Phase 2-A）：灵魄 / 万能真身碎片
//
// 设计（Phase 2 范围已用户确认，掉落率为推断项）：
// - 灵魄（lingpo）：PVE 胜利掉落（普通关 25% / 章节 Boss 50%，扫荡不掉）
//   用途：升星段1-2（升至 ≤6★）时替代 1 只灵侍祭品
// - 万能真身碎片（shard）：第 5/6 章 Boss 胜利 10% 掉落
//   用途：升星段3（升至 7-9★）时替代「同名」要求——
//   消耗 1 只同品质同星级灵侍（不限属性/名称）+ 1 碎片
//
// 资产规则：
// - 获取在 PveFight 事务内（条件 upsert，count + n）
// - 消耗用条件更新 count >= n（防并发超扣），RowsAffected=0 回滚
// ==========================================

import (
	"errors"
	"fmt"
	"math/rand"

	"gorm.io/gorm"
)

const (
	itemTypeSoul  = "lingpo" // 灵魄
	itemTypeShard = "shard"  // 万能真身碎片

	soulDropRateNormal = 25 // 普通关胜利掉灵魄概率（%）
	soulDropRateBoss   = 50 // 章节 Boss 胜利掉灵魄概率（%）
	shardDropRate      = 10 // 第 5/6 章 Boss 胜利掉真身碎片概率（%）
)

// spiritItemNames 道具展示名
var spiritItemNames = map[string]string{
	itemTypeSoul:  "灵魄",
	itemTypeShard: "万能真身碎片",
}

// GetUserSpiritItems 用户道具持有量（map[itemType]count）
func GetUserSpiritItems(userID int64) map[string]int {
	var items []UserSpiritItem
	db.Where("user_id = ?", userID).Find(&items)
	m := map[string]int{}
	for _, it := range items {
		m[it.ItemType] = it.Count
	}
	return m
}

// addSpiritItemTx 事务内增加道具（upsert：不存在则创建）
func addSpiritItemTx(tx *gorm.DB, userID int64, itemType string, n int) error {
	if n <= 0 {
		return nil
	}
	var item UserSpiritItem
	if err := tx.Where("user_id = ? AND item_type = ?", userID, itemType).First(&item).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		item = UserSpiritItem{UserID: userID, ItemType: itemType, Count: n}
		if err := tx.Create(&item).Error; err != nil {
			return err
		}
		return nil
	}
	return tx.Model(&UserSpiritItem{}).Where("id = ?", item.ID).
		Update("count", gorm.Expr("count + ?", n)).Error
}

// spendSpiritItemTx 事务内消耗道具（条件更新防并发超扣）
func spendSpiritItemTx(tx *gorm.DB, userID int64, itemType string, n int) error {
	res := tx.Model(&UserSpiritItem{}).
		Where("user_id = ? AND item_type = ? AND count >= ?", userID, itemType, n).
		Update("count", gorm.Expr("count - ?", n))
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("%s不足", spiritItemNames[itemType])
	}
	return nil
}

// rollPveItemDrops 一次 PVE 胜利的道具掉落判定（返回要获得的道具类型列表，可空）
func rollPveItemDrops(chapterID, stageID int) []string {
	var drops []string
	if stageID == bossStageID {
		// 章节 Boss：灵魄 50%
		if rand.Intn(100) < soulDropRateBoss {
			drops = append(drops, itemTypeSoul)
		}
		// 第 5/6 章 Boss：真身碎片 10%
		if (chapterID == 5 || chapterID == 6) && rand.Intn(100) < shardDropRate {
			drops = append(drops, itemTypeShard)
		}
	} else if rand.Intn(100) < soulDropRateNormal {
		// 普通关：灵魄 25%
		drops = append(drops, itemTypeSoul)
	}
	return drops
}

// rollPveBossTreasureDrop 章节 Boss 掉“下一境界”至宝的判定。
// 至宝链按区域 tier（= MajorRealm）映射：仅化神(tier5)及之后的 Boss 会掉至宝。
// 返回至宝名，无掉落返回空串。
func rollPveBossTreasureDrop(chapterID int) string {
	zone := chapterZone(chapterID)
	if zone == nil {
		return ""
	}
	treasure := cultivationTreasureNameForRealm(zone.Tier)
	if treasure == "" {
		return ""
	}
	if rand.Intn(100) < cultivationTreasureDropRatePercent() {
		return treasure
	}
	return ""
}
