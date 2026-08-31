CREATE TABLE IF NOT EXISTS user_settings (
    id                BIGINT PRIMARY KEY AUTO_INCREMENT,
    user_id           BIGINT NOT NULL UNIQUE,
    notification_enabled TINYINT(1) DEFAULT 1,
    msg_preview_enabled  TINYINT(1) DEFAULT 1,
    mute_list         JSON DEFAULT NULL,
    created_at        DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at        DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);
