package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

// 解析统计接口的专用结构体
type AbsStatsData struct {
	Days           map[string]float64 `json:"days"`
	RecentSessions []struct {
		Date          string  `json:"date"`
		TimeListening float64 `json:"timeListening"`
		MediaMetadata struct {
			Title string `json:"title"`
		} `json:"mediaMetadata"`
	} `json:"recentSessions"`
}

// 排序用的键值对结构
type kv struct {
	Key   string
	Value float64
}

// 新增：用于记录书籍详细数据的结构体
type BookStat struct {
	ListenCount int
	TotalTime   float64
}

// 新增：书籍排序专用的键值对结构
type bookKV struct {
	Key  string
	Stat *BookStat
}

var lastDailyRun, lastWeeklyRun, lastMonthlyRun string

var leaderboardLocation = time.FixedZone("CST", 8*3600)

const leaderboardMaxRawSecondsPerDay = 24 * 3600

// ⏱️ 启动纯原生定时调度器 (无第三方依赖)
func StartLeaderboardScheduler(bot *tgbotapi.BotAPI) {
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		for {
			now := <-ticker.C
			localNow := now.In(leaderboardLocation)
			// 设定每天早上 08:00 准时结算投递
			if localNow.Hour() == 8 && localNow.Minute() == 0 {
				dateStr := localNow.Format("2006-01-02")

				// 1. 日榜：每天都发
				if lastDailyRun != dateStr {
					lastDailyRun = dateStr
					go GenerateAndSendLeaderboard(bot, "daily", localNow)
				}

				// 2. 周榜：每周一发
				if localNow.Weekday() == time.Monday && lastWeeklyRun != dateStr {
					lastWeeklyRun = dateStr
					go GenerateAndSendLeaderboard(bot, "weekly", localNow)
				}

				// 3. 月榜：每月 1 号发
				if localNow.Day() == 1 && lastMonthlyRun != dateStr {
					lastMonthlyRun = dateStr
					go GenerateAndSendLeaderboard(bot, "monthly", localNow)
				}
			}
		}
	}()
	log.Println("✅ 榜单自动化引擎已启动，每日 08:00 准时播报。")
}

