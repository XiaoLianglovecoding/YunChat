CREATE TABLE IF NOT EXISTS private_messages (
    id            BIGINT PRIMARY KEY,
    sender_id     BIGINT NOT NULL,
    receiver_id   BIGINT NOT NULL,
    content       TEXT NOT NULL,
    msg_type      TINYINT NOT NULL DEFAULT 1,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_conv_time (sender_id, receiver_id, created_at),
    INDEX idx_receiver_time (receiver_id, created_at),
    FULLTEXT INDEX ft_content (content)
);

CREATE TABLE IF NOT EXISTS group_messages (
    id            BIGINT PRIMARY KEY,
    group_id      BIGINT NOT NULL,
    sender_id     BIGINT NOT NULL,
    content       TEXT NOT NULL,
    msg_type      TINYINT NOT NULL DEFAULT 1,
    group_seq     BIGINT NOT NULL,
    created_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_group_seq (group_id, group_seq),
    INDEX idx_group_time (group_id, created_at),
    FULLTEXT INDEX ft_content (content)
);

CREATE TABLE IF NOT EXISTS msg_revoked (
    id            BIGINT PRIMARY KEY AUTO_INCREMENT,
    msg_id        BIGINT NOT NULL,
    conv_id       VARCHAR(50) NOT NULL,
    operator_id   BIGINT NOT NULL,
    revoked_at    DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    INDEX idx_msg (msg_id)
);
