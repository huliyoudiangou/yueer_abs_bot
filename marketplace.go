package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const (
	marketplaceStatusActive           = "active"
	marketplaceStatusClosed           = "closed"
	marketplaceStatusReview           = "review"
	marketplaceSecretAvailable        = "available"
	marketplaceSecretSold             = "sold"
	marketplaceSecretClosed           = "closed"
	marketplaceTypeSecret             = "secret"
	marketplaceTypeInventory          = "inventory"
	marketplaceSecretSourceThirdParty = "third_party"
	marketplaceSecretSourceBotInvite  = "bot_invite"
	marketplaceSecretSourceBotRenew   = "bot_renew"
	marketplaceDisputeOpen            = "open"
	marketplaceDisputeClosed          = "closed"

	marketplaceMinSecretListingNameLen    = 2
	marketplaceMaxNameLen                 = 40
	marketplaceMaxSecretLen               = 200
	marketplaceMaxSecretsPerPost          = 50
	marketplaceMaxPrice                   = 100000
	marketplaceMaxInventoryUnits          = 1000
	marketplaceMaxBuyQuantity             = 50
	marketplaceFeePercent                 = 3
	marketplaceConfirmPrice               = 1000
	marketplaceMinUnitPrice               = 1
	marketplaceMinDisputeReasonLen        = 3
	marketplaceMaxDisputeReasonLen        = 200
	marketplaceDisplayNameMaxLen          = 60
	marketplaceDisplayPreviewMaxLen       = 80
	marketplaceDisplayStatusMaxLen        = 24
	marketplaceMaxFilterLen               = 40
	marketplaceSecretListingMinMajorRealm = 2
	marketplaceListingTTL                 = 48 * time.Hour
	marketplaceExpirySweepInterval        = 1 * time.Minute
	marketplaceExpirySweepBatchSize       = 50

	marketplaceSecretListingNameRequirementText = "2-40 个字，且不能包含换行、制表符、其他控制字符或 Unicode 行/段分隔符"
	marketplaceInventoryItemNameRequirementText = "1-40 个字，且不能包含换行、制表符、其他控制字符或 Unicode 行/段分隔符"
)

func HandleMarketplaceCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState) bool {
	if msg == nil || msg.From == nil || msg.Chat == nil {
		return false
	}

	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	if session != nil && strings.HasPrefix(session.GetStep(), "WAITING_MARKET_") {
		if !msg.Chat.IsPrivate() {
			registerIncomingGroupCommandForAutoDelete(msg)
			sendPlainText(bot, msg.Chat.ID, "交易行操作请回到私聊继续，群内不会处理商品信息或卡密。")
			return true
		}
		handleMarketplaceStep(bot, msg, trimmed, session)
		return true
	}

	if !isMarketplaceCommand(trimmed) {
		return false
	}

	if !msg.Chat.IsPrivate() {
		switch {
		case isMarketplaceListCommand(trimmed) || hasMarketplaceCommandPrefix(trimmed, "交易行详情"):
			registerIncomingGroupCommandForAutoDelete(msg)
			handleMarketplacePublicCommand(bot, msg, trimmed)
		default:
			registerIncomingGroupCommandForAutoDelete(msg)
			sendPlainText(bot, msg.Chat.ID, "交易行上架、购买和卡密交付请私聊 Bot 执行。")
		}
		return true
	}

	handleMarketplacePrivateCommand(bot, msg, trimmed, session)
	return true
}

func isMarketplaceCommand(text string) bool {
	return isMarketplaceListCommand(text) ||
		text == "交易行帮助" ||
		text == "我的交易行" ||
		text == "我的购买" ||
		text == "我的订单" ||
		text == "上架商品" ||
		hasMarketplaceCommandPrefix(text, "交易行订单") ||
		hasMarketplaceCommandPrefix(text, "查交易订单") ||
		hasMarketplaceCommandPrefix(text, "举报订单") ||
		hasMarketplaceCommandPrefix(text, "购买商品") ||
		hasMarketplaceCommandPrefix(text, "下架商品") ||
		hasMarketplaceCommandPrefix(text, "交易行详情")
}

func isMarketplaceListCommand(text string) bool {
	return hasMarketplaceCommandPrefix(text, "交易行") ||
		hasMarketplaceCommandPrefix(text, "交易行列表")
}

func hasMarketplaceCommandPrefix(text string, command string) bool {
	if text == command {
		return true
	}
	if !strings.HasPrefix(text, command) {
		return false
	}
	suffix := strings.TrimPrefix(text, command)
	if suffix == "" {
		return true
	}
	r, _ := utf8.DecodeRuneInString(suffix)
	return unicode.IsSpace(r)
}

func handleMarketplacePublicCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) {
	if hasMarketplaceCommandPrefix(text, "交易行详情") {
		handleMarketplaceDetail(bot, msg.Chat.ID, text)
		return
	}
	showMarketplaceListings(bot, msg.Chat.ID, text)
}

func handleMarketplacePrivateCommand(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState) {
	switch {
	case isMarketplaceListCommand(text):
		showMarketplaceListings(bot, msg.Chat.ID, text)
	case text == "交易行帮助":
		showMarketplaceHelp(bot, msg.Chat.ID, msg.From.ID)
	case text == "我的交易行":
		showMyMarketplaceListings(bot, msg.Chat.ID, msg.From.ID)
	case text == "我的购买" || text == "我的订单":
		showMyMarketplacePurchases(bot, msg.Chat.ID, msg.From.ID)
	case text == "上架商品":
		startMarketplaceListingWizard(bot, msg, session)
	case hasMarketplaceCommandPrefix(text, "交易行订单"):
		handleMarketplaceListingOrders(bot, msg, text)
	case hasMarketplaceCommandPrefix(text, "查交易订单"):
		handleMarketplaceAdminOrderQuery(bot, msg, text)
	case hasMarketplaceCommandPrefix(text, "举报订单"):
		handleMarketplaceDispute(bot, msg, text)
	case hasMarketplaceCommandPrefix(text, "交易行详情"):
		handleMarketplaceDetail(bot, msg.Chat.ID, text)
	case hasMarketplaceCommandPrefix(text, "购买商品"):
		handleMarketplaceBuy(bot, msg, text)
	case hasMarketplaceCommandPrefix(text, "下架商品"):
		handleMarketplaceClose(bot, msg, text)
	}
}

func showMarketplaceHelp(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	sendPlainTextNoMarkdown(bot, chatID, marketplaceHelpText(getUserRole(userID)))
}

func marketplaceHelpText(role string) string {
	var b strings.Builder
	b.WriteString("🧾 交易行订单与争议帮助\n\n")
	b.WriteString("订单号在哪里看：\n")
	b.WriteString("- 购买成功提示会显示订单号。\n")
	b.WriteString("- 私聊发送“我的购买”或点击“资产交易 -> 我的购买”，可查看最近购买记录和订单号。\n\n")
	b.WriteString("买家：\n")
	b.WriteString("- 我的购买：查看最近购买记录，已购卡密可自助取回。\n")
	b.WriteString("- 举报订单 订单ID 原因：提交异常订单争议，例如“举报订单 12 卡密无法使用”。\n\n")
	b.WriteString("卖家：\n")
	b.WriteString("- 我的交易行：查看自己上架的商品和商品ID。\n")
	b.WriteString("- 交易行订单 商品ID：查看某个商品最近成交订单，例如“交易行订单 8”。")
	if role == "admin" || role == "super_admin" {
		b.WriteString("\n\n管理员：\n")
		b.WriteString("- 查交易订单 订单ID：只读查看订单和争议记录，例如“查交易订单 12”。")
	}
	return b.String()
}

func startMarketplaceListingWizard(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, session *SessionState) {
	if session == nil {
		return
	}
	session.SetStep("WAITING_MARKET_TYPE")
	sendPlainText(bot, msg.Chat.ID, "🛒 交易行上架\n\n请选择上架方式：\n1. 自由上架：需达到筑基期，自定义商品名和卡密，成交后自动发卡密。\n2. 从背包上架：锁定乾坤袋物品，成交后自动转入买家背包。\n\n请发送 `1` 或 `2`。\n发送“取消”可退出。")
}

func parseMarketplaceSessionInt(session *SessionState, key string, minValue, maxValue int) (int, error) {
	raw := strings.TrimSpace(session.GetTemp(key))
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid marketplace session integer: key=%s value=%s err=%w", formatPlainValue(key), formatPlainValue(raw), err)
	}
	if value < minValue || value > maxValue {
		return 0, fmt.Errorf("marketplace session integer out of range: key=%s value=%d range=%d-%d", formatPlainValue(key), value, minValue, maxValue)
	}
	return value, nil
}

