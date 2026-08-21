package main

// ==========================================
// 装备锻造 / 熔炼 / 穿戴（Phase 1）
//
// 设计（锁定）：
// - 2 槽位：兵甲（物攻向）/ 魂魄（法术向），每只灵侍每槽 1 件
// - bound_to_uid：装备绑定拥有者（UserID），不可流转
// - 锻造：灵晶消耗，品质按 目标50% / -1档30% / -2档20%（下限凡品）
// - 熔炼：返 40% 锻造成本；已穿戴/已刻名锁定不可熔炼
// - 装备加成并入战斗：PVE 推图 / 镜场攻击 / 镜像快照均使用带装备加成属性
//
// 资产规则：
// - 锻造消耗走 SpendLingjing、熔炼返还走 EarnLingjing（equip_melt 流水）
// - 全部在事务内完成；用户级锁防并发
// ==========================================

import (
	"fmt"
	"log"
	"math/rand"

	"gorm.io/gorm"
)

// 槽位定义
var equipmentSlots = []string{"兵甲", "魂魄"}

// 锻造成本（灵晶，按目标品质）
var equipmentForgeCost = map[string]int{
	"凡": 100, "灵": 300, "玄": 800, "地": 2000, "天": 5000, "圣": 12000,
}

// 装备基础属性（按品质）：HP / ATK / DEF / SPD / MAG
var equipmentBaseStats = map[string][5]int{
	"凡": {50, 8, 6, 4, 5},
	"灵": {100, 16, 12, 8, 10},
	"玄": {200, 32, 24, 16, 20},
	"地": {400, 64, 48, 32, 40},
	"天": {800, 128, 96, 64, 80},
	"圣": {1600, 256, 192, 128, 160},
}

const equipmentMeltRefundRatio = 40 // 熔炼返还 = 锻造成本 40%

// equipmentSlotSuffix 槽位显示后缀
func equipmentSlotSuffix(slot string) string {
	if slot == "兵甲" {
		return "兵甲"
	}
	return "魂符"
}

// rollEquipment 按槽位+品质生成一件随机装备（属性随机，数值 ±15% 波动）
func rollEquipment(slot, quality string) ServantEquipment {
	base := equipmentBaseStats[quality]
	attrPool := SpiritAttributes[:5]
	if QualityIndex(quality) >= QualityIndex("地") {
		attrPool = SpiritAttributes // 阴阳仅地阶以上
	}
	attr := attrPool[rand.Intn(len(attrPool))]

	v := func(base int) int {
		if base <= 0 {
			return 0
		}
		return int(float64(base) * (0.85 + rand.Float64()*0.3))
	}

	eq := ServantEquipment{
		SlotType:  slot,
		Quality:   quality,
		Attribute: attr,
	}
	if slot == "兵甲" {
		eq.HP = v(base[0] * 40 / 100)
		eq.ATK = v(base[1])
		eq.DEF = v(base[2])
		eq.SPD = v(base[3])
	} else { // 魂魄
		eq.HP = v(base[0] * 40 / 100)
		eq.DEF = v(base[2] * 30 / 100)
		eq.SPD = v(base[3] * 40 / 100)
		eq.MAG = v(base[4])
	}
	eq.Name = fmt.Sprintf("%s品%s%s", quality, attr, equipmentSlotSuffix(slot))
	return eq
}

// ForgeEquipment 锻造装备（slotIdx/qualityIdx 索引 SpiritQualityNames）
func ForgeEquipment(userID int64, slotIdx, qualityIdx int) (*ServantEquipment, error) {
	if slotIdx < 0 || slotIdx >= len(equipmentSlots) {
		return nil, fmt.Errorf("无效槽位")
	}
	if qualityIdx < 0 || qualityIdx >= len(SpiritQualityNames) {
		return nil, fmt.Errorf("无效品阶")
	}
	slot := equipmentSlots[slotIdx]
	targetQuality := SpiritQualityNames[qualityIdx]
	cost := equipmentForgeCost[targetQuality]

	var created *ServantEquipment
	err := db.Transaction(func(tx *gorm.DB) error {
		if err := SpendLingjing(tx, userID, cost, "consume_forge",
			fmt.Sprintf("装备锻造：%s·%s品（目标）", slot, targetQuality)); err != nil {
			return err
		}
		// 品质滚动：目标 50% / -1 档 30% / -2 档 20%（下限凡品）
		r := rand.Intn(100)
		actualIdx := qualityIdx
		switch {
		case r < 50:
		case r < 80:
			actualIdx = qualityIdx - 1
		default:
			actualIdx = qualityIdx - 2
		}
		if actualIdx < 0 {
			actualIdx = 0
		}
		actualQuality := SpiritQualityNames[actualIdx]
		eq := rollEquipment(slot, actualQuality)
		eq.UserID = userID // bound_to_uid
		eq.ServantID = 0   // 仓库态
		if err := tx.Create(&eq).Error; err != nil {
			return fmt.Errorf("装备落库失败: %s", formatTelegramSendError(err))
		}
		created = &eq
		return nil
	})
	if err != nil {
		log.Printf("[灵侍] 锻造失败 user=%d slot=%s quality=%s err=%s",
			userID, slot, targetQuality, formatTelegramSendError(err))
		return nil, err
	}
	return created, nil
}

