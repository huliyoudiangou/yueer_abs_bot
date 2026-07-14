# Agent Maintenance Notes

## 架构师式维护基石

长期维护本项目时，Agent 必须按 `docs/agent/architect.md` 的中文准则工作：真实准确、不编造证据；按风险分层处理任务；区分事实、假设、推断和待确认信息；优先交付可执行、可验证的小范围改动；涉及资产、权限、迁移、外部副作用或敏感信息时必须保守，并在完成前用当前代码、文档、命令输出或测试结果证明目标已满足。

该准则是 `AGENTS.md` 的补充，不替代本文件后续更细的业务维护约束。若两者冲突，以更具体、更保守、更能保护生产资产和用户数据的规则为准。

## 2026-07-12 ABS API request identification and Cloudflare compatibility

`AbsClient.sendRequest` must set the stable, project-specific `User-Agent` `YueErShengYue-ABS-Bot/1.0` for every Audiobookshelf API request. Do not rely on Go's generic `Go-http-client/1.1` default because an upstream Cloudflare/WAF policy may block it and return HTTP 403 for account status and listening-report requests even when the ABS API key and user data are valid. Requests without a body, including GET, must not include `Content-Type: application/json`; set that header only when sending an actual JSON body. After a deployment, verify the response status from inside the bot container without printing the ABS response body, and confirm the account-status and listening-report logs no longer report 403. Never log or display ABS response fields that can include session tokens.

## 2026-06-23 SystemConfig 部分唯一索引

`SystemConfig` 嵌入 `gorm.Model`，配置 key 的唯一性必须只约束 `deleted_at IS NULL` 的有效配置行。模型字段不得使用 GORM `uniqueIndex` tag 生成全量唯一索引；启动一致性迁移必须用 `ensureSystemConfigKeyPartialUniqueIndex` 替换 `idx_system_configs_key` 的旧全量唯一索引或普通索引。所有按 key 创建或 upsert 的配置写入必须复用统一冲突目标 helper，`ON CONFLICT (key)` 必须带 `TargetWhere deleted_at IS NULL`，避免 SQLite 部分唯一索引冲突目标不匹配，也避免软删除历史配置阻塞后台状态、价格、线路、奖池和活动配置重建。

## 2026-06-23 事件 ID 部分唯一索引

`SectHornBroadcast.HornID`、`SectSecretRealmEvent.RealmID` 和 `WorldBossEvent.BossID` 嵌入 `gorm.Model`，模型字段不得使用 GORM `uniqueIndex` tag 生成全量唯一索引。事件 ID 唯一性必须由启动一致性迁移创建 `deleted_at IS NULL` 部分唯一索引，并能替换同名旧全量唯一索引，避免软删除历史事件阻塞喇叭投递、宗门秘境或世界 Boss 事件恢复、重建和导入。

## 2026-06-23 宗门名部分唯一索引

`Sect.Name` 嵌入 `gorm.Model`，模型字段不得使用 GORM `uniqueIndex` tag 生成全量唯一索引。有效宗门名唯一性必须由 `sects(name) WHERE deleted_at IS NULL` 部分唯一索引兜底，启动迁移必须能替换同名旧全量唯一索引，避免未来软删除、恢复演练或导入历史宗门时被已删除宗门名阻塞。创建宗门仍必须把唯一冲突映射为 `SECT_NAME_EXISTS` 并回滚同事务内的宗主成员创建。

## 2026-06-23 药园 Mini App HTTP 服务超时

药园 Mini App 独立 HTTP server 必须设置请求头读取、请求体读取、响应写出和空闲连接超时。新增或调整 Mini App 路由时，不得移除 `ReadHeaderTimeout`、`ReadTimeout`、`WriteTimeout` 和 `IdleTimeout`；慢速连接应在 HTTP 层被回收，不能长期占用 worker goroutine 或用户级锁。

## 2026-06-22 宗门洞府文案恢复

`sect.go` 的宗门洞府、解锁洞府、个人闭关和宗门闭关用户提示如果出现编码损坏，应只恢复 UTF-8 文案，不改变事务、权限、消耗、闭关时长、成员筛选或宗门声望扣减规则。修复后必须用 UTF-8 读取方式复核关键中文文本，并运行 `gofmt`、`go test ./...`、`go build ./...` 和无人值守门禁。

## 2026-06-23 修仙配置缓存日志净化

`ReloadCultivationRules` 刷新修仙配置缓存后的诊断日志中，配置来源 `source` 必须通过 `formatPlainValue` 规范化后输出；即使当前来源通常为受控字符串，也不得直接输出动态文本，避免历史异常配置来源打乱运维日志。

## 文档同步要求

每次代码修改后都必须检查是否需要同步文档。

需要同步 `docs/agent/` 的情况包括：

- 新增或修改命令、菜单入口、按钮、回调流程；
- 新增或修改用户可见文案、业务规则、经济参数；
- 涉及积分、库存、宗门资产、抽奖、红包、丹药、药园等资产流；
- 涉及权限边界、管理员能力、审计日志；
- 新增或修改数据库模型、索引、迁移、后台任务；
- 改变配置项、部署方式、验证方式。

需要同步 `AGENTS.md` 的情况包括：

- 改变 Agent 的长期工作流程；
- 新增或调整安全边界、硬性禁止事项；
- 新增或调整文档维护规则；
- 新增项目级技术约束或验证要求。

汇报时必须说明：

- 已同步哪些文档；
- 或者说明本次改动不需要同步文档的原因。

## 高危后台任务审计

世界 Boss 和宗门秘境结算会先把活动状态从 `active` 抢占为 `settling`，再计算伤害/奖励并写入资产。后台扫描器必须在扫描 active 活动前恢复超过 30 分钟仍未 `settled` 的陈旧 `settling` 记录为 `active`，让下一轮扫描重新接管结算；恢复日志必须使用 `formatPlainValue` / `formatPlainError` 规范化。不得恢复已经写入 `settled_at` 的记录，避免重复发奖。

后台生命周期任务如果执行用户本地档案物理删除，必须在同一数据库事务内完成本地硬删除和 `AuditLog` 写入。系统任务使用 `actor_id=0`，审计角色记录为 `system`。ABS 删除失败时不得删除本地档案；ABS 已经返回不存在时可以继续清理本地档案，但审计 detail 需要标记 ABS 删除结果，便于后续追溯。

后台生命周期任务如果因账号到期执行自动封禁，必须先调用 ABS 更新服务端账号状态，该外部副作用不得放进数据库事务。ABS 成功后，本地 `User.is_suspended` 写入和 `AUTO_SUSPEND_EXPIRED_USER` 审计必须在同一个短事务内完成；如果本地状态或审计写入失败，应回滚本地事务、尝试写入 `AUTO_SUSPEND_EXPIRED_USER_LOCAL_FAILED`，并且不得发送自动封禁成功通知。`AUTO_SUSPEND_EXPIRED_USER_LOCAL_FAILED` 必须使用可返回错误的审计写入；若失败审计写入失败，必须记录脱敏日志并通知超级管理员人工核查。

后台生命周期任务读取待巡检用户列表失败时，必须记录脱敏日志、通知超级管理员、写入生命周期最近错误，并且不得推进 `daily_lifecycle_last_success_date`；否则后台状态会误报今日巡检已完成，导致到期提醒、自动封禁或宽限期删除被静默跳过。
后台生命周期任务读取 `daily_lifecycle_last_success_date` 失败时必须 fail closed：写入生命周期最近错误、记录脱敏日志并跳过本轮巡检，不得把状态读取失败当成今日未巡检后继续执行自动封禁或自动删除。
后台生命周期任务的成功、失败和半失败诊断日志都必须规范化动态文本：用户名、ABS ID、日期 key、错误详情等进入日志前使用 `formatPlainValue` 或 `formatPlainError`，不得直接输出历史用户名或外部 ID。

后台生命周期任务完成后写入 `daily_lifecycle_last_success_date` 或清理 `daily_lifecycle_last_error` 必须检查写入错误；写入失败时必须记录脱敏日志、通知超级管理员并保留最近错误，避免生命周期动作已执行但成功日期未落库导致重复提醒、重复 ABS 封禁或重复删除巡检。

白名单用户会被生命周期巡检排除，不触发到期封禁和宽限期后的自动物理删除。设置白名单属于生命周期边界变更，必须由超级管理员输入原因并二次确认，禁止作用于自己和超级管理员，成功后写入 `SET_WHITELIST` 审计日志。

`模拟过期` 会主动改变账号生命周期状态，必须按高危运维操作处理：仅限超级管理员，必须输入原因并二次确认，禁止作用于自己和超级管理员，最终执行前再次校验目标身份；执行成功后写入 `SIMULATE_EXPIRE` 审计日志。

`暂停/恢复` 会切换 ABS 服务端账号活跃状态并同步本地封禁标记，必须按高危权限操作处理：仅限超级管理员，必须输入原因并二次确认，禁止作用于自己和超级管理员，最终执行前再次校验目标身份和 ABS 绑定。应先更新 ABS 服务端，成功后再写本地状态和成功审计；ABS 更新失败不得写本地状态并记录失败审计，本地状态或审计写入失败必须提示人工核查并记录本地失败审计。`SUSPEND_USER_FAILED` / `UNSUSPEND_USER_FAILED` 和 `_LOCAL_FAILED` 审计写入都必须显式检查结果；审计再失败时必须记录脱敏日志并通知超级管理员，失败日志中的派生 action 也必须通过 `formatPlainValue` 规范化。

超级管理员授权管理员、设置白名单、人工调账、模拟过期、暂停/恢复和删除用户等高危入口读取目标用户时，必须区分 `ErrRecordNotFound` 和数据库错误；数据库错误必须记录脱敏日志并提示稍后重试，不得误报为查无此人或继续进入二次确认。

用户角色读取不得把数据库错误静默折叠成普通用户。普通权限判断可以按普通用户保守处理，但必须记录 `formatPlainError` 诊断；事务内写入 `AuditLog` 时必须使用带错误返回的角色读取 helper，数据库错误应阻止审计写入并回滚当前高危事务，避免资产或权限操作已经提交但审计角色被误记为 `user`。

ABS 删除类路径统一使用 `IsAbsNotFoundError(err)` 判断服务端账号已不存在的情况，不要在调用点手写 `strings.Contains(err.Error(), "404")`。删除、注销、回滚和遗孀清理中，ABS 已不存在应按幂等成功处理；其他 ABS 错误仍必须保留本地档案或计为失败。遗孀清理会先执行 ABS 服务端删除，清理结束后的 `CLEAN_WIDOWS` 审计写入必须显式检查结果；审计失败时需要记录脱敏日志、通知超级管理员并在最终回执中提示人工核查，避免不可逆外部删除缺少追溯。

ABS 客户端遇到 HTTP 非 2xx 状态且调用方需要分辨状态码时，应返回 `AbsAPIError`，并由 `errors.As` 或既有 helper 读取 `StatusCode`。不要新增依赖错误文案解析状态码的调用点；字符串判断只能作为兼容旧错误的兜底。

ABS 响应体进入错误消息或日志前必须经过统一脱敏与长度限制。`absResponseSnippet` 必须先脱敏 `password`、`token`、`api_key`、`api-key`、`authorization`、`secret` 等字段，再截断展示长度，避免长敏感值在截断后破坏脱敏匹配。调用 ABS 的业务路径记录错误日志时，错误值必须通过 `formatPlainError` 输出，避免外部响应、底层网络错误或未来新增错误上下文把敏感字段、换行、制表符或超长文本直接写入日志。

`replyText` 使用 Telegram Markdown；用户可见错误提示不得直接拼接 `err.Error()`，也不得在 `fmt.Sprintf` 中用 `%v` 直出错误对象。需要展示外部 API、数据库或系统错误时，必须先经过 `formatMarkdownError`，再用 `%s` 放入 Markdown 文案，确保敏感字段脱敏、控制字符规范化、长度受限并完成 Markdown 转义。纯文本管理员通知、panic 兜底日志等诊断文本必须经 `formatPlainError` / `formatPlainValue`，并由 `formatDiagnosticTextForDisplay` 统一脱敏、折叠换行/制表符/不可见控制字符和限长；这些 helper 返回的是字符串，进入 `fmt.Sprintf` 时使用 `%s`，不要继续用 `%v`。日志和审计可以记录原始错误上下文，但审计写入仍会经过统一审计文本格式化。

`state_machine.go` 是用户入口和资产操作集中区。天道奖池、签到、求书、红包、盲盒、丹药、邀请码/续期卡兑换、备份、赛马、骰子和牌九等事务或退款路径的诊断日志必须使用 `formatPlainError`；求书工单 best-effort 日志等共享 helper 的 action/status/purpose 等字符串入参进入日志前必须使用 `formatPlainValue`，诊断文本本身必须保持可读 UTF-8，不得退化为乱码。Telegram 群成员实时校验等 Telegram API 错误使用 `formatTelegramSendError`，不得用 raw `err=%v`。

所有非测试 Go 业务日志只要输出错误对象或错误上下文，都不得使用 `%v` 直出；数据库、事务、随机数、配置和外部服务错误使用 `formatPlainError`，Telegram API 错误使用 `formatTelegramSendError`，panic/recover 值使用 `formatPlainValue` 后再以 `%s` 输出。panic 兜底日志中的动态事件 ID、局 ID、配置 key、状态值等字符串也必须先通过 `formatPlainValue` 再输出，避免异常历史值携带控制字符或敏感片段进入容器日志。`%v` 仅可用于非错误结构化调试值。

Telegram callback 弹窗虽然不使用 Markdown，但属于用户可见的短文本出口。`answerCallback` 必须统一通过 `formatCallbackAlertText` 处理文本，确保按钮弹窗中的旧配置名、错误提示或动态物品名不会携带敏感字段、换行、制表符、不可见控制字符或超出 Telegram callback answer 的长度限制。callback 分发入口需要保留延迟快速确认兜底：业务 handler 若短时间内未能回复 callback，应先发送简短“处理中”确认，避免用户级锁等待、网络发送超时或业务慢路径导致 Telegram callback 过期；业务 handler 已回复时不得重复抢答。

Telegram 发送/编辑失败日志属于外部平台返回的诊断文本。`sendPlainText`、`replyText`、`sendMenu`、二级菜单、无 Markdown 纯文本发送、长消息发送、抽奖公告/回复/置顶、药园面板、管理员通知、榜单置顶、备份置顶、世界 Boss 实时战榜和 `answerCallback` 等共享或局部发送出口记录失败时，必须通过 `formatTelegramSendError` 输出错误，避免 Telegram 返回文本中的敏感字段、换行、制表符或不可见控制字符直接进入日志。Telegram Bot API URL 中的 `bot<TOKEN>/method` 必须被统一脱敏为 `bot***:***/method`，不能让网络错误中的完整 Bot Token 进入容器日志、审计或用户可见错误。

修仙突破公告、失败公告、修仙榜、突破私聊、历史补偿、天道奖池私聊状态和超级管理员遗孀清理进度/结果等用户可见反馈不得直接忽略 `sendAutoDelete` / `bot.Request` 返回值；发送或编辑失败时至少记录日志，并使用 `formatTelegramSendError` 规范化 Telegram 返回错误，方便排查群权限、消息格式或限流问题。通知失败不得中断已提交的资产、账号清理或审计流程；必要时可以降级为发送最终摘要。

手动 `备份数据` 会把加密数据库备份发送到外部 Telegram 备份群组，属于敏感数据外发。该命令必须仅限超级管理员，要求输入原因并二次确认，执行前检查 `BACKUP_GROUP_ID`，成功和失败都写入审计日志。成功或失败审计写入必须显式检查结果；如果审计写入失败，应记录脱敏日志并通知超级管理员人工核查，不能只静默依赖普通日志。备份密钥、明文数据库内容和备份文件内容不得进入日志或普通消息；清理历史明文备份文件等本地路径诊断必须通过 `formatPlainValue` 规范化后再记录，删除失败必须记录 `formatPlainError` 脱敏错误，避免敏感明文文件残留时缺少排障线索，也避免绝对路径、控制字符或异常文件名原样进入容器日志。

自动备份调度读取 `auto_backup_last_success_date`、`auto_backup_retry_count` 或 `auto_backup_last_attempt_at` 失败时必须 fail closed：记录脱敏错误并跳过本轮备份，不得把读取失败当成未成功、0 次重试或无最近尝试后继续外发数据库备份。重试次数和时间配置解析失败同样按状态不可用处理。备份尝试开始前写入 `auto_backup_last_attempt_at` 必须检查错误；写入失败时必须跳过本次备份并通知超级管理员，避免无法记录尝试时间却继续外发数据库备份。备份失败后写入 `auto_backup_retry_count` 和 `auto_backup_last_error` 必须检查错误；写入失败时必须记录脱敏日志并通知超级管理员，避免重试次数或失败状态失真。备份文件发送成功后写入 `auto_backup_last_success_date`、`auto_backup_last_success_at`、`auto_backup_last_message_id`、重置重试次数或清理最近错误都必须检查写入错误；写入失败时必须记录脱敏日志并通知超级管理员人工核查，避免备份已外发但成功状态未落库导致重复外发。备份文件发送成功后的置顶交接读取 `backup_last_pinned_message_id` 失败或解析失败时，必须写入 `backup_last_pin_error`、记录脱敏日志、通知超级管理员并跳过本次置顶交接，不得把状态异常静默当成没有旧置顶后继续调用 Telegram。备份消息置顶成功后写入新的 `backup_last_pinned_message_id` 或清理 `backup_last_pin_error` 失败时，必须记录脱敏日志并通知超级管理员人工核查，不能只依赖不返回错误的系统配置写入 helper。

