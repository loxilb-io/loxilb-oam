package appliance_test

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/loxilb-io/loxilb-oam/internal/appliance"
	"github.com/loxilb-io/loxilb-oam/internal/appliance/hostfixture"
)

// switchedHost is the fixture's client with two switches: down makes every
// call fail as an unreachable adapter does, and stale, when set, answers Job
// in place of the journal — to replay an old entry.
type switchedHost struct {
	appliance.HostClient
	down  atomic.Bool
	stale atomic.Pointer[appliance.HostJob]
}

func (h *switchedHost) Submit(ctx context.Context, req appliance.HostJobRequest) (*appliance.HostJob, error) {
	if h.down.Load() {
		return nil, appliance.ErrHostUnreachable
	}
	return h.HostClient.Submit(ctx, req)
}

func (h *switchedHost) Job(ctx context.Context, id string) (*appliance.HostJob, error) {
	if h.down.Load() {
		return nil, appliance.ErrHostUnreachable
	}
	if job := h.stale.Load(); job != nil {
		replay := *job
		return &replay, nil
	}
	return h.HostClient.Job(ctx, id)
}

func (h *switchedHost) Cancel(ctx context.Context, id string) (*appliance.HostJob, error) {
	if h.down.Load() {
		return nil, appliance.ErrHostUnreachable
	}
	return h.HostClient.Cancel(ctx, id)
}

// lifecycle is a service on a migrated database with a fixture host whose
// jobs move only when the test calls Advance.
type lifecycle struct {
	t       *testing.T
	service *appliance.Service
	db      *sql.DB
	fixture *hostfixture.Host
	host    *switchedHost
	keys    int
}

func newLifecycle(t *testing.T) *lifecycle {
	t.Helper()
	db := migratedDB(t)
	client, fixture, _ := startFixture(t, key, appliance.Actions...)
	host := &switchedHost{HostClient: client}
	return &lifecycle{t: t, service: appliance.NewService(host, db, "test"), db: db, fixture: fixture, host: host}
}

// in returns caller as it appears inside the session with that token ID.
func in(caller appliance.Caller, session string) appliance.Caller {
	caller.SessionID = session
	caller.RequestID = "req-" + session
	caller.SourceIP = "192.0.2.10"
	return caller
}

func (l *lifecycle) plan(caller appliance.Caller, typ appliance.OperationType) *appliance.Operation {
	l.t.Helper()
	l.keys++
	op, created, err := l.service.PlanOperation(context.Background(), caller, fmt.Sprintf("lifecycle-key-%06d", l.keys), caller.RequestID, planRequest(typ))
	require.NoError(l.t, err)
	require.True(l.t, created)
	return op
}

func (l *lifecycle) get(id string) *appliance.Operation {
	l.t.Helper()
	op, err := l.service.GetOperation(context.Background(), admin(1), id)
	require.NoError(l.t, err)
	return op
}

// follow advances the host job n steps, reconciling after each, and returns
// the operation as OAM then sees it.
func (l *lifecycle) follow(id string, steps int) *appliance.Operation {
	l.t.Helper()
	for i := 0; i < steps; i++ {
		require.True(l.t, l.fixture.Advance(id), "step %d", i+1)
		require.NoError(l.t, l.service.ReconcileActive(context.Background()))
	}
	return l.get(id)
}

// authorized plans a destructive operation and authorizes it, returning the
// operation and its challenge.
func (l *lifecycle) authorized(caller appliance.Caller, typ appliance.OperationType) (*appliance.Operation, *appliance.Challenge) {
	l.t.Helper()
	op := l.plan(caller, typ)
	challenge, err := l.service.Authorize(context.Background(), caller, op.ID)
	require.NoError(l.t, err)
	return op, challenge
}

func (l *lifecycle) count(query string, args ...any) int {
	l.t.Helper()
	var n int
	require.NoError(l.t, l.db.QueryRow(query, args...).Scan(&n))
	return n
}

func (l *lifecycle) auditEvents(id string) []string {
	l.t.Helper()
	rows, err := l.db.Query("SELECT event FROM appliance_audit WHERE operation_id = $1 ORDER BY id", id)
	require.NoError(l.t, err)
	defer rows.Close()
	var events []string
	for rows.Next() {
		var event string
		require.NoError(l.t, rows.Scan(&event))
		events = append(events, event)
	}
	require.NoError(l.t, rows.Err())
	return events
}

func requireConflict(t *testing.T, err error, blocker string) {
	t.Helper()
	var conflict *appliance.ConflictError
	require.ErrorAs(t, err, &conflict)
	assert.Equal(t, blocker, conflict.BlockingOperationID)
}

