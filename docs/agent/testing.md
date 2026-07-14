# 单元测试套件（纯逻辑回归）
## 2026-07-12 积分抽奖人工领奖与自定义卡密守护

`lottery_logic_test.go` 新增人工奖品、自定义卡密解析与非法输入回归，守护每行一份卡密、活动内重复卡密拒绝、卡密预览脱敏、AES-GCM 独立域密钥加解密、数据库仅保存密文、创建后清空内存规格中的卡密明文，以及暗号领取时人工奖品只提示联系管理员、自定义卡密从匹配奖品密文解密后私发。

## 2026-07-12 开放注册、服务补偿与听书时长口径守护

`service_operations_logic_test.go` 覆盖积分红包份数 `3-100`、开放注册人数/时长边界、服务补偿有效期规则、ABS 官方累计时长优先级和活跃会话墙钟补算条件，并通过菜单回归守护确保 `开放注册`、`用户补偿` 仅出现在超级管理员【系统配置】且回调保持超级管理员权限；同时检查开放注册名额事务预占/释放、补偿积分流水/幂等状态/审计/后台入口，以及首次观察活跃会话不得从 `StartedAt` 倒推。

## 2026-06-28 药园回收经济平衡

`garden_logic_test.go` 扩展 `TestGardenMarketPriceIsStrictlyAboveBaseForPurchasableHerbs`，并新增 `TestGardenHerbEconomyPriceTable`，用于守护药园普通回收按种子成本约 90% / 期望产量折算；急收价格和额度使用分种子经济表：凝露草 5-6 分且额度 15，青灵叶 10-11 且额度 12，赤阳花 20-22 且额度 8，月华藤 40-43 且额度 5，玄参根 60-64 且额度 3，紫玉芝 140-148 且额度固定 1。`TestGardenZiyuzhiOnlyProfitsOnUrgentMarket` 同步守护紫玉芝不再使用“种子成本 +10”保底。

## 2026-06-28 交易行售罄到期提醒回归

`marketplace_logic_test.go` 新增 `TestMarketplacePurchaseClosesSoldOutListingInTransaction` 和 `TestMarketplaceExpirySkipsSoldOutSellerNotice`，用于守护交易行购买事务在最后一件库存售出后同步把商品状态改为 `closed`；历史遗留的零库存 `active` 商品被到期巡检收口时只能静默关闭并记录日志，不得再给卖家发送 48 小时到期下架提醒。

## 2026-06-24 宗门秘境开启事务结果发布补充

`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmOpenReturnValuesOnlyAfterSuccess`，用于守护宗门秘境开启时，秘境编号、宗门名和本周开启次数等成功回执/实时榜结果只能在扣宗门资金、创建秘境事件和资金消耗流水全部提交成功后发布；事务失败或提交失败时不得发送“秘境已开启”回执或启动实时榜。

## 2026-06-24 宗门周目标结算事务返回值补充

`sect_weekly_task_logic_test.go` 新增 `TestSectWeeklyTaskSettlementReturnValueOnlyAfterSuccess`，用于守护宗门周目标结算只有在结算记录、宗门资金/声望写回和两条贡献流水全部成功提交后，才返回结算结果；事务失败或提交失败时必须返回空结果和错误，避免调用方用未提交的奖励快照发送成功回执。

## 2026-07-02 交易行系统卡密流转守护

`marketplace_logic_test.go` 新增 `TestMarketplaceSecretListingsRequireBotIssuedCodes` 和 `TestMarketplacePurchaseInvalidVerifiedSecretQuarantinesListing`，用于守护交易行自由卡密仅允许 Bot 生成且尚未使用的邀请码或续期卡；未匹配系统卡密表的输入不得作为三方卡密上架，最终购买事务复核卡密失效或来源异常时必须中止交易，并在事务回滚后关闭异常卡密、暂停商品为 `review` 待核查。

## 2026-07-02 赛马取消系统补贴守护

`race_logic_test.go` 新增 `TestRaceUsesPlayerPoolWithoutSystemSubsidy`，用于守护赛马不得再保留系统补贴、庄家配资或总奖池额外注入口径；有中奖者时最终奖池必须等于玩家总筹码 `userPool`，押中冠军者按本金比例瓜分玩家奖池，瓜分余数继续进入天道奖池。

## 2026-06-24 赛马/骰子退款事务返回值补充

`race_logic_test.go` 新增 `TestRaceAndDiceRefundReturnValuesOnlyAfterSuccess`，用于守护赛马和骰子异常退款只有在下注状态抢占、积分返还和退款流水全部成功提交后，才返回退款人数和退款积分；事务失败或提交失败时必须返回 `0,0` 和错误，避免启动恢复或协程异常处理误用未提交事务中的中间退款结果。

## 2026-07-02 推牌九小额坐庄玩法守护

`pai_gow_logic_test.go` 新增推牌九规则和源码守护：牌九开放时间固定为 `18:00-19:55`，骰子让出该时段并调整为 `22:05-次日17:55`；牌九下注口令为 `押 1` 到 `押 5`，每人每局一次，60 秒下注、1 分钟冷却、最多 20 人；牌点按 A=1、2-9 按牌面、10/J/Q/K=0，两张相加取个位，庄闲同点庄赢。源码守护要求牌九下注、退款、中奖流水齐全，启动时扫描未结算牌九下注并自动退款，开奖从 DB active 快照按下注时间发牌，开奖结果使用 `sendAutoDelete` 参与定时删除且不置顶。

## 2026-06-24 注册邀请码预占事务返回值补充

`registration_expiry_logic_test.go` 新增 `TestRegistrationInviteReservationReturnValueOnlyAfterSuccess`，用于守护注册邀请码预占只有在条件更新 `InviteCode.is_used/used_by_id` 和 `RESERVE_INVITE_CODE` 审计全部成功提交后，才返回预占的邀请码记录；事务失败或提交失败时必须返回空记录和错误，避免调用方拿到未提交的邀请码预占状态后继续 ABS 开户。

## 2026-06-24 高危管理员状态事务返回值补充

`admin_input_logic_test.go` 新增 `TestAdminMutationStatusReturnValuesOnlyAfterSuccess`，用于守护 `授权管理员`、`设置白名单` 和 `模拟过期` 的事务状态只在读取、条件写入和审计全部成功提交后返回给调用方；事务失败或提交失败时必须返回零值状态和错误，`模拟过期` 还必须清空过期时间，避免高危回执误用未提交事务中的中间状态。

## 2026-06-24 喇叭与 GitHub 福利配置事务返回值补充

`sect_horn_logic_test.go` 新增 `TestSectHornCreateReturnValueOnlyAfterTransactionSuccess`，用于守护宗门/世界喇叭只有在扣积分、广播主记录和投递明细全部成功提交后，才返回 `HornID`、消耗和收件人数等创建结果；事务失败或提交失败时必须返回零值和错误，避免启动未提交喇叭的投递。`github_benefit_test.go` 新增 `TestGithubBenefitConfigReturnValuesOnlyAfterSuccess`，用于守护 GitHub 福利开启状态和名额配置只有在 `SystemConfig` 写入和审计成功提交后，才返回旧开启状态或旧名额。

## 2026-06-24 管理员配置事务返回值补充

`config_logic_test.go` 新增 `TestAdminConfigMutationReturnValuesOnlyAfterSuccess`，并扩展 `TestSetConfigIntWithAuditUsesCheckedOldValue`，用于守护邀请码/续期卡价格配置和线路配置写入只有在 `SystemConfig` upsert 与对应审计全部成功提交后，才返回旧价格或旧/新线路长度；事务失败或提交失败时必须返回零值和错误，避免管理员回执或后续日志误用未提交事务中的中间配置状态。

## 2026-06-24 GitHub 福利与邀请拉新事务返回值补充

`github_benefit_test.go` 新增 `TestGithubBenefitTransactionalReturnValuesOnlyAfterSuccess`，用于守护 GitHub 福利校验码 claim、剩余名额和最终卡密奖励只能在事务成功提交后返回；事务失败或提交失败时必须返回零值和错误，避免回滚后的卡密、名额或 claim 被调用方发送或展示。`referral_logic_test.go` 新增 `TestReferralTransactionalReturnValuesOnlyAfterSuccess`，用于守护个人邀请链接、试用转正有效期、新人任务延期和邀请者奖励只在事务成功后发布；失败时必须清空链接、有效期、奖励积分和发奖标记，未完成任务路径仍可保留已读取的听书进度秒数。

## 2026-06-24 宗门抽奖参与事务返回值补充

`sect_lottery_logic_test.go` 新增 `TestSectLotteryJoinTransactionalReturnValuesOnlyAfterSuccess`，用于守护 `joinSectLottery` 只有在报名记录创建、参与人数回写和满人开奖判断全部成功提交后，才返回非零参与人数、目标人数和开奖标记；事务失败或提交失败时必须返回 `0,0,false` 和错误，避免回滚后的中间人数或开奖标记触发错误回执/自动开奖。

## 2026-06-24 积分抽奖参与事务返回值补充

`lottery_logic_test.go` 新增 `TestLotteryJoinTransactionalReturnValuesOnlyAfterSuccess`，用于守护 `joinLotteryActivity` 只有在参与记录、付费扣分和人数复核全部成功提交后，才返回非零参与人数和满人开奖标记；事务失败或提交失败时必须返回 `0,false` 和错误，避免回滚后的中间人数或开奖标记触发错误回执/自动开奖。

## 2026-06-24 交易行事务返回值提交后发布补充

`marketplace_logic_test.go` 新增 `TestMarketplaceTransactionalReturnValuesOnlyAfterSuccess`，用于守护自由卡密上架、背包物品上架和下架退款等交易行事务函数只能在事务成功后返回非零商品 ID 或退款数量；事务失败或提交失败时必须返回 0 和错误，避免回滚后的中间 ID 或库存退款数量被后续通知、会话或 Mini App/命令层误用。

## 2026-06-23 药园事务返回值提交后发布补充

`garden_logic_test.go` 新增 `TestGardenTransactionalReturnValuesOnlyAfterSuccess`，用于守护一键种植、一键收获、药草回收和炼丹等事务函数只能在事务成功后返回非零数量、积分或产物名；事务失败或提交失败时必须返回 0/空值和错误，避免回滚后的中间计算结果被 Mini App 或命令层误用。

## 2026-06-23 注册邀请码预校验读错分流补充

`registration_expiry_logic_test.go` 新增 `TestRegistrationInvitePrecheckDistinguishesReadErrors`，用于守护注册流程 `WAITING_REG_INVITE` 读取邀请码时必须区分 `gorm.ErrRecordNotFound` 与数据库错误；数据库错误必须记录脱敏日志并提示稍后重试，不得误报为邀请码无效。

## 2026-06-23 SystemConfig 部分唯一索引守护补充

`maintenance_logic_test.go` 新增 `TestSystemConfigKeyMigrationReplacesFullUniqueIndex`，用于守护 `SystemConfig.Key` 不再使用 GORM 全量 `uniqueIndex` tag，并由启动迁移替换 `idx_system_configs_key` 为 `system_configs(key) WHERE deleted_at IS NULL` 部分唯一索引。`notifier_logic_test.go` 新增 `TestSystemConfigConflictClausesTargetPartialUniqueIndex`，用于守护配置表 do-nothing helper 带 `TargetWhere deleted_at IS NULL`，同时要求配置 value upsert 显式使用相同冲突目标，避免 SQLite 部分唯一索引冲突目标不匹配；`lottery_logic_test.go` 同步要求奖池配置初始化复用该 helper。

## 2026-06-23 事件 ID 部分唯一索引守护补充

`sect_horn_logic_test.go` 新增 `TestSectHornBroadcastIDMigrationReplacesFullUniqueIndex`，`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmEventIDMigrationReplacesFullUniqueIndex`，`world_boss_logic_test.go` 新增 `TestWorldBossEventIDMigrationReplacesFullUniqueIndex`，用于守护宗门/世界喇叭、宗门秘境和世界 Boss 事件 ID 不再由 GORM tag 生成全量唯一索引，而由启动迁移替换为 `deleted_at IS NULL` 部分唯一索引。

## 2026-06-23 宗门名部分唯一索引守护补充

`sect_logic_test.go` 新增 `TestSectNameMigrationReplacesFullUniqueIndex`，用于守护 `Sect.Name` 不再使用 GORM 全量 `uniqueIndex` tag，并由启动迁移替换为 `sects(name) WHERE deleted_at IS NULL` 部分唯一索引，确保软删除历史宗门不会阻塞有效宗门名创建。

本项目针对**资产与安全关键的纯逻辑函数**逐步建立回归测试套件，用于在不连接数据库、不调用 Telegram/ABS 的前提下，锁定核心计算与校验规则，防止后续改动悄悄改变经济、伤害、奖励、时区或安全语义。

## 2026-06-23 背包库存部分唯一索引守护补充

`garden_logic_test.go` 新增 `TestInventoryQuantityUpsertTargetsPartialUniqueIndex` 和 `TestInventoryMigrationCreatesPartialUniqueIndex`，用于守护共享库存发放 helper `inventoryQuantityUpsertClause` 必须匹配 `inventories(user_id, item_name) WHERE deleted_at IS NULL` 部分唯一索引；启动迁移会检测旧库同名全量唯一索引，必要时仅重建索引定义，不删除业务数据，避免历史软删除库存行吸收药园、抽奖、交易行、秘境和商城等发货 upsert，导致资产已提交但正常背包查询看不到新库存。

## 2026-06-23 骰子每日净盈利部分唯一索引守护补充

`race_logic_test.go` 新增 `TestDiceDailyProfitOnConflictTargetsPartialUniqueIndex` 和 `TestDiceDailyProfitMigrationCreatesPartialUniqueIndex`，用于守护骰子每日净盈利 upsert target 必须匹配 `dice_daily_profits(user_id, day_key) WHERE deleted_at IS NULL` 部分唯一索引；启动迁移会检测旧库同名全量唯一索引，必要时仅重建索引定义，不删除业务数据，避免历史软删除统计行吸收 upsert 后让正常查询看不到当日净盈利，导致每日盈利上限失真。

## 2026-06-23 赛马/骰子结算 DB 快照守护补充

`race_logic_test.go` 新增 `TestRaceAndDiceSettlementSnapshotsUseActiveDatabaseBets`，用于守护赛马和骰子开奖结算必须从数据库重读本局 `active` 下注作为资产结算快照；内存下注池只用于局面控制和诊断对照，若内存池与数据库池不一致必须记录脱敏日志并以数据库 active 下注继续结算，避免已扣分落库但未进入内存 map 的下注被漏结算或漏退款。

## 2026-06-23 赛马/骰子中奖分支结算命中数守护补充

`race_logic_test.go` 扩展 `TestRaceWinningSettlementChecksLoserRowsAffected` 并新增 `TestDiceWinningSettlementChecksWinnerAndLoserRowsAffected`，用于守护赛马和骰子有中奖者时，中奖下注与未中奖下注都必须按 DB 快照人数完成状态抢占；赢家或输家命中数不一致时必须回滚结算，骰子输家还必须写入每日净盈利亏损统计，避免部分 active 下注遗留后被启动恢复错误退款。

## 2026-06-23 抽奖公告消息追踪回写守护补充

`lottery_logic_test.go` 新增 `TestLotteryAnnouncementMessageIDUpdatesCheckRowsAffected`，用于守护积分抽奖介绍公告和开奖结果公告在 Telegram 发送、置顶后，回写 `intro_message_id` / `result_message_id` 与置顶状态时必须检查数据库错误和 `RowsAffected`；0 行命中必须记录诊断日志，避免公告已经发出但消息 ID 未落库，导致兑奖截止后的取消置顶任务失去追踪目标。

## 2026-06-23 抽奖领奖锁读取失败 Fail Closed 补充

`lottery_logic_test.go` 新增 `TestLotteryClaimLockReadErrorsFailClosed`，用于守护积分抽奖领奖暗号的安全锁读取必须区分 `gorm.ErrRecordNotFound` 与数据库错误；只有确实没有锁记录才能继续处理，锁表读取失败必须记录脱敏日志并临时拒绝受理领奖，避免错误暗号 5 次锁定后因数据库异常被当成无锁而继续探测。

## 2026-06-23 播放异常每日统计读取失败分流补充

`listening_abuse_logic_test.go` 新增 `TestListeningAbuseDailyStatReadErrorsAreLoggedAndSkipped`，用于守护播放异常风控读取当天和前一天 `DailyListeningStat` 时必须区分 `gorm.ErrRecordNotFound` 与数据库错误；只有确实没有统计记录才能按无统计处理，数据库读取失败必须记录脱敏日志并跳过该用户本轮判断，避免统计表异常被当成 0 小时后漏告警或漏冻结。

同一测试同步守护实时会话补偿不得直接进入播放异常风控：`provisional` / `abs_live_session` 数据必须跳过，`mixed` 数据只允许使用 `official_raw_seconds`。

## 2026-06-23 启动迁移诊断可读性守护补充

`maintenance_logic_test.go` 扩展 `TestMigrationAppliedReadErrorsStopStartup` 与 `TestDBStartupMigrationLogsUseFormattedErrors`，用于守护数据库启动迁移、重复数据预检查、宗门科技/秘境唯一索引、交易行唯一索引、敏感码迁移和安全锁迁移的失败日志必须保留可读 ASCII 诊断文本，并继续使用 `formatPlainError` / `formatPlainValue` 规范化动态错误和 SQL。启动期诊断不得回退为历史乱码，避免生产库索引或敏感数据迁移失败时无法快速定位阻断点。

## 2026-06-23 每日听书统计部分唯一索引守护补充

`daily_listening_sync_test.go` 新增 `TestDailyListeningStatOnConflictTargetsPartialUniqueIndex` 和 `TestDailyListeningStatsMigrationCreatesPartialUniqueIndex`，用于守护 `dailyListeningStatOnConflict` 的 SQLite upsert target 必须匹配 `daily_listening_stats(user_id, day_key) WHERE deleted_at IS NULL` 部分唯一索引；迁移会检测旧库中同名全量唯一索引，必要时仅重建索引为软删除兼容的部分唯一索引，避免 upsert target 无法匹配唯一约束。

`daily_listening_live_test.go` 新增活跃听书会话补偿测试，用于守护北京时间跨日切分、ABS `/api/sessions` 常见 payload 解析、播放位置增量墙钟上限、播放中但位置停滞时的短窗口墙钟 fallback，以及 `AbsLiveListeningCheckpoint(user_id, session_key, item_key) WHERE deleted_at IS NULL` 的 upsert 冲突目标。

同一测试还守护遗留 `sect_listening_daily_progresses(user_id, day_key)` 唯一索引必须按 `deleted_at IS NULL` 创建；若旧库已存在同名全量唯一索引，启动迁移只重建索引定义，不删除业务数据，避免软删除历史缓存挡住同日统计重建。

该组迁移守卫同时要求启动失败日志使用可读诊断文本和 `formatPlainError`，避免索引迁移失败时只能看到历史乱码，影响生产排障。

## 2026-06-23 日志审计格式控制符清理守护补充

`business_errors_logic_test.go` 新增 `TestFormatPlainValueRemovesFormatControlsBeforeRedaction`，用于守护 `formatPlainValue` / 日志审计可读化路径必须先剥离 bidi/格式控制符再脱敏 token、password 等敏感字段，并折叠换行、Unicode 行/段分隔符，避免日志和审计文本被不可见控制符混淆显示顺序或绕过敏感字段匹配。`abs_client_logic_test.go` 与 `github_benefit_test.go` 同步扩展第三方响应片段测试，守护 ABS/GitHub 外部错误文本走同一口径。

## 2026-06-23 药园 Mini App 根路径跳转响应头守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppRootRedirectUsesNoStoreHeaders`，用于守护药园 Mini App 根路径 `/` 跳转到 `/garden` 时也必须带 no-store、Pragma、Expires 和 nosniff 响应头，避免重定向响应绕过 Mini App 的统一缓存与 MIME 嗅探策略。

## 2026-06-23 药园 Mini App 404 响应守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppNotFoundIsJSON`，用于守护药园 Mini App 根路径未知子路径、未知静态资源和嵌入文件缺失时必须返回统一 JSON 错误、no-store 和 nosniff 响应头，避免默认 `http.NotFound` / `http.Error` 绕过 Mini App 的安全响应头。

## 2026-06-23 药园 Mini App 非法方法响应守护补充

`garden_logic_test.go` 扩展 `TestGardenMiniAppMethodNotAllowedIsJSON`，用于守护药园 Mini App 根路径、页面、静态资源和 API action/state 的非允许 HTTP 方法都必须返回统一 JSON 错误、`Allow` 响应头以及 no-store/nosniff 等安全响应头，避免裸 405 响应造成前端或代理侧行为不一致。

## 2026-06-23 药园 Mini App POST body 上限守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppPostBodySizeIsBoundedBeforeActionDecode`，用于守护药园 Mini App 认证包装层必须在所有 POST 资产动作进入 JSON 解码前使用 `http.MaxBytesReader` 和 `gardenMiniAppMaxBodyBytes` 限制请求体大小，避免异常大请求占用服务端资源。

## 2026-06-23 药园 Mini App state 布尔字段守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppStateBooleanValidationIsStrict`，用于守护药园 Mini App 前端 state 校验必须严格检查 `purchasable/urgent/sellable/unlocked/enough` 为布尔值，避免异常 API 响应或本地缓存借由 truthy 字符串误导购买、回收、炼丹和材料齐全状态。

## 2026-06-23 药园 Mini App state 数值边界守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppStateNumericValidationUsesIntegerBounds`，用于守护药园 Mini App 前端 state 校验必须按后端 JSON 合约检查积分、计数、地块编号、库存、价格、限购、材料数量和行情额度等字段为非负整数；地块编号还必须限制在 `1..maxGardenPlots`，避免异常 API 响应或本地缓存污染选择器、进度和资产展示。