func handleMarketplaceStep(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string, session *SessionState) {
	userID := msg.From.ID
	chatID := msg.Chat.ID

	if text == "取消" {
		clearSession(userID)
		sendUserMainMenu(bot, chatID, "已取消交易行操作。")
		return
	}

	switch session.GetStep() {
	case "WAITING_MARKET_BUY_CONFIRM":
		if text != "确认购买商品" {
			sendPlainText(bot, chatID, "请回复 `确认购买商品` 完成购买，或回复 `取消` 退出。")
			return
		}
		listingID64, err := strconv.ParseUint(session.GetTemp("market_buy_listing_id"), 10, 64)
		if err != nil || listingID64 == 0 {
			clearSession(userID)
			sendPlainText(bot, chatID, "❌ 购买会话异常，请重新发起购买。")
			return
		}
		qty, err := parseMarketplaceSessionInt(session, "market_buy_quantity", 1, marketplaceMaxBuyQuantity)
		if err != nil {
			clearSession(userID)
			log.Printf("⚠️ 交易行购买确认会话数量异常: buyer=%d listing=%d err=%s", userID, listingID64, formatPlainError(err))
			sendPlainText(bot, chatID, "❌ 购买会话异常，请重新发起购买。")
			return
		}
		clearSession(userID)
		executeMarketplaceBuy(bot, msg, uint(listingID64), qty)

	case "WAITING_MARKET_TYPE":
		switch text {
		case "1", "自由上架":
			allowed, realmName, err := canCreateMarketplaceSecretListing(userID)
			if err != nil {
				log.Printf("❌ 交易行自由上架境界校验失败: seller=%d err=%s", userID, formatPlainError(err))
				sendPlainText(bot, chatID, "❌ 暂时无法校验修仙境界，请稍后再试。")
				return
			}
			if !allowed {
				sendPlainText(bot, chatID, fmt.Sprintf("❌ 自由卡密上架需达到筑基期后方可使用。\n\n当前境界：%s\n\n道友可先潜心修行并完成突破；从背包上架不受此限制。", realmName))
				return
			}
			session.SetTemp("market_type", marketplaceTypeSecret)
			session.SetStep("WAITING_MARKET_NAME")
			sendPlainText(bot, chatID, "自由上架：请发送商品名称，"+marketplaceSecretListingNameRequirementText+"。")
		case "2", "从背包上架", "背包上架":
			session.SetTemp("market_type", marketplaceTypeInventory)
			session.SetStep("WAITING_MARKET_INVENTORY_ITEM")
			sendMarketplaceInventoryChoices(bot, chatID, userID)
		default:
			sendPlainText(bot, chatID, "请选择上架方式：发送 `1` 自由上架，或发送 `2` 从背包上架。")
		}

	case "WAITING_MARKET_NAME":
		name := strings.TrimSpace(text)
		if !validMarketplaceSecretListingName(name) {
			sendPlainText(bot, chatID, "商品名称需为 "+marketplaceSecretListingNameRequirementText+"，请重新发送：")
			return
		}
		session.SetTemp("market_name", name)
		session.SetStep("WAITING_MARKET_PRICE")
		sendPlainText(bot, chatID, "第二步：请发送商品价格，范围 1-100000 积分。")

	case "WAITING_MARKET_INVENTORY_ITEM":
		itemName := strings.TrimSpace(text)
		if !validMarketplaceInventoryItemName(itemName) {
			sendPlainText(bot, chatID, "物品名称需为 "+marketplaceInventoryItemNameRequirementText+"，请重新发送：")
			return
		}
		var inv Inventory
		if err := DB.Where("user_id = ? AND item_name = ? AND quantity > 0", userID, itemName).First(&inv).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				sendPlainText(bot, chatID, "乾坤袋中没有该物品或数量不足，请重新发送物品名。")
			} else {
				log.Printf("⚠️ 交易行背包上架物品读取失败: seller=%d item=%s err=%s", userID, formatPlainValue(itemName), formatPlainError(err))
				sendPlainText(bot, chatID, "❌ 乾坤袋读取失败，请稍后重试。")
			}
			return
		}
		session.SetTemp("market_item_name", inv.ItemName)
		session.SetStep("WAITING_MARKET_INVENTORY_QUANTITY")
		sendPlainText(bot, chatID, marketplaceInventoryQuantityPrompt(inv.ItemName, inv.Quantity))

	case "WAITING_MARKET_INVENTORY_QUANTITY":
		qty, err := strconv.Atoi(text)
		if err != nil || qty < 1 || qty > marketplaceMaxInventoryUnits {
			sendPlainText(bot, chatID, fmt.Sprintf("数量需为 1-%d 的整数，请重新发送：", marketplaceMaxInventoryUnits))
			return
		}
		itemName := session.GetTemp("market_item_name")
		var inv Inventory
		if err := DB.Where("user_id = ? AND item_name = ? AND quantity >= ?", userID, itemName, qty).First(&inv).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				sendPlainText(bot, chatID, "乾坤袋库存不足，请重新发送较小数量。")
			} else {
				log.Printf("⚠️ 交易行背包上架数量库存读取失败: seller=%d item=%s qty=%d err=%s", userID, formatPlainValue(itemName), qty, formatPlainError(err))
				sendPlainText(bot, chatID, "❌ 乾坤袋库存读取失败，请稍后重试。")
			}
			return
		}
		session.SetTemp("market_inventory_quantity", strconv.Itoa(qty))
		session.SetTemp("market_name", itemName)
		session.SetStep("WAITING_MARKET_PRICE")
		sendPlainText(bot, chatID, fmt.Sprintf("将从乾坤袋锁定【%s】x%d。\n\n请发送每个商品单位的价格，范围 1-100000 积分。", marketplaceVisibleItemName(itemName), qty))

	case "WAITING_MARKET_PRICE":
		price, err := strconv.Atoi(text)
		if err != nil || price < 1 || price > marketplaceMaxPrice {
			sendPlainText(bot, chatID, "价格需为 1-100000 的整数，请重新发送：")
			return
		}
		session.SetTemp("market_price", strconv.Itoa(price))
		if session.GetTemp("market_type") == marketplaceTypeInventory {
			itemName := session.GetTemp("market_item_name")
			qty, err := parseMarketplaceSessionInt(session, "market_inventory_quantity", 1, marketplaceMaxInventoryUnits)
			if err != nil {
				clearSession(userID)
				log.Printf("⚠️ 交易行背包上架会话数量异常: seller=%d item=%s err=%s", userID, formatPlainValue(itemName), formatPlainError(err))
				sendPlainText(bot, chatID, "❌ 上架会话异常，请重新发起交易行操作。")
				return
			}
			listingID, err := createMarketplaceInventoryListing(userID, getTelegramDisplayName(msg.From), itemName, price, qty)
			if err != nil {
				log.Printf("❌ 交易行背包上架失败: seller=%d item=%s qty=%d err=%s", userID, formatPlainValue(itemName), qty, formatPlainError(err))
				sendPlainText(bot, chatID, marketplaceCreateErrorText(err))
				return
			}
			clearSession(userID)
			sendPlainText(bot, chatID, fmt.Sprintf(
				"✅ 已从乾坤袋上架交易行商品 #%d\n\n商品：%s\n单价：%d 积分\n库存：%d\n\n买家可私聊发送：购买商品 %d",
				listingID,
				marketplaceVisibleItemName(itemName),
				price,
				qty,
				listingID,
			))
			notifyMarketplaceListingCreated(bot, listingID, marketplaceTypeInventory, itemName, price, qty, getTelegramDisplayName(msg.From))
			return
		}
		session.SetStep("WAITING_MARKET_SECRETS")
		sendPlainText(bot, chatID, fmt.Sprintf("第三步：请逐行发送卡密，最多 %d 条。单条最多 %d 个字，不能包含制表符或其他控制字符。成交后系统会自动分发其中一条给买家。\n\n注意：卡密只会加密保存，列表中只展示脱敏预览。", marketplaceMaxSecretsPerPost, marketplaceMaxSecretLen))

	case "WAITING_MARKET_SECRETS":
		secrets, ok := parseMarketplaceSecrets(text)
		if !ok {
			sendPlainText(bot, chatID, "存在超过 200 个字，或包含制表符/控制字符的卡密，请删减后重新逐行发送。")
			return
		}
		if len(secrets) == 0 {
			sendPlainText(bot, chatID, "未识别到有效卡密，请逐行发送，单条最多 200 个字，且不能包含制表符或控制字符。")
			return
		}
		if len(secrets) > marketplaceMaxSecretsPerPost {
			sendPlainText(bot, chatID, fmt.Sprintf("单次最多上架 %d 条卡密，请减少后重新发送。", marketplaceMaxSecretsPerPost))
			return
		}

		name := session.GetTemp("market_name")
		price, err := parseMarketplaceSessionInt(session, "market_price", marketplaceMinUnitPrice, marketplaceMaxPrice)
		if err != nil {
			clearSession(userID)
			log.Printf("⚠️ 交易行自由上架会话价格异常: seller=%d err=%s", userID, formatPlainError(err))
			sendPlainText(bot, chatID, "❌ 上架会话异常，请重新发起交易行操作。")
			return
		}
		listingID, err := createMarketplaceSecretListing(userID, getTelegramDisplayName(msg.From), name, price, secrets)
		if err != nil {
			log.Printf("❌ 交易行上架失败: seller=%d err=%s", userID, formatPlainError(err))
			sendPlainText(bot, chatID, marketplaceCreateErrorText(err))
			return
		}

		clearSession(userID)
		sendPlainText(bot, chatID, fmt.Sprintf(
			"✅ 已上架交易行商品 #%d\n\n商品：%s\n价格：%d 积分\n库存：%d\n\n买家可私聊发送：购买商品 %d",
			listingID,
			marketplaceVisibleItemName(name),
			price,
			len(secrets),
			listingID,
		))
		notifyMarketplaceListingCreated(bot, listingID, marketplaceTypeSecret, name, price, len(secrets), getTelegramDisplayName(msg.From))
	}
}

func parseMarketplaceSecrets(text string) ([]string, bool) {
	lines := strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	seen := make(map[string]bool)
	for _, line := range lines {
		code := strings.TrimSpace(line)
		if code == "" {
			continue
		}
		if !validMarketplaceSecretCode(code) {
			return nil, false
		}
		if seen[code] {
			continue
		}
		seen[code] = true
		out = append(out, code)
	}
	return out, true
}

func createMarketplaceSecretListing(sellerID int64, sellerName string, name string, price int, secrets []string) (uint, error) {
	name = strings.TrimSpace(name)
	normalizedSecrets, ok := normalizeMarketplaceSecrets(secrets)
	if sellerID == 0 || !validMarketplaceSecretListingName(name) || price < marketplaceMinUnitPrice || price > marketplaceMaxPrice || !ok {
		return 0, errInvalidMarketplaceListing
	}

	var listingID uint
	err := DB.Transaction(func(tx *gorm.DB) error {
		var txListingID uint
		var seller User
		if err := tx.Select("telegram_id", "username").
			Where("telegram_id = ?", sellerID).
			First(&seller).Error; err != nil {
			return err
		}
		if strings.TrimSpace(sellerName) == "" {
			sellerName = seller.Username
		}
		if err := ensureMarketplaceSecretListingRealmTx(tx, sellerID); err != nil {
			return err
		}

		classifiedSecrets := make([]marketplaceSecretClassification, 0, len(normalizedSecrets))
		for _, raw := range normalizedSecrets {
			codeHash := hashSensitiveToken(raw)
			if codeHash == "" {
				return errSecurityPepperNotConfigured
			}
			classified, err := classifyMarketplaceSecretTx(tx, codeHash)
			if err != nil {
				return err
			}
			classifiedSecrets = append(classifiedSecrets, classified)
		}
		secretSource, err := marketplaceSecretListingSource(classifiedSecrets)
		if err != nil {
			return err
		}
		codeHashes := make([]string, 0, len(classifiedSecrets))
		for _, classified := range classifiedSecrets {
			codeHashes = append(codeHashes, classified.CodeHash)
		}
		var duplicateCount int64
		if err := tx.Model(&MarketplaceSecret{}).
			Where("code_hash IN ? AND status IN ?", codeHashes, []string{marketplaceSecretAvailable, marketplaceSecretSold}).
			Count(&duplicateCount).Error; err != nil {
			return err
		}
		if duplicateCount > 0 {
			return errMarketplaceDuplicateSecret
		}

		listing := MarketplaceListing{
			SellerID:     sellerID,
			SellerName:   marketplaceStoredSellerName(sellerName),
			Name:         name,
			ListingType:  marketplaceTypeSecret,
			SecretSource: secretSource,
			UnitQuantity: 1,
			Price:        price,
			Status:       marketplaceStatusActive,
			ExpiresAt:    marketplaceListingExpiresAtPtr(time.Now()),
		}
		if err := createMarketplaceListingInTx(tx, &listing); err != nil {
			return err
		}
		txListingID = listing.ID

		for i, raw := range normalizedSecrets {
			codeEnc, err := encryptMarketplaceSecret(raw)
			if err != nil {
				return err
			}
			if err := createMarketplaceSecretInTx(tx, &MarketplaceSecret{
				ListingID:   listing.ID,
				SellerID:    sellerID,
				CodeHash:    classifiedSecrets[i].CodeHash,
				CodeEnc:     codeEnc,
				Preview:     maskSecret(raw),
				TokenSource: classifiedSecrets[i].Source,
				TokenRefID:  classifiedSecrets[i].RefID,
				Status:      marketplaceSecretAvailable,
			}); err != nil {
				return err
			}
		}
		listingID = txListingID
		return nil
	})
	if err != nil {
		return 0, err
	}
	return listingID, nil
}

func normalizeMarketplaceSecrets(secrets []string) ([]string, bool) {
	if len(secrets) == 0 {
		return nil, false
	}
	normalized := make([]string, 0, len(secrets))
	seen := make(map[string]bool, len(secrets))
	for _, raw := range secrets {
		code := strings.TrimSpace(raw)
		if !validMarketplaceSecretCode(code) {
			return nil, false
		}
		if seen[code] {
			continue
		}
		seen[code] = true
		normalized = append(normalized, code)
	}
	if len(normalized) == 0 {
		return nil, false
	}
	if len(normalized) > marketplaceMaxSecretsPerPost {
		return nil, false
	}
	return normalized, true
}

type marketplaceSecretClassification struct {
	CodeHash string
	Source   string
	RefID    uint
}

func classifyMarketplaceSecretTx(tx *gorm.DB, codeHash string) (marketplaceSecretClassification, error) {
	if codeHash == "" {
		return marketplaceSecretClassification{}, errSecurityPepperNotConfigured
	}

	var invite InviteCode
	if err := tx.Select("id", "is_used").
		Where("code_hash = ?", codeHash).
		First(&invite).Error; err == nil {
		if invite.IsUsed {
			return marketplaceSecretClassification{}, errMarketplaceVerifiedInvalid
		}
		return marketplaceSecretClassification{CodeHash: codeHash, Source: marketplaceSecretSourceBotInvite, RefID: invite.ID}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return marketplaceSecretClassification{}, err
	}

	var renew RenewCode
	if err := tx.Select("id", "is_used").
		Where("code_hash = ?", codeHash).
		First(&renew).Error; err == nil {
		if renew.IsUsed {
			return marketplaceSecretClassification{}, errMarketplaceVerifiedInvalid
		}
		return marketplaceSecretClassification{CodeHash: codeHash, Source: marketplaceSecretSourceBotRenew, RefID: renew.ID}, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return marketplaceSecretClassification{}, err
	}

	return marketplaceSecretClassification{}, errMarketplaceUnverifiedSecret
}

func marketplaceSecretListingSource(secrets []marketplaceSecretClassification) (string, error) {
	if len(secrets) == 0 {
		return "", errInvalidMarketplaceListing
	}
	source := marketplaceSecretSource(secrets[0].Source)
	if source == marketplaceSecretSourceThirdParty {
		return "", errMarketplaceUnverifiedSecret
	}
	for _, secret := range secrets[1:] {
		if marketplaceSecretSource(secret.Source) != source {
			return "", errMarketplaceMixedSecretSource
		}
	}
	return source, nil
}

func marketplaceSecretSource(source string) string {
	switch source {
	case marketplaceSecretSourceBotInvite, marketplaceSecretSourceBotRenew:
		return source
	default:
		return marketplaceSecretSourceThirdParty
	}
}

func marketplaceSecretVerificationLine(listingType string, source string) string {
	if listingType != marketplaceTypeSecret && listingType != "" {
		return ""
	}
	label := marketplaceSecretVerificationText(source)
	if label == "" {
		return ""
	}
	return "卡密来源：" + escapeMarkdown(label) + "\n"
}

func marketplaceSecretVerificationText(source string) string {
	switch marketplaceSecretSource(source) {
	case marketplaceSecretSourceBotInvite:
		return "系统已校验 · 邀请码"
	case marketplaceSecretSourceBotRenew:
		return "系统已校验 · 续期卡"
	default:
		return "三方卡密 · 未校验"
	}
}

func validMarketplaceSecretCode(code string) bool {
	code = strings.TrimSpace(code)
	if code == "" || len([]rune(code)) > marketplaceMaxSecretLen {
		return false
	}
	return !containsDisallowedControl(code, false)
}

func canCreateMarketplaceSecretListing(userID int64) (bool, string, error) {
	var cul Cultivation
	err := DB.Where("user_id = ?", userID).First(&cul).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		cul = Cultivation{UserID: userID, MajorRealm: 0, MinorRealm: 1}
		return false, GetRealmName(&cul), nil
	}
	if err != nil {
		return false, "", err
	}
	return meetsMarketplaceSecretListingRealm(cul.MajorRealm), GetRealmName(&cul), nil
}

func meetsMarketplaceSecretListingRealm(majorRealm int) bool {
	return majorRealm >= marketplaceSecretListingMinMajorRealm
}

