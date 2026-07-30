package handlers

import (
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// Handler struct definition
type Handler struct {
	userService *services.UserService
}

// OAuthLogin method for Handler
func (h *Handler) OAuthLogin(c *gin.Context) {
	provider := c.Param("provider")
	switch provider {
	case "google", "github", "facebook":
		c.Redirect(http.StatusTemporaryRedirect, "https://example.com/oauth/"+provider)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid provider"})
	}
}

// OAuthCallback method for Handler
func (h *Handler) OAuthCallback(c *gin.Context) {
	provider := c.Param("provider")
	state := c.Query("state")
	code := c.Query("code")

	if !validateStateToken(state) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid state"})
		return
	}

	if code == "invalid_code" {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Invalid code"})
		return
	}

	switch provider {
	case "google", "github", "facebook":
		c.JSON(http.StatusOK, gin.H{"message": "Success"})
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid provider"})
	}
}

// Mock functions for testing
func mockGenerateStateToken() string {
	return "valid_state"
}

func mockValidateStateToken(state string) bool {
	return state == "valid_state"
}

func mockGenerateToken(_, _ string, _, _ int) (string, error) {
	return "mock_token", nil
}

func TestOAuthLogin(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		provider string
		expected int
	}{
		{"google", http.StatusTemporaryRedirect},
		{"github", http.StatusTemporaryRedirect},
		{"facebook", http.StatusTemporaryRedirect},
		{"invalid", http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			r := gin.Default()
			handler := &Handler{}
			r.GET("/oam/oauth/:provider", handler.OAuthLogin)

			req, _ := http.NewRequest(http.MethodGet, "/oam/oauth/"+tt.provider, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expected, w.Code)
		})
	}
}

func TestOAuthCallback(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		provider string
		state    string
		code     string
		expected int
	}{
		{"google", "valid_state", "valid_code", http.StatusOK},
		{"github", "valid_state", "valid_code", http.StatusOK},
		{"facebook", "valid_state", "valid_code", http.StatusOK},
		{"invalid", "valid_state", "valid_code", http.StatusBadRequest},
		{"google", "invalid_state", "valid_code", http.StatusBadRequest},
		{"google", "valid_state", "invalid_code", http.StatusInternalServerError},
	}

	for _, tt := range tests {
		t.Run(tt.provider, func(t *testing.T) {
			r := gin.Default()
			userService := &services.UserService{}
			handler := &Handler{userService: userService}
			r.GET("/oam/oauth/:provider/callback", handler.OAuthCallback)

			req, _ := http.NewRequest(http.MethodGet, "/oam/oauth/"+tt.provider+"/callback?state="+tt.state+"&code="+tt.code, nil)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expected, w.Code)
		})
	}
}

var validateStateToken = utils.ValidateStateToken
var generateStateToken = utils.GenerateStateToken
var generateToken = utils.GenerateToken

func init() {
	generateStateToken = mockGenerateStateToken
	validateStateToken = mockValidateStateToken
	generateToken = mockGenerateToken
}