## 2026-06-23 药园 Mini App 地块状态枚举守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppPlotStatusValidationIsEnumerated`，用于守护药园 Mini App 前端 state 校验只接受 `empty/growing/ready` 三种地块状态，避免异常 API 响应或本地缓存把任意字符串写入 HTML class/data 属性。

## 2026-06-23 药园 Mini App 地块概览 HTML 转义守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppFarmMapSelectedTextIsEscaped`，用于守护药园 Mini App 地块概览区把当前选中地块文本写入 `innerHTML` 前必须经过 `escapeHtml`，避免草药名等动态字段破坏前端 DOM。

## 2026-06-23 药园 Mini App 当前种子详情 HTML 转义守护补充

`garden_logic_test.go` 扩展 `TestGardenMiniAppSeedTimingTextsAreEscaped`，用于同时守护地块详情面板中当前选中种子的生长时长和产量文本写入 `innerHTML` 前必须经过 `escapeHtml`，避免不同渲染入口出现遗漏。

## 2026-06-23 药园 Mini App 种子文本 HTML 转义守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppSeedTimingTextsAreEscaped`，用于守护药园 Mini App 中后端 state 提供的种子生长时长和产量文本写入 `innerHTML` 前必须经过 `escapeHtml`，避免未来配置文本变化时破坏前端 DOM。

## 2026-06-23 药园 Mini App 药铺 HTML 转义守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppMarketGuideEscapesMatchedHerbName`，用于守护药园 Mini App 药铺引导区把 state 中的动态草药名写入 `innerHTML` 前必须经过 `escapeHtml`，避免后端配置或历史缓存中的异常名称破坏前端 DOM。

## 2026-06-23 药园 Mini App state 空列表 JSON 合约守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppStateListsEncodeAsArrays` 和 `TestGardenMiniAppBuildStateInitializesListFields`，用于守护药园 Mini App 后端 state 响应中的 `plots/seeds/herbs/recipes/market` 以及丹方 `materials` 空列表必须编码为 `[]`，不得编码为 `null`，确保前端 state 结构校验和后端 JSON 合约一致。

## 2026-06-23 药园 Mini App state payload 结构守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppStatePayloadIsValidatedBeforeAssignment`，用于守护药园 Mini App 前端把 API 返回的 `payload.state` 写入 `app.state` 前必须经过 `requireGardenStatePayload` 校验；缺少核心对象、`plots/seeds/herbs/recipes/market` 数组，或数组元素关键字段类型异常时必须 fail closed，避免 `ok:true` 但结构异常的响应污染前端状态、DOM selector 或按钮 data 属性。

## 2026-06-23 药园 Mini App API 响应格式守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppAPIRejectsInvalidJSONAndMissingOK`，用于守护药园 Mini App 前端 API 层必须显式拒绝非 JSON 响应和缺少 `ok:true` 的响应，不得把解析失败吞成空对象后继续按成功状态渲染。

## 2026-06-23 药园 Mini App 本地缓存访问守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppCacheUsesSafeLocalStorageAccessor`，用于守护药园 Mini App 保存或读取本地快照前必须通过 `gardenLocalStorage()` 安全获取 `window.localStorage`，保存前还必须确认 state 结构有效，避免部分内嵌浏览器或隐私策略下 storage getter 抛异常导致药园前端流程中断，也避免无效园况落入本地缓存。

## 2026-06-23 药园 Mini App 离线缓存时间推进守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppCacheNormalizesAgedSnapshot`，用于守护药园 Mini App 读取本地快照时必须调用 `normalizeCachedGardenState`，按缓存年龄推进成熟倒计时和可收获状态，并在规范化后复用 state payload 结构校验；旧坏缓存不得进入离线视图。

## 2026-06-23 药园 Mini App HTTP 超时守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppServerHasBoundedTimeouts`，用于守护药园 Mini App HTTP server 必须同时设置 `ReadHeaderTimeout`、`ReadTimeout`、`WriteTimeout` 和 `IdleTimeout`，避免慢速请求、响应卡顿或空闲连接长期占用服务端资源。

## 2026-06-23 药园 Mini App init data 长度守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppInitDataRejectsOversizedHeader` 和 `TestGardenMiniAppInitDataLengthGuardRunsBeforeParse`，用于守护药园 Mini App 认证必须在 URL 解析、排序和 HMAC 计算前拒绝超长 Telegram init data，并保留 `INIT_DATA_TOO_LARGE` 诊断错误码。

## 2026-06-23 药园 Mini App 动作请求未知字段守护补充

`garden_logic_test.go` 扩展 `TestGardenMiniAppActionRequestDecodeRejectsTrailingJSON` 并新增 `TestGardenMiniAppActionRequestDecodeDisallowsUnknownFields`，用于守护药园 Mini App 资产动作 JSON 请求必须拒绝未知字段和尾随 JSON，避免前端协议漂移或异常请求带着零值继续进入种植、收获、购买、回收或炼丹等资产路径。

## 2026-07-10 药园 Mini App 农场布局重构守护

`garden_logic_test.go` 新增 `TestGardenMiniAppFarmLayoutSourceGuards`，用于守护 Mini App 顶部积分/灵田/可收资源栏、灵田两列网格、五入口底部码头、`320px` 小屏适配、`760px` 桌面画布上限、reduced-motion 收敛、本地 `?mock=1` 预览以及种子直达播种/药草直达药铺入口。通用空状态样式不得重新命中 `.farm-tile.empty` 并让空田跨列。

## 2026-06-23 ABS 密码更新 payload 错误处理补充

`abs_client_logic_test.go` 新增 `TestAbsPasswordUpdateChecksPayloadMarshalError`，用于守护 `UpdateAbsPassword` 构造 ABS PATCH 请求体时必须检查 `json.Marshal` 错误并返回明确失败，不得忽略编码错误后继续调用外部 API。

## 2026-06-23 ABS 客户端 HTTP Client 空值防护补充

`abs_client_logic_test.go` 新增 `TestAbsClientSendRequestDefaultsNilHTTPClient`，用于守护 `AbsClient.sendRequest` 在局部初始化或测试构造漏设 `HttpClient` 时使用带超时的本地默认 client 继续请求；该 fallback 不回写共享 `AbsClient` 字段，避免并发请求路径额外引入字段读写竞争。

## 2026-06-23 邀请链接读取错误分流补充

`referral_logic_test.go` 扩展邀请链接 `/start`、`validateReferralCodeForStart` 和 `createReferralTrialAccountInTx` 源码守护，用于确保读取 `ReferralCode` 或邀请者 `User` 时只有 `gorm.ErrRecordNotFound` 映射为链接无效或邀请资格不足；数据库错误必须记录脱敏日志并进入稍后重试/通用失败分支，不得误报为邀请链接无效。

## 2026-06-23 宗门秘境配置快照 Fail Closed 补充

`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmProfileSnapshotCheckedFailsClosed` 和 `TestSectSecretRealmJoinUsesCheckedProfileSnapshot`，并扩展实时刷新、结算源码守护，用于确保宗门秘境进入、实时刷新和结算必须使用开启时保存的配置快照；快照缺失或损坏时不得回退当前配置或默认配置继续参与、刷新奖励估算或发奖，结算必须回滚为 active 等待修复。

## 2026-06-23 听书报告 ABS 失败展示补充

`daily_listening_sync_test.go` 扩展 `TestPersonalReportUsesUnifiedDailyListeningSync`，用于守护听书报告读取 ABS 主统计时必须显式处理请求错误、非 200 状态和 JSON 解析错误，失败时提示稍后重试而不是误显示暂无收听；书籍完成/在听进度读取或解析失败时必须记录脱敏日志，并把数量显示为“读取失败”而不是 0。

## 2026-06-23 每日听书统计降级诊断补充

`daily_listening_sync_test.go` 扩展听书报告和宗门秘境快照源码守护，用于确保路径已拿到本次 ABS `days` 但写入 `daily_listening_stats` 失败时，必须记录 `formatPlainError` 脱敏诊断后再降级使用本次 ABS 数据；宗门秘境按 ABS ID 回查本地用户遇到数据库错误也必须记录日志，只有记录不存在才静默兼容降级。

## 2026-06-23 自动榜单统计失败诊断补充

`leaderboard_logic_test.go` 新增 `TestLeaderboardStatsFailuresAreLoggedAndAllFailureSkipsSend`，用于守护自动榜单逐用户读取 ABS 统计时必须分别累计请求失败、解析失败和成功数量；部分失败必须记录聚合脱敏日志，若本期所有用户统计都失败，必须跳过榜单发送，避免外部统计故障被误公告成无人上榜。

## 2026-06-23 明文备份清理失败诊断补充

`backup_logic_test.go` 扩展 `TestBackupCleanupLogUsesPlainValue`，用于守护历史明文 `.db` 备份清理失败时必须记录经过 `formatPlainValue` 规范化的路径和 `formatPlainError` 脱敏错误；删除操作继续使用原始路径，避免敏感明文文件残留时没有排障线索。

## 2026-06-23 交易行列表读取诊断补充

`marketplace_logic_test.go` 新增 `TestMarketplaceListReadErrorsAreLogged`，用于守护交易行公开列表、我的上架、我的购买和卖家商品订单列表读取失败时必须记录 `formatPlainError` 脱敏诊断；公开列表筛选 kind/keyword 进入日志前必须通过 `formatPlainValue` 规范化，避免数据库异常只提示用户而缺少生产排障上下文。

## 2026-06-23 宗门抽奖发奖重读诊断补充

`sect_lottery_logic_test.go` 扩展 `TestSectLotteryDeliveryStatusUpdatesCheckRowsAffected`，用于守护宗门抽奖私聊发奖前重读活动或奖品失败时必须记录 `formatPlainError` 脱敏诊断，避免后台发奖失败只累计失败数而缺少可排查原因。

## 2026-06-23 世界 Boss Markdown 名称展示守护补充

`world_boss_logic_test.go` 新增 `TestWorldBossMarkdownNamesAreEscaped`，用于守护世界 Boss 降临公告、参加回执、状态页、排行榜、结算公告和实时战榜中的 Boss 名称必须经过 `escapeMarkdown` 后进入 Markdown 消息；同时锁定用户可见伤害公式展示为 `实听小时 × 30 ×（1 + 修为加成 + 宗门科技）`，避免展示口径和实际结算倍率脱节。

## 2026-06-23 药铺回收配置校验顺序守护补充

`garden_logic_test.go` 新增 `TestGardenSellHerbValidatesSeedBeforePricing`，用于守护药铺回收 `gardenSellHerbQuantity` 必须先确认 seed 配置存在，再计算基础回收价；未知 seed 必须 fail closed，不得先进入价格公式后再合并判断，避免后续价格公式变化时异常配置影响资产路径。

`garden_logic_test.go` 新增 `TestGardenSellHerbRequiresPositiveCustomQuantity`，用于守护药铺回收只接受正整数自定义数量；后端、inline 回调和 Mini App 均不得保留 `sell-all`、`sellall` 或 `quantity=-1` 的全量回收入口。

## 2026-06-23 交易行背包上架库存读取分流补充

`marketplace_logic_test.go` 新增 `TestMarketplaceInventoryListingInventoryReadsDistinguishErrors`，用于守护背包物品上架流程选择物品和确认数量时读取 `Inventory` 必须区分 `gorm.ErrRecordNotFound` 与数据库错误；只有确实不存在或数量不足才能提示库存不足，数据库错误必须记录脱敏日志并提示乾坤袋/库存读取失败。

## 2026-06-23 抽奖领奖暗号复用守护补充

`lottery_logic_test.go` 新增 `TestClaimLotteryPrizeByCodeContinuesPastHandledWinners`，用于守护同一领奖暗号命中多个已开奖活动时，`claimLotteryPrizeByCode` 必须继续扫描后续活动中的待领奖中奖记录；已领取、非待领或过期记录只能作为备用提示，不能提前返回挡住仍有效的中奖资格。

## 2026-06-23 宗门秘境开启事务成员读取分流补充

`sect_secret_realm_logic_test.go` 扩展 `TestSectSecretRealmActiveQueriesDistinguishReadErrors`，用于守护确认开启宗门秘境事务内重读操作者 `SectMember` 时必须复用统一成员读取 helper；只有 `gorm.ErrRecordNotFound` 可映射为未加入宗门，数据库错误必须原样回滚到通用失败分支，避免宗门资金入口把 DB 故障误报为业务状态。

## 2026-06-23 宗门成员读取错误分流补充

`sect_logic_test.go` 新增 `TestSectMemberReadsDistinguishNotFoundFromDatabaseErrors`，用于守护我的宗门、成员分页、贡献排行、宗门商店、宗门改名、升级、职位任命、退出/踢人/转让、捐献、贡献兑换、七日续期、洞府闭关、每日任务领奖和周目标结算等入口读取 `SectMember` 时必须通过统一 helper 区分 `gorm.ErrRecordNotFound` 与数据库错误；只有确实不存在才能映射为未入宗或目标不在宗门，数据库错误必须提示读取失败或回滚到通用失败分支。

## 2026-06-23 药园面板库存读取失败守护补充

`garden_logic_test.go` 新增 `TestGardenPanelsUseCheckedInventoryReads`，用于守护灵田管理、选择种子、种子商店、草药背包、药铺回收和丹方炼丹面板必须使用带错误返回的灵田、种植记录、库存、限购、急收额度和丹方解锁读取；读取失败时显示“读取失败”或稍后再试，并不展示依赖该状态的收获、开垦、种植、购买、回收、参悟或炼丹按钮，避免数据库错误被折叠成空灵田、空库存或 0 持有后误导资产操作。

## 2026-06-23 高危审计资产发放统计守护补充

`maintenance_logic_test.go` 新增 `TestHighRiskAuditActionsCoverAssetIssuance`，用于守护 GitHub 福利发放邀请码/续期卡、宗门抽奖创建/开奖/取消、个人邀请体验注册/试用转正/新人任务领取 action 必须纳入 `highRiskAuditActionSet`；`_FAILED` 和 `_LOCAL_FAILED` 后缀也必须归一计入高危审计概览，避免资产、账号权益或卡密流转被统计低估。

## 2026-06-23 修仙默认配置种子创建行数守护补充

`cultivation_config_logic_test.go` 新增 `TestCultivationDefaultConfigCreatesCheckRowsAffected` 和 `TestCultivationConfigRefreshLogFormatsSource`，用于守护启动时补齐默认大境界、小境界和突破配置必须分别通过 `createDefaultCultivationRealmConfigIfMissingInTx`、`createDefaultCultivationMinorRealmConfigIfMissingInTx` 和 `createDefaultBreakthroughConfigIfMissingInTx` 检查数据库 `Error` 与 `RowsAffected`；0 行写入只能按默认配置已存在处理；修仙配置缓存刷新日志中的配置来源必须通过 `formatPlainValue` 规范化。

## 2026-06-23 宗门成员列表诊断日志净化补充

`sect_logic_test.go` 新增 `TestSectMemberListDiagnosticsAreReadable`，用于守护宗门成员列表分页消息编辑失败和成员列表发送失败日志必须保持可读 UTF-8，并继续使用 `formatTelegramSendError` 规范化 Telegram 返回错误。

## 2026-06-23 宗门职位变更诊断日志净化补充

`sect_logic_test.go` 扩展宗门成员删除、职位任命和宗主转让源码守护，要求踢出成员、任命职位和转让宗主失败日志保持可读 UTF-8，并继续使用 `formatPlainError` / `formatPlainValue` 规范化动态诊断字段。

## 2026-06-23 宗门资产路径诊断日志净化补充

`sect_logic_test.go` 扩展宗门捐献和贡献兑换声望源码守护，要求失败日志保持可读 UTF-8，并继续使用 `formatPlainError` 规范化数据库、事务或资产写入错误。

## 2026-06-23 宗门七日续期名额诊断日志净化

`sect_logic_test.go` 新增 `TestSectShopRenewClaimCountDiagnosticsAreReadable`，守护宗门七日续期名额统计失败日志必须保持可读 UTF-8，月份键使用 `formatPlainValue`，数据库错误使用 `formatPlainError`。
同轮扩展 `TestSectShopRenewReactivateFailureAuditsAreChecked`，要求七日续期到账后 ABS 解封失败和本地状态同步失败日志保持可读 UTF-8，并继续规范化 ABS ID 与错误。

## 2026-06-23 状态追踪和安全锁创建行数守护补充

`admin_input_logic_test.go` 新增 `TestBestEffortTraceCreatesCheckRowsAffected`，用于守护自动删除消息登记和求书工单 best-effort 日志写入必须检查数据库 `Error` 与 `RowsAffected`；辅助追踪记录失败只记录脱敏日志，日志中的 action 必须通过 `formatPlainValue` 规范化，0 行写入诊断文本必须保持可读 UTF-8，不改变已经完成的 Telegram 发送或工单状态。`TestSecurityAttemptUpdatesCheckRowsAffected` 扩展首次创建 `SecurityAttemptLock` 的源码守护，要求创建失败撞唯一索引时继续重读累加，0 行创建必须返回错误，避免安全码或抽奖暗号失败次数静默漏记。

## 2026-06-23 自动删除队列清理行数守护补充

`maintenance_logic_test.go` 扩展 `TestMessageSweeperLogsQueueAndDeleteErrors`，用于守护自动删除消息队列清理 `AutoDeleteMsg` 时必须检查数据库删除错误和 `RowsAffected`；只有 Telegram 删除成功或终态错误后才允许清理记录，数据库删除未命中必须记录脱敏日志且诊断文本保持可读 UTF-8。

## 2026-06-23 启动迁移版本标记行数守护补充

`maintenance_logic_test.go` 扩展 `TestMigrationAppliedReadErrorsStopStartup`，用于守护一次性迁移成功后写入 `SchemaMigration` 版本记录必须检查数据库 `Error` 与 `RowsAffected`；版本标记 0 行写入必须阻止启动，避免迁移动作已执行但版本表未记录导致后续重复执行或误判迁移状态。

## 2026-06-23 每日听书统计 upsert 行数守护补充

`daily_listening_sync_test.go` 新增 `TestDailyListeningStatsRecordChecksRowsAffected` 和 `TestDailyListeningSyncUsesFreshPersistedStatsOnly`，用于守护 ABS `listening-stats.days` 写入 `DailyListeningStat` 时必须检查批量 upsert 的 `Error` 与 `RowsAffected`；主动刷新路径遇到本地持久化失败不得计作成功，听书报告和宗门秘境只在本地统计写入成功后才从每日统计表重算，避免旧缓存覆盖本次 ABS 结果。

## 2026-06-23 每日净修为命令读取错误分流补充

`daily_listening_sync_test.go` 新增 `TestDailyListeningCommandsDistinguishReadErrors`，用于守护 `刷新宗门今日净修为` 和 `查看每日净修为 用户ID [YYYY-MM-DD]` 必须区分 `gorm.ErrRecordNotFound` 与数据库读取错误；DB 错误需要记录脱敏日志并提示稍后重试，不得误报为未加入宗门或无记录。

## 2026-06-23 宗门科技读取错误分流补充

`sect_logic_test.go` 扩展 `TestSectTechnologyPanelAndUpgradeCheckLevelReadErrors`，用于守护宗门科技面板、升级确认和确认升级执行读取成员档案或宗门档案时必须区分 `gorm.ErrRecordNotFound` 与数据库错误；DB 错误需要记录脱敏日志并提示稍后重试，确认升级事务内非未找到错误不得映射为未入宗，避免权限和资产入口误导操作者。

## 2026-06-23 每日听书与洞府诊断日志净化补充

`daily_listening_sync_test.go` 扩展每日听书统计写入、ABS 读取/解析和宗门成员批量刷新名单读取源码守护，要求失败日志保持可读 UTF-8，并继续使用 `formatPlainValue` / `formatPlainError` 规范化 ABS ID 和错误。`sect_cave_logic_test.go` 新增 `TestSectCaveDailyListeningDiagnosticsAreReadable`，守护洞府闭关过期关闭、闭关记录读取、每日听书有效时长/原始秒数汇总失败日志保持可读 UTF-8。`sect_logic_test.go` 新增 `TestAwardSectContributionDiagnosticsAreReadable`，守护宗门贡献奖励失败日志保持可读 UTF-8，并规范化奖励原因和错误。

## 2026-06-23 宗门任务诊断日志和流水原因净化补充

`sect_logic_test.go` 扩展宗门升级、任务面板、每日任务领奖和周目标结算源码守护，要求失败日志保持可读 UTF-8，并继续规范化日期键、周键和错误。每日任务贡献流水 reason 必须保存可读中文“宗门每日任务奖励，完成 N 项”，避免资产追溯记录写入历史乱码。
同组守护覆盖宗门升级、每日任务领奖和周目标结算错误分支的用户可见提示，要求未入宗、权限不足、资源不足、已领取、未达成、已结算和通用失败提示保持清晰中文，不得回退为历史乱码。
`TestSectContributionLogReasonsAreReadable` 额外守护宗门捐献、贡献兑换宗门声望、宗门七日续期和听书增长奖励的贡献流水 reason 必须保存可读中文，不得写入历史乱码。

## 2026-06-23 宗门只读面板文案净化补充

`sect_logic_test.go` 新增 `TestSectReadOnlyPanelCopyIsReadable`，守护 `我的宗门`、`宗门排行`、`宗门成员` 和成员分页 callback 的未入宗、读取失败、洞府状态、排行榜标题、分页提示等用户可见文案保持清晰中文，不得回退为历史乱码。
`TestSectContributionRankCopyIsReadable` 守护宗门总贡献/本周贡献排行榜的未入宗、读取失败、空榜、标题、字段名和排行行文案保持清晰中文。

## 2026-06-23 宗门资产入口错误提示净化补充

`sect_logic_test.go` 扩展宗门捐献、贡献兑换宗门声望、贡献换积分关闭入口、踢出成员和宗主转让源码守护，要求参数错误、未入宗、积分/贡献不足、功能关闭和通用失败提示保持清晰中文，避免资产入口或权限操作展示历史乱码。

