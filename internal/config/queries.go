package config

// SQL statements for PostgreSQL.
//
// Placeholders are positional ($1, $2, ...) rather than MySQL's '?'. The
// position is significant: repeating $1 reuses the same argument, so the
// argument list passed at the call site must line up with the highest-numbered
// placeholder, not with the number of occurrences.
//
// Inserts whose generated key the caller needs end in RETURNING id and MUST be
// run with QueryRow, not Exec: the PostgreSQL wire protocol has no
// LastInsertId equivalent, and sql.Result.LastInsertId() returns an error on
// pgx rather than a value.

const (
	// InsertUserQuery returns the generated id — run it with QueryRow.
	InsertUserQuery                  = "INSERT INTO users (username, email, password) VALUES ($1, $2, $3) RETURNING id"
	UpdateUserQuery                  = "UPDATE users SET username = $1, password = $2, email = $3, role = $4 WHERE id = $5"
	DeleteUserQuery                  = "DELETE FROM users WHERE id = $1"
	SelectUserPasswordQuery          = "SELECT password FROM users WHERE username = $1"
	UpdateUserPasswordQuery          = "UPDATE users SET password = $1 WHERE id = $2"
	SelectUserIdQuery                = "SELECT id, password FROM users WHERE username = $1"
	SelectLoxiLBInstancesQuery       = "SELECT id, name, host, port, protocol, description, version, api_endpoint, cimage, ctag, is_active, created_at FROM loxilb_instances"
	SelectActiveLoxiLBInstancesQuery = "SELECT id, name, host, port, protocol, description, version, api_endpoint, cimage, ctag, is_active, created_at FROM loxilb_instances WHERE is_active = TRUE"
	SelectLoxiLBInstanceByIDQuery    = "SELECT id, name, host, port, protocol, description, version, api_endpoint, cimage, ctag, is_active, created_at FROM loxilb_instances WHERE id = $1"
	// InsertLoxiLBInstanceQuery returns the generated id — run it with QueryRow.
	InsertLoxiLBInstanceQuery = "INSERT INTO loxilb_instances (name, host, port, protocol, description, version, api_endpoint, cimage, ctag, is_active) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10) RETURNING id"
	UpdateLoxiLBInstanceQuery = "UPDATE loxilb_instances SET name = $1, host = $2, port = $3, protocol = $4, description = $5, version = $6, api_endpoint = $7, cimage = $8, ctag = $9, is_active = $10 WHERE id = $11"
	DeleteLoxiLBInstanceQuery = "DELETE FROM loxilb_instances WHERE id = $1"
	// Uniqueness pre-checks. The name has no UNIQUE constraint in the schema
	// (the UI addresses instances by name, so duplicates make one of them
	// unreachable) and api_endpoint has one — checking both here turns a
	// driver-level unique violation into a 409 that names the conflicting instance.
	CountLoxiLBInstanceByNameQuery     = "SELECT COUNT(*) FROM loxilb_instances WHERE LOWER(name) = LOWER($1) AND id <> $2"
	CountLoxiLBInstanceByEndpointQuery = "SELECT COUNT(*) FROM loxilb_instances WHERE LOWER(api_endpoint) = LOWER($1) AND id <> $2"
	InsertTokenQuery                   = "INSERT INTO api_tokens (token_value, user_id, scopes, expires_at) VALUES ($1, $2, $3, $4)"
	ValidateTokenQuery                 = "SELECT user_id FROM api_tokens WHERE token_value = $1 AND expires_at > NOW()"
	DeleteTokenQuery                   = "DELETE FROM api_tokens WHERE token_value = $1"
	// SelectLogsQuery previously selected 10 columns — `message` twice and a
	// non-existent `create_at` — against a 9-column Scan, so it could only ever
	// fail. The list now matches LogService.FetchLogs exactly.
	// "timestamp" is quoted because it is also a type name in PostgreSQL.
	SelectLogsQuery = `
        SELECT
            id, level, "timestamp", severity, facility, programname, host, message, created_at
        FROM logs
        WHERE created_at BETWEEN $1 AND $2
        ORDER BY "timestamp" DESC
        LIMIT $3 OFFSET $4
    `
	// InsertAlertQuery returns the generated id — run it with QueryRow.
	InsertAlertQuery                 = "INSERT INTO alerts (instance_id, type, severity, message) VALUES ($1, $2, $3, $4) RETURNING id"
	SelectAlertsQuery                = "SELECT id, instance_id, type, severity, message, created_at FROM alerts WHERE resolved_at IS NULL"
	SelectAlertsQueryPaginated       = "SELECT id, instance_id, type, severity, message, created_at FROM alerts WHERE resolved_at IS NULL ORDER BY created_at DESC LIMIT $1 OFFSET $2"
	SelectAlertsCountQuery           = "SELECT COUNT(*) FROM alerts WHERE resolved_at IS NULL"
	SelectAlertHistoryQuery          = "SELECT id, instance_id, type, severity, message, created_at, resolved_at FROM alerts WHERE created_at BETWEEN $1 AND $2"
	SelectAlertHistoryQueryPaginated = "SELECT id, instance_id, type, severity, message, created_at, resolved_at FROM alerts WHERE created_at BETWEEN $1 AND $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4"
	SelectAlertHistoryCountQuery     = "SELECT COUNT(*) FROM alerts WHERE created_at BETWEEN $1 AND $2"
	InsertAckQuery                   = "INSERT INTO acknowledgments (alert_id, user_id, ack_time) VALUES ($1, $2, $3)"
	UpdateAckQuery                   = "UPDATE alerts SET resolved_at = $1 WHERE id = $2"

	// SelectAlertHistoryPageSuffix is appended to SelectAlertHistoryQuery when
	// paginating it ad hoc. It continues that query's numbering, so it is only
	// valid appended to a statement that already consumes $1 and $2.
	SelectAlertHistoryPageSuffix = " LIMIT $3 OFFSET $4"

	// =================== Login Attempt Tracking Queries ===================
	// Get login attempt record by username and client IP
	SelectLoginAttemptQuery = `
		SELECT id, username, client_ip, failed_count, last_failed_at, blocked_until, created_at, updated_at
		FROM login_attempts
		WHERE username = $1 AND client_ip = $2
	`

	// Insert or update login attempt record (increment failed count).
	// MySQL's ON DUPLICATE KEY UPDATE ... VALUES(col) becomes ON CONFLICT with
	// EXCLUDED.col, which refers to the row that failed to insert.
	UpsertLoginAttemptQuery = `
		INSERT INTO login_attempts (username, client_ip, failed_count, last_failed_at, blocked_until)
		VALUES ($1, $2, 1, $3, $4)
		ON CONFLICT (username, client_ip) DO UPDATE SET
			failed_count = login_attempts.failed_count + 1,
			last_failed_at = EXCLUDED.last_failed_at,
			blocked_until = EXCLUDED.blocked_until
	`

	// Reset login attempt counter for successful login
	ClearLoginAttemptsQuery = `
		DELETE FROM login_attempts
		WHERE username = $1 AND client_ip = $2
	`

	// Reset failed count to 1 when attempt is outside the window
	ResetLoginAttemptCountQuery = `
		UPDATE login_attempts
		SET failed_count = 1, last_failed_at = $1, blocked_until = NULL
		WHERE username = $2 AND client_ip = $3
	`

	// =================== Instance Snapshot Queries ===================
	// The blob column is only selected by the dedicated blob query — list/get
	// stay metadata-only.

	snapshotMetaColumns = `id, instance_id, name, description, trigger_type, schema_version,
		gateway_version, size_bytes, checksum, stored_checksum, encrypted, pinned, checksum_ok,
		created_by, created_at, restore_count, last_restored_at, last_restore_result`

	// The id is a caller-supplied UUID, so this insert needs no RETURNING.
	InsertInstanceSnapshotQuery = `
		INSERT INTO instance_snapshots
			(id, instance_id, name, description, trigger_type, schema_version, gateway_version,
			 size_bytes, checksum, stored_checksum, snapshot_blob, encrypted, pinned, created_by)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
	`

	SelectInstanceSnapshotsQuery = `
		SELECT ` + snapshotMetaColumns + `
		FROM instance_snapshots
		WHERE instance_id = $1
		ORDER BY created_at DESC, id
		LIMIT $2 OFFSET $3
	`

	SelectInstanceSnapshotsCountQuery = "SELECT COUNT(*) FROM instance_snapshots WHERE instance_id = $1"

	SelectSnapshotByIDQuery = `
		SELECT ` + snapshotMetaColumns + `, last_restore_response
		FROM instance_snapshots
		WHERE id = $1
	`

	SelectSnapshotBlobQuery = `
		SELECT ` + snapshotMetaColumns + `, snapshot_blob
		FROM instance_snapshots
		WHERE id = $1
	`

	UpdateSnapshotMetaQuery = `
		UPDATE instance_snapshots
		SET name = $1, description = $2, pinned = $3
		WHERE id = $4
	`

	MarkSnapshotChecksumQuery = "UPDATE instance_snapshots SET checksum_ok = $1 WHERE id = $2"

	DeleteSnapshotQuery = "DELETE FROM instance_snapshots WHERE id = $1"

	RecordSnapshotRestoreQuery = `
		UPDATE instance_snapshots
		SET restore_count = restore_count + 1,
		    last_restored_at = NOW(),
		    last_restore_result = $1,
		    last_restore_response = $2
		WHERE id = $3
	`

	SelectSnapshotScheduleQuery = `
		SELECT instance_id, enabled, interval_hours, retain_count, last_run_at, last_run_result
		FROM instance_snapshot_schedules
		WHERE instance_id = $1
	`

	UpsertSnapshotScheduleQuery = `
		INSERT INTO instance_snapshot_schedules (instance_id, enabled, interval_hours, retain_count)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (instance_id) DO UPDATE SET
			enabled = EXCLUDED.enabled,
			interval_hours = EXCLUDED.interval_hours,
			retain_count = EXCLUDED.retain_count
	`

	// Scheduler/retention/integrity queries

	SelectEnabledSnapshotSchedulesQuery = `
		SELECT instance_id, enabled, interval_hours, retain_count, last_run_at, last_run_result
		FROM instance_snapshot_schedules
		WHERE enabled = TRUE
	`

	RecordSnapshotScheduleRunQuery = `
		UPDATE instance_snapshot_schedules
		SET last_run_at = NOW(), last_run_result = $1
		WHERE instance_id = $2
	`

	SelectSnapshotInstanceIDsQuery = "SELECT DISTINCT instance_id FROM instance_snapshots"

	// Retention candidates: newest first; rows past retain_count get deleted.
	// Pinned and pre_upgrade snapshots are exempt by the WHERE clause.
	SelectSnapshotRetentionCandidatesQuery = `
		SELECT id FROM instance_snapshots
		WHERE instance_id = $1 AND pinned = FALSE AND trigger_type <> 'pre_upgrade'
		ORDER BY created_at DESC, id
	`

	SelectAllSnapshotIDsQuery = "SELECT id FROM instance_snapshots"
)
