package main

import (
	"fmt"
	"math/rand"
)

// ==========================================
// 灵侍体系配置中枢（Phase 1）
// 所有命名、品阶、概率、常量口径统一定义在这里
// ==========================================

// 灵侍属性体系：金木水火土阴阳（7属性闭环，阴阳仅地阶及以上产出）
var SpiritAttributes = []string{"金", "木", "水", "火", "土", "阴", "阳"}
var SpiritQualityNames = []string{"凡", "灵", "玄", "地", "天", "圣"}
var SpiritSlotTypes = []string{"兵甲", "魂魄"}

// 品阶战力定位（基准值）
var QualityBasePower = map[string]int{
	"凡": 110, "灵": 200, "玄": 420, "地": 800, "天": 1600, "圣": 3200,
}

// 品阶成长系数
var QualityGrowth = map[string]float64{
	"凡": 1.05, "灵": 1.06, "玄": 1.08, "地": 1.10, "天": 1.13, "圣": 1.16,
}

// 品阶星级上限（凡3/灵4/玄5/地6/天7/圣9）
var QualityMaxStar = map[string]int{
	"凡": 3, "灵": 4, "玄": 5, "地": 6, "天": 7, "圣": 9,
}

// 品阶压制率
var QualitySuppressRate = map[string]float64{
	"凡": 0.00, "灵": 0.03, "玄": 0.06, "地": 0.09, "天": 0.12, "圣": 0.16,
}

// ==========================================
// 吞噬配置（2026-09 新增）
// 宿主吞噬其他灵侍换取属性点：单只按被吞品阶取点数，星级每 +1 额外 +1；
// 属性点按宿主五维一级基础值比例分配，直接并入基础属性（随等级/星级成长放大）。
// 点数表为设计推断项，待运营调参。
// ==========================================

// DevourPointsByQuality 单只被吞灵侍的属性点预算（按品阶）
var DevourPointsByQuality = map[string]int{
	"凡": 2, "灵": 4, "玄": 8, "地": 16, "天": 32, "圣": 64,
}

// DevourPointsFor 被吞灵侍（品阶+星级）折算的属性点预算
func DevourPointsFor(quality string, star int) int {
	base, ok := DevourPointsByQuality[quality]
	if !ok || base < 1 {
		base = DevourPointsByQuality["凡"]
	}
	if star < 1 {
		star = 1
	}
	return base + star - 1
}

// 境界加权：修为境界对灵侍能力加成
var CultivationPowerWeight = map[int]float64{
	0: 0.00, 1: 0.03, 2: 0.06, 3: 0.09, 4: 0.12, 5: 0.15,
}

// 灵墟区域设定（按修仙 MajorRealm 门槛 0-13 解锁；名称为展示名，运行时以修仙配置为准）
type SpiritZone struct {
	Key        string // 区域ID
	Name       string // 名称
	Tier       int    // MajorRealm 门槛（0-13）
	SpawnRates []int  // 各品阶出现概率（万分率，与 SpiritQualityNames 对齐：凡/灵/玄/地/天/圣）
}

var SpiritZones = []SpiritZone{
	{"qingzhu", "青竹林海", 0, []int{8500, 1350, 145, 5, 0, 0}},
	{"wumu", "迷雾深谷", 1, []int{4800, 4500, 620, 80, 10, 0}},
	{"duanyue", "断岳山脉", 2, []int{1000, 5300, 3050, 550, 50, 5}},
	{"youming", "幽冥绝岭", 3, []int{200, 2900, 4200, 2000, 50, 10}},
	{"guixu", "归墟海眼", 4, []int{0, 600, 4100, 3650, 1400, 100}},
	{"buzhou", "不周山巅", 5, []int{0, 100, 1400, 4550, 3600, 350}},
	{"wendao", "问道星海", 6, []int{0, 0, 300, 3200, 5000, 1500}},
	{"liangyi", "两仪秘境", 7, []int{0, 0, 100, 2200, 5600, 2100}},
	{"guiyuan", "归元天阙", 8, []int{0, 0, 0, 1400, 6000, 2600}},
	{"xianting", "仙庭残迹", 9, []int{0, 0, 0, 800, 6000, 3200}},
	{"chijin", "赤金天池", 10, []int{0, 0, 0, 400, 5600, 4000}},
	{"taiyi", "太一仙山", 11, []int{0, 0, 0, 100, 5000, 4900}},
	{"daluo", "大罗天境", 12, []int{0, 0, 0, 0, 4000, 6000}},
	{"hunyuan", "混元祖庭", 13, []int{0, 0, 0, 0, 2500, 7500}},
}

// 灵侍名录
type ServantName struct {
	Name string // 名字
}

