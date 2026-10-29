package utils

import (
	"github.com/gin-gonic/gin"
	"net/http"
)

// AuthenticationUnavailable fails closed without implying that an otherwise
// valid session was revoked or disclosing authentication-store error details.
func AuthenticationUnavailable(c *gin.Context) {
	c.Header("Retry-After", "5")
	c.JSON(http.StatusServiceUnavailable, gin.H{
		"error": "Authentication is temporarily unavailable", "code": "authentication_unavailable",
	})
}