`备份状态` 是只读运维命令，只能读取自动备份和置顶状态配置，不得创建备份文件、发送 Telegram 文件或改变备份调度状态。状态输出中的错误内容必须限制长度并按 Markdown 转义；读取今日自动备份重试次数、最近成功日期、成功/尝试时间、备份消息 ID、置顶消息 ID、最近备份错误或最近置顶错误失败时必须显示“读取失败”或“状态暂不可用”，不得把读取失败折叠成 0、无或从未成功。查看操作写入 `VIEW_BACKUP_STATUS` 审计日志。

`后台状态` 是只读运维命令，只能读取后台任务已写入的状态配置和进程内运行观测指标，用于查看调度窗口、生命周期巡检、生命周期最近错误、每日听书统计刷新、自动备份、消息队列水位、Telegram 发送队列水位、API 成功/失败、慢用户锁/慢消息处理/callback 兜底和最近 Telegram 错误。该命令不得触发生命周期巡检、ABS 同步、自动备份或 Telegram 文件发送；状态输出中的错误内容必须限制长度并按 Markdown 转义，运行指标错误摘要中的 Telegram endpoint 必须通过 `formatPlainValue` 规范化。读取生命周期最近完成日/错误、每日听书刷新时间/成功数/总数/跳过数/错误、自动备份最近成功日/错误或重试次数失败时，必须显示“读取失败”或“状态暂不可用”，不得折叠成 0、无、从未完成或从未成功。查看操作写入 `VIEW_BACKGROUND_STATUS` 审计日志。

自动删除消息队列清理时，只有 Telegram 删除成功或返回可忽略终态错误后才允许删除 `AutoDeleteMsg` 记录；数据库清理必须检查删除错误和 `RowsAffected`。删除未命中需要记录脱敏日志，不得把队列记录清理假定为成功。

管理员系统监控是只读运维入口，会调用 ABS 用户和会话接口并展示本地积分用户总数、活跃会话和每日净修为刷新状态。读取本地积分用户总数或每日刷新状态配置失败时，必须显示“读取失败”或状态暂不可用，不得把读取失败折叠成 0、无、尚未执行或无错误；诊断日志必须使用 `formatPlainError`。

后台任务写入 `SystemConfig` 的最近错误字段时，必须先通过 `setSystemConfigError` 脱敏并限长；不要把原始 `err.Error()` 持久化到数据库。Markdown 状态面板展示这些持久化错误字段时，必须使用 `formatSystemConfigErrorForMarkdown` 再次脱敏、折叠控制字符、限长并转义，作为旧数据兜底。

后台调度器查询每日净修为刷新用户、宗门周目标自动结算列表、宗门周目标通知对象或写入系统配置失败时，诊断日志必须使用 `formatPlainError`；配置 key 进入日志前必须使用 `formatPlainValue`，不得用 `%v` 直出数据库或配置写入错误。

宗门周目标自动结算完成且本轮无失败后，写入 `sect_weekly_task_auto_settle_last_week` 必须检查错误；写入失败时必须记录脱敏日志并通知超级管理员人工核查，避免宗门资金/声望结算已完成但自动结算周标记未落库导致重复扫描同一周。

每日听书缓存刷新属于批量 ABS 外部读取和本地统计同步任务。调度入口读取 `daily_listening_refresh_last_at` 失败时必须 fail closed，写入 `daily_listening_refresh_last_error`、记录脱敏日志、通知超级管理员并跳过本轮刷新，不得把读取失败或时间解析失败当成从未刷新后继续批量调用 ABS。刷新完成后写入 `daily_listening_refresh_last_at`、成功数、总数、跳过数或清理最近错误必须检查写入错误；写入失败时必须记录脱敏日志并通知超级管理员人工核查，避免批量 ABS 刷新已执行但完成状态未落库导致重复刷新。

每日天道灵气收集属于系统资产流后台任务：北京时间 12:00 后每日最多执行一次，随机向天道奖池注入 5-10 积分。调度入口读取 `daily_fusion_pool_last_success_date` 失败时必须 fail closed，写入 `daily_fusion_pool_last_error`、记录脱敏日志、通知超级管理员并跳过本轮注入，不得把读取失败当成今日未收集。实现必须在 `runFusionPoolLockedTransaction` 内用 `SystemConfig.daily_fusion_pool_last_success_date` 抢占当日执行权，再调用 `addPointsToFusionPoolInTx`，不能只依赖进程内锁或先查后写防重复。`addPointsToFusionPoolInTx` 是抽奖、交易手续费、世界 Boss 和后台灵气注入共享资产路径，最终写回 `fusion_pool_points` 必须限定配置 ID 和 key 并检查 `RowsAffected`，未命中时回滚，避免红包已创建或奖池注入已记入业务流程但水位未落库。群内注入提醒必须在事务提交后发送，可进入 Telegram 异步发送队列；发送或入队失败只记录规范化 Telegram 错误，不回滚已提交的奖池注入；若本次注入触发爆池，保留常规爆池提醒。用户查询 `天道奖池` 时读取或解析 `fusion_pool_points` 失败必须显示“读取失败”并记录脱敏日志，不得误显示为 0。

`addPointsToFusionPoolInTx` 读取到非法 `fusion_pool_points` 水位值时必须 fail closed 并回滚调用事务，不得按 0 继续写回，避免历史奖池水位被抹掉或爆池红包与实际水位不一致。

交易行自动下架属于后台资产维护任务：每分钟扫描到期商品，必须复用 `closeMarketplaceListingScoped` 事务路径关闭商品、关闭未售可售单位，并把背包商品未售库存退回卖家 `Inventory`。未售单位关闭必须检查 `RowsAffected` 与退款依据的 `available` 数一致，未命中或数量不一致时回滚整笔下架，避免库存已退但单位仍可售。历史 active 商品的 48 小时倒计时只在 Bot 启动成功后对 `expires_at IS NULL` 的记录初始化一次，后续重启不得重置已有到期时间。自动下架私聊通知必须在事务提交后发送，发送失败只记录日志；如果到期巡检发现历史 active 商品已无 `available` 可售单位，只能静默关闭状态并记录日志，不得发送到期下架通知。交易行上架成功后的群提醒同样属于事务后通知；补读自由卡密来源失败时必须记录脱敏日志并跳过本次群提醒，不得用未知来源继续发送可能误导的校验标签；发送提醒失败时必须记录脱敏日志，不得回滚已提交上架，也不得把卡密明文或预览写入日志。

交易行自动下架、手动下架和购买成交都必须先确认 `MarketplaceListing.SellerID` 与该商品所有 `MarketplaceSecret.SellerID` 一致。发现不一致时应把商品状态改为 `review`，并检查条件更新的 `RowsAffected`；只有确实暂停 active 商品后才通知超级管理员核查。状态已变化或未改到记录时不得发送“已暂停”通知，也不得发送卖家下架通知、退回库存或发放成交积分，避免异常历史数据或并发状态变化造成资产错流。

交易行自由上架、背包上架、商品详情、公开列表、我的上架、我的购买、卖家商品订单、购买确认、购买成交、订单查询和争议查询等诊断日志必须使用 `formatPlainError`，不得用 `%v` 直出数据库、库存、卡密或事务错误；商品名、卡密来源、筛选关键词和用户输入片段进入日志前必须继续走 `formatPlainValue` 或更严格的脱敏/单行化 helper。商品详情、购买确认、卖家商品订单、管理员订单查询和买家举报订单读取主记录时必须区分 `gorm.ErrRecordNotFound` 与数据库错误，只有确实未找到或已下架时才提示不存在；数据库错误必须记录脱敏日志并提示状态读取失败，不得误导用户认为商品或订单不存在。背包上架流程选择物品和确认数量读取 `Inventory` 时也必须区分记录不存在与数据库错误，只有确实不存在或数量不足才能提示库存不足；数据库错误必须提示乾坤袋/库存读取失败，不得误导用户认为资产已经不存在。

`ABS_API_URL` 是所有 Audiobookshelf API 请求的基础地址，启动校验必须解析 URL 后确认 scheme 只能是 `http` 或 `https`、主机非空、不得包含 URL userinfo、查询参数、fragment、空白或控制字符；生产默认禁止明文 `http`，只有显式 `ALLOW_INSECURE_ABS_HTTP=true` 才能用于本地测试。不得只靠 `strings.HasPrefix` 判断 ABS 地址，避免异常配置通过启动校验后被 ABS 客户端拼接成不可预期请求。

药园成熟提醒属于后台通知任务：每分钟扫描 `garden_plantings` 中 `status=growing`、`matures_at <= now` 且 `mature_notified_at IS NULL` 的记录。提醒前必须用条件更新写入 `mature_notified_at` 抢占该记录，避免多实例或重入重复提醒；同一用户本轮多块成熟灵田应合并私聊。该任务不得发放药草、计算产量或改变 `status`，Telegram 发送失败只记录规范化错误，不回滚提醒标记。

药园 Telegram Mini App 属于同进程 HTTP 能力：`GARDEN_MINI_APP_ENABLED=true` 时启动 `GARDEN_MINI_APP_LISTEN`，入口地址由 `GARDEN_MINI_APP_URL` 提供给 Telegram WebApp 按钮；用户主菜单里的 `药园` Reply Keyboard 按钮应保持普通文本按钮，点击面板或手动发送 `药园` 时由 Bot 发送原 inline 文字交互版药园首页，并在按钮区追加 `打开药园` WebApp 内联按钮，避免不同 Telegram 客户端对 Reply Keyboard `web_app` 的兼容差异，同时保留原文字交互。药园成熟提醒也应保留 `前往灵田` 等 callback 按钮，并在启用 Mini App 时追加 `打开药园` WebApp 按钮。公网必须使用 HTTPS，本地 `localhost/127.0.0.1` 可用于开发调试。`/api/garden/*` 必须校验 Telegram WebApp `initData`，从签名数据解析 Telegram user id，并在执行前获取 `lockUser` 用户级锁；不得让小程序直接连接 SQLite 或直接提交价格、产量、收益、成本、用户 ID 等资产字段。用户必须从 Telegram WebApp 内联按钮打开药园，直接复制 URL 到普通浏览器不会携带 `initData`，前端应提示发送 `药园` 后点击 `打开药园` 重新打开。Mini App 认证失败日志只能记录脱敏原因码（如 `HASH_MISSING`、`HASH_MISMATCH`、`AUTH_DATE_EXPIRED`、`USER_MISSING`、`INIT_DATA_INVALID`）、请求路径、方法、是否存在 initData 和 UA，不得记录 initData、hash、Bot Token 或用户敏感数据。Mini App 静态资源可以与 Bot 同进程服务，但生产部署仍应由反向代理提供 HTTPS。静态 HTML/CSS/JS 需要返回 no-store/no-cache 响应头，前端引用 CSS/JS 时使用版本参数，反向代理的静态资源 location 不得启用 `proxy_cache`，避免 Telegram Desktop、移动端 WebView 或中间代理继续使用旧图标和旧动效资源。

Mini App 前端允许把最近一次成功的 `/api/garden/state` 以 `garden_snapshot` 存入浏览器 `localStorage`，有效期固定 5 分钟，在网络断开或 5xx/超时后只用于展示离线园况、离线横幅和重连提示；离线缓存不得作为资产动作依据，前端必须禁止缓存态提交开垦、购买、种植、收获、回收、参悟和炼丹。前端请求可设置 8 秒超时、5xx/网络错误最多 2 次重试和自动重连，重试间隔可固定为短间隔，页面隐藏时可暂停倒计时、定时同步和重连计时，恢复可见后再拉取后端状态。Mini App 写操作完成事务后如果后端刷新 `/api/garden/state` 失败，动作接口必须返回 `ok=true` 和 `STATE_REFRESH_FAILED` 提示，前端进入只读重连态并只重试状态读取，不得把已提交动作当作 5xx 失败自动重放。本地开发可在非 Telegram 环境、`localhost/127.0.0.1` 且显式 `?mock=1` 时使用前端 Mock 园况，但生产环境缺少 `initData` 必须继续提示从 Telegram 重新打开，不得走 Mock。

宗门喇叭和世界喇叭使用持久化投递队列。确认发送时只在事务内扣积分、创建 `SectHornBroadcast` 和 `SectHornDelivery` 快照；`HornID`、收件人数和消耗等创建结果只能在事务提交成功后返回给调用方，失败时不得启动投递器；Telegram 私聊发送由事务提交后的投递器执行。启动后 `StartSectHornDispatcher` 会扫描 `queued` 以及超时停留在 `sending` 的喇叭并继续投递，避免重启导致已扣费广播永远不发送。投递失败只更新明细并给发起人发送最终回执，不得回滚已提交的扣费事务。完成回执写入前必须显式统计成功/失败投递数；统计失败时把广播标记为失败并记录 `last_error`，不得把读取失败当成 0 发送误导性回执。完成状态写入后重读广播用于发送发起人回执，读取失败也必须记录脱敏诊断，不能静默返回导致“已完成但无回执”难以排查。

宗门喇叭预览和确认创建读取操作者 `SectMember` 时，只有 `gorm.ErrRecordNotFound` 可以映射为未加入宗门；数据库错误必须中止并走通用失败分支，不得误报未入宗，也不得继续扣积分或创建投递队列。宗门喇叭待投递查询、状态占用、广播读取、投递明细读取、投递状态写回和完成状态写入失败时，诊断日志必须使用 `formatPlainError`；Telegram 私聊失败继续使用 `formatTelegramSendError`，不得记录卡密、Bot Token 或未脱敏外部错误。投递明细从 `pending` 写回 `sent` 或 `failed` 后必须检查 `RowsAffected`；未命中说明投递状态已被并发改变，应记录状态竞争日志，避免已发送但仍 pending 或重复投递问题静默发生。广播总状态写回也必须带状态条件并检查 `RowsAffected`：完成态只能从 `sending` 写入，失败态不得覆盖已完成广播。

宗门抽奖由 `StartSectLotteryScheduler` 每分钟扫描到期的定时活动。满人数开奖在报名事务提交后触发，定时开奖由调度器触发，两者都必须通过 `active -> drawing -> drawn` 状态抢占保证幂等。报名事务内回写 `entry_count` 必须限定 active 状态并检查 `RowsAffected`；开奖事务内最终写回 drawn、参与数和中奖数必须限定 drawing 状态并检查 `RowsAffected`，未命中时回滚，避免并发状态变化下产生假成功。开奖事务内只更新活动、中奖者和奖品状态，不能调用 Telegram；事务提交后再私聊中奖者发送卡密，发送失败写入 `SectLotteryWinner.delivery_error` 和 `SectLotteryPrize.delivery_error`，可由宗主或长老私聊 `重发宗门抽奖 活动ID` 重试。发奖前重读活动或奖品失败时必须记录脱敏诊断并跳过本次投递，不得静默只增加失败数；发奖尝试结束后，创建者摘要需要重新读取中奖记录，私聊创建者中奖名单和投递状态，但不得包含卡密明文。创建后成员提醒同样必须在事务提交后发送，结果写入 `SectLotteryReminder`；启动时会对仍 active 的宗门抽奖补扫提醒，已成功提醒过的成员不重复发送。提醒去重状态读取失败时必须跳过本次成员投递并计入失败，不得当作未提醒继续私聊；提醒私聊发送成功但 `SectLotteryReminder` 成功状态写回失败时，也必须计入失败并记录诊断，避免管理员误以为去重状态已落库。卡密明文不得写日志、审计或群消息。
宗门抽奖报名和管理入口读取活动、成员、宗门或用户资格时，必须区分 `gorm.ErrRecordNotFound` 与数据库错误。只有记录确实不存在时才能映射为未加入宗门、活动不存在或账号不符合资格；数据库错误必须 fail closed 并返回脱敏错误。开奖事务内复核候选中奖者资格时，成员离宗、用户不存在或账号状态不符合资格可以跳过该候选，但数据库错误必须回滚开奖等待重试，不得把读取失败当成不符合资格后少发奖。

官方群内红包、天道机缘和其他群命令的成员校验不得让 Telegram `GetChatMember` 短暂失败误伤当前群消息。消息本身来自 `NOTICE_GROUP_ID` 时可视为已在官方群；私聊或其他群聊仍需通过成员校验。群内成员校验失败导致的拒绝路径应尽量记录日志，方便排查 Bot 权限、Telegram API 抖动或群 ID 配置错误。

Telegram API 的可忽略终态错误需要走统一 helper：编辑消息返回 `message is not modified` 时使用 `isTelegramMessageNotModifiedError` 判断；自动删除消息时，消息已不存在、不可删除或 Bot 权限不足等不应重试的终态错误使用 `isTerminalTelegramDeleteError` 判断；取消旧置顶时使用 `isTerminalTelegramUnpinError` 判断消息不存在、已非置顶或无置顶权限等终态错误。不要在业务路径里重复手写 `strings.Contains(err.Error(), "...")`。

自动删除消息队列属于后台维护任务。读取待删队列失败必须记录 `formatPlainError` 后跳过本轮，不得静默当成空队列；Telegram 删除失败只有命中 `isTerminalTelegramDeleteError` 的终态错误才可清理数据库记录，非终态错误必须记录 `formatTelegramSendError` 并保留队列重试；数据库记录清理失败或清理未命中必须记录 `formatPlainError`，诊断文本必须保持可读 UTF-8，避免自动删消息队列长期异常时无诊断线索。

