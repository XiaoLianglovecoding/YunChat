-- MSG-000: reserve globally disjoint message-ID segments in durable MySQL.
-- next_id is the first ID that has not yet been reserved. It may reach
-- 2^53 after handing out Number.MAX_SAFE_INTEGER, but no returned ID may do so.
CREATE TABLE IF NOT EXISTS message_id_allocators (
    namespace VARCHAR(64) NOT NULL,
    next_id BIGINT NOT NULL,
    updated_at DATETIME(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6) ON UPDATE CURRENT_TIMESTAMP(6),
    PRIMARY KEY (namespace),
    CONSTRAINT chk_message_id_next CHECK (next_id BETWEEN 1 AND 9007199254740992)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Keep every persistence path inside the same browser-safe numeric contract,
-- including future MQ consumers that might bypass Generator by mistake.
ALTER TABLE private_messages
    ADD CONSTRAINT chk_private_message_id CHECK (id BETWEEN 1 AND 9007199254740991);
ALTER TABLE group_messages
    ADD CONSTRAINT chk_group_message_id CHECK (id BETWEEN 1 AND 9007199254740991);

-- An upgraded installation may already contain IDs produced by the legacy
-- algorithm. Start strictly after every durable message-ID reference. The
-- duplicate branch also prevents a pre-existing allocator row from moving
-- backwards when this seed statement is deliberately re-run during recovery.
INSERT INTO message_id_allocators(namespace, next_id)
SELECT candidate_namespace, candidate_next_id
FROM (
    SELECT 'message' AS candidate_namespace, GREATEST(
        COALESCE((SELECT MAX(id) FROM private_messages), 0),
        COALESCE((SELECT MAX(id) FROM group_messages), 0),
        COALESCE((SELECT MAX(msg_id) FROM msg_revoked), 0),
        COALESCE((SELECT MAX(msg_id) FROM message_user_states), 0)
    ) + 1 AS candidate_next_id
) AS seed
ON DUPLICATE KEY UPDATE next_id = GREATEST(message_id_allocators.next_id, candidate_next_id);
