// Package appliance is OAM's side of whole-Appliance operations: what the
// installation is, what can be done to it, and — in later changes — the
// durable operations themselves.
//
// OAM does not execute anything on the host. A separate host adapter does,
// and OAM reaches it over a Unix domain socket (see HostClient). A deployment
// without that adapter — every deployment that is not the Appliance — reports
// each action as unsupported rather than hiding the API, so a client can tell
// "not here" from "not allowed" from "not now".
//
// The contract is alpha: names and fields may change until it is agreed with
// its consumers.
package appliance

import "time"

// SchemaVersion identifies the contract carried in every response body.
const SchemaVersion = "appliance-ops/v1alpha1"

// Action is something that can be done to the Appliance as a whole.
type Action string

const (
	ActionBackup      Action = "backup"
	ActionRestore     Action = "restore"
	ActionUpdate      Action = "update"
	ActionRollback    Action = "rollback"
	ActionReset       Action = "reset"
	ActionDiagnostics Action = "diagnostics"
)

// Actions lists every action in the order responses present them.
var Actions = []Action{ActionBackup, ActionRestore, ActionUpdate, ActionRollback, ActionReset, ActionDiagnostics}

// Destructive reports whether an action requires reauthentication before it
// may be executed.
func (a Action) Destructive() bool {
	switch a {
	case ActionRestore, ActionUpdate, ActionRollback, ActionReset:
		return true
	}
	return false
}

// UnavailableReason says why an action cannot be executed right now. It is
// about the installation, never about the caller: see ActionCapability.Permitted.
type UnavailableReason string

const (
	ReasonHostNotConfigured   UnavailableReason = "HOST_NOT_CONFIGURED"
	ReasonHostUnreachable     UnavailableReason = "HOST_UNREACHABLE"
	ReasonHostUnsupported     UnavailableReason = "HOST_UNSUPPORTED"
	ReasonSchemaMismatch      UnavailableReason = "SCHEMA_MISMATCH"
	ReasonOperationInProgress UnavailableReason = "OPERATION_IN_PROGRESS"
	ReasonRecoveryRequired    UnavailableReason = "RECOVERY_REQUIRED"
)

// ActionCapability answers three separate questions about one action.
type ActionCapability struct {
	Action Action `json:"action"`
	// Supported: the host adapter implements the action in a contract
	// version OAM speaks.
	Supported bool `json:"supported"`
	// Available: it can be executed now.
	Available bool `json:"available"`
	// UnavailableReason is set exactly when Available is false.
	UnavailableReason UnavailableReason `json:"unavailable_reason,omitempty"`
	// Permitted: the caller's role may request it. Independent of the above.
	Permitted bool `json:"permitted"`
	// RequiresReauthentication: executing it needs a fresh password check.
	RequiresReauthentication bool `json:"requires_reauthentication"`
}

// Capabilities is the body of GET /oam/v1/appliance/capabilities.
type Capabilities struct {
	SchemaVersion  string `json:"schema_version"`
	HostConfigured bool   `json:"host_configured"`
	// HostFixture is true when the host adapter declares itself a test
	// fixture. Nothing a fixture reports describes a real installation.
	HostFixture          bool               `json:"host_fixture"`
	HostContractVersions []string           `json:"host_contract_versions"`
	ObservedAt           *time.Time         `json:"observed_at,omitempty"`
	Actions              []ActionCapability `json:"actions"`
}

// Liveness and readiness are observed separately and never derived from one
// another. "unknown" is what an unreachable component is — never "ready".
const (
	LivenessAlive   = "alive"
	LivenessDead    = "dead"
	LivenessUnknown = "unknown"

	ReadinessReady    = "ready"
	ReadinessNotReady = "not_ready"
	ReadinessUnknown  = "unknown"
)

// Component is one part of the installation as last observed.
type Component struct {
	Name       string     `json:"name"`
	Version    string     `json:"version,omitempty"`
	Digest     string     `json:"digest,omitempty"`
	Liveness   string     `json:"liveness"`
	Readiness  string     `json:"readiness"`
	ObservedAt *time.Time `json:"observed_at,omitempty"`
	// Stale: the observation could not be refreshed for this response.
	Stale bool `json:"stale"`
}

// Product identifies the installation. It comes from the host adapter and is
// absent when there is none.
type Product struct {
	Model          string `json:"model,omitempty"`
	InstallationID string `json:"installation_id,omitempty"`
	ReleaseVersion string `json:"release_version,omitempty"`
	ReleaseDigest  string `json:"release_digest,omitempty"`
	Fixture        bool   `json:"fixture"`
}

// DatabaseStatus describes OAM's own schema.
type DatabaseStatus struct {
	// SchemaVersion is 0 when the schema is not tracked (OAM_DB_MIGRATE=off
	// on a database that was never migrated by the server).
	SchemaVersion   int        `json:"schema_version"`
	LatestMigration string     `json:"latest_migration,omitempty"`
	AppliedAt       *time.Time `json:"applied_at,omitempty"`
	Adopted         bool       `json:"adopted"`
}

// Status is the body of GET /oam/v1/appliance/status.
type Status struct {
	SchemaVersion string         `json:"schema_version"`
	Product       *Product       `json:"product,omitempty"`
	Components    []Component    `json:"components"`
	Database      DatabaseStatus `json:"database"`
}

// Error origins: which party produced a failure.
const (
	OriginOAM     = "oam"
	OriginHost    = "host"
	OriginGateway = "gateway"
)

// Machine error codes. Stable, never localized; clients branch on these and
// not on the human-readable message.
const (
	CodeUnauthorized      = "UNAUTHORIZED"
	CodePermissionDenied  = "PERMISSION_DENIED"
	CodeHostNotConfigured = "HOST_NOT_CONFIGURED"
	CodeHostUnreachable   = "HOST_UNREACHABLE"
	CodeSchemaMismatch    = "SCHEMA_MISMATCH"
	CodeInternal          = "INTERNAL_ERROR"
)

// Recovery actions a client may offer. A closed list.
const (
	RecoveryRetry          = "RETRY"
	RecoveryReauthenticate = "REAUTHENTICATE"
	RecoveryContactSupport = "CONTACT_SUPPORT"
	RecoveryNone           = "NONE"
)

// Recovery tells the client what, if anything, can be done about an error.
type Recovery struct {
	Action string `json:"action"`
}

// ErrorBody is the error envelope of the appliance endpoints. Error keeps the
// name and type every other OAM endpoint uses for its message, so a client
// that only reads `error` keeps working.
type ErrorBody struct {
	Error       string    `json:"error"`
	Code        string    `json:"code"`
	Origin      string    `json:"origin"`
	OperationID string    `json:"operation_id,omitempty"`
	RequestID   string    `json:"request_id,omitempty"`
	Recovery    *Recovery `json:"recovery,omitempty"`
}
