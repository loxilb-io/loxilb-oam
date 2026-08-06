package models_test

import (
	"strings"
	"testing"

	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/stretchr/testify/assert"
)

// The instance record is the proxy's target definition: every field is
// interpolated into {protocol}://{host}:{port}/netlox/{version}. These tests
// push on the inputs that would produce a malformed — or attacker-chosen —
// endpoint, not just the happy path.

func validFields() models.InstanceFields {
	return models.InstanceFields{
		Name:        "gw-1",
		Host:        "192.0.2.10",
		Port:        "8091",
		Protocol:    "https",
		Description: "primary",
		Version:     "v1",
		Cimage:      "ghcr.io/loxilb-io/loxilb",
		Ctag:        "latest",
	}
}

func TestInstanceFieldsValidateOK(t *testing.T) {
	assert.Nil(t, validFields().Validate())
}

func TestInstanceNameRules(t *testing.T) {
	cases := []struct {
		name  string
		valid bool
	}{
		{"gw-1", true},
		{"GW1", true},
		{"a.b_c-d", true},
		{"1gw", true},
		{"", false},
		{"-gw", false},  // must start alphanumeric
		{".gw", false},  //
		{"gw 1", false}, // anything below would need escaping in ?name=
		{"gw/1", false},
		{"gw?1", false},
		{"gw#1", false},
		{"gw&1", false},
		{"gw%20", false},
	}
	for _, tc := range cases {
		err := models.ValidateInstanceName(tc.name)
		if tc.valid {
			assert.Nil(t, err, "expected %q to be valid", tc.name)
		} else {
			assert.NotNil(t, err, "expected %q to be rejected", tc.name)
			assert.Equal(t, "name", err.Field)
		}
	}
	assert.NotNil(t, models.ValidateInstanceName(strings.Repeat("a", 64)))
	assert.Nil(t, models.ValidateInstanceName(strings.Repeat("a", 63)))
}

func TestInstanceHostRules(t *testing.T) {
	for _, host := range []string{"192.0.2.10", "10.0.0.1", "gw1.example.com", "localhost", "[2001:db8::1]", "[::1]"} {
		assert.True(t, models.IsValidInstanceHost(host), "expected %q to be a valid host", host)
		assert.Nil(t, models.ValidateInstanceHost(host), "expected %q to validate", host)
	}
	for _, host := range []string{"", "192.0.2.999", "192.0.2.010", "256.0.0.1", "gw_1.example.com", "-gw.example.com", "example.com.", "[2001:db8::1", "[not:an:ip]"} {
		assert.NotNil(t, models.ValidateInstanceHost(host), "expected %q to be rejected", host)
	}
}

func TestInstanceHostExplainsTheMistake(t *testing.T) {
	// A generic "invalid host" would leave the operator guessing which of
	// these four very different mistakes they made.
	assert.Contains(t, models.ValidateInstanceHost("https://192.0.2.10").Message, "scheme")
	assert.Contains(t, models.ValidateInstanceHost("192.0.2.10/netlox").Message, "path")
	assert.Contains(t, models.ValidateInstanceHost("192.0.2.10 ").Message, "whitespace")
	assert.Contains(t, models.ValidateInstanceHost("2001:db8::1").Message, "brackets")
}

func TestInstancePortRules(t *testing.T) {
	for _, port := range []string{"1", "80", "8091", "65535"} {
		assert.Nil(t, models.ValidateInstancePort(port), port)
	}
	for _, port := range []string{"", "0", "-1", "65536", "99999", "80.5", "8091a", " 8091"} {
		assert.NotNil(t, models.ValidateInstancePort(port), port)
	}
	// '08091' and '8091' reach the same socket but store as different rows.
	assert.Contains(t, models.ValidateInstancePort("08091").Message, "leading zeros")
}

func TestInstanceProtocolRules(t *testing.T) {
	assert.Nil(t, models.ValidateInstanceProtocol("http"))
	assert.Nil(t, models.ValidateInstanceProtocol("https"))
	for _, proto := range []string{"", "HTTPS", "ftp", "ws"} {
		assert.NotNil(t, models.ValidateInstanceProtocol(proto), proto)
	}
}

func TestInstanceVersionBlocksTraversal(t *testing.T) {
	assert.Nil(t, models.ValidateInstanceVersion("v1"))
	assert.Nil(t, models.ValidateInstanceVersion("v2"))
	// The version is the last path segment of the endpoint — a traversal or
	// an injected segment here re-points the proxy.
	for _, version := range []string{"", "../../config", "v1/..", "v1/config", "/v1", ".."} {
		assert.NotNil(t, models.ValidateInstanceVersion(version), version)
	}
	assert.NotNil(t, models.ValidateInstanceVersion(strings.Repeat("v", 17)))
}

