package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
)

type ProxyService struct {
	loxilbService *LoxiLBService
	client        *http.Client
	identity      GatewayServiceIdentity
}

// ProxyLogEntry represents a proxy request/response log entry
type ProxyLogEntry struct {
	Timestamp    time.Time `json:"timestamp"`
	Type         string    `json:"type"`
	UserID       string    `json:"user_id,omitempty"`
	InstanceID   int       `json:"instance_id"`
	Method       string    `json:"method"`
	OriginalURL  string    `json:"original_url"`
	TargetURL    string    `json:"target_url"`
	RequestSize  int64     `json:"request_size"`
	ResponseCode int       `json:"response_status"`
	ResponseTime int64     `json:"response_time_ms"`
	Error        string    `json:"error,omitempty"`
}

// Sentinel and typed errors ForwardRequest returns, so the handler can
// classify a proxy failure by inspecting the error rather than by matching
// its prose.
//
// The prose-matching that preceded this was silently wrong: every failure of
// client.Do — timeout, refused connection, DNS failure, TLS handshake — was
// flattened to "failed to connect to LoxiLB instance" with the cause
// discarded, so the handler's timeout branch could never be taken and a
// request that merely exceeded the 10s client timeout was reported to the
// operator as 502 "instance unreachable": a positive assertion that the
// instance is down, which is not what a timeout means.
var (
	// ErrProxyReadRequestBody: the caller's body could not be read (400).
	ErrProxyReadRequestBody = errors.New("failed to read request body")
	// ErrProxyCreateRequest: the outbound request could not be constructed (500).
	ErrProxyCreateRequest = errors.New("failed to create request")
	// ErrProxyReadResponse: the instance answered but the body could not be
	// read to completion (502) — a truncated or aborted response.
	ErrProxyReadResponse = errors.New("failed to read response from LoxiLB instance")
)

// ProxyUpstreamError reports that the request left OAM but the transport to
// the LoxiLB instance failed. Err is the original error from http.Client.Do,
// preserved so the handler can tell a timeout from a refused connection from
// a name-resolution or TLS failure. Unwrap makes errors.Is/errors.As reach
// context.DeadlineExceeded, *net.DNSError, syscall.ECONNREFUSED and friends.
type ProxyUpstreamError struct {
	TargetURL string
	Err       error
}

func (e *ProxyUpstreamError) Error() string {
	return fmt.Sprintf("proxy request to LoxiLB instance at %s failed: %v", e.TargetURL, e.Err)
}

func (e *ProxyUpstreamError) Unwrap() error { return e.Err }

// Timeout reports whether the underlying failure was a timeout, satisfying
// the net.Error convention so callers may test it either way.
func (e *ProxyUpstreamError) Timeout() bool {
	var netErr net.Error
	return errors.Is(e.Err, context.DeadlineExceeded) || (errors.As(e.Err, &netErr) && netErr.Timeout())
}

func NewProxyService(loxilbService *LoxiLBService) (*ProxyService, error) {
	identity, err := GatewayServiceIdentityFromEnv()
	if err != nil {
		return nil, err
	}
	return NewProxyServiceWithGatewayIdentity(loxilbService, identity), nil
}

// NewProxyServiceWithGatewayIdentity wires an already validated identity so
// proxy and snapshot clients can share one immutable startup configuration.
func NewProxyServiceWithGatewayIdentity(loxilbService *LoxiLBService, identity GatewayServiceIdentity) *ProxyService {
	// TLS posture for managed instances is centralized in config.InstanceTLSConfig
	// (verify by default; CA-bundle or explicit-insecure opt-in via env).
	//
	// Connections are pooled. Keep-alives were previously disabled outright to
	// avoid handing a request a connection to an instance endpoint that had
	// since been replaced — a real hazard, but one that occurs only when an
	// endpoint changes, while the cost (a fresh TCP, and for managed instances
	// TLS, handshake) was paid on every request forever. Measured by the UI
	// team, a tiny proxied response cost roughly twice the equivalent direct
	// call. Disabling reuse also removed the buffer against transient
	// connection-establishment failures: with every request dialling anew, any
	// SYN loss surfaces to the operator as a 502.
	//
	// The hazard is now handled where it actually arises: CloseIdleConnections
	// is called on the instance mutation paths (see the handlers for instance
	// update/delete and the firmware start/stop/update operations).
	tr := &http.Transport{
		TLSClientConfig:     config.InstanceTLSConfig(),
		DisableKeepAlives:   config.ProxyKeepAlivesDisabled(),
		MaxIdleConns:        proxyMaxIdleConns,
		MaxIdleConnsPerHost: proxyMaxIdleConnsPerHost,
		IdleConnTimeout:     proxyIdleConnTimeout,
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   config.ProxyRequestTimeout(),
	}
	return &ProxyService{
		loxilbService: loxilbService,
		client:        client,
		identity:      identity,
	}
}

