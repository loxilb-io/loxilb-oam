package handlers

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

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

// caller builds the service's view of the authenticated user. It must run
// behind Require, which resolved the user from the database.
func (h *ApplianceHandler) caller(c *gin.Context) appliance.Caller {
	user := middleware.ResolveCaller(c, h.userService)
	if user == nil {
		// Unreachable behind Require; a caller with no permissions is the
		// safe answer if it ever is reached.
		return appliance.Caller{Permitted: func(appliance.Action) bool { return false }}
	}
	return appliance.Caller{
		UserID:   user.ID,
		Username: user.Username,
		Permitted: func(action appliance.Action) bool {
			capability, ok := actionCapability[action]
			return ok && middleware.Can(user.Role, capability)
		},
	}
}

var hostCodeRE = regexp.MustCompile(`^[A-Z][A-Z0-9_]{1,63}$`)

// writeOperationError maps a service error onto the envelope. Anything not
// recognized is logged and reported as an internal error without its text.
func writeOperationError(c *gin.Context, err error) {
	var rejection *appliance.HostRejection
	switch {
	case errors.Is(err, appliance.ErrIdempotencyKey):
		writeApplianceError(c, http.StatusBadRequest, appliance.CodeIdempotencyKeyInvalid, appliance.OriginOAM, err.Error(), appliance.RecoveryNone)
	case errors.Is(err, appliance.ErrSchemaVersion):
		writeApplianceError(c, http.StatusBadRequest, appliance.CodeSchemaMismatch, appliance.OriginOAM, err.Error(), appliance.RecoveryNone)
	case errors.Is(err, appliance.ErrInvalidRequest), errors.Is(err, appliance.ErrInvalidFilter):
		writeApplianceError(c, http.StatusBadRequest, appliance.CodeInvalidRequest, appliance.OriginOAM, err.Error(), appliance.RecoveryNone)
	case errors.Is(err, appliance.ErrPermissionDenied):
		writeApplianceError(c, http.StatusForbidden, appliance.CodePermissionDenied, appliance.OriginOAM, "Forbidden: "+err.Error(), appliance.RecoveryNone)
	case errors.Is(err, appliance.ErrOperationNotFound):
		writeApplianceError(c, http.StatusNotFound, appliance.CodeOperationNotFound, appliance.OriginOAM, "Operation not found", appliance.RecoveryNone)
	case errors.Is(err, appliance.ErrIdempotencyKeyReused):
		writeApplianceError(c, http.StatusConflict, appliance.CodeIdempotencyKeyReused, appliance.OriginOAM, err.Error(), appliance.RecoveryNone)
	case errors.Is(err, appliance.ErrHostNotConfigured):
		writeApplianceError(c, http.StatusNotImplemented, appliance.CodeHostNotConfigured, appliance.OriginOAM,
			"This deployment has no Appliance host adapter", appliance.RecoveryNone)
	case errors.As(err, &rejection):
		// The host's code is relayed if it has the shape of one; its message
		// is not, since it may describe the host.
		code := rejection.Code
		if !hostCodeRE.MatchString(code) {
			code = appliance.CodePlanRejected
		}
		writeApplianceError(c, http.StatusUnprocessableEntity, code, appliance.OriginHost,
			"The Appliance host adapter rejected the request", appliance.RecoveryReplan)
	case errors.Is(err, appliance.ErrHostUnreachable):
		utils.LogError("Appliance host adapter: " + err.Error())
		writeApplianceError(c, http.StatusBadGateway, appliance.CodeHostUnreachable, appliance.OriginHost,
			"The Appliance host adapter did not answer", appliance.RecoveryRetry)
	default:
		utils.LogError("Appliance operation failed: " + err.Error())
		writeApplianceError(c, http.StatusInternalServerError, appliance.CodeInternal, appliance.OriginOAM,
			"Internal error", appliance.RecoveryContactSupport)
	}
}

// IdempotencyKeyHeader names the request header that makes planning safe to
// retry.
const IdempotencyKeyHeader = "Idempotency-Key"

// maxPlanRequestBytes bounds the plan request body.
const maxPlanRequestBytes = 16 << 10