// A backup needs no authorization: plan, submit, and follow the host journal
// to the end. While it runs it occupies the installation; when it is done it
// does not.
func TestBackupRunsToSuccess(t *testing.T) {
	l := newLifecycle(t)
	ctx := context.Background()
	caller := in(admin(1), "s1")

	op := l.plan(caller, appliance.OperationBackup)
	_, err := l.service.Authorize(ctx, caller, op.ID)
	assert.ErrorIs(t, err, appliance.ErrAuthorizationNotRequired)

	_, err = l.service.Submit(ctx, caller, op.ID, "sha256:not-the-plan", "")
	assert.ErrorIs(t, err, appliance.ErrPlanStale)

	queued, err := l.service.Submit(ctx, caller, op.ID, op.PlanHash, "")
	require.NoError(t, err)
	assert.Equal(t, appliance.StateQueued, queued.State)
	assert.NotNil(t, queued.SubmittedAt)
	assert.EqualValues(t, 1, queued.HostGeneration)
	assert.True(t, queued.Cancellable)
	assert.False(t, queued.Stale)

	// The installation is occupied.
	caps := byAction(l.service.Capabilities(ctx, allow(appliance.Actions...)))
	assert.False(t, caps[appliance.ActionBackup].Available)
	assert.Equal(t, appliance.ReasonOperationInProgress, caps[appliance.ActionBackup].UnavailableReason)
	second := l.plan(caller, appliance.OperationBackup)
	_, err = l.service.Submit(ctx, caller, second.ID, second.PlanHash, "")
	requireConflict(t, err, op.ID)

	// Submitting it again changes nothing.
	again, err := l.service.Submit(ctx, caller, op.ID, op.PlanHash, "")
	require.NoError(t, err)
	assert.Equal(t, appliance.StateQueued, again.State)

	running := l.follow(op.ID, 1)
	assert.Equal(t, appliance.StateRunning, running.State)
	assert.Equal(t, "prepare", running.Phase)
	assert.Nil(t, running.FinishedAt)

	verifying := l.follow(op.ID, 2)
	assert.Equal(t, appliance.StateVerifying, verifying.State)
	assert.False(t, verifying.Cancellable, "past the last cancellable step")

	done := l.follow(op.ID, 1)
	assert.Equal(t, appliance.StateSucceeded, done.State)
	assert.NotNil(t, done.FinishedAt)
	assert.False(t, done.Cancellable)
	assert.Empty(t, done.ErrorCode)
	assert.Equal(t, 1, l.fixture.Executions(op.ID))
	assert.Equal(t, 1, l.fixture.Submissions(op.ID))

	// Released.
	caps = byAction(l.service.Capabilities(ctx, allow(appliance.Actions...)))
	assert.True(t, caps[appliance.ActionBackup].Available)
	next, err := l.service.Submit(ctx, caller, second.ID, second.PlanHash, "")
	require.NoError(t, err)
	assert.Equal(t, appliance.StateQueued, next.State)

	// A finished operation is not reconciled or cancelled any more.
	_, err = l.service.Cancel(ctx, caller, op.ID)
	assert.ErrorIs(t, err, appliance.ErrOperationState)
}

