package main

// ==========================================
// 灵墟推图 · PVE 战斗（Phase 1）
//
// 6 章（对应六大灵墟区域，境界解锁与捕捉一致）×（10 普通关 + 1 Boss）
// 神行符体力 10/日，每次挑战消耗 1；三星解锁扫荡（奖励 40%，每关每日 3 次）
// 战斗引擎为回合制，PVP 镜场后续复用 runBattle。
//
// 资产规则：
// - 体力消耗与奖励发放同事务（db.Transaction）
// - 奖励走 EarnLingjing（有流水）
// - 进度 upsert 用唯一索引 (user_id, chapter_id, stage_id) 兜底
// ==========================================

import (
	"errors"
	"fmt"
	"log"
	"math/rand"
	"strings"
	"time"

	"gorm.io/gorm"
)

const (
	divineTravelDailyCap = 10 // 神行符：每日 10 点
	divineTravelCost     = 1  // 每次挑战消耗 1 点
	sweepDailyLimit      = 3  // 每关每日扫荡上限
	sweepRewardRatio     = 40 // 扫荡奖励 = 关卡奖励的 40%
	starUpRewardRatio    = 20 // 升星奖励 = 关卡奖励的 20%
	maxBattleRounds      = 25 // 战斗最大回合
	maxTeamSize          = 5  // 出战上限
	stagesPerChapter     = 10 // 普通关数
	bossStageID          = 11 // 第 11 关 = 章节 Boss

	// 战力权重：只计入真正影响胜负的量。气血不再按 1:1 灌进战力。
	// 数值为设计推断项，和 calcDamage / 技能节奏对齐，待运营调参。
	powerATKWeight = 8.0
	powerHPWeight  = 0.35
	powerDEFWeight = 2.2
	powerSPDWeight = 1.6
	powerMAGWeight = 3.0 // 灵识只按一半折进攻击，战力不得按全额计入

	formationWuxingBonus = 0.08 // 出战凑齐金木水火土：全队攻防血 +8%
	heavenElementBonus   = 0.15 // 镜场天时：当日属性攻防 +15%
	frontRowShare        = 0.72 // 敌方优先打前排的概率
	skillEveryNTurns     = 3    // 每名存活单位每 3 次行动放一次属性技能
)

// chapterBossNames 章节 Boss 名（各区域顶阶灵侍首领）
var chapterBossNames = []string{
	"竹影螳螂王", // ch1 青竹林海
	"雾隐魅狐",  // ch2 迷雾深谷
	"裂地犰狳王", // ch3 断岳山脉
	"罗刹夜魅",  // ch4 幽冥绝岭
	"覆海蛟王",  // ch5 归墟海眼
	"五爪金龙",  // ch6 不周山巅
	"虚空螭龙",  // ch7 问道星海
	"两仪道魇",  // ch8 两仪秘境
	"归元古佛",  // ch9 归元天阙
	"仙庭残魂",  // ch10 仙庭残迹
	"赤金天蟒",  // ch11 赤金天池
	"太一真灵",  // ch12 太一仙山
	"大罗金魔",  // ch13 大罗天境
	"混元祖灵",  // ch14 混元祖庭
}

// wuxingCounter 五行相克（攻方 → 所克）。金克木、木克土、土克水、水克火、火克金。
var wuxingCounter = map[string]string{
	"木": "土", "土": "水", "水": "火", "火": "金", "金": "木",
}

// wuxingGenerate 五行相生（攻方 → 所生）。金生水、水生木、木生火、火生土、土生金。
// 相生不改伤害，只用于编队说明：同队相邻相生没有数值加成，避免和克制叠成第二套隐藏战力。
var wuxingGenerate = map[string]string{
	"金": "水", "水": "木", "木": "火", "火": "土", "土": "金",
}

// elementRelation 攻方打守方的关系，给面板用。空属性返回空串。
func elementRelation(attEl, defEl string) string {
	if attEl == "" || defEl == "" {
		return ""
	}
	if attEl == defEl {
		return "同属"
	}
	if (attEl == "阴" && defEl == "阳") || (attEl == "阳" && defEl == "阴") {
		return "阴阳相冲"
	}
	if wuxingCounter[attEl] == defEl {
		return "克制"
	}
	if wuxingCounter[defEl] == attEl {
		return "被克"
	}
	if wuxingGenerate[attEl] == defEl {
		return "相生"
	}
	if wuxingGenerate[defEl] == attEl {
		return "被生"
	}
	return "无克"
}

// elementRelationHint 一行能看懂的克制说明。
func elementRelationHint(attEl, defEl string) string {
	switch elementRelation(attEl, defEl) {
	case "克制":
		return fmt.Sprintf("%s克%s，伤害更高", attEl, defEl)
	case "被克":
		return fmt.Sprintf("%s被%s克，伤害更低", attEl, defEl)
	case "阴阳相冲":
		return "阴阳相冲，双方伤害都更高"
	case "同属":
		return "同属，无克制"
	case "相生", "被生":
		return "相生不改伤害，只说明五行相邻"
	default:
		return "无克制"
	}
}

