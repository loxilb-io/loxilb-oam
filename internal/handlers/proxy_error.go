package handlers

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"syscall"

	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/services"
)

// classifyProxyError maps a failure from ProxyService.ForwardRequest onto the
// status and operator-facing message the console shows.
//
// It branches on the error, never on the error's text. The previous
// implementation matched prose — `strings.Contains(err.Error(), "timeout")` —
// against messages the service layer had already flattened, so the 504 branch
// was unreachable and a request that merely exceeded the proxy timeout was
// reported as 502 "LoxiLB instance unreachable": a positive assertion that the
// instance is down. That assertion can be false, and is expensive to diagnose,
// because the two UI degradations that follow a dropped proxied request (an
// Add LB dialog with no fields when /meta is lost, a plain-loxilb form when
// /version is lost) are impossible to explain to an operator who has been told
// the instance is unreachable.
//
// String matching is also what let it rot unnoticed: nothing fails when the
// strings drift apart, the branch simply stops being taken.
//
// detail is a curated cause classification, not the raw error — the raw error
// goes to the proxy log, which is where an operator with access to the host
// should read it.
func classifyProxyError(err error) (int, string, string) {
	var reservedErr *services.ReservedEndpointError
	var upstreamErr *services.ProxyUpstreamError

	switch {
	// Surface the guard's own message: it names the offending VIP and the
	// reservation it hit, which is what the operator needs to fix .env or
	// pick another port. The generic default below would hide both.
	case errors.As(err, &reservedErr):
		return http.StatusConflict, err.Error(), ""

	case errors.Is(err, services.ErrGatewayServiceIdentityUnavailable):
		return http.StatusServiceUnavailable, "Gateway service identity unavailable", ""

	case errors.Is(err, services.ErrInstanceNotFound):
		return http.StatusNotFound, services.ErrInstanceNotFound.Error(), ""

	case errors.Is(err, services.ErrProxyReadRequestBody):
		return http.StatusBadRequest, "Failed to read request body", ""

	case errors.Is(err, services.ErrProxyCreateRequest):
		return http.StatusInternalServerError, "Proxy request failed", ""

	case errors.Is(err, services.ErrProxyReadResponse):
		// The instance answered, then the body was truncated. It is emphatically
		// not unreachable, so do not say so.
		return http.StatusBadGateway, "Incomplete response from LoxiLB instance",
			"the instance answered but the response body ended early"

	case errors.As(err, &upstreamErr):
		return classifyUpstreamError(upstreamErr)

	default:
		return http.StatusInternalServerError, "Proxy request failed", ""
	}
}

// classifyUpstreamError distinguishes the transport failures that used to be
// collapsed into one message. The ordering matters: a timeout must be tested
// before the generic unreachable fallback, because a timed-out dial also
// satisfies several of the broader conditions below it.
func classifyUpstreamError(upstreamErr *services.ProxyUpstreamError) (int, string, string) {
	var dnsErr *net.DNSError
	var certErr *tls.CertificateVerificationError
	var recordErr tls.RecordHeaderError

	switch {
	// A timeout means "no answer within the budget", which is not the same
	// claim as "the instance is down" — say only what is known.
	case upstreamErr.Timeout():
		return http.StatusGatewayTimeout, "Request to LoxiLB instance timed out",
			fmt.Sprintf("no response within the %s proxy timeout", config.ProxyRequestTimeout())

	case errors.Is(upstreamErr, context.Canceled):
		// The console navigated away or the client hung up. Nothing is wrong
		// with the instance; 499 is nginx's, so use the closest standard code.
		return http.StatusBadGateway, "Request to LoxiLB instance was cancelled",
			"the client closed the connection before the instance replied"

	case errors.Is(upstreamErr, syscall.ECONNREFUSED):
		return http.StatusBadGateway, "LoxiLB instance unreachable",
			"connection refused by the instance endpoint"

	case errors.Is(upstreamErr, syscall.ECONNRESET), errors.Is(upstreamErr, io.ErrUnexpectedEOF):
		return http.StatusBadGateway, "Connection to LoxiLB instance was reset",
			"the instance closed the connection before replying"

	case errors.As(upstreamErr, &dnsErr):
		return http.StatusBadGateway, "LoxiLB instance address could not be resolved",
			fmt.Sprintf("DNS lookup failed for %q", dnsErr.Name)

	case errors.As(upstreamErr, &certErr), errors.As(upstreamErr, &recordErr):
		// Saying "unreachable" here sends the operator to look at the network
		// when the reachable instance is presenting a certificate OAM will not
		// accept. See OAM_INSTANCE_CA_BUNDLE.
		return http.StatusBadGateway, "TLS handshake with LoxiLB instance failed",
			"the instance is reachable but its TLS certificate was not accepted"

	default:
		return http.StatusBadGateway, "LoxiLB instance unreachable",
			"the connection to the instance endpoint could not be established"
	}
}

// proxyErrorBody renders a classified proxy error. The "error" field keeps the
// shape every existing consumer reads; "detail" is additive and carries the
// cause that used to be discarded at the service boundary.
func proxyErrorBody(message, detail string) map[string]any {
	body := map[string]any{"error": message}
	if detail != "" {
		body["detail"] = detail
	}
	return body
}
