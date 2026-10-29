package config

import (
	"fmt"
	"os"
	"time"
)

// proxyDisableKeepAlivesEnv is an operational escape hatch for the instance
// proxy's outbound connection pool.
//
// Connection reuse is on by default: a handshake per proxied request roughly
// doubled the latency of a small call and made every request vulnerable to a
// transient connection-establishment failure. The hazard reuse introduces —
// a pooled connection to an instance endpoint that has since been replaced —
// is handled by invalidating the pool on the instance mutation paths.
//
// Set OAM_PROXY_DISABLE_KEEPALIVES=true to restore the previous
// dial-per-request behaviour if a deployment hits a connection-reuse problem
// that the invalidation points do not cover.
const proxyDisableKeepAlivesEnv = "OAM_PROXY_DISABLE_KEEPALIVES"

// ProxyKeepAlivesDisabled reports whether outbound connection reuse to managed
// LoxiLB instances has been turned off.
func ProxyKeepAlivesDisabled() bool {
	return os.Getenv(proxyDisableKeepAlivesEnv) == "true"
}

// proxyTimeoutEnv bounds a single proxied request to a managed instance.
//
// The default suits the small management calls the console makes. A
// deployment whose instances carry a large enough configuration for
// /config/meta to run long can raise it, which is preferable to the operator
// seeing a timeout reported for a healthy instance.
const proxyTimeoutEnv = "OAM_PROXY_TIMEOUT"

// DefaultProxyRequestTimeout is the per-request budget when OAM_PROXY_TIMEOUT
// is unset or unparseable.
const DefaultProxyRequestTimeout = 10 * time.Second

// ProxyRequestTimeout returns the per-request timeout for proxied instance
// calls. An unset, unparseable, or non-positive value yields the default:
// a misconfigured duration must not silently disable the timeout.
func ProxyRequestTimeout() time.Duration {
	d, err := parseProxyTimeout(os.Getenv(proxyTimeoutEnv))
	if err != nil {
		return DefaultProxyRequestTimeout
	}
	return d
}

// ProxyRequestTimeoutError reports a set-but-unusable OAM_PROXY_TIMEOUT, so
// startup can say so. Falling back silently is how an operator ends up
// believing a timeout is configured that is not — the same invisible drift
// that let the proxy's timeout classification rot unnoticed. A bad value is
// not fatal, unlike OAM_RESERVED_ENDPOINTS, because the fallback is a safe
// bound rather than an inert guard.
func ProxyRequestTimeoutError() error {
	_, err := parseProxyTimeout(os.Getenv(proxyTimeoutEnv))
	return err
}

func parseProxyTimeout(v string) (time.Duration, error) {
	if v == "" {
		return DefaultProxyRequestTimeout, nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return DefaultProxyRequestTimeout, fmt.Errorf(
			"%s=%q is not a duration (want e.g. 10s, 1m30s)", proxyTimeoutEnv, v)
	}
	if d <= 0 {
		return DefaultProxyRequestTimeout, fmt.Errorf(
			"%s=%q is not positive; a zero or negative timeout would disable the request bound",
			proxyTimeoutEnv, v)
	}
	return d, nil
}