// MeltEquipment 熔炼装备返灵晶（仅仓库态、未锁定）
func MeltEquipment(userID int64, equipmentID uint) (int, error) {
	var refund int
	err := db.Transaction(func(tx *gorm.DB) error {
		var eq ServantEquipment
		if err := tx.Where("id = ? AND user_id = ?", equipmentID, userID).First(&eq).Error; err != nil {
			return fmt.Errorf("装备不存在或不属于你")
		}
		if eq.ServantID != 0 {
			return fmt.Errorf("请先卸下再熔炼")
		}
		if eq.IsLocked {
			return fmt.Errorf("装备已刻名锁定，不可熔炼")
		}
		refund = equipmentForgeCost[eq.Quality] * equipmentMeltRefundRatio / 100
		if refund < 1 {
			refund = 1
		}
		if err := tx.Delete(&ServantEquipment{}, eq.ID).Error; err != nil {
			return err
		}
		return EarnLingjing(tx, userID, refund, "equip_melt", fmt.Sprintf("装备熔炼：%s", eq.Name))
	})
	if err != nil {
		return 0, err
	}
	return refund, nil
}

// EquipEquipment 穿戴装备到灵侍对应槽位（已占用则换槽，旧装备回仓库）
func EquipEquipment(userID int64, equipmentID, servantID uint) error {
	return db.Transaction(func(tx *gorm.DB) error {
		var eq ServantEquipment
		if err := tx.Where("id = ? AND user_id = ?", equipmentID, userID).First(&eq).Error; err != nil {
			return fmt.Errorf("装备不存在或不属于你")
		}
		var s UserSpiritServant
		if err := tx.Where("id = ? AND user_id = ?", servantID, userID).First(&s).Error; err != nil {
			return fmt.Errorf("灵侍不存在或不属于你")
		}
		// 若装备穿在其他灵侍身上，先卸下
		if eq.ServantID != 0 {
			if err := tx.Model(&ServantEquipment{}).Where("id = ?", eq.ID).
				Update("servant_id", 0).Error; err != nil {
				return err
			}
		}
		// 槽位已占用：旧装备回仓库
		var old ServantEquipment
		if err := tx.Where("servant_id = ? AND slot_type = ?", servantID, eq.SlotType).
			First(&old).Error; err == nil {
			if err := tx.Model(&ServantEquipment{}).Where("id = ?", old.ID).
				Update("servant_id", 0).Error; err != nil {
				return err
			}
		}
		eq.ServantID = servantID
		return tx.Save(&eq).Error
	})
}

// UnequipEquipment 卸下装备回仓库
func UnequipEquipment(userID int64, equipmentID uint) error {
	res := db.Model(&ServantEquipment{}).
		Where("id = ? AND user_id = ? AND servant_id > 0", equipmentID, userID).
		Update("servant_id", 0)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("装备未穿戴或不存在")
	}
	return nil
}

// ==========================================
// 战斗加成并入
// ==========================================

// getServantEquipBonus 灵侍已穿戴装备的属性加总成
const (
	equipEnhanceMax      = 10   // 精炼上限
	equipEnhancePerLevel = 0.02 // 每级精炼装备属性加成（+2%）
)

// EquipEnhanceCost 精炼到下一级（当前 Enhance → Enhance+1）的灵晶成本
// 100 × (当前等级+1) × (品阶序号+1)：凡品 +1 = 100，圣品 +1 = 600，圣品 +10 = 6000
func EquipEnhanceCost(e *ServantEquipment) int {
	return 100 * (e.Enhance + 1) * (QualityIndex(e.Quality) + 1)
}