// A destructive operation is submitted only with the challenge issued for
// exactly this operation, plan, user and session.
func TestDestructiveSubmitNeedsItsChallenge(t *testing.T) {
	l := newLifecycle(t)
	ctx := context.Background()
	caller := in(admin(1), "s1")

	op := l.plan(caller, appliance.OperationRestore)
	_, err := l.service.Submit(ctx, caller, op.ID, op.PlanHash, "")
	assert.ErrorIs(t, err, appliance.ErrChallengeRequired, "not authorized yet")

	// Who may authorize, and when.
	_, err = l.service.Authorize(ctx, admin(1), op.ID)
	assert.ErrorIs(t, err, appliance.ErrSessionUnbound, "a token without an ID is no session")
	_, err = l.service.Authorize(ctx, in(viewer(3), "s3"), op.ID)
	assert.ErrorIs(t, err, appliance.ErrPermissionDenied)
	_, err = l.service.Authorize(ctx, caller, "00000000-0000-7000-8000-000000000000")
	assert.ErrorIs(t, err, appliance.ErrOperationNotFound)
	assert.Equal(t, appliance.StatePlanned, l.get(op.ID).State, "a refused authorization changes nothing")

	replaced, err := l.service.Authorize(ctx, caller, op.ID)
	require.NoError(t, err)
	assert.Equal(t, appliance.StateAwaitingAuthorization, l.get(op.ID).State)
	challenge, err := l.service.Authorize(ctx, caller, op.ID)
	require.NoError(t, err)
	assert.NotEqual(t, replaced.Challenge, challenge.Challenge)
	assert.Len(t, challenge.Challenge, 64)
	assert.Equal(t, op.ID, challenge.OperationID)
	assert.Equal(t, op.PlanHash, challenge.PlanHash)
	assert.InDelta(t, appliance.ChallengeTTL.Seconds(), time.Until(challenge.ExpiresAt).Seconds(), 5)
	assert.Equal(t, 1, l.count("SELECT COUNT(*) FROM appliance_challenges WHERE operation_id = $1", op.ID), "one live challenge per operation")

	refusals := []struct {
		name      string
		caller    appliance.Caller
		planHash  string
		challenge string
		want      error
	}{
		{"no challenge", caller, op.PlanHash, "", appliance.ErrChallengeRequired},
		{"an invented challenge", caller, op.PlanHash, strings.Repeat("0", 64), appliance.ErrChallengeMismatch},
		{"the challenge it replaced", caller, op.PlanHash, replaced.Challenge, appliance.ErrChallengeMismatch},
		{"another session of the same user", in(admin(1), "s2"), op.PlanHash, challenge.Challenge, appliance.ErrChallengeMismatch},
		{"another user", in(admin(2), "s1"), op.PlanHash, challenge.Challenge, appliance.ErrChallengeMismatch},
		{"a session without an ID", admin(1), op.PlanHash, challenge.Challenge, appliance.ErrSessionUnbound},
		{"another plan", caller, "sha256:not-the-plan", challenge.Challenge, appliance.ErrPlanStale},
		{"a role that lost the capability", in(viewer(1), "s1"), op.PlanHash, challenge.Challenge, appliance.ErrPermissionDenied},
	}
	for _, r := range refusals {
		_, err := l.service.Submit(ctx, r.caller, op.ID, r.planHash, r.challenge)
		assert.ErrorIs(t, err, r.want, r.name)
	}
	assert.Equal(t, appliance.StateAwaitingAuthorization, l.get(op.ID).State)
	assert.Zero(t, l.fixture.Submissions(op.ID), "nothing refused may reach the host")
	assert.Equal(t, 4, l.count("SELECT COUNT(*) FROM appliance_audit WHERE operation_id = $1 AND event = $2", op.ID, appliance.AuditSubmitDenied),
		"each wrong challenge is recorded")

	queued, err := l.service.Submit(ctx, caller, op.ID, op.PlanHash, challenge.Challenge)
	require.NoError(t, err)
	assert.Equal(t, appliance.StateQueued, queued.State)
	assert.Equal(t, 1, l.count("SELECT COUNT(*) FROM appliance_challenges WHERE operation_id = $1 AND consumed_at IS NOT NULL", op.ID))

	// Presenting it again starts nothing.
	replay, err := l.service.Submit(ctx, caller, op.ID, op.PlanHash, challenge.Challenge)
	require.NoError(t, err)
	assert.Equal(t, queued.ID, replay.ID)
	assert.Equal(t, 1, l.fixture.Executions(op.ID))
	assert.Equal(t, 1, l.fixture.Submissions(op.ID))

	// And it can no longer be authorized.
	_, err = l.service.Authorize(ctx, caller, op.ID)
	assert.ErrorIs(t, err, appliance.ErrOperationState)
}