## 2026-06-23 宗门剩余乱码文案净化补充

`sect_logic_test.go` 新增 `TestSectCopyDoesNotContainMojibake`，用于守护 `sect.go` 不得残留典型 mojibake 标记，并锁定宗门成员空列表、职位任命、宗门商店、宗门七日续期、宗门任务和今日净修为日志等本轮恢复的清晰中文文案。

## 2026-06-23 宗门每日任务状态文案净化补充

`sect_logic_test.go` 扩展 `TestSectDailyTaskStatusesReturnErrors`，守护宗门每日任务状态、任务名称和周目标超额文本保持清晰中文，包括“今日签到”“今日净修为 +1 小时”“今日捐献 N 积分”“已完成/未完成”和“超额 +N%”。

## 2026-06-23 榜单置顶与奖池初始化行数守护补充

`leaderboard_logic_test.go` 扩展 `TestLeaderboardPinStateReadErrorsAreLogged`，用于守护自动榜单置顶消息 ID 写入 `SystemConfig` 时必须复用可检查行数的系统配置 helper；`lottery_logic_test.go` 扩展 `TestFusionPoolUpdateChecksRowsAffected`，用于守护天道奖池水位配置缺失初始化必须通过 `createFusionPoolConfigIfMissingInTx` 检查数据库 `Error` 与 `RowsAffected`，0 行写入只能按配置已存在处理。

## 2026-06-23 我的信息今日净修为读取失败守护补充

`daily_listening_sync_test.go` 新增 `TestMyInfoDailyListeningStatReadFailureDisplaysUnavailable`，用于守护今日每日听书缓存读取提供 checked helper 区分暂无记录和数据库错误；`我的信息` 展示今日净修为时，暂无缓存可显示 `0.00` 小时，数据库读取失败必须记录脱敏日志并显示“读取失败”，不得把异常折叠成 0 小时。

## 2026-06-23 修仙档案读取失败 nil 防护补充

`cultivation_logic_test.go` 新增 `TestCultivationNilCallersFailClosed`，用于守护突破预检、每日净修为同步、听书报告降级展示、听书报告境界变化公告、世界 Boss 修为刷新、历史补偿检查、吞服丹药成功回执、`我的信息` 和管理员 `查询用户` 等入口处理 `GetOrCreateCultivation` 返回 nil 的情况；读取失败时必须提示、记录或显示“读取失败”并 fail closed，不得继续解引用导致 panic，也不得把修仙档案异常误显示为 0 小时或未知境界。

## 2026-06-23 后台 SystemConfig 写入行数守护补充

`notifier_logic_test.go` 新增 `TestSystemConfigWritesCheckRowsAffected`，用于守护每日天道灵气收集抢占记录、事务内系统配置 upsert 和普通系统配置 upsert 都必须检查数据库 `Error` 与 `RowsAffected`；未实际写入必须返回错误并走既有告警路径，避免后台任务已执行但状态标记未落库。

## 2026-06-23 签到连续档案创建行数守护补充

`sign_in_logic_test.go` 扩展 `TestSignInCreateLogsCheckRowsAffected`，用于守护首次签到初始化 `SignInStreak` 时必须通过 `createSignInStreakInTx` 检查数据库 `Error` 与 `RowsAffected`；唯一冲突继续映射为并发签到重试，未实际写入不得继续发放签到积分。

## 2026-06-23 宗门秘境参与记录创建行数守护补充

`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmParticipantCreateChecksRowsAffected`，用于守护进入宗门秘境写入 `SectSecretRealmParticipant` 时必须通过 `createSectSecretRealmParticipantIfMissingInTx` 检查数据库 `Error` 与 `RowsAffected`；0 行写入只表示重复进入，必须保留首次进入基线。

## 2026-06-23 抽奖参与本地档案创建行数守护补充

`lottery_logic_test.go` 新增 `TestLotteryLocalUserCreateChecksRowsAffected`，用于守护积分抽奖参与事务为本地无档案用户补建 `User` 时必须通过 `createLotteryLocalUserIfMissingInTx` 检查数据库 `Error` 与 `RowsAffected`；并发下 0 行写入只表示档案已存在，不得回退为只检查 `.Error` 的直接创建。

## 2026-06-23 药园默认灵田创建行数守护补充

`garden_logic_test.go` 新增 `TestGardenInitialPlotCreateChecksRowsAffected`，用于守护药园达到炼气期后初始化第 1 块免费灵田时必须通过 `createGardenInitialPlotIfMissing` 检查数据库 `Error` 与 `RowsAffected`；并发下 0 行写入只表示默认灵田已存在，不得回退为只检查 `.Error` 的直接创建。

## 2026-06-23 修仙档案懒初始化行数守护补充

`cultivation_logic_test.go` 新增 `TestGetOrCreateCultivationCreateChecksRowsAffected`，用于守护 `GetOrCreateCultivation` 首次创建修仙档案时必须通过 `createCultivationIfMissing` 检查数据库 `Error` 与 `RowsAffected`；并发下 0 行写入必须重读既有档案，避免唯一索引竞态导致调用方拿不到修仙档案。

## 2026-06-23 药园限额记录创建行数守护补充

`garden_logic_test.go` 新增 `TestGardenLimitRecordsCreateChecksRowsAffected`，用于守护药园购种每日限购记录和药铺急收额度记录必须分别通过 `createGardenSeedPurchaseIfMissingInTx`、`createGardenHerbMarketSaleIfMissingInTx` 初始化，并检查数据库 `Error` 与 `RowsAffected`；0 行写入只表示记录已存在，后续仍必须通过条件更新原子占用限购或急收额度。

## 2026-06-23 共享卡密创建行数守护补充

`code_record_logic_test.go` 新增 `TestInviteAndRenewCodeRecordCreatesCheckRowsAffected`，用于守护邀请码和续期卡共享创建入口 `createInviteCodeRecord` / `createRenewCodeRecord` 必须检查数据库 `Error` 与 `RowsAffected`；若卡密记录未实际写入，必须让管理员生成、抽奖发奖或历史补偿事务回滚，避免明文卡密被发送但数据库无对应记录。

## 2026-06-23 注册本地档案创建行数守护补充

`registration_expiry_logic_test.go` 新增 `TestRegistrationNewUserCreateChecksRowsAffected`，用于守护注册流程首次创建本地 `User` 档案时必须通过 `createRegisteredUserInTx` 检查数据库 `Error` 与 `RowsAffected`；若本地档案未实际写入，必须触发 ABS 回滚分支，避免 ABS 已开户但本地安全档案缺失。

## 2026-06-23 幽灵钱包初始化行数守护补充

`registration_expiry_logic_test.go` 新增 `TestEnsureUserWalletCreateChecksRowsAffected` 和 `TestEnsureUserWalletReturnValueOnlyAfterSuccess`，用于守护盲盒等入口为未注册 Telegram 用户初始化幽灵钱包时必须检查 `User` 创建的数据库 `Error` 与 `RowsAffected`；唯一冲突继续重读既有档案，0 行写入不得继续进入后续扣分或资产流程；返回给调用方的本地用户档案和展示名只能在事务成功后发布，事务失败或提交失败时必须返回空档案和错误。

## 2026-06-23 播放异常记录创建行数守护补充

`listening_abuse_logic_test.go` 新增 `TestListeningAbuseRecordCreateChecksRowsAffected`，用于守护播放异常 warning/freeze 记录创建必须通过 `createListeningAbuseRecordInTx` 检查数据库 `Error` 与 `RowsAffected`；唯一冲突或重复 active 冻结导致 0 行写入时只能按幂等重复处理，不得继续写新增审计或发送新增通知。

## 2026-06-23 世界 Boss 参与记录创建行数守护补充

`world_boss_logic_test.go` 新增 `TestWorldBossParticipantCreateChecksRowsAffected`，用于守护 `参加Boss` 写入 `WorldBossParticipant` 时必须通过 `createWorldBossParticipantInTx` 检查数据库 `Error` 与 `RowsAffected`；重复参加导致 0 行写入时只能按幂等结果处理，并保留首次参加基线。

## 2026-06-23 交易行争议创建行数守护补充

`marketplace_logic_test.go` 新增 `TestMarketplaceDisputeCreateChecksRowsAffected`，用于守护买家提交交易争议时必须通过 `createMarketplaceDisputeInTx` 检查数据库 `Error` 与 `RowsAffected`；唯一冲突继续映射为已有处理中争议，未实际写入不得提示提交成功。

## 2026-06-23 交易行上架名称规则提示守护补充

`marketplace_logic_test.go` 新增 `TestMarketplaceNamePromptsReuseValidationRequirementText`，用于守护自由上架商品名和背包上架物品名提示必须复用 `marketplaceSecretListingNameRequirementText` / `marketplaceInventoryItemNameRequirementText`，并明确长度、换行、制表符、控制字符和 Unicode 行/段分隔符边界，避免用户可见提示与实际校验规则漂移。

## 2026-06-23 宗门抽奖提醒记录行数守护补充

`sect_lottery_logic_test.go` 新增 `TestSectLotteryReminderUpsertChecksRowsAffected`，用于守护宗门抽奖成员提醒状态写入 `SectLotteryReminder` 时必须通过 `upsertSectLotteryReminderRecord` 检查数据库 `Error` 与 `RowsAffected`；未实际写入需记录日志，避免补发去重状态静默丢失。

## 2026-06-23 宗门抽奖提醒去重读取失败守护补充

`sect_lottery_logic_test.go` 新增 `TestSectLotteryReminderDedupeReadFailureSkipsDelivery`，用于守护宗门抽奖成员提醒去重记录读取失败时必须返回错误并跳过本次投递；不得把数据库错误当成未提醒继续私聊，避免启动补扫或手动补发时重复打扰成员。

## 2026-06-23 宗门抽奖提醒投递状态写回失败守护补充

`sect_lottery_logic_test.go` 新增 `TestSectLotteryReminderDeliveryFailsWhenStateWriteFails`，用于守护宗门抽奖成员提醒私聊发送后必须检查 `SectLotteryReminder` 状态写回错误；成功状态未落库时投递结果不得统计为成功，并需要记录后续可能重复补发的诊断。

## 2026-06-23 宗门抽奖资格读取错误分流补充

`sect_lottery_logic_test.go` 新增 `TestSectLotteryEligibilityReadErrorsFailClosed`，用于守护宗门抽奖报名、创建者上下文、详情加载、用户资格校验和开奖候选复核必须区分记录不存在与数据库错误；开奖复核遇到数据库错误必须回滚开奖等待重试，不得把读取失败当成候选不符合资格后少发奖。

## 2026-06-23 交易行上架群提醒来源读取失败守护补充

`marketplace_logic_test.go` 扩展 `TestMarketplaceListingNoticeLogsSecretSourceReadErrors`，用于守护自由卡密上架群提醒补读 `secret_source` 失败时必须记录脱敏日志并跳过公告发送；不得把未知来源退化成“三方卡密 · 未校验”后继续发到群里。

## 2026-06-23 交易行商品读取错误分流补充

`marketplace_logic_test.go` 新增 `TestMarketplaceDetailAndBuyConfirmDistinguishReadErrors`，用于守护 `交易行详情` 和高价购买确认读取商品主记录时必须区分 `gorm.ErrRecordNotFound` 与数据库错误；DB 错误需要记录脱敏日志并提示商品状态读取失败，不得误报为商品不存在或已下架。

## 2026-06-23 交易行订单读取错误分流补充

`marketplace_logic_test.go` 新增 `TestMarketplaceOrderEntrypointsDistinguishReadErrors`，用于守护卖家 `交易行订单`、管理员 `查交易订单` 和买家 `举报订单` 读取商品或订单主记录时必须区分未找到与数据库错误；DB 错误需要记录脱敏日志并提示稍后重试，不得误报为商品或订单不存在。

`marketplace_logic_test.go` 新增 `TestMarketplaceManualCloseLogsUnexpectedErrors`，用于守护卖家手动下架商品遇到非业务错误时必须记录包含卖家 ID、商品 ID 和脱敏错误的诊断日志；不得只向用户返回泛化失败提示后静默丢失排障上下文。

## 2026-06-23 宗门喇叭完成回执读取失败诊断补充

`sect_horn_logic_test.go` 新增 `TestSectHornCompletionReceiptReadFailureLogsError`，用于守护宗门/世界喇叭完成状态写入后，重读广播准备发送发起人回执失败时必须记录 `formatPlainError` 脱敏诊断；不得静默返回导致“已完成但无回执”缺少排障线索。

## 2026-06-23 Boss 与秘境后台扫描查询失败诊断补充

`world_boss_logic_test.go` 新增 `TestWorldBossSchedulerQueryFailuresAreLogged`，`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmSchedulerQueryFailuresAreLogged`，用于守护世界 Boss 和宗门秘境后台扫描到期活动、进行中活动的查询失败必须记录 `formatPlainError` 脱敏诊断并跳过本轮；不得静默返回导致实时刷新或到期结算缺少排障线索。

## 2026-06-23 宗门秘境入口读取错误分流补充

`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmCommandReadErrorsAreDistinguished`，用于守护宗门秘境状态、开启、进入、手动结算、排行和明细入口读取宗门成员档案时必须区分未加入宗门与数据库错误；手动结算读取 active 秘境和明细读取参与记录也必须区分未找到与数据库错误，不得把 DB 故障误报为没有可结算秘境或无参与记录。

## 2026-06-23 账号绑定本地档案创建行数守护补充

`account_binding_logic_test.go` 新增 `TestBindLocalUserCreateChecksRowsAffected`，用于守护管理员绑定 ABS 账号并新建本地 `User` 档案时必须通过 `createBoundLocalUserInTx` 检查数据库 `Error` 与 `RowsAffected`；若本地档案未实际写入，必须回滚 `BIND_USER` 审计，避免绑定链路显示成功但本地档案缺失。

## 2026-06-23 宗门科技档案创建行数守护补充

`sect_logic_test.go` 新增 `TestSectTechnologyCreateChecksRowsAffected`，用于守护宗门科技首次升级创建 `SectTechnology` 档案时必须通过 `createSectTechnologyInTx` 检查数据库 `Error` 与 `RowsAffected`；若科技档案未实际写入，必须回滚宗门资金、声望扣减和升级日志，避免资源已扣但科技等级缺失。

## 2026-06-23 求书工单日志创建行数守护补充

`admin_input_logic_test.go` 新增 `TestBookRequestLogInTxChecksRowsAffected`，用于守护求书工单事务内处理日志 helper `createBookRequestLogInTx` 创建 `BookRequestLog` 时必须同时检查 `Error` 与 `RowsAffected`；若日志未实际写入必须回滚工单状态变更和审计，避免工单缺少处理追溯。

## 2026-07-02 求书已上传入库公告守护

`admin_input_logic_test.go` 新增 `TestBookRequestUploadedAnnouncementUsesPermanentGroupMessage` 和 `TestBookRequestUploadedAnnouncementCallbackIsHookedAfterFinish`，用于守护管理员标记求书已上传后，从每个 ABS 媒体库最近 5 条记录中筛选近 20 分钟内最新书籍，管理员确认后才发布 `NOTICE_GROUP_ID` 大群公告；群公告必须使用 `sendNoAutoDelete`，不得置顶或登记自动删除，封面读取失败或 Telegram 图片发送失败可降级纯文本。`book_request_announcement_logic_test.go` 守护确认按钮短 token、带下划线 itemID 解析和过期候选清理。

## 2026-06-23 求书工单主记录创建行数守护补充

`admin_input_logic_test.go` 扩展 `TestBookRequestStateTransitionsWriteLogAndAuditInTransaction`，用于守护用户提交求书时创建 `BookRequest` 主记录必须检查数据库 `Error` 与 `RowsAffected`；主记录、处理日志和 `CREATE_BOOK_REQUEST` 审计任一失败都必须回滚，避免通知管理员或用户提交成功但工单主记录未实际落库。

## 2026-06-23 审计日志创建行数守护补充

`maintenance_logic_test.go` 新增 `TestAuditLogCreateChecksRowsAffected`，用于守护全局审计写入入口 `writeAuditLogInTx` 创建 `AuditLog` 时必须同时检查 `Error` 与 `RowsAffected`；若审计记录未实际写入必须返回错误，让高危事务回滚或触发超管通知，避免不可逆操作缺少审计追溯。

## 2026-06-23 积分流水创建行数守护补充

`cultivation_logic_test.go` 新增 `TestApplyPointDeltaCreatesTransactionChecksRowsAffected`，用于守护全局积分变动入口 `applyPointDeltaInTx` 在完成用户余额条件更新后，创建 `PointTransaction` 必须同时检查 `Error` 与 `RowsAffected`；若流水未实际写入必须回滚余额变动，避免资产余额和流水追溯不一致。

## 2026-06-23 修仙突破记录行数守护补充

`cultivation_logic_test.go` 新增 `TestBreakthroughAttemptCreateChecksRowsAffected`，用于守护突破成功和失败分支必须通过 `createBreakthroughAttemptInTx` 写入 `BreakthroughAttempt`，并同时检查数据库 `Error` 与 `RowsAffected`；若突破记录未实际写入，必须回滚资源扣减、境界/失败次数更新、返还和调养费流水，避免资产或境界变化缺少追溯。

`cultivation_logic_test.go` 新增 `TestBreakthroughCultivationReadErrorsFailClosed`，用于守护突破事务读取 `Cultivation` 档案时只有 `gorm.ErrRecordNotFound` 能映射为未初始化档案；数据库错误必须回滚并进入通用失败分支。该测试同时守护突破成功贡献日志 reason 为可读 UTF-8 中文，避免历史误解码文本进入追溯日志。

## 2026-06-23 宗门秘境事件创建行数守护补充

`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmEventCreateChecksRowsAffected`，用于守护开启宗门秘境事务必须通过 `createSectSecretRealmEventInTx` 创建 `SectSecretRealmEvent`，并同时检查数据库 `Error` 与 `RowsAffected`；若秘境事件未实际写入，必须回滚宗门资金扣减和贡献日志，避免资金已扣但秘境不存在。

## 2026-06-23 宗门秘境开启宗门行触碰守护补充

`sect_secret_realm_logic_test.go` 扩展 `TestSectSecretRealmActiveQueriesDistinguishReadErrors`，用于守护开启宗门秘境事务在读取宗门后触碰宗门行时必须检查数据库 `Error` 与 `RowsAffected`；未命中必须回滚开启流程，避免宗门行状态异常时继续扣资金或创建秘境。

## 2026-06-23 宗门与成员创建行数守护补充

`sect_logic_test.go` 新增 `TestSectAndMemberCreatesCheckRowsAffected`，用于守护创建宗门和加入宗门事务必须通过 `createSectInTx` / `createSectMemberInTx` 写入 `Sect` 与 `SectMember`，并同时检查数据库 `Error` 与 `RowsAffected`；若宗门或成员记录未实际写入，必须回滚积分扣减、宗门资金和人数变更，唯一冲突继续映射为宗门名已存在或用户已加入宗门。

## 2026-06-23 GitHub 福利卡密创建行数守护补充

`github_benefit_test.go` 新增 `TestGithubBenefitRewardCodeCreatesCheckRowsAffected`，用于守护 GitHub 福利最终发放时创建 `InviteCode` 或 `RenewCode` 必须检查数据库 `Error` 与 `RowsAffected`；若卡密记录未实际写入，必须回滚名额扣减、领取状态和审计，避免用户被标记已领取但卡密不存在。

## 2026-06-23 邀请试用注册创建行数守护补充

`referral_logic_test.go` 新增 `TestReferralTrialCreateHelpersCheckRowsAffected`，并扩展 `TestReferralTrialRegisterAndConvertCheckRowsAffected`，用于守护邀请注册创建新试用 `User` 和 `ReferralActivation` 时必须通过 helper 检查数据库 `Error` 与 `RowsAffected`；若试用档案或激活记录未实际写入，必须回滚每日激活额度和本地试用状态，激活记录唯一冲突继续映射为已尝试。

## 2026-06-23 邀请码归因创建行数守护补充

`referral_logic_test.go` 扩展 `TestEnsureReferralCodeReenableChecksRowsAffected`，用于守护个人邀请链接创建新 `ReferralCode` 和重新启用旧 `ReferralCode` 时都必须检查数据库 `Error` 与 `RowsAffected`；若归因码未实际写入，不得向用户展示邀请链接。

`referral_logic_test.go` 新增 `TestReferralBusinessErrorsOnlyForMissingRecords`，用于守护个人邀请链接生成、试用转正和新人任务领奖读取用户、邀请码或激活记录时，只有 `gorm.ErrRecordNotFound` 能映射成业务不存在；数据库错误必须返回通用失败分支并记录脱敏日志。

## 2026-06-23 丹药修为加成行数守护补充

`cultivation_logic_test.go` 新增 `TestPillAudioTimeGrantChecksRowsAffected`，用于守护吞服丹药事务中的 `applyPillAudioTimeInTx` 必须检查 `cultivations` upsert 的 `Error` 与 `RowsAffected`；若丹药修为加成未实际写入，必须回滚背包扣除和使用日志，避免“丹药已消耗但修为加成未到账”。

## 2026-06-23 丹药服用日志创建行数守护补充

`cultivation_logic_test.go` 新增 `TestPillUsageLogCreateChecksRowsAffected`，用于守护吞服丹药事务写入 `ItemUsageLog` 时必须通过 `createItemUsageLogInTx` 检查数据库 `Error` 与 `RowsAffected`；若服用日志未实际写入，必须回滚额度占用、库存扣减和修为加成，避免丹药资产变化缺少服用追溯。

## 2026-06-23 丹药额度初始化行数守护补充

`cultivation_logic_test.go` 新增 `TestPillUsageQuotaCreateChecksRowsAffected`，用于守护吞服丹药事务初始化 `ItemUsageQuota` 时必须通过 `createItemUsageQuotaIfMissingInTx` 检查数据库 `Error` 与 `RowsAffected`；0 行写入只能按本周期额度档案已存在处理，随后仍必须通过条件更新原子占用额度。

