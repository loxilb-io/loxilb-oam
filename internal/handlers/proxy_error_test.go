package handlers

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"syscall"
	"testing"
	"time"

	"encoding/json"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/services"
)

// upstream builds the error ForwardRequest returns for a transport failure.
func upstream(cause error) error {
	return &services.ProxyUpstreamError{
		TargetURL: "http://gateway.test/netlox/v1/meta",
		Err:       &url.Error{Op: "GET", URL: "http://gateway.test/netlox/v1/meta", Err: cause},
	}
}

func TestClassifyProxyError(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantError  string
	}{
		{
			// The regression this whole change exists for: a request that
			// exceeded the proxy timeout used to be reported as 502
			// "LoxiLB instance unreachable" — a positive assertion that the
			// instance is down, which a timeout does not establish.
			name:       "client timeout is 504, not unreachable",
			err:        upstream(context.DeadlineExceeded),
			wantStatus: http.StatusGatewayTimeout,
			wantError:  "Request to LoxiLB instance timed out",
		},
		{
			name:       "net.Error timeout is 504",
			err:        upstream(&net.DNSError{Name: "gateway.test", IsTimeout: true}),
			wantStatus: http.StatusGatewayTimeout,
			wantError:  "Request to LoxiLB instance timed out",
		},
		{
			name:       "connection refused is genuinely unreachable",
			err:        upstream(syscall.ECONNREFUSED),
			wantStatus: http.StatusBadGateway,
			wantError:  "LoxiLB instance unreachable",
		},
		{
			name:       "dns failure names resolution, not reachability",
			err:        upstream(&net.DNSError{Name: "gateway.test", IsNotFound: true}),
			wantStatus: http.StatusBadGateway,
			wantError:  "LoxiLB instance address could not be resolved",
		},
		{
			name:       "tls rejection is not a network problem",
			err:        upstream(&tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}),
			wantStatus: http.StatusBadGateway,
			wantError:  "TLS handshake with LoxiLB instance failed",
		},
		{
			name:       "connection reset",
			err:        upstream(syscall.ECONNRESET),
			wantStatus: http.StatusBadGateway,
			wantError:  "Connection to LoxiLB instance was reset",
		},
		{
			name:       "cancelled request is not the instance's fault",
			err:        upstream(context.Canceled),
			wantStatus: http.StatusBadGateway,
			wantError:  "Request to LoxiLB instance was cancelled",
		},
		{
			name:       "unknown transport failure falls back to unreachable",
			err:        upstream(errors.New("some novel transport failure")),
			wantStatus: http.StatusBadGateway,
			wantError:  "LoxiLB instance unreachable",
		},
		{
			name:       "truncated response is not unreachable",
			err:        fmt.Errorf("%w: %w", services.ErrProxyReadResponse, io.ErrUnexpectedEOF),
			wantStatus: http.StatusBadGateway,
			wantError:  "Incomplete response from LoxiLB instance",
		},
		{
			name:       "missing instance",
			err:        fmt.Errorf("%w (id 7)", services.ErrInstanceNotFound),
			wantStatus: http.StatusNotFound,
			wantError:  services.ErrInstanceNotFound.Error(),
		},
		{
			name:       "unreadable request body",
			err:        fmt.Errorf("%w: %w", services.ErrProxyReadRequestBody, io.ErrUnexpectedEOF),
			wantStatus: http.StatusBadRequest,
			wantError:  "Failed to read request body",
		},
		{
			name:       "request construction failure",
			err:        fmt.Errorf("%w: %w", services.ErrProxyCreateRequest, errors.New("bad method")),
			wantStatus: http.StatusInternalServerError,
			wantError:  "Proxy request failed",
		},
		{
			name:       "gateway identity unavailable",
			err:        fmt.Errorf("wrapped: %w", services.ErrGatewayServiceIdentityUnavailable),
			wantStatus: http.StatusServiceUnavailable,
			wantError:  "Gateway service identity unavailable",
		},
		{
			name:       "unclassified error",
			err:        errors.New("something else entirely"),
			wantStatus: http.StatusInternalServerError,
			wantError:  "Proxy request failed",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			status, message, _ := classifyProxyError(tc.err)
			assert.Equal(t, tc.wantStatus, status)
			assert.Equal(t, tc.wantError, message)
		})
	}
}

