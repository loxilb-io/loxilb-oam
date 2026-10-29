// Package migrate brings the PostgreSQL schema to the version this binary was
// built for, and refuses to run against a schema it does not understand.
//
// Version 1 is the frozen baseline (database/init/00-init-complete.sql).
// Versions 2 and up are the numbered files under database/migrations/postgres.
// What has been applied is recorded in the schema_migrations table.
//
// A database is in one of three states when the server meets it:
//
//   - empty: the baseline is applied, then every later migration;
//   - created before schema_migrations existed (the PostgreSQL container ran
//     the baseline from /docker-entrypoint-initdb.d, or an operator loaded it
//     with psql): the baseline is recorded as already present — "adopted" —
//     and only later migrations run;
//   - already tracked: pending migrations run.
//
// Migrations are forward-only and each runs in its own transaction together
// with the row that records it, so a failed migration leaves no trace. A
// session-level advisory lock serializes concurrent starters.
package migrate

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Mode selects what Run may do to the database.
type Mode string

const (
	// ModeAuto applies whatever is pending. The default.
	ModeAuto Mode = "auto"
	// ModeCheck writes nothing and fails if anything is pending. For
	// deployments where something other than the server — an installer, an
	// update job — runs `loxilb-oam -migrate` under its own control.
	ModeCheck Mode = "check"
	// ModeOff skips migration and every schema check. An escape hatch: the
	// server then trusts that the schema matches.
	ModeOff Mode = "off"
)

// ModeEnv is the environment variable that selects the Mode.
const ModeEnv = "OAM_DB_MIGRATE"

// ModeFromEnv reads OAM_DB_MIGRATE. Unset means ModeAuto; an unrecognized
// value is an error rather than a silent fallback, because guessing "auto"
// for a typo of "check" would modify a database the operator asked to leave
// alone.
func ModeFromEnv() (Mode, error) {
	v := strings.ToLower(strings.TrimSpace(os.Getenv(ModeEnv)))
	switch Mode(v) {
	case "":
		return ModeAuto, nil
	case ModeAuto, ModeCheck, ModeOff:
		return Mode(v), nil
	}
	return "", fmt.Errorf("%s=%q is not one of auto, check, off", ModeEnv, os.Getenv(ModeEnv))
}

// Migration is one schema version.
type Migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string // hex SHA-256 of SQL
}

// Sentinel errors, so callers and tests can tell the refusals apart.
var (
	// ErrSchemaNewer: the database is at a version this binary has no
	// migration for — an older binary started against an upgraded database.
	ErrSchemaNewer = errors.New("database schema is newer than this binary supports")
	// ErrChecksumMismatch: an applied migration differs from the one in this
	// binary.
	ErrChecksumMismatch = errors.New("applied migration does not match this binary")
	// ErrPending: ModeCheck found migrations that have not been applied.
	ErrPending = errors.New("database schema has pending migrations")
	// ErrNotInitialized: ModeCheck found a database with no schema at all.
	ErrNotInitialized = errors.New("database schema is not initialized")
	// ErrHistoryCorrupt: schema_migrations is not a gap-free run from 1.
	ErrHistoryCorrupt = errors.New("schema_migrations history is not contiguous")
)

var fileNameRE = regexp.MustCompile(`^(\d{4})_([a-z0-9]+(?:_[a-z0-9]+)*)\.sql$`)

func checksum(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// Load assembles the full migration list: the baseline as version
// baselineVersion, followed by the NNNN_name.sql files in dir of fsys. Files
// that are not .sql (a README) are ignored; a .sql file with a malformed name,
// a duplicate version, or a gap in the numbering is an error, since any of
// them means two builds could disagree about what a version number contains.
func Load(baselineVersion int, baselineSQL string, fsys fs.FS, dir string) ([]Migration, error) {
	if strings.TrimSpace(baselineSQL) == "" {
		return nil, errors.New("baseline schema is empty")
	}
	migrations := []Migration{{
		Version:  baselineVersion,
		Name:     "baseline",
		SQL:      baselineSQL,
		Checksum: checksum(baselineSQL),
	}}

	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sql") {
			continue
		}
		m := fileNameRE.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration %q is not named NNNN_lower_snake_case.sql", e.Name())
		}
		version, _ := strconv.Atoi(m[1])
		body, err := fs.ReadFile(fsys, dir+"/"+e.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", e.Name(), err)
		}
		if strings.TrimSpace(string(body)) == "" {
			return nil, fmt.Errorf("migration %s is empty", e.Name())
		}
		migrations = append(migrations, Migration{
			Version:  version,
			Name:     m[2],
			SQL:      string(body),
			Checksum: checksum(string(body)),
		})
	}

	sort.SliceStable(migrations, func(i, j int) bool { return migrations[i].Version < migrations[j].Version })
	for i, m := range migrations {
		if want := baselineVersion + i; m.Version != want {
			return nil, fmt.Errorf("migration versions must run from %d without gaps or duplicates: found %04d (%s) where %04d was expected",
				baselineVersion+1, m.Version, m.Name, want)
		}
	}
	return migrations, nil
}