// EnhanceEquipment 精炼一次（事务：扣灵晶 → 条件更新 enhance+1，防并发超上限）
// 返回实际消耗的灵晶
func EnhanceEquipment(tx *gorm.DB, userID int64, equipmentID uint) (int, error) {
	var e ServantEquipment
	if err := tx.Where("id = ? AND user_id = ?", equipmentID, userID).First(&e).Error; err != nil {
		return 0, fmt.Errorf("装备不存在或不属于你")
	}
	if e.Enhance >= equipEnhanceMax {
		return 0, fmt.Errorf("装备已达满精炼：+%d", equipEnhanceMax)
	}
	cost := EquipEnhanceCost(&e)
	if err := SpendLingjing(tx, userID, cost, "equip_enhance",
		fmt.Sprintf("精炼%s +%d", e.Name, e.Enhance+1)); err != nil {
		return 0, err
	}
	res := tx.Model(&ServantEquipment{}).
		Where("id = ? AND user_id = ? AND enhance < ?", equipmentID, userID, equipEnhanceMax).
		Update("enhance", gorm.Expr("enhance + 1"))
	if res.Error != nil {
		return 0, res.Error
	}
	if res.RowsAffected == 0 {
		return 0, fmt.Errorf("装备已达满精炼：+%d", equipEnhanceMax)
	}
	return cost, nil
}

func getServantEquipBonus(userID int64, servantID uint) (hp, atk, def, spd, mag int) {
	var eqs []ServantEquipment
	db.Where("user_id = ? AND servant_id = ?", userID, servantID).Find(&eqs)
	for i := range eqs {
		e := &eqs[i]
		m := 1 + equipEnhancePerLevel*float64(e.Enhance) // 精炼加成
		hp += int(float64(e.HP) * m)
		atk += int(float64(e.ATK) * m)
		def += int(float64(e.DEF) * m)
		spd += int(float64(e.SPD) * m)
		mag += int(float64(e.MAG) * m)
	}
	return
}

// enhanceServantStats 返回并入装备加成后的灵侍副本（战斗/战力计算用，不改原对象）
func enhanceServantStats(userID int64, team []UserSpiritServant) []UserSpiritServant {
	out := make([]UserSpiritServant, 0, len(team))
	for i := range team {
		s := team[i]
		// 先应用等级成长（数据库存一级基础值），再叠加装备加成
		s.HP = ScaledHP(&s)
		s.ATK = ScaledATK(&s)
		s.DEF = ScaledDEF(&s)
		s.SPD = ScaledSPD(&s)
		s.MAG = ScaledMAG(&s)
		hp, atk, def, spd, mag := getServantEquipBonus(userID, s.ID)
		s.HP += hp
		s.ATK += atk
		s.DEF += def
		s.SPD += spd
		s.MAG += mag
		out = append(out, s)
	}
	return out
}

// ListEquipment 装备列表（装备中 + 仓库）
func ListEquipment(userID int64) (equipped, bag []ServantEquipment) {
	var all []ServantEquipment
	db.Where("user_id = ?", userID).Order("id desc").Find(&all)
	for i := range all {
		if all[i].ServantID != 0 {
			equipped = append(equipped, all[i])
		} else {
			bag = append(bag, all[i])
		}
	}
	return
}

// getServantNameByID 灵侍名（不存在返回空）
func getServantNameByID(userID int64, servantID uint) string {
	var s UserSpiritServant
	if err := db.Select("name").Where("id = ? AND user_id = ?", servantID, userID).First(&s).Error; err != nil {
		return ""
	}
	return s.Name
}

// equipmentStatLine 装备属性展示行
func equipmentStatLine(eq *ServantEquipment) string {
	parts := []string{}
	if eq.HP > 0 {
		parts = append(parts, fmt.Sprintf("HP+%d", eq.HP))
	}
	if eq.ATK > 0 {
		parts = append(parts, fmt.Sprintf("攻+%d", eq.ATK))
	}
	if eq.DEF > 0 {
		parts = append(parts, fmt.Sprintf("防+%d", eq.DEF))
	}
	if eq.SPD > 0 {
		parts = append(parts, fmt.Sprintf("速+%d", eq.SPD))
	}
	if eq.MAG > 0 {
		parts = append(parts, fmt.Sprintf("法+%d", eq.MAG))
	}
	enh := ""
	if eq.Enhance > 0 {
		enh = fmt.Sprintf(" 精炼+%d", eq.Enhance)
	}
	return fmt.Sprintf("%s%s（%s）", eq.Name, enh, joinStatParts(parts))
}

func joinStatParts(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	if out == "" {
		return "无加成"
	}
	return out
}
