package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	dailyListeningLivePositionMaxRate      = 2.0
	dailyListeningLiveClockFallbackMax     = 6 * time.Hour
	dailyListeningLiveCheckpointSource     = "abs_live_session"
	dailyListeningLiveProvisionalStatus    = "provisional"
	dailyListeningCrossDaySource           = "abs_sessions_cross_day"
	dailyListeningCrossDayStatus           = "corrected"
	dailyListeningCrossDayClockTolerance   = 2 * time.Minute
	dailyListeningCrossDayMinIdleAllowance = 15 * time.Minute
	dailyListeningSessionPageSize          = 100
	dailyListeningSessionMaxPages          = 5
	dailyListeningCrossDayAlgorithmVersion = 2
)

type AbsLiveListeningCheckpoint struct {
	gorm.Model

	UserID    int64  `gorm:"index;not null"`
	AbsUserID string `gorm:"index;not null"`

	SessionKey string `gorm:"index;not null"`
	ItemKey    string `gorm:"index;not null"`

	LastObservedAt      time.Time `gorm:"index"`
	LastPositionSeconds float64
	LastIsPlaying       bool
	LastServerUpdatedAt time.Time
}

func (AbsLiveListeningCheckpoint) TableName() string {
	return "abs_live_listening_checkpoints"
}

type ABSCrossDaySessionAdjustment struct {
	ID        uint `gorm:"primaryKey"`
	CreatedAt time.Time
	UpdatedAt time.Time

	UserID     int64  `gorm:"index;not null"`
	AbsUserID  string `gorm:"uniqueIndex:ux_abs_cross_day_session,priority:1;index;not null"`
	SessionKey string `gorm:"uniqueIndex:ux_abs_cross_day_session,priority:2;not null"`

	SourceDay        string    `gorm:"index;not null"`
	StartedAt        time.Time `gorm:"index"`
	SessionUpdatedAt time.Time
	ListeningSeconds float64
	AllocationJSON   string
	Status           string    `gorm:"index;not null"`
	Confidence       string    `gorm:"index;not null"`
	Reason           string    `gorm:"index;not null"`
	AlgorithmVersion int       `gorm:"not null"`
	ObservedAt       time.Time `gorm:"index;not null"`
}

func (ABSCrossDaySessionAdjustment) TableName() string {
	return "abs_cross_day_session_adjustments"
}

const (
	crossDayAdjustmentStatusAccepted         = "accepted"
	crossDayAdjustmentStatusBounded          = "bounded"
	crossDayAdjustmentStatusUncertain        = "uncertain"
	crossDayAdjustmentConfidenceEstimated    = "estimated"
	crossDayAdjustmentConfidenceConservative = "conservative"
	crossDayAdjustmentConfidenceUncertain    = "uncertain"
)

type absLiveListeningSession struct {
	SessionKey       string
	ItemKey          string
	PositionSeconds  float64
	HasPosition      bool
	ListeningSeconds float64
	HasListeningTime bool
	DateKey          string
	StartedAt        time.Time
	UpdatedAt        time.Time
	IsPlaying        bool
	HasPlayingState  bool

	// Persisted cross-day metadata is populated only when loading an observed
	// adjustment from SQLite. Bounded allocations contain mathematically
	// guaranteed per-day minimums, never guessed pause placement.
	CrossDayStatus   string
	StoredAllocation map[string]float64
}

func collectDailyListeningSessionData(userID int64, absUserID string, now time.Time) (map[string]float64, []absLiveListeningSession) {
	result := make(map[string]float64)
	if DB == nil || absClient == nil || userID == 0 || strings.TrimSpace(absUserID) == "" {
		return result, nil
	}
	if now.IsZero() {
		now = time.Now()
	}

	body, code, err := absClient.sendRequest("GET", absUserListeningSessionsPath(absUserID, 0, dailyListeningSessionPageSize), nil)
	if err != nil || code != 200 {
		log.Printf("每日听书 ABS 用户最新会话读取失败: user=%d abs=%s code=%d err=%s", userID, formatPlainValue(absUserID), code, formatPlainError(err))
		return result, nil
	}

	sessions, err := parseABSSessionsPayload(body)
	if err != nil {
		log.Printf("每日听书 ABS 用户最新会话解析失败: user=%d abs=%s err=%s", userID, formatPlainValue(absUserID), formatPlainError(err))
		return result, nil
	}

	parsedSessions := make([]absLiveListeningSession, 0, len(sessions))
	for _, raw := range sessions {
		if !absSessionBelongsToUser(raw, absUserID) {
			continue
		}
		session, ok := parseAbsLiveListeningSession(raw, absUserID)
		if !ok {
			log.Printf("每日听书 ABS 用户会话字段不足，跳过统计补算: user=%d abs=%s", userID, formatPlainValue(absUserID))
			continue
		}
		parsedSessions = append(parsedSessions, session)
		deltaByDay := consumeAbsLiveListeningSessionDelta(userID, absUserID, session, now)
		for dayKey, seconds := range deltaByDay {
			if seconds > 0 {
				result[dayKey] += seconds
			}
		}
	}

	return result, parsedSessions
}