// Outbound connection-pool budget for the instance proxy. An OAM manages a
// small number of instances, so a per-host ceiling well above the expected
// concurrent console traffic is enough to keep every request on a warm
// connection without holding sockets open indefinitely.
const (
	proxyMaxIdleConns        = 100
	proxyMaxIdleConnsPerHost = 8
	proxyIdleConnTimeout     = 90 * time.Second
)

// CloseIdleConnections drops every pooled connection to managed instances.
//
// Call it whenever an instance endpoint may have been replaced — an instance
// update or delete, or a firmware start/stop/update that recreates the
// container behind the same address. The next request then dials afresh
// instead of reusing a connection to a container that no longer exists. The
// cost is one cold start on a rare event, rather than a handshake per
// request, which is what disabling keep-alives outright used to charge.
func (p *ProxyService) CloseIdleConnections() {
	p.client.CloseIdleConnections()
}

// GatewayAuthMode returns the non-secret outbound authentication mode for
// health/status reporting.
func (p *ProxyService) GatewayAuthMode() string {
	return p.identity.Mode()
}

// ForwardRequest forwards the request in c to targetPath on the LoxiLB instance
// identified by instanceID.
func (p *ProxyService) ForwardRequest(c *gin.Context, instanceID int, targetPath string) error {
	startTime := time.Now()

	// Fetch LoxiLB instance details
	instance, err := p.loxilbService.FetchLoxiLBInstanceByID(instanceID)
	if err != nil {
		p.logProxyRequest(c, instanceID, "", "", 0, 404, time.Since(startTime).Milliseconds(), fmt.Sprintf("LoxiLB instance not found: %v", err))
		return fmt.Errorf("%w (id %d)", ErrInstanceNotFound, instanceID)
	}

	baseURL := strings.TrimSuffix(instance.ApiEndpoint, "/")

	// Handle path overlap - if the target path starts with the version that's already in ApiEndpoint
	// Extract version from ApiEndpoint (e.g., "v1" from "https://host:port/netlox/v1")
	apiEndpointParts := strings.Split(baseURL, "/")
	if len(apiEndpointParts) > 0 {
		lastPart := apiEndpointParts[len(apiEndpointParts)-1]
		// If target path starts with the same version (with or without leading slash), remove the version from baseURL
		if strings.HasPrefix(targetPath, "/"+lastPart+"/") || strings.HasPrefix(targetPath, "/"+lastPart) {
			baseURL = strings.TrimSuffix(baseURL, "/"+lastPart)
		}
	}

	targetURL := fmt.Sprintf("%s/%s", baseURL, strings.TrimPrefix(targetPath, "/"))

	// Preserve query parameters from original request
	if c.Request.URL.RawQuery != "" {
		targetURL += "?" + c.Request.URL.RawQuery
	}

	// Read request body
	var requestBody []byte
	if c.Request.Body != nil {
		requestBody, err = io.ReadAll(c.Request.Body)
		if err != nil {
			p.logProxyRequest(c, instanceID, c.Request.URL.Path, targetURL, 0, 400, time.Since(startTime).Milliseconds(), fmt.Sprintf("Failed to read request body: %v", err))
			return fmt.Errorf("%w: %w", ErrProxyReadRequestBody, err)
		}
		// Restore body for potential re-reading
		c.Request.Body = io.NopCloser(bytes.NewBuffer(requestBody))
	}

	// Refuse rules that would take over a host endpoint the management plane
	// depends on. This runs before the request leaves OAM: on a converged node
	// an L4 rule on the edge address:port is processed in eBPF ahead of
	// netfilter, so once the gateway has accepted it there is no error to
	// observe — only a console that stopped answering.
	if err := checkReservedEndpoint(config.ReservedEndpoints(), c.Request.Method, targetPath, requestBody); err != nil {
		p.logProxyRequest(c, instanceID, c.Request.URL.Path, targetURL, int64(len(requestBody)), http.StatusConflict, time.Since(startTime).Milliseconds(), err.Error())
		return err
	}

	// Create new request
	req, err := http.NewRequest(c.Request.Method, targetURL, bytes.NewBuffer(requestBody))
	if err != nil {
		p.logProxyRequest(c, instanceID, c.Request.URL.Path, targetURL, int64(len(requestBody)), 500, time.Since(startTime).Milliseconds(), fmt.Sprintf("Failed to create request: %v", err))
		return fmt.Errorf("%w: %w", ErrProxyCreateRequest, err)
	}

	// Copy only explicitly safe end-to-end headers. In particular, browser
	// Authorization/Cookie/API-key credentials must never cross the OAM user
	// identity boundary into the Gateway management plane.
	p.copyHeaders(c.Request.Header, req.Header)
	if err := p.identity.Authorize(req); err != nil {
		p.logProxyRequest(c, instanceID, c.Request.URL.Path, targetURL, int64(len(requestBody)), http.StatusServiceUnavailable, time.Since(startTime).Milliseconds(), "Gateway service identity unavailable")
		return err
	}

	// Set content length if we have a body
	if len(requestBody) > 0 {
		req.ContentLength = int64(len(requestBody))
	}

	// Make the request
	resp, err := p.client.Do(req)
	if err != nil {
		upstreamErr := &ProxyUpstreamError{TargetURL: targetURL, Err: err}
		// Log the status the operator will actually be answered with. Logging
		// 502 for a request that returns 504 would recreate, in the logs, the
		// very confusion this change removes from the response.
		loggedStatus := http.StatusBadGateway
		if upstreamErr.Timeout() {
			loggedStatus = http.StatusGatewayTimeout
		}
		p.logProxyRequest(c, instanceID, c.Request.URL.Path, targetURL, int64(len(requestBody)), loggedStatus, time.Since(startTime).Milliseconds(), fmt.Sprintf("Request failed: %v", err))
		// Carry the cause across the boundary: the handler distinguishes a
		// timeout (504) from an instance that is genuinely unreachable (502),
		// and it can only do so if the original error survives.
		return upstreamErr
	}
	defer resp.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		p.logProxyRequest(c, instanceID, c.Request.URL.Path, targetURL, int64(len(requestBody)), 502, time.Since(startTime).Milliseconds(), fmt.Sprintf("Failed to read response body: %v", err))
		return fmt.Errorf("%w: %w", ErrProxyReadResponse, err)
	}

	// Copy response headers
	for key, values := range resp.Header {
		for _, value := range values {
			c.Header(key, value)
		}
	}

	// Log successful proxy request
	p.logProxyRequest(c, instanceID, c.Request.URL.Path, targetURL, int64(len(requestBody)), resp.StatusCode, time.Since(startTime).Milliseconds(), "")

	// Set status and return response
	c.Status(resp.StatusCode)
	c.Data(resp.StatusCode, resp.Header.Get("Content-Type"), responseBody)

	return nil
}

