package appliance_test

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/loxilb-io/loxilb-oam/internal/appliance"
	"github.com/loxilb-io/loxilb-oam/internal/appliance/hostfixture"
)

var key = []byte("0123456789abcdef0123456789abcdef-test-key")

// startFixture serves a fixture host on a real Unix socket and returns a
// client for it, so these tests cover the transport and the request signing
// as well as the logic above them.
func startFixture(t *testing.T, clientKey []byte, available ...appliance.Action) (appliance.HostClient, *hostfixture.Host, func()) {
	t.Helper()
	// Not t.TempDir(): its path can exceed the length limit of a socket address.
	dir, err := os.MkdirTemp("", "oam-fx")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(dir) })
	socketPath := filepath.Join(dir, "h.sock")

	listener, err := net.Listen("unix", socketPath)
	require.NoError(t, err)
	fixture := hostfixture.New(key, available...)
	server := &http.Server{Handler: fixture.Handler(), ReadHeaderTimeout: time.Second}
	go server.Serve(listener) // returns ErrServerClosed on stop
	stop := func() { server.Close() }
	t.Cleanup(stop)

	client, err := appliance.NewSocketHost(socketPath, clientKey)
	require.NoError(t, err)
	return client, fixture, stop
}

func allow(actions ...appliance.Action) func(appliance.Action) bool {
	return func(a appliance.Action) bool {
		for _, allowed := range actions {
			if a == allowed {
				return true
			}
		}
		return false
	}
}

func byAction(caps appliance.Capabilities) map[appliance.Action]appliance.ActionCapability {
	out := map[appliance.Action]appliance.ActionCapability{}
	for _, c := range caps.Actions {
		out[c.Action] = c
	}
	return out
}

// A deployment that is not an Appliance: every action is reported, none is
// supported, and the reason says there is no host — while what the caller is
// permitted to ask for is still answered truthfully.
func TestCapabilitiesWithoutHost(t *testing.T) {
	service := appliance.NewService(appliance.UnconfiguredHost(), nil, "test")
	caps := service.Capabilities(context.Background(), allow(appliance.Actions...))

	assert.Equal(t, appliance.SchemaVersion, caps.SchemaVersion)
	assert.False(t, caps.HostConfigured)
	assert.False(t, caps.HostFixture)
	assert.NotNil(t, caps.HostContractVersions, "must serialize as [], not null")
	assert.Nil(t, caps.ObservedAt)
	require.Len(t, caps.Actions, len(appliance.Actions))
	for _, c := range caps.Actions {
		assert.False(t, c.Supported, c.Action)
		assert.False(t, c.Available, c.Action)
		assert.Equal(t, appliance.ReasonHostNotConfigured, c.UnavailableReason, c.Action)
		assert.True(t, c.Permitted, c.Action)
	}
}

// With a host: what it offers is available, what it does not is unsupported,
// and permission is reported independently of both.
func TestCapabilitiesWithHost(t *testing.T) {
	host, _, _ := startFixture(t, key, appliance.ActionBackup, appliance.ActionRestore)
	service := appliance.NewService(host, nil, "test")

	caps := service.Capabilities(context.Background(), allow(appliance.ActionBackup, appliance.ActionReset))
	assert.True(t, caps.HostConfigured)
	assert.True(t, caps.HostFixture, "a fixture must be labelled as one")
	assert.Equal(t, []string{appliance.SchemaVersion}, caps.HostContractVersions)
	require.NotNil(t, caps.ObservedAt)

	got := byAction(caps)
	// Offered and permitted.
	assert.Equal(t, appliance.ActionCapability{Action: appliance.ActionBackup, Supported: true, Available: true, Permitted: true}, got[appliance.ActionBackup])
	// Offered, not permitted: still reported as available.
	assert.Equal(t, appliance.ActionCapability{Action: appliance.ActionRestore, Supported: true, Available: true, RequiresReauthentication: true}, got[appliance.ActionRestore])
	// Permitted, not offered: permission does not make it available.
	assert.Equal(t, appliance.ActionCapability{Action: appliance.ActionReset, UnavailableReason: appliance.ReasonHostUnsupported, Permitted: true, RequiresReauthentication: true}, got[appliance.ActionReset])
	// Neither.
	assert.Equal(t, appliance.ActionCapability{Action: appliance.ActionUpdate, UnavailableReason: appliance.ReasonHostUnsupported, RequiresReauthentication: true}, got[appliance.ActionUpdate])

	for _, c := range caps.Actions {
		assert.Equal(t, c.Action.Destructive(), c.RequiresReauthentication, c.Action)
		assert.Equal(t, !c.Available, c.UnavailableReason != "", "%s: a reason is given exactly when unavailable", c.Action)
	}
}

// A host that speaks only other contract versions offers nothing, whatever it
// lists as available.
func TestCapabilitiesSchemaMismatch(t *testing.T) {
	host, fixture, _ := startFixture(t, key, appliance.Actions...)
	fixture.SetContractVersions("appliance-ops/v9")
	caps := appliance.NewService(host, nil, "test").Capabilities(context.Background(), allow(appliance.Actions...))

	assert.Equal(t, []string{"appliance-ops/v9"}, caps.HostContractVersions)
	for _, c := range caps.Actions {
		assert.False(t, c.Available, c.Action)
		assert.False(t, c.Supported, c.Action)
		assert.Equal(t, appliance.ReasonSchemaMismatch, c.UnavailableReason, c.Action)
	}
}