## 2026-06-23 聚宝斋库存发放行数守护补充

`garden_logic_test.go` 新增 `TestTreasureShopInventoryGrantChecksRowsAffected`，用于守护聚宝斋购买事务在扣除积分后发放丹药库存时必须检查 upsert 的 `Error` 与 `RowsAffected`；若库存写入未命中必须回滚交易，避免“积分已扣但物品未入袋”。

## 2026-06-23 抽奖库存奖品发放行数守护补充

`lottery_logic_test.go` 新增 `TestLotteryInventoryGrantChecksRowsAffected`，用于守护积分抽奖领奖事务中的丹药/库存奖品发放 helper 必须检查 upsert 的 `Error` 与 `RowsAffected`；若库存奖品发放写入未命中必须回滚领奖事务，避免中奖状态已标记但奖品未到账。

## 2026-06-23 药园库存发放行数守护补充

`garden_logic_test.go` 新增 `TestGardenGrantInventoryChecksRowsAffected`，用于守护药园种子购买、收获和炼丹产物发放共用的 `gardenGrantInventoryInTx` 必须检查 upsert 的 `Error` 与 `RowsAffected`；若库存发放写入未命中必须让事务失败，避免资产流程出现“扣了积分或消耗材料但库存未到账”仍继续提交。

## 2026-06-23 药园资产记录创建行数守护补充

`garden_logic_test.go` 新增 `TestGardenAssetCreatesCheckRowsAffected`，用于守护药园开垦灵田、单次/批量种植和丹方解锁必须分别通过 `createGardenPlotInTx`、`createGardenPlantingInTx` 与 `createGardenRecipeUnlockInTx` 创建记录，并同时检查数据库 `Error` 与 `RowsAffected`；唯一冲突继续映射为原有业务错误，未实际写入必须回滚积分或库存扣减。

## 2026-06-23 Garden Mini App 动作提交后状态刷新失败守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppActionRequestDecodeRejectsTrailingJSON` 和 `TestGardenMiniAppCommittedActionRefreshFailureIsNotRetryableActionFailure`，用于守护 Mini App 写操作请求只允许空 body 或单个 JSON 对象，拒绝尾随 JSON/垃圾内容；同时守护动作事务已完成但状态刷新失败时，后端必须返回 `ok=true` / `STATE_REFRESH_FAILED`，前端进入只读重连态并只重试 `/api/garden/state`，不得返回会触发动作自动重放的 5xx。

## 2026-06-22 Garden Mini App 方法错误响应守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppMethodNotAllowedIsJSON`，用于守护 Mini App API 状态读取和动作接口遇到不支持的 HTTP 方法时，必须返回带 `Allow` 头的 JSON 错误响应，且不得继续执行业务动作函数，避免前端收到空 405 或错误方法误入资产逻辑。

## 2026-06-22 GitHub API 响应体大小守护补充

`github_benefit_test.go` 新增 `TestReadGithubAPIResponseBodyRejectsOversize`，用于守护 GitHub 福利校验读取第三方响应时必须按 `githubBenefitMaxAPIResponseBytes` 检测超限并失败关闭，不得把被 `LimitReader` 截断后的响应继续当作完整 JSON 解析。

## 2026-06-22 Garden Mini App JSON 响应头守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppJSONResponseHeaders`，用于守护 Mini App API JSON 响应必须带 `application/json; charset=utf-8`、`Cache-Control: no-store`、`Pragma: no-cache`、`Expires: 0` 和 `X-Content-Type-Options: nosniff`，与静态资源输出保持一致，避免接口响应被缓存或 MIME sniffing。

## 2026-06-22 外部 API 错误文本净化守护补充

`abs_client_logic_test.go` 新增 `TestAbsResponseSnippetSanitizesExternalBody`，用于守护 ABS 非成功响应片段在进入错误对象前必须折叠控制字符、脱敏 password/token/Bearer 等敏感片段，并按 160 rune 截断；`github_benefit_test.go` 新增 `TestGithubAPIErrorMessageSanitizesExternalText`，用于守护 GitHub API 远端 message 在拼入错误前完成同样的脱敏和限长，避免第三方响应体污染日志或未来新增展示出口。

## 2026-06-22 Garden Mini App 鉴权失败日志守护补充

`garden_logic_test.go` 新增 `TestGardenMiniAppAuthFailureLogIsSanitized`，用于守护 Garden Mini App 鉴权失败日志只记录鉴权错误码、规范化后的 method/path/UA 和 init data 是否存在的布尔值；不得把 `X-Telegram-Init-Data` 原文、原始路径或原始 UA 写入日志，避免 Telegram 鉴权串或异常请求头污染诊断日志。

## 2026-06-22 备份清理路径日志参数守护补充

`backup_logic_test.go` 扩展 `TestBackupCleanupLogUsesPlainValue`，除继续确认删除历史明文 `.db` 文件时使用原始路径执行 `os.Remove` 外，进一步守护清理成功日志必须打印 `formatPlainValue` 后的路径变量，避免未来把本地路径、控制字符或异常文件名原样写入容器日志。

## 2026-06-22 敏感数据迁移回写行数守护补充

`maintenance_logic_test.go` 新增 `TestSensitiveDataMigrationsCheckRowsAffected`，用于守护启动阶段用户安全码、邀请码和续期卡敏感数据迁移回写必须同时检查 GORM `Error` 与 `RowsAffected`；迁移已从数据库批量读到目标记录后，若回写未命中必须中止并报错，避免敏感字段迁移漏写却继续标记流程健康。

## 2026-06-22 生命周期自动封禁/删除审计守护补充

`notifier_logic_test.go` 扩展 `TestAutoSuspendLocalFailureAuditIsChecked`，用于守护生命周期自动封禁本地失败审计和自动物理删除审计 detail 中的用户名、ABS ID 和 ABS 删除结果等动态字符串必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长，避免后台高危任务追溯记录被历史异常值打乱。

## 2026-06-22 生命周期成功日志用户名守护补充

`notifier_logic_test.go` 新增 `TestLifecycleSuccessLogsUsePlainValue`，用于守护生命周期自动封禁和自动物理删除成功日志中的历史用户名必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长，避免后台诊断日志被异常用户名打乱。

## 2026-06-22 宗门七日续期恢复审计守护补充

`sect_logic_test.go` 扩展 `TestSectShopRenewReactivateFailureAuditsAreChecked`，用于守护宗门七日续期已到账后，ABS 恢复失败和本地同步失败审计 detail 中的 ABS ID 必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长，避免资产已变更后的异常追溯记录被外部 ID 打乱。

## 2026-06-22 邀请码与邀请转正审计守护补充

`referral_logic_test.go` 新增 `TestInviteRegistrationAuditDetailsUsePlainValue` 和 `TestReferralAuditDetailsUsePlainValue`，用于守护邀请码注册预占/补偿释放、邀请试用注册和试用转正审计 detail 中的邀请码预览、用户名、ABS ID 和补偿原因等动态字符串必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长，避免卡密资产追溯记录被历史异常值打乱。

## 2026-06-22 管理员调账与物理删号审计守护补充

`admin_input_logic_test.go` 新增 `TestAdminAdjustAndForceDeleteAuditDetailsUsePlainValue`，用于守护超级管理员调账流水描述、`ADJUST_POINTS` 审计 detail 和 `FORCE_DELETE_USER` 审计 detail 中的原因、用户名和 ABS ID 等动态字符串必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长，避免资产和删号追溯记录被用户输入或历史异常值打乱。

## 2026-06-22 GitHub 福利配置审计守护补充

`github_benefit_test.go` 扩展 GitHub 福利配置写入源码守护，用于确保开启状态和名额变更审计 detail 中的管理员原因必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长，纯文本预览和二次确认中的待执行命令、变更原因也必须通过 `formatPlainValue` 展示，避免高危福利配置审计和确认消息被原因文本或异常会话值打乱；新增 `TestGithubBenefitNoUncheckedConfigFallbackHelpers`，用于禁止重新引入读取失败时按关闭或 0 处理的 GitHub 福利配置 helper，避免后续业务误用 unchecked fallback。

## 2026-06-22 GitHub 福利登录名 URL 边界守护补充

`github_benefit_test.go` 扩展 `normalizeGithubLogin` 用例，守护用户校验 GitHub 福利时只接受普通用户名、`@用户名` 或 `https://github.com/用户名` 形式，必须拒绝 `http://`、URL userinfo 和非 `github.com` 主机，避免宽松前缀解析把外部身份输入边界放宽。

## 2026-06-22 ABS API 基础 URL 启动校验守护补充

`config_logic_test.go` 新增 `TestAbsAPIURLRequiresValidBaseURL` 和 `TestValidateConfigUsesParsedAbsAPIURL`，用于守护 `ABS_API_URL` 启动校验必须解析 URL 后确认 scheme、主机、userinfo、query、fragment、空白和控制字符边界，生产 HTTP 禁用判断必须基于解析结果，不得退回 `strings.HasPrefix` 前缀判断。

## 2026-06-22 Telegram 启动诊断脱敏守护补充

`telegram_diagnostics_test.go` 新增 `TestTelegramStartupDiagnosticsAreSanitized` 和 `TestTelegramMetricErrorFormatsEndpoint`，用于守护 Bot 初始化失败日志必须使用 `formatTelegramSendError`，启动成功日志、长轮询 allowed_updates 和 `bot_startup_health.txt` 中的 Bot 用户名必须通过 `formatPlainValue` 规范化，Telegram 运行指标错误摘要中的 endpoint 也必须通过 `formatPlainValue` 折叠控制字符，避免 Bot Token、异常外部用户名、异常更新类型或异常请求路径进入日志/持久化诊断文件和后台状态面板。

## 2026-06-22 备份清理路径诊断脱敏守护补充

`backup_logic_test.go` 新增 `TestBackupCleanupLogUsesPlainValue`，用于守护历史明文备份清理日志只能记录经过 `formatPlainValue` 规范化的本地路径，删除文件时继续使用原始路径，避免绝对路径、控制字符或异常文件名原样进入容器日志。

## 2026-06-22 自动备份状态读取失败展示守护补充

`notifier_logic_test.go` 新增 `TestAutoBackupRetryCountStatusReadErrorsAreVisible` 和 `TestBackupStatusReportConfigReadsAreChecked`，用于守护 `备份状态` 和 `后台状态` 面板读取今日自动备份重试次数失败时必须显示“读取失败”并标记状态暂不可用；`备份状态` 面板读取最近成功日期、成功/尝试时间、备份消息 ID、置顶消息 ID、最近备份错误和最近置顶错误失败时，也必须显示“读取失败”或状态暂不可用，不得把配置读取失败折叠成 `0/%d`、`无` 或 `从未成功` 等正常状态。

## 2026-06-22 后台状态读取失败展示守护补充

`notifier_logic_test.go` 新增 `TestBackgroundStatusReportConfigReadsAreChecked`，用于守护 `后台状态` 面板读取生命周期最近完成日/错误、每日听书刷新时间/成功数/总数/跳过数/错误、自动备份最近成功日/错误等状态配置失败时，必须显示“读取失败”或状态暂不可用，不得把配置读取失败折叠成 `0`、`无`、`从未完成` 或 `从未成功` 等正常状态；新增 `TestNoUncheckedSystemConfigTimeFallbackHelpers`，禁止重新引入读取系统配置时间失败时按空时间处理的 unchecked helper。

## 2026-06-22 系统监控读取失败展示守护补充

`abs_client_logic_test.go` 新增 `TestServerStatsStatusReadsAreVisible`，用于守护管理员系统监控读取 ABS 用户总数、活跃会话数、本地积分用户总数或每日净修为刷新状态配置失败时，必须显示“读取失败”或状态暂不可用，不得把外部响应、数据库或配置读取失败折叠成 `0`、`尚未执行` 或 `无` 等正常状态。

## 2026-06-22 宗门秘境配置审计守护补充

`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmConfigAuditDetailUsesPlainValue`、`TestSectSecretRealmConfigWriteDetailsUsePlainValue` 和 `TestSectSecretRealmConfigAdminPlainRepliesUsePlainValue`，用于守护宗门秘境配置写入审计 detail 中的管理员原因、档位 key、档位名称和掉落物品名必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长，纯文本预览和二次确认中的待执行命令、变更原因也必须通过 `formatPlainValue` 展示，避免高危配置审计和确认消息被原因文本、异常会话值或配置值打乱。

## 2026-06-22 抽奖审计 detail 守护补充

`lottery_logic_test.go` 扩展 `TestCreateLotteryAuditIsTransactional` 和 `TestForceDrawLotteryAuditIsTransactional`，`sect_lottery_logic_test.go` 新增 `TestSectLotteryAuditDetailsUsePlainValue`，用于守护积分抽奖和宗门抽奖创建/开奖/取消审计 detail，以及开奖 `result_note` 中的活动标题、模式、开奖原因等动态字符串必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长，避免活动标题、调度原因或历史异常值破坏审计展示。

## 2026-06-22 抽奖创建 session 数值守护补充

`lottery_logic_test.go` 新增 `TestLotterySessionNumericParsersFailClosed` 和 `TestLotteryCreateSessionValuesDoNotIgnoreParseErrors`，用于守护积分抽奖创建确认/落库阶段重新读取 session 中的参与消耗、满人开奖人数和定时开奖时间时，必须显式解析、检查范围并在异常时失败关闭，不得因忽略 `strconv` 错误把费用、人数或开奖时间静默折叠成 0。

## 2026-06-22 交易行 session 数值守护补充

`marketplace_logic_test.go` 新增 `TestMarketplaceSessionIntParserFailsClosed` 和 `TestMarketplaceSessionValuesDoNotIgnoreParseErrors`，用于守护交易行购买确认、背包上架和自由卡密上架在确认/落库阶段重新读取 session 中的购买数量、背包锁定数量和上架价格时，必须显式解析、检查范围并在异常时失败关闭，不得因忽略 `strconv` 错误把数量或价格静默折叠成默认值。

## 2026-06-22 宗门抽奖 session 数值守护补充

`sect_lottery_logic_test.go` 新增 `TestSectLotterySessionParsersFailClosed` 和 `TestSectLotteryCreateSessionValuesDoNotIgnoreParseErrors`，用于守护宗门抽奖创建确认/落库阶段重新读取 session 中的宗门 ID、满人开奖人数和定时开奖时间时，必须显式解析、检查范围并在异常时失败关闭，不得因忽略 `strconv` 错误把宗门、人数或开奖时间静默折叠成 0。

## 2026-06-22 红包与调账 session 数值守护补充

`redpacket_logic_test.go` 新增 `TestRedPacketCountStepChecksSessionAmountParseError`，用于守护红包创建在个数输入阶段重新读取 session 中的红包总积分时，必须显式检查解析错误并失败关闭，不得忽略 `strconv` 错误继续进入资产事务；`admin_input_logic_test.go` 新增 `TestAdminAdjustReasonStepChecksSessionParseErrors`，用于守护管理员调账在原因输入后生成二次确认前，必须显式检查目标用户 ID 和调账数值的 session 解析错误，避免异常会话生成目标或金额为 0 的误导性确认摘要。

## 2026-06-22 卡密生成 session 数值守护补充

`admin_input_logic_test.go` 新增 `TestAdminGenerateCodeSessionValuesCheckParseErrors`，用于守护超级管理员批量生成邀请码和续期卡在原因输入后生成二次确认、以及最终确认执行前，必须显式检查邀请码数量、续期卡天数和续期卡数量的 session 解析错误与范围边界，不得因忽略 `strconv` 错误把卡密数量或面额静默折叠成 0。

## 2026-06-24 卡密生成事务返回守护补充

`admin_input_logic_test.go` 新增 `TestAdminCodeGenerationReturnValuesOnlyAfterAuditSuccess`，用于守护超级管理员批量生成邀请码和续期卡时，明文卡密列表必须先写入事务内临时列表，只有全部卡密记录创建且 `GENERATE_INVITE_CODES` / `GENERATE_RENEW_CODES` 审计写入成功后，才发布给外层返回；事务失败或提交失败时必须返回空列表和错误。

## 2026-06-22 聚宝斋购买价格守护补充

`garden_logic_test.go` 新增 `TestTreasureShopConfirmChecksSessionPriceParseError` 和 `TestTreasureShopPurchaseRejectsNonPositivePrice`，用于守护聚宝斋购买确认阶段重新读取 session 中的商品价格时，必须显式检查解析错误和非正价格；购买事务入口也必须拒绝 0 或负数价格，避免异常会话或调用方错误导致不扣积分发放物品。

## 2026-06-22 药园回调田号解析守护补充

`garden_logic_test.go` 新增 `TestGardenPlotCallbacksCheckParseErrors`，用于守护药园 `plantlist`、`plant` 和 `harvest` 回调中的灵田编号必须显式检查 `strconv.Atoi` 错误和非正编号，不得把异常回调参数静默折叠成 0 号田后继续渲染、种植或收获。

## 2026-06-22 求书工单回调 ID 解析守护补充

`admin_input_logic_test.go` 新增 `TestBookRequestCallbackChecksRequestIDParseError`，用于守护求书工单管理员回调在读取工单 ID 时，必须显式检查 `strconv.ParseUint` 错误和 0 值，不得把异常回调数据静默折叠成 0 后继续查询或处理工单。

## 2026-06-22 求书工单管理员消息 ID 解析守护补充

`admin_input_logic_test.go` 新增 `TestBookRequestAdminMessageIDsCheckParseErrors`，用于守护求书工单管理员备注和要求用户补充信息流程，在刷新原管理员消息前读取 chat/message ID 时，必须显式检查 `strconv.ParseInt` 错误和 0 值，不得把异常会话值静默折叠成 0 后继续作为有效消息定位使用。

## 2026-06-22 求书工单管理员消息 ID 写入守护补充

`admin_input_logic_test.go` 新增 `TestBookRequestAdminMessageIDWritesCheckRowsAffected`，用于守护求书工单通知、接单、备注、要求补充信息和完成/拒绝回调中补写管理员消息 ID 时，必须通过统一 helper 检查数据库 `Error` 和 `RowsAffected`，不得静默忽略消息定位写入失败。

## 2026-06-22 高危管理员审计 detail 守护补充

`admin_input_logic_test.go` 新增 `TestHighRiskAdminAuditDetailsUsePlainValue`，用于守护高危管理员本地变更、清理遗孀、暂停/恢复失败和手动备份审计 detail 中的原因、用户名和 ABS ID 等动态字符串必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长，避免用户输入、管理员备注或外部 ID 破坏审计展示。

## 2026-06-22 高危 ABS 操作守护补充

`admin_input_logic_test.go` 新增 `TestSuspendAndDeleteRecheckSuperAdminBeforeABSCalls`，用于守护 `暂停/恢复` 和 `删除用户` 在确认阶段重新读取目标用户后，必须先复核目标不是超级管理员，再调用 ABS 状态更新或 ABS 删除；物理删除入口也必须在进入原因和二次确认前复核目标身份，避免确认期间角色变化导致外部副作用先发生。

`admin_input_logic_test.go` 新增 `TestSuspendFailureAuditWritesAreChecked`，用于守护 `暂停/恢复` 的 ABS 更新失败审计和本地同步失败审计必须通过可返回错误的 `writeAuditLogInTx` 写入，并在失败审计也写入失败时通知超级管理员人工核查；失败日志中的派生 action 必须通过 `formatPlainValue` 规范化。

`admin_input_logic_test.go` 新增 `TestRenewReactivateFailureAuditWritesAreChecked`，用于守护续期卡恢复封禁账号时，ABS 恢复失败审计和本地同步失败审计必须通过可返回错误的 `writeAuditLogInTx` 写入，并在失败审计也写入失败时通知超级管理员人工核查。

`sect_logic_test.go` 新增 `TestSectShopRenewReactivateFailureAuditsAreChecked` 和 `TestSectShopRenewActionsAreHighRiskAudits`，用于守护宗门七日续期恢复封禁账号时，ABS 恢复失败审计和本地同步失败审计必须通过可返回错误的 `writeAuditLogInTx` 写入，并确保 `SECT_SHOP_RENEW` / `SECT_SHOP_RENEW_REACTIVATE` 纳入高危审计统计。

## 2026-06-22 手动备份审计守护补充

`admin_input_logic_test.go` 新增 `TestManualBackupAuditWritesAreChecked`，用于守护手动 `备份数据` 成功和失败路径必须通过可返回错误的 `writeAuditLogInTx` 写入 `MANUAL_BACKUP` / `MANUAL_BACKUP_FAILED`，并在审计写入失败时通知超级管理员人工核查，避免敏感备份外发后审计缺失只停留在普通日志。

## 2026-06-22 遗孀清理审计守护补充

`admin_input_logic_test.go` 新增 `TestCleanWidowsAuditWritesAreChecked`，用于守护 `清理遗孀` 在 ABS 服务端删除完成后必须通过可返回错误的 `writeAuditLogInTx` 写入 `CLEAN_WIDOWS`，并在审计写入失败时通知超级管理员人工核查，避免不可逆外部删除缺少高危审计追溯。

## 2026-06-22 修仙配置审计守护补充

`admin_input_logic_test.go` 新增 `TestCultivationAdminWriteAuditsAreTransactional`、`TestCultivationThresholdAuditDetailsUsePlainValue` 和 `TestCultivationAdminPlainRepliesUsePlainValue`，用于守护 `重载修仙配置` 必须显式检查审计写入错误，`设置突破成功率`、`设置突破消耗`、`设置突破冷却`、`设置突破最低修为`、`设置境界门槛` 和 `设置小境界门槛` 必须在配置写入事务内检查配置更新 `RowsAffected`，并完成规则校验和 `AuditLog` 写入；配置缓存刷新只能在事务提交后执行，审计 detail 中的管理员原因、境界名和小境界名必须通过 `formatPlainValue` 规范化，纯文本预览、二次确认和成功回执中的待执行命令、原因、境界名和小境界名也必须通过 `formatPlainValue` 展示，避免审计缺失、长事务、未实际落库却提示成功或动态文本打乱审计展示。

