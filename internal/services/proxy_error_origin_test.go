package services

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// relayGatewayResponse proxies one request to a Gateway that answers with
// status and upstreamHeaders. preset stands in for what CORSMiddleware has
// already written by the time the proxy runs.
func relayGatewayResponse(t *testing.T, status int, upstreamHeaders, preset http.Header) *httptest.ResponseRecorder {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectProxyInstance(mock, "http://gateway.test/netlox/v1")

	proxy := NewProxyServiceWithGatewayIdentity(
		NewLoxiLBService(db), mustGatewayIdentity(t, GatewayAuthModeDisabled, ""))
	proxy.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		header := upstreamHeaders.Clone()
		header.Set("Content-Type", "application/json")
		return &http.Response{
			StatusCode: status,
			Header:     header,
			Body:       io.NopCloser(strings.NewReader(`{"code":401,"message":"Missing or invalid credentials"}`)),
			Request:    req,
		}, nil
	})}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/oam/loxilbs/1/netlox/v1/config/cistate/all", nil)
	for key, values := range preset {
		for _, value := range values {
			ctx.Header(key, value)
		}
	}

	require.NoError(t, proxy.ForwardRequest(ctx, 1, mustGatewayPath(t, "/v1/config/cistate/all")))
	require.NoError(t, mock.ExpectationsWereMet())
	return recorder
}

// The defect this guards: a Gateway that refused OAM's management credential
// answered 401, OAM relayed it unmarked, and the console could not tell it
// from its own session expiring, so it signed the operator out a second
// after every login.
func TestProxyMarksGatewayFailuresWithTheirOrigin(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusInternalServerError} {
		recorder := relayGatewayResponse(t, status, http.Header{}, nil)
		assert.Equal(t, status, recorder.Code, "the Gateway's status must reach the console unchanged")
		assert.Equal(t, ErrorOriginGateway, recorder.Header().Get(ErrorOriginHeader), "status %d", status)
	}
}

func TestProxyDoesNotMarkGatewaySuccess(t *testing.T) {
	recorder := relayGatewayResponse(t, http.StatusOK, http.Header{}, nil)
	assert.Empty(t, recorder.Header().Get(ErrorOriginHeader))
}

// Only OAM knows which hop failed. A Gateway sending the marker itself must
// not be able to steer the console: claiming "oam" would sign the operator
// out, and on a success it would invent an error that never happened.
func TestProxyNeverRelaysTheGatewaysOwnOriginClaim(t *testing.T) {
	claim := http.Header{ErrorOriginHeader: []string{"oam"}}

	failed := relayGatewayResponse(t, http.StatusUnauthorized, claim, nil)
	assert.Equal(t, []string{ErrorOriginGateway}, failed.Header().Values(ErrorOriginHeader))

	succeeded := relayGatewayResponse(t, http.StatusOK, claim, nil)
	assert.Empty(t, succeeded.Header().Values(ErrorOriginHeader))
}

// Observed on the testbed: a disallowed Origin got no CORS grant from
// /oam/users/me but `Access-Control-Allow-Origin: *` from every pass-through,
// because the Gateway's CORS headers overwrote the ones OAM had just set.
func TestProxyKeepsOAMsCORSPolicyOverTheGateways(t *testing.T) {
	upstream := http.Header{
		"Access-Control-Allow-Origin":   []string{"*"},
		"Access-Control-Allow-Headers":  []string{"Content-Type, Authorization, X-Requested-With"},
		"Access-Control-Expose-Headers": []string{"X-Gateway-Only"},
		"Access-Control-Max-Age":        []string{"86400"},
		"X-Request-Id":                  []string{"req-123"},
	}
	preset := http.Header{
		"Access-Control-Allow-Origin":   []string{"https://console.example"},
		"Access-Control-Expose-Headers": []string{ErrorOriginHeader},
	}

	recorder := relayGatewayResponse(t, http.StatusUnauthorized, upstream, preset)
	got := recorder.Header()
	assert.Equal(t, []string{"https://console.example"}, got.Values("Access-Control-Allow-Origin"))
	assert.Equal(t, []string{ErrorOriginHeader}, got.Values("Access-Control-Expose-Headers"))
	assert.Empty(t, got.Values("Access-Control-Allow-Headers"))
	assert.Empty(t, got.Values("Access-Control-Max-Age"))
	assert.Equal(t, "req-123", got.Get("X-Request-Id"), "ordinary Gateway headers must still be relayed")
}

// A Gateway that cannot audit answers 503 with a Retry-After and a body that
// says why. OAM relays all three as they are and adds only whose answer it
// is: it does not reword the body, and it does not turn other 503s into this
// one.
func TestProxyRelaysAGateway503Unchanged(t *testing.T) {
	const body = `{"code":503,"message":"audit_unavailable","result":"audit writer is not accepting records"}`
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectProxyInstance(mock, "http://gateway.test/netlox/v1")

	proxy := NewProxyServiceWithGatewayIdentity(
		NewLoxiLBService(db), mustGatewayIdentity(t, GatewayAuthModeDisabled, ""))
	proxy.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusServiceUnavailable,
			Header:     http.Header{"Content-Type": []string{"application/json"}, "Retry-After": []string{"5"}},
			Body:       io.NopCloser(strings.NewReader(body)),
			Request:    req,
		}, nil
	})}

	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/oam/loxilbs/1/netlox/v1/config/loadbalancer", strings.NewReader(`{}`))

	require.NoError(t, proxy.ForwardRequest(ctx, 1, mustGatewayPath(t, "/v1/config/loadbalancer")))
	assert.Equal(t, http.StatusServiceUnavailable, recorder.Code)
	assert.Equal(t, "5", recorder.Header().Get("Retry-After"))
	assert.Equal(t, ErrorOriginGateway, recorder.Header().Get(ErrorOriginHeader))
	assert.Equal(t, body, recorder.Body.String())
}

// The call to the instance carries the caller's context: when the console
// goes away, the call stops, and the handler is told it was cancelled rather
// than that the instance is unreachable.
func TestProxyStopsWhenTheCallerGoesAway(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectProxyInstance(mock, "http://gateway.test/netlox/v1")

	proxy := NewProxyServiceWithGatewayIdentity(
		NewLoxiLBService(db), mustGatewayIdentity(t, GatewayAuthModeDisabled, ""))
	proxy.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		<-req.Context().Done() // an instance that never answers
		return nil, req.Context().Err()
	})}

	callerContext, callerLeaves := context.WithCancel(context.Background())
	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/oam/loxilbs/1/netlox/v1/meta", nil).WithContext(callerContext)

	done := make(chan error, 1)
	go func() { done <- proxy.ForwardRequest(ctx, 1, mustGatewayPath(t, "/v1/meta")) }()
	callerLeaves()

	select {
	case err := <-done:
		var upstreamErr *ProxyUpstreamError
		require.ErrorAs(t, err, &upstreamErr)
		assert.ErrorIs(t, err, context.Canceled)
		assert.False(t, upstreamErr.Timeout(), "a caller that left is not a timeout")
	case <-time.After(5 * time.Second):
		t.Fatal("the call to the instance outlived the caller")
	}
}
