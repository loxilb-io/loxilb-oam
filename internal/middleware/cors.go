package middleware

import (
	"net/http"
	"strings"

	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/services"

	"github.com/gin-gonic/gin"
)

// CORSMiddleware sets the Cross-Origin Resource Sharing headers.
//
// When OAM_ALLOWED_ORIGINS is set (comma-separated allowlist), the request's
// Origin is reflected only if it matches, with credentials allowed and
// "Vary: Origin" for caches; non-matching origins get no CORS headers (the
// browser blocks the response). When unset, falls back to "*" WITHOUT
// credentials — a dev convenience flagged by a startup SECURITY warning.
// The UI authenticates via the Authorization header (not cookies), which
// works in both modes.
func CORSMiddleware() gin.HandlerFunc {
	allowHeaders := strings.Join(corsAllowedRequestHeaders(), ", ")
	exposeHeaders := strings.Join(corsExposedResponseHeaders, ", ")
	return func(c *gin.Context) {
		if config.CORSUsingWildcard() {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if origin := c.GetHeader("Origin"); origin != "" {
			for _, allowed := range config.AllowedOrigins {
				if origin == allowed {
					c.Header("Access-Control-Allow-Origin", origin)
					c.Header("Access-Control-Allow-Credentials", "true")
					c.Header("Vary", "Origin")
					break
				}
			}
		}
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		c.Header("Access-Control-Allow-Headers", allowHeaders)
		c.Header("Access-Control-Expose-Headers", exposeHeaders)

		// Handle preflight request
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusOK)
			return
		}

		c.Next()
	}
}

// corsExposedResponseHeaders are the response headers cross-origin JavaScript
// may read. A browser hides every header that is not safelisted unless it is
// exposed, so a console served from another origin sees none of these
// otherwise: without the error-origin marker it treats every Gateway 401 as
// its own session ending, and without Retry-After it cannot honour the wait a
// 429 or a 503 asked for. Same-origin deployments are unaffected.
var corsExposedResponseHeaders = []string{
	services.ErrorOriginHeader,
	"Retry-After",
	"X-Request-Id",
	"X-Correlation-Id",
	"X-Snapshot-Checksum",
	"X-Content-Checksum",
	"Content-Disposition",
}

// corsAllowedRequestHeaders are the request headers a cross-origin console
// may send: OAM's own, and every header the instance proxy forwards. Taking
// the second set from the proxy keeps the two from drifting apart.
func corsAllowedRequestHeaders() []string {
	headers := []string{"Origin", "Authorization"}
	return append(headers, services.GatewayRequestHeaders()...)
}