// applied is one row of schema_migrations.
type applied struct {
	Version  int
	Checksum string
}

// pending validates the recorded history against the migrations this binary
// carries and returns the ones still to apply. history must be ordered by
// version.
func pending(history []applied, migrations []Migration) ([]Migration, error) {
	first := migrations[0].Version
	latest := migrations[len(migrations)-1].Version
	for i, row := range history {
		if row.Version != first+i {
			return nil, fmt.Errorf("%w: found version %d where %d was expected", ErrHistoryCorrupt, row.Version, first+i)
		}
		if row.Version > latest {
			return nil, fmt.Errorf("%w: database is at version %d, this binary knows up to %d", ErrSchemaNewer, history[len(history)-1].Version, latest)
		}
		if m := migrations[i]; row.Checksum != m.Checksum {
			return nil, fmt.Errorf("%w: version %d (%s)", ErrChecksumMismatch, m.Version, m.Name)
		}
	}
	return migrations[len(history):], nil
}

// Status is the outcome of Run.
type Status struct {
	Mode    Mode
	Version int   // schema version after Run; 0 when ModeOff (not inspected)
	Latest  int   // newest version this binary carries
	Applied []int // versions applied by this call
	Adopted bool  // an untracked existing schema was recorded as the baseline
}

const createHistoryTable = `CREATE TABLE IF NOT EXISTS schema_migrations (
    version    INTEGER PRIMARY KEY,
    name       TEXT        NOT NULL,
    checksum   TEXT        NOT NULL,
    adopted    BOOLEAN     NOT NULL DEFAULT FALSE,
    applied_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
)`

// baselineProbeTable is a table every baseline database has. Its presence
// without a schema_migrations history identifies a database created before
// migrations were tracked.
const baselineProbeTable = "users"

// lockKey is the advisory-lock key that serializes migration runs
// ("OAMSCHEM" as a big-endian int64). Advisory locks are per database, so it
// only has to be distinct from other keys taken in OAM's own database.
const lockKey int64 = 0x4f414d5343484d45

// queryer is the read subset shared by *sql.DB and *sql.Conn.
type queryer interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

func tableExists(ctx context.Context, q queryer, name string) (bool, error) {
	var exists bool
	err := q.QueryRowContext(ctx, "SELECT to_regclass($1) IS NOT NULL", name).Scan(&exists)
	return exists, err
}

func readHistory(ctx context.Context, q queryer) ([]applied, error) {
	rows, err := q.QueryContext(ctx, "SELECT version, checksum FROM schema_migrations ORDER BY version")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := []applied{}
	for rows.Next() {
		var a applied
		if err := rows.Scan(&a.Version, &a.Checksum); err != nil {
			return nil, err
		}
		history = append(history, a)
	}
	return history, rows.Err()
}

// Run brings the database to the newest version in migrations (ModeAuto),
// verifies that it already is there (ModeCheck), or does nothing (ModeOff).
// migrations is the list returned by Load.
func Run(ctx context.Context, db *sql.DB, migrations []Migration, mode Mode) (Status, error) {
	if len(migrations) == 0 {
		return Status{}, errors.New("no migrations loaded")
	}
	status := Status{Mode: mode, Latest: migrations[len(migrations)-1].Version}
	switch mode {
	case ModeOff:
		return status, nil
	case ModeCheck:
		return check(ctx, db, migrations, status)
	case ModeAuto:
		return apply(ctx, db, migrations, status)
	}
	return status, fmt.Errorf("unknown migration mode %q", mode)
}