func ensureMarketplaceSecretListingRealmTx(tx *gorm.DB, sellerID int64) error {
	var cul Cultivation
	err := tx.Select("user_id", "major_realm", "minor_realm").
		Where("user_id = ?", sellerID).
		First(&cul).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errMarketplaceRealmTooLow
	}
	if err != nil {
		return err
	}
	if !meetsMarketplaceSecretListingRealm(cul.MajorRealm) {
		return errMarketplaceRealmTooLow
	}
	return nil
}

func validateMarketplaceSecrets(secrets []string) bool {
	_, ok := normalizeMarketplaceSecrets(secrets)
	return ok
}

func createMarketplaceListingInTx(tx *gorm.DB, listing *MarketplaceListing) error {
	if tx == nil || listing == nil {
		return fmt.Errorf("MARKETPLACE_LISTING_INVALID")
	}
	entry := *listing
	entry.SellerName = marketplaceDisplayText(entry.SellerName, marketplaceDisplayNameMaxLen, "-")
	entry.Name = marketplaceDisplayText(entry.Name, marketplaceMaxNameLen, "-")
	entry.Description = formatPlainValue(entry.Description)
	entry.ListingType = formatPlainValue(entry.ListingType)
	entry.SecretSource = formatPlainValue(entry.SecretSource)
	entry.ItemName = marketplaceDisplayText(entry.ItemName, marketplaceMaxNameLen, "")
	entry.Status = formatPlainValue(entry.Status)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("MARKETPLACE_LISTING_CREATE_MISSED")
	}
	*listing = entry
	return nil
}

func createMarketplaceSecretInTx(tx *gorm.DB, secret *MarketplaceSecret) error {
	if tx == nil || secret == nil {
		return fmt.Errorf("MARKETPLACE_SECRET_INVALID")
	}
	entry := *secret
	entry.Preview = formatPlainValue(entry.Preview)
	entry.TokenSource = formatPlainValue(entry.TokenSource)
	entry.Status = formatPlainValue(entry.Status)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("MARKETPLACE_SECRET_CREATE_MISSED")
	}
	*secret = entry
	return nil
}

func createMarketplaceDisputeInTx(tx *gorm.DB, dispute *MarketplaceDispute) error {
	if tx == nil || dispute == nil {
		return fmt.Errorf("MARKETPLACE_DISPUTE_INVALID")
	}
	entry := *dispute
	entry.Reason = formatPlainValue(entry.Reason)
	entry.Status = formatPlainValue(entry.Status)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("MARKETPLACE_DISPUTE_CREATE_MISSED")
	}
	*dispute = entry
	return nil
}

func validMarketplaceSecretListingName(name string) bool {
	name = strings.TrimSpace(name)
	nameLen := len([]rune(name))
	if nameLen < marketplaceMinSecretListingNameLen || nameLen > marketplaceMaxNameLen {
		return false
	}
	return !containsDisallowedControl(name, false)
}

func validMarketplaceInventoryItemName(name string) bool {
	name = strings.TrimSpace(name)
	nameLen := len([]rune(name))
	if nameLen == 0 || nameLen > marketplaceMaxNameLen {
		return false
	}
	return !containsDisallowedControl(name, false)
}

func createMarketplaceInventoryListing(sellerID int64, sellerName string, itemName string, price int, quantity int) (uint, error) {
	itemName = strings.TrimSpace(itemName)
	if sellerID == 0 || !validMarketplaceInventoryItemName(itemName) || price < marketplaceMinUnitPrice || price > marketplaceMaxPrice || quantity <= 0 || quantity > marketplaceMaxInventoryUnits {
		return 0, errInvalidMarketplaceListing
	}

	var listingID uint
	err := DB.Transaction(func(tx *gorm.DB) error {
		var txListingID uint
		var seller User
		if err := tx.Select("telegram_id", "username").
			Where("telegram_id = ?", sellerID).
			First(&seller).Error; err != nil {
			return err
		}
		if strings.TrimSpace(sellerName) == "" {
			sellerName = seller.Username
		}

		res := tx.Model(&Inventory{}).
			Where("user_id = ? AND item_name = ? AND quantity >= ?", sellerID, itemName, quantity).
			UpdateColumn("quantity", gorm.Expr("quantity - ?", quantity))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errMarketplaceInventoryNotEnough
		}

		listing := MarketplaceListing{
			SellerID:     sellerID,
			SellerName:   marketplaceStoredSellerName(sellerName),
			Name:         itemName,
			ListingType:  marketplaceTypeInventory,
			ItemName:     itemName,
			UnitQuantity: 1,
			Price:        price,
			Status:       marketplaceStatusActive,
			ExpiresAt:    marketplaceListingExpiresAtPtr(time.Now()),
		}
		if err := createMarketplaceListingInTx(tx, &listing); err != nil {
			return err
		}
		txListingID = listing.ID

		for i := 0; i < quantity; i++ {
			if err := createMarketplaceSecretInTx(tx, &MarketplaceSecret{
				ListingID: listing.ID,
				SellerID:  sellerID,
				CodeHash:  fmt.Sprintf("inventory:%d:%d:%d", sellerID, listing.ID, i+1),
				CodeEnc:   marketplaceTypeInventory,
				Preview:   itemName,
				Status:    marketplaceSecretAvailable,
			}); err != nil {
				return err
			}
		}
		listingID = txListingID
		return nil
	})
	if err != nil {
		return 0, err
	}
	return listingID, nil
}

func sendMarketplaceInventoryChoices(bot *tgbotapi.BotAPI, chatID int64, userID int64) {
	var items []Inventory
	if err := DB.Where("user_id = ? AND quantity > 0", userID).Order("item_name asc").Find(&items).Error; err != nil {
		sendPlainText(bot, chatID, "❌ 读取乾坤袋失败，请稍后再试。")
		return
	}
	if len(items) == 0 {
		sendPlainText(bot, chatID, "乾坤袋中暂无可上架物品。")
		return
	}

	var b strings.Builder
	b.WriteString("从背包上架：请发送要上架的物品名称，" + marketplaceInventoryItemNameRequirementText + "。\n\n当前乾坤袋：\n")
	for _, item := range items {
		line := fmt.Sprintf("- %s x%d", marketplaceVisibleItemName(item.ItemName), item.Quantity)
		if effect := pillEffectSummary(item.ItemName); effect != "" {
			line += "｜" + effect
		}
		b.WriteString(line + "\n")
	}
	sendPlainText(bot, chatID, strings.TrimSpace(b.String()))
}

func marketplaceErrorCode(err error) string {
	switch {
	case errors.Is(err, errInvalidMarketplaceListing):
		return "INVALID_MARKETPLACE_LISTING"
	case errors.Is(err, errMarketplaceDuplicateSecret):
		return "MARKETPLACE_DUPLICATE_SECRET"
	case errors.Is(err, errMarketplaceInventoryNotEnough):
		return "MARKETPLACE_INVENTORY_NOT_ENOUGH"
	case errors.Is(err, errMarketplaceListingNotFound):
		return "MARKETPLACE_LISTING_NOT_FOUND"
	case errors.Is(err, errMarketplaceSelfBuy):
		return "MARKETPLACE_SELF_BUY"
	case errors.Is(err, errMarketplaceOutOfStock):
		return "MARKETPLACE_OUT_OF_STOCK"
	case errors.Is(err, errMarketplaceQuantityTooLarge):
		return "MARKETPLACE_QUANTITY_TOO_LARGE"
	case errors.Is(err, errMarketplaceInvalidPrice):
		return "MARKETPLACE_INVALID_PRICE"
	case errors.Is(err, errMarketplaceInvalidType):
		return "MARKETPLACE_INVALID_TYPE"
	case errors.Is(err, errMarketplaceRealmTooLow):
		return "MARKETPLACE_REALM_TOO_LOW"
	case errors.Is(err, errMarketplaceMixedSecretSource):
		return "MARKETPLACE_MIXED_SECRET_SOURCE"
	case errors.Is(err, errMarketplaceVerifiedInvalid):
		return "MARKETPLACE_VERIFIED_SECRET_INVALID"
	case errors.Is(err, errMarketplaceUnverifiedSecret):
		return "MARKETPLACE_UNVERIFIED_SECRET"
	case errors.Is(err, errPointsNotEnough):
		return "POINTS_NOT_ENOUGH"
	case errors.Is(err, errSecurityPepperNotConfigured):
		return "SECURITY_PEPPER_NOT_CONFIGURED"
	case err != nil:
		if code := knownMarketplaceErrorCode(err.Error()); code != "" {
			return code
		}
		return "UNKNOWN"
	default:
		return ""
	}
}

func knownMarketplaceErrorCode(code string) string {
	switch code {
	case "INVALID_MARKETPLACE_LISTING",
		"MARKETPLACE_DUPLICATE_SECRET",
		"MARKETPLACE_INVENTORY_NOT_ENOUGH",
		"MARKETPLACE_LISTING_NOT_FOUND",
		"MARKETPLACE_SELF_BUY",
		"MARKETPLACE_OUT_OF_STOCK",
		"MARKETPLACE_QUANTITY_TOO_LARGE",
		"MARKETPLACE_INVALID_PRICE",
		"MARKETPLACE_INVALID_TYPE",
		"MARKETPLACE_REALM_TOO_LOW",
		"MARKETPLACE_MIXED_SECRET_SOURCE",
		"MARKETPLACE_VERIFIED_SECRET_INVALID",
		"MARKETPLACE_UNVERIFIED_SECRET",
		"POINTS_NOT_ENOUGH",
		"SECURITY_PEPPER_NOT_CONFIGURED":
		return code
	default:
		return ""
	}
}

func marketplaceCreateErrorText(err error) string {
	if errors.Is(err, errMarketplaceMixedSecretSource) {
		return "❌ 同一件自由卡密商品不能混放邀请码和续期卡，请拆分后分别上架。"
	}
	if errors.Is(err, errMarketplaceUnverifiedSecret) {
		return "❌ 交易行仅允许上架 Bot 生成的邀请码或续期卡，未识别卡密不可上架。"
	}
	if err == nil {
		return "❌ 上架失败，请稍后再试。"
	}
	if isUniqueConstraintError(err) {
		return "❌ 存在已上架或已售出的重复卡密，请更换后重试。"
	}
	switch marketplaceErrorCode(err) {
	case "MARKETPLACE_INVENTORY_NOT_ENOUGH":
		return "❌ 乾坤袋库存不足，上架失败。"
	case "SECURITY_PEPPER_NOT_CONFIGURED":
		return "❌ 系统安全密钥未配置，无法保存卡密。"
	case "MARKETPLACE_DUPLICATE_SECRET":
		return "❌ 存在已上架或已售出的重复卡密，请更换后重试。"
	case "MARKETPLACE_REALM_TOO_LOW":
		return "❌ 自由卡密上架需达到筑基期后方可使用。"
	case "MARKETPLACE_MIXED_SECRET_SOURCE":
		return "❌ 同一件自由卡密商品不能混放 Bot 产出卡密和三方卡密，请拆分后分别上架。"
	case "MARKETPLACE_VERIFIED_SECRET_INVALID":
		return "❌ Bot 产出卡密已被使用或当前不可用，请更换后再上架。"
	default:
		return "❌ 上架失败，请稍后再试。"
	}
}

func showMarketplaceListings(bot *tgbotapi.BotAPI, chatID int64, text string) {
	filter := parseMarketplaceListFilter(text)
	if !filter.Valid {
		sendPlainText(bot, chatID, fmt.Sprintf("交易行筛选关键词需为 1-%d 个字，且不能包含换行、制表符或控制/分隔字符。", marketplaceMaxFilterLen))
		return
	}
	var listings []MarketplaceListing
	query := marketplaceActiveListingQuery(DB, time.Now()).
		Where(marketplaceListingHasStockCondition(), marketplaceSecretAvailable).
		Where(marketplaceListingAllowedTypeCondition(), marketplaceTypeSecret, marketplaceTypeInventory)
	switch filter.Kind {
	case marketplaceTypeSecret:
		query = query.Where("listing_type = ? OR listing_type = ''", marketplaceTypeSecret)
	case marketplaceTypeInventory:
		query = query.Where("listing_type = ?", marketplaceTypeInventory)
	}
	if filter.Keyword != "" {
		like := marketplaceLikePattern(filter.Keyword)
		query = query.Where("(name LIKE ? ESCAPE '\\' OR item_name LIKE ? ESCAPE '\\')", like, like)
	}

	if err := query.Order("updated_at DESC").Limit(10).Find(&listings).Error; err != nil {
		log.Printf("⚠️ 交易行列表读取失败: chat=%d kind=%s keyword=%s err=%s", chatID, formatPlainValue(filter.Kind), formatPlainValue(filter.Keyword), formatPlainError(err))
		sendPlainText(bot, chatID, "❌ 查询交易行失败，请稍后再试。")
		return
	}

	if len(listings) == 0 {
		sendPlainText(bot, chatID, "🛒 交易行暂时没有在售商品。\n\n私聊发送“上架商品”可以寄售卡密。")
		return
	}

	var b strings.Builder
	title := marketplaceListTitle(filter)
	b.WriteString(title + "\n\n")
	for _, listing := range listings {
		stock, stockErr := countMarketplaceListingStock(listing.ID)
		if stockErr != nil {
			log.Printf("⚠️ 查询交易行库存失败: listing=%d err=%s", listing.ID, formatPlainError(stockErr))
			b.WriteString(fmt.Sprintf(
				"#%d 【%s】\n类型：%s｜价格：%d 积分｜库存：读取失败｜卖家：%s\n%s%s剩余：%s\n\n",
				listing.ID,
				marketplaceMarkdownItemName(listing.Name),
				marketplaceTypeText(listing.ListingType),
				listing.Price,
				escapeMarkdown(displayMarketplaceSeller(listing)),
				marketplaceListingPillEffectLine(listing.ListingType, listing.Name),
				marketplaceSecretVerificationLine(listing.ListingType, listing.SecretSource),
				escapeMarkdown(marketplaceListingRemainingText(listing, time.Now())),
			))
			continue
		}
		if stock <= 0 {
			continue
		}
		b.WriteString(fmt.Sprintf(
			"#%d 【%s】\n类型：%s｜价格：%d 积分｜库存：%d｜卖家：%s\n%s%s剩余：%s\n发送：购买商品 %d\n\n",
			listing.ID,
			marketplaceMarkdownItemName(listing.Name),
			marketplaceTypeText(listing.ListingType),
			listing.Price,
			stock,
			escapeMarkdown(displayMarketplaceSeller(listing)),
			marketplaceListingPillEffectLine(listing.ListingType, listing.Name),
			marketplaceSecretVerificationLine(listing.ListingType, listing.SecretSource),
			escapeMarkdown(marketplaceListingRemainingText(listing, time.Now())),
			listing.ID,
		))
	}

	outText := strings.TrimSpace(b.String())
	if outText == title {
		outText = "🛒 交易行暂时没有有库存的在售商品。"
	}
	replyText(bot, chatID, outText)
}

