package main

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestMarketplaceSellerGroupsMatchListing(t *testing.T) {
	tests := []struct {
		name            string
		listingSellerID int64
		groups          []marketplaceSellerGroup
		want            bool
	}{
		{
			name:            "matching single seller",
			listingSellerID: 1001,
			groups:          []marketplaceSellerGroup{{SellerID: 1001, Count: 2}},
			want:            true,
		},
		{
			name:            "different seller",
			listingSellerID: 1001,
			groups:          []marketplaceSellerGroup{{SellerID: 2002, Count: 2}},
			want:            false,
		},
		{
			name:            "multiple secret sellers",
			listingSellerID: 1001,
			groups: []marketplaceSellerGroup{
				{SellerID: 1001, Count: 1},
				{SellerID: 2002, Count: 1},
			},
			want: false,
		},
		{
			name:            "no secrets",
			listingSellerID: 1001,
			groups:          nil,
			want:            false,
		},
		{
			name:            "zero listing seller",
			listingSellerID: 0,
			groups:          []marketplaceSellerGroup{{SellerID: 1001, Count: 2}},
			want:            false,
		},
		{
			name:            "zero secret count",
			listingSellerID: 1001,
			groups:          []marketplaceSellerGroup{{SellerID: 1001, Count: 0}},
			want:            false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := marketplaceSellerGroupsMatchListing(tt.listingSellerID, tt.groups); got != tt.want {
				t.Fatalf("marketplaceSellerGroupsMatchListing() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestMarketplaceOriginalValueFloor(t *testing.T) {
	minTests := []struct {
		original int
		want     int
	}{
		{original: 0, want: 0},
		{original: 1, want: 1},
		{original: 100, want: 85},
		{original: 150, want: 128},
		{original: 300, want: 255},
	}
	for _, tt := range minTests {
		if got := marketplaceMinOriginalValuePrice(tt.original); got != tt.want {
			t.Fatalf("marketplaceMinOriginalValuePrice(%d) = %d, want %d", tt.original, got, tt.want)
		}
	}

	maxTests := []struct {
		original int
		want     int
	}{
		{original: 0, want: 0},
		{original: 1, want: 1},
		{original: 15, want: 17},
		{original: 100, want: 115},
		{original: 300, want: 345},
	}
	for _, tt := range maxTests {
		if got := marketplaceMaxOriginalValuePrice(tt.original); got != tt.want {
			t.Fatalf("marketplaceMaxOriginalValuePrice(%d) = %d, want %d", tt.original, got, tt.want)
		}
	}

	if original, ok := marketplaceInventoryOriginalValue("聚灵丹"); !ok || original != 120 {
		t.Fatalf("marketplaceInventoryOriginalValue(聚灵丹) = %d,%v want 120,true", original, ok)
	}
	if original, ok := marketplaceInventoryOriginalValue("凝露草种子"); !ok || original != 15 {
		t.Fatalf("marketplaceInventoryOriginalValue(凝露草种子) = %d,%v want 15,true", original, ok)
	}
}

func TestMarketplacePointExchangeRenewGuards(t *testing.T) {
	marketplaceSource, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go err = %v", err)
	}
	marketplaceText := string(marketplaceSource)
	for _, want := range []string{
		"marketplaceMinOriginalValuePercent",
		"marketplaceMaxOriginalValuePercent",
		"classifyMarketplaceSecretTx(tx, codeHash, sellerID)",
		"renew.Source == renewCodeSourcePointExchange",
		"renew.OwnerUserID != sellerID",
		"errMarketplaceSecretOwnerMismatch",
		"marketplaceSecretListingMinPriceTx",
		"marketplaceSecretListingMaxPriceTx",
		"errMarketplacePriceBelowFloor",
		"errMarketplacePriceAboveCeiling",
		"transferMarketplaceRenewCodeOwnerInTx(tx, secret, buyerID)",
		`Update("owner_user_id", buyerID)`,
	} {
		if !strings.Contains(marketplaceText, want) {
			t.Fatalf("marketplace point-exchange renew guard missing %q", want)
		}
	}

	stateSource, err := os.ReadFile("state_machine.go")
	if err != nil {
		t.Fatalf("read state_machine.go err = %v", err)
	}
	stateText := string(stateSource)
	for _, want := range []string{
		`const renewCodeSourcePointExchange = "point_exchange"`,
		"createRenewCodeRecordWithMeta(tx, candidateCode, renewDays, renewCodeSourcePointExchange, userID)",
		"rCode.Source == renewCodeSourcePointExchange",
		"rCode.OwnerUserID != userID",
		"errRenewCodeOwnerMismatch",
		`case "RENEW_CODE_OWNER_MISMATCH":`,
	} {
		if !strings.Contains(stateText, want) {
			t.Fatalf("renew code owner guard missing %q", want)
		}
	}

	dbSource, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go err = %v", err)
	}
	dbText := string(dbSource)
	for _, want := range []string{
		"OwnerUserID int64",
		"Source      string",
	} {
		if !strings.Contains(dbText, want) {
			t.Fatalf("renew code model provenance field missing %q", want)
		}
	}
}

func TestMarketplaceFeeUsesFivePercent(t *testing.T) {
	tests := []struct {
		amount int
		want   int
	}{
		{amount: 0, want: 0},
		{amount: 1, want: 0},
		{amount: 10, want: 1},
		{amount: 100, want: 5},
		{amount: 240, want: 12},
	}
	for _, tt := range tests {
		if got := calculateMarketplaceFee(tt.amount); got != tt.want {
			t.Fatalf("calculateMarketplaceFee(%d) = %d, want %d", tt.amount, got, tt.want)
		}
	}
}

func TestMarketplaceReviewStatusTextAndAction(t *testing.T) {
	listing := MarketplaceListing{
		Status:      marketplaceStatusReview,
		ListingType: marketplaceTypeInventory,
	}

	if got := marketplaceStatusText(marketplaceStatusReview); got != "异常待核查" {
		t.Fatalf("marketplaceStatusText(review) = %q", got)
	}
	if got := marketplaceDetailActionText(listing, 1); got != "该商品数据异常，已暂停交易并等待管理员核查。" {
		t.Fatalf("marketplaceDetailActionText(review) = %q", got)
	}

	listing.Status = marketplaceStatusActive
	if got := marketplaceDetailActionTextWithStockStatus(listing, 1, false); got != "库存状态暂不可用，请稍后再试。" {
		t.Fatalf("marketplaceDetailActionTextWithStockStatus(unavailable) = %q", got)
	}
}

func TestMarketplaceStockText(t *testing.T) {
	if got := marketplaceStockText(5, true); got != "5" {
		t.Fatalf("marketplaceStockText(available) = %q", got)
	}
	if got := marketplaceStockText(0, false); got != "读取失败" {
		t.Fatalf("marketplaceStockText(unavailable) = %q", got)
	}
}

func TestMarketplaceSessionIntParserFailsClosed(t *testing.T) {
	session := &SessionState{}

	session.SetTemp("market_buy_quantity", "bad")
	if _, err := parseMarketplaceSessionInt(session, "market_buy_quantity", 1, marketplaceMaxBuyQuantity); err == nil {
		t.Fatal("invalid marketplace buy quantity should fail closed")
	}

	session.SetTemp("market_buy_quantity", "0")
	if _, err := parseMarketplaceSessionInt(session, "market_buy_quantity", 1, marketplaceMaxBuyQuantity); err == nil {
		t.Fatal("out-of-range marketplace buy quantity should fail closed")
	}

	session.SetTemp("market_buy_quantity", "2")
	if got, err := parseMarketplaceSessionInt(session, "market_buy_quantity", 1, marketplaceMaxBuyQuantity); err != nil || got != 2 {
		t.Fatalf("valid marketplace buy quantity parse failed: got=%d err=%v", got, err)
	}

	session.SetTemp("market_price", "100001")
	if _, err := parseMarketplaceSessionInt(session, "market_price", marketplaceMinUnitPrice, marketplaceMaxPrice); err == nil {
		t.Fatal("out-of-range marketplace price should fail closed")
	}
}

func TestMarketplaceSessionValuesDoNotIgnoreParseErrors(t *testing.T) {
	source, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go err = %v", err)
	}
	text := string(source)
	start := strings.Index(text, "func handleMarketplaceStep(")
	if start < 0 {
		t.Fatal("handleMarketplaceStep missing")
	}
	end := strings.Index(text[start:], "func parseMarketplaceSecrets(")
	if end < 0 {
		t.Fatal("handleMarketplaceStep boundary missing")
	}
	fn := text[start : start+end]
	for _, want := range []string{
		`parseMarketplaceSessionInt(session, "market_buy_quantity", 1, marketplaceMaxBuyQuantity)`,
		`parseMarketplaceSessionInt(session, "market_inventory_quantity", 1, marketplaceMaxInventoryUnits)`,
		`parseMarketplaceSessionInt(session, "market_price", marketplaceMinUnitPrice, marketplaceMaxPrice)`,
	} {
		if !strings.Contains(fn, want) {
			t.Fatalf("marketplace session parse guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		`qty, _ := strconv.Atoi(session.GetTemp("market_buy_quantity"))`,
		`qty, _ := strconv.Atoi(session.GetTemp("market_inventory_quantity"))`,
		`price, _ := strconv.Atoi(session.GetTemp("market_price"))`,
	} {
		if strings.Contains(fn, unsafe) {
			t.Fatalf("marketplace session value still ignores parse errors: %q", unsafe)
		}
	}
}

func TestMarketplaceBuyConfirmTextIncludesPillEffect(t *testing.T) {
	text, err := marketplaceBuyConfirmText(MarketplaceListing{
		Name:        "九转造化丹",
		ListingType: marketplaceTypeInventory,
		Price:       350,
		SellerID:    1001,
	}, 1, 3, 2002)
	if err != nil {
		t.Fatalf("marketplaceBuyConfirmText() err = %v", err)
	}
	if !marketplaceTestContainsAll(text, []string{"功效：", "增加 3.0 小时丹药修为", "确认购买商品"}) {
		t.Fatalf("marketplace buy confirm text missing pill effect: %s", text)
	}
}

func TestMarketplaceDealNoticesIncludePillEffect(t *testing.T) {
	result := marketplacePurchaseResult{
		Listing: MarketplaceListing{
			Name:        "聚灵丹",
			ListingType: marketplaceTypeInventory,
		},
		PurchaseIDs:  []uint{8},
		Quantity:     2,
		GrossAmount:  240,
		FeeAmount:    7,
		SellerAmount: 233,
	}

	buyerText := marketplaceInventoryPurchaseSuccessText(result)
	if !marketplaceTestContainsAll(buyerText, []string{"功效：", "本周最多 3 颗", "物品已放入乾坤袋"}) {
		t.Fatalf("inventory purchase success text missing pill effect: %s", buyerText)
	}

	sellerText := marketplaceSellerDealNoticeText(result)
	if !marketplaceTestContainsAll(sellerText, []string{"功效：", "本周最多 3 颗", "实收：233 积分"}) {
		t.Fatalf("seller deal notice text missing pill effect: %s", sellerText)
	}
}

func TestMarketplacePillEffectOnlyForInventoryListings(t *testing.T) {
	if got := marketplaceListingPillEffectLine(marketplaceTypeInventory, "聚灵丹"); !strings.Contains(got, "功效：") {
		t.Fatalf("inventory pill effect line = %q, want effect", got)
	}
	if got := marketplaceListingPillEffectLine(marketplaceTypeSecret, "聚灵丹"); got != "" {
		t.Fatalf("secret listing pill effect line = %q, want empty", got)
	}

	secretNotice := formatMarketplaceListingGroupNotice(9, marketplaceTypeSecret, "聚灵丹", 10, 1, "seller", marketplaceSecretSourceBotInvite)
	if strings.Contains(secretNotice, "功效：") {
		t.Fatalf("secret group notice should not include pill effect: %s", secretNotice)
	}
	if !strings.Contains(secretNotice, "系统已校验") {
		t.Fatalf("secret group notice should include verified label: %s", secretNotice)
	}

	confirmText, err := marketplaceBuyConfirmText(MarketplaceListing{
		Name:         "聚灵丹",
		ListingType:  marketplaceTypeSecret,
		SecretSource: marketplaceSecretSourceBotRenew,
		Price:        10,
		SellerID:     1001,
	}, 1, 1, 2002)
	if err != nil {
		t.Fatalf("marketplaceBuyConfirmText(secret) err = %v", err)
	}
	if strings.Contains(confirmText, "功效：") {
		t.Fatalf("secret buy confirm should not include pill effect: %s", confirmText)
	}
	if !strings.Contains(confirmText, "系统已校验") || !strings.Contains(confirmText, "续期卡") {
		t.Fatalf("secret buy confirm should include verification label: %s", confirmText)
	}
}

func TestMarketplaceListingNoticeLogsSecretSourceReadErrors(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func notifyMarketplaceListingCreated(")
	if start < 0 {
		t.Fatal("notifyMarketplaceListingCreated missing")
	}
	end := strings.Index(text[start:], "func formatMarketplaceListingGroupNotice(")
	if end < 0 {
		t.Fatal("notifyMarketplaceListingCreated boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		`DB.Select("secret_source").Where("id = ?", listingID).First(&listing).Error`,
		"交易行上架群提醒读取卡密来源失败",
		"formatPlainError(err)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace listing notice source read guard missing %q", want)
		}
	}
	logIdx := strings.Index(block, "交易行上架群提醒读取卡密来源失败")
	if logIdx < 0 {
		t.Fatal("marketplace listing notice source read failure log missing")
	}
	failureBlock := block[logIdx:minInt(len(block), logIdx+240)]
	if !strings.Contains(failureBlock, "return") {
		t.Fatalf("marketplace listing notice should not send group notice when secret source read fails: %s", failureBlock)
	}
}

func TestMarketplaceTransactionalReturnValuesOnlyAfterSuccess(t *testing.T) {
	source, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go err = %v", err)
	}
	text := string(source)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		wants     []string
		forbidden []string
	}{
		{
			name:      "secret listing",
			startFunc: "func createMarketplaceSecretListing(",
			endFunc:   "func normalizeMarketplaceSecrets(",
			wants: []string{
				"var txListingID uint",
				"txListingID = listing.ID",
				"listingID = txListingID",
				"if err != nil {\n\t\treturn 0, err\n\t}",
				"return listingID, nil",
			},
			forbidden: []string{"return listingID, err"},
		},
		{
			name:      "inventory listing",
			startFunc: "func createMarketplaceInventoryListing(",
			endFunc:   "func sendMarketplaceInventoryChoices(",
			wants: []string{
				"var txListingID uint",
				"txListingID = listing.ID",
				"listingID = txListingID",
				"if err != nil {\n\t\treturn 0, err\n\t}",
				"return listingID, nil",
			},
			forbidden: []string{"return listingID, err"},
		},
		{
			name:      "close listing",
			startFunc: "func closeMarketplaceListingScoped(",
			endFunc:   "func StartMarketplaceExpiryScheduler(",
			wants: []string{
				"txRefundQty := 0",
				"txRefundQty = int(availableCount) * unitQty",
				"refundQty = txRefundQty",
				"if err != nil {\n\t\treturn 0, err\n\t}",
				"return refundQty, nil",
			},
			forbidden: []string{"return refundQty, err"},
		},
	}
	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s start missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endFunc)
		if end < 0 {
			t.Fatalf("%s boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range tt.wants {
			if !strings.Contains(block, want) {
				t.Fatalf("%s transactional return guard missing %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still returns possibly rolled-back values: %s", tt.name, unsafe)
			}
		}
	}
}

