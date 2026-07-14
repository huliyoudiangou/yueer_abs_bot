package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestCalculateSectWeeklyTaskRewardConservativeExcess(t *testing.T) {
	reward := calculateSectWeeklyTaskReward(219, 408, 86)
	if reward.AchievedCount != 3 {
		t.Fatalf("achieved = %d, want 3", reward.AchievedCount)
	}
	if reward.ExcessPercentTotal != 266 {
		t.Fatalf("excess percent = %d, want 266", reward.ExcessPercentTotal)
	}
	if reward.BaseFunds != 250 || reward.BasePrestige != 100 || reward.ExcessFunds != 212 || reward.ExcessPrestige != 79 {
		t.Fatalf("reward split = base %d/%d excess %d/%d, want base 250/100 excess 212/79",
			reward.BaseFunds, reward.BasePrestige, reward.ExcessFunds, reward.ExcessPrestige)
	}
	if reward.Funds != 462 || reward.Prestige != 179 {
		t.Fatalf("reward = funds %d prestige %d, want funds 462 prestige 179", reward.Funds, reward.Prestige)
	}
}

func TestCalculateSectWeeklyTaskRewardBaseTiers(t *testing.T) {
	tests := []struct {
		name          string
		signCount     int64
		listenHours   float64
		taskCount     int64
		achieved      int
		funds         int
		prestige      int
		excessPercent int
	}{
		{name: "none", signCount: 99, listenHours: 199.9, taskCount: 59, achieved: 0, funds: 0, prestige: 0, excessPercent: 0},
		{name: "one", signCount: 100, listenHours: 0, taskCount: 0, achieved: 1, funds: 50, prestige: 20, excessPercent: 0},
		{name: "two", signCount: 100, listenHours: 200, taskCount: 0, achieved: 2, funds: 120, prestige: 50, excessPercent: 0},
		{name: "three", signCount: 100, listenHours: 200, taskCount: 60, achieved: 3, funds: 250, prestige: 100, excessPercent: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reward := calculateSectWeeklyTaskReward(tt.signCount, tt.listenHours, tt.taskCount)
			if reward.AchievedCount != tt.achieved || reward.Funds != tt.funds || reward.Prestige != tt.prestige || reward.ExcessPercentTotal != tt.excessPercent {
				t.Fatalf("reward = achieved %d funds %d prestige %d excess %d, want achieved %d funds %d prestige %d excess %d",
					reward.AchievedCount, reward.Funds, reward.Prestige, reward.ExcessPercentTotal,
					tt.achieved, tt.funds, tt.prestige, tt.excessPercent)
			}
		})
	}
}

func TestCalculateSectWeeklyTaskRewardExcessCap(t *testing.T) {
	reward := calculateSectWeeklyTaskReward(400, 800, 240)
	if reward.ExcessPercentTotal != 600 {
		t.Fatalf("excess percent = %d, want 600", reward.ExcessPercentTotal)
	}
	if reward.Funds != 730 || reward.Prestige != 280 {
		t.Fatalf("reward = funds %d prestige %d, want funds 730 prestige 280", reward.Funds, reward.Prestige)
	}
}

func TestSectWeekKeyUsesBeijingMonday(t *testing.T) {
	tm := time.Date(2026, 6, 13, 23, 30, 0, 0, time.FixedZone("CST", 8*3600))
	if got := sectWeekKey(tm); got != "2026-06-08" {
		t.Fatalf("week key = %s, want 2026-06-08", got)
	}
}

func TestSectWeeklyTaskAutoSettlementTargetWeek(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	before := time.Date(2026, 6, 15, 8, 59, 0, 0, loc)
	if _, due := sectWeeklyTaskAutoSettlementTargetWeek(before); due {
		t.Fatal("auto settlement should not be due before Monday 09:00")
	}

	mondayNine := time.Date(2026, 6, 15, 9, 0, 0, 0, loc)
	targetWeek, due := sectWeeklyTaskAutoSettlementTargetWeek(mondayNine)
	if !due {
		t.Fatal("auto settlement should be due at Monday 09:00")
	}
	if got := targetWeek.Format("2006-01-02"); got != "2026-06-08" {
		t.Fatalf("target week = %s, want 2026-06-08", got)
	}

	tuesday := time.Date(2026, 6, 16, 9, 0, 0, 0, loc)
	if _, due := sectWeeklyTaskAutoSettlementTargetWeek(tuesday); due {
		t.Fatal("auto settlement should only run on Monday")
	}
}

func TestSectWeeklyTaskSettlementChecksRewardRowsAffected(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	helperStart := strings.Index(text, "func createSectWeeklyTaskSettlementInTx(")
	if helperStart < 0 {
		t.Fatal("createSectWeeklyTaskSettlementInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "type SectListeningDailyProgress struct")
	if helperEnd < 0 {
		t.Fatal("createSectWeeklyTaskSettlementInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"settlement.WeekKey = formatPlainValue(settlement.WeekKey)",
		"settlement.SettledByName = formatPlainValue(settlement.SettledByName)",
		"res := tx.Create(settlement)",
		"res.Error != nil",
		"isUniqueConstraintError(res.Error)",
		"errSectWeeklyTaskAlreadySettled",
		"res.RowsAffected == 0",
		"SECT_WEEKLY_TASK_SETTLEMENT_CREATE_MISSED",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("weekly task settlement helper guard missing %q", want)
		}
	}

	start := strings.Index(text, "func settleSectWeeklyTaskRewardForSectTx(")
	if start < 0 {
		t.Fatal("settleSectWeeklyTaskRewardForSectTx missing")
	}
	end := strings.Index(text[start:], "func formatSectWeeklyTaskSettlementReason(")
	if end < 0 {
		t.Fatal("settleSectWeeklyTaskRewardForSectTx boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"weeklyRewardRes := tx.Model(&Sect{})",
		"weeklyRewardRes.RowsAffected == 0",
		"SECT_WEEKLY_TASK_REWARD_UPDATE_MISSED",
		"createSectWeeklyTaskSettlementInTx(tx, &settlement)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("weekly task reward rows affected guard missing %q", want)
		}
	}
	if strings.Contains(block, `Updates(map[string]interface{}{
		"funds":    gorm.Expr("funds + ?", reward.Funds),
		"prestige": gorm.Expr("prestige + ?", reward.Prestige),
	}).Error`) {
		t.Fatal("weekly task reward still ignores RowsAffected")
	}
	if strings.Contains(block, "tx.Create(&settlement).Error") {
		t.Fatal("weekly task settlement create still ignores RowsAffected")
	}
}

func TestSectWeeklyTaskSettlementReturnValueOnlyAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("sect.go")
	if err != nil {
		t.Fatalf("read sect.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func settleSectWeeklyTaskReward(")
	if start < 0 {
		t.Fatal("settleSectWeeklyTaskReward missing")
	}
	end := strings.Index(text[start:], "func sectWeeklyTaskManualSettlementTargetTx(")
	if end < 0 {
		t.Fatal("settleSectWeeklyTaskReward boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"var txResult sectWeeklyTaskSettlementResult",
		"settleSectWeeklyTaskRewardForSectTx(tx, member.SectID, targetTime, actorID, actorName, &txResult)",
		"result = txResult",
		"return sectWeeklyTaskSettlementResult{}, err",
		"return result, nil",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("weekly task settlement return guard missing %q", want)
		}
	}
	if strings.Contains(block, "return result, err") {
		t.Fatal("weekly task settlement still returns transactional intermediate result on error")
	}
}
