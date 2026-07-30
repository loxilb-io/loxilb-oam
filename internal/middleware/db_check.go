package middleware

import (
	"database/sql"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"

	"github.com/gin-gonic/gin"
)

// DBCheckMiddleware returns a gin middleware that verifies the database
// connection on each request. If the connection is lost, it raises an alert
// and attempts to reconnect (using TLS when ssl_option is "true"), updating the
// shared *db pointer under mu on success.
func DBCheckMiddleware(db **sql.DB, dsn string, mu *sync.Mutex, ssl_option, sslCaCertFilePath, sslCaClientCertFilePath, sslCaClientKeyFilePath string, alertService *services.AlertService) gin.HandlerFunc {
	return func(c *gin.Context) {
		mu.Lock()
		defer mu.Unlock()

		if err := (*db).Ping(); err != nil {
			utils.LogError(fmt.Sprintf("Database connection lost: %s", err))

			// Create an alert for the lost connection
			alertReq := models.CreateAlertRequest{
				InstanceID: 0, // Assuming 0 for the instance ID, update as needed
				Type:       config.AlertTypeDBDisconnect,
				Severity:   config.SeverityCritical,
				Message:    fmt.Sprintf("Database connection lost: %s", err),
			}
			_, alertErr := alertService.CreateAlert(alertReq)
			if alertErr != nil {
				utils.LogError(fmt.Sprintf("Failed to create alert: %s", alertErr))
			}

			// Attempt to reconnect according to the SSL option
			var newDB *sql.DB
			var err error
			if ssl_option == "true" {
				newDB, err = services.ConnectWithSecureTLS(dsn, config.DbMaxRetries, config.DbRetryBackoff, sslCaCertFilePath, sslCaClientCertFilePath, sslCaClientKeyFilePath)
			} else {
				newDB, err = services.ConnectWithRetry(dsn, 5, 2*time.Second)
			}

			if err != nil {
				utils.LogError(fmt.Sprintf("Reconnection failed: %s", err))
				c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Database connection lost"})
				c.Abort()
				return
			}

			// Update the db pointer
			*db = newDB
		}
		c.Next()
	}
}