// A challenge is good for one operation only, expires, and cannot be used
// twice even if the operation were somehow still waiting.
func TestChallengeIsBoundExpiresAndIsConsumedOnce(t *testing.T) {
	l := newLifecycle(t)
	ctx := context.Background()
	caller := in(admin(1), "s1")

	// Issued for another operation.
	first, forFirst := l.authorized(caller, appliance.OperationRestore)
	_, err := l.service.Cancel(ctx, caller, first.ID)
	require.NoError(t, err)
	op, challenge := l.authorized(caller, appliance.OperationRollback)
	_, err = l.service.Submit(ctx, caller, op.ID, op.PlanHash, forFirst.Challenge)
	assert.ErrorIs(t, err, appliance.ErrChallengeMismatch)

	// Expired.
	_, err = l.db.Exec("UPDATE appliance_challenges SET expires_at = NOW() - INTERVAL '1 second' WHERE operation_id = $1", op.ID)
	require.NoError(t, err)
	_, err = l.service.Submit(ctx, caller, op.ID, op.PlanHash, challenge.Challenge)
	assert.ErrorIs(t, err, appliance.ErrChallengeExpired)

	// Already consumed.
	_, err = l.db.Exec("UPDATE appliance_challenges SET expires_at = NOW() + INTERVAL '1 minute', consumed_at = NOW() WHERE operation_id = $1", op.ID)
	require.NoError(t, err)
	_, err = l.service.Submit(ctx, caller, op.ID, op.PlanHash, challenge.Challenge)
	assert.ErrorIs(t, err, appliance.ErrChallengeConsumed)

	assert.Zero(t, l.fixture.Submissions(op.ID))
	assert.Equal(t, appliance.StateAwaitingAuthorization, l.get(op.ID).State)

	// A challenge never outlives its plan.
	_, err = l.db.Exec("UPDATE appliance_operations SET plan_expires_at = NOW() + INTERVAL '30 seconds' WHERE id = $1", op.ID)
	require.NoError(t, err)
	short, err := l.service.Authorize(ctx, caller, op.ID)
	require.NoError(t, err)
	assert.LessOrEqual(t, time.Until(short.ExpiresAt).Seconds(), 31.0)

	// The plan expires between authorization and submission.
	_, err = l.db.Exec("UPDATE appliance_operations SET plan_expires_at = NOW() - INTERVAL '1 second' WHERE id = $1", op.ID)
	require.NoError(t, err)
	_, err = l.service.Submit(ctx, caller, op.ID, op.PlanHash, short.Challenge)
	assert.ErrorIs(t, err, appliance.ErrPlanExpired)
	_, err = l.service.Authorize(ctx, caller, op.ID)
	assert.ErrorIs(t, err, appliance.ErrPlanExpired)
	expired := l.get(op.ID)
	assert.Equal(t, appliance.StateCancelled, expired.State)
	assert.Equal(t, appliance.CodePlanExpired, expired.ErrorCode)

	// Expiry released the installation.
	_, _ = l.authorized(caller, appliance.OperationRestore)
}

// Many submits presenting one challenge at once: the host starts one job.
func TestConcurrentSubmitsExecuteOnce(t *testing.T) {
	l := newLifecycle(t)
	caller := in(admin(1), "s1")
	op, challenge := l.authorized(caller, appliance.OperationReset)

	const racers = 16
	errs := make([]error, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = l.service.Submit(context.Background(), caller, op.ID, op.PlanHash, challenge.Challenge)
		}(i)
	}
	close(start)
	wg.Wait()

	accepted := 0
	for i, err := range errs {
		switch {
		case err == nil:
			// The winner, or a racer that arrived after it and was answered
			// with the operation already submitted.
			accepted++
		case errors.Is(err, appliance.ErrChallengeConsumed), errors.Is(err, appliance.ErrOperationState):
			// Refused: the challenge was already spent.
		default:
			t.Errorf("racer %d: unexpected error %v", i, err)
		}
	}
	assert.GreaterOrEqual(t, accepted, 1)
	assert.Equal(t, 1, l.fixture.Executions(op.ID), "the host must start exactly one job")
	assert.Equal(t, 1, l.fixture.Submissions(op.ID), "and be asked exactly once")
	assert.Equal(t, 1, l.count("SELECT COUNT(*) FROM appliance_audit WHERE operation_id = $1 AND event = $2", op.ID, appliance.AuditSubmit))
	assert.Equal(t, appliance.StateQueued, l.get(op.ID).State)
}

// One operation at a time per installation, from authorization until a
// terminal state — and an operation that needs an operator keeps it.
func TestOneActiveOperationPerInstallation(t *testing.T) {
	l := newLifecycle(t)
	ctx := context.Background()
	caller := in(admin(1), "s1")
	other := in(admin(2), "s2")

	holder, _ := l.authorized(caller, appliance.OperationRestore)

	waiting := l.plan(other, appliance.OperationUpdate)
	_, err := l.service.Authorize(ctx, other, waiting.ID)
	requireConflict(t, err, holder.ID)
	backup := l.plan(other, appliance.OperationBackup)
	_, err = l.service.Submit(ctx, other, backup.ID, backup.PlanHash, "")
	requireConflict(t, err, holder.ID)
	assert.Equal(t, appliance.StatePlanned, l.get(waiting.ID).State)
	assert.Equal(t, appliance.StatePlanned, l.get(backup.ID).State)
	assert.Zero(t, l.count("SELECT COUNT(*) FROM appliance_challenges WHERE operation_id = $1", waiting.ID), "a refused authorization leaves no challenge")

	// Cancelling the holder frees the installation.
	cancelled, err := l.service.Cancel(ctx, caller, holder.ID)
	require.NoError(t, err)
	assert.Equal(t, appliance.StateCancelled, cancelled.State)
	assert.Equal(t, appliance.CodeCancelledByUser, cancelled.ErrorCode)

	// An operation that ends needing an operator does not.
	l.fixture.SetOutcome(hostfixture.OutcomeRecovery)
	_, err = l.service.Submit(ctx, other, backup.ID, backup.PlanHash, "")
	require.NoError(t, err)
	stuck := l.follow(backup.ID, 4)
	assert.Equal(t, appliance.StateRecoveryRequired, stuck.State)
	assert.Equal(t, "FIXTURE_COMPENSATION_FAILED", stuck.ErrorCode)
	assert.Equal(t, appliance.OriginHost, stuck.ErrorOrigin)
	assert.Nil(t, stuck.FinishedAt, "not finished: an operator still has to resolve it")
	assert.False(t, stuck.Cancellable)

	_, err = l.service.Authorize(ctx, other, waiting.ID)
	requireConflict(t, err, backup.ID)
	caps := byAction(l.service.Capabilities(ctx, allow(appliance.Actions...)))
	assert.Equal(t, appliance.ReasonRecoveryRequired, caps[appliance.ActionRestore].UnavailableReason)
	_, err = l.service.Cancel(ctx, other, backup.ID)
	assert.ErrorIs(t, err, appliance.ErrNotCancellable)
}

