package middleware

import (
	"database/sql"
	"fmt"
	"net/http"

	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"

	"github.com/gin-gonic/gin"
)

// DBCheckMiddleware returns a gin middleware that verifies database
// reachability before the handler runs, and answers 503 when the database is
// unreachable.
//
// It does not attempt to reconnect. *sql.DB is a pool that redials on its own;
// a failed Ping means no healthy connection was available at that instant, not
// that the pool is dead. The earlier implementation replaced the pool on a
// failed ping, but every service holds the *sql.DB it was constructed with, so
// the replacement was only ever visible here — leaving the rest of the server
// pointed at a pool this middleware had already closed.
func DBCheckMiddleware(db *sql.DB, alertService *services.AlertService) gin.HandlerFunc {
	return func(c *gin.Context) {
		if err := db.Ping(); err != nil {
			utils.LogError(fmt.Sprintf("Database connection lost: %s", err))

			// Record an alert for the lost connection. InstanceID 0 means
			// "the OAM server itself" rather than a managed instance.
			alertReq := models.CreateAlertRequest{
				InstanceID: 0,
				Type:       config.AlertTypeDBDisconnect,
				Severity:   config.SeverityCritical,
				Message:    fmt.Sprintf("Database connection lost: %s", err),
			}
			if _, alertErr := alertService.CreateAlert(alertReq); alertErr != nil {
				utils.LogError(fmt.Sprintf("Failed to create alert: %s", alertErr))
			}

			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Database connection lost"})
			c.Abort()
			return
		}
		c.Next()
	}
}
