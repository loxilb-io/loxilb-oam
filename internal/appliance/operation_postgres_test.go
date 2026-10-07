package appliance_test

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/loxilb-io/loxilb-oam/database"
	"github.com/loxilb-io/loxilb-oam/internal/appliance"
	"github.com/loxilb-io/loxilb-oam/internal/appliance/hostfixture"
	"github.com/loxilb-io/loxilb-oam/internal/migrate"
)

// The operation store is tested against a real PostgreSQL server: the
// idempotency guarantee is a unique constraint and the expiry rule is the
// database clock, neither of which a mock reproduces. Skipped unless
// OAM_TEST_DATABASE_URL names a server where the role may CREATE DATABASE.
func migratedDB(t *testing.T) *sql.DB {
	t.Helper()
	adminURL := os.Getenv("OAM_TEST_DATABASE_URL")
	if adminURL == "" {
		t.Skip("OAM_TEST_DATABASE_URL is not set; skipping PostgreSQL appliance tests")
	}
	admin, err := sql.Open("pgx", adminURL)
	require.NoError(t, err)
	suffix := make([]byte, 6)
	_, err = rand.Read(suffix)
	require.NoError(t, err)
	name := "oam_appliance_test_" + hex.EncodeToString(suffix)
	_, err = admin.Exec("CREATE DATABASE " + name)
	require.NoError(t, err)
	u, err := url.Parse(adminURL)
	require.NoError(t, err)
	u.Path = "/" + name
	db, err := sql.Open("pgx", u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		admin.Exec("DROP DATABASE IF EXISTS " + name + " WITH (FORCE)")
		admin.Close()
	})

	migrations, err := migrate.Load(database.BaselineVersion, database.BaselineSQL, database.Migrations, database.MigrationsDir)
	require.NoError(t, err)
	_, err = migrate.Run(context.Background(), db, migrations, migrate.ModeAuto)
	require.NoError(t, err)
	return db
}

func admin(id int) appliance.Caller {
	return appliance.Caller{UserID: id, Username: fmt.Sprintf("admin%d", id), Permitted: allow(appliance.Actions...)}
}

func viewer(id int) appliance.Caller {
	return appliance.Caller{UserID: id, Username: fmt.Sprintf("viewer%d", id), Permitted: allow()}
}

func planRequest(t appliance.OperationType) appliance.PlanRequest {
	req := appliance.PlanRequest{SchemaVersion: appliance.SchemaVersion, Type: t}
	switch t {
	case appliance.OperationRestore:
		req.ArchiveRef = "backup-1"
	case appliance.OperationUpdate:
		req.TargetReleaseRef = "v9.9.9"
	}
	return req
}

func operationCount(t *testing.T, db *sql.DB) int {
	t.Helper()
	var n int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM appliance_operations").Scan(&n))
	return n
}

func newOperationService(t *testing.T, available ...appliance.Action) (*appliance.Service, *sql.DB) {
	t.Helper()
	db := migratedDB(t)
	host, _, _ := startFixture(t, key, available...)
	return appliance.NewService(host, db, "test"), db
}

const keyA, keyB = "idempotency-key-aaaa", "idempotency-key-bbbb"

