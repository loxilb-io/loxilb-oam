// Package hostfixture is a stand-in for the Appliance host adapter, for
// developing and testing OAM and its clients before the real adapter exists.
//
// It speaks the adapter's protocol and enforces its request authentication,
// but it executes nothing and the installation it describes is invented.
// Everything it returns is marked `"fixture": true`. A result obtained
// against it says how OAM behaves, not that an Appliance works.
package hostfixture

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/appliance"
)

// Host is an in-memory host adapter.
type Host struct {
	key    []byte
	nonces *appliance.NonceCache
	now    func() time.Time

	mu          sync.Mutex
	versions    []string
	available   map[appliance.Action]bool
	outcome     Outcome
	stepEvery   time.Duration
	jobs        map[string]*job
	submissions map[string]int
	executions  map[string]int
}

// New returns a fixture that authenticates requests with key, speaks the
// current contract version and reports the given actions as available. Every
// other action is reported as unsupported by the host.
func New(key []byte, available ...appliance.Action) *Host {
	h := &Host{
		key:         key,
		nonces:      appliance.NewNonceCache(),
		now:         time.Now,
		versions:    []string{appliance.SchemaVersion},
		available:   map[appliance.Action]bool{},
		outcome:     OutcomeSucceed,
		jobs:        map[string]*job{},
		submissions: map[string]int{},
		executions:  map[string]int{},
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
	mux.HandleFunc("POST /v1/plans", h.plan)
	mux.HandleFunc("POST /v1/jobs", h.submitJob)
	mux.HandleFunc("GET /v1/jobs/{id}", h.getJob)
	mux.HandleFunc("POST /v1/jobs/{id}/cancel", h.cancelJob)
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
		r.Body = io.NopCloser(bytes.NewReader(body))
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
		InstallationID: fixtureInstallationID,
		Model:          "fixture",
		ReleaseVersion: "0.0.0-fixture",
		ReleaseDigest:  fixtureReleaseDigest,
		Fixture:        true,
		Components: []appliance.HostComponent{
			{Name: "host-adapter", Version: "0.0.0-fixture", Liveness: appliance.LivenessAlive, Readiness: appliance.ReadinessReady},
		},
		ObservedAt: h.now().UTC(),
	})
}

// Fixture identity, shared by /v1/identity and plans.
const (
	fixtureInstallationID = "FIXTURE-INSTALLATION"
	fixtureReleaseDigest  = "sha256:0000000000000000000000000000000000000000000000000000000000000000"
)

// RefMissing is a reference the fixture always rejects, to exercise the
// "host says no" path: an archive_ref or target_release_ref with this value
// does not exist.
const RefMissing = "missing"

func reject(w http.ResponseWriter, code, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnprocessableEntity)
	json.NewEncoder(w).Encode(appliance.HostRejection{Code: code, Message: message}) // an encode error means the peer hung up
}

func fakeDigest(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// plan validates a request the way an adapter would, against an invented
// installation. Nothing is executed and nothing is remembered.
func (h *Host) plan(w http.ResponseWriter, r *http.Request) {
	var req appliance.HostPlanRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "unreadable plan request", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	available := h.available[req.Type.Action()]
	h.mu.Unlock()
	switch {
	case !req.Type.Valid() || !available:
		reject(w, string(appliance.ReasonHostUnsupported), "this host adapter does not offer the operation")
		return
	case req.ArchiveRef == RefMissing:
		reject(w, "ARCHIVE_NOT_FOUND", "no admitted archive has that reference")
		return
	case req.TargetReleaseRef == RefMissing:
		reject(w, "RELEASE_NOT_FOUND", "no admitted release has that reference")
		return
	}

	plan := appliance.Plan{
		CurrentReleaseDigest: fixtureReleaseDigest,
		Compatibility:        "compatible",
		AffectedResources:    []string{},
	}
	switch req.Type {
	case appliance.OperationBackup:
		plan.AffectedResources = []string{"oam-database", "gateway-config", "host-config"}
	case appliance.OperationRestore:
		plan.ArchiveDigest = fakeDigest("archive", req.ArchiveRef)
		plan.IrreversibleAfterPhase = "stop-services"
		plan.AffectedResources = []string{"oam-database", "gateway-config", "host-config", "sessions"}
	case appliance.OperationUpdate:
		plan.TargetReleaseDigest = fakeDigest("release", req.TargetReleaseRef)
		plan.IrreversibleAfterPhase = "switch-slot"
		plan.AffectedResources = []string{"release-slot", "oam-database"}
	case appliance.OperationRollback:
		plan.TargetReleaseDigest = fakeDigest("release", "previous")
		plan.IrreversibleAfterPhase = "switch-slot"
		plan.AffectedResources = []string{"release-slot", "oam-database"}
	case appliance.OperationReset:
		plan.IrreversibleAfterPhase = "wipe-state"
		plan.AffectedResources = []string{"oam-database", "gateway-config", "host-config", "sessions", "backups"}
	}
	writeJSON(w, appliance.HostPlan{
		PlanHash:       planHash(req.Type, req.ArchiveRef, req.TargetReleaseRef),
		InstallationID: fixtureInstallationID,
		Model:          "fixture",
		Fixture:        true,
		Plan:           plan,
	})
}

// planHash covers what was asked and the installation it was asked of — not
// the operation ID, so the same request always plans to the same hash and a
// submit can be checked against it.
func planHash(t appliance.OperationType, archiveRef, targetReleaseRef string) string {
	return fakeDigest(string(t), archiveRef, targetReleaseRef, fixtureInstallationID)
}
