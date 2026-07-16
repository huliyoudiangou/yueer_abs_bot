package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

const (
	bookRequestAnnouncementWindow        = 20 * time.Minute
	bookRequestAnnouncementQueryLimit    = 20
	bookRequestAnnouncementDisplayLimit  = 10
	bookRequestAnnouncementMaxQueryPages = 10
	bookRequestAnnouncementPreviewTTL    = 24 * time.Hour
	bookRequestAnnouncementMaxCoverBytes = 2 * 1024 * 1024
)

var bookRequestAnnouncementLocks sync.Map
var bookRequestAnnouncementPreviewItems sync.Map
var bookRequestAnnouncementCandidateSnapshots sync.Map

type bookAnnouncementPreviewEntry struct {
	ReqID     uint
	ItemID    string
	ExpiresAt time.Time
}

type bookAnnouncementCandidateSnapshot struct {
	ReqID      uint
	Candidates []BookAnnouncementCandidate
	ExpiresAt  time.Time
}

type absFlexibleTime struct {
	time.Time
}

func (t *absFlexibleTime) UnmarshalJSON(data []byte) error {
	raw := strings.TrimSpace(string(data))
	if raw == "" || raw == "null" {
		return nil
	}

	if raw[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		s = strings.TrimSpace(s)
		if s == "" {
			return nil
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			t.Time = absUnixTime(n)
			return nil
		}
		if f, err := strconv.ParseFloat(s, 64); err == nil {
			t.Time = absUnixTime(int64(f))
			return nil
		}
		if parsed, err := time.Parse(time.RFC3339Nano, s); err == nil {
			t.Time = parsed
			return nil
		}
		if parsed, err := time.Parse("2006-01-02 15:04:05", s); err == nil {
			t.Time = parsed
			return nil
		}
		return nil
	}

	n, err := strconv.ParseInt(raw, 10, 64)
	if err == nil {
		t.Time = absUnixTime(n)
		return nil
	}
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		t.Time = absUnixTime(int64(f))
	}
	return nil
}

func absUnixTime(n int64) time.Time {
	if n <= 0 {
		return time.Time{}
	}
	if n > 1_000_000_000_000 {
		return time.UnixMilli(n)
	}
	return time.Unix(n, 0)
}

type absLibrarySummary struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type absLibraryItem struct {
	ID        string          `json:"id"`
	LibraryID string          `json:"libraryId"`
	AddedAt   absFlexibleTime `json:"addedAt"`
	CreatedAt absFlexibleTime `json:"createdAt"`
	UpdatedAt absFlexibleTime `json:"updatedAt"`
	Media     struct {
		Metadata absBookMetadata `json:"metadata"`
	} `json:"media"`
	MediaMetadata absBookMetadata `json:"mediaMetadata"`
}

type absBookMetadata struct {
	Title        string   `json:"title"`
	Subtitle     string   `json:"subtitle"`
	Narrators    []string `json:"narrators"`
	NarratorName string   `json:"narratorName"`
	Narrator     string   `json:"narrator"`
}

type BookAnnouncementCandidate struct {
	ItemID      string
	LibraryID   string
	LibraryName string
	Title       string
	Narrators   string
	RecentAt    time.Time
}

func (c *AbsClient) GetRecentBookAnnouncementCandidates(window time.Duration, now time.Time) ([]BookAnnouncementCandidate, error) {
	if window <= 0 {
		window = bookRequestAnnouncementWindow
	}
	if now.IsZero() {
		now = time.Now()
	}

	libraries, err := c.getAbsLibraries()
	if err != nil {
		return nil, err
	}
	if len(libraries) == 0 {
		return nil, nil
	}

	libraryNames := make(map[string]string, len(libraries))
	candidatesByItemID := make(map[string]BookAnnouncementCandidate)
	successfulLibraries := 0

	for _, library := range libraries {
		library.ID = strings.TrimSpace(library.ID)
		if library.ID == "" {
			continue
		}
		libraryNames[library.ID] = strings.TrimSpace(library.Name)

		libraryReadSucceeded := false
		for page := 0; page < bookRequestAnnouncementMaxQueryPages; page++ {
			items, err := c.getRecentAbsLibraryItemsPage(library.ID, bookRequestAnnouncementQueryLimit, page)
			if err != nil {
				log.Printf("ABS recent library page read failed: library=%s page=%d err=%s", formatPlainValue(library.ID), page, formatPlainError(err))
				break
			}
			libraryReadSucceeded = true
			pageReachedWindowEnd := false
			for _, item := range items {
				candidate := bookAnnouncementCandidateFromItem(item, libraryNames)
				if candidate.ItemID == "" || candidate.RecentAt.IsZero() || candidate.RecentAt.After(now.Add(2*time.Minute)) {
					continue
				}
				if candidate.RecentAt.Before(now.Add(-window)) {
					pageReachedWindowEnd = true
					continue
				}
				if existing, ok := candidatesByItemID[candidate.ItemID]; !ok || candidate.RecentAt.After(existing.RecentAt) {
					candidatesByItemID[candidate.ItemID] = candidate
				}
			}
			if len(items) < bookRequestAnnouncementQueryLimit || pageReachedWindowEnd {
				break
			}
		}
		if libraryReadSucceeded {
			successfulLibraries++
		}
	}

	if successfulLibraries == 0 {
		return nil, fmt.Errorf("所有 ABS 媒体库最近入库读取均失败")
	}

	candidates := make([]BookAnnouncementCandidate, 0, len(candidatesByItemID))
	for _, candidate := range candidatesByItemID {
		candidates = append(candidates, candidate)
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].RecentAt.Equal(candidates[j].RecentAt) {
			return candidates[i].ItemID < candidates[j].ItemID
		}
		return candidates[i].RecentAt.After(candidates[j].RecentAt)
	})
	return candidates, nil
}

func (c *AbsClient) GetBookAnnouncementCandidateByItemID(itemID string) (*BookAnnouncementCandidate, error) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return nil, fmt.Errorf("ABS item ID 为空")
	}

	libraries, err := c.getAbsLibraries()
	if err != nil {
		return nil, err
	}
	libraryNames := make(map[string]string, len(libraries))
	for _, library := range libraries {
		libraryNames[strings.TrimSpace(library.ID)] = strings.TrimSpace(library.Name)
	}

	item, err := c.getAbsLibraryItem(itemID)
	if err != nil {
		return nil, err
	}
	candidate := bookAnnouncementCandidateFromItem(item, libraryNames)
	if candidate.ItemID == "" {
		return nil, fmt.Errorf("ABS item 缺少 ID")
	}
	return &candidate, nil
}

