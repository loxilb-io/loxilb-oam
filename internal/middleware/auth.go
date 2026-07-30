package middleware

import (
	"net/http"
	"strings"

	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"

	"github.com/gin-gonic/gin"
)

/*
TokenAuthMiddleware is a middleware that checks the Authorization header for a valid token.
A token must pass two checks: (1) JWT signature + expiry, and (2) presence in the
server-side token store (api_tokens), which is where logout deletes it — without
the store check a "logged-out" token keeps working until natural expiry.
If the token is valid, it sets the username in the context and calls the next handler.
If the token is invalid or revoked, it returns a 401 Unauthorized response.
*/
func TokenAuthMiddleware(userService *services.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		token := c.GetHeader("Authorization")
		if token == "" {
			utils.LogError("Authorization header is required")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Authorization header is required"})
			c.Abort()
			return
		}

		token = strings.TrimPrefix(token, "Bearer ")
		claims, err := utils.ValidateToken(token)
		if err != nil {
			if err == utils.ErrTokenExpired {
				utils.LogWarning("Token is expired")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Token is expired"})
			} else {
				utils.LogError("Invalid token")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			}
			c.Abort()
			return
		}

		// A signed, unexpired JWT is not sufficient — it must still exist
		// in the server-side token store. Fail closed on store errors.
		valid, err := userService.ValidateToken(token)
		if err != nil {
			utils.LogError("Token store lookup failed: " + err.Error())
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Could not validate token"})
			c.Abort()
			return
		}
		if !valid {
			utils.LogWarning("Rejected revoked token for user: " + claims.Username)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Token has been revoked"})
			c.Abort()
			return
		}

		// Set the username in the context
		utils.LogInfo("Token validated for user: " + claims.Username)
		c.Set("username", claims)

		c.Next()
	}
}
