package main

import (
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	telegramHTTPClientTimeout     = 75 * time.Second
	telegramSendHTTPTimeout       = 12 * time.Second
	telegramCallbackHTTPTimeout   = 4 * time.Second
	telegramLongPollTimeout       = 60
	telegramPollRetryDelay        = 3 * time.Second
	telegramPollConflictError     = 409
	telegramWebhookDropOnStartup  = true
	userLockSlowWaitThreshold     = 2 * time.Second
	messageQueueSlowWaitThreshold = 2 * time.Second
	messageHandleSlowThreshold    = 5 * time.Second
	callbackFastAckDelay          = 1200 * time.Millisecond
	callbackHandleSlowThreshold   = 5 * time.Second
)

var userLocks sync.Map // map[int64]*userLockEntry

type telegramMessageJob struct {
	msg        *tgbotapi.Message
	enqueuedAt time.Time
}

type telegramHTTPClient struct {
	longPoll *http.Client
	callback *http.Client
	send     *http.Client
	upload   *http.Client
}

type userLockEntry struct {
	mu sync.Mutex

	metaMu   sync.Mutex
	lastUsed time.Time
	inUse    int
}

// lockUser 获取用户级锁，并返回释放函数。
// 注意：必须 defer unlock()，不要直接操作 entry.mu。
func lockUser(userID int64) func() {
	now := time.Now()

	lockValue, _ := userLocks.LoadOrStore(userID, &userLockEntry{
		lastUsed: now,
	})

	entry := lockValue.(*userLockEntry)

	entry.metaMu.Lock()
	entry.inUse++
	entry.lastUsed = now
	entry.metaMu.Unlock()

	entry.mu.Lock()

	return func() {
		entry.mu.Unlock()

		entry.metaMu.Lock()
		entry.inUse--
		if entry.inUse < 0 {
			entry.inUse = 0
		}
		entry.lastUsed = time.Now()
		entry.metaMu.Unlock()
	}
}

func newTelegramHTTPClient() *telegramHTTPClient {
	return &telegramHTTPClient{
		longPoll: &http.Client{Timeout: telegramHTTPClientTimeout},
		callback: &http.Client{Timeout: telegramCallbackHTTPTimeout},
		send:     &http.Client{Timeout: telegramSendHTTPTimeout},
		upload:   &http.Client{Timeout: telegramHTTPClientTimeout},
	}
}

func (c *telegramHTTPClient) Do(req *http.Request) (*http.Response, error) {
	if c == nil {
		return http.DefaultClient.Do(req)
	}

	endpoint := telegramEndpointFromRequest(req)
	start := time.Now()
	client := c.clientForEndpoint(endpoint)
	resp, err := client.Do(req)
	duration := time.Since(start)
	statusCode := 0
	if resp != nil {
		statusCode = resp.StatusCode
	}
	recordTelegramAPICall(endpoint, duration, err, statusCode)
	if duration > telegramSendHTTPTimeout && endpoint != "getUpdates" {
		log.Printf("⚠️ Telegram API 调用耗时过长: endpoint=%s duration=%s err=%s", formatPlainValue(endpoint), duration, formatTelegramSendError(err))
	}
	return resp, err
}

func (c *telegramHTTPClient) clientForEndpoint(endpoint string) *http.Client {
	switch endpoint {
	case "getUpdates":
		return c.longPoll
	case "answerCallbackQuery":
		return c.callback
	case "sendDocument", "sendPhoto", "sendVideo", "sendAudio", "sendVoice", "sendAnimation":
		return c.upload
	default:
		return c.send
	}
}

func telegramEndpointFromRequest(req *http.Request) string {
	if req == nil || req.URL == nil {
		return "unknown"
	}
	path := strings.Trim(req.URL.EscapedPath(), "/")
	if path == "" {
		return "unknown"
	}
	parts := strings.Split(path, "/")
	last := parts[len(parts)-1]
	if last == "" {
		return "unknown"
	}
	return last
}

