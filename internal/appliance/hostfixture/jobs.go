package hostfixture

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/appliance"
)

// Outcome selects how the fixture's pretend executions end.
type Outcome string

const (
	// OutcomeSucceed: every job runs through to SUCCEEDED. The default.
	OutcomeSucceed Outcome = "succeed"
	// OutcomeFail: jobs fail in their first phase, before anything
	// irreversible.
	OutcomeFail Outcome = "fail"
	// OutcomeRecovery: jobs fail past the irreversible phase, compensation
	// fails too, and they end in RECOVERY_REQUIRED.
	OutcomeRecovery Outcome = "recovery"
)

type step struct {
	state appliance.OperationState
	phase string
}

// job is one journal entry. Its progress is a position in a fixed script;
// nothing is executed.
type job struct {
	request appliance.HostJobRequest
	script  []step
	// irreversibleAt is the index of the first step that can no longer be
	// cancelled.
	irreversibleAt int
	position       int
	generation     int64
	cancelled      bool
	steppedAt      time.Time
}

// script lays out the states a job of the given type passes through.
func script(t appliance.OperationType, outcome Outcome) ([]step, int) {
	point := map[appliance.OperationType]string{
		appliance.OperationRestore:  "stop-services",
		appliance.OperationUpdate:   "switch-slot",
		appliance.OperationRollback: "switch-slot",
		appliance.OperationReset:    "wipe-state",
	}[t]
	steps := []step{{appliance.StateQueued, ""}, {appliance.StateRunning, "prepare"}}
	if outcome == OutcomeFail {
		return append(steps, step{appliance.StateFailed, ""}), len(steps)
	}
	irreversibleAt := len(steps)
	if point == "" {
		// A backup changes nothing, so it stays cancellable until it verifies.
		steps = append(steps, step{appliance.StateRunning, "archive"})
		irreversibleAt = len(steps)
	} else {
		steps = append(steps, step{appliance.StateRunning, point})
	}
	if outcome == OutcomeRecovery {
		return append(steps, step{appliance.StateCompensating, "undo"}, step{appliance.StateRecoveryRequired, ""}), irreversibleAt
	}
	return append(steps, step{appliance.StateVerifying, "verify"}, step{appliance.StateSucceeded, ""}), irreversibleAt
}

func (j *job) current() step {
	if j.cancelled {
		return step{appliance.StateCancelled, ""}
	}
	return j.script[j.position]
}

func (j *job) done() bool {
	return j.cancelled || j.position == len(j.script)-1
}

func (j *job) entry() appliance.HostJob {
	now := j.current()
	out := appliance.HostJob{
		OperationID: j.request.OperationID,
		State:       now.state,
		Phase:       now.phase,
		Generation:  j.generation,
		Cancellable: !j.done() && j.position < j.irreversibleAt,
		Fixture:     true,
	}
	switch now.state {
	case appliance.StateFailed:
		out.ErrorCode = "FIXTURE_FAILURE"
	case appliance.StateRecoveryRequired:
		out.ErrorCode = "FIXTURE_COMPENSATION_FAILED"
	}
	return out
}

// SetOutcome selects how jobs submitted from now on will end.
func (h *Host) SetOutcome(outcome Outcome) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.outcome = outcome
}

// SetStepEvery makes jobs advance one step per interval on their own. Zero,
// the default for New, leaves them where they are until Advance is called.
func (h *Host) SetStepEvery(interval time.Duration) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.stepEvery = interval
}

// Advance moves a job one step along its script and reports whether it moved.
func (h *Host) Advance(operationID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	j := h.jobs[operationID]
	if j == nil || j.done() {
		return false
	}
	j.position++
	j.generation++
	j.steppedAt = h.now()
	return true
}

// Forget drops a job from the journal, as a host that lost its journal would.
func (h *Host) Forget(operationID string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	delete(h.jobs, operationID)
}

// Submissions reports how many times the operation was submitted, including
// repeats the journal answered without starting anything.
func (h *Host) Submissions(operationID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.submissions[operationID]
}

// Executions reports how many jobs were actually started for the operation.
// Anything above one would be a double execution.
func (h *Host) Executions(operationID string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.executions[operationID]
}

// catchUp applies the steps that have come due, when stepping is automatic.
// Must be called with h.mu held.
func (h *Host) catchUp(j *job) {
	if h.stepEvery <= 0 {
		return
	}
	for !j.done() && h.now().Sub(j.steppedAt) >= h.stepEvery {
		j.position++
		j.generation++
		j.steppedAt = j.steppedAt.Add(h.stepEvery)
	}
}

func (h *Host) submitJob(w http.ResponseWriter, r *http.Request) {
	var req appliance.HostJobRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.OperationID == "" {
		http.Error(w, "unreadable job request", http.StatusBadRequest)
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.submissions[req.OperationID]++

	// Idempotent on the operation ID: a job the journal already holds is
	// returned as it stands and nothing is started.
	if existing := h.jobs[req.OperationID]; existing != nil {
		h.catchUp(existing)
		writeJSON(w, existing.entry())
		return
	}
	switch {
	case !req.Type.Valid() || !h.available[req.Type.Action()]:
		reject(w, string(appliance.ReasonHostUnsupported), "this host adapter does not offer the operation")
		return
	case req.InstallationID != fixtureInstallationID:
		reject(w, "INSTALLATION_MISMATCH", "the job was planned for another installation")
		return
	case req.PlanHash != planHash(req.Type, req.ArchiveRef, req.TargetReleaseRef):
		reject(w, "PLAN_STALE", "the plan no longer matches the installation")
		return
	}
	steps, irreversibleAt := script(req.Type, h.outcome)
	j := &job{request: req, script: steps, irreversibleAt: irreversibleAt, generation: 1, steppedAt: h.now()}
	h.jobs[req.OperationID] = j
	h.executions[req.OperationID]++
	writeJSON(w, j.entry())
}

func (h *Host) notFound(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusNotFound)
	json.NewEncoder(w).Encode(appliance.HostRejection{Code: "JOB_NOT_FOUND", Message: "the journal has no such job"}) // an encode error means the peer hung up
}

func (h *Host) getJob(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	j := h.jobs[r.PathValue("id")]
	if j == nil {
		h.notFound(w)
		return
	}
	h.catchUp(j)
	writeJSON(w, j.entry())
}

func (h *Host) cancelJob(w http.ResponseWriter, r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	j := h.jobs[r.PathValue("id")]
	if j == nil {
		h.notFound(w)
		return
	}
	h.catchUp(j)
	if j.cancelled {
		writeJSON(w, j.entry())
		return
	}
	if j.done() || j.position >= j.irreversibleAt {
		reject(w, appliance.CodeOperationNotCancellable, "the job has passed the point where it can be stopped")
		return
	}
	j.cancelled = true
	j.generation++
	writeJSON(w, j.entry())
}
