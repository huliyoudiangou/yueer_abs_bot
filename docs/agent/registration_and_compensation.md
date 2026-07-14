# 开放注册与服务补偿运维规则

## 目标与风险级别

本模块用于临时开放免邀请码注册，以及服务宕机后的全体用户统一补偿。两个入口统一位于私聊 Bot 的 `超级管理员控制台 → ⚙️ 系统配置`。两项能力均属于超级管理员高危运维能力：开放注册会改变开户注册权限边界；用户补偿会直接变更积分和 ABS 有效期。必须私聊执行、二次确认、写入审计，并保持数据库资产变动与 Telegram/ABS 外部副作用分离。

## 开放注册

### 数据模型与状态

- `OpenRegistrationCampaign` 保存活动编号、模式、人数上限或结束时间、状态、创建人、原因和关闭时间。
- 模式只有 `quota`（限制注册总人数）和 `duration`（限制开放时长）。
- 活动状态使用 `active` / `closed`。
- `OpenRegistrationReservation` 以 `(campaign_id, user_id)` 唯一，名额状态为 `reserved`、`completed`、`released`。
- 数据库使用部分唯一索引 `idx_open_registration_single_active`，仅允许一条 `status='active' AND deleted_at IS NULL` 的活动，避免并发创建多个开放窗口。

### 注册优先级

1. 通过个人邀请链接进入的新人体验注册继续走 referral 流程。
2. 积分兑换等流程已预填邀请码时，继续走邀请码开户注册，不占开放注册名额。
3. `INVITE_REQUIRED=false` 表示全局免邀请码，保持原行为。
4. 仅当 `INVITE_REQUIRED=true`、没有 referral、没有预填邀请码时，才检查当前 active 开放注册活动。
5. 活动状态读取失败时 fail closed，保守回退到邀请码流程。

### 人数模式并发规则

- 用户提交最终开户注册前，必须在数据库事务内创建或恢复本人的 `reserved` 名额。
- 创建预占后，在同一事务内统计本活动 `reserved + completed` 数量并二次复核；超过上限必须回滚预占，不能依赖“先查再写”。
- ABS 开户失败时，在独立短事务中把名额改为 `released`。
- ABS 开户成功且本地用户档案写入成功时，必须在同一个本地事务中把名额改为 `completed` 并写 `OPEN_REGISTRATION_COMPLETE` 审计。
- 本地事务失败时，先在事务外回滚 ABS 账号；ABS 回滚成功后再释放开放注册名额。若 ABS 回滚失败，不得把流程伪报为安全完成，必须提示人工核查。

### 时长模式与关闭

- 时长模式保存明确的 `EndsAt`，最长开放 7 天。
- 查询活动、预占名额和状态展示时都会尝试把已到期活动条件更新为 `closed`。
- 超级管理员可提前关闭；创建和关闭均要求原因、二次确认和审计。
- 开放注册只覆盖邀请码门槛，不改变用户名、安全码、密码、ABS 开户、本地档案和既有账号重复注册校验。

## 用户补偿

### 创建与用户快照

- `CompensationCampaign` 保存活动编号、创建人、每人积分、每人天数、事故说明、状态、接收人数和完成统计。
- 创建时读取当前本地用户列表，并在同一事务内创建活动和每位用户一条 `CompensationGrant`；后续新注册用户不会自动加入本期补偿。
- `(campaign_id, user_id)` 唯一索引保证同一活动每位用户只有一条发放记录。
- 支持仅积分、仅 ABS 天数、积分与天数同时补偿；积分上限和天数上限继续由管理向导校验。
- 创建必须写 `CREATE_USER_COMPENSATION` 审计，完成状态抢占与 `COMPLETE_USER_COMPENSATION` 审计在同一事务内提交。

### 单用户资产事务

- 每位用户单独使用短事务，避免全服补偿形成长事务和大范围锁等待。
- 积分通过 `applyPointDeltaInTx` 发放，流水类型固定为 `service_outage_compensation`，`RefType=compensation_campaign`，`RefID=活动编号`。
- 积分、该用户有效期调整、发放前后快照和 Grant 从 `pending` 到 `applied` 的条件更新必须在同一事务内完成；任一步失败整体回滚。
- 有限期 ABS 账号：未过期时从原到期时间延长，已过期时从当前时间延长。
- 无限期账号（`ExpireAt=nil`）保持无限期；未绑定 ABS 的本地积分档案不补 ABS 天数，但仍可接收积分。
- 原本因到期处于暂停状态且成功延长有效期的账号，在事务提交后调用 ABS 恢复；ABS 外部调用成功后再用独立短事务同步本地状态并写恢复审计。

### 后台恢复与幂等

- 活动状态为 `queued` / `processing`，后台启动时立即扫描，此后每 30 秒扫描一次。
- `CompensationGrant` 状态为 `pending`、`applied`、`sent`、`delivery_failed`。只有 `pending` 才能变更资产，条件更新和唯一索引共同防止重复发放。
- 单用户资产事务失败时保持 `pending`，累计 `AttemptCount`、`LastAttemptAt` 和脱敏截断后的 `LastError`，后续扫描继续重试。
- 进程重启后，未完成的 `queued` / `processing` 活动会继续扫描；只有不存在 `pending` / `applied` 明细时才能收口为 completed。

### 外部通知与失败语义

- Telegram 私聊和 ABS API 调用不得放入资产事务。
- Telegram 私聊失败只把 Grant 标记为 `delivery_failed`，不得回滚已到账积分或天数。
- Telegram 发送成功但发送状态写回失败时，下轮理论上可能重复发送通知；当前 Telegram Bot API 没有业务幂等键，这是现有实现的已知边界。日志必须保留活动编号、用户 ID 和脱敏错误，便于人工判断。
- ABS 解封失败会保存 `ReactivationError` 并计入管理员完成回执，但当前不会无限自动重试；管理员必须人工核查这些账号的 ABS 与本地状态。
- 完成回执中的 ABS 恢复失败统计若读取失败，应显示“读取失败”并记录日志，不得把已经完成的资产任务重新标记为失败。

## 运维核查与剩余边界

- 管理员完成回执至少核对接收用户、成功私聊、私聊失败、ABS 恢复失败。
- 当前尚未提供按活动查询失败明细或手动重发通知的管理命令；需要通过数据库只读查询和日志定位 `delivery_failed`、`reactivation_ok=false`、持续 `pending` 的记录。
- 当前宿主机 Go 工具链为 `CGO_ENABLED=0`，纯逻辑与源码守护测试不打开 SQLite。真实事务、部分唯一索引和并发竞争验证需要在带 cgo/gcc 的 Docker 或 CI 环境运行，未执行时不得宣称已完成数据库并发实测。