// attrMult 属性倍率：攻方 vs 守方
func attrMult(attEl, defEl string) float64 {
	if attEl == "" || defEl == "" || attEl == defEl {
		return 1.0
	}
	if (attEl == "阴" && defEl == "阳") || (attEl == "阳" && defEl == "阴") {
		return 1.30 // 阴阳相冲
	}
	if wuxingCounter[attEl] == defEl {
		return 1.25 // 五行克制
	}
	if wuxingCounter[defEl] == attEl {
		return 0.85 // 被克
	}
	return 1.0
}

// BattleFighter 战斗实体（队员/敌方通用，PVP 复用）
// Row：0 前排承伤，1 后排输出。旧镜像 JSON 没有该字段时按 0 反序列化，开战前再按速度重排。
type BattleFighter struct {
	Name      string `json:"Name"`
	Quality   string `json:"Quality"`
	Element   string `json:"Element"` // 五行 / 阴 / 阳
	MaxHP     int    `json:"MaxHP"`
	HP        int    `json:"HP"`
	ATK       int    `json:"ATK"`
	DEF       int    `json:"DEF"`
	SPD       int    `json:"SPD"`
	Row       int    `json:"Row,omitempty"`
	Shield    int    `json:"-"`
	Burn      int    `json:"-"` // 剩余灼烧回合
	Slow      int    `json:"-"` // 剩余减速回合（速度临时减半）
	AtkDown   int    `json:"-"` // 剩余偷攻回合
	AtkStolen int    `json:"-"` // 被阴属性偷走的攻击
	Actions   int    `json:"-"`
}

// BattleBrief 一场战斗的可读简报（不进资产，只给面板）
type BattleBrief struct {
	Lines []string
}

func (b *BattleBrief) add(format string, args ...interface{}) {
	if b == nil || len(b.Lines) >= 6 {
		return
	}
	b.Lines = append(b.Lines, fmt.Sprintf(format, args...))
}

func (b *BattleBrief) Text() string {
	if b == nil || len(b.Lines) == 0 {
		return ""
	}
	return strings.Join(b.Lines, "\n")
}

// SpiritBattleResult 一场战斗的结果
type SpiritBattleResult struct {
	Win         bool
	Stars       int // 败 0，胜 1-3
	TeamHPLeft  int
	TeamHPTotal int
	Rounds      int
	Brief       string
}

// chapterZone 章节 → 灵墟区域
func chapterZone(chapterID int) *SpiritZone {
	if chapterID < 1 || chapterID > len(SpiritZones) {
		return nil
	}
	return &SpiritZones[chapterID-1]
}

// stageAffix 推图词缀。同一关固定，不随每次挑战重抽，避免同一关时而能过时而不能过。
type stageAffix struct {
	Key  string
	Name string
	Hint string
}

func stageAffixOf(chapterID, stageID int) stageAffix {
	// Boss 不挂词缀，避免和章节首领数值叠成不可读的墙。
	if stageID == bossStageID {
		return stageAffix{}
	}
	switch (chapterID + stageID) % 4 {
	case 1:
		return stageAffix{Key: "iron", Name: "铁壁", Hint: "敌方防御较高，金系破防更有效"}
	case 2:
		return stageAffix{Key: "swift", Name: "速攻", Hint: "敌方先手，后排更容易被压"}
	case 3:
		el := SpiritAttributes[(chapterID+stageID)%5]
		return stageAffix{Key: "resist", Name: el + "抗", Hint: "该属性伤害降低，换克制属性更稳"}
	default:
		return stageAffix{}
	}
}

// applyStageAffix 把词缀写进敌人。resist 不改面板数值，结算时再减伤。
func applyStageAffix(enemy *BattleFighter, affix stageAffix) {
	if enemy == nil || affix.Key == "" {
		return
	}
	switch affix.Key {
	case "iron":
		enemy.DEF += enemy.DEF/2 + 8
	case "swift":
		enemy.SPD += enemy.SPD/2 + 12
		enemy.ATK += enemy.ATK / 5
	}
}

// buildStageEnemy 生成关卡敌人（数值按章节品阶线性成长，并挂固定词缀）
func buildStageEnemy(chapterID, stageID int) *BattleFighter {
	zone := chapterZone(chapterID)
	if zone == nil {
		return nil
	}
	tier := zone.Tier
	base := 60 + 70*tier
	power := int(float64(base) * (1 + 0.08*float64(stageID-1)))
	isBoss := stageID == bossStageID
	if isBoss {
		power = base * 3
	}
	name := fmt.Sprintf("%s·野灵 %d", zone.Name, stageID)
	el := SpiritAttributes[(chapterID+stageID)%5]
	if isBoss {
		name = chapterBossNames[chapterID-1]
		el = SpiritAttributes[(chapterID*2+1)%5]
	}
	atk := 15 + 12*tier + stageID*2
	if isBoss {
		atk += 30
	}
	// 品阶体系只有 凡/灵/玄/地/天/圣 六档；炼虚(tier6)起的大境界怪物全部按“圣”处理。
	qualityIndex := tier
	if qualityIndex >= len(SpiritQualityNames) {
		qualityIndex = len(SpiritQualityNames) - 1
	}
	enemy := &BattleFighter{
		Name:    name,
		Quality: SpiritQualityNames[qualityIndex],
		Element: el,
		MaxHP:   power * 2,
		HP:      power * 2,
		ATK:     atk,
		DEF:     8 + 6*tier,
		SPD:     40 + tier*5,
		Row:     0,
	}
	applyStageAffix(enemy, stageAffixOf(chapterID, stageID))
	return enemy
}