## 2026-06-22 积分抽奖审计守护补充

`lottery_logic_test.go` 新增 `TestCreateLotteryAuditIsTransactional` 和 `TestForceDrawLotteryAuditIsTransactional`，并扩展 `TestCancelLotteryRefundMarksParticipantBeforeRefund`，用于守护积分抽奖创建必须在活动和奖品创建事务内写入 `CREATE_LOTTERY` 审计，手动强制开奖必须在开奖事务内写入 `FORCE_DRAW_LOTTERY` 审计，开奖结果备注中的开奖原因必须通过 `formatPlainValue` 规范化，取消抽奖必须在退款事务内写入 `CANCEL_LOTTERY` 审计，并且累计退款额只能在退款事务成功后返回，避免活动已创建、开奖已生效或积分已退还但高危审计缺失、追溯备注被异常原因文本打乱，或管理员回执误用未提交的退款总额。

## 设计原则

- **只测纯逻辑**：所有测试只覆盖输入→输出确定（或可断言区间/不变量）的函数，不打开 SQLite、不发网络请求。
- **把 `AGENTS.md` 规则编码为断言**：例如红包并发重试边界、群成员校验豁免、世界 Boss 血量上下限、每小时伤害系数、抽奖奖品校验、北京时间日界等，文档规则一旦被改坏即测试失败。
- **不变量优先**：除点值用例外，还对关键资产函数加了不变量断言（如交易手续费必须严格小于本金、随机奖励必须落在文档区间、同一自然日灵田刊例必须稳定）。

## 运行方式

测试可直接用仓库内置 Go 工具链运行，**无需 cgo**（默认 `CGO_ENABLED=0`）：

```bash
.tools/go/bin/go test ./...
```

这与无人值守门禁 `scripts/agent_unattended.ps1` 中的 `go test ./...` 步骤一致，因此新增测试会随门禁一起执行并计入 `reports/agent_gate.json`。

### 关于 cgo / SQLite 的边界

项目通过 `gorm.io/driver/sqlite`（依赖 cgo 的 `mattn/go-sqlite3`）连接数据库。`CGO_ENABLED=0` 下：

- 测试二进制**可以正常编译**（sqlite 驱动在无 cgo 时只在真正 `Open` 连接时报错）；
- 因此**不要在这些测试里打开数据库**——任何需要真实 SQLite 连接的测试（如事务、唯一索引、并发竞态）必须走 cgo，本机无 gcc，只能在 Docker（`golang:1.22-alpine3.20` + `gcc musl-dev`，对应 `-UseDockerGo`）中运行。
- 当前套件刻意避开了 DB 依赖，因此可在宿主机直接跑通。

## 测试文件与覆盖范围