func TestMarketplaceSecretSourceHelpers(t *testing.T) {
	tests := []struct {
		source string
		want   string
	}{
		{marketplaceSecretSourceBotInvite, "系统已校验 · 邀请码"},
		{marketplaceSecretSourceBotRenew, "系统已校验 · 续期卡"},
		{marketplaceSecretSourceThirdParty, "三方卡密 · 未校验"},
		{"", "三方卡密 · 未校验"},
		{"legacy", "三方卡密 · 未校验"},
	}
	for _, tt := range tests {
		if got := marketplaceSecretVerificationText(tt.source); got != tt.want {
			t.Fatalf("marketplaceSecretVerificationText(%q) = %q, want %q", tt.source, got, tt.want)
		}
	}

	source, err := marketplaceSecretListingSource([]marketplaceSecretClassification{
		{Source: marketplaceSecretSourceBotInvite},
		{Source: marketplaceSecretSourceBotInvite},
	})
	if err != nil || source != marketplaceSecretSourceBotInvite {
		t.Fatalf("same bot invite source = %q, err = %v", source, err)
	}

	if _, err := marketplaceSecretListingSource([]marketplaceSecretClassification{
		{Source: marketplaceSecretSourceThirdParty},
	}); !errors.Is(err, errMarketplaceUnverifiedSecret) {
		t.Fatalf("third-party source err = %v, want errMarketplaceUnverifiedSecret", err)
	}
}

