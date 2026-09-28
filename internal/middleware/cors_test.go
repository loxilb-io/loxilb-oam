package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/middleware"
	"github.com/loxilb-io/loxilb-oam/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// A cross-origin console can read only the response headers CORS exposes.
// Unexposed, the error-origin marker reads as absent, and the console treats
// every Gateway 401 as its own session ending: the marker must be exposed in
// both CORS modes, on preflight and on the actual response.
func TestCORSExposesTheErrorOriginMarker(t *testing.T) {
	saved := config.AllowedOrigins
	t.Cleanup(func() { config.AllowedOrigins = saved })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.CORSMiddleware())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusUnauthorized) })

	for name, origins := range map[string][]string{
		"wildcard":  nil,
		"allowlist": {"http://localhost:3000"},
	} {
		config.AllowedOrigins = origins
		for _, method := range []string{http.MethodOptions, http.MethodGet} {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(method, "/x", nil)
			req.Header.Set("Origin", "http://localhost:3000")
			r.ServeHTTP(rec, req)
			assert.Equal(t, services.ErrorOriginHeader, rec.Header().Get("Access-Control-Expose-Headers"), "%s %s", name, method)
		}
	}
}
