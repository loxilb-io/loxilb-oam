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

func TestCanonicalGatewayPath(t *testing.T) {
	accepted := map[string]string{
		"/v1/config/loadbalancer/all": "/config/loadbalancer/all",
		"/config/loadbalancer/all":    "/config/loadbalancer/all",
		"/config/loadbalancer/all/":   "/config/loadbalancer/all",
		"/v1":                         "/",
		"/v1/":                        "/",
		"/":                           "/",
		// Only the leading version segment is the version.
		"/v1/v1/meta":      "/v1/meta",
		"/config/v1":       "/config/v1",
		"/v1beta/meta":     "/v1beta/meta",
		"/config/cert/a b": "/config/cert/a b",
		"/config/route/destinationIPNet/192.168.1.0/24": "/config/route/destinationIPNet/192.168.1.0/24",
		// Dots inside a name are a name, not a dot segment.
		"/log-archives/loxilb.log.1": "/log-archives/loxilb.log.1",
		"/config/cert/..hidden":      "/config/cert/..hidden",
	}
	for raw, want := range accepted {
		path, err := CanonicalGatewayPath(raw, raw)
		require.NoError(t, err, raw)
		assert.Equal(t, want, path.String(), raw)
	}

	refused := []string{
		"",
		"config/restore",
		"//config/restore",
		"/config//restore",
		"/config/restore//",
		"/lb/../config/restore",
		"/config/restore/..",
		"/./config/restore",
		// The reserved-endpoint guard matches "config/loadbalancer": this
		// spelling does not contain it, and the Gateway's router would still
		// have cleaned it into that path.
		"/config/x/../loadbalancer",
		`/config\restore`,
		"/config/restore?mode=commit",
		"/config/restore#x",
		"/config/restore\x00",
		"/config/restore\r\n",
		"/config/\xff",
	}
	for _, raw := range refused {
		_, err := CanonicalGatewayPath(raw, raw)
		assert.ErrorIs(t, err, ErrGatewayPathInvalid, "%q", raw)
	}

	// A decoded "/" cannot be told from a real one, so the encoded form is
	// looked for in the path as it was sent.
	for _, escaped := range []string{"/oam/loxilbs/1/netlox/config%2Frestore", "/oam/loxilbs/1/netlox/config%2frestore", "/oam/loxilbs/1/netlox/config%5Crestore"} {
		_, err := CanonicalGatewayPath("/config/restore", escaped)
		assert.ErrorIs(t, err, ErrGatewayPathInvalid, escaped)
	}
}

func TestClassifyGatewayRequestDeniesAnUnsetPath(t *testing.T) {
	assert.Equal(t, GatewayAccessDenied, ClassifyGatewayRequest(http.MethodGet, GatewayPath{}))
	var unset GatewayAccess
	assert.Equal(t, GatewayAccessDenied, unset, "the zero decision must deny")
}

// The classifier and the middleware matrix test cover the decisions; this
// pins the one distinction that is easy to lose, between a family and a
// longer name that merely starts with it.
func TestClassifyGatewayRequestMatchesWholeSegments(t *testing.T) {
	for raw, want := range map[string]GatewayAccess{
		"/auth/users":          GatewayAccessDenied,
		"/authx/users":         GatewayAccessAdmin,
		"/config/restore":      GatewayAccessAdmin,
		"/config/restore/x":    GatewayAccessAdmin,
		"/config/restorex":     GatewayAccessAdmin,
		"/config/loadbalancer": GatewayAccessAuthenticated,
		"/audit/status":        GatewayAccessAuthenticated,
		"/audit/status/x":      GatewayAccessOperator,
		"/audit/statusx":       GatewayAccessOperator,
		"/logs":                GatewayAccessOperator,
		"/logsx":               GatewayAccessAdmin,
	} {
		assert.Equal(t, want, ClassifyGatewayRequest(http.MethodGet, mustGatewayPath(t, raw)), raw)
	}
	assert.Equal(t, GatewayAccessAdmin, ClassifyGatewayRequest(http.MethodDelete, mustGatewayPath(t, "/logs")))
}

func TestGatewayTargetURL(t *testing.T) {
	cases := []struct {
		endpoint, path, query, want string
	}{
		{"https://gw:8091/netlox/v1", "/v1/config/loadbalancer/all", "", "https://gw:8091/netlox/v1/config/loadbalancer/all"},
		{"https://gw:8091/netlox/v1", "/config/loadbalancer/all", "", "https://gw:8091/netlox/v1/config/loadbalancer/all"},
		{"https://gw:8091/netlox/v1/", "/v1/meta", "", "https://gw:8091/netlox/v1/meta"},
		{"https://gw:8091/netlox", "/v1/meta", "", "https://gw:8091/netlox/v1/meta"},
		{"https://gw:8091/netlox", "/meta", "", "https://gw:8091/netlox/meta"},
		{"https://gw:8091/netlox/v1", "/v1", "", "https://gw:8091/netlox/v1"},
		{"https://gw:8091/netlox/v1", "/v1/config/loadbalancer", "a=1&b=%2F", "https://gw:8091/netlox/v1/config/loadbalancer?a=1&b=%2F"},
		// A name is escaped as a path segment; it cannot open a query.
		{"https://gw:8091/netlox/v1", "/config/cert/a b", "", "https://gw:8091/netlox/v1/config/cert/a%20b"},
	}
	for _, tc := range cases {
		got, err := gatewayTargetURL(tc.endpoint, mustGatewayPath(t, tc.path), tc.query)
		require.NoError(t, err, tc.path)
		assert.Equal(t, tc.want, got, "%s + %s", tc.endpoint, tc.path)
	}

	_, err := gatewayTargetURL("https://gw:8091/netlox/v1", GatewayPath{}, "")
	assert.ErrorIs(t, err, ErrGatewayPathInvalid)
}

// The request that leaves OAM is built from the canonical path, whatever the
// spelling of the request that came in.
func TestForwardRequestSendsTheCanonicalPath(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectProxyInstance(mock, "http://gateway.test/netlox/v1")

	var sent string
	proxy := NewProxyServiceWithGatewayIdentity(
		NewLoxiLBService(db), mustGatewayIdentity(t, GatewayAuthModeDisabled, ""))
	proxy.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		sent = req.URL.String()
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{}`)),
			Request:    req,
		}, nil
	})}

	gin.SetMode(gin.TestMode)
	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodGet, "/oam/loxilbs/1/netlox/v1/config/loadbalancer/all/?x=1", nil)

	require.NoError(t, proxy.ForwardRequest(ctx, 1, mustGatewayPath(t, "/v1/config/loadbalancer/all/")))
	require.NoError(t, mock.ExpectationsWereMet())
	assert.Equal(t, "http://gateway.test/netlox/v1/config/loadbalancer/all?x=1", sent)
}

// The guard sees the canonical path, in which the load-balancer endpoint can
// only be spelled one way.
func TestReservedEndpointGuardSeesTheCanonicalPath(t *testing.T) {
	reserved := reservedList(t, "192.168.0.8:8443")
	body := lbRule("192.168.0.8", "", "8443", "tcp")

	path := mustGatewayPath(t, "/v1/config/loadbalancer/")
	var reservedErr *ReservedEndpointError
	assert.ErrorAs(t, checkReservedEndpoint(reserved, http.MethodPost, path.String(), body), &reservedErr)
}
