package appliance

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/loxilb-io/loxilb-oam/internal/utils"
)

var (
	// ErrOperationState: the operation is not in a state that allows the request.
	ErrOperationState = errors.New("the operation is not in a state that allows this")
	// ErrPlanExpired: the plan was not submitted in time.
	ErrPlanExpired = errors.New("the plan has expired")
	// ErrPlanStale: the plan_hash presented is not the operation's.
	ErrPlanStale = errors.New("plan_hash does not match the operation's plan")
	// ErrAuthorizationNotRequired: the operation type needs no challenge.
	ErrAuthorizationNotRequired = errors.New("this operation does not require authorization")
	// ErrSessionUnbound: the caller's token carries no session identifier to
	// bind an authorization to (it predates unique token IDs).
	ErrSessionUnbound = errors.New("this session cannot authorize operations; log in again")
	// ErrChallengeRequired: a destructive operation was submitted without one.
	ErrChallengeRequired = errors.New("a challenge is required to submit this operation")
	// ErrChallengeMismatch: the challenge is unknown, or was issued for another
	// operation, plan, user or session.
	ErrChallengeMismatch = errors.New("the challenge does not authorize this request")
	// ErrChallengeConsumed: the challenge was already used.
	ErrChallengeConsumed = errors.New("the challenge was already used")
	// ErrChallengeExpired: the challenge is no longer valid.
	ErrChallengeExpired = errors.New("the challenge has expired")
	// ErrNotCancellable: the operation can no longer be cancelled.
	ErrNotCancellable = errors.New("the operation can no longer be cancelled")
)

// ConflictError reports that another operation occupies the installation.
type ConflictError struct {
	BlockingOperationID string
}

func (e *ConflictError) Error() string {
	return "another operation is active on this installation: " + e.BlockingOperationID
}

// Challenge is returned once by Authorize. OAM keeps only its hash.
type Challenge struct {
	Challenge   string    `json:"challenge"`
	ExpiresAt   time.Time `json:"expires_at"`
	OperationID string    `json:"operation_id"`
	PlanHash    string    `json:"plan_hash"`
}

