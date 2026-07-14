package main

import (
	"os"
	"strings"
	"testing"
)

func TestSectCaveSectRetreatCost(t *testing.T) {
	tests := []struct {
		level int
		want  int
	}{
		{level: 0, want: 70},
		{level: 1, want: 70},
		{level: 4, want: 100},
		{level: 10, want: 160},
	}

	for _, tt := range tests {
		if got := sectCaveSectRetreatCost(tt.level); got != tt.want {
			t.Fatalf("sectCaveSectRetreatCost(%d) = %d, want %d", tt.level, got, tt.want)
		}
	}
}

func TestCalculateSectEffectiveHoursWithRetreat(t *testing.T) {
	tests := []struct {
		name           string
		rawHours       float64
		retreatHours   float64
		effectiveHours float64
	}{
		{name: "no retreat keeps normal pressure", rawHours: 10, retreatHours: 0, effectiveHours: 5.3},
		{name: "first four hours unchanged", rawHours: 3, retreatHours: 2, effectiveHours: 3.0},
		{name: "four to eight improves to sixty percent", rawHours: 6, retreatHours: 2, effectiveHours: 5.2},
		{name: "after eight improves to fifteen percent", rawHours: 10, retreatHours: 2, effectiveHours: 5.5},
		{name: "retreat capped by raw hours", rawHours: 5, retreatHours: 8, effectiveHours: 4.6},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateSectEffectiveHoursFromSecondsWithRetreat(tt.rawHours*3600, tt.retreatHours*3600)
			if !floatEquals(got, tt.effectiveHours, 0.0001) {
				t.Fatalf("effective = %.4f, want %.4f", got, tt.effectiveHours)
			}
		})
	}
}

func TestCalculateSectRetreatBonusHours(t *testing.T) {
	tests := []struct {
		name string
		raw  float64
		base float64
		want float64
	}{
		{name: "no new listening", raw: 6, base: 6, want: 0},
		{name: "new listening in first four hours has no bonus", raw: 4, base: 2, want: 0},
		{name: "new listening in pressure band", raw: 6, base: 4, want: 0.6},
		{name: "new listening after eight hours", raw: 10, base: 8, want: 0.2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateSectRetreatBonusHours(tt.raw*3600, tt.base*3600)
			if !floatEquals(got, tt.want, 0.0001) {
				t.Fatalf("bonus = %.4f, want %.4f", got, tt.want)
			}
		})
	}
}

func TestCalculateSectRetreatBonusHoursForRecordCapsDuration(t *testing.T) {
	got := calculateSectRetreatBonusHoursForRecord(12*3600, 8*3600, 2*3600)
	if !floatEquals(got, 0.2, 0.0001) {
		t.Fatalf("bonus = %.4f, want 0.2", got)
	}
}

func TestGetActiveSectCaveRetreatTxReturnsDatabaseErrors(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"func getActiveSectCaveRetreatTx(tx *gorm.DB, userID int64, now time.Time) (SectCaveRetreat, bool, error)",
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"return SectCaveRetreat{}, false, err",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("active retreat helper must expose database errors; missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"if retreat, ok := getActiveSectCaveRetreatTx",
		"if _, ok := getActiveSectCaveRetreatTx",
		"retreat, ok := getActiveSectCaveRetreatTx",
	} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("active retreat caller ignores database error: %s", unsafe)
		}
	}
}

func TestCreateSectCaveRetreatInTxChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	helperStart := strings.Index(text, "func createSectCaveRetreatInTx(")
	if helperStart < 0 {
		t.Fatal("createSectCaveRetreatInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func (SectContributionLog) TableName()")
	if helperEnd < 0 {
		t.Fatal("createSectCaveRetreatInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"retreat.Mode = formatPlainValue(retreat.Mode)",
		"retreat.Status = formatPlainValue(retreat.Status)",
		"retreat.StartedByName = formatPlainValue(retreat.StartedByName)",
		"res := tx.Create(retreat)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"SECT_CAVE_RETREAT_CREATE_MISSED",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect cave retreat helper guard missing %q", want)
		}
	}

	personalStart := strings.Index(text, "func handleStartPersonalSectCaveRetreat(")
	if personalStart < 0 {
		t.Fatal("handleStartPersonalSectCaveRetreat missing")
	}
	personalEnd := strings.Index(text[personalStart:], "func handleStartSectCaveRetreat(")
	if personalEnd < 0 {
		t.Fatal("handleStartPersonalSectCaveRetreat boundary missing")
	}
	personalBlock := text[personalStart : personalStart+personalEnd]
	if !strings.Contains(personalBlock, "createSectCaveRetreatInTx(tx, &SectCaveRetreat{") {
		t.Fatal("personal sect cave retreat should use createSectCaveRetreatInTx")
	}

	sectStart := strings.Index(text, "func handleStartSectCaveRetreat(")
	if sectStart < 0 {
		t.Fatal("handleStartSectCaveRetreat missing")
	}
	sectEnd := strings.Index(text[sectStart:], "func awardSectContributionTx(")
	if sectEnd < 0 {
		t.Fatal("handleStartSectCaveRetreat boundary missing")
	}
	sectBlock := text[sectStart : sectStart+sectEnd]
	if !strings.Contains(sectBlock, "createSectCaveRetreatInTx(tx, &SectCaveRetreat{") {
		t.Fatal("sect cave group retreat should use createSectCaveRetreatInTx")
	}

	if strings.Contains(personalBlock, "tx.Create(&SectCaveRetreat{") || strings.Contains(sectBlock, "tx.Create(&SectCaveRetreat{") {
		t.Fatal("sect cave retreat create still ignores RowsAffected")
	}
}