func TestMarketplaceSecretListingsRequireBotIssuedCodes(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)

	classifyStart := strings.Index(text, "func classifyMarketplaceSecretTx(")
	if classifyStart < 0 {
		t.Fatal("classifyMarketplaceSecretTx missing")
	}
	classifyEnd := strings.Index(text[classifyStart:], "func marketplaceSecretListingSource(")
	if classifyEnd < 0 {
		t.Fatal("classifyMarketplaceSecretTx boundary missing")
	}
	classifyBlock := text[classifyStart : classifyStart+classifyEnd]
	if !strings.Contains(classifyBlock, "return marketplaceSecretClassification{}, errMarketplaceUnverifiedSecret") {
		t.Fatal("unmatched marketplace secret should be rejected instead of treated as third-party")
	}
	if strings.Contains(classifyBlock, "Source: marketplaceSecretSourceThirdParty") {
		t.Fatal("new secret listings should not classify unmatched codes as third-party")
	}

	sourceStart := strings.Index(text, "func marketplaceSecretListingSource(")
	if sourceStart < 0 {
		t.Fatal("marketplaceSecretListingSource missing")
	}
	sourceEnd := strings.Index(text[sourceStart:], "func marketplaceSecretSource(")
	if sourceEnd < 0 {
		t.Fatal("marketplaceSecretListingSource boundary missing")
	}
	sourceBlock := text[sourceStart : sourceStart+sourceEnd]
	for _, want := range []string{
		"source == marketplaceSecretSourceThirdParty",
		"return \"\", errMarketplaceUnverifiedSecret",
	} {
		if !strings.Contains(sourceBlock, want) {
			t.Fatalf("marketplace secret source guard missing %q", want)
		}
	}
}

