package services

import (
	"bytes"
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