func (c *AbsClient) getAbsLibraries() ([]absLibrarySummary, error) {
	body, code, err := c.sendRequest("GET", "/api/libraries", nil)
	if err != nil {
		return nil, fmt.Errorf("读取 ABS 媒体库失败: %w", err)
	}
	if code != 200 {
		return nil, &AbsAPIError{Operation: "读取 ABS 媒体库失败", StatusCode: code, Message: "响应: " + absResponseSnippet(body)}
	}

	var wrapped struct {
		Libraries []absLibrarySummary `json:"libraries"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil && len(wrapped.Libraries) > 0 {
		return wrapped.Libraries, nil
	}

	var direct []absLibrarySummary
	if err := json.Unmarshal(body, &direct); err == nil {
		return direct, nil
	}

	return nil, fmt.Errorf("解析 ABS 媒体库失败")
}

func (c *AbsClient) getLatestAbsLibraryItem(libraryID string) (absLibraryItem, bool, error) {
	var zero absLibraryItem
	items, err := c.getRecentAbsLibraryItemsPage(libraryID, 1, 0)
	if err != nil {
		return zero, false, err
	}
	if len(items) == 0 {
		return zero, false, nil
	}
	return items[0], true, nil
}

func (c *AbsClient) getRecentAbsLibraryItems(libraryID string, limit int) ([]absLibraryItem, error) {
	return c.getRecentAbsLibraryItemsPage(libraryID, limit, 0)
}

func (c *AbsClient) getRecentAbsLibraryItemsPage(libraryID string, limit int, page int) ([]absLibraryItem, error) {
	if limit <= 0 {
		limit = bookRequestAnnouncementQueryLimit
	}
	if page < 0 {
		page = 0
	}
	path := fmt.Sprintf(
		"/api/libraries/%s/items?limit=%d&page=%d&sort=addedAt&desc=1&collapseseries=0",
		url.PathEscape(libraryID),
		limit,
		page,
	)
	body, code, err := c.sendRequest("GET", path, nil)
	if err != nil {
		return nil, fmt.Errorf("读取 ABS 媒体库条目失败: %w", err)
	}
	if code != 200 {
		return nil, &AbsAPIError{Operation: "读取 ABS 媒体库条目失败", StatusCode: code, Message: "响应: " + absResponseSnippet(body)}
	}

	items, err := parseAbsLibraryItems(body)
	if err != nil {
		return nil, err
	}
	return items, nil
}

func (c *AbsClient) getAbsLibraryItem(itemID string) (absLibraryItem, error) {
	var zero absLibraryItem
	body, code, err := c.sendRequest("GET", "/api/items/"+url.PathEscape(itemID), nil)
	if err != nil {
		return zero, fmt.Errorf("读取 ABS 书籍条目失败: %w", err)
	}
	if code != 200 {
		return zero, &AbsAPIError{Operation: "读取 ABS 书籍条目失败", StatusCode: code, Message: "响应: " + absResponseSnippet(body)}
	}

	var wrapped struct {
		LibraryItem absLibraryItem `json:"libraryItem"`
		Item        absLibraryItem `json:"item"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil {
		if strings.TrimSpace(wrapped.LibraryItem.ID) != "" {
			return wrapped.LibraryItem, nil
		}
		if strings.TrimSpace(wrapped.Item.ID) != "" {
			return wrapped.Item, nil
		}
	}

	var item absLibraryItem
	if err := json.Unmarshal(body, &item); err != nil {
		return zero, fmt.Errorf("解析 ABS 书籍条目失败: %w", err)
	}
	return item, nil
}

func parseAbsLibraryItems(body []byte) ([]absLibraryItem, error) {
	var wrapped struct {
		Results      []absLibraryItem `json:"results"`
		Items        []absLibraryItem `json:"items"`
		LibraryItems []absLibraryItem `json:"libraryItems"`
	}
	if err := json.Unmarshal(body, &wrapped); err == nil {
		switch {
		case len(wrapped.Results) > 0:
			return wrapped.Results, nil
		case len(wrapped.Items) > 0:
			return wrapped.Items, nil
		case len(wrapped.LibraryItems) > 0:
			return wrapped.LibraryItems, nil
		}
	}

	var direct []absLibraryItem
	if err := json.Unmarshal(body, &direct); err == nil {
		return direct, nil
	}

	return nil, nil
}

func bookAnnouncementCandidateFromItem(item absLibraryItem, libraryNames map[string]string) BookAnnouncementCandidate {
	metadata := item.Media.Metadata
	if strings.TrimSpace(metadata.Title) == "" {
		metadata = item.MediaMetadata
	}

	libraryID := strings.TrimSpace(item.LibraryID)
	libraryName := strings.TrimSpace(libraryNames[libraryID])
	if libraryName == "" {
		libraryName = "未知媒体库"
	}

	return BookAnnouncementCandidate{
		ItemID:      strings.TrimSpace(item.ID),
		LibraryID:   libraryID,
		LibraryName: libraryName,
		Title:       displayBookAnnouncementText(metadata.Title, "未命名书籍"),
		Narrators:   displayBookAnnouncementNarrators(metadata),
		RecentAt:    itemRecentTime(item),
	}
}

func itemRecentTime(item absLibraryItem) time.Time {
	if !item.AddedAt.IsZero() {
		return item.AddedAt.Time
	}
	if item.CreatedAt.After(item.UpdatedAt.Time) {
		return item.CreatedAt.Time
	}
	return item.UpdatedAt.Time
}

func displayBookAnnouncementNarrators(metadata absBookMetadata) string {
	var parts []string
	for _, narrator := range metadata.Narrators {
		narrator = strings.TrimSpace(narrator)
		if narrator != "" {
			parts = append(parts, narrator)
		}
	}
	for _, narrator := range []string{metadata.NarratorName, metadata.Narrator} {
		narrator = strings.TrimSpace(narrator)
		if narrator != "" {
			parts = append(parts, narrator)
		}
	}
	if len(parts) == 0 {
		return "未标注"
	}
	return displayBookAnnouncementText(strings.Join(parts, "、"), "未标注")
}

func displayBookAnnouncementText(text string, fallback string) string {
	text = strings.TrimSpace(formatDiagnosticTextForDisplay(text))
	if text == "" {
		return fallback
	}
	return truncateRunes(text, 80)
}

func formatBookAnnouncementDeliveryKey(reqID uint) string {
	return fmt.Sprintf("BR-%d", reqID)
}

func formatBookAnnouncementCaption(candidate BookAnnouncementCandidate) string {
	return fmt.Sprintf(
		"📚 新书已入库\n\n媒体库：%s\n书名：%s\n演播：%s\n\n前往 ABS 搜索即可收听。",
		displayBookAnnouncementText(candidate.LibraryName, "未知媒体库"),
		displayBookAnnouncementText(candidate.Title, "未命名书籍"),
		displayBookAnnouncementText(candidate.Narrators, "未标注"),
	)
}

func (c *AbsClient) DownloadBookAnnouncementCover(itemID string) ([]byte, error) {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return nil, fmt.Errorf("ABS item ID 为空")
	}

	body, code, err := c.sendRequest("GET", "/api/items/"+url.PathEscape(itemID)+"/cover?width=512", nil)
	if err != nil {
		return nil, fmt.Errorf("下载 ABS 封面失败: %w", err)
	}
	if code != 200 {
		return nil, &AbsAPIError{Operation: "下载 ABS 封面失败", StatusCode: code}
	}
	if len(body) == 0 {
		return nil, fmt.Errorf("ABS 封面为空")
	}
	if len(body) > bookRequestAnnouncementMaxCoverBytes {
		return nil, fmt.Errorf("ABS 封面过大")
	}
	return body, nil
}

