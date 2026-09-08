-- MyIM final schema reference after migrations 001..014.
-- This file documents a clean install; the application executes scripts/migrations instead.
CREATE TABLE users (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, username VARCHAR(50) NOT NULL UNIQUE, password_hash VARCHAR(255) NOT NULL,
 nickname VARCHAR(50) NOT NULL DEFAULT '', avatar_url VARCHAR(255) NOT NULL DEFAULT '', sign VARCHAR(255) NOT NULL DEFAULT '', gender TINYINT NOT NULL DEFAULT 0,
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE friend_requests (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, from_user_id BIGINT NOT NULL, to_user_id BIGINT NOT NULL, message VARCHAR(200) DEFAULT '', status TINYINT NOT NULL DEFAULT 0,
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
 UNIQUE KEY uk_pair(from_user_id,to_user_id), INDEX idx_from_user(from_user_id,status), INDEX idx_request_target_page(to_user_id,status,created_at,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE friendships (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, user_id BIGINT NOT NULL, friend_id BIGINT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE KEY uk_bidirectional(user_id,friend_id), INDEX idx_friend_page(user_id,created_at,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE `groups` (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, name VARCHAR(100) NOT NULL, notice VARCHAR(500) DEFAULT '', owner_id BIGINT NOT NULL, max_members INT NOT NULL DEFAULT 500,
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP, INDEX idx_owner(owner_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE group_tombstones (
 group_id BIGINT PRIMARY KEY, owner_id BIGINT NOT NULL, dissolved_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
 INDEX idx_group_tombstone_time(dissolved_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE group_members (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, group_id BIGINT NOT NULL, user_id BIGINT NOT NULL, role TINYINT NOT NULL DEFAULT 0, muted_until DATETIME NULL,
 joined_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, UNIQUE KEY uk_group_user(group_id,user_id), INDEX idx_user(user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE message_id_allocators (
 namespace VARCHAR(64) PRIMARY KEY, next_id BIGINT NOT NULL,
 updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
 CONSTRAINT chk_message_id_next CHECK(next_id BETWEEN 1 AND 9007199254740992)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

INSERT INTO message_id_allocators(namespace,next_id) VALUES('message',1);

CREATE TABLE private_messages (
 id BIGINT PRIMARY KEY, client_msg_id VARCHAR(64) NULL, sender_id BIGINT NOT NULL, receiver_id BIGINT NOT NULL, content TEXT NOT NULL,
 msg_type TINYINT NOT NULL DEFAULT 1, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE KEY uk_private_sender_client(sender_id,client_msg_id), INDEX idx_conv_time(sender_id,receiver_id,created_at), INDEX idx_receiver_time(receiver_id,created_at), FULLTEXT INDEX ft_content(content),
 CONSTRAINT chk_private_message_id CHECK(id BETWEEN 1 AND 9007199254740991)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE group_messages (
 id BIGINT PRIMARY KEY, client_msg_id VARCHAR(64) NULL, group_id BIGINT NOT NULL, sender_id BIGINT NOT NULL, content TEXT NOT NULL, msg_type TINYINT NOT NULL DEFAULT 1,
 group_seq BIGINT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE KEY uk_group_sender_client(sender_id,client_msg_id), UNIQUE KEY uk_group_sequence(group_id,group_seq), INDEX idx_group_time(group_id,created_at), FULLTEXT INDEX ft_content(content),
 CONSTRAINT chk_group_message_id CHECK(id BETWEEN 1 AND 9007199254740991)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE msg_revoked (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, msg_id BIGINT NOT NULL, conv_id VARCHAR(50) NOT NULL, operator_id BIGINT NOT NULL, revoked_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE KEY uk_revoked_conv_msg(conv_id,msg_id), INDEX idx_msg(msg_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE moments (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, author_id BIGINT NOT NULL, content TEXT NOT NULL, media_urls JSON DEFAULT NULL, visibility TINYINT NOT NULL DEFAULT 2,
 created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, INDEX idx_author_time(author_id,created_at), INDEX idx_time(created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE moment_likes (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, moment_id BIGINT NOT NULL, user_id BIGINT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE KEY uk_moment_user(moment_id,user_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE moment_comments (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, moment_id BIGINT NOT NULL, user_id BIGINT NOT NULL, content VARCHAR(500) NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 INDEX idx_comment_page(moment_id,created_at,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE blacklist (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, user_id BIGINT NOT NULL, blocked_id BIGINT NOT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
 UNIQUE KEY uk_pair(user_id,blocked_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE user_settings (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, user_id BIGINT NOT NULL UNIQUE, notification_enabled TINYINT(1) NOT NULL DEFAULT 1, msg_preview_enabled TINYINT(1) NOT NULL DEFAULT 1,
 mute_list JSON DEFAULT NULL, created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP, updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
 FOREIGN KEY(user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE cache_reconcile_events (
 id BIGINT PRIMARY KEY AUTO_INCREMENT, resource_type VARCHAR(32) NOT NULL, resource_id BIGINT NOT NULL,
 status TINYINT NOT NULL DEFAULT 0, attempts INT NOT NULL DEFAULT 0,
 available_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), locked_at DATETIME(6) NULL,
 lock_token VARCHAR(64) NOT NULL DEFAULT '', last_error VARCHAR(500) NOT NULL DEFAULT '',
 created_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6), updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
 INDEX idx_cache_event_claim(status,available_at,id), INDEX idx_cache_event_resource(resource_type,resource_id,id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE message_user_states (
 user_id BIGINT NOT NULL, conv_id VARCHAR(50) NOT NULL, msg_id BIGINT NOT NULL, deleted_at DATETIME(6) NULL,
 PRIMARY KEY(user_id,conv_id,msg_id), INDEX idx_user_deleted(user_id,deleted_at), INDEX idx_conv_msg(conv_id,msg_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;