// check inspects without writing — it does not even create schema_migrations.
func check(ctx context.Context, db *sql.DB, migrations []Migration, status Status) (Status, error) {
	tracked, err := tableExists(ctx, db, "schema_migrations")
	if err != nil {
		return status, fmt.Errorf("inspect schema: %w", err)
	}
	history := []applied{}
	if tracked {
		if history, err = readHistory(ctx, db); err != nil {
			return status, fmt.Errorf("read schema_migrations: %w", err)
		}
	}
	if len(history) == 0 {
		hasBaseline, err := tableExists(ctx, db, baselineProbeTable)
		if err != nil {
			return status, fmt.Errorf("inspect schema: %w", err)
		}
		if !hasBaseline {
			return status, ErrNotInitialized
		}
		// An untracked baseline database: at version 1 in fact, though not
		// yet on record.
		history = []applied{{Version: migrations[0].Version, Checksum: migrations[0].Checksum}}
	}
	todo, err := pending(history, migrations)
	if err != nil {
		return status, err
	}
	status.Version = history[len(history)-1].Version
	if len(todo) > 0 {
		return status, fmt.Errorf("%w: at version %d, version %d is available — run `loxilb-oam -migrate` or set %s=auto",
			ErrPending, status.Version, status.Latest, ModeEnv)
	}
	return status, nil
}

func apply(ctx context.Context, db *sql.DB, migrations []Migration, status Status) (Status, error) {
	// The advisory lock is held by a session, so everything runs on one
	// dedicated connection rather than whichever the pool hands out.
	conn, err := db.Conn(ctx)
	if err != nil {
		return status, fmt.Errorf("acquire connection: %w", err)
	}
	defer conn.Close()

	if _, err := conn.ExecContext(ctx, "SELECT pg_advisory_lock($1)", lockKey); err != nil {
		return status, fmt.Errorf("acquire migration lock: %w", err)
	}
	// Unlock on a context that outlives ctx: if ctx was cancelled the lock
	// must still be released before the connection returns to the pool.
	defer conn.ExecContext(context.WithoutCancel(ctx), "SELECT pg_advisory_unlock($1)", lockKey)

	if _, err := conn.ExecContext(ctx, createHistoryTable); err != nil {
		return status, fmt.Errorf("create schema_migrations: %w", err)
	}
	history, err := readHistory(ctx, conn)
	if err != nil {
		return status, fmt.Errorf("read schema_migrations: %w", err)
	}

	if len(history) == 0 {
		hasBaseline, err := tableExists(ctx, conn, baselineProbeTable)
		if err != nil {
			return status, fmt.Errorf("inspect schema: %w", err)
		}
		if hasBaseline {
			base := migrations[0]
			if _, err := conn.ExecContext(ctx,
				"INSERT INTO schema_migrations (version, name, checksum, adopted) VALUES ($1, $2, $3, TRUE)",
				base.Version, base.Name, base.Checksum); err != nil {
				return status, fmt.Errorf("record existing schema as version %d: %w", base.Version, err)
			}
			history = []applied{{Version: base.Version, Checksum: base.Checksum}}
			status.Adopted = true
		}
	}

	todo, err := pending(history, migrations)
	if err != nil {
		return status, err
	}
	if len(history) > 0 {
		status.Version = history[len(history)-1].Version
	}
	for _, m := range todo {
		if err := applyOne(ctx, conn, m); err != nil {
			return status, fmt.Errorf("migration %04d (%s): %w", m.Version, m.Name, err)
		}
		status.Version = m.Version
		status.Applied = append(status.Applied, m.Version)
	}
	return status, nil
}

// applyOne runs a migration and records it in the same transaction.
func applyOne(ctx context.Context, conn *sql.Conn, m Migration) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() // no-op after Commit

	// No arguments, so pgx sends this over the simple query protocol, which
	// is what allows a file of several statements in one call.
	if _, err := tx.ExecContext(ctx, m.SQL); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT INTO schema_migrations (version, name, checksum) VALUES ($1, $2, $3)",
		m.Version, m.Name, m.Checksum); err != nil {
		return err
	}
	return tx.Commit()
}

// CurrentState is the newest applied migration.
type CurrentState struct {
	Version   int
	Name      string
	Adopted   bool
	AppliedAt time.Time
}

// Current returns the newest applied migration, or nil when the schema is not
// tracked: schema_migrations is absent or empty, as on a database that has
// only ever been run with OAM_DB_MIGRATE=off. It never writes.
func Current(ctx context.Context, db *sql.DB) (*CurrentState, error) {
	tracked, err := tableExists(ctx, db, "schema_migrations")
	if err != nil || !tracked {
		return nil, err
	}
	var state CurrentState
	err = db.QueryRowContext(ctx,
		"SELECT version, name, adopted, applied_at FROM schema_migrations ORDER BY version DESC LIMIT 1").
		Scan(&state.Version, &state.Name, &state.Adopted, &state.AppliedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &state, nil
}