func maybePromptBookRequestGroupAnnouncement(bot *tgbotapi.BotAPI, adminID int64, req BookRequest) string {
	if bot == nil || absClient == nil || AppConfig == nil || AppConfig.NoticeGroupID == 0 {
		return "已处理，未配置大群公告"
	}
	if req.Status != bookRequestStatusUploaded && req.Status != bookRequestStatusCompleted {
		return "已处理"
	}

	if err := sendBookAnnouncementCandidatePrompt(bot, adminID, req.ID); err != nil {
		log.Printf("⚠️ 求书已上传后生成入库公告候选失败: req=%d admin=%d err=%s", req.ID, adminID, formatPlainError(err))
		if retryErr := sendBookAnnouncementRetryPrompt(bot, adminID, req.ID); retryErr != nil {
			log.Printf("⚠️ 求书公告重试提示发送失败: req=%d admin=%d err=%s", req.ID, adminID, formatTelegramSendError(retryErr))
		}
		return "已处理，公告候选生成失败"
	}
	return "已处理，请选择并确认群公告"
}

func sendBookAnnouncementRetryPrompt(bot *tgbotapi.BotAPI, adminID int64, reqID uint) error {
	msg := tgbotapi.NewMessage(adminID, fmt.Sprintf("✅ 求书工单 #%d 已处理。\n\n⚠️ 新书公告候选读取失败。工单状态不受影响，可稍后点击‘重新读取候选’重试。", reqID))
	msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("🔄 重新读取候选", fmt.Sprintf("br_ann_refresh_%d", reqID)),
			tgbotapi.NewInlineKeyboardButtonData("跳过公告", fmt.Sprintf("br_ann_skip_%d", reqID)),
		),
	)
	_, err := sendNoAutoDelete(bot, msg)
	return err
}

func sendBookAnnouncementCandidatePrompt(bot *tgbotapi.BotAPI, adminID int64, reqID uint) error {
	if bot == nil || absClient == nil {
		return fmt.Errorf("公告服务未初始化")
	}
	candidates, err := absClient.GetRecentBookAnnouncementCandidates(bookRequestAnnouncementWindow, time.Now())
	if err != nil {
		return err
	}
	if len(candidates) == 0 {
		msg := tgbotapi.NewMessage(adminID, fmt.Sprintf("✅ 求书工单 #%d 已处理。\n\n近 20 分钟内未找到可公告的新书。若 ABS 扫描刚完成，可稍后点击‘重新读取候选’。", reqID))
		msg.ReplyMarkup = tgbotapi.NewInlineKeyboardMarkup(
			tgbotapi.NewInlineKeyboardRow(
				tgbotapi.NewInlineKeyboardButtonData("🔄 重新读取候选", fmt.Sprintf("br_ann_refresh_%d", reqID)),
				tgbotapi.NewInlineKeyboardButtonData("跳过公告", fmt.Sprintf("br_ann_skip_%d", reqID)),
			),
		)
		_, err = sendNoAutoDelete(bot, msg)
		return err
	}
	if len(candidates) == 1 {
		return sendBookAnnouncementPreview(bot, adminID, reqID, candidates[0])
	}
	return sendBookAnnouncementCandidateSelection(bot, adminID, reqID, candidates)
}

func sendBookAnnouncementCandidateSelection(bot *tgbotapi.BotAPI, adminID int64, reqID uint, candidates []BookAnnouncementCandidate) error {
	snapshotToken := storeBookAnnouncementCandidateSnapshot(reqID, candidates)
	if snapshotToken == "" {
		return fmt.Errorf("BOOK_ANNOUNCEMENT_CANDIDATE_SNAPSHOT_PERSIST_FAILED")
	}
	text, markup, _, ok := buildBookAnnouncementCandidatePage(reqID, snapshotToken, candidates, 0)
	if !ok {
		return fmt.Errorf("BOOK_ANNOUNCEMENT_CANDIDATE_PAGE_EMPTY")
	}

	msg := tgbotapi.NewMessage(adminID, text)
	msg.ReplyMarkup = markup
	_, err := sendNoAutoDelete(bot, msg)
	return err
}

func buildBookAnnouncementCandidatePage(reqID uint, snapshotToken string, candidates []BookAnnouncementCandidate, page int) (string, tgbotapi.InlineKeyboardMarkup, int, bool) {
	return buildBookAnnouncementCandidatePageWithTokenStore(reqID, snapshotToken, candidates, page, storeBookAnnouncementPreviewCandidate)
}