func notifyMarketplaceListingCreated(bot *tgbotapi.BotAPI, listingID uint, listingType string, name string, price int, stock int, sellerName string) {
	if bot == nil || AppConfig == nil || AppConfig.NoticeGroupID == 0 || listingID == 0 {
		return
	}

	secretSource := ""
	if listingType == marketplaceTypeSecret || listingType == "" {
		var listing MarketplaceListing
		if err := DB.Select("secret_source").Where("id = ?", listingID).First(&listing).Error; err == nil {
			secretSource = listing.SecretSource
		} else {
			log.Printf("⚠️ 交易行上架群提醒读取卡密来源失败: listing=%d err=%s", listingID, formatPlainError(err))
			return
		}
	}
	text := formatMarketplaceListingGroupNotice(listingID, listingType, name, price, stock, sellerName, secretSource)
	msg := tgbotapi.NewMessage(AppConfig.NoticeGroupID, text)
	msg.ParseMode = "Markdown"
	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("交易行上架群提醒发送失败: listing=%d chat=%d err=%s", listingID, AppConfig.NoticeGroupID, formatTelegramSendError(err))
	}
}

func formatMarketplaceListingGroupNotice(listingID uint, listingType string, name string, price int, stock int, sellerName string, secretSource string) string {
	if price < 0 {
		price = 0
	}
	if stock < 0 {
		stock = 0
	}

	seller := marketplaceDisplayText(sellerName, marketplaceDisplayNameMaxLen, "神秘道友")
	return fmt.Sprintf(
		"🛒 **【交易行新货上架】**\n\n"+
			"商品：%s\n"+
			"%s"+
			"%s"+
			"类型：%s\n"+
			"单价：`%d` 积分\n"+
			"库存：`%d`\n"+
			"有效期：`48小时`\n"+
			"卖家：%s\n\n"+
			"查看：`交易行详情 %d`\n"+
			"购买：私聊 Bot 发送 `购买商品 %d`",
		marketplaceMarkdownItemName(name),
		marketplaceListingPillEffectLine(listingType, name),
		marketplaceSecretVerificationLine(listingType, secretSource),
		escapeMarkdown(marketplaceTypeText(listingType)),
		price,
		stock,
		escapeMarkdown(seller),
		listingID,
		listingID,
	)
}

func marketplaceListingHasStockCondition() string {
	return "EXISTS (SELECT 1 FROM marketplace_secrets ms WHERE ms.listing_id = marketplace_listings.id AND ms.status = ? AND ms.deleted_at IS NULL)"
}

func marketplaceListingAllowedTypeCondition() string {
	return "(listing_type = ? OR listing_type = ? OR listing_type = '')"
}

type marketplaceListFilter struct {
	Kind    string
	Keyword string
	Label   string
	Valid   bool
}

func marketplaceListTitle(filter marketplaceListFilter) string {
	title := "🛒 交易行在售商品"
	if filter.Label != "" {
		title += " · " + escapeMarkdown(filter.Label)
	}
	return title
}

func marketplaceLikePattern(keyword string) string {
	return "%" + escapeMarketplaceLikeKeyword(keyword) + "%"
}

func escapeMarketplaceLikeKeyword(keyword string) string {
	var b strings.Builder
	for _, r := range keyword {
		switch r {
		case '\\', '%', '_':
			b.WriteRune('\\')
		}
		b.WriteRune(r)
	}
	return b.String()
}

func parseMarketplaceListFilter(text string) marketplaceListFilter {
	text = strings.TrimSpace(text)
	text = strings.TrimPrefix(text, "交易行列表")
	text = strings.TrimPrefix(text, "交易行")
	raw := strings.TrimSpace(text)
	if raw == "" {
		return marketplaceListFilter{Valid: true}
	}

	switch raw {
	case "卡密", "自由", "自由卡密":
		return marketplaceListFilter{Kind: marketplaceTypeSecret, Label: "自由卡密", Valid: true}
	case "背包", "背包物品":
		return marketplaceListFilter{Kind: marketplaceTypeInventory, Label: "背包物品", Valid: true}
	case "丹药", "灵草", "种子":
		return marketplaceListFilter{Kind: marketplaceTypeInventory, Keyword: raw, Label: raw, Valid: true}
	default:
		if !validMarketplaceFilterKeyword(raw) {
			return marketplaceListFilter{}
		}
		return marketplaceListFilter{Keyword: raw, Label: raw, Valid: true}
	}
}

func validMarketplaceFilterKeyword(keyword string) bool {
	keyword = strings.TrimSpace(keyword)
	keywordLen := len([]rune(keyword))
	if keywordLen == 0 || keywordLen > marketplaceMaxFilterLen {
		return false
	}
	if containsDisallowedControl(keyword, false) {
		return false
	}
	return true
}

func showMyMarketplaceListings(bot *tgbotapi.BotAPI, chatID int64, sellerID int64) {
	var listings []MarketplaceListing
	if err := DB.Where("seller_id = ?", sellerID).
		Order("updated_at DESC").
		Limit(20).
		Find(&listings).Error; err != nil {
		log.Printf("⚠️ 我的交易行列表读取失败: seller=%d err=%s", sellerID, formatPlainError(err))
		sendPlainText(bot, chatID, "❌ 查询我的交易行失败，请稍后再试。")
		return
	}
	if len(listings) == 0 {
		sendPlainText(bot, chatID, "你还没有上架过商品。")
		return
	}

	var b strings.Builder
	b.WriteString("🧾 我的交易行\n\n")
	now := time.Now()
	for _, listing := range listings {
		stock, stockErr := countMarketplaceListingStock(listing.ID)
		if stockErr != nil {
			log.Printf("⚠️ 查询我的交易行库存失败: seller=%d listing=%d err=%s", sellerID, listing.ID, formatPlainError(stockErr))
		}
		b.WriteString(fmt.Sprintf(
			"#%d 【%s】｜%s｜%s\n价格：%d｜库存：%s｜已售：%d\n有效期：%s\n",
			listing.ID,
			marketplaceMarkdownItemName(listing.Name),
			marketplaceTypeText(listing.ListingType),
			marketplaceEffectiveStatusText(listing, now),
			listing.Price,
			marketplaceStockText(stock, stockErr == nil),
			listing.SoldCount,
			escapeMarkdown(marketplaceListingRemainingText(listing, now)),
		))
		if listing.Status == marketplaceStatusActive {
			b.WriteString(fmt.Sprintf("下架：下架商品 %d\n", listing.ID))
		}
		b.WriteString("\n")
	}
	replyText(bot, chatID, strings.TrimSpace(b.String()))
}

func handleMarketplaceDetail(bot *tgbotapi.BotAPI, chatID int64, text string) {
	id, ok := parseMarketplaceID(text, "交易行详情")
	if !ok {
		sendPlainText(bot, chatID, "用法：交易行详情 商品ID")
		return
	}

	var listing MarketplaceListing
	if err := DB.First(&listing, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendPlainText(bot, chatID, "❌ 未找到该交易行商品。")
		} else {
			log.Printf("⚠️ 交易行详情商品读取失败: listing=%d chat=%d err=%s", id, chatID, formatPlainError(err))
			sendPlainText(bot, chatID, "❌ 商品状态读取失败，请稍后重试。")
		}
		return
	}
	stock, stockErr := countMarketplaceListingStock(listing.ID)
	stockText := marketplaceStockText(stock, stockErr == nil)
	if stockErr != nil {
		log.Printf("⚠️ 查询交易行详情库存失败: listing=%d err=%s", listing.ID, formatPlainError(stockErr))
	}
	now := time.Now()
	replyText(bot, chatID, fmt.Sprintf(
		"🛒 交易行商品 #%d\n\n商品：%s\n%s%s类型：%s\n价格：%d 积分\n状态：%s\n库存：%s\n已售：%d\n有效期：%s\n卖家：%s\n\n%s",
		listing.ID,
		marketplaceMarkdownItemName(listing.Name),
		marketplaceListingPillEffectLine(listing.ListingType, listing.Name),
		marketplaceSecretVerificationLine(listing.ListingType, listing.SecretSource),
		marketplaceTypeText(listing.ListingType),
		listing.Price,
		marketplaceEffectiveStatusText(listing, now),
		stockText,
		listing.SoldCount,
		escapeMarkdown(marketplaceListingRemainingText(listing, now)),
		escapeMarkdown(displayMarketplaceSeller(listing)),
		marketplaceDetailActionTextWithStockStatus(listing, stock, stockErr == nil),
	))
}

func marketplacePillEffectLine(itemName string) string {
	if effect := pillEffectSummary(itemName); effect != "" {
		return "功效：" + escapeMarkdown(effect) + "\n"
	}
	return ""
}

func marketplaceListingPillEffectLine(listingType string, itemName string) string {
	if listingType != marketplaceTypeInventory {
		return ""
	}
	return marketplacePillEffectLine(itemName)
}

func marketplaceDetailActionText(listing MarketplaceListing, stock int64) string {
	return marketplaceDetailActionTextWithStockStatus(listing, stock, true)
}

func marketplaceDetailActionTextWithStockStatus(listing MarketplaceListing, stock int64, stockAvailable bool) string {
	if _, err := marketplaceListingTypeForPurchase(listing.ListingType); err != nil {
		return "该商品类型异常，暂不可购买，请联系管理员核查。"
	}
	if listing.Status == marketplaceStatusReview {
		return "该商品数据异常，已暂停交易并等待管理员核查。"
	}
	if listing.Status != marketplaceStatusActive {
		return "该商品已下架，无法购买。"
	}
	if marketplaceListingExpired(listing, time.Now()) {
		return "该商品已到期，系统将自动下架，无法购买。"
	}
	if !stockAvailable {
		return "库存状态暂不可用，请稍后再试。"
	}
	if listing.Status == marketplaceStatusActive && stock > 0 {
		return fmt.Sprintf("购买请私聊发送：购买商品 %d", listing.ID)
	}
	if listing.Status == marketplaceStatusActive {
		return "该商品当前库存不足，暂不可购买。"
	}
	return "该商品暂不可购买。"
}

func handleMarketplaceBuy(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) {
	if !msg.Chat.IsPrivate() {
		sendPlainText(bot, msg.Chat.ID, "购买商品请私聊 Bot 执行。")
		return
	}

	listingID, buyQty, ok := parseMarketplaceBuyCommand(text)
	if !ok {
		sendPlainText(bot, msg.Chat.ID, "用法：购买商品 商品ID [数量]")
		return
	}
	if buyQty > marketplaceMaxBuyQuantity {
		sendPlainText(bot, msg.Chat.ID, marketplaceBuyQuantityTooLargeText())
		return
	}

	if shouldConfirmMarketplaceBuy(listingID, buyQty) {
		var listing MarketplaceListing
		if err := marketplaceActiveListingQuery(DB, time.Now()).Where("id = ?", listingID).First(&listing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				sendPlainText(bot, msg.Chat.ID, "❌ 商品不存在或已下架。")
			} else {
				log.Printf("⚠️ 交易行购买确认商品读取失败: buyer=%d listing=%d err=%s", msg.From.ID, listingID, formatPlainError(err))
				sendPlainText(bot, msg.Chat.ID, "❌ 商品状态读取失败，请稍后重试。")
			}
			return
		}
		stock, stockErr := countMarketplaceListingStock(listingID)
		if stockErr != nil {
			log.Printf("❌ 交易行购买确认库存查询失败: buyer=%d listing=%d err=%s", msg.From.ID, listingID, formatPlainError(stockErr))
			sendPlainText(bot, msg.Chat.ID, "❌ 商品库存状态暂不可用，请稍后重试。")
			return
		}
		if int64(buyQty) > stock {
			sendPlainText(bot, msg.Chat.ID, "❌ 商品库存不足。")
			return
		}
		confirmText, err := marketplaceBuyConfirmText(listing, buyQty, stock, msg.From.ID)
		if err != nil {
			if marketplaceBuyErrorShouldLog(err) {
				log.Printf("❌ 交易行购买确认失败: buyer=%d listing=%d err=%s", msg.From.ID, listingID, formatPlainError(err))
			}
			sendPlainText(bot, msg.Chat.ID, marketplaceBuyErrorText(err))
			return
		}
		session := getSession(msg.From.ID)
		session.SetTemp("market_buy_listing_id", fmt.Sprintf("%d", listingID))
		session.SetTemp("market_buy_quantity", fmt.Sprintf("%d", buyQty))
		session.SetStep("WAITING_MARKET_BUY_CONFIRM")
		sendPlainText(bot, msg.Chat.ID, confirmText)
		return
	}

	executeMarketplaceBuy(bot, msg, listingID, buyQty)
}