Bot 使用 Telegram 长轮询接收 update。启动长轮询前必须显式查询并清理 webhook，避免同一 Token 曾经设置 webhook 后 `getUpdates` 无法收到 `/start`、菜单按钮和群命令。启动清理使用 `DeleteWebhookConfig{DropPendingUpdates:true}`，丢弃离线期间积压的旧 update，避免旧交互命令在重启后排队执行或堵塞当前用户命令；资产变动必须以数据库事务和幂等记录为准，不能依赖 Telegram 离线 update 补偿。清理失败应直接阻止启动并记录规范化 Telegram 错误。Bot 初始化、webhook 清理和长轮询启动阶段的 Telegram API 错误必须使用 `formatTelegramSendError`，避免 `https://api.telegram.org/bot<TOKEN>/...` 出现在日志或 panic 输出中。

不要直接使用第三方库的 `GetUpdatesChan`。项目入口使用自有 `pollTelegramUpdates` 循环，以便记录 update 批次、推进 offset，并在 Telegram 返回 `409 Conflict` 时直接停止启动中的 Bot。409 通常表示同一个 Bot Token 还有另一个进程、容器或旧部署实例正在 long polling；继续运行只会表现为“进程活着但命令无响应”。

启动日志必须保留运行身份信息，包括当前可执行文件路径、可执行文件修改时间和工作目录，用于确认部署环境实际运行的是新二进制还是旧 exe/旧容器镜像。Telegram 命令类消息入队时应记录命令头、user、chat 和 message_id，但不得记录完整用户输入，避免密码、安全码、暗号等敏感内容进入日志。

`BOT_STARTUP_NOTIFY_ADMINS` 默认关闭，仅用于排查“进程启动但命令无响应”。开启后，Bot 完成 Telegram 登录和 webhook 清理后会给 `ADMIN_TELEGRAM_IDS` 中的超级管理员发送一条纯文本自检通知。该通知不得包含 token、数据库路径、密钥或其他敏感配置；排查完成后应关闭，避免每次重启打扰管理员。

Docker 部署使用 bind mount `./data:/app/data` 保存 SQLite 数据。容器入口必须先以 root 修正 `/app/data` 权限，再通过 `su-exec app` 降权运行 Bot；不要在 `docker-compose.yml` 中固定 `user: "10001:10001"`，否则宿主机新建或 root 拥有的 `data/` 目录可能导致 SQLite 无法打开数据库，Bot 进程启动失败并表现为所有命令无响应。容器实际运行 Bot 后仍应是非 root 用户。由于 compose 使用 `cap_drop: ALL`，必须显式加回 `CHOWN`、`SETUID`、`SETGID`，否则 entrypoint 会出现 `chown: Permission denied` 或 `su-exec: setgroups: Operation not permitted` 并在进入 Go 程序前退出。

启动时必须在打开 SQLite 前自动创建 `DATABASE_URL` 的父目录，默认 `data/bot_data.db` 不得因为 `data/` 不存在而启动失败。完成 Telegram webhook 清理和进入长轮询时，进程会在数据库目录写入 `bot_startup_health.txt`，只记录阶段、Bot 用户名和时间，用于确认进程是否已经走到 polling；该文件不得包含 token、密钥或数据库绝对路径，Bot 用户名写入前必须通过 `formatPlainValue` 规范化。长轮询启动日志中的 allowed updates 也必须先折叠为受控字符串并通过 `formatPlainValue`，不得用 `%v` 直出动态切片。

Telegram Bot API 客户端必须设置明确 HTTP 超时，避免 `GetChatMember`、消息发送或其他平台请求在网络异常时无限占用用户级锁。长轮询使用 60 秒 update timeout，因此 `getUpdates` 的 HTTP client timeout 必须大于长轮询 timeout；普通发送/编辑/查询类请求应使用更短超时，callback answer 使用最短超时，备份或媒体上传可保留较长超时。不要把所有 Telegram API 请求共用同一个 75 秒超时，否则网络波动会把用户级锁和 worker 吞吐一起拖慢。

长轮询日志需要保留 update 批次和消息队列水位，用于区分 Telegram 网络抖动、队列积压和业务处理慢。worker 处理消息时应记录队列等待超阈值、用户级锁等待超阈值和单条消息处理耗时超阈值；callback 分发也应记录超阈值耗时。日志只能记录命令头、callback data、user/chat/message_id、耗时和队列水位等诊断字段，不得记录完整用户输入、密码、安全码、抽奖暗号或卡密。

Telegram 异步发送调度器只用于“事务已提交后、失败只需记录日志或人工补发”的通知类消息，例如超级管理员告警、后台群公告、修仙突破群公告、世界 Boss/宗门秘境实时榜、榜单发送置顶、抽奖创建公告和抽奖开奖结果公告。不得把注册/绑定/续期/卡密交付/安全码/管理员二次确认/状态机下一步提示等依赖消息顺序或含敏感明文的交互改为普通内存异步队列；抽奖中奖者私聊包含领奖暗号，必须保持同步发送，失败时通知管理员人工补发。异步队列是进程内短队列，不保证重启后补发；队列满、重复 dedupe 或最终发送失败只记录指标和日志，不回滚已提交资产。新增异步发送点必须提供明确 `kind`，需要防重复时提供稳定 `dedupeKey`，并通过 `formatTelegramSendError` 记录失败。

需要回写 Telegram `message_id` 或置顶状态的异步 job，必须在 job 内完成发送、置顶/取消置顶和数据库状态回写。实时榜类 job 应在执行时重新读取最新事件再渲染，避免队列中旧文本覆盖新进度；持久公告或榜单这类无法可靠判断“超时但实际已发送”的消息，默认单次尝试，避免重试造成重复公告。若异步入队失败，可以降级同步执行，但不得在资产事务内等待 Telegram 发送结果。榜单和备份置顶交接读取旧置顶 `message_id` 失败或解析失败时必须记录脱敏日志，不得静默当作没有旧置顶。

自动生成听书榜单读取已绑定 ABS 用户列表失败时必须记录脱敏日志；只有查询成功且用户数为 0 时才可以静默返回，避免数据库故障被误判为无人上榜。逐用户读取 ABS 统计时，部分请求失败或 JSON 解析失败必须记录聚合计数诊断；如果本期所有用户统计都失败，必须跳过榜单发送，避免把外部统计故障误公告成空榜。

私聊 `/start`、`/admin`、`取消` 和返回主菜单这类基础入口必须在抽奖暗号安全锁、官方群成员校验和其他业务命令预处理之前处理。尤其 `/start` 不应依赖数据库查询或 `GetChatMember`，避免外部 API 或 DB 短时异常时用户连主菜单都无法打开。

私聊 `/start` 在 update 分发层保留快速响应通道：收到后直接清理会话并发送用户主菜单，不进入 worker 队列、不等待用户级锁。该路径只能执行无资产副作用、无权限提升、无数据库写入的入口恢复动作；如果菜单发送失败，应尝试纯文本兜底并记录规范化 Telegram 错误。

`审计日志` / `查审计` 是只读追溯命令，仅限超级管理员。查询天数只接受正整数，最多覆盖最近 30 天并最多返回 20 条，支持按操作者/目标 ID 或 action 精确过滤；输出详情必须限长并按 Markdown 转义，查看操作写入 `VIEW_AUDIT_LOGS` 审计日志。该命令不得修改被查询审计记录，不得暴露卡密、备份密钥、密码或安全码明文。

`审计概览` / `查审计概览` 是只读聚合命令，仅限超级管理员。统计天数只接受正整数，最多覆盖最近 30 天，只展示总量、失败/异常数量、高危操作数量、Top action 和 Top actor，不展示 `AuditLog.detail`；查看操作写入 `VIEW_AUDIT_SUMMARY` 审计日志。

`卡密查码` 是超级管理员只读溯源命令。查询邀请码、续期卡或已核销使用者档案时必须显式区分 `ErrRecordNotFound` 和数据库错误；读取失败只提示稍后重试或在使用者档案处显示“读取失败”，并记录脱敏日志，不得把数据库错误误报为查无此码、未使用或用户已注销。审计只记录卡密类型、使用状态和使用者 ID，不得记录卡密明文。

管理员 `查询用户` 是只读档案命令。按 TG ID 查询时，只有 `gorm.ErrRecordNotFound` 才允许回退到用户名查询；TG ID 或用户名读取遇到数据库错误必须记录脱敏日志并提示稍后重试，不得继续 fallback，也不得误报为“未查找到该用户”。

用户可见统计页和资产列表不得把数据库读取失败当成 0 或空列表。邀请统计、活动参与人数、抽奖详情奖品/中奖记录、乾坤袋库存、红包领取后余额和抢空榜、交易订单争议记录、宗门任务状态、Boss 参与、Boss 参加成功后的基线重读、宗门秘境进入成功后的参与记录重读和类似计数/聚合/列表查询必须显式检查 `Count` / `Scan` / `First` / `Find` 的错误；读取失败时展示“读取失败”或“状态暂不可用”，并记录脱敏日志。管理员状态面板同样不得把计数错误默默显示为 0；系统监控读取 ABS 用户列表或会话列表时，请求失败、结构异常或 JSON 解析失败都必须记录脱敏日志并显示“读取失败”。GitHub 福利状态、备份状态和后台状态等只读面板里的聚合数据必须在错误时显示读取失败或状态暂不可用，不可用 `0` 伪装正常读数。

宗门秘境活动配置快照属于结算资产口径。只读展示可以在记录脱敏日志后降级展示当前配置名称，但进入秘境、实时刷新和结算不得在快照缺失或解析失败时回退当前配置或默认配置；结算路径必须回滚 `settling -> active`，等待人工修复后重试，避免管理员后续配置变更影响进行中活动的资产发放。

红包领取失败后的补充状态判断也必须显式返回并处理数据库错误。判断“用户是否已领取所有活跃红包”和“是否存在仅限 Boss 参与者的不可领红包”时，`Count` 失败不得折叠成 false 后继续提示没有红包、已抢光或无资格；必须记录脱敏日志并提示状态暂时读取失败。

每日签到属于积分资产流。更新 `SignInStreak` 连续签到档案时必须按读取到的旧 `last_sign_date/current_streak_days/total_sign_days/cycle_seq/break_count` 条件更新并检查 `RowsAffected`；状态已变化时回滚事务并提示并发重试，避免签到日志、基础积分、连签奖励与连续档案半更新或整行旧快照覆盖。签到事务末尾写回 `User.last_sign_at` 也必须检查 `RowsAffected`，未命中时回滚整笔签到资产事务，避免积分流水已写入但用户主档签到时间未落库。

审计概览中的高危操作由代码中的统一高危 action 集合判定。新增会改变资产、权限、生命周期、备份外发、抽奖活动或修仙经济配置的 `AuditLog.action` 时，必须同步加入该集合并补充测试，避免高危统计漏报。失败型 action 后缀 `_FAILED`、`_LOCAL_FAILED` 会归一到基础 action 后再判断风险等级，确保高危操作失败或半失败也计入审计概览。
GitHub 福利最终发放邀请码/续期卡的 `CLAIM_GITHUB_BENEFIT_INVITE` / `CLAIM_GITHUB_BENEFIT_RENEW`，宗门抽奖创建、开奖、取消的 `CREATE_SECT_LOTTERY` / `DRAW_SECT_LOTTERY` / `CANCEL_SECT_LOTTERY`，以及个人邀请体验注册、试用转正、新人任务领取的 `REFERRAL_TRIAL_REGISTER` / `TRIAL_CONVERT_FORMAL` / `REFERRAL_TRIAL_TASK_CLAIM` 都属于资产、账号权益或卡密流转 action，必须纳入高危审计统计。

事务内审计写入必须使用传入的 `tx` 读取操作者角色，不得在 `writeAuditLogInTx` 内回退到全局 `DB` 查询；这会破坏当前事务视图，且在 SQLite 连接池收窄时可能造成等待。系统任务 `actor_id=0` 必须直接记录角色 `system`，不要查询用户表。

一次性迁移版本检查必须区分“版本记录不存在”和数据库读取错误；只有 `gorm.ErrRecordNotFound` 可以视为未执行，其他读取错误必须阻止启动并记录脱敏错误，不得把迁移表异常误判为需要重新执行迁移。迁移执行成功后写入 `SchemaMigration` 版本记录必须检查 `Error` 和 `RowsAffected`；如果版本标记未实际写入，应阻止启动，避免迁移已执行但后续重复执行或误判状态。

数据库打开、PRAGMA 设置、`AutoMigrate`、索引创建、重复数据预检查和一次性迁移等启动阶段失败日志，错误对象必须使用 `formatPlainError` 输出；SQL 文本、版本号、标题等动态字符串使用 `formatPlainValue`，避免底层驱动错误、路径、连接参数或控制字符原样进入容器日志。

求书工单的用户补充信息路径必须按当前状态做条件更新。用户私聊回复处于 `need_info` 的工单时，更新条件至少包含工单 ID、用户 ID 和 `need_info` 状态；状态更新和 `user_reply` 处理日志必须在同一个事务内提交。数据库错误、日志写入失败或状态已变化时不得通知“已收到”，避免并发处理下产生假成功或无日志状态变更。

求书工单提交限额必须在创建事务内复核：每日提交数按北京时间当天 `[00:00, 次日00:00)` 统计，待处理数按 `status=pending` 统计；只有事务内复核仍未超过“每日 3 条、待处理 5 条”后才能创建 `BookRequest`。入口处的前置检查只能用于提前提示，不能作为唯一防线。
求书工单创建、`BookRequestLog` 和 `CREATE_BOOK_REQUEST` 审计必须在同一个数据库事务内提交；主 `BookRequest` 创建、日志和审计写入都必须检查数据库错误和 `RowsAffected`。任一写入失败或未命中时必须回滚工单创建并提示提交失败，不得通知管理员或用户提交成功。

求书工单创建成功后通知管理员时，读取数据库管理员列表失败必须记录脱敏日志；仍可继续通知 `.env` 中配置的管理员，但不得静默吞掉数据库读取错误，避免误判没有管理员需要通知。

管理员接单成功后会重新读取工单用于刷新面板、写日志和通知用户。若重载失败，不得继续使用旧 pending 状态对象；必须把内存中的工单状态、接单人和接单时间同步为刚写入的值，并记录日志后继续发送正确接单通知。
管理员接单的状态更新、`BookRequestLog` 和 `CLAIM_BOOK_REQUEST` 审计必须在同一个数据库事务内提交；日志或审计写入失败时必须回滚状态更新并提示接单失败，不得出现工单已接单但缺少处理日志或审计记录。

管理员将求书工单标记为已上传或暂无资源时，条件更新成功后会重新读取工单用于刷新面板和通知用户。若重载失败，不得继续使用旧状态对象发送通知；必须把内存中的工单状态、处理人和完成时间同步为刚写入的值，并记录日志后继续发送正确结果通知。
管理员将求书工单标记为已上传或暂无资源时，状态更新、`BookRequestLog` 和 `HANDLE_BOOK_REQUEST` 审计必须在同一个数据库事务内提交；日志或审计写入失败时必须回滚状态更新并提示处理失败，不得发送用户结果通知。
管理员将求书工单标记为已上传后，ABS 最近入库查询、封面下载、管理员预览和大群公告都属于事务提交后的外部副作用；失败只记录日志或提示管理员，不得回滚已完成工单。入库公告从每个 ABS 媒体库最近 5 条入库记录中筛选近 20 分钟内最新一本 ABS 条目，并需管理员确认后发布到 `NOTICE_GROUP_ID`；公告必须使用 `sendNoAutoDelete`，不得置顶、不得登记自动删除。公告正文只能使用 ABS 元数据中的媒体库、书名和演播，不带求书用户、用户备注或喜马拉雅链接。封面下载失败、管理员预览图片发送失败或大群公告图片发送失败时，应记录脱敏日志并降级发送同内容纯文本，避免因 Telegram 媒体上传异常阻断公告。

管理员备注求书工单时，备注更新、`admin_note` 处理日志和 `BOOK_REQUEST_ADMIN_NOTE` 审计必须在同一个数据库事务内提交；日志或审计写入失败时必须回滚备注更新并提示保存失败，不得刷新面板或提示保存成功。

管理员要求用户补充求书信息时，状态更新、`need_info` 处理日志和 `BOOK_REQUEST_NEED_INFO` 审计必须在同一个数据库事务内提交；日志或审计写入失败时必须回滚状态更新并提示设置失败，不得通知用户补充信息。

管理员将求书工单设置为需要补充信息时，条件更新成功后同样会重新读取工单用于刷新面板和通知用户。若重载失败，不得跳过用户通知；必须把内存中的工单状态、管理员备注、处理人和更新时间同步为刚写入的值，并记录日志后继续发送补充请求。

管理员保存求书工单备注时，条件更新成功后如果重新读取工单失败，不能提示“原工单消息已刷新”。应使用刚写入的备注、处理人和更新时间继续尝试刷新，并明确告知管理员备注已保存但消息刷新状态需要稍后查看。

用户档案、管理员查用户、排行榜、红包榜单等 Markdown 输出只要展示用户名、ABS ID、用户输入备注或数据库中可能来自用户的名称，都必须先 `escapeMarkdown`；即使文本放在反引号中也不能省略转义。通过 Telegram username 组装 `@username` mention 时使用 `telegramUsernameMentionMarkdown`，不要直接拼接 `@`，避免下划线、星号等字符破坏 Markdown。若字段历史上可能已经保存过 Markdown 转义后的昵称，输出时使用 `escapeMarkdownPreservingEscapes`，避免未转义旧数据注入，也避免新数据出现双重反斜杠。