func TestPlanOperationRecordsThePlan(t *testing.T) {
	service, db := newOperationService(t, appliance.Actions...)
	ctx := context.Background()

	req := planRequest(appliance.OperationRestore)
	req.Note = "after the disk swap"
	op, created, err := service.PlanOperation(ctx, admin(1), keyA, "req-1", req)
	require.NoError(t, err)
	assert.True(t, created)

	assert.True(t, appliance.ValidOperationID(op.ID))
	assert.Equal(t, appliance.StatePlanned, op.State)
	assert.Equal(t, appliance.OperationRestore, op.Type)
	assert.Equal(t, "admin1", op.Actor)
	assert.Equal(t, "req-1", op.RequestID)
	assert.Equal(t, "after the disk swap", op.Note)
	assert.True(t, op.HostFixture, "a fixture plan must be labelled")
	assert.True(t, op.RequiresReauthentication)
	assert.False(t, op.Redacted)
	assert.NotEmpty(t, op.PlanHash)
	assert.Equal(t, "FIXTURE-INSTALLATION", op.InstallationID)
	require.NotNil(t, op.Plan)
	assert.NotEmpty(t, op.Plan.ArchiveDigest)
	assert.Equal(t, "stop-services", op.Plan.IrreversibleAfterPhase)
	assert.NotEmpty(t, op.Plan.AffectedResources)
	assert.Equal(t, appliance.ReconciliationInSync, op.Reconciliation)
	assert.Nil(t, op.SubmittedAt)
	assert.Nil(t, op.FinishedAt)
	assert.InDelta(t, appliance.PlanTTL.Seconds(), op.PlanExpiresAt.Sub(op.CreatedAt).Seconds(), 2)

	backup, _, err := service.PlanOperation(ctx, admin(1), keyB, "", planRequest(appliance.OperationBackup))
	require.NoError(t, err)
	assert.False(t, backup.RequiresReauthentication)
	assert.Equal(t, 2, operationCount(t, db))
}

// The same key and request is the same operation; the same key with another
// request is a conflict; keys are per caller.
func TestPlanOperationIdempotency(t *testing.T) {
	service, db := newOperationService(t, appliance.Actions...)
	ctx := context.Background()

	first, created, err := service.PlanOperation(ctx, admin(1), keyA, "", planRequest(appliance.OperationBackup))
	require.NoError(t, err)
	require.True(t, created)

	again, created, err := service.PlanOperation(ctx, admin(1), keyA, "", planRequest(appliance.OperationBackup))
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, first.ID, again.ID)
	assert.Equal(t, 1, operationCount(t, db))

	_, _, err = service.PlanOperation(ctx, admin(1), keyA, "", planRequest(appliance.OperationRollback))
	assert.ErrorIs(t, err, appliance.ErrIdempotencyKeyReused)
	assert.Equal(t, 1, operationCount(t, db))

	other, created, err := service.PlanOperation(ctx, admin(2), keyA, "", planRequest(appliance.OperationBackup))
	require.NoError(t, err)
	assert.True(t, created, "another user's key is another key")
	assert.NotEqual(t, first.ID, other.ID)
}

// Racing identical requests: every caller gets the same operation, one row
// exists, and exactly one call reports having created it.
func TestPlanOperationConcurrentSameKey(t *testing.T) {
	service, db := newOperationService(t, appliance.Actions...)

	const racers = 12
	ids := make([]string, racers)
	createdBy := make([]bool, racers)
	errs := make([]error, racers)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			op, created, err := service.PlanOperation(context.Background(), admin(1), keyA, "", planRequest(appliance.OperationBackup))
			errs[i] = err
			if op != nil {
				ids[i], createdBy[i] = op.ID, created
			}
		}(i)
	}
	close(start)
	wg.Wait()

	creators := 0
	for i := range errs {
		require.NoError(t, errs[i], "racer %d", i)
		assert.Equal(t, ids[0], ids[i], "racer %d got a different operation", i)
		if createdBy[i] {
			creators++
		}
	}
	assert.Equal(t, 1, creators)
	assert.Equal(t, 1, operationCount(t, db))
}

