package services_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/stretchr/testify/require"
)

// A SQL mock cannot distinguish a retained token from a token whose owner was
// deleted. Exercise the actual store query against PostgreSQL in a scratch
// schema; OAM_TEST_DATABASE_URL is the same opt-in used by other package tests.
func TestStoredTokenRequiresExistingUserPostgres(t *testing.T) {
	dsn := os.Getenv("OAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OAM_TEST_DATABASE_URL is not set")
	}
	db, err := sql.Open("pgx", dsn)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	suffix := make([]byte, 8)
	_, err = rand.Read(suffix)
	require.NoError(t, err)
	schema := "oam_token_test_" + hex.EncodeToString(suffix)
	_, err = db.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := db.Exec("DROP SCHEMA " + schema + " CASCADE")
		if cleanupErr != nil {
			t.Errorf("drop scratch schema: %v", cleanupErr)
		}
		db.Close()
	})
	_, err = db.Exec("SET search_path TO " + schema)
	require.NoError(t, err)
	_, err = db.Exec(`CREATE TABLE users (id integer PRIMARY KEY, role text NOT NULL);
		CREATE TABLE api_tokens (token_value text UNIQUE NOT NULL, user_id varchar(255) NOT NULL, expires_at timestamp NOT NULL);
		INSERT INTO users VALUES (1, 'viewer'), (2, 'admin');
		INSERT INTO api_tokens VALUES
		('deleted-user-session', '1', NOW() + INTERVAL '1 hour'),
		('unaffected-admin-session', '2', NOW() + INTERVAL '1 hour'),
		('expired-admin-session', '2', NOW() - INTERVAL '1 hour')`)
	require.NoError(t, err)
	svc := services.NewUserService(db)
	check := func(token string, want bool) {
		t.Helper()
		valid, checkErr := svc.ValidateToken(token)
		require.NoError(t, checkErr)
		require.Equal(t, want, valid, "token %s", token)
	}
	check("deleted-user-session", true)
	check("unaffected-admin-session", true)
	check("expired-admin-session", false)
	require.NoError(t, svc.DeleteUser("1"))
	check("deleted-user-session", false)
	check("unaffected-admin-session", true)
	// Cover an orphan that predates the fix or is issued after deletion by an
	// already-running login. Reject it without casting arbitrary text to int.
	_, err = db.Exec(`INSERT INTO api_tokens VALUES
		('late-orphan', '1', NOW() + INTERVAL '1 hour'),
		('non-numeric-owner', 'missing-user', NOW() + INTERVAL '1 hour')`)
	require.NoError(t, err)
	check("late-orphan", false)
	check("non-numeric-owner", false)
}
