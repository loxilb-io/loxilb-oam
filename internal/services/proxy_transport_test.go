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