func parseABSSessionsPayload(body []byte) ([]map[string]interface{}, error) {
	var arr []map[string]interface{}
	if err := json.Unmarshal(body, &arr); err == nil {
		return arr, nil
	}

	var obj map[string]interface{}
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, err
	}
	for _, key := range []string{"sessions", "data", "items", "results"} {
		if raw, ok := obj[key]; ok {
			if sessions := interfaceSliceToMaps(raw); sessions != nil {
				return sessions, nil
			}
		}
	}
	return nil, fmt.Errorf("ABS_SESSIONS_FIELD_MISSING")
}

func interfaceSliceToMaps(raw interface{}) []map[string]interface{} {
	list, ok := raw.([]interface{})
	if !ok {
		return nil
	}
	result := make([]map[string]interface{}, 0, len(list))
	for _, item := range list {
		if obj, ok := item.(map[string]interface{}); ok {
			result = append(result, obj)
		}
	}
	return result
}

func parseAbsLiveListeningSession(raw map[string]interface{}, absUserID string) (absLiveListeningSession, bool) {
	sessionKey := firstString(raw, "id", "sessionId", "sessionID", "session_id", "socketId", "socketID")
	itemKey := firstString(raw, "libraryItemId", "libraryItemID", "library_item_id", "mediaItemId", "mediaItemID", "media_item_id", "bookId", "bookID", "episodeId", "episodeID", "chapterId", "chapterID")
	if itemKey == "" {
		itemKey = nestedFirstString(raw, []string{"mediaProgress", "libraryItem", "mediaItem", "book", "item"}, "id", "libraryItemId", "libraryItemID", "mediaItemId", "mediaItemID")
	}
	if itemKey == "" {
		itemKey = "unknown"
	}

	startedAt, hasStart := firstTime(raw, "startedAt", "createdAt", "start", "connectedAt")
	updatedAt, hasUpdate := firstTime(raw, "updatedAt", "lastUpdate", "lastSeenAt", "lastActivityAt", "serverUpdatedAt")
	listeningSeconds, hasListeningTime := firstFloat(raw, "timeListening", "time_listening")
	dateKey := strings.TrimSpace(firstString(raw, "date"))
	position, hasPosition := firstFloat(raw, "currentTime", "current_time", "position", "positionSeconds", "progress")
	if !hasPosition {
		position, hasPosition = nestedFirstFloat(raw, []string{"mediaProgress", "playback", "progress"}, "currentTime", "current_time", "position", "positionSeconds", "progress")
	}
	if !hasPosition && hasListeningTime {
		position = listeningSeconds
		hasPosition = true
	}

	isPlaying, hasPlaying := sessionPlayingState(raw)
	// /api/users/{id}/listening-sessions is a historical session list. A missing
	// playback state must never be interpreted as actively playing.
	if !hasPlaying {
		isPlaying = false
	}

	if sessionKey == "" {
		baseTime := ""
		if hasStart {
			baseTime = startedAt.UTC().Format(time.RFC3339Nano)
		}
		sessionKey = fmt.Sprintf("%s:%s:%s", strings.TrimSpace(absUserID), itemKey, baseTime)
	}

	if !hasPosition && !hasStart && !hasUpdate {
		return absLiveListeningSession{}, false
	}

	return absLiveListeningSession{
		SessionKey:       formatPlainValue(sessionKey),
		ItemKey:          formatPlainValue(itemKey),
		PositionSeconds:  position,
		HasPosition:      hasPosition,
		ListeningSeconds: listeningSeconds,
		HasListeningTime: hasListeningTime,
		DateKey:          dateKey,
		StartedAt:        startedAt,
		UpdatedAt:        updatedAt,
		IsPlaying:        isPlaying,
		HasPlayingState:  hasPlaying,
	}, true
}

