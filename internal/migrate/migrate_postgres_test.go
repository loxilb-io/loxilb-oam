package migrate

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"net/url"
	"os"
	"sync"
	"testing"
	"testing/fstest"

	_ "github.com/jackc/pgx/v5/stdlib" // registers the "pgx" driver
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/loxilb-io/loxilb-oam/database"
)

// These tests run against a real PostgreSQL server, because what they verify —
// multi-statement files, transactional DDL, advisory locking — is exactly what
// a mock cannot tell us. They are skipped unless OAM_TEST_DATABASE_URL names a
// server on which the connecting role may CREATE DATABASE; each test works in
// a scratch database of its own and drops it afterwards.
const testDatabaseURLEnv = "OAM_TEST_DATABASE_URL"

func scratchDB(t *testing.T) *sql.DB {
	t.Helper()
	adminURL := os.Getenv(testDatabaseURLEnv)
	if adminURL == "" {
		t.Skip(testDatabaseURLEnv + " is not set; skipping PostgreSQL migration tests")
	}
	admin, err := sql.Open("pgx", adminURL)
	require.NoError(t, err)

	suffix := make([]byte, 6)
	_, err = rand.Read(suffix)
	require.NoError(t, err)
	name := "oam_migrate_test_" + hex.EncodeToString(suffix)
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
	return db
}

func embedded(t *testing.T) []Migration {
	t.Helper()
	migrations, err := Load(database.BaselineVersion, database.BaselineSQL, database.Migrations, database.MigrationsDir)
	require.NoError(t, err)
	return migrations
}

// withExtra appends synthetic migrations after the embedded ones.
func withExtra(t *testing.T, extra map[string]string) []Migration {
	t.Helper()
	fsys := fstest.MapFS{}
	for name, body := range extra {
		fsys["m/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	base := embedded(t)
	last := base[len(base)-1]
	// Load numbers the extra files on from the last embedded version; its
	// first element is that version again, which base already holds.
	all, err := Load(last.Version, last.SQL, fsys, "m")
	require.NoError(t, err)
	return append(append([]Migration{}, base...), all[1:]...)
}

type historyRow struct {
	Version int
	Name    string
	Adopted bool
}

func history(t *testing.T, db *sql.DB) []historyRow {
	t.Helper()
	rows, err := db.Query("SELECT version, name, adopted FROM schema_migrations ORDER BY version")
	require.NoError(t, err)
	defer rows.Close()
	var out []historyRow
	for rows.Next() {
		var r historyRow
		require.NoError(t, rows.Scan(&r.Version, &r.Name, &r.Adopted))
		out = append(out, r)
	}
	require.NoError(t, rows.Err())
	return out
}

func hasTable(t *testing.T, db *sql.DB, name string) bool {
	t.Helper()
	ok, err := tableExists(context.Background(), db, name)
	require.NoError(t, err)
	return ok
}

// An empty database is brought to the latest version by the server itself.
func TestPostgresEmptyDatabaseGetsBaseline(t *testing.T) {
	db := scratchDB(t)
	migrations := embedded(t)
	latest := migrations[len(migrations)-1].Version

	status, err := Run(t.Context(), db, migrations, ModeAuto)
	require.NoError(t, err)
	assert.Equal(t, latest, status.Version)
	assert.False(t, status.Adopted)
	assert.Len(t, status.Applied, len(migrations))

	for _, table := range []string{"users", "api_tokens", "loxilb_instances", "instance_snapshots", "system_settings"} {
		assert.True(t, hasTable(t, db, table), table)
	}
	rows := history(t, db)
	require.Len(t, rows, len(migrations))
	assert.Equal(t, historyRow{Version: 1, Name: "baseline", Adopted: false}, rows[0])

	// A second run is a no-op.
	status, err = Run(t.Context(), db, migrations, ModeAuto)
	require.NoError(t, err)
	assert.Empty(t, status.Applied)
	assert.Equal(t, latest, status.Version)
	assert.Len(t, history(t, db), len(migrations))
}

// A database the PostgreSQL container initialized from the baseline file,
// before migrations were tracked, is adopted — not re-created — and keeps its
// data.
func TestPostgresExistingDatabaseIsAdopted(t *testing.T) {
	db := scratchDB(t)
	_, err := db.Exec(database.BaselineSQL)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO users (username, email, password, role) VALUES ('keep', 'keep@test.local', 'x', 'viewer')`)
	require.NoError(t, err)
	require.False(t, hasTable(t, db, "schema_migrations"))

	migrations := embedded(t)
	status, err := Run(t.Context(), db, migrations, ModeAuto)
	require.NoError(t, err)
	assert.True(t, status.Adopted)

	rows := history(t, db)
	require.NotEmpty(t, rows)
	assert.Equal(t, historyRow{Version: 1, Name: "baseline", Adopted: true}, rows[0])

	var n int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM users WHERE username = 'keep'`).Scan(&n))
	assert.Equal(t, 1, n, "adoption must not touch existing rows")

	status, err = Run(t.Context(), db, migrations, ModeAuto)
	require.NoError(t, err)
	assert.False(t, status.Adopted, "adoption happens once")
}

