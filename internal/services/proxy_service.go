package services

import (
	"bytes"
	"encoding/json"
	"fmt"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

type ProxyService struct {
	loxilbService *LoxiLBService
	client        *http.Client
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

func NewProxyService(loxilbService *LoxiLBService) *ProxyService {
	// TLS posture for managed instances is centralized in config.InstanceTLSConfig
	// (verify by default; CA-bundle or explicit-insecure opt-in via env).
	// DisableKeepAlives prevents connection reuse issues when instance endpoints change.
	tr := &http.Transport{
		TLSClientConfig:     config.InstanceTLSConfig(),
		DisableKeepAlives:   true, // Prevent connection pooling/reuse
		MaxIdleConns:        0,    // No idle connection pooling
		MaxIdleConnsPerHost: 0,    // No idle connections per host
		IdleConnTimeout:     0,    // Close idle connections immediately
	}
	client := &http.Client{
		Transport: tr,
		Timeout:   10 * time.Second,
	}
	return &ProxyService{
		loxilbService: loxilbService,
		client:        client,
	}
}

// ForwardRequest forwards the request in c to targetPath on the LoxiLB instance
// identified by instanceID.
func (p *ProxyService) ForwardRequest(c *gin.Context, instanceID int, targetPath string) error {
	startTime := time.Now()

	// Fetch LoxiLB instance details
	instance, err := p.loxilbService.FetchLoxiLBInstanceByID(instanceID)
	if err != nil {
		p.logProxyRequest(c, instanceID, "", "", 0, 404, time.Since(startTime).Milliseconds(), "LoxiLB instance not found")
		return fmt.Errorf("LoxiLB instance not found")
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
			p.logProxyRequest(c, instanceID, c.Request.URL.Path, targetURL, 0, 400, time.Since(startTime).Milliseconds(), "Failed to read request body")
			return fmt.Errorf("failed to read request body")
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
		p.logProxyRequest(c, instanceID, c.Request.URL.Path, targetURL, int64(len(requestBody)), 500, time.Since(startTime).Milliseconds(), "Failed to create request")
		return fmt.Errorf("failed to create request")
	}

	// Copy relevant headers (excluding hop-by-hop headers)
	p.copyHeaders(c.Request.Header, req.Header)

	// Set content length if we have a body
	if len(requestBody) > 0 {
		req.ContentLength = int64(len(requestBody))
	}

	// Make the request
	resp, err := p.client.Do(req)
	if err != nil {
		p.logProxyRequest(c, instanceID, c.Request.URL.Path, targetURL, int64(len(requestBody)), 502, time.Since(startTime).Milliseconds(), fmt.Sprintf("Request failed: %v", err))
		return fmt.Errorf("failed to connect to LoxiLB instance")
	}
	defer resp.Body.Close()

	// Read response body
	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		p.logProxyRequest(c, instanceID, c.Request.URL.Path, targetURL, int64(len(requestBody)), 502, time.Since(startTime).Milliseconds(), "Failed to read response body")
		return fmt.Errorf("failed to read response")
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

/*
copyHeaders copies HTTP headers from source to destination, excluding hop-by-hop headers
*/
func (p *ProxyService) copyHeaders(src, dst http.Header) {
	// Headers that should not be forwarded
	hopByHopHeaders := map[string]bool{
		"Connection":          true,
		"Keep-Alive":          true,
		"Proxy-Authenticate":  true,
		"Proxy-Authorization": true,
		"Te":                  true,
		"Trailers":            true,
		"Transfer-Encoding":   true,
		"Upgrade":             true,
	}

	for key, values := range src {
		if !hopByHopHeaders[key] {
			for _, value := range values {
				dst.Add(key, value)
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
