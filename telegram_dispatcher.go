package main

import (
	"errors"
	"log"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

const (
	telegramAsyncWorkers       = 4
	telegramAsyncQueueCapacity = 1024
	telegramAsyncMaxAttempts   = 2
)

type telegramAsyncPriority int

const (
	telegramAsyncPriorityLow telegramAsyncPriority = iota
	telegramAsyncPriorityNormal
	telegramAsyncPriorityHigh
)

type telegramAsyncJob struct {
	Kind        string
	DedupeKey   string
	Priority    telegramAsyncPriority
	MaxAttempts int
	Send        func() error
}

type telegramDispatcher struct {
	high   chan telegramAsyncJob
	normal chan telegramAsyncJob
	low    chan telegramAsyncJob

	mu     sync.Mutex
	dedupe map[string]struct{}
}

var telegramAsyncDispatcher *telegramDispatcher

func StartTelegramDispatcher(bot *tgbotapi.BotAPI) {
	if bot == nil || telegramAsyncDispatcher != nil {
		return
	}

	d := &telegramDispatcher{
		high:   make(chan telegramAsyncJob, telegramAsyncQueueCapacity/4),
		normal: make(chan telegramAsyncJob, telegramAsyncQueueCapacity/2),
		low:    make(chan telegramAsyncJob, telegramAsyncQueueCapacity/4),
		dedupe: make(map[string]struct{}),
	}
	telegramAsyncDispatcher = d
	recordTelegramSendQueueState(0, d.capacity())

	for i := 0; i < telegramAsyncWorkers; i++ {
		go d.worker(i + 1)
	}
	log.Printf("✅ Telegram 异步发送调度器已启动: workers=%d capacity=%d", telegramAsyncWorkers, d.capacity())
}

func enqueueTelegramAsync(job telegramAsyncJob) bool {
	if telegramAsyncDispatcher == nil || job.Send == nil {
		recordAsyncTelegramDropped()
		return false
	}
	return telegramAsyncDispatcher.enqueue(job)
}

func (d *telegramDispatcher) enqueue(job telegramAsyncJob) bool {
	if strings.TrimSpace(job.Kind) == "" {
		job.Kind = "telegram_async"
	}
	if job.MaxAttempts <= 0 {
		job.MaxAttempts = telegramAsyncMaxAttempts
	}

	if job.DedupeKey != "" {
		d.mu.Lock()
		if _, exists := d.dedupe[job.DedupeKey]; exists {
			d.mu.Unlock()
			recordAsyncTelegramDropped()
			return false
		}
		d.dedupe[job.DedupeKey] = struct{}{}
		d.mu.Unlock()
	}

	ch := d.channel(job.Priority)
	select {
	case ch <- job:
		recordAsyncTelegramEnqueued()
		recordTelegramSendQueueState(d.length(), d.capacity())
		return true
	default:
		d.releaseDedupe(job.DedupeKey)
		recordAsyncTelegramDropped()
		recordTelegramSendQueueState(d.length(), d.capacity())
		log.Printf("⚠️ Telegram 异步发送队列已满，丢弃通知: kind=%s priority=%d", formatPlainValue(job.Kind), job.Priority)
		return false
	}
}

func (d *telegramDispatcher) channel(priority telegramAsyncPriority) chan telegramAsyncJob {
	switch priority {
	case telegramAsyncPriorityHigh:
		return d.high
	case telegramAsyncPriorityLow:
		return d.low
	default:
		return d.normal
	}
}

func (d *telegramDispatcher) length() int {
	return len(d.high) + len(d.normal) + len(d.low)
}

func (d *telegramDispatcher) capacity() int {
	return cap(d.high) + cap(d.normal) + cap(d.low)
}

func (d *telegramDispatcher) releaseDedupe(key string) {
	if key == "" {
		return
	}
	d.mu.Lock()
	delete(d.dedupe, key)
	d.mu.Unlock()
}

func (d *telegramDispatcher) worker(workerID int) {
	for {
		job := d.nextJob()
		recordTelegramSendQueueState(d.length(), d.capacity())
		d.runJob(workerID, job)
		d.releaseDedupe(job.DedupeKey)
		recordTelegramSendQueueState(d.length(), d.capacity())
	}
}

func (d *telegramDispatcher) nextJob() telegramAsyncJob {
	for {
		select {
		case job := <-d.high:
			return job
		default:
		}
		select {
		case job := <-d.normal:
			return job
		default:
		}
		select {
		case job := <-d.high:
			return job
		case job := <-d.normal:
			return job
		case job := <-d.low:
			return job
		}
	}
}

func (d *telegramDispatcher) runJob(workerID int, job telegramAsyncJob) {
	attempts := job.MaxAttempts
	for attempt := 1; attempt <= attempts; attempt++ {
		err := job.Send()
		if err == nil || isTelegramMessageNotModifiedError(err) {
			recordAsyncTelegramOK()
			return
		}

		if attempt >= attempts || !isRetryableTelegramAsyncError(err) {
			recordAsyncTelegramFailed(err)
			log.Printf("⚠️ Telegram 异步发送失败: worker=%d kind=%s attempt=%d err=%s", workerID, formatPlainValue(job.Kind), attempt, formatTelegramSendError(err))
			return
		}

		recordAsyncTelegramRetried()
		sleep := telegramAsyncRetryDelay(err, attempt)
		log.Printf("⚠️ Telegram 异步发送准备重试: worker=%d kind=%s attempt=%d sleep=%s err=%s", workerID, formatPlainValue(job.Kind), attempt, sleep, formatTelegramSendError(err))
		time.Sleep(sleep)
	}
}

func isRetryableTelegramAsyncError(err error) bool {
	if err == nil {
		return false
	}
	var tgErr *tgbotapi.Error
	if errors.As(err, &tgErr) {
		if tgErr.Code == 429 || tgErr.Code >= 500 {
			return true
		}
		return false
	}
	text := strings.ToLower(err.Error())
	return strings.Contains(text, "timeout") ||
		strings.Contains(text, "deadline exceeded") ||
		strings.Contains(text, "bad gateway") ||
		strings.Contains(text, "connection reset") ||
		strings.Contains(text, "temporary") ||
		strings.Contains(text, "i/o timeout")
}

func telegramAsyncRetryDelay(err error, attempt int) time.Duration {
	var tgErr *tgbotapi.Error
	if errors.As(err, &tgErr) && tgErr.RetryAfter > 0 {
		return time.Duration(tgErr.RetryAfter) * time.Second
	}
	if attempt <= 1 {
		return 500 * time.Millisecond
	}
	return time.Duration(attempt) * time.Second
}
