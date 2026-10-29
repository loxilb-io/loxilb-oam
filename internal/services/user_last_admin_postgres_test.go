package services_test

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"os"
	"sync"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/stretchr/testify/require"
)

// Different connections represent separate OAM processes; a process mutex
// cannot preserve administrator membership across them.
func TestLastAdminMembershipPostgres(t *testing.T) {
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
	a, b := open(), open()
	defer a.Close()
	defer b.Close()
	suffix := make([]byte, 8)
	_, err := rand.Read(suffix)
	require.NoError(t, err)
	schema := "oam_admin_test_" + hex.EncodeToString(suffix)
	_, err = a.Exec("CREATE SCHEMA " + schema)
	require.NoError(t, err)
	defer func() { _, err := a.Exec("DROP SCHEMA " + schema + " CASCADE"); require.NoError(t, err) }()
	for _, db := range []*sql.DB{a, b} {
		_, err = db.Exec("SET search_path TO " + schema)
		require.NoError(t, err)
	}
	_, err = a.Exec("CREATE TABLE users (id integer PRIMARY KEY, username text NOT NULL, role text NOT NULL)")
	require.NoError(t, err)
	sa, sb := services.NewUserService(a), services.NewUserService(b)
	_, err = a.Exec("INSERT INTO users VALUES (1,'sole','admin')")
	require.NoError(t, err)
	require.ErrorIs(t, sa.UpdateUser(1, map[string]interface{}{"role": "viewer"}), services.ErrAdminDeletion)
	require.ErrorIs(t, sa.DeleteUser("1"), services.ErrAdminDeletion)
	require.NoError(t, sa.UpdateUser(1, map[string]interface{}{"role": "admin"}))
	for _, kind := range []string{"delete", "demote", "mixed"} {
		t.Run(kind, func(t *testing.T) {
			for round := 0; round < 20; round++ {
				_, err := a.Exec("TRUNCATE users; INSERT INTO users VALUES (1,'one','admin'),(2,'two','admin')")
				require.NoError(t, err)
				start := make(chan struct{})
				results := make(chan error, 2)
				var wg sync.WaitGroup
				for i, svc := range []*services.UserService{sa, sb} {
					wg.Add(1)
					go func(i int, svc *services.UserService) {
						defer wg.Done()
						<-start
						if kind == "delete" || kind == "mixed" && i == 0 {
							results <- svc.DeleteUser([]string{"1", "2"}[i])
						} else {
							results <- svc.UpdateUser(i+1, map[string]interface{}{"role": "viewer"})
						}
					}(i, svc)
				}
				close(start)
				wg.Wait()
				close(results)
				successes, denied := 0, 0
				for err := range results {
					if err == nil {
						successes++
					} else if errors.Is(err, services.ErrAdminDeletion) {
						denied++
					} else {
						t.Fatalf("round %d: %v", round, err)
					}
				}
				require.Equal(t, 1, successes)
				require.Equal(t, 1, denied)
				var count int
				require.NoError(t, a.QueryRow("SELECT count(*) FROM users WHERE role='admin'").Scan(&count))
				require.Equal(t, 1, count)
			}
		})
	}
}
