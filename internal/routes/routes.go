package routes

import (
	"database/sql"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/handlers"
	"github.com/loxilb-io/loxilb-oam/internal/middleware"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"sync"

	// swagger-generated OpenAPI spec; blank import registers it for the docs route
	_ "github.com/loxilb-io/loxilb-oam/docs"

	"github.com/gin-gonic/gin"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

func SetupRoutes(router *gin.Engine, db *sql.DB, dsn, ssl_option, sslCaCertFilePath, sslCaClientCertFilePath, sslCaClientKeyFilePath string, handler *handlers.Handler, userService *services.UserService, mu *sync.Mutex, alertService *services.AlertService) {
	// Handle preflight requests
	router.Use(middleware.CORSMiddleware())

	// Per-IP rate limiters: credential endpoints get a tight budget,
	// the gateway proxy a generous one that still bounds abuse.
	authLimiter := middleware.NewRateLimiter(config.LoginRateLimitRPS, config.LoginRateLimitBurst)
	proxyLimiter := middleware.NewRateLimiter(config.ProxyRateLimitRPS, config.ProxyRateLimitBurst)

	// Public routes
	router.POST("/oam/login", middleware.RateLimit(authLimiter), handler.Login)

	// Setup routes (no authentication required for initial admin setup)
	router.GET("/oam/setup/status", handler.GetSetupStatus)
	router.POST("/oam/setup/update-admin", middleware.RateLimit(authLimiter), handler.UpdateAdminCredentials)

	// SECURITY: the unauthenticated POST /oam/admin/reset route was REMOVED.
	// It allowed any anonymous caller
	// to reset the admin account to known default credentials and take over.
	// Admin reset is now a break-glass, local-only operation via the
	// `cmd/reset_admin` CLI (requires shell access to the host).

	// OAuth Routes (opt-in, experimental — only registered when enabled).
	if config.OAuthEnabled() {
		router.GET("/oam/oauth/:provider", handler.OAuthLogin)
		router.GET("/oam/oauth/:provider/callback", handler.OAuthCallback)
	}

	// Serve Swagger API documentation
	router.GET("/oam/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))

	// Apply DBCheckMiddleware only to the /health endpoint
	router.GET("/oam/health", middleware.DBCheckMiddleware(&db, dsn, mu, ssl_option, sslCaCertFilePath, sslCaClientCertFilePath, sslCaClientKeyFilePath, alertService), handler.HealthCheck)

	// Protected routes with basic auth only
	protected := router.Group("/oam")
	protected.Use(middleware.TokenAuthMiddleware(userService))
	{
		// Logout
		protected.POST("/logout", handler.Logout)

		// Basic User Management
		protected.GET("/users/me", handler.GetMe)

		// User administration — admin role required.
		// Listing users, creating users (incl. setting roles), and deleting
		// users are privileged operations. Self-service is handled separately:
		// GET /users/me (own profile) and PUT /users/:id (self-or-admin, with
		// role changes gated to admins inside the handler).
		protected.GET("/users", middleware.RequireAdmin(userService), handler.GetUsers)
		protected.POST("/users", middleware.RequireAdmin(userService), handler.CreateUser)
		protected.PUT("/users/:id", handler.UpdateUser)
		protected.DELETE("/users/:id", middleware.RequireAdmin(userService), handler.DeleteUser)

		// LoxiLB Instance Management — reads for all roles, mutations admin-only
		// (operator has instance READ, no instance write/delete)
		protected.GET("/loxilbs", handler.GetLoxiLBInstances)
		protected.GET("/loxilbs/:id", handler.GetLoxiLBInstanceByID)
		protected.POST("/loxilbs", middleware.RequireCapability(userService, middleware.ActInstanceWrite), handler.CreateLoxiLBInstance)
		protected.PUT("/loxilbs/:id", middleware.RequireCapability(userService, middleware.ActInstanceWrite), handler.UpdateLoxiLBInstance)
		protected.DELETE("/loxilbs/:id", middleware.RequireCapability(userService, middleware.ActInstanceWrite), handler.DeleteLoxiLBInstance)

		// Log Monitoring
		protected.GET("/logs", handler.GetLogs)
		protected.GET("/logs/archives", handler.GetLogArchives)
		protected.GET("/logs/archives/:filename", handler.GetLogArchivesFilename)

		// Alert Monitoring — acknowledging/creating alerts is day-to-day
		// operation (admin + operator); viewer is read-only
		protected.POST("/alerts", middleware.RequireCapability(userService, middleware.ActAlertWrite), handler.CreateAlert)
		protected.GET("/alerts", handler.GetActiveAlerts)
		protected.PUT("/alerts/:id/acknowledge", middleware.RequireCapability(userService, middleware.ActAlertWrite), handler.AcknowledgeAlert)
		protected.GET("/alerts/history", handler.GetAlertHistory)

		// Legacy /oam/config/* (OAM DB export/import mislabeled "Config
		// Management") was REMOVED — replaced by the instance-snapshot
		// feature below.

		// Instance Snapshots —
		// take/upload/restore/delete/patch/schedule-write/DOWNLOAD require
		// config_write (the document carries IPsec PSKs and cert private
		// keys); list and metadata reads are open to all authenticated roles.
		protected.POST("/instances/:id/snapshots", middleware.RequireCapability(userService, middleware.ActConfigWrite), handler.TakeSnapshot)
		protected.GET("/instances/:id/snapshots", handler.ListSnapshots)
		protected.POST("/instances/:id/snapshots/upload", middleware.RequireCapability(userService, middleware.ActConfigWrite), handler.UploadSnapshot)
		protected.GET("/instances/:id/snapshot-schedule", handler.GetSnapshotSchedule)
		protected.PUT("/instances/:id/snapshot-schedule", middleware.RequireCapability(userService, middleware.ActConfigWrite), handler.PutSnapshotSchedule)
		protected.GET("/snapshots/:sid", handler.GetSnapshot)
		protected.PATCH("/snapshots/:sid", middleware.RequireCapability(userService, middleware.ActConfigWrite), handler.UpdateSnapshot)
		protected.DELETE("/snapshots/:sid", middleware.RequireCapability(userService, middleware.ActConfigWrite), handler.DeleteSnapshot)
		protected.GET("/snapshots/:sid/download", middleware.RequireCapability(userService, middleware.ActConfigWrite), handler.DownloadSnapshot)
		protected.POST("/snapshots/:sid/restore", middleware.RequireCapability(userService, middleware.ActConfigWrite), handler.RestoreSnapshot)

		// LoxiLB Proxy - Forward requests to LoxiLB instances (auth-only, no license gate).
		// Method-gated: GET/HEAD/OPTIONS for all roles, mutating
		// methods require gateway_write (admin/operator).
		protected.Any("/loxilbs/:id/netlox/*path", middleware.RateLimit(proxyLimiter), middleware.RequireGatewayCapability(userService), handler.ProxyToLoxiLB)

		// LoxiLB Firmware Management — instance mutation, admin-only
		protected.PUT("/loxilbs/:id/firmware", middleware.RequireCapability(userService, middleware.ActInstanceWrite), handler.UpdateLoxiLBInstanceFirmware)
		protected.PUT("/loxilbs/:id/firmware/start", middleware.RequireCapability(userService, middleware.ActInstanceWrite), handler.StartLoxiLBInstanceFirmware)
		protected.PUT("/loxilbs/:id/firmware/stop", middleware.RequireCapability(userService, middleware.ActInstanceWrite), handler.StoptLoxiLBInstanceFirmware)
	}
}