func fetchABSCrossDayListeningSessions(absUserID string) ([]absLiveListeningSession, error) {
	if absClient == nil || strings.TrimSpace(absUserID) == "" {
		return nil, fmt.Errorf("ABS \u4f1a\u8bdd\u8bfb\u53d6\u53c2\u6570\u4e0d\u5b8c\u6574")
	}

	sessions := make([]absLiveListeningSession, 0, dailyListeningSessionPageSize)
	seenSessionKeys := make(map[string]struct{}, dailyListeningSessionPageSize)
	for page := 0; page < dailyListeningSessionMaxPages; page++ {
		body, code, err := absClient.sendRequest("GET", absUserListeningSessionsPath(absUserID, page, dailyListeningSessionPageSize), nil)
		if err != nil {
			return nil, err
		}
		if code != 200 {
			return nil, &AbsAPIError{Operation: "\u8bfb\u53d6 ABS \u7528\u6237\u4f1a\u8bdd\u5931\u8d25", StatusCode: code, Message: "\u54cd\u5e94: " + absResponseSnippet(body)}
		}
		rawSessions, err := parseABSSessionsPayload(body)
		if err != nil {
			return nil, err
		}
		for _, raw := range rawSessions {
			if !absSessionBelongsToUser(raw, absUserID) {
				continue
			}
			session, ok := parseAbsLiveListeningSession(raw, absUserID)
			if !ok {
				continue
			}
			key := strings.TrimSpace(session.SessionKey)
			if key == "" {
				continue
			}
			if _, exists := seenSessionKeys[key]; exists {
				continue
			}
			seenSessionKeys[key] = struct{}{}
			sessions = append(sessions, session)
		}
		if len(rawSessions) < dailyListeningSessionPageSize {
			break
		}
	}
	return sessions, nil
}

func persistAndLoadABSCrossDaySessions(userID int64, absUserID string, days map[string]float64, sessions []absLiveListeningSession, observedAt time.Time) ([]absLiveListeningSession, error) {
	if DB == nil {
		return nil, fmt.Errorf("ABS_CROSS_DAY_DB_EMPTY")
	}
	if userID == 0 || strings.TrimSpace(absUserID) == "" {
		return nil, fmt.Errorf("ABS_CROSS_DAY_USER_EMPTY")
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}

	observations, diagnostics := buildABSCrossDaySessionObservations(userID, absUserID, sessions, observedAt)
	if err := upsertABSCrossDaySessionObservations(DB, observations); err != nil {
		return nil, fmt.Errorf("persist ABS cross-day sessions: %w", err)
	}
	if len(diagnostics) > 0 {
		parts := make([]string, 0, len(diagnostics))
		for key, count := range diagnostics {
			parts = append(parts, fmt.Sprintf("%s=%d", key, count))
		}
		sort.Strings(parts)
		log.Printf("ABS cross-day session diagnostics: user=%d abs=%s %s", userID, formatPlainValue(absUserID), strings.Join(parts, ","))
	}

	sourceDays := make([]string, 0, len(days))
	for dayKey := range days {
		dayKey = strings.TrimSpace(dayKey)
		if validDailyListeningDayKey(dayKey) {
			sourceDays = append(sourceDays, dayKey)
		}
	}
	if len(sourceDays) == 0 {
		return nil, nil
	}
	sort.Strings(sourceDays)
	var stored []ABSCrossDaySessionAdjustment
	if err := DB.Where(
		"user_id = ? AND abs_user_id = ? AND status IN ? AND algorithm_version = ? AND source_day IN ?",
		userID, strings.TrimSpace(absUserID), []string{crossDayAdjustmentStatusAccepted, crossDayAdjustmentStatusBounded}, dailyListeningCrossDayAlgorithmVersion, sourceDays,
	).Order("started_at ASC, session_key ASC").Find(&stored).Error; err != nil {
		return nil, fmt.Errorf("load ABS cross-day sessions: %w", err)
	}

	result := make([]absLiveListeningSession, 0, len(stored))
	for _, row := range stored {
		allocation := make(map[string]float64)
		if strings.TrimSpace(row.AllocationJSON) != "" {
			if err := json.Unmarshal([]byte(row.AllocationJSON), &allocation); err != nil {
				log.Printf("ABS cross-day allocation decode failed: user=%d session=%s err=%s", userID, formatPlainValue(row.SessionKey), formatPlainError(err))
				continue
			}
		}
		result = append(result, absLiveListeningSession{
			SessionKey:       row.SessionKey,
			DateKey:          row.SourceDay,
			StartedAt:        row.StartedAt,
			UpdatedAt:        row.SessionUpdatedAt,
			ListeningSeconds: row.ListeningSeconds,
			HasListeningTime: true,
			CrossDayStatus:   row.Status,
			StoredAllocation: allocation,
		})
	}
	return result, nil
}

