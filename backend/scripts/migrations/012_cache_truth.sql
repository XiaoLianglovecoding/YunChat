-- CACHE-001: MySQL 是关系数据的唯一真相，Redis 只保存可重建的投影。

-- 好友列表按 (created_at,id) 稳定翻页；user_id 是最左前缀，可以同时服务单用户扫描。
ALTER TABLE friendships
    ADD INDEX idx_friend_page (user_id, created_at, id);

-- 这不是“把某个 Redis key SET/DEL”的命令表，而是“请重新读取该资源的当前真相”。
-- 因而即使旧事件晚到，消费者也只会把 Redis 修复为 MySQL 的最新状态，不会复活旧关系。
CREATE TABLE IF NOT EXISTS cache_reconcile_events (
    id BIGINT NOT NULL AUTO_INCREMENT,
    resource_type VARCHAR(32) NOT NULL,
    resource_id BIGINT NOT NULL,
    status TINYINT NOT NULL DEFAULT 0 COMMENT '0=pending,1=processing,2=done',
    attempts INT NOT NULL DEFAULT 0,
    available_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    locked_at DATETIME(6) NULL,
    lock_token VARCHAR(64) NOT NULL DEFAULT '',
    last_error VARCHAR(500) NOT NULL DEFAULT '',
    created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (id),
    INDEX idx_cache_event_claim (status, available_at, id),
    INDEX idx_cache_event_resource (resource_type, resource_id, id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
