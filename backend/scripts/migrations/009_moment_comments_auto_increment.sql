-- Existing deployments: assign new comments a database-generated sequential ID.
-- Existing IDs are intentionally preserved so links to historical comments remain valid.
ALTER TABLE moment_comments
    MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT;

-- Resets empty development/test tables; populated tables keep max(id) + 1.
ALTER TABLE moment_comments AUTO_INCREMENT = 1;
