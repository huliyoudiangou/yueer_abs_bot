package main

import (
	"os"
	"strings"
	"testing"

	"gorm.io/gorm/clause"
)

func TestRaceUsesPlayerPoolWithoutSystemSubsidy(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)
	for _, bad := range []string{
		"raceSystemSubsidyRate",
		"raceSystemSubsidyPercent",
		"int(float64(userPool) * raceSystemSubsidyRate)",
		"float64(userPool) * 0.30",
		"庄家配资(30%%)",
		"注入总奖池的 **30%%**",
		"福利补贴",
		"庄家配资",
		"系统将额外注入",
	} {
		if strings.Contains(text, bad) {
			t.Fatalf("race should not reference system subsidy: %q", bad)
		}
	}
	if !strings.Contains(text, "totalPrizePool := userPool") {
		t.Fatal("race settlement should use player pool only")
	}
	if !strings.Contains(text, "玩家筹码组成奖池") || !strings.Contains(text, "玩家奖池") {
		t.Fatal("race user-facing text should explain player-only prize pool")
	}
}

func TestCalculateHorseRaceBetRange(t *testing.T) {
	tests := []struct {
		name      string
		avgPoints float64
		wantMin   int
		wantMax   int
	}{
		{name: "minimum floor", avgPoints: 0, wantMin: 3, wantMax: 15},
		{name: "normal range", avgPoints: 1000, wantMin: 30, wantMax: 150},
		{name: "max capped", avgPoints: 10000, wantMin: 300, wantMax: 500},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotMin, gotMax := calculateHorseRaceBetRange(tt.avgPoints)
			if gotMin != tt.wantMin || gotMax != tt.wantMax {
				t.Fatalf("calculateHorseRaceBetRange(%v) = %d/%d, want %d/%d", tt.avgPoints, gotMin, gotMax, tt.wantMin, tt.wantMax)
			}
		})
	}
}

func TestRaceAndDiceSystemEatSettlementChecksRowsAffected(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)
	for _, marker := range []string{"DICE_BET_SETTLEMENT_MISSED", "RACE_BET_SETTLEMENT_MISSED"} {
		idx := strings.Index(text, marker)
		if idx < 0 {
			t.Fatalf("missing settlement guard: %s", marker)
		}
		start := idx - 220
		if start < 0 {
			start = 0
		}
		block := text[start:minInt(len(text), idx+len(marker)+80)]
		if !strings.Contains(block, "userPool > 0 && res.RowsAffected == 0") {
			t.Fatalf("settlement guard %s should check RowsAffected only when pool exists: %s", marker, block)
		}
	}
}

