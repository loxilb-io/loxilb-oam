package services

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"syscall"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// forwardWithTransportError drives ForwardRequest against a transport that
// always fails with cause, and returns the error the handler would receive.
func forwardWithTransportError(t *testing.T, cause error) error {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectProxyInstance(mock, "http://gateway.test/netlox/v1")

	proxy := NewProxyServiceWithGatewayIdentity(
		NewLoxiLBService(db), mustGatewayIdentity(t, GatewayAuthModeDisabled, ""))
	proxy.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return nil, &url.Error{Op: req.Method, URL: req.URL.String(), Err: cause}
	})}

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/oam/loxilbs/1/netlox/v1/meta", nil)

	err = proxy.ForwardRequest(ctx, 1, "/v1/meta")
	require.NoError(t, mock.ExpectationsWereMet())
	return err
}

// The defect this guards: the cause used to be discarded, so a timeout and a
// refused connection arrived at the handler as the same opaque string.
func TestForwardRequestPreservesTransportCause(t *testing.T) {
	causes := map[string]error{
		"timeout":            context.DeadlineExceeded,
		"connection refused": syscall.ECONNREFUSED,
		"dns failure":        &net.DNSError{Name: "gateway.test", IsNotFound: true},
		"unexpected eof":     io.ErrUnexpectedEOF,
	}

	for name, cause := range causes {
		t.Run(name, func(t *testing.T) {
			err := forwardWithTransportError(t, cause)
			require.Error(t, err)

			var upstreamErr *ProxyUpstreamError
			require.ErrorAs(t, err, &upstreamErr, "the handler must be able to recognise a transport failure")
			assert.ErrorIs(t, err, cause, "the original cause must survive the service boundary")
			assert.Contains(t, upstreamErr.TargetURL, "gateway.test")
		})
	}
}

// ProxyUpstreamError.Timeout must be true for both shapes a timeout takes:
// a deadline on the request context and a net.Error that reports one.
func TestProxyUpstreamErrorTimeoutDetection(t *testing.T) {
	deadline := &ProxyUpstreamError{Err: context.DeadlineExceeded}
	assert.True(t, deadline.Timeout())

	netTimeout := &ProxyUpstreamError{Err: &net.DNSError{IsTimeout: true}}
	assert.True(t, netTimeout.Timeout())

	refused := &ProxyUpstreamError{Err: syscall.ECONNREFUSED}
	assert.False(t, refused.Timeout())
}

func TestForwardRequestReturnsSentinelForMissingInstance(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery(".*").WillReturnError(errors.New("sql: no rows in result set"))

	proxy := NewProxyServiceWithGatewayIdentity(
		NewLoxiLBService(db), mustGatewayIdentity(t, GatewayAuthModeDisabled, ""))
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/oam/loxilbs/1/netlox/v1/meta", nil)

	err = proxy.ForwardRequest(ctx, 1, "/v1/meta")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInstanceNotFound)
}

// OAM-2: reuse is the default, so a proxied request no longer pays a fresh
// handshake, and the pool can still be invalidated on demand.
func TestProxyTransportReusesConnectionsByDefault(t *testing.T) {
	proxy := NewProxyServiceWithGatewayIdentity(nil, mustGatewayIdentity(t, GatewayAuthModeDisabled, ""))

	tr, ok := proxy.client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.False(t, tr.DisableKeepAlives, "connection reuse must be on by default")
	assert.Positive(t, tr.MaxIdleConnsPerHost)
	assert.Positive(t, tr.IdleConnTimeout)

	assert.NotPanics(t, proxy.CloseIdleConnections)
}

func TestProxyTransportKeepAlivesCanBeDisabled(t *testing.T) {
	t.Setenv("OAM_PROXY_DISABLE_KEEPALIVES", "true")

	proxy := NewProxyServiceWithGatewayIdentity(nil, mustGatewayIdentity(t, GatewayAuthModeDisabled, ""))
	tr, ok := proxy.client.Transport.(*http.Transport)
	require.True(t, ok)
	assert.True(t, tr.DisableKeepAlives)
}

// CloseIdleConnections must actually return the pooled socket, otherwise the
// invalidation the handlers call is decorative.
func TestCloseIdleConnectionsDropsPooledConnection(t *testing.T) {
	var dials int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	proxy := NewProxyServiceWithGatewayIdentity(nil, mustGatewayIdentity(t, GatewayAuthModeDisabled, ""))
	tr := proxy.client.Transport.(*http.Transport)
	tr.DialContext = func(ctx context.Context, network, addr string) (net.Conn, error) {
		dials++
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}

	get := func() {
		resp, err := proxy.client.Get(server.URL)
		require.NoError(t, err)
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}

	get()
	get()
	assert.Equal(t, 1, dials, "the second request must reuse the pooled connection")

	proxy.CloseIdleConnections()
	get()
	assert.Equal(t, 2, dials, "invalidation must force the next request to dial afresh")
}