func executeMarketplaceBuy(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, listingID uint, buyQty int) {
	result, err := purchaseMarketplaceListing(msg.From.ID, listingID, buyQty)
	if err != nil {
		if errors.Is(err, errMarketplaceSellerMismatch) {
			handleMarketplaceSellerMismatch(bot, listingID, "purchase")
		}
		if marketplaceBuyErrorShouldLog(err) {
			log.Printf("❌ 交易行购买失败: buyer=%d listing=%d err=%s", msg.From.ID, listingID, formatPlainError(err))
		}
		sendPlainText(bot, msg.Chat.ID, marketplaceBuyErrorText(err))
		return
	}

	if result.Listing.ListingType == marketplaceTypeInventory {
		sendPlainText(bot, msg.Chat.ID, marketplaceInventoryPurchaseSuccessText(result))
	} else {
		sendPlainTextNoMarkdown(bot, msg.Chat.ID, marketplaceSecretPurchaseSuccessText(result))
	}
	if result.Listing.SellerID != 0 {
		sendPlainText(bot, result.Listing.SellerID, marketplaceSellerDealNoticeText(result))
	}
}

func marketplaceBuyQuantityTooLargeText() string {
	return fmt.Sprintf("❌ 单次最多购买 %d 件。", marketplaceMaxBuyQuantity)
}

func marketplaceInventoryPurchaseSuccessText(result marketplacePurchaseResult) string {
	return fmt.Sprintf(
		"✅ 交易行购买成功\n\n商品：%s x%d\n%s扣除：%d 积分\n订单：%s\n\n物品已放入乾坤袋。",
		marketplaceVisibleItemName(result.Listing.Name),
		result.Quantity,
		marketplaceListingPillEffectLine(result.Listing.ListingType, result.Listing.Name),
		result.GrossAmount,
		result.PurchaseIDText(),
	)
}

func marketplaceSecretPurchaseSuccessText(result marketplacePurchaseResult) string {
	return fmt.Sprintf(
		"✅ 交易行购买成功\n\n商品：%s x%d\n%s%s扣除：%d 积分\n订单：%s\n\n卡密：\n%s",
		marketplaceVisibleItemName(result.Listing.Name),
		result.Quantity,
		marketplaceListingPillEffectLine(result.Listing.ListingType, result.Listing.Name),
		marketplaceSecretVerificationLine(result.Listing.ListingType, result.Listing.SecretSource),
		result.GrossAmount,
		result.PurchaseIDText(),
		strings.Join(result.Codes, "\n"),
	)
}

func marketplaceSellerDealNoticeText(result marketplacePurchaseResult) string {
	return fmt.Sprintf(
		"🛒 交易行成交\n\n商品：%s x%d\n%s成交额：%d 积分\n手续费：%d\n实收：%d 积分\n订单：%s",
		marketplaceVisibleItemName(result.Listing.Name),
		result.Quantity,
		marketplaceListingPillEffectLine(result.Listing.ListingType, result.Listing.Name),
		result.GrossAmount,
		result.FeeAmount,
		result.SellerAmount,
		result.PurchaseIDText(),
	)
}

func marketplaceBuyPointDescription(itemName string, quantity int) string {
	return fmt.Sprintf("交易行购买【%s】x%d", marketplaceVisibleItemName(itemName), quantity)
}

func marketplaceSellPointDescription(itemName string, quantity int, feeAmount int) string {
	return fmt.Sprintf("交易行售出【%s】x%d，手续费 %d", marketplaceVisibleItemName(itemName), quantity, feeAmount)
}

func marketplaceBuyErrorText(err error) string {
	if errors.Is(err, errMarketplaceUnverifiedSecret) {
		return "❌ 卡密来源无法通过系统校验，交易已中止，请换其他商品或联系卖家。"
	}
	if err == nil {
		return "❌ 购买失败，请稍后再试。"
	}
	if isUniqueConstraintError(err) {
		return "❌ 商品库存刚刚被其他道友抢先锁定，请刷新交易行后重试。"
	}
	switch marketplaceErrorCode(err) {
	case "MARKETPLACE_LISTING_NOT_FOUND":
		return "❌ 商品不存在或已下架。"
	case "MARKETPLACE_SELF_BUY":
		return "❌ 不能购买自己上架的商品。"
	case "MARKETPLACE_OUT_OF_STOCK":
		return "❌ 商品库存已空。"
	case "MARKETPLACE_QUANTITY_TOO_LARGE":
		return marketplaceBuyQuantityTooLargeText()
	case "MARKETPLACE_INVALID_PRICE":
		return "❌ 商品价格异常，交易已中止。"
	case "MARKETPLACE_INVALID_TYPE":
		return "❌ 商品类型异常，交易已中止。"
	case "MARKETPLACE_SELLER_MISMATCH":
		return "❌ 商品卖家数据异常，已暂停交易并通知管理员核查。"
	case "MARKETPLACE_VERIFIED_SECRET_INVALID":
		return "❌ 系统已校验卡密当前不可用，交易已中止，请换其他商品或联系卖家。"
	case "POINTS_NOT_ENOUGH":
		return "❌ 积分不足，无法购买该商品。"
	default:
		return "❌ 购买失败，请稍后再试。"
	}
}

func marketplaceBuyErrorShouldLog(err error) bool {
	if err == nil {
		return false
	}
	if isUniqueConstraintError(err) {
		return false
	}
	switch marketplaceErrorCode(err) {
	case "MARKETPLACE_LISTING_NOT_FOUND",
		"MARKETPLACE_SELF_BUY",
		"MARKETPLACE_OUT_OF_STOCK",
		"MARKETPLACE_QUANTITY_TOO_LARGE",
		"MARKETPLACE_VERIFIED_SECRET_INVALID",
		"MARKETPLACE_UNVERIFIED_SECRET",
		"POINTS_NOT_ENOUGH":
		return false
	default:
		return true
	}
}

func shouldConfirmMarketplaceBuy(listingID uint, buyQty int) bool {
	if buyQty > 1 {
		return true
	}
	var listing MarketplaceListing
	if err := marketplaceActiveListingQuery(DB.Select("price"), time.Now()).Where("id = ?", listingID).First(&listing).Error; err != nil {
		// 查询失败时按需要二次确认处理（fail-safe）：高价支付确认环节宁可
		// 多弹一次确认，也不要因读取异常而静默跳过。后续真正成交仍会复核。
		return true
	}
	return listing.Price*buyQty >= marketplaceConfirmPrice
}

func marketplaceBuyConfirmText(listing MarketplaceListing, buyQty int, stock int64, buyerID int64) (string, error) {
	if buyerID != 0 && listing.SellerID == buyerID {
		return "", errMarketplaceSelfBuy
	}
	if listing.Price < marketplaceMinUnitPrice || listing.Price > marketplaceMaxPrice {
		return "", errMarketplaceInvalidPrice
	}
	listingType, err := marketplaceListingTypeForPurchase(listing.ListingType)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(
		"🛒 交易行购买确认\n\n商品：%s\n%s%s类型：%s\n单价：%d\n数量：%d\n总价：%d 积分\n库存：%d\n\n确认购买请回复：确认购买商品\n取消请回复：取消",
		marketplaceVisibleItemName(listing.Name),
		marketplaceListingPillEffectLine(listingType, listing.Name),
		marketplaceSecretVerificationLine(listingType, listing.SecretSource),
		marketplaceTypeText(listingType),
		listing.Price,
		buyQty,
		listing.Price*buyQty,
		stock,
	), nil
}

func marketplaceVerifiedSecretUsableTx(tx *gorm.DB, secret MarketplaceSecret) (bool, error) {
	switch marketplaceSecretSource(secret.TokenSource) {
	case marketplaceSecretSourceBotInvite:
		if secret.TokenRefID == 0 || secret.CodeHash == "" {
			return false, nil
		}
		var count int64
		if err := tx.Model(&InviteCode{}).
			Where("id = ? AND code_hash = ? AND is_used = ?", secret.TokenRefID, secret.CodeHash, false).
			Count(&count).Error; err != nil {
			return false, err
		}
		return count > 0, nil
	case marketplaceSecretSourceBotRenew:
		if secret.TokenRefID == 0 || secret.CodeHash == "" {
			return false, nil
		}
		var count int64
		if err := tx.Model(&RenewCode{}).
			Where("id = ? AND code_hash = ? AND is_used = ?", secret.TokenRefID, secret.CodeHash, false).
			Count(&count).Error; err != nil {
			return false, err
		}
		return count > 0, nil
	default:
		return true, nil
	}
}

type marketplacePurchaseResult struct {
	Codes        []string
	Listing      MarketplaceListing
	PurchaseIDs  []uint
	Quantity     int
	GrossAmount  int
	FeeAmount    int
	SellerAmount int
}

func (r marketplacePurchaseResult) PurchaseIDText() string {
	if len(r.PurchaseIDs) == 0 {
		return "-"
	}
	parts := make([]string, 0, len(r.PurchaseIDs))
	for _, id := range r.PurchaseIDs {
		parts = append(parts, fmt.Sprintf("#%d", id))
	}
	return strings.Join(parts, ", ")
}

func createMarketplacePurchaseInTx(tx *gorm.DB, purchase *MarketplacePurchase) error {
	if tx == nil || purchase == nil {
		return fmt.Errorf("MARKETPLACE_PURCHASE_INVALID")
	}
	entry := *purchase
	entry.ItemName = marketplaceDisplayText(entry.ItemName, marketplaceMaxNameLen, "-")
	entry.CodePreview = formatPlainValue(entry.CodePreview)
	res := tx.Create(&entry)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return fmt.Errorf("MARKETPLACE_PURCHASE_CREATE_MISSED")
	}
	purchase.ID = entry.ID
	return nil
}

