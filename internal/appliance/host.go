package appliance

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// Environment variables that connect OAM to the host adapter. Both unset
// means "this is not an Appliance"; one without the other is a configuration
// error.
const (
	HostSocketEnv  = "OAM_APPLIANCE_HOST_SOCKET"
	HostKeyFileEnv = "OAM_APPLIANCE_HOST_KEY_FILE"
)

var (
	// ErrHostNotConfigured: no host adapter is configured for this deployment.
	ErrHostNotConfigured = errors.New("appliance host adapter is not configured")
	// ErrHostUnreachable: one is configured but did not answer.
	ErrHostUnreachable = errors.New("appliance host adapter is unreachable")
)

// HostActionState is what the host adapter says about one action.
type HostActionState struct {
	Action    Action `json:"action"`
	Available bool   `json:"available"`
	// Reason is the host's own explanation when Available is false; one of
	// the UnavailableReason values, or empty.
	Reason UnavailableReason `json:"reason,omitempty"`
}

// HostCapabilities is the body of the adapter's GET /v1/capabilities.
type HostCapabilities struct {
	ContractVersions []string          `json:"contract_versions"`
	Fixture          bool              `json:"fixture"`
	Actions          []HostActionState `json:"actions"`
	ObservedAt       time.Time         `json:"observed_at"`
}

// HostComponent is one component as the host sees it.
type HostComponent struct {
	Name      string `json:"name"`
	Version   string `json:"version,omitempty"`
	Digest    string `json:"digest,omitempty"`
	Liveness  string `json:"liveness"`
	Readiness string `json:"readiness"`
}

// HostIdentity is the body of the adapter's GET /v1/identity.
type HostIdentity struct {
	InstallationID string          `json:"installation_id"`
	Model          string          `json:"model"`
	ReleaseVersion string          `json:"release_version"`
	ReleaseDigest  string          `json:"release_digest"`
	Fixture        bool            `json:"fixture"`
	Components     []HostComponent `json:"components"`
	ObservedAt     time.Time       `json:"observed_at"`
}

// HostPlanRequest is the body of the adapter's POST /v1/plans.
type HostPlanRequest struct {
	SchemaVersion    string        `json:"schema_version"`
	OperationID      string        `json:"operation_id"`
	Type             OperationType `json:"type"`
	ArchiveRef       string        `json:"archive_ref,omitempty"`
	TargetReleaseRef string        `json:"target_release_ref,omitempty"`
}

// HostPlan is the adapter's answer: the request is executable, and this is
// what executing it would involve. PlanHash binds a later submit to exactly
// this plan.
type HostPlan struct {
	PlanHash       string `json:"plan_hash"`
	InstallationID string `json:"installation_id"`
	Model          string `json:"model"`
	Fixture        bool   `json:"fixture"`
	Plan
}

// HostRejection is the adapter refusing a request it understood: an unknown
// archive, an incompatible release. It is the host's verdict, not a failure
// to reach it.
type HostRejection struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *HostRejection) Error() string {
	return "appliance host adapter rejected the request: " + e.Code
}

// HostClient is everything OAM asks of the host adapter.
type HostClient interface {
	// Configured reports whether a host adapter exists for this deployment.
	Configured() bool
	Capabilities(ctx context.Context) (*HostCapabilities, error)
	Identity(ctx context.Context) (*HostIdentity, error)
	// Plan asks the adapter to validate a request. It has no side effects on
	// the host. A *HostRejection error is the adapter saying no.
	Plan(ctx context.Context, req HostPlanRequest) (*HostPlan, error)
}

// unconfiguredHost is the HostClient of a deployment with no adapter.
type unconfiguredHost struct{}

func (unconfiguredHost) Configured() bool { return false }
func (unconfiguredHost) Capabilities(context.Context) (*HostCapabilities, error) {
	return nil, ErrHostNotConfigured
}
func (unconfiguredHost) Identity(context.Context) (*HostIdentity, error) {
	return nil, ErrHostNotConfigured
}
func (unconfiguredHost) Plan(context.Context, HostPlanRequest) (*HostPlan, error) {
	return nil, ErrHostNotConfigured
}

// UnconfiguredHost returns the HostClient of a deployment with no adapter.
func UnconfiguredHost() HostClient { return unconfiguredHost{} }

