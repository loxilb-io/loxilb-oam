package appliance

import (
	"context"
	"errors"
	"fmt"
)

// Caller is the authenticated user on whose behalf the service acts.
// Permitted is their authorization, resolved from the database for this
// request.
type Caller struct {
	UserID    int
	Username  string
	Permitted func(Action) bool
}

var (
	// ErrSchemaVersion: the request names a contract version OAM does not speak.
	ErrSchemaVersion = errors.New("unsupported schema_version")
	// ErrIdempotencyKey: the Idempotency-Key header is missing or malformed.
	ErrIdempotencyKey = errors.New("a valid Idempotency-Key header is required")
	// ErrPermissionDenied: the caller's role may not run this operation type.
	ErrPermissionDenied = errors.New("your role does not permit this operation")
	// ErrIdempotencyKeyReused: the key already names a different request.
	ErrIdempotencyKeyReused = errors.New("the idempotency key was already used for a different request")
	// ErrOperationNotFound: no operation has that ID.
	ErrOperationNotFound = errors.New("operation not found")
	// ErrInvalidFilter: a list parameter is malformed.
	ErrInvalidFilter = errors.New("invalid list parameter")
)

// List page sizes.
const (
	DefaultListLimit = 20
	MaxListLimit     = 100
)

// PlanOperation validates a request with the host adapter and records the
// resulting plan. Nothing is executed. It returns the operation and whether
// this call created it: the same idempotency key with the same request
// returns the operation made the first time.
func (s *Service) PlanOperation(ctx context.Context, caller Caller, key, requestID string, req PlanRequest) (*Operation, bool, error) {
	if !ValidIdempotencyKey(key) {
		return nil, false, ErrIdempotencyKey
	}
	if req.SchemaVersion != SchemaVersion {
		return nil, false, fmt.Errorf("%w: this server speaks %s", ErrSchemaVersion, SchemaVersion)
	}
	if err := req.Validate(); err != nil {
		return nil, false, err
	}
	// Authorization comes before anything that reveals state: a caller who
	// may not run the operation learns nothing from asking.
	if !caller.Permitted(req.Type.Action()) {
		return nil, false, ErrPermissionDenied
	}
	if err := expirePlans(ctx, s.db); err != nil {
		return nil, false, err
	}

	existing, err := operationByKey(ctx, s.db, caller.UserID, key)
	if err != nil {
		return nil, false, err
	}
	if existing != nil {
		return sameRequest(existing, req)
	}

	id, err := newOperationID(s.now())
	if err != nil {
		return nil, false, err
	}
	plan, err := s.host.Plan(ctx, HostPlanRequest{
		SchemaVersion:    SchemaVersion,
		OperationID:      id,
		Type:             req.Type,
		ArchiveRef:       req.ArchiveRef,
		TargetReleaseRef: req.TargetReleaseRef,
	})
	if err != nil {
		return nil, false, err
	}

	inserted, err := insertPlanned(ctx, s.db, id, caller, key, requestID, req, plan)
	if err != nil {
		return nil, false, err
	}
	if !inserted {
		// A concurrent request with the same key won the insert. Planning has
		// no side effects on the host, so losing costs nothing: answer with
		// the winner, exactly as a later retry would be answered.
		winner, err := operationByKey(ctx, s.db, caller.UserID, key)
		if err != nil {
			return nil, false, err
		}
		if winner == nil {
			return nil, false, errors.New("operation vanished between insert and read")
		}
		return sameRequest(winner, req)
	}
	op, err := operationByID(ctx, s.db, id)
	if err != nil {
		return nil, false, err
	}
	if op == nil {
		return nil, false, errors.New("operation vanished between insert and read")
	}
	return op, true, nil
}

// sameRequest answers a repeated idempotency key: the stored operation if the
// request is the one that created it, a conflict otherwise.
func sameRequest(existing *Operation, req PlanRequest) (*Operation, bool, error) {
	if existing.RequestHash != req.Hash() {
		return nil, false, ErrIdempotencyKeyReused
	}
	return existing, false, nil
}

// visibleTo returns the operation as caller may see it: in full if they may
// run its type, otherwise without the plan.
func visibleTo(op Operation, caller Caller) Operation {
	if caller.Permitted(op.Type.Action()) {
		return op
	}
	return op.redacted()
}

// GetOperation returns one operation.
func (s *Service) GetOperation(ctx context.Context, caller Caller, id string) (*Operation, error) {
	if !ValidOperationID(id) {
		return nil, ErrOperationNotFound
	}
	if err := expirePlans(ctx, s.db); err != nil {
		return nil, err
	}
	op, err := operationByID(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	if op == nil {
		return nil, ErrOperationNotFound
	}
	visible := visibleTo(*op, caller)
	return &visible, nil
}

// ListOperations returns a page of operations, newest first.
func (s *Service) ListOperations(ctx context.Context, caller Caller, f ListFilter) (*OperationList, error) {
	if f.State != "" && !f.State.Valid() {
		return nil, fmt.Errorf("%w: state", ErrInvalidFilter)
	}
	if f.Type != "" && !f.Type.Valid() {
		return nil, fmt.Errorf("%w: type", ErrInvalidFilter)
	}
	if f.Cursor != "" && !ValidOperationID(f.Cursor) {
		return nil, fmt.Errorf("%w: cursor", ErrInvalidFilter)
	}
	if f.Limit == 0 {
		f.Limit = DefaultListLimit
	}
	if f.Limit < 1 || f.Limit > MaxListLimit {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidFilter, MaxListLimit)
	}
	if err := expirePlans(ctx, s.db); err != nil {
		return nil, err
	}
	rows, err := listOperations(ctx, s.db, f)
	if err != nil {
		return nil, err
	}
	out := &OperationList{SchemaVersion: SchemaVersion, Items: make([]Operation, 0, len(rows))}
	if len(rows) > f.Limit {
		rows = rows[:f.Limit]
		out.NextCursor = rows[len(rows)-1].ID
	}
	for _, op := range rows {
		out.Items = append(out.Items, visibleTo(op, caller))
	}
	return out, nil
}
