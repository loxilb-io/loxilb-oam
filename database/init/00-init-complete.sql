-- Complete database initialization for Docker (runs automatically on first startup)
-- This file combines schema creation and data insertion for Docker Compose

USE loxioam;

-- Users table (role column drives RBAC)
CREATE TABLE IF NOT EXISTS users (
    id INT AUTO_INCREMENT PRIMARY KEY,
    username VARCHAR(255) NOT NULL UNIQUE,
    password VARCHAR(255) NOT NULL,
    -- RBAC Phase 2 3-role model; 'user' kept as legacy alias of operator
    role ENUM('admin', 'operator', 'viewer', 'user') DEFAULT 'viewer',
    email VARCHAR(255) DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    credentials_updated BOOLEAN DEFAULT FALSE COMMENT 'True if user has updated from default credentials',
    credentials_updated_at TIMESTAMP NULL COMMENT 'When credentials were last updated',
    must_change_password BOOLEAN DEFAULT FALSE COMMENT 'True if user must change password on next login'
);

-- LoxiLB instances table
CREATE TABLE IF NOT EXISTS loxilb_instances (
    id INT AUTO_INCREMENT PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    host VARCHAR(255) NOT NULL,
    port VARCHAR(255) NOT NULL,
    protocol VARCHAR(10) NOT NULL DEFAULT 'https',
    description TEXT,
    version VARCHAR(255) NOT NULL,
    api_endpoint VARCHAR(255) NOT NULL UNIQUE,
    cimage VARCHAR(255) NOT NULL,
    ctag VARCHAR(255) NOT NULL,
    is_active BOOLEAN DEFAULT TRUE,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- API tokens table
CREATE TABLE IF NOT EXISTS api_tokens (
    token_id INT AUTO_INCREMENT PRIMARY KEY,
    token_value TEXT NOT NULL,
    user_id VARCHAR(255) NOT NULL,
    scopes TEXT,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE KEY uk_token_value (token_value(255))
);

-- Logs table
CREATE TABLE IF NOT EXISTS logs (
    id INT AUTO_INCREMENT PRIMARY KEY,
    level VARCHAR(10),
    timestamp DATETIME,
    severity VARCHAR(45),
    facility VARCHAR(45),
    programname VARCHAR(45),
    host VARCHAR(255),
    message TEXT,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

-- Alerts table
CREATE TABLE IF NOT EXISTS alerts (
    id INT AUTO_INCREMENT PRIMARY KEY,
    instance_id INT NOT NULL,
    type ENUM('DB_DISCONNECT', 'API_UNREACHABLE', 'HIGH_CPU', 'MEMORY_LEAK') NOT NULL,
    severity ENUM('INFO', 'WARNING', 'CRITICAL') NOT NULL,
    message TEXT NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    resolved_at TIMESTAMP NULL DEFAULT NULL,
    FOREIGN KEY (instance_id) REFERENCES loxilb_instances(id) ON DELETE CASCADE
);

-- Acknowledgment table
CREATE TABLE IF NOT EXISTS acknowledgments (
    id INT AUTO_INCREMENT PRIMARY KEY,
    alert_id INT NOT NULL,
    user_id INT NOT NULL,
    ack_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (alert_id) REFERENCES alerts(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

-- System settings table for persistent configuration
CREATE TABLE IF NOT EXISTS system_settings (
    id INT AUTO_INCREMENT PRIMARY KEY,
    setting_key VARCHAR(100) NOT NULL UNIQUE,
    setting_value TEXT NOT NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    INDEX idx_setting_key (setting_key)
);

-- System-wide configuration table (for global configuration tracking - see migration 001_add_admin_credential_tracking.sql)
CREATE TABLE IF NOT EXISTS system_config (
    config_key VARCHAR(50) PRIMARY KEY COMMENT 'Configuration key identifier',
    config_value TEXT NOT NULL COMMENT 'Configuration value (JSON or string)',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT 'When configuration was created',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT 'When configuration was last updated'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='System-wide configuration storage';

-- Seed initial configuration values for admin credential tracking
INSERT INTO system_config (config_key, config_value) VALUES
('admin_credentials_updated', 'false'),
('admin_setup_version', '1.0')
ON DUPLICATE KEY UPDATE config_value = config_value;

-- Initialize system first boot timestamp
INSERT INTO system_settings (setting_key, setting_value) 
VALUES ('first_boot_at', NOW())
ON DUPLICATE KEY UPDATE setting_value = setting_value;

-- Insert dummy data into loxilb_instances table (only if empty).
-- api_endpoint MUST agree with protocol/host/port/version: the server derives
-- it from those four fields on every write, so a seed row that disagrees
-- (this one said https:// while protocol was http) is a row the application
-- itself could never produce.
INSERT IGNORE INTO loxilb_instances (name, host, port, protocol, description, version, api_endpoint, cimage, ctag) VALUES
('LoxiLB-Instance-1', 'loxilb-enterprise', '11111', 'http', 'Local LoxiLB instance', 'v1', 'http://loxilb-enterprise:11111/netlox/v1', 'ghcr.io/loxilb-io/loxilb', 'v0.9.8.7');

-- Create indexes for performance (only if they don't exist)
-- We'll use a different approach to avoid duplicate key errors

-- Create a stored procedure to create indexes safely
DELIMITER $$

CREATE PROCEDURE CreateIndexIfNotExists(
    IN tableName VARCHAR(64),
    IN indexName VARCHAR(64), 
    IN columns VARCHAR(255)
)
BEGIN
    DECLARE indexExists INTEGER DEFAULT 0;
    
    SELECT COUNT(1) INTO indexExists
    FROM information_schema.statistics 
    WHERE table_schema = DATABASE() 
    AND table_name = tableName 
    AND index_name = indexName;
    
    IF indexExists = 0 THEN
        SET @sql = CONCAT('CREATE INDEX ', indexName, ' ON ', tableName, '(', columns, ')');
        PREPARE stmt FROM @sql;
        EXECUTE stmt;
        DEALLOCATE PREPARE stmt;
    END IF;
END$$

DELIMITER ;

-- Create indexes using the stored procedure
CALL CreateIndexIfNotExists('users', 'idx_users_username', 'username');
CALL CreateIndexIfNotExists('users', 'idx_users_credentials_updated', 'credentials_updated');
CALL CreateIndexIfNotExists('users', 'idx_users_username_credentials', 'username, credentials_updated');
CALL CreateIndexIfNotExists('loxilb_instances', 'idx_loxilb_instances_api_endpoint', 'api_endpoint');
CALL CreateIndexIfNotExists('api_tokens', 'idx_api_tokens_user_id', 'user_id');
CALL CreateIndexIfNotExists('api_tokens', 'idx_api_tokens_expires_at', 'expires_at');
CALL CreateIndexIfNotExists('logs', 'idx_logs_timestamp', 'timestamp');
CALL CreateIndexIfNotExists('logs', 'idx_logs_level', 'level');
CALL CreateIndexIfNotExists('alerts', 'idx_alerts_instance_id', 'instance_id');
CALL CreateIndexIfNotExists('alerts', 'idx_alerts_created_at', 'created_at');
CALL CreateIndexIfNotExists('acknowledgments', 'idx_acknowledgments_alert_id', 'alert_id');

-- (legacy config_exports table removed — replaced by instance_snapshots)

-- Login attempts table for tracking failed login attempts and lockouts
-- Prevents brute force attacks by blocking access after repeated failures
CREATE TABLE IF NOT EXISTS login_attempts (
    id INT AUTO_INCREMENT PRIMARY KEY,
    username VARCHAR(255) NOT NULL,
    client_ip VARCHAR(64) NOT NULL,
    failed_count INT NOT NULL DEFAULT 0,
    last_failed_at DATETIME NOT NULL,
    blocked_until DATETIME NULL,
    created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
    UNIQUE KEY uniq_username_ip (username, client_ip),
    INDEX idx_blocked_until (blocked_until),
    INDEX idx_username (username),
    INDEX idx_last_failed_at (last_failed_at)
);

-- Drop the stored procedure as it's no longer needed
DROP PROCEDURE CreateIndexIfNotExists;

-- Instance snapshot orchestration.
-- Kept in sync with database/migrations/003_add_instance_snapshots.sql.
-- Blobs live IN the database — container-local files are what broke the
-- legacy config-export feature.
CREATE TABLE IF NOT EXISTS instance_snapshots (
    id                CHAR(36) PRIMARY KEY,
    instance_id       INT NOT NULL,
    name              VARCHAR(128) NOT NULL,
    description       TEXT,
    trigger_type      ENUM('manual','scheduled','pre_restore','pre_upgrade') NOT NULL,
    schema_version    VARCHAR(16)  NOT NULL,
    gateway_version   VARCHAR(64)  NOT NULL,
    size_bytes        INT UNSIGNED NOT NULL,
    checksum          CHAR(71) NOT NULL,
    stored_checksum   CHAR(71) NOT NULL,
    snapshot_blob     MEDIUMBLOB NOT NULL,
    encrypted         BOOLEAN NOT NULL DEFAULT FALSE,
    pinned            BOOLEAN NOT NULL DEFAULT FALSE,
    checksum_ok       BOOLEAN NOT NULL DEFAULT TRUE,
    created_by        VARCHAR(64) NOT NULL,
    created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    restore_count     INT UNSIGNED NOT NULL DEFAULT 0,
    last_restored_at  TIMESTAMP NULL,
    last_restore_result ENUM('ok','rolled_back','rollback_failed') NULL,
    last_restore_response MEDIUMTEXT NULL,
    INDEX idx_snap_instance (instance_id, created_at DESC),
    CONSTRAINT fk_snap_instance FOREIGN KEY (instance_id)
        REFERENCES loxilb_instances(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS instance_snapshot_schedules (
    instance_id       INT PRIMARY KEY,
    enabled           BOOLEAN NOT NULL DEFAULT FALSE,
    interval_hours    INT UNSIGNED NOT NULL DEFAULT 24,
    retain_count      INT UNSIGNED NOT NULL DEFAULT 10,
    last_run_at       TIMESTAMP NULL,
    last_run_result   VARCHAR(255) NULL,
    CONSTRAINT fk_sched_instance FOREIGN KEY (instance_id)
        REFERENCES loxilb_instances(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;