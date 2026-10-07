package appliance

import (
	"context"
	"database/sql"
	"errors"
	"slices"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/migrate"
)

// Service answers the read-only appliance questions.
type Service struct {
	host    HostClient
	db      *sql.DB
	version string
	now     func() time.Time
}

// NewService wires the service. version is this binary's release identifier.
func NewService(host HostClient, db *sql.DB, version string) *Service {
	return &Service{host: host, db: db, version: version, now: time.Now}
}

// hostReason maps a host-client failure onto the reason reported to clients.
func hostReason(err error) UnavailableReason {
	if errors.Is(err, ErrHostNotConfigured) {
		return ReasonHostNotConfigured
	}
	return ReasonHostUnreachable
}

// Capabilities reports, for every action, whether it exists here, whether it
// can run now, and whether the caller may ask for it. permitted is the
// caller's authorization; it is evaluated for every action regardless of what
// the host says, so the two axes stay independent.
//
// It never fails: an absent or silent host is an answer, not an error.
func (s *Service) Capabilities(ctx context.Context, permitted func(Action) bool) Capabilities {
	out := Capabilities{
		SchemaVersion:        SchemaVersion,
		HostConfigured:       s.host.Configured(),
		HostContractVersions: []string{},
		Actions:              make([]ActionCapability, 0, len(Actions)),
	}

	hostCaps, err := s.host.Capabilities(ctx)
	// The reason every action shares when the host as a whole cannot serve.
	var blanket UnavailableReason
	switch {
	case err != nil:
		blanket = hostReason(err)
	case !slices.Contains(hostCaps.ContractVersions, SchemaVersion):
		blanket = ReasonSchemaMismatch
	}
	byAction := map[Action]HostActionState{}
	if hostCaps != nil {
		out.HostFixture = hostCaps.Fixture
		out.HostContractVersions = append(out.HostContractVersions, hostCaps.ContractVersions...)
		observed := hostCaps.ObservedAt
		out.ObservedAt = &observed
		for _, state := range hostCaps.Actions {
			byAction[state.Action] = state
		}
	}

	for _, action := range Actions {
		entry := ActionCapability{
			Action:                   action,
			Permitted:                permitted(action),
			RequiresReauthentication: action.Destructive(),
		}
		state, known := byAction[action]
		switch {
		case blanket != "":
			entry.UnavailableReason = blanket
			// A host that is merely unreachable may well support the action;
			// what cannot be claimed is that it does.
		case !known:
			entry.UnavailableReason = ReasonHostUnsupported
		case state.Available:
			entry.Supported = true
			entry.Available = true
		default:
			// The host knows the action but cannot run it now. "Unsupported"
			// is the one reason that also means it does not exist here.
			entry.UnavailableReason = state.Reason
			if entry.UnavailableReason == "" {
				entry.UnavailableReason = ReasonHostUnsupported
			}
			entry.Supported = entry.UnavailableReason != ReasonHostUnsupported
		}
		out.Actions = append(out.Actions, entry)
	}
	return out
}

// Status describes the installation: what it is, which components were
// observed in what state, and where OAM's schema stands. A component that
// could not be observed is reported as unknown and stale — never as ready.
func (s *Service) Status(ctx context.Context) Status {
	now := s.now().UTC()
	out := Status{SchemaVersion: SchemaVersion}

	// OAM is answering this request, which is the whole of the evidence that
	// it is alive and ready.
	out.Components = append(out.Components, Component{
		Name: "oam", Version: s.version, Liveness: LivenessAlive, Readiness: ReadinessReady, ObservedAt: &now,
	})

	database := Component{Name: "database", Liveness: LivenessUnknown, Readiness: ReadinessUnknown, Stale: true}
	if err := s.db.PingContext(ctx); err == nil {
		database = Component{Name: "database", Liveness: LivenessAlive, Readiness: ReadinessReady, ObservedAt: &now}
		if current, err := migrate.Current(ctx, s.db); err == nil && current != nil {
			applied := current.AppliedAt.UTC()
			out.Database = DatabaseStatus{
				SchemaVersion:   current.Version,
				LatestMigration: current.Name,
				AppliedAt:       &applied,
				Adopted:         current.Adopted,
			}
		}
	}
	out.Components = append(out.Components, database)

	if !s.host.Configured() {
		return out
	}
	identity, err := s.host.Identity(ctx)
	if err != nil {
		out.Components = append(out.Components, Component{
			Name: "host-adapter", Liveness: LivenessUnknown, Readiness: ReadinessUnknown, Stale: true,
		})
		return out
	}
	observed := identity.ObservedAt.UTC()
	out.Product = &Product{
		Model:          identity.Model,
		InstallationID: identity.InstallationID,
		ReleaseVersion: identity.ReleaseVersion,
		ReleaseDigest:  identity.ReleaseDigest,
		Fixture:        identity.Fixture,
	}
	for _, c := range identity.Components {
		out.Components = append(out.Components, Component{
			Name: c.Name, Version: c.Version, Digest: c.Digest,
			Liveness:   knownOr(c.Liveness, LivenessUnknown, LivenessAlive, LivenessDead),
			Readiness:  knownOr(c.Readiness, ReadinessUnknown, ReadinessReady, ReadinessNotReady),
			ObservedAt: &observed,
		})
	}
	return out
}

// knownOr returns value if it is one of allowed, else fallback — so a value
// the host invents cannot pass for a healthy one.
func knownOr(value, fallback string, allowed ...string) string {
	if slices.Contains(allowed, value) {
		return value
	}
	return fallback
}
