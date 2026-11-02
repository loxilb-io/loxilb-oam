package main

import (
	"context"
	"encoding/json"
	"flag"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Runs real main in a subprocess so process supervision is tested rather than
// merely asserting that a configuration validator returns an error.
func TestStartupSupervisionHelper(_ *testing.T) {
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

// These refusals come before any database connection, so no DSN is needed.
func TestStartupRefusesPlaceholderSecrets(t *testing.T) {
	const leakedComment = "# e.g. `openssl rand -base64 48`"
	for _, tc := range []struct {
		name, variable, value, want string
	}{
		{"jwt-comment", "OAM_JWT_SECRET", leakedComment, "OAM_JWT_SECRET"},
		{"jwt-blank", "OAM_JWT_SECRET", "   ", "OAM_JWT_SECRET"},
		{"admin-comment", "OAM_DEFAULT_ADMIN_PASSWORD", "# bootstrap admin; change after first login.", "OAM_DEFAULT_ADMIN_PASSWORD"},
		{"admin-placeholder", "OAM_DEFAULT_ADMIN_PASSWORD", "CHANGE_ME", "OAM_DEFAULT_ADMIN_PASSWORD"},
		{"gateway-token-placeholder", "OAM_GATEWAY_SERVICE_TOKEN", "CHANGE_ME", "OAM_GATEWAY_SERVICE_TOKEN"},
		{"database-placeholder", "DB_PASSWORD", "CHANGE_ME", "database password"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			logOffset := startupLogSize()
			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestStartupSupervisionHelper$")
			cmd.Dir = t.TempDir()
			cmd.Env = append(os.Environ(),
				"OAM_STARTUP_TEST_HELPER=1", "OAM_STARTUP_TEST_ARGUMENTS=[]",
				"DB_HOST=127.0.0.1", "DB_PORT=1", "DB_USER=oamuser", "DB_PASSWORD=startup-test-db-password",
				"OAM_RESERVED_ENDPOINTS=", "OAM_GATEWAY_AUTH_MODE=disabled", "OAM_GATEWAY_SERVICE_TOKEN=",
				"OAM_JWT_SECRET=startup-test-only-secret-32-bytes-long",
				"OAM_DEFAULT_ADMIN_PASSWORD=Valid!Oam7462",
				tc.variable+"="+tc.value)
			output, err := cmd.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal("startup on a placeholder secret stayed alive")
			}
			exit, ok := err.(*exec.ExitError)
			if !ok || exit.ExitCode() != 1 {
				t.Fatalf("expected exit1, got %v", err)
			}
			logged := string(output) + startupLogSince(logOffset)
			if !strings.Contains(logged, "SECURITY") || !strings.Contains(logged, tc.want) {
				t.Fatalf("refusal does not name %q:\n%s", tc.want, logged)
			}
			if strings.TrimSpace(tc.value) != "" && strings.Contains(logged, tc.value) {
				t.Fatal("refusal logged the secret value")
			}
		})
	}
}

// utils logs to this file when it can open it, and to stderr otherwise.
const startupLogFile = "/var/log/loxioam.log"

func startupLogSize() int64 {
	info, err := os.Stat(startupLogFile)
	if err != nil {
		return 0
	}
	return info.Size()
}

func startupLogSince(offset int64) string {
	content, err := os.ReadFile(startupLogFile)
	if err != nil || int64(len(content)) < offset {
		return ""
	}
	return string(content[offset:])
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
