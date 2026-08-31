-- Safe to run on existing deployments. Docker's init directory only runs when
-- the data volume is first created, so this migration is also provided for
-- manual application to previously initialized databases.
ALTER TABLE moment_comments
    MODIFY COLUMN id BIGINT NOT NULL AUTO_INCREMENT;