func buildBookAnnouncementCandidatePageWithTokenStore(reqID uint, snapshotToken string, candidates []BookAnnouncementCandidate, page int, tokenStore func(uint, string) string) (string, tgbotapi.InlineKeyboardMarkup, int, bool) {
	var empty tgbotapi.InlineKeyboardMarkup
	if reqID == 0 || strings.TrimSpace(snapshotToken) == "" || len(candidates) == 0 || tokenStore == nil {
		return "", empty, 0, false
	}
	totalPages := (len(candidates) + bookRequestAnnouncementDisplayLimit - 1) / bookRequestAnnouncementDisplayLimit
	if page < 0 {
		page = 0
	}
	if page >= totalPages {
		page = totalPages - 1
	}
	start := page * bookRequestAnnouncementDisplayLimit
	end := start + bookRequestAnnouncementDisplayLimit
	if end > len(candidates) {
		end = len(candidates)
	}

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, end-start+2)
	for index := start; index < end; index++ {
		candidate := candidates[index]
		token := tokenStore(reqID, candidate.ItemID)
		if token == "" {
			return "", empty, 0, false
		}
		label := truncateRunes(fmt.Sprintf("%d. %s", index+1, displayBookAnnouncementText(candidate.Title, "\u672a\u547d\u540d\u4e66\u7c4d")), 48)
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData(label, fmt.Sprintf("br_ann_sel_%d_%s", reqID, token)),
		))
	}
	if totalPages > 1 {
		navigation := make([]tgbotapi.InlineKeyboardButton, 0, 2)
		if page > 0 {
			navigation = append(navigation, tgbotapi.NewInlineKeyboardButtonData("\u2b05\ufe0f \u4e0a\u4e00\u9875", formatBookAnnouncementPageCallback(reqID, page-1, snapshotToken)))
		}
		if page+1 < totalPages {
			navigation = append(navigation, tgbotapi.NewInlineKeyboardButtonData("\u4e0b\u4e00\u9875 \u27a1\ufe0f", formatBookAnnouncementPageCallback(reqID, page+1, snapshotToken)))
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(navigation...))
	}
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(
		tgbotapi.NewInlineKeyboardButtonData("\U0001f504 \u91cd\u65b0\u8bfb\u53d6\u5019\u9009", fmt.Sprintf("br_ann_refresh_%d", reqID)),
		tgbotapi.NewInlineKeyboardButtonData("\u8df3\u8fc7\u516c\u544a", fmt.Sprintf("br_ann_skip_%d", reqID)),
	))

	text := fmt.Sprintf("\u2705 \u6c42\u4e66\u5de5\u5355 #%d \u5df2\u5904\u7406\u3002\n\n\u8fd1 20 \u5206\u949f\u5171\u627e\u5230 %d \u672c\u65b0\u4e66\uff0c\u5f53\u524d\u7b2c %d/%d \u9875\u3002\u8bf7\u9009\u62e9\u672c\u5de5\u5355\u5bf9\u5e94\u4e66\u7c4d\u540e\u518d\u9884\u89c8\u53d1\u5e03\u3002", reqID, len(candidates), page+1, totalPages)
	return text, tgbotapi.NewInlineKeyboardMarkup(rows...), page, true
}

func storeBookAnnouncementCandidateSnapshot(reqID uint, candidates []BookAnnouncementCandidate) string {
	if DB == nil || reqID == 0 || len(candidates) == 0 {
		return ""
	}
	now := time.Now()
	cleanupExpiredBookAnnouncementCandidateSnapshots(now)
	body, err := json.Marshal(candidates)
	if err != nil {
		log.Printf("book announcement candidate snapshot encode failed: req=%d err=%s", reqID, formatPlainError(err))
		return ""
	}
	for attempt := 0; attempt < 3; attempt++ {
		token := generateRandomCode(12)
		snapshot := bookAnnouncementCandidateSnapshot{
			ReqID:      reqID,
			Candidates: append([]BookAnnouncementCandidate(nil), candidates...),
			ExpiresAt:  now.Add(bookRequestAnnouncementPreviewTTL),
		}
		row := BookRequestAnnouncementCandidateSnapshot{
			Token: token, RequestID: reqID, CandidateJSON: string(body), ExpiresAt: snapshot.ExpiresAt,
		}
		if err := DB.Create(&row).Error; err != nil {
			log.Printf("book announcement candidate snapshot persist failed: req=%d attempt=%d err=%s", reqID, attempt+1, formatPlainError(err))
			continue
		}
		bookRequestAnnouncementCandidateSnapshots.Store(token, snapshot)
		return token
	}
	return ""
}

func resolveBookAnnouncementCandidateSnapshot(reqID uint, token string, now time.Time) ([]BookAnnouncementCandidate, bool) {
	token = strings.TrimSpace(token)
	if reqID == 0 || token == "" {
		return nil, false
	}
	if now.IsZero() {
		now = time.Now()
	}
	if value, ok := bookRequestAnnouncementCandidateSnapshots.Load(token); ok {
		snapshot, valid := value.(bookAnnouncementCandidateSnapshot)
		if valid && snapshot.ReqID == reqID && len(snapshot.Candidates) > 0 && (snapshot.ExpiresAt.IsZero() || !now.After(snapshot.ExpiresAt)) {
			return append([]BookAnnouncementCandidate(nil), snapshot.Candidates...), true
		}
		bookRequestAnnouncementCandidateSnapshots.Delete(token)
	}
	if DB == nil {
		return nil, false
	}
	var row BookRequestAnnouncementCandidateSnapshot
	if err := DB.Where("token = ? AND request_id = ?", token, reqID).First(&row).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("book announcement candidate snapshot lookup failed: req=%d err=%s", reqID, formatPlainError(err))
		}
		return nil, false
	}
	if now.After(row.ExpiresAt) {
		_ = DB.Delete(&row).Error
		return nil, false
	}
	var candidates []BookAnnouncementCandidate
	if err := json.Unmarshal([]byte(row.CandidateJSON), &candidates); err != nil || len(candidates) == 0 {
		log.Printf("book announcement candidate snapshot decode failed: req=%d err=%s", reqID, formatPlainError(err))
		return nil, false
	}
	bookRequestAnnouncementCandidateSnapshots.Store(token, bookAnnouncementCandidateSnapshot{ReqID: reqID, Candidates: candidates, ExpiresAt: row.ExpiresAt})
	return append([]BookAnnouncementCandidate(nil), candidates...), true
}