var ServantNamePool = map[string][]ServantName{
	"凡": {
		{Name: "铁羽鸡"}, {Name: "铜甲龟"}, {Name: "金线蛇"}, {Name: "锈刃螳螂"},
		{Name: "青竹鼠"}, {Name: "藤尾猫"}, {Name: "叶灵兔"}, {Name: "松果猬"},
		{Name: "溪涧灵蛙"}, {Name: "雨声鱼"}, {Name: "露珠精"}, {Name: "清溪蟹"},
		{Name: "灶火狸"}, {Name: "炭爪狸"}, {Name: "烛尾鼠"}, {Name: "暖阳雀"},
		{Name: "泥丸兽"}, {Name: "坡地龟"}, {Name: "黄篱犬"}, {Name: "灶土蜂"},
	},
	"灵": {
		{Name: "银铃燕"}, {Name: "金丝灵猴"}, {Name: "剑穗雀"}, {Name: "铜镜貉"},
		{Name: "灵芝麋"}, {Name: "杏花貂"}, {Name: "碧眼虎"}, {Name: "月牙鲤"},
		{Name: "潮音贝"}, {Name: "碧潭蛟崽"}, {Name: "雪沫狐"}, {Name: "流萤蝶"},
		{Name: "赤尾狐"}, {Name: "焰心狸"}, {Name: "灯笼火鸦"}, {Name: "山罄兽"},
		{Name: "陶纹猫"}, {Name: "坡上灵驹"}, {Name: "岩针蜂"}, {Name: "竹露狐"},
	},
	"玄": {
		{Name: "玄铁狼"}, {Name: "银翼雕"}, {Name: "金瞳虎"}, {Name: "锁甲鳄"},
		{Name: "青木猿"}, {Name: "鬼脸藤妖"}, {Name: "血纹蟒"}, {Name: "翡翠孔雀"},
		{Name: "玄波龟"}, {Name: "冰晶蛇"}, {Name: "雾隐鲛"}, {Name: "墨鳞鳅"},
		{Name: "熔岩蜥"}, {Name: "三足火雏"}, {Name: "焰鬃狮"}, {Name: "地火猬"},
		{Name: "山魁"}, {Name: "磐石小巨人"}, {Name: "裂地犰狳"}, {Name: "黄风貂"},
	},
	"地": {
		{Name: "庚金剑虎"}, {Name: "白银狮鹫"}, {Name: "万刃甲龙"}, {Name: "千年树魅"},
		{Name: "建木灵猿"}, {Name: "青帝藤蛟"}, {Name: "覆海蛟"}, {Name: "冰渊螭"},
		{Name: "捣药玉兔"}, {Name: "火麟·幼"}, {Name: "三昧火鸦"}, {Name: "焚天雀"},
		{Name: "镇岳玄龟"}, {Name: "息壤兽"}, {Name: "大地苍熊"}, {Name: "黄泉蝶"},
		{Name: "罗刹夜魅"}, {Name: "忘川犬"}, {Name: "烈阳金鹏"}, {Name: "紫电雷驹"},
	},
	"天": {
		{Name: "白虎·少年"}, {Name: "轩辕剑灵"}, {Name: "大鹏金翅雕"}, {Name: "青龙·幼"},
		{Name: "句芒残念"}, {Name: "天桃木魅"}, {Name: "玄武·稚"}, {Name: "覆海蛟王"},
		{Name: "天河鲸"}, {Name: "朱雀·雏"}, {Name: "九阳乌·少年"}, {Name: "焚世红莲"},
		{Name: "黄土麒麟"}, {Name: "石敢当"}, {Name: "鬼帝陶俑"}, {Name: "冥凰·幼"},
		{Name: "六尾妖狐"}, {Name: "雷泽龙驹"}, {Name: "追日神驹"}, {Name: "九曜曦兽"},
	},
	"圣": {
		{Name: "五爪金龙"}, {Name: "金翅大鹏·圣"}, {Name: "帝江"}, {Name: "九爪苍龙"},
		{Name: "句芒·真身"}, {Name: "英招"}, {Name: "九天鲲鹏"}, {Name: "应龙"},
		{Name: "精卫"}, {Name: "元凤"}, {Name: "金乌大圣"}, {Name: "毕方"},
		{Name: "麒麟圣皇"}, {Name: "陆吾"}, {Name: "混沌饕餮"}, {Name: "朱雀·圣"},
		{Name: "太阴望舒"}, {Name: "烛九阴"}, {Name: "白泽"}, {Name: "夸父逐日猿"},
	},
}

// 装备品阶（与灵侍品阶同名）
var EquipmentQualityNames = SpiritQualityNames

// 境界-品阶对应（用于 Boss/秘境解锁判断）
func GetRealmForQuality(quality string) int {
	for i, v := range SpiritQualityNames {
		if v == quality {
			return i + 1
		}
	}
	return 1
}

