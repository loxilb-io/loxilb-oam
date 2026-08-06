# MySQL Database Schema

The LoxiLB OAM system uses a MySQL database to store users, managed LoxiLB
instances, API tokens, logs, alerts, acknowledgments, login-attempt tracking,
instance snapshots, and system configuration.

This document is the schema reference. The authoritative definitions live in:

- `database/init/00-init-complete.sql` — full schema for fresh installs (runs
  automatically on first container startup)
- `database/migrations/*.sql` — incremental migrations applied to existing
  databases

## Database Information

- **Database Name:** `loxioam`
- **Default DB User:** `oamuser` (override with `-db-user` or the `DB_USER` env
  var; the canonical connection surface is the `DB_*` family — see
  [DEPLOYMENT.md](../DEPLOYMENT.md))
- **Default Host / Port:** `127.0.0.1` / `3306`

Credentials are supplied at runtime through environment variables and CLI flags;
they are not hardcoded.

## Tables

### `users`

Stores user authentication details and RBAC role.

| Column                   | Type | Description |
|--------------------------|------|-------------|
| `id`                     | `INT AUTO_INCREMENT PRIMARY KEY` | Unique user ID |
| `username`               | `VARCHAR(255) NOT NULL UNIQUE` | Unique username |
| `password`               | `VARCHAR(255) NOT NULL` | bcrypt password hash |
| `role`                   | `ENUM('admin','operator','viewer','user') DEFAULT 'viewer'` | RBAC role. `user` is a legacy alias treated as `operator`. |
| `oauth_provider`         | `VARCHAR(50) DEFAULT NULL` | **Unused** — see note below |
| `email`                  | `VARCHAR(255) DEFAULT NULL` | User email |
| `oauth_id`               | `VARCHAR(255) DEFAULT NULL` | **Unused** — see note below |
| `oauth_token`            | `TEXT DEFAULT NULL` | **Unused** — see note below |
| `created_at`             | `TIMESTAMP DEFAULT CURRENT_TIMESTAMP` | Creation time |
| `credentials_updated`    | `BOOLEAN DEFAULT FALSE` | True once the user changes from default credentials |
| `credentials_updated_at` | `TIMESTAMP NULL` | When credentials were last updated |
| `must_change_password`   | `BOOLEAN DEFAULT FALSE` | Force password change on next login |

> **The `oauth_*` columns are retained but never written.** OAuth login was
> removed before the public release; authentication is username/password against
> this table. The columns (and the `idx_users_oauth_id` index) are kept so a
> future identity-provider integration needs no migration — they are always
> `NULL` today, and the API omits them from user responses.

### `loxilb_instances`

Stores managed LoxiLB instance information.

| Column         | Type | Description |
|----------------|------|-------------|
| `id`           | `INT AUTO_INCREMENT PRIMARY KEY` | Unique instance ID |
| `name`         | `VARCHAR(255) NOT NULL` | Instance name |
| `host`         | `VARCHAR(255) NOT NULL` | Host address |
| `port`         | `VARCHAR(255) NOT NULL` | Port number |
| `protocol`     | `VARCHAR(10) NOT NULL DEFAULT 'https'` | API protocol |
| `description`  | `TEXT` | Instance description |
| `version`      | `VARCHAR(255) NOT NULL` | LoxiLB version |
| `api_endpoint` | `VARCHAR(255) NOT NULL UNIQUE` | API endpoint URL |
| `cimage`       | `VARCHAR(255) NOT NULL` | Container image |
| `ctag`         | `VARCHAR(255) NOT NULL` | Container tag |
| `is_active`    | `BOOLEAN DEFAULT TRUE` | Whether the instance is active |
| `created_at`   | `TIMESTAMP DEFAULT CURRENT_TIMESTAMP` | Creation time |

### `api_tokens`

Stores API tokens used for authentication and authorization.

| Column        | Type | Description |
|---------------|------|-------------|
| `token_id`    | `INT AUTO_INCREMENT PRIMARY KEY` | Unique token ID |
| `token_value` | `TEXT NOT NULL` (unique on first 255 chars) | API token value |
| `user_id`     | `VARCHAR(255) NOT NULL` | Associated user ID |
| `scopes`      | `TEXT` | Token permissions |
| `expires_at`  | `TIMESTAMP NOT NULL` | Token expiration |
| `created_at`  | `TIMESTAMP DEFAULT CURRENT_TIMESTAMP` | Creation time |