// OAM stops, or the adapter is away, between recording a submission and
// delivering it. The reconciler delivers it — once.
func TestUndeliveredSubmitIsDeliveredOnce(t *testing.T) {
	l := newLifecycle(t)
	ctx := context.Background()
	caller := in(admin(1), "s1")
	op, challenge := l.authorized(caller, appliance.OperationUpdate)

	l.host.down.Store(true)
	queued, err := l.service.Submit(ctx, caller, op.ID, op.PlanHash, challenge.Challenge)
	require.NoError(t, err, "the submission is recorded even though the host is away")
	assert.Equal(t, appliance.StateQueued, queued.State)
	assert.EqualValues(t, 0, queued.HostGeneration)
	assert.True(t, queued.Stale)
	assert.Equal(t, appliance.ReconciliationHostUnreachable, queued.Reconciliation)

	require.NoError(t, l.service.ReconcileActive(ctx))
	assert.Zero(t, l.fixture.Submissions(op.ID))
	assert.True(t, l.get(op.ID).Stale)

	l.host.down.Store(false)
	for i := 0; i < 3; i++ {
		require.NoError(t, l.service.ReconcileActive(ctx))
	}
	delivered := l.get(op.ID)
	assert.Equal(t, appliance.StateQueued, delivered.State)
	assert.EqualValues(t, 1, delivered.HostGeneration)
	assert.False(t, delivered.Stale)
	assert.Equal(t, 1, l.fixture.Executions(op.ID))
	assert.Equal(t, 1, l.fixture.Submissions(op.ID), "later rounds read the journal; they do not submit again")

	done := l.follow(op.ID, 4)
	assert.Equal(t, appliance.StateSucceeded, done.State)
	assert.Equal(t, 1, l.fixture.Executions(op.ID))
}