// A configured host that does not answer — stopped, or rejecting OAM's
// signature — is unreachable. It is never reported as "not configured", and
// nothing is reported as available.
func TestCapabilitiesHostUnreachable(t *testing.T) {
	t.Run("stopped", func(t *testing.T) {
		host, _, stop := startFixture(t, key, appliance.Actions...)
		stop()
		caps := appliance.NewService(host, nil, "test").Capabilities(context.Background(), allow())
		assert.True(t, caps.HostConfigured)
		for _, c := range caps.Actions {
			assert.False(t, c.Available, c.Action)
			assert.Equal(t, appliance.ReasonHostUnreachable, c.UnavailableReason, c.Action)
		}
	})
	t.Run("wrong key", func(t *testing.T) {
		host, _, _ := startFixture(t, []byte("a-different-key-a-different-key-a-different"), appliance.Actions...)
		_, err := host.Capabilities(context.Background())
		require.ErrorIs(t, err, appliance.ErrHostUnreachable)
		caps := appliance.NewService(host, nil, "test").Capabilities(context.Background(), allow())
		for _, c := range caps.Actions {
			assert.Equal(t, appliance.ReasonHostUnreachable, c.UnavailableReason, c.Action)
		}
	})
}

func TestNewSocketHostRejectsShortKey(t *testing.T) {
	_, err := appliance.NewSocketHost("/nonexistent.sock", []byte("short"))
	assert.Error(t, err)
}

func TestHostClientFromEnv(t *testing.T) {
	t.Setenv(appliance.HostSocketEnv, "")
	t.Setenv(appliance.HostKeyFileEnv, "")
	host, err := appliance.HostClientFromEnv()
	require.NoError(t, err)
	assert.False(t, host.Configured(), "nothing set: not an Appliance")

	// Half a configuration is an error, not "not an Appliance".
	t.Setenv(appliance.HostSocketEnv, "/run/x.sock")
	_, err = appliance.HostClientFromEnv()
	assert.Error(t, err)

	t.Setenv(appliance.HostKeyFileEnv, filepath.Join(t.TempDir(), "missing"))
	_, err = appliance.HostClientFromEnv()
	assert.Error(t, err, "unreadable key file")

	keyFile := filepath.Join(t.TempDir(), "key")
	require.NoError(t, os.WriteFile(keyFile, append(key, '\n'), 0o600))
	t.Setenv(appliance.HostKeyFileEnv, keyFile)
	host, err = appliance.HostClientFromEnv()
	require.NoError(t, err)
	assert.True(t, host.Configured())
}

func componentNamed(status appliance.Status, name string) *appliance.Component {
	for i := range status.Components {
		if status.Components[i].Name == name {
			return &status.Components[i]
		}
	}
	return nil
}

func TestStatusWithoutHost(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()
	applied := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	mock.ExpectPing()
	mock.ExpectQuery("SELECT to_regclass").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery("SELECT version, name, adopted, applied_at FROM schema_migrations").
		WillReturnRows(sqlmock.NewRows([]string{"version", "name", "adopted", "applied_at"}).AddRow(1, "baseline", true, applied))

	status := appliance.NewService(appliance.UnconfiguredHost(), db, "v1.2.3").Status(context.Background())

	assert.Nil(t, status.Product, "no host, no product identity")
	assert.Nil(t, componentNamed(status, "host-adapter"))
	oam := componentNamed(status, "oam")
	require.NotNil(t, oam)
	assert.Equal(t, "v1.2.3", oam.Version)
	assert.Equal(t, appliance.ReadinessReady, componentNamed(status, "database").Readiness)
	assert.Equal(t, appliance.DatabaseStatus{SchemaVersion: 1, LatestMigration: "baseline", AppliedAt: &applied, Adopted: true}, status.Database)
	assert.NoError(t, mock.ExpectationsWereMet())
}

// An unreachable database or host is unknown and stale — never ready.
func TestStatusUnknownIsNeverReady(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectPing().WillReturnError(assert.AnError)

	host, _, stop := startFixture(t, key)
	stop()
	status := appliance.NewService(host, db, "test").Status(context.Background())

	for _, name := range []string{"database", "host-adapter"} {
		c := componentNamed(status, name)
		require.NotNil(t, c, name)
		assert.Equal(t, appliance.ReadinessUnknown, c.Readiness, name)
		assert.Equal(t, appliance.LivenessUnknown, c.Liveness, name)
		assert.True(t, c.Stale, name)
		assert.Nil(t, c.ObservedAt, name)
	}
	assert.Nil(t, status.Product)
	assert.Zero(t, status.Database.SchemaVersion)
}

func TestStatusWithHost(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.MonitorPingsOption(true))
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectPing()
	// An untracked schema (OAM_DB_MIGRATE=off): version 0, not an error.
	mock.ExpectQuery("SELECT to_regclass").WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

	host, _, _ := startFixture(t, key)
	status := appliance.NewService(host, db, "test").Status(context.Background())

	require.NotNil(t, status.Product)
	assert.True(t, status.Product.Fixture)
	assert.Equal(t, "FIXTURE-INSTALLATION", status.Product.InstallationID)
	adapter := componentNamed(status, "host-adapter")
	require.NotNil(t, adapter)
	assert.Equal(t, appliance.ReadinessReady, adapter.Readiness)
	assert.False(t, adapter.Stale)
	assert.Zero(t, status.Database.SchemaVersion)
}
