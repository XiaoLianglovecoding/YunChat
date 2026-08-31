-- Foundation hardening: retain the upstream tables, then add idempotency and per-user state.
ALTER TABLE private_messages
    ADD COLUMN client_msg_id VARCHAR(64) NULL AFTER id,
    ADD UNIQUE KEY uk_private_sender_client (sender_id, client_msg_id);

ALTER TABLE group_messages
    ADD COLUMN client_msg_id VARCHAR(64) NULL AFTER id,
    ADD UNIQUE KEY uk_group_sender_client (sender_id, client_msg_id),
    ADD UNIQUE KEY uk_group_sequence (group_id, group_seq);

ALTER TABLE msg_revoked
    ADD UNIQUE KEY uk_revoked_conv_msg (conv_id, msg_id);

ALTER TABLE friend_requests
    ADD INDEX idx_request_target_page (to_user_id, status, created_at, id);

ALTER TABLE moment_comments
    ADD INDEX idx_comment_page (moment_id, created_at, id);

-- Remove indexes whose complete key order is already covered by a UNIQUE or wider index.
ALTER TABLE users DROP INDEX idx_username;
ALTER TABLE friendships DROP INDEX idx_user;
ALTER TABLE group_members DROP INDEX idx_group;
ALTER TABLE group_messages DROP INDEX idx_group_seq;
ALTER TABLE moment_likes DROP INDEX idx_moment;
ALTER TABLE moment_comments DROP INDEX idx_moment_time;
ALTER TABLE blacklist DROP INDEX idx_user;
ALTER TABLE friend_requests DROP INDEX idx_to_user;

UPDATE users SET nickname = '' WHERE nickname IS NULL;
UPDATE users SET avatar_url = '' WHERE avatar_url IS NULL;
UPDATE users SET sign = '' WHERE sign IS NULL;
ALTER TABLE users
    MODIFY nickname VARCHAR(50) NOT NULL DEFAULT '',
    MODIFY avatar_url VARCHAR(255) NOT NULL DEFAULT '',
    MODIFY sign VARCHAR(255) NOT NULL DEFAULT '',
    MODIFY gender TINYINT NOT NULL DEFAULT 0;

UPDATE moments SET visibility = 2 WHERE visibility = 1;
ALTER TABLE moments MODIFY visibility TINYINT NOT NULL DEFAULT 2;

ALTER TABLE user_settings
    MODIFY notification_enabled TINYINT(1) NOT NULL DEFAULT 1,
    MODIFY msg_preview_enabled TINYINT(1) NOT NULL DEFAULT 1,
    MODIFY created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    MODIFY updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP;

CREATE TABLE IF NOT EXISTS message_user_states (
    user_id BIGINT NOT NULL,
    conv_id VARCHAR(50) NOT NULL,
    msg_id BIGINT NOT NULL,
    deleted_at DATETIME(6) NULL,
    PRIMARY KEY (user_id, conv_id, msg_id),
    INDEX idx_user_deleted (user_id, deleted_at),
    INDEX idx_conv_msg (conv_id, msg_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