func buildABSCrossDaySessionObservations(userID int64, absUserID string, sessions []absLiveListeningSession, observedAt time.Time) ([]ABSCrossDaySessionAdjustment, map[string]int) {
	observations := make([]ABSCrossDaySessionAdjustment, 0, len(sessions))
	diagnostics := make(map[string]int)
	for _, session := range sessions {
		status, confidence, reason, tracked := classifyABSCrossDaySession(session)
		if !tracked {
			continue
		}
		allocationJSON := ""
		switch status {
		case crossDayAdjustmentStatusAccepted:
			allocationJSON = crossDaySessionAllocationJSON(session)
		case crossDayAdjustmentStatusBounded:
			allocationJSON = crossDaySessionMinimumAllocationJSON(session)
		}
		diagnostics[status+":"+reason]++
		observations = append(observations, ABSCrossDaySessionAdjustment{
			UserID:           userID,
			AbsUserID:        strings.TrimSpace(absUserID),
			SessionKey:       strings.TrimSpace(session.SessionKey),
			SourceDay:        strings.TrimSpace(session.DateKey),
			StartedAt:        session.StartedAt,
			SessionUpdatedAt: session.UpdatedAt,
			ListeningSeconds: session.ListeningSeconds,
			AllocationJSON:   allocationJSON,
			Status:           status,
			Confidence:       confidence,
			Reason:           reason,
			AlgorithmVersion: dailyListeningCrossDayAlgorithmVersion,
			ObservedAt:       observedAt,
		})
	}
	return observations, diagnostics
}

func upsertABSCrossDaySessionObservations(tx *gorm.DB, observations []ABSCrossDaySessionAdjustment) error {
	if tx == nil {
		return fmt.Errorf("ABS_CROSS_DAY_DB_EMPTY")
	}
	if len(observations) == 0 {
		return nil
	}
	return tx.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "abs_user_id"}, {Name: "session_key"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"user_id", "source_day", "started_at", "session_updated_at", "listening_seconds",
			"allocation_json", "status", "confidence", "reason", "algorithm_version", "observed_at", "updated_at",
		}),
	}).CreateInBatches(&observations, 50).Error
}

func classifyABSCrossDaySession(session absLiveListeningSession) (status string, confidence string, reason string, tracked bool) {
	if strings.TrimSpace(session.SessionKey) == "" || session.StartedAt.IsZero() || session.UpdatedAt.IsZero() || !session.UpdatedAt.After(session.StartedAt) {
		return "", "", "", false
	}
	if sectDayKey(session.StartedAt) == sectDayKey(session.UpdatedAt.Add(-time.Nanosecond)) {
		return "", "", "", false
	}
	if !session.HasListeningTime || session.ListeningSeconds <= 0 || math.IsNaN(session.ListeningSeconds) || math.IsInf(session.ListeningSeconds, 0) {
		return crossDayAdjustmentStatusUncertain, crossDayAdjustmentConfidenceUncertain, "listening_invalid", true
	}
	if !validDailyListeningDayKey(session.DateKey) {
		return crossDayAdjustmentStatusUncertain, crossDayAdjustmentConfidenceUncertain, "source_day_invalid", true
	}
	wallSeconds := session.UpdatedAt.Sub(session.StartedAt).Seconds()
	if session.ListeningSeconds > wallSeconds+dailyListeningCrossDayClockTolerance.Seconds() {
		return crossDayAdjustmentStatusUncertain, crossDayAdjustmentConfidenceUncertain, "listening_exceeds_wall", true
	}
	idleSeconds := math.Max(0, wallSeconds-session.ListeningSeconds)
	maxIdleSeconds := math.Max(dailyListeningCrossDayMinIdleAllowance.Seconds(), session.ListeningSeconds*0.25)
	if idleSeconds > maxIdleSeconds {
		return crossDayAdjustmentStatusBounded, crossDayAdjustmentConfidenceConservative, "idle_excess_lower_bound", true
	}
	return crossDayAdjustmentStatusAccepted, crossDayAdjustmentConfidenceEstimated, "wall_clock_ratio", true
}