func hashChallenge(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

const oneActiveIndex = "uk_appliance_operations_one_active"

// isActiveConflict reports whether err is the one-active-operation index
// refusing a transition.
func isActiveConflict(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == oneActiveIndex
}

type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// blockingOperation returns the ID of the operation occupying installation,
// other than exceptID; empty if none.
func blockingOperation(ctx context.Context, db queryRower, installationID, exceptID string) (string, error) {
	var id string
	err := db.QueryRowContext(ctx, `
		SELECT id FROM appliance_operations
		 WHERE installation_id = $1 AND id <> $2
		   AND state IN ('AWAITING_AUTHORIZATION', 'QUEUED', 'RUNNING', 'VERIFYING', 'COMPENSATING', 'RECOVERY_REQUIRED')
		 LIMIT 1`, installationID, exceptID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return id, err
}

func (s *Service) conflict(ctx context.Context, op *Operation) error {
	blocker, err := blockingOperation(ctx, s.db, op.InstallationID, op.ID)
	if err != nil {
		return err
	}
	return &ConflictError{BlockingOperationID: blocker}
}

// load fetches an operation the caller is allowed to act on (not merely see).
func (s *Service) load(ctx context.Context, caller Caller, id string) (*Operation, error) {
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
	if !caller.Permitted(op.Type.Action()) {
		return nil, ErrPermissionDenied
	}
	return op, nil
}

// expiredOr turns "the plan expired" into its own error for an operation that
// was cancelled for that reason, and otherwise reports a state error.
func expiredOr(op *Operation) error {
	if op.State == StateCancelled && op.ErrorCode == CodePlanExpired {
		return ErrPlanExpired
	}
	return fmt.Errorf("%w: it is %s", ErrOperationState, op.State)
}

// AuthorizeRequest is the body of POST …/operations/{id}/authorize.
type AuthorizeRequest struct {
	// Password is the caller's current password. It is verified and
	// discarded; it is never stored or logged.
	Password string `json:"password"`
}

// SubmitRequest is the body of POST …/operations/{id}/submit.
type SubmitRequest struct {
	// PlanHash is the plan the caller reviewed.
	PlanHash string `json:"plan_hash"`
	// Challenge is the value Authorize returned. Required for an operation
	// that requires reauthentication, ignored otherwise.
	Challenge string `json:"challenge,omitempty"`
}

// authorizable loads an operation and checks everything about authorizing it
// that can be checked without changing anything.
func (s *Service) authorizable(ctx context.Context, caller Caller, id string) (*Operation, error) {
	op, err := s.load(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	if !op.Type.Action().Destructive() {
		return nil, ErrAuthorizationNotRequired
	}
	if caller.SessionID == "" {
		return nil, ErrSessionUnbound
	}
	if op.State != StatePlanned && op.State != StateAwaitingAuthorization {
		return nil, expiredOr(op)
	}
	return op, nil
}

// CanAuthorize reports whether Authorize would be refused for a reason that
// has nothing to do with the caller's password. The HTTP layer asks before it
// verifies one, so a request that could never be authorized neither costs a
// password attempt nor leaves a denial in the audit trail.
func (s *Service) CanAuthorize(ctx context.Context, caller Caller, id string) error {
	_, err := s.authorizable(ctx, caller, id)
	return err
}

// Authorize issues a one-use challenge for a destructive operation. The
// caller must already have proven their password for this request; that
// check belongs to the HTTP layer, which owns lockout.
//
// The challenge is bound to the operation, its plan, the installation, the
// user and the session. Authorizing moves the operation to
// AWAITING_AUTHORIZATION, which occupies the installation: a second operation
// cannot be authorized until this one is submitted, cancelled or expires.
// Authorizing again replaces the previous challenge.
func (s *Service) Authorize(ctx context.Context, caller Caller, id string) (*Challenge, error) {
	op, err := s.authorizable(ctx, caller, id)
	if err != nil {
		return nil, err
	}

	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return nil, err
	}
	value := hex.EncodeToString(raw)

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() // no-op after Commit

	result, err := tx.ExecContext(ctx, `
		UPDATE appliance_operations SET state = $1
		 WHERE id = $2 AND state IN ($3, $1) AND plan_expires_at > NOW()`,
		string(StateAwaitingAuthorization), op.ID, string(StatePlanned))
	if err != nil {
		if isActiveConflict(err) {
			tx.Rollback()
			return nil, s.conflict(ctx, op)
		}
		return nil, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		return nil, ErrPlanExpired
	}
	// One live challenge per operation.
	if _, err := tx.ExecContext(ctx,
		"DELETE FROM appliance_challenges WHERE operation_id = $1 AND consumed_at IS NULL", op.ID); err != nil {
		return nil, err
	}
	var expiresAt time.Time
	if err := tx.QueryRowContext(ctx, `
		INSERT INTO appliance_challenges
			(challenge_hash, operation_id, actor_user_id, session_id, plan_hash, installation_id, action, expires_at)
		SELECT $1, id, $2, $3, plan_hash, installation_id, type,
		       LEAST(NOW() + make_interval(secs => $4), plan_expires_at)
		  FROM appliance_operations WHERE id = $5
		RETURNING expires_at`,
		hashChallenge(value), caller.UserID, caller.SessionID, ChallengeTTL.Seconds(), op.ID).Scan(&expiresAt); err != nil {
		return nil, err
	}
	if err := writeAudit(ctx, tx, auditEntry{
		OperationID: op.ID, Event: AuditAuthorize, Actor: &caller,
		OldState: op.State, NewState: StateAwaitingAuthorization,
		Detail: auditDetail{Type: op.Type, PlanHash: op.PlanHash},
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return &Challenge{Challenge: value, ExpiresAt: expiresAt.UTC(), OperationID: op.ID, PlanHash: op.PlanHash}, nil
}

// AuthorizationDenied records a failed reauthentication against an operation.
// reason is a machine code.
func (s *Service) AuthorizationDenied(ctx context.Context, caller Caller, id, reason string) {
	if !ValidOperationID(id) {
		return
	}
	audit(ctx, s.db, auditEntry{OperationID: id, Event: AuditAuthorizeDenied, Actor: &caller, Detail: auditDetail{Reason: reason}})
}

// consumeChallenge marks the challenge used, inside tx, if and only if it
// authorizes exactly this request. A single UPDATE decides: of any number of
// concurrent submits presenting the same challenge, one gets the row.
func consumeChallenge(ctx context.Context, tx *sql.Tx, op *Operation, caller Caller, value string) error {
	hash := hashChallenge(value)
	result, err := tx.ExecContext(ctx, `
		UPDATE appliance_challenges SET consumed_at = NOW()
		 WHERE challenge_hash = $1 AND operation_id = $2 AND actor_user_id = $3 AND session_id = $4
		   AND plan_hash = $5 AND installation_id = $6
		   AND consumed_at IS NULL AND expires_at > NOW()`,
		hash, op.ID, caller.UserID, caller.SessionID, op.PlanHash, op.InstallationID)
	if err != nil {
		return err
	}
	if n, _ := result.RowsAffected(); n == 1 {
		return nil
	}
	// Refused. Say why, as far as that can be said without confirming to a
	// stranger that a challenge exists: every binding mismatch is the same
	// error as an unknown challenge.
	var consumed sql.NullTime
	var expired bool
	err = tx.QueryRowContext(ctx, `
		SELECT consumed_at, expires_at <= NOW() FROM appliance_challenges
		 WHERE challenge_hash = $1 AND operation_id = $2 AND actor_user_id = $3 AND session_id = $4
		   AND plan_hash = $5 AND installation_id = $6`,
		hash, op.ID, caller.UserID, caller.SessionID, op.PlanHash, op.InstallationID).Scan(&consumed, &expired)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return ErrChallengeMismatch
	case err != nil:
		return err
	case consumed.Valid:
		return ErrChallengeConsumed
	case expired:
		return ErrChallengeExpired
	}
	return ErrChallengeConsumed // consumed between the two statements
}

func (s *Service) jobRequest(op *Operation) HostJobRequest {
	return HostJobRequest{
		SchemaVersion:    SchemaVersion,
		OperationID:      op.ID,
		Type:             op.Type,
		InstallationID:   op.InstallationID,
		PlanHash:         op.PlanHash,
		ArchiveRef:       op.request.ArchiveRef,
		TargetReleaseRef: op.request.TargetReleaseRef,
	}
}

// Submit hands a planned operation to the host adapter.
//
// A destructive operation must be AWAITING_AUTHORIZATION and present its
// challenge; a backup is submitted straight from PLANNED. In one transaction
// the challenge is consumed, the operation becomes QUEUED and the submission
// is audited — so a consumed challenge always has a queued operation and an
// audit row to show for it. Only then is the host asked. If OAM stops between
// the two, the reconciler finds a QUEUED operation the host has never
// reported on and submits it; the adapter's idempotency on the operation ID
// makes that safe.
//
// Submitting an operation that was already submitted returns it unchanged.
func (s *Service) Submit(ctx context.Context, caller Caller, id, planHash, challenge string) (*Operation, error) {
	op, err := s.load(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	if planHash != op.PlanHash {
		return nil, ErrPlanStale
	}
	if op.SubmittedAt != nil {
		return op, nil
	}
	destructive := op.Type.Action().Destructive()
	expected := StatePlanned
	if destructive {
		expected = StateAwaitingAuthorization
		if op.State == StatePlanned {
			return nil, ErrChallengeRequired
		}
	}
	if op.State != expected {
		return nil, expiredOr(op)
	}
	if destructive && challenge == "" {
		return nil, ErrChallengeRequired
	}
	if destructive && caller.SessionID == "" {
		return nil, ErrSessionUnbound
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback() // no-op after Commit

	if destructive {
		if err := consumeChallenge(ctx, tx, op, caller, challenge); err != nil {
			tx.Rollback()
			if errors.Is(err, ErrChallengeMismatch) || errors.Is(err, ErrChallengeConsumed) || errors.Is(err, ErrChallengeExpired) {
				audit(ctx, s.db, auditEntry{OperationID: op.ID, Event: AuditSubmitDenied, Actor: &caller, OldState: op.State,
					Detail: auditDetail{Type: op.Type, Reason: challengeCode(err)}})
			}
			return nil, err
		}
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE appliance_operations SET state = $1, submitted_at = NOW()
		 WHERE id = $2 AND state = $3 AND plan_expires_at > NOW()`,
		string(StateQueued), op.ID, string(expected))
	if err != nil {
		if isActiveConflict(err) {
			tx.Rollback()
			return nil, s.conflict(ctx, op)
		}
		return nil, err
	}
	if n, _ := result.RowsAffected(); n != 1 {
		// Lost a race with expiry, cancellation or another submit.
		return nil, ErrOperationState
	}
	if err := writeAudit(ctx, tx, auditEntry{
		OperationID: op.ID, Event: AuditSubmit, Actor: &caller, OldState: op.State, NewState: StateQueued,
		Detail: auditDetail{Type: op.Type, PlanHash: op.PlanHash},
	}); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	utils.LogInfo("Appliance operation submitted: id=" + op.ID + " type=" + string(op.Type) + " by=" + caller.Username)

	s.deliver(ctx, op.ID)
	return s.reload(ctx, op.ID)
}

// challengeCode is the machine code of a challenge refusal.
func challengeCode(err error) string {
	switch {
	case errors.Is(err, ErrChallengeConsumed):
		return CodeChallengeConsumed
	case errors.Is(err, ErrChallengeExpired):
		return CodeChallengeExpired
	}
	return CodeChallengeMismatch
}

func (s *Service) reload(ctx context.Context, id string) (*Operation, error) {
	op, err := operationByID(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	if op == nil {
		return nil, ErrOperationNotFound
	}
	return op, nil
}

// hostErrorCodeOK reports whether a code from the adapter has the shape of a
// machine code and may be stored and shown.
func hostErrorCodeOK(code string) bool {
	if len(code) < 2 || len(code) > 64 {
		return false
	}
	for i, c := range code {
		if !(c >= 'A' && c <= 'Z' || i > 0 && (c >= '0' && c <= '9' || c == '_')) {
			return false
		}
	}
	return true
}

// undelivered locks the row of an operation and returns it if it is recorded
// as submitted while the host has never reported on it. The lock is what
// keeps delivering such an operation and cancelling it from interleaving:
// whoever holds it asks the host and records the answer before the other can
// look. It returns nil, without error, if the operation is no longer in that
// condition.
func undelivered(ctx context.Context, tx *sql.Tx, id string) (*Operation, error) {
	op, err := scanOperation(tx.QueryRowContext(ctx,
		"SELECT "+operationColumns+" FROM appliance_operations WHERE id = $1 FOR UPDATE", id))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if op.State != StateQueued || op.HostGeneration != 0 {
		return nil, nil
	}
	return op, nil
}

// deliver hands an operation that is recorded as submitted to the host
// adapter, unless someone else has delivered or cancelled it in the meantime.
// It is used by Submit and by the reconciler, and is safe to repeat.
func (s *Service) deliver(ctx context.Context, id string) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		utils.LogError("Appliance operation " + id + ": failed to begin delivery: " + err.Error())
		return
	}
	defer tx.Rollback() // no-op after Commit
	op, err := undelivered(ctx, tx, id)
	if err != nil {
		utils.LogError("Appliance operation " + id + ": failed to read for delivery: " + err.Error())
		return
	}
	if op == nil {
		return
	}
	s.submitToHost(ctx, tx, op)
	if err := tx.Commit(); err != nil {
		// Whatever the host did is in its journal; the reconciler reads it.
		utils.LogError("Appliance operation " + id + ": failed to record delivery: " + err.Error())
	}
}

// submitToHost asks the adapter to execute a QUEUED operation and records its
// answer through db.
func (s *Service) submitToHost(ctx context.Context, db execer, op *Operation) {
	job, err := s.host.Submit(ctx, s.jobRequest(op))
	var rejection *HostRejection
	switch {
	case err == nil:
		s.applyJob(ctx, db, op, job)
	case errors.As(err, &rejection):
		// The host will not run it. That is final: the operation failed
		// before it started.
		code := rejection.Code
		if !hostErrorCodeOK(code) {
			code = CodeSubmitRejected
		}
		s.finishWithoutHost(ctx, db, op, StateFailed, code, OriginHost, AuditStateChange)
	default:
		// Not delivered, or not known to be. The operation stays QUEUED with
		// no host generation, which is exactly what the reconciler retries.
		s.markUnreachable(ctx, db, op)
	}
}

// applyJob brings the operation up to a journal entry, if the entry is newer
// than what the row already reflects.
func (s *Service) applyJob(ctx context.Context, db execer, op *Operation, job *HostJob) {
	errorOrigin := ""
	if job.ErrorCode != "" {
		errorOrigin = OriginHost
	}
	errorCode := job.ErrorCode
	if errorCode != "" && !hostErrorCodeOK(errorCode) {
		errorCode = CodeSubmitRejected
	}
	result, err := db.ExecContext(ctx, `
		UPDATE appliance_operations
		   SET state = $1, phase = $2, host_generation = $3, cancellable = $4, reconciliation = $5,
		       error_code = $6, error_origin = $7,
		       finished_at = CASE WHEN $8 THEN COALESCE(finished_at, NOW()) ELSE NULL END
		 WHERE id = $9 AND host_generation < $3`,
		string(job.State), job.Phase, job.Generation, job.Cancellable && !job.State.Terminal(), ReconciliationInSync,
		errorCode, errorOrigin, job.State.Terminal(), op.ID)
	if err != nil {
		utils.LogError("Appliance operation " + op.ID + ": failed to record host state: " + err.Error())
		return
	}
	if n, _ := result.RowsAffected(); n != 1 {
		// Nothing newer. Still worth clearing a stale marker: the host answered.
		if op.Reconciliation == ReconciliationHostUnreachable {
			db.ExecContext(ctx, "UPDATE appliance_operations SET reconciliation = $1 WHERE id = $2 AND reconciliation = $3",
				ReconciliationInSync, op.ID, ReconciliationHostUnreachable) // cosmetic; retried next round
		}
		return
	}
	if job.State != op.State || job.Phase != op.Phase {
		audit(ctx, db, auditEntry{OperationID: op.ID, Event: AuditStateChange, OldState: op.State, NewState: job.State,
			Detail: auditDetail{Type: op.Type, Phase: job.Phase, HostGeneration: job.Generation, ErrorCode: errorCode, ErrorOrigin: errorOrigin}})
	}
}

// finishWithoutHost moves an operation to a state OAM decided on its own.
func (s *Service) finishWithoutHost(ctx context.Context, db execer, op *Operation, state OperationState, code, origin, event string) {
	reconciliation := ReconciliationInSync
	if code == CodeHostJobLost {
		reconciliation = ReconciliationHostUnknown
	}
	result, err := db.ExecContext(ctx, `
		UPDATE appliance_operations
		   SET state = $1, error_code = $2, error_origin = $3, reconciliation = $4, cancellable = FALSE,
		       finished_at = CASE WHEN $5 THEN NOW() ELSE NULL END
		 WHERE id = $6 AND state = $7`,
		string(state), code, origin, reconciliation, state.Terminal(), op.ID, string(op.State))
	if err != nil {
		utils.LogError("Appliance operation " + op.ID + ": failed to record " + string(state) + ": " + err.Error())
		return
	}
	if n, _ := result.RowsAffected(); n == 1 {
		audit(ctx, db, auditEntry{OperationID: op.ID, Event: event, OldState: op.State, NewState: state,
			Detail: auditDetail{Type: op.Type, ErrorCode: code, ErrorOrigin: origin, Reason: code}})
	}
}

func (s *Service) markUnreachable(ctx context.Context, db execer, op *Operation) {
	if op.Reconciliation == ReconciliationHostUnreachable {
		return
	}
	if _, err := db.ExecContext(ctx, "UPDATE appliance_operations SET reconciliation = $1 WHERE id = $2",
		ReconciliationHostUnreachable, op.ID); err != nil {
		utils.LogError("Appliance operation " + op.ID + ": failed to mark host unreachable: " + err.Error())
	}
}

// reconcile brings one submitted, unfinished operation up to the host
// journal. It never causes anything to be executed twice:
//
//   - the journal has an entry: follow it;
//   - it has none and never reported on the operation: OAM stopped between
//     recording the submission and delivering it, so deliver it now;
//   - it has none but did report before: the journal lost it. Nothing is
//     resubmitted — whatever ran, ran. The operation needs an operator.
//   - the adapter does not answer: keep what is known and mark it stale.
func (s *Service) reconcile(ctx context.Context, op *Operation) {
	if op.SubmittedAt == nil || op.State.Terminal() {
		return
	}
	job, err := s.host.Job(ctx, op.ID)
	switch {
	case err == nil:
		s.applyJob(ctx, s.db, op, job)
	case errors.Is(err, ErrHostJobNotFound) && op.HostGeneration == 0:
		s.deliver(ctx, op.ID)
	case errors.Is(err, ErrHostJobNotFound):
		if op.State != StateRecoveryRequired || op.ErrorCode != CodeHostJobLost {
			s.finishWithoutHost(ctx, s.db, op, StateRecoveryRequired, CodeHostJobLost, OriginOAM, AuditReconcile)
		}
	default:
		s.markUnreachable(ctx, s.db, op)
	}
}

// Reconcile re-reads the host journal for one operation now.
func (s *Service) Reconcile(ctx context.Context, caller Caller, id string) (*Operation, error) {
	op, err := s.load(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	s.reconcile(ctx, op)
	return s.reload(ctx, op.ID)
}

// ReconcileActive reconciles every submitted, unfinished operation once.
func (s *Service) ReconcileActive(ctx context.Context) error {
	if !s.host.Configured() {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, "SELECT "+operationColumns+` FROM appliance_operations
		 WHERE submitted_at IS NOT NULL
		   AND state IN ('QUEUED', 'RUNNING', 'VERIFYING', 'COMPENSATING', 'RECOVERY_REQUIRED')
		 ORDER BY id LIMIT 50`)
	if err != nil {
		return err
	}
	var active []*Operation
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			rows.Close()
			return err
		}
		active = append(active, op)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, op := range active {
		s.reconcile(ctx, op)
	}
	return nil
}

// ReconcileInterval is how often the host journal is polled while an
// operation is in flight.
const ReconcileInterval = 2 * time.Second

// RunReconciler follows the host journal until ctx is done. OAM asks; the
// adapter never calls back. So a restarted OAM simply resumes asking, and a
// lost, repeated or late answer cannot corrupt anything — an answer is
// applied only if its generation is newer than the one already stored.
func (s *Service) RunReconciler(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	failing := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := s.ReconcileActive(ctx)
			if err != nil && !failing && ctx.Err() == nil {
				utils.LogError("Appliance reconciler: " + err.Error())
			}
			failing = err != nil
		}
	}
}

// Cancel stops an operation if that is still possible. Before submission it
// is OAM's decision and always succeeds. After, the host adapter decides: it
// refuses once the operation has passed its irreversible phase.
func (s *Service) Cancel(ctx context.Context, caller Caller, id string) (*Operation, error) {
	op, err := s.load(ctx, caller, id)
	if err != nil {
		return nil, err
	}
	switch {
	case op.State == StateCancelled:
		return op, nil
	case op.State.Terminal():
		return nil, fmt.Errorf("%w: it is %s", ErrOperationState, op.State)
	case op.SubmittedAt == nil:
		result, err := s.db.ExecContext(ctx, `
			UPDATE appliance_operations
			   SET state = $1, error_code = $2, error_origin = $3, finished_at = NOW()
			 WHERE id = $4 AND state IN ($5, $6)`,
			string(StateCancelled), CodeCancelledByUser, OriginOAM, op.ID, string(StatePlanned), string(StateAwaitingAuthorization))
		if err != nil {
			return nil, err
		}
		if n, _ := result.RowsAffected(); n != 1 {
			return nil, ErrOperationState // submitted in the meantime
		}
		audit(ctx, s.db, auditEntry{OperationID: op.ID, Event: AuditCancel, Actor: &caller, OldState: op.State, NewState: StateCancelled,
			Detail: auditDetail{Type: op.Type, Reason: CodeCancelledByUser}})
		return s.reload(ctx, op.ID)
	}

	audit(ctx, s.db, auditEntry{OperationID: op.ID, Event: AuditCancel, Actor: &caller, OldState: op.State,
		Detail: auditDetail{Type: op.Type, Phase: op.Phase}})
	job, err := s.host.Cancel(ctx, op.ID)
	var rejection *HostRejection
	switch {
	case err == nil:
		s.applyJob(ctx, s.db, op, job)
	case errors.As(err, &rejection):
		return nil, ErrNotCancellable
	case errors.Is(err, ErrHostJobNotFound) && op.HostGeneration == 0:
		if err := s.cancelUndelivered(ctx, op.ID); err != nil {
			return nil, err
		}
	default:
		return nil, err
	}
	return s.reload(ctx, op.ID)
}

// cancelUndelivered cancels an operation that is recorded as submitted and
// that the host does not know. It holds the operation's row while it asks the
// host once more, so the reconciler cannot deliver the operation between the
// host saying "no such job" and OAM recording the cancellation — which would
// leave a cancelled operation running.
func (s *Service) cancelUndelivered(ctx context.Context, id string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit
	op, err := undelivered(ctx, tx, id)
	if err != nil {
		return err
	}
	if op == nil {
		// Delivered while this request was in flight. Cancelling it is now
		// the host's decision; the caller asks again.
		return fmt.Errorf("%w: it was delivered to the host in the meantime", ErrOperationState)
	}
	job, err := s.host.Cancel(ctx, id)
	var rejection *HostRejection
	switch {
	case err == nil:
		// The host has it after all: an earlier delivery was never recorded.
		s.applyJob(ctx, tx, op, job)
	case errors.Is(err, ErrHostJobNotFound):
		// Nothing on the host to stop, and now nothing will be delivered.
		s.finishWithoutHost(ctx, tx, op, StateCancelled, CodeCancelledByUser, OriginOAM, AuditStateChange)
	case errors.As(err, &rejection):
		return ErrNotCancellable
	default:
		return err
	}
	return tx.Commit()
}