// The reserved-endpoint guard's own message names the offending VIP and the
// reservation it hit; the generic default would hide both.
func TestClassifyProxyErrorSurfacesReservedEndpointMessage(t *testing.T) {
	reserved := &services.ReservedEndpointError{
		VIP:      "10.0.0.1",
		Port:     443,
		Protocol: "tcp",
		Reserved: config.ReservedEndpoint{},
	}

	status, message, _ := classifyProxyError(fmt.Errorf("guard: %w", reserved))
	assert.Equal(t, http.StatusConflict, status)
	assert.Contains(t, message, "10.0.0.1")
	assert.Contains(t, message, "OAM_RESERVED_ENDPOINTS")
}

// A timeout must never be described to an operator as the instance being down.
func TestTimeoutIsNeverReportedAsUnreachable(t *testing.T) {
	_, message, detail := classifyProxyError(upstream(context.DeadlineExceeded))
	assert.NotContains(t, message, "unreachable")
	assert.Contains(t, detail, config.ProxyRequestTimeout().String())
}

func TestProxyErrorBodyOmitsEmptyDetail(t *testing.T) {
	assert.Equal(t, map[string]any{"error": "boom"}, proxyErrorBody("boom", ""))
	assert.Equal(t,
		map[string]any{"error": "boom", "detail": "why"},
		proxyErrorBody("boom", "why"))
}

// End-to-end through the real handler, the real ProxyService and a real
// network: this is the test that would have caught the dead 504 branch, which
// no amount of unit-testing the pieces in isolation did.
func TestProxyToLoxiLBEndToEndClassification(t *testing.T) {
	// Keep the wall-clock cost of the timeout case to a fraction of a second.
	t.Setenv("OAM_PROXY_TIMEOUT", "300ms")

	// A server that accepts the connection and then never answers: reachable,
	// but slow. The operator must be told "timed out", not "unreachable".
	hanging := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer hanging.Close()

	// A port with nothing listening: genuinely unreachable.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	deadEndpoint := "http://" + listener.Addr().String()
	require.NoError(t, listener.Close())

	cases := []struct {
		name       string
		endpoint   string
		wantStatus int
		wantError  string
	}{
		{"slow instance", hanging.URL + "/netlox/v1", http.StatusGatewayTimeout, "Request to LoxiLB instance timed out"},
		{"dead instance", deadEndpoint + "/netlox/v1", http.StatusBadGateway, "LoxiLB instance unreachable"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			defer db.Close()
			mock.ExpectQuery(".*").WillReturnRows(sqlmock.NewRows([]string{
				"id", "name", "host", "port", "protocol", "description",
				"version", "api_endpoint", "cimage", "ctag", "is_active", "created_at",
			}).AddRow(1, "gw", "127.0.0.1", "1", "http", "", "v1", tc.endpoint,
				"loxilb", "latest", true, time.Unix(0, 0).UTC()))

			identity, err := services.NewGatewayServiceIdentity(services.GatewayAuthModeDisabled, "")
			require.NoError(t, err)
			proxy := services.NewProxyServiceWithGatewayIdentity(services.NewLoxiLBService(db), identity)

			h := NewHandler(nil, nil, nil, nil, proxy, nil, 0)
			gin.SetMode(gin.TestMode)
			router := gin.New()
			router.GET("/oam/loxilbs/:id/netlox/*path", h.ProxyToLoxiLB)

			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder,
				httptest.NewRequest(http.MethodGet, "/oam/loxilbs/1/netlox/v1/config/meta", nil))

			assert.Equal(t, tc.wantStatus, recorder.Code)

			var body map[string]any
			require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &body))
			assert.Equal(t, tc.wantError, body["error"])
			assert.NotEmpty(t, body["detail"], "the cause that used to be discarded must reach the operator")
		})
	}
}
