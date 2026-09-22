package config

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
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
