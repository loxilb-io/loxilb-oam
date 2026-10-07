// Package hostfixture is a stand-in for the Appliance host adapter, for
// developing and testing OAM and its clients before the real adapter exists.
//
// It speaks the adapter's protocol and enforces its request authentication,
// but it executes nothing and the installation it describes is invented.
// Everything it returns is marked `"fixture": true`. A result obtained
// against it says how OAM behaves, not that an Appliance works.
package hostfixture

import (
	"encoding/json"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/appliance"
)

// Host is an in-memory host adapter.
type Host struct {
	key    []byte
	nonces *appliance.NonceCache
	now    func() time.Time

	mu        sync.Mutex
	versions  []string
	available map[appliance.Action]bool
}

// New returns a fixture that authenticates requests with key, speaks the
// current contract version and reports the given actions as available. Every
// other action is reported as unsupported by the host.
func New(key []byte, available ...appliance.Action) *Host {
	h := &Host{
		key:       key,
		nonces:    appliance.NewNonceCache(),
		now:       time.Now,
		versions:  []string{appliance.SchemaVersion},
		available: map[appliance.Action]bool{},
	}
	for _, a := range available {
		h.available[a] = true
	}
	return h
}

// SetContractVersions replaces the contract versions the fixture claims to
// speak, to exercise a version mismatch.
func (h *Host) SetContractVersions(versions ...string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.versions = versions
}

// Handler serves the adapter protocol.
func (h *Host) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/capabilities", h.capabilities)
	mux.HandleFunc("GET /v1/identity", h.identity)
	return h.authenticated(mux)
}

func (h *Host) authenticated(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			http.Error(w, "unreadable body", http.StatusBadRequest)
			return
		}
		if err := appliance.VerifyRequest(r, h.key, body, h.now(), h.nonces); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v) // an encode error means the peer hung up
}

func (h *Host) capabilities(w http.ResponseWriter, _ *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := appliance.HostCapabilities{
		ContractVersions: append([]string{}, h.versions...),
		Fixture:          true,
		ObservedAt:       h.now().UTC(),
	}
	for _, a := range appliance.Actions {
		state := appliance.HostActionState{Action: a, Available: h.available[a]}
		if !state.Available {
			state.Reason = appliance.ReasonHostUnsupported
		}
		out.Actions = append(out.Actions, state)
	}
	writeJSON(w, out)
}

func (h *Host) identity(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, appliance.HostIdentity{
		InstallationID: "FIXTURE-INSTALLATION",
		Model:          "fixture",
		ReleaseVersion: "0.0.0-fixture",
		ReleaseDigest:  "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		Fixture:        true,
		Components: []appliance.HostComponent{
			{Name: "host-adapter", Version: "0.0.0-fixture", Liveness: appliance.LivenessAlive, Readiness: appliance.ReadinessReady},
		},
		ObservedAt: h.now().UTC(),
	})
}