乾坤袋、聚宝斋、药园面板、丹药使用确认、修仙突破确认和突破公告等 Markdown 输出展示物品名时使用 `inventoryItemMarkdownName`，只影响展示转义，不改变库存键、商品 ID、按钮 callback、突破配置或资产流。该 helper 会把换行、制表符、普通控制字符和 Unicode 行/段分隔符折叠为空格、为空名提供 `-` 兜底并转义 Markdown，作为历史库存数据的展示防线。按钮文本和纯文本消息可以保留原始展示名；进入 Markdown 的物品名不得直接拼接 `Inventory.ItemName`、`treasureShopItem.Name`、药园 `cfg.SeedName` / `cfg.HerbName` / `cfg.ProductName` / `mat.ItemName`、修仙配置 `req.PillName` 或会话中的 `itemName` / `pillName`。药园丹方名等非物品配置名进入 Markdown 时至少使用 `escapeMarkdown`。

药园和聚宝斋读取钱包、灵田、种植记录、背包、限购、急收额度、丹方解锁或执行按钮购买失败时，诊断日志必须使用 `formatPlainError`，不得用 `%v` 直出数据库或事务错误。灵田管理、选择种子、种子商店、草药背包、药铺回收和丹方炼丹面板读取灵田、种植记录、库存、限购、急收额度或丹方解锁状态失败时，必须显示“读取失败”或提示稍后再试，并不得展示依赖该状态的收获、开垦、种植、购买、回收、参悟或炼丹按钮，避免数据库错误被折叠成空灵田、空库存或 0 持有后误导用户操作。

药铺回收属于积分发放资产路径。`gardenSellHerbQuantity` 必须先确认 seed 配置存在，再计算基础回收价、急收价或进入库存扣减；未知 seed 必须 fail closed 为不可回收，不能让零值或异常配置进入价格公式后再合并判断。回收数量只能是用户输入的正整数，文本命令、inline 按钮和 Mini App 都不得再提交 `全部`、`all`、`sell-all` 或 `quantity=-1` 触发全量回收。

`AuditLog.target` 和 `AuditLog.detail` 写入前必须经过统一敏感文本脱敏、控制字符规范化和长度限制，至少覆盖密码、token、API key、Authorization、SECURITY_PEPPER、BACKUP_ENCRYPT_KEY、安全码、邀请码、续期卡、卡密和 URL 内嵌 `user:pass@host` 凭据等字段；换行、制表符和其他不可见控制字符应折叠为空格，避免审计查询面板被打乱。审计查询展示历史记录时也必须再次走 `formatAuditTextForDisplay` 并限长，作为旧数据兜底。当前写入侧 target 最多保留 200 字，detail 最多保留 1000 字，避免外部 API 或数据库错误把大段文本长期持久化。

积分流水列表是 Markdown 出口，`PointTransaction.Type` 和 `PointTransaction.Description` 展示前必须走 `pointTransactionTypeMarkdown` / `pointTransactionDescriptionMarkdown`，避免商品名、抽奖标题、管理员原因或异常历史流水中的 Markdown 元字符、控制字符、token、密码、备份密钥等打乱用户视图或泄漏诊断信息。该处理只影响查询展示，不改变流水入库内容、资产金额或流水类型。

`SecurityAttemptLock` 用于安全码和抽奖暗号等失败次数持久化。记录失败次数时必须兼容首次失败并发创建：如果创建 `(user_id, purpose)` 记录撞唯一索引，必须重新读取现有行并按既有失败次数继续累加，不能因为唯一冲突漏记失败或绕过临时锁定。首次创建锁记录、更新失败次数和清理成功校验后的锁状态都必须检查 `RowsAffected`；创建或更新未命中应返回错误，不得静默漏记。

账号安全入口、换绑安全码校验、修改密码、删号注销安全码校验和仅解绑安全码校验流程不得在本地档案读取失败时继续校验安全码、调用 ABS 改密、解绑或注销；读取失败只提示稍后重试并记录脱敏日志，缺少 ABS 用户 ID 时中止流程并提示重新绑定，避免空安全码或空 ABS ID 进入外部副作用。

超级管理员系统配置写操作必须可追溯。`设置线路` 等会改变用户可见服务入口的命令，`邀请码价格`、`续期卡价格` 等会改变经济参数的命令，都需要输入变更原因、展示确认摘要、二次确认后写入，并记录 `AuditLog`。这些纯本地配置写入必须在同一数据库事务内完成 `SystemConfig` upsert 和 `writeAuditLogInTx`，不得先提交配置再单独写审计；确认阶段应在事务内重新读取提交时旧值，保证审计中的旧值/新值对应真实落库结果，旧值读取或整数解析失败必须回滚配置写入。兑换商城展示和实际扣费读取邀请码/续期卡价格时，数据库错误或非法整数配置必须中止，不得静默使用默认价。线路配置正文统一通过 `validateServerLinesContent` 校验：1-4000 字，可换行，不得包含制表符或其他控制/分隔字符，且最终写入 `server_lines` 前必须再次复核，避免不可见字符进入用户可见线路面板。高危操作原因统一通过 `validateAdminReason` 校验：trim 后至少 5 字，超过 200 字按既有规则截断，且不得包含换行、制表符或其他控制/分隔字符，避免打乱确认消息、审计日志和管理员通知。新增高危原因输入流程时，提示文案应复用 `adminReasonRequirementText` / `adminReasonInvalidText`，保证管理员看到的规则与实际校验一致。

用户获取线路时必须显式区分未配置和数据库读取失败；读取失败提示稍后再试并记录脱敏日志，不得误显示为管理员未配置。展示正文必须通过 `serverLinesMarkdownBody`：历史配置先重新校验，合法内容再 Markdown 转义；不得把 `SystemConfig(server_lines).Value` 直接拼到 `replyText` 或其他 Markdown 消息中。

超级管理员纯本地权限和生命周期变更必须保持状态写入与审计原子性。`授权管理员`、`设置白名单`、`模拟过期` 这类不依赖 ABS/Telegram 外部副作用的写操作，应在事务内重新读取目标用户状态、复核自己/超级管理员/已存在状态保护；其中 `设置白名单` 也必须禁止目标为超级管理员。使用条件更新提交变更，并在同一事务内调用 `writeAuditLogInTx`；如果目标状态在确认期间变化，应中止并提示重新发起，不得覆盖最新状态，也不得把无效操作记录成成功审计。

超级管理员批量生成邀请码和续期卡属于卡密资产生成。确认执行后必须在一个数据库事务内完成全部 `InviteCode` / `RenewCode` 创建和 `GENERATE_INVITE_CODES` / `GENERATE_RENEW_CODES` 审计写入；任一步失败必须整批回滚，不得向管理员发送未提交或未审计的明文卡密。Telegram 发送卡密列表必须在事务提交后执行，发送失败只记录规范化错误，不回滚已提交卡密。

邀请码注册流程使用预占加补偿模式：先在数据库短事务内条件更新预占邀请码并写 `RESERVE_INVITE_CODE` 审计，事务提交成功后才向调用方返回预占的邀请码记录，再调用 ABS 开户。ABS 开户失败时通过短事务退回邀请码并写 `RELEASE_INVITE_CODE`；本地档案事务提交成功时写 `USE_INVITE_CODE` 最终审计；本地档案失败且 ABS 回滚成功后才允许退回邀请码。如果 ABS 回滚失败，不得退回邀请码，必须提示管理员核查遗留 ABS 账号风险。所有审计只能使用邀请码记录 ID 和脱敏预览，不得记录邀请码明文。

注册入口读取本地正式账号失败时必须提示稍后重试并记录脱敏日志，不得把数据库错误当成未注册继续开户，避免重复注册或绕过试用转正分支。

GitHub 福利发放卡密时，GitHub API 查询和 Telegram 私聊发送都不得放进数据库事务。事务内只处理名额扣减、按绑定状态创建邀请码或 150 天续期卡、claim claimed 更新和审计写入；事务提交后再把明文卡密发给用户。审计、日志和错误详情不得记录邀请码/续期卡明文或 GitHub API 返回的大段原文；GitHub API 失败只提示用户稍后重试，不消耗名额。校验时标记 pending claim 过期必须检查更新错误和 `RowsAffected`；数据库错误必须返回并记录脱敏日志，状态未命中按 pending 已变化处理，不得继续当作正常过期。GitHub 福利高危管理写入的纯文本预览和二次确认虽然不使用 Markdown，也必须对待执行命令和变更原因使用 `formatPlainValue`，避免异常会话值或历史输入打乱管理员确认消息。

`GITHUB_API_TOKEN` 是 GitHub 福利可选服务端配置，用于给 `GET https://api.github.com/users/{login}` 增加 `Authorization: Bearer ...`，提高 GitHub API 认证请求限额。该 token 不需要用户提供，不需要仓库权限，建议使用无 scope / No repository access 的 PAT；不得写入日志、审计或用户可见错误。未配置时功能仍走匿名请求，但可能更容易触发 GitHub 403 rate limit。

绑定归属变更属于本地资产控制权变更。进入绑定、完成 ABS 密码校验后查询既有绑定、首次挂载、当前 Telegram 档案更新、安全码换绑和仅解绑不删号都必须区分本地档案未找到与数据库错误；读取失败必须提示稍后重试并记录脱敏日志，不得把数据库错误当成未绑定或首次接入继续流程。首次挂载、当前 Telegram 档案更新、安全码换绑和仅解绑不删号都必须在完成既有身份校验后，通过短事务同时创建/更新本地 `User` 并写入 `BIND_USER`、`REBIND_USER` 或 `UNBIND_USER` 审计；事务内需要复核目标档案 ID、ABS 用户 ID 或当前 Telegram ID，安全码换绑必须按进入会话时的原始 `telegram_id` 加 ABS 用户 ID 条件更新，避免会话期间档案被换绑或解绑后仍覆盖最新状态。绑定归属审计 detail 中的用户名、ABS ID 等动态字符串必须先经过 `formatPlainValue`，避免用户输入、外部 ID 或历史异常值破坏审计展示。不得在状态机中直接 `DB.Create(&User{...})`、`DB.Model(...).Updates(...)` 或直接更新 `telegram_id/status` 后再单独写审计。

`users(abs_user_id)` 唯一索引必须只约束 `abs_user_id <> '' AND deleted_at IS NULL` 的有效绑定档案；启动迁移必须能替换同名旧全量唯一索引，避免未绑定空 ABS ID 或软删除历史档案阻塞新的绑定、换绑和解绑后重绑。

`暂停/恢复` 需要先调用 ABS 更新服务端账号状态，该外部副作用不得放进数据库事务。调用 ABS 前必须在确认阶段重新读取目标用户后复核目标不是超级管理员，不能只依赖会话开始时或单独角色查询的结果。ABS 调用成功后，本地 `User.is_suspended` 写入和 `SUSPEND_USER` / `UNSUSPEND_USER` 成功审计必须在同一个短事务内完成；本地状态写入必须按用户 ID、ABS 用户 ID 和超级管理员保护条件做条件更新，并检查 `RowsAffected`。如果本地状态或审计写入失败，应整笔本地事务回滚，记录 `_LOCAL_FAILED` 审计并提示管理员人工核查，避免出现“本地已标记成功但无成功审计”的状态。ABS 更新失败审计和本地失败审计都必须使用可返回错误的审计写入；审计写入失败时通知超级管理员，避免外部状态变化或失败事实只停留在普通日志。

续期卡使用时，卡密核销、到期时间延长和 `USE_RENEW_CODE` 审计必须在同一个数据库事务内完成，且审计不得保存续期卡明文。读取 `RenewCode` 和当前 `User` 时，只有 `gorm.ErrRecordNotFound` 可以映射为卡密无效或账户不存在；数据库错误必须回滚并走通用失败分支，不得误报为卡密无效或未检测到账户。卡密核销后写 `User.expire_at` 必须限定当前用户和非试用账号状态并检查 `RowsAffected`，未命中时回滚，避免续期卡已消费但有效期未延长。若需要恢复过期封禁账号，必须先提交续期事务，再调用 ABS 恢复服务端账号状态。ABS 成功后，本地 `User.is_suspended=false` 与 `RENEW_REACTIVATE_USER` 审计必须在同一个短事务内完成；ABS 失败写 `RENEW_REACTIVATE_USER_FAILED`，本地状态或审计失败写 `RENEW_REACTIVATE_USER_LOCAL_FAILED` 并提示用户联系管理员，避免续期已到账但权限恢复不可追溯。续期恢复失败审计必须显式检查写入结果；审计再失败时记录脱敏日志并通知超级管理员。

自助注销、超级管理员物理删号和生命周期自动删除都会先调用 ABS 删除服务端账号；该外部删除不得放进数据库事务。自助注销确认时读取本地档案失败必须提示稍后重试且不得执行 ABS 删除或本地删除。超级管理员物理删号在调用 ABS 删除前必须在确认阶段重新读取目标用户后复核目标不是超级管理员，不能只依赖会话开始时或单独角色查询的结果。ABS 删除成功或服务端已不存在后，本地 `User` 硬删除和 `SELF_DELETE_USER` / `FORCE_DELETE_USER` / `AUTO_DELETE_EXPIRED_USER` 审计必须在同一个数据库事务内完成；硬删除必须限定当前用户 ID、Telegram ID、ABS 用户 ID 和超级管理员保护条件并检查 `RowsAffected`。若本地删除未命中或审计写入失败，应回滚本地删除并提示或记录人工核查，避免出现“本地档案已消失但无审计”或“未删除却写成功审计”的不可追溯状态。

修仙配置写入会改变突破概率、积分消耗、冷却、修为门槛和境界门槛，属于生产经济/成长参数变更。相关命令必须仅限超级管理员私聊执行，要求输入变更原因并二次确认；数据库写入、事务内规则校验和对应 `AuditLog` 必须放在同一个事务内，配置更新必须检查数据库错误和 `RowsAffected`，未命中、配置校验或审计写入失败时整笔回滚。事务提交后再刷新配置缓存；缓存刷新失败不能回滚已提交配置和审计，但必须明确返回错误，便于管理员核查。凡人引导配置可保留 0 成本/0 冷却；非凡人大境界突破成功率不得设置为 0% 或超过 95%，积分消耗必须大于 0，冷却不得低于 1 小时，最低修为必须大于 0 小时。修仙配置写入的私聊纯文本预览、二次确认和成功回执虽然不使用 Markdown，也必须对待执行命令、原因、境界名和小境界名等动态展示值使用 `formatPlainValue`，避免历史配置或异常输入中的换行、制表符、控制字符、敏感片段或超长文本打乱管理员会话。

修仙大境界、小境界和突破配置分别依赖 `cultivation_realm_configs(major_realm) WHERE deleted_at IS NULL`、`cultivation_minor_realm_configs(major_realm, minor_realm) WHERE deleted_at IS NULL` 和 `breakthrough_configs(from_major_realm) WHERE deleted_at IS NULL` 唯一索引；模型字段不得使用 GORM `uniqueIndex` tag 生成全量唯一索引。启动迁移必须能替换同名旧全量唯一索引，避免软删除历史配置阻塞重新创建有效配置行；索引修复后必须补跑默认配置种子，只补缺失有效行，不覆盖已有生产配置。

修仙档案读取/创建、境界同步、累计听书时长更新、突破事务、天道奖池注入和修仙榜查询的错误日志必须使用 `formatPlainError`，不得用 `%v` 直出数据库、外部服务或事务错误。

突破事务内读取 `Cultivation` 档案时，只有 `gorm.ErrRecordNotFound` 可以映射为 `errCultivationNotFound` 并提示初始化档案；数据库错误必须原样返回并走通用失败分支，确保资源扣除、境界更新和突破记录全部回滚，不能误报为未初始化修仙档案。突破成功写入宗门贡献等追溯日志时，reason 必须保持可读 UTF-8 中文，不得保存历史误解码文本。

每日听书统计写入 `DailyListeningStat` 时，修为口径必须严格使用 ABS `listening-stats.days` 中对应北京时间自然日的官方值，并检查批量 upsert 的数据库错误和 `RowsAffected`。`/api/users/{id}/listening-sessions` 是历史听书会话列表，不是可信的实时播放源：不得按返回的每条历史会话并行累计墙钟增量，也不得在缺少明确播放状态时默认视为播放中。写入失败或 0 行写入不得被主动刷新路径计作成功，避免后台成功数、用户手动刷新和后续修为同步基于未落库数据继续推进。

`DailyListeningStat` 中 `official_raw_seconds`、`live_raw_seconds`、`source` 和 `fetch_status` 继续保留兼容字段，但每日净修为刷新必须把本次官方日值写入 `raw_seconds` / `official_raw_seconds`，并清零旧的 `live_raw_seconds`。ABS 成功返回非 nil 的 `days` 对象但未包含北京时间当天键时，当天官方值按 0 upsert，用于自动修复旧版本历史会话误补算造成的当日污染；若响应根本缺少 `days` 字段，则不得把未知状态误写为 0。播放异常风控仍只使用 ABS 官方日统计。

每日净修为私聊命令必须区分记录不存在和数据库读取错误。`刷新宗门今日净修为` 读取操作者宗门成员档案失败时，只有 `gorm.ErrRecordNotFound` 可以提示未加入宗门；其他数据库错误必须记录 `formatPlainError` 并提示稍后重试。`查看每日净修为 用户ID [YYYY-MM-DD]` 读取 `DailyListeningStat` 失败时，只有未找到可以提示无记录；其他数据库错误不得误报为无记录，日期键进入日志前必须使用 `formatPlainValue`。