func crossDaySessionAllocationJSON(session absLiveListeningSession) string {
	segments := splitDurationByBeijingDay(session.StartedAt, session.UpdatedAt)
	total := 0.0
	for _, seconds := range segments {
		total += seconds
	}
	if total <= 0 {
		return ""
	}
	allocation := make(map[string]float64, len(segments))
	for dayKey, seconds := range segments {
		allocation[dayKey] = session.ListeningSeconds * seconds / total
	}
	body, err := json.Marshal(allocation)
	if err != nil {
		return ""
	}
	return string(body)
}

func crossDaySessionMinimumAllocationJSON(session absLiveListeningSession) string {
	segments := splitDurationByBeijingDay(session.StartedAt, session.UpdatedAt)
	wallSeconds := session.UpdatedAt.Sub(session.StartedAt).Seconds()
	if wallSeconds <= 0 || session.ListeningSeconds <= 0 || session.ListeningSeconds > wallSeconds+dailyListeningCrossDayClockTolerance.Seconds() {
		return ""
	}
	allocation := make(map[string]float64, len(segments))
	for dayKey, segmentSeconds := range segments {
		minimum := math.Max(0, session.ListeningSeconds-(wallSeconds-segmentSeconds))
		minimum = math.Min(minimum, math.Min(session.ListeningSeconds, segmentSeconds))
		if minimum > 1e-6 {
			allocation[dayKey] = minimum
		}
	}
	body, err := json.Marshal(allocation)
	if err != nil {
		return ""
	}
	return string(body)
}

func rebalanceABSDaysForCrossDaySessions(days map[string]float64, sessions []absLiveListeningSession) (map[string]float64, map[string]bool) {
	adjusted := make(map[string]float64, len(days))
	remainingOfficialByDay := make(map[string]float64, len(days))
	for dayKey, seconds := range days {
		dayKey = strings.TrimSpace(dayKey)
		if dayKey == "" {
			continue
		}
		seconds = positiveListeningSeconds(seconds)
		adjusted[dayKey] = seconds
		remainingOfficialByDay[dayKey] = seconds
	}

	correctedDays := make(map[string]bool)
	orderedSessions := append([]absLiveListeningSession(nil), sessions...)
	sort.SliceStable(orderedSessions, func(i, j int) bool {
		if orderedSessions[i].StartedAt.Equal(orderedSessions[j].StartedAt) {
			return orderedSessions[i].SessionKey < orderedSessions[j].SessionKey
		}
		return orderedSessions[i].StartedAt.Before(orderedSessions[j].StartedAt)
	})
	seenSessionKeys := make(map[string]bool, len(orderedSessions))
	for _, session := range orderedSessions {
		sessionKey := strings.TrimSpace(session.SessionKey)
		if sessionKey == "" || seenSessionKeys[sessionKey] {
			continue
		}
		seenSessionKeys[sessionKey] = true

		if session.CrossDayStatus == crossDayAdjustmentStatusBounded {
			sourceDay := strings.TrimSpace(session.DateKey)
			remaining := remainingOfficialByDay[sourceDay]
			if !validDailyListeningDayKey(sourceDay) || remaining+1e-6 < session.ListeningSeconds {
				continue
			}
			moveByDay := make(map[string]float64)
			totalMoved := 0.0
			for dayKey, seconds := range session.StoredAllocation {
				seconds = positiveListeningSeconds(seconds)
				if dayKey == sourceDay || !validDailyListeningDayKey(dayKey) || seconds <= 1e-6 {
					continue
				}
				moveByDay[dayKey] += seconds
				totalMoved += seconds
			}
			if totalMoved <= 1e-6 || totalMoved > session.ListeningSeconds+1e-6 {
				continue
			}
			remainingOfficialByDay[sourceDay] = math.Max(0, remaining-session.ListeningSeconds)
			adjusted[sourceDay] = math.Max(0, adjusted[sourceDay]-totalMoved)
			correctedDays[sourceDay] = true
			for dayKey, seconds := range moveByDay {
				adjusted[dayKey] += seconds
				correctedDays[dayKey] = true
			}
			continue
		}

		if !crossDayListeningSessionCanRebalance(session) {
			continue
		}
		segments := splitDurationByBeijingDay(session.StartedAt, session.UpdatedAt)
		if len(segments) < 2 {
			continue
		}

		sourceDay := strings.TrimSpace(session.DateKey)
		remaining := remainingOfficialByDay[sourceDay]
		if remaining+1e-6 < session.ListeningSeconds {
			continue
		}
		remainingOfficialByDay[sourceDay] = math.Max(0, remaining-session.ListeningSeconds)
		adjusted[sourceDay] = math.Max(0, adjusted[sourceDay]-session.ListeningSeconds)

		totalSegmentSeconds := 0.0
		for _, segmentSeconds := range segments {
			totalSegmentSeconds += segmentSeconds
		}
		if totalSegmentSeconds <= 0 {
			continue
		}
		for dayKey, segmentSeconds := range segments {
			adjusted[dayKey] += session.ListeningSeconds * (segmentSeconds / totalSegmentSeconds)
			correctedDays[dayKey] = true
		}
		correctedDays[sourceDay] = true
	}
	return adjusted, correctedDays
}

