package services

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// The generic instance proxy reaches any path of the Gateway's management API
// with OAM's own service identity, which the Gateway treats as an
// administrator. OAM is therefore the only boundary between an OAM role and
// the Gateway, and it has to decide on the path as well as the method.
//
// Two rules keep that decision honest:
//
//   - the path is reduced to one canonical form before anything looks at it,
//     and the request that leaves OAM is built from that same form. The
//     Gateway's router cleans dot segments and trailing slashes, so a path
//     OAM classified in any other spelling would be a different path by the
//     time the Gateway routed it.
//   - the families a role may reach are listed. A path that is not listed is
//     admin-only, so a Gateway API added later is not open to lower roles
//     until someone decides that it should be.

// gatewayAPIVersion is the version segment of the Gateway API root
// (/netlox/v1). Callers may write it or leave it out.
const gatewayAPIVersion = "v1"

// ErrGatewayPathInvalid reports a proxy path OAM will not forward in any
// spelling: the handler answers 400 and nothing is sent to the Gateway.
var ErrGatewayPathInvalid = errors.New("invalid gateway path")

// GatewayPath is a proxied request's path relative to the Gateway API root,
// in the canonical form that is both authorized and forwarded. The zero value
// is not a valid path; obtain one from CanonicalGatewayPath.
type GatewayPath struct {
	rel       string // "/config/loadbalancer/all"; "/" is the API root
	versioned bool   // the caller wrote the version segment
}

// String returns the path relative to the Gateway API root.
func (p GatewayPath) String() string { return p.rel }

// CanonicalGatewayPath reduces the proxy route's wildcard to canonical form.
// decoded is the wildcard as the router matched it (percent-decoded, starting
// with "/"); escaped is the request path as it was sent, which is the only
// place an encoded separator is still visible.
//
// It refuses rather than repairs. A path with dot segments, empty segments, an
// encoded separator or characters that change meaning in a URL has no honest
// reading, and no client of the proxy sends one.
func CanonicalGatewayPath(decoded, escaped string) (GatewayPath, error) {
	lowered := strings.ToLower(escaped)
	if strings.Contains(lowered, "%2f") || strings.Contains(lowered, "%5c") {
		return GatewayPath{}, fmt.Errorf("%w: encoded path separator", ErrGatewayPathInvalid)
	}
	if !strings.HasPrefix(decoded, "/") || !utf8.ValidString(decoded) {
		return GatewayPath{}, ErrGatewayPathInvalid
	}
	for _, r := range decoded {
		if r < 0x20 || r == 0x7f || r == '\\' || r == '?' || r == '#' {
			return GatewayPath{}, fmt.Errorf("%w: character not allowed in a path", ErrGatewayPathInvalid)
		}
	}

	// One trailing slash is the same resource to the Gateway; more than one
	// is an empty segment and is refused below.
	trimmed := strings.TrimSuffix(decoded, "/")
	if trimmed == "" {
		return GatewayPath{rel: "/"}, nil
	}
	segments := strings.Split(trimmed[1:], "/")
	for _, segment := range segments {
		if segment == "" || segment == "." || segment == ".." {
			return GatewayPath{}, fmt.Errorf("%w: empty or dot segment", ErrGatewayPathInvalid)
		}
	}

	path := GatewayPath{}
	if segments[0] == gatewayAPIVersion {
		path.versioned = true
		segments = segments[1:]
	}
	path.rel = "/" + strings.Join(segments, "/")
	return path, nil
}

// gatewayTargetURL builds the outbound URL from the instance's API endpoint
// and the canonical path. The path is set on a url.URL, never concatenated
// into a string, so nothing in it can become a query or a fragment.
func gatewayTargetURL(apiEndpoint string, path GatewayPath, rawQuery string) (string, error) {
	if path.rel == "" {
		return "", ErrGatewayPathInvalid
	}
	target, err := url.Parse(strings.TrimSuffix(apiEndpoint, "/"))
	if err != nil {
		return "", err
	}
	base := strings.TrimSuffix(target.Path, "/")
	// An endpoint is normally registered with the version in it. A caller
	// that also wrote the version means the same API root, not a second one.
	if path.versioned && !strings.HasSuffix(base, "/"+gatewayAPIVersion) {
		base += "/" + gatewayAPIVersion
	}
	target.Path = base + strings.TrimSuffix(path.rel, "/")
	target.RawPath = ""
	target.RawQuery = rawQuery
	target.Fragment = ""
	return target.String(), nil
}