听书报告读取 ABS `listening-stats` 主统计时必须显式处理请求错误、非 200 状态和 JSON 解析错误；失败时提示稍后重试并记录脱敏日志，不得继续把 `rawTotalSeconds=0` 误判为暂无收听。读取 ABS 用户书籍进度失败时不得把已完成/在听数量显示为 0，应显示“读取失败”并记录脱敏日志，保留已成功读取的听书时长和净修为信息。

每日听书统计写入、ABS 读取/解析、宗门成员批量刷新名单读取、洞府闭关加成读取和听书汇总统计失败日志必须保持可读 UTF-8；ABS ID、日期键、原因等动态字段使用 `formatPlainValue`，数据库、事务、ABS 或 JSON 解析错误使用 `formatPlainError`，不得用乱码前缀或 raw `%v` 影响后台排障。

## 修仙档案并发写入约束（重要资产边界）

修仙档案 `cultivations` 的境界与冷却字段（`major_realm`、`minor_realm`、`consolidate_until`、`tribulation_fails`、`pill_audio_time`）是用户付费突破的核心资产，更新时不得使用全行 `DB.Save(cul)`：

- 突破结算在用户级锁内、用条件 `Updates`（`WHERE major_realm=? AND minor_realm=?`）原子写入境界；但听书时长同步多由后台批量任务触发（`notifier.refreshAllDailyListeningStatsWithOptions`、`sect` 成员刷新、`world_boss` 修为加成读取），这些路径**不持有用户级锁**。
- 因此 `SyncCultivationRealm` 只能用定向条件更新（`WHERE user_id=? AND major_realm=oldMajor`）写自己负责的小段位/凡人进阶大境界；累计听书时长统一走 `persistCultivationAudioTime`，只 `UpdateColumn("total_audio_time")`。任何新增的档案写入路径都必须遵守「只写本路径负责的列」，禁止 `DB.Save(cul)` 全行覆盖，否则会用陈旧快照回退用户刚突破的境界，造成「扣了资源却退回原境界」的资产损失。

世界 Boss 结算的资产与幂等约束：

- 结算时单个参与者读取 ABS 失败，必须保留其已实时累计的 `Damage`（与实时刷新路径一致），不得回退成 `BaseHours` 把伤害清零，否则会漏发奖励并低估总伤害导致击杀误判。
- 世界 Boss 最后 `15` 分钟禁止新增参与，`Boss状态` 和实时战榜仍可刷新，已成功参加者继续按墙钟封顶造成伤害。
- `参加Boss` 读取本地用户档案时必须区分数据库错误、未注册/未绑定和账号状态不可用；数据库错误需要记录脱敏日志并提示稍后重试，不得误报为未绑定账号。
- 实时刷新读取参与者本地档案时也必须区分数据库错误和未注册/未绑定；数据库错误需要记录脱敏日志并沿用参与者旧 `Damage`，不得把 DB 故障当成未绑定导致实时总伤害被低估。
- 实时刷新和结算读取修为或宗门科技伤害加成失败时，不得把读取失败当成 0 加成后写回伤害；实时刷新应记录脱敏日志并沿用旧 `Damage`，结算必须回滚为 `active` 等待重试，避免排名和奖励被低估。
- `参加Boss` 成功后的即时血量刷新，以及实时刷新和结算时写回 `WorldBossEvent` 血量、击杀状态和参与人数，都必须限定 active 状态并检查 `RowsAffected`，未命中不得继续返回假成功。实时刷新和结算时写回 `WorldBossParticipant` 伤害字段必须按参与记录 ID、Boss ID 和用户 ID 条件更新并检查 `RowsAffected`；实时刷新未命中只记录脱敏日志，结算未命中必须回滚为 active 等待重试。
- 世界 Boss 后台扫描到期活动和进行中活动时，查询失败必须记录 `formatPlainError` 脱敏日志并跳过本轮扫描，不得静默返回。世界 Boss 启动、实时刷新、状态/排行刷新、奖励结算和实时战榜状态回写的错误日志必须使用 `formatPlainError`，不得用 `%v` 直出外部或数据库错误，诊断文本必须保持可读 UTF-8。实时战榜重发后写回 `board_chat_id` / `board_message_id` 必须检查数据库错误和 `RowsAffected`；未命中要记录脱敏日志，避免消息已发出但本地榜单消息 ID 丢失后持续重复重发。
- 击杀奖励的天道奖池 `+10` 必须在 `grantWorldBossRewards` 的结算事务内、经 `runFusionPoolLockedTransaction`（先持 `fusionPoolMutex` 再开事务）注入，受 `status='settling'→'settled'` 闸门保护，保证与结算原子提交且不会因重发结算消息二次注入。
- 击杀奖励 Boss 红包固定为 `30` 积分 / `10` 份，必须写入 `ref_type=world_boss`、`ref_id=boss_id`、`claim_scope=world_boss_participant`，领取查询必须限制为本期 Boss 参与者，且不得阻塞同类普通红包继续领取。
- 实听增量墙钟封顶在结算时以 `min(now, EndAt)` 为窗口上界，确保提前击杀的早结算也按真实经过时长封顶。
- 结算进入 `settling` 后若中途失败，需要把事件回滚为 `active` 以便后续重试；该回滚必须检查 `RowsAffected` 并记录脱敏日志，未改到 `settling` 记录时不得静默当作已恢复。

宗门秘境实时榜维护约束：

- 后台调度器每分钟巡检秘境，并对活跃秘境按约 2 分钟节流刷新常驻实时榜；`宗门秘境`、`宗门秘境排行` 和成员进入后也会触发一次实时刷新。
- `宗门秘境`、`开启宗门秘境`、`进入宗门秘境`、`结算宗门秘境`、`宗门秘境排行` 和 `宗门秘境明细` 等用户入口读取宗门成员档案时，必须区分 `gorm.ErrRecordNotFound` 和数据库错误；只有成员确实不存在时才能提示未加入宗门，数据库错误必须记录脱敏日志并提示稍后重试。宗门档案、手动结算活动和明细参与记录读取也必须区分记录不存在与数据库错误，不得把 DB 故障误报为宗门档案异常、当前没有可结算秘境或未找到参与记录。
- 确认开启宗门秘境的事务内会重读操作者 `SectMember` 作为权限和宗门资金扣减前的最终依据；该重读必须复用统一成员读取 helper。只有 `gorm.ErrRecordNotFound` 可以映射为未加入宗门，数据库错误必须原样返回并回滚，不得误报未入宗后掩盖资金入口数据库故障。
- `进入宗门秘境` 读取本地用户档案时必须区分数据库错误、未注册/未绑定和账号状态不可用；数据库错误需要记录脱敏日志并提示稍后重试，不得误报为未绑定账号。
- 开启宗门秘境事务在读取宗门后用于抢占/触碰宗门行的写入必须检查数据库错误和 `RowsAffected`；未命中必须回滚开启流程，不得继续扣宗门资金或创建秘境事件。
- 秘境编号、宗门名和本周开启次数等用于成功回执和实时榜的结果只能在开启事务提交成功后发布；提交失败不得发送“秘境已开启”回执，也不得启动实时榜。
- 秘境实时刷新和结算读取参与者本地档案时也必须区分数据库错误和未注册/未绑定；实时刷新遇到数据库错误只记录脱敏日志并保留旧值，结算遇到数据库错误必须回滚为 `active` 等待重试，避免参与者被永久漏发奖励。
- 实时刷新会读取 ABS、同步总净修为，并更新参与者的原始增量、墙钟封顶、秘境压制后净修为、护道加成、预计积分/贡献/声望和掉落；参与者和事件汇总写回都必须限定 active 状态并检查 `RowsAffected`。它不得发放资产，最终发奖仍只能由结算事务完成。
- 宗门秘境后台扫描到期活动和进行中活动时，查询失败必须记录 `formatPlainError` 脱敏日志并跳过本轮扫描，不得静默返回。宗门秘境开启、实时刷新、状态/排行刷新、奖励结算和实时榜状态回写的错误日志必须使用 `formatPlainError`，不得用 `%v` 直出外部或数据库错误，诊断文本必须保持可读 UTF-8。
- 宗门秘境配置写入的私聊纯文本预览和二次确认必须对待执行命令和变更原因使用 `formatPlainValue`；配置写入审计 detail 中的档位 key、档位名称、掉落物品名和原因也必须规范化，避免高危配置确认和审计展示被控制字符、敏感片段或超长文本打乱。
- 实时榜消息 ID 保存在 `SectSecretRealmEvent.BoardChatID/BoardMessageID`，编辑失败时可以重发并更新消息 ID；消息 ID 写回必须检查数据库错误和 `RowsAffected`，未命中要记录脱敏日志，避免消息已发出但本地榜单消息 ID 丢失后持续重复重发。Telegram 发送/编辑失败只记录日志，不得回滚已提交的加入或结算状态。
- 秘境结束后 `宗门秘境` 状态查询只提示当前没有可参加的秘境；不要继续展示旧秘境的 `进入宗门秘境` 可用指令，历史榜单和明细查询仍保留各自入口。

雷劫外溢扣分是对第三方的副作用，必须尽力而为：受害者余额在「选取→扣款」之间被并发消费而不足（`errPointsNotEnough`）或账号已删除（`errUserNotFound`）时跳过该受害者，绝不能因此回滚突破发起者已结算成功的境界与扣费；仅真正的数据库错误才中断事务。

抽奖领奖暗号的 5 次错误锁定不得被静默兜底路径绕过：私聊裸消息走 `claimLotteryPrizeByCode(silent=true)` 时，锁定校验对静默与显式路径一律生效；当暗号哈希命中已开奖活动但当前账号不在中奖名单时（明确的领奖尝试信号），无论是否静默都要计入失败次数。暗号哈希未命中的兜底路径属于普通私聊消息，绝不能在此计失败以免误锁正常聊天用户。

同一个领奖暗号可能被不同期抽奖复用。按暗号哈希查到多个已开奖活动时，领奖流程必须优先寻找当前用户仍处于 `pending` 且未过期的中奖记录；已领取、非待领或过期记录只能作为没有可领奖项时的备用提示，不得提前返回挡住其他活动中的有效中奖资格。

积分抽奖公告记录、自动开奖、参与失败、开奖结果记录、暗号解密、奖池水位解析、领奖、过期标记、暗号失败次数、取消抽奖、定时开奖和置顶清理等诊断日志必须使用 `formatPlainError`；涉及历史配置值、置顶消息标签等动态字符串时使用 `formatPlainValue`，不得用 `%v` / `%q` 直出数据库、加密或事务错误上下文。

积分抽奖创建、强制开奖和取消属于高危运营资产流。创建活动时，活动记录、奖品记录和 `CREATE_LOTTERY` 审计必须在同一数据库事务内提交；超级管理员手动强制开奖时，开奖状态流转、中奖记录、天道奖池注入、活动结果写回和 `FORCE_DRAW_LOTTERY` 审计必须在同一开奖事务内提交；取消活动时，状态关闭、退款标记、积分返还、累计退款额和 `CANCEL_LOTTERY` 审计必须在同一事务内提交。不得在事务提交后再用不返回错误的 `writeAuditLog` 补写这些高危审计，避免活动已创建、开奖已生效或积分已退还但审计缺失。

宗门流水 `SectContributionLog.BalanceAfter` 必须反映更新后的真实资产余额：加入宗门、捐献宗门、开启宗门秘境等资金变动在原子更新 `funds` 后需重新读取（事务内可见自身写入）再记账；贡献兑换声望在更新 `prestige` 后需重新读取声望余额，回复中的剩余贡献也应重读 `SectMember.Contribution`。宗门捐献扣除用户积分后，宗门资金增加和成员贡献/周贡献增加都必须检查 `RowsAffected`，未命中时回滚整笔捐献事务，避免扣积分但宗门资产未落库。贡献兑换声望扣除个人贡献后，宗门声望增加和个人声望增加也必须检查 `RowsAffected`，未命中时回滚整笔兑换事务，避免贡献已扣但声望未到账。`贡献换积分` 已关闭，不得新增扣贡献换积分路径。不得用未加锁的旧快照 `sect.Funds + cost`、`sect.Funds - cost`、`sect.Prestige + delta` 或 `member.Contribution - cost` 估算，避免并发下流水和展示余额错误。
宗门捐献、贡献兑换宗门声望、七日续期、听书增长奖励和每日任务奖励等 `SectContributionLog.Reason` 必须保存可读中文并经过既有规范化 helper 处理；不得写入历史乱码或未规范化的用户输入，避免资产追溯记录无法审计。

宗门每日任务和周目标结算属于宗门资产奖励流。每日任务领取创建 `SectDailyTaskClaim` 后，宗门资金/声望奖励和成员贡献/周贡献奖励写回都必须检查 `RowsAffected`，任一未命中必须回滚，避免已标记领奖但资产未到账。周目标结算创建 `SectWeeklyTaskSettlement` 后，宗门资金/声望奖励写回也必须检查 `RowsAffected`，未命中必须回滚，避免周结算记录和流水存在但宗门资产未增加；结算结果只能在事务提交成功后返回，提交失败不得给调用方可用于回执的奖励快照。
宗门每日任务领取、周目标结算和任务面板状态读取失败日志必须保持可读 UTF-8，日期键、周键等动态字段使用 `formatPlainValue`，数据库或事务错误使用 `formatPlainError`。每日任务贡献流水 reason 必须保存可读中文，避免宗门资产追溯记录出现历史乱码。
宗门升级、每日任务领奖和周目标结算的用户可见错误提示必须保持清晰中文；未入宗、权限不足、资源不足、已领取、未达成、已结算和通用失败分支不得显示历史乱码，避免用户无法判断下一步操作。
宗门每日任务面板中的任务名、完成状态和周目标超额文本必须保持清晰中文；“今日签到”“今日净修为 +1 小时”“今日捐献 N 积分”“已完成/未完成”和“超额 +N%”不得回退为历史乱码。

宗门商店七日续期会扣减个人贡献并延长当前绑定账号有效期，属于跨资产事务。扣贡献、写 `User.expire_at`、创建 `SectShopPurchase`、回填 `SectShopRenewClaim.purchase_id`、写贡献流水和审计必须在同一事务内完成；`expire_at` 和 `purchase_id` 写入必须使用条件更新并检查 `RowsAffected`，未命中时回滚，避免出现贡献已扣但有效期未延长或月度名额追踪未绑定购买记录。若事务提交后需要恢复 ABS 服务端状态，ABS 恢复失败审计和本地同步失败审计都必须显式检查写入结果；审计再失败时记录脱敏日志并通知超级管理员。
七日续期名额统计失败属于资产入口前置诊断；到账后 ABS 解封失败或本地状态同步失败属于跨系统资产追溯诊断；这些日志必须保持可读 UTF-8，月份键、ABS ID 等动态字段使用 `formatPlainValue`，数据库、事务或 ABS 错误使用 `formatPlainError`。

宗门职位和宗主归属变更属于权限边界。任命、踢出、退出宗门、转让宗主等事务内删除 `SectMember`、更新 `SectMember.role`、`Sect.owner_id`、`Sect.owner_name` 或成员计数时必须使用条件更新并检查 `RowsAffected`；成员删除必须带当前身份、宗门和角色条件，踢人还必须确认操作者当前角色仍与权限检查时一致，确认确实删到记录后才能扣减 `Sect.member_count`。宗主转让必须在同一事务内完成旧宗主降级、新宗主升级和宗门 owner 字段更新，任一步未改到记录都必须回滚，避免并发状态变化造成双宗主、无宗主或宗门 owner 字段与成员角色不一致。任命、踢人和转让失败日志必须保持可读 UTF-8，并对角色、错误等动态字段使用 `formatPlainValue` / `formatPlainError`。
`SectMember.UserID` 不得使用 GORM `uniqueIndex` tag 生成全量唯一索引；有效成员唯一性必须由 `sect_members(user_id) WHERE deleted_at IS NULL` 部分唯一索引兜底，启动迁移需替换旧全量索引，确保退出/踢出后的软删除记录不会挡住重新入宗。
踢出成员和宗主转让的用户可见错误提示必须保持清晰中文，尤其未入宗、权限不足、目标不在宗门和通用失败分支不得显示历史乱码或串到其他业务语义。

启动迁移替换软删除部分唯一索引时，不能只检查旧索引 SQL 是否包含 `deleted_at IS NULL`；必须规范化并比对完整索引定义，包括索引列和全部谓词。否则同名旧索引如果只缺少 `status='active'`、`code_hash <> ''`、`secret_id > 0` 等业务限定条件，会被误判为健康，继续阻塞有效资产记录或扩大唯一约束范围。