func main() {
	time.Local = time.FixedZone("CST", 8*3600)
	logRuntimeIdentity()

	LoadConfig()
	InitDB()

	// 启动兜底：如果上次机器人在赛马结算前崩溃或重启，自动退还未结算下注。
	recoverActiveRaceBetsOnStartup()
	recoverActiveDiceBetsOnStartup()

	absClient = NewAbsClient()

	bot, err := tgbotapi.NewBotAPIWithClient(
		AppConfig.TgToken,
		tgbotapi.APIEndpoint,
		newTelegramHTTPClient(),
	)
	if err != nil {
		log.Panicf("🤖 Bot 启动失败: %s", formatTelegramSendError(err))
	}

	bot.Debug = false // 建议线上环境关闭上帝视角，避免日志爆炸
	log.Printf("✅ Bot 已成功启动，当前运行账号: %s", formatPlainValue(bot.Self.UserName))
	StartGardenMiniAppServer()

	if info, err := bot.GetWebhookInfo(); err != nil {
		log.Printf("⚠️ 查询 Telegram webhook 状态失败，将继续尝试清理: %s", formatTelegramSendError(err))
	} else if info.URL != "" || info.PendingUpdateCount > 0 {
		log.Printf("ℹ️ Telegram webhook 状态: url_configured=%t pending_updates=%d", info.URL != "", info.PendingUpdateCount)
	}

	if _, err := bot.Request(tgbotapi.DeleteWebhookConfig{DropPendingUpdates: telegramWebhookDropOnStartup}); err != nil {
		log.Fatalf("❌ 清理 Telegram webhook 失败，长轮询无法可靠接收消息: %s", formatTelegramSendError(err))
	}
	log.Printf("✅ Telegram webhook 已清理，开始使用长轮询接收消息: drop_pending=%t", telegramWebhookDropOnStartup)
	writeStartupHealth("webhook_cleared", bot.Self.UserName)
	notifyAdminsOnStartup(bot)
	if err := initializeMarketplaceListingExpiry(DB, time.Now()); err != nil {
		log.Fatalf("❌ 初始化交易行历史商品 48 小时下架倒计时失败，禁止启动: %s", formatPlainError(err))
	}

	sweeperOnce.Do(func() {
		DB.AutoMigrate(&AutoDeleteMsg{})
		startMessageSweeper(bot)
	})

	StartTelegramDispatcher(bot)

	// 正式启动后台核心任务（生命周期巡检与加密备份）
	startBackgroundJobs(bot)

	// 🚨 新增核心：启动全自动榜单定时投递引擎 (包含日榜、周榜、月榜及热门书单)
	StartLeaderboardScheduler(bot)
	StartWorldBossScheduler(bot)
	StartSectSecretRealmScheduler(bot)
	StartLotteryScheduler(bot)
	StartSectLotteryScheduler(bot)
	StartSectHornDispatcher(bot)
	StartMarketplaceExpiryScheduler(bot)
	StartGardenMaturityNotifier(bot)

	// 🚨 修复隐患 2：实现真正的高并发 Worker 工作池
	jobs := make(chan telegramMessageJob, AppConfig.QueueCapacity)
	recordMessageQueueState(0, cap(jobs))
	for w := 1; w <= AppConfig.WorkerCount; w++ {
		go botWorker(bot, jobs)
	}

	pollTelegramUpdates(bot, jobs)
}

func pollTelegramUpdates(bot *tgbotapi.BotAPI, jobs chan<- telegramMessageJob) {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = telegramLongPollTimeout
	u.AllowedUpdates = []string{"message", "callback_query"}

	log.Printf("✅ Telegram 长轮询已启动: timeout=%ds allowed_updates=%s", telegramLongPollTimeout, formatPlainValue(strings.Join(u.AllowedUpdates, ",")))
	writeStartupHealth("polling_started", bot.Self.UserName)

	for {
		updates, err := bot.GetUpdates(u)
		if err != nil {
			var apiErr *tgbotapi.Error
			if errors.As(err, &apiErr) && apiErr.Code == telegramPollConflictError {
				log.Fatalf("❌ Telegram getUpdates 冲突：同一个 Bot Token 正被其他进程或部署实例接收更新，请先停止旧实例。err=%s", formatTelegramSendError(err))
			}

			log.Printf("⚠️ Telegram getUpdates 失败，%s 后重试: %s", telegramPollRetryDelay, formatTelegramSendError(err))
			time.Sleep(telegramPollRetryDelay)
			continue
		}

		if len(updates) == 0 {
			continue
		}

		firstID := updates[0].UpdateID
		lastID := updates[len(updates)-1].UpdateID
		recordMessageQueueState(len(jobs), cap(jobs))
		log.Printf("📥 收到 Telegram updates: count=%d first=%d last=%d queue=%d/%d", len(updates), firstID, lastID, len(jobs), cap(jobs))

		for _, update := range updates {
			if update.UpdateID >= u.Offset {
				u.Offset = update.UpdateID + 1
			}
			dispatchTelegramUpdate(bot, jobs, update)
		}
	}
}

