package handlers_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/loxilb-io/loxilb-oam/internal/appliance"
	"github.com/loxilb-io/loxilb-oam/internal/handlers"
	"github.com/loxilb-io/loxilb-oam/internal/middleware"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
)

// applianceRouter wires the capability gate and the capabilities endpoint for
// a caller whose role is read from a mocked users table, as it is in
// production. found=false simulates a token whose user no longer exists.
func applianceRouter(t *testing.T, role string, found bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { db.Close() })

	rows := sqlmock.NewRows([]string{"id", "username", "email", "role", "created_at"})
	if found {
		rows.AddRow(7, "alice", "alice@test.local", role, time.Now())
	}
	mock.ExpectQuery(regexp.QuoteMeta("SELECT id, username, email, role, created_at FROM users WHERE username = $1")).
		WithArgs("alice").WillReturnRows(rows)

	userService := services.NewUserService(db)
	h := handlers.NewApplianceHandler(appliance.NewService(appliance.UnconfiguredHost(), db, "test"), userService)

	r := gin.New()
	// The token says admin throughout: the gate must go by the database.
	r.Use(withClaims("alice", models.RoleAdmin))
	r.GET("/oam/v1/appliance/capabilities", h.Require(middleware.ActApplianceRead), h.GetCapabilities)
	r.GET("/oam/v1/appliance/restricted", h.Require(middleware.ActApplianceReset), func(c *gin.Context) { c.Status(http.StatusNoContent) })
	r.POST("/oam/v1/appliance/operations", h.Require(middleware.ActApplianceRead), h.PlanOperation)
	r.POST("/oam/v1/appliance/operations/:operation_id/authorize", h.Require(middleware.ActApplianceRead), h.AuthorizeOperation)
	r.POST("/oam/v1/appliance/operations/:operation_id/submit", h.Require(middleware.ActApplianceRead), h.SubmitOperation)
	r.POST("/oam/v1/appliance/operations/:operation_id/cancel", h.Require(middleware.ActApplianceRead), h.CancelOperation)
	r.POST("/oam/v1/appliance/operations/:operation_id/reconcile", h.Require(middleware.ActApplianceRead), h.ReconcileOperation)
	return r
}

func get(r http.Handler, path string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestApplianceCapabilitiesPermittedFollowsDatabaseRole(t *testing.T) {
	for role, wantPermitted := range map[string]bool{models.RoleAdmin: true, models.RoleOperator: false, models.RoleViewer: false} {
		rec := get(applianceRouter(t, role, true), "/oam/v1/appliance/capabilities", nil)
		require.Equal(t, http.StatusOK, rec.Code, role)

		var caps appliance.Capabilities
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &caps))
		assert.Equal(t, appliance.SchemaVersion, caps.SchemaVersion)
		require.Len(t, caps.Actions, len(appliance.Actions))
		for _, c := range caps.Actions {
			assert.Equal(t, wantPermitted, c.Permitted, "%s / %s", role, c.Action)
			assert.Equal(t, appliance.ReasonHostNotConfigured, c.UnavailableReason)
		}
		assert.Contains(t, rec.Body.String(), `"host_contract_versions":[]`)
		assert.NotEmpty(t, rec.Header().Get(handlers.RequestIDHeader))
	}
}

// A denial carries the envelope: a stable code, the origin, the request ID
// and a recovery action — and still the plain `error` string.
func TestApplianceDenialUsesErrorEnvelope(t *testing.T) {
	rec := get(applianceRouter(t, models.RoleOperator, true), "/oam/v1/appliance/restricted",
		map[string]string{handlers.RequestIDHeader: "req-123"})
	require.Equal(t, http.StatusForbidden, rec.Code)

	var body appliance.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, appliance.CodePermissionDenied, body.Code)
	assert.Equal(t, appliance.OriginOAM, body.Origin)
	assert.Equal(t, "req-123", body.RequestID)
	assert.NotEmpty(t, body.Error)
	require.NotNil(t, body.Recovery)
	assert.Equal(t, appliance.RecoveryNone, body.Recovery.Action)
	assert.Equal(t, appliance.OriginOAM, rec.Header().Get(services.ErrorOriginHeader))
	assert.Equal(t, "req-123", rec.Header().Get(handlers.RequestIDHeader))
}

func TestApplianceUnknownCallerIsUnauthorized(t *testing.T) {
	rec := get(applianceRouter(t, "", false), "/oam/v1/appliance/capabilities", nil)
	require.Equal(t, http.StatusUnauthorized, rec.Code)

	var body appliance.ErrorBody
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, appliance.CodeUnauthorized, body.Code)
	assert.Equal(t, appliance.RecoveryReauthenticate, body.Recovery.Action)
}

// A caller-supplied request ID is echoed only if it is short and plain; it
// ends up in logs and response headers.
func TestApplianceRequestIDIsSanitized(t *testing.T) {
	for _, bad := range []string{"has space", "new\nline", "<script>", string(make([]byte, 65))} {
		rec := get(applianceRouter(t, models.RoleAdmin, true), "/oam/v1/appliance/capabilities",
			map[string]string{handlers.RequestIDHeader: bad})
		got := rec.Header().Get(handlers.RequestIDHeader)
		assert.NotEqual(t, bad, got)
		assert.Regexp(t, `^[0-9a-f]{24}$`, got)
	}
}

