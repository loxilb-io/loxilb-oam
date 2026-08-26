-- Instance snapshot orchestration.
-- Snapshot blobs live IN the database (gzip'd, optionally AES-256-GCM) — never
-- on container-local disk; that is what killed the legacy config-export feature
-- (rows persisted in MySQL while files died with the container).
-- NOTE: instance_id is INT to match loxilb_instances.id (the design doc's
-- BIGINT was written before checking the live schema).

USE loxioam;

CREATE TABLE IF NOT EXISTS instance_snapshots (
    id                CHAR(36) PRIMARY KEY,             -- UUID
    instance_id       INT NOT NULL,                     -- FK -> loxilb_instances.id
    name              VARCHAR(128) NOT NULL,            -- operator-facing label
    description       TEXT,
    trigger_type      ENUM('manual','scheduled','pre_restore','pre_upgrade') NOT NULL,
    schema_version    VARCHAR(16)  NOT NULL,            -- from snapshot envelope
    gateway_version   VARCHAR(64)  NOT NULL,            -- from snapshot envelope
    size_bytes        INT UNSIGNED NOT NULL,            -- uncompressed JSON size
    checksum          CHAR(71) NOT NULL,                -- "sha256:<hex>", gateway-computed (envelope)
    stored_checksum   CHAR(71) NOT NULL,                -- "sha256:<hex>" over the raw JSON bytes as received, OAM-computed
    snapshot_blob     MEDIUMBLOB NOT NULL,              -- gzip(JSON), AES-256-GCM when encrypted
    encrypted         BOOLEAN NOT NULL DEFAULT FALSE,
    pinned            BOOLEAN NOT NULL DEFAULT FALSE,   -- exempt from retention
    checksum_ok       BOOLEAN NOT NULL DEFAULT TRUE,    -- integrity sweep verdict (§7)
    created_by        VARCHAR(64) NOT NULL,             -- JWT username
    created_at        TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    restore_count     INT UNSIGNED NOT NULL DEFAULT 0,
    last_restored_at  TIMESTAMP NULL,
    last_restore_result ENUM('ok','rolled_back','rollback_failed') NULL,
    last_restore_response MEDIUMTEXT NULL,              -- full gateway restore response JSON (audit, §5)
    INDEX idx_snap_instance (instance_id, created_at DESC),
    CONSTRAINT fk_snap_instance FOREIGN KEY (instance_id)
        REFERENCES loxilb_instances(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

CREATE TABLE IF NOT EXISTS instance_snapshot_schedules (
    instance_id       INT PRIMARY KEY,                  -- FK -> loxilb_instances.id
    enabled           BOOLEAN NOT NULL DEFAULT FALSE,
    interval_hours    INT UNSIGNED NOT NULL DEFAULT 24, -- simple interval, not cron (v1)
    retain_count      INT UNSIGNED NOT NULL DEFAULT 10, -- per-instance keep-N (unpinned)
    last_run_at       TIMESTAMP NULL,
    last_run_result   VARCHAR(255) NULL,
    CONSTRAINT fk_sched_instance FOREIGN KEY (instance_id)
        REFERENCES loxilb_instances(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;