### `logs`

Stores system logs for monitoring purposes.

| Column        | Type | Description |
|---------------|------|-------------|
| `id`          | `INT AUTO_INCREMENT PRIMARY KEY` | Unique log entry ID |
| `level`       | `VARCHAR(10)` | Log level (INFO, ERROR, …) |
| `timestamp`   | `DATETIME` | Log timestamp |
| `severity`    | `VARCHAR(45)` | Severity level |
| `facility`    | `VARCHAR(45)` | Log facility |
| `programname` | `VARCHAR(45)` | Program that generated the log |
| `host`        | `VARCHAR(255)` | Host where the log originated |
| `message`     | `TEXT` | Log message |
| `created_at`  | `TIMESTAMP DEFAULT CURRENT_TIMESTAMP` | Creation time |

### `alerts`

Stores system alerts for monitoring failures and security issues.

| Column        | Type | Description |
|---------------|------|-------------|
| `id`          | `INT AUTO_INCREMENT PRIMARY KEY` | Unique alert ID |
| `instance_id` | `INT NOT NULL` | Related instance (`FK → loxilb_instances.id`, `ON DELETE CASCADE`) |
| `type`        | `ENUM('DB_DISCONNECT','API_UNREACHABLE','HIGH_CPU','MEMORY_LEAK') NOT NULL` | Alert type |
| `severity`    | `ENUM('INFO','WARNING','CRITICAL') NOT NULL` | Alert severity |
| `message`     | `TEXT NOT NULL` | Alert message |
| `created_at`  | `TIMESTAMP DEFAULT CURRENT_TIMESTAMP` | Creation time |
| `resolved_at` | `TIMESTAMP NULL DEFAULT NULL` | When the alert was resolved |

### `acknowledgments`

Stores acknowledgment records for alerts.

| Column     | Type | Description |
|------------|------|-------------|
| `id`       | `INT AUTO_INCREMENT PRIMARY KEY` | Unique acknowledgment ID |
| `alert_id` | `INT NOT NULL` | Related alert (`FK → alerts.id`, `ON DELETE CASCADE`) |
| `user_id`  | `INT NOT NULL` | Acknowledging user (`FK → users.id`, `ON DELETE CASCADE`) |
| `ack_time` | `TIMESTAMP DEFAULT CURRENT_TIMESTAMP` | Acknowledgment time |

### `login_attempts`

Tracks failed login attempts and lockouts to defend against brute-force attacks.
Keyed by `(username, client_ip)`.

| Column           | Type | Description |
|------------------|------|-------------|
| `id`             | `INT AUTO_INCREMENT PRIMARY KEY` | Unique row ID |
| `username`       | `VARCHAR(255) NOT NULL` | Username attempted |
| `client_ip`      | `VARCHAR(64) NOT NULL` | Source IP |
| `failed_count`   | `INT NOT NULL DEFAULT 0` | Consecutive failures |
| `last_failed_at` | `DATETIME NOT NULL` | Last failure time |
| `blocked_until`  | `DATETIME NULL` | Lockout expiry (null = not locked) |
| `created_at`     | `DATETIME DEFAULT CURRENT_TIMESTAMP` | Creation time |
| `updated_at`     | `DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP` | Last update time |

### `instance_snapshots`

Stores instance configuration snapshots. Snapshot blobs are stored **in the
database** (gzip-compressed JSON, optionally AES-256-GCM encrypted) — never on
container-local disk. Added by migration `003_add_instance_snapshots.sql`.

