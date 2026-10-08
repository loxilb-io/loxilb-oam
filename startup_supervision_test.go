package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/url"
	"os"
	"os/exec"
	"testing"
	"time"
)

// Runs real main in a subprocess so process supervision is tested rather than
// merely asserting that a configuration validator returns an error.
func TestStartupSupervisionHelper(t *testing.T) {
	if os.Getenv("OAM_STARTUP_TEST_HELPER") != "1" {
		return
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("OAM_STARTUP_TEST_ARGUMENTS")), &args); err != nil {
		os.Exit(99)
	}
	flag.CommandLine = flag.NewFlagSet("loxilb-oam", flag.ExitOnError)
	os.Args = append([]string{"loxilb-oam"}, args...)
	main()
	os.Exit(0)
}

func TestStartupFailuresReportNonzeroExit(t *testing.T) {
	dsn := os.Getenv("OAM_STARTUP_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("requires a dedicated startup-test PostgreSQL database")
	}
	u, err := url.Parse(dsn)
	if err != nil || u.User == nil || u.Hostname() == "" {
		t.Fatal("invalid startup-test DSN")
	}
	pw, _ := u.User.Password()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	for _, tc := range []struct {
		name string
		env  []string
		args []string
	}{
		{"snapshot-key", []string{"SNAPSHOT_ENC_KEY=invalid!"}, nil},
		{"gateway-identity", []string{"OAM_GATEWAY_AUTH_MODE=invalid"}, nil},
		{"token-lifetime", nil, []string{"-token-expiration=0"}},
		{"http-listener", nil, []string{"-port=invalid"}},
		{"https-keypair", nil, []string{"-port=0", "-enable-https", "-ssl-cert-file=/nonexistent-startup-test.crt", "-ssl-key-file=/nonexistent-startup-test.key"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			args, _ := json.Marshal(tc.args)
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartupSupervisionHelper$")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(),
				"OAM_STARTUP_TEST_HELPER=1", "OAM_STARTUP_TEST_ARGUMENTS="+string(args),
				"DB_HOST="+u.Hostname(), "DB_PORT="+port, "DB_USER="+u.User.Username(),
				"DB_PASSWORD="+pw, "DB_NAME="+u.Path[1:], "OAM_DB_MIGRATE=auto",
				"OAM_GATEWAY_AUTH_MODE=disabled", "OAM_RESERVED_ENDPOINTS=",
				"OAM_JWT_SECRET=startup-test-only-secret-32-bytes-long",
				"OAM_DEFAULT_ADMIN_PASSWORD=Valid!Oam7462",
				"SNAPSHOT_ENC_KEY=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=")
			cmd.Env = append(cmd.Env, tc.env...)
			err := cmd.Run()
			if ctx.Err() != nil {
				t.Fatal("failed startup stayed alive instead of reporting failure")
			}
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("expected failed startup exit1, got %v", err)
			}
		})
	}
}
