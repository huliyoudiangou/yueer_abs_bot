package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"log"
	"sync"
	"time"
)

// ==========================================
// 🛡️ 状态机底层：高并发安全封装与防泄漏
// ==========================================
type SessionState struct {
	step      string            // 私有化，强制走方法
	tempData  map[string]string // 私有化，强制走方法
	mu        sync.RWMutex      // 升级为读写锁，提升超高频读取时的并发性能
	updatedAt time.Time         // 🛡️ 新增：最后活跃时间，供清道夫协程识别僵尸会话
}

var callbackAckStates sync.Map // map[string]*atomic.Bool

// SetStep 安全写入当前步骤
func (s *SessionState) SetStep(step string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.step = step
	s.updatedAt = time.Now()
}

// GetStep 安全读取当前步骤
func (s *SessionState) GetStep() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.step
}

// SetTemp 安全写入临时数据
func (s *SessionState) SetTemp(key, val string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.tempData == nil {
		s.tempData = make(map[string]string)
	}
	s.tempData[key] = val
	s.updatedAt = time.Now()
}

// GetTemp 安全读取临时数据
func (s *SessionState) GetTemp(key string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.tempData == nil {
		return ""
	}
	return s.tempData[key]
}

type AutoDeleteMsg struct {
	ID        uint  `gorm:"primaryKey"`
	ChatID    int64 `gorm:"index"`
	MessageID int
	DeleteAt  time.Time `gorm:"index"`
}

var UserSessions sync.Map
var absClient *AbsClient
var sweeperOnce sync.Once
var groupMemberCache sync.Map  // 缓存群成员状态，防 TG 接口频控
var fusionPoolMutex sync.Mutex // 🌊 新增：天道奖池独立并发锁

const (
	groupMemberPositiveTTL = 5 * time.Minute
	groupMemberNegativeTTL = 1 * time.Minute
	groupMemberFreshTTL    = 30 * time.Second
	blindBoxCost           = 20
)

func getSession(userID int64) *SessionState {
	val, _ := UserSessions.LoadOrStore(userID, &SessionState{
		step:      "IDLE",
		tempData:  make(map[string]string),
		updatedAt: time.Now(),
	})
	return val.(*SessionState)
}

func clearSession(userID int64) {
	UserSessions.Delete(userID)
}

func generateRandomCode(length int) string {
	if length <= 0 {
		length = 16
	}

	bytes := make([]byte, (length+1)/2)
	if _, err := rand.Read(bytes); err != nil {
		log.Printf("❌ 生成随机码失败: %s", formatPlainError(err))
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}

	code := hex.EncodeToString(bytes)
	if len(code) > length {
		code = code[:length]
	}
	return code
}

func getManualPillUsageConfig(itemName string, t time.Time) (periodStart time.Time, periodKey string, maxCount int, cycleName string, addHours float64, ok bool) {
	loc := time.FixedZone("CST", 8*3600)
	now := t.In(loc)

	switch itemName {
	case "聚灵丹":
		maxCount = 3
		cycleName = "本周"
		addHours = 1.0
	case "九转造化丹":
		maxCount = 2
		cycleName = "本周"
		addHours = 3.0
	case "万年仙玉髓":
		maxCount = 1
		cycleName = "本月"
		addHours = 10.0
	default:
		return time.Time{}, "", 0, "", 0, false
	}

	if itemName == "万年仙玉髓" {
		periodStart = time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)
		periodKey = fmt.Sprintf("%04d-%02d", now.Year(), int(now.Month()))
	} else {
		weekday := int(now.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		todayZero := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
		periodStart = todayZero.AddDate(0, 0, -(weekday - 1))
		isoYear, isoWeek := now.ISOWeek()
		periodKey = fmt.Sprintf("%04d-W%02d", isoYear, isoWeek)
	}

	return periodStart, periodKey, maxCount, cycleName, addHours, true
}

func countManualPillUsage(userID int64, itemName string, periodStart time.Time) (int64, error) {
	var usedCount int64
	err := DB.Model(&ItemUsageLog{}).
		Where("user_id = ? AND item_name = ? AND used_at >= ?", userID, itemName, periodStart).
		Count(&usedCount).Error
	return usedCount, err
}

func manualPillUsageCountText(usedCount int64, maxCount int, available bool) string {
	if !available {
		return fmt.Sprintf("读取失败/%d", maxCount)
	}
	return fmt.Sprintf("%d/%d", usedCount, maxCount)
}