func dispatchTelegramUpdate(bot *tgbotapi.BotAPI, jobs chan<- telegramMessageJob, update tgbotapi.Update) {
	if update.Message != nil {
		logIncomingMessage(update.Message)
		if handlePrivateStartFastPath(bot, update.Message) {
			return
		}
		select {
		case jobs <- telegramMessageJob{msg: update.Message, enqueuedAt: time.Now()}:
			recordMessageQueueState(len(jobs), cap(jobs))
		default:
			recordMessageQueueDropped()
			fromID := int64(0)
			if update.Message.From != nil {
				fromID = update.Message.From.ID
			}
			log.Printf("⚠️ 消息队列已满，丢弃消息: user=%d chat=%d", fromID, update.Message.Chat.ID)
			replyText(bot, update.Message.Chat.ID, "⚠️ 当前系统繁忙，请稍后再试。")
		}
	}

	if update.CallbackQuery != nil {
		go func(cb *tgbotapi.CallbackQuery) {
			if cb.From == nil {
				return
			}
			defer logCallbackHandlingDuration(cb, time.Now())
			startDelayedCallbackAck(bot, cb.ID)
			unlock := lockUser(cb.From.ID)
			defer unlock()
			if handleGardenCallback(bot, cb) {
				return
			}
			if handleSectMemberPageCallback(bot, cb) {
				return
			}
			if handleMenuCallback(bot, cb) {
				return
			}
			if handleAdminMenuCallback(bot, cb) {
				return
			}
			handleBookRequestCallback(bot, cb)
		}(update.CallbackQuery)
	}
}

func handlePrivateStartFastPath(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) bool {
	if msg == nil || msg.Chat == nil || msg.From == nil || !msg.Chat.IsPrivate() {
		return false
	}
	if !isTelegramCommandText(msg.Text, "/start") {
		return false
	}
	if _, ok := parseReferralStartPayload(msg.Text); ok {
		return false
	}

	userID := msg.From.ID
	chatID := msg.Chat.ID
	clearSession(userID)
	log.Printf("⚡ 私聊 /start 快速响应: user=%d chat=%d message_id=%d", userID, chatID, msg.MessageID)

	if err := sendUserMainMenu(bot, chatID, "👋 欢迎使用【悦耳声阅】用户管理系统："); err != nil {
		msg := tgbotapi.NewMessage(chatID, "欢迎使用【悦耳声阅】。主菜单暂时发送失败，请稍后再试或联系管理员查看 Bot 日志。")
		if _, fallbackErr := sendNoAutoDelete(bot, msg); fallbackErr != nil {
			log.Printf("发送 /start 纯文本兜底失败: chat=%d err=%s", chatID, formatTelegramSendError(fallbackErr))
		}
	}

	return true
}

func notifyAdminsOnStartup(bot *tgbotapi.BotAPI) {
	if AppConfig == nil || !AppConfig.StartupNotifyAdmins {
		return
	}

	text := "【悦耳声阅 Bot 启动自检】\n" +
		"进程已完成 Telegram 登录和 webhook 清理，即将进入长轮询。\n" +
		"如果你能收到这条消息，说明当前运行实例的 Token 和 Telegram 发送链路可用。"

	for adminID := range AppConfig.AdminIDs {
		msg := tgbotapi.NewMessage(adminID, text)
		if _, err := sendNoAutoDelete(bot, msg); err != nil {
			log.Printf("发送启动自检通知失败: admin=%d err=%s", adminID, formatTelegramSendError(err))
			continue
		}
		log.Printf("✅ 已发送启动自检通知: admin=%d", adminID)
	}
}

func writeStartupHealth(stage string, botUsername string) {
	if AppConfig == nil {
		return
	}
	dir := sqliteDatabaseDir(AppConfig.DbURL)
	if dir == "" {
		return
	}
	content := "stage=" + stage + "\n" +
		"bot=" + formatPlainValue(botUsername) + "\n" +
		"at=" + time.Now().Format(time.RFC3339) + "\n"

	path := filepath.Join(dir, "bot_startup_health.txt")
	if err := os.WriteFile(path, []byte(content), 0640); err != nil {
		log.Printf("⚠️ 写入启动健康标记失败: path=%s err=%s", formatPlainValue(path), formatPlainError(err))
	}
}

func logRuntimeIdentity() {
	exePath, exeErr := os.Executable()
	cwd, cwdErr := os.Getwd()

	exeModTime := ""
	if exeErr == nil {
		if stat, err := os.Stat(exePath); err == nil {
			exeModTime = stat.ModTime().Format(time.RFC3339)
		}
	}

	if exeErr != nil {
		exePath = "unknown: " + formatPlainError(exeErr)
	}
	if cwdErr != nil {
		cwd = "unknown: " + formatPlainError(cwdErr)
	}

	log.Printf("ℹ️ 运行身份: exe=%s exe_mtime=%s cwd=%s", formatPlainValue(exePath), formatPlainValue(exeModTime), formatPlainValue(cwd))
}