// stageReward 关卡首通奖励（灵晶）
func stageReward(chapterID, stageID int) int {
	base := 20 + chapterID*15
	if stageID == bossStageID {
		return base * 6
	}
	return base + stageID*5
}

// teamToFighters 出阵灵侍 → 战斗实体。调用前属性必须已经是缩放后的战斗值。
func teamToFighters(team []UserSpiritServant) []*BattleFighter {
	out := make([]*BattleFighter, 0, len(team))
	for i := range team {
		s := &team[i]
		out = append(out, &BattleFighter{
			Name:    s.Name,
			Quality: s.Quality,
			Element: s.Attribute,
			MaxHP:   s.HP,
			HP:      s.HP,
			ATK:     s.ATK + s.MAG/2,
			DEF:     s.DEF,
			SPD:     s.SPD,
		})
	}
	assignBattleRows(out)
	return out
}

// assignBattleRows 速度高的靠后，速度低的顶前排。单人队伍站前排。
// 旧镜像没有 Row 时也走这里，避免历史快照全站前排。
func assignBattleRows(team []*BattleFighter) {
	if len(team) <= 1 {
		if len(team) == 1 {
			team[0].Row = 0
		}
		return
	}
	order := append([]*BattleFighter(nil), team...)
	for i := 1; i < len(order); i++ {
		for j := i; j > 0 && order[j].SPD < order[j-1].SPD; j-- {
			order[j], order[j-1] = order[j-1], order[j]
		}
	}
	frontN := (len(order) + 1) / 2
	if frontN < 1 {
		frontN = 1
	}
	if frontN > len(order)-1 {
		frontN = len(order) - 1
	}
	for i, f := range order {
		if i < frontN {
			f.Row = 0
		} else {
			f.Row = 1
		}
	}
}

// wuxingFormationActive 出战凑齐金木水火土才开阵眼。阴阳不计入，也不替代五行。
func wuxingFormationActive(team []*BattleFighter) bool {
	seen := map[string]bool{}
	for _, f := range team {
		if f == nil {
			continue
		}
		seen[f.Element] = true
	}
	for _, el := range SpiritAttributes[:5] {
		if !seen[el] {
			return false
		}
	}
	return true
}

// applyTeamAura 阵眼与天时只加攻防血，不加先手，避免速度被两套加成反复放大。
func applyTeamAura(team []*BattleFighter, heaven string) (formation bool) {
	formation = wuxingFormationActive(team)
	for _, f := range team {
		if f == nil {
			continue
		}
		mul := 1.0
		if formation {
			mul += formationWuxingBonus
		}
		if heaven != "" && f.Element == heaven {
			mul += heavenElementBonus
		}
		if mul == 1 {
			continue
		}
		f.MaxHP = int(float64(f.MaxHP) * mul)
		f.HP = f.MaxHP
		f.ATK = int(float64(f.ATK) * mul)
		f.DEF = int(float64(f.DEF) * mul)
	}
	return formation
}

// FighterPower 单只战斗实体的战力。必须和 calcDamage 用同一套有效攻击。
// 高血低攻不再能靠气血堆出虚高战力。
func FighterPower(f *BattleFighter) int {
	if f == nil {
		return 0
	}
	atk := f.ATK
	if atk < 1 {
		atk = 1
	}
	defRatio := float64(f.DEF) / float64(atk+f.DEF+1)
	if defRatio > 0.55 {
		defRatio = 0.55
	}
	spdRatio := float64(f.SPD) / 80.0
	if spdRatio > 0.35 {
		spdRatio = 0.35
	}
	score := float64(atk)*powerATKWeight +
		float64(f.MaxHP)*powerHPWeight*(0.55+defRatio) +
		float64(f.DEF)*powerDEFWeight +
		float64(f.SPD)*powerSPDWeight*(1+spdRatio)
	q := QualityGrowth[f.Quality]
	if q <= 0 {
		q = 1
	}
	score *= q
	if score < 1 {
		return 1
	}
	return int(score)
}

// TeamFighterPower 队伍战力 = 个体战力之和 ×（1 + 五行阵眼）。
// 不再按品阶种类加分：品阶已经在伤害和个体战力里。
func TeamFighterPower(team []*BattleFighter) int {
	if len(team) == 0 {
		return 0
	}
	sum := 0
	for _, f := range team {
		sum += FighterPower(f)
	}
	if wuxingFormationActive(team) {
		sum = int(float64(sum) * (1 + formationWuxingBonus))
	}
	return sum
}

