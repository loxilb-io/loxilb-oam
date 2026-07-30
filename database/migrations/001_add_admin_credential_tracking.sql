-- Migration: Add admin credential tracking columns to users table
-- This migration adds support for tracking when admin credentials are updated from defaults

-- Add credential tracking columns to existing users table
ALTER TABLE users
ADD COLUMN credentials_updated BOOLEAN DEFAULT FALSE COMMENT 'True if user has updated from default credentials',
ADD COLUMN credentials_updated_at TIMESTAMP NULL COMMENT 'When credentials were last updated',
ADD COLUMN must_change_password BOOLEAN DEFAULT FALSE COMMENT 'True if user must change password on next login';

-- Create system_config table for global configuration tracking
CREATE TABLE IF NOT EXISTS system_config (
    config_key VARCHAR(50) PRIMARY KEY COMMENT 'Configuration key identifier',
    config_value TEXT NOT NULL COMMENT 'Configuration value (JSON or string)',
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP COMMENT 'When configuration was created',
    updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP COMMENT 'When configuration was last updated'
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci COMMENT='System-wide configuration storage';

-- Insert initial configuration values for admin credential tracking
INSERT INTO system_config (config_key, config_value) VALUES
('admin_credentials_updated', 'false') ON DUPLICATE KEY UPDATE config_value = config_value,
('admin_setup_version', '1.0') ON DUPLICATE KEY UPDATE config_value = config_value;

-- Create index for faster credential status queries
CREATE INDEX idx_users_credentials_updated ON users (credentials_updated);
CREATE INDEX idx_users_username_credentials ON users (username, credentials_updated);