func crossDayListeningSessionCanRebalance(session absLiveListeningSession) bool {
	if !session.HasListeningTime || session.ListeningSeconds <= 0 ||
		math.IsNaN(session.ListeningSeconds) || math.IsInf(session.ListeningSeconds, 0) ||
		session.StartedAt.IsZero() || session.UpdatedAt.IsZero() || !session.UpdatedAt.After(session.StartedAt) ||
		!validDailyListeningDayKey(session.DateKey) {
		return false
	}
	if sectDayKey(session.StartedAt) == sectDayKey(session.UpdatedAt.Add(-time.Nanosecond)) {
		return false
	}

	wallSeconds := session.UpdatedAt.Sub(session.StartedAt).Seconds()
	if session.ListeningSeconds > wallSeconds+dailyListeningCrossDayClockTolerance.Seconds() {
		return false
	}
	idleSeconds := math.Max(0, wallSeconds-session.ListeningSeconds)
	maxIdleSeconds := math.Max(dailyListeningCrossDayMinIdleAllowance.Seconds(), session.ListeningSeconds*0.25)
	return idleSeconds <= maxIdleSeconds
}

func validDailyListeningDayKey(dayKey string) bool {
	dayKey = strings.TrimSpace(dayKey)
	if len(dayKey) != len("2006-01-02") {
		return false
	}
	parsed, err := time.ParseInLocation("2006-01-02", dayKey, dailyOperationsLocation)
	return err == nil && parsed.Format("2006-01-02") == dayKey
}

func absSessionBelongsToUser(raw map[string]interface{}, absUserID string) bool {
	want := strings.TrimSpace(absUserID)
	if want == "" {
		return false
	}
	for _, got := range []string{
		firstString(raw, "userId", "userID", "user_id", "user"),
		nestedFirstString(raw, []string{"user", "account"}, "id", "userId", "userID"),
	} {
		if strings.TrimSpace(got) == want {
			return true
		}
	}
	return false
}

func consumeAbsLiveListeningSessionDelta(userID int64, absUserID string, session absLiveListeningSession, now time.Time) map[string]float64 {
	result := make(map[string]float64)
	if DB == nil || userID == 0 || session.SessionKey == "" || session.ItemKey == "" || !session.IsPlaying {
		upsertAbsLiveListeningCheckpoint(userID, absUserID, session, now)
		return result
	}

	var cp AbsLiveListeningCheckpoint
	err := DB.Where("user_id = ? AND session_key = ? AND item_key = ?", userID, session.SessionKey, session.ItemKey).First(&cp).Error
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("每日听书活跃会话 checkpoint 读取失败: user=%d session=%s err=%s", userID, formatPlainValue(session.SessionKey), formatPlainError(err))
		upsertAbsLiveListeningCheckpoint(userID, absUserID, session, now)
		return result
	}

	if err == nil && !cp.LastObservedAt.IsZero() {
		usedPositionDelta := false
		if session.HasPosition && cp.LastPositionSeconds >= 0 {
			delta := session.PositionSeconds - cp.LastPositionSeconds
			if delta > 0 && livePositionDeltaAllowed(delta, cp.LastObservedAt, now) {
				addAllocatedLiveSeconds(result, cp.LastObservedAt, now, delta)
				usedPositionDelta = true
			}
		}
		if !usedPositionDelta && canUseLiveClockFallback(session, cp) {
			if deltaSeconds, ok := liveClockFallbackSeconds(cp.LastObservedAt, now); ok {
				addAllocatedLiveSeconds(result, cp.LastObservedAt, now, deltaSeconds)
			}
		}
	} else {
		// 首次观察只建立 checkpoint。缺少上一采样点时无法证明 StartedAt 之后始终在播放，
		// 直接按墙钟补算会把暂停、离线和陈旧会话误计为听书时长。
	}

	upsertAbsLiveListeningCheckpoint(userID, absUserID, session, now)
	return result
}

