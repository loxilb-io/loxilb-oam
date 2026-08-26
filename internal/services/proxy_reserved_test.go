package services

import (
	"errors"
	"strings"
	"testing"

	"github.com/loxilb-io/loxilb-oam/internal/config"
)

// lbRule builds a minimal LoxiLB load-balancer rule body.
func lbRule(externalIP, host, port, proto string) []byte {
	hostField := ""
	if host != "" {
		hostField = `"host": "` + host + `", `
	}
	return []byte(`{"serviceArguments": {"externalIP": "` + externalIP + `", ` + hostField +
		`"port": ` + port + `, "protocol": "` + proto + `", "sel": 8, "mode": 4}, "endpoints": []}`)
}

func reservedList(t *testing.T, v string) []config.ReservedEndpoint {
	t.Helper()
	list, err := config.ParseReservedEndpoints(v)
	if err != nil {
		t.Fatalf("ParseReservedEndpoints(%q): %v", v, err)
	}
	return list
}

func TestCheckReservedEndpoint(t *testing.T) {
	// The converged single-node default: the management edge on the host's
	// only address.
	const edge = "192.168.0.8:8443"
	const lbPath = "/v1/config/loadbalancer"

	tests := []struct {
		name     string
		reserved string
		method   string
		path     string
		body     []byte
		wantErr  bool
	}{
		{
			name:     "rule on the edge endpoint is refused",
			reserved: edge,
			method:   "POST",
			path:     lbPath,
			body:     lbRule("192.168.0.8", "192.168.0.8", "8443", "tcp"),
			wantErr:  true,
		},
		{
			name:     "rule on a different port is allowed",
			reserved: edge,
			method:   "POST",
			path:     lbPath,
			body:     lbRule("192.168.0.8", "192.168.0.8", "8080", "tcp"),
		},
		{
			name:     "rule on a different VIP address is allowed",
			reserved: edge,
			method:   "POST",
			path:     lbPath,
			body:     lbRule("10.10.10.254", "10.10.10.254", "8443", "tcp"),
		},
		{
			// A wildcard VIP captures every address on the host, including the
			// edge's — the case that would silently kill the console.
			name:     "wildcard VIP is refused",
			reserved: edge,
			method:   "POST",
			path:     lbPath,
			body:     lbRule("0.0.0.0", "0.0.0.0", "8443", "tcp"),
			wantErr:  true,
		},
		{
			// mode 4 binds `host`, so naming the edge there must be caught even
			// when externalIP points somewhere harmless.
			name:     "collision via the L7 host field alone is refused",
			reserved: edge,
			method:   "POST",
			path:     lbPath,
			body:     lbRule("10.10.10.254", "192.168.0.8", "8443", "tcp"),
			wantErr:  true,
		},
		{
			name:     "port given as a JSON string is still checked",
			reserved: edge,
			method:   "POST",
			path:     lbPath,
			body:     []byte(`{"serviceArguments":{"externalIP":"192.168.0.8","port":"8443","protocol":"tcp"}}`),
			wantErr:  true,
		},
		{
			name:     "PUT is checked as well as POST",
			reserved: edge,
			method:   "PUT",
			path:     lbPath,
			body:     lbRule("192.168.0.8", "", "8443", "tcp"),
			wantErr:  true,
		},
		{
			name:     "path is matched case-insensitively",
			reserved: edge,
			method:   "POST",
			path:     "/v1/Config/LoadBalancer",
			body:     lbRule("192.168.0.8", "", "8443", "tcp"),
			wantErr:  true,
		},
		{
			// Deleting a rule cannot hijack anything, and reads must not be
			// blocked — only rule creation is guarded.
			name:     "GET is never blocked",
			reserved: edge,
			method:   "GET",
			path:     lbPath,
			body:     lbRule("192.168.0.8", "", "8443", "tcp"),
		},
		{
			name:     "DELETE is never blocked",
			reserved: edge,
			method:   "DELETE",
			path:     lbPath,
			body:     lbRule("192.168.0.8", "", "8443", "tcp"),
		},
		{
			name:     "unrelated config paths pass through",
			reserved: edge,
			method:   "POST",
			path:     "/v1/config/ai/apikey",
			body:     lbRule("192.168.0.8", "", "8443", "tcp"),
		},
		{
			name:    "no reservations configured means the guard is inert",
			method:  "POST",
			path:    lbPath,
			body:    lbRule("192.168.0.8", "", "8443", "tcp"),
			wantErr: false,
		},
		{
			name:     "empty body passes through",
			reserved: edge,
			method:   "POST",
			path:     lbPath,
			body:     nil,
		},
		{
			// Shapes the guard does not understand are left for the gateway to
			// reject, so an unrelated future API is not broken by this check.
			name:     "malformed JSON passes through",
			reserved: edge,
			method:   "POST",
			path:     lbPath,
			body:     []byte(`{not json`),
		},
		{
			name:     "body without a usable port passes through",
			reserved: edge,
			method:   "POST",
			path:     lbPath,
			body:     []byte(`{"serviceArguments":{"externalIP":"192.168.0.8","protocol":"tcp"}}`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkReservedEndpoint(reservedList(t, tt.reserved), tt.method, tt.path, tt.body)
			if tt.wantErr && err == nil {
				t.Fatal("expected the rule to be refused, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("expected the rule to be allowed, got: %v", err)
			}
		})
	}
}

// The handler distinguishes this rejection from a generic proxy failure so the
// operator sees which VIP collided with which reservation, not a bare 500.
func TestCheckReservedEndpointErrorIsIdentifiable(t *testing.T) {
	err := checkReservedEndpoint(
		reservedList(t, "192.168.0.8:8443"),
		"POST", "/v1/config/loadbalancer",
		lbRule("192.168.0.8", "192.168.0.8", "8443", "tcp"),
	)
	if err == nil {
		t.Fatal("expected the rule to be refused")
	}

	var reservedErr *ReservedEndpointError
	if !errors.As(err, &reservedErr) {
		t.Fatalf("error is not a *ReservedEndpointError: %T", err)
	}
	if reservedErr.Port != 8443 || reservedErr.VIP != "192.168.0.8" {
		t.Errorf("error carries VIP %s:%d, want 192.168.0.8:8443", reservedErr.VIP, reservedErr.Port)
	}
	for _, want := range []string{"192.168.0.8:8443", "OAM_RESERVED_ENDPOINTS"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error message %q does not mention %q", err.Error(), want)
		}
	}
}
