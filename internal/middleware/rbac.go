package middleware

import (
	"net/http"

	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"

	"github.com/gin-gonic/gin"
)

// Context keys populated by ResolveCaller / RequireAdmin so downstream
// handlers can make self-vs-admin authorization decisions without repeating
// the username->user lookup.
const (
	CtxCallerUser = "caller_user"
	CtxCallerRole = "caller_role"
)

// Action is a named capability that a role may or may not hold. Routes ask
// Can(role, action) via RequireCapability instead of hardcoding role names.
type Action string

const (
	ActUserAdmin     Action = "user_admin"     // user management (list/create/delete users, change roles)
	ActInstanceWrite Action = "instance_write" // create/update/delete loxilb instances, firmware ops
	ActGatewayWrite  Action = "gateway_write"  // mutating methods through the gateway proxy
	ActConfigWrite   Action = "config_write"   // configuration export/import
	ActAlertWrite    Action = "alert_write"    // create/acknowledge alerts
)

// roleCapabilities is the single-source capability matrix:
// admin = everything; operator = day-to-day gateway/alert work, instance READ
// only; viewer = GET-only everywhere. Reads are granted by authentication
// alone and are not listed here.
var roleCapabilities = map[string]map[Action]bool{
	models.RoleAdmin: {
		ActUserAdmin:     true,
		ActInstanceWrite: true,
		ActGatewayWrite:  true,
		ActConfigWrite:   true,
		ActAlertWrite:    true,
	},
	models.RoleOperator: {
		ActGatewayWrite: true,
		ActAlertWrite:   true,
	},
	models.RoleViewer: {},
}

// Can reports whether a role holds a capability. Legacy role "user" is
// treated as operator.
func Can(role string, action Action) bool {
	caps, ok := roleCapabilities[models.NormalizeRole(role)]
	return ok && caps[action]
}

// resolveCaller loads the authenticated user from the JWT claims that
// TokenAuthMiddleware placed in the context, and caches it for the request.
// Returns nil if claims are missing/invalid or the user no longer exists.
func resolveCaller(c *gin.Context, userService *services.UserService) *models.User {
	if cached, ok := c.Get(CtxCallerUser); ok {
		if u, ok := cached.(*models.User); ok {
			return u
		}
	}

	claimsInterface, exists := c.Get("username")
	if !exists {
		return nil
	}
	claims, ok := claimsInterface.(*utils.Claims)
	if !ok {
		return nil
	}
	user, err := userService.GetUserByUsername(claims.Username)
	if err != nil || user == nil {
		return nil
	}
	c.Set(CtxCallerUser, user)
	c.Set(CtxCallerRole, user.Role)
	return user
}

// ResolveCaller loads the caller into the request context and returns it, for
// handlers that need self-vs-admin logic (e.g. UpdateUser). Must run after
// TokenAuthMiddleware. Returns nil when the caller cannot be resolved.
func ResolveCaller(c *gin.Context, userService *services.UserService) *models.User {
	return resolveCaller(c, userService)
}

// RequireAdmin aborts the request with 403 unless the authenticated caller has
// the admin role. Apply after TokenAuthMiddleware. Used to gate user
// administration and other privileged operations.
func RequireAdmin(userService *services.UserService) gin.HandlerFunc {
	return RequireCapability(userService, ActUserAdmin)
}

// RequireCapability aborts with 403 unless the authenticated caller's role
// holds the given capability. The role is resolved from the DB (not JWT
// claims) so a role change takes effect without waiting for token expiry.
// Apply after TokenAuthMiddleware.
func RequireCapability(userService *services.UserService, action Action) gin.HandlerFunc {
	return func(c *gin.Context) {
		user := resolveCaller(c, userService)
		if user == nil {
			utils.LogError("RBAC: could not resolve caller for capability-gated route")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}
		if !Can(user.Role, action) {
			utils.LogWarning("RBAC: user '" + user.Username + "' (role " + user.Role + ") denied " + string(action) + " on " + c.FullPath())
			c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden: your role does not permit this operation"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// RequireGatewayCapability gates the loxilb gateway proxy by HTTP method:
// safe methods pass on authentication alone; mutating methods require
// the gateway_write capability (admin/operator — viewer is read-only).
func RequireGatewayCapability(userService *services.UserService) gin.HandlerFunc {
	writeCheck := RequireCapability(userService, ActGatewayWrite)
	return func(c *gin.Context) {
		switch c.Request.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
			c.Next()
			return
		}
		writeCheck(c)
	}
}
