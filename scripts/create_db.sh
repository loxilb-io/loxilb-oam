#!/bin/bash

# Database credentials — from the canonical DB_* env family. No password ships
# in source: DB_PASSWORD must be set in the environment.
DB_USER="${DB_USER:-oamuser}"
DB_PASSWORD="${DB_PASSWORD:?set DB_PASSWORD in the environment}"
DB_HOST="${DB_HOST:-127.0.0.1}"
DB_PORT="${DB_PORT:-3306}"
DB_NAME="${DB_NAME:-loxioam}"

# SQL commands to create the database and tables
SQL_COMMANDS="
CREATE DATABASE IF NOT EXISTS ${DB_NAME};

USE ${DB_NAME};

CREATE TABLE IF NOT EXISTS users (
    id INT AUTO_INCREMENT PRIMARY KEY,
    username VARCHAR(255) NOT NULL UNIQUE,
    password VARCHAR(255) NOT NULL,

    oauth_provider VARCHAR(50) DEFAULT NULL,
    email VARCHAR(255) DEFAULT NULL,
    oauth_id VARCHAR(255) DEFAULT NULL,
    oauth_token TEXT DEFAULT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

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
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
);

CREATE TABLE IF NOT EXISTS api_tokens (
    token_id SERIAL PRIMARY KEY,
    token_value TEXT NOT NULL,
    user_id TEXT NOT NULL,
    scopes TEXT,
    expires_at TIMESTAMP NOT NULL,
    created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (token_value(255))
);

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

-- Alerts Table
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

-- Acknowledgment Table
CREATE TABLE IF NOT EXISTS acknowledgments (
    id INT AUTO_INCREMENT PRIMARY KEY,
    alert_id INT NOT NULL,
    user_id INT NOT NULL,
    ack_time TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
    FOREIGN KEY (alert_id) REFERENCES alerts(id) ON DELETE CASCADE,
    FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
);

"

# Execute the SQL commands
mysql -u${DB_USER} -p${DB_PASSWORD} -h${DB_HOST} -P${DB_PORT} -e "${SQL_COMMANDS}"