func TestInstanceImageAndTagRules(t *testing.T) {
	for _, image := range []string{"loxilb", "loxilb-io/loxilb", "ghcr.io/loxilb-io/loxilb", "registry.local:5000/loxilb-io/loxilb"} {
		assert.Nil(t, models.ValidateInstanceImage(image), image)
	}
	assert.Contains(t, models.ValidateInstanceImage("ghcr.io/loxilb-io/loxilb:latest").Message, "ctag")
	assert.Contains(t, models.ValidateInstanceImage("ghcr.io/lox ilb").Message, "whitespace")
	assert.NotNil(t, models.ValidateInstanceImage(""))
	assert.NotNil(t, models.ValidateInstanceImage("ghcr.io/LoxiLB"))
	assert.NotNil(t, models.ValidateInstanceImage(strings.Repeat("a", 256)))

	for _, tag := range []string{"latest", "v0.9.7", "u24", "2026-08-05", "_x"} {
		assert.Nil(t, models.ValidateInstanceTag(tag), tag)
	}
	for _, tag := range []string{"", "-latest", ".latest", "lat est", "lat/est", strings.Repeat("a", 129)} {
		assert.NotNil(t, models.ValidateInstanceTag(tag), tag)
	}
}

func TestInstanceDescriptionCap(t *testing.T) {
	assert.Nil(t, models.ValidateInstanceDescription(strings.Repeat("x", 1024)))
	assert.NotNil(t, models.ValidateInstanceDescription(strings.Repeat("x", 1025)))
}

func TestNormalizeTrimsAndDefaults(t *testing.T) {
	fields := models.InstanceFields{
		Name:     "  gw-1 ",
		Host:     " 192.0.2.10 ",
		Port:     " 8091 ",
		Protocol: " HTTPS ",
		Cimage:   " ghcr.io/loxilb-io/loxilb ",
		Ctag:     " latest ",
	}
	fields.Normalize()

	assert.Equal(t, "gw-1", fields.Name)
	assert.Equal(t, "192.0.2.10", fields.Host)
	assert.Equal(t, "8091", fields.Port)
	assert.Equal(t, "https", fields.Protocol) // lowercased, not rejected
	assert.Equal(t, "v1", fields.Version)     // historical default preserved
	assert.Nil(t, fields.Validate())
}

func TestNormalizeDoesNotInventAProtocol(t *testing.T) {
	// Defaulting an absent protocol to https is how an http instance used to
	// get its endpoint silently rewritten to a port that does not speak TLS.
	fields := validFields()
	fields.Protocol = ""
	fields.Normalize()

	assert.Equal(t, "", fields.Protocol)
	err := fields.Validate()
	if assert.NotNil(t, err) {
		assert.Equal(t, "protocol", err.Field)
	}
}

func TestValidateReportsTheOffendingField(t *testing.T) {
	cases := map[string]func(*models.InstanceFields){
		"name":        func(f *models.InstanceFields) { f.Name = "" },
		"host":        func(f *models.InstanceFields) { f.Host = "http://x" },
		"port":        func(f *models.InstanceFields) { f.Port = "0" },
		"protocol":    func(f *models.InstanceFields) { f.Protocol = "ftp" },
		"version":     func(f *models.InstanceFields) { f.Version = "../x" },
		"cimage":      func(f *models.InstanceFields) { f.Cimage = "" },
		"ctag":        func(f *models.InstanceFields) { f.Ctag = "-bad" },
		"description": func(f *models.InstanceFields) { f.Description = strings.Repeat("x", 2000) },
	}
	for field, mutate := range cases {
		fields := validFields()
		mutate(&fields)
		err := fields.Validate()
		if assert.NotNil(t, err, "expected %s to be rejected", field) {
			assert.Equal(t, field, err.Field)
			assert.Contains(t, err.Error(), field)
		}
	}
}

func TestAPIEndpointMatchesTheStoredFormat(t *testing.T) {
	// Must stay byte-identical to what the service writes, or the uniqueness
	// pre-check would compare against a string that is never stored.
	assert.Equal(t, "https://192.0.2.10:8091/netlox/v1", validFields().APIEndpoint())

	v6 := validFields()
	v6.Host = "[2001:db8::1]"
	v6.Protocol = "http"
	assert.Equal(t, "http://[2001:db8::1]:8091/netlox/v1", v6.APIEndpoint())
}