func TestSectCaveActiveRetreatReadFailuresAreHandled(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		"sect cave active retreat read failed",
		"sect cave personal active retreat read failed",
		"sect cave member active retreat read failed",
		"闭关状态读取失败",
		"读取失败",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("active retreat read failure should be visible; missing %q", want)
		}
	}
}

func TestSectCaveRetreatMigrationReplacesFullUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("sect_cave_retreats(active user)"`)
	if start < 0 {
		t.Fatal("sect cave retreat migration block missing")
	}
	end := strings.Index(text[start:], `if installed, err := ensureMarketplaceOpenDisputeUniqueIndex(DB)`)
	if end < 0 {
		t.Fatal("sect cave retreat migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM sect_cave_retreats",
		"WHERE deleted_at IS NULL AND status = 'active'",
		"ensureSectCaveRetreatActivePartialUniqueIndex(DB)",
		"sect cave retreat unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("sect cave retreat migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureSectCaveRetreatActivePartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureSectCaveRetreatActivePartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureInventoryPartialUniqueIndex(")
	if helperEnd < 0 {
		t.Fatal("sect cave retreat partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_sect_cave_retreats_active_user_unique",
		"ON sect_cave_retreats(user_id)",
		"WHERE deleted_at IS NULL AND status = 'active'",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("sect cave retreat partial index helper missing %q", want)
		}
	}
}

func TestSectCaveDailyListeningDiagnosticsAreReadable(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)

	activeStart := strings.Index(text, "func activeSectCaveRetreatBonusHours(")
	if activeStart < 0 {
		t.Fatal("activeSectCaveRetreatBonusHours missing")
	}
	activeEnd := strings.Index(text[activeStart:], "func cappedListeningSeconds(")
	if activeEnd < 0 {
		t.Fatal("activeSectCaveRetreatBonusHours boundary missing")
	}
	activeBlock := text[activeStart : activeStart+activeEnd]
	for _, want := range []string{
		"宗门洞府闭关过期关闭失败",
		"宗门洞府闭关记录读取失败",
		"formatPlainError(err)",
	} {
		if !strings.Contains(activeBlock, want) {
			t.Fatalf("sect cave active bonus diagnostic guard missing %q", want)
		}
	}

	effectiveStart := strings.Index(text, "func sumDailyListeningEffectiveHours(")
	if effectiveStart < 0 {
		t.Fatal("sumDailyListeningEffectiveHours missing")
	}
	effectiveEnd := strings.Index(text[effectiveStart:], "func sumDailyListeningRawSeconds(")
	if effectiveEnd < 0 {
		t.Fatal("sumDailyListeningEffectiveHours boundary missing")
	}
	effectiveBlock := text[effectiveStart : effectiveStart+effectiveEnd]
	if !strings.Contains(effectiveBlock, "每日听书有效时长汇总失败") || !strings.Contains(effectiveBlock, "formatPlainError(err)") {
		t.Fatal("daily listening effective hours diagnostic guard missing")
	}

	rawStart := strings.Index(text, "func sumDailyListeningRawSeconds(")
	if rawStart < 0 {
		t.Fatal("sumDailyListeningRawSeconds missing")
	}
	rawEnd := strings.Index(text[rawStart:], "func refreshDailyListeningStatsFromABS(")
	if rawEnd < 0 {
		t.Fatal("sumDailyListeningRawSeconds boundary missing")
	}
	rawBlock := text[rawStart : rawStart+rawEnd]
	if !strings.Contains(rawBlock, "每日听书原始秒数汇总失败") || !strings.Contains(rawBlock, "formatPlainError(err)") {
		t.Fatal("daily listening raw seconds diagnostic guard missing")
	}

	for name, block := range map[string]string{
		"activeSectCaveRetreatBonusHours": activeBlock,
		"sumDailyListeningEffectiveHours": effectiveBlock,
		"sumDailyListeningRawSeconds":     rawBlock,
	} {
		if strings.Contains(block, "log.Printf(\"\u95c1") || strings.Contains(block, "log.Printf(\"\u95c2") {
			t.Fatalf("%s diagnostics contain mojibake", name)
		}
	}
}

func floatEquals(a float64, b float64, tolerance float64) bool {
	if a > b {
		return a-b <= tolerance
	}
	return b-a <= tolerance
}
