CREATE TABLE IF NOT EXISTS `groups` (
    id            BIGINT PRIMARY KEY AUTO_INCREMENT,
    name          VARCHAR(100) NOT NULL,
    notice        VARCHAR(500) DEFAULT '',
    owner_id      BIGINT NOT NULL,
    max_members   INT NOT NULL DEFAULT 500,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_owner (owner_id)
);

CREATE TABLE IF NOT EXISTS group_members (
    id            BIGINT PRIMARY KEY AUTO_INCREMENT,
    group_id      BIGINT NOT NULL,
    user_id       BIGINT NOT NULL,
    role          TINYINT NOT NULL DEFAULT 0,
    muted_until   DATETIME NULL,
    joined_at     DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_group_user (group_id, user_id),
    INDEX idx_group (group_id),
    INDEX idx_user (user_id)
);