func cleanupExpiredBookAnnouncementCandidateSnapshots(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	bookRequestAnnouncementCandidateSnapshots.Range(func(key, value any) bool {
		snapshot, ok := value.(bookAnnouncementCandidateSnapshot)
		if !ok || (!snapshot.ExpiresAt.IsZero() && now.After(snapshot.ExpiresAt)) {
			bookRequestAnnouncementCandidateSnapshots.Delete(key)
		}
		return true
	})
	if DB != nil {
		if err := DB.Where("expires_at < ?", now).Delete(&BookRequestAnnouncementCandidateSnapshot{}).Error; err != nil {
			log.Printf("book announcement expired candidate snapshot cleanup failed: %s", formatPlainError(err))
		}
	}
}

func formatBookAnnouncementPageCallback(reqID uint, page int, token string) string {
	return fmt.Sprintf("br_ann_page_%d_%d_%s", reqID, page, token)
}

func parseBookAnnouncementPageCallback(data string) (uint, int, string, bool) {
	if !strings.HasPrefix(data, "br_ann_page_") {
		return 0, 0, "", false
	}
	parts := strings.SplitN(strings.TrimPrefix(data, "br_ann_page_"), "_", 3)
	if len(parts) != 3 {
		return 0, 0, "", false
	}
	reqID64, reqErr := strconv.ParseUint(parts[0], 10, 64)
	page64, pageErr := strconv.ParseUint(parts[1], 10, 31)
	token := strings.TrimSpace(parts[2])
	if reqErr != nil || pageErr != nil || reqID64 == 0 || token == "" {
		return 0, 0, "", false
	}
	return uint(reqID64), int(page64), token, true
}

func sendBookAnnouncementPreview(bot *tgbotapi.BotAPI, adminID int64, reqID uint, candidate BookAnnouncementCandidate) error {
	caption := "请确认是否发布到大群：\n\n" + formatBookAnnouncementCaption(candidate) + "\n\n\u516c\u544a\u51ed\u8bc1\uff1a" + formatBookAnnouncementDeliveryKey(reqID)
	token := storeBookAnnouncementPreviewCandidate(reqID, candidate.ItemID)
	if token == "" {
		return fmt.Errorf("BOOK_ANNOUNCEMENT_PREVIEW_TOKEN_PERSIST_FAILED")
	}
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📣 确认发布", fmt.Sprintf("br_ann_pub_%d_%s", reqID, token)),
		),
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("↩️ 重新选择", fmt.Sprintf("br_ann_refresh_%d", reqID)),
			tgbotapi.NewInlineKeyboardButtonData("跳过公告", fmt.Sprintf("br_ann_skip_%d", reqID)),
		),
	)

	if cover, err := absClient.DownloadBookAnnouncementCover(candidate.ItemID); err == nil {
		photo := tgbotapi.NewPhoto(adminID, tgbotapi.FileBytes{Name: "cover.jpg", Bytes: cover})
		photo.Caption = caption
		photo.ReplyMarkup = keyboard
		if _, sendErr := sendNoAutoDelete(bot, photo); sendErr == nil {
			return nil
		} else {
			log.Printf("⚠️ 求书入库公告预览封面发送失败，降级为纯文本: req=%d item=%s err=%s", reqID, formatPlainValue(candidate.ItemID), formatTelegramSendError(sendErr))
		}
	} else {
		log.Printf("⚠️ 求书入库公告预览封面读取失败: req=%d item=%s err=%s", reqID, formatPlainValue(candidate.ItemID), formatPlainError(err))
	}

	msg := tgbotapi.NewMessage(adminID, caption)
	msg.ReplyMarkup = keyboard
	_, err := sendNoAutoDelete(bot, msg)
	return err
}

func storeBookAnnouncementPreviewCandidate(reqID uint, itemID string) string {
	itemID = strings.TrimSpace(itemID)
	if DB == nil || reqID == 0 || itemID == "" {
		return ""
	}
	now := time.Now()
	cleanupExpiredBookAnnouncementPreviewCandidates(now)
	for attempt := 0; attempt < 3; attempt++ {
		token := generateRandomCode(16)
		entry := bookAnnouncementPreviewEntry{ReqID: reqID, ItemID: itemID, ExpiresAt: now.Add(bookRequestAnnouncementPreviewTTL)}
		row := BookRequestAnnouncementPreviewCandidate{Token: token, RequestID: reqID, ItemID: itemID, ExpiresAt: entry.ExpiresAt}
		if err := DB.Create(&row).Error; err != nil {
			log.Printf("book announcement preview candidate persist failed: req=%d attempt=%d err=%s", reqID, attempt+1, formatPlainError(err))
			continue
		}
		bookRequestAnnouncementPreviewItems.Store(token, entry)
		return token
	}
	return ""
}

func resolveBookAnnouncementPreviewCandidate(reqID uint, token string, now time.Time) (string, bool) {
	token = strings.TrimSpace(token)
	if reqID == 0 || token == "" {
		return "", false
	}
	if now.IsZero() {
		now = time.Now()
	}
	if value, ok := bookRequestAnnouncementPreviewItems.Load(token); ok {
		entry, valid := value.(bookAnnouncementPreviewEntry)
		if valid && entry.ReqID == reqID && strings.TrimSpace(entry.ItemID) != "" && (entry.ExpiresAt.IsZero() || !now.After(entry.ExpiresAt)) {
			return entry.ItemID, true
		}
		deleteBookAnnouncementPreviewCandidateToken(token)
	}
	if DB == nil {
		return "", false
	}
	var row BookRequestAnnouncementPreviewCandidate
	if err := DB.Where("token = ? AND request_id = ?", token, reqID).First(&row).Error; err != nil {
		if !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("book announcement preview candidate lookup failed: req=%d err=%s", reqID, formatPlainError(err))
		}
		return "", false
	}
	if now.After(row.ExpiresAt) || strings.TrimSpace(row.ItemID) == "" {
		_ = DB.Delete(&row).Error
		return "", false
	}
	bookRequestAnnouncementPreviewItems.Store(token, bookAnnouncementPreviewEntry{ReqID: reqID, ItemID: row.ItemID, ExpiresAt: row.ExpiresAt})
	return row.ItemID, true
}

func deleteBookAnnouncementPreviewCandidateToken(token string) {
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	bookRequestAnnouncementPreviewItems.Delete(token)
	if DB != nil {
		if err := DB.Where("token = ?", token).Delete(&BookRequestAnnouncementPreviewCandidate{}).Error; err != nil {
			log.Printf("book announcement preview candidate delete failed: err=%s", formatPlainError(err))
		}
	}
}