| Column                  | Type | Description |
|-------------------------|------|-------------|
| `id`                    | `CHAR(36) PRIMARY KEY` | UUID |
| `instance_id`           | `INT NOT NULL` | `FK → loxilb_instances.id`, `ON DELETE CASCADE` |
| `name`                  | `VARCHAR(128) NOT NULL` | Operator-facing label |
| `description`           | `TEXT` | Optional description |
| `trigger_type`          | `ENUM('manual','scheduled','pre_restore','pre_upgrade') NOT NULL` | How the snapshot was taken |
| `schema_version`        | `VARCHAR(16) NOT NULL` | Snapshot envelope schema version |
| `gateway_version`       | `VARCHAR(64) NOT NULL` | Gateway version at capture |
| `size_bytes`            | `INT UNSIGNED NOT NULL` | Uncompressed JSON size |
| `checksum`              | `CHAR(71) NOT NULL` | `sha256:<hex>`, gateway-computed (envelope) |
| `stored_checksum`       | `CHAR(71) NOT NULL` | `sha256:<hex>` over raw received bytes, OAM-computed |
| `snapshot_blob`         | `MEDIUMBLOB NOT NULL` | gzip(JSON), AES-256-GCM when encrypted |
| `encrypted`             | `BOOLEAN NOT NULL DEFAULT FALSE` | Whether the blob is encrypted |
| `pinned`                | `BOOLEAN NOT NULL DEFAULT FALSE` | Exempt from retention |
| `checksum_ok`           | `BOOLEAN NOT NULL DEFAULT TRUE` | Integrity-sweep verdict |
| `created_by`            | `VARCHAR(64) NOT NULL` | JWT username of creator |
| `created_at`            | `TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP` | Creation time |
| `restore_count`         | `INT UNSIGNED NOT NULL DEFAULT 0` | Times restored |
| `last_restored_at`      | `TIMESTAMP NULL` | Last restore time |
| `last_restore_result`   | `ENUM('ok','rolled_back','rollback_failed') NULL` | Last restore outcome |
| `last_restore_response` | `MEDIUMTEXT NULL` | Full gateway restore response (audit) |

Index: `idx_snap_instance (instance_id, created_at DESC)`.

### `instance_snapshot_schedules`

Per-instance snapshot schedule and retention policy.

| Column            | Type | Description |
|-------------------|------|-------------|
| `instance_id`     | `INT PRIMARY KEY` | `FK → loxilb_instances.id`, `ON DELETE CASCADE` |
| `enabled`         | `BOOLEAN NOT NULL DEFAULT FALSE` | Schedule enabled |
| `interval_hours`  | `INT UNSIGNED NOT NULL DEFAULT 24` | Simple interval (not cron) |
| `retain_count`    | `INT UNSIGNED NOT NULL DEFAULT 10` | Per-instance keep-N (unpinned) |
| `last_run_at`     | `TIMESTAMP NULL` | Last scheduled run |
| `last_run_result` | `VARCHAR(255) NULL` | Last run result/message |

### `system_config`

System-wide configuration key/value store (e.g. admin credential tracking).

| Column         | Type | Description |
|----------------|------|-------------|
| `config_key`   | `VARCHAR(50) PRIMARY KEY` | Configuration key |
| `config_value` | `TEXT NOT NULL` | Configuration value (string or JSON) |
| `created_at`   | `TIMESTAMP DEFAULT CURRENT_TIMESTAMP` | Creation time |
| `updated_at`   | `TIMESTAMP DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP` | Last update time |

### `system_settings`

Installation-level settings (e.g. `installation_id`, `first_boot_at`).

| Column          | Type | Description |
|-----------------|------|-------------|
| `id`            | `INT AUTO_INCREMENT PRIMARY KEY` | Unique row ID |
| `setting_key`   | `VARCHAR(100) NOT NULL UNIQUE` | Setting key |
| `setting_value` | `TEXT NOT NULL` | Setting value |
| `created_at`    | `DATETIME DEFAULT CURRENT_TIMESTAMP` | Creation time |
| `updated_at`    | `DATETIME DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP` | Last update time |

## Initialization & Migrations

- **Fresh install:** `database/init/00-init-complete.sql` creates all tables,
  performance indexes, and seed rows. Docker Compose mounts it so it runs
  automatically on first MySQL startup.
- **Existing databases:** apply the numbered files in `database/migrations/` in
  order.

Do not embed credentials in initialization scripts; supply them via the
environment (see [DEPLOYMENT.md](../DEPLOYMENT.md)).

## Notes

- **Foreign keys** with `ON DELETE CASCADE` keep alerts, acknowledgments, and
  snapshots consistent when an instance or alert is removed.
- **Indexes** on frequently queried columns (usernames, timestamps, instance
  IDs) support pagination and lookup performance.

## Related Files

- `internal/config/constants.go` — configuration constants ([constants.md](constants.md))
- `internal/services/db_service.go` — database interactions