func TestRaceAndDiceBetCreateChecksRowsAffected(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)

	helperStart := strings.Index(text, "func createDiceBetInTx(")
	if helperStart < 0 {
		t.Fatal("createDiceBetInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "var GroupRaces sync.Map")
	if helperEnd < 0 {
		t.Fatal("bet create helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"func createDiceBetInTx(tx *gorm.DB, bet *DiceBet) error",
		"func createRaceBetInTx(tx *gorm.DB, bet *RaceBet) error",
		"entry.UserName = formatPlainValue(entry.UserName)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"DICE_BET_CREATE_MISSED",
		"RACE_BET_CREATE_MISSED",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("race/dice bet create helper guard missing %q", want)
		}
	}

	diceStart := strings.Index(text, "func handleDiceGame(")
	if diceStart < 0 {
		t.Fatal("handleDiceGame missing")
	}
	diceEnd := strings.Index(text[diceStart:], "func runDiceRoutine(")
	if diceEnd < 0 {
		t.Fatal("handleDiceGame boundary missing")
	}
	diceBlock := text[diceStart : diceStart+diceEnd]
	if !strings.Contains(diceBlock, "createDiceBetInTx(tx, &DiceBet{") {
		t.Fatal("dice bet transaction should use createDiceBetInTx")
	}
	if strings.Contains(diceBlock, "tx.Create(&DiceBet{") {
		t.Fatal("dice bet transaction still creates bet without RowsAffected guard")
	}

	raceStart := strings.Index(text, "func handleHorseRace(")
	if raceStart < 0 {
		t.Fatal("handleHorseRace missing")
	}
	raceEnd := strings.Index(text[raceStart:], "func runHorseRaceRoutine(")
	if raceEnd < 0 {
		t.Fatal("handleHorseRace boundary missing")
	}
	raceBlock := text[raceStart : raceStart+raceEnd]
	if !strings.Contains(raceBlock, "createRaceBetInTx(tx, &RaceBet{") {
		t.Fatal("race bet transaction should use createRaceBetInTx")
	}
	if strings.Contains(raceBlock, "tx.Create(&RaceBet{") {
		t.Fatal("race bet transaction still creates bet without RowsAffected guard")
	}
}

func TestRaceAndDiceSettlementSnapshotsUseActiveDatabaseBets(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)

	for _, tt := range []struct {
		name      string
		startFunc string
		endFunc   string
		helper    string
		markers   []string
		forbidden []string
	}{
		{
			name:      "dice",
			startFunc: "func runDiceRoutine(",
			endFunc:   "func handleHorseRace(",
			helper:    "func loadActiveDiceBetsSnapshot(",
			markers: []string{
				"betsSnapshot, userPool, err := loadActiveDiceBetsSnapshot(diceID)",
				"memoryUserPool := globalDice.TotalPool",
				"dice settlement db snapshot differs from memory",
				`DB.Where("dice_id = ? AND status = ?", diceID, RaceBetStatusActive).Find(&bets).Error`,
			},
			forbidden: []string{
				"betsSnapshot := make(map[int64]*DicePlayerBet, len(globalDice.Bets))",
				"for uid, bet := range globalDice.Bets",
			},
		},
		{
			name:      "race",
			startFunc: "func runHorseRaceRoutine(",
			endFunc:   "",
			helper:    "func loadActiveRaceBetsSnapshot(",
			markers: []string{
				"betsSnapshot, userPool, err := loadActiveRaceBetsSnapshot(raceID)",
				"memoryUserPool := globalRace.TotalPool",
				"race settlement db snapshot differs from memory",
				`DB.Where("race_id = ? AND status = ?", raceID, RaceBetStatusActive).Find(&bets).Error`,
			},
			forbidden: []string{
				"betsSnapshot := make(map[int64]*PlayerBet, len(globalRace.Bets))",
				"for uid, bet := range globalRace.Bets",
			},
		},
	} {
		helperStart := strings.Index(text, tt.helper)
		if helperStart < 0 {
			t.Fatalf("%s settlement db snapshot helper missing", tt.name)
		}
		routineStart := strings.Index(text, tt.startFunc)
		if routineStart < 0 {
			t.Fatalf("%s routine missing", tt.name)
		}
		routineEnd := len(text) - routineStart
		if tt.endFunc != "" {
			routineEnd = strings.Index(text[routineStart:], tt.endFunc)
			if routineEnd < 0 {
				t.Fatalf("%s routine boundary missing", tt.name)
			}
		}
		block := text[routineStart : routineStart+routineEnd]
		combined := text[helperStart:] + block
		for _, want := range tt.markers {
			if !strings.Contains(combined, want) {
				t.Fatalf("%s settlement db snapshot guard missing %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s settlement still builds snapshot only from memory: %s", tt.name, unsafe)
			}
		}
	}
}

func TestRaceAndDicePanicLogsFormatGameIDs(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`log.Printf("⚠️ 骰子协程 panic，准备退款: dice_id=%s panic=%s", formatPlainValue(diceID), formatPlainValue(r))`,
		`log.Printf("⚠️ 赛马协程 panic，准备退款: race_id=%s panic=%s", formatPlainValue(raceID), formatPlainValue(r))`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("panic log should format dynamic game id: %s", want)
		}
	}
	for _, unsafe := range []string{
		`log.Printf("⚠️ 骰子协程 panic，准备退款: dice_id=%s panic=%s", diceID, formatPlainValue(r))`,
		`log.Printf("⚠️ 赛马协程 panic，准备退款: race_id=%s panic=%s", raceID, formatPlainValue(r))`,
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("panic log still uses raw game id: %s", unsafe)
		}
	}
}