func cleanupExpiredBookAnnouncementPreviewCandidates(now time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	bookRequestAnnouncementPreviewItems.Range(func(key, value any) bool {
		entry, ok := value.(bookAnnouncementPreviewEntry)
		if !ok || (!entry.ExpiresAt.IsZero() && now.After(entry.ExpiresAt)) {
			bookRequestAnnouncementPreviewItems.Delete(key)
		}
		return true
	})
	if DB != nil {
		if err := DB.Where("expires_at < ?", now).Delete(&BookRequestAnnouncementPreviewCandidate{}).Error; err != nil {
			log.Printf("book announcement expired preview candidate cleanup failed: %s", formatPlainError(err))
		}
	}
}

func handleBookRequestAnnouncementCallback(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery) bool {
	if cb == nil || cb.From == nil {
		return false
	}

	data := cb.Data
	if reqID, ok := parseBookRequestCallbackID(data, "br_ann_skip_"); ok {
		removeBookAnnouncementPreviewButtons(bot, cb)
		answerCallback(bot, cb.ID, fmt.Sprintf("已跳过求书 #%d 的大群公告", reqID))
		return true
	}

	if reqID, ok := parseBookRequestCallbackID(data, "br_ann_refresh_"); ok {
		req, allowed := loadOperableBookRequestAnnouncement(bot, cb, reqID, "callback announcement refresh")
		if !allowed {
			return true
		}
		if err := sendBookAnnouncementCandidatePrompt(bot, cb.From.ID, req.ID); err != nil {
			log.Printf("⚠️ 求书公告候选重新读取失败: req=%d admin=%d err=%s", reqID, cb.From.ID, formatPlainError(err))
			answerCallback(bot, cb.ID, "候选读取失败，请稍后重试")
			return true
		}
		removeBookAnnouncementPreviewButtons(bot, cb)
		answerCallback(bot, cb.ID, "已重新读取公告候选")
		return true
	}

	if reqID, page, snapshotToken, ok := parseBookAnnouncementPageCallback(data); ok {
		req, allowed := loadOperableBookRequestAnnouncement(bot, cb, reqID, "callback announcement page")
		if !allowed {
			return true
		}
		candidates, resolved := resolveBookAnnouncementCandidateSnapshot(req.ID, snapshotToken, time.Now())
		if !resolved {
			if err := sendBookAnnouncementCandidatePrompt(bot, cb.From.ID, req.ID); err != nil {
				answerCallback(bot, cb.ID, "\u5019\u9009\u5df2\u5931\u6548\u4e14\u91cd\u65b0\u8bfb\u53d6\u5931\u8d25\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5")
				return true
			}
			removeBookAnnouncementPreviewButtons(bot, cb)
			answerCallback(bot, cb.ID, "\u5019\u9009\u5df2\u5931\u6548\uff0c\u5df2\u91cd\u65b0\u8bfb\u53d6")
			return true
		}
		text, markup, actualPage, built := buildBookAnnouncementCandidatePage(req.ID, snapshotToken, candidates, page)
		if !built || cb.Message == nil {
			answerCallback(bot, cb.ID, "\u5019\u9009\u5206\u9875\u4e0d\u53ef\u7528")
			return true
		}
		edit := tgbotapi.NewEditMessageText(cb.Message.Chat.ID, cb.Message.MessageID, text)
		edit.ReplyMarkup = &markup
		if _, err := bot.Send(edit); err != nil && !isTelegramMessageNotModifiedError(err) {
			log.Printf("book announcement candidate page edit failed: req=%d page=%d err=%s", req.ID, actualPage, formatTelegramSendError(err))
			answerCallback(bot, cb.ID, "\u7ffb\u9875\u5931\u8d25\uff0c\u8bf7\u7a0d\u540e\u91cd\u8bd5")
			return true
		}
		answerCallback(bot, cb.ID, fmt.Sprintf("\u5df2\u5207\u6362\u5230\u7b2c %d \u9875", actualPage+1))
		return true
	}

	if reqID, token, ok := parseBookAnnouncementTokenCallback(data, "br_ann_sel_"); ok {
		if absClient == nil {
			answerCallback(bot, cb.ID, "ABS 客户端未初始化，无法读取书籍详情")
			return true
		}
		req, allowed := loadOperableBookRequestAnnouncement(bot, cb, reqID, "callback announcement select")
		if !allowed {
			return true
		}
		itemID, resolved := resolveBookAnnouncementPreviewCandidate(reqID, token, time.Now())
		if !resolved {
			if err := sendBookAnnouncementCandidatePrompt(bot, cb.From.ID, req.ID); err != nil {
				answerCallback(bot, cb.ID, "候选已失效且重新读取失败，请稍后重试")
				return true
			}
			removeBookAnnouncementPreviewButtons(bot, cb)
			answerCallback(bot, cb.ID, "候选已失效，已重新读取")
			return true
		}
		candidate, err := absClient.GetBookAnnouncementCandidateByItemID(itemID)
		if err != nil {
			log.Printf("⚠️ 求书公告候选详情读取失败: req=%d item=%s admin=%d err=%s", reqID, formatPlainValue(itemID), cb.From.ID, formatPlainError(err))
			answerCallback(bot, cb.ID, "书籍详情读取失败，请重新选择或稍后重试")
			return true
		}
		if err := sendBookAnnouncementPreview(bot, cb.From.ID, req.ID, *candidate); err != nil {
			log.Printf("⚠️ 求书入库公告预览发送失败: req=%d admin=%d item=%s err=%s", req.ID, cb.From.ID, formatPlainValue(itemID), formatTelegramSendError(err))
			answerCallback(bot, cb.ID, "公告预览发送失败，请稍后重试")
			return true
		}
		deleteBookAnnouncementPreviewCandidateToken(token)
		removeBookAnnouncementPreviewButtons(bot, cb)
		answerCallback(bot, cb.ID, "已生成所选书籍的公告预览")
		return true
	}

	reqID, token, ok := parseBookAnnouncementPublishCallback(data)
	if !ok {
		return false
	}
	if AppConfig == nil || AppConfig.NoticeGroupID == 0 {
		answerCallback(bot, cb.ID, "未配置通知群，无法发布")
		return true
	}
	if absClient == nil {
		answerCallback(bot, cb.ID, "ABS 客户端未初始化，无法发布")
		return true
	}

	req, allowed := loadOperableBookRequestAnnouncement(bot, cb, reqID, "callback announcement publish")
	if !allowed {
		return true
	}
	itemID, resolved := resolveBookAnnouncementPreviewCandidate(reqID, token, time.Now())
	if !resolved {
		if err := sendBookAnnouncementCandidatePrompt(bot, cb.From.ID, req.ID); err != nil {
			answerCallback(bot, cb.ID, "公告预览已失效，重新读取候选失败，请稍后重试")
			return true
		}
		removeBookAnnouncementPreviewButtons(bot, cb)
		answerCallback(bot, cb.ID, "公告预览已失效，已重新读取候选")
		return true
	}

	lockKey := reqID
	if _, loaded := bookRequestAnnouncementLocks.LoadOrStore(lockKey, time.Now()); loaded {
		answerCallback(bot, cb.ID, "\u8be5\u5de5\u5355\u516c\u544a\u6b63\u5728\u5904\u7406\uff0c\u8bf7\u52ff\u91cd\u590d\u70b9\u51fb")
		return true
	}
	defer bookRequestAnnouncementLocks.Delete(lockKey)

	published, lookupErr := bookRequestGroupAnnouncementAlreadyPublished(reqID)
	if lookupErr != nil {
		log.Printf("book announcement delivery lookup failed: req=%d err=%s", reqID, formatPlainError(lookupErr))
		answerCallback(bot, cb.ID, "\u516c\u544a\u72b6\u6001\u67e5\u8be2\u5931\u8d25\uff0c\u4e3a\u907f\u514d\u91cd\u590d\u53d1\u9001\u5df2\u62d2\u7edd\u672c\u6b21\u64cd\u4f5c")
		return true
	}
	if published {
		removeBookAnnouncementPreviewButtons(bot, cb)
		answerCallback(bot, cb.ID, "\u8be5\u5de5\u5355\u7684\u65b0\u4e66\u516c\u544a\u5df2\u7ecf\u53d1\u5e03")
		return true
	}
	_, claimErr := claimBookRequestAnnouncementDelivery(reqID, itemID, AppConfig.NoticeGroupID, cb.From.ID, time.Now())
	if claimErr != nil {
		switch {
		case errors.Is(claimErr, errBookAnnouncementAlreadySent):
			removeBookAnnouncementPreviewButtons(bot, cb)
			answerCallback(bot, cb.ID, "\u8be5\u5de5\u5355\u7684\u65b0\u4e66\u516c\u544a\u5df2\u7ecf\u53d1\u5e03")
		case errors.Is(claimErr, errBookAnnouncementUncertain):
			removeBookAnnouncementPreviewButtons(bot, cb)
			answerCallback(bot, cb.ID, "\u4e0a\u6b21\u53d1\u9001\u7ed3\u679c\u4e0d\u786e\u5b9a\uff0c\u8bf7\u5148\u4eba\u5de5\u68c0\u67e5\u901a\u77e5\u7fa4\uff1bBot \u4e0d\u4f1a\u81ea\u52a8\u91cd\u53d1")
		case errors.Is(claimErr, errBookAnnouncementInProgress):
			answerCallback(bot, cb.ID, "\u8be5\u5de5\u5355\u516c\u544a\u6b63\u5728\u53d1\u9001\uff0c\u8bf7\u52ff\u91cd\u590d\u64cd\u4f5c")
		default:
			log.Printf("book announcement delivery claim failed: req=%d err=%s", reqID, formatPlainError(claimErr))
			answerCallback(bot, cb.ID, "\u516c\u544a\u72b6\u6001\u5199\u5165\u5931\u8d25\uff0c\u4e3a\u907f\u514d\u91cd\u590d\u53d1\u9001\u5df2\u62d2\u7edd\u672c\u6b21\u64cd\u4f5c")
		}
		return true
	}

	sentMessage, publishErr := publishBookRequestGroupAnnouncement(bot, req, itemID)
	if publishErr != nil {
		status := bookAnnouncementDeliveryFailed
		errorCode := "pre_send_or_rejected"
		if publishErr.DeliveryMayHaveSucceeded {
			status = bookAnnouncementDeliveryUncertain
			errorCode = "telegram_result_uncertain"
		}
		if markErr := markBookRequestAnnouncementDelivery(reqID, status, errorCode); markErr != nil {
			log.Printf("book announcement delivery failure state write failed: req=%d err=%s", reqID, formatPlainError(markErr))
			answerCallback(bot, cb.ID, "\u53d1\u9001\u6216\u72b6\u6001\u767b\u8bb0\u5931\u8d25\uff0c\u7ed3\u679c\u6309\u4e0d\u786e\u5b9a\u5904\u7406\uff1b\u8bf7\u5148\u68c0\u67e5\u901a\u77e5\u7fa4\uff0c\u52ff\u91cd\u590d\u70b9\u51fb")
			return true
		}
		log.Printf("book announcement publish failed: req=%d item=%s admin=%d uncertain=%t err=%s", reqID, formatPlainValue(itemID), cb.From.ID, publishErr.DeliveryMayHaveSucceeded, formatPlainError(publishErr.Err))
		if publishErr.DeliveryMayHaveSucceeded {
			removeBookAnnouncementPreviewButtons(bot, cb)
			answerCallback(bot, cb.ID, "\u53d1\u9001\u7ed3\u679c\u4e0d\u786e\u5b9a\uff0c\u5df2\u505c\u6b62\u81ea\u52a8\u91cd\u53d1\uff1b\u8bf7\u4eba\u5de5\u68c0\u67e5\u901a\u77e5\u7fa4")
		} else {
			answerCallback(bot, cb.ID, "\u516c\u544a\u672a\u53d1\u51fa\uff0c\u5df2\u5b89\u5168\u6807\u8bb0\u5931\u8d25\uff0c\u53ef\u7a0d\u540e\u91cd\u8bd5")
		}
		return true
	}

	if err := finalizeBookRequestAnnouncementDelivery(req, cb.From.ID, getTelegramDisplayName(cb.From), sentMessage.MessageID, time.Now()); err != nil {
		log.Printf("book announcement sent but finalization failed: req=%d message=%d err=%s", reqID, sentMessage.MessageID, formatPlainError(err))
		if markErr := markBookRequestAnnouncementDelivery(reqID, bookAnnouncementDeliveryUncertain, "telegram_sent_finalize_failed", sentMessage.MessageID); markErr != nil {
			log.Printf("book announcement finalization fallback state write failed: req=%d message=%d err=%s", reqID, sentMessage.MessageID, formatPlainError(markErr))
		}
		removeBookAnnouncementPreviewButtons(bot, cb)
		answerCallback(bot, cb.ID, "\u516c\u544a\u5df2\u53d1\u51fa\uff0c\u4f46\u72b6\u6001\u767b\u8bb0\u5931\u8d25\uff1b\u4e3a\u907f\u514d\u91cd\u590d\u8bf7\u52ff\u518d\u6b21\u53d1\u5e03\uff0c\u5e76\u8054\u7cfb\u8d85\u7ba1\u68c0\u67e5")
		return true
	}

	deleteBookAnnouncementPreviewCandidateToken(token)
	removeBookAnnouncementPreviewButtons(bot, cb)
	answerCallback(bot, cb.ID, "\u5df2\u53d1\u5e03\u5230\u5927\u7fa4")
	return true
}

