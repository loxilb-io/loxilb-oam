package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/middleware"
	"github.com/loxilb-io/loxilb-oam/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// corsHeaderList splits a comma-separated CORS header value.
func corsHeaderList(value string) []string {
	var out []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			out = append(out, item)
		}
	}
	return out
}

// A cross-origin console can read only the response headers CORS exposes, and
// send only the request headers it allows. Unexposed, the error-origin marker
// reads as absent and the console treats every Gateway 401 as its own session
// ending; Retry-After reads as absent and a 429 or 503 cannot be honoured.
// Both lists must hold in both CORS modes, on preflight and on the response.
func TestCORSHeaderLists(t *testing.T) {
	saved := config.AllowedOrigins
	t.Cleanup(func() { config.AllowedOrigins = saved })

	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(middleware.CORSMiddleware())
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusUnauthorized) })

	wantExposed := []string{
		services.ErrorOriginHeader, "Retry-After", "X-Request-Id", "X-Correlation-Id",
		"X-Snapshot-Checksum", "X-Content-Checksum", "Content-Disposition",
	}
	wantAllowed := append([]string{"Origin", "Authorization", "Content-Type"}, services.GatewayRequestHeaders()...)
	// The headers this exists for, named so that removing one from the
	// proxy's list fails here rather than passing silently.
	wantAllowed = append(wantAllowed, "If-Match", "If-None-Match", "X-Request-Id", "X-Correlation-Id")

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
			assert.ElementsMatch(t, wantExposed, corsHeaderList(rec.Header().Get("Access-Control-Expose-Headers")), "%s %s", name, method)
			assert.Subset(t, corsHeaderList(rec.Header().Get("Access-Control-Allow-Headers")), wantAllowed, "%s %s", name, method)
		}
	}
}
