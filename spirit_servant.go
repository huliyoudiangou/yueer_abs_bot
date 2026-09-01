package main

import (
	"fmt"
	"log"
	"math"
	"math/rand"
	"sort"
	"strings"

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
// 必须走传入的 tx 落库（调用方在捕捉/孵化事务内）：用全局 db 会让灵侍脱离调用方事务，
// 外层回滚时灵侍残留（灵晶已退但灵侍白得），且小连接池下全局查询会死等。
func createServantRecord(tx *gorm.DB, userID int64, chosenQuality string, zone SpiritZone) (*UserSpiritServant, error) {

	// 同名重抽（默认最多 30 次）；重名检查走 tx（同上，不查全局连接池）
	name := "灵侍"
	for retry := 0; retry < 30; retry++ {
		pool := ServantNamePool[chosenQuality]
		if len(pool) == 0 {
			break
		}
		name = pool[rand.Intn(len(pool))].Name
		var dup int64
		if err := tx.Model(&UserSpiritServant{}).
			Where("user_id = ? AND name = ?", userID, name).Count(&dup).Error; err != nil {
			return nil, err
		}
		if dup == 0 {
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
	if err := tx.Create(ser).Error; err != nil {
		return nil, err
	}
	log.Printf("[灵侍] 生成 user=%d zone=%s quality=%s name=%s", userID, formatPlainValue(zone.Key), formatPlainValue(chosenQuality), formatPlainValue(name))
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

// levelGrowthPerLevel 等级成长：每级 +2% 一级基础值（线性累计，不叠加）
// 数据库存储的 HP/ATK/DEF/SPD/MAG 恒为一级基础值，战斗/战力走缩放函数
const levelGrowthPerLevel = 0.02

// starGrowthPerStar 星级成长：每星 +5% 一级基础值（线性累计，不叠加）
// 与等级倍率在缩放函数中相乘（唯一缩放点），保证升星对战力/战斗的实际感知；
// 数值为设计推断项，待运营调参。
const starGrowthPerStar = 0.05

// LevelGrowthMult 指定等级的属性倍率（Lv.1 = 1.0，Lv.11 = 1.20，Lv.91 = 2.80）
func LevelGrowthMult(level int) float64 {
	if level < 1 {
		level = 1
	}
	return 1 + levelGrowthPerLevel*float64(level-1)
}

// StarGrowthMult 指定星级的属性倍率（1星 = 1.0，3星 = 1.10，9星 = 1.40）
func StarGrowthMult(star int) float64 {
	if star < 1 {
		star = 1
	}
	return 1 + starGrowthPerStar*float64(star-1)
}

// 等级+星级缩放属性（战力/战斗引擎共享口径）：DB 存一级基础值，缩放 = 基础 × 等级倍率 × 星级倍率
func ScaledHP(s *UserSpiritServant) int {
	return int(float64(s.HP) * LevelGrowthMult(s.Level) * StarGrowthMult(s.Star))
}
func ScaledATK(s *UserSpiritServant) int {
	return int(float64(s.ATK) * LevelGrowthMult(s.Level) * StarGrowthMult(s.Star))
}
func ScaledDEF(s *UserSpiritServant) int {
	return int(float64(s.DEF) * LevelGrowthMult(s.Level) * StarGrowthMult(s.Star))
}
func ScaledSPD(s *UserSpiritServant) int {
	return int(float64(s.SPD) * LevelGrowthMult(s.Level) * StarGrowthMult(s.Star))
}
func ScaledMAG(s *UserSpiritServant) int {
	return int(float64(s.MAG) * LevelGrowthMult(s.Level) * StarGrowthMult(s.Star))
}

func GetBattlePower(s *UserSpiritServant) int {
	total := ScaledHP(s) + ScaledATK(s) + ScaledDEF(s) + ScaledSPD(s) + ScaledMAG(s)
	return int(float64(total) * QualityGrowth[s.Quality])
}

func GetLevelUpRequirement(s *UserSpiritServant) int {
	return 10 + s.Level*15
}

// FeedCostByQuality 每级喂养成本（灵晶）
var FeedCostByQuality = map[string]int{
	"凡": 30, "灵": 50, "玄": 80, "地": 120, "天": 200, "圣": 300,
}

// FeedSpirit 喂养升级：消耗灵晶（成本×次数，超出等级上限的部分不计费）→ 等级提升
// 返回实际消耗的灵晶
func FeedSpirit(tx *gorm.DB, userID int64, servantID uint, amount int) (int, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("喂养次数必须大于0")
	}
	var s UserSpiritServant
	if err := tx.Where("id = ? AND user_id = ?", servantID, userID).First(&s).Error; err != nil {
		return 0, fmt.Errorf("灵侍不存在")
	}
	maxLevel := MaxLevelByStar(s.Star)
	if s.Level >= maxLevel {
		return 0, fmt.Errorf("已达当前星级等级上限：%d级（升星可提高上限）", maxLevel)
	}
	if available := maxLevel - s.Level; amount > available {
		amount = available
	}
	costPer := FeedCostByQuality[s.Quality]
	if costPer <= 0 {
		costPer = FeedCostByQuality["凡"]
	}
	cost := costPer * amount
	if err := SpendLingjing(tx, userID, cost, "spirit_feed",
		fmt.Sprintf("喂养%s×%d", s.Name, amount)); err != nil {
		return 0, err
	}
	s.Level += amount
	if err := tx.Save(&s).Error; err != nil {
		return 0, err
	}
	return cost, nil
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
// 祭品不能是自己/已锁定/出战中/穿戴装备；成功后等级重置为 1，祭品被消耗（软删除，其功法修习一并清理）
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
		// 已穿戴装备的灵侍不能作祭品（防装备随祭品一起消失，与吞噬口径一致）
		// fail-closed：装备状态查询失败时中止升星，不得按无装备继续销毁
		equipped, eqErr := equippedServantIDSet(tx, userID)
		if eqErr != nil {
			return fmt.Errorf("装备状态查询失败，已中止升星")
		}

		for _, sid := range sacrificeIDs {
			if sid == targetServantID {
				return fmt.Errorf("不能用自己作祭品")
			}
			var sacrifice UserSpiritServant
			if err := tx.Where("id = ? AND user_id = ?", sid, userID).First(&sacrifice).Error; err != nil {
				return fmt.Errorf("祭品无效：%d", sid)
			}
			if reason := servantConsumeBlockReason(&sacrifice, equipped); reason != "" {
				return fmt.Errorf("祭品不可消耗：%s", reason)
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

		// 消耗祭品（批量软删除；前述校验已确认全部有效）
		del := tx.Where("user_id = ? AND id IN ?", userID, sacrificeIDs).Delete(&UserSpiritServant{})
		if del.Error != nil || int(del.RowsAffected) != len(sacrificeIDs) {
			return fmt.Errorf("删除祭品失败")
		}
		// 清理祭品的功法修习（软删除保留审计；装备已前置排除，不会随祭品消失）
		if err := tx.Where("user_id = ? AND servant_id IN ?", userID, sacrificeIDs).
			Delete(&ServantManualStudy{}).Error; err != nil {
			return fmt.Errorf("祭品功法清理失败")
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
		userID, formatPlainValue(target.Name), nextStar, len(sacrificeIDs), formatPlainValue(useItem))
	return nil
}

// ListShardSacrifices 列出真身碎片可用祭品（同品质+同星级，不限属性/名称；排除自己/锁定/出战中，战力高→低）
func ListShardSacrifices(userID int64, target *UserSpiritServant) []UserSpiritServant {
	var cands []UserSpiritServant
	if err := db.Where("user_id = ?", userID).
		Order("id asc").Find(&cands).Error; err != nil {
		return nil
	}
	equipped, err := equippedServantIDSet(db, userID)
	if err != nil {
		log.Printf("[灵侍] 碎片祭品装备状态查询失败 user=%d err=%s", userID, formatTelegramSendError(err))
		return nil
	}
	var out []UserSpiritServant
	for _, c := range cands {
		if c.ID == target.ID || c.IsLocked || c.IsDeployed || equipped[c.ID] {
			continue
		}
		if c.Quality == target.Quality && c.Star == target.Star {
			out = append(out, c)
		}
	}
	SortServantsByPower(userID, out)
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

// StarUpRequirementText 下一次升星的需求说明（明确当前星级 → 目标星级，以及祭品星级要求）
func StarUpRequirementText(t *UserSpiritServant) string {
	nextStar := t.Star + 1
	switch {
	case nextStar <= 3:
		return fmt.Sprintf("本次升星：⭐%d → ⭐%d（需 1 只祭品）\n祭品要求：同品阶（%s）+ 同属性（%s），祭品星级不限",
			t.Star, nextStar, t.Quality, t.Attribute)
	case nextStar <= 6:
		return fmt.Sprintf("本次升星：⭐%d → ⭐%d（需 1 只祭品）\n祭品要求：同品阶（%s）+ 同属性（%s）+ 祭品星级必须为 %d 星（与当前星级相同）",
			t.Star, nextStar, t.Quality, t.Attribute, t.Star)
	default:
		return fmt.Sprintf("本次升星：⭐%d → ⭐%d（需 1 只祭品）\n祭品要求：同名（%s）+ 祭品星级必须为 %d 星（与当前星级相同，属性不限）",
			t.Star, nextStar, t.Name, t.Star)
	}
}

// StarUpBreakEvenLevel 升星后（等级重置为 1）重新喂养至该等级，
// 战力（不含装备/功法口径）即可超越升星前；升星必获星级倍率，故回本等级恒 ≤ 升星前等级
func StarUpBreakEvenLevel(oldLevel, oldStar, newStar int) int {
	oldMult := LevelGrowthMult(oldLevel) * StarGrowthMult(oldStar)
	newMult := StarGrowthMult(newStar)
	if newMult <= 0 {
		return 1
	}
	need := oldMult / newMult
	lv := int(math.Ceil(1 + (need-1)/levelGrowthPerLevel))
	if lv < 1 {
		lv = 1
	}
	return lv
}

// ListStarUpSacrifices 列出下一星可用的祭品（排除自己/锁定/出战中，按段规则过滤，战力高→低）
func ListStarUpSacrifices(userID int64, target *UserSpiritServant) []UserSpiritServant {
	var cands []UserSpiritServant
	if err := db.Where("user_id = ?", userID).
		Order("id asc").Find(&cands).Error; err != nil {
		return nil
	}
	nextStar := target.Star + 1
	equipped, err := equippedServantIDSet(db, userID)
	if err != nil {
		log.Printf("[灵侍] 升星候选装备状态查询失败 user=%d err=%s", userID, formatTelegramSendError(err))
		return nil
	}
	var out []UserSpiritServant
	for _, c := range cands {
		if c.ID == target.ID || c.IsLocked || c.IsDeployed || equipped[c.ID] {
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
	SortServantsByPower(userID, out)
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

// pickDeployedTeamTx 取出战队伍：已上阵、战力（含装备）高→低，最多 maxTeamSize 名。
// PVE 推图 / 镜场上架 / PVP 攻击统一用此口径选队（与出战队列面板展示顺序一致）。
func pickDeployedTeamTx(tx *gorm.DB, userID int64) ([]UserSpiritServant, error) {
	var team []UserSpiritServant
	if err := tx.Where("user_id = ? AND is_deployed = ?", userID, true).
		Find(&team).Error; err != nil {
		return nil, err
	}
	sortServantsWithBonus(equipBonusMap(tx, userID), servantManualBonusPctMap(tx, userID), team) // 装备/功法加成走 tx，不占独立连接
	if len(team) > maxTeamSize {
		team = team[:maxTeamSize]
	}
	return team, nil
}

// toggleTeamDeploy 上阵/下阵（含上限检查），返回用户提示文案。
// 调用方需已持有用户锁（handleSpiritCallback 约定），计数检查后落库不并发穿透。
func toggleTeamDeploy(q *gorm.DB, userID int64, servantID uint) (string, error) {
	var target UserSpiritServant
	if err := q.Where("id = ? AND user_id = ?", servantID, userID).First(&target).Error; err != nil {
		return "", fmt.Errorf("灵侍不存在或不属于你")
	}
	if target.IsDeployed {
		if err := q.Transaction(func(tx *gorm.DB) error {
			return TeamDeploy(tx, userID, servantID, false)
		}); err != nil {
			return "", err
		}
		return fmt.Sprintf("🔙 %s 已下阵", target.Name), nil
	}
	var deployedCount int64
	q.Model(&UserSpiritServant{}).
		Where("user_id = ? AND is_deployed = ?", userID, true).Count(&deployedCount)
	if deployedCount >= int64(maxTeamSize) {
		return "", fmt.Errorf("出战队列已满（%d/%d），请先下阵再上阵", maxTeamSize, maxTeamSize)
	}
	if err := q.Transaction(func(tx *gorm.DB) error {
		return TeamDeploy(tx, userID, servantID, true)
	}); err != nil {
		return "", err
	}
	return fmt.Sprintf("⚔️ %s 已上阵", target.Name), nil
}

// servantConsumeBlockReason 灵侍能否被消耗（升星祭品 / 吞噬 等销毁性操作共用口径）。
// 返回空串表示可消耗；否则返回用户可读的拒绝原因。
// equipped 必须来自 equippedServantIDSet（fail-closed，查询失败调用方须中止）。
// 未来若开放灵侍交易/转移，必须在此单点追加「上架中不可消耗」互斥，避免各消耗路径遗漏。
func servantConsumeBlockReason(s *UserSpiritServant, equipped map[uint]bool) string {
	switch {
	case s == nil:
		return "灵侍不存在"
	case s.IsLocked:
		return s.Name + " 已锁定"
	case s.IsDeployed:
		return s.Name + " 出战中"
	case equipped[s.ID]:
		return s.Name + " 穿戴着装备（需先卸下）"
	}
	return ""
}

// ==========================================
// 吞噬（2026-09 新增）：宿主吞噬其他灵侍换取属性点
// 规则：
//   - 任意品阶/属性的灵侍均可被吞噬；出战中、已锁定、宿主自身、穿戴装备的不可被吞噬
//   - 每只被吞灵侍按品阶+星级折算属性点（DevourPointsFor，spirit_config.go）
//   - 属性点按宿主五维一级基础值比例分配（最大余数法），并入基础属性（随等级/星级成长放大）
//   - 全程事务 + 写 ServantDevourLog 审计；调用方需持有用户锁（回调链约定）
// ==========================================

// distributeDevourPoints 将属性点按宿主五维一级基础值比例分配（最大余数法，保持属性结构不失真）
func distributeDevourPoints(host *UserSpiritServant, points int) (hp, atk, def, spd, mag int) {
	if host == nil || points <= 0 {
		return 0, 0, 0, 0, 0
	}
	base := [5]int{host.HP, host.ATK, host.DEF, host.SPD, host.MAG}
	total := 0
	for _, v := range base {
		if v > 0 {
			total += v
		}
	}
	floors := [5]int{}
	remain := points
	if total <= 0 {
		// 异常兜底：宿主无基础值时五维轮流平分（必须把 points 全部分完，不得静默丢失）
		for i := 0; remain > 0; i = (i + 1) % 5 {
			floors[i]++
			remain--
		}
	} else {
		exact := [5]float64{}
		for i, v := range base {
			if v <= 0 {
				continue
			}
			exact[i] = float64(points) * float64(v) / float64(total)
			floors[i] = int(exact[i])
			remain -= floors[i]
		}
		order := []int{0, 1, 2, 3, 4}
		sort.SliceStable(order, func(i, j int) bool {
			return exact[order[i]]-float64(floors[order[i]]) > exact[order[j]]-float64(floors[order[j]])
		})
		for i := 0; remain > 0 && i < 5; i++ { // 小数部分大者优先补 1（余数最多 4）
			floors[order[i]]++
			remain--
		}
		for i := 0; remain > 0; i = (i + 1) % 5 { // 防御式兜底（理论不可达）
			floors[i]++
			remain--
		}
	}
	return floors[0], floors[1], floors[2], floors[3], floors[4]
}

// DevourOutcome 吞噬结果（ack/面板展示用）
type DevourOutcome struct {
	Count                  int
	Points                 int
	HP, ATK, DEF, SPD, MAG int
	PowerBefore            int
	PowerAfter             int
	QualityBreakdown       string // 品阶分布，如 "凡×10 灵×8 玄×5"
}

// listDevourCandidatesQ 可被宿主吞噬的灵侍列表。
// qualityIdx：0-4 = 品阶及以下（凡/灵/玄/地/天），-1 = 不限品阶。
// 排除：宿主自身、已锁定、出战中、穿戴装备的灵侍；按战力（含装备/功法）高→低。
// q 为 DB 句柄：面板/预览传 db，事务执行传 tx（小连接池下不占独立连接）。
func listDevourCandidatesQ(q *gorm.DB, userID int64, host *UserSpiritServant, qualityIdx int) []UserSpiritServant {
	var cands []UserSpiritServant
	if err := q.Where("user_id = ?", userID).Order("id asc").Find(&cands).Error; err != nil {
		return nil
	}
	equipped, err := equippedServantIDSet(q, userID)
	if err != nil {
		log.Printf("[灵侍] 吞噬候选装备状态查询失败 user=%d err=%s", userID, formatTelegramSendError(err))
		return nil
	}
	var out []UserSpiritServant
	for _, c := range cands {
		if host != nil && c.ID == host.ID {
			continue
		}
		if c.IsLocked || c.IsDeployed || equipped[c.ID] {
			continue
		}
		if qualityIdx >= 0 && QualityIndex(c.Quality) > qualityIdx {
			continue
		}
		out = append(out, c)
	}
	sortServantsWithBonus(equipBonusMap(q, userID), servantManualBonusPctMap(q, userID), out)
	return out
}

// ListDevourCandidates 可吞噬候选（面板用，走全局 db）
func ListDevourCandidates(userID int64, host *UserSpiritServant, qualityIdx int) []UserSpiritServant {
	return listDevourCandidatesQ(db, userID, host, qualityIdx)
}

// DevourServants 吞噬执行：devouredIDs 在函数内全量复核（归属/锁定/出战/装备/去重）。
// 必须在 db.Transaction 内调用；成功后宿主基础属性累加、被吞灵侍软删除、写审计日志。
func DevourServants(tx *gorm.DB, userID int64, hostID uint, devouredIDs []uint) (*DevourOutcome, error) {
	if len(devouredIDs) == 0 {
		return nil, fmt.Errorf("请选择要被吞噬的灵侍")
	}
	var host UserSpiritServant
	if err := tx.Where("id = ? AND user_id = ?", hostID, userID).First(&host).Error; err != nil {
		return nil, fmt.Errorf("宿主灵侍不存在或不属于你")
	}
	// 去重 + 排除自身
	seen := map[uint]bool{}
	ids := make([]uint, 0, len(devouredIDs))
	for _, id := range devouredIDs {
		if id == hostID || id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) == 0 {
		return nil, fmt.Errorf("请选择要被吞噬的灵侍")
	}

	// fail-closed：装备状态查询失败时中止吞噬，不得按无装备继续销毁
	equipped, eqErr := equippedServantIDSet(tx, userID)
	if eqErr != nil {
		return nil, fmt.Errorf("装备状态查询失败，已中止吞噬")
	}
	// 批量取回被吞灵侍（IN 一次查询；数量不符说明存在无效/他人灵侍，直接失败）
	var victims []UserSpiritServant
	if err := tx.Where("user_id = ? AND id IN ?", userID, ids).Find(&victims).Error; err != nil {
		return nil, err
	}
	if len(victims) != len(ids) {
		return nil, fmt.Errorf("存在无效的被吞噬灵侍，请刷新后重试")
	}
	qualityCount := map[string]int{}
	for i := range victims {
		v := &victims[i]
		if reason := servantConsumeBlockReason(v, equipped); reason != "" {
			return nil, fmt.Errorf("不可吞噬：%s", reason)
		}
		qualityCount[v.Quality]++
	}

	bonusMap := equipBonusMap(tx, userID)
	manualMap := servantManualBonusPctMap(tx, userID)
	out := &DevourOutcome{Count: len(victims)}
	for _, v := range victims {
		out.Points += DevourPointsFor(v.Quality, v.Star)
	}
	out.HP, out.ATK, out.DEF, out.SPD, out.MAG = distributeDevourPoints(&host, out.Points)
	out.PowerBefore = EnhancedBattlePower(bonusMap, manualMap, &host)

	// 累加宿主一级基础属性（gorm.Expr 原子累加，防并发丢更新）
	if err := tx.Model(&UserSpiritServant{}).Where("id = ?", host.ID).Updates(map[string]interface{}{
		"hp":  gorm.Expr("hp + ?", out.HP),
		"atk": gorm.Expr("atk + ?", out.ATK),
		"def": gorm.Expr("def + ?", out.DEF),
		"spd": gorm.Expr("spd + ?", out.SPD),
		"mag": gorm.Expr("mag + ?", out.MAG),
	}).Error; err != nil {
		return nil, err
	}

	// 消耗被吞灵侍（批量软删除，与升星祭品口径一致）+ 清理其功法修习（软删除保留审计，装备已前置排除）
	del := tx.Where("user_id = ? AND id IN ?", userID, ids).Delete(&UserSpiritServant{})
	if del.Error != nil || int(del.RowsAffected) != len(ids) {
		return nil, fmt.Errorf("吞噬消耗失败")
	}
	if err := tx.Where("user_id = ? AND servant_id IN ?", userID, ids).
		Delete(&ServantManualStudy{}).Error; err != nil {
		return nil, fmt.Errorf("被吞噬灵侍功法清理失败")
	}

	// 复核宿主并计算吞噬后战力
	var hostAfter UserSpiritServant
	if err := tx.Where("id = ?", host.ID).First(&hostAfter).Error; err != nil {
		return nil, err
	}
	out.PowerAfter = EnhancedBattlePower(bonusMap, manualMap, &hostAfter)

	breakdown := make([]string, 0, len(qualityCount))
	for _, q := range SpiritQualityNames {
		if n := qualityCount[q]; n > 0 {
			breakdown = append(breakdown, fmt.Sprintf("%s×%d", q, n))
		}
	}
	out.QualityBreakdown = strings.Join(breakdown, " ")

	logRecord := ServantDevourLog{
		UserID:        userID,
		HostID:        host.ID,
		HostName:      host.Name,
		DevouredCount: out.Count,
		Points:        out.Points,
		HPGain:        out.HP,
		ATKGain:       out.ATK,
		DEFGain:       out.DEF,
		SPDGain:       out.SPD,
		MAGGain:       out.MAG,
		Remark:        fmt.Sprintf("吞噬%d只[%s]", out.Count, out.QualityBreakdown),
	}
	if err := tx.Create(&logRecord).Error; err != nil {
		return nil, fmt.Errorf("吞噬日志写入失败")
	}
	log.Printf("[灵侍] 吞噬成功 user=%d host=%s(%d) count=%d points=%d power %d->%d",
		userID, formatPlainValue(host.Name), host.ID, out.Count, out.Points, out.PowerBefore, out.PowerAfter)
	return out, nil
}

// DevourAckText 吞噬成功的 ack 文案（单只/一键共用）
func DevourAckText(out *DevourOutcome) string {
	return fmt.Sprintf("🩸 吞噬完成：%d 只（%s），+%d 属性点：气血+%d 攻+%d 防+%d 速+%d 识+%d，战力 +%d",
		out.Count, out.QualityBreakdown, out.Points, out.HP, out.ATK, out.DEF, out.SPD, out.MAG,
		out.PowerAfter-out.PowerBefore)
}