// ServantBattlePower 面板/排序用战力。装备与功法必须已经写进五维，否则和战斗不一致。
func ServantBattlePower(s *UserSpiritServant) int {
	if s == nil {
		return 0
	}
	return FighterPower(&BattleFighter{
		Quality: s.Quality,
		Element: s.Attribute,
		MaxHP:   s.HP,
		ATK:     s.ATK + s.MAG/2,
		DEF:     s.DEF,
		SPD:     s.SPD,
	})
}

// effectiveATK 当前攻击。阴属性偷攻期间临时下降，不会改库存属性。
func effectiveATK(f *BattleFighter) int {
	if f == nil {
		return 1
	}
	atk := f.ATK - f.AtkStolen
	if atk < 1 {
		atk = 1
	}
	return atk
}

func effectiveSPD(f *BattleFighter) int {
	if f == nil {
		return 0
	}
	if f.Slow > 0 {
		spd := f.SPD / 2
		if spd < 1 {
			return 1
		}
		return spd
	}
	return f.SPD
}

// calcDamage 伤害 = max(1, 有效攻×1.15 - 防×0.55) × 属性 × 品阶 × 词缀抗性。
// 防御改为比例减伤封顶，避免「攻略低于防」时所有输出都变成 1，高防号无意义碾压低攻号。
func calcDamage(att, def *BattleFighter, resistEl string) int {
	atk := float64(effectiveATK(att))
	base := atk * 1.15
	mitigation := float64(def.DEF) / (float64(def.DEF) + atk*1.4 + 20)
	if mitigation > 0.62 {
		mitigation = 0.62
	}
	base *= 1 - mitigation
	if base < 1 {
		base = 1
	}
	mult := attrMult(att.Element, def.Element)
	mult += QualitySuppressRate[att.Quality] - QualitySuppressRate[def.Quality]
	if resistEl != "" && att.Element == resistEl {
		mult *= 0.72
	}
	if mult < 0.25 {
		mult = 0.25
	}
	dmg := int(base * mult)
	if dmg < 1 {
		return 1
	}
	return dmg
}

func applyDamage(target *BattleFighter, dmg int) int {
	if target == nil || dmg <= 0 || target.HP <= 0 {
		return 0
	}
	if target.Shield > 0 {
		if dmg <= target.Shield {
			target.Shield -= dmg
			return 0
		}
		dmg -= target.Shield
		target.Shield = 0
	}
	target.HP -= dmg
	if target.HP < 0 {
		target.HP = 0
	}
	return dmg
}

// castElementSkill 每 3 次行动放一次。技能只改节奏。
// 治疗、护盾、灼烧按攻击或防御算，不按气血算，避免高血单位靠技能把战力口径再次打穿。
func castElementSkill(att *BattleFighter, allies, enemies []*BattleFighter, brief *BattleBrief) {
	if att == nil || att.HP <= 0 {
		return
	}
	switch att.Element {
	case "金":
		target := firstAlive(enemies)
		if target == nil {
			return
		}
		shred := target.DEF / 8
		if shred < 1 {
			shred = 1
		}
		target.DEF -= shred
		if target.DEF < 0 {
			target.DEF = 0
		}
		brief.add("金·破防：%s 削去 %s %d 防", att.Name, target.Name, shred)
	case "木":
		heal := effectiveATK(att) / 4
		if heal < 1 {
			heal = 1
		}
		before := att.HP
		att.HP += heal
		if att.HP > att.MaxHP {
			att.HP = att.MaxHP
		}
		if att.HP > before {
			brief.add("木·回春：%s 回复 %d 气血", att.Name, att.HP-before)
		}
	case "水":
		target := fastestAlive(enemies)
		if target == nil {
			return
		}
		target.Slow = 1
		brief.add("水·迟滞：%s 使 %s 速度减半", att.Name, target.Name)
	case "火":
		target := firstAlive(enemies)
		if target == nil {
			return
		}
		target.Burn = 2
		brief.add("火·灼烧：%s 点燃 %s", att.Name, target.Name)
	case "土":
		shield := att.DEF / 2
		if shield < 1 {
			shield = 1
		}
		att.Shield += shield
		brief.add("土·岩盾：%s 获得 %d 护盾", att.Name, shield)
	case "阴":
		target := firstAlive(enemies)
		if target == nil {
			return
		}
		stolen := effectiveATK(target) / 10
		if stolen < 1 {
			stolen = 1
		}
		target.AtkStolen += stolen
		target.AtkDown = 1
		brief.add("阴·夺攻：%s 偷去 %s %d 攻", att.Name, target.Name, stolen)
	case "阳":
		cleared := false
		for _, f := range allies {
			if f == nil || f.HP <= 0 {
				continue
			}
			if f.Burn > 0 || f.Slow > 0 || f.AtkDown > 0 {
				f.Burn, f.Slow, f.AtkDown, f.AtkStolen = 0, 0, 0, 0
				cleared = true
			}
		}
		if cleared {
			brief.add("阳·净明：%s 净化了己方减益", att.Name)
		}
	}
}