func TestMarketplacePurchaseInvalidVerifiedSecretQuarantinesListing(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func purchaseMarketplaceListing(")
	if start < 0 {
		t.Fatal("purchaseMarketplaceListing missing")
	}
	end := strings.Index(text[start:], "func quarantineMarketplaceInvalidSecret(")
	if end < 0 {
		t.Fatal("purchaseMarketplaceListing boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"invalidSecretID := uint(0)",
		"listing.ListingType == marketplaceTypeSecret",
		"marketplaceSecretSource(secret.TokenSource) == marketplaceSecretSourceThirdParty",
		"invalidSecretID = secret.ID",
		"return errMarketplaceUnverifiedSecret",
		"return errMarketplaceVerifiedInvalid",
		"quarantineMarketplaceInvalidSecret(DB, listingID, invalidSecretID)",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace invalid secret quarantine guard missing %q", want)
		}
	}
	for _, forbidden := range []string{
		"continue\n\t\t\t}",
		`Update("status", marketplaceSecretClosed)`,
	} {
		if strings.Contains(block, forbidden) {
			t.Fatalf("marketplace purchase should not skip invalid verified secrets inside asset transaction: %q", forbidden)
		}
	}

	helperStart := strings.Index(text, "func quarantineMarketplaceInvalidSecret(")
	if helperStart < 0 {
		t.Fatal("quarantineMarketplaceInvalidSecret missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func showMyMarketplacePurchases(")
	if helperEnd < 0 {
		t.Fatal("quarantineMarketplaceInvalidSecret boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"db.Transaction(func(tx *gorm.DB) error",
		"Update(\"status\", marketplaceSecretClosed)",
		"Update(\"status\", marketplaceStatusReview)",
		"secretRes.Error",
		"listingRes.Error",
		"listingRes.RowsAffected == 0 && secretRes.RowsAffected == 0",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("marketplace invalid secret quarantine helper missing %q", want)
		}
	}
}

func TestMarketplaceListingNamesRejectUnsafeText(t *testing.T) {
	if !validMarketplaceSecretListingName("可靠卡密") {
		t.Fatal("valid secret listing name should be accepted")
	}
	if !validMarketplaceInventoryItemName("聚灵丹") {
		t.Fatal("valid inventory item name should be accepted")
	}
	for _, name := range []string{
		"卡密\n礼包",
		"卡密\t礼包",
		"卡密\x00礼包",
		"卡密\u2028礼包",
		"卡密\u2029礼包",
	} {
		if validMarketplaceSecretListingName(name) {
			t.Fatalf("unsafe secret listing name accepted: %q", name)
		}
		if validMarketplaceInventoryItemName(name) {
			t.Fatalf("unsafe inventory item name accepted: %q", name)
		}
	}
	if validMarketplaceSecretListingName("短") {
		t.Fatal("short secret listing name should be rejected")
	}
	if validMarketplaceInventoryItemName("") {
		t.Fatal("empty inventory item name should be rejected")
	}
}

func TestMarketplaceNamePromptsReuseValidationRequirementText(t *testing.T) {
	source, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go err = %v", err)
	}
	text := string(source)
	for _, want := range []string{
		`marketplaceSecretListingNameRequirementText = "2-40 个字，且不能包含换行、制表符、其他控制字符或 Unicode 行/段分隔符"`,
		`marketplaceInventoryItemNameRequirementText = "1-40 个字，且不能包含换行、制表符、其他控制字符或 Unicode 行/段分隔符"`,
		`"商品名称需为 "+marketplaceSecretListingNameRequirementText+"，请重新发送："`,
		`"物品名称需为 "+marketplaceInventoryItemNameRequirementText+"，请重新发送："`,
		`"从背包上架：请发送要上架的物品名称，" + marketplaceInventoryItemNameRequirementText + "。`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("marketplace name prompt requirement missing %q", want)
		}
	}
	for _, stale := range []string{
		"商品名称需为 2-40 个字，且不能包含换行、制表符或控制字符",
		"物品名称需为 1-40 个字，且不能包含换行、制表符或控制字符",
		"从背包上架：请发送要上架的物品名称。\n\n当前乾坤袋",
	} {
		if strings.Contains(text, stale) {
			t.Fatalf("marketplace name prompt still contains stale text %q", stale)
		}
	}
}

func TestValidMarketplaceDisputeReasonRejectsUnsafeText(t *testing.T) {
	valid := strings.Repeat("好", marketplaceMinDisputeReasonLen)
	if !validMarketplaceDisputeReason(valid) {
		t.Fatalf("validMarketplaceDisputeReason(%q) = false, want true", valid)
	}
	if validMarketplaceDisputeReason("短") {
		t.Fatal("short dispute reason should be rejected")
	}
	if validMarketplaceDisputeReason(strings.Repeat("长", marketplaceMaxDisputeReasonLen+1)) {
		t.Fatal("overlong dispute reason should be rejected")
	}
	for _, reason := range []string{
		"卡密\n无法使用",
		"卡密\t无法使用",
		"卡密\x00无法使用",
		"卡密\u2028无法使用",
		"卡密\u2029无法使用",
	} {
		if validMarketplaceDisputeReason(reason) {
			t.Fatalf("unsafe dispute reason accepted: %q", reason)
		}
	}
}

func TestMarketplaceDisplayTextNormalizesUnsafeSeparators(t *testing.T) {
	got := marketplaceDisplayText("  卖家\n名称\tA\u2028B\u2029C\x00D  ", 20, "-")
	if got != "卖家 名称 A B C D" {
		t.Fatalf("marketplaceDisplayText normalized = %q", got)
	}
	if got := marketplaceDisplayText("\x00\u2028\t", 20, "-"); got != "-" {
		t.Fatalf("marketplaceDisplayText empty fallback = %q", got)
	}
}

func TestMarketplaceSecretSellerGroupsLogText(t *testing.T) {
	if got := marketplaceSecretSellerGroupsLogText(nil); got != "none" {
		t.Fatalf("empty groups log text = %q", got)
	}
	got := marketplaceSecretSellerGroupsLogText([]marketplaceSellerGroup{
		{SellerID: 1001, Count: 2},
		{SellerID: 2002, Count: 1},
	})
	if got != "1001:2,2002:1" {
		t.Fatalf("groups log text = %q", got)
	}
}

func marketplaceTestContainsAll(text string, parts []string) bool {
	for _, part := range parts {
		if !strings.Contains(text, part) {
			return false
		}
	}
	return true
}
func TestMarketplacePurchaseSoldCountChecksRowsAffected(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	marker := `UpdateColumn("sold_count", gorm.Expr("sold_count + ?", buyQty))`
	idx := strings.Index(text, marker)
	if idx < 0 {
		t.Fatalf("missing sold_count update marker: %s", marker)
	}
	blockStart := idx - 220
	if blockStart < 0 {
		blockStart = 0
	}
	block := text[blockStart:minInt(len(text), idx+260)]
	if !strings.Contains(block, "res.RowsAffected == 0") {
		t.Fatalf("sold_count update should check RowsAffected: %s", block)
	}
	if !strings.Contains(block, "MARKETPLACE_SOLD_COUNT_UPDATE_MISSED") {
		t.Fatalf("sold_count update should return a distinct consistency error: %s", block)
	}
	if !strings.Contains(block, `Where("id = ? AND status = ?", listing.ID, marketplaceStatusActive)`) {
		t.Fatalf("sold_count update should require active listing status: %s", block)
	}
}

func TestMarketplacePurchaseClosesSoldOutListingInTransaction(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func purchaseMarketplaceListing(")
	if start < 0 {
		t.Fatal("purchaseMarketplaceListing missing")
	}
	end := strings.Index(text[start:], "func showMyMarketplacePurchases(")
	if end < 0 {
		t.Fatal("purchaseMarketplaceListing boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"var remainingAvailable int64",
		`Where("listing_id = ? AND status = ?", listing.ID, marketplaceSecretAvailable)`,
		"Count(&remainingAvailable).Error",
		"if remainingAvailable == 0",
		`Update("status", marketplaceStatusClosed)`,
		"MARKETPLACE_SOLD_OUT_CLOSE_MISSED",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace sold-out close guard missing %q", want)
		}
	}
	soldCountIdx := strings.Index(block, `UpdateColumn("sold_count", gorm.Expr("sold_count + ?", buyQty))`)
	remainingIdx := strings.Index(block, "var remainingAvailable int64")
	if soldCountIdx < 0 || remainingIdx < 0 || remainingIdx < soldCountIdx {
		t.Fatal("marketplace sold-out close should run after sold_count update")
	}
}

func TestMarketplaceExpirySkipsSoldOutSellerNotice(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func closeExpiredMarketplaceListings(")
	if start < 0 {
		t.Fatal("closeExpiredMarketplaceListings missing")
	}
	end := strings.Index(text[start:], "func marketplaceExpiredCloseNoticeText(")
	if end < 0 {
		t.Fatal("closeExpiredMarketplaceListings boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"var availableCount int64",
		`Where("listing_id = ? AND status = ?", listing.ID, marketplaceSecretAvailable)`,
		"Count(&availableCount).Error",
		"if availableCount == 0",
		"closeMarketplaceListingByID(DB, listing.ID)",
		`handleMarketplaceSellerMismatch(bot, listing.ID, "auto_expire_sold_out")`,
		"marketplace expiry sold-out listing closed without seller notice",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace expiry sold-out guard missing %q", want)
		}
	}
	soldOutStart := strings.Index(block, "if availableCount == 0")
	normalCloseStart := strings.Index(block, "refundQty, err := closeMarketplaceListingByID(DB, listing.ID)")
	if soldOutStart < 0 || normalCloseStart < 0 || normalCloseStart < soldOutStart {
		t.Fatal("marketplace expiry sold-out guard should run before normal close and notice path")
	}
	soldOutBlock := block[soldOutStart:normalCloseStart]
	forbiddenSend := "send" + "PlainText("
	forbiddenNotice := "marketplaceExpired" + "CloseNoticeText"
	if strings.Contains(soldOutBlock, forbiddenSend) || strings.Contains(soldOutBlock, forbiddenNotice) {
		t.Fatalf("marketplace sold-out expiry path should not notify seller: %s", soldOutBlock)
	}
}

func TestMarketplacePurchaseTransactionKeepsAssetGuards(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func purchaseMarketplaceListing(")
	if start < 0 {
		t.Fatal("purchaseMarketplaceListing missing")
	}
	end := strings.Index(text[start:], "func showMyMarketplacePurchases(")
	if end < 0 {
		t.Fatal("purchaseMarketplaceListing boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"marketplaceActiveListingQuery(tx, time.Now()).Where(\"id = ?\", listingID)",
		"validateMarketplaceListingSellerConsistencyTx(tx, listing)",
		"listing.SellerID == buyerID",
		"listing.Price < marketplaceMinUnitPrice || listing.Price > marketplaceMaxPrice",
		`tx.Where("listing_id = ? AND status = ?", listingID, marketplaceSecretAvailable)`,
		"marketplaceVerifiedSecretUsableTx(tx, secret)",
		`Where("id = ? AND status = ?", secret.ID, marketplaceSecretAvailable)`,
		"gardenGrantInventoryInTx(tx, buyerID, itemName, totalQuantity)",
		strings.Join([]string{"applyPointDelta", "InTx("}, ""),
		strings.Join([]string{"buyerID,", "\n\t\t\t-grossAmount"}, ""),
		strings.Join([]string{"listing.SellerID,", "\n\t\t\t\tsellerAmount"}, ""),
		strings.Join([]string{"addPointsToFusionPool", "InTx(tx, feeAmount)"}, ""),
		"createMarketplacePurchaseInTx(tx, &purchase)",
		`Where("id = ? AND status = ?", listing.ID, marketplaceStatusActive)`,
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace purchase asset guard missing %q", want)
		}
	}
}

func TestMarketplacePurchaseCreateChecksRowsAffected(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	helperStart := strings.Index(text, "func createMarketplacePurchaseInTx(")
	if helperStart < 0 {
		t.Fatal("createMarketplacePurchaseInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func purchaseMarketplaceListing(")
	if helperEnd < 0 {
		t.Fatal("createMarketplacePurchaseInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"entry := *purchase",
		"entry.ItemName = marketplaceDisplayText(entry.ItemName, marketplaceMaxNameLen, \"-\")",
		"entry.CodePreview = formatPlainValue(entry.CodePreview)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"MARKETPLACE_PURCHASE_CREATE_MISSED",
		"purchase.ID = entry.ID",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("marketplace purchase helper guard missing %q", want)
		}
	}

	purchaseStart := strings.Index(text, "func purchaseMarketplaceListing(")
	if purchaseStart < 0 {
		t.Fatal("purchaseMarketplaceListing missing")
	}
	purchaseEnd := strings.Index(text[purchaseStart:], "func showMyMarketplacePurchases(")
	if purchaseEnd < 0 {
		t.Fatal("purchaseMarketplaceListing boundary missing")
	}
	purchaseBlock := text[purchaseStart : purchaseStart+purchaseEnd]
	if !strings.Contains(purchaseBlock, "createMarketplacePurchaseInTx(tx, &purchase)") {
		t.Fatal("purchaseMarketplaceListing should use createMarketplacePurchaseInTx")
	}
	if strings.Contains(purchaseBlock, "tx.Create(&purchase).Error") {
		t.Fatal("marketplace purchase create still ignores RowsAffected")
	}
}

func TestMarketplacePurchaseSecretMigrationReplacesFullUniqueIndex(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, `assertNoDuplicateGroups("marketplace_purchases(secret_id)"`)
	if start < 0 {
		t.Fatal("marketplace purchase secret migration block missing")
	}
	end := strings.Index(text[start:], `markMigrationAppliedIfMissing("20260101_consistency_indexes")`)
	if end < 0 {
		t.Fatal("marketplace purchase secret migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"FROM marketplace_purchases",
		"WHERE secret_id > 0 AND deleted_at IS NULL",
		"ensureMarketplacePurchaseSecretPartialUniqueIndex(DB)",
		"marketplace purchase secret unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace purchase secret migration block missing %q", want)
		}
	}

	helperStart := strings.Index(text, "func ensureMarketplacePurchaseSecretPartialUniqueIndex(")
	if helperStart < 0 {
		t.Fatal("ensureMarketplacePurchaseSecretPartialUniqueIndex missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func ensureGardenPlotPartialUniqueIndexes(")
	if helperEnd < 0 {
		t.Fatal("marketplace purchase secret partial index helper boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"ensureSoftDeletePartialUniqueIndex",
		"idx_marketplace_purchases_secret_unique",
		"ON marketplace_purchases(secret_id)",
		"WHERE secret_id > 0 AND deleted_at IS NULL",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("marketplace purchase secret partial index helper missing %q", want)
		}
	}
}

func TestMarketplaceSpecialMigrationsReplaceStaleUniqueIndexes(t *testing.T) {
	data, err := os.ReadFile("db.go")
	if err != nil {
		t.Fatalf("read db.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "if installed, err := ensureMarketplaceOpenDisputeUniqueIndex(DB)")
	if start < 0 {
		t.Fatal("marketplace special migration block missing")
	}
	end := strings.Index(text[start:], `assertNoDuplicateGroups("marketplace_purchases(secret_id)"`)
	if end < 0 {
		t.Fatal("marketplace special migration block boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"ensureMarketplaceOpenDisputeUniqueIndex(DB)",
		"marketplace open dispute unique index migration failed; startup blocked",
		"ensureMarketplaceActiveSecretHashUniqueIndex(DB)",
		"marketplace active secret hash unique index migration failed; startup blocked",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace special migration block missing %q", want)
		}
	}

	openStart := strings.Index(text, "func ensureMarketplaceOpenDisputeUniqueIndex(")
	if openStart < 0 {
		t.Fatal("ensureMarketplaceOpenDisputeUniqueIndex missing")
	}
	openEnd := strings.Index(text[openStart:], "func marketplaceOpenDisputeDuplicateGroups(")
	if openEnd < 0 {
		t.Fatal("marketplace open dispute helper boundary missing")
	}
	openBlock := text[openStart : openStart+openEnd]
	for _, want := range []string{
		"marketplaceOpenDisputeDuplicateGroups(db)",
		"ensureSoftDeletePartialUniqueIndex",
		"idx_marketplace_disputes_open_purchase_buyer_unique",
		"ON marketplace_disputes(purchase_id, buyer_id)",
		"WHERE status = 'open' AND deleted_at IS NULL",
	} {
		if !strings.Contains(openBlock, want) {
			t.Fatalf("marketplace open dispute helper missing %q", want)
		}
	}
	if strings.Contains(openBlock, "db.Exec(`") {
		t.Fatal("marketplace open dispute helper must not use raw CREATE INDEX IF NOT EXISTS")
	}

	secretStart := strings.Index(text, "func ensureMarketplaceActiveSecretHashUniqueIndex(")
	if secretStart < 0 {
		t.Fatal("ensureMarketplaceActiveSecretHashUniqueIndex missing")
	}
	secretEnd := strings.Index(text[secretStart:], "func marketplaceActiveSecretHashDuplicateGroups(")
	if secretEnd < 0 {
		t.Fatal("marketplace active secret helper boundary missing")
	}
	secretBlock := text[secretStart : secretStart+secretEnd]
	for _, want := range []string{
		"marketplaceActiveSecretHashDuplicateGroups(db)",
		"quarantineDuplicateMarketplaceAvailableSecrets(db, dups)",
		"ensureSoftDeletePartialUniqueIndex",
		"idx_marketplace_secrets_active_code_hash_unique",
		"ON marketplace_secrets(code_hash)",
		"WHERE code_hash <> '' AND status = 'available' AND deleted_at IS NULL",
	} {
		if !strings.Contains(secretBlock, want) {
			t.Fatalf("marketplace active secret helper missing %q", want)
		}
	}
	if strings.Contains(secretBlock, "db.Exec(`") {
		t.Fatal("marketplace active secret helper must not use raw CREATE INDEX IF NOT EXISTS")
	}
}

func TestMarketplaceListingAndSecretCreateChecksRowsAffected(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	listingHelperStart := strings.Index(text, "func createMarketplaceListingInTx(")
	if listingHelperStart < 0 {
		t.Fatal("createMarketplaceListingInTx missing")
	}
	listingHelperEnd := strings.Index(text[listingHelperStart:], "func createMarketplaceSecretInTx(")
	if listingHelperEnd < 0 {
		t.Fatal("createMarketplaceListingInTx boundary missing")
	}
	listingHelperBlock := text[listingHelperStart : listingHelperStart+listingHelperEnd]
	for _, want := range []string{
		"entry := *listing",
		"entry.SellerName = marketplaceDisplayText(entry.SellerName, marketplaceDisplayNameMaxLen, \"-\")",
		"entry.Name = marketplaceDisplayText(entry.Name, marketplaceMaxNameLen, \"-\")",
		"entry.Description = formatPlainValue(entry.Description)",
		"entry.ListingType = formatPlainValue(entry.ListingType)",
		"entry.SecretSource = formatPlainValue(entry.SecretSource)",
		"entry.ItemName = marketplaceDisplayText(entry.ItemName, marketplaceMaxNameLen, \"\")",
		"entry.Status = formatPlainValue(entry.Status)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"MARKETPLACE_LISTING_CREATE_MISSED",
		"*listing = entry",
	} {
		if !strings.Contains(listingHelperBlock, want) {
			t.Fatalf("marketplace listing helper guard missing %q", want)
		}
	}

	secretHelperStart := strings.Index(text, "func createMarketplaceSecretInTx(")
	if secretHelperStart < 0 {
		t.Fatal("createMarketplaceSecretInTx missing")
	}
	secretHelperEnd := strings.Index(text[secretHelperStart:], "func validMarketplaceSecretListingName(")
	if secretHelperEnd < 0 {
		t.Fatal("createMarketplaceSecretInTx boundary missing")
	}
	secretHelperBlock := text[secretHelperStart : secretHelperStart+secretHelperEnd]
	for _, want := range []string{
		"entry := *secret",
		"entry.Preview = formatPlainValue(entry.Preview)",
		"entry.TokenSource = formatPlainValue(entry.TokenSource)",
		"entry.Status = formatPlainValue(entry.Status)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"MARKETPLACE_SECRET_CREATE_MISSED",
		"*secret = entry",
	} {
		if !strings.Contains(secretHelperBlock, want) {
			t.Fatalf("marketplace secret helper guard missing %q", want)
		}
	}
	for _, unsafe := range []string{
		"entry.CodeEnc = formatPlainValue(entry.CodeEnc)",
		"entry.CodeHash = formatPlainValue(entry.CodeHash)",
	} {
		if strings.Contains(secretHelperBlock, unsafe) {
			t.Fatalf("marketplace secret helper should not rewrite sensitive token fields: %s", unsafe)
		}
	}

	secretListingStart := strings.Index(text, "func createMarketplaceSecretListing(")
	if secretListingStart < 0 {
		t.Fatal("createMarketplaceSecretListing missing")
	}
	secretListingEnd := strings.Index(text[secretListingStart:], "func normalizeMarketplaceSecrets(")
	if secretListingEnd < 0 {
		t.Fatal("createMarketplaceSecretListing boundary missing")
	}
	secretListingBlock := text[secretListingStart : secretListingStart+secretListingEnd]
	inventoryStart := strings.Index(text, "func createMarketplaceInventoryListing(")
	if inventoryStart < 0 {
		t.Fatal("createMarketplaceInventoryListing missing")
	}
	inventoryEnd := strings.Index(text[inventoryStart:], "func sendMarketplaceInventoryChoices(")
	if inventoryEnd < 0 {
		t.Fatal("createMarketplaceInventoryListing boundary missing")
	}
	inventoryBlock := text[inventoryStart : inventoryStart+inventoryEnd]
	for _, block := range []struct {
		name string
		text string
	}{
		{name: "secret listing", text: secretListingBlock},
		{name: "inventory listing", text: inventoryBlock},
	} {
		for _, want := range []string{
			"createMarketplaceListingInTx(tx, &listing)",
			"createMarketplaceSecretInTx(tx, &MarketplaceSecret{",
		} {
			if !strings.Contains(block.text, want) {
				t.Fatalf("%s path missing helper %q", block.name, want)
			}
		}
		for _, unsafe := range []string{
			"tx.Create(&listing).Error",
			"tx.Create(&MarketplaceSecret{",
		} {
			if strings.Contains(block.text, unsafe) {
				t.Fatalf("%s create still ignores RowsAffected: %s", block.name, unsafe)
			}
		}
	}
}

func TestMarketplaceDisputeCreateChecksRowsAffected(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	helperStart := strings.Index(text, "func createMarketplaceDisputeInTx(")
	if helperStart < 0 {
		t.Fatal("createMarketplaceDisputeInTx missing")
	}
	helperEnd := strings.Index(text[helperStart:], "func validMarketplaceSecretListingName(")
	if helperEnd < 0 {
		t.Fatal("createMarketplaceDisputeInTx boundary missing")
	}
	helperBlock := text[helperStart : helperStart+helperEnd]
	for _, want := range []string{
		"entry := *dispute",
		"entry.Reason = formatPlainValue(entry.Reason)",
		"entry.Status = formatPlainValue(entry.Status)",
		"res := tx.Create(&entry)",
		"res.Error != nil",
		"res.RowsAffected == 0",
		"MARKETPLACE_DISPUTE_CREATE_MISSED",
		"*dispute = entry",
	} {
		if !strings.Contains(helperBlock, want) {
			t.Fatalf("marketplace dispute helper guard missing %q", want)
		}
	}

	disputeStart := strings.Index(text, "func handleMarketplaceDispute(")
	if disputeStart < 0 {
		t.Fatal("handleMarketplaceDispute missing")
	}
	disputeEnd := strings.Index(text[disputeStart:], "func hasOpenMarketplaceDispute(")
	if disputeEnd < 0 {
		t.Fatal("handleMarketplaceDispute boundary missing")
	}
	disputeBlock := text[disputeStart : disputeStart+disputeEnd]
	if !strings.Contains(disputeBlock, "createMarketplaceDisputeInTx(DB, &dispute)") {
		t.Fatal("marketplace dispute flow does not use createMarketplaceDisputeInTx")
	}
	if strings.Contains(disputeBlock, "DB.Create(&dispute).Error") {
		t.Fatal("marketplace dispute flow still creates dispute directly")
	}
}

func TestMarketplaceAdminOrderQueryHandlesDisputeReadErrors(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func handleMarketplaceAdminOrderQuery(")
	if start < 0 {
		t.Fatal("handleMarketplaceAdminOrderQuery missing")
	}
	end := strings.Index(text[start:], "func handleMarketplaceDispute(")
	if end < 0 {
		t.Fatal("handleMarketplaceAdminOrderQuery boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"Find(&disputes).Error",
		"交易订单争议记录读取失败",
		"争议记录：读取失败",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace order dispute read guard missing %q", want)
		}
	}
	if strings.Contains(block, "_ = DB.Where(\"purchase_id = ?\", purchase.ID).Order(\"created_at DESC\").Limit(5).Find(&disputes).Error") {
		t.Fatal("marketplace order query still ignores dispute read errors")
	}
}

func TestMarketplaceListReadErrorsAreLogged(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
	}{
		{
			name:      "public listings",
			startFunc: "func showMarketplaceListings(",
			endFunc:   "func notifyMarketplaceListingCreated(",
			markers: []string{
				"交易行列表读取失败",
				"formatPlainValue(filter.Kind)",
				"formatPlainValue(filter.Keyword)",
				"formatPlainError(err)",
			},
		},
		{
			name:      "seller listings",
			startFunc: "func showMyMarketplaceListings(",
			endFunc:   "func handleMarketplaceDetail(",
			markers: []string{
				"我的交易行列表读取失败",
				"formatPlainError(err)",
			},
		},
		{
			name:      "buyer purchases",
			startFunc: "func showMyMarketplacePurchases(",
			endFunc:   "func marketplacePurchaseSecretQuery(",
			markers: []string{
				"我的交易行购买记录读取失败",
				"formatPlainError(err)",
			},
		},
		{
			name:      "listing orders",
			startFunc: "func handleMarketplaceListingOrders(",
			endFunc:   "func handleMarketplaceAdminOrderQuery(",
			markers: []string{
				"交易行商品订单列表读取失败",
				"formatPlainError(err)",
			},
		},
	}
	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s start missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endFunc)
		if end < 0 {
			t.Fatalf("%s boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range tt.markers {
			if !strings.Contains(block, want) {
				t.Fatalf("%s read error diagnostic missing %q", tt.name, want)
			}
		}
	}
}

func TestMarketplaceMyPurchasesLogsSecretReadAndDecryptErrors(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func showMyMarketplacePurchases(")
	if start < 0 {
		t.Fatal("showMyMarketplacePurchases missing")
	}
	end := strings.Index(text[start:], "func marketplacePurchaseSecretQuery(")
	if end < 0 {
		t.Fatal("showMyMarketplacePurchases boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"marketplacePurchaseSecretQuery(DB, purchase, buyerID).First(&secret).Error",
		"decryptMarketplaceSecret(secret.CodeEnc)",
		"交易行我的购买卡密读取失败",
		"交易行我的购买卡密解密失败",
		"formatPlainError(err)",
		"purchase.ID",
		"purchase.ListingID",
		"purchase.SecretID",
		"buyerID",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace my purchases secret diagnostics missing %q", want)
		}
	}
	if strings.Contains(block, "secret.CodeEnc,") || strings.Contains(block, "codeText, formatPlainError") {
		t.Fatal("marketplace my purchases diagnostics should not log secret ciphertext or plaintext")
	}
}

func TestMarketplaceQuarantineForReviewChecksRowsAffected(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func quarantineMarketplaceListingForReview(")
	if start < 0 {
		t.Fatal("quarantineMarketplaceListingForReview missing")
	}
	end := strings.Index(text[start:], "func closeMarketplaceListingScoped(")
	if end < 0 {
		t.Fatal("quarantineMarketplaceListingForReview boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"res := db.Model(&MarketplaceListing{})",
		"res.Error",
		"res.RowsAffected == 0",
		"return errMarketplaceListingNotFound",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace quarantine rows affected guard missing %q", want)
		}
	}
	if strings.Contains(block, `Update("status", marketplaceStatusReview).Error`) {
		t.Fatal("marketplace quarantine still ignores RowsAffected")
	}
}

func TestMarketplaceCloseChecksClosedSecretRowsAffected(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func closeMarketplaceListingScoped(")
	if start < 0 {
		t.Fatal("closeMarketplaceListingScoped missing")
	}
	end := strings.Index(text[start:], "func StartMarketplaceExpiryScheduler(")
	if end < 0 {
		t.Fatal("closeMarketplaceListingScoped boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"var availableCount int64",
		"Count(&availableCount).Error",
		"secretRes := tx.Model(&MarketplaceSecret{})",
		"secretRes.Error",
		"secretRes.RowsAffected != availableCount",
		"marketplace close available units changed",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace close secret rows affected guard missing %q", want)
		}
	}
	if strings.Contains(block, `Update("status", marketplaceClosedSecretStatus()).Error`) {
		t.Fatal("marketplace close still ignores closed secret RowsAffected")
	}
}

func TestMarketplaceDetailAndBuyConfirmDistinguishReadErrors(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)

	detailStart := strings.Index(text, "func handleMarketplaceDetail(")
	if detailStart < 0 {
		t.Fatal("handleMarketplaceDetail missing")
	}
	detailEnd := strings.Index(text[detailStart:], "func marketplacePillEffectLine(")
	if detailEnd < 0 {
		t.Fatal("handleMarketplaceDetail boundary missing")
	}
	detailBlock := text[detailStart : detailStart+detailEnd]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"交易行详情商品读取失败",
		"formatPlainError(err)",
		"商品状态读取失败，请稍后重试。",
	} {
		if !strings.Contains(detailBlock, want) {
			t.Fatalf("marketplace detail read error guard missing %q", want)
		}
	}
	if strings.Contains(detailBlock, `if err := DB.First(&listing, id).Error; err != nil {
		sendPlainText(bot, chatID, "❌ 未找到该交易行商品。")
		return
	}`) {
		t.Fatal("marketplace detail still treats all read errors as not found")
	}

	buyStart := strings.Index(text, "func handleMarketplaceBuy(")
	if buyStart < 0 {
		t.Fatal("handleMarketplaceBuy missing")
	}
	buyEnd := strings.Index(text[buyStart:], "func executeMarketplaceBuy(")
	if buyEnd < 0 {
		t.Fatal("handleMarketplaceBuy boundary missing")
	}
	buyBlock := text[buyStart : buyStart+buyEnd]
	for _, want := range []string{
		"errors.Is(err, gorm.ErrRecordNotFound)",
		"交易行购买确认商品读取失败",
		"formatPlainError(err)",
		"商品状态读取失败，请稍后重试。",
	} {
		if !strings.Contains(buyBlock, want) {
			t.Fatalf("marketplace buy confirm read error guard missing %q", want)
		}
	}
	if strings.Contains(buyBlock, `if err := marketplaceActiveListingQuery(DB, time.Now()).Where("id = ?", listingID).First(&listing).Error; err != nil {
			sendPlainText(bot, msg.Chat.ID, "❌ 商品不存在或已下架。")
			return
		}`) {
		t.Fatal("marketplace buy confirm still treats all read errors as not found")
	}
}

func TestMarketplaceInventoryListingInventoryReadsDistinguishErrors(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func handleMarketplaceStep(")
	if start < 0 {
		t.Fatal("handleMarketplaceStep missing")
	}
	end := strings.Index(text[start:], "func createMarketplaceSecretListing(")
	if end < 0 {
		t.Fatal("handleMarketplaceStep boundary missing")
	}
	block := text[start : start+end]

	checks := []struct {
		name    string
		start   string
		end     string
		markers []string
	}{
		{
			name:  "inventory item",
			start: `case "WAITING_MARKET_INVENTORY_ITEM":`,
			end:   `case "WAITING_MARKET_INVENTORY_QUANTITY":`,
			markers: []string{
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"交易行背包上架物品读取失败",
				"formatPlainValue(itemName)",
				"formatPlainError(err)",
				"乾坤袋读取失败，请稍后重试。",
			},
		},
		{
			name:  "inventory quantity",
			start: `case "WAITING_MARKET_INVENTORY_QUANTITY":`,
			end:   `case "WAITING_MARKET_PRICE":`,
			markers: []string{
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"交易行背包上架数量库存读取失败",
				"formatPlainValue(itemName)",
				"formatPlainError(err)",
				"乾坤袋库存读取失败，请稍后重试。",
			},
		},
	}
	for _, tc := range checks {
		branchStart := strings.Index(block, tc.start)
		if branchStart < 0 {
			t.Fatalf("%s branch missing", tc.name)
		}
		branchEnd := strings.Index(block[branchStart:], tc.end)
		if branchEnd < 0 {
			t.Fatalf("%s branch boundary missing", tc.name)
		}
		branch := block[branchStart : branchStart+branchEnd]
		for _, want := range tc.markers {
			if !strings.Contains(branch, want) {
				t.Fatalf("%s inventory read guard missing %q", tc.name, want)
			}
		}
	}
}

func TestMarketplaceOrderEntrypointsDistinguishReadErrors(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)

	tests := []struct {
		name      string
		startFunc string
		endFunc   string
		markers   []string
		forbidden []string
	}{
		{
			name:      "seller listing orders",
			startFunc: "func handleMarketplaceListingOrders(",
			endFunc:   "func handleMarketplaceAdminOrderQuery(",
			markers: []string{
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"交易行商品订单读取商品失败",
				"商品订单状态读取失败，请稍后重试。",
				"formatPlainError(err)",
			},
			forbidden: []string{
				`if err := DB.Where("id = ? AND seller_id = ?", listingID, msg.From.ID).First(&listing).Error; err != nil {
		sendPlainText(bot, msg.Chat.ID, "❌ 未找到本人交易行商品。")
		return
	}`,
			},
		},
		{
			name:      "admin order query",
			startFunc: "func handleMarketplaceAdminOrderQuery(",
			endFunc:   "func handleMarketplaceDispute(",
			markers: []string{
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"管理员查询交易订单读取失败",
				"交易订单读取失败，请稍后重试。",
				"formatPlainError(err)",
			},
			forbidden: []string{
				`if err := DB.First(&purchase, uint(orderID)).Error; err != nil {
		sendPlainText(bot, msg.Chat.ID, "❌ 未找到该交易订单。")
		return
	}`,
			},
		},
		{
			name:      "buyer dispute",
			startFunc: "func handleMarketplaceDispute(",
			endFunc:   "func hasOpenMarketplaceDispute(",
			markers: []string{
				"errors.Is(err, gorm.ErrRecordNotFound)",
				"交易争议订单读取失败",
				"交易订单读取失败，请稍后重试。",
				"formatPlainError(err)",
			},
			forbidden: []string{
				`if err := DB.Where("id = ? AND buyer_id = ?", uint(orderID), msg.From.ID).First(&purchase).Error; err != nil {
		sendPlainText(bot, msg.Chat.ID, "❌ 未找到你的该笔交易订单。")
		return
	}`,
			},
		},
	}

	for _, tt := range tests {
		start := strings.Index(text, tt.startFunc)
		if start < 0 {
			t.Fatalf("%s start missing", tt.name)
		}
		end := strings.Index(text[start:], tt.endFunc)
		if end < 0 {
			t.Fatalf("%s boundary missing", tt.name)
		}
		block := text[start : start+end]
		for _, want := range tt.markers {
			if !strings.Contains(block, want) {
				t.Fatalf("%s read error guard missing %q", tt.name, want)
			}
		}
		for _, unsafe := range tt.forbidden {
			if strings.Contains(block, unsafe) {
				t.Fatalf("%s still treats all read errors as not found", tt.name)
			}
		}
	}
}

func TestMarketplaceManualCloseLogsUnexpectedErrors(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func handleMarketplaceClose(")
	if start < 0 {
		t.Fatal("handleMarketplaceClose missing")
	}
	end := strings.Index(text[start:], "func closeMarketplaceListingInTx(")
	if end < 0 {
		t.Fatal("handleMarketplaceClose boundary missing")
	}
	block := text[start : start+end]
	for _, want := range []string{
		"errors.Is(err, errMarketplaceCloseNotFound)",
		"errors.Is(err, errMarketplaceSellerMismatch)",
		"errors.Is(err, errMarketplaceInvalidType)",
		"交易行商品下架失败",
		"formatPlainError(err)",
		"❌ 下架失败，请稍后再试。",
	} {
		if !strings.Contains(block, want) {
			t.Fatalf("marketplace manual close error guard missing %q", want)
		}
	}
	logIndex := strings.Index(block, "交易行商品下架失败")
	replyIndex := strings.Index(block, `sendPlainText(bot, msg.Chat.ID, "❌ 下架失败，请稍后再试。")`)
	if logIndex < 0 || replyIndex < 0 || logIndex > replyIndex {
		t.Fatal("marketplace manual close generic failure should log diagnostics before replying")
	}
}

func TestMarketplaceForceCloseRequiresSuperAdminAndAudit(t *testing.T) {
	if !isMarketplaceCommand("强制下架商品 8") {
		t.Fatal("force close marketplace command should be recognized")
	}
	if _, ok := parseMarketplaceID("强制下架商品 8", "强制下架商品"); !ok {
		t.Fatal("force close marketplace command should parse listing id")
	}
	if got := marketplaceErrorCode(errMarketplacePriceAboveCeiling); got != "MARKETPLACE_PRICE_ABOVE_CEILING" {
		t.Fatalf("marketplaceErrorCode(price above ceiling) = %s", got)
	}
	if got := marketplaceCreateErrorText(errMarketplacePriceAboveCeiling); !strings.Contains(got, "115%") {
		t.Fatalf("marketplace price ceiling text = %q, want 115%% hint", got)
	}

	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	start := strings.Index(text, "func handleMarketplaceForceCloseStart(")
	if start < 0 {
		t.Fatal("handleMarketplaceForceCloseStart missing")
	}
	end := strings.Index(text[start:], "func handleMarketplaceDispute(")
	if end < 0 {
		t.Fatal("force close handler boundary missing")
	}
	handlerBlock := text[start : start+end]
	for _, want := range []string{
		"!msg.Chat.IsPrivate()",
		"!isSuperAdmin(msg.From.ID)",
		"validateAdminReason(text)",
		`text != "确认强制下架"`,
		"forceCloseMarketplaceListingInTx(DB, msg.From.ID",
		"notifyMarketplaceForceClosedSeller(bot, result, reason, msg.From.ID)",
	} {
		if !strings.Contains(handlerBlock, want) {
			t.Fatalf("marketplace force close handler guard missing %q", want)
		}
	}

	forceStart := strings.Index(text, "func forceCloseMarketplaceListingInTx(")
	if forceStart < 0 {
		t.Fatal("forceCloseMarketplaceListingInTx missing")
	}
	forceEnd := strings.Index(text[forceStart:], "func notifyMarketplaceForceClosedSeller(")
	if forceEnd < 0 {
		t.Fatal("force close transaction boundary missing")
	}
	forceBlock := text[forceStart : forceStart+forceEnd]
	if !strings.Contains(forceBlock, "closeMarketplaceListingScopedWithAudit(db, 0, listingID, false, actorID, reason)") {
		t.Fatal("force close should call unscoped close helper with audit")
	}

	closeStart := strings.Index(text, "func closeMarketplaceListingScopedWithAudit(")
	if closeStart < 0 {
		t.Fatal("closeMarketplaceListingScopedWithAudit missing")
	}
	closeEnd := strings.Index(text[closeStart:], "func StartMarketplaceExpiryScheduler(")
	if closeEnd < 0 {
		t.Fatal("closeMarketplaceListingScopedWithAudit boundary missing")
	}
	closeBlock := text[closeStart : closeStart+closeEnd]
	for _, want := range []string{
		"FORCE_CLOSE_MARKETPLACE_LISTING",
		"writeAuditLogInTx(",
	} {
		if !strings.Contains(closeBlock, want) {
			t.Fatalf("marketplace force close tx audit guard missing %q", want)
		}
	}
	if strings.Contains(closeBlock, "writeAuditLog(actorID") {
		t.Fatal("force close audit must be transactional")
	}
}

func TestMarketplaceDiagnosticsUseSanitizedErrors(t *testing.T) {
	data, err := os.ReadFile("marketplace.go")
	if err != nil {
		t.Fatalf("read marketplace.go: %v", err)
	}
	text := string(data)
	if !strings.Contains(text, "formatPlainError(err)") {
		t.Fatal("marketplace diagnostics should use formatPlainError")
	}
	rawErrFormat := string([]byte{'e', 'r', 'r', '=', '%', 'v'})
	if strings.Contains(text, rawErrFormat) {
		t.Fatal("marketplace diagnostics should not log raw error values")
	}
}
