package middleware

import (
	"net/http"

	"github.com/loxilb-io/loxilb-oam/internal/config"

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
		c.Header("Access-Control-Allow-Headers", "Origin, Content-Type, Authorization")

		// Handle preflight request
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(http.StatusOK)
			return
		}

		c.Next()
	}
}
