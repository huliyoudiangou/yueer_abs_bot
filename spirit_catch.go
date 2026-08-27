package main

import (
	"errors"
	"fmt"
	"log"
	"math/rand"

	"gorm.io/gorm"
)

// ==========================================
// 灵墟捕捉逻辑（Phase 1）
// 消耗灵晶 → 滚动品阶（含保底）→ 滚动捕捉成功率 → 落库
// ==========================================

// CatchResult 捕捉结果
type CatchResult struct {
	Success    bool               // 是否成功捕捉
	Servant    *UserSpiritServant // 成功时为灵侍指针
	EncounterQ string             // 遭遇的品阶
	Escape     bool               // 灵侍是否逃脱
	UsedTianP  bool               // 是否触发天品保底
	UsedShengP bool               // 是否触发圣品保底
	Pity       *SpiritZonePity    // 更新后的保底记录
}

// CatchSpiritServant 执行一次捕捉
// 全程在事务内完成：消耗灵晶 → 保底判断 → 品阶滚动 → 捕捉率滚动 → 落库
func CatchSpiritServant(tx *gorm.DB, userID int64, zoneKey, ropeKey string) (*CatchResult, error) {
	result := &CatchResult{}

	// 1. 查找区域
	var zone *SpiritZone
	for i := range SpiritZones {
		if SpiritZones[i].Key == zoneKey {
			zone = &SpiritZones[i]
			break
		}
	}
	if zone == nil {
		return nil, fmt.Errorf("未知灵墟区域")
	}

	// 2. 查找灵索
	var rope *SpiritRope
	for i := range SpiritRopes {
		if SpiritRopes[i].Key == ropeKey {
			rope = &SpiritRopes[i]
			break
		}
	}
	if rope == nil {
		return nil, fmt.Errorf("未知灵索")
	}

	// 3. 境界校验（不硬编码境界名，通过 MajorRealm 数值比较）
	// 走 tx 读取（不查全局连接池：本函数在捕捉事务内运行，小连接池下全局查询会死等）
	var cul Cultivation
	majorRealm := 0
	if err := tx.Where("user_id = ?", userID).First(&cul).Error; err == nil {
		majorRealm = cul.MajorRealm
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, err
	}
	if majorRealm < zone.Tier {
		return nil, fmt.Errorf("境界不足，需提升修为后方可进入此区域")
	}

	// 4. 消耗灵晶（SpendLingjing 内部已有余额校验）
	if err := SpendLingjing(tx, userID, rope.Cost, "consume_catch",
		fmt.Sprintf("灵墟捕捉：%s / %s", zone.Name, rope.Name)); err != nil {
		return nil, err
	}

	// 5. 获取或创建保底记录
	pity := getOrCreateSpiritPity(tx, userID, zone.Key)

	// 6. 保底判断（优先级：圣品保底 > 天品保底 > 正常滚动）
	pity.TotalPulls++

	// 圣品保底（仅区域4-5）
	if threshold, ok := ShengPityThreshold[zone.Key]; ok && pity.ShengPity >= threshold {
		result.UsedShengP = true
		result.EncounterQ = "圣"
		servant, err := CreateSpiritServantWithTx(tx, userID, "圣", *zone)
		if err != nil {
			return nil, fmt.Errorf("保底圣品创建失败: %s", formatTelegramSendError(err))
		}
		pity.ShengPity = 0
		pity.TianPity = 0 // 圣品也重置天品保底
		result.Success = true
		result.Servant = servant
		result.Pity = pity
		if err := tx.Save(pity).Error; err != nil {
			return nil, fmt.Errorf("保底记录保存失败: %s", formatTelegramSendError(err))
		}
		return result, nil
	}

	// 天品保底
	if pity.TianPity >= TianPityThreshold {
		result.UsedTianP = true
		result.EncounterQ = "天"
		servant, err := CreateSpiritServantWithTx(tx, userID, "天", *zone)
		if err != nil {
			return nil, fmt.Errorf("保底天品创建失败: %s", formatTelegramSendError(err))
		}
		pity.TianPity = 0
		result.Success = true
		result.Servant = servant
		result.Pity = pity
		if err := tx.Save(pity).Error; err != nil {
			return nil, fmt.Errorf("保底记录保存失败: %s", formatTelegramSendError(err))
		}
		return result, nil
	}

	// 7. 正常滚动品阶（万分率）
	encounterQ := rollZoneQuality(*zone)
	result.EncounterQ = encounterQ

	// 8. 滚动捕捉成功率
	baseRate := CaptureSuccessRate[encounterQ]
	captureRate := baseRate + rope.Bonus
	if captureRate > 1.0 {
		captureRate = 1.0
	}

	if rand.Float64() < captureRate {
		// 9a. 捕捉成功
		servant, err := CreateSpiritServantWithTx(tx, userID, encounterQ, *zone)
		if err != nil {
			return nil, fmt.Errorf("灵侍创建失败: %s", formatTelegramSendError(err))
		}
		result.Success = true
		result.Servant = servant

		// 获得天品及以上 → 重置天品保底
		if QualityIndex(encounterQ) >= QualityIndex("天") {
			pity.TianPity = 0
		}
		// 获得圣品 → 重置圣品保底
		if QualityIndex(encounterQ) >= QualityIndex("圣") {
			pity.ShengPity = 0
		}
	} else {
		// 9b. 捕捉失败 ─ 灵侍逃跑了
		result.Success = false
		result.Escape = true

		// 保底计数器仍然递增（本次拉抽已发生）
		pity.TianPity++
		if _, ok := ShengPityThreshold[zone.Key]; ok {
			pity.ShengPity++
		}
	}

	result.Pity = pity
	if err := tx.Save(pity).Error; err != nil {
		return nil, fmt.Errorf("保底记录保存失败: %s", formatTelegramSendError(err))
	}
	log.Printf("[灵侍] 捕捉 user=%d zone=%s rope=%s encounter=%s success=%v tianPity=%d shengPity=%d",
		userID, zone.Key, rope.Key, encounterQ, result.Success, pity.TianPity, pity.ShengPity)
	return result, nil
}

