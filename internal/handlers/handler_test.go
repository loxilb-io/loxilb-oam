package handlers_test

import (
	"database/sql"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/handlers"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// newTestHandler builds a Handler wired to a sqlmock-backed UserService. The
// other services are nil: the handlers exercised here only touch the user
// service (or fail during request binding before any service call).
func newTestHandler(t *testing.T) (*handlers.Handler, sqlmock.Sqlmock, func()) {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	// user, loxilb and alert services all wrap the same stub DB; sqlmock matches
	// queries in declared order regardless of which service issues them.
	userService := services.NewUserService(db)
	loxilbService := services.NewLoxiLBService(db)
	alertService := services.NewAlertService(db)
	h := handlers.NewHandler(userService, loxilbService, nil, alertService, nil, nil, 60)
	return h, mock, func() { db.Close() }
}

// withClaims returns a middleware that injects JWT claims into the context the
// way TokenAuthMiddleware would, so downstream handlers can resolve the caller.
func withClaims(username, role string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("username", &utils.Claims{Username: username, Role: role})
	}
}

func TestLoginBadJSON(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.POST("/oam/login", h.Login)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oam/login", strings.NewReader("{not-json"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLogoutMissingHeader(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.POST("/oam/logout", h.Logout)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oam/logout", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestLogoutOK(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	// Logout deletes the presented token from the api_tokens store.
	mock.ExpectExec("DELETE FROM api_tokens WHERE token_value = ?").
		WithArgs("tok123").
		WillReturnResult(sqlmock.NewResult(0, 1))

	r := gin.New()
	r.POST("/oam/logout", h.Logout)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oam/logout", nil)
	req.Header.Set("Authorization", "Bearer tok123")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "Logged out successfully")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMeNoClaims(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.GET("/oam/users/me", h.GetMe)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/users/me", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGetMeOK(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	rows := sqlmock.NewRows([]string{"id", "username", "email", "role", "created_at"}).
		AddRow(7, "alice", "alice@test.local", "admin", time.Now())
	mock.ExpectQuery("SELECT id, username, email, role, created_at FROM users WHERE username = ?").
		WithArgs("alice").
		WillReturnRows(rows)

	r := gin.New()
	r.GET("/oam/users/me", withClaims("alice", "admin"), h.GetMe)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/users/me", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "alice")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetMeNotFound(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	mock.ExpectQuery("SELECT id, username, email, role, created_at FROM users WHERE username = ?").
		WithArgs("ghost").
		WillReturnError(sql.ErrNoRows)

	r := gin.New()
	r.GET("/oam/users/me", withClaims("ghost", "viewer"), h.GetMe)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/users/me", nil)
	r.ServeHTTP(rec, req)

	// A missing user maps to 404 (sql.ErrNoRows branch).
	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateUserBadJSON(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.POST("/oam/users", h.CreateUser)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oam/users", strings.NewReader(`{"username":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	// Missing required email/password fields → binding failure → 400.
	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetUsersOK(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	rows := sqlmock.NewRows([]string{"id", "username", "password", "role", "email", "created_at"}).
		AddRow(1, "alice", "hash", "admin", "alice@test.local", time.Now()).
		AddRow(2, "bob", "hash", "viewer", "bob@test.local", time.Now())
	mock.ExpectQuery("SELECT id, username, password, role, email, created_at FROM users").
		WillReturnRows(rows)

	r := gin.New()
	r.GET("/oam/users", h.GetUsers)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/users", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "alice")
	assert.Contains(t, rec.Body.String(), "bob")
	// The password hash must never leak in the response (User.Password is json:"-").
	assert.NotContains(t, rec.Body.String(), "hash")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetUsersDBError(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	mock.ExpectQuery("SELECT id, username, password, role, email, created_at FROM users").
		WillReturnError(sqlmock.ErrCancelled)

	r := gin.New()
	r.GET("/oam/users", h.GetUsers)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/users", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSetupStatusNoAdmin(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	// No admin row exists → CheckDefaultAdminCredentials reports no default
	// credentials and adminExists=false, so the per-admin credentials query is
	// skipped and only the installation-id lookup follows.
	mock.ExpectQuery("SELECT id, password FROM users WHERE username").
		WithArgs("admin", "admin").
		WillReturnError(sql.ErrNoRows)
	mock.ExpectQuery("SELECT setting_value FROM system_settings WHERE setting_key = 'installation_id'").
		WillReturnRows(sqlmock.NewRows([]string{"setting_value"}).AddRow("inst-abc123"))

	r := gin.New()
	r.GET("/oam/setup/status", h.GetSetupStatus)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/setup/status", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), `"adminExists":false`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetSetupStatusDBError(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	// A hard DB error (not sql.ErrNoRows) while checking credentials surfaces as 500.
	mock.ExpectQuery("SELECT id, password FROM users WHERE username").
		WithArgs("admin", "admin").
		WillReturnError(sqlmock.ErrCancelled)

	r := gin.New()
	r.GET("/oam/setup/status", h.GetSetupStatus)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/setup/status", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateAdminCredentialsBadJSON(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.POST("/oam/setup/update-admin", h.UpdateAdminCredentials)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oam/setup/update-admin", strings.NewReader("{bad"))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateAdminCredentialsPasswordMismatch(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.POST("/oam/setup/update-admin", h.UpdateAdminCredentials)

	// All binding-required fields present and valid, but the confirmation does
	// not match the new password → 400 before any DB access.
	body := `{
		"currentUsername": "admin",
		"currentPassword": "oldpassword",
		"newUsername": "administrator",
		"newPassword": "newpassword1",
		"confirmPassword": "different1",
		"newEmail": "admin@test.local"
	}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oam/setup/update-admin", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "confirmation")
}

func TestProxyToLoxiLBBadInstanceID(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.Any("/oam/loxilbs/:id/netlox/*path", h.ProxyToLoxiLB)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/loxilbs/not-a-number/netlox/config/loadbalancer/all", nil)
	r.ServeHTTP(rec, req)

	// A non-numeric instance id is rejected before the proxy service is called.
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "Invalid LoxiLB instance ID")
}