// 📊 核心聚合并生成榜单
func GenerateAndSendLeaderboard(bot *tgbotapi.BotAPI, timeframe string, now time.Time) {
	if AppConfig.NoticeGroupID == 0 {
		return // 未配置大群，终止发送
	}
	localNow := now.In(leaderboardLocation)

	var users []User
	// 提取所有绑定了 ABS 账号的真实用户
	if err := DB.Where("abs_user_id != ''").Find(&users).Error; err != nil {
		log.Printf("⚠️ 榜单用户列表读取失败: timeframe=%s err=%s", formatPlainValue(timeframe), formatPlainError(err))
		return
	}
	if len(users) == 0 {
		return
	}

	// 确定时间过滤范围
	validDates := make(map[string]bool)
	var title string

	switch timeframe {
	case "daily":
		title = "🏆 **昨日听书风云榜**"
		validDates[localNow.AddDate(0, 0, -1).Format("2006-01-02")] = true
	case "weekly":
		title = "🏆 **上周听书风云榜**"
		for i := 1; i <= 7; i++ {
			validDates[localNow.AddDate(0, 0, -i).Format("2006-01-02")] = true
		}
	case "monthly":
		title = "🏆 **上月听书风云榜**"
		lastMonth := localNow.AddDate(0, -1, 0).Format("2006-01")
		validDates[lastMonth] = true
	}
	monthPrefix := localNow.AddDate(0, -1, 0).Format("2006-01")

	userTimeMap := make(map[string]float64)
	// 🚨 升级1：将单一的时长 Map 升级为多维度的结构体 Map
	bookStatsMap := make(map[string]*BookStat)
	statsSuccess := 0
	statsRequestFailures := 0
	statsParseFailures := 0

	var wg sync.WaitGroup
	var dataMu sync.Mutex
	semaphore := make(chan struct{}, 5)

	// 遍历查询所有用户数据 (并发执行)
	for _, u := range users {
		wg.Add(1)
		go func(user User) {
			defer wg.Done()

			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			body, code, err := absClient.sendRequest("GET", absUserListeningStatsPath(user.AbsUserID), nil)
			if err != nil || code != 200 {
				dataMu.Lock()
				statsRequestFailures++
				dataMu.Unlock()
				return
			}

			var stats AbsStatsData
			if err := json.Unmarshal(body, &stats); err != nil {
				dataMu.Lock()
				statsParseFailures++
				dataMu.Unlock()
				return
			}

			localUserTime := 0.0
			// 局部字典也同步升级
			localBookMap := make(map[string]*BookStat)

			// 统计该用户总时长
			for date, seconds := range stats.Days {
				isValid := isLeaderboardDateValid(date, timeframe, validDates, monthPrefix)
				if isValid && seconds > 0 {
					localUserTime += capLeaderboardDailySeconds(seconds)
				}
			}

			// 🚨 升级2：统计热门书籍（同时记录次数和时长）
			for _, session := range stats.RecentSessions {
				isValid := isLeaderboardDateValid(session.Date, timeframe, validDates, monthPrefix)
				bookName := session.MediaMetadata.Title
				if isValid && session.TimeListening > 0 && bookName != "" {
					if _, exists := localBookMap[bookName]; !exists {
						localBookMap[bookName] = &BookStat{}
					}
					// 每次有效会话记为 1 次收听
					localBookMap[bookName].ListenCount++
					localBookMap[bookName].TotalTime += session.TimeListening
				}
			}

			dataMu.Lock()
			statsSuccess++
			userTimeMap[user.Username] += localUserTime
			// 🚨 升级3：合并用户的局部书籍数据到全局
			for book, stat := range localBookMap {
				if _, exists := bookStatsMap[book]; !exists {
					bookStatsMap[book] = &BookStat{}
				}
				bookStatsMap[book].ListenCount += stat.ListenCount
				bookStatsMap[book].TotalTime += stat.TotalTime
			}
			dataMu.Unlock()

		}(u)
	}

	wg.Wait()
	if statsRequestFailures > 0 || statsParseFailures > 0 {
		log.Printf("⚠️ 榜单部分用户统计读取失败: timeframe=%s total=%d success=%d request_failed=%d parse_failed=%d",
			formatPlainValue(timeframe), len(users), statsSuccess, statsRequestFailures, statsParseFailures)
	}
	if statsSuccess == 0 {
		log.Printf("⚠️ 榜单统计全部失败，跳过本期发送: timeframe=%s total=%d", formatPlainValue(timeframe), len(users))
		return
	}

	finalMsg := title + "\n\n"

	// 1. 排序并截断用户榜
	userTop := sortMap(userTimeMap, 10)
	if len(userTop) > 0 {
		finalMsg += "⏱ **肝帝排行榜 (总时长)**\n"
		medals := []string{"🥇", "🥈", "🥉", "4️⃣", "5️⃣", "6️⃣", "7️⃣", "8️⃣", "9️⃣", "🔟"}
		for i, kv := range userTop {
			finalMsg += fmt.Sprintf("%s **%s** - `%.1f` 小时\n", medals[i], escapeMarkdown(kv.Key), kv.Value/3600.0)
		}
	} else {
		finalMsg += "⏱ **肝帝排行榜**\n🫙 本周期内无道友闭关听书...\n"
	}

	finalMsg += "\n"

	// 🚨 升级4：全新的双维度独立排序逻辑
	var bookTop []bookKV
	for k, v := range bookStatsMap {
		bookTop = append(bookTop, bookKV{k, v})
	}

	sort.Slice(bookTop, func(i, j int) bool {
		// 主条件：按收听次数降序
		if bookTop[i].Stat.ListenCount != bookTop[j].Stat.ListenCount {
			return bookTop[i].Stat.ListenCount > bookTop[j].Stat.ListenCount
		}
		// 次条件：次数相同，按总时长降序
		return bookTop[i].Stat.TotalTime > bookTop[j].Stat.TotalTime
	})

	// 截取前 5 名
	if len(bookTop) > 5 {
		bookTop = bookTop[:5]
	}

	// 🚨 升级5：渲染格式调整，展示双维度
	if len(bookTop) > 0 {
		finalMsg += "📚 **万界风云书单 (热门追更)**\n"
		bookMedals := []string{"🔥", "🌟", "✨", "📖", "📖"}
		for i, kv := range bookTop {
			finalMsg += fmt.Sprintf("%s 《%s》 - 收听 `%d` 次 | 累计 `%.1f` 小时\n", bookMedals[i], escapeMarkdown(kv.Key), kv.Stat.ListenCount, kv.Stat.TotalTime/3600.0)
		}
	} else {
		finalMsg += "📚 **万界风云书单**\n🫙 暂无热门书籍上榜...\n"
	}

	if AppConfig.NoticeGroupID != 0 {
		sendAndManageLeaderboardPin(bot, AppConfig.NoticeGroupID, finalMsg, timeframe)
	}
}

func normalizeLeaderboardDateKey(date string) string {
	date = strings.TrimSpace(date)
	if len(date) >= len("2006-01-02") {
		return date[:len("2006-01-02")]
	}
	return date
}

func isLeaderboardDateValid(date string, timeframe string, validDates map[string]bool, monthPrefix string) bool {
	dayKey := normalizeLeaderboardDateKey(date)
	if dayKey == "" {
		return false
	}
	if timeframe == "monthly" {
		return strings.HasPrefix(dayKey, monthPrefix)
	}
	return validDates[dayKey]
}

func capLeaderboardDailySeconds(seconds float64) float64 {
	if seconds <= 0 {
		return 0
	}
	if seconds > leaderboardMaxRawSecondsPerDay {
		return leaderboardMaxRawSecondsPerDay
	}
	return seconds
}

