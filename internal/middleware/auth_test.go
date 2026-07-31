package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/loxilb-io/loxilb-oam/internal/middleware"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// authRouter builds a minimal router that puts TokenAuthMiddleware in front of
// a trivial 200 handler, backed by the given userService (which may be nil for
// paths that fail before the server-side token-store lookup).
func authRouter(userService *services.UserService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/protected", middleware.TokenAuthMiddleware(userService), func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"ok": true})
	})
	return r
}

func TestTokenAuthMissingHeader(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	authRouter(nil).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "Authorization header is required")
}

func TestTokenAuthInvalidToken(t *testing.T) {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer not-a-real-jwt")
	authRouter(nil).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "Invalid token")
}

func TestTokenAuthExpiredToken(t *testing.T) {
	// A token minted with a negative lifetime is already expired; the JWT layer
	// reports ErrTokenExpired before any DB lookup, so no userService is needed.
	expired, err := utils.GenerateToken("alice", "admin", 1, -1)
	assert.NoError(t, err)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+expired)
	authRouter(nil).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "Token is expired")
}

func TestTokenAuthRevokedToken(t *testing.T) {
	// A signed, unexpired JWT whose value is absent from api_tokens (e.g. after
	// logout) must be rejected as revoked. GenerateToken and ValidateToken share
	// the same process-level signing key, so the token verifies regardless of
	// whether OAM_JWT_SECRET is set in the test environment.
	token, err := utils.GenerateToken("alice", "admin", 1, 60)
	assert.NoError(t, err)

	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	userService := services.NewUserService(db)

	// The store lookup finds no matching, unexpired token → revoked.
	mock.ExpectQuery("SELECT user_id FROM api_tokens WHERE token_value").
		WithArgs(token).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	authRouter(userService).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
	assert.Contains(t, rec.Body.String(), "revoked")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestTokenAuthValidToken(t *testing.T) {
	token, err := utils.GenerateToken("alice", "admin", 1, 60)
	assert.NoError(t, err)

	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	userService := services.NewUserService(db)

	// A live token present in the store passes the middleware.
	mock.ExpectQuery("SELECT user_id FROM api_tokens WHERE token_value").
		WithArgs(token).
		WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow("1"))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/protected", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	authRouter(userService).ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}