func tickFighterStatus(f *BattleFighter) {
	if f == nil || f.HP <= 0 {
		return
	}
	if f.Burn > 0 {
		burn := f.ATK / 8
		if burn < 1 {
			burn = 1
		}
		applyDamage(f, burn)
		f.Burn--
	}
	if f.Slow > 0 {
		f.Slow--
	}
	if f.AtkDown > 0 {
		f.AtkDown--
		if f.AtkDown == 0 {
			f.AtkStolen = 0
		}
	}
}

func firstAlive(team []*BattleFighter) *BattleFighter {
	for _, f := range team {
		if f != nil && f.HP > 0 {
			return f
		}
	}
	return nil
}

func fastestAlive(team []*BattleFighter) *BattleFighter {
	var best *BattleFighter
	for _, f := range team {
		if f == nil || f.HP <= 0 {
			continue
		}
		if best == nil || effectiveSPD(f) > effectiveSPD(best) {
			best = f
		}
	}
	return best
}

// pickBattleTarget 优先打前排。前排全灭后才稳定打到后排。
func pickBattleTarget(enemies []*BattleFighter, rng *rand.Rand) *BattleFighter {
	var front, back []*BattleFighter
	for _, f := range enemies {
		if f == nil || f.HP <= 0 {
			continue
		}
		if f.Row == 0 {
			front = append(front, f)
		} else {
			back = append(back, f)
		}
	}
	pool := front
	if len(front) == 0 {
		pool = back
	} else if len(back) > 0 && rng.Float64() > frontRowShare {
		pool = back
	}
	if len(pool) == 0 {
		return nil
	}
	return pool[rng.Intn(len(pool))]
}

func aliveCount(team []*BattleFighter) int {
	n := 0
	for _, f := range team {
		if f != nil && f.HP > 0 {
			n++
		}
	}
	return n
}

type battleActor struct {
	f    *BattleFighter
	side int // 0=攻方/我方，1=守方/敌方
}

func battleOrder(teamA, teamB []*BattleFighter) []battleActor {
	var order []battleActor
	for _, f := range teamA {
		if f != nil && f.HP > 0 {
			order = append(order, battleActor{f: f, side: 0})
		}
	}
	for _, f := range teamB {
		if f != nil && f.HP > 0 {
			order = append(order, battleActor{f: f, side: 1})
		}
	}
	for i := 1; i < len(order); i++ {
		for j := i; j > 0; j-- {
			a, b := order[j], order[j-1]
			if effectiveSPD(a.f) > effectiveSPD(b.f) || (effectiveSPD(a.f) == effectiveSPD(b.f) && a.side < b.side) {
				order[j], order[j-1] = b, a
			} else {
				break
			}
		}
	}
	return order
}

// prepareBattleTeams 开战前统一挂阵眼、天时、站位。resistEl 只作用于打向 teamB 的伤害。
func prepareBattleTeams(teamA, teamB []*BattleFighter, heaven string, brief *BattleBrief) {
	assignBattleRows(teamA)
	assignBattleRows(teamB)
	if applyTeamAura(teamA, heaven) {
		brief.add("五行阵眼：我方凑齐五行，攻防血 +8%%")
	}
	if applyTeamAura(teamB, "") && len(teamB) > 1 {
		brief.add("五行阵眼：对方凑齐五行")
	}
	if heaven != "" {
		brief.add("今日天时：%s行攻防 +15%%", heaven)
	}
	if len(battleOrder(teamA, teamB)) > 0 {
		first := battleOrder(teamA, teamB)[0]
		side := "我方"
		if first.side == 1 {
			side = "对方"
		}
		brief.add("先手：%s·%s（速 %d）", side, first.f.Name, effectiveSPD(first.f))
	}
}

// resolveBattle 共享回合制。teamA 为攻方，teamB 为守方。
// resistEl 非空时，该属性打向守方的伤害降低（推图抗性词缀）。
// heaven 非空时，该属性单位攻防血 +15%（镜场天时，只加攻方）。
func resolveBattle(teamA, teamB []*BattleFighter, resistEl, heaven string, rng *rand.Rand) *TeamBattleResult {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	brief := &BattleBrief{}
	prepareBattleTeams(teamA, teamB, heaven, brief)
	res := &TeamBattleResult{Brief: brief.Text()}
	for _, f := range teamA {
		if f != nil {
			res.HPTotalA += f.MaxHP
		}
	}
	for _, f := range teamB {
		if f != nil {
			res.HPTotalB += f.MaxHP
		}
	}

	for round := 1; round <= maxBattleRounds; round++ {
		res.Rounds = round
		for _, f := range append(append([]*BattleFighter{}, teamA...), teamB...) {
			tickFighterStatus(f)
		}
		if aliveCount(teamB) == 0 || aliveCount(teamA) == 0 {
			break
		}
		for _, a := range battleOrder(teamA, teamB) {
			if a.f.HP <= 0 {
				continue
			}
			allies, enemies := teamA, teamB
			if a.side == 1 {
				allies, enemies = teamB, teamA
			}
			if aliveCount(enemies) == 0 {
				break
			}
			a.f.Actions++
			if a.f.Actions%skillEveryNTurns == 0 {
				castElementSkill(a.f, allies, enemies, brief)
			}
			if a.f.HP <= 0 || aliveCount(enemies) == 0 {
				continue
			}
			target := pickBattleTarget(enemies, rng)
			if target == nil || target.HP <= 0 {
				continue
			}
			resist := ""
			if a.side == 0 {
				resist = resistEl
			}
			applyDamage(target, calcDamage(a.f, target, resist))
		}
		if aliveCount(teamB) == 0 || aliveCount(teamA) == 0 {
			break
		}
	}

	for _, f := range teamA {
		if f != nil && f.HP > 0 {
			res.HPLeftA += f.HP
		}
	}
	for _, f := range teamB {
		if f != nil && f.HP > 0 {
			res.HPLeftB += f.HP
		}
	}
	res.Win = aliveCount(teamB) == 0 && aliveCount(teamA) > 0
	// 超时未分胜负：剩余血量占比高的一方胜。完全同比例时守方胜，攻方必须打出优势。
	if aliveCount(teamA) > 0 && aliveCount(teamB) > 0 && res.HPTotalA > 0 && res.HPTotalB > 0 {
		ratioA := float64(res.HPLeftA) / float64(res.HPTotalA)
		ratioB := float64(res.HPLeftB) / float64(res.HPTotalB)
		res.Win = ratioA > ratioB
	}
	res.Brief = brief.Text()
	return res
}

