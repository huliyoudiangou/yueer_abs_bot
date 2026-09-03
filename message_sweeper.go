package main

import (
	"fmt"
	"log"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// 🛡️ 核心重构：后台巡警协程，集成了消息安全清理与内存垃圾回收
func startMessageSweeper(bot *tgbotapi.BotAPI) {
	// 1. Cron 定时清理消息任务
	go func() {
		for {
			time.Sleep(30 * time.Second)

			var msgs []AutoDeleteMsg
			if err := DB.Where("delete_at <= ?", time.Now()).Find(&msgs).Error; err != nil {
				log.Printf("⚠️ 自动删消息队列读取失败: err=%s", formatPlainError(err))
				continue
			}
			for _, m := range msgs {
				_, err := bot.Request(tgbotapi.NewDeleteMessage(m.ChatID, m.MessageID))

				// 仅在成功删除，或确认该消息已被提前手动删除/不可删除时，才清除数据库记录
				if err == nil || isTerminalTelegramDeleteError(err) {
					res := DB.Delete(&m)
					if deleteErr := res.Error; deleteErr != nil {
						log.Printf("⚠️ 自动删消息记录清理失败: id=%d chat=%d message=%d err=%s", m.ID, m.ChatID, m.MessageID, formatPlainError(deleteErr))
					} else if res.RowsAffected == 0 {
						deleteErr := fmt.Errorf("AUTO_DELETE_MSG_DELETE_MISSED")
						log.Printf("⚠️ 自动删消息记录清理未命中: id=%d chat=%d message=%d err=%s", m.ID, m.ChatID, m.MessageID, formatPlainError(deleteErr))
					}
				} else {
					log.Printf("⚠️ 自动删除 Telegram 消息失败: id=%d chat=%d message=%d err=%s", m.ID, m.ChatID, m.MessageID, formatTelegramSendError(err))
				}

				time.Sleep(50 * time.Millisecond)
			}
		}
	}()

	// 2. 内存清道夫任务
	go func() {
		for {
			time.Sleep(1 * time.Hour)
			now := time.Now()

			// 清理过期的群成员鉴权缓存
			groupMemberCache.Range(func(key, value interface{}) bool {
				if entry, ok := value.(*groupMemberCacheEntry); ok {
					if now.After(entry.expireAt) {
						groupMemberCache.Delete(key)
					}
				} else {
					// 兼容旧缓存格式或异常数据
					groupMemberCache.Delete(key)
				}
				return true
			})

			// 清理超过 2 小时处于游离状态的僵尸会话
			UserSessions.Range(func(key, value interface{}) bool {
				if session, ok := value.(*SessionState); ok {
					session.mu.RLock()
					lastActive := session.updatedAt
					session.mu.RUnlock()

					if now.Sub(lastActive) > 2*time.Hour {
						UserSessions.Delete(key)
					}
				}
				return true
			})

			// 清理超过 6 小时未使用且当前无人持有的用户锁，防止 userLocks 无限增长。
			// 不能删除 inUse > 0 的锁，否则同一用户可能被分配到两把锁，破坏状态机串行保证。
			userLocks.Range(func(key, value interface{}) bool {
				entry, ok := value.(*userLockEntry)
				if !ok {
					userLocks.Delete(key)
					return true
				}

				entry.metaMu.Lock()
				idleTooLong := now.Sub(entry.lastUsed) > 6*time.Hour
				inUse := entry.inUse
				entry.metaMu.Unlock()

				if idleTooLong && inUse == 0 {
					userLocks.Delete(key)
				}

				return true
			})
		}
	}()
}

func sendGroupAutoDeleteMessage(bot *tgbotapi.BotAPI, chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if _, err := sendAutoDelete(bot, msg); err != nil {
		log.Printf("发送 Telegram 自动删除消息失败: %s", formatTelegramSendError(err))
	}
}

func sendGroupAutoDeleteMessageAsync(bot *tgbotapi.BotAPI, chatID int64, text string, kind string, dedupeKey string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "Markdown"
	if !enqueueAutoDelete(bot, msg, kind, telegramAsyncPriorityNormal, dedupeKey) {
		log.Printf("⚠️ Telegram 群通知异步入队失败: chat=%d kind=%s", chatID, formatPlainValue(kind))
	}
}

func isTelegramCommandText(text string, command string) bool {
	fields := strings.Fields(strings.TrimSpace(text))
	if len(fields) == 0 {
		return false
	}

	head := strings.ToLower(fields[0])
	command = strings.ToLower(command)
	return head == command || strings.HasPrefix(head, command+"@")
}
