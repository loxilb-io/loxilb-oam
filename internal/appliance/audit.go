package appliance

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/loxilb-io/loxilb-oam/internal/utils"
)

// Audit events. One row is written for each.
const (
	AuditPlan            = "plan"
	AuditAuthorize       = "authorize"
	AuditAuthorizeDenied = "authorize_denied"
	AuditSubmit          = "submit"
	AuditSubmitDenied    = "submit_denied"
	AuditStateChange     = "state_change"
	AuditCancel          = "cancel"
	AuditReconcile       = "reconcile"
)

// auditDetail is everything an audit row may say beyond its fixed columns.
// It is a struct, not a map, on purpose: a field that is not declared here
// cannot be written, so nothing secret can reach the audit trail by accident.
// It has no field for a password, a challenge, a token or archive content,
// and must never gain one.
type auditDetail struct {
	Type           OperationType `json:"type,omitempty"`
	PlanHash       string        `json:"plan_hash,omitempty"`
	Phase          string        `json:"phase,omitempty"`
	HostGeneration int64         `json:"host_generation,omitempty"`
	ErrorCode      string        `json:"error_code,omitempty"`
	ErrorOrigin    string        `json:"error_origin,omitempty"`
	// Reason is a machine code explaining a denial or a reconciliation.
	Reason string `json:"reason,omitempty"`
}

type auditEntry struct {
	OperationID string
	Event       string
	// Actor is nil for what OAM did on its own (following the host journal).
	Actor    *Caller
	OldState OperationState
	NewState OperationState
	Detail   auditDetail
}

// execer is satisfied by *sql.DB and *sql.Tx.
type execer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func writeAudit(ctx context.Context, db execer, e auditEntry) error {
	detail, err := json.Marshal(e.Detail)
	if err != nil {
		return err
	}
	var userID sql.NullInt64
	var username, requestID, sourceIP string
	if e.Actor != nil {
		userID = sql.NullInt64{Int64: int64(e.Actor.UserID), Valid: true}
		username, requestID, sourceIP = e.Actor.Username, e.Actor.RequestID, e.Actor.SourceIP
	}
	_, err = db.ExecContext(ctx, `
		INSERT INTO appliance_audit
			(operation_id, event, actor_user_id, actor_username, request_id, source_ip, old_state, new_state, detail)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		e.OperationID, e.Event, userID, username, requestID, sourceIP, string(e.OldState), string(e.NewState), detail)
	return err
}

// audit records an event that has already happened. A failure to record it
// cannot undo the event, so it is logged loudly rather than returned; events
// that must not happen unrecorded are written inside their transaction with
// writeAudit instead.
func audit(ctx context.Context, db execer, e auditEntry) {
	if err := writeAudit(ctx, db, e); err != nil {
		utils.LogError("AUDIT: failed to record " + e.Event + " for operation " + e.OperationID + ": " + err.Error())
	}
}
