CREATE TABLE IF NOT EXISTS moments (
    id            BIGINT PRIMARY KEY AUTO_INCREMENT,
    author_id     BIGINT NOT NULL,
    content       TEXT NOT NULL,
    media_urls    JSON DEFAULT NULL,
    visibility    TINYINT NOT NULL DEFAULT 1,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_author_time (author_id, created_at),
    INDEX idx_time (created_at)
);

CREATE TABLE IF NOT EXISTS moment_likes (
    id            BIGINT PRIMARY KEY AUTO_INCREMENT,
    moment_id     BIGINT NOT NULL,
    user_id       BIGINT NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_moment_user (moment_id, user_id),
    INDEX idx_moment (moment_id)
);

CREATE TABLE IF NOT EXISTS moment_comments (
    id            BIGINT PRIMARY KEY AUTO_INCREMENT,
    moment_id     BIGINT NOT NULL,
    user_id       BIGINT NOT NULL,
    content       VARCHAR(500) NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_moment_time (moment_id, created_at)
);