func TestRaceAndDiceRefundReturnValuesOnlyAfterSuccess(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		forbidden string
	}{
		{
			name:      "race",
			startFunc: "func refundRaceBetsByRaceID(",
			endFunc:   "func recoverActiveRaceBetsOnStartup(",
			forbidden: "return refundCount, refundPoints, err",
		},
		{
			name:      "dice",
			startFunc: "func refundDiceBetsByDiceID(",
			endFunc:   "func recoverActiveDiceBetsOnStartup(",
			forbidden: "return refundCount, refundPoints, err",
		},
	}
	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s refund function missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endFunc)
		if end < 0 {
			t.Fatalf("%s refund function boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range []string{
			"txRefundCount := 0",
			"txRefundPoints := 0",
			"refundCount = txRefundCount",
			"refundPoints = txRefundPoints",
			"return 0, 0, err",
			"return refundCount, refundPoints, nil",
		} {
			if !strings.Contains(block, want) {
				t.Fatalf("%s refund return guard missing %q", tt.name, want)
			}
		}
		if strings.Contains(block, tt.forbidden) {
			t.Fatalf("%s refund still returns transactional intermediate values on error", tt.name)
		}
	}
}

func TestDiceDailyProfitWritesCheckRowsAffected(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func upsertDiceDailyProfitDeltaInTx(")
	if start < 0 {
		t.Fatal("upsertDiceDailyProfitDeltaInTx missing")
	}
	end := strings.Index(text[start:], "var GroupRaces sync.Map")
	if end < 0 {
		t.Fatal("dice daily profit helper boundary missing")
	}
	helperBlock := text[start : start+end]
	for _, want := range []string{
		"func upsertDiceDailyProfitDeltaInTx(tx *gorm.DB, userID int64, dayKey string, delta int) error",
		"func diceDailyProfitDeltaOnConflict(delta int) clause.OnConflict",
		"func createDiceDailyProfitInTx(tx *gorm.DB, stat *DiceDailyProfit) error",
		"func updateDiceDailyProfitDeltaInTx(tx *gorm.DB, statID uint, delta int) error",
		"res := tx.Clauses(diceDailyProfitDeltaOnConflict(delta))",
		"TargetWhere: clause.Where{Exprs: []clause.Expression{",
		`clause.Eq{Column: clause.Column{Name: "deleted_at"}, Value: nil}`,
		"res := tx.Create(stat)",
		"UpdateColumn(\"net_profit\", gorm.Expr(\"net_profit + ?\", delta))",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"DICE_DAILY_PROFIT_UPSERT_MISSED",
		"DICE_DAILY_PROFIT_CREATE_MISSED",
		"DICE_DAILY_PROFIT_UPDATE_MISSED",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("dice daily profit helper guard missing %q", want)
		}
	}

	routineStart := strings.Index(text, "func runDiceRoutine(")
	if routineStart < 0 {
		t.Fatal("runDiceRoutine missing")
	}
	routineEnd := strings.Index(text[routineStart:], "func handleHorseRace(")
	if routineEnd < 0 {
		t.Fatal("runDiceRoutine boundary missing")
	}
	routineBlock := text[routineStart : routineStart+routineEnd]
	for _, want := range []string{
		"upsertDiceDailyProfitDeltaInTx(tx, uid, dayKey, -bet.Points)",
		"createDiceDailyProfitInTx(tx, &stat)",
		"updateDiceDailyProfitDeltaInTx(tx, stat.ID, delta)",
	} {
		if !strings.Contains(routineBlock, want) {
			t.Fatalf("dice routine should use daily profit helper %q", want)
		}
	}
	for _, unsafe := range []string{
		"}).Create(&DiceDailyProfit{",
		"tx.Create(&stat).Error",
		"UpdateColumn(\"net_profit\", gorm.Expr(\"net_profit + ?\", delta)).Error",
	} {
		if strings.Contains(routineBlock, unsafe) {
			t.Fatalf("dice daily profit write still ignores RowsAffected: %s", unsafe)
		}
	}
}

