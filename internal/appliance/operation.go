package appliance

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// OperationType is a whole-Appliance operation that can be planned. It is the
// Action set without diagnostics, which is an export rather than a change.
type OperationType string

const (
	OperationBackup   OperationType = "backup"
	OperationRestore  OperationType = "restore"
	OperationUpdate   OperationType = "update"
	OperationRollback OperationType = "rollback"
	OperationReset    OperationType = "reset"
)

// Valid reports whether t is a known operation type.
func (t OperationType) Valid() bool {
	switch t {
	case OperationBackup, OperationRestore, OperationUpdate, OperationRollback, OperationReset:
		return true
	}
	return false
}

// Action is the capability-discovery action this operation type executes.
func (t OperationType) Action() Action { return Action(t) }

// OperationState is where an operation stands. OAM sets PLANNED,
// AWAITING_AUTHORIZATION and a pre-submit CANCELLED; every other state is the
// host adapter's.
type OperationState string

const (
	StatePlanned               OperationState = "PLANNED"
	StateAwaitingAuthorization OperationState = "AWAITING_AUTHORIZATION"
	StateQueued                OperationState = "QUEUED"
	StateRunning               OperationState = "RUNNING"
	StateVerifying             OperationState = "VERIFYING"
	StateSucceeded             OperationState = "SUCCEEDED"
	StateFailed                OperationState = "FAILED"
	StateCompensating          OperationState = "COMPENSATING"
	StateRolledBack            OperationState = "ROLLED_BACK"
	StateRecoveryRequired      OperationState = "RECOVERY_REQUIRED"
	StateCancelled             OperationState = "CANCELLED"
)

// Valid reports whether s is a known state.
func (s OperationState) Valid() bool {
	switch s {
	case StatePlanned, StateAwaitingAuthorization, StateQueued, StateRunning, StateVerifying,
		StateSucceeded, StateFailed, StateCompensating, StateRolledBack, StateRecoveryRequired, StateCancelled:
		return true
	}
	return false
}

// hostOwned reports whether s is a state the host adapter reports, as opposed
// to one only OAM sets before submission.
func (s OperationState) hostOwned() bool {
	switch s {
	case StateQueued, StateRunning, StateVerifying, StateSucceeded, StateFailed,
		StateCompensating, StateRolledBack, StateRecoveryRequired, StateCancelled:
		return true
	}
	return false
}

// Terminal reports whether nothing further will happen to an operation in
// this state. RECOVERY_REQUIRED is not terminal: it ends automation, but the
// host may still report how an operator resolved it.
func (s OperationState) Terminal() bool {
	switch s {
	case StateSucceeded, StateFailed, StateRolledBack, StateCancelled:
		return true
	}
	return false
}

// Active reports whether an operation in this state occupies the
// installation, so that no other may be authorized or submitted.
func (s OperationState) Active() bool {
	switch s {
	case StateAwaitingAuthorization, StateQueued, StateRunning, StateVerifying, StateCompensating, StateRecoveryRequired:
		return true
	}
	return false
}

// PlanTTL is how long a plan may be submitted after it was made. The host
// validated the request against the installation as it was then; the longer
// the wait, the less that validation is worth.
const PlanTTL = 15 * time.Minute

// Limits on request fields. References are opaque names the host adapter
// resolves; they are never paths and nothing is uploaded through OAM.
const (
	maxRefLength         = 256
	maxNoteLength        = 500
	minIdempotencyKeyLen = 16
	maxIdempotencyKeyLen = 128
)

// PlanRequest is the body of POST /oam/v1/appliance/operations.
type PlanRequest struct {
	SchemaVersion string        `json:"schema_version"`
	Type          OperationType `json:"type"`
	// ArchiveRef names a backup archive the host has admitted. Required for
	// restore, refused otherwise.
	ArchiveRef string `json:"archive_ref,omitempty"`
	// TargetReleaseRef names a release the host has admitted. Required for
	// update, refused otherwise.
	TargetReleaseRef string `json:"target_release_ref,omitempty"`
	// Note is free text for the audit trail.
	Note string `json:"note,omitempty"`
}

// ErrInvalidRequest wraps every reason a plan request is refused before the
// host is asked.
var ErrInvalidRequest = errors.New("invalid plan request")

// Validate checks the request's shape. It does not consult the host.
func (r PlanRequest) Validate() error {
	fail := func(format string, args ...any) error {
		return fmt.Errorf("%w: "+format, append([]any{ErrInvalidRequest}, args...)...)
	}
	if !r.Type.Valid() {
		return fail("type must be one of backup, restore, update, rollback, reset")
	}
	if len(r.ArchiveRef) > maxRefLength || len(r.TargetReleaseRef) > maxRefLength {
		return fail("references are limited to %d characters", maxRefLength)
	}
	if len(r.Note) > maxNoteLength {
		return fail("note is limited to %d characters", maxNoteLength)
	}
	if (r.Type == OperationRestore) != (r.ArchiveRef != "") {
		return fail("archive_ref is required for restore and not accepted for other types")
	}
	if (r.Type == OperationUpdate) != (r.TargetReleaseRef != "") {
		return fail("target_release_ref is required for update and not accepted for other types")
	}
	return nil
}