// runStageBattle PVE：我方队伍打单名关卡敌人。resist 来自该关固定词缀，不从敌人属性猜测。
func runStageBattle(team []*BattleFighter, enemy *BattleFighter, chapterID, stageID int) *SpiritBattleResult {
	enemies := []*BattleFighter{}
	if enemy != nil {
		enemies = []*BattleFighter{enemy}
	}
	resist := ""
	if affix := stageAffixOf(chapterID, stageID); affix.Key == "resist" {
		resist = strings.TrimSuffix(affix.Name, "抗")
	}
	battle := resolveBattle(team, enemies, resist, "", nil)
	res := &SpiritBattleResult{
		Win:         battle.Win,
		TeamHPLeft:  battle.HPLeftA,
		TeamHPTotal: battle.HPTotalA,
		Rounds:      battle.Rounds,
		Brief:       battle.Brief,
	}
	if res.Win && res.TeamHPTotal > 0 {
		ratio := float64(res.TeamHPLeft) / float64(res.TeamHPTotal)
		switch {
		case ratio >= 0.80:
			res.Stars = 3
		case ratio >= 0.45:
			res.Stars = 2
		default:
			res.Stars = 1
		}
	}
	return res
}

// TodayHeavenElement 镜场天时：北京时间日期固定一个五行，当天所有镜场攻击共用。
// 不进数据库。重启、跨进程结果一致，因为只由日期决定。
func TodayHeavenElement(now time.Time) string {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		loc = time.FixedZone("CST", 8*3600)
	}
	day := now.In(loc).Format("20060102")
	n := 0
	for _, c := range day {
		n = n*10 + int(c-'0')
	}
	return SpiritAttributes[n%5]
}

// ==========================================
// 神行符（体力）
// ==========================================

// getOrCreateStaminaTx 获取体力行，跨天自动重置为每日上限
func getOrCreateStaminaTx(tx *gorm.DB, userID int64) (*SpiritBattleStamina, error) {
	today := time.Now().Format("20060102")
	var s SpiritBattleStamina
	err := tx.Where("user_id = ?", userID).First(&s).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		s = SpiritBattleStamina{UserID: userID, DayKey: today, Stamina: divineTravelDailyCap}
		if e := tx.Create(&s).Error; e != nil {
			return nil, e
		}
		return &s, nil
	}
	if err != nil {
		return nil, err
	}
	if s.DayKey != today {
		s.DayKey = today
		s.Stamina = divineTravelDailyCap
		if e := tx.Save(&s).Error; e != nil {
			return nil, e
		}
	}
	return &s, nil
}

// GetUserStamina 剩余体力（面板展示用，非事务）
func GetUserStamina(userID int64) int {
	s, err := getOrCreateStaminaTx(db, userID)
	if err != nil {
		return 0
	}
	return s.Stamina
}

// ==========================================
// PVE 挑战 / 扫荡
// ==========================================

// PveFightResult 一次挑战的结果
type PveFightResult struct {
	Win          bool
	Stars        int
	Reward       int
	EnemyName    string
	TeamHPLeft   int
	TeamHPTotal  int
	StaminaLeft  int
	IsBoss       bool
	DroppedEgg   *SpiritEgg // 胜利掉蛋（普通关/Boss，可空）
	DroppedItems []string   // 掉落道具（灵魄/真身碎片）
	// DroppedTreasure Boss 掉落的至宝名（可空）；化神及之后 Boss 概率掉下一境至宝，扫荡不掉。
	DroppedTreasure string
	Brief           string
}