func TestDiceDailyProfitOnConflictTargetsPartialUniqueIndex(t *testing.T) {
	onConflict := diceDailyProfitDeltaOnConflict(7)
	if len(onConflict.Columns) != 2 ||
		onConflict.Columns[0].Name != "user_id" ||
		onConflict.Columns[1].Name != "day_key" {
		t.Fatalf("dice daily profit upsert columns = %#v", onConflict.Columns)
	}
	if len(onConflict.TargetWhere.Exprs) != 1 {
		t.Fatalf("dice daily profit upsert target where = %#v", onConflict.TargetWhere.Exprs)
	}
	eq, ok := onConflict.TargetWhere.Exprs[0].(clause.Eq)
	if !ok {
		t.Fatalf("dice daily profit upsert target where should use clause.Eq, got %#v", onConflict.TargetWhere.Exprs[0])
	}
	col, ok := eq.Column.(clause.Column)
	if !ok || col.Name != "deleted_at" || eq.Value != nil {
		t.Fatalf("dice daily profit upsert target where should match deleted_at IS NULL, got %#v", eq)
	}
}

func TestDiceDailyProfitMigrationCreatesPartialUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("dice_daily_profits(user_id, day_key)"`)
	if start < 0 {
		t.Fatal("dice daily profit migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("item_usage_quotas(user_id, item_name, period_key)"`)
	if end < 0 {
		t.Fatal("dice daily profit migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"WHERE deleted_at IS NULL",
		"ensureDiceDailyProfitPartialUniqueIndex(DB)",
		"dice daily profit unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("dice daily profit migration missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureDiceDailyProfitPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureDiceDailyProfitPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureSectTechnologyUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("ensureDiceDailyProfitPartialUniqueIndex boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"FROM sqlite_master",
		"idx_dice_daily_profits_user_day_unique",
		"DROP INDEX IF EXISTS idx_dice_daily_profits_user_day_unique",
		"CREATE UNIQUE INDEX IF NOT EXISTS idx_dice_daily_profits_user_day_unique",
		"ON dice_daily_profits(user_id, day_key)",
		"WHERE deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("dice daily profit partial index helper missing %q", want)
		}
	}
}

func TestRaceWinningSettlementChecksLoserRowsAffected(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func runHorseRaceRoutine(")
	if start < 0 {
		t.Fatal("runHorseRaceRoutine missing")
	}
	block := text[start:]
	for _, required := range []string{
		"expectedWinnerCount",
		"expectedLoserCount",
		"claimedWinnerCount != expectedWinnerCount",
		"loserRes.RowsAffected != int64(expectedLoserCount)",
		"RACE_WINNER_SETTLEMENT_MISSED",
		"RACE_LOSER_SETTLEMENT_MISSED",
	} {
		if !strings.Contains(block, required) {
			t.Fatalf("loser settlement guard should contain %q: %s", required, block)
		}
	}
}

func TestDiceWinningSettlementChecksWinnerAndLoserRowsAffected(t *testing.T) {
	data, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func runDiceRoutine(")
	if start < 0 {
		t.Fatal("runDiceRoutine missing")
	}
	end := strings.Index(text[start:], "func handleHorseRace(")
	if end < 0 {
		t.Fatal("runDiceRoutine boundary missing")
	}
	block := text[start : start+end]
	for _, required := range []string{
		"expectedWinnerCount",
		"expectedLoserCount",
		"claimedLoserCount",
		"claimedWinnerCount != expectedWinnerCount",
		"claimedLoserCount != expectedLoserCount",
		"DICE_WINNER_SETTLEMENT_MISSED",
		"DICE_LOSER_SETTLEMENT_MISSED",
		"upsertDiceDailyProfitDeltaInTx(tx, uid, dayKey, -bet.Points)",
	} {
		if !strings.Contains(block, required) {
			t.Fatalf("dice winning settlement guard should contain %q: %s", required, block)
		}
	}
}
