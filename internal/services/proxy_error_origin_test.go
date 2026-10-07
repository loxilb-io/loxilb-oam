package services

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