// PveFight 执行一次推图挑战（全程事务：扣体力 → 战斗 → 记进度 → 发奖励）
func PveFight(userID int64, chapterID, stageID int) (*PveFightResult, error) {
	zone := chapterZone(chapterID)
	if zone == nil {
		return nil, fmt.Errorf("章节不存在")
	}
	if stageID < 1 || stageID > bossStageID {
		return nil, fmt.Errorf("关卡不存在")
	}

	result := &PveFightResult{IsBoss: stageID == bossStageID}

	// 境界校验（事务外读取：GetOrCreateCultivation 走全局连接池，
	// 在事务内调用会在小连接池下互等连接而死锁）
	cul := GetOrCreateCultivation(userID)
	if cul == nil || cul.MajorRealm < zone.Tier {
		return nil, fmt.Errorf("境界不足，此章节需更高修为")
	}

	err := db.Transaction(func(tx *gorm.DB) error {
		// 2. 关卡解锁：前关至少 1 星
		if stageID > 1 {
			var prev SpiritStageProgress
			if err := tx.Where("user_id = ? AND chapter_id = ? AND stage_id = ?", userID, chapterID, stageID-1).
				First(&prev).Error; err != nil || prev.Stars < 1 {
				return fmt.Errorf("上一关尚未通关，无法挑战此关")
			}
		}

		// 3. 出战队伍（战力高→低，最多 maxTeamSize）
		team, err := pickDeployedTeamTx(tx, userID)
		if err != nil {
			return err
		}
		if len(team) == 0 {
			return fmt.Errorf("尚未编排出战灵侍，请先在出战队列编队")
		}
		team = enhanceServantStats(tx, userID, team) // 并入装备加成

		// 4. 神行符消耗
		stamina, err := getOrCreateStaminaTx(tx, userID)
		if err != nil {
			return err
		}
		if stamina.Stamina < divineTravelCost {
			return fmt.Errorf("神行符已用尽，每日零点恢复")
		}
		stamina.Stamina -= divineTravelCost
		if err := tx.Save(stamina).Error; err != nil {
			return err
		}
		result.StaminaLeft = stamina.Stamina

		// 5. 战斗
		enemy := buildStageEnemy(chapterID, stageID)
		result.EnemyName = enemy.Name
		battle := runStageBattle(teamToFighters(team), enemy, chapterID, stageID)
		result.Win = battle.Win
		result.Stars = battle.Stars
		result.TeamHPLeft = battle.TeamHPLeft
		result.TeamHPTotal = battle.TeamHPTotal
		result.Brief = battle.Brief
		if !battle.Win {
			return nil
		}

		// 6. 进度与奖励（首通全额 / 升星 20%）
		var prog SpiritStageProgress
		oldStars := 0
		if err := tx.Where("user_id = ? AND chapter_id = ? AND stage_id = ?", userID, chapterID, stageID).
			First(&prog).Error; err != nil {
			if !isUniqueConstraintError(err) && !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
			prog = SpiritStageProgress{UserID: userID, ChapterID: chapterID, StageID: stageID}
		} else {
			oldStars = prog.Stars
		}

		if battle.Stars > oldStars {
			reward := stageReward(chapterID, stageID)
			if oldStars > 0 {
				reward = reward * starUpRewardRatio / 100
			}
			prog.Stars = battle.Stars
			now := time.Now()
			prog.ClearedAt = &now
			var saveErr error
			if prog.ID == 0 {
				saveErr = tx.Create(&prog).Error
			} else {
				saveErr = tx.Save(&prog).Error
			}
			if saveErr != nil {
				// 并发首通兜底：改为更新
				saveErr = tx.Model(&SpiritStageProgress{}).
					Where("user_id = ? AND chapter_id = ? AND stage_id = ?", userID, chapterID, stageID).
					Updates(map[string]interface{}{"stars": battle.Stars, "cleared_at": now}).Error
			}
			if saveErr != nil {
				return fmt.Errorf("进度写入失败: %s", formatTelegramSendError(saveErr))
			}
			if reward > 0 {
				if err := EarnLingjing(tx, userID, reward, "pve_clear",
					fmt.Sprintf("灵墟推图奖励：第%d章第%d关", chapterID, stageID)); err != nil {
					return err
				}
				result.Reward = reward
			}
		}

		// 7. 掉蛋（普通关 10% / Boss 30%，扫荡不掉）
		if stageID == bossStageID {
			egg, err := dropBossEgg(tx, userID, chapterID, zone)
			if err != nil {
				return err
			}
			result.DroppedEgg = egg
		} else {
			egg, err := dropNormalEgg(tx, userID, chapterID, zone)
			if err != nil {
				return err
			}
			result.DroppedEgg = egg
		}

		// 8. 道具掉落（灵魄/万能真身碎片，扫荡不掉）
		if items := rollPveItemDrops(chapterID, stageID); len(items) > 0 {
			for _, it := range items {
				if err := addSpiritItemTx(tx, userID, it, 1); err != nil {
					return err
				}
			}
			result.DroppedItems = items
		}

		// 9. 至宝掉落（化神及之后的章节 Boss 概率掉“下一境界”至宝，扫荡不掉）。
		if stageID == bossStageID {
			if treasure := rollPveBossTreasureDrop(chapterID); treasure != "" {
				if err := gardenGrantInventoryInTx(tx, userID, treasure, 1); err != nil {
					return err
				}
				result.DroppedTreasure = treasure
			}
		}
		return nil
	})
	if err != nil {
		log.Printf("[灵侍] 推图挑战 user=%d ch=%d stage=%d err=%s", userID, chapterID, stageID, formatTelegramSendError(err))
		return nil, err
	}
	return result, nil
}

