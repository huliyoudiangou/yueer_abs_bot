package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"log"
	"math/big"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
)

func getUserRoleFromDBChecked(db *gorm.DB, userID int64) (string, error) {
	if AppConfig != nil && AppConfig.AdminIDs != nil && AppConfig.AdminIDs[userID] {
		return "super_admin", nil
	}
	if db == nil {
		return "user", fmt.Errorf("ROLE_DB_EMPTY")
	}
	var u User
	err := db.Where("telegram_id = ?", userID).First(&u).Error
	if err == nil {
		if strings.TrimSpace(u.Role) == "" {
			return "user", nil
		}
		return u.Role, nil
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "user", nil
	}
	return "user", err
}

func getUserRoleFromDB(db *gorm.DB, userID int64) string {
	role, err := getUserRoleFromDBChecked(db, userID)
	if err != nil {
		log.Printf("⚠️ 用户角色读取失败，按普通用户处理: user=%d err=%s", userID, formatPlainError(err))
	}
	return role
}

func getUserRole(userID int64) string {
	return getUserRoleFromDB(DB, userID)
}

func getAuditActorRoleInTx(tx *gorm.DB, actorID int64) (string, error) {
	if actorID == 0 {
		return "system", nil
	}
	return getUserRoleFromDBChecked(tx, actorID)
}

func isSuperAdmin(userID int64) bool {
	return getUserRole(userID) == "super_admin"
}