| 文件 | 覆盖函数 | 关联规则 |
| --- | --- | --- |
| `account_binding_logic_test.go` | `rebindLocalUserWithAudit` / 绑定审计 detail 源码守护 | 安全码换绑必须在事务内按目标档案 ID、原始 `telegram_id` 和 ABS 用户 ID 条件更新，并检查 `RowsAffected` 后再写入 `REBIND_USER` 审计，避免过期换绑会话覆盖已变更归属的本地资产档案；绑定、换绑和解绑审计 detail 中的用户名、ABS ID 等动态字符串必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长 |
| `account_status_logic_test.go` | `buildAccountStatusDisplay`、`localAccountStatusText` | 账号状态展示必须覆盖播放异常临停、ABS 服务端停用但本地仍有效、本地过期但 ABS 仍启用、白名单忽略到期时间等状态；本地/服务端不一致或临停中不得显示为正常 |
| `abs_client_logic_test.go` | `absUserPath`、`absUserListeningStatsPath`、`absUserListeningSessionsPath`、`AbsClient` 用户路径源码守护、`GetServerStats` 源码守护 | ABS 用户 ID 和模板用户 ID 作为 URL path segment 使用前必须通过统一 helper 做 path escaping；改密、删号、封禁/解封、账号状态读取、听书报告、用户最新会话和注册模板读取不得直接拼接 raw `/api/users/{id}` 路径，避免历史异常 ABS ID 改变请求路径；管理员系统监控读取 ABS 用户列表、活跃会话、本地积分用户总数或每日净修为刷新状态失败时必须显示“读取失败”或状态暂不可用，不得把外部响应、数据库或配置异常折叠成 0、尚未执行或无错误 |
| `service_operations_logic_test.go` | `validRedPacketCount`、`openRegistrationCampaignAvailability`、`compensationExpireAt`、`authoritativeABSListeningTotalSeconds`、`canUseLiveClockFallback`、系统配置菜单入口及服务运维源码守护 | 守护红包份数 `3-100`、开放注册人数/时长边界、补偿有效期规则、ABS 官方累计时长优先、首次会话不倒推；确保开放注册和用户补偿仅位于超级管理员【系统配置】且回调权限正确，并守护事务、幂等、流水、审计与后台接线 |
| `backup_logic_test.go` | `cleanupStalePlainBackups` 源码守护 | 清理历史明文备份文件时，删除操作必须使用原始本地路径，成功和失败日志诊断只能记录经过 `formatPlainValue` 规范化的路径，删除失败还必须使用 `formatPlainError` 记录脱敏错误，避免敏感明文文件残留时缺少排障线索，也避免绝对路径、控制字符或异常文件名原样进入容器日志 |
| `blind_box_logic_test.go` | `executeBlindBoxOpen` 源码守护 | 积分盲盒扣积分、创建邀请码或续期卡、生成中奖回执和大奖广播必须保持事务返回一致性；只有事务成功后才能返回卡密/邀请码文案或广播内容，事务失败或提交失败必须返回空消息和错误，避免卡密未落库却被发送 |
| `admin_input_logic_test.go` | `validateAdminReason`、`adminAdjustDailyLimitExceeded`、`validateBookRequestNote`、`validateServerLinesContent`、`serverLinesMarkdownBody`、`inventoryItemMarkdownName`、`notifyBookRequestAdmins` 源码守护、`loadBookRequestByID` 源码守护、`createBookRequestWithinLimits` / `reloadBookRequestAfterClaim` / `createBookRequestLogInTx` / `createBookRequestLog` 源码守护、`createAutoDeleteMessageRecord` 源码守护、求书入库公告源码守护、账号安全改密流程源码守护、`WAITING_QUERY_CODE` 源码守护、只读面板读取失败源码守护、账号入口读取失败源码守护、高危管理员目标读取源码守护、管理员查询用户读取源码守护、`WAITING_CONFIRM_CLEAN_WIDOWS` / `WAITING_CONFIRM_SUSPEND_USER` / `WAITING_RENEW_CODE` 源码守护、`applySuspendLocalStatusWithAudit` / `applyRenewReactivateLocalStatusWithAudit` / `deleteLocalUserWithAudit` / `recordSecurityAttemptFailureInTx` / `updateSecurityAttemptFailureInTx` 源码守护 | 高危管理员操作原因必须至少 5 字、超过 200 字截断保存，并拒绝换行、制表符、控制字符和 Unicode 分隔符；管理员调账每日额度按变动绝对值累计，刚好达到 20000 可通过，超过必须拒绝；求书备注、用户补充内容、管理员备注和要求补充说明允许换行但必须拒绝超长、制表符、普通控制字符和 Unicode 行/段分隔符，四个入口都必须复用 `validateBookRequestNote`；线路配置允许换行并归一 CRLF，但拒绝空内容、超长内容、制表符、控制字符和 Unicode 分隔符；用户获取线路时必须复核历史配置并转义 Markdown 特殊字符，非法历史配置显示异常提示，读取失败不得误显示为未配置；Markdown 物品名展示必须折叠换行、制表符、普通控制字符和 Unicode 行/段分隔符，保留单行转义后的安全展示并为空名提供 `-` 兜底；求书工单通知管理员时必须检查数据库管理员列表读取错误并记录脱敏日志，不得静默吞掉；求书工单详情、接单、要求补充信息、管理员备注、完成/拒绝以及会话输入读取主工单时必须区分不存在和数据库错误，DB 错误不得误报为工单不存在；求书工单接单成功后重载失败必须使用已写入接单状态继续刷新和通知，管理员消息 ID 写入失败必须记录日志；求书工单创建、用户补充、接单、管理员备注、要求补充和标记已上传/暂无资源必须把状态更新、`BookRequestLog` 和对应审计/日志放在同一事务内，主工单记录、日志和审计创建都必须检查 `RowsAffected`，事务提交后才通知用户或刷新消息；求书入库公告必须从每个 ABS 媒体库最近 5 条记录中筛选近 20 分钟内最新书籍，确认按钮必须使用短 token 解析到缓存 itemID，图片发送失败必须降级纯文本，且公告仍不得自动删除或置顶；求书 best-effort 日志和自动删除消息登记失败只记录脱敏日志，但写入必须检查 `RowsAffected`，且诊断文本必须保持可读 UTF-8；账号安全、换绑、改密、注销和解绑安全码流程读取本地档案失败时不得继续校验空安全码、调用空 ABS ID 改密、解绑或注销；安全码失败次数首次创建、累加和成功后清理锁状态必须做条件写入/更新并检查 `RowsAffected`，日志错误必须脱敏；卡密查码读取邀请码、续期卡或使用者档案失败时必须区分未找到和数据库错误，不得误报查无此码或用户已注销；管理员查询用户按 TG ID 查询时只有记录不存在才能回退用户名查询，数据库错误必须提示稍后重试，不得误报为未查到；天道奖池读取失败必须显示读取失败，不得误显示为 0；我的信息、听书报告、注册、绑定、绑定校验后既有绑定查询和自助注销读取本地档案失败时必须提示稍后重试或记录日志，不得误显示为无资产、幽灵钱包、未注册、未绑定、首次接入或继续注销；授权管理员、白名单、调账、模拟过期、暂停/恢复和物理删号读取目标用户失败时必须提示稍后重试并记录日志，不得误报查无此人；清理遗孀执行 ABS 删除后必须显式检查 `CLEAN_WIDOWS` 审计写入并在失败时通知超级管理员；暂停/恢复和续期恢复的 ABS 更新失败审计、本地同步失败审计必须显式检查并在审计写入失败时通知超级管理员；修仙境界门槛配置审计 detail 中的境界名和小境界名必须通过 `formatPlainValue` 规范化；暂停/恢复、续期恢复和本地物理删号写入必须做条件更新/删除并检查 `RowsAffected`，避免本地状态未落库却写入成功审计，或本地未删除却写成功删除审计 |
| `book_request_announcement_logic_test.go` | `storeBookAnnouncementPreviewCandidate`、`parseBookAnnouncementPublishCallback`、`resolveBookAnnouncementPreviewCandidate` | 求书入库公告确认按钮必须保持 Telegram callback data 小于 64 字节，且通过短 token 解析回原始 ABS itemID；带下划线的 ABS itemID 不得被 callback 解析截断；过期预览候选必须失效并从内存缓存清理 |
| `business_errors_logic_test.go` | `renewRedeemErrorCode`、`WAITING_RENEW_CODE` 源码守护 | 新人体验账号使用普通续期卡时必须映射为 `TRIAL_CANNOT_USE_RENEW_CODE`，确保用户看到需先用正式邀请码转正的明确提示，而不是泛化续期失败；普通续期卡读取 `RenewCode` 和当前 `User` 时，只有 `gorm.ErrRecordNotFound` 能映射成卡密无效或账户不存在，数据库错误必须进入通用失败分支；核销后写入用户有效期必须检查 `RowsAffected`，避免卡密已消费但有效期未延长 |
| `code_record_logic_test.go` | `createInviteCodeRecord` / `createRenewCodeRecord` 源码守护 | 邀请码和续期卡共享创建入口必须检查数据库错误和 `RowsAffected`，防止管理员生成、抽奖发奖或历史补偿事务提交后出现明文卡密已发送但数据库无对应记录 |
| `config_logic_test.go` | `getConfigIntFromDBChecked`、`setConfigIntWithAudit`、`isValidAbsAPIURL`、`isAbsAPIHTTPURL`、`isLocalGardenMiniAppURL`、兑换价格读取源码守护 | 邀请码/续期卡价格配置缺失或空值可使用默认价；数据库读取错误或非法整数配置必须中止兑换商城展示、实际扣费和调价确认，不得静默使用默认价，且不得重新引入读取失败时使用默认价的 unchecked 整数配置 helper；设置价格审计旧值必须在事务内用带错误返回的读取 helper 获取，失败时回滚；ABS API 基础地址必须解析 URL 后确认 scheme、主机、userinfo、query、fragment、空白和控制字符边界，生产 HTTP 禁用判断必须基于解析结果；Mini App 本地 HTTP 例外必须解析 URL 后精确匹配 `localhost`、`127.0.0.1` 或 `::1` 主机，拒绝 `localhost.evil.com`、userinfo 和其他前缀冒充 |
| `cultivation_config_logic_test.go` | `createDefaultCultivationRealmConfigIfMissingInTx`、`createDefaultCultivationMinorRealmConfigIfMissingInTx`、`createDefaultBreakthroughConfigIfMissingInTx`、`ReloadCultivationRules` 源码守护 | 启动补齐默认大境界、小境界和突破配置时必须检查数据库错误和 `RowsAffected`；并发 0 行写入只能按默认配置已存在处理，防止修仙配置种子半初始化；修仙配置缓存刷新日志中的配置来源必须通过 `formatPlainValue` 规范化，避免历史异常来源打乱诊断日志 |
| `cultivation_logic_test.go` | `cultivationRankDisplayName`、`cultivationPointDescriptionName`、`SyncCultivationRealm`、`persistCultivationAudioTime`、`createItemUsageQuotaIfMissingInTx`、`createItemUsageLogInTx`、`applyPillAudioTimeInTx`、`handleBreakthrough` / `ExecuteBreakthrough` 源码守护 | 修仙榜用户名展示必须在用户已加入宗门时追加宗门名，未加入宗门时不追加；用户名和宗门名进入 Markdown 前必须转义；突破自动代购积分流水中的丹药名必须单行化；境界同步写回和累计听书时长写回必须检查更新结果，未命中时记录脱敏诊断日志，避免后台同步静默失效；吞服丹药事务初始化额度、写使用日志和增加丹药修为都必须检查数据库错误和 `RowsAffected`，额度初始化 0 行写入只能按本周期档案已存在处理；突破检查自动代购前钱包或突破丹库存读取失败必须提示稍后重试并记录日志，不得误显示为 0 积分、余额不足或无丹药后继续代购；突破事务读取修仙档案时只有 `gorm.ErrRecordNotFound` 能映射为未初始化档案，数据库错误必须回滚并进入通用失败分支；突破成功贡献日志 reason 必须保持可读 UTF-8 中文；修仙模块诊断日志不得使用 raw `err=%v` |
| `daily_listening_live_test.go` | `absUserListeningSessionsPath`、`splitDurationByBeijingDay`、`parseABSSessionsPayload`、`parseAbsLiveListeningSession`、`rebalanceABSDaysForCrossDaySessions`、`livePositionDeltaAllowed`、`liveClockFallbackSeconds`、`absLiveListeningCheckpointOnConflict` | 每日听书必须读取当前 ABS 用户最新 100 条会话而不是全局会话默认第一页；会话解析必须区分 `currentTime` 和 `timeListening`，并按 `startedAt + timeListening` 将 22:00 至次日 08:00 等连续播放跨北京时间日界拆分，拆分前后总秒数守恒；播放位置增量不得超过墙钟上限，播放中但位置停滞时可用短窗口墙钟 fallback，checkpoint upsert 必须匹配 `abs_live_listening_checkpoints(user_id, session_key, item_key) WHERE deleted_at IS NULL` 部分唯一索引 |
| `daily_listening_sync_test.go` | `GetPersonalReport`、`recordDailyListeningStatsFromABSDays`、`refreshDailyListeningStatsFromABS`、每日净修为命令源码守护、宗门秘境听书快照静态同步路径 | 听书报告必须复用每日净修为统一同步 helper 写入 `Cultivation.TotalAudioTime`，并检查同步 helper 返回结果；听书报告读取 ABS 主统计失败或解析失败时必须提示稍后重试，不得误显示暂无收听；ABS 书籍进度读取失败时必须把已完成/在听数量显示为“读取失败”而不是 0；按 ABS 用户 ID 回查本地用户失败时必须记录脱敏日志并跳过同步，不得静默吞掉数据库错误；每日听书统计批量 upsert 必须检查数据库错误和 `RowsAffected`，主动刷新路径遇到持久化失败不得计作成功；`刷新宗门今日净修为` 和 `查看每日净修为 用户ID [YYYY-MM-DD]` 必须区分未找到与数据库错误，DB 错误不得误报为未加入宗门或无记录；听书报告和宗门秘境只在本地统计写入成功后才从每日统计表重算，避免旧缓存覆盖本次 ABS 结果，写入失败降级使用本次 ABS 数据时必须记录脱敏诊断；宗门秘境按 ABS ID 回查本地用户遇到数据库错误也必须记录日志，只有记录不存在才静默兼容降级；不得绕过该 helper 直接持久化 `total_audio_time`，也不得在同步失败时把总净修为覆盖成 0，避免漏算当天洞府闭关加成或数据库读取失败导致报告总净修为和刷新今日净修为不一致 |
| `exchange_logic_test.go` | 积分兑换后邀请码/续期卡立即使用源码守护 | 积分兑换邀请码和续期卡必须先完成扣分与卡密落库，事务成功后才提示是否立即使用；二次确认会话只能保存卡密 hash 和脱敏预览，不得保存明文卡密；续期卡确认必须复用 `redeemRenewCodeByHash` 与统一续期回执，失败时说明卡密未消费；邀请码确认必须重新读取账号状态，试用账号直接转正，未注册 ABS 账号进入注册流程并预填邀请码，已有正式账号不得提示或直接消费邀请码 |
| `garden_logic_test.go` | `gardenHerbBaseSellPrice`、`gardenHerbMarketPrice`、`gardenHerbMarketLimit`、`gardenHerbMarketDayKey`、`gardenTodayHerbMarketOffers`、`gardenSellHerbQuantity`、`gardenDurationText`、`gardenYieldText`、`gardenErrorCode`、`gardenActionErrorText`、`gardenRecipeByKey`、`gardenPointDescriptionName`、`treasureShopPointDescriptionName`、`pillEffectSummary`、`treasureShopHomeMarkdownText`、`treasureShopItemMarkdownText`、`treasureShopItemButtonLabel`、`treasureShopBuyConfirmMarkdownText`、`treasureShopBuySuccessMarkdownText`、`manualPillUsageCountText`、`gardenCountText`、`gardenMaturityNoticeText`、种子商店/聚宝斋钱包展示源码守护 | 药园基础回收价按种子成本约 90% / 期望产量折算，当前可购买种子的普通回收期望总额必须小幅低于种子成本；药铺回收必须先确认 seed 配置存在再计算基础回收价或进入急收/库存扣减，回收数量必须为正整数，不得保留 `sell-all`、`sellall` 或 `quantity=-1` 全量回收入口；可购买灵草急收价和额度必须匹配分种子经济表，紫玉芝急收价限定 140-148 且额度固定 1，不得恢复“种子成本 +10”保底；药园种子购买、药草回收、丹方参悟和炼丹炉火积分流水中的物品/丹方名必须单行化，聚宝斋购买积分流水中的物品名也必须单行化；聚灵丹配方材料量必须保持与聚宝斋价格接近，九转造化丹炼制成本固定为炉火 30 积分、青灵叶 x8、玄参根 x3、紫玉芝 x1；丹药功效说明覆盖手动修为丹和突破丹，非丹药不返回功效，聚宝斋首页必须提示查看简要功效，商品展示、商品按钮、购买确认和购买成功提示必须包含对应丹药功效或短标识；丹毒计数、药园首页统计、种子商店钱包、聚宝斋钱包和聚宝斋首页乾坤袋数量读取失败时必须显示读取失败，不得把数据库错误当成 0；药园/聚宝斋诊断日志不得使用 raw `err=%v`，种子限购和药市急收读取失败日志中的日期 key 必须通过 `formatPlainValue` 规范化；急收药草按北京时间 22:00 刷新周期稳定生成且不超过 2 种；药园成熟倒计时、成熟提醒和种子产量展示文案稳定，成熟提醒必须按灵田编号排序并转义物品名；药园购买、种植、一键种植、收获、回收、丹方和炼丹失败必须按业务错误码展示明确提示，其中无空闲灵田必须映射为 `GARDEN_NO_EMPTY_PLOT`，唯一约束冲突提示用户刷新，未知错误回退为泛化失败；Mini App 写操作请求必须拒绝尾随 JSON/垃圾内容，动作已提交但状态刷新失败时不得返回可触发动作重试的 5xx，前端必须进入只读重连态 |
| `github_benefit_test.go` | `normalizeGithubLogin`、`validateGithubBenefitEligibility`、`isGithubBenefitWriteCommand`、`isGithubBenefitAdminCommand`、`githubBenefitRewardForUser`、`githubBenefitAdminCountText`、`githubBenefitHasClaimedTelegramInTx`、`githubBenefitHasClaimedGithubInTx`、`githubBenefitEnabledInTxChecked`、`githubBenefitQuotaInTxChecked`、`githubBenefitEnabledChecked`、`githubBenefitQuotaChecked`、`createGithubBenefitPendingClaimInTx`、`getActiveGithubBenefitPendingClaim` / `claimGithubBenefitReward` 源码守护、`newGithubAPIRequest` | GitHub 用户名、福利资格、管理员命令识别、奖励类型和 API Authorization 构造规则稳定；领取前 Telegram/GitHub 防重查询必须显式返回并处理数据库错误，不能把查询失败当成未领取而继续发邀请码或续期卡；GitHub 福利 pending claim 创建必须检查数据库错误和 `RowsAffected`，防止验证码和名额校验流程已通过但待验证记录未落库；过期 pending claim 标记失败必须返回数据库错误且检查 `RowsAffected`，状态未命中不得当成正常过期；GitHub 福利开启状态和名额读取必须区分配置缺失与数据库错误，领取、发奖和管理写入遇到数据库错误或非法名额值时必须中止，管理员状态读取失败时必须显示读取失败，不得把数据库错误当成关闭或 0 |
| `inventory_logic_test.go` | `state_machine.go` 乾坤袋列表源码守护 | 乾坤袋读取 `Inventory` 失败时必须提示稍后重试并记录日志，不得把数据库错误当成空背包 |
| `leaderboard_logic_test.go` | `GenerateAndSendLeaderboard`、`sendAndManageLeaderboardPinSync` 源码守护 | 自动榜单读取已绑定 ABS 用户列表失败时必须记录脱敏日志，不得静默当作没有用户；榜单请求 ABS 用户听书统计时必须对 `AbsUserID` 做 URL path escaping，避免历史异常外部 ID 改变请求路径；逐用户 ABS 统计读取或解析部分失败时必须记录聚合计数诊断，全部失败时必须跳过发送，避免误公告空榜；榜单置顶交接读取旧置顶状态失败或旧消息 ID 解析失败时必须记录脱敏日志，不得静默当作没有旧置顶；榜单新消息发送失败、置顶状态写入失败和交接成功日志必须使用 `formatPlainError` / `formatPlainValue` 规范化动态诊断字段 |
| `listening_abuse_logic_test.go` | `listeningAbuseYesterdayKey`、`previousDayKey`、`listeningAbuseShouldFreeze`、`listeningAbuseRawHoursFromStat`、`listeningAbuseReleaseBlocked`、`recordListeningAbuseReleaseError` / `recordListeningAbuseNoticeError` / `writeListeningAbuseFailureAudit` / `createListeningAbuseRecordInTx` / `createListeningAbuseWarning` / `runListeningAbuseMonitorIfNeeded` 源码守护 | 播放异常风控扫描日期、连续异常冻结和到期/封禁阻止自动解冻规则稳定；风控读取每日听书统计时必须跳过纯实时补偿数据，`mixed` 和 `corrected` 跨日修正数据只取 `official_raw_seconds`，只有跨日拆分值而没有官方日值时必须跳过；解冻失败写入 `release_error` 和通知失败写入 `notice_error` 都必须使用脱敏截断错误并检查数据库错误与 `RowsAffected`；warning/freeze 记录创建必须检查数据库错误和 `RowsAffected`，0 行写入只能按幂等重复处理；warning 记录和 `LISTENING_ABUSE_WARNING` 审计必须在同一事务内提交，私聊提醒必须在事务提交后发送；自动冻结、到期恢复和既往不咎恢复失败审计必须使用可返回错误的审计写入并在审计失败时通知超管，失败审计告警中的 label/action 必须通过 `formatPlainValue` 规范化，自动解冻被阻止的 `LISTENING_ABUSE_RELEASE_BLOCKED` 审计原因必须通过 `formatPlainValue` 规范化，诊断和审计 detail 中的日期 key 必须通过 `formatPlainValue` 规范化；读取生效日或末次扫描日失败必须写最近错误、日志和超管通知并跳过本轮巡检，通知中的配置 key 必须通过 `formatPlainValue` 规范化；首次初始化生效日和扫描完成后的末次扫描日、扫描时间、最近错误写入都必须检查错误并在失败时通知超管，不得直接持久化原始 `err.Error()`、静默忽略数据库写入失败或在状态不可读/不可写时继续造成重复扫描 |
| `lottery_logic_test.go` | `validLotteryTitle`、`validLotteryClaimCode`、`validLotteryPrizeName`、`validLotteryCustomCode`、`encryptLotteryCustomCode` / `decryptLotteryCustomCode`、`parseLotteryPrizeSpecs`、`lotteryDisplayText`、`lotteryPointDescriptionTitle`、`lotteryPrizeDisplayText`、`lotteryStatusText`、`countLotteryParticipants`、`handleLotteryDetailCommand`、`joinLotteryActivity`、`createLotteryActivityInTx`、`createLotteryPrizeInTx`、`createLotteryParticipantInTx`、`createLotteryWinnerInTx`、`createLotteryClaimLogInTx`、`createLotteryActivityFromSession` / `drawLotteryActivity` / `claimLotteryPrizeByCode` / `claimLotteryWinner` / `cancelLotteryActivityWithFullRefund` / `addPointsToFusionPoolInTx` 源码守护 | 积分抽奖活动名称必须为 2-60 个字、领奖暗号必须为 3-40 个字；人工领奖奖品和自定义卡密必须按受支持格式解析，自定义卡密每行一份、拒绝重复与控制字符、使用独立 AES-GCM 域密钥加密且数据库仅保存密文，人工奖品凭暗号领取后只提示联系管理员，，并拒绝换行、制表符、普通控制字符和 Unicode 行/段分隔符；奖品配置必须稳定支持积分、续期、邀请码和系统已知丹药，拒绝空配置、非法数量、未知类型和未知丹药；抽奖展示文本和积分流水描述活动名必须折叠换行、制表符、普通控制字符和 Unicode 行/段分隔符，丹药奖品展示名必须单行化，未知历史状态不得原样回显；抽奖活动列表和详情页读取活动、参与人数、奖品或中奖记录失败时必须展示读取失败，不得把数据库错误当成活动不存在、0 人、空奖品或无中奖记录；群内参与回填公告群 ID 必须限定 active 状态并检查 `RowsAffected`，并发已写入时可继续，否则回滚参与事务；创建抽奖必须在活动和奖品创建事务内写入 `CREATE_LOTTERY` 审计，活动和奖品创建必须走 `createLotteryActivityInTx` / `createLotteryPrizeInTx` 并检查数据库错误和 `RowsAffected`；参与记录创建必须走 `createLotteryParticipantInTx`，报名唯一冲突仍映射为已参加，防止参与扣费、人数回写和报名追踪不一致；中奖记录创建必须走 `createLotteryWinnerInTx`，中奖唯一冲突仍跳过该候选，防止开奖状态和中奖追踪不一致；强制开奖必须在开奖事务内写入 `FORCE_DRAW_LOTTERY` 审计，并检查活动关闭或开奖结果写回的 `RowsAffected`，开奖结果备注中的开奖原因必须通过 `formatPlainValue` 规范化；同一暗号命中多个已开奖活动时，领奖流程必须继续扫描后续待领奖中奖记录，已领取、非待领或过期记录只能作为备用提示；中奖领奖必须先条件更新抢占 `claimed` 状态并检查 `RowsAffected`，卡密预览更新也必须检查行数，且不得使用整行 `Save` 覆盖中奖记录；领奖成功和领奖过期的 `LotteryClaimLog` 必须统一走 `createLotteryClaimLogInTx`，规范化 action/detail 并检查数据库错误和 `RowsAffected`，防止奖品状态已变动但领奖追踪未落库；取消抽奖退款必须先条件更新抢占参与者退款标记并检查 `RowsAffected`，活动累计退款字段和 `CANCEL_LOTTERY` 审计也必须在同一事务内完成；天道奖池共享写回必须检查 `RowsAffected`，非法历史水位必须 fail closed 并回滚，避免注入或爆池红包与水位记录不一致，也不得按 0 覆盖历史水位；积分抽奖诊断日志不得使用 raw `err=%v`，置顶/取消置顶失败日志和超管通知中的消息标签必须通过 `formatPlainValue` 规范化 |
| `maintenance_logic_test.go` | `startMessageSweeper`、`getUserRoleFromDBChecked`、`writeAuditLogInTx`、`migrationAlreadyApplied` / `markMigrationAppliedIfMissing`、`db.go` 启动迁移日志、`state_machine.go` 诊断日志、状态机审计 detail 源码守护、全仓业务错误日志源码守护 | 自动删消息队列读取失败、Telegram 删除非终态失败和数据库记录清理失败都必须记录脱敏日志，不得静默忽略，诊断文本必须保持可读 UTF-8；只有成功删除或终态删除错误才可清理队列记录，清理 `AutoDeleteMsg` 必须检查删除错误和 `RowsAffected`；用户角色读取失败不得静默折叠成普通用户，普通权限判断需记录脱敏日志，事务审计写入必须在角色读取失败时返回错误并回滚；迁移版本记录读取失败必须阻止启动，不得当作迁移未执行；迁移版本记录创建必须检查数据库错误和 `RowsAffected`，未实际写入必须阻止启动；数据库启动和迁移失败日志不得用 `%v` 直出错误，必须使用 `formatPlainError`；状态机资产/运维路径诊断日志不得使用 raw `err=%v`；注册、续期、注销和管理员查询审计 detail 中的用户名、卡密预览和 ABS ID 等动态字符串必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长；非测试 Go 业务日志输出错误对象时不得使用 `%v` |
| `marketplace_logic_test.go` | `calculateMarketplaceFee`、`marketplacePurchaseGrossAmount`、`marketplacePurchaseFeeAmount`、`marketplacePurchaseSellerAmount`、`isMarketplaceCommand`、`isMarketplaceListCommand`、`parseMarketplaceID`、`parseMarketplaceBuyCommand`、`parseMarketplaceListFilter`、`hasMarketplaceCommandPrefix`、`marketplaceLikePattern`、`marketplaceStatusText`、`marketplaceDisputeStatusText`、`marketplacePurchaseStatusText`、`marketplaceTypeText`、`marketplaceErrorCode`、`marketplaceCreateErrorText`、`marketplaceDetailActionText`、`marketplaceDetailActionTextWithStockStatus`、`marketplaceStockText`、`marketplaceListingExpiresAt`、`marketplaceListingRemainingText`、`marketplaceEffectiveStatusText`、`closeMarketplaceListingByID`、`closeMarketplaceListingScoped`、`marketplaceSellerGroupsMatchListing`、`marketplaceSecretSellerGroupsLogText`、`marketplaceBuyQuantityTooLargeText`、`marketplaceBuyErrorText`、`marketplaceBuyConfirmText`、`marketplaceListingPillEffectLine`、`marketplaceSecretVerificationText`、`marketplaceSecretListingSource`、`marketplaceInventoryPurchaseSuccessText`、`marketplaceSellerDealNoticeText`、`marketplaceBuyPointDescription`、`marketplaceSellPointDescription`、`marketplacePurchaseDeliveryType`、`marketplaceListingTypeForClose`、`marketplaceClosedSecretStatus`、`marketplacePurchaseResult.PurchaseIDText`、`createMarketplaceListingInTx`、`createMarketplaceSecretInTx`、`createMarketplacePurchaseInTx`、`formatMarketplaceListingGroupNotice`、`showMarketplaceListings` / `showMyMarketplaceListings` / `showMyMarketplacePurchases` / `handleMarketplaceListingOrders` / `notifyMarketplaceListingCreated` / `quarantineMarketplaceListingForReview` 源码守护、`marketplaceDisplayText`、`parseMarketplaceSecrets`、`validateMarketplaceSecrets`、`validMarketplaceSecretListingName`、`validMarketplaceInventoryItemName`、`validMarketplaceDisputeReason` | 交易行手续费最低 1 积分但不能吃掉全部成交额；历史订单展示金额必须钳制在合法范围，异常数据不得显示负手续费、负实收或超额实收；交易行入口命令和列表命令必须保持明确边界，避免误拦截普通聊天文本；商品 ID 和购买命令默认数量、显式数量、非法数量、超单次上限数量的解析语义稳定；列表筛选别名、自定义关键词长度和控制/分隔字符拒绝规则稳定；带参数命令必须在命令名后有空白边界，避免误拦截普通聊天文本；筛选关键词里的 `%`、`_` 和反斜杠必须按 LIKE 字面量转义；交易行枚举展示、交付类型、关闭状态、异常 `review` 状态、库存状态和订单 ID 展示必须稳定兜底；交易行错误码必须识别哨兵、包装错误和旧字符串错误；上架/购买失败提示、购买确认文案、购买成功文案、卖家成交通知和详情页购买动作文案必须按业务错误、库存、下架、到期和异常类型稳定分类，库存读取失败时不得提示购买命令，确认和成交通知必须展示背包物品类丹药商品功效，自由卡密不得仅凭名称展示丹药功效，且新自由卡密上架必须仅允许 Bot 生成且尚未使用的邀请码或续期卡；自由卡密和背包物品上架创建商品主记录、卡密/库存单位记录必须分别走 `createMarketplaceListingInTx` 和 `createMarketplaceSecretInTx`，检查数据库错误和 `RowsAffected`，且不得规范化或改写卡密密文、哈希等敏感字段，防止背包库存已扣或卡密已校验但商品/库存单位追踪未落库；交易行购买记录创建必须检查数据库错误和 `RowsAffected`，并规范化物品名和卡密预览，防止买家扣款、卖家入账、手续费注入后订单追踪未落库；交易行有效期固定为 48 小时；自动/手动下架路径必须关闭商品、关闭未售单位并把背包库存退回卖家，未售单位关闭 `RowsAffected` 必须和退款依据一致；卖家字段不一致时必须检查暂停商品的 `RowsAffected`，未改到 active 记录时不得继续通知或退库存；积分流水描述必须单行化物品名；上架群提醒必须转义 Markdown、单行化用户字段并钳制负价格/库存，自由卡密来源补读失败必须记录脱敏日志并跳过公告发送；公开列表、我的上架、我的购买和卖家商品订单列表读取失败必须记录脱敏日志，筛选关键词进入日志前必须 `formatPlainValue`；交易行诊断日志不得使用 raw `err=%v`；交易行展示清洗必须把换行、制表符、普通控制字符和 Unicode 行/段分隔符折叠成单行文本；自由卡密输入、自由上架商品名、背包上架物品名必须拒绝制表符、回车和其他控制/分隔字符，同时自由卡密输入保留空行忽略和重复行去重；交易行争议原因必须为 3-200 个字，并拒绝换行、制表符、其他控制字符和 Unicode 分隔符 |
| `marketplace_logic_test.go` | `handleMarketplaceDetail` / `handleMarketplaceBuy` 源码守护 | 交易行详情和高价购买确认读取商品主记录时必须区分未找到与数据库错误；数据库错误不得误报为商品不存在或已下架，必须记录脱敏日志并提示商品状态读取失败 |
| `marketplace_logic_test.go` | `handleMarketplaceListingOrders` / `handleMarketplaceAdminOrderQuery` / `handleMarketplaceDispute` 源码守护 | 卖家商品订单、管理员订单查询和买家举报订单读取商品或订单主记录时必须区分未找到与数据库错误；数据库错误不得误报为商品或订单不存在，必须记录脱敏日志并提示稍后重试 |
| `marketplace_logic_test.go` | `handleMarketplaceClose` 源码守护 | 卖家手动下架商品遇到非业务错误时必须记录脱敏诊断日志，包含卖家 ID 和商品 ID；不得只返回泛化失败提示后静默丢失排障上下文 |
| `marketplace_logic_test.go` | `handleMarketplaceAdminOrderQuery` 源码守护 | 管理员查交易订单时，争议记录读取失败必须显示读取失败并记录日志，不得误显示为无争议记录 |
| `marketplace_logic_test.go` | `createMarketplaceDisputeInTx` / `handleMarketplaceDispute` 源码守护 | 买家提交交易争议创建 `MarketplaceDispute` 必须检查数据库错误和 `RowsAffected`，并规范化原因和状态；唯一冲突继续映射为已有处理中争议，未实际写入不得提示提交成功 |
| `notifier_logic_test.go` | `runDailyLifecycleIfNeeded`、`runDailyLifecycleOperations`、`runDailyListeningRefreshIfNeeded`、`runSectWeeklyTaskAutoSettlementIfNeeded`、`runDailyFusionPoolCollectIfNeeded`、`runAutoBackupIfNeeded`、`runAutoBackupAttempt`、`pinLatestBackupMessage`、`formatBackgroundStatusReport`、`AUTO_SUSPEND_EXPIRED_USER_LOCAL_FAILED` 源码守护 | 每日生命周期巡检读取成功日期失败时必须跳过本轮巡检，不得把状态读取失败当成今日未巡检后继续自动封禁或自动删除；读取用户列表失败时必须记录并展示最近错误、通知超级管理员且不得推进成功日期；每日生命周期巡检完成后的成功日期和最近错误写入必须检查错误并在失败时通知超管，避免生命周期动作已执行但成功状态未落库；生命周期自动封禁和自动物理删除成功日志中的用户名必须通过 `formatPlainValue` 规范化；每日听书缓存刷新读取上次刷新时间失败时必须跳过本轮刷新，不得把状态读取失败当成从未刷新后继续批量调用 ABS；每日听书缓存刷新完成后的刷新时间、成功数、总数、跳过数和最近错误写入必须检查错误并在失败时通知超管，避免批量 ABS 刷新已执行但完成状态未落库；宗门周目标自动结算读取上次结算周失败时必须跳过本轮结算，不得把状态读取失败当成未结算后继续发放宗门资金或声望；宗门周目标自动结算完成后写入已结算周必须检查错误并在失败时通知超管，避免宗门资金/声望已发放但自动结算周标记未落库；每日天道灵气收集读取成功日期失败时必须跳过本轮注入，不得把状态读取失败当成今日未收集后继续注入天道奖池；后台任务状态读写失败和失败告警通知中的日期、周次和配置 key 必须通过 `formatPlainValue` 规范化；自动备份读取成功日期、重试次数或最近尝试时间失败时必须跳过本轮备份，不得把状态读取失败当成未成功或 0 次重试后继续外发数据库备份；自动备份尝试开始前的最近尝试时间、失败后的重试次数和最近错误写入必须检查错误并在失败时通知超管，避免无法节流重试或失败状态失真；自动备份发送成功后的成功日期、成功时间、消息 ID、重试次数和最近错误写入必须检查错误并在失败时通知超管，避免备份已外发但成功状态未落库；备份置顶交接读取旧置顶消息 ID 失败或解析失败时必须跳过置顶交接，不得静默当成没有旧置顶后继续调用 Telegram；备份置顶成功后写入置顶状态或清理置顶错误失败时必须通知超管人工核查；后台状态必须安全展示生命周期最近错误；后台调度器诊断日志不得使用 raw `err=%v`；ABS 已完成自动封禁但本地状态或成功审计失败时，失败审计必须使用可返回错误的写入路径并在审计再失败时通知超管 |
| `pai_gow_logic_test.go` | `paiGowHandPoint`、`isPaiGowOpenTime`、`isPaiGowBetCommand`、`isDiceOpenTime`、牌九资产流源码守护 | 推牌九牌点规则、开放时段、下注口令和骰子避让时段必须稳定；牌九下注、退款、中奖流水必须齐全，下注记录创建和唯一索引必须存在；启动时必须扫描未结算牌九下注并退款；开奖必须从 DB active 快照按下注时间发牌，用状态 CAS 抢占下注行，开奖结果必须使用 `sendAutoDelete` 参与定时删除且不置顶 |
| `race_logic_test.go` | `calculateHorseRaceBetRange`、`createDiceBetInTx`、`createRaceBetInTx`、`upsertDiceDailyProfitDeltaInTx`、`createDiceDailyProfitInTx`、`updateDiceDailyProfitDeltaInTx`、赛马/骰子结算源代码守护 | 赛马下注范围必须按全服正积分用户平均积分的 3%/15% 计算，最低 3/15，最高下注不超过 500；骰子和赛马下注记录创建必须检查数据库错误和 `RowsAffected`，并规范化下注用户名，防止积分已扣但下注记录未落库；骰子每日净盈利记录的 upsert、首次创建和增量更新都必须检查数据库错误和 `RowsAffected`，防止赔付已发放但每日盈利上限依据未更新；赛马/骰子系统通吃结算必须检查状态更新命中行数，赛马有中奖者结算还必须检查未中奖下注收尾的 `RowsAffected` 与快照人数一致，避免资产发放后下注状态半更新；赛马/骰子协程 panic 兜底日志中的 race_id/dice_id 必须通过 `formatPlainValue` 规范化后输出 |
| `redpacket_logic_test.go` | `handleGrabRedPacket` / `userAlreadyGrabbedAllActiveRedPackets` / `hasActiveIneligibleWorldBossRedPacketTx` / `createRedPacketInTx` / `createRedPacketGrabInTx` 源码守护、`WAITING_RED_POINTS` 源码守护 | 制作积分红包输入金额后的钱包读取失败必须提示重新输入并记录日志，不得误判为余额不足或清空会话；普通积分红包和天道爆池红包创建必须统一走 `createRedPacketInTx`，规范化展示/范围字段并检查数据库错误和 `RowsAffected`，普通红包 ID 唯一冲突仍允许重试，防止用户积分已扣或天道奖池水位已变动但红包记录未落库；红包领取记录创建必须检查数据库错误和 `RowsAffected`，并规范化领取者名称，防止红包剩余份数/余额已扣减、用户已加分但领取明细未落库；红包领取成功后的余额重读和抢空榜读取失败时必须显示读取失败并记录日志，不得误显示为 0 积分或空榜；红包领取失败后的补充状态查询必须返回并处理数据库错误，不得把活跃红包、可领红包或 Boss 专属红包统计读取失败误判为空状态 |
| `referral_logic_test.go` | `referralInviterMeetsCultivationRequirement`、`referralStatsText`、`referralStatsUnavailableText`、`capReferralTrialTaskSeconds`、邀请链接 `/start` 源码守护、`validateReferralCodeForStart`、`ensureReferralCode` 源码守护、`createReferralTrialAccountInTx` / `convertTrialToFormalWithInviteCode` / `claimReferralTrialTask` 源码守护 | 个人邀请拉新门槛必须稳定为炼气初期及以上：凡人不得生成或继续使用邀请资格，炼气初期、炼气更高小境界和筑基及以上均可通过；邀请统计正常展示累计激活、有效新人和本月奖励，统计读取失败时必须显示读取失败，不得把数据库错误当成 0；生成 `t.me` 邀请深链时必须对 `start` payload 做 query escaping，避免历史异常邀请码截断或注入额外 query 参数；新人任务累计秒数必须按试用开始到当前/结束的实际墙钟时长封顶，避免 ABS 日聚合把试用开始前的当日历史听书误算入任务；邀请链接进入新人体验注册前读取既有正式账号失败时必须停止并记录脱敏日志，不得误当成没有账号继续注册；`/start ref_` 预校验和邀请链接注册读取 `ReferralCode` 或邀请者 `User` 时，只有 `gorm.ErrRecordNotFound` 能映射为链接无效或邀请资格不足，数据库错误必须进入稍后重试/通用失败分支；生成邀请链接、试用转正和新人任务领奖读取用户、邀请码或激活记录时，只有 `gorm.ErrRecordNotFound` 能映射成业务不存在，数据库错误必须进入通用失败分支；已存在但被禁用的个人邀请码重新启用、复用旧空档案创建试用账号、试用转正、新人任务领奖时的用户状态更新以及 activation 生效标记都必须检查 `RowsAffected`，避免账号、邀请码、邀请奖励或新人状态半更新 |
| `registration_expiry_logic_test.go` | `registrationExpireAtForExistingUser`、`createRegisteredUserInTx`、`ensureUserWalletInTx`、`ensureUserWallet`、`WAITING_REG_PASS` 源码守护 | 邀请码重新注册已有本地档案时，默认有效期、永久配置和旧有效期合并规则稳定；注册流程首次创建本地 `User` 档案或复用已有本地档案写入新 ABS 账号和正式账号状态时都必须检查 `RowsAffected`，避免 ABS 已开户但本地档案未落库；盲盒等入口为未注册用户初始化幽灵钱包时必须检查 `User` 创建行数，唯一冲突只能重读既有档案，未实际写入不得继续资产流程；钱包初始化事务失败或提交失败时不得向调用方暴露事务内读取/创建的用户档案和展示名 |
| `sect_cave_logic_test.go` | `sectCaveSectRetreatCost`、`calculateSectEffectiveHoursFromSecondsWithRetreat`、`calculateSectRetreatBonusHours`、`calculateSectRetreatBonusHoursForRecord`、`getActiveSectCaveRetreatTx`、`createSectCaveRetreatInTx` 源码守护 | 宗门长时闭关成本为 `60 + 宗门等级 * 10`；洞府闭关只对闭关开始后的新增听书减缓压制，前 4 小时仍全额，4-8 小时段提升到 60%，8 小时后提升到 15%；无新增听书或新增听书仍在前 4 小时时不得产生额外净修为；单条闭关记录的加成必须受闭关持续时长封顶；active 闭关查询必须返回并处理数据库错误，不能把读取失败当成无闭关继续创建个人或宗门闭关；个人闭关和宗门闭关创建记录必须统一走 `createSectCaveRetreatInTx`，规范化模式、状态和启动人名称，并检查数据库错误和 `RowsAffected`，防止个人/宗门声望已扣但闭关记录未落库 |
| `sect_logic_test.go` | `sectShopRenewMonthlyLimit`、`sectShopRenewNextExpireAt`、`sectShopRenewAllowedByExpireLimit`、`sectShopRenewJoinedLongEnough`、`sectShopMonthKey`、`sectPointDescriptionName`、`sectContributionLogReason`、`createSectContributionLogInTx`、`createSectTechnologyLogInTx`、`createSectDailyTaskClaimInTx`、`createSectShopPurchaseInTx`、`formatSectMemberListPage`、`sectMemberListPageMarkup`、`sectClaimExistsForDay`、`sectWeeklySettlementExists`、`querySectWeeklyTaskStats`、`getSectDailyTaskStatuses`、`getSectDailyTaskStatusesTx`、`sectDailyTaskIncompleteText`、`sectErrorCode`、`loadSectMemberByUserInTx`、`loadTargetSectMemberByUserInTx`、`handleSectShopRenew` / `handleExchangeSectContributionForPrestige` / `handleDonateSect` / `handleExitSect` / `handleKickSectMember` / `handleUpgradeSect` / `handleAppointSectRole` / `handleTransferSectOwner` / `highRiskAuditActionSet` 源码守护 | 宗门七日续期的等级月名额固定为 2/3/5/7/10/13/16/20/24/30；已过期账号从当前时间续期，剩余 38 天可兑换、39 天拒绝；加入当前宗门满 7 天才可兑换；月度限额使用北京时间月份键；创建宗门、加入宗门和宗门捐献的积分流水描述中宗门名必须单行化，宗门改名贡献日志里的旧名和新名也必须单行化，通用宗门贡献奖励日志 reason 必须通过 `formatPlainValue` 规范化后落库；所有 `SectContributionLog` 创建必须统一走 `createSectContributionLogInTx`，并检查数据库错误和 `RowsAffected`，防止资产已变动但贡献追踪日志未落库；宗门科技升级日志必须统一走 `createSectTechnologyLogInTx`，规范化用户名并检查数据库错误和 `RowsAffected`，防止资金/声望和科技等级已变动但升级追溯未落库；通用宗门贡献奖励写入宗门声望和成员贡献/周贡献都必须检查 `RowsAffected`，防止贡献日志存在但宗门或成员资产未到账；宗门七日续期扣贡献后写入账号有效期和名额购买绑定必须检查 `RowsAffected`，防止贡献已扣但有效期或追踪记录未落库；宗门商店购买记录必须统一走 `createSectShopPurchaseInTx`，规范化记录类型和日期键，并检查数据库错误和 `RowsAffected`，防止贡献兑换声望或七日续期资产已变动但购买追踪未落库；宗门七日续期恢复封禁账号失败审计必须显式检查，`SECT_SHOP_RENEW` / `SECT_SHOP_RENEW_REACTIVATE` 必须纳入高危审计统计；宗门捐献扣除用户积分后，宗门资金和成员贡献/周贡献写回必须检查 `RowsAffected`，防止积分已扣但宗门资产未落库；贡献兑换声望扣除个人贡献后，宗门声望和个人声望写回必须检查 `RowsAffected`，防止贡献已扣但声望未到账；宗门每日任务领奖创建领取记录必须检查数据库错误和 `RowsAffected`，领取记录创建后，宗门资金/声望和成员贡献/周贡献写回也必须检查 `RowsAffected`，防止领取幂等记录缺失或已标记领奖但资产未到账；宗门成员读取必须通过统一 helper 区分 `gorm.ErrRecordNotFound` 与数据库错误，只有真实不存在才能映射为未入宗或目标不在宗门，数据库错误必须回滚到通用失败分支；宗门成员列表每页固定 30 人，超过 30 人时必须显示页码并提供上一页/下一页按钮；宗门任务页查询今日奖励领取状态和周目标结算状态时必须显式区分未找到与数据库错误，查询异常不得被当成未领取或未结算；宗门周目标统计查询必须返回并处理数据库错误，结算路径不得把统计失败当成 0 进度后误判未达成；宗门每日任务统计查询也必须返回并处理数据库错误，不能把查询失败当成空任务列表或 0 完成度；领取宗门个人任务奖励未满足三项时必须列出未完成项目和当前进度，错误码必须映射到该专门提示分支；重复领取必须映射到已领取提示分支；宗门升级成功后成员上限重读失败必须显示读取失败并记录日志，不得误显示为 0 人；退出/踢出宗门必须检查成员删除和成员计数扣减的 `RowsAffected`，退出删除必须带非宗主角色条件，踢出删除必须带目标当前角色和操作者当前角色条件，职位任命和宗主转让必须检查角色/owner 条件更新的 `RowsAffected`，防止权限状态半更新；宗门主模块诊断日志不得使用 raw `err=%v`，`sect.go` 不得残留典型 mojibake 标记，成员列表、职位任命、宗门商店、七日续期、宗门任务和净修为日志等文案必须保持清晰中文 |
| `sect_lottery_logic_test.go` | `normalizeSectLotteryTitle`、`parseSectLotterySecrets`、`encryptSectLotterySecret`、`decryptSectLotterySecret`、`sectLotteryUserEligibleAt`、`sectLotteryReminderText`、`sectLotteryCreatorSummaryText`、`sectLotteryNonWinnerNoticeText`、`createSectLotteryInTx`、`createSectLotteryPrizeInTx`、`createSectLotteryEntryInTx`、`createSectLotteryWinnerInTx`、`upsertSectLotteryReminderRecord`、`sectLotteryReminderAlreadyDelivered`、`joinSectLottery` / `drawSectLottery` / `deliverSectLotteryWinner` / `markSectLotteryDeliveryFailed` / `main.go` 调度器挂载源码检查 | 宗门抽奖标题和卡密导入规则稳定，重复/空/超长/控制字符卡密会被拒绝；卡密使用 `SECURITY_PEPPER` 派生 AES-GCM 加密，密文不包含明文；参与资格必须为有效未暂停正式账号；成员提醒、中奖私聊、未中奖通知、创建者开奖摘要以及列表/详情页里的宗门名、活动名等动态展示文本必须单行化；成员提醒文案必须包含宗门、活动、活动 ID、历史贡献门槛和参与命令；创建者开奖摘要必须包含中奖名单和投递状态且不展示卡密明文；未中奖参与者通知必须说明开奖和未中奖；活动、奖品、报名和中奖记录创建必须统一走对应 `createSectLottery*InTx` helper，规范化动态文本并检查数据库错误和 `RowsAffected`，报名唯一冲突必须仍映射为已参加，中奖唯一冲突必须仍跳过该候选；报名参与数回写、开奖最终状态回写以及发奖成功和失败状态写回都必须检查 `RowsAffected`，避免并发状态变化、卡密已发送但状态被静默覆盖或重复补发；发奖前重读活动或奖品失败必须记录脱敏诊断；提醒状态 upsert、提醒失败写入 `last_error`、发奖失败写入 `delivery_error` 前都必须通过 `formatPlainValue` 规范化原因并检查数据库错误和 `RowsAffected`，避免 Telegram 诊断文本或历史异常值破坏排障展示，或补发去重状态静默丢失；提醒去重状态读取失败必须跳过本次投递，不得当成未提醒继续私聊；提醒私聊成功但状态写回失败时投递结果不得统计为成功；创建、开奖、取消审计 detail 和开奖 `result_note` 中的标题、模式、开奖原因等动态字符串必须通过 `formatPlainValue` 规范化；启动入口必须挂载宗门抽奖定时开奖调度器 |
| `sect_lottery_logic_test.go` | 宗门抽奖资格读取源码守护 | 报名、创建者上下文、详情加载和用户资格校验必须区分记录不存在与数据库错误；开奖候选资格复核中，业务性不符合资格可以跳过候选，数据库错误必须返回并回滚开奖，避免读取失败导致少发奖 |
| `sect_secret_realm_logic_test.go` | `calculateSectSecretRealmRewards`、`calculateSectSecretRealmRewardsForRealm`、`calculateSectSecretRealmRewardsForProfile`、`calculateSectSecretRealmRewardPointsForProfile`、`sectSecretRealmPointDescriptionName`、`sectSecretRealmWeeklyOpenWindow`、`sumSectSecretRealmRawListeningSeconds`、`calculateSectSecretRealmRawDeltaSeconds`、`calculateSectSecretRealmSuppressedHours`、`calculateSectSecretRealmSuppressedHoursForProfile`、`sectSecretRealmRewardMultiplierForMajor`、`sectSecretRealmGuardianBonusPercentForMajor`、`applySectSecretRealmHourBonus`、`sectSecretRealmGuardian`、`sectSecretRealmDropForScore`、`sectSecretRealmDropForScoreWithProfile`、`defaultSectSecretRealmConfig`、`normalizeSectSecretRealmProfile`、`getSectSecretRealmProfileCost`、`sectSecretRealmProfileFromSnapshotChecked`、`canJoinSectSecretRealmAt`、`handleJoinSectSecretRealm`、`handleOpenSectSecretRealm`、`loadSectSecretRealmConfigChecked`、`refreshSectSecretRealmLiveProgress`、`settleSectSecretRealm`、`grantSectSecretRealmRewards`、`ensureSectSecretRealmLiveBoardSync` 源码守护 | 宗门秘境奖励门槛、固定积分公式、固定积分境界加成、贡献/声望境界倍率、向下取整和单次上限稳定，积分奖励流水描述和开启秘境贡献日志中的秘境名必须单行化；普通开启消耗默认 `100 + 宗门等级 * 30`，普通/高阶/限时有效门槛默认 0.2/0.75/0.2 小时，旧默认经济配置会归一到当前默认值且自定义值可保留；每周开启窗口使用北京时间周一到下周一；秘境原始听书秒数汇总、墙钟封顶和前 2 小时全额/之后 50% 的普通秘境压制曲线稳定；高阶秘境档位门槛、成本、压制曲线、贡献/声望倍率和掉落概率稳定；护道者只按最高境界选择且筑基/结丹/元婴及以上加成分别为 3%/6%/10%；秘境掉落池、有效门槛和概率边界稳定；秘境仅在 active 且当前时间处于开始和结束之间时可参加；开启秘境、查看秘境配置和写入秘境配置读取配置失败时必须中止，不得按默认配置继续扣宗门资金、展示误导配置或覆盖写入；开启秘境事务触碰宗门行必须检查 `RowsAffected`，未命中必须回滚；写入秘境配置审计 detail 中的管理员原因、档位 key、档位名称和掉落物品名必须通过 `formatPlainValue` 规范化；进入秘境、实时刷新和结算必须使用开启时保存的配置快照，快照缺失或损坏时不得回退当前配置或默认配置继续参与、刷新奖励估算或发奖，结算必须回滚为 active 等待修复；进入秘境、实时刷新和结算读取本地档案失败不得误报为未绑定，结算遇到数据库读取错误必须回滚等待重试，成功后参与记录重读失败必须显示读取失败并记录日志，不得误显示为凡人或 0 小时；最终结算奖励集合必须排除本地档案缺失或已解绑 ABS 的参与者，清零其结算奖励并检查 `RowsAffected`，护道者也只能从有效参与者中选择；秘境实时刷新事件汇总写回、秘境贡献和声望资产更新都必须检查 `RowsAffected`，防止实时状态或奖励流水与资产落库不一致；宗门秘境实时榜消息 ID 写回必须检查数据库错误和 `RowsAffected`；后台扫描进行中和到期秘境查询失败必须记录脱敏日志并跳过本轮，不得静默返回；宗门秘境诊断日志不得使用 raw `err=%v`，实时刷新诊断文本必须保持可读 UTF-8 |
| `sect_secret_realm_logic_test.go` | 宗门秘境命令入口源码守护 | 宗门秘境状态、开启、进入、手动结算、排行和明细入口读取宗门成员档案时必须区分未加入宗门与数据库错误；宗门档案、手动结算活动和明细参与记录读取失败时也必须区分未找到与数据库错误，不得用业务不存在提示掩盖 DB 故障 |
| `sect_horn_logic_test.go` | `createSectHornBroadcastInTx`、`createSectHornDeliveriesInTx`、`sect_horn.go` 源码守护 | 宗门/世界喇叭预览和确认创建读取操作者成员档案时，只有 `gorm.ErrRecordNotFound` 能映射为未加入宗门，数据库错误必须中止，不得继续扣积分或创建投递队列；投递器诊断日志必须使用 `formatPlainError`，Telegram 发送失败必须使用 `formatTelegramSendError`，不得使用 raw `err=%v`；扣除喇叭积分后，广播主记录必须统一走 `createSectHornBroadcastInTx` 并检查数据库错误和 `RowsAffected`，投递明细必须统一走 `createSectHornDeliveriesInTx` 并要求批量创建行数等于收件人数，防止积分已扣但投递任务缺失或不完整；投递明细写回 `sent` 或 `failed` 后必须检查 `RowsAffected` 并记录状态竞争标记，避免 Telegram 已发送但本地状态未命中时静默等待重复投递；广播总状态完成/失败写回必须带状态条件并检查 `RowsAffected`，失败 `last_error` 写入前必须通过 `formatPlainValue` 规范化原因，避免 Telegram 诊断文本或历史异常值破坏排障展示；完成状态写入后重读广播准备发送回执失败时必须记录脱敏诊断，不得静默返回；宗门/世界喇叭积分流水描述中的宗门名必须单行化，避免历史异常宗门名打乱流水展示 |
| `sect_weekly_task_logic_test.go` | `calculateSectWeeklyTaskReward`、`sectWeeklyTaskExcessPercent`、`sectWeekKey`、`sectWeeklyTaskAutoSettlementTargetWeek`、`createSectWeeklyTaskSettlementInTx`、`settleSectWeeklyTaskRewardForSectTx` 源码守护 | 宗门周目标固定为签到 100、净修为 200 小时、个人任务 60 次；达成 1/2/3 项的宗门资金和声望基础奖励稳定；超额百分比按单项封顶 200%、资金 x0.8、声望 x0.3 向下取整；北京时间自然周周一 key 稳定；自动结算只在北京时间周一 09:00 后触发并结算上一周；周目标结算记录创建必须统一走 `createSectWeeklyTaskSettlementInTx`，规范化周 key 和结算人名称，保留唯一冲突到已结算业务错误的映射，并检查数据库错误和 `RowsAffected`；周目标结算创建结算记录后，宗门资金/声望写回必须检查 `RowsAffected`，防止结算记录和流水存在但资产未到账 |
| `sign_in_logic_test.go` | `signInDateKey`、`signInMonthKey`、`signInDayInCycle`、`calculateCycleSignReward`、`calculateSignStreakReward`、`createSignInLogInTx`、`createSignInRewardClaimInTx`、`handleUserSignIn` 源码守护 | 签到日期和月份 key 必须使用北京时间边界；30 天奖励周期第 31 天回到第 1 天、第 33 天再次命中第 3 天奖励；连签奖励只在 3/7/14/21/30 天发放且随机奖励落在文档区间；旧月度全勤奖励必须按当月实际天数计算并设置已领奖标记，重复调用不得重复发同一档奖励；连续签到档案写回必须按旧状态条件更新并检查 `RowsAffected`，不得用整行 `Save` 覆盖并发状态；签到日志创建必须检查数据库错误和 `RowsAffected`，防止连续签到档案已更新但签到追踪未落库；连签奖励幂等领取记录创建必须规范化描述和 ref，并检查数据库错误和 `RowsAffected`，防止领奖记录未落库却继续发放奖励积分；用户主档 `last_sign_at` 写回也必须检查 `RowsAffected`，避免签到积分和主档时间半更新；`last_sign_date` 超前异常日志中的日期 key 必须通过 `formatPlainValue` 规范化 |
| `telegram_diagnostics_test.go` | `formatTelegramSendError`、`formatTelegramMetricError`、`isTerminalTelegramUnpinError`、启动诊断源码守护 | Telegram Bot API 网络错误中的 `https://api.telegram.org/bot<TOKEN>/method` 必须脱敏为 `bot***:***/method`，避免 Bot Token 从容器日志或诊断文本泄漏；运行指标错误摘要中的 endpoint 必须通过 `formatPlainValue` 规范化后再进入后台状态面板；Bot 初始化失败日志必须使用 Telegram 错误脱敏 helper，启动成功日志和 `bot_startup_health.txt` 中的 Bot 用户名必须通过 `formatPlainValue` 规范化；取消置顶旧消息时，消息不存在、已非置顶、无置顶权限等终态错误可不重试，超时、限流和 5xx 必须视为非终态并记录 |
| `testing_docs_test.go` | `docs/agent/testing.md` 测试文件清单 | 仓库实际 `*_test.go` 必须全部记录在本文档表格中，文档也不得保留已经不存在的测试文件名，防止测试覆盖说明漂移 |
| `ui_test.go` | `userMainMenuReplyMarkup`、`gardenInlineMarkupWithMiniApp` | Mini App 启用时，用户主菜单药园入口仍必须是 `药园` 普通文本按钮，不得携带 `web_app`；点击面板或发送药园后仍保留原 inline 文字交互按钮，并在按钮区追加 `打开药园` WebApp 内联按钮；Mini App 关闭时不追加 WebApp 按钮，避免 Telegram 客户端面板兼容差异 |
| `wealth_leaderboard_logic_test.go` | `formatWealthLeaderboardPage`、`wealthLeaderboardPageMarkup`、财富榜群聊入口和 callback 分发源码守护 | 财富榜必须统计普通用户积分 Top 50，每页最多 30 人并显示页码；用户名进入 Markdown 前必须转义；群聊提前返回前必须处理 `财富榜` / `积分榜` 并登记原始群命令自动删除；分页只能通过 `wealth_page:` inline 按钮触发，callback 必须在普通菜单 callback 之前分发，避免群内按钮被私聊菜单限制拦截 |
| `world_boss_logic_test.go` | `newWorldBossRedPacket`、`worldBossRedPacketTotalPoints`、`worldBossRedPacketCount`、`worldBossPointDescriptionName`、`canJoinWorldBossAt`、`worldBossJoinDeadline`、`worldBossEventExists`、`createWorldBossEventRecord`、`createWorldBossParticipantInTx`、`createWorldBossRedPacketInTx`、`handleJoinWorldBoss`、`handleWorldBossStatus`、`handleWorldBossRank`、`sendWorldBossSettlement`、`renderWorldBossLiveBoard`、`refreshWorldBossStoredHPByParticipants`、`refreshWorldBossLiveDamage` / `settleWorldBoss` / `grantWorldBossRewards` / `awardWorldBossSectRewardTx` / `resetWorldBossToActive`、`ensureWorldBossLiveBoardSync` 源码守护 | 世界 Boss 击杀红包固定为 30 积分 / 10 份；Boss 红包必须记录 `ref_type=world_boss`、`ref_id=boss_id` 和 `claim_scope=world_boss_participant`，确保领取范围限定为本期 Boss 参与者；世界 Boss 最后 15 分钟开始禁止新增参加；降临公告、参加回执、状态页、排行榜、实时战榜和结算公告中的 Boss 名称必须转义 Markdown，伤害公式展示必须包含 `1 + 修为加成 + 宗门科技`；世界 Boss 启动前检查本期事件是否已存在时必须显式区分未找到与数据库错误，查询失败不得继续创建事件或发送降临通知；世界 Boss 事件创建必须走 `createWorldBossEventRecord` 并检查数据库错误和 `RowsAffected`，避免未实际落库却继续发送降临公告；`参加Boss` 创建参与记录必须检查数据库错误和 `RowsAffected`，重复参加 0 行写入只能按幂等结果处理且不得覆盖首次参加基线；击杀红包创建必须走 `createWorldBossRedPacketInTx` 并检查数据库错误和 `RowsAffected`，避免结算奖励、天道奖池注入和红包追踪不一致；`参加Boss` 和实时刷新读取本地档案失败不得误报为未绑定，实时刷新遇到数据库错误必须记录日志并沿用旧伤害，成功后参与记录重读失败必须显示读取失败并记录日志，不得误显示为 0 小时；参与后即时血量刷新、实时刷新和结算写回 Boss 伤害、血量和参与人数必须检查 `RowsAffected`；Boss 奖励标记、成员贡献和宗门声望更新都必须检查 `RowsAffected`，防止奖励流水写入但资产或幂等状态未到账，积分奖励流水描述中的 Boss 名必须单行化；结算成功后事件重读失败必须用本次结算状态、剩余血量、击杀状态和参与人数补齐公告快照；实时战榜消息 ID 写回必须检查数据库错误和 `RowsAffected`；后台扫描进行中和到期 Boss 查询失败必须记录脱敏日志并跳过本轮，不得静默返回；结算失败回滚 `settling -> active` 必须检查 `RowsAffected` 并记录脱敏日志；世界 Boss 诊断日志不得使用 raw `err=%v`，实时刷新诊断文本必须保持可读 UTF-8 |