func loadOperableBookRequestAnnouncement(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery, reqID uint, context string) (BookRequest, bool) {
	req, found, err := loadBookRequestByID(DB, reqID, context)
	if err != nil {
		answerCallback(bot, cb.ID, "查询工单失败，请稍后重试")
		return BookRequest{}, false
	}
	if !found {
		answerCallback(bot, cb.ID, "工单不存在")
		return BookRequest{}, false
	}
	if req.Status != bookRequestStatusUploaded && req.Status != bookRequestStatusCompleted {
		answerCallback(bot, cb.ID, "该工单尚未标记已上传")
		return BookRequest{}, false
	}
	if !canOperateBookRequest(req, cb.From.ID) {
		answerCallback(bot, cb.ID, "只有接单人或超级管理员可以操作公告")
		return BookRequest{}, false
	}
	return req, true
}

func parseBookAnnouncementPublishCallback(data string) (uint, string, bool) {
	return parseBookAnnouncementTokenCallback(data, "br_ann_pub_")
}

func parseBookAnnouncementTokenCallback(data string, prefix string) (uint, string, bool) {
	if !strings.HasPrefix(data, prefix) {
		return 0, "", false
	}
	rest := strings.TrimPrefix(data, prefix)
	reqPart, tokenPart, ok := strings.Cut(rest, "_")
	if !ok {
		return 0, "", false
	}
	reqID64, err := strconv.ParseUint(reqPart, 10, 64)
	token := strings.TrimSpace(tokenPart)
	if err != nil || reqID64 == 0 || token == "" {
		return 0, "", false
	}
	return uint(reqID64), token, true
}

