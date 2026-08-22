-- =====================================================================
-- 灵侍捕捉一致性对账（只读 SELECT，可安全在生产库执行）
--
-- 背景：2026-08 之前上线的版本（提交 4ef3ff8 之前）捕捉流程无事务包装：
--   扣灵晶独立提交后，若进程在「灵侍落库 / 保底保存」之前崩溃或遇 DB 错误，
--   会出现「灵晶已扣、灵侍未得」的脏数据。本脚本用于排查该窗口的候选异常。
--
-- 判定逻辑（三条同时满足才是候选）：
--   1. 存在 type='consume_catch' 的灵晶流水；
--   2. 该用户在流水后 60 秒内没有新灵侍落库（排除「捕捉成功」）；
--   3. 对应区域的保底记录在该时刻之后从未更新过
--      （排除「正常逃脱」——正常完成无论成败都会在同一时刻更新保底行）。
--
-- 使用方式（服务器侧，部署目录）：
--   sqlite3 data/abs_bot.db < audit_catch_consistency.sql
--
-- 对候选行再核对容器日志（每条完成的捕捉都会写一行，圣/天保底成功路径除外）：
--   docker logs abs_tg_bot 2>&1 | grep "灵侍] 捕捉 user=<user_id>"
--   有日志且 success=false → 正常逃脱，保底计数可能丢失（轻微，无资产损失）
--   有日志且 success=true  → 理论上灵侍应存在，若缺失需人工核查
--   该时刻无日志行         → 流程中断（脏数据）；如需补偿须走管理员人工调账流程
--                            （事务内操作 + 写灵晶流水 + AuditLog 审计）
--
-- 已知局限：若同一用户同一区域在异常之后又正常拉抽过，保底行会被刷新，
-- 该次异常可能被掩盖（漏报）；此时只能依赖日志核对。
-- =====================================================================
SELECT
    lt.user_id        AS user_id,
    lt.created_at     AS spend_at,
    lt.description    AS pull_desc,
    p.total_pulls     AS pity_total_pulls,
    p.updated_at      AS pity_last_update
FROM lingjing_transactions lt
LEFT JOIN user_spirit_servants s
       ON s.user_id = lt.user_id
      AND s.created_at >= lt.created_at
      AND s.created_at <= datetime(lt.created_at, '+60 seconds')
LEFT JOIN spirit_zone_pities p
       ON p.user_id = lt.user_id
      AND p.zone_key = CASE
            WHEN lt.description LIKE '%青竹林海%' THEN 'qingzhu'
            WHEN lt.description LIKE '%迷雾深谷%' THEN 'wumu'
            WHEN lt.description LIKE '%断岳山脉%' THEN 'duanyue'
            WHEN lt.description LIKE '%幽冥绝岭%' THEN 'youming'
            WHEN lt.description LIKE '%归墟海眼%' THEN 'guixu'
            WHEN lt.description LIKE '%不周山巅%' THEN 'buzhou'
        END
WHERE lt.type = 'consume_catch'
  AND s.id IS NULL
  AND (p.id IS NULL OR p.updated_at < lt.created_at);
