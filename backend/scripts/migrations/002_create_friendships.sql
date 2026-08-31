CREATE TABLE IF NOT EXISTS friend_requests (
    id            BIGINT PRIMARY KEY AUTO_INCREMENT,
    from_user_id  BIGINT NOT NULL,
    to_user_id    BIGINT NOT NULL,
    message       VARCHAR(200) DEFAULT '',
    status        TINYINT NOT NULL DEFAULT 0,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_from_user (from_user_id, status),
    INDEX idx_to_user (to_user_id, status),
    UNIQUE KEY uk_pair (from_user_id, to_user_id)
);

CREATE TABLE IF NOT EXISTS friendships (
    id            BIGINT PRIMARY KEY AUTO_INCREMENT,
    user_id       BIGINT NOT NULL,
    friend_id     BIGINT NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_bidirectional (user_id, friend_id),
    INDEX idx_user (user_id)
);
