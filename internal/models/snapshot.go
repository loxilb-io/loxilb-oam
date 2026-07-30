package models

import (
	"encoding/json"
	"time"
)

// Instance snapshot orchestration models.
// The snapshot document itself is an opaque blob owned by the gateway — OAM
// stores envelope metadata only and never parses config semantics.

// Snapshot trigger types (instance_snapshots.trigger_type).
const (
	SnapshotTriggerManual     = "manual"
	SnapshotTriggerScheduled  = "scheduled"
	SnapshotTriggerPreRestore = "pre_restore"
	SnapshotTriggerPreUpgrade = "pre_upgrade"
)

// Restore outcomes (instance_snapshots.last_restore_result), mirroring the
// gateway's restore result states.
const (
	RestoreResultOK             = "ok"
	RestoreResultRolledBack     = "rolled_back"
	RestoreResultRollbackFailed = "rollback_failed"
)

// InstanceSnapshot is one stored gateway-config snapshot row.
// The blob is never serialized in API responses — download streams it.
type InstanceSnapshot struct {
	ID                string     `json:"id"`
	InstanceID        int        `json:"instance_id"`
	Name              string     `json:"name"`
	Description       string     `json:"description"`
	TriggerType       string     `json:"trigger_type"`
	SchemaVersion     string     `json:"schema_version"`
	GatewayVersion    string     `json:"gateway_version"`
	SizeBytes         int64      `json:"size_bytes"` // uncompressed JSON size
	Checksum          string     `json:"checksum"`   // "sha256:<hex>", gateway-computed (envelope)
	StoredChecksum    string     `json:"stored_checksum"` // "sha256:<hex>" over raw JSON bytes as received, OAM-computed
	Blob              []byte     `json:"-"`
	Encrypted         bool       `json:"encrypted"`
	Pinned            bool       `json:"pinned"`
	ChecksumOK        bool       `json:"checksum_ok"` // integrity-sweep verdict
	CreatedBy         string     `json:"created_by"`
	CreatedAt         time.Time  `json:"created_at"`
	RestoreCount      int        `json:"restore_count"`
	LastRestoredAt    *time.Time `json:"last_restored_at,omitempty"`
	LastRestoreResult *string    `json:"last_restore_result,omitempty"`
	// LastRestoreResponse is the full gateway response JSON of the most
	// recent restore attempt (the audit record). Only populated on the
	// single-snapshot GET, not in lists.
	LastRestoreResponse *string `json:"last_restore_response,omitempty"`
}

// InstanceSnapshotSchedule is the per-instance scheduled-snapshot/retention row.
type InstanceSnapshotSchedule struct {
	InstanceID    int        `json:"instance_id"`
	Enabled       bool       `json:"enabled"`
	IntervalHours int        `json:"interval_hours"`
	RetainCount   int        `json:"retain_count"`
	LastRunAt     *time.Time `json:"last_run_at,omitempty"`
	LastRunResult *string    `json:"last_run_result,omitempty"`
}

// TakeSnapshotRequest is the body of POST /oam/instances/:id/snapshots.
type TakeSnapshotRequest struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	TriggerType string `json:"trigger_type"` // defaults to "manual"
}

// UpdateSnapshotRequest is the body of PATCH /oam/snapshots/:sid.
// Pointer fields distinguish "not sent" from zero values.
type UpdateSnapshotRequest struct {
	Name        *string `json:"name,omitempty"`
	Description *string `json:"description,omitempty"`
	Pinned      *bool   `json:"pinned,omitempty"`
}

// RestoreSnapshotRequest is the body of POST /oam/snapshots/:sid/restore.
type RestoreSnapshotRequest struct {
	Mode string `json:"mode"` // "dry-run" (default) | "commit"
	// TargetInstanceID restores the snapshot onto a different instance than
	// the one it was taken from (cross-instance restore). Defaults to the
	// snapshot's own instance.
	TargetInstanceID *int `json:"target_instance_id,omitempty"`
}

// RestoreOutcome is OAM's envelope around the gateway's restore response.
// GatewayResponse is the gateway's response body verbatim — no rewording.
type RestoreOutcome struct {
	SnapshotID           string          `json:"snapshot_id"`
	InstanceID           int             `json:"instance_id"` // restore target
	Mode                 string          `json:"mode"`
	CrossInstance        bool            `json:"cross_instance,omitempty"`
	PreRestoreSnapshotID string          `json:"pre_restore_snapshot_id,omitempty"`
	GatewayStatus        int             `json:"gateway_status"`
	GatewayResponse      json.RawMessage `json:"gateway_response" swaggertype:"object"`
}

// SnapshotScheduleRequest is the body of PUT /oam/instances/:id/snapshot-schedule.
type SnapshotScheduleRequest struct {
	Enabled       bool `json:"enabled"`
	IntervalHours int  `json:"interval_hours" binding:"omitempty,min=1,max=8760"`
	RetainCount   int  `json:"retain_count" binding:"omitempty,min=1,max=1000"`
}

// PaginatedSnapshotsResponse is the list envelope for GET /oam/instances/:id/snapshots.
type PaginatedSnapshotsResponse struct {
	Data       []InstanceSnapshot `json:"data"`
	Pagination PaginationMeta     `json:"pagination"`
}
