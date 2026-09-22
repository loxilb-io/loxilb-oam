package config

import (
	"os"
	"time"
)

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
	v := os.Getenv(proxyTimeoutEnv)
	if v == "" {
		return DefaultProxyRequestTimeout
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return DefaultProxyRequestTimeout
	}
	return d
}
