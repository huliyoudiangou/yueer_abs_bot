package main

import "sync"

// 三个群内休闲游戏：推牌九、三界骰局、皇家赛马。
// 统一由该协调器保证：同一群同一时间只能有一个游戏处于进行中。

const (
	casualGamePaiGow = "pai_gow"
	casualGameDice   = "dice"
	casualGameRace   = "race"
)

// casualGameChatMu 按群串行化“发起休闲游戏”的检查与占位。
var casualGameChatMu sync.Map // chatID -> *sync.Mutex

func lockCasualGameChat(chatID int64) func() {
	v, _ := casualGameChatMu.LoadOrStore(chatID, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func casualGameDisplayName(game string) string {
	switch game {
	case casualGamePaiGow:
		return "推牌九"
	case casualGameDice:
		return "三界骰局"
	case casualGameRace:
		return "赛马"
	default:
		return "休闲游戏"
	}
}

// casualGameActiveInChat 返回当前群内处于进行中的另一个休闲游戏。
// exclude 用于排除正在尝试发起的那个游戏自身。
// 调用方应先持有 lockCasualGameChat(chatID)，避免两个发起请求交错检查。
func casualGameActiveInChat(chatID int64, exclude string) (string, bool) {
	if exclude != casualGameRace {
		if state := getRaceState(chatID); state != nil {
			state.Mu.Lock()
			active := state.IsActive
			state.Mu.Unlock()
			if active {
				return casualGameRace, true
			}
		}
	}
	if exclude != casualGameDice {
		if state := getDiceState(chatID); state != nil {
			state.Mu.Lock()
			active := state.IsActive
			state.Mu.Unlock()
			if active {
				return casualGameDice, true
			}
		}
	}
	if exclude != casualGamePaiGow {
		if state := getPaiGowState(chatID); state != nil {
			state.Mu.Lock()
			active := state.IsActive
			state.Mu.Unlock()
			if active {
				return casualGamePaiGow, true
			}
		}
	}
	return "", false
}