func logIncomingMessage(msg *tgbotapi.Message) {
	if msg == nil || msg.Chat == nil {
		return
	}

	fromID := int64(0)
	if msg.From != nil {
		fromID = msg.From.ID
	}

	text := ""
	if fields := strings.Fields(msg.Text); len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		text = strings.ToLower(fields[0])
	}

	if text != "" {
		log.Printf("📨 Telegram 命令入队: command=%s user=%d chat=%d message_id=%d", formatPlainValue(text), fromID, msg.Chat.ID, msg.MessageID)
	}
}

func logMessageHandlingDuration(msg *tgbotapi.Message, startedAt time.Time) {
	if msg == nil || msg.Chat == nil || startedAt.IsZero() {
		return
	}
	duration := time.Since(startedAt)
	if duration <= messageHandleSlowThreshold {
		return
	}
	recordSlowMessageHandle()
	fromID := int64(0)
	if msg.From != nil {
		fromID = msg.From.ID
	}
	command := ""
	if fields := strings.Fields(msg.Text); len(fields) > 0 && strings.HasPrefix(fields[0], "/") {
		command = strings.ToLower(fields[0])
	}
	log.Printf("⚠️ Telegram 消息处理耗时过长: user=%d chat=%d message_id=%d command=%s duration=%s", fromID, msg.Chat.ID, msg.MessageID, formatPlainValue(command), duration)
}

func logCallbackHandlingDuration(cb *tgbotapi.CallbackQuery, startedAt time.Time) {
	if cb == nil || cb.From == nil || startedAt.IsZero() {
		return
	}
	duration := time.Since(startedAt)
	if duration <= callbackHandleSlowThreshold {
		return
	}
	recordSlowCallbackHandle()
	chatID := int64(0)
	messageID := 0
	if cb.Message != nil && cb.Message.Chat != nil {
		chatID = cb.Message.Chat.ID
		messageID = cb.Message.MessageID
	}
	log.Printf("⚠️ Telegram callback 处理耗时过长: user=%d chat=%d message_id=%d data=%s duration=%s", cb.From.ID, chatID, messageID, formatPlainValue(cb.Data), duration)
}

// 高并发消费者
func botWorker(bot *tgbotapi.BotAPI, jobs <-chan telegramMessageJob) {
	for job := range jobs {
		func() {
			startedAt := time.Now()
			defer func() {
				if r := recover(); r != nil {
					log.Printf("⚠️ botWorker 处理消息时发生 panic，已恢复: panic=%s", formatPlainValue(r))
				}
			}()

			msg := job.msg
			if msg == nil {
				return
			}
			if queueWait := time.Since(job.enqueuedAt); !job.enqueuedAt.IsZero() && queueWait > messageQueueSlowWaitThreshold {
				fromID := int64(0)
				if msg.From != nil {
					fromID = msg.From.ID
				}
				log.Printf("⚠️ Telegram 消息队列等待过久: user=%d chat=%d message_id=%d wait=%s queue=%d/%d", fromID, msg.Chat.ID, msg.MessageID, queueWait, len(jobs), cap(jobs))
			}
			recordMessageQueueState(len(jobs), cap(jobs))
			defer logMessageHandlingDuration(msg, startedAt)

			// 没有 From 的消息直接忽略，避免 handleInteractiveMessage 空指针 panic
			if msg.From == nil {
				log.Printf("⚠️ 忽略无 From 的消息: chat=%d message_id=%d", msg.Chat.ID, msg.MessageID)
				return
			}
			lockWaitStart := time.Now()
			unlock := lockUser(msg.From.ID)
			if wait := time.Since(lockWaitStart); wait > userLockSlowWaitThreshold {
				recordSlowUserLockWait()
				log.Printf("⚠️ 用户锁等待过久: user=%d chat=%d message_id=%d wait=%s", msg.From.ID, msg.Chat.ID, msg.MessageID, wait)
			}
			defer unlock()
			if isTelegramCommandText(msg.Text, "/start") {
				log.Printf("ℹ️ worker 处理 /start: user=%d chat=%d message_id=%d", msg.From.ID, msg.Chat.ID, msg.MessageID)
			}
			handleInteractiveMessage(bot, msg)
		}()
	}
}