func canUseLiveClockFallback(session absLiveListeningSession, checkpoint AbsLiveListeningCheckpoint) bool {
	return session.HasPlayingState && session.IsPlaying && checkpoint.LastIsPlaying
}
func liveClockFallbackSeconds(lastObservedAt time.Time, now time.Time) (float64, bool) {
	if lastObservedAt.IsZero() || !now.After(lastObservedAt) {
		return 0, false
	}
	delta := now.Sub(lastObservedAt)
	if delta <= 0 || delta > dailyListeningLiveClockFallbackMax {
		return 0, false
	}
	return delta.Seconds(), true
}

func livePositionDeltaAllowed(delta float64, lastObservedAt time.Time, now time.Time) bool {
	if delta <= 0 || lastObservedAt.IsZero() || now.Before(lastObservedAt) {
		return false
	}
	elapsed := now.Sub(lastObservedAt).Seconds()
	if elapsed <= 0 {
		return false
	}
	return delta <= elapsed*dailyListeningLivePositionMaxRate+60
}

func addAllocatedLiveSeconds(result map[string]float64, start time.Time, end time.Time, seconds float64) {
	if seconds <= 0 || !end.After(start) {
		return
	}
	if seconds > sectMaxRawListeningSecondsPerDay {
		seconds = sectMaxRawListeningSecondsPerDay
	}

	segments := splitDurationByBeijingDay(start, end)
	total := 0.0
	for _, segmentSeconds := range segments {
		total += segmentSeconds
	}
	if total <= 0 {
		return
	}
	for dayKey, segmentSeconds := range segments {
		result[dayKey] += seconds * (segmentSeconds / total)
	}
}

func splitDurationByBeijingDay(start time.Time, end time.Time) map[string]float64 {
	result := make(map[string]float64)
	if !end.After(start) {
		return result
	}
	loc := dailyOperationsLocation
	cursor := start
	for cursor.Before(end) {
		local := cursor.In(loc)
		nextLocalMidnight := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, loc)
		next := nextLocalMidnight
		if next.After(end) {
			next = end
		}
		if next.After(cursor) {
			result[local.Format("2006-01-02")] += next.Sub(cursor).Seconds()
		}
		cursor = next
	}
	return result
}

func upsertAbsLiveListeningCheckpoint(userID int64, absUserID string, session absLiveListeningSession, observedAt time.Time) {
	if DB == nil || userID == 0 || session.SessionKey == "" || session.ItemKey == "" {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now()
	}
	entry := AbsLiveListeningCheckpoint{
		UserID:              userID,
		AbsUserID:           strings.TrimSpace(absUserID),
		SessionKey:          session.SessionKey,
		ItemKey:             session.ItemKey,
		LastObservedAt:      observedAt,
		LastPositionSeconds: -1,
		LastIsPlaying:       session.IsPlaying,
		LastServerUpdatedAt: session.UpdatedAt,
	}
	if session.HasPosition {
		entry.LastPositionSeconds = session.PositionSeconds
	}
	res := DB.Clauses(absLiveListeningCheckpointOnConflict(observedAt)).Create(&entry)
	if res.Error != nil {
		log.Printf("每日听书活跃会话 checkpoint 写入失败: user=%d session=%s err=%s", userID, formatPlainValue(session.SessionKey), formatPlainError(res.Error))
	}
}

func absLiveListeningCheckpointOnConflict(now time.Time) clause.OnConflict {
	return clause.OnConflict{
		Columns: []clause.Column{
			{Name: "user_id"},
			{Name: "session_key"},
			{Name: "item_key"},
		},
		TargetWhere: clause.Where{Exprs: []clause.Expression{
			clause.Eq{Column: clause.Column{Name: "deleted_at"}, Value: nil},
		}},
		DoUpdates: clause.Assignments(map[string]interface{}{
			"abs_user_id":            gorm.Expr("excluded.abs_user_id"),
			"last_observed_at":       gorm.Expr("excluded.last_observed_at"),
			"last_position_seconds":  gorm.Expr("excluded.last_position_seconds"),
			"last_is_playing":        gorm.Expr("excluded.last_is_playing"),
			"last_server_updated_at": gorm.Expr("excluded.last_server_updated_at"),
			"updated_at":             now,
			"deleted_at":             nil,
		}),
	}
}

