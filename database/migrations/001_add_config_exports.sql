-- Add configuration exports table for OAM Configuration Management
-- File: database/migrations/001_add_config_exports.sql
-- This migration adds the config_exports table to support configuration export/import functionality

USE loxioam;

-- Create config_exports table for tracking configuration exports
CREATE TABLE IF NOT EXISTS config_exports (
    id VARCHAR(64) PRIMARY KEY,
    export_type ENUM('manual', 'scheduled') DEFAULT 'manual',
    exported_by VARCHAR(255) NOT NULL,
    description TEXT,
    file_path VARCHAR(500) NOT NULL,
    file_size BIGINT NOT NULL,
    checksum VARCHAR(64) NOT NULL,
    exported_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    expires_at TIMESTAMP NOT NULL,
    download_count INT DEFAULT 0,
    last_downloaded_at TIMESTAMP NULL,
    
    INDEX idx_exported_by (exported_by),
    INDEX idx_exported_at (exported_at),
    INDEX idx_expires_at (expires_at)
);

-- Insert initial comment for tracking
INSERT INTO config_exports (id, export_type, exported_by, description, file_path, file_size, checksum, exported_at, expires_at) 
VALUES ('migration_marker', 'manual', 'system', 'Migration marker - safe to delete', '/dev/null', 0, 'migration', NOW(), DATE_ADD(NOW(), INTERVAL 1 DAY))
ON DUPLICATE KEY UPDATE id=id;

-- Remove the marker immediately (this ensures table creation worked)
DELETE FROM config_exports WHERE id = 'migration_marker';