func purchaseMarketplaceListing(buyerID int64, listingID uint, buyQty int) (marketplacePurchaseResult, error) {
	if buyQty <= 0 {
		buyQty = 1
	}
	if buyQty > marketplaceMaxBuyQuantity {
		return marketplacePurchaseResult{}, errMarketplaceQuantityTooLarge
	}

	var listing MarketplaceListing
	codes := make([]string, 0, buyQty)
	purchaseIDs := make([]uint, 0, buyQty)
	totalQuantity := 0
	grossAmount := 0
	feeAmount := 0
	sellerAmount := 0
	invalidSecretID := uint(0)

	runTx := func(tx *gorm.DB) error {
		if err := marketplaceActiveListingQuery(tx, time.Now()).Where("id = ?", listingID).
			First(&listing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errMarketplaceListingNotFound
			}
			return err
		}
		if err := validateMarketplaceListingSellerConsistencyTx(tx, listing); err != nil {
			return err
		}
		if listing.SellerID == buyerID {
			return errMarketplaceSelfBuy
		}

		if listing.Price < marketplaceMinUnitPrice || listing.Price > marketplaceMaxPrice {
			return errMarketplaceInvalidPrice
		}
		listingType, err := marketplaceListingTypeForPurchase(listing.ListingType)
		if err != nil {
			return err
		}
		listing.ListingType = listingType
		if listing.UnitQuantity <= 0 {
			listing.UnitQuantity = 1
		}

		secrets := make([]MarketplaceSecret, 0, buyQty)
		for len(secrets) < buyQty {
			var secret MarketplaceSecret
			if err := tx.Where("listing_id = ? AND status = ?", listingID, marketplaceSecretAvailable).
				Order("id ASC").
				First(&secret).Error; err != nil {
				if errors.Is(err, gorm.ErrRecordNotFound) {
					return errMarketplaceOutOfStock
				}
				return err
			}
			if listing.ListingType == marketplaceTypeSecret {
				if secret.TokenSource == "" {
					secret.TokenSource = listing.SecretSource
				}
				if marketplaceSecretSource(secret.TokenSource) == marketplaceSecretSourceThirdParty {
					invalidSecretID = secret.ID
					return errMarketplaceUnverifiedSecret
				}
				usable, err := marketplaceVerifiedSecretUsableTx(tx, secret)
				if err != nil {
					return err
				}
				if !usable {
					invalidSecretID = secret.ID
					return errMarketplaceVerifiedInvalid
				}
			}

			now := time.Now()
			res := tx.Model(&MarketplaceSecret{}).
				Where("id = ? AND status = ?", secret.ID, marketplaceSecretAvailable).
				Updates(map[string]interface{}{
					"status":   marketplaceSecretSold,
					"buyer_id": buyerID,
					"sold_at":  &now,
				})
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected > 0 {
				secrets = append(secrets, secret)
			}
		}

		totalQuantity = listing.UnitQuantity * buyQty
		grossAmount = listing.Price * buyQty
		feeAmount = calculateMarketplaceFee(grossAmount)
		sellerAmount = grossAmount - feeAmount

		if listing.ListingType == marketplaceTypeInventory {
			itemName := strings.TrimSpace(listing.ItemName)
			if itemName == "" {
				itemName = listing.Name
			}
			if err := gardenGrantInventoryInTx(tx, buyerID, itemName, totalQuantity); err != nil {
				return err
			}
		} else {
			for _, secret := range secrets {
				plainCode, err := decryptMarketplaceSecret(secret.CodeEnc)
				if err != nil {
					return err
				}
				codes = append(codes, plainCode)
			}
		}

		if err := applyPointDeltaInTx(
			tx,
			buyerID,
			-grossAmount,
			"marketplace_buy",
			marketplaceBuyPointDescription(listing.Name, buyQty),
			"marketplace",
			fmt.Sprintf("%d", listing.ID),
		); err != nil {
			return err
		}

		if sellerAmount > 0 {
			if err := applyPointDeltaInTx(
				tx,
				listing.SellerID,
				sellerAmount,
				"marketplace_sell",
				marketplaceSellPointDescription(listing.Name, buyQty, feeAmount),
				"marketplace",
				fmt.Sprintf("%d", listing.ID),
			); err != nil {
				return err
			}
		}

		if feeAmount > 0 {
			if _, _, err := addPointsToFusionPoolInTx(tx, feeAmount); err != nil {
				return err
			}
		}

		remainingFee := feeAmount
		for i, secret := range secrets {
			remainingItems := len(secrets) - i
			rowFee := 0
			if remainingFee > 0 && remainingItems > 0 {
				rowFee = remainingFee / remainingItems
				if remainingFee%remainingItems != 0 {
					rowFee++
				}
				remainingFee -= rowFee
			}
			rowSellerAmount := listing.Price - rowFee
			purchase := MarketplacePurchase{
				ListingID:    listing.ID,
				SecretID:     secret.ID,
				SellerID:     listing.SellerID,
				BuyerID:      buyerID,
				ItemName:     listing.Name,
				DeliveryType: listing.ListingType,
				Quantity:     listing.UnitQuantity,
				Price:        listing.Price,
				GrossAmount:  listing.Price,
				FeeAmount:    rowFee,
				SellerAmount: rowSellerAmount,
				Status:       "paid",
				CodePreview:  secret.Preview,
			}
			if err := createMarketplacePurchaseInTx(tx, &purchase); err != nil {
				return err
			}
			purchaseIDs = append(purchaseIDs, purchase.ID)
		}

		res := tx.Model(&MarketplaceListing{}).
			Where("id = ? AND status = ?", listing.ID, marketplaceStatusActive).
			UpdateColumn("sold_count", gorm.Expr("sold_count + ?", buyQty))
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("MARKETPLACE_SOLD_COUNT_UPDATE_MISSED")
		}

		var remainingAvailable int64
		if err := tx.Model(&MarketplaceSecret{}).
			Where("listing_id = ? AND status = ?", listing.ID, marketplaceSecretAvailable).
			Count(&remainingAvailable).Error; err != nil {
			return err
		}
		if remainingAvailable == 0 {
			closeRes := tx.Model(&MarketplaceListing{}).
				Where("id = ? AND status = ?", listing.ID, marketplaceStatusActive).
				Update("status", marketplaceStatusClosed)
			if closeRes.Error != nil {
				return closeRes.Error
			}
			if closeRes.RowsAffected == 0 {
				return fmt.Errorf("MARKETPLACE_SOLD_OUT_CLOSE_MISSED")
			}
		}
		return nil
	}

	err := runFusionPoolLockedTransaction(runTx)
	if err != nil {
		if invalidSecretID != 0 && (errors.Is(err, errMarketplaceVerifiedInvalid) || errors.Is(err, errMarketplaceUnverifiedSecret)) {
			if quarantineErr := quarantineMarketplaceInvalidSecret(DB, listingID, invalidSecretID); quarantineErr != nil {
				log.Printf("⚠️ 交易行异常卡密暂停商品失败: listing=%d secret=%d err=%s", listingID, invalidSecretID, formatPlainError(quarantineErr))
			}
		}
		return marketplacePurchaseResult{}, err
	}

	return marketplacePurchaseResult{
		Codes:        codes,
		Listing:      listing,
		PurchaseIDs:  purchaseIDs,
		Quantity:     totalQuantity,
		GrossAmount:  grossAmount,
		FeeAmount:    feeAmount,
		SellerAmount: sellerAmount,
	}, nil
}

func quarantineMarketplaceInvalidSecret(db *gorm.DB, listingID uint, secretID uint) error {
	if db == nil || listingID == 0 || secretID == 0 {
		return errMarketplaceListingNotFound
	}
	return db.Transaction(func(tx *gorm.DB) error {
		secretRes := tx.Model(&MarketplaceSecret{}).
			Where("id = ? AND listing_id = ? AND status = ?", secretID, listingID, marketplaceSecretAvailable).
			Update("status", marketplaceSecretClosed)
		if secretRes.Error != nil {
			return secretRes.Error
		}

		listingRes := tx.Model(&MarketplaceListing{}).
			Where("id = ? AND status = ?", listingID, marketplaceStatusActive).
			Update("status", marketplaceStatusReview)
		if listingRes.Error != nil {
			return listingRes.Error
		}
		if listingRes.RowsAffected == 0 && secretRes.RowsAffected == 0 {
			return errMarketplaceListingNotFound
		}
		return nil
	})
}

func showMyMarketplacePurchases(bot *tgbotapi.BotAPI, chatID int64, buyerID int64) {
	var purchases []MarketplacePurchase
	if err := DB.Where("buyer_id = ?", buyerID).
		Order("created_at DESC").
		Limit(10).
		Find(&purchases).Error; err != nil {
		log.Printf("⚠️ 我的交易行购买记录读取失败: buyer=%d err=%s", buyerID, formatPlainError(err))
		sendPlainText(bot, chatID, "❌ 查询购买记录失败，请稍后再试。")
		return
	}
	if len(purchases) == 0 {
		sendPlainText(bot, chatID, "你还没有交易行购买记录。")
		return
	}

	var b strings.Builder
	b.WriteString("📦 我的交易行购买\n\n")
	for _, purchase := range purchases {
		itemName := marketplaceDisplayText(purchase.ItemName, marketplaceDisplayNameMaxLen, "-")
		gross := marketplacePurchaseGrossAmount(purchase)
		deliveryType, err := marketplacePurchaseDeliveryType(purchase.DeliveryType)
		if err != nil {
			b.WriteString(fmt.Sprintf(
				"订单 #%d｜%s｜%d 积分\n交付：类型异常，请联系管理员核查订单。\n\n",
				purchase.ID,
				itemName,
				gross,
			))
			continue
		}
		if deliveryType == marketplaceTypeInventory {
			qty := purchase.Quantity
			if qty <= 0 {
				qty = 1
			}
			b.WriteString(fmt.Sprintf(
				"订单 #%d｜%s x%d｜%d 积分\n交付：已放入乾坤袋\n\n",
				purchase.ID,
				itemName,
				qty,
				gross,
			))
			continue
		}

		var secret MarketplaceSecret
		codeText := "卡密读取失败，请联系管理员核查订单。"
		if err := marketplacePurchaseSecretQuery(DB, purchase, buyerID).First(&secret).Error; err == nil {
			if code, err := decryptMarketplaceSecret(secret.CodeEnc); err == nil {
				codeText = code
			} else {
				log.Printf("⚠️ 交易行我的购买卡密解密失败: order=%d listing=%d secret=%d buyer=%d err=%s", purchase.ID, purchase.ListingID, purchase.SecretID, buyerID, formatPlainError(err))
			}
		} else {
			log.Printf("⚠️ 交易行我的购买卡密读取失败: order=%d listing=%d secret=%d buyer=%d err=%s", purchase.ID, purchase.ListingID, purchase.SecretID, buyerID, formatPlainError(err))
		}
		b.WriteString(fmt.Sprintf(
			"订单 #%d｜%s｜%d 积分\n卡密：%s\n\n",
			purchase.ID,
			itemName,
			gross,
			codeText,
		))
	}
	sendPlainTextNoMarkdown(bot, chatID, strings.TrimSpace(b.String()))
}

func marketplacePurchaseSecretQuery(db *gorm.DB, purchase MarketplacePurchase, buyerID int64) *gorm.DB {
	where, args := marketplacePurchaseSecretWhere(purchase, buyerID)
	return db.Where(where, args...)
}

func marketplacePurchaseDeliveryType(deliveryType string) (string, error) {
	return marketplaceListingTypeForPurchase(deliveryType)
}

func marketplacePurchaseSecretWhere(purchase MarketplacePurchase, buyerID int64) (string, []interface{}) {
	return "id = ? AND listing_id = ? AND buyer_id = ? AND status = ?", []interface{}{
		purchase.SecretID,
		purchase.ListingID,
		buyerID,
		marketplaceSecretSold,
	}
}

func handleMarketplaceListingOrders(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) {
	listingID, ok := parseMarketplaceID(text, "交易行订单")
	if !ok {
		sendPlainText(bot, msg.Chat.ID, "用法：交易行订单 商品ID")
		return
	}

	var listing MarketplaceListing
	if err := DB.Where("id = ? AND seller_id = ?", listingID, msg.From.ID).First(&listing).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendPlainText(bot, msg.Chat.ID, "❌ 未找到本人交易行商品。")
		} else {
			log.Printf("⚠️ 交易行商品订单读取商品失败: seller=%d listing=%d err=%s", msg.From.ID, listingID, formatPlainError(err))
			sendPlainText(bot, msg.Chat.ID, "❌ 商品订单状态读取失败，请稍后重试。")
		}
		return
	}

	var purchases []MarketplacePurchase
	if err := DB.Where("listing_id = ?", listingID).Order("created_at DESC").Limit(20).Find(&purchases).Error; err != nil {
		log.Printf("⚠️ 交易行商品订单列表读取失败: seller=%d listing=%d err=%s", msg.From.ID, listingID, formatPlainError(err))
		sendPlainText(bot, msg.Chat.ID, "❌ 查询交易行订单失败，请稍后再试。")
		return
	}
	if len(purchases) == 0 {
		sendPlainText(bot, msg.Chat.ID, "该商品暂无成交订单。")
		return
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("🧾 交易行订单 · #%d %s\n\n", listing.ID, marketplaceVisibleItemName(listing.Name)))
	for _, purchase := range purchases {
		b.WriteString(formatMarketplacePurchaseLine(purchase, false))
	}
	sendPlainTextNoMarkdown(bot, msg.Chat.ID, strings.TrimSpace(b.String()))
}

func handleMarketplaceAdminOrderQuery(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) {
	if getUserRole(msg.From.ID) != "super_admin" && getUserRole(msg.From.ID) != "admin" {
		sendPlainText(bot, msg.Chat.ID, "❌ 权限不足。")
		return
	}

	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "查交易订单"))
	orderID, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || orderID == 0 {
		sendPlainText(bot, msg.Chat.ID, "用法：查交易订单 订单ID")
		return
	}

	var purchase MarketplacePurchase
	if err := DB.First(&purchase, uint(orderID)).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendPlainText(bot, msg.Chat.ID, "❌ 未找到该交易订单。")
		} else {
			log.Printf("⚠️ 管理员查询交易订单读取失败: admin=%d order=%d err=%s", msg.From.ID, orderID, formatPlainError(err))
			sendPlainText(bot, msg.Chat.ID, "❌ 交易订单读取失败，请稍后重试。")
		}
		return
	}

	var disputes []MarketplaceDispute
	disputeErr := DB.Where("purchase_id = ?", purchase.ID).Order("created_at DESC").Limit(5).Find(&disputes).Error
	if disputeErr != nil {
		log.Printf("⚠️ 交易订单争议记录读取失败: purchase=%d admin=%d err=%s", purchase.ID, msg.From.ID, formatPlainError(disputeErr))
	}

	var b strings.Builder
	b.WriteString("🧾 交易订单详情\n\n")
	b.WriteString(formatMarketplacePurchaseLine(purchase, true))
	if disputeErr != nil {
		b.WriteString("\n争议记录：读取失败，请稍后重试。\n")
	} else if len(disputes) > 0 {
		b.WriteString("\n争议记录：\n")
		for _, dispute := range disputes {
			b.WriteString(fmt.Sprintf("- #%d｜%s｜%s\n",
				dispute.ID,
				marketplaceDisputeStatusText(dispute.Status),
				marketplaceDisplayText(dispute.Reason, marketplaceDisplayPreviewMaxLen, "-"),
			))
		}
	}
	sendPlainTextNoMarkdown(bot, msg.Chat.ID, strings.TrimSpace(b.String()))
}