func sessionPlayingState(raw map[string]interface{}) (bool, bool) {
	for _, key := range []string{"paused", "isPaused", "is_paused"} {
		if v, ok := boolValue(raw[key]); ok {
			return !v, true
		}
	}
	for _, key := range []string{"playing", "isPlaying", "is_playing", "active", "isActive"} {
		if v, ok := boolValue(raw[key]); ok {
			return v, true
		}
	}
	for _, key := range []string{"state", "status", "playbackStatus", "playback_state"} {
		if s := strings.ToLower(strings.TrimSpace(fmt.Sprint(raw[key]))); s != "" && s != "<nil>" {
			if strings.Contains(s, "paused") || strings.Contains(s, "pause") || strings.Contains(s, "idle") || strings.Contains(s, "stop") {
				return false, true
			}
			if strings.Contains(s, "play") || strings.Contains(s, "active") {
				return true, true
			}
		}
	}
	return false, false
}

func firstString(raw map[string]interface{}, keys ...string) string {
	for _, key := range keys {
		if value := stringValue(raw[key]); value != "" {
			return value
		}
	}
	return ""
}

func nestedFirstString(raw map[string]interface{}, parents []string, keys ...string) string {
	for _, parent := range parents {
		if child, ok := raw[parent].(map[string]interface{}); ok {
			if value := firstString(child, keys...); value != "" {
				return value
			}
		}
	}
	return ""
}

func firstFloat(raw map[string]interface{}, keys ...string) (float64, bool) {
	for _, key := range keys {
		if value, ok := floatValue(raw[key]); ok {
			return value, true
		}
	}
	return 0, false
}

func nestedFirstFloat(raw map[string]interface{}, parents []string, keys ...string) (float64, bool) {
	for _, parent := range parents {
		if child, ok := raw[parent].(map[string]interface{}); ok {
			if value, ok := firstFloat(child, keys...); ok {
				return value, true
			}
		}
	}
	return 0, false
}

func firstTime(raw map[string]interface{}, keys ...string) (time.Time, bool) {
	for _, key := range keys {
		if value, ok := timeValue(raw[key]); ok {
			return value, true
		}
	}
	return time.Time{}, false
}

func stringValue(raw interface{}) string {
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case json.Number:
		return strings.TrimSpace(v.String())
	case float64:
		if v == math.Trunc(v) {
			return strconv.FormatInt(int64(v), 10)
		}
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	}
	return ""
}

func floatValue(raw interface{}) (float64, bool) {
	switch v := raw.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		f, err := v.Float64()
		return f, err == nil
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return f, err == nil
	}
	return 0, false
}

func boolValue(raw interface{}) (bool, bool) {
	switch v := raw.(type) {
	case bool:
		return v, true
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "true", "1", "yes", "playing", "active":
			return true, true
		case "false", "0", "no", "paused", "idle", "stopped":
			return false, true
		}
	}
	return false, false
}

func timeValue(raw interface{}) (time.Time, bool) {
	switch v := raw.(type) {
	case time.Time:
		return v, !v.IsZero()
	case string:
		s := strings.TrimSpace(v)
		if s == "" {
			return time.Time{}, false
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02T15:04:05.000Z07:00", "2006-01-02 15:04:05"} {
			if t, err := time.Parse(layout, s); err == nil {
				return t, true
			}
			if t, err := time.ParseInLocation(layout, s, dailyOperationsLocation); err == nil {
				return t, true
			}
		}
		if n, err := strconv.ParseInt(s, 10, 64); err == nil {
			return unixFlexibleTime(n)
		}
	case float64:
		return unixFlexibleTime(int64(v))
	case int64:
		return unixFlexibleTime(v)
	case int:
		return unixFlexibleTime(int64(v))
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return unixFlexibleTime(n)
		}
	}
	return time.Time{}, false
}

func unixFlexibleTime(n int64) (time.Time, bool) {
	if n <= 0 {
		return time.Time{}, false
	}
	if n > 1_000_000_000_000 {
		return time.UnixMilli(n), true
	}
	return time.Unix(n, 0), true
}