type bookAnnouncementPublishError struct {
	Err                      error
	DeliveryMayHaveSucceeded bool
}

func (e *bookAnnouncementPublishError) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func definiteTelegramRejection(err error) bool {
	var tgErr *tgbotapi.Error
	return errors.As(err, &tgErr) && tgErr.Code >= 400 && tgErr.Code < 500 && tgErr.Code != 429
}

func publishBookRequestGroupAnnouncement(bot *tgbotapi.BotAPI, req BookRequest, itemID string) (tgbotapi.Message, *bookAnnouncementPublishError) {
	var zero tgbotapi.Message
	if bot == nil {
		return zero, &bookAnnouncementPublishError{Err: fmt.Errorf("Telegram Bot \u672a\u521d\u59cb\u5316")}
	}
	if absClient == nil {
		return zero, &bookAnnouncementPublishError{Err: fmt.Errorf("ABS \u5ba2\u6237\u7aef\u672a\u521d\u59cb\u5316")}
	}
	if AppConfig == nil || AppConfig.NoticeGroupID == 0 {
		return zero, &bookAnnouncementPublishError{Err: fmt.Errorf("\u901a\u77e5\u7fa4\u672a\u914d\u7f6e")}
	}
	candidate, err := absClient.GetBookAnnouncementCandidateByItemID(itemID)
	if err != nil {
		return zero, &bookAnnouncementPublishError{Err: err}
	}
	caption := formatBookAnnouncementCaption(*candidate) + "\n\n\u516c\u544a\u51ed\u8bc1\uff1a" + formatBookAnnouncementDeliveryKey(req.ID)

	if cover, coverErr := absClient.DownloadBookAnnouncementCover(candidate.ItemID); coverErr == nil {
		photo := tgbotapi.NewPhoto(AppConfig.NoticeGroupID, tgbotapi.FileBytes{Name: "cover.jpg", Bytes: cover})
		photo.Caption = caption
		if sent, sendErr := sendNoAutoDelete(bot, photo); sendErr == nil {
			return sent, nil
		} else if !definiteTelegramRejection(sendErr) {
			return zero, &bookAnnouncementPublishError{Err: fmt.Errorf("send group announcement photo: %w", sendErr), DeliveryMayHaveSucceeded: true}
		} else {
			log.Printf("book announcement photo definitely rejected; falling back to text: req=%d item=%s err=%s", req.ID, formatPlainValue(candidate.ItemID), formatTelegramSendError(sendErr))
		}
	} else {
		log.Printf("book announcement cover unavailable; falling back to text: req=%d item=%s err=%s", req.ID, formatPlainValue(candidate.ItemID), formatPlainError(coverErr))
	}

	msg := tgbotapi.NewMessage(AppConfig.NoticeGroupID, caption)
	sent, err := sendNoAutoDelete(bot, msg)
	if err != nil {
		return zero, &bookAnnouncementPublishError{
			Err:                      fmt.Errorf("send group announcement text: %w", err),
			DeliveryMayHaveSucceeded: !definiteTelegramRejection(err),
		}
	}
	return sent, nil
}

func removeBookAnnouncementPreviewButtons(bot *tgbotapi.BotAPI, cb *tgbotapi.CallbackQuery) {
	if bot == nil || cb == nil || cb.Message == nil {
		return
	}
	emptyMarkup := tgbotapi.NewInlineKeyboardMarkup()
	edit := tgbotapi.NewEditMessageReplyMarkup(cb.Message.Chat.ID, cb.Message.MessageID, emptyMarkup)
	if _, err := bot.Request(edit); err != nil && !isTelegramMessageNotModifiedError(err) {
		log.Printf("⚠️ 移除求书公告预览按钮失败: chat=%d msg=%d err=%s", cb.Message.Chat.ID, cb.Message.MessageID, formatTelegramSendError(err))
	}
}