// 辅助函数：字典按 Value 降序排序并截取 Top N
func sortMap(m map[string]float64, topN int) []kv {
	var ss []kv
	for k, v := range m {
		ss = append(ss, kv{k, v})
	}
	sort.Slice(ss, func(i, j int) bool {
		return ss[i].Value > ss[j].Value
	})
	if len(ss) > topN {
		ss = ss[:topN]
	}
	return ss
}

// =========================================================================
// 🏆 榜单专属：全自动动态置顶与新老替换引擎 (永久留存版)
// =========================================================================
func sendAndManageLeaderboardPin(bot *tgbotapi.BotAPI, chatID int64, text string, timeframe string) {
	if bot == nil || chatID == 0 {
		return
	}
	if enqueueTelegramAsync(telegramAsyncJob{
		Kind:        "leaderboard_pin",
		DedupeKey:   "leaderboard_pin:" + timeframe,
		Priority:    telegramAsyncPriorityNormal,
		MaxAttempts: 1,
		Send: func() error {
			return sendAndManageLeaderboardPinSync(bot, chatID, text, timeframe)
		},
	}) {
		return
	}

	log.Printf("⚠️ [%s] 榜单发送异步入队失败，改为同步发送", formatPlainValue(timeframe))
	if err := sendAndManageLeaderboardPinSync(bot, chatID, text, timeframe); err != nil {
		log.Printf("❌ 【天机阁异常】[%s] 榜单同步发送失败: %s", formatPlainValue(timeframe), formatTelegramSendError(err))
	}
}

func sendAndManageLeaderboardPinSync(bot *tgbotapi.BotAPI, chatID int64, text string, timeframe string) error {
	// 1. 动态构建该时间周期榜单在 SystemConfig 表中的唯一辨识 Key
	configKey := "pinned_leaderboard_" + timeframe

	// 2. 预检数据库，读取上一期历史置顶的 MessageID
	var cfg SystemConfig
	oldMsgID := 0
	if err := DB.Where("key = ?", configKey).First(&cfg).Error; err == nil && cfg.Value != "" {
		if parsed, parseErr := strconv.Atoi(cfg.Value); parseErr == nil {
			oldMsgID = parsed
		} else {
			log.Printf("⚠️ 【天机阁异常】[%s] 旧榜单置顶消息ID解析失败: key=%s value=%s err=%s",
				formatPlainValue(timeframe),
				formatPlainValue(configKey),
				formatPlainValue(cfg.Value),
				formatPlainError(parseErr),
			)
		}
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		log.Printf("⚠️ 【天机阁异常】[%s] 旧榜单置顶状态读取失败: key=%s err=%s",
			formatPlainValue(timeframe),
			formatPlainValue(configKey),
			formatPlainError(err),
		)
	}

	// 3. 使用原生安全通道投递新榜单（不走 sendGroupAutoDeleteMessage，从而永久留存）
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	sentMsg, err := bot.Send(msg)
	if err != nil {
		log.Printf("❌ 【天机阁异常】新一期 [%s] 榜单发送失败: %s", formatPlainValue(timeframe), formatTelegramSendError(err))
		return err
	}

	// 4. 优雅换防：若存在历史老置顶，执行物理摘除 (Unpin)
	if oldMsgID > 0 {
		unpinCfg := tgbotapi.UnpinChatMessageConfig{
			ChatID:    chatID,
			MessageID: oldMsgID,
		}
		if _, unpinErr := bot.Request(unpinCfg); unpinErr != nil && !isTerminalTelegramUnpinError(unpinErr) {
			log.Printf("⚠️ 【天机阁异常】[%s] 取消旧榜单置顶失败: chat=%d message_id=%d err=%s",
				formatPlainValue(timeframe),
				chatID,
				oldMsgID,
				formatTelegramSendError(unpinErr),
			)
		}
	}

	// 5. 挂载新尊：将最新发送的榜单推上置顶宝座，并开启纯静默模式
	pinCfg := tgbotapi.PinChatMessageConfig{
		ChatID:              chatID,
		MessageID:           sentMsg.MessageID,
		DisableNotification: true, // 🚨 关键：开启静默置顶
	}

	if _, pinErr := bot.Request(pinCfg); pinErr != nil {
		log.Printf("⚠️ 【天道受限】榜单置顶失败！请检查 Bot 是否在群内拥有「置顶消息」的管理员特权: %s", formatTelegramSendError(pinErr))
	}

	// 6. 状态持久化：将本期最新的 MessageID 写入或更新进系统配置表
	newValue := strconv.Itoa(sentMsg.MessageID)
	if err := setSystemConfigStringChecked(configKey, newValue); err != nil {
		log.Printf("⚠️ 榜单置顶状态写入失败: key=%s value=%s err=%s", formatPlainValue(configKey), formatPlainValue(newValue), formatPlainError(err))
	}

	log.Printf("✅ 【天道榜单巡查】[%s] 榜单交接顺利完成，新置顶 ID: %s", formatPlainValue(timeframe), formatPlainValue(newValue))
	return nil
}
