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
	ActGatewayWrite  Action = "gateway_write"  // day-to-day changes, and operator-level reads, through the gateway proxy
	ActGatewayAdmin  Action = "gateway_admin"  // gateway paths the proxy reserves for administrators
	ActConfigWrite   Action = "config_write"   // configuration export/import
	ActAlertWrite    Action = "alert_write"    // create/acknowledge alerts
	ActLogRead       Action = "log_read"       // read the server log and its archives

	// Whole-Appliance lifecycle. Each is granted on its own: holding
	// config_write, or any other capability above, implies none of them.
	ActApplianceRead        Action = "appliance_read"        // status, capabilities, operation list/detail
	ActApplianceBackup      Action = "appliance_backup"      // back up the whole Appliance
	ActApplianceRestore     Action = "appliance_restore"     // restore it from a backup
	ActApplianceUpdate      Action = "appliance_update"      // install a newer release
	ActApplianceRollback    Action = "appliance_rollback"    // return to the previous release
	ActApplianceReset       Action = "appliance_reset"       // factory reset
	ActApplianceDiagnostics Action = "appliance_diagnostics" // support bundle / sensitive diagnostics export
)

// roleCapabilities is the single-source capability matrix:
// admin = everything; operator = day-to-day gateway/alert work, instance READ
// only; viewer = GET-only everywhere. Reads of *resources* are granted by
// authentication alone and are not listed here.
//
// ActLogRead is the exception: the server log is not a resource, it is a
// process-wide diagnostic stream that can incidentally capture anything the
// code paths touch. Gating it admin-only means a future stray log line cannot
// become a privilege-escalation path for a lower-privileged role.
var roleCapabilities = map[string]map[Action]bool{
	models.RoleAdmin: {
		ActUserAdmin:     true,
		ActInstanceWrite: true,
		ActGatewayWrite:  true,
		ActGatewayAdmin:  true,
		ActConfigWrite:   true,
		ActAlertWrite:    true,
		ActLogRead:       true,

		ActApplianceRead:        true,
		ActApplianceBackup:      true,
		ActApplianceRestore:     true,
		ActApplianceUpdate:      true,
		ActApplianceRollback:    true,
		ActApplianceReset:       true,
		ActApplianceDiagnostics: true,
	},
	// Operator and viewer may see the Appliance's state but change none of
	// it: every lifecycle action starts out admin-only.
	models.RoleOperator: {
		ActGatewayWrite:  true,
		ActAlertWrite:    true,
		ActApplianceRead: true,
	},
	models.RoleViewer: {
		ActApplianceRead: true,
	},
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

// CtxGatewayPath is where RequireGatewayAccess leaves the canonical Gateway
// path it authorized, for the proxy handler to forward.
const CtxGatewayPath = "gateway_path"

// RequireGatewayAccess gates the loxilb gateway proxy by method and Gateway
// path. The proxy speaks to the Gateway with OAM's own identity, which the
// Gateway treats as an administrator, so this is the only place an OAM role
// is held to less than that.
//
// The caller is resolved from the database for every method, reads included,
// so a deleted user or a changed role takes effect on the next request. A
// refusal is OAM's own answer: nothing has been sent to the Gateway.
func RequireGatewayAccess(userService *services.UserService) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Every refusal below is OAM's own answer, and says so.
		user := resolveCaller(c, userService)
		if user == nil {
			services.MarkOAMOrigin(c)
			utils.LogError("RBAC: could not resolve caller for the gateway proxy")
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
			c.Abort()
			return
		}
		if !services.GatewayMethodAllowed(c.Request.Method) {
			services.MarkOAMOrigin(c)
			c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "Method not allowed through the gateway proxy"})
			c.Abort()
			return
		}
		path, err := services.CanonicalGatewayPath(c.Param("path"), c.Request.URL.EscapedPath())
		if err != nil {
			services.MarkOAMOrigin(c)
			utils.LogWarning("RBAC: user '" + user.Username + "' sent a gateway path the proxy does not forward")
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid gateway path"})
			c.Abort()
			return
		}

		allowed := false
		switch services.ClassifyGatewayRequest(c.Request.Method, path) {
		case services.GatewayAccessAuthenticated:
			allowed = true
		case services.GatewayAccessOperator:
			allowed = Can(user.Role, ActGatewayWrite)
		case services.GatewayAccessAdmin:
			allowed = Can(user.Role, ActGatewayAdmin)
		}
		if !allowed {
			services.MarkOAMOrigin(c)
			utils.LogWarning("RBAC: user '" + user.Username + "' (role " + user.Role + ") denied " + c.Request.Method + " " + path.String() + " through the gateway proxy")
			c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden: your role does not permit this operation"})
			c.Abort()
			return
		}
		c.Set(CtxGatewayPath, path)
		c.Next()
	}
}
