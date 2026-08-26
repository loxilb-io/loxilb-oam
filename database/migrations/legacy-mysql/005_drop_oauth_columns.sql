-- Migration 005: drop the unused OAuth columns from `users`.
--
-- OAuth login (Google/GitHub/Facebook) was withdrawn before the public release,
-- so nothing reads or writes these columns any more. `oauth_token` in
-- particular held provider access tokens in plaintext and has no business in a
-- published schema.
--
-- Safe to run on any existing database: the application no longer selects these
-- columns, and on deployments that ran the withdrawn flow the values were only
-- ever populated for OAuth-created accounts, which could not authenticate
-- through the password path regardless.
--
-- A future identity-provider integration should model identities in their own
-- table (unique on provider + subject, supporting more than one provider per
-- user) rather than reviving these three columns.
--
-- Idempotent: each statement is skipped when the object is already absent.

USE loxioam;

-- Drop the index on oauth_id first (dropping the column would take it anyway,
-- but this keeps the migration explicit and re-runnable).
SET @idx_exists := (
    SELECT COUNT(1) FROM information_schema.statistics
    WHERE table_schema = DATABASE()
      AND table_name   = 'users'
      AND index_name   = 'idx_users_oauth_id'
);
SET @sql := IF(@idx_exists > 0, 'DROP INDEX idx_users_oauth_id ON users', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

-- Drop the three columns, each only if present.
SET @col_exists := (
    SELECT COUNT(1) FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'users' AND column_name = 'oauth_provider'
);
SET @sql := IF(@col_exists > 0, 'ALTER TABLE users DROP COLUMN oauth_provider', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
    SELECT COUNT(1) FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'users' AND column_name = 'oauth_id'
);
SET @sql := IF(@col_exists > 0, 'ALTER TABLE users DROP COLUMN oauth_id', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;

SET @col_exists := (
    SELECT COUNT(1) FROM information_schema.columns
    WHERE table_schema = DATABASE() AND table_name = 'users' AND column_name = 'oauth_token'
);
SET @sql := IF(@col_exists > 0, 'ALTER TABLE users DROP COLUMN oauth_token', 'DO 0');
PREPARE stmt FROM @sql; EXECUTE stmt; DEALLOCATE PREPARE stmt;
