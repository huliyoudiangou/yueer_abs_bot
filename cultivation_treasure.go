package main

import "strings"

// ==========================================
// 至宝（高境突破材料）
//
// 化神之后，突破不再靠“丹药/积分代购”，而改为消耗“对应至宝 ×1 + 积分渡劫费”。
// 至宝只能通过两条途径获得，禁止上架交易行、禁止被丹药代购路径购买：
//  1. 灵墟章节 Boss：前一境界的地图 Boss 概率掉落“下一境界”的至宝；
//  2. 世界 Boss：结算时按参与者当前大境界概率掉落“突破到下一境界”的至宝。
//
// 资产规则：
//   - 掉落走 gardenGrantInventoryInTx（Inventory 累加，受部分唯一索引兜底）；
//   - 突破消耗走 Inventory 条件扣减（quantity >= 1），失败/成功均消耗，与保底次数联动；
//   - 至宝名不得与任何可交易/可代购道具冲突，marketplace 上架处会强制拦截。
// ==========================================

// cultivationTreasureByFromRealm 由“当前大境界（突破前）”索引对应至宝名。
// 键 5~12 对应“化神→炼虚 … 大罗→道祖”；道祖(13)封顶无下一境，故无至宝。
var cultivationTreasureByFromRealm = map[int]string{
	5:  "虚空真髓", // 化神 → 炼虚
	6:  "合道天珠", // 炼虚 → 合体
	7:  "混元一气", // 合体 → 大乘
	8:  "渡厄仙引", // 大乘 → 真仙
	9:  "赤金仙露", // 真仙 → 金仙
	10: "太乙玄黄", // 金仙 → 太乙
	11: "大罗道果", // 太乙 → 大罗
	12: "混沌道种", // 大罗 → 道祖
}

// cultivationTreasureDropRatePercent 至宝掉落概率（%）。灵墟 Boss 与世界 Boss 共用。
func cultivationTreasureDropRatePercent() int { return 10 }

// cultivationTreasureNameForRealm 返回“从该大境界突破到下一境”所需的至宝名；无则返回空串。
func cultivationTreasureNameForRealm(major int) string {
	return cultivationTreasureByFromRealm[major]
}

// isCultivationTreasureItemName 判断某 Inventory 物品名是否为至宝。
func isCultivationTreasureItemName(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, v := range cultivationTreasureByFromRealm {
		if v == name {
			return true
		}
	}
	return false
}