// Hash identifies the request for idempotency: two requests with the same
// hash ask for the same thing. It covers every field that changes what would
// be done, in a fixed order, and deliberately not schema_version.
func (r PlanRequest) Hash() string {
	canonical, _ := json.Marshal([]string{string(r.Type), r.ArchiveRef, r.TargetReleaseRef, r.Note})
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:])
}

// ValidIdempotencyKey reports whether key is acceptable: long enough to be
// unguessable by accident, short and plain enough to store and log.
func ValidIdempotencyKey(key string) bool {
	if len(key) < minIdempotencyKeyLen || len(key) > maxIdempotencyKeyLen {
		return false
	}
	return strings.IndexFunc(key, func(r rune) bool { return r <= ' ' || r > '~' }) < 0
}

// Plan is what the host adapter established when it validated a request. It
// is stored as returned and shown only to callers who may run the operation.
type Plan struct {
	// IrreversibleAfterPhase is the phase after which cancellation is refused.
	IrreversibleAfterPhase string   `json:"irreversible_after_phase,omitempty"`
	CurrentReleaseDigest   string   `json:"current_release_digest,omitempty"`
	TargetReleaseDigest    string   `json:"target_release_digest,omitempty"`
	ArchiveDigest          string   `json:"archive_digest,omitempty"`
	Compatibility          string   `json:"compatibility,omitempty"`
	AffectedResources      []string `json:"affected_resources"`
}

// ChallengeTTL is how long an authorization challenge may be used. It never
// outlives the plan it authorizes.
const ChallengeTTL = 5 * time.Minute

// Reconciliation values: how this row relates to the host journal.
const (
	// ReconciliationInSync: the row reflects the journal as last read.
	ReconciliationInSync = "IN_SYNC"
	// ReconciliationHostUnreachable: the journal could not be read; the row
	// shows the last state known and may be stale.
	ReconciliationHostUnreachable = "HOST_UNREACHABLE"
	// ReconciliationHostUnknown: the journal has no entry for an operation
	// it had previously reported on.
	ReconciliationHostUnknown = "HOST_UNKNOWN"
)

// Operation is one planned or executed whole-Appliance operation, as returned
// by the API.
type Operation struct {
	SchemaVersion  string         `json:"schema_version"`
	ID             string         `json:"id"`
	Type           OperationType  `json:"type"`
	State          OperationState `json:"state"`
	Phase          string         `json:"phase,omitempty"`
	InstallationID string         `json:"installation_id"`
	Model          string         `json:"model,omitempty"`
	Actor          string         `json:"actor"`
	RequestID      string         `json:"request_id,omitempty"`
	Note           string         `json:"note,omitempty"`
	// HostFixture: the plan came from a fixture host adapter and describes
	// nothing real.
	HostFixture bool `json:"host_fixture"`

	// PlanHash and Plan are present only for callers permitted to run this
	// type of operation; Redacted is true when they were withheld.
	PlanHash      string    `json:"plan_hash,omitempty"`
	PlanExpiresAt time.Time `json:"plan_expires_at"`
	Plan          *Plan     `json:"plan,omitempty"`
	Redacted      bool      `json:"redacted"`

	RequiresReauthentication bool `json:"requires_reauthentication"`
	// Cancellable: a cancel request would be accepted now. Decided by OAM
	// before submission and by the host adapter after.
	Cancellable bool `json:"cancellable"`
	// HostGeneration is the host journal generation this operation reflects;
	// 0 until the host has reported on it.
	HostGeneration int64  `json:"host_generation"`
	Reconciliation string `json:"reconciliation"`
	// Stale: the host could not be read at the last attempt, so State may be
	// out of date.
	Stale       bool   `json:"stale"`
	ErrorCode   string `json:"error_code,omitempty"`
	ErrorOrigin string `json:"error_origin,omitempty"`

	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	SubmittedAt *time.Time `json:"submitted_at,omitempty"`
	FinishedAt  *time.Time `json:"finished_at,omitempty"`

	// Not serialized: who owns the row, the idempotency identity, and the
	// request as planned (needed to submit it).
	ActorUserID int         `json:"-"`
	RequestHash string      `json:"-"`
	request     PlanRequest `json:"-"`
}

// redacted returns a copy without the detail reserved for callers who may run
// the operation: what it would touch and which artifacts it involves.
func (o Operation) redacted() Operation {
	o.PlanHash = ""
	o.Plan = nil
	o.Note = ""
	o.Redacted = true
	return o
}

// OperationList is the body of GET /oam/v1/appliance/operations.
type OperationList struct {
	SchemaVersion string      `json:"schema_version"`
	Items         []Operation `json:"items"`
	// NextCursor is passed back as `cursor` for the next page; absent on the
	// last one.
	NextCursor string `json:"next_cursor,omitempty"`
}

// newOperationID returns a UUIDv7: 48 bits of Unix milliseconds followed by
// random bits, so IDs sort by creation time and need no coordination.
func newOperationID(now time.Time) (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[6:]); err != nil {
		return "", err
	}
	ms := uint64(now.UnixMilli())
	b[0], b[1], b[2], b[3], b[4], b[5] = byte(ms>>40), byte(ms>>32), byte(ms>>24), byte(ms>>16), byte(ms>>8), byte(ms)
	b[6] = (b[6] & 0x0f) | 0x70 // version 7
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32], nil
}

// ValidOperationID reports whether id has the shape of a UUID, so a malformed
// path parameter is a 404 rather than a database error.
func ValidOperationID(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
				return false
			}
		}
	}
	return true
}
