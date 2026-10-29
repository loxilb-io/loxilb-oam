package middleware_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"regexp"
	"testing"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/middleware"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatewayCaller describes who is behind a proxied request.
type gatewayCaller struct {
	claimsRole string // role in the token; "" means no claims at all
	dbRole     string // role in the database; "" means the user no longer exists
}

func callerWithRole(role string) gatewayCaller {
	return gatewayCaller{claimsRole: role, dbRole: role}
}

// gatewayOutcome is what the proxy route did with a request. forwarded counts
// how often the proxy handler ran: a refusal must leave it at zero, since the
// handler is the only thing that calls the Gateway.
type gatewayOutcome struct {
	status    int
	forwarded int
	path      string // the canonical path handed to the proxy handler
	origin    string // the error-origin marker on the response
}

// gatewayProxyRequest sends one request through RequireGatewayAccess, with
// the claims TokenAuthMiddleware would have set and a sqlmock-backed user.
func gatewayProxyRequest(t *testing.T, caller gatewayCaller, method, target string) gatewayOutcome {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	if caller.claimsRole != "" {
		query := mock.ExpectQuery(regexp.QuoteMeta("SELECT id, username, email, role, created_at FROM users WHERE username = $1")).
			WithArgs("alice")
		if caller.dbRole == "" {
			query.WillReturnError(sql.ErrNoRows)
		} else {
			query.WillReturnRows(sqlmock.NewRows([]string{"id", "username", "email", "role", "created_at"}).
				AddRow(7, "alice", "alice@test.local", caller.dbRole, time.Now()))
		}
	}

	outcome := gatewayOutcome{}
	router := gin.New()
	router.Use(func(c *gin.Context) {
		if caller.claimsRole != "" {
			c.Set("username", &utils.Claims{Username: "alice", Role: caller.claimsRole})
		}
	})
	router.Any("/proxy/*path", middleware.RequireGatewayAccess(services.NewUserService(db)), func(c *gin.Context) {
		outcome.forwarded++
		if value, ok := c.Get(middleware.CtxGatewayPath); ok {
			outcome.path = value.(services.GatewayPath).String()
		}
		c.Status(http.StatusOK)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(method, "/proxy"+target, nil))
	outcome.status = recorder.Code
	outcome.origin = recorder.Header().Get(services.ErrorOriginHeader)
	return outcome
}

const (
	ok        = http.StatusOK
	forbidden = http.StatusForbidden
)

// The proxy speaks to the Gateway as an administrator, so what each OAM role
// may reach through it is decided here and nowhere else. Every refusal is
// checked to have stopped before the proxy handler.
func TestGatewayAccessMatrix(t *testing.T) {
	cases := []struct {
		method, path            string
		admin, operator, viewer int
	}{
		// Gateway accounts and its management token: nobody, through the proxy.
		{http.MethodPost, "/v1/auth/token/upgrade", forbidden, forbidden, forbidden},
		{http.MethodGet, "/v1/auth/users", forbidden, forbidden, forbidden},
		{http.MethodPost, "/auth/users", forbidden, forbidden, forbidden},
		{http.MethodDelete, "/auth/users/3", forbidden, forbidden, forbidden},
		{http.MethodPost, "/auth/login", forbidden, forbidden, forbidden},

		// The whole-configuration document: administrators only, reads included.
		{http.MethodGet, "/v1/config/snapshot", ok, forbidden, forbidden},
		{http.MethodGet, "/config/export", ok, forbidden, forbidden},
		{http.MethodPost, "/v1/config/restore", ok, forbidden, forbidden},
		{http.MethodPost, "/config/import", ok, forbidden, forbidden},
		{http.MethodPost, "/config/persist", ok, forbidden, forbidden},

		// Audit: everyone sees the status, operators see the configuration,
		// only administrators change it.
		{http.MethodGet, "/audit/status", ok, ok, ok},
		{http.MethodGet, "/audit/policy", ok, ok, forbidden},
		{http.MethodGet, "/audit/sink", ok, ok, forbidden},
		{http.MethodGet, "/audit/sinks/ops", ok, ok, forbidden},
		{http.MethodPost, "/audit/policy", ok, forbidden, forbidden},
		{http.MethodPost, "/audit/sink", ok, forbidden, forbidden},
		{http.MethodPut, "/audit/sinks/ops", ok, forbidden, forbidden},
		{http.MethodDelete, "/audit/sinks/ops", ok, forbidden, forbidden},
		{http.MethodPost, "/audit/rotate", ok, forbidden, forbidden},

		// The Gateway's process log.
		{http.MethodGet, "/logs", ok, ok, forbidden},
		{http.MethodGet, "/log-archives", ok, ok, forbidden},
		{http.MethodGet, "/log-archives/loxilb.log.1", ok, ok, forbidden},

		// Day-to-day operation is unchanged.
		{http.MethodGet, "/v1/config/loadbalancer/all", ok, ok, ok},
		{http.MethodHead, "/v1/config/loadbalancer/all", ok, ok, ok},
		{http.MethodPost, "/v1/config/loadbalancer", ok, ok, forbidden},
		{http.MethodPost, "/config/ai/apikey", ok, ok, forbidden},
		{http.MethodPatch, "/config/ai/apikey/k1", ok, ok, forbidden},
		{http.MethodDelete, "/v1/config/route/destinationIPNet/192.168.1.0/24", ok, ok, forbidden},
		{http.MethodPost, "/config/cert", ok, ok, forbidden},
		{http.MethodPut, "/maintenance", ok, ok, forbidden},
		{http.MethodGet, "/version", ok, ok, ok},
		{http.MethodGet, "/status/ready", ok, ok, ok},
		{http.MethodGet, "/diagnostics", ok, ok, ok},

		// A path nobody has classified is admin-only.
		{http.MethodGet, "/something/new", ok, forbidden, forbidden},
		{http.MethodPost, "/config/newfamily", ok, forbidden, forbidden},
		{http.MethodGet, "/config", ok, forbidden, forbidden},
		{http.MethodGet, "/", ok, forbidden, forbidden},
		{http.MethodGet, "/oauth/google/token", ok, forbidden, forbidden},
		// The Gateway's router is case-sensitive, so this is not /config/restore.
		{http.MethodPost, "/Config/Restore", ok, forbidden, forbidden},
		// The query is not part of the decision.
		{http.MethodPost, "/config/restore?mode=commit", ok, forbidden, forbidden},
		{http.MethodPost, "/config/loadbalancer?mode=commit", ok, ok, forbidden},
	}

	roles := []struct {
		role string
		want func(admin, operator, viewer int) int
	}{
		{models.RoleAdmin, func(admin, _, _ int) int { return admin }},
		{models.RoleOperator, func(_, operator, _ int) int { return operator }},
		{models.RoleLegacyUser, func(_, operator, _ int) int { return operator }},
		{models.RoleViewer, func(_, _, viewer int) int { return viewer }},
	}

	for _, tc := range cases {
		for _, r := range roles {
			want := r.want(tc.admin, tc.operator, tc.viewer)
			got := gatewayProxyRequest(t, callerWithRole(r.role), tc.method, tc.path)
			assert.Equal(t, want, got.status, "%s %s as %s", tc.method, tc.path, r.role)
			wantForwarded := 0
			if want == ok {
				wantForwarded = 1
			}
			assert.Equal(t, wantForwarded, got.forwarded, "%s %s as %s: calls reaching the proxy handler", tc.method, tc.path, r.role)
		}
	}
}

// A caller OAM cannot resolve gets no answer about any path, reads included.
// (A missing or expired token is refused earlier, by TokenAuthMiddleware.)
func TestGatewayAccessRequiresAResolvableCaller(t *testing.T) {
	callers := map[string]gatewayCaller{
		"user deleted after login": {claimsRole: models.RoleAdmin, dbRole: ""},
		"no claims":                {},
	}
	for name, caller := range callers {
		for _, method := range []string{http.MethodGet, http.MethodPost} {
			got := gatewayProxyRequest(t, caller, method, "/v1/config/loadbalancer/all")
			assert.Equal(t, http.StatusUnauthorized, got.status, "%s, %s", name, method)
			assert.Zero(t, got.forwarded, "%s, %s", name, method)
		}
	}
}

// The role comes from the database on every request, so a change does not
// wait for the token to expire, in either direction.
func TestGatewayAccessFollowsTheStoredRole(t *testing.T) {
	demoted := gatewayProxyRequest(t, gatewayCaller{claimsRole: models.RoleAdmin, dbRole: models.RoleViewer},
		http.MethodPost, "/config/loadbalancer")
	assert.Equal(t, forbidden, demoted.status)
	assert.Zero(t, demoted.forwarded)

	promoted := gatewayProxyRequest(t, gatewayCaller{claimsRole: models.RoleViewer, dbRole: models.RoleAdmin},
		http.MethodPost, "/config/restore")
	assert.Equal(t, ok, promoted.status)
	assert.Equal(t, 1, promoted.forwarded)
}

// Every spelling of a path is either refused or decided exactly as the plain
// path is. These are all ways of writing POST /config/restore, which an
// operator may not send.
func TestGatewayAccessCannotBeSidesteppedBySpelling(t *testing.T) {
	operator := callerWithRole(models.RoleOperator)

	sameDecision := []string{
		"/config/restore",
		"/v1/config/restore",
		"/config/restore/",
		"/v1/config/restore/",
	}
	for _, target := range sameDecision {
		got := gatewayProxyRequest(t, operator, http.MethodPost, target)
		assert.Equal(t, forbidden, got.status, target)
		assert.Zero(t, got.forwarded, target)
	}

	refused := []string{
		"//config/restore",
		"/config//restore",
		"/config/restore//",
		"/lb/../config/restore",
		"/config/loadbalancer/../restore",
		"/./config/restore",
		"/lb/%2e%2e/config/restore",
		"/lb/%2E%2E/config/restore",
		"/config%2Frestore",
		"/config%2frestore",
		"/config%5Crestore",
		"/config/restore%3Fmode=commit",
		"/config/restore%23x",
		"/config/restore%00",
		"/config/restore%0d%0a",
	}
	for _, role := range []string{models.RoleOperator, models.RoleAdmin} {
		for _, target := range refused {
			got := gatewayProxyRequest(t, callerWithRole(role), http.MethodPost, target)
			assert.Equal(t, http.StatusBadRequest, got.status, "%s as %s", target, role)
			assert.Zero(t, got.forwarded, "%s as %s", target, role)
		}
	}
}

// The handler forwards what it is handed, so what it is handed must be the
// canonical path and not the spelling the client chose.
func TestGatewayAccessHandsOverTheCanonicalPath(t *testing.T) {
	for target, want := range map[string]string{
		"/v1/config/loadbalancer/all":   "/config/loadbalancer/all",
		"/config/loadbalancer/all/":     "/config/loadbalancer/all",
		"/v1/config/cert/my%20cert":     "/config/cert/my cert",
		"/v1/config/loadbalancer?x=%2F": "/config/loadbalancer",
		"/v1":                           "/",
	} {
		got := gatewayProxyRequest(t, callerWithRole(models.RoleAdmin), http.MethodGet, target)
		assert.Equal(t, ok, got.status, target)
		assert.Equal(t, want, got.path, target)
	}
}

func TestGatewayAccessRefusesMethodsTheProxyDoesNotForward(t *testing.T) {
	for _, method := range []string{http.MethodTrace, http.MethodConnect, http.MethodOptions} {
		got := gatewayProxyRequest(t, callerWithRole(models.RoleAdmin), method, "/v1/version")
		assert.Equal(t, http.StatusMethodNotAllowed, got.status, method)
		assert.Zero(t, got.forwarded, method)
	}
}

// A refusal is OAM's answer and is marked as OAM's; a request that passes is
// not marked here at all, since the proxy decides that from the instance's
// answer.
func TestGatewayAccessMarksItsRefusalsAsOAMs(t *testing.T) {
	refusals := []struct {
		caller gatewayCaller
		method string
		target string
		status int
	}{
		{gatewayCaller{claimsRole: models.RoleAdmin}, http.MethodGet, "/v1/version", http.StatusUnauthorized},
		{callerWithRole(models.RoleAdmin), http.MethodTrace, "/v1/version", http.StatusMethodNotAllowed},
		{callerWithRole(models.RoleAdmin), http.MethodGet, "/lb/../version", http.StatusBadRequest},
		{callerWithRole(models.RoleViewer), http.MethodPost, "/config/loadbalancer", forbidden},
	}
	for _, tc := range refusals {
		got := gatewayProxyRequest(t, tc.caller, tc.method, tc.target)
		assert.Equal(t, tc.status, got.status, "%s %s", tc.method, tc.target)
		assert.Equal(t, services.ErrorOriginOAM, got.origin, "%s %s", tc.method, tc.target)
	}

	passed := gatewayProxyRequest(t, callerWithRole(models.RoleViewer), http.MethodGet, "/v1/version")
	assert.Equal(t, ok, passed.status)
	assert.Empty(t, passed.origin)
}

// OAM's own 429 carries the wait and says whose it is, so it cannot be taken
// for an instance that is rate-limiting.
func TestRateLimitRefusalIsMarkedAsOAMs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/x", middleware.RateLimit(middleware.NewRateLimiter(0.001, 1)), func(c *gin.Context) { c.Status(http.StatusOK) })

	var last *httptest.ResponseRecorder
	for i := 0; i < 2; i++ {
		last = httptest.NewRecorder()
		router.ServeHTTP(last, httptest.NewRequest(http.MethodGet, "/x", nil))
	}
	assert.Equal(t, http.StatusTooManyRequests, last.Code)
	assert.Equal(t, "1", last.Header().Get("Retry-After"))
	assert.Equal(t, services.ErrorOriginOAM, last.Header().Get(services.ErrorOriginHeader))
}