// Requests refused before or by the host leave no row behind.
func TestPlanOperationRefusals(t *testing.T) {
	service, db := newOperationService(t, appliance.ActionBackup, appliance.ActionRestore)
	ctx := context.Background()

	_, _, err := service.PlanOperation(ctx, admin(1), "short", "", planRequest(appliance.OperationBackup))
	assert.ErrorIs(t, err, appliance.ErrIdempotencyKey)

	wrongVersion := planRequest(appliance.OperationBackup)
	wrongVersion.SchemaVersion = "appliance-ops/v9"
	_, _, err = service.PlanOperation(ctx, admin(1), keyA, "", wrongVersion)
	assert.ErrorIs(t, err, appliance.ErrSchemaVersion)

	_, _, err = service.PlanOperation(ctx, admin(1), keyA, "", appliance.PlanRequest{SchemaVersion: appliance.SchemaVersion, Type: appliance.OperationRestore})
	assert.ErrorIs(t, err, appliance.ErrInvalidRequest)

	_, _, err = service.PlanOperation(ctx, viewer(3), keyA, "", planRequest(appliance.OperationBackup))
	assert.ErrorIs(t, err, appliance.ErrPermissionDenied)

	// The host does not offer reset.
	var rejection *appliance.HostRejection
	_, _, err = service.PlanOperation(ctx, admin(1), keyA, "", planRequest(appliance.OperationReset))
	require.ErrorAs(t, err, &rejection)
	assert.Equal(t, string(appliance.ReasonHostUnsupported), rejection.Code)

	// The host does not know the archive.
	missing := planRequest(appliance.OperationRestore)
	missing.ArchiveRef = hostfixture.RefMissing
	_, _, err = service.PlanOperation(ctx, admin(1), keyA, "", missing)
	require.ErrorAs(t, err, &rejection)
	assert.Equal(t, "ARCHIVE_NOT_FOUND", rejection.Code)

	assert.Zero(t, operationCount(t, db), "no refused request may leave an operation")

	// A key whose request was refused is not burned.
	_, created, err := service.PlanOperation(ctx, admin(1), keyA, "", planRequest(appliance.OperationBackup))
	require.NoError(t, err)
	assert.True(t, created)
}

func TestPlanOperationWithoutOrWithSilentHost(t *testing.T) {
	db := migratedDB(t)
	ctx := context.Background()

	_, _, err := appliance.NewService(appliance.UnconfiguredHost(), db, "test").
		PlanOperation(ctx, admin(1), keyA, "", planRequest(appliance.OperationBackup))
	assert.ErrorIs(t, err, appliance.ErrHostNotConfigured)

	host, _, stop := startFixture(t, key, appliance.Actions...)
	stop()
	_, _, err = appliance.NewService(host, db, "test").
		PlanOperation(ctx, admin(1), keyA, "", planRequest(appliance.OperationBackup))
	assert.ErrorIs(t, err, appliance.ErrHostUnreachable)
	assert.Zero(t, operationCount(t, db))
}

// A plan nobody submitted in time is cancelled, and says why.
func TestPlanExpires(t *testing.T) {
	service, db := newOperationService(t, appliance.Actions...)
	ctx := context.Background()
	op, _, err := service.PlanOperation(ctx, admin(1), keyA, "", planRequest(appliance.OperationBackup))
	require.NoError(t, err)
	fresh, _, err := service.PlanOperation(ctx, admin(1), keyB, "", planRequest(appliance.OperationBackup))
	require.NoError(t, err)

	_, err = db.Exec("UPDATE appliance_operations SET plan_expires_at = NOW() - INTERVAL '1 second' WHERE id = $1", op.ID)
	require.NoError(t, err)

	expired, err := service.GetOperation(ctx, admin(1), op.ID)
	require.NoError(t, err)
	assert.Equal(t, appliance.StateCancelled, expired.State)
	assert.Equal(t, appliance.CodePlanExpired, expired.ErrorCode)
	assert.Equal(t, appliance.OriginOAM, expired.ErrorOrigin)
	assert.NotNil(t, expired.FinishedAt)

	still, err := service.GetOperation(ctx, admin(1), fresh.ID)
	require.NoError(t, err)
	assert.Equal(t, appliance.StatePlanned, still.State)

	// Replaying the expired operation's key returns it, cancelled — it does
	// not quietly plan a new one under an old key.
	replay, created, err := service.PlanOperation(ctx, admin(1), keyA, "", planRequest(appliance.OperationBackup))
	require.NoError(t, err)
	assert.False(t, created)
	assert.Equal(t, op.ID, replay.ID)
	assert.Equal(t, appliance.StateCancelled, replay.State)
}