// PveSweep 扫荡（需三星，每关每日 3 次，奖励 = 40%）
func PveSweep(userID int64, chapterID, stageID int) (int, error) {
	if chapterZone(chapterID) == nil {
		return 0, fmt.Errorf("章节不存在")
	}
	var reward int
	err := db.Transaction(func(tx *gorm.DB) error {
		var prog SpiritStageProgress
		if err := tx.Where("user_id = ? AND chapter_id = ? AND stage_id = ?", userID, chapterID, stageID).
			First(&prog).Error; err != nil {
			return fmt.Errorf("该关尚未通关，无法扫荡")
		}
		if prog.Stars < 3 {
			return fmt.Errorf("需三星通关才可扫荡")
		}
		today := time.Now().Format("20060102")
		if prog.SweepDay != today {
			prog.SweepDay = today
			prog.SweepCount = 0
		}
		if prog.SweepCount >= sweepDailyLimit {
			return fmt.Errorf("今日扫荡次数已用尽（3次），明日再来")
		}
		reward = stageReward(chapterID, stageID) * sweepRewardRatio / 100
		if reward < 1 {
			reward = 1
		}
		prog.SweepCount++
		if err := tx.Save(&prog).Error; err != nil {
			return err
		}
		return EarnLingjing(tx, userID, reward, "pve_sweep",
			fmt.Sprintf("灵墟推图扫荡：第%d章第%d关", chapterID, stageID))
	})
	if err != nil {
		return 0, err
	}
	return reward, nil
}

// listSweepableProgress 列出当前可扫荡的关卡进度（三星 + 章节已解锁，按章节/关卡升序）。
// 「一键扫荡」与推图主页按钮计数必须共用这一筛选口径，否则按钮显示的可扫次数会与实际扫荡范围不一致。
// 境界读取在事务外（GetOrCreateCultivation 走全局连接池，事务内调用会死等）。
func listSweepableProgress(userID int64) ([]SpiritStageProgress, error) {
	cul := GetOrCreateCultivation(userID)
	majorRealm := 0
	if cul != nil {
		majorRealm = cul.MajorRealm
	}

	var progs []SpiritStageProgress
	if err := db.Where("user_id = ? AND stars >= ?", userID, 3).
		Order("chapter_id asc, stage_id asc").Find(&progs).Error; err != nil {
		return nil, err
	}

	out := make([]SpiritStageProgress, 0, len(progs))
	for _, p := range progs {
		zone := chapterZone(p.ChapterID)
		if zone == nil || majorRealm < zone.Tier {
			continue
		}
		out = append(out, p)
	}
	return out, nil
}

// remainingSweepsFor 某关今日剩余可扫荡次数（跨天按满额，已用尽为 0）。
func remainingSweepsFor(p SpiritStageProgress, today string) int {
	if p.SweepDay == today {
		if remain := sweepDailyLimit - p.SweepCount; remain > 0 {
			return remain
		}
		return 0
	}
	return sweepDailyLimit
}

// PveSweepAll 一键扫荡：扫荡所有已解锁章节中三星关的剩余每日次数。
// 每关每日 3 次上限、三星门槛与奖励口径完全复用 PveSweep，不新增资产规则；
// 每次扫荡各自事务（与单关扫荡一致），中途异常只跳过该关，已完成的扫荡不回滚。
// 返回 (总扫荡次数, 总奖励灵晶, error)。
func PveSweepAll(userID int64) (int, int, error) {
	progs, err := listSweepableProgress(userID)
	if err != nil {
		return 0, 0, err
	}

	today := time.Now().Format("20060102")
	totalSweeps, totalReward := 0, 0
	for _, p := range progs {
		for i := 0; i < remainingSweepsFor(p, today); i++ {
			reward, err := PveSweep(userID, p.ChapterID, p.StageID)
			if err != nil {
				// 该关已用尽或状态变化：跳过，继续下一关
				break
			}
			totalSweeps++
			totalReward += reward
		}
	}
	if totalSweeps > 0 {
		log.Printf("[灵侍] 一键扫荡 user=%d sweeps=%d reward=%d", userID, totalSweeps, totalReward)
	}
	return totalSweeps, totalReward, nil
}

// countSweepableStages 统计用户当前剩余可扫荡次数，供推图主页按钮展示。
// 与 PveSweepAll 共用 listSweepableProgress，保证「显示的可扫次数」=「实际会扫的次数」。
func countSweepableStages(userID int64) int {
	progs, err := listSweepableProgress(userID)
	if err != nil {
		return 0
	}
	today := time.Now().Format("20060102")
	total := 0
	for _, p := range progs {
		total += remainingSweepsFor(p, today)
	}
	return total
}