func requireSuperAdmin(bot *tgbotapi.BotAPI, chatID int64, userID int64) bool {
	if !isSuperAdmin(userID) {
		replyText(bot, chatID, "❌ 权限不足：该操作仅限超级管理员。")
		return false
	}
	return true
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func adminAdjustDailyLimitExceeded(todayTotal int, delta int) bool {
	return todayTotal+absInt(delta) > 20000
}

// ghostWalletUsernamePrefix 钱包占位用户名前缀（用户没有 Telegram 用户名时）。
// 该前缀的 username 是内部占位，不得出现在对外公告里（见 splashVictimDisplayName）。
const ghostWalletUsernamePrefix = "ghost_tg_"

func ghostWalletUsername(userID int64) string {
	return fmt.Sprintf("%s%d", ghostWalletUsernamePrefix, userID)
}

func ensureUserWalletInTx(tx *gorm.DB, tgUser *tgbotapi.User) (User, string, error) {
	if tgUser == nil {
		return User{}, "", errTelegramUserMissing
	}

	displayName := getTelegramDisplayName(tgUser)
	var u User
	err := tx.Where("telegram_id = ?", tgUser.ID).First(&u).Error
	if err == nil {
		syncUserDisplayNameTx(tx, &u, displayName)
		return u, displayName, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return User{}, displayName, err
	}

	u = User{
		TelegramID:  tgUser.ID,
		Username:    ghostWalletUsername(tgUser.ID),
		DisplayName: strings.TrimSpace(strings.TrimPrefix(displayName, "@")),
		Points:      0,
	}
	res := tx.Create(&u)
	if res.Error != nil {
		if isUniqueConstraintError(res.Error) {
			if retryErr := tx.Where("telegram_id = ?", tgUser.ID).First(&u).Error; retryErr == nil {
				syncUserDisplayNameTx(tx, &u, displayName)
				return u, displayName, nil
			}
		}
		return User{}, displayName, res.Error
	}
	if res.RowsAffected == 0 {
		return User{}, displayName, fmt.Errorf("USER_WALLET_CREATE_MISSED")
	}

	return u, displayName, nil
}

// syncUserDisplayNameTx 钱包事务内顺带刷新显示名缓存（尽力而为：失败仅记日志，不影响钱包主流程）。
// 群公告（雷劫外溢等）按 DB 选人，靠这份缓存按真人称呼，替代 ghost_tg_* 占位用户名。
// 仅在值变化时写入，避免热路径写放大。
func syncUserDisplayNameTx(tx *gorm.DB, u *User, displayName string) {
	if tx == nil || u == nil {
		return
	}
	fresh := strings.TrimSpace(strings.TrimPrefix(displayName, "@"))
	if fresh == "" || strings.TrimSpace(u.DisplayName) == fresh {
		return
	}
	if err := tx.Model(&User{}).Where("id = ?", u.ID).Update("display_name", fresh).Error; err != nil {
		log.Printf("⚠️ 用户显示名缓存刷新失败: user=%d err=%s", u.ID, formatPlainError(err))
		return
	}
	u.DisplayName = fresh
}

func ensureUserWallet(tgUser *tgbotapi.User) (User, string, error) {
	var u User
	var displayName string
	err := DB.Transaction(func(tx *gorm.DB) error {
		txUser, txDisplayName, innerErr := ensureUserWalletInTx(tx, tgUser)
		if innerErr != nil {
			return innerErr
		}
		u = txUser
		displayName = txDisplayName
		return nil
	})
	if err != nil {
		return User{}, "", err
	}
	return u, displayName, nil
}

func executeBlindBoxOpen(tgUser *tgbotapi.User) (string, string, error) {
	if tgUser == nil {
		return "", "", errTelegramUserMissing
	}

	userID := tgUser.ID
	safeName := escapeMarkdown(tgUser.FirstName)
	resultPrefix := fmt.Sprintf("📦 盲盒开启中...\n💰 已扣除 `%d` 积分。\n\n", blindBoxCost)

	var txReplyMsg, txBroadcastMsg string
	blindBoxRefID := fmt.Sprintf("blind_box:%d:%s", userID, generateRandomCode(8))
	err := DB.Transaction(func(tx *gorm.DB) error {
		if err := applyPointDeltaInTx(
			tx,
			userID,
			-blindBoxCost,
			"blind_box_cost",
			fmt.Sprintf("开启积分盲盒，消耗 %d 积分", blindBoxCost),
			"blind_box",
			blindBoxRefID,
		); err != nil {
			if errors.Is(err, errPointsNotEnough) {
				return errPointsNotEnough
			}
			return err
		}

		nBig, err := rand.Int(rand.Reader, big.NewInt(1000))
		if err != nil {
			return err
		}
		roll := int(nBig.Int64()) + 1

		switch {
		case roll <= 739:
			code := fmt.Sprintf("R%d-%s", 1, generateRandomCode(16))
			if err := createRenewCodeRecord(tx, code, 1); err != nil {
				return err
			}
			txReplyMsg = resultPrefix + fmt.Sprintf("🎉 恭喜获得保底奖励：**【1天续期卡】**！\n💳 专属卡密：`%s`\n(卡密已升级为16位安全密钥，请在此发送充值)", code)
		case roll <= 939:
			code := fmt.Sprintf("R%d-%s", 3, generateRandomCode(16))
			if err := createRenewCodeRecord(tx, code, 3); err != nil {
				return err
			}
			txReplyMsg = resultPrefix + fmt.Sprintf("🎉 恭喜获得小奖：**【3天续期卡】**！\n💳 专属卡密：`%s`\n(卡密已升级为16位安全密钥，请在此发送充值)", code)
		case roll <= 959:
			code := generateRandomCode(16)
			if err := createInviteCodeRecord(tx, code); err != nil {
				return err
			}
			txReplyMsg = resultPrefix + fmt.Sprintf("🎉 运气不错！恭喜获得：**【专属邀请码】**！\n🎫 邀请码：`%s`\n(可直接用于开户)", code)
		case roll <= 989:
			code := fmt.Sprintf("R%d-%s", 30, generateRandomCode(16))
			if err := createRenewCodeRecord(tx, code, 30); err != nil {
				return err
			}
			txReplyMsg = resultPrefix + fmt.Sprintf("🎊 运气爆棚！获得大奖：**【30天续期月卡】**！\n💳 专属卡密：`%s`", code)
			txBroadcastMsg = fmt.Sprintf("🎰 **欧皇降临！**\n\n恭喜 @%s 在积分盲盒中单抽入魂，斩获大奖 **【💳 30天续期月卡】**！", safeName)
		case roll <= 999:
			code := fmt.Sprintf("R%d-%s", 90, generateRandomCode(16))
			if err := createRenewCodeRecord(tx, code, 90); err != nil {
				return err
			}
			txReplyMsg = resultPrefix + fmt.Sprintf("🌟 鸿运当头！获得珍稀大奖：**【90天续期季卡】**！\n💳 专属卡密：`%s`", code)
			txBroadcastMsg = fmt.Sprintf("🌟 **鸿运降临！**\n\n恭喜 @%s 在积分盲盒中斩获珍稀大奖 **【💳 90天续期季卡】**！", safeName)
		case roll <= 1000:
			code := fmt.Sprintf("R%d-%s", 365, generateRandomCode(16))
			if err := createRenewCodeRecord(tx, code, 365); err != nil {
				return err
			}
			txReplyMsg = resultPrefix + fmt.Sprintf("👑 欧皇附体！！！获得终极大奖：**【365天尊享年卡】**！！！\n💳 专属卡密：`%s`", code)
			txBroadcastMsg = fmt.Sprintf("👑👑 **全服通报：终极欧皇诞生！** 👑👑\n\n天呐！@%s 爆发了惊人气运，直接抽中了终极大奖 **【👑 365天尊享年卡】**！！！", safeName)
		}
		return nil
	})

	if err != nil {
		return "", "", err
	}

	return txReplyMsg, txBroadcastMsg, nil
}