// hostRequestTimeout bounds one call to the adapter. It is a local socket:
// an adapter that has not answered in this long is not going to.
const hostRequestTimeout = 5 * time.Second

// maxHostResponseBytes caps what OAM reads from the adapter.
const maxHostResponseBytes = 1 << 20

type socketHost struct {
	key    []byte
	client *http.Client
	now    func() time.Time
}

// NewSocketHost returns a HostClient that speaks to the adapter listening on
// the Unix socket at socketPath, signing each request with key.
func NewSocketHost(socketPath string, key []byte) (HostClient, error) {
	if len(key) < MinKeyBytes {
		return nil, fmt.Errorf("appliance host key must be at least %d bytes, got %d", MinKeyBytes, len(key))
	}
	dialer := &net.Dialer{}
	return &socketHost{
		key: key,
		client: &http.Client{
			Timeout: hostRequestTimeout,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return dialer.DialContext(ctx, "unix", socketPath)
				},
			},
		},
		now: time.Now,
	}, nil
}

// HostClientFromEnv builds the HostClient the environment describes. With
// neither variable set it returns the unconfigured client. A half-configured
// or unusable setup is an error, so an Appliance never starts believing it
// has no host adapter because of a typo.
func HostClientFromEnv() (HostClient, error) {
	socketPath := strings.TrimSpace(os.Getenv(HostSocketEnv))
	keyFile := strings.TrimSpace(os.Getenv(HostKeyFileEnv))
	if socketPath == "" && keyFile == "" {
		return UnconfiguredHost(), nil
	}
	if socketPath == "" || keyFile == "" {
		return nil, fmt.Errorf("%s and %s must be set together", HostSocketEnv, HostKeyFileEnv)
	}
	key, err := os.ReadFile(keyFile)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", HostKeyFileEnv, err)
	}
	return NewSocketHost(socketPath, bytes.TrimSpace(key))
}

func (h *socketHost) Configured() bool { return true }

func (h *socketHost) get(ctx context.Context, path string, out any) error {
	return h.do(ctx, http.MethodGet, path, nil, out)
}

// do sends one signed request. in, when not nil, is sent as the JSON body.
func (h *socketHost) do(ctx context.Context, method, path string, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return err
		}
	}
	// The host part is ignored by the Unix dialer; it only has to parse.
	req, err := http.NewRequestWithContext(ctx, method, "http://appliance-host"+path, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if err := SignRequest(req, h.key, payload, h.now()); err != nil {
		return err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrHostUnreachable, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxHostResponseBytes))
	if err != nil {
		return fmt.Errorf("%w: %v", ErrHostUnreachable, err)
	}
	if resp.StatusCode == http.StatusUnprocessableEntity {
		// The one non-200 that is part of the contract: the adapter
		// understood the request and refuses it, with a code.
		var rejection HostRejection
		if err := json.Unmarshal(body, &rejection); err == nil && rejection.Code != "" {
			return &rejection
		}
	}
	if resp.StatusCode != http.StatusOK {
		// The adapter answered, but not usefully. Its body is not relayed:
		// it is not part of the contract and may describe the host.
		return fmt.Errorf("%w: adapter answered %s with HTTP %d", ErrHostUnreachable, path, resp.StatusCode)
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("%w: adapter answered %s with an unreadable body", ErrHostUnreachable, path)
	}
	return nil
}

func (h *socketHost) Capabilities(ctx context.Context) (*HostCapabilities, error) {
	var out HostCapabilities
	if err := h.get(ctx, "/v1/capabilities", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *socketHost) Identity(ctx context.Context) (*HostIdentity, error) {
	var out HostIdentity
	if err := h.get(ctx, "/v1/identity", &out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (h *socketHost) Plan(ctx context.Context, req HostPlanRequest) (*HostPlan, error) {
	var out HostPlan
	if err := h.do(ctx, http.MethodPost, "/v1/plans", req, &out); err != nil {
		return nil, err
	}
	if out.PlanHash == "" || out.InstallationID == "" {
		return nil, fmt.Errorf("%w: adapter returned a plan without a hash or installation", ErrHostUnreachable)
	}
	return &out, nil
}
