package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProxyRequestTimeout(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want time.Duration
	}{
		{"unset uses the default", "", DefaultProxyRequestTimeout},
		{"valid duration is honoured", "45s", 45 * time.Second},
		// A misconfigured value must not silently disable the timeout, which
		// is what a zero http.Client.Timeout would do.
		{"unparseable falls back", "not-a-duration", DefaultProxyRequestTimeout},
		{"zero falls back", "0s", DefaultProxyRequestTimeout},
		{"negative falls back", "-5s", DefaultProxyRequestTimeout},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(proxyTimeoutEnv, tc.env)
			assert.Equal(t, tc.want, ProxyRequestTimeout())
		})
	}
}

func TestProxyKeepAlivesDisabled(t *testing.T) {
	t.Setenv(proxyDisableKeepAlivesEnv, "")
	assert.False(t, ProxyKeepAlivesDisabled(), "connection reuse is the default")

	t.Setenv(proxyDisableKeepAlivesEnv, "true")
	assert.True(t, ProxyKeepAlivesDisabled())

	// Only the exact opt-in string counts, matching the other OAM toggles.
	t.Setenv(proxyDisableKeepAlivesEnv, "TRUE")
	assert.False(t, ProxyKeepAlivesDisabled())
}

// A silently-ignored timeout is how an operator ends up believing a bound is
// configured that is not, so a set-but-unusable value must be reportable.
func TestProxyRequestTimeoutError(t *testing.T) {
	t.Setenv(proxyTimeoutEnv, "")
	assert.NoError(t, ProxyRequestTimeoutError())

	t.Setenv(proxyTimeoutEnv, "45s")
	assert.NoError(t, ProxyRequestTimeoutError())

	// A bare number is the plausible typo: valid-looking, not a Go duration.
	t.Setenv(proxyTimeoutEnv, "10")
	err := ProxyRequestTimeoutError()
	require.Error(t, err)
	assert.Contains(t, err.Error(), proxyTimeoutEnv)
	assert.Equal(t, DefaultProxyRequestTimeout, ProxyRequestTimeout())

	t.Setenv(proxyTimeoutEnv, "0s")
	require.Error(t, ProxyRequestTimeoutError())
}
