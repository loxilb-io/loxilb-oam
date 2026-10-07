package appliance

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// storedPlan is the JSON kept in appliance_operations.plan.
type storedPlan struct {
	Fixture bool `json:"fixture"`
	Plan
}

const operationColumns = `id, type, state, phase, installation_id, model, actor_user_id, actor_username,
	request_id, request_hash, request, plan_hash, plan_expires_at, plan, reconciliation,
	error_code, error_origin, created_at, updated_at, submitted_at, finished_at`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOperation(row rowScanner) (*Operation, error) {
	op := Operation{SchemaVersion: SchemaVersion}
	var requestJSON, planJSON []byte
	var submittedAt, finishedAt sql.NullTime
	if err := row.Scan(&op.ID, &op.Type, &op.State, &op.Phase, &op.InstallationID, &op.Model,
		&op.ActorUserID, &op.Actor, &op.RequestID, &op.RequestHash, &requestJSON, &op.PlanHash,
		&op.PlanExpiresAt, &planJSON, &op.Reconciliation, &op.ErrorCode, &op.ErrorOrigin,
		&op.CreatedAt, &op.UpdatedAt, &submittedAt, &finishedAt); err != nil {
		return nil, err
	}
	var request PlanRequest
	if err := json.Unmarshal(requestJSON, &request); err != nil {
		return nil, fmt.Errorf("stored request of operation %s is unreadable: %w", op.ID, err)
	}
	var plan storedPlan
	if err := json.Unmarshal(planJSON, &plan); err != nil {
		return nil, fmt.Errorf("stored plan of operation %s is unreadable: %w", op.ID, err)
	}
	if plan.AffectedResources == nil {
		plan.AffectedResources = []string{}
	}
	op.Note = request.Note
	op.HostFixture = plan.Fixture
	op.Plan = &plan.Plan
	op.RequiresReauthentication = op.Type.Action().Destructive()
	op.CreatedAt, op.UpdatedAt, op.PlanExpiresAt = op.CreatedAt.UTC(), op.UpdatedAt.UTC(), op.PlanExpiresAt.UTC()
	if submittedAt.Valid {
		t := submittedAt.Time.UTC()
		op.SubmittedAt = &t
	}
	if finishedAt.Valid {
		t := finishedAt.Time.UTC()
		op.FinishedAt = &t
	}
	return &op, nil
}

// insertPlanned stores a newly planned operation. It reports false, without
// error, when the caller already holds an operation under the same
// idempotency key — the unique key decides a race between two identical
// requests, and the loser reads the winner.
func insertPlanned(ctx context.Context, db *sql.DB, id string, caller Caller, key, requestID string, req PlanRequest, plan *HostPlan) (bool, error) {
	requestJSON, err := json.Marshal(req)
	if err != nil {
		return false, err
	}
	planJSON, err := json.Marshal(storedPlan{Fixture: plan.Fixture, Plan: plan.Plan})
	if err != nil {
		return false, err
	}
	// Expiry is computed on the database clock, the same one that later
	// decides whether the plan has expired.
	result, err := db.ExecContext(ctx, `
		INSERT INTO appliance_operations
			(id, type, state, installation_id, model, actor_user_id, actor_username, request_id,
			 idempotency_key, request_hash, request, plan_hash, plan_expires_at, plan)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, NOW() + make_interval(secs => $13), $14)
		ON CONFLICT ON CONSTRAINT uk_appliance_operations_idempotency DO NOTHING`,
		id, string(req.Type), string(StatePlanned), plan.InstallationID, plan.Model, caller.UserID, caller.Username,
		requestID, key, req.Hash(), requestJSON, plan.PlanHash, PlanTTL.Seconds(), planJSON)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func operationByKey(ctx context.Context, db *sql.DB, userID int, key string) (*Operation, error) {
	op, err := scanOperation(db.QueryRowContext(ctx,
		"SELECT "+operationColumns+" FROM appliance_operations WHERE actor_user_id = $1 AND idempotency_key = $2", userID, key))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return op, err
}

func operationByID(ctx context.Context, db *sql.DB, id string) (*Operation, error) {
	op, err := scanOperation(db.QueryRowContext(ctx,
		"SELECT "+operationColumns+" FROM appliance_operations WHERE id = $1", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return op, err
}

// expirePlans cancels plans nobody submitted in time. It runs before every
// read and write of operations rather than on a timer: an expired plan is
// only observable through those, so this is exactly as prompt as it needs to
// be and there is nothing to keep running.
func expirePlans(ctx context.Context, db *sql.DB) error {
	_, err := db.ExecContext(ctx, `
		UPDATE appliance_operations
		   SET state = $1, error_code = $2, error_origin = $3, finished_at = NOW()
		 WHERE state IN ($4, $5) AND plan_expires_at < NOW()`,
		string(StateCancelled), CodePlanExpired, OriginOAM, string(StatePlanned), string(StateAwaitingAuthorization))
	return err
}

// ListFilter narrows and pages GET /operations.
type ListFilter struct {
	State  OperationState // empty: any
	Type   OperationType  // empty: any
	Cursor string         // ID of the last item of the previous page
	Limit  int
}

// listOperations returns up to limit+1 rows, newest first; the extra row tells
// the caller whether another page exists.
func listOperations(ctx context.Context, db *sql.DB, f ListFilter) ([]Operation, error) {
	where := []string{}
	args := []any{}
	add := func(condition string, value any) {
		args = append(args, value)
		where = append(where, fmt.Sprintf(condition, len(args)))
	}
	if f.State != "" {
		add("state = $%d", string(f.State))
	}
	if f.Type != "" {
		add("type = $%d", string(f.Type))
	}
	if f.Cursor != "" {
		add("id < $%d", f.Cursor)
	}
	query := "SELECT " + operationColumns + " FROM appliance_operations"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	args = append(args, f.Limit+1)
	query += fmt.Sprintf(" ORDER BY id DESC LIMIT $%d", len(args))

	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Operation{}
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *op)
	}
	return out, rows.Err()
}
