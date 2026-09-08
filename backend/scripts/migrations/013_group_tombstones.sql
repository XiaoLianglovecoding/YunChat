-- A disbanded group no longer has a row in `groups`, but its ID must remain a
-- durable negative fact. Redis can be restored from an older backup; without
-- this tombstone that old positive membership projection could be trusted.
CREATE TABLE IF NOT EXISTS group_tombstones (
    group_id      BIGINT PRIMARY KEY,
    owner_id      BIGINT NOT NULL,
    dissolved_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
    INDEX idx_group_tombstone_time (dissolved_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