// safeGatewayRequestHeaders is an allowlist, rather than a denylist, because
// new browser credentials must not become transitively trusted by Gateway.
var safeGatewayRequestHeaders = map[string]bool{
	"Accept":           true,
	"Accept-Encoding":  true,
	"Accept-Language":  true,
	"Cache-Control":    true,
	"Content-Encoding": true,
	"Content-Type":     true,
	"If-Match":         true,
	"If-None-Match":    true,
	"X-Correlation-Id": true,
	"X-Request-Id":     true,
}

/*
copyHeaders copies explicitly safe HTTP headers from source to destination.
*/
func (p *ProxyService) copyHeaders(src, dst http.Header) {
	for key, values := range src {
		if safeGatewayRequestHeaders[http.CanonicalHeaderKey(key)] {
			for _, value := range values {
				dst.Add(http.CanonicalHeaderKey(key), value)
			}
		}
	}
}

/*
logProxyRequest logs proxy request details for monitoring and debugging
*/
func (p *ProxyService) logProxyRequest(c *gin.Context, instanceID int, originalURL, targetURL string, requestSize int64, responseCode int, responseTimeMs int64, errorMsg string) {
	// Extract user ID from context if available
	userID := ""
	if userIDValue, exists := c.Get("user_id"); exists {
		if uid, ok := userIDValue.(int); ok {
			userID = strconv.Itoa(uid)
		}
	}

	logEntry := ProxyLogEntry{
		Timestamp:    time.Now(),
		Type:         "proxy_request",
		UserID:       userID,
		InstanceID:   instanceID,
		Method:       c.Request.Method,
		OriginalURL:  originalURL,
		TargetURL:    targetURL,
		RequestSize:  requestSize,
		ResponseCode: responseCode,
		ResponseTime: responseTimeMs,
		Error:        errorMsg,
	}

	// Log based on response code
	if responseCode >= 400 || errorMsg != "" {
		utils.LogError(fmt.Sprintf("Proxy request failed - Instance: %d, Method: %s, URL: %s, Status: %d, Error: %s, Time: %dms",
			instanceID, c.Request.Method, originalURL, responseCode, errorMsg, responseTimeMs))
	} else {
		utils.LogInfo(fmt.Sprintf("Proxy request successful - Instance: %d, Method: %s, URL: %s, Target URL: %s, Status: %d, Time: %dms",
			instanceID, c.Request.Method, originalURL, targetURL, responseCode, responseTimeMs))
	}

	// logEntry can be forwarded to a database or external logging system;
	// it is currently served by the logging utility used above.
	_ = logEntry
}

