package main

// ==========================================
// 灵侍蛋 · 章节 Boss 掉落（Phase 1）
//
// 设计（用户已确认方向：推图 Boss 掉蛋）：
// - 击败章节 Boss（stage 11）：每次 30% 概率掉蛋（扫荡不掉）
// - 蛋品质上限地阶：凡/灵/玄/地，权重随章节推进向地阶偏移
// - 蛋入蛋库存（绑定 UserID），孵化消耗蛋 → 生成对应品质灵侍
//
// 资产规则：
// - 掉蛋在 PveFight 事务内创建（与体力消耗/奖励同事务）
// - 孵化：蛋状态条件更新（bag→hatched）防并发重复孵化
// - 灵侍生成复用 CreateSpiritServantRecord 同路径，数值口径与捕捉一致
// ==========================================

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"time"

	"gorm.io/gorm"
)

const (
	bossEggDropRate = 30 // Boss 胜利掉蛋概率（%）
	eggStatusBag    = "bag"
	eggStatusHatch  = "hatched"
)

// spiritEggQualities 蛋品质顺序（与 chapterEggWeights 列对齐）
var spiritEggQualities = []string{"凡", "灵", "玄", "地"}

// chapterEggWeights 章节→蛋品质权重（凡/灵/玄/地，合计 100），越高章越倾向地阶
var chapterEggWeights = [6][4]int{
	{60, 30, 8, 2},   // ch1 青竹林海
	{40, 35, 18, 7},  // ch2 迷雾深谷
	{25, 35, 27, 13}, // ch3 断岳山脉
	{15, 30, 34, 21}, // ch4 幽冥绝岭
	{8, 27, 38, 27},  // ch5 归墟海眼
	{3, 22, 40, 35},  // ch6 不周山巅
}

// rollBossEggDrop 判定一次 Boss 胜利是否掉蛋，返回品质（"" = 不掉）
func rollBossEggDrop(chapterID int) string {
	if rand.Intn(100) >= bossEggDropRate {
		return ""
	}
	w := chapterEggWeights[chapterID-1]
	roll := rand.Intn(100)
	sum := 0
	for i, r := range w {
		sum += r
		if roll < sum {
			return spiritEggQualities[i]
		}
	}
	return spiritEggQualities[0]
}

// spiritZoneByKey 按 key 查灵墟（孵化需还原来源区域）
func spiritZoneByKey(key string) *SpiritZone {
	for i := range SpiritZones {
		if SpiritZones[i].Key == key {
			return &SpiritZones[i]
		}
	}
	return nil
}

// dropBossEgg 在 PveFight 事务内创建蛋（调用方保证 Boss 胜利），不掉时返回 nil
func dropBossEgg(tx *gorm.DB, userID int64, chapterID int, zone *SpiritZone) (*SpiritEgg, error) {
	quality := rollBossEggDrop(chapterID)
	if quality == "" {
		return nil, nil
	}
	egg := &SpiritEgg{
		UserID:   userID,
		Quality:  quality,
		ZoneKey:  zone.Key,
		ZoneName: zone.Name,
		Status:   eggStatusBag,
	}
	if err := tx.Create(egg).Error; err != nil {
		return nil, err
	}
	return egg, nil
}

// ListEggs 返回未孵化蛋（≤20）与最近孵化历史（≤5）
func ListEggs(userID int64) (bag []SpiritEgg, hatched []SpiritEgg) {
	db.Where("user_id = ? AND status = ?", userID, eggStatusBag).
		Order("id desc").Limit(20).Find(&bag)
	db.Where("user_id = ? AND status = ?", userID, eggStatusHatch).
		Order("id desc").Limit(5).Find(&hatched)
	return
}

// HatchEgg 孵化灵侍蛋（消耗蛋 → 生成同品质灵侍）
func HatchEgg(userID int64, eggID uint) (*UserSpiritServant, error) {
	var servant *UserSpiritServant
	err := db.Transaction(func(tx *gorm.DB) error {
		var egg SpiritEgg
		if err := tx.Where("id = ? AND user_id = ?", eggID, userID).First(&egg).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return fmt.Errorf("灵侍蛋不存在或不属于你")
			}
			return err
		}
		// 条件更新：仅 bag 态蛋可孵化（防并发重复孵化）
		now := time.Now()
		res := tx.Model(&SpiritEgg{}).
			Where("id = ? AND user_id = ? AND status = ?", eggID, userID, eggStatusBag).
			Updates(map[string]interface{}{"status": eggStatusHatch, "hatched_at": now})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("此灵侍蛋已孵化")
		}
		zone := spiritZoneByKey(egg.ZoneKey)
		if zone == nil {
			zone = &SpiritZones[0] // 兜底：来源区域缺失
		}
		ser, err := CreateSpiritServantWithTx(tx, userID, egg.Quality, *zone)
		if err != nil {
			return err
		}
		servant = ser
		return nil
	})
	if err != nil {
		log.Printf("[灵侍] 蛋孵化失败 user=%d egg=%d err=%s", userID, eggID, formatTelegramSendError(err))
		return nil, err
	}
	log.Printf("[灵侍] 蛋孵化成功 user=%d egg=%d servant=%d", userID, eggID, servant.ID)
	return servant, nil
}
