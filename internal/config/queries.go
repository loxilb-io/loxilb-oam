package config

const (
	InsertUserQuery                  = "INSERT INTO users (username, email, password) VALUES (?, ?, ?)"
	InsertOAuthUserQuery             = "INSERT INTO users (username, password, oauth_provider, email, oauth_id) VALUES (?, ?, ?, ?, ?)"
	UpdateUserQuery                  = "UPDATE users SET username = ?, password = ?, email = ?, role = ? WHERE id = ?"
	UpdateOAuthUserQuery             = "UPDATE users SET username = ?, password = ?, oauth_provider = ?, oauth_id = ?, email = ? WHERE id = ?"
	DeleteUserQuery                  = "DELETE FROM users WHERE id = ?"
	SelectUserPasswordQuery          = "SELECT password FROM users WHERE username = ?"
	UpdateUserPasswordQuery          = "UPDATE users SET password = ? WHERE id = ?"
	SelectUserIdQuery                = "SELECT id, password FROM users WHERE username = ?"
	SelectLoxiLBInstancesQuery       = "SELECT id, name, host, port, protocol, description, version, api_endpoint, cimage, ctag, is_active, created_at FROM loxilb_instances"
	SelectActiveLoxiLBInstancesQuery = "SELECT id, name, host, port, protocol, description, version, api_endpoint, cimage, ctag, is_active, created_at FROM loxilb_instances WHERE is_active = TRUE"
	SelectLoxiLBInstanceByIDQuery    = "SELECT id, name, host, port, protocol, description, version, api_endpoint, cimage, ctag, is_active, created_at FROM loxilb_instances WHERE id = ?"
	InsertLoxiLBInstanceQuery        = "INSERT INTO loxilb_instances (name, host, port, protocol, description, version, api_endpoint, cimage, ctag, is_active) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)"
	UpdateLoxiLBInstanceQuery        = "UPDATE loxilb_instances SET name = ?, host = ?, port = ?, protocol = ?, description = ?, version = ?, api_endpoint = ?, cimage = ?, ctag = ?, is_active = ? WHERE id = ?"
	DeleteLoxiLBInstanceQuery        = "DELETE FROM loxilb_instances WHERE id = ?"
	// Uniqueness pre-checks. The name has no UNIQUE constraint in the schema
	// (the UI addresses instances by name, so duplicates make one of them
	// unreachable) and api_endpoint has one — checking both here turns a
	// driver-level 1062 into a 409 that names the conflicting instance.
	CountLoxiLBInstanceByNameQuery     = "SELECT COUNT(*) FROM loxilb_instances WHERE LOWER(name) = LOWER(?) AND id <> ?"
	CountLoxiLBInstanceByEndpointQuery = "SELECT COUNT(*) FROM loxilb_instances WHERE LOWER(api_endpoint) = LOWER(?) AND id <> ?"
	InsertTokenQuery                   = "INSERT INTO api_tokens (token_value, user_id, scopes, expires_at) VALUES (?, ?, ?, ?)"
	ValidateTokenQuery                 = "SELECT user_id FROM api_tokens WHERE token_value = ? AND expires_at > NOW()"
	DeleteTokenQuery                   = "DELETE FROM api_tokens WHERE token_value = ?"
	SelectLogsQuery                    = `
        SELECT 
            id, level, timestamp, message, severity, facility, programname, host, message, create_at
        FROM logs
        WHERE created_at BETWEEN ? AND ?
        ORDER BY timestamp DESC
        LIMIT ? OFFSET ?
    `
	InsertAlertQuery                 = "INSERT INTO alerts (instance_id, type, severity, message) VALUES (?, ?, ?, ?)"
	SelectAlertsQuery                = "SELECT id, instance_id, type, severity, message, created_at FROM alerts WHERE resolved_at IS NULL"
	SelectAlertsQueryPaginated       = "SELECT id, instance_id, type, severity, message, created_at FROM alerts WHERE resolved_at IS NULL ORDER BY created_at DESC LIMIT ? OFFSET ?"
	SelectAlertsCountQuery           = "SELECT COUNT(*) FROM alerts WHERE resolved_at IS NULL"
	SelectAlertHistoryQuery          = "SELECT id, instance_id, type, severity, message, created_at, resolved_at FROM alerts WHERE created_at BETWEEN ? AND ?"
	SelectAlertHistoryQueryPaginated = "SELECT id, instance_id, type, severity, message, created_at, resolved_at FROM alerts WHERE created_at BETWEEN ? AND ? ORDER BY created_at DESC LIMIT ? OFFSET ?"
	SelectAlertHistoryCountQuery     = "SELECT COUNT(*) FROM alerts WHERE created_at BETWEEN ? AND ?"
	InsertAckQuery                   = "INSERT INTO acknowledgments (alert_id, user_id, ack_time) VALUES (?, ?, ?)"
	UpdateAckQuery                   = "UPDATE alerts SET resolved_at = ? WHERE id = ?"
	// =================== Login Attempt Tracking Queries ===================
	// Get login attempt record by username and client IP
	SelectLoginAttemptQuery = `
		SELECT id, username, client_ip, failed_count, last_failed_at, blocked_until, created_at, updated_at
		FROM login_attempts
		WHERE username = ? AND client_ip = ?
	`

	// Insert or update login attempt record (increment failed count)
	UpsertLoginAttemptQuery = `
		INSERT INTO login_attempts (username, client_ip, failed_count, last_failed_at, blocked_until)
		VALUES (?, ?, 1, ?, ?)
		ON DUPLICATE KEY UPDATE 
			failed_count = failed_count + 1,
			last_failed_at = VALUES(last_failed_at),
			blocked_until = VALUES(blocked_until)
	`

	// Reset login attempt counter for successful login
	ClearLoginAttemptsQuery = `
		DELETE FROM login_attempts
		WHERE username = ? AND client_ip = ?
	`

	// Reset failed count to 1 when attempt is outside the window
	ResetLoginAttemptCountQuery = `
		UPDATE login_attempts
		SET failed_count = 1, last_failed_at = ?, blocked_until = NULL
		WHERE username = ? AND client_ip = ?
	`

	// =================== Instance Snapshot Queries ===================
	// The blob column is only selected by the dedicated blob query — list/get
	// stay metadata-only.

	snapshotMetaColumns = `id, instance_id, name, description, trigger_type, schema_version,
		gateway_version, size_bytes, checksum, stored_checksum, encrypted, pinned, checksum_ok,
		created_by, created_at, restore_count, last_restored_at, last_restore_result`

	InsertInstanceSnapshotQuery = `
		INSERT INTO instance_snapshots
			(id, instance_id, name, description, trigger_type, schema_version, gateway_version,
			 size_bytes, checksum, stored_checksum, snapshot_blob, encrypted, pinned, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	SelectInstanceSnapshotsQuery = `
		SELECT ` + snapshotMetaColumns + `
		FROM instance_snapshots
		WHERE instance_id = ?
		ORDER BY created_at DESC, id
		LIMIT ? OFFSET ?
	`

	SelectInstanceSnapshotsCountQuery = "SELECT COUNT(*) FROM instance_snapshots WHERE instance_id = ?"

	SelectSnapshotByIDQuery = `
		SELECT ` + snapshotMetaColumns + `, last_restore_response
		FROM instance_snapshots
		WHERE id = ?
	`

	SelectSnapshotBlobQuery = `
		SELECT ` + snapshotMetaColumns + `, snapshot_blob
		FROM instance_snapshots
		WHERE id = ?
	`

	UpdateSnapshotMetaQuery = `
		UPDATE instance_snapshots
		SET name = ?, description = ?, pinned = ?
		WHERE id = ?
	`

	MarkSnapshotChecksumQuery = "UPDATE instance_snapshots SET checksum_ok = ? WHERE id = ?"

	DeleteSnapshotQuery = "DELETE FROM instance_snapshots WHERE id = ?"

	RecordSnapshotRestoreQuery = `
		UPDATE instance_snapshots
		SET restore_count = restore_count + 1,
		    last_restored_at = NOW(),
		    last_restore_result = ?,
		    last_restore_response = ?
		WHERE id = ?
	`

	SelectSnapshotScheduleQuery = `
		SELECT instance_id, enabled, interval_hours, retain_count, last_run_at, last_run_result
		FROM instance_snapshot_schedules
		WHERE instance_id = ?
	`

	UpsertSnapshotScheduleQuery = `
		INSERT INTO instance_snapshot_schedules (instance_id, enabled, interval_hours, retain_count)
		VALUES (?, ?, ?, ?)
		ON DUPLICATE KEY UPDATE
			enabled = VALUES(enabled),
			interval_hours = VALUES(interval_hours),
			retain_count = VALUES(retain_count)
	`

	// Scheduler/retention/integrity queries

	SelectEnabledSnapshotSchedulesQuery = `
		SELECT instance_id, enabled, interval_hours, retain_count, last_run_at, last_run_result
		FROM instance_snapshot_schedules
		WHERE enabled = TRUE
	`

	RecordSnapshotScheduleRunQuery = `
		UPDATE instance_snapshot_schedules
		SET last_run_at = NOW(), last_run_result = ?
		WHERE instance_id = ?
	`

	SelectSnapshotInstanceIDsQuery = "SELECT DISTINCT instance_id FROM instance_snapshots"

	// Retention candidates: newest first; rows past retain_count get deleted.
	// Pinned and pre_upgrade snapshots are exempt by the WHERE clause.
	SelectSnapshotRetentionCandidatesQuery = `
		SELECT id FROM instance_snapshots
		WHERE instance_id = ? AND pinned = FALSE AND trigger_type <> 'pre_upgrade'
		ORDER BY created_at DESC, id
	`

	SelectAllSnapshotIDsQuery = "SELECT id FROM instance_snapshots"
)