// Journal reads that arrive twice or out of order, an adapter that goes
// quiet, and a journal that loses a job it had reported on.
func TestReconcileFollowsTheJournalAndNeverResubmits(t *testing.T) {
	l := newLifecycle(t)
	ctx := context.Background()
	caller := in(admin(1), "s1")
	op := l.plan(caller, appliance.OperationBackup)
	_, err := l.service.Submit(ctx, caller, op.ID, op.PlanHash, "")
	require.NoError(t, err)

	current := l.follow(op.ID, 2)
	require.Equal(t, "archive", current.Phase)
	require.EqualValues(t, 3, current.HostGeneration)
	changes := l.count("SELECT COUNT(*) FROM appliance_audit WHERE operation_id = $1 AND event = $2", op.ID, appliance.AuditStateChange)
	assert.Equal(t, 2, changes)

	// The same entry again.
	again, err := l.service.Reconcile(ctx, caller, op.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 3, again.HostGeneration)

	// An older entry, arriving late.
	l.host.stale.Store(&appliance.HostJob{OperationID: op.ID, State: appliance.StateRunning, Phase: "prepare", Generation: 2, Cancellable: true})
	late, err := l.service.Reconcile(ctx, caller, op.ID)
	require.NoError(t, err)
	assert.Equal(t, "archive", late.Phase, "a lower generation never overwrites")
	assert.EqualValues(t, 3, late.HostGeneration)
	l.host.stale.Store(nil)
	assert.Equal(t, changes, l.count("SELECT COUNT(*) FROM appliance_audit WHERE operation_id = $1 AND event = $2", op.ID, appliance.AuditStateChange),
		"a repeated or late entry is not a state change")

	// The adapter goes quiet: what is known is kept and marked stale.
	l.host.down.Store(true)
	quiet, err := l.service.Reconcile(ctx, caller, op.ID)
	require.NoError(t, err)
	assert.True(t, quiet.Stale)
	assert.Equal(t, appliance.StateRunning, quiet.State)
	assert.Equal(t, "archive", quiet.Phase)
	l.host.down.Store(false)
	back, err := l.service.Reconcile(ctx, caller, op.ID)
	require.NoError(t, err)
	assert.False(t, back.Stale)

	// Reading is for those who could run it.
	_, err = l.service.Reconcile(ctx, in(viewer(3), "s3"), op.ID)
	assert.ErrorIs(t, err, appliance.ErrPermissionDenied)

	// The journal loses the job. Whatever ran, ran: nothing is resubmitted.
	l.fixture.Forget(op.ID)
	for i := 0; i < 3; i++ {
		require.NoError(t, l.service.ReconcileActive(ctx))
	}
	lost := l.get(op.ID)
	assert.Equal(t, appliance.StateRecoveryRequired, lost.State)
	assert.Equal(t, appliance.CodeHostJobLost, lost.ErrorCode)
	assert.Equal(t, appliance.OriginOAM, lost.ErrorOrigin)
	assert.Equal(t, appliance.ReconciliationHostUnknown, lost.Reconciliation)
	assert.Equal(t, 1, l.fixture.Submissions(op.ID), "a lost job must never be resubmitted")
	assert.Equal(t, 1, l.fixture.Executions(op.ID))
	assert.Equal(t, 1, l.count("SELECT COUNT(*) FROM appliance_audit WHERE operation_id = $1 AND event = $2", op.ID, appliance.AuditReconcile))
}

// The host's own verdicts: a job that fails, and a submit it refuses.
func TestHostFailureAndRejection(t *testing.T) {
	l := newLifecycle(t)
	ctx := context.Background()
	caller := in(admin(1), "s1")

	l.fixture.SetOutcome(hostfixture.OutcomeFail)
	op := l.plan(caller, appliance.OperationBackup)
	_, err := l.service.Submit(ctx, caller, op.ID, op.PlanHash, "")
	require.NoError(t, err)
	failed := l.follow(op.ID, 2)
	assert.Equal(t, appliance.StateFailed, failed.State)
	assert.Equal(t, "FIXTURE_FAILURE", failed.ErrorCode)
	assert.Equal(t, appliance.OriginHost, failed.ErrorOrigin)
	assert.NotNil(t, failed.FinishedAt)
	l.fixture.SetOutcome(hostfixture.OutcomeSucceed)

	// The installation changed under the plan: the host refuses the submit,
	// and the operation fails before it started. The failure released the
	// installation, or this could not be submitted at all.
	refused := l.plan(caller, appliance.OperationBackup)
	_, err = l.db.Exec("UPDATE appliance_operations SET plan_hash = 'sha256:drifted' WHERE id = $1", refused.ID)
	require.NoError(t, err)
	result, err := l.service.Submit(ctx, caller, refused.ID, "sha256:drifted", "")
	require.NoError(t, err)
	assert.Equal(t, appliance.StateFailed, result.State)
	assert.Equal(t, "PLAN_STALE", result.ErrorCode)
	assert.Equal(t, appliance.OriginHost, result.ErrorOrigin)
	assert.NotNil(t, result.FinishedAt)
	assert.Zero(t, l.fixture.Executions(refused.ID))
}

