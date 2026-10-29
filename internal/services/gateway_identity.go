package services

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

const (
	// GatewayAuthModeEnv selects the OAM-to-Gateway authentication plane.
	// It is deliberately separate from OAM user JWT authentication.
	GatewayAuthModeEnv = "OAM_GATEWAY_AUTH_MODE"
	// GatewayServiceTokenEnv carries the dedicated Gateway management token.
	// It must never contain a browser/OAM user JWT.
	GatewayServiceTokenEnv = "OAM_GATEWAY_SERVICE_TOKEN"
	// GatewayServiceTokenFileEnv points at a file containing the dedicated
	// Gateway management token. It is the preferred container/appliance input
	// because the token then stays out of the process environment.
	GatewayServiceTokenFileEnv = "OAM_GATEWAY_SERVICE_TOKEN_FILE"

	GatewayAuthModeDisabled     = "disabled"
	GatewayAuthModeServiceToken = "service-token"
)

const maxGatewayServiceTokenFileSize = 4096

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
// A raw environment value remains available for compatibility, while
// file-based delivery is preferred for containers and appliances. Supplying
// both sources is ambiguous and fails closed.
func GatewayServiceIdentityFromEnv() (GatewayServiceIdentity, error) {
	return gatewayServiceIdentityFromSources(
		os.Getenv(GatewayAuthModeEnv),
		os.Getenv(GatewayServiceTokenEnv),
		os.Getenv(GatewayServiceTokenFileEnv),
	)
}

func gatewayServiceIdentityFromSources(mode, token, tokenFile string) (GatewayServiceIdentity, error) {
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	if normalizedMode == "" {
		normalizedMode = GatewayAuthModeDisabled
	}
	token = strings.TrimSpace(token)
	tokenFile = strings.TrimSpace(tokenFile)

	if token != "" && tokenFile != "" {
		return GatewayServiceIdentity{}, fmt.Errorf(
			"%s and %s are both set; configure exactly one Gateway service-token source",
			GatewayServiceTokenEnv, GatewayServiceTokenFileEnv,
		)
	}

	if tokenFile == "" {
		return NewGatewayServiceIdentity(normalizedMode, token)
	}
	if normalizedMode != GatewayAuthModeServiceToken {
		if normalizedMode == GatewayAuthModeDisabled {
			return GatewayServiceIdentity{}, fmt.Errorf(
				"%s is set while %s=%s; select %s explicitly or remove the unused secret file",
				GatewayServiceTokenFileEnv, GatewayAuthModeEnv, GatewayAuthModeDisabled, GatewayAuthModeServiceToken,
			)
		}
		return NewGatewayServiceIdentity(normalizedMode, "")
	}

	fileToken, err := readGatewayServiceTokenFile(tokenFile)
	if err != nil {
		return GatewayServiceIdentity{}, err
	}
	return NewGatewayServiceIdentity(normalizedMode, fileToken)
}

func readGatewayServiceTokenFile(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s must be an absolute path", GatewayServiceTokenFileEnv)
	}
	info, err := os.Lstat(path)
	if err != nil {
		return "", fmt.Errorf("cannot inspect %s: %w", GatewayServiceTokenFileEnv, err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return "", fmt.Errorf("%s must name a regular file and must not be a symlink", GatewayServiceTokenFileEnv)
	}
	if info.Size() > maxGatewayServiceTokenFileSize {
		return "", fmt.Errorf("%s exceeds the %d-byte limit", GatewayServiceTokenFileEnv, maxGatewayServiceTokenFileSize)
	}
	contents, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", GatewayServiceTokenFileEnv, err)
	}
	return strings.TrimSpace(string(contents)), nil
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