// GatewayAccess is what a proxied request requires of the caller. The zero
// value denies, so an unset decision cannot let a request through.
type GatewayAccess int

const (
	// GatewayAccessDenied: not available through the proxy to any role.
	GatewayAccessDenied GatewayAccess = iota
	// GatewayAccessAdmin: administrators only.
	GatewayAccessAdmin
	// GatewayAccessOperator: administrators and operators.
	GatewayAccessOperator
	// GatewayAccessAuthenticated: every authenticated role.
	GatewayAccessAuthenticated
)

// GatewayMethodAllowed reports whether the proxy forwards the method at all.
// The route is registered for every method, which would otherwise include
// TRACE and CONNECT. OPTIONS never arrives: the CORS middleware answers it.
func GatewayMethodAllowed(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}

// gatewayDocumentFamilies are the whole-configuration endpoints under
// /config. The document they read and write carries IPsec and certificate
// secrets, and writing it replaces the Gateway's configuration; OAM's own
// snapshot routes are admin-only for the same reason.
var gatewayDocumentFamilies = map[string]bool{
	"snapshot": true,
	"export":   true,
	"restore":  true,
	"import":   true,
	"persist":  true,
}

// gatewayConfigFamilies are the /config families of day-to-day operation:
// every role reads them, operators and administrators change them.
var gatewayConfigFamilies = map[string]bool{
	"ai": true, "bfd": true, "bgp": true, "cert": true, "cistate": true,
	"conntrack": true, "cors": true, "dpu": true, "endpoint": true,
	"endpointhoststate": true, "fdb": true, "firewall": true, "gpu": true,
	"halfclose": true, "ipfilter": true, "ipsec": true, "ipv4address": true,
	"ipv6address": true, "l4trace": true, "l7policy": true,
	"llamafirewall": true, "loadbalancer": true, "metrics": true,
	"mirror": true, "neighbor": true, "opa": true, "params": true,
	"pii": true, "policy": true, "port": true, "route": true,
	"securityrate": true, "session": true, "sessionulcl": true, "trace": true,
	"tunnel": true, "vlan": true, "worker": true,
}

// gatewayOperationalFamilies are the top-level families treated like the
// /config families above.
var gatewayOperationalFamilies = map[string]bool{
	"diagnostics": true, "maintenance": true, "meta": true, "metrics": true,
	"nodegraph": true, "sni": true, "status": true, "version": true,
}

// ClassifyGatewayRequest decides what a proxied request requires. Matching is
// by whole segment and case-sensitive, as the Gateway's router is.
func ClassifyGatewayRequest(method string, path GatewayPath) GatewayAccess {
	if path.rel == "" {
		return GatewayAccessDenied
	}
	segments := strings.Split(strings.TrimPrefix(path.rel, "/"), "/")
	family, sub := segments[0], ""
	if len(segments) > 1 {
		sub = segments[1]
	}
	read := method == http.MethodGet || method == http.MethodHead

	standard := func() GatewayAccess {
		if read {
			return GatewayAccessAuthenticated
		}
		return GatewayAccessOperator
	}

	switch family {
	case "auth":
		// Gateway accounts and its management token. Changing either through
		// OAM's identity is a takeover of the Gateway, and of OAM's access to
		// it.
		return GatewayAccessDenied

	case "config":
		switch {
		case gatewayDocumentFamilies[sub]:
			return GatewayAccessAdmin
		case gatewayConfigFamilies[sub]:
			return standard()
		}

	case "audit":
		switch {
		case !read:
			// Retention, collectors and rotation decide what the audit trail
			// keeps, so changing them is not day-to-day operation.
			return GatewayAccessAdmin
		case sub == "status" && len(segments) == 2:
			return GatewayAccessAuthenticated
		default:
			return GatewayAccessOperator
		}

	case "logs", "log-archives":
		// A process log can carry anything the code paths touched, which is
		// why OAM's own log is not open to every role either.
		if read {
			return GatewayAccessOperator
		}

	default:
		if gatewayOperationalFamilies[family] {
			return standard()
		}
	}
	return GatewayAccessAdmin
}
