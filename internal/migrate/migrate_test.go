package migrate

import (
	"errors"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/loxilb-io/loxilb-oam/database"
)

// The migrations compiled into the binary must always load: a malformed file
// name would otherwise only surface when a server tried to start.
func TestEmbeddedMigrationsLoad(t *testing.T) {
	migrations, err := Load(database.BaselineVersion, database.BaselineSQL, database.Migrations, database.MigrationsDir)
	require.NoError(t, err)
	require.NotEmpty(t, migrations)
	assert.Equal(t, 1, migrations[0].Version)
	assert.Equal(t, "baseline", migrations[0].Name)
}

func TestLoadOrdersAndIgnoresNonSQL(t *testing.T) {
	fsys := fstest.MapFS{
		"m/0003_third.sql":  {Data: []byte("SELECT 3;")},
		"m/0002_second.sql": {Data: []byte("SELECT 2;")},
		"m/README.md":       {Data: []byte("not a migration")},
	}
	migrations, err := Load(1, "SELECT 1;", fsys, "m")
	require.NoError(t, err)
	require.Len(t, migrations, 3)
	assert.Equal(t, []int{1, 2, 3}, []int{migrations[0].Version, migrations[1].Version, migrations[2].Version})
	assert.Equal(t, "second", migrations[1].Name)
	assert.Equal(t, checksum("SELECT 2;"), migrations[1].Checksum)
}

func TestLoadRejectsBadSets(t *testing.T) {
	cases := map[string]fstest.MapFS{
		"gap":            {"m/0003_x.sql": {Data: []byte("SELECT 1;")}},
		"duplicate":      {"m/0002_a.sql": {Data: []byte("SELECT 1;")}, "m/0002_b.sql": {Data: []byte("SELECT 1;")}},
		"reuses base":    {"m/0001_x.sql": {Data: []byte("SELECT 1;")}},
		"bad name":       {"m/2_x.sql": {Data: []byte("SELECT 1;")}},
		"uppercase name": {"m/0002_AddThing.sql": {Data: []byte("SELECT 1;")}},
		"empty file":     {"m/0002_x.sql": {Data: []byte("  \n")}},
	}
	for name, fsys := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := Load(1, "SELECT 1;", fsys, "m")
			assert.Error(t, err)
		})
	}

	_, err := Load(1, "   ", fstest.MapFS{"m/README.md": {Data: []byte("x")}}, "m")
	assert.Error(t, err, "empty baseline")
}

func testMigrations(n int) []Migration {
	out := make([]Migration, n)
	for i := range out {
		sql := "SELECT " + string(rune('1'+i)) + ";"
		out[i] = Migration{Version: i + 1, Name: "m", SQL: sql, Checksum: checksum(sql)}
	}
	return out
}

func historyOf(migrations []Migration) []applied {
	out := make([]applied, len(migrations))
	for i, m := range migrations {
		out[i] = applied{Version: m.Version, Checksum: m.Checksum}
	}
	return out
}

func TestPending(t *testing.T) {
	migrations := testMigrations(3)

	todo, err := pending(nil, migrations)
	require.NoError(t, err)
	assert.Len(t, todo, 3, "empty history: everything is pending")

	todo, err = pending(historyOf(migrations[:1]), migrations)
	require.NoError(t, err)
	require.Len(t, todo, 2)
	assert.Equal(t, 2, todo[0].Version)

	todo, err = pending(historyOf(migrations), migrations)
	require.NoError(t, err)
	assert.Empty(t, todo, "fully applied: nothing pending")
}

// An older binary must refuse a database a newer release has migrated.
func TestPendingRefusesNewerSchema(t *testing.T) {
	newer := testMigrations(4)
	_, err := pending(historyOf(newer), newer[:3])
	assert.ErrorIs(t, err, ErrSchemaNewer)
}

// A migration edited after it was applied is refused, not silently accepted.
func TestPendingRefusesChangedMigration(t *testing.T) {
	migrations := testMigrations(2)
	history := historyOf(migrations)
	history[1].Checksum = checksum("something else")
	_, err := pending(history, migrations)
	assert.ErrorIs(t, err, ErrChecksumMismatch)
}

func TestPendingRefusesGappedHistory(t *testing.T) {
	migrations := testMigrations(3)
	history := historyOf(migrations)
	_, err := pending([]applied{history[0], history[2]}, migrations)
	assert.ErrorIs(t, err, ErrHistoryCorrupt)

	_, err = pending([]applied{history[1]}, migrations)
	assert.ErrorIs(t, err, ErrHistoryCorrupt, "history must start at the baseline")
}

func TestModeFromEnv(t *testing.T) {
	for value, want := range map[string]Mode{"": ModeAuto, "auto": ModeAuto, " Check ": ModeCheck, "OFF": ModeOff} {
		t.Setenv(ModeEnv, value)
		got, err := ModeFromEnv()
		require.NoError(t, err, value)
		assert.Equal(t, want, got, value)
	}

	// A typo must not fall back to "auto" and modify the database.
	t.Setenv(ModeEnv, "chek")
	_, err := ModeFromEnv()
	assert.Error(t, err)
}

func TestRunRejectsEmptyMigrationList(t *testing.T) {
	_, err := Run(t.Context(), nil, nil, ModeAuto)
	assert.Error(t, err)
	assert.False(t, errors.Is(err, ErrPending))
}

// fileName builds a migration file name for a version.
func fileName(version int, name string) string {
	return string([]byte{byte('0' + version/1000%10), byte('0' + version/100%10), byte('0' + version/10%10), byte('0' + version%10)}) + "_" + name + ".sql"
}