// ReservedEndpointError reports that a load-balancer rule was refused because
// its VIP would collide with a host endpoint listed in OAM_RESERVED_ENDPOINTS.
// The handler maps it to 409 Conflict.
type ReservedEndpointError struct {
	VIP      string                  // the address the rejected rule asked for
	Port     int                     // the port the rejected rule asked for
	Protocol string                  // the protocol the rejected rule asked for
	Reserved config.ReservedEndpoint // the reservation it collided with
}

func (e *ReservedEndpointError) Error() string {
	vip := e.VIP
	if vip == "" {
		vip = "*"
	}
	return fmt.Sprintf(
		"load-balancer VIP %s:%d/%s collides with reserved host endpoint %s "+
			"(OAM_RESERVED_ENDPOINTS); choose a different VIP address or port",
		vip, e.Port, e.Protocol, e.Reserved)
}

// lbRuleEnvelope is the slice of the LoxiLB load-balancer rule body the guard
// needs. Everything else in the rule is passed through untouched.
type lbRuleEnvelope struct {
	ServiceArguments struct {
		ExternalIP string          `json:"externalIP"`
		Host       string          `json:"host"`
		Port       json.RawMessage `json:"port"`
		Protocol   string          `json:"protocol"`
	} `json:"serviceArguments"`
}

// checkReservedEndpoint refuses a load-balancer rule whose VIP would take over a
// host endpoint the management plane depends on.
//
// It runs on the OAM side because that is the path the console and every
// scripted client use. It is NOT airtight on its own: a caller with network
// access to the gateway's own REST API can still program the rule directly. On a
// converged node, keep the gateway's plaintext listener on loopback (HOST=
// 127.0.0.1) and restrict its TLS listener, so OAM is the only reachable path.
//
// Fails open on request shapes it does not recognise — a body with no usable
// port is left for the gateway to validate — but fails closed on the address:
// once a reserved port is in play, a VIP that cannot be parsed is refused
// rather than waved through.
func checkReservedEndpoint(reserved []config.ReservedEndpoint, method, targetPath string, body []byte) error {
	if len(reserved) == 0 || len(body) == 0 {
		return nil
	}
	if method != http.MethodPost && method != http.MethodPut {
		return nil
	}
	if !strings.Contains(strings.ToLower(targetPath), "config/loadbalancer") {
		return nil
	}

	var rule lbRuleEnvelope
	if err := json.Unmarshal(body, &rule); err != nil {
		return nil // not a shape we understand; the gateway will validate it
	}

	port, ok := parseRulePort(rule.ServiceArguments.Port)
	if !ok {
		return nil
	}
	proto := rule.ServiceArguments.Protocol

	// externalIP is the VIP; host is the address the L7 fullproxy (mode 4)
	// actually binds. They are normally equal, but check both so a rule cannot
	// slip through by naming the edge address in only one of them.
	for _, vip := range []string{rule.ServiceArguments.ExternalIP, rule.ServiceArguments.Host} {
		if vip == "" && rule.ServiceArguments.ExternalIP != "" {
			continue // host omitted; externalIP already covered it
		}
		if match, hit := config.MatchReservedEndpoint(reserved, vip, port, proto); hit {
			return &ReservedEndpointError{VIP: vip, Port: port, Protocol: proto, Reserved: match}
		}
	}
	return nil
}

// parseRulePort reads the rule's port, which LoxiLB accepts as either a JSON
// number or a quoted string.
func parseRulePort(raw json.RawMessage) (int, bool) {
	s := strings.Trim(strings.TrimSpace(string(raw)), `"`)
	if s == "" {
		return 0, false
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0, false
	}
	return n, true
}