func TestCancel(t *testing.T) {
	l := newLifecycle(t)
	ctx := context.Background()
	caller := in(admin(1), "s1")

	// Before submission: OAM's decision.
	planned := l.plan(caller, appliance.OperationBackup)
	_, err := l.service.Cancel(ctx, in(viewer(3), "s3"), planned.ID)
	assert.ErrorIs(t, err, appliance.ErrPermissionDenied)
	cancelled, err := l.service.Cancel(ctx, caller, planned.ID)
	require.NoError(t, err)
	assert.Equal(t, appliance.StateCancelled, cancelled.State)
	assert.Equal(t, appliance.CodeCancelledByUser, cancelled.ErrorCode)
	assert.NotNil(t, cancelled.FinishedAt)
	again, err := l.service.Cancel(ctx, caller, planned.ID)
	require.NoError(t, err, "cancelling a cancelled operation is not an error")
	assert.Equal(t, appliance.StateCancelled, again.State)
	_, err = l.service.Submit(ctx, caller, planned.ID, planned.PlanHash, "")
	assert.ErrorIs(t, err, appliance.ErrOperationState)

	// After submission, while the host still allows it.
	early, challenge := l.authorized(caller, appliance.OperationRestore)
	_, err = l.service.Submit(ctx, caller, early.ID, early.PlanHash, challenge.Challenge)
	require.NoError(t, err)
	require.True(t, l.follow(early.ID, 1).Cancellable)
	stopped, err := l.service.Cancel(ctx, caller, early.ID)
	require.NoError(t, err)
	assert.Equal(t, appliance.StateCancelled, stopped.State)
	assert.NotNil(t, stopped.FinishedAt)
	assert.False(t, stopped.Cancellable)

	// Past the irreversible phase the host refuses.
	late, challenge := l.authorized(caller, appliance.OperationRestore)
	_, err = l.service.Submit(ctx, caller, late.ID, late.PlanHash, challenge.Challenge)
	require.NoError(t, err)
	committed := l.follow(late.ID, 2)
	assert.Equal(t, "stop-services", committed.Phase)
	assert.False(t, committed.Cancellable)
	_, err = l.service.Cancel(ctx, caller, late.ID)
	assert.ErrorIs(t, err, appliance.ErrNotCancellable)
	assert.Equal(t, appliance.StateRunning, l.get(late.ID).State)
	require.Equal(t, appliance.StateSucceeded, l.follow(late.ID, 2).State)

	// A submission that never reached the host: nothing to stop there, and
	// nothing is delivered afterwards.
	l.host.down.Store(true)
	undelivered := l.plan(caller, appliance.OperationBackup)
	_, err = l.service.Submit(ctx, caller, undelivered.ID, undelivered.PlanHash, "")
	require.NoError(t, err)
	_, err = l.service.Cancel(ctx, caller, undelivered.ID)
	assert.ErrorIs(t, err, appliance.ErrHostUnreachable, "whether it was delivered cannot be known while the host is away")
	l.host.down.Store(false)
	gone, err := l.service.Cancel(ctx, caller, undelivered.ID)
	require.NoError(t, err)
	assert.Equal(t, appliance.StateCancelled, gone.State)
	require.NoError(t, l.service.ReconcileActive(ctx))
	assert.Zero(t, l.fixture.Executions(undelivered.ID))
}

// Cancelling a submission the host never received races the reconciler
// delivering it. Whichever gets there first, the outcome is one or the other:
// an operation reported as cancelled is never running on the host.
func TestCancelNeverRacesDelivery(t *testing.T) {
	l := newLifecycle(t)
	ctx := context.Background()
	caller := in(admin(1), "s1")

	delivered, cancelled := 0, 0
	for round := 0; round < 8; round++ {
		l.host.down.Store(true)
		op := l.plan(caller, appliance.OperationBackup)
		_, err := l.service.Submit(ctx, caller, op.ID, op.PlanHash, "")
		require.NoError(t, err)
		l.host.down.Store(false)

		// Hold the operation's row so both contenders have asked the host —
		// and been told it has no such job — before either may act on it.
		hold, err := l.db.BeginTx(ctx, nil)
		require.NoError(t, err)
		_, err = hold.Exec("SELECT 1 FROM appliance_operations WHERE id = $1 FOR UPDATE", op.ID)
		require.NoError(t, err)

		var cancelErr error
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			_, cancelErr = l.service.Cancel(context.Background(), caller, op.ID)
		}()
		go func() {
			defer wg.Done()
			_ = l.service.ReconcileActive(context.Background())
		}()
		time.Sleep(150 * time.Millisecond)
		require.NoError(t, hold.Rollback())
		wg.Wait()

		after := l.get(op.ID)
		if l.fixture.Executions(op.ID) == 0 {
			cancelled++
			require.NoError(t, cancelErr, "round %d", round)
			assert.Equal(t, appliance.StateCancelled, after.State, "round %d", round)
			require.NoError(t, l.service.ReconcileActive(ctx))
			assert.Zero(t, l.fixture.Executions(op.ID), "round %d: a cancelled operation was delivered afterwards", round)
			continue
		}
		delivered++
		assert.Equal(t, 1, l.fixture.Executions(op.ID), "round %d", round)
		assert.ErrorIs(t, cancelErr, appliance.ErrOperationState, "round %d: the cancel must not report success", round)
		assert.Equal(t, appliance.StateQueued, after.State, "round %d", round)
		assert.EqualValues(t, 1, after.HostGeneration, "round %d", round)
		// Asked again, the host — which now has it — stops it.
		stopped, err := l.service.Cancel(ctx, caller, op.ID)
		require.NoError(t, err)
		require.Equal(t, appliance.StateCancelled, stopped.State)
	}
	t.Logf("delivered first in %d rounds, cancelled first in %d", delivered, cancelled)
}