// Later migrations apply in order on top of an adopted or fresh baseline, and
// only the missing ones run on the next start.
func TestPostgresAppliesLaterMigrationsIncrementally(t *testing.T) {
	db := scratchDB(t)
	base := embedded(t)
	next := base[len(base)-1].Version + 1

	_, err := Run(t.Context(), db, base, ModeAuto)
	require.NoError(t, err)

	one := withExtra(t, map[string]string{
		fileName(next, "add_widgets"): "CREATE TABLE widgets (id INTEGER PRIMARY KEY);\nINSERT INTO widgets VALUES (1);",
	})
	status, err := Run(t.Context(), db, one, ModeAuto)
	require.NoError(t, err)
	assert.Equal(t, []int{next}, status.Applied)
	assert.True(t, hasTable(t, db, "widgets"))

	two := withExtra(t, map[string]string{
		fileName(next, "add_widgets"):     "CREATE TABLE widgets (id INTEGER PRIMARY KEY);\nINSERT INTO widgets VALUES (1);",
		fileName(next+1, "widgets_label"): "ALTER TABLE widgets ADD COLUMN label TEXT NOT NULL DEFAULT '';",
	})
	status, err = Run(t.Context(), db, two, ModeAuto)
	require.NoError(t, err)
	assert.Equal(t, []int{next + 1}, status.Applied)
	assert.Equal(t, next+1, status.Version)

	// The older binary now refuses this database.
	_, err = Run(t.Context(), db, one, ModeAuto)
	assert.ErrorIs(t, err, ErrSchemaNewer)
	_, err = Run(t.Context(), db, one, ModeCheck)
	assert.ErrorIs(t, err, ErrSchemaNewer)

	// And an edited copy of an applied migration is refused too.
	edited := withExtra(t, map[string]string{
		fileName(next, "add_widgets"):     "CREATE TABLE widgets (id BIGINT PRIMARY KEY);",
		fileName(next+1, "widgets_label"): "ALTER TABLE widgets ADD COLUMN label TEXT NOT NULL DEFAULT '';",
	})
	_, err = Run(t.Context(), db, edited, ModeAuto)
	assert.ErrorIs(t, err, ErrChecksumMismatch)
}

// A migration that fails part-way leaves nothing behind: neither the objects
// its earlier statements created nor a history row.
func TestPostgresFailedMigrationRollsBack(t *testing.T) {
	db := scratchDB(t)
	base := embedded(t)
	next := base[len(base)-1].Version + 1
	_, err := Run(t.Context(), db, base, ModeAuto)
	require.NoError(t, err)

	broken := withExtra(t, map[string]string{
		fileName(next, "half_done"): "CREATE TABLE half_done (id INTEGER PRIMARY KEY);\nSELECT * FROM no_such_table;",
	})
	status, err := Run(t.Context(), db, broken, ModeAuto)
	require.Error(t, err)
	assert.Empty(t, status.Applied)
	assert.Equal(t, next-1, status.Version)
	assert.False(t, hasTable(t, db, "half_done"))
	assert.Len(t, history(t, db), len(base))

	// The lock was released: a corrected build proceeds.
	fixed := withExtra(t, map[string]string{
		fileName(next, "half_done"): "CREATE TABLE half_done (id INTEGER PRIMARY KEY);",
	})
	_, err = Run(t.Context(), db, fixed, ModeAuto)
	require.NoError(t, err)
	assert.True(t, hasTable(t, db, "half_done"))
}

// Check mode reports and never writes.
func TestPostgresCheckModeNeverWrites(t *testing.T) {
	db := scratchDB(t)
	base := embedded(t)
	next := base[len(base)-1].Version + 1

	_, err := Run(t.Context(), db, base, ModeCheck)
	assert.ErrorIs(t, err, ErrNotInitialized)
	assert.False(t, hasTable(t, db, "schema_migrations"))
	assert.False(t, hasTable(t, db, "users"))

	// An untracked baseline database satisfies a baseline-only binary without
	// being adopted...
	_, err = db.Exec(database.BaselineSQL)
	require.NoError(t, err)
	if len(base) == 1 {
		status, err := Run(t.Context(), db, base, ModeCheck)
		require.NoError(t, err)
		assert.Equal(t, 1, status.Version)
	}
	assert.False(t, hasTable(t, db, "schema_migrations"), "check mode must not create the history table")

	// ...and is reported as behind when the binary carries more.
	ahead := withExtra(t, map[string]string{fileName(next, "more"): "CREATE TABLE more (id INTEGER);"})
	_, err = Run(t.Context(), db, ahead, ModeCheck)
	assert.ErrorIs(t, err, ErrPending)
	assert.False(t, hasTable(t, db, "more"))

	// Off does not look at all.
	status, err := Run(t.Context(), db, ahead, ModeOff)
	require.NoError(t, err)
	assert.Zero(t, status.Version)
	assert.False(t, hasTable(t, db, "schema_migrations"))

	// Once applied, check passes.
	_, err = Run(t.Context(), db, ahead, ModeAuto)
	require.NoError(t, err)
	status, err = Run(t.Context(), db, ahead, ModeCheck)
	require.NoError(t, err)
	assert.Equal(t, next, status.Version)
}

// Several servers starting at once against one empty database: every one
// succeeds and each migration is applied exactly once.
func TestPostgresConcurrentStartersApplyOnce(t *testing.T) {
	db := scratchDB(t)
	base := embedded(t)
	next := base[len(base)-1].Version + 1
	// A non-idempotent statement: a second application would fail.
	migrations := withExtra(t, map[string]string{fileName(next, "once"): "CREATE TABLE once (id INTEGER PRIMARY KEY);\nINSERT INTO once VALUES (1);"})

	const starters = 8
	errs := make([]error, starters)
	applied := make([]int, starters)
	var wg sync.WaitGroup
	for i := 0; i < starters; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			status, err := Run(context.Background(), db, migrations, ModeAuto)
			errs[i] = err
			applied[i] = len(status.Applied)
		}(i)
	}
	wg.Wait()

	total := 0
	for i := range errs {
		require.NoError(t, errs[i], "starter %d", i)
		total += applied[i]
	}
	assert.Equal(t, len(migrations), total, "each migration applied by exactly one starter")
	assert.Len(t, history(t, db), len(migrations))
}
