package tests

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/middleware"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestCapabilityMatrix(t *testing.T) {
	tests := []struct {
		role   string
		action middleware.Action
		want   bool
	}{
		// admin holds everything
		{models.RoleAdmin, middleware.ActUserAdmin, true},
		{models.RoleAdmin, middleware.ActInstanceWrite, true},
		{models.RoleAdmin, middleware.ActGatewayWrite, true},
		{models.RoleAdmin, middleware.ActConfigWrite, true},
		{models.RoleAdmin, middleware.ActAlertWrite, true},
		// operator: gateway + alerts only
		{models.RoleOperator, middleware.ActGatewayWrite, true},
		{models.RoleOperator, middleware.ActAlertWrite, true},
		{models.RoleOperator, middleware.ActUserAdmin, false},
		{models.RoleOperator, middleware.ActInstanceWrite, false},
		{models.RoleOperator, middleware.ActConfigWrite, false},
		// legacy "user" behaves as operator
		{models.RoleLegacyUser, middleware.ActGatewayWrite, true},
		{models.RoleLegacyUser, middleware.ActUserAdmin, false},
		// viewer: nothing
		{models.RoleViewer, middleware.ActGatewayWrite, false},
		{models.RoleViewer, middleware.ActAlertWrite, false},
		{models.RoleViewer, middleware.ActUserAdmin, false},
		// unknown role: nothing
		{"bogus", middleware.ActGatewayWrite, false},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, middleware.Can(tt.role, tt.action),
			"Can(%q, %q)", tt.role, tt.action)
	}
}

func TestRoleHelpers(t *testing.T) {
	assert.Equal(t, models.RoleOperator, models.NormalizeRole(models.RoleLegacyUser))
	assert.Equal(t, models.RoleAdmin, models.NormalizeRole(models.RoleAdmin))

	assert.True(t, models.IsValidRole(models.RoleAdmin))
	assert.True(t, models.IsValidRole(models.RoleOperator))
	assert.True(t, models.IsValidRole(models.RoleViewer))
	assert.True(t, models.IsValidRole(models.RoleLegacyUser))
	assert.False(t, models.IsValidRole("root"))
	assert.False(t, models.IsValidRole(""))
}

// gatewayRequest exercises RequireGatewayCapability with a sqlmock-backed
// user of the given role behind valid JWT claims, returning the status code.
func gatewayRequest(t *testing.T, role, method string) int {
	t.Helper()
	gin.SetMode(gin.TestMode)

	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock: %v", err)
	}
	defer db.Close()

	userService := services.NewUserService(db)

	// Mutating methods resolve the caller from the DB; safe methods do not.
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
	default:
		rows := sqlmock.NewRows([]string{"id", "username", "email", "role", "oauth_provider", "oauth_id", "created_at"}).
			AddRow(7, "alice", "alice@test.local", role, nil, nil, time.Now())
		mock.ExpectQuery("SELECT id, username, email, role, oauth_provider, oauth_id, created_at FROM users WHERE username = ?").
			WithArgs("alice").
			WillReturnRows(rows)
	}

	router := gin.New()
	// Simulate TokenAuthMiddleware having placed the claims in the context.
	router.Use(func(c *gin.Context) {
		c.Set("username", &utils.Claims{Username: "alice", Role: role})
	})
	router.Any("/proxy/*path", middleware.RequireGatewayCapability(userService), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"forwarded": true})
	})

	req := httptest.NewRequest(method, "/proxy/v1/version", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec.Code
}

func TestGatewayMethodGating(t *testing.T) {
	// Reads pass for every role
	for _, role := range []string{models.RoleAdmin, models.RoleOperator, models.RoleViewer, models.RoleLegacyUser} {
		assert.Equal(t, http.StatusOK, gatewayRequest(t, role, http.MethodGet), "GET as %s", role)
	}

	// Writes: admin/operator (and legacy user) pass, viewer is blocked
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch} {
		assert.Equal(t, http.StatusOK, gatewayRequest(t, models.RoleAdmin, method), "%s as admin", method)
		assert.Equal(t, http.StatusOK, gatewayRequest(t, models.RoleOperator, method), "%s as operator", method)
		assert.Equal(t, http.StatusOK, gatewayRequest(t, models.RoleLegacyUser, method), "%s as legacy user", method)
		assert.Equal(t, http.StatusForbidden, gatewayRequest(t, models.RoleViewer, method), "%s as viewer", method)
	}
}