宗门创建、改名、升级、转让、捐献、贡献兑换、七日续期、洞府闭关、成员列表、每日净修为统计、宗门任务和周目标结算等诊断日志必须使用 `formatPlainError` 或 `formatTelegramSendError`，不得用 `%v` 直出数据库、ABS、Telegram 或事务错误；宗门名、原因和日期键等动态字段进入日志前仍需使用 `formatPlainValue`，诊断文本必须保持可读 UTF-8。
宗门科技面板、升级确认和确认升级执行读取成员档案或宗门档案时，必须区分 `gorm.ErrRecordNotFound` 和数据库错误。只有成员确实不存在时才能提示未加入宗门；数据库错误必须记录 `formatPlainError` 脱敏日志并提示稍后重试，不得把读取失败误报为未入宗或宗门档案异常。确认升级事务内成员读取的非未找到错误必须回滚为通用升级失败并记录诊断，避免权限和资产入口误导操作者。
我的宗门、成员分页、贡献排行、宗门商店、宗门改名、升级、职位任命、退出/踢人/转让、捐献、贡献兑换、七日续期、洞府闭关、每日任务领奖和周目标结算等入口读取 `SectMember` 时，必须通过统一成员读取 helper 区分 `gorm.ErrRecordNotFound` 与数据库错误。只有真实不存在才能映射为未入宗或目标不在宗门；数据库错误必须提示读取失败，事务内还必须原样返回并回滚到通用失败分支，避免资产、权限或任务入口把 DB 故障误报为业务状态。
`我的宗门`、`宗门排行`、`宗门成员` 和成员分页 callback 属于高频只读入口；未入宗、读取失败、洞府状态、排行榜标题和分页提示必须保持清晰中文，不得显示历史乱码，避免用户误以为命令失败或不知道下一步操作。
宗门总贡献/本周贡献排行榜也属于只读入口；未入宗、宗门档案读取失败、排行读取失败、空榜、标题和字段名必须保持清晰中文，避免用户无法区分总贡献和本周贡献口径。

宗门捐献和贡献兑换声望属于资产路径；事务失败日志必须保持可读 UTF-8，并通过 `formatPlainError` 输出数据库、事务或资产写入错误，便于在不泄露敏感上下文的前提下定位扣减和到账失败。
宗门捐献、贡献兑换宗门声望和贡献换积分关闭入口的用户可见提示必须保持清晰中文；参数错误、未入宗、积分/贡献不足、功能关闭和通用失败分支不得显示历史乱码，避免用户在资产入口误操作或无法判断失败原因。

宗门洞府闭关维护约束：

- 洞府解锁、个人闭关和宗门闭关都是资产操作，必须使用事务和条件扣减，不能把个人贡献、个人声望或宗门声望扣成负数。
- `SectCaveRetreat.BaseRawSeconds` 记录闭关开始时北京时间当天原始听书基线，闭关加成只作用于基线之后的新增听书，不得回溯放大闭关前听书。
- 同一用户同一时间只能存在一个 active 闭关；依赖 `sect_cave_retreats(user_id) WHERE status='active' AND deleted_at IS NULL` 唯一索引兜底。
- 查询 active 闭关必须显式区分 `gorm.ErrRecordNotFound` 和其他数据库错误；数据库错误必须记录日志并向用户显示读取失败或回滚当前事务，不能当成“没有闭关”继续创建或覆盖状态。
- 宗门长时闭关的 ABS 基线刷新在事务外完成；事务内只扣宗门声望、创建闭关记录、写宗门声望流水，避免网络请求占用资产事务。
- 关闭过期闭关只更新 `status=expired`，不得删除历史闭关记录，便于追踪声望消耗和加成来源。

## 无人值守自检

项目提供只读自检脚本：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_audit.ps1 -Out reports/agent_audit.json
```

脚本用途：

- 扫描直接修改 `User.points` 但未收敛到 `applyPointDeltaInTx` 的路径；
- 扫描直接创建 `PointTransaction` 但未收敛到 `applyPointDeltaInTx` 的路径；
- 扫描直接创建 `AuditLog` 但未收敛到 `writeAuditLogInTx` / `writeAuditLog` 的路径；
- 扫描 `AuditLog.target` / `AuditLog.detail` 存储和审计查询展示是否绕过 `formatAuditTextForDisplay`，确保敏感文本脱敏、控制字符规范化和旧数据展示兜底保持一致；
- 扫描库存或额度扣减缺少非负条件的路径；
- 扫描宗门资金、声望、个人贡献、个人声望扣减缺少匹配非负条件的路径；
- 扫描红包剩余份数/积分扣减是否使用 `left_count + left_points + is_finished` CAS 条件；
- 扫描赛马/骰子/牌九中奖发奖是否先通过下注状态 CAS helper 抢占 `active` 下注行；赛马有中奖者结算时，未中奖下注的批量收尾必须检查 `RowsAffected` 与快照应收尾人数一致；牌九必须按 DB active 快照逐条抢占并在同一事务内写牌面、结果和赔付，避免发奖和下注状态半更新；
- 扫描直接创建 `Inventory` 但没有使用 `inventoryQuantityUpsertClause` 的背包发货路径；
- 标记 `Unscoped().Delete`、`DELETE FROM`、`DROP` 等破坏性数据操作；
- 标记 `DeleteUser` 等 ABS 删除调用；
- 扫描 ABS 404/not-found 判断是否绕过 `IsAbsNotFoundError` 直接匹配错误文案；
- 扫描 ABS 相关错误日志是否直接输出裸 `err`，要求通过 `formatPlainError` 统一脱敏、控制字符规范化和限长；
- 扫描潜在敏感信息日志；
- 扫描 `recover()` 后写日志的 panic 值是否直接通过 `%v` 输出，要求使用 `formatPlainValue` 先脱敏并限长；
- 扫描 `log.Printf` / `log.Fatalf` 中的动态 `item` / `name` / `new_name` / `reason` / `abs` / 字符串 `user` / `key` / `value` / `url` / `code_hash` / `purchase_key` / `mode` / `role` / `status` / `purpose` / `realm` / `boss` / `race_id` / `dice_id` / `title` / `version` / `sql` 字段是否直接输出原始字符串，要求每个动态 `%s` 字段都使用 `formatPlainValue` 折叠控制字符并限制长度，避免用户输入、ABS ID、配置值、状态枚举、事件 ID 或启动迁移诊断文本打乱日志；
- 扫描生产 Go 代码中的 `fmt.Printf` / `fmt.Println`，要求使用 `log.Printf` / `log.Println` 维持统一日志出口；
- 扫描生产 Go 代码中 `fmt.Errorf("...%v", err)` 包装错误的路径，要求使用 `%w` 保留错误链，确保后续 `errors.Is` / `errors.As` 和集中错误码 helper 仍可识别底层原因；
- 扫描管理员纯文本通知中直接展示 `%v` 原始错误或 panic 的路径，要求使用 `formatPlainError` / `formatPlainValue` 先脱敏并限长，且格式化后的字符串用 `%s` 放入通知模板；
- 扫描 `sendPlainText`、`replyText`、`answerCallback` 等用户可见消息中直接展示 `err.Error()` 或通过 `%v` 直出错误对象的路径，要求先经过 `formatPlainError` 或 `formatMarkdownError`；
- 扫描 `formatMarkdownError` / `formatPlainValue` 是否绕过 `formatDiagnosticTextForDisplay`，确保用户可见错误、管理员通知和 panic 兜底日志中的诊断文本统一脱敏、控制字符规范化和限长；
- 扫描 `AuditLog.detail` 构造中通过 `%v` 直接拼接动态错误对象的路径，要求调用点先使用 `formatPlainError`，即使审计写入侧已有最终脱敏兜底也不能省略；
- 扫描 `AuditLog.detail` 构造中直接拼接用户名、ABS ID、卡密预览、外部状态或用户输入等动态字符串的路径，要求调用点先使用 `formatPlainValue` 折叠控制字符、脱敏并限长，避免审计展示被历史异常值打乱；
- 扫描 `answerCallback` 和 `formatCallbackAlertText` 是否绕过统一 callback 弹窗格式化，确保按钮弹窗文本会脱敏、控制字符规范化并限制在 Telegram callback answer 长度内；
- 扫描共享 Telegram 发送/编辑 helper 的失败日志是否绕过 `formatTelegramSendError`，确保外部平台返回错误进入日志前会脱敏、控制字符规范化和限长；
- 扫描修仙模块和状态机中已收敛的用户可见 Telegram 通知发送/编辑是否直接忽略返回值，要求发送失败写入日志并使用 `formatTelegramSendError`；
- 扫描 Markdown 状态面板展示持久化 `SystemConfig` 错误字段时是否绕过 `formatSystemConfigErrorForMarkdown`，确保旧的原始错误值也会再次脱敏、控制字符规范化、限长并转义；
- 扫描已知 SQLite partial unique index upsert 是否缺少匹配的 `TargetWhere` 谓词；
- 扫描生产代码中的 `SystemConfig` 写入是否缺少 `ON CONFLICT` upsert 或统一配置 helper；
- 扫描超级管理员纯本地配置、权限和生命周期写操作是否把状态写入与 `writeAuditLogInTx` 放在同一数据库事务内，避免高危变更已生效但审计缺失；
- 扫描超级管理员批量生成邀请码/续期卡是否绕过事务 helper 逐条创建并单独写审计，避免卡密资产已创建但审计缺失或管理员收到未提交卡密；
- 扫描注册流程是否绕过 `reserveInviteCodeForRegistrationWithAudit` / `releaseInviteCodeReservationWithAudit` 直接更新 `InviteCode`，避免邀请码预占、退回或最终使用缺少审计；本地档案写入失败且 ABS 回滚失败时不得退回邀请码；
- 扫描续期卡成功核销是否在同一事务内写入 `USE_RENEW_CODE` 审计，避免卡密已消费但缺少追溯记录；
- 扫描首次挂载、安全码换绑和仅解绑不删号是否绕过 `bindLocalUserWithAudit` / `rebindLocalUserWithAudit` / `unbindLocalUserWithAudit` 直接创建或更新本地绑定档案，避免本地资产控制权变化缺少审计；
- 扫描 `暂停/恢复` 是否在 ABS 成功后绕过事务 helper 直接更新 `is_suspended` 或单独写成功审计，避免本地封禁状态和成功审计不一致；
- 扫描生命周期自动封禁是否在 ABS 成功后绕过事务 helper 直接更新 `is_suspended` 或漏写 `AUTO_SUSPEND_EXPIRED_USER` 审计，避免后台权限变更不可追溯；
- 扫描续期卡恢复过期封禁账号时是否在 ABS 成功后绕过事务 helper 直接更新 `is_suspended` 或漏写 `RENEW_REACTIVATE_USER` 审计，避免续期已到账但权限恢复不可追溯；
- 扫描自助注销、超级管理员物理删号和生命周期自动删除是否绕过事务 helper 直接 `Unscoped().Delete` 本地用户或单独写成功审计，避免本地硬删除缺少审计；
- 扫描事务内天道奖池注入是否通过 `runFusionPoolLockedTransaction` 保持 `fusionPoolMutex -> DB transaction` 锁顺序；
- 扫描数据库事务回调内是否混入 Telegram 发送/编辑、ABS API 请求或备份外发等外部副作用；
- 扫描生产代码中数据库唯一约束错误是否绕过 `isUniqueConstraintError` 直接匹配 `err.Error()` 文案；
- 扫描生产代码中 GORM 未找到记录错误是否绕过 `errors.Is(err, gorm.ErrRecordNotFound)` 直接比较 sentinel；
- 扫描已收敛到包级 sentinel 的业务错误（如 `LOTTERY_NOT_ACTIVE`、`LOTTERY_WAITING_DRAW`、`LOTTERY_FULL`、`ALREADY_JOINED`、`MARKETPLACE_CLOSE_NOT_FOUND`、`REALM_NOT_ACTIVE`、`REALM_ALREADY_ACTIVE`、`TECH_LEVEL_CHANGED`、`POINTS_NOT_ENOUGH`、`INSUFFICIENT_POINTS`、`ALREADY_GRABBED`、`ALREADY_BET`、`USAGE_LIMIT_REACHED`、`ITEM_NOT_ENOUGH`、`USER_NOT_FOUND`、`SECURITY_PEPPER_NOT_CONFIGURED`、`ALREADY_SIGNED`、`SIGN_DATE_IN_FUTURE`、`CONCURRENT_SIGN_IN_RETRY`、`INVALID_INVITE_CODE`、`INVALID_RENEW_CODE`、`TELEGRAM_USER_MISSING`、`ABS_USER_ID_EMPTY`、`ABS_REFRESH_FAILED`、`ABS_REFRESH_FAILED_USING_CACHE`、`target_is_super_admin`、`adjust_no_effect`、`daily_adjust_limit_exceeded`、`ALREADY_IN_SECT`、`SECT_NAME_EXISTS`、`SECT_NOT_FOUND`、`SECT_FULL`、`NOT_IN_SECT`、`TARGET_NOT_IN_SECT`、`NO_PERMISSION`、`SAME_NAME`、`FUNDS_NOT_ENOUGH`、`ONLY_OWNER`、`MAX_LEVEL`、`PRESTIGE_NOT_ENOUGH`、`RESOURCE_NOT_ENOUGH`、`CANNOT_APPOINT_OWNER`、`CULTIVATION_NOT_FOUND`、`MAX_REALM_REACHED`、`CONSOLIDATING`、`NOT_READY`、`INSUFFICIENT_CULTIVATION`、`NO_PILL`、`INVALID_BREAKTHROUGH_MODE`、`CULTIVATION_STATE_CHANGED`、`RANDOM_FAILED`、`INVALID_MARKETPLACE_LISTING`、`MARKETPLACE_DUPLICATE_SECRET`、`MARKETPLACE_INVENTORY_NOT_ENOUGH`、`MARKETPLACE_LISTING_NOT_FOUND`、`MARKETPLACE_SELF_BUY`、`MARKETPLACE_OUT_OF_STOCK`、`MARKETPLACE_QUANTITY_TOO_LARGE`、`MARKETPLACE_INVALID_PRICE`、`MARKETPLACE_INVALID_TYPE`、`CREATE_INVITE_CODE_FAILED`、`CREATE_RENEW_CODE_FAILED`、`CREATE_REDPACKET_FAILED`、`GARDEN_PLOT_MAX`、`GARDEN_DAILY_LIMIT`、`GARDEN_SEED_NOT_AVAILABLE`、`GARDEN_SEED_UNKNOWN`、`GARDEN_PLOT_NOT_FOUND`、`GARDEN_PLOT_BUSY`、`GARDEN_SEED_NOT_ENOUGH`、`GARDEN_NO_ACTIVE_PLANT`、`GARDEN_NOT_MATURE`、`GARDEN_ALREADY_HARVESTED`、`GARDEN_NO_MATURE_PLANT`、`GARDEN_HERB_NOT_SELLABLE`、`GARDEN_HERB_NOT_ENOUGH`、`GARDEN_HERB_QUANTITY_INVALID`、`GARDEN_RECIPE_UNKNOWN`、`GARDEN_RECIPE_UNLOCKED`、`GARDEN_RECIPE_LOCKED`、`GARDEN_MATERIAL_NOT_ENOUGH`）是否退回 `err.Error()` 字符串比较，要求使用 `errors.Is`；
- 扫描宗门每日任务和周目标状态错误（`SECT_DAILY_TASK_NOT_ALL_COMPLETED`、`ALREADY_CLAIMED`、`SECT_WEEKLY_TASK_NOT_ACHIEVED`、`SECT_WEEKLY_TASK_ALREADY_SETTLED`）是否退回 `fmt.Errorf("CODE")` 或 `err.Error()==...`，这些状态必须使用包级 sentinel 并由 `sectErrorCode` 通过 `errors.Is` 识别；
- 扫描生产代码中已登记到 sentinel 列表的业务错误码是否通过 `fmt.Errorf("CODE")` 直接制造；已收敛的业务错误码必须使用包级 sentinel，并通过 `errors.Is` 或对应 error-code helper 识别；
- 对外用于分支和用户提示的 `*ErrorCode(err error)` helper 必须只返回稳定业务码、`UNKNOWN` 或空字符串；未知底层错误不得回退 `err.Error()`，避免数据库、加密、外部服务细节被误当业务码传播；如需兼容旧的裸字符串业务码，只能经集中白名单识别已登记业务码；
- 扫描后台 `SystemConfig` 错误字段是否绕过 `setSystemConfigError` 直接持久化原始 `err.Error()`；
- 扫描生产代码是否绕过 `recordSecurityAttemptFailureInTx` 直接创建 `SecurityAttemptLock`，避免并发首次失败时唯一冲突导致失败次数漏记；
- 扫描审计日志写入格式化是否绕过 `formatAuditTextForDisplay`，避免换行、制表符或不可见控制字符进入 `AuditLog.target` / `AuditLog.detail` 后打乱审计查询；
- 扫描审计日志查询展示是否直接对历史 `AuditLog.target` / `AuditLog.detail` 调用 `redactSensitiveAuditText`，展示侧必须改用 `formatAuditTextForDisplay`，保证旧数据也会脱敏并规范化控制字符；
- 扫描 `validateAdminReason` 是否缺少控制/分隔字符校验；高危管理员操作原因会进入二次确认、审计日志和通知文本，不能包含换行、制表符或不可见控制/分隔字符；
- 扫描高危管理员操作原因提示是否仍使用“原因太短/至少 5 个字”等过期短提示；提示必须复用 `adminReasonRequirementText` / `adminReasonInvalidText`，确保管理员可见规则和 `validateAdminReason` 一致；
- 扫描 `validateSectName` 是否缺少控制字符校验；宗门名会进入宗门面板、排行榜、积分流水和运维日志，不能包含换行、制表符或不可见控制字符；
- 扫描宗门名错误提示是否仍使用未提及控制字符的过期短提示；提示必须复用 `sectNameInvalidText`，确保用户可见规则和 `validateSectName` 一致；
- 扫描 `validateXmlyLink` 是否缺少控制字符校验；求书链接会进入工单、通知、审计和运维上下文，不能包含换行、制表符或不可见控制字符；
- 扫描求书喜马拉雅链接提示是否绕过 `bookRequestLinkRequirementText` 手写短规则；提示必须覆盖 `https://`、长度、非首页路径、空白/控制字符和允许域名，确保用户可见规则和 `validateXmlyLink` 一致；
- 扫描 `validateBookRequestNote` 是否缺少控制字符校验；求书备注和补充说明会进入工单、通知、处理日志和运维上下文，允许换行但不能包含制表符或其他不可见控制字符；
- 扫描 `validateServerLinesContent` 是否缺少控制/分隔字符校验；线路配置会进入用户可见线路面板和 Markdown 输出，允许换行但不能包含制表符或其他不可见控制/分隔字符；
- 扫描 `server_lines` 系统配置写入前是否缺少 `validateServerLinesContent` 复核，避免会话内容异常或未来新增写入路径绕过线路正文校验；
- 扫描求书用户补充日志是否绕过 `markBookRequestUserReplied`；只有 `id + user_id + need_info` 条件更新和 `user_reply` 日志在同一事务内成功后才能通知成功，避免并发状态变化下产生假成功；
- 扫描求书接单处理是否绕过 `reloadBookRequestAfterClaim`；条件更新成功后即使重载失败，也必须用已写入接单状态继续刷新和通知用户；
- 扫描求书已上传/暂无资源处理日志是否绕过 `reloadBookRequestAfterFinish`；条件更新成功后即使重载失败，也必须用已写入状态继续通知用户；
- 扫描求书需补充信息日志是否绕过 `reloadBookRequestAfterNeedInfo`；状态更新、`need_info` 日志和 `BOOK_REQUEST_NEED_INFO` 审计必须同事务提交，条件更新成功后即使重载失败，也必须继续发送补充请求；
- 扫描求书管理员备注日志是否绕过 `reloadBookRequestAfterAdminNote`；备注更新、`admin_note` 日志和 `BOOK_REQUEST_ADMIN_NOTE` 审计必须同事务提交，条件更新成功后即使重载失败，也必须用已写入备注继续尝试刷新，并且不能误报原消息已刷新；
- 扫描积分抽奖创建流程是否绕过 `validLotteryTitle` 只做长度校验；活动标题会进入群公告、活动列表、积分流水和审计上下文，必须拒绝换行、制表符和控制字符，避免打乱运营记录和用户可见文本；
- 扫描积分抽奖领奖暗号创建流程是否绕过 `validLotteryClaimCode` 只做长度校验；暗号会加密保存并在开奖后私聊中奖者，必须拒绝换行、制表符和控制字符，避免打乱私聊提醒和运维核查文本；
- 扫描积分抽奖活动名称和领奖暗号提示是否仍使用只写长度的过期短提示；提示必须复用 `lotteryTitleRequirementText` / `lotteryClaimCodeRequirementText`，确保运营可见规则和实际校验一致；
- 扫描 Markdown 场景中是否直接通过 `"@" + UserName` 或 `fmt.Sprintf("@%s", UserName)` 拼接 Telegram username mention；必须使用 `telegramUsernameMentionMarkdown` 统一去除前导 `@` 并转义 Markdown 元字符；
- 扫描 `replyText`、菜单面板发送/编辑和自动删除消息等 Markdown 出口是否直接展示 `Inventory.ItemName`、`treasureShopItem.Name`、修仙配置 `req.PillName` 或会话 `itemName` / `pillName`；必须使用 `inventoryItemMarkdownName`，按钮文本、callback、库存键和资产流水仍可保留原始物品名；
- 扫描药园 Markdown 渲染行是否直接展示种子、灵草、成丹、材料或丹方名；物品名必须使用 `inventoryItemMarkdownName`，丹方名等配置展示名必须使用 `escapeMarkdown`，按钮文本、callback、库存键和积分流水仍可保留原始名称；
- 扫描 Telegram API 可忽略终态错误是否绕过 `isTelegramMessageNotModifiedError` / `isTerminalTelegramDeleteError` / `isTerminalTelegramUnpinError` 直接匹配 `err.Error()` 文案；榜单和备份取消旧置顶只能忽略消息不存在、已非置顶、无置顶权限等终态错误，超时、限流和 5xx 必须记录日志，备份置顶链路还应写入置顶错误配置，方便管理员排查；
- 扫描交易行卡密交付是否误走 `sendPlainText` / `replyText` / `answerCallback` 等通用可见消息 helper；实际卡密必须走 `sendPlainTextNoMarkdown` 这类显式纯文本出口，避免卡密中的特殊字符被 Telegram 格式解析或破坏展示；
- 扫描交易行买卖积分流水描述是否绕过 `marketplaceBuyPointDescription` / `marketplaceSellPointDescription` 直接拼接 `listing.Name`，避免异常历史商品名打乱积分流水视图；
- 扫描交易行自由上架商品名和背包上架物品名提示是否仍使用只写长度的过期短提示；提示必须复用 `marketplaceSecretListingNameRequirementText` / `marketplaceInventoryItemNameRequirementText`，确保用户可见规则和实际校验一致；
- 扫描高危 `AuditLog.action` 是否写入后漏加到 `highRiskAuditActionSet`，并在报告中定位到写入行；扫描范围同时覆盖 `writeAuditLog` 和事务内 `writeAuditLogInTx` 的字面量 action，避免审计概览低估资产、权限、生命周期、备份或配置变更风险；本地归属变更 `BIND_USER` / `REBIND_USER` / `UNBIND_USER` 和生命周期高危前缀（手动 `SUSPEND_` / `UNSUSPEND_`、续期恢复 `RENEW_REACTIVATE_`、后台 `AUTO_SUSPEND_` / `AUTO_DELETE_`）都必须纳入；
- 检查已使用的积分流水类型是否缺少 `pointTransactionTypeText` 展示；缺失时报告首次发现该流水类型的源码位置，方便补齐展示文案。