> 涉及 `AppConfig` 的测试必须在 `t.Cleanup` 中恢复原值，避免污染其他用例；当前测试均不打开数据库、不调用 Telegram/ABS 网络接口。

## 播放异常风控测试

`listening_abuse_logic_test.go` 覆盖播放异常风控的纯逻辑边界：后台扫描日期必须使用北京时间昨天，`previousDayKey` 必须正确跨月回退，生效日日期键必须规范且支持按 `YYYY-MM-DD` 比较，单日阈值为严格大于 `15` 小时，连续两天都超过阈值才冻结，账号到期边界必须阻止自动解冻；warning/freeze 记录创建必须检查行数，0 行写入只能按幂等重复处理；warning 记录和 `LISTENING_ABUSE_WARNING` 审计必须同事务提交，私聊提醒必须在事务提交后发送；冻结/恢复失败审计必须走可检查错误的写入路径，并在审计写入失败时通知超级管理员；自动解冻被阻止的审计原因必须通过 `formatPlainValue` 规范化；读取生效日或末次扫描日失败时必须 fail closed，不能重置生效日、解冻、既往不咎恢复或继续扫描；初始化生效日或扫描完成状态写入失败时必须通知超级管理员，不能把写入失败静默吞掉后继续巡检或误报完成；播放异常记录的每日动作和 active freeze 两条部分唯一索引必须由启动迁移替换同名旧全量索引。

