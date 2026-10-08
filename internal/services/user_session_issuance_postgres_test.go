package services_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"os"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	passwordUtils "github.com/loxilb-io/loxilb-oam/pkg/utils"
	"github.com/stretchr/testify/require"
)

func TestSessionIssuanceSerializedWithRecoveryPostgres(t *testing.T) {
	dsn := os.Getenv("OAM_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("OAM_TEST_DATABASE_URL is not set")
	}
	open := func() *sql.DB {
		db, err := sql.Open("pgx", dsn)
		require.NoError(t, err)
		db.SetMaxOpenConns(1)
		db.SetMaxIdleConns(1)
		return db
	}
	a, b, c := open(), open(), open()
	defer a.Close()
	defer b.Close()
	defer c.Close()
	suffix := make([]byte, 8)
	_, err := rand.Read(suffix)
	require.NoError(t, err)
	schema := "oam_session_test_" + hex.EncodeToString(suffix)
	_, err = a.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	defer func() { _, err := a.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) }()
	for _, db := range []*sql.DB{a, b, c} {
		_, err = db.Exec("SET search_path TO " + schema)
		require.NoError(t, err)
	}
	_, err = a.Exec(`CREATE TABLE users(id integer PRIMARY KEY, username text NOT NULL, password text NOT NULL,role text NOT NULL);
 CREATE TABLE api_tokens(token_value text PRIMARY KEY,user_id text NOT NULL,scopes text,expires_at timestamp NOT NULL)`)
	require.NoError(t, err)
	oldPassword, newPassword := "Initial!R7v2mQ9", "Renewed!T8n3kW5"
	oldHash, err := passwordUtils.HashPassword(oldPassword)
	require.NoError(t, err)
	_, err = a.Exec("INSERT INTO users VALUES(1,'operator',$1,'operator')", oldHash)
	require.NoError(t, err)
	sa, sb := services.NewUserService(a), services.NewUserService(b)
	_, _, valid, err := sa.ValidateUser("operator", oldPassword)
	require.NoError(t, err)
	require.True(t, valid)
	require.NoError(t, sb.UpdateUser(1, map[string]interface{}{"password": newPassword}))
	require.ErrorIs(t, sa.SaveTokenForCredentials(1, "operator", oldPassword, "stale-token"), services.ErrInvalidPassword)
	var count int
	require.NoError(t, a.QueryRow("SELECT count(*) FROM api_tokens").Scan(&count))
	require.Zero(t, count)
	require.NoError(t, sa.SaveTokenForCredentials(1, "operator", newPassword, "fresh-token"))
	_, err = a.Exec("DELETE FROM users")
	require.NoError(t, err)
	require.ErrorIs(t, sa.SaveTokenForCredentials(1, "operator", newPassword, "orphan-token"), services.ErrUserNotFound)
	// Hold token insertion after credential verification to force recovery to
	// wait. After insertion commits, recovery must delete that issued token.
	_, err = a.Exec("TRUNCATE api_tokens")
	require.NoError(t, err)
	_, err = a.Exec("INSERT INTO users VALUES(1,'operator',$1,'operator')", oldHash)
	require.NoError(t, err)
	_, err = c.Exec("SELECT pg_advisory_lock(603)")
	require.NoError(t, err)
	defer c.Exec("SELECT pg_advisory_unlock(603)")
	_, err = c.Exec(`CREATE FUNCTION block_token() RETURNS trigger LANGUAGE plpgsql AS 'BEGIN PERFORM pg_advisory_xact_lock(603); RETURN NEW; END'; CREATE TRIGGER block_token BEFORE INSERT ON api_tokens FOR EACH ROW EXECUTE FUNCTION block_token()`)
	require.NoError(t, err)
	issued, changed := make(chan error, 1), make(chan error, 1)
	go func() { issued <- sa.SaveTokenForCredentials(1, "operator", oldPassword, "ordered-token") }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		var waiting int
		require.NoError(t, c.QueryRow("SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND objid=603 AND NOT granted").Scan(&waiting))
		if waiting > 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("issuance did not reach barrier")
		}
		time.Sleep(10 * time.Millisecond)
	}
	go func() { changed <- sb.UpdateUser(1, map[string]interface{}{"password": newPassword}) }()
	select {
	case err := <-changed:
		t.Fatalf("recovery committed before issuance released: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	_, err = c.Exec("SELECT pg_advisory_unlock(603)")
	require.NoError(t, err)
	require.NoError(t, <-issued)
	require.NoError(t, <-changed)
	valid, err = sa.ValidateToken("ordered-token")
	require.NoError(t, err)
	require.False(t, valid)
	var stored string
	require.NoError(t, c.QueryRow("SELECT password FROM users WHERE id=1").Scan(&stored))
	valid, err = passwordUtils.VerifyPassword(newPassword, stored)
	require.NoError(t, err)
	require.True(t, valid)
}