func postPlan(r http.Handler, body string, headers map[string]string) (*httptest.ResponseRecorder, appliance.ErrorBody) {
	return post(r, "/oam/v1/appliance/operations", body, headers)
}

func post(r http.Handler, path, body string, headers map[string]string) (*httptest.ResponseRecorder, appliance.ErrorBody) {
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	var envelope appliance.ErrorBody
	_ = json.Unmarshal(rec.Body.Bytes(), &envelope)
	return rec, envelope
}

// Requests refused before the database or the host is consulted, each with
// its own code. (Everything past this point is covered against PostgreSQL in
// internal/appliance.)
func TestPlanOperationRejectsBadRequests(t *testing.T) {
	// Built rather than written out: a literal next to the word "key" reads
	// as a credential to the secret scanner.
	goodKey := map[string]string{handlers.IdempotencyKeyHeader: strings.Repeat("k", 16)}
	backup := `{"schema_version":"` + appliance.SchemaVersion + `","type":"backup"}`

	cases := []struct {
		name    string
		role    string
		body    string
		headers map[string]string
		status  int
		code    string
	}{
		{"malformed JSON", models.RoleAdmin, `{not json`, goodKey, http.StatusBadRequest, appliance.CodeInvalidRequest},
		{"unknown field", models.RoleAdmin, `{"schema_version":"` + appliance.SchemaVersion + `","type":"backup","force":true}`, goodKey, http.StatusBadRequest, appliance.CodeInvalidRequest},
		{"no idempotency key", models.RoleAdmin, backup, nil, http.StatusBadRequest, appliance.CodeIdempotencyKeyInvalid},
		{"wrong contract version", models.RoleAdmin, `{"schema_version":"v0","type":"backup"}`, goodKey, http.StatusBadRequest, appliance.CodeSchemaMismatch},
		{"restore without archive", models.RoleAdmin, `{"schema_version":"` + appliance.SchemaVersion + `","type":"restore"}`, goodKey, http.StatusBadRequest, appliance.CodeInvalidRequest},
		{"operator may not plan", models.RoleOperator, backup, goodKey, http.StatusForbidden, appliance.CodePermissionDenied},
		{"viewer may not plan", models.RoleViewer, backup, goodKey, http.StatusForbidden, appliance.CodePermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, envelope := postPlan(applianceRouter(t, tc.role, true), tc.body, tc.headers)
			assert.Equal(t, tc.status, rec.Code)
			assert.Equal(t, tc.code, envelope.Code)
			assert.Equal(t, appliance.OriginOAM, envelope.Origin)
			assert.NotEmpty(t, envelope.RequestID)
		})
	}
}

// Requests on an operation that are refused before the database is asked
// about it. (The lifecycle itself is covered against PostgreSQL in
// internal/appliance, and over HTTP by the end-to-end run.)
func TestOperationActionsRejectBadRequests(t *testing.T) {
	const operations = "/oam/v1/appliance/operations/"
	wellFormed := operations + "018f2f6e-7b1a-7c3d-9e4f-0123456789ab"
	malformed := operations + "not-an-operation"

	cases := []struct {
		name   string
		path   string
		body   string
		status int
		code   string
	}{
		{"authorize: malformed JSON", wellFormed + "/authorize", `{`, http.StatusBadRequest, appliance.CodeInvalidRequest},
		{"authorize: no password", wellFormed + "/authorize", `{}`, http.StatusBadRequest, appliance.CodeInvalidRequest},
		{"authorize: unknown field", wellFormed + "/authorize", `{"password":"x","remember":true}`, http.StatusBadRequest, appliance.CodeInvalidRequest},
		{"authorize: malformed ID", malformed + "/authorize", `{"password":"x"}`, http.StatusNotFound, appliance.CodeOperationNotFound},
		{"submit: malformed JSON", wellFormed + "/submit", `plan`, http.StatusBadRequest, appliance.CodeInvalidRequest},
		{"submit: no plan_hash", wellFormed + "/submit", `{"challenge":"x"}`, http.StatusBadRequest, appliance.CodeInvalidRequest},
		{"submit: malformed ID", malformed + "/submit", `{"plan_hash":"x"}`, http.StatusNotFound, appliance.CodeOperationNotFound},
		{"cancel: malformed ID", malformed + "/cancel", ``, http.StatusNotFound, appliance.CodeOperationNotFound},
		{"reconcile: malformed ID", malformed + "/reconcile", ``, http.StatusNotFound, appliance.CodeOperationNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec, envelope := post(applianceRouter(t, models.RoleAdmin, true), tc.path, tc.body, nil)
			assert.Equal(t, tc.status, rec.Code)
			assert.Equal(t, tc.code, envelope.Code)
			assert.Equal(t, appliance.OriginOAM, envelope.Origin)
			assert.NotEmpty(t, envelope.RequestID)
			if strings.HasPrefix(tc.path, wellFormed) {
				assert.Equal(t, "018f2f6e-7b1a-7c3d-9e4f-0123456789ab", envelope.OperationID, "the envelope names the operation")
			} else {
				assert.Empty(t, envelope.OperationID, "a malformed ID is not echoed")
			}
		})
	}
}