func handleMarketplaceDispute(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) {
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "举报订单"))
	parts := strings.Fields(raw)
	if len(parts) < 2 {
		sendPlainText(bot, msg.Chat.ID, "用法：举报订单 订单ID 原因")
		return
	}
	orderID, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || orderID == 0 {
		sendPlainText(bot, msg.Chat.ID, "用法：举报订单 订单ID 原因")
		return
	}
	reason := strings.TrimSpace(strings.TrimPrefix(raw, parts[0]))
	if !validMarketplaceDisputeReason(reason) {
		sendPlainText(bot, msg.Chat.ID, "举报原因需为 3-200 个字，且不能包含换行、制表符或控制字符。")
		return
	}

	var purchase MarketplacePurchase
	if err := DB.Where("id = ? AND buyer_id = ?", uint(orderID), msg.From.ID).First(&purchase).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			sendPlainText(bot, msg.Chat.ID, "❌ 未找到你的该笔交易订单。")
		} else {
			log.Printf("⚠️ 交易争议订单读取失败: buyer=%d order=%d err=%s", msg.From.ID, orderID, formatPlainError(err))
			sendPlainText(bot, msg.Chat.ID, "❌ 交易订单读取失败，请稍后重试。")
		}
		return
	}
	hasOpenDispute, err := hasOpenMarketplaceDispute(DB, purchase.ID, msg.From.ID)
	if err != nil {
		log.Printf("⚠️ 查询交易争议失败: purchase=%d buyer=%d err=%s", purchase.ID, msg.From.ID, formatPlainError(err))
		sendPlainText(bot, msg.Chat.ID, "❌ 举报状态查询失败，请稍后再试。")
		return
	}
	if hasOpenDispute {
		sendPlainText(bot, msg.Chat.ID, "该订单已有处理中争议，请等待管理员处理。")
		return
	}

	dispute := MarketplaceDispute{
		PurchaseID: purchase.ID,
		ListingID:  purchase.ListingID,
		SellerID:   purchase.SellerID,
		BuyerID:    purchase.BuyerID,
		Reason:     reason,
		Status:     marketplaceDisputeOpen,
	}
	if err := createMarketplaceDisputeInTx(DB, &dispute); err != nil {
		if isUniqueConstraintError(err) {
			sendPlainText(bot, msg.Chat.ID, "该订单已有处理中争议，请等待管理员处理。")
			return
		}
		sendPlainText(bot, msg.Chat.ID, "❌ 举报提交失败，请稍后再试。")
		return
	}
	sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("✅ 已提交交易争议 #%d，管理员可用 `查交易订单 %d` 查看。", dispute.ID, purchase.ID))
}

func hasOpenMarketplaceDispute(db *gorm.DB, purchaseID uint, buyerID int64) (bool, error) {
	if db == nil || purchaseID == 0 || buyerID == 0 {
		return false, nil
	}
	var count int64
	if err := db.Model(&MarketplaceDispute{}).
		Where("purchase_id = ? AND buyer_id = ? AND status = ?", purchaseID, buyerID, marketplaceDisputeOpen).
		Count(&count).Error; err != nil {
		return false, err
	}
	return count > 0, nil
}

func validMarketplaceDisputeReason(reason string) bool {
	reason = strings.TrimSpace(reason)
	reasonLen := len([]rune(reason))
	if reasonLen < marketplaceMinDisputeReasonLen || reasonLen > marketplaceMaxDisputeReasonLen {
		return false
	}
	return !containsDisallowedControl(reason, false)
}

func formatMarketplacePurchaseLine(purchase MarketplacePurchase, adminView bool) string {
	qty := purchase.Quantity
	if qty <= 0 {
		qty = 1
	}
	gross := marketplacePurchaseGrossAmount(purchase)
	feeAmount := marketplacePurchaseFeeAmount(purchase, gross)
	sellerAmount := marketplacePurchaseSellerAmount(purchase, gross)
	line := fmt.Sprintf(
		"订单 #%d｜商品 #%d｜%s x%d\n买家：%d｜卖家：%d\n成交：%d｜手续费：%d｜卖家实收：%d｜状态：%s\n",
		purchase.ID,
		purchase.ListingID,
		marketplaceDisplayText(purchase.ItemName, marketplaceDisplayNameMaxLen, "-"),
		qty,
		purchase.BuyerID,
		purchase.SellerID,
		gross,
		feeAmount,
		sellerAmount,
		marketplacePurchaseStatusText(purchase.Status),
	)
	if adminView {
		line += fmt.Sprintf("类型：%s｜卡密预览：%s\n",
			marketplaceTypeText(purchase.DeliveryType),
			marketplaceDisplayText(purchase.CodePreview, marketplaceDisplayPreviewMaxLen, "-"),
		)
	}
	return line + "\n"
}

func marketplaceDisplayText(text string, maxLen int, fallback string) string {
	text = strings.Map(func(r rune) rune {
		if containsDisallowedControl(string(r), false) {
			return ' '
		}
		return r
	}, text)
	text = strings.Join(strings.Fields(text), " ")
	if text == "" {
		return fallback
	}
	return truncateRunes(text, maxLen)
}

func marketplacePurchaseGrossAmount(purchase MarketplacePurchase) int {
	if purchase.GrossAmount > 0 {
		return purchase.GrossAmount
	}
	qty := purchase.Quantity
	if qty <= 0 {
		qty = 1
	}
	gross := purchase.Price * qty
	if gross < 0 {
		return 0
	}
	return gross
}

func marketplacePurchaseFeeAmount(purchase MarketplacePurchase, gross int) int {
	if gross < 0 {
		gross = 0
	}
	if purchase.FeeAmount < 0 {
		return 0
	}
	if purchase.FeeAmount > gross {
		return gross
	}
	return purchase.FeeAmount
}

func marketplacePurchaseSellerAmount(purchase MarketplacePurchase, gross int) int {
	if gross < 0 {
		gross = 0
	}
	if purchase.SellerAmount > 0 {
		if purchase.SellerAmount > gross {
			return gross
		}
		return purchase.SellerAmount
	}
	amount := gross - marketplacePurchaseFeeAmount(purchase, gross)
	if amount < 0 {
		return 0
	}
	if amount > gross {
		return gross
	}
	return amount
}

func marketplaceVisibleItemName(name string) string {
	return marketplaceDisplayText(name, marketplaceDisplayNameMaxLen, "-")
}

func marketplaceMarkdownItemName(name string) string {
	return escapeMarkdown(marketplaceVisibleItemName(name))
}

func marketplaceListingTypeForPurchase(listingType string) (string, error) {
	switch listingType {
	case "", marketplaceTypeSecret:
		return marketplaceTypeSecret, nil
	case marketplaceTypeInventory:
		return marketplaceTypeInventory, nil
	default:
		return "", errMarketplaceInvalidType
	}
}

func marketplaceInventoryQuantityPrompt(itemName string, quantity int) string {
	return fmt.Sprintf(
		"当前【%s】可上架 `%d` 个。\n\n请发送本次上架数量，范围 1-%d：",
		marketplaceVisibleItemName(itemName),
		quantity,
		minInt(quantity, marketplaceMaxInventoryUnits),
	)
}

func marketplaceStoredSellerName(name string) string {
	return marketplaceDisplayText(name, marketplaceDisplayNameMaxLen, "")
}

func sendPlainTextNoMarkdown(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("发送 Telegram 纯文本消息失败: chat=%d err=%s", chatID, formatTelegramSendError(err))
	}
}

func handleMarketplaceClose(bot *tgbotapi.BotAPI, msg *tgbotapi.Message, text string) {
	listingID, ok := parseMarketplaceID(text, "下架商品")
	if !ok {
		sendPlainText(bot, msg.Chat.ID, "用法：下架商品 商品ID")
		return
	}

	refundQty, err := closeMarketplaceListingInTx(DB, msg.From.ID, listingID)
	if err != nil {
		if errors.Is(err, errMarketplaceCloseNotFound) {
			sendPlainText(bot, msg.Chat.ID, "❌ 未找到可下架的本人商品。")
			return
		}
		if errors.Is(err, errMarketplaceSellerMismatch) {
			handleMarketplaceSellerMismatch(bot, listingID, "manual_close")
			sendPlainText(bot, msg.Chat.ID, "❌ 商品卖家数据异常，已暂停交易并通知管理员核查。")
			return
		}
		if errors.Is(err, errMarketplaceInvalidType) {
			sendPlainText(bot, msg.Chat.ID, "❌ 商品类型异常，已中止下架，请联系管理员核查。")
			return
		}
		log.Printf("⚠️ 交易行商品下架失败: seller=%d listing=%d err=%s", msg.From.ID, listingID, formatPlainError(err))
		sendPlainText(bot, msg.Chat.ID, "❌ 下架失败，请稍后再试。")
		return
	}
	if refundQty > 0 {
		sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("✅ 已下架交易行商品 #%d，未售出的背包物品 x%d 已退回乾坤袋。", listingID, refundQty))
		return
	}
	sendPlainText(bot, msg.Chat.ID, fmt.Sprintf("✅ 已下架交易行商品 #%d，未售出的卡密不会继续出售。", listingID))
}

func closeMarketplaceListingInTx(db *gorm.DB, sellerID int64, listingID uint) (int, error) {
	return closeMarketplaceListingScoped(db, sellerID, listingID, true)
}

func closeMarketplaceListingByID(db *gorm.DB, listingID uint) (int, error) {
	return closeMarketplaceListingScoped(db, 0, listingID, false)
}

type marketplaceSellerGroup struct {
	SellerID int64
	Count    int64
}

func validateMarketplaceListingSellerConsistencyTx(tx *gorm.DB, listing MarketplaceListing) error {
	if tx == nil || listing.ID == 0 || listing.SellerID == 0 {
		return errMarketplaceSellerMismatch
	}

	var groups []marketplaceSellerGroup
	if err := tx.Model(&MarketplaceSecret{}).
		Select("seller_id, COUNT(*) AS count").
		Where("listing_id = ?", listing.ID).
		Group("seller_id").
		Find(&groups).Error; err != nil {
		return err
	}
	if !marketplaceSellerGroupsMatchListing(listing.SellerID, groups) {
		log.Printf("⚠️ 交易行商品卖家数据不一致: listing=%d listing_seller=%d secret_seller_groups=%s",
			listing.ID, listing.SellerID, marketplaceSecretSellerGroupsLogText(groups))
		return errMarketplaceSellerMismatch
	}
	return nil
}

func marketplaceSellerGroupsMatchListing(listingSellerID int64, groups []marketplaceSellerGroup) bool {
	return listingSellerID != 0 &&
		len(groups) == 1 &&
		groups[0].SellerID == listingSellerID &&
		groups[0].Count > 0
}

func marketplaceSecretSellerGroupsLogText(groups []marketplaceSellerGroup) string {
	if len(groups) == 0 {
		return "none"
	}
	parts := make([]string, 0, len(groups))
	for _, group := range groups {
		parts = append(parts, fmt.Sprintf("%d:%d", group.SellerID, group.Count))
	}
	return strings.Join(parts, ",")
}

func handleMarketplaceSellerMismatch(bot *tgbotapi.BotAPI, listingID uint, context string) {
	if err := quarantineMarketplaceListingForReview(DB, listingID); err != nil {
		log.Printf("⚠️ 暂停异常交易行商品失败: listing=%d context=%s err=%s", listingID, formatPlainValue(context), formatPlainError(err))
		return
	}
	log.Printf("⚠️ 已暂停异常交易行商品: listing=%d context=%s", listingID, formatPlainValue(context))
	notifySuperAdminsPlain(bot, fmt.Sprintf(
		"⚠️ 交易行商品 #%d 卖家数据不一致，已暂停交易。\n\n场景：%s\n请核查 marketplace_listings.seller_id 与 marketplace_secrets.seller_id 后人工处理库存/积分。",
		listingID,
		formatPlainValue(context),
	))
}

func quarantineMarketplaceListingForReview(db *gorm.DB, listingID uint) error {
	if db == nil {
		return fmt.Errorf("DB_NOT_READY")
	}
	if listingID == 0 {
		return errMarketplaceListingNotFound
	}
	res := db.Model(&MarketplaceListing{}).
		Where("id = ? AND status = ?", listingID, marketplaceStatusActive).
		Update("status", marketplaceStatusReview)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errMarketplaceListingNotFound
	}
	return nil
}

