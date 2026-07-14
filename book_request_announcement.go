package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	bookRequestAnnouncementWindow         = 20 * time.Minute
	bookRequestAnnouncementCandidateLimit = 5
	bookRequestAnnouncementPreviewTTL     = 30 * time.Minute
	bookRequestAnnouncementMaxCoverBytes  = 2 * 1024 * 1024
)

var bookRequestAnnouncementLocks sync.Map
var bookRequestAnnouncementPreviewItems sync.Map

type bookAnnouncementPreviewEntry struct {
	ReqID     uint
	ItemID    string
	ExpiresAt time.Time
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

func (c *AbsClient) GetRecentBookAnnouncementCandidate(window time.Duration, now time.Time) (*BookAnnouncementCandidate, error) {
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
	var candidates []BookAnnouncementCandidate

	for _, library := range libraries {
		library.ID = strings.TrimSpace(library.ID)
		if library.ID == "" {
			continue
		}
		libraryNames[library.ID] = strings.TrimSpace(library.Name)

		items, err := c.getRecentAbsLibraryItems(library.ID, bookRequestAnnouncementCandidateLimit)
		if err != nil {
			log.Printf("⚠️ ABS 最近入库读取失败: library=%s err=%s", formatPlainValue(library.ID), formatPlainError(err))
			continue
		}

		for _, item := range items {
			candidate := bookAnnouncementCandidateFromItem(item, libraryNames)
			if candidate.ItemID == "" || candidate.RecentAt.IsZero() {
				continue
			}
			candidates = append(candidates, candidate)
		}
	}

	if len(candidates) == 0 {
		return nil, nil
	}

	sort.Slice(candidates, func(i, j int) bool {
		return candidates[i].RecentAt.After(candidates[j].RecentAt)
	})

	latest := candidates[0]
	if latest.RecentAt.Before(now.Add(-window)) || latest.RecentAt.After(now.Add(2*time.Minute)) {
		return nil, nil
	}

	return &latest, nil
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
	items, err := c.getRecentAbsLibraryItems(libraryID, 1)
	if err != nil {
		return zero, false, err
	}
	if len(items) == 0 {
		return zero, false, nil
	}
	return items[0], true, nil
}

func (c *AbsClient) getRecentAbsLibraryItems(libraryID string, limit int) ([]absLibraryItem, error) {
	if limit <= 0 {
		limit = bookRequestAnnouncementCandidateLimit
	}
	path := fmt.Sprintf(
		"/api/libraries/%s/items?limit=%d&page=0&sort=addedAt&desc=1&collapseseries=0",
		url.PathEscape(libraryID),
		limit,
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

	candidate, err := absClient.GetRecentBookAnnouncementCandidate(bookRequestAnnouncementWindow, time.Now())
	if err != nil {
		log.Printf("⚠️ 求书已上传后读取 ABS 最近入库失败: req=%d admin=%d err=%s", req.ID, adminID, formatPlainError(err))
		sendPlainText(bot, adminID, fmt.Sprintf("✅ 求书工单 #%d 已处理。\n\n⚠️ 读取 ABS 最近入库失败，已跳过大群公告。", req.ID))
		return "已处理，公告查询失败"
	}
	if candidate == nil {
		sendPlainText(bot, adminID, fmt.Sprintf("✅ 求书工单 #%d 已处理。\n\n未找到近 20 分钟内入库书籍，已跳过大群公告。", req.ID))
		return "已处理，未找到近20分钟入库书籍"
	}

	if err := sendBookAnnouncementPreview(bot, adminID, req.ID, *candidate); err != nil {
		log.Printf("⚠️ 求书入库公告预览发送失败: req=%d admin=%d item=%s err=%s", req.ID, adminID, formatPlainValue(candidate.ItemID), formatTelegramSendError(err))
		return "已处理，公告预览发送失败"
	}

	return "已处理，请确认群公告"
}

func sendBookAnnouncementPreview(bot *tgbotapi.BotAPI, adminID int64, reqID uint, candidate BookAnnouncementCandidate) error {
	caption := "请确认是否发布到大群：\n\n" + formatBookAnnouncementCaption(candidate)
	token := storeBookAnnouncementPreviewCandidate(reqID, candidate.ItemID)
	keyboard := tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("📣 确认发布", fmt.Sprintf("br_ann_pub_%d_%s", reqID, token)),
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
	cleanupExpiredBookAnnouncementPreviewCandidates(time.Now())
	token := generateRandomCode(16)
	bookRequestAnnouncementPreviewItems.Store(token, bookAnnouncementPreviewEntry{
		ReqID:     reqID,
		ItemID:    strings.TrimSpace(itemID),
		ExpiresAt: time.Now().Add(bookRequestAnnouncementPreviewTTL),
	})
	return token
}

func resolveBookAnnouncementPreviewCandidate(reqID uint, token string, now time.Time) (string, bool) {
	token = strings.TrimSpace(token)
	if reqID == 0 || token == "" {
		return "", false
	}
	if now.IsZero() {
		now = time.Now()
	}

	value, ok := bookRequestAnnouncementPreviewItems.Load(token)
	if !ok {
		return "", false
	}
	entry, ok := value.(bookAnnouncementPreviewEntry)
	if !ok || entry.ReqID != reqID || strings.TrimSpace(entry.ItemID) == "" {
		bookRequestAnnouncementPreviewItems.Delete(token)
		return "", false
	}
	if !entry.ExpiresAt.IsZero() && now.After(entry.ExpiresAt) {
		bookRequestAnnouncementPreviewItems.Delete(token)
		return "", false
	}
	return entry.ItemID, true
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

	req, found, err := loadBookRequestByID(DB, reqID, "callback announcement publish")
	if err != nil {
		answerCallback(bot, cb.ID, "查询工单失败，请稍后重试")
		return true
	}
	if !found {
		answerCallback(bot, cb.ID, "工单不存在")
		return true
	}
	if req.Status != bookRequestStatusUploaded && req.Status != bookRequestStatusCompleted {
		answerCallback(bot, cb.ID, "该工单尚未标记已上传")
		return true
	}
	if !canOperateBookRequest(req, cb.From.ID) {
		answerCallback(bot, cb.ID, "只有接单人或超级管理员可以发布")
		return true
	}

	itemID, ok := resolveBookAnnouncementPreviewCandidate(reqID, token, time.Now())
	if !ok {
		answerCallback(bot, cb.ID, "公告预览已失效，请重新生成预览")
		return true
	}

	lockKey := fmt.Sprintf("%d:%s", reqID, token)
	if _, loaded := bookRequestAnnouncementLocks.LoadOrStore(lockKey, time.Now()); loaded {
		answerCallback(bot, cb.ID, "该公告正在发布，请勿重复点击")
		return true
	}

	if err := publishBookRequestGroupAnnouncement(bot, req, itemID); err != nil {
		bookRequestAnnouncementLocks.Delete(lockKey)
		log.Printf("⚠️ 求书入库公告发布失败: req=%d item=%s admin=%d err=%s", reqID, formatPlainValue(itemID), cb.From.ID, formatPlainError(err))
		answerCallback(bot, cb.ID, "发布失败，请稍后重试")
		return true
	}

	createBookRequestLog(req.ID, cb.From.ID, getTelegramDisplayName(cb.From), "group_announce", req.Status, req.Status, "admin published book request group announcement")
	bookRequestAnnouncementPreviewItems.Delete(token)
	removeBookAnnouncementPreviewButtons(bot, cb)
	answerCallback(bot, cb.ID, "已发布到大群")
	return true
}

func parseBookAnnouncementPublishCallback(data string) (uint, string, bool) {
	const prefix = "br_ann_pub_"
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

func publishBookRequestGroupAnnouncement(bot *tgbotapi.BotAPI, req BookRequest, itemID string) error {
	if bot == nil {
		return fmt.Errorf("Telegram Bot 未初始化")
	}
	if absClient == nil {
		return fmt.Errorf("ABS 客户端未初始化")
	}
	candidate, err := absClient.GetBookAnnouncementCandidateByItemID(itemID)
	if err != nil {
		return err
	}
	caption := formatBookAnnouncementCaption(*candidate)

	if cover, err := absClient.DownloadBookAnnouncementCover(candidate.ItemID); err == nil {
		photo := tgbotapi.NewPhoto(AppConfig.NoticeGroupID, tgbotapi.FileBytes{Name: "cover.jpg", Bytes: cover})
		photo.Caption = caption
		if _, sendErr := sendNoAutoDelete(bot, photo); sendErr == nil {
			return nil
		} else {
			log.Printf("⚠️ 求书入库公告封面发送失败，降级为纯文本: req=%d item=%s err=%s", req.ID, formatPlainValue(candidate.ItemID), formatTelegramSendError(sendErr))
		}
	} else {
		log.Printf("⚠️ 求书入库公告封面读取失败，降级为纯文本: req=%d item=%s err=%s", req.ID, formatPlainValue(candidate.ItemID), formatPlainError(err))
	}

	msg := tgbotapi.NewMessage(AppConfig.NoticeGroupID, caption)
	_, err = sendNoAutoDelete(bot, msg)
	return err
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
