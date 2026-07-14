package main

import (
	"os"
	"strings"
	"testing"
)

func TestRedPacketSuccessDisplayHandlesReadErrors(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleGrabRedPacket(")
	if start < 0 {
		t.Fatal("handleGrabRedPacket missing")
	}
	end := strings.Index(text[start:], "func backupDatabaseToTelegram(")
	if end < 0 {
		t.Fatal("handleGrabRedPacket boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"First(&u).Error",
		"红包领取后余额读取失败",
		"balanceText = \"`读取失败`\"",
		"Find(&grabs).Error",
		"红包抢空榜读取失败",
		"气运榜暂时读取失败",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("red packet success display read guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"DB.Where(\"telegram_id = ?\", userID).First(&u)\n",
		"DB.Where(\"packet_id = ?\", packet.ID).Order(\"points desc\").Find(&grabs)\n",
	} {
		if strings.Contains(block, unsafe) {
			t.Fatalf("red packet display still ignores DB errors: %s", unsafe)
		}
	}
}

func TestRedPacketStateReadErrorsAreReturned(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	start := strings.Index(text, "func userAlreadyGrabbedAllActiveRedPackets(")
	if start < 0 {
		t.Fatal("userAlreadyGrabbedAllActiveRedPackets missing")
	}
	end := strings.Index(text[start:], "func handleGrabRedPacket(")
	if end < 0 {
		t.Fatal("userAlreadyGrabbedAllActiveRedPackets boundary missing")
	}
	helperBlock := text[start : start+end]
	for _, want := range []string{
		"func userAlreadyGrabbedAllActiveRedPackets(userID int64, prefix string) (bool, error)",
		"return false, err",
		"return eligibleCount == 0, nil",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("red packet grabbed-all helper must return DB errors, missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"err != nil || activeCount == 0",
		"return err == nil && eligibleCount == 0",
	} {
		if strings.Contains(helperBlock, unsafe) {
			t.Fatalf("red packet grabbed-all helper still hides DB errors: %s", unsafe)
		}
	}

	start = strings.Index(text, "func hasActiveIneligibleWorldBossRedPacketTx(")
	if start < 0 {
		t.Fatal("hasActiveIneligibleWorldBossRedPacketTx missing")
	}
	end = strings.Index(text[start:], "func claimableRedPacketQuery(")
	if end < 0 {
		t.Fatal("hasActiveIneligibleWorldBossRedPacketTx boundary missing")
	}
	bossBlock := text[start : start+end]
	for _, want := range []string{
		"func hasActiveIneligibleWorldBossRedPacketTx(tx *gorm.DB, userID int64, prefix string) (bool, error)",
		"return false, err",
		"return count > 0, nil",
	} {
		if !strings.Contains(bossBlock, want) {
			t.Fatalf("red packet boss eligibility helper must return DB errors, missing %q", want)
		}
	}
	if strings.Contains(bossBlock, "return err == nil && count > 0") {
		t.Fatal("red packet boss eligibility helper still hides DB errors")
	}

	start = strings.Index(text, "func handleGrabRedPacket(")
	if start < 0 {
		t.Fatal("handleGrabRedPacket missing")
	}
	end = strings.Index(text[start:], "func backupDatabaseToTelegram(")
	if end < 0 {
		t.Fatal("handleGrabRedPacket boundary missing")
	}
	handleBlock := text[start : start+end]
	for _, want := range []string{
		"redPacketStateErr",
		"formatPlainError(redPacketStateErr)",
		"userAlreadyGrabbedAllActiveRedPackets(userID, prefix)",
		"hasActiveIneligibleWorldBossRedPacket(userID, prefix)",
	} {
		if !strings.Contains(handleBlock, want) {
			t.Fatalf("red packet handler state read error guard missing %q", want)
		}
	}
	if strings.Contains(handleBlock, "userAlreadyGrabbedAllActiveRedPackets(userID, prefix))") ||
		strings.Contains(handleBlock, "hasActiveIneligibleWorldBossRedPacket(userID, prefix)") &&
			!strings.Contains(handleBlock, "ineligibleWorldBossPacket, redPacketStateErr = hasActiveIneligibleWorldBossRedPacket(userID, prefix)") {
		t.Fatal("red packet handler still treats state read helpers as bool-only checks")
	}
}

func TestRedPacketGrabCreateChecksRowsAffected(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	helperStart := strings.Index(text, "func createRedPacketGrabInTx(")
	if helperStart < 0 {
		t.Fatal("createRedPacketGrabInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func grabRedPacketInTx(")
	if helperEnd < 0 {
		t.Fatal("createRedPacketGrabInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"entry := *grab",
		"entry.GrabberName = formatPlainValue(entry.GrabberName)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"RED_PACKET_GRAB_CREATE_MISSED",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("red packet grab helper guard missing %q", want)
		}
	}

	grabStart := strings.Index(text, "func grabRedPacketInTx(")
	if grabStart < 0 {
		t.Fatal("grabRedPacketInTx missing")
	}
	grabEnd := strings.Index(text[grabStart:], "func isRetryableRedPacketGrabError(")
	if grabEnd < 0 {
		t.Fatal("grabRedPacketInTx boundary missing")
	}
	grabBlock := text[grabStart : grabStart+grabEnd]
	if !strings.Contains(grabBlock, "createRedPacketGrabInTx(tx, &RedPacketGrab{") {
		t.Fatal("red packet grab transaction should use createRedPacketGrabInTx")
	}
	if strings.Contains(grabBlock, "tx.Create(&RedPacketGrab{") {
		t.Fatal("red packet grab transaction still creates grab record without RowsAffected guard")
	}
}

func TestRedPacketCreateChecksRowsAffected(t *testing.T) {
	stateSource, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	stateText := string(stateSource)

	helperStart := strings.Index(stateText, "func createRedPacketInTx(")
	if helperStart < 0 {
		t.Fatal("createRedPacketInTx missing")
	}
	helperEnd := strings.Index(stateText[helperStart:], "func grabRedPacketInTx(")
	if helperEnd < 0 {
		t.Fatal("createRedPacketInTx boundary missing")
	}
	helperBlock := stateText[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"entry := *packet",
		"entry.ID = formatPlainValue(entry.ID)",
		"entry.SenderName = formatPlainValue(entry.SenderName)",
		"entry.RefType = formatPlainValue(entry.RefType)",
		"entry.RefID = formatPlainValue(entry.RefID)",
		"entry.ClaimScope = formatPlainValue(entry.ClaimScope)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"RED_PACKET_CREATE_MISSED",
		"*packet = entry",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("red packet helper guard missing %q", want)
		}
	}

	countStart := strings.Index(stateText, `case "WAITING_RED_COUNT":`)
	if countStart < 0 {
		t.Fatal("WAITING_RED_COUNT branch missing")
	}
	countEnd := strings.Index(stateText[countStart:], `case "WAITING_PROMOTE_ID":`)
	if countEnd < 0 {
		t.Fatal("WAITING_RED_COUNT branch boundary missing")
	}
	countBlock := stateText[countStart : countStart+countEnd]
	for _, want := range []string{
		"packet := RedPacket{",
		"err := createRedPacketInTx(tx, &packet)",
		"isUniqueConstraintError(err)",
	} {
		if !strings.Contains(countBlock, want) {
			t.Fatalf("send red packet path missing helper behavior %q", want)
		}
	}
	if strings.Contains(countBlock, "tx.Create(&RedPacket{") {
		t.Fatal("send red packet path still creates packet without RowsAffected guard")
	}

	lotterySource, err := os.ReadFile("lottery.go")
	if err != nil {
		t.Fatalf("read lottery.go err = %v", err)
	}
	lotteryText := string(lotterySource)
	fusionFuncName := "addPointsTo" + "FusionPoolInTx"
	fusionStart := strings.Index(lotteryText, "func "+fusionFuncName+"(")
	if fusionStart < 0 {
		t.Fatal("addPointsToFusionPoolInTx missing")
	}
	fusionEnd := strings.Index(lotteryText[fusionStart:], "func claimLotteryPrizeByCode(")
	if fusionEnd < 0 {
		t.Fatal("addPointsToFusionPoolInTx boundary missing")
	}
	fusionBlock := lotteryText[fusionStart : fusionStart+fusionEnd]
	for _, want := range []string{
		"packet := RedPacket{",
		"createRedPacketInTx(tx, &packet)",
	} {
		if !strings.Contains(fusionBlock, want) {
			t.Fatalf("fusion pool burst red packet path missing helper %q", want)
		}
	}
	if strings.Contains(fusionBlock, "tx.Create(&RedPacket{") {
		t.Fatal("fusion pool burst red packet still creates packet without RowsAffected guard")
	}
}

func TestExchangeAndRedPacketTransactionResultsPublishAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)

	exchangeStart := strings.Index(text, `case "WAITING_EXCHANGE_CHOICE":`)
	if exchangeStart < 0 {
		t.Fatal("WAITING_EXCHANGE_CHOICE branch missing")
	}
	exchangeEnd := strings.Index(text[exchangeStart:], `case "WAITING_RED_POINTS":`)
	if exchangeEnd < 0 {
		t.Fatal("WAITING_EXCHANGE_CHOICE branch boundary missing")
	}
	exchangeBlock := text[exchangeStart : exchangeStart+exchangeEnd]
	for _, want := range []string{
		"var txCode string",
		"txCode = \"\"",
		"txCode = candidateCode",
		"if txCode == \"\"",
		"if err == nil {\n\t\t\t\tcode = txCode\n\t\t\t}",
	} {
		if !strings.Contains(exchangeBlock, want) {
			t.Fatalf("exchange transaction result publication guard missing %q", want)
		}
	}
	if strings.Count(exchangeBlock, "if err == nil {\n\t\t\t\tcode = txCode\n\t\t\t}") != 2 {
		t.Fatal("invite and renew exchange codes must publish only after transaction success")
	}
	for _, unsafe := range []string{
		"code = candidateCode",
		"if code == \"\"",
	} {
		if strings.Contains(exchangeBlock, unsafe) {
			t.Fatalf("exchange transaction still publishes/checks outer code inside transaction: %q", unsafe)
		}
	}

	redStart := strings.Index(text, `case "WAITING_RED_COUNT":`)
	if redStart < 0 {
		t.Fatal("WAITING_RED_COUNT branch missing")
	}
	redEnd := strings.Index(text[redStart:], `case "WAITING_PROMOTE_ID":`)
	if redEnd < 0 {
		t.Fatal("WAITING_RED_COUNT branch boundary missing")
	}
	redBlock := text[redStart : redStart+redEnd]
	for _, want := range []string{
		"txRedID := \"\"",
		"txRedID = candidateID",
		"if txRedID == \"\"",
		"\"redpacket\",\n\t\t\t\ttxRedID",
	} {
		if !strings.Contains(redBlock, want) {
			t.Fatalf("red packet transaction id guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"var redID string",
		"redID = candidateID",
		"\"redpacket\",\n\t\t\t\tredID",
	} {
		if strings.Contains(redBlock, unsafe) {
			t.Fatalf("red packet transaction still uses outer id state: %q", unsafe)
		}
	}
}

func TestRedPacketSendWalletReadErrorDoesNotLookInsufficient(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "WAITING_RED_POINTS":`)
	if start < 0 {
		t.Fatal("WAITING_RED_POINTS branch missing")
	}
	end := strings.Index(text[start:], `case "WAITING_RED_COUNT":`)
	if end < 0 {
		t.Fatal("WAITING_RED_POINTS branch boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"发红包前钱包读取失败",
		"First(&u).Error",
		"钱包读取失败，请稍后重新输入红包金额",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("red packet wallet read guard missing %q", want)
		}
	}
	if strings.Contains(block, "DB.Where(\"telegram_id = ?\", userID).First(&u)\n") ||
		strings.Contains(block, "DB.Where(\"telegram_id = ?\", userID).First(&u)\r\n") {
		t.Fatal("red packet amount branch still treats wallet read errors as zero balance")
	}
}

func TestRedPacketCountStepChecksSessionAmountParseError(t *testing.T) {
	source, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, `case "WAITING_RED_COUNT":`)
	if start < 0 {
		t.Fatal("WAITING_RED_COUNT branch missing")
	}
	end := strings.Index(text[start:], `case "WAITING_PROMOTE_ID":`)
	if end < 0 {
		t.Fatal("WAITING_RED_COUNT branch boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`pts, err := strconv.Atoi(session.GetTemp("red_points"))`,
		`if err != nil || pts < 10`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("red packet count step session amount parse guard missing %q", want)
		}
	}
	if strings.Contains(block, `pts, _ := strconv.Atoi(session.GetTemp("red_points"))`) {
		t.Fatal("red packet count step still ignores session amount parse error")
	}
}