脚本启动时会先执行轻量规则自检，覆盖业务错误 sentinel 列表重复项、已登记 sentinel 的 `fmt.Errorf("CODE")` 回退、未登记技术错误码（如 `NO_PRIZES`、`ABS_STATUS_%d`）不误报、直接 `err.Error()` 比较会报错、`errors.Is` 正确用法不误报、`*ErrorCode(err error)` 直接 `return err.Error()` 会报错且集中白名单 fallback 不误报、直接创建 `SecurityAttemptLock` 会报错且统一 helper 不误报、审计日志存储未复用展示格式化 helper 会报错且 helper 写法不误报、审计日志展示直接使用原始脱敏 helper 会报错且展示格式化 helper 不误报、诊断文本格式化未复用 `formatDiagnosticTextForDisplay` 会报错且 helper 写法不误报、callback 弹窗未复用 `formatCallbackAlertText` 或未按上限截断会报错且 helper 写法不误报、共享 Telegram 发送 helper 失败日志直接输出裸 `err` 会报错且 `formatTelegramSendError` 写法不误报、修仙模块用户可见发送直接忽略返回值会报错且记录失败日志不误报、持久化 `SystemConfig` 错误字段 Markdown 展示直接转义旧值会报错且 `formatSystemConfigErrorForMarkdown` helper 写法不误报、管理员高危原因缺少控制字符校验会报错、管理员原因短提示未复用共享规则文案会报错且共享常量写法不误报、宗门名缺少控制字符校验会报错、宗门名短提示未复用共享错误文案会报错且共享常量写法不误报、求书喜马拉雅链接缺少控制字符校验会报错且完整校验不误报、求书喜马拉雅链接短提示未复用共享规则文案会报错且共享常量写法不误报、求书备注缺少控制字符校验会报错且完整校验不误报、线路配置缺少控制字符校验会报错且完整校验不误报、`server_lines` 写入前缺少线路配置复核会报错且 helper 写法不误报、求书用户补充绕过条件更新 helper 写日志会报错且 helper 写法不误报、求书完成处理绕过重载降级 helper 会报错且 helper 写法不误报、求书需补充信息绕过重载降级 helper 会报错且 helper 写法不误报、求书管理员备注绕过重载降级 helper 会报错且 helper 写法不误报、抽奖标题只做长度校验会报错且 `validLotteryTitle` 不误报、抽奖标题短提示未复用共享规则文案会报错且共享常量写法不误报、抽奖领奖暗号只做长度校验会报错且 `validLotteryClaimCode` 不误报、抽奖领奖暗号短提示未复用共享规则文案会报错且共享常量写法不误报、交易行上架名称短提示未复用共享规则文案会报错且共享常量写法不误报、Telegram username 直接拼接 mention 会报错且 `telegramUsernameMentionMarkdown` 不误报、物品名和突破丹药名直接进入 Markdown 可见消息会报错且 `inventoryItemMarkdownName` 不误报、药园 Markdown 面板直接拼接种子/灵草/材料/丹方名会报错且展示 helper 不误报、按钮文本保留原始物品名不误报、Telegram 可忽略终态错误直接匹配文案会报错且统一 helper 不误报、交易行卡密通用发送器误用会报错且显式纯文本发送不误报、交易行流水描述直接拼接原始商品名会报错且 helper 写法不误报等边界。自检失败会生成 `agent-audit-self-test` error，防止维护审计正则时把既有防线改弱或改成大面积误报。

退出码：

- 存在 `error` 级问题时返回非 0；
- 使用 `-NoFail` 可强制返回 0，适合只生成报告的定时任务。

该脚本只读扫描源码，不连接数据库，不调用 Telegram 或 ABS API，不修改业务数据。

项目同时提供无人值守编排脚本：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_unattended.ps1
```

默认步骤：

- `scripts/agent_repair.ps1`
- `gofmt -w` 顶层 Go 源文件（PowerShell 下由脚本枚举文件，避免 `*.go` 被当作字面量传给 `gofmt`）
- `scripts/agent_audit.ps1`
- `go test ./...`（纯逻辑回归套件，无需 cgo；覆盖范围与运行细节见 `docs/agent/testing.md`）
- `go build ./...`
- `scripts/agent_doctor.ps1`
- `scripts/agent_tasks.ps1`

输出文件：

- `reports/agent_unattended.json`
- `reports/agent_repair.json`
- `reports/agent_toolchain.json`
- `reports/agent_audit.json`
- `reports/agent_doctor.json`
- `reports/agent_tasks.json`
- `reports/agent_status.json`
- `reports/agent_gate.json`
- `reports/agent_summary.md`

参数：

- `-NoFormat`：跳过 `gofmt`，适合只读巡检；
- `-BootstrapGo`：本机和 `.tools/go` 都缺少 Go 时，先尝试运行 `scripts/agent_bootstrap_go.ps1` 安装便携 Go；
- `-BootstrapTimeoutSeconds <n>`：限制便携 Go 自举下载步骤的超时时间，默认 300 秒；
- `-BootstrapArchivePath <path>`：使用已下载的本地 Go zip 安装，适合受限网络；
- `-BootstrapOfflineDir <path>`：自动发现离线 Go zip 的目录，默认 `.tools/offline`；
- `-BootstrapDownloadUrl <url>`：使用自定义 Go zip 镜像地址；
- `-BootstrapSHA256 <hash>`：指定自定义 zip 的 sha256 校验值；
- `-BootstrapIncludeAll`：解析 Go 官方包含历史版本的元数据，适合 latest 元数据异常或需要旧版本回退；
- `-BootstrapFallbackVersions <list>`：逗号分隔的回退版本列表，例如 `1.23.12,1.22.12`，会优先于自动 stable 候选；
- `-BootstrapBaseUrls <list>`：逗号分隔的下载基地址列表，例如 `https://go.dev/dl,https://mirror.example/golang`；
- `-BootstrapMaxCandidates <n>`：最多探测 n 个候选版本，避免受限网络下长时间扫 URL；
- `-BootstrapUseBits`：下载 Go zip 时使用 Windows BITS；
- `-UseDockerGo`：本机缺少 Go 工具链时，尝试用 `golang:1.22-alpine3.20` 容器执行 `gofmt`、`go test ./...`、`go build ./...`；
- `-ContinueOnFailure`：继续生成报告并让进程返回 0，适合定时任务先采集结果；报告内 `exit_code` 仍保留真实验证结果；
- `-Report <path>`：指定无人值守编排报告路径。
- `-TaskReport <path>`：指定维护任务队列报告路径。

编排脚本不会等待人工输入。缺少 `go`、`gofmt` 等工具时会在报告中标记为 `skipped`，不会伪造测试或构建已通过。存在失败步骤或必需工具缺失，且未使用 `-ContinueOnFailure` 时返回非 0。`agent_tasks` 负责把巡检结果转换为任务队列；存在待办时任务报告中的 `todo_count` 会大于 0。

安全自修复可单独执行：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_repair.ps1
```

该脚本只清理自动化自身产生的临时文件：

- `reports/agent_step_*`
- `.tools/go*.zip`
- `.tools/go_extract`

使用 `-WhatIf` 可只生成计划而不删除。该脚本不连接数据库，不调用 Telegram 或 ABS API，不删除业务数据，不修改源码。

编排脚本会优先识别项目内便携 Go 工具链：

- `.tools/go/bin/go.exe`
- `.tools/go/bin/gofmt.exe`

工具链诊断可单独执行：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_toolchain.ps1 -NoFail
```

该脚本只读检查本机 Go、`gofmt`、Docker、winget、`.tools/go` 和 `.tools/offline` 中的离线 Go zip，输出 `reports/agent_toolchain.json`。当工具链缺失时，`agent_tasks` 会优先使用该报告生成具体待办，并抑制 `go test`、`go build`、`gofmt` 的重复泛化 missing-tool 待办。

工具链获取入口可预览可执行方案：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_acquire_toolchain.ps1
```

该脚本默认只输出计划，不安装、不下载、不修改系统。显式 `-Install` 时才会执行选定方案：优先使用 `-ArchivePath` 或 `.tools/offline` 离线包，也可使用 `-DownloadUrl` 镜像。系统级 winget 安装必须同时传入 `-Install -AllowSystemInstall`，避免无人值守流程意外修改系统环境。

`scripts/agent_acquire_toolchain.ps1` 和 `scripts/agent_bootstrap_go.ps1` 可识别 `.tools/offline` 中的 `go*.windows-amd64.zip`、`go*.windows-amd64.msi`，以及 winget 下载缓存常见的 `Go Programming Language*_X64_wix*.msi`。MSI 走 `msiexec /a` 管理提取到 `.tools/go`，不执行系统级安装，不修改 PATH。

安装便携 Go：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_bootstrap_go.ps1
```

该脚本下载官方 Windows Go zip 到 `.tools/`，解压到 `.tools/go/`，不修改系统 PATH。若网络下载中断，可重复运行；`-TimeoutSeconds <n>` 可调整下载超时，`-Force` 可强制重装。

自举脚本优先从 `.tools/offline` 自动发现 `go*.windows-amd64.zip`，找到后直接按本地归档安装。下载地址默认从 Go 官方 `https://go.dev/dl/?mode=json` 解析 stable Windows amd64 zip，并在下载前用小范围 Range GET 探测 URL 是否可用；探测临时文件写入 `.tools/probes` 并在每次探测后清理。如果 latest 元数据中的候选下载地址不可用，会尝试下一个 stable 候选。`-IncludeAll` 可改用 `https://go.dev/dl/?mode=json&include=all` 解析历史版本；`-FallbackVersions` 指定的版本会优先尝试。下载先写入 `.part` 临时文件，成功下载后才转为 zip；失败或超时时会清理临时文件并在报告中返回失败。若当前网络无法下载大文件，或官方元数据返回的下载地址不可用，自举失败会在 `agent_unattended` 的 `bootstrap_go` 步骤和 `agent_tasks` 的 `toolchain-bootstrap-failed` 任务中体现。

受限网络下可改用本地 zip 或镜像：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_bootstrap_go.ps1 -ArchivePath C:\path\go.windows-amd64.zip
powershell -ExecutionPolicy Bypass -File scripts/agent_bootstrap_go.ps1 -DownloadUrl https://mirror.example/go.windows-amd64.zip -SHA256 <hash>
powershell -ExecutionPolicy Bypass -File scripts/agent_bootstrap_go.ps1 -IncludeAll -FallbackVersions 1.23.12,1.22.12 -MaxCandidates 2
```

离线无人值守方式：

```powershell
New-Item -ItemType Directory -Force .tools/offline
# 将 go*.windows-amd64.zip 放入 .tools/offline 后，后续 agent_watch -BootstrapGo 会自动安装。
```

注册无人值守计划任务前可预览命令：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_schedule.ps1
```

该脚本默认只输出将要执行的 `schtasks.exe` 命令，不修改系统。确认后可显式安装：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_schedule.ps1 -Install
```

计划任务默认每小时执行一次 `agent_watch.ps1 -Iterations 1`，适合无人值守定时巡检。卸载时使用：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_schedule.ps1 -Uninstall
```

维护任务队列可单独生成：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_tasks.ps1 -NoFail
```

任务队列来源：

- `reports/agent_audit.json` 中的 `error` 和 `warn`；
- `reports/agent_unattended.json` 中失败或跳过的验证步骤。
- `reports/agent_doctor.json` 中自动化系统自身的错误和警告。

任务状态：

- `todo`：会阻止无人值守验证被视为健康；
- `review`：需要复核或文档化，但不会单独作为阻断错误；
- `accepted`：已进入 `docs/agent/audit_baseline.json` 的已知 review 项。

Review 基线：

- `docs/agent/audit_baseline.json` 只允许接受 `review` 级任务；
- `todo` 和 `error` 级任务永远不能被基线抑制；
- 新增 warning 如果不在基线中，会以 `review` 状态出现在任务队列中；
- Review 接受优先按任务 ID 精确匹配；
- 若修改或移动代码仅导致行号变化、任务 ID 漂移，`agent_tasks` 会用 `source/kind/title/file/evidence` 组成的稳定签名兜底匹配；
- 同一稳定签名出现多次时，只有当前任务数量和基线数量一致才会按签名接受，避免新增同类高危语句被旧基线误放行；
- `docs/agent/audit_baseline.json` 中的 accepted review 应保留 `source`、`kind`、`title`、`file`、`line`、`evidence`、`reason`，其中 `line` 作为人工定位信息，稳定签名不依赖 `line`；
- 修改证据、规则、文件或新增 warning 仍会重新浮出为 `review`，需要重新复核。

自动化系统自检：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_doctor.ps1
```