## 注册有效期测试

`registration_expiry_logic_test.go` 覆盖邀请码重新注册已有本地档案时的有效期合并规则：旧有效期为空、过期或短于本次默认注册有效期时必须补到默认有效期；旧有效期更长时必须保留；`ACCOUNT_VALID_DAYS=0` 的永久默认配置必须能清空旧的有限到期时间。

## 待补测试方向

当前仓库测试覆盖仍然很薄，后续应优先补齐：

- 每日任务领奖事务并发（需 SQLite/cgo 环境）。

## 扩展约定

- 新增**纯逻辑**资产/安全/时区函数时，应同步在对应测试文件补用例；改动经济规则、伤害系数、奖励区间、时区口径时，**必须**先更新或确认相关断言。
- 需要数据库的事务/并发/唯一索引测试请单独放在 cgo 可用环境（Docker）下运行，不要混入当前无 cgo 套件，以免破坏宿主机门禁的可运行性。
- 改动测试文件清单时，必须同步更新本文档表格，保持「文档所列文件」与仓库实际 `*_test.go` 一致，避免文档声称存在但实际缺失的回归套件。
- `scripts/agent_audit.ps1` 属于无人值守门禁的一部分，新增规则时必须带脚本内自检用例；当前审计会阻断源码中的乱码/占位符式用户可见或诊断文本，避免日志和提示退化成不可运维的 `??`、替换符或误解码片段。

## 2026-06-22 GitHub 福利领奖审计守护补充

`github_benefit_test.go` 新增 `TestGithubBenefitClaimAuditDetailsUsePlainValue`，用于守护 GitHub 福利邀请码/续期卡领奖审计 detail 中的 GitHub login 和卡密预览必须通过 `formatPlainValue` 折叠控制字符、脱敏并限长，避免外部账号字段或卡密追踪字段打乱高危资产发放审计展示。

## 2026-06-23 活动 settling 恢复守护

`world_boss_logic_test.go` 新增 `TestWorldBossStaleSettlingRecoveryIsWired`，`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmStaleSettlingRecoveryIsWired`，用于守护世界 Boss 和宗门秘境后台扫描器必须先恢复超过 30 分钟仍未 `settled` 的陈旧 `settling` 活动为 `active`，再交给既有刷新/结算路径重试，避免进程在结算抢占后崩溃导致活动永久卡住且奖励不再发放。

## 2026-06-23 世界 Boss 实时与结算伤害集合守护补充

`world_boss_logic_test.go` 新增 `TestWorldBossSettlementRewardParticipantsMatchDamageTotal`，并扩展实时刷新用户读取守护，用于守护世界 Boss 实时刷新和最终奖励集合必须与仍有本地档案且已绑定 ABS 的有效参与者一致；本地账号缺失或已解绑 ABS 的参与记录必须清零计算伤害、检查 `RowsAffected`，最终结算还必须从奖励排序中排除，避免陈旧参与记录保留旧伤害后继续展示或获得积分、宗门贡献或声望。

## 2026-06-23 宗门秘境结算奖励集合守护补充

`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmSettlementRewardParticipantsMatchEligibleSet`，并扩展实时刷新用户读取守护，用于守护宗门秘境实时刷新和最终奖励集合必须与仍有本地档案且已绑定 ABS 的有效参与者一致；失效参与记录必须清零计算奖励和掉落、检查 `RowsAffected`，最终结算还必须从护道者选择和奖励发放集合中排除，避免旧实时奖励残留后继续展示或发放积分、宗门贡献、声望或背包物品。

## 2026-06-23 药园软删除唯一索引守护补充

`garden_logic_test.go` 新增 `TestGardenLimitMigrationsReplaceFullUniqueIndexes`，用于守护药园每日种子限购、药草急收限额和丹方解锁迁移必须把同名旧全量唯一索引替换为 `deleted_at IS NULL` 部分唯一索引，避免软删除历史记录阻塞新的有效限购或解锁记录。

## 2026-06-23 丹药额度软删除唯一索引守护补充

`cultivation_logic_test.go` 扩展 `TestPillUsageQuotaCreateChecksRowsAffected` 并新增 `TestItemUsageQuotaMigrationCreatesPartialUniqueIndex`，用于守护 `ItemUsageQuota` 初始化 upsert 的 conflict target 必须匹配 `deleted_at IS NULL` 部分唯一索引，启动迁移也必须能替换同名旧全量唯一索引。

## 2026-06-23 宗门七日续期领取索引守护补充

`sect_logic_test.go` 新增 `TestSectShopRenewClaimUsesPartialUniqueIndexes`，用于守护 `SectShopRenewClaim` 不再通过 GORM tag 生成全量唯一索引，启动迁移必须创建 `deleted_at IS NULL` 部分唯一索引，并且领取名额预占必须使用不指定冲突列的 `ON CONFLICT DO NOTHING`。

## 2026-06-23 签到软删除唯一索引守护补充

`sign_in_logic_test.go` 新增 `TestSignInMigrationsReplaceFullUniqueIndexes`，用于守护月度连签、全局连签、每日签到日志和连签奖励领取记录的唯一索引必须由启动迁移替换为 `deleted_at IS NULL` 部分唯一索引，避免软删除历史记录误挡新的签到积分发放或奖励幂等记录。

## 2026-06-23 邀请拉新软删除唯一索引守护补充

`referral_logic_test.go` 新增 `TestReferralMigrationsReplaceFullUniqueIndexes`，用于守护 `ReferralCode(user_id)`、`ReferralCode(code)` 和 `ReferralActivation(invitee_id)` 的唯一索引必须由启动迁移替换为 `deleted_at IS NULL` 部分唯一索引，避免软删除历史归因码或激活记录误挡新的邀请链接、试用注册和归因流程。

## 2026-06-23 GitHub 福利领取索引守护补充

`github_benefit_test.go` 新增 `TestGithubBenefitClaimedMigrationsReplaceFullUniqueIndexes`，用于守护 GitHub 福利 claimed TG/GitHub 防重唯一索引必须由启动迁移替换为带 `status='claimed'` 和 `deleted_at IS NULL` 条件的部分唯一索引，避免旧全量索引误挡 pending/expired 历史记录或软删除记录后的重新处理。

## 2026-06-23 宗门任务奖励索引守护补充

`sect_logic_test.go` 新增 `TestSectTaskRewardMigrationsReplaceFullUniqueIndexes`，用于守护宗门每日任务领奖和宗门周目标结算的幂等唯一索引必须由启动迁移替换为 `deleted_at IS NULL` 部分唯一索引，避免软删除历史领取/结算记录误挡新的宗门资金、声望和贡献奖励发放。

## 2026-06-23 活动参与中奖索引守护补充

`world_boss_logic_test.go` 新增 `TestWorldBossParticipantMigrationReplacesFullUniqueIndex`，`lottery_logic_test.go` 新增 `TestLotteryEntryMigrationsReplaceFullUniqueIndexes`，用于守护世界 Boss 参与、积分抽奖参与和积分抽奖中奖记录的幂等唯一索引必须由启动迁移替换为 `deleted_at IS NULL` 部分唯一索引，避免软删除历史参与/中奖记录误挡新的活动参与、开奖和奖励追踪。

## 2026-06-23 药园灵田种植索引守护补充

`garden_logic_test.go` 新增 `TestGardenPlotMigrationsReplaceFullUniqueIndexes`，用于守护药园灵田 `garden_plots(user_id, plot_no)` 和生长中种植 `garden_plantings(plot_id)` 的唯一索引必须由启动迁移替换为带 `deleted_at IS NULL` 条件的部分唯一索引，其中种植记录还必须限定 `status = 'growing'`，避免软删除灵田或历史种植记录误挡初始化、开垦和播种事务。

## 2026-06-23 宗门洞府闭关索引守护补充

`sect_cave_logic_test.go` 新增 `TestSectCaveRetreatMigrationReplacesFullUniqueIndex`，用于守护 active 闭关 `sect_cave_retreats(user_id)` 的唯一索引必须由启动迁移替换为 `deleted_at IS NULL AND status = 'active'` 部分唯一索引，避免已结束或软删除闭关记录阻塞新的个人/宗门闭关。

## 2026-06-23 播放异常风控索引守护补充

`listening_abuse_logic_test.go` 新增 `TestListeningAbuseRecordMigrationsReplaceFullUniqueIndexes`，用于守护播放异常记录 `listening_abuse_records(user_id, day_key, action)` 和 active freeze `listening_abuse_records(user_id)` 的唯一索引必须由启动迁移替换为部分唯一索引，避免软删除记录或已解除冻结记录误挡新的 warning/freeze 幂等记录。

## 2026-06-23 一致性迁移部分唯一索引守护补充

`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmParticipantMigrationReplacesFullUniqueIndex`，`sect_horn_logic_test.go` 新增 `TestSectHornDeliveryMigrationReplacesFullUniqueIndex`，`marketplace_logic_test.go` 新增 `TestMarketplacePurchaseSecretMigrationReplacesFullUniqueIndex`，用于守护宗门秘境参与、宗门喇叭投递和交易行卡密订单绑定的部分唯一索引必须由一致性启动迁移替换同名旧全量唯一索引，避免软删除历史记录阻塞新的参与、投递或购买资产记录。

## 2026-06-23 宗门抽奖索引补迁移守护补充

`sect_lottery_logic_test.go` 新增 `TestSectLotteryMigrationsReplaceFullUniqueIndexes`，用于守护宗门抽奖报名、中奖用户和中奖奖品三条幂等部分唯一索引必须通过统一 helper 替换同名旧全量唯一索引；同时要求新增 `20260623_sect_lottery_partial_unique_indexes` 补迁移，确保已经执行旧 `20260618_sect_lottery_indexes` 的生产库也会重建索引定义。

## 2026-06-23 敏感卡密和安全锁索引守护补充

`maintenance_logic_test.go` 新增 `TestSensitiveCodeHashMigrationsUsePartialUniqueIndexes`，用于守护邀请码和续期卡 `code_hash` 唯一索引必须只约束 `code_hash <> '' AND deleted_at IS NULL` 的有效行，并通过统一 helper 替换旧索引定义。`admin_input_logic_test.go` 新增 `TestSecurityAttemptLockMigrationReplacesFullUniqueIndex`，用于守护 `SecurityAttemptLock(user_id, purpose)` 唯一索引必须通过补迁移替换同名旧全量唯一索引，避免软删除安全锁记录阻塞新的失败次数记录。

## 2026-06-23 宗门科技和秘境 active 索引守护补充

`sect_logic_test.go` 新增 `TestSectTechnologyMigrationReplacesFullUniqueIndex`，用于守护宗门科技 `sect_technologies(sect_id, tech_key)` 唯一索引必须由启动迁移替换为 `deleted_at IS NULL` 部分唯一索引。`sect_secret_realm_logic_test.go` 新增 `TestSectSecretRealmActiveMigrationReplacesFullUniqueIndex`，用于守护同宗门 active 秘境 `sect_secret_realm_events(sect_id)` 唯一索引必须由启动迁移替换为 `status = 'active' AND deleted_at IS NULL` 部分唯一索引，避免旧全量索引误挡软删除或已结束历史记录后的新创建。

## 2026-06-23 ABS 绑定和小境界配置索引守护补充

`account_binding_logic_test.go` 新增 `TestUsersAbsUserIDMigrationReplacesFullUniqueIndex`，用于守护 `users(abs_user_id)` 唯一索引必须由启动迁移替换为 `abs_user_id <> '' AND deleted_at IS NULL` 部分唯一索引。`cultivation_config_logic_test.go` 新增 `TestCultivationMinorRealmConfigMigrationReplacesFullUniqueIndex`，用于守护小境界配置 `cultivation_minor_realm_configs(major_realm, minor_realm)` 唯一索引必须由启动迁移替换为 `deleted_at IS NULL` 部分唯一索引，避免旧全量索引阻塞有效绑定或配置重建。

## 2026-06-23 宗门成员归属索引守护补充

`sect_logic_test.go` 新增 `TestSectMemberUserMigrationReplacesFullUniqueIndex`，用于守护 `SectMember.UserID` 不再通过 GORM tag 生成全量唯一索引，启动迁移必须把旧 `idx_sect_members_user_id` 替换为 `sect_members(user_id) WHERE deleted_at IS NULL` 部分唯一索引，避免退出或踢出后的软删除成员记录阻塞用户重新加入宗门。

## 2026-06-23 修仙配置索引和补种守护补充

`cultivation_config_logic_test.go` 新增 `TestCultivationConfigMigrationsReplaceFullUniqueIndexes`，用于守护 `CultivationRealmConfig.MajorRealm` 和 `BreakthroughConfig.FromMajorRealm` 不再通过 GORM tag 生成全量唯一索引，启动迁移必须替换为 `deleted_at IS NULL` 部分唯一索引，并在索引修复后补跑默认配置种子，避免旧全量索引曾经让缺失的大境界、突破或小境界默认配置静默跳过。

## 2026-06-23 部分唯一索引定义比对守护补充

`maintenance_logic_test.go` 新增 `TestSQLiteIndexDefinitionComparisonRequiresExactPredicate`，用于守护统一索引迁移 helper 必须规范化并比对完整 SQLite 索引定义，而不是只检查 `deleted_at IS NULL`。测试覆盖等价的 `IF NOT EXISTS` 差异可通过，但缺少 `status='active'`、缺少 `code_hash <> ''` 或索引列错误必须判定为不匹配，确保启动迁移能替换同名但谓词不完整的旧索引。

## 2026-06-23 交易行特殊索引迁移守护补充

`marketplace_logic_test.go` 新增 `TestMarketplaceSpecialMigrationsReplaceStaleUniqueIndexes`，用于守护交易行 open 争议和 available 卡密哈希两条带重复数据处理的特殊索引迁移仍必须通过统一 helper 替换同名旧索引；open 争议重复继续 fail closed，可售卡密重复继续保留最早一条并关闭其余记录。

## 2026-06-23 交易行购买资产事务守护补充

`marketplace_logic_test.go` 新增 `TestMarketplacePurchaseTransactionKeepsAssetGuards`，并扩展 `TestMarketplacePurchaseSoldCountChecksRowsAffected`，用于守护交易行购买事务必须在事务内重新读取 active 商品、校验卖家一致性、校验价格、锁定可售单位、复核 Bot 已校验卡密可用性、完成买家扣款、卖家入账、手续费注入和订单创建；成交计数回写也必须要求商品仍为 active 并检查 `RowsAffected`，避免状态变化后仍静默推进成交追踪。

## 2026-06-24 兑换与红包事务结果发布补充

`redpacket_logic_test.go` 新增 `TestExchangeAndRedPacketTransactionResultsPublishAfterSuccess`，用于守护用户兑换邀请码/续期卡时，明文卡密只能在扣积分、卡密记录和流水事务成功提交后发布到成功回执变量；发放积分红包时，红包 ID 只作为事务内临时状态参与红包创建和 `redpacket_send` 流水写入，避免事务失败或提交失败后误用未提交卡密或红包编号。

## 2026-07-03 交易行底价与积分兑换续期卡归属守护

`marketplace_logic_test.go` 新增 `TestMarketplaceOriginalValueFloor` 和 `TestMarketplacePointExchangeRenewGuards`，用于守护交易行上架价不得低于系统原价值 85%、可识别背包物品原价值口径、积分兑换续期卡只能由当前持有人上架、购买成交时在事务内转移续期卡归属给买家。`business_errors_logic_test.go` 扩展续期卡错误码映射，确保非持有人核销积分兑换续期卡时返回稳定业务码。2026-07-06 起该守护扩展为系统可识别原价值商品上架价必须处于 85%-115%。

## 2026-07-06 交易行限价、手续费与强制下架守护

`marketplace_logic_test.go` 扩展 `TestMarketplaceOriginalValueFloor`、`TestMarketplacePointExchangeRenewGuards`，并新增 `TestMarketplaceFeeUsesFivePercent` 和 `TestMarketplaceForceCloseRequiresSuperAdminAndAudit`，用于守护交易行手续费为 5%、系统可识别原价值商品上架价不得超过 115%、超价错误码和文案稳定，以及 `强制下架商品 商品ID` 必须限超级管理员私聊、合规原因、二次确认，并在关闭商品/未售单位/退回未售背包库存的同一事务内写入 `FORCE_CLOSE_MARKETPLACE_LISTING` 审计。

## 2026-07-10 药园 Mini App 场景与真实目录守护

`garden_logic_test.go` 扩展 `TestGardenMiniAppFarmLayoutSourceGuards`，用于守护灵田页保留巡园指引和快捷操作坞，并要求 localhost `?mock=1` 预览覆盖生产配置中的 8 种灵草与 6 张丹方名称，拒绝重新引入紫纹灵芝、青元草、玄霜花等非生产目录占位名。浏览器验证需覆盖 `320 / 390 / 760 / 1280` 宽度、五个底部页面、无横向滚动和无控制台错误；视觉截图只用于人工检查排版，不替代资产事务测试。

该守护同时要求生长期作物按真实物品 key 调用 SVG 图标，并保留作物摆动、灵气上升、批量收获、丹炉火焰、丹炉烟气和选中物品浮动等关键动画定义。Playwright 验证还需在 `prefers-reduced-motion: reduce` 下确认统一降级规则生效，动画存在不代表资产动作成功。

炼丹视觉守护要求存在六种丹药配色/纹样映射、丹方图谱、炉心图、完整丹炉结构、悬丹和符阵动画，并明确禁止 `pillLogoSVG` 重新使用 SVG `<text>` 字形充当丹药主图。浏览器检查需确认六张丹方均渲染图谱、丹药 SVG 无文字节点，且 `320px` 窄屏下炉体与名称不重叠。

## 2026-07-03 牌九提示与开奖结果定时删除守护

`pai_gow_logic_test.go` 扩展牌九源码守护，要求牌九开局、状态和下注成功提示包含更完整的规则、剩余下注时间、桌面人数和总注信息，并要求开奖结果使用 `sendAutoDelete` 参与定时删除且不置顶。
## 2026-07-12 当日听书误刷满 24 小时回归守护

`daily_listening_live_test.go` 新增历史会话缺少播放状态时不得默认为播放中的测试，防止 `/api/users/{id}/listening-sessions` 返回的多条历史记录同时按墙钟补算。

`daily_listening_sync_test.go` 新增官方日统计修复测试：ABS 成功返回非 nil 的空 `days` 对象时，必须生成北京时间当天 0 值 upsert 记录，清零旧 `live_raw_seconds` 并恢复 `abs_days` / `ok` / `abs_refresh` 口径；同时静态守护每日净修为写入链路不得再次调用历史会话补算或跨日重分配。
