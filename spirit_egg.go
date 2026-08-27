package main

// ==========================================
// 灵侍蛋 · 推图掉落（Phase 1 扩展）
//
// 设计（用户已确认）：
// - 普通关每次胜利 10% 掉蛋（扫荡不掉）：凡人~结丹(ch1-4)最多地品；元婴及以上(ch5+)可掉天品。
// - 章节 Boss（stage 11）每次胜利 30% 掉蛋（扫荡不掉）：化神及以前(ch1-6)最多天品；化神以后(ch7+)可掉圣品。
// - 所有品阶都有概率（低阶不消失），图难度越高品质权重整体上移；天/圣为最高档、低概率。
// - 蛋入蛋库存（绑定 UserID），孵化消耗蛋 → 生成对应品质灵侍
//
// 资产规则：
// - 掉蛋在 PveFight 事务内创建（与体力消耗/奖励同事务）
// - 孵化：蛋状态条件更新（bag→hatched）防并发重复孵化
// - 灵侍生成复用 CreateSpiritServantWithTx 同路径，数值口径与捕捉一致
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
	bossEggDropRate   = 30 // Boss 胜利掉蛋概率（%）
	normalEggDropRate = 10 // 普通关胜利掉蛋概率（%）
	eggStatusBag      = "bag"
	eggStatusHatch    = "hatched"
)

// spiritEggQualities 蛋品质顺序（与章节权重列对齐，共 6 品）
var spiritEggQualities = []string{"凡", "灵", "玄", "地", "天", "圣"}

// normalStageEggWeights 普通关掉蛋品质权重（凡/灵/玄/地/天/圣，合计 100）。
// 普通关：凡人~结丹（ch1-4）最多地品；元婴及以上（ch5+）可掉天品；永远不掉圣品。
var normalStageEggWeights = [14][6]int{
	{55, 32, 10, 3, 0, 0},   // ch1 青竹林海
	{45, 34, 15, 6, 0, 0},   // ch2 迷雾深谷
	{35, 35, 21, 9, 0, 0},   // ch3 断岳山脉
	{28, 34, 26, 12, 0, 0},  // ch4 幽冥绝岭
	{22, 32, 28, 15, 3, 0},  // ch5 归墟海眼（元婴，可出天品）
	{18, 29, 29, 18, 6, 0},  // ch6 不周山巅
	{15, 27, 28, 21, 9, 0},  // ch7 问道星海
	{12, 25, 28, 24, 11, 0}, // ch8 两仪秘境
	{9, 23, 27, 27, 14, 0},  // ch9 归元天阙
	{7, 21, 26, 29, 17, 0},  // ch10 仙庭残迹
	{5, 19, 25, 31, 20, 0},  // ch11 赤金天池
	{4, 17, 24, 32, 23, 0},  // ch12 太一仙山
	{3, 15, 23, 34, 25, 0},  // ch13 大罗天境
	{2, 13, 22, 35, 28, 0},  // ch14 混元祖庭
}

// bossStageEggWeights Boss 关掉蛋品质权重（凡/灵/玄/地/天/圣，合计 100）。
// Boss：化神及以前（ch1-6）最多天品；化神以后（ch7+）可掉圣品。
var bossStageEggWeights = [14][6]int{
	{40, 38, 16, 5, 1, 0},  // ch1 青竹林海
	{33, 38, 20, 7, 2, 0},  // ch2 迷雾深谷
	{26, 37, 24, 10, 3, 0}, // ch3 断岳山脉
	{20, 36, 27, 13, 4, 0}, // ch4 幽冥绝岭
	{15, 34, 29, 16, 6, 0}, // ch5 归墟海眼
	{11, 32, 30, 19, 8, 0}, // ch6 不周山巅
	{9, 29, 30, 21, 10, 1}, // ch7 问道星海（炼虚，可出圣品）
	{8, 27, 29, 23, 11, 2}, // ch8 两仪秘境
	{7, 25, 28, 25, 12, 3}, // ch9 归元天阙
	{6, 23, 27, 27, 13, 4}, // ch10 仙庭残迹
	{5, 21, 26, 29, 14, 5}, // ch11 赤金天池
	{4, 19, 25, 31, 15, 6}, // ch12 太一仙山
	{3, 17, 24, 33, 16, 7}, // ch13 大罗天境
	{2, 15, 23, 35, 17, 8}, // ch14 混元祖庭
}

// rollEggDrop 按权重行滚动一次品质；rate 为掉蛋概率（%），不掉返回 ""。
// weights 为品质权重行切片（与 spiritEggQualities 列对齐，合计 100）。
func rollEggDrop(weights []int, rate int) string {
	if len(weights) == 0 || rand.Intn(100) >= rate {
		return ""
	}
	roll := rand.Intn(100)
	sum := 0
	for i, r := range weights {
		sum += r
		if roll < sum {
			if i >= 0 && i < len(spiritEggQualities) {
				return spiritEggQualities[i]
			}
			return ""
		}
	}
	return spiritEggQualities[0]
}

// rollBossEggDrop 判定一次 Boss 胜利是否掉蛋（30%），返回品质（"" = 不掉）
func rollBossEggDrop(chapterID int) string {
	if chapterID < 1 || chapterID > len(bossStageEggWeights) {
		return ""
	}
	return rollEggDrop(bossStageEggWeights[chapterID-1][:], bossEggDropRate)
}

// rollNormalEggDrop 判定一次普通关胜利是否掉蛋（10%），返回品质（"" = 不掉）
func rollNormalEggDrop(chapterID int) string {
	if chapterID < 1 || chapterID > len(normalStageEggWeights) {
		return ""
	}
	return rollEggDrop(normalStageEggWeights[chapterID-1][:], normalEggDropRate)
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
	return createEggInTx(tx, userID, quality, zone)
}

// dropNormalEgg 在 PveFight 事务内创建普通关掉蛋（10%），不掉时返回 nil
func dropNormalEgg(tx *gorm.DB, userID int64, chapterID int, zone *SpiritZone) (*SpiritEgg, error) {
	quality := rollNormalEggDrop(chapterID)
	if quality == "" {
		return nil, nil
	}
	return createEggInTx(tx, userID, quality, zone)
}

// createEggInTx 在事务内落一颗灵侍蛋（调用方保证品质已判定）
func createEggInTx(tx *gorm.DB, userID int64, quality string, zone *SpiritZone) (*SpiritEgg, error) {
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

// ListEggs 返回未孵化蛋（新→旧，分页；page 从 1 起，越界钳制）与最近孵化历史（≤5）
// 未孵化蛋无持有上限，必须分页保证所有蛋可达（否则老蛋无法孵化）
func ListEggs(userID int64, page, pageSize int) (bag []SpiritEgg, hatched []SpiritEgg, total int64) {
	db.Model(&SpiritEgg{}).Where("user_id = ? AND status = ?", userID, eggStatusBag).Count(&total)
	if total > 0 {
		if pageSize <= 0 {
			pageSize = 10
		}
		if page < 1 {
			page = 1
		}
		maxPage := int((total + int64(pageSize) - 1) / int64(pageSize))
		if page > maxPage {
			page = maxPage
		}
		db.Where("user_id = ? AND status = ?", userID, eggStatusBag).
			Order("id desc").Offset((page - 1) * pageSize).Limit(pageSize).Find(&bag)
	}
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
