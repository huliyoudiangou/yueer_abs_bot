package main

import (
	"errors"
	"fmt"
	"log"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/go-telegram-bot-api/telegram-bot-api/v5"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

func handleInteractiveMessage(bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	if msg == nil || msg.Chat == nil || msg.From == nil {
		return
	}

	sweeperOnce.Do(func() {
		DB.AutoMigrate(&AutoDeleteMsg{})
		startMessageSweeper(bot)
	})

	userID := msg.From.ID
	chatID := msg.Chat.ID
	text := strings.TrimSpace(msg.Text)

	if msg.Chat.IsPrivate() {
		if isTelegramCommandText(text, "/start") {
			clearSession(userID)
			if code, ok := parseReferralStartPayload(text); ok {
				if err := validateReferralCodeForStart(code, userID); err != nil {
					if errors.Is(err, errReferralSelfInvite) {
						replyText(bot, chatID, "❌ 不能使用自己的邀请链接注册新人体验。")
					} else if errors.Is(err, errReferralInvalidCode) || errors.Is(err, errReferralInviterNotEligible) {
						replyText(bot, chatID, "❌ 邀请链接无效或已停用。")
					} else {
						log.Printf("⚠️ 邀请链接预校验失败: user=%d err=%s", userID, formatPlainError(err))
						replyText(bot, chatID, "❌ 邀请链接暂时读取失败，请稍后再试。")
					}
					return
				}
				var u User
				if err := DB.Where("telegram_id = ? AND abs_user_id <> ?", userID, "").First(&u).Error; err == nil {
					replyText(bot, chatID, "⚠️ 您已经拥有听书账号，不能重复领取新人体验。")
					return
				} else if !errors.Is(err, gorm.ErrRecordNotFound) {
					log.Printf("⚠️ 邀请链接注册读取本地正式账号失败: user=%d err=%s", userID, formatPlainError(err))
					replyText(bot, chatID, "❌ 本地档案读取失败，请稍后再试。")
					return
				}
				session := getSession(userID)
				session.SetTemp("referral_code", code)
				session.SetStep("WAITING_REG_USER")
				replyText(bot, chatID, "🎧 欢迎领取新人体验。\n\n通过邀请链接注册可获得 `7` 天体验权限；体验期内听书满 `10` 小时，可领取 `7` 天体验延期。\n\n第一步：请输入您想要的用户名\n仅限 3-20 位字母、数字或下划线。")
				UserSessions.Store(userID, session)
				return
			}
			sendUserMainMenu(bot, chatID, "👋 欢迎使用【悦耳声阅】用户管理系统：")
			return
		}

		if isTelegramCommandText(text, "/admin") {
			clearSession(userID)
			role := getUserRole(userID)
			if role == "super_admin" {
				sendMenu(bot, chatID, "👑 欢迎进入超级管理员控制台：", SuperAdminMenu)
			} else if role == "admin" {
				sendMenu(bot, chatID, "🛠️ 欢迎进入普通管理员控制台：", NormalAdminMenu)
			} else {
				replyText(bot, chatID, "❌ 拒绝访问：您没有管理员权限。")
			}
			return
		}

		if text == "取消" || strings.Contains(text, "返回") {
			clearSession(userID)
			sendUserMainMenu(bot, chatID, "✅ 已为您切换至主菜单：")
			return
		}
	}

	if locked, lockMessage := getLotteryClaimLockMessage(userID); locked {
		if msg.Chat.IsPrivate() {
			sendPlainText(bot, chatID, lockMessage)
		} else {
			registerIncomingGroupCommandForAutoDelete(msg)
			sendLotteryGroupPlainText(bot, chatID, lockMessage)
		}
		return
	}

	if AppConfig.NoticeGroupID != 0 && !isMessageFromNoticeGroup(msg) && !isUserInGroup(bot, userID, AppConfig.NoticeGroupID) {
		if msg.Chat.IsPrivate() {
			replyText(bot, chatID, "⚠️ **访问受限：您尚未加入官方交流群！**\n\n为了保障社群的健康生态，本机器人系统仅对官方群成员开放。\n👉 您必须先加入我们的官方大群，才能解锁各项功能。")
		}
		return
	}

	// 万灵阁入口：放在群门槛之后、会话状态判断之前。
	// 主菜单其余按钮要求会话处于 IDLE；若会话卡在等待步骤（突破确认/求书补充等），
	// 那些按钮会静默无响应，而万灵阁作为灵侍体系总入口必须始终可用。
	if text == "🐉 万灵阁" || text == "万灵阁" {
		if msg.Chat.IsPrivate() {
			clearSession(userID)
			SendSpiritPanel(bot, userID, chatID)
		} else {
			sendPlainText(bot, chatID, "🐉 万灵阁仅在私聊开放，请私聊我使用")
		}
		return
	}

	if HandleBookAnnouncementRecoveryCommand(bot, msg, text) {
		return
	}

	if HandleCultivationAdminReadOnlyCommand(bot, msg, text) {
		return
	}

	if HandleCultivationAdminWriteCommand(bot, msg, text) {
		return
	}

	if HandleSectSecretRealmAdminCommand(bot, msg, text) {
		return
	}

	if HandleGithubBenefitCommand(bot, msg, text, nil) {
		return
	}

	if HandleWorldBossCommand(bot, msg, text) {
		return
	}

	if HandleSectSecretRealmCommand(bot, msg, text) {
		return
	}

	session := getSession(userID)

	if HandleSectLotteryCommand(bot, msg, text, session) {
		return
	}

	if HandleSectCommand(bot, msg, text) {
		return
	}

	role := getUserRole(userID)

	if HandleAdminServiceOperations(bot, msg, text, session, role) {
		return
	}

	if HandleReferralCommand(bot, msg, text) {
		return
	}

	if HandleMarketplaceCommand(bot, msg, text, session) {
		return
	}

	if handleDailyListeningStatCommand(bot, msg, text, role) {
		return
	}

	if HandleLotteryCommand(bot, msg, text, session, role) {
		return
	}

	if !msg.Chat.IsPrivate() && session.GetStep() == "WAITING_CONFIRM_BREAKTHROUGH" {
		handleBreakthroughConfirmation(bot, msg, session, text)
		return
	}

	if session.GetStep() == "IDLE" && msg.Chat.IsPrivate() && text != "" && text != "取消" {
		var needInfoReq BookRequest
		if err := DB.
			Where("user_id = ? AND status = ?", userID, bookRequestStatusNeedInfo).
			Order("last_action_at DESC").
			First(&needInfoReq).Error; err == nil {

			if !isMenuLikeBookRequestReply(text) {
				now := time.Now()
				replyNote, ok := validateBookRequestNote(text)
				if !ok {
					sendPlainText(bot, chatID, bookRequestNoteInvalidText)
					return
				}

				actorName := getTelegramDisplayName(msg.From)
				updated, err := markBookRequestUserReplied(DB, needInfoReq, actorName, replyNote, now)
				if err != nil {
					log.Printf("⚠️ 求书用户补充信息写入失败: req=%d user=%d err=%s", needInfoReq.ID, userID, formatPlainError(err))
					sendPlainText(bot, chatID, "❌ 补充信息提交失败，请稍后再试。")
					return
				}
				if !updated {
					sendPlainText(bot, chatID, "⚠️ 该求书工单状态已变化，请稍后查看最新状态。")
					return
				}

				sendPlainText(bot, chatID, fmt.Sprintf("✅ 已收到你对求书 #%d 的补充信息，已通知接单管理员。", needInfoReq.ID))

				if needInfoReq.AssigneeID != 0 {
					sendPlainText(bot, needInfoReq.AssigneeID, fmt.Sprintf(
						"📚 求书 #%d 用户已补充信息：\n\n%s\n\n工单已回到处理中。",
						needInfoReq.ID,
						replyNote,
					))
				}

				// 刷新管理端工单消息文本，避免停留在旧的“需补充信息”状态。
				var refreshedReq BookRequest
				if err := DB.Where("id = ?", needInfoReq.ID).First(&refreshedReq).Error; err == nil {
					refreshStoredBookRequestAdminMessage(bot, refreshedReq, false, 0, 0)
				}

				return
			}
		}
	}

	if text == "抢" || text == "沾仙气" {
		registerIncomingGroupCommandForAutoDelete(msg)
		handleGrabRedPacket(bot, msg)
		return
	}

	if text == "发起骰子" || isDiceBetCommand(text) {
		registerIncomingGroupCommandForAutoDelete(msg)
		handleDiceGame(bot, msg)
		return
	}
	if text == "发起牌九" || text == "牌九状态" || text == "取消牌九" || isPaiGowBetCommand(text) {
		registerIncomingGroupCommandForAutoDelete(msg)
		handlePaiGowGame(bot, msg)
		return
	}
	if text == "发起赛马" || strings.HasPrefix(text, "押 ") {
		registerIncomingGroupCommandForAutoDelete(msg)
		handleHorseRace(bot, msg)
		return
	}

	// 🌊 核心新增：主动勘测天道大水池进度
	if text == "🌊 天道奖池" || text == "天道奖池" {
		registerIncomingGroupCommandForAutoDelete(msg)

		var poolCfg SystemConfig
		currentPool := 0
		poolAvailable := true
		if err := DB.Where("key = ?", "fusion_pool_points").First(&poolCfg).Error; err == nil {
			if points, parseErr := strconv.Atoi(poolCfg.Value); parseErr == nil {
				currentPool = points
			} else {
				log.Printf("⚠️ 天道奖池配置解析失败: err=%s", formatPlainError(parseErr))
				poolAvailable = false
			}
		} else if errors.Is(err, gorm.ErrRecordNotFound) {
			currentPool = 0
		} else {
			log.Printf("⚠️ 天道奖池读取失败: err=%s", formatPlainError(err))
			poolAvailable = false
		}

		progress := float64(currentPool) / 300.0 * 10.0
		bar := ""
		for i := 0; i < 10; i++ {
			if float64(i) < progress {
				bar += "█"
			} else {
				bar += "░"
			}
		}

		progressText := fmt.Sprintf("`[%s]` **%d/300**", bar, currentPool)
		if !poolAvailable {
			progressText = "`读取失败`"
		}
		reply := fmt.Sprintf("🌊 **【天道大奖池·实时勘测】** 🌊\n\n当前天地灵气汇聚进度：\n%s\n\n💡 *当进度达到 300 时，天道将自动降下 30 份全服大红包！*\n👉 赶紧去听书精进，或者呼唤老怪出关吧！", progressText)

		if msg.Chat.IsPrivate() {
			if _, err := sendAutoDelete(bot, tgbotapi.NewMessage(chatID, reply)); err != nil {
				log.Printf("发送天道奖池私聊状态失败: chat=%d err=%s", chatID, formatTelegramSendError(err))
			}
		} else {
			sendGroupAutoDeleteMessage(bot, chatID, reply)
		}
		return
	}

	if text == "突破" {
		registerIncomingGroupCommandForAutoDelete(msg)
		// 🚨 调用全新的天道扫描预检引擎
		HandleBreakthroughRequest(bot, msg)
		return
	}
	if text == "修仙榜" || text == "仙道榜" || text == "修仙排行榜" {
		registerIncomingGroupCommandForAutoDelete(msg)

		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err == nil && u.AbsUserID != "" {
			fetchReportAndCheckUpgrade(bot, userID, u.AbsUserID)
			checkAndCompensateLegacyUser(bot, userID)
		} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			log.Printf("⚠️ 修仙榜刷新用户档案读取失败: user=%d err=%s", userID, formatPlainError(err))
		}

		handleCultivationRank(bot, msg.Chat.ID)
		return
	}

	if text == userMenuGardenText || text == "药园" {
		registerIncomingGroupCommandForAutoDelete(msg)
		handleGardenEntry(bot, msg)
		return
	}

	if handleGardenSellCommand(bot, msg, text) {
		return
	}

	if handleMenuEntry(bot, msg, text) {
		return
	}

	if handleAdminMenuEntry(bot, msg, text) {
		return
	}

	if !msg.Chat.IsPrivate() {
		if isWealthLeaderboardCommand(text) {
			registerIncomingGroupCommandForAutoDelete(msg)
			handleWealthLeaderboardCommand(bot, msg)
			return
		}
		if strings.HasPrefix(text, "/") {
			safeName := escapeMarkdown(msg.From.FirstName)
			sendGroupAutoDeleteMessage(bot, chatID, fmt.Sprintf("⚠️ @%s 为了保持群内整洁，请**私聊我**执行各项操作指令哦！", safeName))
			registerIncomingGroupCommandForAutoDelete(msg)
		}
		return
	}

	menuKeywords := []string{
		"注册", "绑定", "签到", "兑换", "卡密回收", "回收卡密", "邀请码", "续期卡",
		"线路", "报告", "我的信息", "安全", "删号", "注销",
		"修改密码", "修改用户名", "红包", "解绑", "修改本地",
		"监控", "操控", "生成", "授权", "白名单", "设置",
		"模拟过期", "备份", "备份状态", "后台状态", "审计概览", "审计日志", "查审计", "价格", "盲盒", "清理遗孀", "财富榜", "积分榜",
		"查询", "封禁", "暂停", "删除用户", "查码", "抽奖", "积分抽奖",
		"突破", "修仙榜", "仙道榜", "修仙排行榜",
		"查看修仙配置", "查看突破配置", "查看境界配置",
		"重载修仙配置", "设置突破成功率", "设置突破消耗", "设置突破冷却", "设置突破最低修为",
		"设置境界门槛", "设置小境界门槛",
		"查看秘境配置", "设置秘境档位", "设置秘境倍率", "设置秘境掉落",
		"发起赛马", "发起牌九", "牌九状态", "取消牌九", "押", "天道奖池", "乾坤袋", "药园", "回收灵草", "求书", "我的求书", "待处理求书", "我的处理工单",
		"交易行", "交易行帮助", "上架商品", "购买商品", "下架商品", "强制下架商品", "我的交易行", "我的购买", "我的订单", "交易行订单", "查交易订单", "举报订单",
		"创建宗门", "加入宗门", "退出宗门", "我的宗门", "宗门排行", "宗门成员", "捐献宗门",
		"升级宗门", "宗门改名", "确认宗门改名", "修改宗门名称", "确认修改宗门名称", "任命长老", "任命成员", "踢出宗门", "转让宗主", "宗门贡献榜", "宗门周榜",
		"宗门任务", "我的宗门任务", "领取宗门任务奖励", "结算宗门周目标", "宗门商店", "贡献换声望", "宗门七日续期", "确认宗门七日续期", "洞府", "解锁洞府", "闭关", "宗门闭关",
		"创建宗门抽奖", "宗门抽奖", "参加宗门抽奖", "查看宗门抽奖", "重发宗门抽奖", "提醒宗门抽奖", "补发宗门抽奖提醒", "取消宗门抽奖",
		"宗门秘境", "开启宗门秘境", "确认开启宗门秘境", "开启普通宗门秘境", "确认开启普通宗门秘境", "开启高阶宗门秘境", "确认开启高阶宗门秘境", "开启限时宗门秘境", "确认开启限时宗门秘境", "进入宗门秘境", "结算宗门秘境", "宗门秘境排行", "宗门秘境明细",
		"宗门喇叭", "世界喇叭", "确认宗门喇叭", "确认世界喇叭",
		"世界Boss", "Boss状态", "参加Boss", "Boss排行", "宗门科技", "护宗神兽", "升级科技", "确认升级科技", "藏经阁", "升级藏经阁", "解锁功法", "观法", "确认观法兑换", "修习", "宗门运势", "开运",
		"流水", "我的流水", "查流水",
		"刷新我的今日净修为", "刷新宗门今日净修为", "刷新全服今日净修为", "查看每日净修为", "万灵阁",
	}

	isMenuCommand := false
	for _, kw := range menuKeywords {
		if strings.Contains(text, kw) {
			isMenuCommand = true
			break
		}
	}

	if text == "确认注销" {
		isMenuCommand = false
	}

	if isMenuCommand && session.GetStep() == "IDLE" {
		clearSession(userID)
		session = getSession(userID)

		if strings.Contains(text, "药园") {
			handleGardenEntry(bot, msg)
			return
		}

		if text == "📚 待处理求书" {
			if !isBookRequestAdmin(userID) {
				sendPlainText(bot, chatID, "❌ 权限不足。")
				return
			}

			showPendingBookRequests(bot, chatID)
			return
		}

		if text == "📚 我的处理工单" || text == "我的处理工单" {
			if !isBookRequestAdmin(userID) {
				sendPlainText(bot, chatID, "❌ 权限不足。")
				return
			}

			showMyClaimedBookRequests(bot, chatID, userID)
			return
		}

		if text == "📋 我的求书" {
			showMyBookRequests(bot, chatID, userID)
			return
		}

		if text == "📚 求书" {
			handleBookRequestStart(bot, msg, session)
			return
		}

		if text == "我的流水" || text == "积分流水" || text == "📒 我的流水" || strings.HasPrefix(text, "查流水") {
			handlePointTransactionQuery(bot, chatID, userID, text)
			return
		}

		if text == "审计概览" || strings.HasPrefix(text, "查审计概览") {
			handleAuditSummaryQuery(bot, chatID, userID, text)
			return
		}

		if text == "审计日志" || strings.HasPrefix(text, "查审计") {
			handleAuditLogQuery(bot, chatID, userID, text)
			return
		}

		if strings.Contains(text, "获取线路") || (strings.Contains(text, "获取") && strings.Contains(text, "线路")) {
			var cfg SystemConfig
			lines := "⚠️ 管理员暂未配置任何线路，请稍后再试。"
			if err := DB.Where("key = ?", "server_lines").First(&cfg).Error; err == nil && cfg.Value != "" {
				lines = serverLinesMarkdownBody(cfg.Value)
			} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 用户获取线路读取配置失败: user=%d err=%s", userID, formatPlainError(err))
				lines = "⚠️ 线路配置暂时读取失败，请稍后再试。"
			}
			replyText(bot, chatID, "🗺️ **服务器实时线路**\n\n"+lines)
			return
		}

		if strings.Contains(text, "我的信息") {
			var u User
			userErr := DB.Where("telegram_id = ?", userID).First(&u).Error
			if userErr == nil {
				statusText := resolveUserAccountStatusDisplay(u, time.Now(), accountStatusDisplaySelf, true).Text

				if u.AbsUserID != "" {
					fetchReportAndCheckUpgrade(bot, userID, u.AbsUserID)
					checkAndCompensateLegacyUser(bot, userID)
				}
				cul := GetOrCreateCultivation(userID)
				if cul == nil {
					replyText(bot, chatID, "⚠️ 修仙档案暂时读取失败，请稍后重试。")
					return
				}
				realmStr := GetRealmName(cul)
				todayEffectiveHours := 0.0
				todayEffectiveHoursText := "`0.00`"
				if todayStat, ok, err := getTodayDailyListeningStatChecked(userID, time.Now()); err != nil {
					log.Printf("⚠️ 我的信息今日净修为读取失败: user=%d err=%s", userID, formatPlainError(err))
					todayEffectiveHoursText = "`读取失败`"
				} else if ok {
					todayEffectiveHours = applySectFortuneNetCultivationBuff(userID, todayStat.EffectiveHours+activeSectCaveRetreatBonusHours(userID, time.Now()))
					todayEffectiveHoursText = fmt.Sprintf("`%.2f`", todayEffectiveHours)
				}

				// 🩸 核心新增：天道时间时区计算与周/月度丹毒沉淀勘测
				loc := time.FixedZone("CST", 8*3600)
				now := time.Now().In(loc)

				// 精确计算本周一 00:00:00 的时间节点
				offset := int(now.Weekday()) - 1
				if offset == -1 {
					offset = 6 // 适配周日逻辑
				}
				thisMonday := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc).AddDate(0, 0, -offset)

				// 精确计算本月 1 号 00:00:00 的时间节点
				thisMonthFirst := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, loc)

				// 高并发查表：读取该道友周期内的嗑药计数
				countJuLing, errJuLing := countManualPillUsage(userID, "聚灵丹", thisMonday)
				countJiuZhuan, errJiuZhuan := countManualPillUsage(userID, "九转造化丹", thisMonday)
				countXianYu, errXianYu := countManualPillUsage(userID, "万年仙玉髓", thisMonthFirst)
				if errJuLing != nil {
					log.Printf("⚠️ 查询聚灵丹丹毒计数失败: user=%d err=%s", userID, formatPlainError(errJuLing))
				}
				if errJiuZhuan != nil {
					log.Printf("⚠️ 查询九转造化丹丹毒计数失败: user=%d err=%s", userID, formatPlainError(errJiuZhuan))
				}
				if errXianYu != nil {
					log.Printf("⚠️ 查询万年仙玉髓丹毒计数失败: user=%d err=%s", userID, formatPlainError(errXianYu))
				}

				// 渲染全新的多维档案面板
				info := fmt.Sprintf("👤 **我的账户档案**\n\n"+
					"🏷️ **当前名称**: `%s`\n"+
					"🆔 **TG 绑定ID**: `%d`\n"+
					"🌍 **当前积分**: `%d`\n"+
					"⏳ **账号状态**: %s\n"+
					"──────────────\n"+
					"📿 **修仙境界**: %s\n"+
					"⏱ **总计修为**: `%.1f` 小时\n"+
					" ├ 🎧 **闭关苦修**: `%.1f` 小时\n"+
					" └ 💊 **丹药药力**: `%.1f` 小时\n"+
					"🌅 **今日净修为**: %s 小时\n"+
					"🪵 **渡劫气运**: `%d` 次失败累积\n"+
					"──────────────\n"+
					"🩸 **【体内丹毒沉淀】** *(每周一零点重置)*\n"+
					"🍵 聚灵丹: `%s` 次\n"+
					"💊 九转造化丹: `%s` 次\n"+
					"🍎 万年仙玉髓: `%s` 次 *(每月限额)*",
					escapeMarkdown(u.Username), userID, u.Points, statusText,
					realmStr, cul.TotalAudioTime+cul.PillAudioTime, cul.TotalAudioTime, cul.PillAudioTime, todayEffectiveHoursText, cul.TribulationFails,
					manualPillUsageCountText(countJuLing, 3, errJuLing == nil),
					manualPillUsageCountText(countJiuZhuan, 2, errJiuZhuan == nil),
					manualPillUsageCountText(countXianYu, 1, errXianYu == nil))

				replyText(bot, chatID, info)
			} else if errors.Is(userErr, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "⚠️ 您还没有任何资产记录。")
			} else {
				log.Printf("⚠️ 我的信息读取本地档案失败: user=%d err=%s", userID, formatPlainError(userErr))
				replyText(bot, chatID, "⚠️ 账户档案暂时读取失败，请稍后重试。")
			}
			return
		}

		if strings.Contains(text, "签到") {
			handleUserSignIn(bot, msg)
			return
		}

		if isWealthLeaderboardCommand(text) {
			handleWealthLeaderboardCommand(bot, msg)
			return
		}

		if text == "卡密回收" || text == "回收卡密" {
			if _, _, err := ensureUserWallet(msg.From); err != nil {
				log.Printf("❌ 卡密回收钱包初始化失败: user=%d err=%s", userID, formatPlainError(err))
				sendPlainText(bot, chatID, "❌ 钱包初始化失败，请稍后重试。")
				return
			}
			session.SetStep("WAITING_CODE_CASHOUT_INPUT")
			UserSessions.Store(userID, session)
			sendPlainText(bot, chatID, "♻️ 本服卡密回收\n\n仅支持本 Bot 生成且当前真实可用的邀请码或续期卡，按当前系统定价的 60% 回收。回收后卡密永久失效且不可恢复。\n\n请发送需要回收的完整卡密，或发送“取消”退出。")
			return
		}

		if strings.Contains(text, "兑换") {
			u, _, walletErr := ensureUserWallet(msg.From)
			if walletErr != nil {
				log.Printf("❌ 创建幽灵钱包失败: user=%d err=%s", userID, formatPlainError(walletErr))
				replyText(bot, chatID, "❌ 钱包初始化失败，请稍后重试。")
				return
			}
			invPrice, invPriceErr := getConfigIntChecked("invite_price", 300)
			renPrice, renPriceErr := getConfigIntChecked("renew_price", 150)
			if invPriceErr != nil || renPriceErr != nil {
				log.Printf("⚠️ 兑换商城价格配置读取失败: user=%d invite_err=%s renew_err=%s", userID, formatPlainError(invPriceErr), formatPlainError(renPriceErr))
				replyText(bot, chatID, "❌ 价格配置暂时读取失败，请稍后重试。")
				return
			}
			session.SetStep("WAITING_EXCHANGE_CHOICE")
			replyText(bot, chatID, fmt.Sprintf("🪙 **欢迎光临积分福利商城**\n您的可用资产: `%d` 积分\n\n请回复对应的数字进行操作：\n[1] 消耗 **%d** 积分 -> 兑换【邀请码】\n[2] 消耗 **%d** 积分 -> 兑换【30天续期卡】\n\n丹药与奇珍请从一级菜单进入【🏪 聚宝斋】。\n输入 `取消` 退出。", u.Points, invPrice, renPrice))
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "乾坤袋") {
			var items []Inventory
			if err := DB.Where("user_id = ? AND quantity > 0", userID).Find(&items).Error; err != nil {
				log.Printf("⚠️ 乾坤袋读取失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "🎒 乾坤袋暂时读取失败，请稍后重试。")
				return
			}

			if len(items) == 0 {
				replyText(bot, chatID, "🎒 **【我的乾坤袋】**\n\n🫙 里面空空如也...\n👉 请从一级菜单进入【🏪 聚宝斋】选购天地奇珍。")
				return
			}

			msgText := "🎒 **【我的乾坤袋】**\n\n"
			for i, item := range items {
				msgText += fmt.Sprintf("**[%d]** %s - 拥有数量: `%d`\n", i+1, inventoryItemMarkdownName(item.ItemName), item.Quantity)
				if effectLine := pillEffectMarkdownLine(item.ItemName); effectLine != "" {
					msgText += "  " + effectLine + "\n"
				}
				// 将序号与物品名称绑定，存入缓存供下一步读取
				session.SetTemp(fmt.Sprintf("inv_item_%d", i+1), item.ItemName)
			}
			msgText += "\n👉 **请输入你要使用的物品序号 (如 `1`)，或输入 `取消` 退出。**"

			// 🚨🚨🚨 核心修复：加上这一行，把拼接好的背包界面发出来！
			replyText(bot, chatID, msgText)

			session.SetStep("WAITING_INVENTORY_ACTION")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "盲盒") {
			u, _, walletErr := ensureUserWallet(msg.From)
			if walletErr != nil {
				log.Printf("❌ 创建幽灵钱包失败: user=%d err=%s", userID, formatPlainError(walletErr))
				replyText(bot, chatID, "❌ 钱包初始化失败，请稍后重试。")
				return
			}
			if u.Points < blindBoxCost {
				replyText(bot, chatID, fmt.Sprintf("🎁 开启盲盒需要 %d 积分，您当前余额为 %d 积分。\n\n💡 **新人指引**：赶紧去点击面板上的【📆 每日签到】白嫖第一桶金吧！", blindBoxCost, u.Points))
				return
			}

			session.SetStep("WAITING_CONFIRM_BLIND_BOX")
			replyText(bot, chatID, fmt.Sprintf("🎁 **积分盲盒确认**\n\n开启一次将扣除 `%d` 积分。\n当前余额：`%d` 积分\n扣除后余额：`%d` 积分\n\n确认开启请回复：`确认开启盲盒`\n取消请回复：`取消`。", blindBoxCost, u.Points, u.Points-blindBoxCost))
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "报告") {
			var u User
			userErr := DB.Where("telegram_id = ?", userID).First(&u).Error
			if userErr == nil && u.AbsUserID != "" {
				replyText(bot, chatID, "🔍 正在提取您的战绩...")
				reportStr := fetchReportAndCheckUpgrade(bot, userID, u.AbsUserID)
				checkAndCompensateLegacyUser(bot, userID)
				replyText(bot, chatID, reportStr)
			} else if userErr == nil || errors.Is(userErr, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "⚠️ 您只有幽灵钱包，请先注册/绑定真实听书账号。")
			} else {
				log.Printf("⚠️ 听书报告入口读取本地档案失败: user=%d err=%s", userID, formatPlainError(userErr))
				replyText(bot, chatID, "❌ 本地档案读取失败，请稍后再试。")
			}
			return
		}

		if strings.Contains(text, "注册") || (strings.Contains(text, "邀请码") && !strings.Contains(text, "生成") && !strings.Contains(text, "价格")) {
			var u User
			err := DB.Where("telegram_id = ? AND abs_user_id != ?", userID, "").First(&u).Error
			if err == nil {
				if isTrialAccount(u) {
					session.SetStep("WAITING_TRIAL_FORMAL_INVITE")
					replyText(bot, chatID, "🎫 当前为新人体验账号。\n\n请发送正式邀请码完成转正；转正后即可使用普通续期卡。")
					UserSessions.Store(userID, session)
					return
				}
				replyText(bot, chatID, "⚠️ 您已经拥有正式账号了，无需重复注册。")
				return
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 注册入口读取本地正式账号失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 本地档案读取失败，请稍后再试。")
				return
			}
			session.SetStep("WAITING_REG_USER")
			replyText(bot, chatID, "📝 **第一步：请输入您想要的用户名**\n(⚠️ 仅限 3-20 位字母、数字、下划线)")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "绑定") {
			var u User
			err := DB.Where("telegram_id = ? AND abs_user_id != ?", userID, "").First(&u).Error
			if err == nil {
				replyText(bot, chatID, "⚠️ 您当前已经绑定过正式账号了。")
				return
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 绑定入口读取本地正式账号失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 本地档案读取失败，请稍后再试。")
				return
			}
			session.SetStep("WAITING_BIND_USER")
			replyText(bot, chatID, "🔗 **请输入您在有声书服的已有用户名：**")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "安全") {
			var u User
			if err := DB.Where("telegram_id = ?", userID).First(&u).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "⚠️ 您还未绑定账户。")
				return
			} else if err != nil {
				log.Printf("⚠️ 账户安全入口读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
				return
			}
			textOut, markup := renderFeatureMenu("security", userID)
			sendMenuPanel(bot, chatID, textOut, markup)
			return
		}

		if strings.Contains(text, "修改用户名") {
			var u User
			if err := DB.Where("telegram_id = ?", userID).First(&u).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "⚠️ 您还未绑定账户。")
				return
			} else if err != nil {
				log.Printf("⚠️ 修改用户名入口读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
				return
			}
			if strings.TrimSpace(u.AbsUserID) == "" {
				replyText(bot, chatID, "⚠️ 您只有幽灵钱包，请先注册或绑定真实听书账号后再修改用户名。")
				return
			}
			session.SetStep("WAITING_USERNAME_AUTH")
			replyText(bot, chatID, "🔒 **请输入您的安全码(PIN)以验证所有权：**")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "密码") {
			session.SetStep("WAITING_SAFETY_AUTH")
			replyText(bot, chatID, "🔒 **请输入您的安全码(PIN)以验证所有权：**")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "解绑") {
			session.SetStep("WAITING_UNBIND_AUTH")
			replyText(bot, chatID, "🔒 **请输入您的安全码(PIN)确认解绑：**")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "红包") {
			session.SetStep("WAITING_RED_POINTS")
			replyText(bot, chatID, "🧧 **欢迎发起社群积分红包**\n请输入红包的 **总积分金额** (最少10)：")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "续期卡") && !strings.Contains(text, "生成") && !strings.Contains(text, "价格") {
			session.SetStep("WAITING_RENEW_CODE")
			replyText(bot, chatID, "💳 请发送您的**续期卡密**：")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "注销") || strings.Contains(text, "删号") {
			session.SetStep("WAITING_DELETE_AUTH")
			replyText(bot, chatID, "⚠️ **高危操作警告**：此操作将硬删除本地记录及有声书服务端资产！\n\n请先输入您的安全码(PIN)验证身份。")
			UserSessions.Store(userID, session)
			return
		}

		if strings.Contains(text, "监控") || strings.Contains(text, "操控") || strings.Contains(text, "生成") || strings.Contains(text, "模拟过期") || strings.Contains(text, "价格") || strings.Contains(text, "查询") || strings.Contains(text, "封禁") || strings.Contains(text, "暂停") || strings.Contains(text, "删除用户") || strings.Contains(text, "查码") {

			if role != "super_admin" && role != "admin" {
				replyText(bot, chatID, "❌ 权限不足，拒绝越权访问。")
				return
			}

			// 普通管理员只允许监控和查询。
			// 其余所有写操作、高危操作、卡密查看操作均限制为超级管理员。
			if strings.Contains(text, "查询") {
				session.SetStep("WAITING_QUERY_USER")
				replyText(bot, chatID, "🔍 请输入要查询的 **Telegram ID**、**带 @ 的 TG 用户名** 或 **ABS 用户名**：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "监控") {
				replyText(bot, chatID, absClient.GetServerStats())
				writeAuditLog(userID, "VIEW_SERVER_STATS", "ABS", "管理员查看系统监控")
				return
			}

			// 以下全部是超级管理员专属。
			if role != "super_admin" {
				replyText(bot, chatID, "❌ 权限不足：普通管理员只能使用【系统监控】和【查询用户】。")
				return
			}

			if strings.Contains(text, "暂停") || strings.Contains(text, "封禁") {
				session.SetStep("WAITING_SUSPEND_USER")
				replyText(bot, chatID, "🛑 请输入要封禁/解封的用户 **Telegram ID**：\n系统会自动反转当前封禁状态。")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "删除用户") {
				session.SetStep("WAITING_FORCE_DELETE_USER")
				replyText(bot, chatID, "⚠️ **高危操作：物理删号**\n请输入要彻底抹除的用户 **Telegram ID**：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "查码") {
				session.SetStep("WAITING_QUERY_CODE")
				replyText(bot, chatID, "🔍 请发送需要追溯的 **邀请码** 或 **续期卡密**：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "模拟过期") {
				session.SetStep("WAITING_SIMULATE_EXPIRE")
				replyText(bot, chatID, "⏱️ 请输入要强制过期的用户 TG ID：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "操控") {
				session.SetStep("WAITING_MANAGE_POINTS_ID")
				replyText(bot, chatID, "🎛️ 请输入需要人工调账的用户 **Telegram ID**：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "生成") && strings.Contains(text, "邀请") {
				session.SetStep("WAITING_GEN_INVITE_COUNT")
				replyText(bot, chatID, "🔢 请输入生成【邀请码】的数量，建议一次不超过 100 张：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "生成") && strings.Contains(text, "续期") {
				session.SetStep("WAITING_GEN_RENEW_DAYS")
				replyText(bot, chatID, "📅 请输入需要生成的续期卡天数，允许范围 1-365：")
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "邀请码价格") {
				price, err := getConfigIntChecked("invite_price", 300)
				if err != nil {
					log.Printf("⚠️ 邀请码价格配置读取失败: actor=%d err=%s", userID, formatPlainError(err))
					replyText(bot, chatID, "❌ 邀请码价格配置暂时读取失败，请稍后重试。")
					return
				}
				session.SetStep("WAITING_SET_INVITE_PRICE")
				replyText(bot, chatID, fmt.Sprintf("🔢 当前售价为 `%d` 积分。请输入新的售卖积分：", price))
				UserSessions.Store(userID, session)
				return
			}

			if strings.Contains(text, "续期卡价格") {
				price, err := getConfigIntChecked("renew_price", 150)
				if err != nil {
					log.Printf("⚠️ 续期卡价格配置读取失败: actor=%d err=%s", userID, formatPlainError(err))
					replyText(bot, chatID, "❌ 续期卡价格配置暂时读取失败，请稍后重试。")
					return
				}
				session.SetStep("WAITING_SET_RENEW_PRICE")
				replyText(bot, chatID, fmt.Sprintf("🔢 当前售价为 `%d` 积分。请输入新的售卖积分：", price))
				UserSessions.Store(userID, session)
				return
			}
		}

		if strings.Contains(text, "授权") || strings.Contains(text, "白名单") || (strings.Contains(text, "设置") && strings.Contains(text, "线路")) || strings.Contains(text, "备份") || strings.Contains(text, "后台状态") {
			if role != "super_admin" {
				replyText(bot, chatID, "❌ 此为超级管理员专属功能。")
				return
			}
			if strings.Contains(text, "后台状态") {
				replyText(bot, chatID, formatBackgroundStatusReport())
				writeAuditLog(userID, "VIEW_BACKGROUND_STATUS", "background_jobs", "超级管理员查看后台任务状态")
				return
			}
			if strings.Contains(text, "备份状态") {
				replyText(bot, chatID, formatBackupStatusReport())
				writeAuditLog(userID, "VIEW_BACKUP_STATUS", "database_backup", "超级管理员查看数据库备份状态")
				return
			}
			if strings.Contains(text, "备份") {
				if AppConfig == nil || AppConfig.BackupGroupID == 0 {
					replyText(bot, chatID, "⚠️ 系统环境变量中尚未配置 `BACKUP_GROUP_ID`，无法发送。")
					return
				}
				session.SetStep("WAITING_BACKUP_REASON")
				replyText(bot, chatID, "📝 手动数据库备份会生成加密备份并发送到备份群组。\n请输入本次备份原因，"+adminReasonRequirementText+"：")
				UserSessions.Store(userID, session)
				return
			}
			if strings.Contains(text, "授权") {
				session.SetStep("WAITING_PROMOTE_ID")
				replyText(bot, chatID, "👤 请输入准备提拔的用户的 **Telegram ID**：")
				UserSessions.Store(userID, session)
				return
			}
			if strings.Contains(text, "白名单") {
				session.SetStep("WAITING_WHITELIST_ID")
				replyText(bot, chatID, "🏳️ 请输入要免除保号惩罚的用户 **Telegram ID**：")
				UserSessions.Store(userID, session)
				return
			}
			if strings.Contains(text, "设置") && strings.Contains(text, "线路") {
				session.SetStep("WAITING_SET_SERVER_LINES")
				replyText(bot, chatID, "🗺️ **请发送全新的服务器线路配置内容**：")
				UserSessions.Store(userID, session)
				return
			}
		}

		if text == "📚 待处理求书" {

			if !isBookRequestAdmin(userID) {
				sendPlainText(bot, chatID, "❌ 权限不足。")
				return
			}

			showPendingBookRequests(bot, chatID)
			return
		}

		if strings.Contains(text, "清理遗孀") {
			if role != "super_admin" {
				replyText(bot, chatID, "❌ 此为超级管理员专属高危功能。")
				return
			}

			replyText(bot, chatID, "⏳ 正在拉取服务端数据进行全局对账，请稍候...")

			absUsers, err := absClient.GetAllUsers()
			if err != nil {
				replyText(bot, chatID, "❌ 无法连接到 ABS 服务端获取用户列表。")
				return
			}

			var localUsers []User
			if err := DB.Where("abs_user_id != ''").Find(&localUsers).Error; err != nil {
				replyText(bot, chatID, "❌ 本地数据库读取异常，为防止误删全服，对账协议已强行中止！")
				return
			}

			localMap := make(map[string]bool)
			for _, lu := range localUsers {
				localMap[lu.AbsUserID] = true
			}

			var widowIDs []string
			var widowNames []string
			for _, au := range absUsers {
				if au.Type == "root" || au.Type == "admin" {
					continue
				}
				if !localMap[au.ID] {
					widowIDs = append(widowIDs, au.ID)
					widowNames = append(widowNames, au.Username)
				}
			}

			if len(widowIDs) == 0 {
				replyText(bot, chatID, "🎉 **全局对账完毕！**\n\nABS 服务端的所有常规账号均已完美绑定，不存在任何遗孀账号。")
				clearSession(userID)
				return
			}

			session.SetTemp("widow_ids", strings.Join(widowIDs, ","))
			session.SetStep("WAITING_CLEAN_WIDOWS_REASON")

			replyText(bot, chatID, fmt.Sprintf("⚠️ **警告：发现 %d 个未绑定 Bot 的遗孀账号**\n\n正在为您全量导出名单，请仔细核对：", len(widowIDs)))

			batchSize := 100
			for i := 0; i < len(widowNames); i += batchSize {
				end := i + batchSize
				if end > len(widowNames) {
					end = len(widowNames)
				}
				batch := widowNames[i:end]

				batchMsg := fmt.Sprintf("📦 **遗孀名单分组 (%d-%d)：**\n`%s`", i+1, end, strings.Join(batch, ", "))
				replyText(bot, chatID, batchMsg)

				time.Sleep(150 * time.Millisecond)
			}

			confirmMsg := "🚨 **以上为当前服务端检测出的全量遗孀名单**\n\n⚠️ *此操作将硬删除 ABS 服务端数据，不可逆！*\n\n请先输入本次清理原因，" + adminReasonRequirementText + "。"
			replyText(bot, chatID, confirmMsg)

			UserSessions.Store(userID, session)
			return
		}
	}

	switch session.GetStep() {
	case "WAITING_GARDEN_SELL_QTY":
		seedKey := strings.TrimSpace(session.GetTemp("garden_sell_seed_key"))
		herbName := strings.TrimSpace(session.GetTemp("garden_sell_herb_name"))
		if seedKey == "" {
			sendPlainText(bot, chatID, "药铺回收状态已失效，请重新进入药铺。")
			clearSession(userID)
			return
		}
		qty, err := strconv.Atoi(strings.TrimSpace(text))
		if err != nil || qty <= 0 {
			sendPlainText(bot, chatID, "回收数量必须是正整数，请重新发送数量，或发送“取消”退出。")
			return
		}
		points, soldQty, err := gardenSellHerbQuantity(userID, seedKey, qty)
		if err != nil {
			replyText(bot, chatID, gardenActionErrorText(err))
			return
		}
		if herbName == "" {
			if cfg, ok := gardenSeedByKey(seedKey); ok {
				herbName = cfg.HerbName
			}
		}
		replyText(bot, chatID, fmt.Sprintf("✅ 药铺回收【%s】x%d，获得 %d 积分。", inventoryItemMarkdownName(herbName), soldQty, points))
		clearSession(userID)
		return

	case "WAITING_CONFIRM_SECT_HORN":
		handleSectHornSession(bot, msg, session, text)

	case "WAITING_BOOK_LINK":
		xmlyLink, ok := validateXmlyLink(text)
		if !ok {
			sendPlainText(bot, chatID,
				"❌ 链接格式不正确。\n\n"+
					"请发送以 https:// 开头的喜马拉雅链接，仅支持：\n"+
					"ximalaya.com\n"+
					"www.ximalaya.com\n"+
					"m.ximalaya.com\n"+
					"xima.tv",
			)
			return
		}

		session.SetTemp("book_xmly_link", xmlyLink)
		session.SetStep("WAITING_BOOK_USER_NOTE")
		UserSessions.Store(userID, session)

		sendPlainText(bot, chatID,
			fmt.Sprintf(
				"✅ 链接已记录。\n\n喜马拉雅链接：\n%s\n\n是否需要填写备注？\n例如：想要全集、缺少某几集、指定版本等。\n\n没有备注请发送：跳过",
				xmlyLink,
			),
		)

	case "WAITING_BOOK_USER_NOTE":
		userNote := strings.TrimSpace(text)
		if userNote == "跳过" || userNote == "无" || userNote == "没有" {
			userNote = ""
		}

		if normalizedNote, ok := validateBookRequestNote(userNote); !ok {
			sendPlainText(bot, chatID, bookRequestNoteInvalidText)
			return
		} else {
			userNote = normalizedNote
		}

		session.SetTemp("book_user_note", userNote)
		session.SetStep("WAITING_BOOK_CONFIRM")
		UserSessions.Store(userID, session)

		xmlyLink := session.GetTemp("book_xmly_link")

		costText := ""
		if _, plan, err := loadBookRequestUserPlanWithDB(DB, userID, time.Now()); err == nil {
			costText = fmt.Sprintf("\n本次提交将消耗 %d 积分。", plan.Cost)
		}

		sendPlainText(bot, chatID,
			fmt.Sprintf(
				"📚 请确认求书信息：\n\n"+
					"喜马拉雅链接：\n%s\n\n"+
					"用户备注：\n%s%s\n\n"+
					"确认提交请回复：确认提交\n"+
					"重新填写请回复：重新填写\n"+
					"取消请回复：取消",
				xmlyLink,
				displayBookRequestText(userNote, "无"),
				costText,
			),
		)

	case "WAITING_BOOK_CONFIRM":
		if text == "确认提交" {
			submitBookRequest(bot, msg, session)
			return
		}

		if text == "重新填写" {
			session.SetStep("WAITING_BOOK_LINK")
			session.SetTemp("book_xmly_link", "")
			session.SetTemp("book_user_note", "")
			UserSessions.Store(userID, session)

			sendPlainText(bot, chatID,
				"📚 请重新发送喜马拉雅链接。\n\n"+
					"要求："+bookRequestLinkRequirementText+"。",
			)
			return
		}

		sendPlainText(bot, chatID, "请回复：确认提交、重新填写，或取消。")

	case "WAITING_BOOK_CANCEL_REASON":
		if !isBookRequestAdmin(userID) {
			sendPlainText(bot, chatID, "❌ 权限不足。")
			clearSession(userID)
			return
		}

		cancelReason, ok := validateAdminReason(text)
		if !ok {
			sendPlainText(bot, chatID, "❌ "+adminReasonInvalidText)
			return
		}

		reqIDRaw := session.GetTemp("book_cancel_req_id")
		reqID64, err := strconv.ParseUint(reqIDRaw, 10, 64)
		if err != nil || reqID64 == 0 {
			sendPlainText(bot, chatID, "❌ 工单编号异常，请重新操作。")
			clearSession(userID)
			return
		}

		reqID := uint(reqID64)
		adminName := getTelegramDisplayName(msg.From)

		currentReq, found, err := loadBookRequestByID(DB, reqID, "cancel reason input")
		if err != nil {
			sendPlainText(bot, chatID, "❌ 查询工单失败，请稍后再试。")
			return
		}
		if !found {
			sendPlainText(bot, chatID, "❌ 工单不存在，请重新操作。")
			clearSession(userID)
			return
		}

		if !isBookRequestOperableStatus(currentReq.Status) {
			sendPlainText(bot, chatID, "⚠️ 该工单当前不能取消。")
			clearSession(userID)
			return
		}

		if !canOperateBookRequest(currentReq, userID) {
			sendPlainText(bot, chatID, "❌ 只有接单人或超级管理员可以取消该工单。")
			clearSession(userID)
			return
		}

		now := time.Now()
		oldStatus := currentReq.Status

		cancelled := false
		refunded := 0
		err = DB.Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&BookRequest{}).
				Where("id = ? AND status IN ?", reqID, bookRequestOperableStatuses()).
				Updates(map[string]interface{}{
					"status":         bookRequestStatusCancelled,
					"admin_id":       userID,
					"admin_name":     adminName,
					"last_action_at": &now,
					"completed_at":   &now,
				})
			if res.Error != nil {
				return fmt.Errorf("book request cancel update failed: %s", formatPlainError(res.Error))
			}
			if res.RowsAffected == 0 {
				return nil
			}
			cancelled = true

			amount, err := refundBookRequestInTx(tx, &currentReq, userID, adminName, "cancelled by admin: "+cancelReason, now)
			if err != nil {
				return err
			}
			refunded = amount

			if err := createBookRequestLogInTx(tx, reqID, userID, adminName, "cancel", oldStatus, bookRequestStatusCancelled, cancelReason); err != nil {
				return err
			}
			return writeAuditLogInTx(tx, userID, "CANCEL_BOOK_REQUEST", fmt.Sprintf("%d", reqID), 0, "admin cancelled book request; reason="+cancelReason)
		})
		if err != nil {
			log.Printf("book request cancel failed: req=%d admin=%d err=%s", reqID, userID, formatPlainError(err))
			sendPlainText(bot, chatID, "❌ 取消工单失败，请稍后再试。")
			return
		}

		if !cancelled {
			sendPlainText(bot, chatID, "⚠️ 该工单状态已变化，无法取消，请查看最新状态。")
			clearSession(userID)
			return
		}

		var updatedReq BookRequest
		adminMsgChatID := int64(0)
		adminMsgID := 0
		if err := reloadBookRequestAfterFinish(DB, &updatedReq, reqID, bookRequestStatusCancelled, userID, adminName, now); err == nil {
			chatIDRaw := session.GetTemp("book_cancel_chat_id")
			msgIDRaw := session.GetTemp("book_cancel_message_id")

			chatID64, chatParseErr := strconv.ParseInt(chatIDRaw, 10, 64)
			msgID64, msgParseErr := strconv.ParseInt(msgIDRaw, 10, 64)
			if chatParseErr == nil && msgParseErr == nil {
				adminMsgChatID = chatID64
				adminMsgID = int(msgID64)
			}

			if adminMsgChatID != 0 && adminMsgID != 0 {
				editBookRequestAdminMessage(bot, adminMsgChatID, adminMsgID, updatedReq, true)
			}
			refreshStoredBookRequestAdminMessage(bot, updatedReq, true, adminMsgChatID, adminMsgID)
		}

		refundText := "本次取消未产生积分退还。"
		if refunded > 0 {
			refundText = fmt.Sprintf("已退还 %d 积分。", refunded)
		}
		sendPlainText(bot, chatID, fmt.Sprintf("✅ 已取消求书工单 #%d，%s", reqID, refundText))
		sendPlainText(bot, updatedReq.UserID, fmt.Sprintf(
			"📚 你的求书 #%d 已被管理员取消。\n\n原因：%s\n\n%s",
			reqID,
			cancelReason,
			refundText,
		))
		clearSession(userID)

	case "WAITING_BOOK_ADMIN_NOTE":
		if !isBookRequestAdmin(userID) {
			sendPlainText(bot, chatID, "❌ 权限不足。")
			clearSession(userID)
			return
		}

		adminNote, ok := validateBookRequestNote(text)
		if !ok {
			sendPlainText(bot, chatID, bookRequestNoteInvalidText)
			return
		}
		if adminNote == "" {
			sendPlainText(bot, chatID, "❌ 管理员备注不能为空，请重新发送，或发送“取消”退出。")
			return
		}

		reqIDRaw := session.GetTemp("book_admin_note_req_id")
		reqID64, err := strconv.ParseUint(reqIDRaw, 10, 64)
		if err != nil || reqID64 == 0 {
			sendPlainText(bot, chatID, "❌ 工单编号异常，请重新操作。")
			clearSession(userID)
			return
		}

		reqID := uint(reqID64)
		adminName := getTelegramDisplayName(msg.From)

		currentReq, found, err := loadBookRequestByID(DB, reqID, "admin note input")
		if err != nil {
			sendPlainText(bot, chatID, "❌ 查询工单失败，请稍后再试。")
			return
		}
		if !found {
			sendPlainText(bot, chatID, "❌ 工单不存在，请重新操作。")
			clearSession(userID)
			return
		}

		if !isBookRequestOperableStatus(currentReq.Status) {
			sendPlainText(bot, chatID, "⚠️ 该工单当前不能添加备注。")
			clearSession(userID)
			return
		}

		if !canOperateBookRequest(currentReq, userID) {
			sendPlainText(bot, chatID, "❌ 只有接单人或超级管理员可以备注该工单。")
			clearSession(userID)
			return
		}

		now := time.Now()

		noteSaved := false
		err = DB.Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&BookRequest{}).
				Where("id = ? AND status IN ?", reqID, bookRequestOperableStatuses()).
				Updates(map[string]interface{}{
					"admin_note":     adminNote,
					"admin_id":       userID,
					"admin_name":     adminName,
					"last_action_at": &now,
				})
			if res.Error != nil {
				return fmt.Errorf("book request admin note update failed: %s", formatPlainError(res.Error))
			}
			if res.RowsAffected == 0 {
				return nil
			}
			noteSaved = true
			if err := createBookRequestLogInTx(tx, reqID, userID, adminName, "admin_note", currentReq.Status, currentReq.Status, adminNote); err != nil {
				return err
			}
			return writeAuditLogInTx(tx, userID, "BOOK_REQUEST_ADMIN_NOTE", fmt.Sprintf("%d", reqID), 0, "admin added book request note")
		})
		if err != nil {
			log.Printf("book request admin note failed: req=%d admin=%d err=%s", reqID, userID, formatPlainError(err))
			sendPlainText(bot, chatID, "\u274c \u4fdd\u5b58\u5907\u6ce8\u5931\u8d25\uff0c\u8bf7\u7a0d\u540e\u518d\u8bd5\u3002")
			return
		}

		if !noteSaved {
			sendPlainText(bot, chatID, "\u26a0\ufe0f \u8be5\u5de5\u5355\u4e0d\u5b58\u5728\u6216\u5df2\u5904\u7406\uff0c\u65e0\u6cd5\u7ee7\u7eed\u6dfb\u52a0\u5907\u6ce8\u3002")
			clearSession(userID)
			return
		}

		var updatedReq BookRequest
		if err := reloadBookRequestAfterAdminNote(DB, &updatedReq, reqID, adminNote, userID, adminName, now); err == nil {
			adminMsgChatIDRaw := session.GetTemp("book_admin_note_chat_id")
			adminMsgIDRaw := session.GetTemp("book_admin_note_message_id")

			adminMsgChatID, chatParseErr := strconv.ParseInt(adminMsgChatIDRaw, 10, 64)
			adminMsgID64, msgParseErr := strconv.ParseInt(adminMsgIDRaw, 10, 64)
			if chatParseErr != nil || msgParseErr != nil || adminMsgChatID == 0 || adminMsgID64 == 0 {
				adminMsgChatID = 0
				adminMsgID64 = 0
			}
			currentMsgID := int(adminMsgID64)

			if adminMsgChatID != 0 && currentMsgID != 0 {
				editBookRequestAdminMessage(bot, adminMsgChatID, currentMsgID, updatedReq, false)
			}

			refreshStoredBookRequestAdminMessage(bot, updatedReq, false, adminMsgChatID, currentMsgID)
		}

		sendPlainText(bot, chatID, fmt.Sprintf("✅ 已保存求书工单 #%d 的管理员备注，原工单消息已刷新。", reqID))
		clearSession(userID)

	case "WAITING_BOOK_NEED_INFO_NOTE":
		if !isBookRequestAdmin(userID) {
			sendPlainText(bot, chatID, "❌ 权限不足。")
			clearSession(userID)
			return
		}

		needInfoNote, ok := validateBookRequestNote(text)
		if !ok {
			sendPlainText(bot, chatID, bookRequestNoteInvalidText)
			return
		}
		if needInfoNote == "" {
			sendPlainText(bot, chatID, "❌ 补充信息说明不能为空，请重新发送，或发送“取消”退出。")
			return
		}

		reqIDRaw := session.GetTemp("book_need_info_req_id")
		reqID64, err := strconv.ParseUint(reqIDRaw, 10, 64)
		if err != nil || reqID64 == 0 {
			sendPlainText(bot, chatID, "❌ 工单编号异常，请重新操作。")
			clearSession(userID)
			return
		}

		reqID := uint(reqID64)

		currentReq, found, err := loadBookRequestByID(DB, reqID, "need info input")
		if err != nil {
			sendPlainText(bot, chatID, "❌ 查询工单失败，请稍后再试。")
			return
		}
		if !found {
			sendPlainText(bot, chatID, "❌ 工单不存在，请重新操作。")
			clearSession(userID)
			return
		}

		if !isBookRequestOperableStatus(currentReq.Status) {
			sendPlainText(bot, chatID, "⚠️ 该工单当前不能要求补充信息。")
			clearSession(userID)
			return
		}

		if !canOperateBookRequest(currentReq, userID) {
			sendPlainText(bot, chatID, "❌ 只有接单人或超级管理员可以操作该工单。")
			clearSession(userID)
			return
		}

		now := time.Now()
		adminName := getTelegramDisplayName(msg.From)
		needInfoSaved := false
		err = DB.Transaction(func(tx *gorm.DB) error {
			res := tx.Model(&BookRequest{}).
				Where("id = ? AND status IN ?", reqID, bookRequestOperableStatuses()).
				Updates(map[string]interface{}{
					"status":     bookRequestStatusNeedInfo,
					"admin_note": needInfoNote,
					"admin_id":   userID,
					"admin_name": adminName,
					// 接管语义：要求补充信息的管理员成为当前接单人，
					// 保证用户补充后通知发到实际跟进人（canOperateBookRequest 同样按 assignee 判定）。
					"assignee_id":    userID,
					"assignee_name":  adminName,
					"last_action_at": &now,
					"remind_count":   0,
				})
			if res.Error != nil {
				return fmt.Errorf("book request need info update failed: %s", formatPlainError(res.Error))
			}
			if res.RowsAffected == 0 {
				return nil
			}
			needInfoSaved = true
			if err := createBookRequestLogInTx(tx, reqID, userID, adminName, "need_info", currentReq.Status, bookRequestStatusNeedInfo, needInfoNote); err != nil {
				return err
			}
			return writeAuditLogInTx(tx, userID, "BOOK_REQUEST_NEED_INFO", fmt.Sprintf("%d", reqID), 0, "admin requested book request info")
		})
		if err != nil {
			log.Printf("book request need info failed: req=%d admin=%d err=%s", reqID, userID, formatPlainError(err))
			sendPlainText(bot, chatID, "\u274c \u8bbe\u7f6e\u8865\u5145\u4fe1\u606f\u5931\u8d25\uff0c\u8bf7\u7a0d\u540e\u518d\u8bd5\u3002")
			return
		}
		if !needInfoSaved {
			sendPlainText(bot, chatID, "\u26a0\ufe0f \u8be5\u5de5\u5355\u72b6\u6001\u5df2\u53d8\u5316\uff0c\u8bf7\u7a0d\u540e\u67e5\u770b\u6700\u65b0\u72b6\u6001\u3002")
			clearSession(userID)
			return
		}

		var updatedReq BookRequest
		if err := reloadBookRequestAfterNeedInfo(DB, &updatedReq, reqID, needInfoNote, userID, adminName, now); err == nil {
			adminMsgChatIDRaw := session.GetTemp("book_need_info_chat_id")
			adminMsgIDRaw := session.GetTemp("book_need_info_message_id")

			adminMsgChatID, chatParseErr := strconv.ParseInt(adminMsgChatIDRaw, 10, 64)
			adminMsgID64, msgParseErr := strconv.ParseInt(adminMsgIDRaw, 10, 64)
			if chatParseErr != nil || msgParseErr != nil || adminMsgChatID == 0 || adminMsgID64 == 0 {
				adminMsgChatID = 0
				adminMsgID64 = 0
			}
			currentMsgID := int(adminMsgID64)

			if adminMsgChatID != 0 && currentMsgID != 0 {
				editBookRequestAdminMessage(bot, adminMsgChatID, currentMsgID, updatedReq, false)
			}

			refreshStoredBookRequestAdminMessage(bot, updatedReq, false, adminMsgChatID, currentMsgID)

			sendPlainText(bot, updatedReq.UserID, fmt.Sprintf(
				"❓ 你的求书 #%d 需要补充信息：\n\n%s\n\n请直接回复补充内容，系统会通知接单管理员。",
				updatedReq.ID,
				needInfoNote,
			))
		}

		sendPlainText(bot, chatID, fmt.Sprintf("✅ 已将求书工单 #%d 设置为需要用户补充信息。", reqID))
		clearSession(userID)

	case "WAITING_SET_INVITE_PRICE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		price, err := strconv.Atoi(text)
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 金额格式错误，请输入 0-100000 之间的整数：")
			return
		}

		oldPrice, err := getConfigIntChecked("invite_price", 300)
		if err != nil {
			log.Printf("⚠️ 邀请码价格配置读取失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 邀请码价格配置暂时读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		session.SetTemp("invite_price_new", strconv.Itoa(price))
		session.SetTemp("invite_price_old", strconv.Itoa(oldPrice))
		session.SetStep("WAITING_SET_INVITE_PRICE_REASON")
		replyText(bot, chatID, fmt.Sprintf("📝 邀请码售价将从 `%d` 调整为 `%d` 积分。\n请输入本次调价原因，%s：", oldPrice, price, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_SET_INVITE_PRICE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		price, err := strconv.Atoi(session.GetTemp("invite_price_new"))
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 价格会话状态异常，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}

		oldPrice, err := getConfigIntChecked("invite_price", 300)
		if err != nil {
			log.Printf("⚠️ 邀请码价格二次确认读取配置失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 邀请码价格配置暂时读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		session.SetTemp("invite_price_reason", reason)
		session.SetStep("WAITING_CONFIRM_SET_INVITE_PRICE")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **邀请码价格二次确认**\n\n当前售价：`%d` 积分\n新售价：`%d` 积分\n原因：`%s`\n\n确认更新请回复：`确认设置邀请码价格`\n取消请回复：`取消`",
			oldPrice,
			price,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SET_INVITE_PRICE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认设置邀请码价格" {
			replyText(bot, chatID, "🛑 已取消设置邀请码价格。")
			clearSession(userID)
			return
		}

		price, err := strconv.Atoi(session.GetTemp("invite_price_new"))
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 价格会话状态异常，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("invite_price_reason")

		if _, err := setConfigIntWithAudit(userID, "invite_price", price, 300, "SET_INVITE_PRICE", "invite_price", reason); err != nil {
			log.Printf("⚠️ 设置邀请码价格失败: actor=%d price=%d err=%s", userID, price, formatPlainError(err))
			replyText(bot, chatID, "❌ 邀请码价格更新失败，请稍后重试。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("✅ **管理面板配置成功：邀请码售价已变更为 %d 积分！**", price))
		clearSession(userID)

	case "WAITING_CONFIRM_BREAKTHROUGH":
		handleBreakthroughConfirmation(bot, msg, session, text)

	case "WAITING_CODE_CASHOUT_INPUT":
		quote, err := inspectCodeCashout(text)
		if err != nil {
			if errors.Is(err, errCodeCashoutInvalid) {
				sendPlainText(bot, chatID, "❌ 该卡密不是本服真实可用的邀请码或续期卡，或已经被使用/回收。请核对后重试。")
				return
			}
			if errors.Is(err, errCodeCashoutPriceInvalid) {
				sendPlainText(bot, chatID, "❌ 当前系统定价无法计算有效回收积分，本次回收已中止。")
				clearSession(userID)
				return
			}
			log.Printf("❌ 卡密回收预检失败: user=%d err=%s", userID, formatPlainError(err))
			sendPlainText(bot, chatID, "❌ 卡密状态或回收价格读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		session.SetTemp("cashout_kind", quote.Kind)
		session.SetTemp("cashout_record_id", strconv.FormatUint(uint64(quote.RecordID), 10))
		session.SetTemp("cashout_hash", quote.Hash)
		session.SetTemp("cashout_preview", quote.Preview)
		session.SetTemp("cashout_name", quote.Name)
		session.SetTemp("cashout_points", strconv.Itoa(quote.Points))
		session.SetStep("WAITING_CODE_CASHOUT_CONFIRM")
		UserSessions.Store(userID, session)
		sendPlainText(bot, chatID, fmt.Sprintf("♻️ 本服卡密回收确认\n\n类型：%s\n卡密：%s\n回收所得：%d 积分\n\n确认后该卡密将永久失效，无法充值、交易或恢复。\n\n确认请回复：确认回收卡密\n取消请回复：取消", quote.Name, quote.Preview, quote.Points))

	case "WAITING_CODE_CASHOUT_CONFIRM":
		if text != "确认回收卡密" {
			sendPlainText(bot, chatID, "请回复“确认回收卡密”，或发送“取消”退出。")
			return
		}
		recordID, idErr := strconv.ParseUint(session.GetTemp("cashout_record_id"), 10, 64)
		points, pointsErr := strconv.Atoi(session.GetTemp("cashout_points"))
		quote := codeCashoutQuote{Kind: session.GetTemp("cashout_kind"), RecordID: uint(recordID), Hash: session.GetTemp("cashout_hash"), Preview: session.GetTemp("cashout_preview"), Name: session.GetTemp("cashout_name"), Points: points}
		if idErr != nil || pointsErr != nil || quote.RecordID == 0 || quote.Points <= 0 || quote.Hash == "" {
			sendPlainText(bot, chatID, "❌ 回收确认会话已失效，请重新发起卡密回收。")
			clearSession(userID)
			return
		}
		awarded, err := executeCodeCashout(userID, quote)
		if err != nil {
			switch {
			case errors.Is(err, errCodeCashoutInvalid):
				sendPlainText(bot, chatID, "❌ 卡密已被使用、回收或状态发生变化，本次未发放积分。")
			case errors.Is(err, errCodeCashoutPriceChanged):
				sendPlainText(bot, chatID, "⚠️ 系统定价在确认期间发生变化，本次未回收。请重新发起以确认最新回收价。")
			case errors.Is(err, errCodeCashoutPriceInvalid):
				sendPlainText(bot, chatID, "❌ 当前系统定价无法计算有效回收积分，本次回收已中止。")
			default:
				log.Printf("❌ 卡密回收事务失败: user=%d kind=%s record=%d err=%s", userID, formatPlainValue(quote.Kind), quote.RecordID, formatPlainError(err))
				sendPlainText(bot, chatID, "❌ 卡密回收失败，本次未发放积分且卡密状态未改变，请稍后重试。")
			}
			clearSession(userID)
			return
		}
		sendPlainText(bot, chatID, fmt.Sprintf("✅ 卡密回收成功\n\n类型：%s\n卡密：%s\n获得：%d 积分\n\n该卡密已永久失效。", quote.Name, quote.Preview, awarded))
		clearSession(userID)

	case "WAITING_CONFIRM_BLIND_BOX":
		if text == "确认开启盲盒" {
			replyMsg, broadcastMsg, err := executeBlindBoxOpen(msg.From)
			if err != nil {
				if errors.Is(err, errPointsNotEnough) {
					replyText(bot, chatID, "❌ 您的积分储备余额不足，开盒失败。")
				} else {
					log.Printf("❌ 积分盲盒事务失败: user=%d cost=%d err=%s", userID, blindBoxCost, formatPlainError(err))
					replyText(bot, chatID, "❌ 奖品生成触发底层碰撞保护，为您中止交易。\n💰 本次操作未扣除您的任何积分。")
				}
				clearSession(userID)
				return
			}

			replyText(bot, chatID, replyMsg)
			if broadcastMsg != "" && AppConfig.NoticeGroupID != 0 {
				sendGroupAutoDeleteMessage(bot, AppConfig.NoticeGroupID, broadcastMsg)
			}
		} else {
			replyText(bot, chatID, "🛑 已取消开启积分盲盒。")
		}
		clearSession(userID)

	case "WAITING_SHOP_BUY":
		item, exists := treasureShopItemFromText(text)
		if !exists {
			replyText(bot, chatID, "❌ 输入序号有误，请重新输入或发送 `取消`：")
			return
		}
		session.SetTemp("buy_item_name", item.Name)
		session.SetTemp("buy_item_price", strconv.Itoa(item.Price))
		session.SetStep("WAITING_CONFIRM_SHOP_BUY")
		replyText(bot, chatID, treasureShopBuyConfirmMarkdownText(item)+"\n👉 请回复 `确认购买` 或 `取消`。")
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SHOP_BUY":
		if text == "确认购买" {
			shopItemName := session.GetTemp("buy_item_name")
			price, err := strconv.Atoi(session.GetTemp("buy_item_price"))
			if err != nil || price <= 0 {
				replyText(bot, chatID, "❌ 聚宝斋购买会话异常，已中止。请重新发起购买。")
				clearSession(userID)
				return
			}

			if err := purchaseTreasureShopItem(userID, shopItemName, price); err != nil {
				if errors.Is(err, errInsufficientPoints) {
					replyText(bot, chatID, "❌ 您的积分不足，购买失败。")
				} else {
					replyText(bot, chatID, "❌ 交易异常，购买失败，请稍后重试。")
				}
			} else {
				replyText(bot, chatID, treasureShopBuySuccessMarkdownText(shopItemName))
			}
		} else {
			replyText(bot, chatID, "🛑 已取消购买。")
		}
		clearSession(userID)

	case "WAITING_INVENTORY_ACTION":
		itemName := session.GetTemp(fmt.Sprintf("inv_item_%s", text))
		if itemName == "" {
			replyText(bot, chatID, "❌ 输入序号有误，请重新输入或发送 `取消`：")
			return
		}

		// 🛡️ 防呆机制：拦截破境丹，提醒自动消耗
		if strings.Contains(itemName, "丹") && itemName != "聚灵丹" && itemName != "九转造化丹" {
			replyText(bot, chatID, fmt.Sprintf("⚠️ **【%s】** 是渡劫破阶专用丹药。\n👉 *无需手动吞服*，请在达到境界大圆满时直接发送 `突破` 指令，天道雷劫降临时系统会自动吞服！", inventoryItemMarkdownName(itemName)))
			clearSession(userID)
			return
		}

		// 🧪 经验丹丹毒测算拦截。
		if periodStart, _, maxCount, cycleName, addHours, ok := getManualPillUsageConfig(itemName, time.Now()); ok {
			usedCount, err := countManualPillUsage(userID, itemName, periodStart)
			if err != nil {
				log.Printf("⚠️ 查询丹药服用额度失败: user=%d item=%s err=%s", userID, formatPlainValue(itemName), formatPlainError(err))
				replyText(bot, chatID, "❌ 丹毒沉淀读取失败，暂不能吞服丹药，请稍后重试。")
				clearSession(userID)
				return
			}

			if int(usedCount) >= maxCount {
				replyText(bot, chatID, fmt.Sprintf("🩸 **丹毒警告**：您%s服用【%s】已达上限 (%d/%d次)，继续吞服恐会爆体而亡！", cycleName, inventoryItemMarkdownName(itemName), usedCount, maxCount))
				clearSession(userID)
				return
			}

			session.SetTemp("use_item_name", itemName)
			session.SetTemp("use_item_hours", fmt.Sprintf("%.1f", addHours))
			session.SetTemp("use_item_cycle", cycleName)
			session.SetTemp("use_item_count", fmt.Sprintf("%d", usedCount))
			session.SetTemp("use_item_max", fmt.Sprintf("%d", maxCount))

			session.SetStep("WAITING_CONFIRM_USE_ITEM")
			replyText(bot, chatID, fmt.Sprintf("🔮 **使用确认**：您正准备吞服 **【%s】**。\n⚠️ *%s已服药 (%d/%d) 次，吞服后修为将暴涨 `%.1f` 小时！*\n👉 请回复 `确认使用` 或 `取消`。", inventoryItemMarkdownName(itemName), cycleName, usedCount, maxCount, addHours))
			UserSessions.Store(userID, session)
		}

	case "WAITING_CONFIRM_USE_ITEM":
		if text == "确认使用" {
			itemName := session.GetTemp("use_item_name")

			now := time.Now()
			periodStart, periodKey, maxCount, cycleName, addHours, ok := getManualPillUsageConfig(itemName, now)
			if !ok {
				replyText(bot, chatID, "❌ 该物品不可手动吞服。")
				clearSession(userID)
				return
			}

			// 🛡️ 高并发原子更新消耗引擎：
			// 1. 初始化本周期额度记录。
			// 2. used_count < maxCount 时才能 +1。
			// 3. 扣背包库存。
			// 4. 写使用日志。
			// 5. 增加丹药修为加成。
			// 这些动作在同一个事务里，任何一步失败都会回滚。
			err := DB.Transaction(func(tx *gorm.DB) error {
				var historicalUsed int64
				if err := tx.Model(&ItemUsageLog{}).
					Where("user_id = ? AND item_name = ? AND used_at >= ?", userID, itemName, periodStart).
					Count(&historicalUsed).Error; err != nil {
					return err
				}

				initialUsed := int(historicalUsed)
				if initialUsed > maxCount {
					initialUsed = maxCount
				}

				// 如果额度记录不存在，就按照历史日志初始化。
				// 如果已经存在，则不覆盖。
				if err := createItemUsageQuotaIfMissingInTx(tx, &ItemUsageQuota{
					UserID:    userID,
					ItemName:  itemName,
					PeriodKey: periodKey,
					UsedCount: initialUsed,
				}); err != nil {
					return err
				}

				quotaRes := tx.Model(&ItemUsageQuota{}).
					Where("user_id = ? AND item_name = ? AND period_key = ? AND used_count < ?", userID, itemName, periodKey, maxCount).
					UpdateColumn("used_count", gorm.Expr("used_count + 1"))

				if quotaRes.Error != nil {
					return quotaRes.Error
				}

				if quotaRes.RowsAffected == 0 {
					return errUsageLimitReached
				}

				res := tx.Model(&Inventory{}).
					Where("user_id = ? AND item_name = ? AND quantity > 0", userID, itemName).
					UpdateColumn("quantity", gorm.Expr("quantity - 1"))

				if res.Error != nil {
					return res.Error
				}

				if res.RowsAffected == 0 {
					return errItemNotEnough
				}

				usageLog := ItemUsageLog{
					UserID:   userID,
					ItemName: itemName,
					UsedAt:   now,
				}
				if err := createItemUsageLogInTx(tx, &usageLog); err != nil {
					return err
				}

				return applyPillAudioTimeInTx(tx, userID, addHours)
			})

			if err != nil {
				if errors.Is(err, errUsageLimitReached) {
					replyText(bot, chatID, fmt.Sprintf("🩸 **丹毒警告**：您%s服用【%s】已达上限，不能继续吞服。", cycleName, inventoryItemMarkdownName(itemName)))
				} else if errors.Is(err, errItemNotEnough) {
					replyText(bot, chatID, "❌ 吞服失败，乾坤袋内该物品余量不足。")
				} else {
					log.Printf("⚠️ 吞服丹药事务失败: user=%d item=%s err=%s", userID, formatPlainValue(itemName), formatPlainError(err))
					replyText(bot, chatID, "❌ 吞服失败，系统繁忙，请稍后重试。")
				}
			} else {
				cul := GetOrCreateCultivation(userID)
				newRealm := "`读取失败`"
				if cul != nil {
					SyncCultivationRealm(cul)
					newRealm = GetRealmName(cul)
				} else {
					log.Printf("⚠️ 吞服丹药成功后修仙档案读取失败: user=%d item=%s", userID, formatPlainValue(itemName))
				}
				replyText(bot, chatID, fmt.Sprintf("✨ **吞服成功！**\n\n磅礴的药力在体内化开，您的总修为凭空增加了 `%.1f` 小时！\n📿 当前境界：**%s**", addHours, newRealm))
			}
		} else {
			replyText(bot, chatID, "🛑 已取消吞服。")
		}
		clearSession(userID)

	case "WAITING_SET_RENEW_PRICE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		price, err := strconv.Atoi(text)
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 金额格式错误，请输入 0-100000 之间的整数：")
			return
		}

		oldPrice, err := getConfigIntChecked("renew_price", 150)
		if err != nil {
			log.Printf("⚠️ 续期卡价格配置读取失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 续期卡价格配置暂时读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		session.SetTemp("renew_price_new", strconv.Itoa(price))
		session.SetTemp("renew_price_old", strconv.Itoa(oldPrice))
		session.SetStep("WAITING_SET_RENEW_PRICE_REASON")
		replyText(bot, chatID, fmt.Sprintf("📝 续期卡售价将从 `%d` 调整为 `%d` 积分。\n请输入本次调价原因，%s：", oldPrice, price, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_SET_RENEW_PRICE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		price, err := strconv.Atoi(session.GetTemp("renew_price_new"))
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 价格会话状态异常，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}

		oldPrice, err := getConfigIntChecked("renew_price", 150)
		if err != nil {
			log.Printf("⚠️ 续期卡价格二次确认读取配置失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 续期卡价格配置暂时读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		session.SetTemp("renew_price_reason", reason)
		session.SetStep("WAITING_CONFIRM_SET_RENEW_PRICE")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **续期卡价格二次确认**\n\n当前售价：`%d` 积分\n新售价：`%d` 积分\n原因：`%s`\n\n确认更新请回复：`确认设置续期卡价格`\n取消请回复：`取消`",
			oldPrice,
			price,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SET_RENEW_PRICE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认设置续期卡价格" {
			replyText(bot, chatID, "🛑 已取消设置续期卡价格。")
			clearSession(userID)
			return
		}

		price, err := strconv.Atoi(session.GetTemp("renew_price_new"))
		if err != nil || price < 0 || price > 100000 {
			replyText(bot, chatID, "❌ 价格会话状态异常，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("renew_price_reason")

		if _, err := setConfigIntWithAudit(userID, "renew_price", price, 150, "SET_RENEW_PRICE", "renew_price", reason); err != nil {
			log.Printf("⚠️ 设置续期卡价格失败: actor=%d price=%d err=%s", userID, price, formatPlainError(err))
			replyText(bot, chatID, "❌ 续期卡价格更新失败，请稍后重试。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("✅ **管理面板配置成功：续期卡售价已变更为 %d 积分！**", price))
		clearSession(userID)

	case "WAITING_EXCHANGE_CHOICE":
		if text == "1" {
			invPrice, err := getConfigIntChecked("invite_price", 300)
			if err != nil {
				log.Printf("⚠️ 兑换邀请码价格配置读取失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 邀请码价格配置暂时读取失败，本次交易未扣除积分，请稍后重试。")
				clearSession(userID)
				return
			}

			var code string
			var txCode string

			err = DB.Transaction(func(tx *gorm.DB) error {
				txCode = ""
				// 1. 原子扣除兑换所需积分并写入流水。
				if err := applyPointDeltaInTx(
					tx,
					userID,
					-invPrice,
					"exchange_invite",
					fmt.Sprintf("兑换邀请码，消耗 %d 积分", invPrice),
					"exchange",
					"invite",
				); err != nil {
					if errors.Is(err, errPointsNotEnough) {
						return errInsufficientPoints
					}
					return err
				}

				// 2. 生成邀请码并写入数据库。失败则事务回滚，积分不会被扣。
				for i := 0; i < 5; i++ {
					candidateCode := generateRandomCode(16)

					if err := createInviteCodeRecord(tx, candidateCode); err == nil {
						txCode = candidateCode
						break
					} else {
						if isUniqueConstraintError(err) {
							continue
						}
						return err
					}
				}

				if txCode == "" {
					return errCreateInviteCodeFailed
				}

				return nil
			})
			if err == nil {
				code = txCode
			}

			if err != nil {
				switch assetCreationErrorCode(err) {
				case "USER_NOT_FOUND":
					replyText(bot, chatID, "❌ 未检测到您的积分账户，请先完成注册、绑定或签到初始化账户。")
				case "INSUFFICIENT_POINTS":
					replyText(bot, chatID, "❌ 您的积分储备余额不足，兑换失败。")
				case "CREATE_INVITE_CODE_FAILED":
					replyText(bot, chatID, "❌ 邀请码生成失败，本次交易未扣除积分，请稍后重试。")
				case "SECURITY_PEPPER_NOT_CONFIGURED":
					replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
				default:
					log.Printf("❌ 兑换邀请码事务失败: user=%d price=%d err=%s", userID, invPrice, formatPlainError(err))
					replyText(bot, chatID, "❌ 兑换失败，本次交易未扣除积分，请稍后重试。")
				}

				clearSession(userID)
				return
			}

			inviteHash := hashSensitiveToken(code)
			useMode, modeErr := determineExchangeInviteUseMode(userID)
			if modeErr != nil {
				log.Printf("⚠️ 兑换邀请码后读取直接使用资格失败: user=%d err=%s", userID, formatPlainError(modeErr))
			}
			if inviteHash != "" && modeErr == nil && useMode != exchangeInviteUseNone {
				session.SetTemp("exchange_invite_hash", inviteHash)
				session.SetTemp("exchange_invite_preview", maskSecret(code))
				session.SetTemp("exchange_invite_mode", string(useMode))
				session.SetStep("WAITING_EXCHANGE_INVITE_USE_CONFIRM")
				UserSessions.Store(userID, session)

				if useMode == exchangeInviteUseTrial {
					replyText(bot, chatID, fmt.Sprintf("🎉 **兑换成功！扣除 %d 积分**\n🎁 你的专属邀请码为：`%s`\n\n检测到你当前是新人体验账号，可直接使用该邀请码转为正式账号。\n\n确认使用请回复：`确认使用邀请码`\n暂不使用请回复：`取消`。", invPrice, code))
				} else {
					replyText(bot, chatID, fmt.Sprintf("🎉 **兑换成功！扣除 %d 积分**\n🎁 你的专属邀请码为：`%s`\n\n检测到你尚未注册 ABS 账号，可直接使用该邀请码进入开户注册流程，并跳过再次输入邀请码。\n\n确认开户注册请回复：`确认使用邀请码`\n暂不使用请回复：`取消`。", invPrice, code))
				}
				return
			}

			replyText(bot, chatID, fmt.Sprintf("🎉 **兑换成功！扣除 %d 积分**\n🎁 你的专属邀请码为：`%s`", invPrice, code))
			clearSession(userID)
			return

		} else if text == "2" {
			renPrice, err := getConfigIntChecked("renew_price", 150)
			if err != nil {
				log.Printf("⚠️ 兑换续期卡价格配置读取失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 续期卡价格配置暂时读取失败，本次交易未扣除积分，请稍后重试。")
				clearSession(userID)
				return
			}

			var code string
			var txCode string
			const renewDays = 30

			err = DB.Transaction(func(tx *gorm.DB) error {
				txCode = ""
				// 1. 原子扣除兑换所需积分并写入流水。
				if err := applyPointDeltaInTx(
					tx,
					userID,
					-renPrice,
					"exchange_renew",
					fmt.Sprintf("兑换 %d 天续期卡，消耗 %d 积分", renewDays, renPrice),
					"exchange",
					"renew",
				); err != nil {
					if errors.Is(err, errPointsNotEnough) {
						return errInsufficientPoints
					}
					return err
				}

				// 2. 生成续期卡并写入数据库。失败则事务回滚，积分不会被扣。
				for i := 0; i < 5; i++ {
					candidateCode := fmt.Sprintf("R%d-%s", renewDays, generateRandomCode(16))

					if err := createRenewCodeRecordWithMeta(tx, candidateCode, renewDays, renewCodeSourcePointExchange, userID); err == nil {
						txCode = candidateCode
						break
					} else {
						if isUniqueConstraintError(err) {
							continue
						}
						return err
					}
				}

				if txCode == "" {
					return errCreateRenewCodeFailed
				}

				return nil
			})
			if err == nil {
				code = txCode
			}

			if err != nil {
				switch assetCreationErrorCode(err) {
				case "USER_NOT_FOUND":
					replyText(bot, chatID, "❌ 未检测到您的积分账户，请先完成注册、绑定或签到初始化账户。")
				case "INSUFFICIENT_POINTS":
					replyText(bot, chatID, "❌ 您的积分储备余额不足，兑换失败。")
				case "CREATE_RENEW_CODE_FAILED":
					replyText(bot, chatID, "❌ 续期卡生成失败，本次交易未扣除积分，请稍后重试。")
				case "SECURITY_PEPPER_NOT_CONFIGURED":
					replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
				default:
					log.Printf("❌ 兑换续期卡事务失败: user=%d price=%d err=%s", userID, renPrice, formatPlainError(err))
					replyText(bot, chatID, "❌ 兑换失败，本次交易未扣除积分，请稍后重试。")
				}

				clearSession(userID)
				return
			}

			renewHash := hashSensitiveToken(code)
			if renewHash != "" {
				session.SetTemp("exchange_renew_hash", renewHash)
				session.SetTemp("exchange_renew_preview", maskSecret(code))
				session.SetStep("WAITING_EXCHANGE_RENEW_USE_CONFIRM")
				UserSessions.Store(userID, session)
				replyText(bot, chatID, fmt.Sprintf("🎉 **兑换成功！扣除 %d 积分**\n🎁 30天续期卡密为：`%s`\n\n是否现在直接使用这张续期卡，为你的账号延长 `%d` 天？\n\n确认使用请回复：`确认使用续期卡`\n暂不使用请回复：`取消`。", renPrice, code, renewDays))
				return
			}

			replyText(bot, chatID, fmt.Sprintf("🎉 **兑换成功！扣除 %d 积分**\n🎁 30天续期卡密为：`%s`", renPrice, code))
			clearSession(userID)
			return

		} else if text == "3" {
			clearSession(userID)
			showTreasureShopHome(bot, msg.From, chatID)
			return

		} else {
			replyText(bot, chatID, "⚠️ 输入不识别。请输入数字 1 或 2，或发送 `取消`。")
			return
		}

	case "WAITING_EXCHANGE_RENEW_USE_CONFIRM":
		if text != "确认使用续期卡" {
			replyText(bot, chatID, "请回复 `确认使用续期卡`，或发送 `取消` 暂不使用。")
			return
		}
		if AppConfig.NoticeGroupID != 0 && !isUserInGroupFresh(bot, userID, AppConfig.NoticeGroupID) {
			replyText(bot, chatID, "🚫 检测到您当前不在指定群组内，无法使用续期卡。卡密仍未消费，可稍后再试。")
			clearSession(userID)
			return
		}

		renewHash := strings.TrimSpace(session.GetTemp("exchange_renew_hash"))
		if renewHash == "" {
			replyText(bot, chatID, "❌ 续期卡确认会话已失效。卡密未被消费，请使用刚才收到的卡密手动续期。")
			clearSession(userID)
			return
		}

		result, err := redeemRenewCodeByHash(userID, renewHash)
		if err != nil {
			switch renewRedeemErrorCode(err) {
			case "INVALID_RENEW_CODE":
				replyText(bot, chatID, "❌ 卡密无效或已被消费。")
			case "USER_NOT_FOUND":
				replyText(bot, chatID, "⚠️ 未检测到有效账户。卡密仍未消费，请先注册或绑定账号。")
			case "TRIAL_CANNOT_USE_RENEW_CODE":
				replyText(bot, chatID, "⚠️ 当前为新人体验账号，普通续期卡需使用正式邀请码转正后才能使用。卡密仍未消费。")
			case "RENEW_CODE_OWNER_MISMATCH":
				replyText(bot, chatID, "⚠️ 这张续期卡归属异常，未执行核销。")
			case "SECURITY_PEPPER_NOT_CONFIGURED":
				replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			default:
				log.Printf("⚠️ 兑换续期卡立即使用失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 续期失败，卡密未被消费，请稍后手动使用。")
			}
			clearSession(userID)
			return
		}

		sendRenewRedeemResult(bot, chatID, userID, result)
		clearSession(userID)

	case "WAITING_EXCHANGE_INVITE_USE_CONFIRM":
		if text != "确认使用邀请码" {
			replyText(bot, chatID, "请回复 `确认使用邀请码`，或发送 `取消` 暂不使用。")
			return
		}

		inviteHash := strings.TrimSpace(session.GetTemp("exchange_invite_hash"))
		if inviteHash == "" {
			replyText(bot, chatID, "❌ 邀请码确认会话已失效。邀请码未被消费，请使用刚才收到的邀请码手动注册或转正。")
			clearSession(userID)
			return
		}

		useMode, err := determineExchangeInviteUseMode(userID)
		if err != nil {
			log.Printf("⚠️ 兑换邀请码确认读取账号状态失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 账号状态暂时读取失败，邀请码未被消费，请稍后手动使用。")
			clearSession(userID)
			return
		}

		switch useMode {
		case exchangeInviteUseTrial:
			nextExpireAt, err := convertTrialToFormalWithInviteCode(userID, inviteHash)
			if err != nil {
				if errors.Is(err, errInvalidInviteCode) {
					replyText(bot, chatID, "❌ 邀请码无效或已被使用，请使用刚才收到的邀请码重新确认。")
				} else if errors.Is(err, errTrialFormalInviteOnly) {
					replyText(bot, chatID, "⚠️ 当前账号已不再是新人体验账号，邀请码未被消费。")
				} else {
					log.Printf("兑换邀请码立即转正失败: user=%d err=%s", userID, formatPlainError(err))
					replyText(bot, chatID, "❌ 转正失败，邀请码未被消费，请稍后手动使用。")
				}
				clearSession(userID)
				return
			}
			replyText(bot, chatID, fmt.Sprintf("✅ 转正成功。\n\n账号已转为正式用户，普通续期卡现已可用。\n当前到期时间：`%s`", nextExpireAt.Format("2006-01-02")))
			clearSession(userID)

		case exchangeInviteUseRegister:
			session.SetTemp("invite_hash", inviteHash)
			session.SetTemp("invite_preview", session.GetTemp("exchange_invite_preview"))
			session.SetTemp("exchange_invite_hash", "")
			session.SetTemp("exchange_invite_preview", "")
			session.SetTemp("exchange_invite_mode", "")
			session.SetStep("WAITING_REG_USER")
			UserSessions.Store(userID, session)
			replyText(bot, chatID, "🎫 已为本次开户注册预填刚兑换的邀请码。\n\n📝 **第一步：请输入您想要的用户名**\n(⚠️ 仅限 3-20 位字母、数字、下划线)")

		default:
			replyText(bot, chatID, "⚠️ 当前账号已经拥有正式 ABS 账号，无需直接使用邀请码。邀请码未被消费。")
			clearSession(userID)
		}

	case "WAITING_RED_POINTS":
		pts, err := strconv.Atoi(text)
		if err != nil || pts < 10 {
			replyText(bot, chatID, "❌ 金额不规范，最少 10 积分：")
			return
		}
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 发红包前钱包读取失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 钱包读取失败，请稍后重新输入红包金额。")
			return
		}
		if u.Points < pts {
			replyText(bot, chatID, "❌ 您钱包里的可用积分不足。")
			clearSession(userID)
			return
		}
		session.SetTemp("red_points", text)
		session.SetStep("WAITING_RED_COUNT")
		replyText(bot, chatID, "🔢 请输入裂变分发的 **红包总个数** (3-100)：")
		UserSessions.Store(userID, session)

	case "WAITING_RED_COUNT":
		count, err := strconv.Atoi(text)
		if err != nil || !validRedPacketCount(count) {
			replyText(bot, chatID, "❌ 个数限制在 3 ~ 100 个之间：")
			return
		}

		pts, err := strconv.Atoi(session.GetTemp("red_points"))
		if err != nil || pts < 10 {
			replyText(bot, chatID, "❌ 红包金额异常，请重新发起红包。")
			clearSession(userID)
			return
		}
		if pts < count {
			replyText(bot, chatID, "❌ 红包总积分不能小于红包个数，每个红包至少需要 1 积分。")
			return
		}

		cleanSender := escapeMarkdown(msg.From.FirstName + " " + msg.From.LastName)

		err = DB.Transaction(func(tx *gorm.DB) error {
			txRedID := ""
			// 1. 创建红包。ID 碰撞时重试；后续扣分失败会回滚红包记录。
			for i := 0; i < 5; i++ {
				candidateID := "HB-" + generateRandomCode(10)

				packet := RedPacket{
					ID:          candidateID,
					SenderID:    userID,
					SenderName:  cleanSender,
					TotalPoints: pts,
					Count:       count,
					LeftCount:   count,
					LeftPoints:  pts,
					CreatedAt:   time.Now(),
				}
				err := createRedPacketInTx(tx, &packet)

				if err == nil {
					txRedID = candidateID
					break
				}

				if isUniqueConstraintError(err) {
					continue
				}

				return err
			}

			if txRedID == "" {
				return errCreateRedPacketFailed
			}

			// 2. 原子扣除发包人积分并写入流水。流水和红包创建同事务，避免账实不一致。
			if err := applyPointDeltaInTx(
				tx,
				userID,
				-pts,
				"redpacket_send",
				fmt.Sprintf("发放积分红包，消耗 %d 积分", pts),
				"redpacket",
				txRedID,
			); err != nil {
				if errors.Is(err, errPointsNotEnough) {
					return errInsufficientPoints
				}
				return err
			}

			return nil
		})

		if err != nil {
			switch assetCreationErrorCode(err) {
			case "USER_NOT_FOUND":
				replyText(bot, chatID, "❌ 未检测到您的积分账户，请先完成注册、绑定或签到初始化账户。")
			case "INSUFFICIENT_POINTS":
				replyText(bot, chatID, "❌ 发包过程中积分不足。")
			case "CREATE_REDPACKET_FAILED":
				replyText(bot, chatID, "❌ 红包编号生成失败，本次交易未扣除积分，请稍后重试。")
			default:
				log.Printf("❌ 发红包事务失败: user=%d points=%d count=%d err=%s", userID, pts, count, formatPlainError(err))
				replyText(bot, chatID, "❌ 红包创建失败，本次交易未扣除积分，请稍后重试。")
			}

			clearSession(userID)
			return
		}

		replyText(bot, chatID, "🚀 **红包打包成功！**\n📢 机器人已在大群同步发放。")

		if AppConfig.NoticeGroupID != 0 {
			群信息 := fmt.Sprintf(
				"🧧 **%s 发放了一个拼手气积分红包！**\n\n"+
					"💰 红包总额: `%d` 积分\n"+
					"📦 红包份数: `%d` 份\n\n"+
					"👇 快在群内回复关键字 【`抢`】 拼手气吧！",
				cleanSender,
				pts,
				count,
			)
			sendGroupAutoDeleteMessage(bot, AppConfig.NoticeGroupID, 群信息)
		}

		clearSession(userID)

	case "WAITING_PROMOTE_ID":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "请输入纯数字ID：")
			return
		}
		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止授权自己为管理员。")
			clearSession(userID)
			return
		}
		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已是超级管理员，无需授权。")
			clearSession(userID)
			return
		}
		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 本地查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 授权管理员目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		if tUser.Role == "admin" {
			replyText(bot, chatID, "ℹ️ 目标用户已经是管理员，无需重复授权。")
			clearSession(userID)
			return
		}

		session.SetTemp("promote_tgt_uid", strconv.FormatInt(tgtID, 10))
		session.SetTemp("promote_tgt_username", tUser.Username)
		session.SetStep("WAITING_PROMOTE_REASON")
		replyText(bot, chatID, fmt.Sprintf("📝 即将授权用户 `%s` / `%d` 为管理员。\n请输入授权原因，%s：", escapeMarkdown(tUser.Username), tgtID, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_PROMOTE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tgtID := session.GetTemp("promote_tgt_uid")
		username := session.GetTemp("promote_tgt_username")
		session.SetTemp("promote_reason", reason)
		session.SetStep("WAITING_CONFIRM_PROMOTE")

		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **授权管理员二次确认**\n\n目标用户：`%s`\nTG ID：`%s`\n原因：`%s`\n\n确认授权请回复：`确认授权管理员`\n取消请回复：`取消`",
			escapeMarkdown(username),
			tgtID,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_PROMOTE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认授权管理员" {
			replyText(bot, chatID, "🛑 已取消授权管理员。")
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(session.GetTemp("promote_tgt_uid"), 10, 64)
		if err != nil || tgtID == 0 {
			replyText(bot, chatID, "❌ 授权会话状态异常，已中止。请重新发起授权流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("promote_reason")

		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止授权自己为管理员。")
			clearSession(userID)
			return
		}
		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已是超级管理员，无需授权。")
			clearSession(userID)
			return
		}

		status, err := promoteAdminWithAudit(userID, tgtID, reason)
		if err != nil {
			log.Printf("⚠️ 授权管理员失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 授权失败，请稍后重试。")
			clearSession(userID)
			return
		}
		switch status {
		case adminMutationSelf:
			replyText(bot, chatID, "❌ 禁止授权自己为管理员。")
			clearSession(userID)
			return
		case adminMutationNotFound:
			replyText(bot, chatID, "❌ 本地查无此人。")
			clearSession(userID)
			return
		case adminMutationTargetSuperAdmin:
			replyText(bot, chatID, "❌ 目标用户已是超级管理员，无需授权。")
			clearSession(userID)
			return
		case adminMutationAlreadyAdmin:
			replyText(bot, chatID, "ℹ️ 目标用户已经是管理员，无需重复授权。")
			clearSession(userID)
			return
		case adminMutationTargetStateChanged:
			replyText(bot, chatID, "⚠️ 目标用户状态已变化，请重新发起授权流程。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("👑 晋升成功！用户 `%d` 已成为【管理员】。", tgtID))
		clearSession(userID)

	case "WAITING_WHITELIST_ID":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "请输入纯数字：")
			return
		}
		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止将自己加入白名单。")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 白名单目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		if tUser.Role == "super_admin" {
			replyText(bot, chatID, "❌ 禁止将超级管理员加入白名单。")
			clearSession(userID)
			return
		}

		if tUser.IsWhitelist {
			replyText(bot, chatID, "ℹ️ 目标用户已经在白名单中，无需重复设置。")
			clearSession(userID)
			return
		}

		session.SetTemp("whitelist_tgt_uid", strconv.FormatInt(tgtID, 10))
		session.SetTemp("whitelist_tgt_username", tUser.Username)
		session.SetStep("WAITING_WHITELIST_REASON")
		replyText(bot, chatID, fmt.Sprintf("📝 即将将用户 `%s` / `%d` 加入白名单。\n请输入设置原因，%s：", escapeMarkdown(tUser.Username), tgtID, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_WHITELIST_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tgtID := session.GetTemp("whitelist_tgt_uid")
		username := session.GetTemp("whitelist_tgt_username")
		session.SetTemp("whitelist_reason", reason)
		session.SetStep("WAITING_CONFIRM_WHITELIST")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **白名单二次确认**\n\n目标用户：`%s`\nTG ID：`%s`\n原因：`%s`\n\n加入白名单后，该用户将跳过账号生命周期封禁和自动清理。\n确认设置请回复：`确认设置白名单`\n取消请回复：`取消`",
			escapeMarkdown(username),
			tgtID,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_WHITELIST":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认设置白名单" {
			replyText(bot, chatID, "🛑 已取消设置白名单。")
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(session.GetTemp("whitelist_tgt_uid"), 10, 64)
		if err != nil || tgtID == 0 {
			replyText(bot, chatID, "❌ 白名单会话状态异常，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("whitelist_reason")

		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止将自己加入白名单。")
			clearSession(userID)
			return
		}

		status, err := setWhitelistWithAudit(userID, tgtID, reason)
		if err != nil {
			log.Printf("⚠️ 设置白名单失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 设置白名单失败，请稍后重试。")
			clearSession(userID)
			return
		}
		switch status {
		case adminMutationSelf:
			replyText(bot, chatID, "❌ 禁止将自己加入白名单。")
			clearSession(userID)
			return
		case adminMutationNotFound:
			replyText(bot, chatID, "❌ 查无此人。")
			clearSession(userID)
			return
		case adminMutationTargetSuperAdmin:
			replyText(bot, chatID, "❌ 禁止将超级管理员加入白名单。")
			clearSession(userID)
			return
		case adminMutationAlreadyWhitelisted:
			replyText(bot, chatID, "ℹ️ 目标用户已经在白名单中，无需重复设置。")
			clearSession(userID)
			return
		case adminMutationTargetStateChanged:
			replyText(bot, chatID, "⚠️ 目标用户状态已变化，请重新发起白名单设置流程。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("🏳️ 用户 `%d` 已进入圣光白名单。", tgtID))
		clearSession(userID)

	case "WAITING_SET_SERVER_LINES":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if len([]rune(text)) > 4000 {
			replyText(bot, chatID, "❌ 线路配置内容过长，请控制在 4000 字以内。")
			return
		}

		session.SetTemp("server_lines_content", text)
		session.SetStep("WAITING_SET_SERVER_LINES_REASON")
		replyText(bot, chatID, fmt.Sprintf(
			"📝 **线路配置预览**\n\n%s\n\n请输入本次更新原因，"+adminReasonRequirementText+"：",
			escapeMarkdown(truncateRunes(text, 800)),
		))
		UserSessions.Store(userID, session)

	case "WAITING_SET_SERVER_LINES_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		lines := session.GetTemp("server_lines_content")
		if len([]rune(lines)) > 4000 {
			replyText(bot, chatID, "❌ 线路配置会话状态异常或内容过长，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}

		session.SetTemp("server_lines_reason", reason)
		session.SetStep("WAITING_CONFIRM_SET_SERVER_LINES")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **线路配置二次确认**\n\n内容长度：`%d` 字\n原因：`%s`\n\n确认更新请回复：`确认设置线路`\n取消请回复：`取消`",
			len([]rune(lines)),
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SET_SERVER_LINES":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认设置线路" {
			replyText(bot, chatID, "🛑 已取消设置线路。")
			clearSession(userID)
			return
		}

		lines := session.GetTemp("server_lines_content")
		reason := session.GetTemp("server_lines_reason")
		if len([]rune(lines)) > 4000 {
			replyText(bot, chatID, "❌ 线路配置会话状态异常或内容过长，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}

		validatedLines, ok := validateServerLinesContent(lines)
		if !ok {
			replyText(bot, chatID, "❌ 线路配置会话状态异常或内容不符合要求，已中止。请重新发起设置流程。")
			clearSession(userID)
			return
		}
		lines = validatedLines

		if _, _, err := setServerLinesWithAudit(userID, lines, reason); err != nil {
			log.Printf("⚠️ 更新线路配置失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 线路配置更新失败，请稍后重试。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "✅ **线路配置已成功更新！**")
		clearSession(userID)

	case "WAITING_BACKUP_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}
		if AppConfig == nil || AppConfig.BackupGroupID == 0 {
			replyText(bot, chatID, "⚠️ 系统环境变量中尚未配置 `BACKUP_GROUP_ID`，无法发送。")
			clearSession(userID)
			return
		}

		session.SetTemp("backup_reason", reason)
		session.SetStep("WAITING_CONFIRM_BACKUP")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **手动备份二次确认**\n\n目标：备份群组\n原因：`%s`\n\n备份文件会使用 AES-GCM 加密后发送，请确认备份密钥已妥善保管。\n确认执行请回复：`确认备份`\n取消请回复：`取消`",
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_BACKUP":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认备份" {
			replyText(bot, chatID, "🛑 已取消手动备份。")
			clearSession(userID)
			return
		}

		reason := session.GetTemp("backup_reason")
		if _, ok := validateAdminReason(reason); !ok {
			replyText(bot, chatID, "❌ 备份会话状态异常，已中止。请重新发起备份流程。")
			clearSession(userID)
			return
		}
		if AppConfig == nil || AppConfig.BackupGroupID == 0 {
			replyText(bot, chatID, "⚠️ 系统环境变量中尚未配置 `BACKUP_GROUP_ID`，无法发送。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "⏳ 正在打包加密数据库备份并发送到备份群组...")
		go backupDatabaseToTelegram(bot, userID, reason)
		clearSession(userID)

	case "WAITING_MANAGE_POINTS_ID":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "格式错误，请输入纯数字 TG ID：")
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 目标未查到记录。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 调账目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		session.SetTemp("tgt_uid", text)
		session.SetTemp("tgt_username", tUser.Username)
		session.SetStep("WAITING_MANAGE_POINTS_VAL")
		replyText(bot, chatID, "🔢 请输入增减的积分数值。\n\n限制：\n- 单次最多 `5000` 积分\n- 每个超级管理员每日累计最多 `20000` 积分\n\n增加输入正数，如 `100`；扣除输入负数，如 `-50`。")
		UserSessions.Store(userID, session)

	case "WAITING_MANAGE_POINTS_VAL":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		val, err := strconv.Atoi(text)
		if err != nil || val == 0 {
			replyText(bot, chatID, "格式错误，请输入非 0 整数：")
			return
		}

		if absInt(val) > 5000 {
			replyText(bot, chatID, "❌ 单次调账不能超过 5000 积分。")
			return
		}

		todayTotal, err := getTodayAuditDeltaTotal(userID, "ADJUST_POINTS")
		if err != nil {
			log.Printf("⚠️ 调账额度查询失败: actor=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 调账额度暂时无法读取，请稍后再试。")
			clearSession(userID)
			return
		}
		if adminAdjustDailyLimitExceeded(todayTotal, val) {
			replyText(bot, chatID, fmt.Sprintf("❌ 今日调账额度不足。\n\n今日已累计调整：`%d`\n本次申请：`%d`\n每日上限：`20000`", todayTotal, absInt(val)))
			clearSession(userID)
			return
		}

		session.SetTemp("points_delta", strconv.Itoa(val))
		session.SetStep("WAITING_MANAGE_POINTS_REASON")
		replyText(bot, chatID, "📝 请输入本次调账原因，"+adminReasonRequirementText+"。\n例如：`活动奖励补发`、`异常积分回滚`。")
		UserSessions.Store(userID, session)

	case "WAITING_MANAGE_POINTS_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tID, err := strconv.ParseInt(session.GetTemp("tgt_uid"), 10, 64)
		if err != nil || tID == 0 {
			replyText(bot, chatID, "❌ 调账会话状态异常，已中止。请重新发起调账流程。")
			clearSession(userID)
			return
		}
		val, err := strconv.Atoi(session.GetTemp("points_delta"))
		if err != nil || val == 0 {
			replyText(bot, chatID, "❌ 调账数值状态异常，已中止。请重新发起调账流程。")
			clearSession(userID)
			return
		}

		session.SetTemp("points_reason", reason)
		session.SetStep("WAITING_CONFIRM_MANAGE_POINTS")

		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **调账二次确认**\n\n目标用户：`%d`\n变动积分：`%+d`\n原因：`%s`\n\n确认执行请回复：`确认调账`\n取消请回复：`取消`",
			tID,
			val,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_MANAGE_POINTS":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认调账" {
			replyText(bot, chatID, "🛑 已取消调账。")
			clearSession(userID)
			return
		}

		tID, err := strconv.ParseInt(session.GetTemp("tgt_uid"), 10, 64)
		if err != nil || tID == 0 {
			replyText(bot, chatID, "❌ 调账会话状态异常，已中止。请重新发起调账流程。")
			clearSession(userID)
			return
		}

		val, err := strconv.Atoi(session.GetTemp("points_delta"))
		if err != nil || val == 0 {
			replyText(bot, chatID, "❌ 调账数值状态异常，已中止。请重新发起调账流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("points_reason")

		var beforePoints int
		var afterPoints int
		var actualDelta int
		var targetName string

		err = DB.Transaction(func(tx *gorm.DB) error {
			todayTotal, err := getTodayAuditDeltaTotalTx(tx, userID, "ADJUST_POINTS")
			if err != nil {
				return err
			}
			if adminAdjustDailyLimitExceeded(todayTotal, val) {
				return fmt.Errorf("%w:%d", errDailyAdjustLimitExceeded, todayTotal)
			}

			var tUser User
			if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
				Where("telegram_id = ?", tID).
				First(&tUser).Error; err != nil {
				return err
			}

			beforePoints = tUser.Points
			targetName = tUser.Username

			afterPoints = beforePoints + val
			if afterPoints < 0 {
				afterPoints = 0
			}

			actualDelta = afterPoints - beforePoints
			if actualDelta == 0 {
				return errAdjustNoEffect
			}

			if err := applyPointDeltaInTx(
				tx,
				tID,
				actualDelta,
				"admin_adjust",
				fmt.Sprintf("管理员调账：%s", formatPlainValue(reason)),
				"admin_adjust",
				fmt.Sprintf("%d", userID),
			); err != nil {
				return err
			}

			return writeAuditLogInTx(
				tx,
				userID,
				"ADJUST_POINTS",
				fmt.Sprintf("%d", tID),
				actualDelta,
				fmt.Sprintf("用户 %s(%d) 积分从 %d 调整为 %d，申请变动 %+d，实际变动 %+d，原因：%s", formatPlainValue(targetName), tID, beforePoints, afterPoints, val, actualDelta, formatPlainValue(reason)),
			)
		})

		if err != nil {
			if errors.Is(err, errDailyAdjustLimitExceeded) {
				replyText(bot, chatID, "❌ 今日调账额度不足，每个超级管理员每日累计最多 `20000` 积分。")
			} else if errors.Is(err, errAdjustNoEffect) {
				replyText(bot, chatID, "❌ 本次调账不会产生实际积分变化。")
			} else {
				replyText(bot, chatID, "❌ 调账失败，目标用户可能不存在。")
			}
			clearSession(userID)
			return
		}
		replyText(bot, chatID, fmt.Sprintf("🛠️ **调账成功！**\n用户 `%d` 积分从 `%d` 变更为 `%d`。\n实际变动：`%+d`。", tID, beforePoints, afterPoints, actualDelta))
		replyText(bot, tID, fmt.Sprintf("🔔 **系统账务通知**\n管理员调整了您的积分。\n\n积分变动：`%+d`\n变动后余额：`%d`\n原因：`%s`", actualDelta, afterPoints, escapeMarkdown(reason)))
		clearSession(userID)

	case "WAITING_REG_USER":
		valid, _ := regexp.MatchString(`^[a-zA-Z0-9_]{3,20}$`, text)
		if !valid {
			replyText(bot, chatID, "❌ 用户名格式不合规！只允许 3-20 位的字母、数字或下划线：")
			return
		}

		session.SetTemp("username", text)

		if session.GetTemp("referral_code") != "" {
			session.SetStep("WAITING_REG_SEC_CODE")
			replyText(bot, chatID, "🛡️ 第二步：请设置安全码(PIN)，至少 6 位。")
			UserSessions.Store(userID, session)
			return
		}

		// 已由兑换流程预填邀请码时，不再要求重复输入。
		if session.GetTemp("invite_hash") != "" {
			session.SetStep("WAITING_REG_SEC_CODE")
			replyText(bot, chatID, "🛡️ **第二步：请设置安全码(PIN)**")
			UserSessions.Store(userID, session)
			return
		}

		if AppConfig.InviteRequired {
			campaign, available, openErr := activeOpenRegistrationAt(DB, time.Now())
			if openErr != nil {
				log.Printf("⚠️ 注册时读取开放注册状态失败，保守回退邀请码流程: user=%d err=%s", userID, formatPlainError(openErr))
			}
			if openErr == nil && available {
				session.SetTemp("open_registration_campaign_id", campaign.CampaignID)
				session.SetStep("WAITING_REG_SEC_CODE")
				replyText(bot, chatID, "🚪 当前处于开放注册期，无需邀请码。\n\n🛡️ **第二步：请设置安全码(PIN)**")
			} else {
				session.SetTemp("open_registration_campaign_id", "")
				session.SetStep("WAITING_REG_INVITE")
				replyText(bot, chatID, "🎫 **第二步：请输入您的邀请码**")
			}
		} else {
			session.SetStep("WAITING_REG_SEC_CODE")
			replyText(bot, chatID, "🛡️ **第二步：请设置安全码(PIN)**")
		}

		UserSessions.Store(userID, session)

	case "WAITING_REG_INVITE":
		inviteHash := hashSensitiveToken(text)
		if inviteHash == "" {
			replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			clearSession(userID)
			return
		}

		var invite InviteCode
		if err := DB.Where("code_hash = ? AND is_used = ?", inviteHash, false).First(&invite).Error; err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) {
				log.Printf("注册邀请码预校验读取失败: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 邀请码暂时读取失败，请稍后重试。")
				return
			}
			replyText(bot, chatID, "❌ 邀请码无效，请重新输入或发送 `取消`：")
			return
		}

		session.SetTemp("invite_hash", inviteHash)
		session.SetTemp("invite_preview", maskSecret(text))
		session.SetStep("WAITING_REG_SEC_CODE")
		replyText(bot, chatID, "🛡️ **第三步：请设置安全码(PIN)**")
		UserSessions.Store(userID, session)

	case "WAITING_TRIAL_FORMAL_INVITE":
		inviteHash := hashSensitiveToken(text)
		if inviteHash == "" {
			replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			clearSession(userID)
			return
		}
		nextExpireAt, err := convertTrialToFormalWithInviteCode(userID, inviteHash)
		if err != nil {
			if errors.Is(err, errInvalidInviteCode) {
				replyText(bot, chatID, "❌ 邀请码无效或已被使用，请重新输入或发送 `取消`。")
				return
			}
			if errors.Is(err, errTrialFormalInviteOnly) {
				replyText(bot, chatID, "⚠️ 当前账号不是新人体验账号，无需转正。")
				clearSession(userID)
				return
			}
			log.Printf("trial formal conversion failed: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 转正失败，请稍后重试。")
			return
		}
		replyText(bot, chatID, fmt.Sprintf("✅ 转正成功。\n\n账号已转为正式用户，普通续期卡现已可用。\n当前到期时间：`%s`", nextExpireAt.Format("2006-01-02")))
		clearSession(userID)

	case "WAITING_REG_SEC_CODE":
		if len(text) < 6 {
			replyText(bot, chatID, "❌ 安全码过短，请至少设置 6 位：")
			return
		}

		secCodeHash := hashSensitiveToken(text)
		if secCodeHash == "" {
			replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			clearSession(userID)
			return
		}

		session.SetTemp("security_code_hash", secCodeHash)
		session.SetStep("WAITING_REG_PASS")
		replyText(bot, chatID, "🔐 **最后一步：请输入您的 ABS 登录密码**\n\n密码只会用于本次开户，不会写入机器人本地会话。")
		UserSessions.Store(userID, session)

	case "WAITING_REG_PASS":
		if len(text) < 6 {
			replyText(bot, chatID, "❌ 密码太短，请至少 6 位：")
			return
		}

		username := session.GetTemp("username")
		password := text
		inviteHash := session.GetTemp("invite_hash")
		referralCode := session.GetTemp("referral_code")
		secCodeHash := session.GetTemp("security_code_hash")

		if username == "" || secCodeHash == "" {
			replyText(bot, chatID, "❌ 注册会话已失效，请重新开始注册。")
			clearSession(userID)
			return
		}

		var openReservation OpenRegistrationReservation
		openCampaignID := session.GetTemp("open_registration_campaign_id")
		if inviteHash == "" && referralCode == "" && openCampaignID != "" {
			var reserveErr error
			openReservation, reserveErr = reserveOpenRegistrationSlot(userID, openCampaignID, time.Now())
			if reserveErr != nil {
				switch {
				case errors.Is(reserveErr, errOpenRegistrationFull):
					replyText(bot, chatID, "❌ 本轮开放注册名额刚刚已满，请使用邀请码注册。")
				case errors.Is(reserveErr, errOpenRegistrationExpired), errors.Is(reserveErr, errOpenRegistrationUnavailable):
					replyText(bot, chatID, "❌ 本轮开放注册已结束，请使用邀请码注册。")
				default:
					log.Printf("⚠️ 开放注册名额预占失败: user=%d campaign=%s err=%s", userID, formatPlainValue(openCampaignID), formatPlainError(reserveErr))
					replyText(bot, chatID, "❌ 开放注册名额暂时无法锁定，请稍后重试或使用邀请码。")
				}
				clearSession(userID)
				return
			}
		}

		var reservedInvite InviteCode
		if inviteHash != "" {
			var reserveErr error
			reservedInvite, reserveErr = reserveInviteCodeForRegistrationWithAudit(userID, inviteHash)
			if reserveErr != nil {
				replyText(bot, chatID, "❌ 哎呀手慢了！这个邀请码刚刚已被他人抢先使用。")
				clearSession(userID)
				return
			}
		}

		replyText(bot, chatID, "⏳ 正在连接服务器为您开户...")
		id, err := absClient.RegisterUser(username, password)
		if err != nil {
			var releaseErr error
			if openReservation.ID != 0 {
				if openErr := releaseOpenRegistrationReservation(openReservation.ID, userID, "abs_register_failed"); openErr != nil {
					log.Printf("⚠️ ABS 开户失败后开放注册名额释放失败: user=%d reservation=%d err=%s", userID, openReservation.ID, formatPlainError(openErr))
					replyText(bot, chatID, "❌ 开户失败，且开放注册名额释放异常。系统已记录，请联系管理员核查。")
					clearSession(userID)
					return
				}
			}
			if inviteHash != "" {
				releaseErr = releaseInviteCodeReservationWithAudit(userID, inviteHash, "abs_register_failed")
				if releaseErr != nil {
					log.Printf("⚠️ ABS 开户失败后邀请码退回失败: user=%d invite_id=%d err=%s release_err=%s",
						userID, reservedInvite.ID, formatPlainError(err), formatPlainError(releaseErr))
					replyText(bot, chatID, "❌ 开户失败，且邀请码退回失败。系统已记录异常，请联系管理员核查后再重试。")
					clearSession(userID)
					return
				}
			}

			retryHint := "请稍后重试。"
			if inviteHash != "" {
				retryHint = "🔄 邀请码已退回，请稍后重试。"
			}
			replyText(bot, chatID, "❌ 开户失败: "+formatMarkdownError(err)+"\n"+retryHint)
			return
		}

		var expPtr *time.Time
		if AppConfig.AccountValidDays > 0 {
			exp := time.Now().AddDate(0, 0, AppConfig.AccountValidDays)
			expPtr = &exp
		}

		dbErr := DB.Transaction(func(tx *gorm.DB) error {
			if referralCode != "" {
				_, err := createReferralTrialAccountInTx(tx, userID, username, id, secCodeHash, referralCode, time.Now())
				return err
			}

			var existU User
			err := tx.Where("telegram_id = ?", userID).First(&existU).Error

			if err == nil {
				updates := map[string]interface{}{
					"username":      username,
					"abs_user_id":   id,
					"security_code": secCodeHash,
					"status":        "active",
					"is_suspended":  false,
					"account_type":  accountTypeFormal,
				}

				if nextExpireAt, shouldUpdateExpireAt := registrationExpireAtForExistingUser(existU.ExpireAt, expPtr); shouldUpdateExpireAt {
					if nextExpireAt == nil {
						updates["expire_at"] = nil
					} else {
						updates["expire_at"] = nextExpireAt
					}
				}

				userRes := tx.Model(&User{}).
					Where("id = ? AND telegram_id = ?", existU.ID, userID).
					Updates(updates)
				if userRes.Error != nil {
					return userRes.Error
				}
				if userRes.RowsAffected == 0 {
					return fmt.Errorf("REGISTRATION_USER_STATE_CHANGED")
				}
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				user := User{
					TelegramID:   userID,
					Username:     username,
					AbsUserID:    id,
					SecurityCode: secCodeHash,
					ExpireAt:     expPtr,
					Status:       "active",
					IsSuspended:  false,
					AccountType:  accountTypeFormal,
				}
				if err := createRegisteredUserInTx(tx, &user); err != nil {
					return err
				}
			} else {
				return err
			}

			if openReservation.ID != 0 {
				if err := completeOpenRegistrationReservationInTx(tx, openReservation.ID, userID, time.Now()); err != nil {
					return err
				}
				if err := writeAuditLogInTx(tx, userID, "OPEN_REGISTRATION_COMPLETE", openCampaignID, 0,
					fmt.Sprintf("user %s(%d) completed open registration abs_user_id=%s", formatPlainValue(username), userID, formatPlainValue(id))); err != nil {
					return err
				}
			}

			if reservedInvite.ID != 0 {
				return writeAuditLogInTx(
					tx,
					userID,
					"USE_INVITE_CODE",
					fmt.Sprintf("invite_code_id=%d", reservedInvite.ID),
					0,
					fmt.Sprintf("user %s(%d) completed registration with invite code %s abs_user_id=%s",
						formatPlainValue(username), userID, formatPlainValue(reservedInvite.CodePreview), formatPlainValue(id)),
				)
			}
			return nil
		})

		if dbErr != nil {
			rollbackErr := absClient.DeleteUser(id)

			if rollbackErr != nil && !IsAbsNotFoundError(rollbackErr) {
				replyText(bot, chatID, fmt.Sprintf(
					"❌ **注册中止！**\n\nABS 已创建账号，但本地安全档案写入失败：%s\n\n⚠️ 系统尝试回滚 ABS 账号也失败：%s\n请立刻联系管理员处理，避免产生遗孀账号。",
					formatMarkdownError(dbErr),
					formatMarkdownError(rollbackErr),
				))
				return
			}

			if openReservation.ID != 0 {
				if releaseErr := releaseOpenRegistrationReservation(openReservation.ID, userID, "local_registration_failed"); releaseErr != nil {
					log.Printf("⚠️ 本地注册失败后开放注册名额释放失败: user=%d reservation=%d err=%s", userID, openReservation.ID, formatPlainError(releaseErr))
					replyText(bot, chatID, "❌ 本地开户注册失败，ABS 已回滚，但开放注册名额释放异常，请联系管理员核查。")
					clearSession(userID)
					return
				}
			}

			if referralCode != "" {
				switch {
				case errors.Is(dbErr, errReferralDailyLimit):
					replyText(bot, chatID, "⚠️ 该邀请链接今日新人体验名额已满，请明天再试。")
				case errors.Is(dbErr, errReferralAlreadyTried):
					replyText(bot, chatID, "⚠️ 您已经领取过新人体验，不能重复领取。")
				case errors.Is(dbErr, errReferralSelfInvite):
					replyText(bot, chatID, "❌ 不能使用自己的邀请链接注册新人体验。")
				case errors.Is(dbErr, errReferralInvalidCode), errors.Is(dbErr, errReferralInviterNotEligible):
					replyText(bot, chatID, "❌ 邀请链接无效、已停用，或邀请者暂不具备邀请资格。")
				default:
					log.Printf("⚠️ 邀请链接注册本地归因失败: user=%d err=%s", userID, formatPlainError(dbErr))
					replyText(bot, chatID, "❌ 新人体验注册失败，本次 ABS 账号已回滚，请稍后重试。")
				}
				clearSession(userID)
				return
			}

			if inviteHash != "" {
				if releaseErr := releaseInviteCodeReservationWithAudit(userID, inviteHash, "local_registration_failed"); releaseErr != nil {
					log.Printf("⚠️ 本地注册失败后邀请码退回失败: user=%d invite_id=%d err=%s",
						userID, reservedInvite.ID, formatPlainError(releaseErr))
					replyText(bot, chatID, fmt.Sprintf(
						"❌ 注册失败：本地安全档案写入失败，系统已回滚 ABS 账号，但邀请码退回失败。\n\n错误详情：%s\n请联系管理员核查后再重试。",
						formatMarkdownError(dbErr),
					))
					return
				}
			}

			retryHint := "请稍后重试。"
			if inviteHash != "" {
				retryHint = "🔄 邀请码已退回，请稍后重试。"
			}
			replyText(bot, chatID, fmt.Sprintf(
				"❌ 注册失败：本地安全档案写入失败，系统已自动回滚 ABS 账号。\n\n错误详情：%s\n%s",
				formatMarkdownError(dbErr),
				retryHint,
			))
			return
		}

		if err := ensureABSCrossDaySessionScanCursor(userID, id); err != nil {
			log.Printf("ABS cross-day history cursor enrollment failed after registration: user=%d abs=%s err=%s", userID, formatPlainValue(id), formatPlainError(err))
		}

		if referralCode != "" {
			replyText(bot, chatID, "🎉 新人体验注册成功。\n\n已获得 `7` 天听书体验权限。体验期内累计听书满 `10` 小时后，发送 `新人任务` 可领取 `7` 天体验延期。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "🎉 注册成功！")

		clearSession(userID)

	case "WAITING_BIND_USER":
		session.SetTemp("username", text)
		session.SetStep("WAITING_BIND_PASS")
		replyText(bot, chatID, "🔐 **请输入密码验权：**")
		UserSessions.Store(userID, session)

	case "WAITING_BIND_PASS":
		username := session.GetTemp("username")
		password := text
		replyText(bot, chatID, "⏳ 正在校验身份...")
		go func() {
			absID, err := absClient.VerifyUser(username, password)
			if err != nil {
				replyText(bot, chatID, "❌ 验证失败: "+formatMarkdownError(err))
				return
			}
			var existingUser User
			existingErr := DB.Where("username = ? AND abs_user_id != ?", username, "").First(&existingUser).Error
			if existingErr == nil {
				session.SetTemp("abs_id", absID)
				session.SetTemp("username", username)
				session.SetStep("WAITING_REBIND_SEC_AUTH")
				replyText(bot, chatID, "🔔 **检测到该资产已被绑定**\n请输入原先设置的 **安全码** 强制迁移：")
				UserSessions.Store(userID, session)
				return
			} else if !errors.Is(existingErr, gorm.ErrRecordNotFound) {
				log.Printf("⚠️ 绑定校验后读取既有绑定失败: user=%d username=%s err=%s", userID, formatPlainValue(username), formatPlainError(existingErr))
				replyText(bot, chatID, "❌ 本地绑定状态读取失败，请稍后重试。")
				return
			}
			session.SetTemp("abs_id", absID)
			session.SetTemp("username", username)
			session.SetStep("WAITING_BIND_CREATE_SEC")
			replyText(bot, chatID, "🛡️ 检测到首次接入，**请初始化一个安全码：**")
			UserSessions.Store(userID, session)
		}()

	case "WAITING_REBIND_SEC_AUTH":
		username := session.GetTemp("username")
		absID := session.GetTemp("abs_id")
		var oldUser User

		if err := DB.Where("username = ?", username).First(&oldUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 未找到可迁移的本地档案。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 换绑安全码校验读取旧档案失败: user=%d username=%s err=%s", userID, formatPlainValue(username), formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		if ok, errMsg := verifyUserSecurityCodeWithCooldown(userID, text, oldUser.SecurityCode); !ok {
			replyText(bot, chatID, errMsg)
			return
		}

		if err := rebindLocalUserWithAudit(userID, oldUser.ID, absID); err != nil {
			log.Printf("⚠️ 换绑本地档案或审计写入失败: user=%d target_id=%d abs=%s err=%s", userID, oldUser.ID, formatPlainValue(absID), formatPlainError(err))
			replyText(bot, chatID, "❌ 换绑失败，请稍后重试。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "🎉 **数据安全迁移成功！换绑完成。**\n\n原有效期、积分和资产已原样恢复。")
		if err := ensureABSCrossDaySessionScanCursor(userID, absID); err != nil {
			log.Printf("ABS cross-day history cursor enrollment failed after rebind: user=%d abs=%s err=%s", userID, formatPlainValue(absID), formatPlainError(err))
		}

		clearSession(userID)

	case "WAITING_BIND_CREATE_SEC":
		if len(text) < 6 {
			replyText(bot, chatID, "❌ 安全码过短，请至少设置 6 位：")
			return
		}

		secCodeHash := hashSensitiveToken(text)
		if secCodeHash == "" {
			replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			clearSession(userID)
			return
		}

		absID := session.GetTemp("abs_id")
		username := session.GetTemp("username")

		var expPtr *time.Time
		if AppConfig.AccountValidDays > 0 {
			exp := time.Now().AddDate(0, 0, AppConfig.AccountValidDays)
			expPtr = &exp
		}

		if err := bindLocalUserWithAudit(userID, username, absID, secCodeHash, expPtr); err != nil {
			log.Printf("⚠️ 绑定本地档案或审计写入失败: user=%d username=%s abs=%s err=%s",
				userID, formatPlainValue(username), formatPlainValue(absID), formatPlainError(err))
			replyText(bot, chatID, "❌ 绑定失败，该账号可能已存在本地档案，请尝试走换绑流程。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "🎉 **挂载且安全档案构建成功！资产已同步合并。**")
		if err := ensureABSCrossDaySessionScanCursor(userID, absID); err != nil {
			log.Printf("ABS cross-day history cursor enrollment failed after bind: user=%d abs=%s err=%s", userID, formatPlainValue(absID), formatPlainError(err))
		}

		clearSession(userID)

	case "WAITING_SAFETY_AUTH":
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 安全码验证读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重新输入安全码。")
			return
		}
		if ok, errMsg := verifyUserSecurityCodeWithCooldown(userID, text, u.SecurityCode); !ok {
			replyText(bot, chatID, errMsg)
			clearSession(userID)
			return
		}
		session.SetStep("WAITING_NEW_PASSWORD")
		replyText(bot, chatID, "🔓 验证通过！**请输入新密码：**")
		UserSessions.Store(userID, session)

	case "WAITING_NEW_PASSWORD":
		if len(text) < 6 {
			replyText(bot, chatID, "❌ 密码太短：")
			return
		}
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 修改密码读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重新输入新密码。")
			return
		}
		if strings.TrimSpace(u.AbsUserID) == "" {
			log.Printf("⚠️ 修改密码缺少 ABS 用户ID: user=%d", userID)
			replyText(bot, chatID, "❌ 本地档案缺少 ABS 账号信息，请重新绑定后再试。")
			clearSession(userID)
			return
		}
		replyText(bot, chatID, "⏳ 正在同步密码...")
		go func() {
			if err := absClient.UpdateAbsPassword(u.AbsUserID, text); err != nil {
				replyText(bot, chatID, "❌ 服务端密码更改失败: "+formatMarkdownError(err))
				return
			}
			replyText(bot, chatID, "✅ **服务端密码已修改！**")
		}()
		clearSession(userID)

	case "WAITING_USERNAME_AUTH":
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 修改用户名安全码校验读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重新输入安全码。")
			clearSession(userID)
			return
		}
		if ok, errMsg := verifyUserSecurityCodeWithCooldown(userID, text, u.SecurityCode); !ok {
			replyText(bot, chatID, errMsg)
			clearSession(userID)
			return
		}
		session.SetStep("WAITING_USERNAME_PASSWORD")
		replyText(bot, chatID, "🔓 安全码验证通过！\n\n🔑 **请输入您当前的登录密码以进一步验证身份：**")
		UserSessions.Store(userID, session)

	case "WAITING_USERNAME_PASSWORD":
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 修改用户名密码校验读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		if strings.TrimSpace(u.AbsUserID) == "" {
			replyText(bot, chatID, "❌ 本地档案缺少 ABS 账号信息，请重新绑定后再试。")
			clearSession(userID)
			return
		}
		replyText(bot, chatID, "⏳ 正在校验密码...")
		if _, err := absClient.VerifyUser(u.Username, text); err != nil {
			log.Printf("⚠️ 修改用户名密码校验失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 密码错误，身份校验未通过。修改用户名已取消。")
			clearSession(userID)
			return
		}
		session.SetStep("WAITING_NEW_USERNAME")
		replyText(bot, chatID, "✅ 密码验证通过！\n\n📝 **请输入新的用户名**\n(⚠️ 仅限 3-20 位字母、数字、下划线)")
		UserSessions.Store(userID, session)

	case "WAITING_NEW_USERNAME":
		newUsername := strings.TrimSpace(text)
		if valid, _ := regexp.MatchString(`^[a-zA-Z0-9_]{3,20}$`, newUsername); !valid {
			replyText(bot, chatID, "❌ 用户名格式不合规！只允许 3-20 位的字母、数字或下划线，请重新输入：")
			return
		}

		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; err != nil {
			log.Printf("⚠️ 修改用户名读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重新输入新用户名。")
			return
		}
		if strings.TrimSpace(u.AbsUserID) == "" {
			replyText(bot, chatID, "❌ 本地档案缺少 ABS 账号信息，请重新绑定后再试。")
			clearSession(userID)
			return
		}
		oldUsername := u.Username
		if newUsername == oldUsername {
			replyText(bot, chatID, "⚠️ 新用户名与当前用户名相同，请输入一个不同的用户名：")
			return
		}

		// 改名前先做一次本地占用预检，便于快速失败、避免无谓的 ABS 调用。
		var conflictCount int64
		if err := DB.Model(&User{}).
			Where("username = ? AND telegram_id <> ?", newUsername, userID).
			Count(&conflictCount).Error; err != nil {
			log.Printf("⚠️ 修改用户名占用预检失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 用户名校验失败，请稍后重试。")
			return
		}
		if conflictCount > 0 {
			replyText(bot, chatID, "❌ 该用户名已被占用，请换一个再试：")
			return
		}

		replyText(bot, chatID, "⏳ 正在同步用户名到服务端...")

		if err := absClient.UpdateAbsUsername(u.AbsUserID, newUsername); err != nil {
			log.Printf("⚠️ 修改用户名服务端写入失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 服务端用户名修改失败: "+formatMarkdownError(err))
			clearSession(userID)
			return
		}

		if err := renameLocalUsernameWithAudit(userID, oldUsername, newUsername, u.AbsUserID); err != nil {
			// 本地写入失败：尝试把 ABS 用户名回滚到旧值，避免本地与服务端不一致。
			rollbackErr := absClient.UpdateAbsUsername(u.AbsUserID, oldUsername)
			if rollbackErr != nil {
				log.Printf("❌ 修改用户名本地写入失败且 ABS 回滚失败: user=%d new=%s err=%s rollback_err=%s",
					userID, formatPlainValue(newUsername), formatPlainError(err), formatPlainError(rollbackErr))
				replyText(bot, chatID, fmt.Sprintf(
					"❌ **修改中止！**\n\nABS 已改名，但本地档案写入失败：%s\n\n⚠️ 系统尝试回滚 ABS 用户名也失败：%s\n请立刻联系管理员处理。",
					formatMarkdownError(err),
					formatMarkdownError(rollbackErr),
				))
				clearSession(userID)
				return
			}

			if errors.Is(err, errUsernameTaken) {
				replyText(bot, chatID, "❌ 该用户名刚刚已被他人抢先占用，本次修改已回滚，请换一个再试。")
			} else if errors.Is(err, errUsernameUnchanged) {
				replyText(bot, chatID, "⚠️ 新用户名与当前用户名相同，本次未做修改。")
			} else {
				log.Printf("⚠️ 修改用户名本地写入失败（ABS 已回滚）: user=%d err=%s", userID, formatPlainError(err))
				replyText(bot, chatID, "❌ 本地档案写入失败，本次修改已回滚，请稍后重试。")
			}
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("✅ **用户名修改成功！**\n\n新用户名：`%s`\n下次登录有声书请使用新用户名。", escapeMarkdown(newUsername)))
		clearSession(userID)

	case "WAITING_DELETE_AUTH":
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "⚠️ 未检测到有效账户。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 注销安全码校验读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		if ok, errMsg := verifyUserSecurityCodeWithCooldown(userID, text, u.SecurityCode); !ok {
			replyText(bot, chatID, errMsg)
			clearSession(userID)
			return
		}

		session.SetStep("WAITING_CONFIRM_DELETE")
		replyText(bot, chatID, "⚠️ **最终确认**：此操作不可逆，将永久删除 ABS 账号和本地档案。\n\n确认请回复：`确认注销`")
		UserSessions.Store(userID, session)

	case "WAITING_UNBIND_AUTH":
		var u User
		if err := DB.Where("telegram_id = ?", userID).First(&u).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "⚠️ 未检测到有效账户。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 解绑安全码校验读取本地档案失败: user=%d err=%s", userID, formatPlainError(err))
			replyText(bot, chatID, "❌ 本地档案读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		if ok, errMsg := verifyUserSecurityCodeWithCooldown(userID, text, u.SecurityCode); !ok {
			replyText(bot, chatID, errMsg)
			clearSession(userID)
			return
		}

		if err := unbindLocalUserWithAudit(userID, u.AbsUserID); err != nil {
			log.Printf("⚠️ 解绑本地档案或审计写入失败: user=%d abs=%s err=%s", userID, formatPlainValue(u.AbsUserID), formatPlainError(err))
			replyText(bot, chatID, "❌ 解绑失败，请稍后重试。")
			clearSession(userID)
			return
		}

		sendUserMainMenu(bot, chatID, "🔄 **本地安全解除挂载成功！**\n\n您的资产档案已冻结保留，重新绑定时不会重新赠送有效期。")
		clearSession(userID)

	case "WAITING_RENEW_CODE":
		if AppConfig.NoticeGroupID != 0 && !isUserInGroupFresh(bot, userID, AppConfig.NoticeGroupID) {
			replyText(bot, chatID, "🚫 检测到您当前不在指定群组内，无法使用续期卡。")
			clearSession(userID)
			return
		}

		renewHash := hashSensitiveToken(text)
		if renewHash == "" {
			replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			clearSession(userID)
			return
		}

		result, err := redeemRenewCodeByHash(userID, renewHash)

		if err != nil {
			switch renewRedeemErrorCode(err) {
			case "INVALID_RENEW_CODE":
				replyText(bot, chatID, "❌ 卡密无效或已被消费。")
			case "USER_NOT_FOUND":
				replyText(bot, chatID, "⚠️ 未检测到有效账户。")
			case "TRIAL_CANNOT_USE_RENEW_CODE":
				replyText(bot, chatID, "⚠️ 当前为新人体验账号，仅支持新人体验延期。普通续期卡需使用正式邀请码转正后才能使用。")
			case "RENEW_CODE_OWNER_MISMATCH":
				replyText(bot, chatID, "⚠️ 这张续期卡来自积分兑换，仅限当前持有人本人使用；若需转让，请通过交易行出售后完成归属转移。")
			case "SECURITY_PEPPER_NOT_CONFIGURED":
				replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			default:
				replyText(bot, chatID, "❌ 续期失败，请稍后重试。")
			}
			return
		}

		sendRenewRedeemResult(bot, chatID, userID, result)
		clearSession(userID)

	case "WAITING_CONFIRM_DELETE":
		if text == "确认注销" {
			if isSuperAdmin(userID) {
				replyText(bot, chatID, "❌ 超级管理员账号禁止通过自助注销物理删除。请先完成权限交接并移出超级管理员名单后再处理。")
				clearSession(userID)
				return
			}

			var u User
			userErr := DB.Where("telegram_id = ?", userID).First(&u).Error
			if userErr == nil {
				if u.AbsUserID != "" {
					if err := absClient.DeleteUser(u.AbsUserID); err != nil && !IsAbsNotFoundError(err) {
						replyText(bot, chatID, fmt.Sprintf(
							"❌ **注销中止！**\n\nABS 服务端删除失败：%s\n\n为了避免服务端账号残留，本地档案暂时保留。请稍后重试或联系管理员。",
							formatMarkdownError(err),
						))
						clearSession(userID)
						return
					}
				}

				if err := deleteLocalUserWithAudit(userID, userID, u.AbsUserID, "SELF_DELETE_USER", func(deleted User) string {
					return fmt.Sprintf("用户自助注销并物理删除本地档案：username=%s tg=%d abs_user_id=%s", formatPlainValue(deleted.Username), userID, formatPlainValue(deleted.AbsUserID))
				}); err != nil {
					log.Printf("⚠️ 用户自助注销本地档案或审计写入失败: user=%d abs=%s err=%s", userID, formatPlainValue(u.AbsUserID), formatPlainError(err))
					replyText(bot, chatID, "⚠️ ABS 账号已删除，但本地档案或审计写入失败，请立即联系管理员人工核查。")
					clearSession(userID)
					return
				}
				replyText(bot, chatID, "🗑 账户和本地安全档案已连根抹除。")
			} else if errors.Is(userErr, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "⚠️ 未找到本地账户档案，注销无需执行。")
			} else {
				log.Printf("⚠️ 自助注销读取本地档案失败: user=%d err=%s", userID, formatPlainError(userErr))
				replyText(bot, chatID, "❌ 本地档案读取失败，注销未执行，请稍后重试。")
			}
		} else {
			replyText(bot, chatID, "注销中止。")
		}
		clearSession(userID)

	case "WAITING_GEN_INVITE_COUNT":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		count, err := strconv.Atoi(text)
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 请输入有效数量，范围 1-100：")
			return
		}

		session.SetTemp("invite_count", strconv.Itoa(count))
		session.SetStep("WAITING_GEN_INVITE_REASON")
		replyText(bot, chatID, "📝 请输入本次批量生成邀请码的原因，"+adminReasonRequirementText+"：")
		UserSessions.Store(userID, session)

	case "WAITING_GEN_INVITE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		count, err := strconv.Atoi(session.GetTemp("invite_count"))
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 邀请码生成数量状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}
		session.SetTemp("invite_reason", reason)
		session.SetStep("WAITING_CONFIRM_GEN_INVITE")

		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **批量生成邀请码二次确认**\n\n数量：`%d`\n原因：`%s`\n\n确认生成请回复：`确认生成邀请码`\n取消请回复：`取消`",
			count,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_GEN_INVITE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认生成邀请码" {
			replyText(bot, chatID, "🛑 已取消生成邀请码。")
			clearSession(userID)
			return
		}

		count, err := strconv.Atoi(session.GetTemp("invite_count"))
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 邀请码生成数量状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("invite_reason")

		res := "✅ **成功生成邀请码：**\n\n"
		codes, err := generateInviteCodesWithAudit(userID, count, reason)
		if err != nil {
			log.Printf("⚠️ 批量生成邀请码失败: actor=%d count=%d err=%s", userID, count, formatPlainError(err))
			replyText(bot, chatID, "❌ 邀请码生成失败，未创建任何新卡密，请稍后重试。")
			clearSession(userID)
			return
		}
		for _, c := range codes {
			res += fmt.Sprintf("`%s`\n", c)
		}

		replyText(bot, chatID, res)
		clearSession(userID)

	case "WAITING_GEN_RENEW_DAYS":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		days, err := strconv.Atoi(text)
		if err != nil || days <= 0 || days > 365 {
			replyText(bot, chatID, "❌ 天数输入错误，允许范围 1-365：")
			return
		}

		session.SetTemp("days", strconv.Itoa(days))
		session.SetStep("WAITING_GEN_RENEW_COUNT")
		replyText(bot, chatID, fmt.Sprintf("🔢 确认面额为 `%d` 天。请输入生成的卡密张数，范围 1-100：", days))
		UserSessions.Store(userID, session)

	case "WAITING_GEN_RENEW_COUNT":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		count, err := strconv.Atoi(text)
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 张数限制 1-100：")
			return
		}

		session.SetTemp("renew_count", strconv.Itoa(count))
		session.SetStep("WAITING_GEN_RENEW_REASON")
		replyText(bot, chatID, "📝 请输入本次批量生成续期卡的原因，"+adminReasonRequirementText+"：")
		UserSessions.Store(userID, session)

	case "WAITING_GEN_RENEW_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		days, err := strconv.Atoi(session.GetTemp("days"))
		if err != nil || days <= 0 || days > 365 {
			replyText(bot, chatID, "❌ 续期卡天数状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}
		count, err := strconv.Atoi(session.GetTemp("renew_count"))
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 续期卡生成数量状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}

		session.SetTemp("renew_reason", reason)
		session.SetStep("WAITING_CONFIRM_GEN_RENEW")

		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **批量生成续期卡二次确认**\n\n面额：`%d` 天\n数量：`%d` 张\n原因：`%s`\n\n确认生成请回复：`确认生成续期卡`\n取消请回复：`取消`",
			days,
			count,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_GEN_RENEW":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认生成续期卡" {
			replyText(bot, chatID, "🛑 已取消生成续期卡。")
			clearSession(userID)
			return
		}

		days, err := strconv.Atoi(session.GetTemp("days"))
		if err != nil || days <= 0 || days > 365 {
			replyText(bot, chatID, "❌ 续期卡天数状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}
		count, err := strconv.Atoi(session.GetTemp("renew_count"))
		if err != nil || count <= 0 || count > 100 {
			replyText(bot, chatID, "❌ 续期卡生成数量状态异常，已中止。请重新发起生成流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("renew_reason")

		res := fmt.Sprintf("✅ **成功生成 %d 天续期卡密：**\n\n", days)
		codes, err := generateRenewCodesWithAudit(userID, days, count, reason)
		if err != nil {
			log.Printf("⚠️ 批量生成续期卡失败: actor=%d days=%d count=%d err=%s", userID, days, count, formatPlainError(err))
			replyText(bot, chatID, "❌ 续期卡生成失败，未创建任何新卡密，请稍后重试。")
			clearSession(userID)
			return
		}
		for _, c := range codes {
			res += fmt.Sprintf("`%s`\n", c)
		}

		replyText(bot, chatID, res)
		clearSession(userID)

	case "WAITING_SIMULATE_EXPIRE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "❌ 格式错误，请输入纯数字 TG ID。")
			return
		}
		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止将自己强制设为过期。")
			clearSession(userID)
			return
		}
		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户是超级管理员，禁止模拟过期。")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 模拟过期目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		session.SetTemp("simulate_expire_tgt_uid", strconv.FormatInt(tgtID, 10))
		session.SetTemp("simulate_expire_tgt_username", tUser.Username)
		session.SetStep("WAITING_SIMULATE_EXPIRE_REASON")
		replyText(bot, chatID, fmt.Sprintf("📝 即将将用户 `%s` / `%d` 强制设为已过期。\n请输入操作原因，%s：", escapeMarkdown(tUser.Username), tgtID, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_SIMULATE_EXPIRE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tgtID := session.GetTemp("simulate_expire_tgt_uid")
		username := session.GetTemp("simulate_expire_tgt_username")
		session.SetTemp("simulate_expire_reason", reason)
		session.SetStep("WAITING_CONFIRM_SIMULATE_EXPIRE")
		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **模拟过期二次确认**\n\n目标用户：`%s`\nTG ID：`%s`\n原因：`%s`\n\n此操作会把用户到期时间改为昨天，并解除本地封禁状态，后续生命周期巡检会按过期账号处理。\n确认执行请回复：`确认模拟过期`\n取消请回复：`取消`",
			escapeMarkdown(username),
			tgtID,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SIMULATE_EXPIRE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认模拟过期" {
			replyText(bot, chatID, "🛑 已取消模拟过期。")
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(session.GetTemp("simulate_expire_tgt_uid"), 10, 64)
		if err != nil || tgtID == 0 {
			replyText(bot, chatID, "❌ 模拟过期会话状态异常，已中止。请重新发起流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("simulate_expire_reason")

		if tgtID == userID {
			replyText(bot, chatID, "❌ 禁止将自己强制设为过期。")
			clearSession(userID)
			return
		}
		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		status, _, err := simulateExpireWithAudit(userID, tgtID, reason)
		if err != nil {
			log.Printf("⚠️ 模拟过期失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 模拟过期写入失败，请稍后重试。")
			clearSession(userID)
			return
		}
		switch status {
		case adminMutationSelf:
			replyText(bot, chatID, "❌ 禁止将自己强制设为过期。")
			clearSession(userID)
			return
		case adminMutationNotFound:
			replyText(bot, chatID, "❌ 查无此人。")
			clearSession(userID)
			return
		case adminMutationTargetSuperAdmin:
			replyText(bot, chatID, "❌ 目标用户是超级管理员，操作中止。")
			clearSession(userID)
			return
		case adminMutationTargetStateChanged:
			replyText(bot, chatID, "⚠️ 目标用户状态已变化，请重新发起模拟过期流程。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("⏱️ 用户 `%d` 已被设置为过期。", tgtID))
		clearSession(userID)

	case "WAITING_CLEAN_WIDOWS_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		idsStr := session.GetTemp("widow_ids")
		ids := strings.Split(idsStr, ",")

		session.SetTemp("widow_reason", reason)
		session.SetStep("WAITING_CONFIRM_CLEAN_WIDOWS")

		replyText(bot, chatID, fmt.Sprintf(
			"🚨 **清理遗孀二次确认**\n\n待清理数量：`%d`\n原因：`%s`\n\n此操作会硬删除 ABS 服务端账号，不可逆。\n确认执行请回复：`确认清理`\n取消请回复：`取消`",
			len(ids),
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_CLEAN_WIDOWS":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text == "确认清理" {
			processingMsg, sendErr := sendAutoDelete(bot, tgbotapi.NewMessage(chatID, "💥 正在执行物理清除，请勿操作机器人...\n由于账号可能较多，这可能需要几分钟时间。"))
			if sendErr != nil {
				log.Printf("发送遗孀清理进度消息失败: chat=%d err=%s", chatID, formatTelegramSendError(sendErr))
			}

			idsStr := session.GetTemp("widow_ids")
			reason := session.GetTemp("widow_reason")
			ids := strings.Split(idsStr, ",")

			go func(targetIDs []string, msgID int, reason string) {
				successCount := 0
				failCount := 0

				for _, id := range targetIDs {
					if id != "" {
						if err := absClient.DeleteUser(id); err == nil || IsAbsNotFoundError(err) {
							successCount++
						} else {
							failCount++
						}
						time.Sleep(150 * time.Millisecond)
					}
				}

				auditErr := writeAuditLogInTx(DB, userID, "CLEAN_WIDOWS", "ABS", 0, fmt.Sprintf("清理遗孀账号，目标 %d 个，成功 %d 个，失败 %d 个，原因：%s", len(targetIDs), successCount, failCount, formatPlainValue(reason)))
				if auditErr != nil {
					log.Printf("⚠️ 遗孀清理审计写入失败: actor=%d targets=%d success=%d fail=%d err=%s", userID, len(targetIDs), successCount, failCount, formatPlainError(auditErr))
					notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ 遗孀清理已执行，但 CLEAN_WIDOWS 审计写入失败。\n执行人：%d\n目标：%d\n成功：%d\n失败：%d\n错误：%s\n请立即人工核查。", userID, len(targetIDs), successCount, failCount, formatPlainError(auditErr)))
				}

				finalText := fmt.Sprintf("✅ **大清洗完成！**\n成功抹除了 `%d` 个遗孀账号。\n失败：`%d` 个。", successCount, failCount)
				if auditErr != nil {
					finalText += "\n\n⚠️ 审计写入失败，已通知超级管理员人工核查。"
				}
				editMsg := tgbotapi.NewEditMessageText(chatID, msgID, finalText)
				editMsg.ParseMode = "Markdown"
				if _, err := bot.Request(editMsg); err != nil {
					log.Printf("编辑遗孀清理进度消息失败: chat=%d message=%d err=%s", chatID, msgID, formatTelegramSendError(err))
				}
			}(ids, processingMsg.MessageID, reason)

		} else {
			replyText(bot, chatID, "🛑 已中止清理任务，遗孀们松了一口气。")
		}

		clearSession(userID)

	case "WAITING_QUERY_USER":
		var targetUser User
		foundUser := false

		cleanQuery := strings.TrimSpace(strings.TrimPrefix(text, "@"))

		if tgID, parseErr := strconv.ParseInt(cleanQuery, 10, 64); parseErr == nil {
			err := DB.Where("telegram_id = ?", tgID).First(&targetUser).Error
			if err == nil {
				foundUser = true
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				err = DB.Where("username = ?", cleanQuery).First(&targetUser).Error
				if err == nil {
					foundUser = true
				} else if errors.Is(err, gorm.ErrRecordNotFound) {
					foundUser = false
				} else {
					log.Printf("⚠️ 查询用户用户名回退读取失败: actor=%d query=%s err=%s", userID, formatPlainValue(cleanQuery), formatPlainError(err))
					replyText(bot, chatID, "❌ 用户档案读取失败，请稍后重试。")
					clearSession(userID)
					return
				}
			} else {
				log.Printf("⚠️ 查询用户 TG ID 读取失败: actor=%d query=%s err=%s", userID, formatPlainValue(cleanQuery), formatPlainError(err))
				replyText(bot, chatID, "❌ 用户档案读取失败，请稍后重试。")
				clearSession(userID)
				return
			}
		} else {
			err := DB.Where("username = ?", cleanQuery).First(&targetUser).Error
			if err == nil {
				foundUser = true
			} else if errors.Is(err, gorm.ErrRecordNotFound) {
				foundUser = false
			} else {
				log.Printf("⚠️ 查询用户用户名读取失败: actor=%d query=%s err=%s", userID, formatPlainValue(cleanQuery), formatPlainError(err))
				replyText(bot, chatID, "❌ 用户档案读取失败，请稍后重试。")
				clearSession(userID)
				return
			}
		}

		if !foundUser {
			replyText(bot, chatID, "❌ 数据库中未查找到该用户。")
			clearSession(userID)
			return
		}

		status := resolveUserAccountStatusDisplay(targetUser, time.Now(), accountStatusDisplayAdmin, true).Text

		expText := "永久有效"
		if targetUser.IsWhitelist {
			expText = "🏳️ 白名单 (永久免保号清理)"
		} else if targetUser.ExpireAt != nil {
			expText = targetUser.ExpireAt.Format("2006-01-02 15:04:05")
		}

		realRole := getUserRole(targetUser.TelegramID)
		roleDisplay := "👤 普通用户"
		if realRole == "super_admin" {
			roleDisplay = "👑 超级管理员"
		} else if realRole == "admin" {
			roleDisplay = "🛠️ 管理员"
		}

		targetCul := GetOrCreateCultivation(targetUser.TelegramID)
		targetRealm := GetRealmName(targetCul)
		targetCultivationHoursText := "`读取失败`"
		targetTribulationFailsText := "`读取失败`"
		if targetCul != nil {
			targetCultivationHoursText = fmt.Sprintf("`%.1f`", targetCul.TotalAudioTime)
			targetTribulationFailsText = fmt.Sprintf("%d", targetCul.TribulationFails)
		} else {
			targetRealm = "`读取失败`"
		}

		info := fmt.Sprintf("📊 **用户档案查询结果**\n\n"+
			"👤 **名称 (TG/ABS)**: `%s`\n"+
			"🆔 **TG 绑定 ID**: `%d`\n"+
			"🔑 **ABS 库 ID**: `%s`\n"+
			"🪪 **当前积分**: `%d`\n"+
			"🎖️ **系统角色**: %s\n"+
			"⏳ **到期时间**: %s\n"+
			"🛡️ **当前状态**: %s\n"+
			"──────────────\n"+
			"📿 **修仙境界**: %s\n"+
			"⏱ **闭关时长**: %s 小时 (失败: %s)",
			escapeMarkdown(targetUser.Username), targetUser.TelegramID, escapeMarkdown(targetUser.AbsUserID),
			targetUser.Points, roleDisplay, expText, status,
			targetRealm, targetCultivationHoursText, targetTribulationFailsText)

		writeAuditLog(
			userID,
			"QUERY_USER_PROFILE",
			fmt.Sprintf("%d", targetUser.TelegramID),
			fmt.Sprintf("管理员查询用户档案：username=%s abs_user_id=%s", formatPlainValue(targetUser.Username), formatPlainValue(targetUser.AbsUserID)),
		)
		replyText(bot, chatID, info)
		clearSession(userID)

	case "WAITING_SUSPEND_USER":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "❌ 格式错误，请输入纯数字 TG ID：")
			return
		}

		if tgtID == userID {
			replyText(bot, chatID, "❌ 警告：系统禁止对自己执行封禁操作！")
			clearSession(userID)
			return
		}

		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 警告：免死金牌生效，无法对超级管理员执行封禁操作！")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 本地数据库查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 封禁入口目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		if tUser.AbsUserID == "" {
			replyText(bot, chatID, "⚠️ 该用户为幽灵账户（未绑定 ABS），无需封禁。")
			clearSession(userID)
			return
		}

		newSuspendStatus := !tUser.IsSuspended
		actionText := "封禁/暂停"
		confirmText := "确认封禁"
		if !newSuspendStatus {
			actionText = "解封/恢复"
			confirmText = "确认解封"
		}

		session.SetTemp("suspend_tgt_uid", strconv.FormatInt(tgtID, 10))
		session.SetTemp("suspend_tgt_username", tUser.Username)
		session.SetTemp("suspend_new_status", fmt.Sprintf("%t", newSuspendStatus))
		session.SetTemp("suspend_confirm_text", confirmText)
		session.SetTemp("suspend_action_text", actionText)
		session.SetStep("WAITING_SUSPEND_REASON")

		replyText(bot, chatID, fmt.Sprintf("📝 即将对用户 `%d` 执行【%s】。\n请输入操作原因，%s：", tgtID, actionText, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_SUSPEND_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tgtID := session.GetTemp("suspend_tgt_uid")
		username := session.GetTemp("suspend_tgt_username")
		actionText := session.GetTemp("suspend_action_text")
		confirmText := session.GetTemp("suspend_confirm_text")

		session.SetTemp("suspend_reason", reason)
		session.SetStep("WAITING_CONFIRM_SUSPEND_USER")

		replyText(bot, chatID, fmt.Sprintf(
			"⚠️ **封禁/解封二次确认**\n\n目标用户：`%s`\nTG ID：`%s`\n操作：`%s`\n原因：`%s`\n\n确认执行请回复：`%s`\n取消请回复：`取消`",
			escapeMarkdown(username),
			tgtID,
			escapeMarkdown(actionText),
			escapeMarkdown(reason),
			confirmText,
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_SUSPEND_USER":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		confirmText := session.GetTemp("suspend_confirm_text")
		if text != confirmText {
			replyText(bot, chatID, "🛑 已取消封禁/解封操作。")
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(session.GetTemp("suspend_tgt_uid"), 10, 64)
		if err != nil || tgtID == 0 {
			replyText(bot, chatID, "❌ 封禁/解封会话状态异常，已中止。请重新发起流程。")
			clearSession(userID)
			return
		}
		reason, ok := validateAdminReason(session.GetTemp("suspend_reason"))
		if !ok {
			replyText(bot, chatID, "❌ 封禁/解封原因异常，已中止。请重新发起流程。")
			clearSession(userID)
			return
		}
		newSuspendRaw := session.GetTemp("suspend_new_status")
		if newSuspendRaw != "true" && newSuspendRaw != "false" {
			replyText(bot, chatID, "❌ 封禁/解封目标状态异常，已中止。请重新发起流程。")
			clearSession(userID)
			return
		}
		newSuspendStatus := newSuspendRaw == "true"

		if tgtID == userID {
			replyText(bot, chatID, "❌ 警告：系统禁止对自己执行封禁操作！")
			clearSession(userID)
			return
		}
		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 本地数据库查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 封禁确认目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}
		if tUser.Role == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已经是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		if tUser.AbsUserID == "" {
			replyText(bot, chatID, "⚠️ 该用户为幽灵账户（未绑定 ABS），无法同步服务端封禁状态。")
			clearSession(userID)
			return
		}

		actionText := "封禁/暂停"
		auditAction := "SUSPEND_USER"
		if !newSuspendStatus {
			actionText = "解封/恢复"
			auditAction = "UNSUSPEND_USER"
		}

		apiErr := absClient.SetUserActiveStatus(tUser.AbsUserID, !newSuspendStatus)
		if apiErr != nil {
			auditErr := writeAuditLogInTx(DB, userID, auditAction+"_FAILED", fmt.Sprintf("%d", tgtID), 0, fmt.Sprintf("用户 %s(%d) 执行%s时 ABS 服务端状态更新失败，原因：%s，错误：%s", formatPlainValue(tUser.Username), tgtID, actionText, formatPlainValue(reason), formatPlainError(apiErr)))
			if auditErr != nil {
				log.Printf("⚠️ ABS 状态更新失败审计写入失败: actor=%d target=%d action=%s err=%s", userID, tgtID, formatPlainValue(auditAction+"_FAILED"), formatPlainError(auditErr))
				notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ %s 失败，且失败审计写入失败。\n执行人：%d\n目标：%d\nABS错误：%s\n审计错误：%s", actionText, userID, tgtID, formatPlainError(apiErr), formatPlainError(auditErr)))
			}
			replyText(bot, chatID, fmt.Sprintf("❌ ABS 服务端状态更新失败: %s", formatMarkdownError(apiErr)))
			clearSession(userID)
			return
		}

		if err := applySuspendLocalStatusWithAudit(userID, tgtID, tUser.AbsUserID, newSuspendStatus, auditAction, reason); err != nil {
			log.Printf("⚠️ ABS 状态已更新，但本地封禁状态或审计写入失败: user=%d err=%s", tgtID, formatPlainError(err))
			auditErr := writeAuditLogInTx(DB, userID, auditAction+"_LOCAL_FAILED", fmt.Sprintf("%d", tgtID), 0, fmt.Sprintf("用户 %s(%d) 执行%s时 ABS 已更新但本地状态或审计写入失败，原因：%s，错误：%s", formatPlainValue(tUser.Username), tgtID, actionText, formatPlainValue(reason), formatPlainError(err)))
			if auditErr != nil {
				log.Printf("⚠️ ABS 状态已更新，但本地失败审计写入失败: actor=%d target=%d action=%s err=%s", userID, tgtID, formatPlainValue(auditAction+"_LOCAL_FAILED"), formatPlainError(auditErr))
				notifySuperAdminsPlain(bot, fmt.Sprintf("⚠️ ABS 已执行 %s，但本地状态或成功审计失败，且本地失败审计写入失败。\n执行人：%d\n目标：%d\n本地错误：%s\n审计错误：%s\n请立即人工核查。", actionText, userID, tgtID, formatPlainError(err), formatPlainError(auditErr)))
				replyText(bot, chatID, "⚠️ ABS 状态已更新，但本地状态和失败审计写入均异常，已通知超级管理员人工核查。")
			} else {
				replyText(bot, chatID, "⚠️ ABS 状态已更新，但本地状态或审计写入失败，请管理员手动核查。")
			}
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("✅ **%s 成功！** 用户 `%d` 的服务端权限已同步更新。", actionText, tgtID))

		clearSession(userID)

	case "WAITING_FORCE_DELETE_USER":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			replyText(bot, chatID, "❌ 格式错误，请输入纯数字 TG ID：")
			return
		}

		if tgtID == userID {
			replyText(bot, chatID, "❌ 警告：系统禁止执行物理自毁操作！")
			clearSession(userID)
			return
		}

		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 警告：免死金牌生效，无法抹除超级管理员！")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 本地数据库查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 物理删号目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		if tUser.Role == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已经是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		session.SetTemp("delete_tgt_uid", strconv.FormatInt(tgtID, 10))
		session.SetTemp("delete_tgt_username", tUser.Username)
		session.SetStep("WAITING_FORCE_DELETE_REASON")

		replyText(bot, chatID, fmt.Sprintf("📝 即将物理删除用户 `%s` / `%d`。\n请输入删除原因，%s：", escapeMarkdown(tUser.Username), tgtID, adminReasonRequirementText))
		UserSessions.Store(userID, session)

	case "WAITING_FORCE_DELETE_REASON":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		reason, ok := validateAdminReason(text)
		if !ok {
			replyText(bot, chatID, adminReasonInvalidText)
			return
		}

		tgtID := session.GetTemp("delete_tgt_uid")
		username := session.GetTemp("delete_tgt_username")

		session.SetTemp("delete_reason", reason)
		session.SetStep("WAITING_CONFIRM_FORCE_DELETE")

		replyText(bot, chatID, fmt.Sprintf(
			"🚨 **物理删号二次确认**\n\n目标用户：`%s`\nTG ID：`%s`\n原因：`%s`\n\n此操作将删除 ABS 账号和本地资产，不可逆。\n确认执行请回复：`确认删除`\n取消请回复：`取消`",
			escapeMarkdown(username),
			tgtID,
			escapeMarkdown(reason),
		))
		UserSessions.Store(userID, session)

	case "WAITING_CONFIRM_FORCE_DELETE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}

		if text != "确认删除" {
			replyText(bot, chatID, "🛑 已取消物理删号。")
			clearSession(userID)
			return
		}

		tgtID, err := strconv.ParseInt(session.GetTemp("delete_tgt_uid"), 10, 64)
		if err != nil || tgtID == 0 {
			replyText(bot, chatID, "❌ 删号会话状态异常，已中止。请重新发起物理删号流程。")
			clearSession(userID)
			return
		}
		reason := session.GetTemp("delete_reason")

		if tgtID == userID {
			replyText(bot, chatID, "❌ 警告：系统禁止执行物理自毁操作！")
			clearSession(userID)
			return
		}

		if getUserRole(tgtID) == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		var tUser User
		if err := DB.Where("telegram_id = ?", tgtID).First(&tUser).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			replyText(bot, chatID, "❌ 本地数据库查无此人。")
			clearSession(userID)
			return
		} else if err != nil {
			log.Printf("⚠️ 物理删号确认目标用户读取失败: actor=%d target=%d err=%s", userID, tgtID, formatPlainError(err))
			replyText(bot, chatID, "❌ 目标用户读取失败，请稍后重试。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, "⏳ 正在执行跨端抹除协议...")

		if tUser.Role == "super_admin" {
			replyText(bot, chatID, "❌ 目标用户已经是超级管理员，操作中止。")
			clearSession(userID)
			return
		}

		if tUser.AbsUserID != "" {
			apiErr := absClient.DeleteUser(tUser.AbsUserID)
			if apiErr != nil && !IsAbsNotFoundError(apiErr) {
				replyText(bot, chatID, fmt.Sprintf("❌ **删号行动中止！**\nABS 服务端无响应或拒绝删除: %s\n\n⚠️ 为防止该用户变为不受监管的账号，系统已保留其本地档案。请检查 ABS 服务器状态后重试！", formatMarkdownError(apiErr)))
				clearSession(userID)
				return
			}
		}

		if err := deleteLocalUserWithAudit(userID, tgtID, tUser.AbsUserID, "FORCE_DELETE_USER", func(deleted User) string {
			return fmt.Sprintf("物理删除用户 %s(%d)，ABS ID=%s，原因：%s", formatPlainValue(deleted.Username), tgtID, formatPlainValue(deleted.AbsUserID), formatPlainValue(reason))
		}); err != nil {
			log.Printf("⚠️ ABS 已删除，但本地用户删除或审计写入失败: user=%d abs=%s err=%s", tgtID, formatPlainValue(tUser.AbsUserID), formatPlainError(err))
			replyText(bot, chatID, "⚠️ ABS 账号已删除，但本地档案或审计写入失败，请立即人工核查数据库。")
			clearSession(userID)
			return
		}

		replyText(bot, chatID, fmt.Sprintf("🗑️ **处决完成**。用户 `%d` 的 ABS 账号及本地资产已被删除。", tgtID))

		clearSession(userID)

	case "WAITING_QUERY_CODE":
		if !requireSuperAdmin(bot, chatID, userID) {
			clearSession(userID)
			return
		}
		queryCode := strings.TrimSpace(text)
		queryHash := hashSensitiveToken(queryCode)
		if queryHash == "" {
			replyText(bot, chatID, "❌ 系统安全密钥未配置，请联系管理员。")
			clearSession(userID)
			return
		}

		var foundType string
		var isUsed bool
		var usedByID int64
		displayCode := maskSecret(queryCode)

		var invCode InviteCode
		inviteErr := DB.Where("code_hash = ?", queryHash).First(&invCode).Error
		if inviteErr == nil {
			foundType = "🎫 专属邀请码"
			isUsed = invCode.IsUsed
			usedByID = invCode.UsedByID
			if invCode.CodePreview != "" {
				displayCode = invCode.CodePreview
			}
		} else if !errors.Is(inviteErr, gorm.ErrRecordNotFound) {
			log.Printf("⚠️ 卡密溯源邀请码读取失败: user=%d err=%s", userID, formatPlainError(inviteErr))
			replyText(bot, chatID, "❌ 卡密查询失败，请稍后重试。")
			clearSession(userID)
			return
		} else {
			var renCode RenewCode
			renewErr := DB.Where("code_hash = ?", queryHash).First(&renCode).Error
			if renewErr == nil {
				foundType = fmt.Sprintf("💳 %d天续期卡", renCode.Days)
				isUsed = renCode.IsUsed
				usedByID = renCode.UsedByID
				if renCode.CodePreview != "" {
					displayCode = renCode.CodePreview
				}
			} else if errors.Is(renewErr, gorm.ErrRecordNotFound) {
				replyText(bot, chatID, "❌ 查无此码。请确认卡密输入正确，且是由本系统生成的。")
				clearSession(userID)
				return
			} else {
				log.Printf("⚠️ 卡密溯源续期卡读取失败: user=%d err=%s", userID, formatPlainError(renewErr))
				replyText(bot, chatID, "❌ 卡密查询失败，请稍后重试。")
				clearSession(userID)
				return
			}
		}

		statusText := "🟢 **未使用** (可正常分发或使用)"
		useInfo := ""

		if isUsed {
			statusText = "🔴 **已使用/已核销**"
			var user User
			userErr := DB.Where("telegram_id = ?", usedByID).First(&user).Error
			if userErr == nil {
				safeName := escapeMarkdown(user.Username)
				useInfo = fmt.Sprintf("\n👤 **使用者名称**: `%s`\n🆔 **使用者 TG ID**: `%d`", safeName, usedByID)
			} else if errors.Is(userErr, gorm.ErrRecordNotFound) {
				useInfo = fmt.Sprintf("\n👤 **使用者 TG ID**: `%d` (该用户可能已注销或退群)", usedByID)
			} else {
				log.Printf("⚠️ 卡密溯源使用者档案读取失败: admin=%d target=%d err=%s", userID, usedByID, formatPlainError(userErr))
				useInfo = fmt.Sprintf("\n👤 **使用者 TG ID**: `%d`\n👤 **使用者档案**: `读取失败`", usedByID)
			}
		}

		info := fmt.Sprintf("🔍 **卡密溯源档案**\n\n"+
			"🏷️ **资产类型**: %s\n"+
			"🔑 **卡密内容**: `%s`\n"+
			"🛡️ **当前状态**: %s%s",
			foundType, displayCode, statusText, useInfo)

		writeAuditLog(userID, "QUERY_CODE", foundType, fmt.Sprintf("查询卡密状态，类型：%s，是否使用：%t，使用者：%d", foundType, isUsed, usedByID))
		replyText(bot, chatID, info)
		clearSession(userID)
	}
}