// PlanOperation handles POST /oam/v1/appliance/operations.
// @Summary Plan an Appliance operation (alpha)
// @Description Validates the request with the host adapter and records the plan. Nothing is executed. Repeating the request with the same Idempotency-Key returns the same operation (200); the same key with a different request is a conflict (409). The caller's role must hold the capability for the operation type. A plan expires 15 minutes after it is made. Contract appliance-ops/v1alpha1 — subject to change.
// @Tags appliance
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param Idempotency-Key header string true "16-128 printable ASCII characters, unique per intended operation"
// @Param request body appliance.PlanRequest true "What to plan"
// @Success 200 {object} appliance.Operation "The operation this key already created"
// @Success 201 {object} appliance.Operation "Planned"
// @Failure 400 {object} appliance.ErrorBody
// @Failure 401 {object} appliance.ErrorBody
// @Failure 403 {object} appliance.ErrorBody
// @Failure 409 {object} appliance.ErrorBody
// @Failure 422 {object} appliance.ErrorBody "The host adapter rejected the request"
// @Failure 501 {object} appliance.ErrorBody "No host adapter in this deployment"
// @Failure 502 {object} appliance.ErrorBody "The host adapter did not answer"
// @Security BearerAuth
// @Router /oam/v1/appliance/operations [post]
func (h *ApplianceHandler) PlanOperation(c *gin.Context) {
	var req appliance.PlanRequest
	body := http.MaxBytesReader(c.Writer, c.Request.Body, maxPlanRequestBytes)
	decoder := json.NewDecoder(body)
	// An unknown field is refused: on an alpha contract it is far more
	// likely a renamed field silently ignored than something harmless.
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&req); err != nil {
		writeApplianceError(c, http.StatusBadRequest, appliance.CodeInvalidRequest, appliance.OriginOAM,
			"Invalid request body", appliance.RecoveryNone)
		return
	}
	op, created, err := h.service.PlanOperation(c.Request.Context(), h.caller(c), c.GetHeader(IdempotencyKeyHeader), requestID(c), req)
	if err != nil {
		writeOperationError(c, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
		utils.LogInfo("Appliance operation planned: id=" + op.ID + " type=" + string(op.Type) + " by=" + op.Actor)
	}
	c.JSON(status, op)
}

// GetOperation handles GET /oam/v1/appliance/operations/:operation_id.
// @Summary Read an Appliance operation (alpha)
// @Description Returns one operation. The plan — what it would touch and the artifacts involved — is included only for callers whose role may run that operation type; for others `redacted` is true.
// @Tags appliance
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param operation_id path string true "Operation ID"
// @Success 200 {object} appliance.Operation
// @Failure 401 {object} appliance.ErrorBody
// @Failure 403 {object} appliance.ErrorBody
// @Failure 404 {object} appliance.ErrorBody
// @Security BearerAuth
// @Router /oam/v1/appliance/operations/{operation_id} [get]
func (h *ApplianceHandler) GetOperation(c *gin.Context) {
	op, err := h.service.GetOperation(c.Request.Context(), h.caller(c), c.Param("operation_id"))
	if err != nil {
		writeOperationError(c, err)
		return
	}
	c.JSON(http.StatusOK, op)
}

// ListOperations handles GET /oam/v1/appliance/operations.
// @Summary List Appliance operations (alpha)
// @Description Newest first. `items` is always an array. Pass `next_cursor` back as `cursor` for the next page.
// @Tags appliance
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param limit query int false "Page size, 1-100 (default 20)"
// @Param cursor query string false "next_cursor of the previous page"
// @Param state query string false "Only operations in this state"
// @Param type query string false "Only operations of this type"
// @Success 200 {object} appliance.OperationList
// @Failure 400 {object} appliance.ErrorBody
// @Failure 401 {object} appliance.ErrorBody
// @Failure 403 {object} appliance.ErrorBody
// @Security BearerAuth
// @Router /oam/v1/appliance/operations [get]
func (h *ApplianceHandler) ListOperations(c *gin.Context) {
	filter := appliance.ListFilter{
		State:  appliance.OperationState(c.Query("state")),
		Type:   appliance.OperationType(c.Query("type")),
		Cursor: c.Query("cursor"),
	}
	if raw := c.Query("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit == 0 {
			writeOperationError(c, fmt.Errorf("%w: limit", appliance.ErrInvalidFilter))
			return
		}
		filter.Limit = limit
	}
	list, err := h.service.ListOperations(c.Request.Context(), h.caller(c), filter)
	if err != nil {
		writeOperationError(c, err)
		return
	}
	c.JSON(http.StatusOK, list)
}
