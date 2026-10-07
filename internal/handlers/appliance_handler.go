package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"net/http"
	"regexp"

	"github.com/gin-gonic/gin"

	"github.com/loxilb-io/loxilb-oam/internal/appliance"
	"github.com/loxilb-io/loxilb-oam/internal/middleware"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
)

// ApplianceHandler serves /oam/v1/appliance. It is separate from Handler
// because it shares none of its services.
type ApplianceHandler struct {
	service     *appliance.Service
	userService *services.UserService
}

func NewApplianceHandler(service *appliance.Service, userService *services.UserService) *ApplianceHandler {
	return &ApplianceHandler{service: service, userService: userService}
}

// RequestIDHeader carries the correlation ID of a request. A caller may
// supply one; otherwise OAM generates it. It is echoed on every response and
// repeated in error bodies.
const RequestIDHeader = "X-Request-ID"

const ctxRequestID = "appliance_request_id"

var requestIDRE = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

// requestID returns the request's correlation ID, assigning one on first use.
// A supplied value is accepted only if it is short and plain, since it is
// written to the log and returned in headers.
func requestID(c *gin.Context) string {
	if id := c.GetString(ctxRequestID); id != "" {
		return id
	}
	id := c.GetHeader(RequestIDHeader)
	if !requestIDRE.MatchString(id) {
		b := make([]byte, 12)
		_, _ = rand.Read(b) // crypto/rand.Read does not fail
		id = hex.EncodeToString(b)
	}
	c.Set(ctxRequestID, id)
	c.Header(RequestIDHeader, id)
	return id
}

// writeApplianceError sends the appliance error envelope. The origin is also
// set as a header, where the gateway proxy already puts it.
func writeApplianceError(c *gin.Context, status int, code, origin, message, recovery string) {
	c.Header(services.ErrorOriginHeader, origin)
	c.AbortWithStatusJSON(status, appliance.ErrorBody{
		Error:     message,
		Code:      code,
		Origin:    origin,
		RequestID: requestID(c),
		Recovery:  &appliance.Recovery{Action: recovery},
	})
}

// Require gates an appliance route on a capability, like
// middleware.RequireCapability, but answers with the appliance error
// envelope so a client can tell a denial from every other failure by its
// code. The role is read from the database, not the token.
func (h *ApplianceHandler) Require(action middleware.Action) gin.HandlerFunc {
	return func(c *gin.Context) {
		requestID(c)
		user := middleware.ResolveCaller(c, h.userService)
		if user == nil {
			writeApplianceError(c, http.StatusUnauthorized, appliance.CodeUnauthorized, appliance.OriginOAM,
				"Unauthorized", appliance.RecoveryReauthenticate)
			return
		}
		if !middleware.Can(user.Role, action) {
			utils.LogWarning("RBAC: user '" + user.Username + "' (role " + user.Role + ") denied " + string(action) + " on " + c.FullPath())
			writeApplianceError(c, http.StatusForbidden, appliance.CodePermissionDenied, appliance.OriginOAM,
				"Forbidden: your role does not permit this operation", appliance.RecoveryNone)
			return
		}
		c.Next()
	}
}

// actionCapability maps each appliance action onto the capability that
// permits requesting it.
var actionCapability = map[appliance.Action]middleware.Action{
	appliance.ActionBackup:      middleware.ActApplianceBackup,
	appliance.ActionRestore:     middleware.ActApplianceRestore,
	appliance.ActionUpdate:      middleware.ActApplianceUpdate,
	appliance.ActionRollback:    middleware.ActApplianceRollback,
	appliance.ActionReset:       middleware.ActApplianceReset,
	appliance.ActionDiagnostics: middleware.ActApplianceDiagnostics,
}

// GetCapabilities handles GET /oam/v1/appliance/capabilities.
// @Summary Appliance capabilities (alpha)
// @Description For each whole-Appliance action, reports three independent facts: whether the host adapter supports it, whether it can run now (and if not, why), and whether the caller's role may request it. A deployment with no host adapter reports every action as unsupported with HOST_NOT_CONFIGURED. Contract appliance-ops/v1alpha1 — subject to change.
// @Tags appliance
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Success 200 {object} appliance.Capabilities
// @Failure 401 {object} appliance.ErrorBody
// @Failure 403 {object} appliance.ErrorBody
// @Security BearerAuth
// @Router /oam/v1/appliance/capabilities [get]
func (h *ApplianceHandler) GetCapabilities(c *gin.Context) {
	role := c.GetString(middleware.CtxCallerRole)
	c.JSON(http.StatusOK, h.service.Capabilities(c.Request.Context(), func(action appliance.Action) bool {
		capability, ok := actionCapability[action]
		return ok && middleware.Can(role, capability)
	}))
}

// GetStatus handles GET /oam/v1/appliance/status.
// @Summary Appliance status (alpha)
// @Description Product identity (from the host adapter, when there is one), each component's version with separately observed liveness and readiness, and OAM's database schema version. A component that could not be observed is "unknown" and stale, never ready. Contract appliance-ops/v1alpha1 — subject to change.
// @Tags appliance
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Success 200 {object} appliance.Status
// @Failure 401 {object} appliance.ErrorBody
// @Failure 403 {object} appliance.ErrorBody
// @Security BearerAuth
// @Router /oam/v1/appliance/status [get]
func (h *ApplianceHandler) GetStatus(c *gin.Context) {
	c.JSON(http.StatusOK, h.service.Status(c.Request.Context()))
}
