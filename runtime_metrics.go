package main

import (
	"fmt"
	"sync/atomic"
	"time"
)

type runtimeMetrics struct {
	startedAt time.Time

	messageQueueLen atomic.Int64
	messageQueueCap atomic.Int64
	sendQueueLen    atomic.Int64
	sendQueueCap    atomic.Int64

	telegramAPIOK            atomic.Uint64
	telegramAPIFailed        atomic.Uint64
	telegramGetUpdatesOK     atomic.Uint64
	telegramGetUpdatesFailed atomic.Uint64
	telegramSendTotalMillis  atomic.Uint64
	telegramSendSamples      atomic.Uint64
	telegramSendMaxMillis    atomic.Uint64

	callbackFastAck     atomic.Uint64
	slowUserLockWait    atomic.Uint64
	slowMessageHandle   atomic.Uint64
	slowCallbackHandle  atomic.Uint64
	messageQueueDropped atomic.Uint64

	asyncSendEnqueued atomic.Uint64
	asyncSendDropped  atomic.Uint64
	asyncSendOK       atomic.Uint64
	asyncSendFailed   atomic.Uint64
	asyncSendRetried  atomic.Uint64

	lastTelegramError   atomic.Value // string
	lastGetUpdatesError atomic.Value // string
	lastAsyncSendError  atomic.Value // string
}

var appRuntimeMetrics = newRuntimeMetrics()

func newRuntimeMetrics() *runtimeMetrics {
	return &runtimeMetrics{startedAt: time.Now()}
}

func recordMessageQueueState(length int, capacity int) {
	appRuntimeMetrics.messageQueueLen.Store(int64(length))
	appRuntimeMetrics.messageQueueCap.Store(int64(capacity))
}

func recordTelegramSendQueueState(length int, capacity int) {
	appRuntimeMetrics.sendQueueLen.Store(int64(length))
	appRuntimeMetrics.sendQueueCap.Store(int64(capacity))
}

func recordTelegramAPICall(endpoint string, duration time.Duration, err error, statusCode int) {
	if endpoint == "" {
		endpoint = "unknown"
	}

	failed := err != nil || statusCode >= 500
	if endpoint == "getUpdates" {
		if failed {
			appRuntimeMetrics.telegramGetUpdatesFailed.Add(1)
			appRuntimeMetrics.lastGetUpdatesError.Store(formatTelegramMetricError(endpoint, err, statusCode))
		} else {
			appRuntimeMetrics.telegramGetUpdatesOK.Add(1)
		}
		return
	}

	if failed {
		appRuntimeMetrics.telegramAPIFailed.Add(1)
		appRuntimeMetrics.lastTelegramError.Store(formatTelegramMetricError(endpoint, err, statusCode))
	} else {
		appRuntimeMetrics.telegramAPIOK.Add(1)
	}

	millis := uint64(duration.Milliseconds())
	appRuntimeMetrics.telegramSendTotalMillis.Add(millis)
	appRuntimeMetrics.telegramSendSamples.Add(1)
	updateMaxUint64(&appRuntimeMetrics.telegramSendMaxMillis, millis)
}

func formatTelegramMetricError(endpoint string, err error, statusCode int) string {
	safeEndpoint := formatPlainValue(endpoint)
	if err != nil {
		return fmt.Sprintf("%s: %s", safeEndpoint, formatTelegramSendError(err))
	}
	if statusCode > 0 {
		return fmt.Sprintf("%s: HTTP_%d", safeEndpoint, statusCode)
	}
	return safeEndpoint + ": unknown"
}

func updateMaxUint64(slot *atomic.Uint64, value uint64) {
	for {
		current := slot.Load()
		if value <= current {
			return
		}
		if slot.CompareAndSwap(current, value) {
			return
		}
	}
}

func recordCallbackFastAck() {
	appRuntimeMetrics.callbackFastAck.Add(1)
}

func recordSlowUserLockWait() {
	appRuntimeMetrics.slowUserLockWait.Add(1)
}