func closeMarketplaceListingScoped(db *gorm.DB, sellerID int64, listingID uint, requireSeller bool) (int, error) {
	if db == nil {
		return 0, fmt.Errorf("DB_NOT_READY")
	}

	refundQty := 0
	err := db.Transaction(func(tx *gorm.DB) error {
		txRefundQty := 0
		var listing MarketplaceListing
		query := tx.Where("id = ? AND status = ?", listingID, marketplaceStatusActive)
		if requireSeller {
			query = query.Where("seller_id = ?", sellerID)
		}
		if err := query.First(&listing).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errMarketplaceCloseNotFound
			}
			return err
		}
		if err := validateMarketplaceListingSellerConsistencyTx(tx, listing); err != nil {
			return err
		}
		listingType, err := marketplaceListingTypeForClose(listing.ListingType)
		if err != nil {
			return err
		}
		listing.ListingType = listingType

		update := tx.Model(&MarketplaceListing{}).
			Where("id = ? AND status = ?", listingID, marketplaceStatusActive)
		if requireSeller {
			update = update.Where("seller_id = ?", sellerID)
		}
		res := update.Update("status", marketplaceStatusClosed)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return errMarketplaceCloseNotFound
		}

		var availableCount int64
		if err := tx.Model(&MarketplaceSecret{}).
			Where("listing_id = ? AND status = ?", listing.ID, marketplaceSecretAvailable).
			Count(&availableCount).Error; err != nil {
			return err
		}
		if listing.ListingType == marketplaceTypeInventory {
			unitQty := listing.UnitQuantity
			if unitQty <= 0 {
				unitQty = 1
			}
			txRefundQty = int(availableCount) * unitQty
			if txRefundQty > 0 {
				itemName := strings.TrimSpace(listing.ItemName)
				if itemName == "" {
					itemName = listing.Name
				}
				if err := gardenGrantInventoryInTx(tx, listing.SellerID, itemName, txRefundQty); err != nil {
					return err
				}
			}
		}
		secretRes := tx.Model(&MarketplaceSecret{}).
			Where("listing_id = ? AND status = ?", listing.ID, marketplaceSecretAvailable).
			Update("status", marketplaceClosedSecretStatus())
		if secretRes.Error != nil {
			return secretRes.Error
		}
		if secretRes.RowsAffected != availableCount {
			return fmt.Errorf("marketplace close available units changed: listing=%d expected=%d actual=%d", listing.ID, availableCount, secretRes.RowsAffected)
		}
		refundQty = txRefundQty
		return nil
	})
	if err != nil {
		return 0, err
	}
	return refundQty, nil
}

func StartMarketplaceExpiryScheduler(bot *tgbotapi.BotAPI) {
	go func() {
		closeExpiredMarketplaceListings(bot, time.Now())

		ticker := time.NewTicker(marketplaceExpirySweepInterval)
		defer ticker.Stop()
		for now := range ticker.C {
			closeExpiredMarketplaceListings(bot, now)
		}
	}()

	log.Println("✅ 交易行自动下架调度器已启动：每分钟巡检过期商品")
}

func closeExpiredMarketplaceListings(bot *tgbotapi.BotAPI, now time.Time) {
	if DB == nil {
		return
	}

	var listings []MarketplaceListing
	if err := DB.
		Where("status = ? AND expires_at IS NOT NULL AND expires_at <= ?", marketplaceStatusActive, now).
		Order("expires_at ASC").
		Limit(marketplaceExpirySweepBatchSize).
		Find(&listings).Error; err != nil {
		log.Printf("⚠️ 查询过期交易行商品失败: err=%s", formatPlainError(err))
		return
	}

	for _, listing := range listings {
		var availableCount int64
		if err := DB.Model(&MarketplaceSecret{}).
			Where("listing_id = ? AND status = ?", listing.ID, marketplaceSecretAvailable).
			Count(&availableCount).Error; err != nil {
			log.Printf("marketplace expiry stock count failed: listing=%d seller=%d err=%s", listing.ID, listing.SellerID, formatPlainError(err))
			continue
		}
		if availableCount == 0 {
			if _, err := closeMarketplaceListingByID(DB, listing.ID); err != nil {
				if errors.Is(err, errMarketplaceCloseNotFound) {
					continue
				}
				if errors.Is(err, errMarketplaceSellerMismatch) {
					handleMarketplaceSellerMismatch(bot, listing.ID, "auto_expire_sold_out")
					continue
				}
				log.Printf("marketplace expiry sold-out close failed: listing=%d seller=%d err=%s", listing.ID, listing.SellerID, formatPlainError(err))
				continue
			}
			log.Printf("marketplace expiry sold-out listing closed without seller notice: listing=%d seller=%d expires_at=%s",
				listing.ID, listing.SellerID, marketplaceListingExpiresAtLogText(listing))
			continue
		}

		refundQty, err := closeMarketplaceListingByID(DB, listing.ID)
		if err != nil {
			if errors.Is(err, errMarketplaceCloseNotFound) {
				continue
			}
			if errors.Is(err, errMarketplaceSellerMismatch) {
				handleMarketplaceSellerMismatch(bot, listing.ID, "auto_expire")
				continue
			}
			log.Printf("⚠️ 自动下架过期交易行商品失败: listing=%d seller=%d err=%s", listing.ID, listing.SellerID, formatPlainError(err))
			continue
		}

		log.Printf("✅ 自动下架过期交易行商品: listing=%d seller=%d refund_qty=%d expires_at=%s",
			listing.ID, listing.SellerID, refundQty, marketplaceListingExpiresAtLogText(listing))
		if bot != nil && listing.SellerID != 0 {
			sendPlainText(bot, listing.SellerID, marketplaceExpiredCloseNoticeText(listing, refundQty))
		}
	}
}

func marketplaceExpiredCloseNoticeText(listing MarketplaceListing, refundQty int) string {
	if listing.ListingType == marketplaceTypeInventory {
		return fmt.Sprintf(
			"🛒 交易行商品 #%d 已满 48 小时自动下架。\n\n商品：%s\n未售出的背包物品退回数量：%d",
			listing.ID,
			marketplaceVisibleItemName(listing.Name),
			refundQty,
		)
	}
	return fmt.Sprintf(
		"🛒 交易行商品 #%d 已满 48 小时自动下架。\n\n商品：%s\n未售出的卡密已停止出售。",
		listing.ID,
		marketplaceVisibleItemName(listing.Name),
	)
}

func initializeMarketplaceListingExpiry(db *gorm.DB, now time.Time) error {
	if db == nil {
		return fmt.Errorf("DB_NOT_READY")
	}
	expiresAt := marketplaceListingExpiresAt(now)
	res := db.Model(&MarketplaceListing{}).
		Where("status = ? AND expires_at IS NULL AND deleted_at IS NULL", marketplaceStatusActive).
		Update("expires_at", expiresAt)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected > 0 {
		log.Printf("✅ 已初始化交易行历史在售商品 48 小时下架倒计时: count=%d expires_at=%s",
			res.RowsAffected, expiresAt.Format(time.RFC3339))
	}
	return nil
}

func marketplaceListingExpiresAt(now time.Time) time.Time {
	if now.IsZero() {
		now = time.Now()
	}
	return now.Add(marketplaceListingTTL)
}

func marketplaceListingExpiresAtPtr(now time.Time) *time.Time {
	expiresAt := marketplaceListingExpiresAt(now)
	return &expiresAt
}

func marketplaceActiveListingQuery(db *gorm.DB, now time.Time) *gorm.DB {
	if now.IsZero() {
		now = time.Now()
	}
	return db.Where("status = ?", marketplaceStatusActive).
		Where("(expires_at IS NULL OR expires_at > ?)", now)
}

func marketplaceListingExpired(listing MarketplaceListing, now time.Time) bool {
	if listing.ExpiresAt == nil {
		return false
	}
	if now.IsZero() {
		now = time.Now()
	}
	return !listing.ExpiresAt.After(now)
}

func marketplaceEffectiveStatusText(listing MarketplaceListing, now time.Time) string {
	if listing.Status == marketplaceStatusActive && marketplaceListingExpired(listing, now) {
		return "已到期"
	}
	return marketplaceStatusText(listing.Status)
}

func marketplaceListingRemainingText(listing MarketplaceListing, now time.Time) string {
	if listing.ExpiresAt == nil {
		return "待初始化"
	}
	if now.IsZero() {
		now = time.Now()
	}
	if !listing.ExpiresAt.After(now) {
		return "已到期，等待自动下架"
	}
	return marketplaceDurationText(listing.ExpiresAt.Sub(now))
}

func marketplaceDurationText(d time.Duration) string {
	if d <= 0 {
		return "已到期"
	}
	minutes := int(d / time.Minute)
	if d%time.Minute != 0 {
		minutes++
	}
	if minutes < 60 {
		return fmt.Sprintf("%d分钟", minutes)
	}
	hours := minutes / 60
	remainingMinutes := minutes % 60
	if hours < 24 {
		if remainingMinutes == 0 {
			return fmt.Sprintf("%d小时", hours)
		}
		return fmt.Sprintf("%d小时%d分钟", hours, remainingMinutes)
	}
	days := hours / 24
	remainingHours := hours % 24
	if remainingHours == 0 {
		return fmt.Sprintf("%d天", days)
	}
	return fmt.Sprintf("%d天%d小时", days, remainingHours)
}

func marketplaceListingExpiresAtLogText(listing MarketplaceListing) string {
	if listing.ExpiresAt == nil {
		return "-"
	}
	return listing.ExpiresAt.Format(time.RFC3339)
}

func marketplaceListingTypeForClose(listingType string) (string, error) {
	return marketplaceListingTypeForPurchase(listingType)
}

func marketplaceClosedSecretStatus() string {
	return marketplaceSecretClosed
}

func parseMarketplaceID(text string, prefix string) (uint, bool) {
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), prefix))
	id64, err := strconv.ParseUint(raw, 10, 64)
	if err != nil || id64 == 0 {
		return 0, false
	}
	return uint(id64), true
}

func parseMarketplaceBuyCommand(text string) (uint, int, bool) {
	raw := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(text), "购买商品"))
	parts := strings.Fields(raw)
	if len(parts) < 1 || len(parts) > 2 {
		return 0, 0, false
	}
	id64, err := strconv.ParseUint(parts[0], 10, 64)
	if err != nil || id64 == 0 {
		return 0, 0, false
	}
	qty := 1
	if len(parts) == 2 {
		qtyText := strings.TrimSpace(parts[1])
		parsed, err := strconv.ParseInt(qtyText, 10, 64)
		if err != nil {
			if isPositiveIntegerText(qtyText) {
				return uint(id64), marketplaceMaxBuyQuantity + 1, true
			}
			return 0, 0, false
		}
		if parsed <= 0 {
			return 0, 0, false
		}
		if parsed > int64(marketplaceMaxBuyQuantity) {
			return uint(id64), marketplaceMaxBuyQuantity + 1, true
		}
		qty = int(parsed)
	}
	return uint(id64), qty, true
}

func isPositiveIntegerText(text string) bool {
	if text == "" {
		return false
	}
	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func calculateMarketplaceFee(amount int) int {
	if amount <= 0 || marketplaceFeePercent <= 0 {
		return 0
	}
	fee := amount * marketplaceFeePercent / 100
	if fee < 1 {
		fee = 1
	}
	if fee >= amount {
		fee = amount - 1
	}
	if fee < 0 {
		return 0
	}
	return fee
}

func countMarketplaceListingStock(listingID uint) (int64, error) {
	var count int64
	err := DB.Model(&MarketplaceSecret{}).
		Where("listing_id = ? AND status = ?", listingID, marketplaceSecretAvailable).
		Count(&count).Error
	return count, err
}

func marketplaceStockText(stock int64, available bool) string {
	if !available {
		return "读取失败"
	}
	return fmt.Sprintf("%d", stock)
}

func minInt(a int, b int) int {
	if a < b {
		return a
	}
	return b
}

func displayMarketplaceSeller(listing MarketplaceListing) string {
	return marketplaceDisplayText(listing.SellerName, marketplaceDisplayNameMaxLen, fmt.Sprintf("%d", listing.SellerID))
}

func marketplaceStatusText(status string) string {
	switch status {
	case marketplaceStatusActive:
		return "在售"
	case marketplaceStatusClosed:
		return "已下架"
	case marketplaceStatusReview:
		return "异常待核查"
	default:
		return "未知状态"
	}
}

func marketplaceDisputeStatusText(status string) string {
	switch status {
	case marketplaceDisputeOpen:
		return "处理中"
	case marketplaceDisputeClosed:
		return "已关闭"
	default:
		return "未知状态"
	}
}

func marketplacePurchaseStatusText(status string) string {
	switch status {
	case "paid":
		return "已支付"
	default:
		return "未知状态"
	}
}

func marketplaceTypeText(listingType string) string {
	switch listingType {
	case "", marketplaceTypeSecret:
		return "自由卡密"
	case marketplaceTypeInventory:
		return "背包物品"
	default:
		return "未知类型"
	}
}

func encryptMarketplaceSecret(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("EMPTY_MARKETPLACE_SECRET")
	}

	pepper := getSensitivePepper()
	if pepper == "" {
		return "", errSecurityPepperNotConfigured
	}

	key := sha256.Sum256([]byte("marketplace-secret:" + pepper))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}

	sealed := gcm.Seal(nil, nonce, []byte(raw), nil)
	payload := append(nonce, sealed...)
	return "gcm$" + base64.RawURLEncoding.EncodeToString(payload), nil
}

func decryptMarketplaceSecret(encrypted string) (string, error) {
	encrypted = strings.TrimSpace(encrypted)
	if !strings.HasPrefix(encrypted, "gcm$") {
		return "", fmt.Errorf("INVALID_MARKETPLACE_SECRET_CIPHER")
	}

	pepper := getSensitivePepper()
	if pepper == "" {
		return "", errSecurityPepperNotConfigured
	}

	payload, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(encrypted, "gcm$"))
	if err != nil {
		return "", err
	}

	key := sha256.Sum256([]byte("marketplace-secret:" + pepper))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) <= gcm.NonceSize() {
		return "", fmt.Errorf("INVALID_MARKETPLACE_SECRET_PAYLOAD")
	}

	nonce := payload[:gcm.NonceSize()]
	ciphertext := payload[gcm.NonceSize():]
	plain, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return "", err
	}
	return string(plain), nil
}
