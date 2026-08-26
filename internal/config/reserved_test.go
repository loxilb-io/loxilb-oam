package config

import (
	"net"
	"testing"
)

func TestParseReservedEndpoints(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    []ReservedEndpoint
		wantErr bool
	}{
		{name: "empty yields nothing", in: ""},
		{name: "whitespace only yields nothing", in: "  ,  , "},
		{
			name: "address and port",
			in:   "192.168.0.8:8443",
			want: []ReservedEndpoint{{IP: net.ParseIP("192.168.0.8"), Port: 8443}},
		},
		{
			name: "explicit protocol",
			in:   "192.168.0.8:8443/tcp",
			want: []ReservedEndpoint{{IP: net.ParseIP("192.168.0.8"), Port: 8443, Proto: "tcp"}},
		},
		{
			name: "omitted address reserves the port everywhere",
			in:   ":8443",
			want: []ReservedEndpoint{{Port: 8443}},
		},
		{
			name: "wildcard address is kept and treated as unspecified when matching",
			in:   "0.0.0.0:8080",
			want: []ReservedEndpoint{{IP: net.ParseIP("0.0.0.0"), Port: 8080}},
		},
		{
			name: "ipv6 literal",
			in:   "[::1]:8443",
			want: []ReservedEndpoint{{IP: net.ParseIP("::1"), Port: 8443}},
		},
		{
			name: "multiple entries with surrounding space",
			in:   " 192.168.0.8:8443/tcp , 0.0.0.0:8080 ",
			want: []ReservedEndpoint{
				{IP: net.ParseIP("192.168.0.8"), Port: 8443, Proto: "tcp"},
				{IP: net.ParseIP("0.0.0.0"), Port: 8080},
			},
		},
		{name: "missing port is rejected", in: "192.168.0.8", wantErr: true},
		{name: "non-numeric port is rejected", in: "192.168.0.8:https", wantErr: true},
		{name: "port zero is rejected", in: "192.168.0.8:0", wantErr: true},
		{name: "port above range is rejected", in: "192.168.0.8:65536", wantErr: true},
		{name: "hostname instead of IP is rejected", in: "oam.example.internal:8443", wantErr: true},
		{name: "unknown protocol is rejected", in: "192.168.0.8:8443/sctp", wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseReservedEndpoints(tt.in)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("ParseReservedEndpoints(%q) = %v, want error", tt.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseReservedEndpoints(%q) returned unexpected error: %v", tt.in, err)
			}
			if len(got) != len(tt.want) {
				t.Fatalf("ParseReservedEndpoints(%q) returned %d entries, want %d: %v", tt.in, len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i].Port != tt.want[i].Port || got[i].Proto != tt.want[i].Proto || !got[i].IP.Equal(tt.want[i].IP) {
					t.Errorf("entry %d = %+v, want %+v", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestMatchReservedEndpoint(t *testing.T) {
	// The converged single-node default: one management edge on the host's only
	// address, reserved so no LB rule can be programmed on top of it.
	edge := mustParse(t, "192.168.0.8:8443")

	tests := []struct {
		name      string
		list      []ReservedEndpoint
		ip        string
		port      int
		proto     string
		wantMatch bool
	}{
		{name: "exact collision", list: edge, ip: "192.168.0.8", port: 8443, proto: "tcp", wantMatch: true},
		{name: "different port is allowed", list: edge, ip: "192.168.0.8", port: 8080, proto: "tcp"},
		{name: "different address is allowed", list: edge, ip: "10.10.10.254", port: 8443, proto: "tcp"},
		{
			// The dangerous case: a VIP of 0.0.0.0 captures traffic to every
			// address on the host, the edge address included.
			name:      "wildcard rule VIP collides with a specific reservation",
			list:      edge,
			ip:        "0.0.0.0",
			port:      8443,
			proto:     "tcp",
			wantMatch: true,
		},
		{
			name:      "wildcard reservation protects every address",
			list:      mustParse(t, ":8443"),
			ip:        "10.10.10.254",
			port:      8443,
			proto:     "tcp",
			wantMatch: true,
		},
		{
			name:      "protocol-less reservation covers udp too",
			list:      edge,
			ip:        "192.168.0.8",
			port:      8443,
			proto:     "udp",
			wantMatch: true,
		},
		{
			name:  "protocol-specific reservation ignores the other protocol",
			list:  mustParse(t, "192.168.0.8:8443/tcp"),
			ip:    "192.168.0.8",
			port:  8443,
			proto: "udp",
		},
		{
			// A VIP this code cannot parse must be refused, not waved through:
			// the guard's whole purpose is to fail closed.
			name:      "unparseable rule address is treated as a wildcard",
			list:      edge,
			ip:        "not-an-ip",
			port:      8443,
			proto:     "tcp",
			wantMatch: true,
		},
		{name: "empty reservation list never matches", list: nil, ip: "192.168.0.8", port: 8443, proto: "tcp"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := MatchReservedEndpoint(tt.list, tt.ip, tt.port, tt.proto)
			if ok != tt.wantMatch {
				t.Fatalf("MatchReservedEndpoint(%v, %q, %d, %q) matched=%v (%v), want %v",
					tt.list, tt.ip, tt.port, tt.proto, ok, got, tt.wantMatch)
			}
		})
	}
}

func mustParse(t *testing.T, v string) []ReservedEndpoint {
	t.Helper()
	out, err := ParseReservedEndpoints(v)
	if err != nil {
		t.Fatalf("ParseReservedEndpoints(%q): %v", v, err)
	}
	return out
}