// 阴阳品阶不可交易（硬约束骨架）
var TradeLockedQuality = map[string]bool{
	"凡": false, "灵": false, "玄": false, "地": false, "天": false, "圣": true,
}

// 等级上限 = 星级 × 10
func MaxLevelByStar(star int) int { return star * 10 }

// 灵尘换算（1 灵晶 = 100 灵尘）
func LingchenFromLingjing(lingjing int) int { return lingjing * 100 }

// 等级提升的经验需求（线性骨架）
func ExperienceForLevel(level int) int { return level * 13 }

// 灵晶兑换比率：1 积分 = 10 灵晶
func LingjingExchangeRate() int { return 10 }

// 灵晶兑换日限额（1000 积分 = 10000 灵晶）
func LingjingDailyCap() int { return 10000 }

// 灵晶兑换最小单位（100 积分 = 1000 灵晶）
func LingjingMinExchange() int { return 100 }

// 灵晶捕捉境界门槛
var LingjingCaptureRequirement = map[string]int{
	"凡": 0, "灵": 1, "玄": 1, "地": 3, "天": 4, "圣": 5,
}

// 捕捉成功率（按品阶）
var CaptureSuccessRate = map[string]float64{
	"凡": 0.90, "灵": 0.75, "玄": 0.55, "地": 0.40, "天": 0.25, "圣": 0.12,
}

// 缚灵索配置
type SpiritRope struct {
	Key   string
	Name  string
	Cost  int     // 灵晶消耗
	Bonus float64 // 捕捉率加成
}

var SpiritRopes = []SpiritRope{
	{Key: "fusu", Name: "缚灵索", Cost: 30, Bonus: 0.0},
	{Key: "xuanling", Name: "玄灵索", Cost: 120, Bonus: 0.15},
}

// 天品保底阈值
const TianPityThreshold = 30

// 圣品保底阈值（灵墟区域4-5生效，高境区域继续递减）
var ShengPityThreshold = map[string]int{
	"guixu":    300,
	"buzhou":   150,
	"wendao":   200,
	"liangyi":  180,
	"guiyuan":  160,
	"xianting": 140,
	"chijin":   120,
	"taiyi":    100,
	"daluo":    80,
	"hunyuan":  60,
}

// 每日免费探索次数（区域通用）
const DailyFreeExplore = 3

// PVP 每日奖励场次
func LingjingBattleDailyCap() int { return 10 }

// 灵侍现状简述
func GetServantSummary(s *UserSpiritServant) string {
	return fmt.Sprintf("[%s·%s] 等级%d/%d（星%d/%d）",
		s.Quality, s.Attribute, s.Level, MaxLevelByStar(s.Star), s.Star, QualityMaxStar[s.Quality])
}

// 提取灵侍图鉴
func GetServantProfile(userID int64) ([]UserSpiritServant, error) {
	var list []UserSpiritServant
	err := db.Where("user_id = ?", userID).Find(&list).Error
	return list, err
}

// 检查是否存在同名灵侍
func CheckDuplicateName(userID int64, name string) bool {
	var count int64
	db.Model(&UserSpiritServant{}).Where("user_id = ? AND name = ?", userID, name).Count(&count)
	return count > 0
}

// 队伍战力估算（骨架：个体战力和 + 品阶多样性加成）
func CalculateTeamPower(team []UserSpiritServant, level int) int {
	power := 0
	uniqueQual := make(map[string]bool)
	for _, s := range team {
		uniqueQual[s.Quality] = true
		power += int(float64(s.HP+s.ATK+s.DEF+s.SPD+s.MAG) * QualityGrowth[s.Quality])
	}
	return int(float64(power) * (1 + float64(len(uniqueQual))*0.05))
}

// 随机生成一只灵侍模板（不落库，骨架用）
func RandServant(quality string, attributeFilter string) UserSpiritServant {
	pool := ServantNamePool[quality]
	if len(pool) == 0 {
		return UserSpiritServant{Quality: quality, Name: "未知灵侍"}
	}
	s := pool[rand.Intn(len(pool))]
	attribute := attributeFilter
	if attribute == "" {
		attribute = "金"
	}
	return UserSpiritServant{
		Quality:   quality,
		Name:      s.Name,
		Attribute: attribute,
		Level:     1,
		Star:      1,
		HP:        100,
		ATK:       20,
		DEF:       15,
		SPD:       10,
		MAG:       12,
	}
}

// 品阶索引
func QualityIndex(quality string) int {
	for i, v := range SpiritQualityNames {
		if v == quality {
			return i
		}
	}
	return 0
}

// 区域今日免费探索判断（骨架）
func GetZoneTodayFreeExplore(zone SpiritZone, dailyUsed int, dailyFree int) bool {
	return dailyUsed < dailyFree
}