// A caller who could not run an operation sees that it exists and its state,
// not what it would touch.
func TestOperationVisibility(t *testing.T) {
	service, _ := newOperationService(t, appliance.Actions...)
	ctx := context.Background()
	req := planRequest(appliance.OperationRestore)
	req.Note = "sensitive context"
	op, _, err := service.PlanOperation(ctx, admin(1), keyA, "", req)
	require.NoError(t, err)

	seen, err := service.GetOperation(ctx, viewer(9), op.ID)
	require.NoError(t, err)
	assert.True(t, seen.Redacted)
	assert.Nil(t, seen.Plan)
	assert.Empty(t, seen.PlanHash)
	assert.Empty(t, seen.Note)
	assert.Equal(t, appliance.StatePlanned, seen.State)
	assert.Equal(t, op.ID, seen.ID)

	list, err := service.ListOperations(ctx, viewer(9), appliance.ListFilter{})
	require.NoError(t, err)
	require.Len(t, list.Items, 1)
	assert.True(t, list.Items[0].Redacted)

	// Permission is per type: backup-only sees backups in full, not restores.
	backupOnly := appliance.Caller{UserID: 5, Username: "b", Permitted: allow(appliance.ActionBackup)}
	seen, err = service.GetOperation(ctx, backupOnly, op.ID)
	require.NoError(t, err)
	assert.True(t, seen.Redacted)

	for _, id := range []string{"not-a-uuid", "00000000-0000-7000-8000-000000000000"} {
		_, err = service.GetOperation(ctx, admin(1), id)
		assert.ErrorIs(t, err, appliance.ErrOperationNotFound, id)
	}
}

func TestListOperations(t *testing.T) {
	service, _ := newOperationService(t, appliance.Actions...)
	ctx := context.Background()

	empty, err := service.ListOperations(ctx, admin(1), appliance.ListFilter{})
	require.NoError(t, err)
	assert.NotNil(t, empty.Items, "must serialize as [], not null")
	assert.Empty(t, empty.Items)
	assert.Empty(t, empty.NextCursor)

	types := []appliance.OperationType{appliance.OperationBackup, appliance.OperationRestore, appliance.OperationBackup, appliance.OperationUpdate, appliance.OperationBackup}
	var ids []string
	for i, typ := range types {
		op, _, err := service.PlanOperation(ctx, admin(1), fmt.Sprintf("list-key-%012d", i), "", planRequest(typ))
		require.NoError(t, err)
		ids = append(ids, op.ID)
		time.Sleep(2 * time.Millisecond) // distinct creation milliseconds
	}

	// Newest first, two per page, no item repeated or skipped.
	var seen []string
	cursor := ""
	for page := 0; page < 5; page++ {
		list, err := service.ListOperations(ctx, admin(1), appliance.ListFilter{Limit: 2, Cursor: cursor})
		require.NoError(t, err)
		for _, op := range list.Items {
			seen = append(seen, op.ID)
		}
		if cursor = list.NextCursor; cursor == "" {
			break
		}
	}
	want := []string{ids[4], ids[3], ids[2], ids[1], ids[0]}
	assert.Equal(t, want, seen)

	backups, err := service.ListOperations(ctx, admin(1), appliance.ListFilter{Type: appliance.OperationBackup})
	require.NoError(t, err)
	assert.Len(t, backups.Items, 3)

	none, err := service.ListOperations(ctx, admin(1), appliance.ListFilter{State: appliance.StateRunning})
	require.NoError(t, err)
	assert.Empty(t, none.Items)

	for name, bad := range map[string]appliance.ListFilter{
		"state":  {State: "DONE"},
		"type":   {Type: "reboot"},
		"cursor": {Cursor: "x"},
		"limit":  {Limit: 101},
		"neg":    {Limit: -1},
	} {
		_, err := service.ListOperations(ctx, admin(1), bad)
		assert.ErrorIs(t, err, appliance.ErrInvalidFilter, name)
	}
}