// rollZoneQuality 按区域万分率滚动一次品阶
func rollZoneQuality(zone SpiritZone) string {
	roll := rand.Intn(10000)
	sum := 0
	for i, rate := range zone.SpawnRates {
		if rate <= 0 || i >= len(SpiritQualityNames) {
			continue
		}
		sum += rate
		if roll < sum {
			return SpiritQualityNames[i]
		}
	}
	return SpiritQualityNames[0] // 兜底：凡品
}

// getOrCreateSpiritPity 获取或创建保底记录
func getOrCreateSpiritPity(tx *gorm.DB, userID int64, zoneKey string) *SpiritZonePity {
	var pity SpiritZonePity
	err := tx.Where("user_id = ? AND zone_key = ?", userID, zoneKey).First(&pity).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		pity = SpiritZonePity{UserID: userID, ZoneKey: zoneKey}
		if e := tx.Create(&pity).Error; e != nil {
			log.Printf("[灵侍] 保底记录创建失败 user=%d zone=%s err=%s", userID, zoneKey, formatTelegramSendError(e))
		}
		return &pity
	}
	if err != nil {
		log.Printf("[灵侍] 保底查询失败 user=%d zone=%s err=%s", userID, zoneKey, formatTelegramSendError(err))
		pity = SpiritZonePity{UserID: userID, ZoneKey: zoneKey}
		if e := tx.Create(&pity).Error; e != nil {
			log.Printf("[灵侍] 保底记录创建失败 user=%d zone=%s err=%s", userID, zoneKey, formatTelegramSendError(e))
		}
		return &pity
	}
	return &pity
}

// GetUserZonePity 获取用户在某个区域的当前保底状态（供面板展示用）
func GetUserZonePity(userID int64, zoneKey string) *SpiritZonePity {
	var pity SpiritZonePity
	err := db.Where("user_id = ? AND zone_key = ?", userID, zoneKey).First(&pity).Error
	if err != nil {
		return nil
	}
	return &pity
}

// CheckZoneUnlocked 检查用户是否解锁某个灵墟区域
func CheckZoneUnlocked(userID int64, zone SpiritZone) bool {
	cul := GetOrCreateCultivation(userID)
	if cul == nil {
		return zone.Tier == 0
	}
	return cul.MajorRealm >= zone.Tier
}
