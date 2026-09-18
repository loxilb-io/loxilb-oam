package services

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
)

const (
	// GatewayAuthModeEnv selects the OAM-to-Gateway authentication plane.
	// It is deliberately separate from OAM user JWT authentication.
	GatewayAuthModeEnv = "OAM_GATEWAY_AUTH_MODE"
	// GatewayServiceTokenEnv carries the dedicated Gateway management token.
	// It must never contain a browser/OAM user JWT.
	GatewayServiceTokenEnv = "OAM_GATEWAY_SERVICE_TOKEN"

	GatewayAuthModeDisabled     = "disabled"
	GatewayAuthModeServiceToken = "service-token"
)

var ErrGatewayServiceIdentityUnavailable = errors.New("gateway service identity is unavailable")

// GatewayServiceIdentity is the outbound OAM-to-Gateway authentication
// configuration. The token is intentionally private so it cannot be logged by
// generic struct formatting or exposed through a status response.
type GatewayServiceIdentity struct {
	mode  string
	token string
}

// NewGatewayServiceIdentity validates a gateway authentication configuration.
// disabled preserves the historical auth-disabled deployment. service-token
// is fail-closed and requires a dedicated token.
func NewGatewayServiceIdentity(mode, token string) (GatewayServiceIdentity, error) {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" {
		mode = GatewayAuthModeDisabled
	}
	token = strings.TrimSpace(token)

	switch mode {
	case GatewayAuthModeDisabled:
		if token != "" {
			return GatewayServiceIdentity{}, fmt.Errorf(
				"%s is set while %s=%s; select %s explicitly or remove the unused secret",
				GatewayServiceTokenEnv, GatewayAuthModeEnv, GatewayAuthModeDisabled, GatewayAuthModeServiceToken,
			)
		}
	case GatewayAuthModeServiceToken:
		if token == "" {
			return GatewayServiceIdentity{}, fmt.Errorf(
				"%w: %s=%s requires %s",
				ErrGatewayServiceIdentityUnavailable, GatewayAuthModeEnv, GatewayAuthModeServiceToken, GatewayServiceTokenEnv,
			)
		}
		if strings.ContainsAny(token, "\r\n") {
			return GatewayServiceIdentity{}, fmt.Errorf("%s contains an invalid line break", GatewayServiceTokenEnv)
		}
		if strings.HasPrefix(strings.ToLower(token), "bearer ") {
			return GatewayServiceIdentity{}, fmt.Errorf("%s must contain the raw token without the Bearer prefix", GatewayServiceTokenEnv)
		}
	default:
		return GatewayServiceIdentity{}, fmt.Errorf(
			"invalid %s %q: expected %q or %q",
			GatewayAuthModeEnv, mode, GatewayAuthModeDisabled, GatewayAuthModeServiceToken,
		)
	}

	return GatewayServiceIdentity{mode: mode, token: token}, nil
}

// GatewayServiceIdentityFromEnv reads and validates the outbound identity.
func GatewayServiceIdentityFromEnv() (GatewayServiceIdentity, error) {
	return NewGatewayServiceIdentity(os.Getenv(GatewayAuthModeEnv), os.Getenv(GatewayServiceTokenEnv))
}

// Mode is safe to expose in logs and health responses. It never includes the
// credential itself.
func (g GatewayServiceIdentity) Mode() string {
	if g.mode == "" {
		return GatewayAuthModeDisabled
	}
	return g.mode
}

// Authorize removes any caller credential and applies only the configured OAM
// service identity. The validation is repeated here as defence in depth for
// zero-value or incorrectly constructed identities.
func (g GatewayServiceIdentity) Authorize(req *http.Request) error {
	req.Header.Del("Authorization")
	switch g.Mode() {
	case GatewayAuthModeDisabled:
		return nil
	case GatewayAuthModeServiceToken:
		if g.token == "" {
			return fmt.Errorf("%w: %s is empty", ErrGatewayServiceIdentityUnavailable, GatewayServiceTokenEnv)
		}
		req.Header.Set("Authorization", "Bearer "+g.token)
		return nil
	default:
		return fmt.Errorf("%w: unsupported mode %q", ErrGatewayServiceIdentityUnavailable, g.mode)
	}
}