// Every event leaves one audit row naming the operation, the actor and the
// request — and nothing that could be used to repeat it.
func TestAuditTrail(t *testing.T) {
	l := newLifecycle(t)
	ctx := context.Background()
	caller := in(admin(1), "s1")

	op := l.plan(caller, appliance.OperationRestore)
	l.service.AuthorizationDenied(ctx, caller, op.ID, appliance.CodeReauthenticationFailed)
	l.service.AuthorizationDenied(ctx, caller, "not-an-operation", appliance.CodeReauthenticationFailed)
	challenge, err := l.service.Authorize(ctx, caller, op.ID)
	require.NoError(t, err)
	_, err = l.service.Submit(ctx, caller, op.ID, op.PlanHash, strings.Repeat("f", 64))
	require.ErrorIs(t, err, appliance.ErrChallengeMismatch)
	_, err = l.service.Submit(ctx, caller, op.ID, op.PlanHash, challenge.Challenge)
	require.NoError(t, err)
	require.Equal(t, appliance.StateSucceeded, l.follow(op.ID, 4).State)

	assert.Equal(t, []string{
		appliance.AuditPlan, appliance.AuditAuthorizeDenied, appliance.AuditAuthorize, appliance.AuditSubmitDenied, appliance.AuditSubmit,
		appliance.AuditStateChange, appliance.AuditStateChange, appliance.AuditStateChange, appliance.AuditStateChange,
	}, l.auditEvents(op.ID))
	assert.Equal(t, 9, l.count("SELECT COUNT(*) FROM appliance_audit"), "nothing is recorded against an ID that is not an operation's")

	// What the caller did carries who, from where and under which request;
	// what OAM read from the host carries no actor.
	assert.Equal(t, 5, l.count(`SELECT COUNT(*) FROM appliance_audit
		WHERE actor_user_id = 1 AND actor_username = 'admin1' AND request_id = 'req-s1' AND source_ip = '192.0.2.10'`))
	assert.Equal(t, 4, l.count("SELECT COUNT(*) FROM appliance_audit WHERE actor_user_id IS NULL AND event = $1", appliance.AuditStateChange))
	assert.Equal(t, 1, l.count("SELECT COUNT(*) FROM appliance_audit WHERE event = $1 AND old_state = 'AWAITING_AUTHORIZATION' AND new_state = 'QUEUED'", appliance.AuditSubmit))
	assert.Equal(t, 1, l.count("SELECT COUNT(*) FROM appliance_audit WHERE event = $1 AND detail->>'reason' = $2", appliance.AuditSubmitDenied, appliance.CodeChallengeMismatch))
	assert.Equal(t, 1, l.count("SELECT COUNT(*) FROM appliance_audit WHERE new_state = 'SUCCEEDED'"))

	// The challenge is stored nowhere: not in the audit trail, not with the
	// operation, and not in its own table, which keeps a hash.
	for _, table := range []string{"appliance_audit", "appliance_operations", "appliance_challenges"} {
		var dump sql.NullString
		require.NoError(t, l.db.QueryRow("SELECT string_agg(row_to_json(t)::text, ' ') FROM "+table+" t").Scan(&dump))
		require.True(t, dump.Valid, table)
		assert.NotContains(t, dump.String, challenge.Challenge, table)
		assert.NotContains(t, dump.String, strings.Repeat("f", 64), table)
	}
}

// The reconciler loop does what ReconcileActive does, on its own, and stops
// with its context.
func TestRunReconciler(t *testing.T) {
	l := newLifecycle(t)
	caller := in(admin(1), "s1")
	op := l.plan(caller, appliance.OperationBackup)

	l.host.down.Store(true)
	_, err := l.service.Submit(context.Background(), caller, op.ID, op.PlanHash, "")
	require.NoError(t, err)
	l.host.down.Store(false)

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		l.service.RunReconciler(ctx, 20*time.Millisecond)
		close(stopped)
	}()
	l.fixture.SetStepEvery(30 * time.Millisecond)
	require.Eventually(t, func() bool {
		seen, err := l.service.GetOperation(context.Background(), admin(1), op.ID)
		return err == nil && seen.State == appliance.StateSucceeded
	}, 10*time.Second, 25*time.Millisecond)
	assert.Equal(t, 1, l.fixture.Executions(op.ID))

	cancel()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("the reconciler did not stop with its context")
	}
}