Doctor 检查内容：

- `scripts/agent_*.ps1` PowerShell 语法解析；
- `reports/agent_audit.json`、`reports/agent_tasks.json`、`reports/agent_watch_latest.json`、`docs/agent/audit_baseline.json` 是否为合法 JSON；
- 任务队列、latest、baseline 是否包含关键字段；
- accepted review 是否保留 `source/kind/title/file/evidence/reason` 等稳定签名和复核字段；
- baseline 是否有重复 ID、无法通过 ID 或稳定签名匹配的过期项，或错误地接受了非 accepted 状态；
- 是否残留 `reports/agent_step_*` 临时输出文件或 `.tools/go*.zip` 临时 Go 归档。

Doctor 做 baseline 检查时会优先跟随 `reports/agent_watch_latest.json` 中的 `task_report_path` 读取最新一轮任务文件；没有 latest 任务路径时才回退到 `reports/agent_tasks.json`，避免用旧顶层任务报告误判基线过期。

Doctor 的 `error` 会通过 `agent_tasks` 变成 `todo`，`warn` 会变成 `review`。

健康摘要可单独生成：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_status.ps1 -NoFail
```

该脚本读取 `reports/agent_watch_latest.json`，并优先跟随 latest 中的 `task_report_path` 读取最新一轮任务文件；没有 latest 任务路径时才读取 `reports/agent_tasks.json`。输出写入 `reports/agent_status.json`。摘要包含整体 `status`、真实 `exit_code`、todo/review/accepted 数量、最近一轮步骤状态和待处理问题。未加 `-NoFail` 时，存在不健康状态或未处理 todo 会返回非 0。

无人值守放行门禁：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_gate.ps1 -RunWatch
```

该脚本面向 CI、计划任务或外部监控，默认读取 latest/status/tasks/toolchain 报告并写入 `reports/agent_gate.json`。加 `-RunWatch` 时会先执行一轮 `agent_watch.ps1 -Iterations 1`，再根据以下条件判断是否放行：

- `agent_status` 的真实 `exit_code` 必须为 0；
- latest 状态必须是 `healthy`；
- latest 报告不能超过 `-MaxReportAgeMinutes`，默认 1440 分钟；
- 任务队列不能有 `todo`；
- Go 工具链必须 ready；
- `agent_repair`、`agent_toolchain`、`gofmt`、`agent_audit`、`go_test`、`go_build`、`agent_doctor`、`agent_tasks` 必须全部 `passed`。

常用参数：

- `-RunWatch`：先主动跑一轮巡检，再判断门禁；
- `-BootstrapGo`：跑 watch 时允许便携 Go 自举；
- `-UseDockerGo`：跑 watch 时允许 Docker Go 工具链；
- `-WatchTimeoutSeconds <n>`：限制 watch 单轮最长时间，默认 900 秒；
- `-MaxReportAgeMinutes <n>`：允许 latest 报告的最大年龄，默认 1440 分钟，传 0 可关闭新鲜度检查；
- `-SummaryReport <path>`：指定 Markdown 摘要路径，默认 `reports/agent_summary.md`；
- `-NoFail`：始终返回 0，但报告内保留真实 `exit_code`，适合只采集状态的外部系统。

当前环境若缺少 Go/gofmt，门禁会失败并明确报告 `toolchain-not-ready`、`step-not-passed` 或 `open-todos`，不会把跳过的 `go test ./...`、`go build ./...` 误判为健康。

Markdown 摘要可单独生成：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_report.ps1
```

该脚本读取 `agent_status`、`agent_gate`、最新任务队列和工具链报告，写入 `reports/agent_summary.md`，用于人工快速浏览或外部系统归档。`agent_watch.ps1` 每轮结束后会自动刷新摘要；`agent_gate.ps1` 判断完成后也会自动刷新摘要。

## 无人值守循环

长期巡检使用：

```powershell
powershell -ExecutionPolicy Bypass -File scripts/agent_watch.ps1
```

默认每 3600 秒执行一次 `agent_unattended.ps1 -ContinueOnFailure`，不等待人工输入。每轮报告写入：

- `reports/agent_runs/agent_unattended_<timestamp>_<pid>_<n>.json`
- `reports/agent_runs/agent_repair_<timestamp>_<pid>_<n>.json`
- `reports/agent_runs/agent_toolchain_<timestamp>_<pid>_<n>.json`
- `reports/agent_runs/agent_audit_<timestamp>_<pid>_<n>.json`
- `reports/agent_runs/agent_doctor_<timestamp>_<pid>_<n>.json`
- `reports/agent_runs/agent_tasks_<timestamp>_<pid>_<n>.json`
- `reports/agent_watch_latest.json`
- `reports/agent_status.json`
- `reports/agent_summary.md`

常用参数：

- `-Iterations <n>`：只运行 n 轮；不传或传 0 表示持续运行；
- `-IntervalSeconds <n>`：设置两轮之间的等待秒数；
- `-RetainRuns <n>`：最多保留最近 n 份历史运行报告，默认 100；传 0 表示不按份数清理；
- `-RetainDays <n>`：删除早于 n 天的历史运行报告，默认 0 表示不按天数清理；
- `-LockFile <path>`：防重入锁文件路径，默认 `reports/agent_watch.lock`；
- `-StaleLockMinutes <n>`：锁文件超过 n 分钟或持锁进程不存在时视为陈旧锁，默认 720；
- `-ForceUnlock`：启动前强制清理已有锁，只应在确认没有其他巡检进程运行时使用；
- `-StatusReport <path>`：每轮生成的健康摘要路径，默认 `reports/agent_status.json`；
- `-SummaryReport <path>`：每轮生成的 Markdown 摘要路径，默认 `reports/agent_summary.md`；
- `-NoFormat`：向编排脚本透传，跳过 `gofmt`；
- `-BootstrapGo`：向编排脚本透传，允许缺少 Go 时尝试便携 Go 自举；
- `-BootstrapTimeoutSeconds <n>`：向编排脚本透传，限制便携 Go 自举等待时间；
- `-BootstrapArchivePath <path>`、`-BootstrapOfflineDir <path>`、`-BootstrapDownloadUrl <url>`、`-BootstrapSHA256 <hash>`、`-BootstrapIncludeAll`、`-BootstrapFallbackVersions <list>`、`-BootstrapBaseUrls <list>`、`-BootstrapMaxCandidates <n>`、`-BootstrapUseBits`：向编排脚本透传便携 Go 自举来源和下载方式；
- `-UseDockerGo`：向编排脚本透传，允许使用 Go Docker 镜像作为验证工具链；
- `-Strict`：发现真实验证结果非 0 时立即返回非 0。

`agent_watch_latest.json` 会保留最新一轮摘要，包括每个步骤的状态、缺失工具原因、真实 `exit_code`、`todo_count`、`review_count`、`accepted_count`、历史报告路径和本轮历史报告清理结果。每轮写入 latest 后会把本轮历史报告同步到顶层 `reports/agent_unattended.json`、`reports/agent_repair.json`、`reports/agent_toolchain.json`、`reports/agent_audit.json`、`reports/agent_doctor.json` 和 `reports/agent_tasks.json`，避免只读取顶层报告的工具拿到旧结果。随后自动运行 `agent_status.ps1` 和 `agent_report.ps1`，让外部调度或监控可读取 `reports/agent_status.json` 判断健康状态，或读取 `reports/agent_summary.md` 查看人类可读摘要。

守护脚本使用 `reports/agent_watch.lock` 防止多个巡检循环叠跑。启动时如果发现仍存活的持锁进程，会写入 `status=locked` 的 latest 并返回 2；如果持锁进程不存在或锁超过 `-StaleLockMinutes`，会自动清理陈旧锁。watch 内部调用 `agent_repair` 和 `agent_doctor` 时会跳过自身活跃锁检查；单独运行这些脚本时仍会检查并清理或报告陈旧锁。

历史清理按运行组删除 `reports/agent_runs/agent_*_<timestamp>_<pid>_<n>.json`，不触碰业务数据。守护脚本本身只调用本地脚本和 Go 工具链，不连接数据库，不调用 Telegram 或 ABS API。

## 播放异常风控维护约束

- 后台播放异常风控每日 03:00 后执行，必须在每日听书缓存刷新之后检查，目标为北京时间昨天和前天两个完整自然日。
- 播放异常风控必须从 `listening_abuse_effective_start_day` 起算；该配置为空时按首次运行新逻辑的北京时间当天初始化，初始化写入必须检查错误，写入失败时必须记录最近错误、通知超级管理员并中止本轮巡检；生效日前的历史收听时长既往不咎，不得参与连续两日冻结判断。
- 读取 `listening_abuse_effective_start_day` 或 `listening_abuse_last_scan_day` 失败时必须 fail closed：写入 `listening_abuse_last_error`、记录脱敏日志、通知超级管理员，并跳过本轮既往不咎恢复、到期恢复和扫描；不得在状态不可读时重置生效日或按未扫描继续执行。
- 播放异常风控完成扫描后写入 `listening_abuse_last_scan_day`、`listening_abuse_last_scan_at` 或清理 `listening_abuse_last_error` 必须检查写入错误；写入失败时必须记录脱敏日志、通知超级管理员并保留最近错误，避免提醒、冻结或解冻副作用已发生但扫描完成标记未落库导致重复巡检。
- 已由播放异常风控冻结且 `day_key < listening_abuse_effective_start_day` 的 active 记录应自动恢复 ABS 权限并标记为 `amnestied`；若账号已到期、被管理员封禁或 ABS ID 已变化，必须阻止恢复并写 release blocked 审计。
- 播放异常 warning 记录和 `LISTENING_ABUSE_WARNING` 审计必须在同一个数据库事务内提交；私聊提醒只能在事务提交后发送。若提醒记录或审计写入失败，不得发送未审计的播放异常提醒，避免自动风控缺少追溯。
- 播放异常提醒或冻结通知发送失败时，`ListeningAbuseRecord.notice_error` 只能保存经过统一脱敏和长度限制后的诊断文本，写入必须检查数据库错误和 `RowsAffected`；写入失败或未命中记录要记录脱敏日志，不能静默吞掉，也不能持久化原始 `err.Error()`。
- 判断口径使用 `daily_listening_stats.raw_seconds` 折算的 ABS 原始播放小时，不使用天道压制后的 `effective_hours`。
- 单日 `> 15` 小时只创建 `warning` 记录并私聊提醒；连续两个自然日都 `> 15` 小时才创建 `freeze` 记录并暂停 ABS 权限 24 小时。
- 异常冻结记录写入 `ListeningAbuseRecord`；同一用户同一天同一动作只能有一条记录，同一用户只能有一个 `active` 异常冻结。
- 异常冻结不得复用到期生命周期的 `User.IsSuspended` 语义。解冻时若用户已到期、已被管理员封禁或 ABS ID 已变化，必须阻止自动恢复，避免误解封。
- 用户侧 `我的信息`、管理员 `查询用户` 和“有效 ABS 账号”资格判断必须识别 active 异常冻结记录；用户侧 `我的信息` 读取本地档案失败时必须提示稍后重试，不得误显示为无资产；展示状态还需要读取 ABS `isActive` 并提示本地/服务端不一致，不得把读取失败或服务端停用静默显示为正常。
- ABS 禁用/启用属于外部副作用，不能放进数据库事务；若禁用 ABS 后本地冻结记录写入失败，应尝试恢复 ABS 并写失败审计。
- 自动恢复或既往不咎恢复调用 ABS 失败时，`release_error` 只能保存 `formatPlainError` 后的脱敏截断文本，并检查数据库错误和 `RowsAffected`；写入失败或未命中记录要记录脱敏日志，不得把原始 `err.Error()` 直接持久化或静默吞掉。
- 播放异常自动冻结失败、到期恢复失败和既往不咎恢复失败的失败审计必须使用可返回错误的审计写入；若失败审计写入失败，必须记录脱敏日志并通知超级管理员人工核查。超管告警中的审计动作和告警标签等动态文本必须通过 `formatPlainValue` 规范化，避免异常标签或历史值打乱通知。
- 冻结、解冻、解冻失败和解冻阻止必须写 `AuditLog`，并纳入高危审计统计。

## 注册有效期维护约束

- 邀请码注册写入已有本地 `User` 档案时，不得只在 `expire_at IS NULL` 时初始化有效期；必须通过 `registrationExpireAtForExistingUser` 保证重新注册后至少拥有本次默认注册有效期，同时保留更长的旧有效期，避免旧到期时间导致新注册账号只剩极短有效期。
- 注册流程复用已有本地 `User` 档案写入新 ABS 账号、安全码和正式账号状态时，必须按当前 `id/telegram_id` 条件更新并检查 `RowsAffected`；未命中时必须回滚本地事务并触发外层 ABS 账号删除和邀请码退回补偿，避免 ABS 已开户但本地档案未落库。

## 求书工单维护约束

- 求书工单详情、接单、要求补充信息、管理员备注、完成/拒绝以及对应会话输入路径读取 `BookRequest` 时，必须区分 `gorm.ErrRecordNotFound` 和数据库错误；只有真实不存在才能提示“工单不存在”，数据库错误必须记录脱敏日志并提示稍后重试，不能清理仍可重试的管理员输入会话。
- 求书工单状态更新、日志和审计仍必须在同一数据库事务内完成；事务提交后再刷新 Telegram 消息或通知用户，外部副作用失败只记录脱敏日志。
- 求书已上传后的大群入库公告必须与工单状态解耦：先完成工单和用户通知，再从每个 ABS 媒体库最近 5 条记录中筛选近 20 分钟内最新书籍并让管理员确认发布。封面通过 Bot 后端下载后上传 Telegram，不能让 Telegram 直接拉取受保护 ABS URL；封面下载失败或 Telegram 图片发送失败时降级纯文本，不阻断工单完成。

## 个人邀请拉新维护约束

- 新增模型 `ReferralCode`、`ReferralActivation`、`ReferralDailyActivationQuota`、`ReferralMonthlyRewardQuota` 以及 `User.account_type/trial_started_at/trial_ends_at` 仅允许通过 `AutoMigrate` 增量添加，不得清理历史用户、邀请码或续期卡数据。
- `referral_codes.user_id`、`referral_codes.code` 和 `referral_activations.invitee_id` 必须有软删除兼容的唯一索引，避免重复个人码、重复归因和重复领取试用。
- 个人邀请链接生成和邀请链接注册落库时都必须复核邀请者为正式账号、已绑定 ABS 账号且修为至少炼气初期，避免旧链接绕过当前门槛。
- 已存在但被禁用的个人邀请码重新启用时，必须按当前 `id/user_id/is_enabled=false` 条件更新并检查 `RowsAffected`，状态已变化时回滚并提示重试，避免并发下误报已启用。
- 邀请链接 `/start` 进入新人体验注册前，读取当前 Telegram 用户既有正式账号必须区分 `ErrRecordNotFound` 和数据库错误；数据库错误必须记录脱敏日志并停止注册引导，不得误当成没有账号后继续进入体验注册流程。
- 个人邀请链接生成、`/start ref_` 预校验、邀请链接注册落库、试用转正和新人任务领奖读取 `User`、`InviteCode`、`ReferralCode`、`ReferralActivation` 时，必须只把 `gorm.ErrRecordNotFound` 映射成用户不存在、邀请码无效或无可领取任务；数据库错误必须原样返回外层失败分支并记录脱敏日志，不得误报为资格不足、邀请链接无效、邀请码无效或任务不存在。
- `referral_daily_activation_quotas(inviter_id, day_key)` 和 `referral_monthly_reward_quotas(inviter_id, month_key)` 必须使用普通唯一索引，供 SQLite `ON CONFLICT` 原子占用每日激活名额和每月奖励额度。
- 邀请链接注册包含 ABS 开户外部副作用；ABS 开户不得放进数据库事务，本地归因写入失败后必须回滚 ABS 账号。
- 邀请链接创建试用账号时，如果复用本地旧空档案，只能条件更新无 ABS 绑定的目标档案并检查 `RowsAffected`；试用账号使用正式邀请码转正时，核销邀请码和更新 `User.account_type/expire_at` 必须在同一事务内完成，用户更新需限定当前 trial 档案并检查 `RowsAffected`，避免邀请码已核销但本地账号未转正。
- 新人任务检查可在事务外刷新 ABS 听书统计；体验延期、邀请者积分奖励和 `ReferralActivation` 生效标记必须在同一数据库事务内完成，并写入 `PointTransaction(referral_reward)`。延期写 `User.expire_at` 和标记 activation effective 必须做条件更新并检查 `RowsAffected`，未命中时回滚，避免邀请奖励已发但新人有效期或归因状态未落库。
- 普通续期卡核销必须在标记 `RenewCode.is_used=true` 之前拒绝试用账号，防止试用用户消耗正式续期卡绕过转正。
- 试用转正只能核销正式 `InviteCode`，不得生成新 ABS 账号，也不得把普通续期卡当作转正凭证。