func recordSlowMessageHandle() {
	appRuntimeMetrics.slowMessageHandle.Add(1)
}

func recordSlowCallbackHandle() {
	appRuntimeMetrics.slowCallbackHandle.Add(1)
}

func recordMessageQueueDropped() {
	appRuntimeMetrics.messageQueueDropped.Add(1)
}

func recordAsyncTelegramEnqueued() {
	appRuntimeMetrics.asyncSendEnqueued.Add(1)
}

func recordAsyncTelegramDropped() {
	appRuntimeMetrics.asyncSendDropped.Add(1)
}

func recordAsyncTelegramOK() {
	appRuntimeMetrics.asyncSendOK.Add(1)
}

func recordAsyncTelegramFailed(err error) {
	appRuntimeMetrics.asyncSendFailed.Add(1)
	if err != nil {
		appRuntimeMetrics.lastAsyncSendError.Store(formatTelegramSendError(err))
	}
}

func recordAsyncTelegramRetried() {
	appRuntimeMetrics.asyncSendRetried.Add(1)
}

func runtimeMetricString(value atomic.Value) string {
	if v, ok := value.Load().(string); ok && v != "" {
		return v
	}
	return "无"
}

func formatRuntimeMetricsReport() string {
	uptime := time.Since(appRuntimeMetrics.startedAt).Truncate(time.Second)
	samples := appRuntimeMetrics.telegramSendSamples.Load()
	avgMillis := uint64(0)
	if samples > 0 {
		avgMillis = appRuntimeMetrics.telegramSendTotalMillis.Load() / samples
	}

	return fmt.Sprintf(
		"运行观测：\n"+
			"进程运行：`%s`\n"+
			"消息队列：`%d/%d`，丢弃：`%d`\n"+
			"发送队列：`%d/%d`，入队/成功/失败/丢弃/重试：`%d/%d/%d/%d/%d`\n"+
			"Telegram API：成功 `%d` / 失败 `%d`，发送平均/最大耗时：`%dms/%dms`\n"+
			"getUpdates：成功 `%d` / 失败 `%d`\n"+
			"慢路径：用户锁 `%d`，消息处理 `%d`，callback `%d`，callback兜底 `%d`\n"+
			"最近 Telegram 错误：`%s`\n"+
			"最近 getUpdates 错误：`%s`\n"+
			"最近异步发送错误：`%s`",
		uptime,
		appRuntimeMetrics.messageQueueLen.Load(),
		appRuntimeMetrics.messageQueueCap.Load(),
		appRuntimeMetrics.messageQueueDropped.Load(),
		appRuntimeMetrics.sendQueueLen.Load(),
		appRuntimeMetrics.sendQueueCap.Load(),
		appRuntimeMetrics.asyncSendEnqueued.Load(),
		appRuntimeMetrics.asyncSendOK.Load(),
		appRuntimeMetrics.asyncSendFailed.Load(),
		appRuntimeMetrics.asyncSendDropped.Load(),
		appRuntimeMetrics.asyncSendRetried.Load(),
		appRuntimeMetrics.telegramAPIOK.Load(),
		appRuntimeMetrics.telegramAPIFailed.Load(),
		avgMillis,
		appRuntimeMetrics.telegramSendMaxMillis.Load(),
		appRuntimeMetrics.telegramGetUpdatesOK.Load(),
		appRuntimeMetrics.telegramGetUpdatesFailed.Load(),
		appRuntimeMetrics.slowUserLockWait.Load(),
		appRuntimeMetrics.slowMessageHandle.Load(),
		appRuntimeMetrics.slowCallbackHandle.Load(),
		appRuntimeMetrics.callbackFastAck.Load(),
		formatSystemConfigErrorForMarkdown(runtimeMetricString(appRuntimeMetrics.lastTelegramError)),
		formatSystemConfigErrorForMarkdown(runtimeMetricString(appRuntimeMetrics.lastGetUpdatesError)),
		formatSystemConfigErrorForMarkdown(runtimeMetricString(appRuntimeMetrics.lastAsyncSendError)),
	)
}
