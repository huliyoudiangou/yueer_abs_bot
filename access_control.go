package main

import (
	"fmt"
	"log"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// ==========================================
// 🛡️ 核心基建：强健的权限与发卡防抖组件
// ==========================================
type groupMemberCacheEntry struct {
	inGroup  bool
	expireAt time.Time
}

func isUserInGroup(bot *tgbotapi.BotAPI, userID int64, groupID int64) bool {
	if groupID == 0 {
		return true
	}

	cacheKey := fmt.Sprintf("%d:%d", groupID, userID)

	if cached, ok := groupMemberCache.Load(cacheKey); ok {
		if entry, ok := cached.(*groupMemberCacheEntry); ok && time.Now().Before(entry.expireAt) {
			return entry.inGroup
		}
	}

	member, err := bot.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: groupID,
			UserID: userID,
		},
	})
	if err != nil {
		groupMemberCache.Store(cacheKey, &groupMemberCacheEntry{
			inGroup:  false,
			expireAt: time.Now().Add(groupMemberNegativeTTL),
		})
		return false
	}

	inGroup := member.Status == "member" ||
		member.Status == "creator" ||
		member.Status == "administrator" ||
		member.Status == "restricted"

	ttl := groupMemberNegativeTTL
	if inGroup {
		ttl = groupMemberPositiveTTL
	}

	groupMemberCache.Store(cacheKey, &groupMemberCacheEntry{
		inGroup:  inGroup,
		expireAt: time.Now().Add(ttl),
	})

	return inGroup
}

func isUserInGroupFresh(bot *tgbotapi.BotAPI, userID int64, groupID int64) bool {
	if groupID == 0 {
		return true
	}

	cacheKey := fmt.Sprintf("%d:%d", groupID, userID)

	if cached, ok := groupMemberCache.Load(cacheKey); ok {
		if entry, ok := cached.(*groupMemberCacheEntry); ok && time.Now().Before(entry.expireAt) {
			return entry.inGroup
		}
	}

	member, err := bot.GetChatMember(tgbotapi.GetChatMemberConfig{
		ChatConfigWithUser: tgbotapi.ChatConfigWithUser{
			ChatID: groupID,
			UserID: userID,
		},
	})
	if err != nil {
		log.Printf("⚠️ 实时群成员校验失败: user=%d group=%d err=%s", userID, groupID, formatTelegramSendError(err))
		groupMemberCache.Store(cacheKey, &groupMemberCacheEntry{
			inGroup:  false,
			expireAt: time.Now().Add(groupMemberNegativeTTL),
		})
		return false
	}

	inGroup := member.Status == "member" ||
		member.Status == "creator" ||
		member.Status == "administrator" ||
		member.Status == "restricted"

	groupMemberCache.Store(cacheKey, &groupMemberCacheEntry{
		inGroup:  inGroup,
		expireAt: time.Now().Add(groupMemberFreshTTL),
	})

	return inGroup
}

func isMessageFromNoticeGroup(msg *tgbotapi.Message) bool {
	return AppConfig != nil &&
		AppConfig.NoticeGroupID != 0 &&
		msg != nil &&
		msg.Chat != nil &&
		msg.Chat.ID == AppConfig.NoticeGroupID
}
