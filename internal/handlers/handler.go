package handlers

import (
	"crypto/subtle"
	"database/sql"
	"errors"
	"fmt"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

var (
	cursorMap sync.Map
)

// Handler struct holds the services and configuration needed for handling requests.
type Handler struct {
	userService     *services.UserService
	loxilbService   *services.LoxiLBService
	logService      *services.LogService
	alertService    *services.AlertService
	proxyService    *services.ProxyService
	snapshotService *services.SnapshotService

	expirationMinutes int
}

func NewHandler(userService *services.UserService, loxilbService *services.LoxiLBService, logService *services.LogService, alertService *services.AlertService, proxyService *services.ProxyService, snapshotService *services.SnapshotService, expirationMinutes int) *Handler {
	return &Handler{
		userService:       userService,
		loxilbService:     loxilbService,
		logService:        logService,
		alertService:      alertService,
		proxyService:      proxyService,
		snapshotService:   snapshotService,
		expirationMinutes: expirationMinutes,
	}
}

// Login handles user login requests.
// @Summary User login
// @Description Authenticates a user and returns a JWT token with comprehensive license information if the credentials are valid.
// @Tags auth
// @Accept json
// @Produce json
// @Param loginRequest body models.LoginRequest true "User credentials"
// @Success 200 {object} models.EnhancedLoginResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 429 {object} models.ErrorResponse "Too many failed login attempts"
// @Failure 500 {object} models.ErrorResponse
// @Router /oam/login [post]
func (h *Handler) Login(c *gin.Context) {
	var loginRequest models.LoginRequest
	if err := c.ShouldBindJSON(&loginRequest); err != nil {
		utils.LogError("Failed to bind JSON: " + err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Get client IP for login attempt tracking
	clientIP := c.ClientIP()
	now := time.Now()

	// Check if login is currently blocked due to too many failed attempts
	isBlocked, remainingTime, err := h.userService.IsLoginBlocked(loginRequest.Username, clientIP, now)
	if err != nil {
		utils.LogError("Failed to check login block status: " + err.Error())
		// Continue with login - don't fail on tracking errors
	}

	if isBlocked {
		utils.LogWarning(fmt.Sprintf("Login attempt blocked for user %s from IP %s, remaining lockout time: %v",
			loginRequest.Username, clientIP, remainingTime))

		// Set Retry-After header with remaining seconds
		retryAfterSeconds := int(remainingTime.Seconds()) + 1
		c.Header("Retry-After", strconv.Itoa(retryAfterSeconds))

		// Return consistent error message to prevent user enumeration
		c.JSON(http.StatusTooManyRequests, gin.H{
			"error":               "Too many failed login attempts. Please try again later. Retry after " + strconv.Itoa(retryAfterSeconds) + " seconds.",
			"retry_after_seconds": retryAfterSeconds,
		})
		return
	}

	// Validate user credentials using the service
	user_id, _, valid, err := h.userService.ValidateUser(loginRequest.Username, loginRequest.Password)
	if !valid || err != nil {
		// Record failed login attempt
		blockedUntil, recordErr := h.userService.RecordFailedLogin(loginRequest.Username, clientIP, now)
		if recordErr != nil {
			utils.LogError("Failed to record failed login attempt: " + recordErr.Error())
		}

		// Check if this failure triggered a lockout
		if blockedUntil != nil {
			remainingTime := time.Until(*blockedUntil)
			retryAfterSeconds := int(remainingTime.Seconds()) + 1
			c.Header("Retry-After", strconv.Itoa(retryAfterSeconds))

			utils.LogWarning(fmt.Sprintf("Login blocked for user %s from IP %s due to repeated failures",
				loginRequest.Username, clientIP))

			c.JSON(http.StatusTooManyRequests, gin.H{
				"error":               "Too many failed login attempts. Account temporarily locked.",
				"retry_after_seconds": retryAfterSeconds,
			})
			return
		}

		// Handle specific validation errors
		if err != nil {
			switch err.(*services.ValidationError).Type {
			case "USER_NOT_FOUND":
				utils.LogWarning("User not found: " + loginRequest.Username)
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
			case "INVALID_PASSWORD":
				utils.LogWarning("Invalid password for user: " + loginRequest.Username)
				c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
			case "SYSTEM_ERROR":
				utils.LogError("System error during validation: " + err.Error())
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
			default:
				utils.LogError("Unknown validation error: " + err.Error())
				c.JSON(http.StatusInternalServerError, gin.H{"error": "Internal server error"})
			}
		} else {
			utils.LogWarning("Invalid credentials for user: " + loginRequest.Username)
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Invalid username or password"})
		}
		return
	}

	// Successful authentication - clear any login attempt records
	if clearErr := h.userService.ClearLoginAttempts(loginRequest.Username, clientIP); clearErr != nil {
		utils.LogError("Failed to clear login attempts: " + clearErr.Error())
		// Don't fail the login on clearing errors
	}

	// Include role/user_id claims (informational — authz reads the DB)
	role := ""
	if u, uerr := h.userService.GetUserByUsername(loginRequest.Username); uerr == nil && u != nil {
		role = u.Role
	}

	token, err := utils.GenerateToken(loginRequest.Username, role, user_id, h.expirationMinutes)
	if err != nil {
		utils.LogError("Failed to generate token: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not generate token"})
		return
	}

	err = h.userService.SaveToken(strconv.Itoa(user_id), token)
	if err != nil {
		utils.LogError("Failed to save token: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not save token"})
		return
	}

	utils.LogInfo("User logged in: " + loginRequest.Username)
	c.JSON(http.StatusOK, gin.H{"id": user_id, "token": token})
}

// Logout handles user logout requests. It invalidates the user's token.
// @Summary User logout
// @Description Invalidates the user's token and logs them out.
// @Tags auth
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Success 200 {object} models.MessageResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/logout [post]
func (h *Handler) Logout(c *gin.Context) {
	token := c.GetHeader("Authorization")
	if token == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Authorization header is required"})
		return
	}

	token = strings.TrimPrefix(token, "Bearer ")

	err := h.userService.Logout(token)
	if err != nil {
		utils.LogError("Failed to logout: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Could not logout"})
		return
	}

	utils.LogInfo("User logged out")
	c.JSON(http.StatusOK, gin.H{"message": "Logged out successfully"})
}

// GetUsers handles the fetching of all users.
// @Summary Fetch all users
// @Description Retrieves all users from the database and returns them as a JSON response.
// @Tags users
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Success 200 {array} models.User
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/users [get]
func (h *Handler) GetUsers(c *gin.Context) {
	users, err := h.userService.GetUsers()
	if err != nil {
		utils.LogError("Failed to fetch users: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch users"})
		return
	}

	utils.LogInfo("Fetched users")
	c.JSON(http.StatusOK, users)
}

// GetMe handles fetching the current user's profile information.
// @Summary Get current user profile
// @Description Retrieves the authenticated user's profile information based on the JWT token
// @Tags users
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Success 200 {object} models.User
// @Failure 401 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/users/me [get]
func (h *Handler) GetMe(c *gin.Context) {
	// Extract claims from context set by the auth middleware
	claimsInterface, exists := c.Get("username")
	if !exists {
		utils.LogError("Claims not found in context")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: claims not found"})
		return
	}

	claims, ok := claimsInterface.(*utils.Claims)
	if !ok {
		utils.LogError("Claims in context are not of correct type")
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized: invalid claims format"})
		return
	}

	// Fetch user information by username from claims
	user, err := h.userService.GetUserByUsername(claims.Username)
	if err != nil {
		if err == sql.ErrNoRows {
			utils.LogWarning("User not found: " + claims.Username)
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
			return
		}
		utils.LogError("Failed to fetch user: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch user profile"})
		return
	}

	utils.LogInfo("Fetched current user profile: " + claims.Username)
	c.JSON(http.StatusOK, user)
}

// CreateUser handles the creation of a new user.
// @Summary Create a new user
// @Description Creates a new user in the system with optional license key and role
// @Tags users
// @Accept json
// @Produce json
// @Param user body models.CreateUserRequest true "User data"
// @Success 201 {object} models.UserIdResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/users [post]
func (h *Handler) CreateUser(c *gin.Context) {
	var user models.CreateUserRequest
	if err := c.ShouldBindJSON(&user); err != nil {
		utils.LogError("Failed to bind JSON: " + err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	userID, err := h.userService.CreateUserWithRole(
		user.Username,
		user.Email,
		user.Password,
		user.Role,
	)
	if err != nil {
		utils.LogError("Failed to create user: " + err.Error())
		// Return more specific error messages
		if strings.Contains(err.Error(), "username already exists") {
			c.JSON(http.StatusConflict, gin.H{"error": "Username already exists"})
		} else if strings.Contains(err.Error(), "invalid role") {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else if strings.Contains(err.Error(), "password validation failed") ||
			strings.Contains(err.Error(), "password must") {
			// Handle password validation errors with 400 Bad Request
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create user"})
		}
		return
	}

	utils.LogInfo(fmt.Sprintf("User created: %s", user.Username))
	c.JSON(http.StatusCreated, gin.H{"id": userID, "message": "User created successfully"})
}

// UpdateUser handles partial or full user updates with flexible field support.
// @Summary Update user fields
// @Description Updates specific user fields (username, email, role, password) based on provided JSON payload. Only non-empty fields are updated.
// @Tags users
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "User ID"
// @Param updates body map[string]interface{} true "Fields to update (username, email, role, password)"
// @Success 200 {object} models.MessageResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/users/{id} [put]
// resolveCaller loads the authenticated user from the JWT claims that
// TokenAuthMiddleware placed in the context. Returns nil if the caller cannot
// be resolved (missing/invalid claims or unknown user).
func (h *Handler) resolveCaller(c *gin.Context) *models.User {
	claimsInterface, exists := c.Get("username")
	if !exists {
		return nil
	}
	claims, ok := claimsInterface.(*utils.Claims)
	if !ok {
		return nil
	}
	user, err := h.userService.GetUserByUsername(claims.Username)
	if err != nil {
		return nil
	}
	return user
}

func (h *Handler) UpdateUser(c *gin.Context) {
	// Extract user ID from the URL path parameter
	userID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid user ID"})
		return
	}

	// Authorization: a caller may update their own profile; only an admin may
	// update another user. Resolve the caller from the JWT claims that
	// TokenAuthMiddleware placed in the context.
	caller := h.resolveCaller(c)
	if caller == nil {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized"})
		return
	}
	callerIsAdmin := caller.Role == "admin"
	if !callerIsAdmin && caller.ID != userID {
		utils.LogWarning(fmt.Sprintf("RBAC: user '%s' (id %d) denied update of user id %d", caller.Username, caller.ID, userID))
		c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden: you may only update your own account"})
		return
	}

	// Parse the JSON payload as a flexible map
	var updates map[string]interface{}
	if err := c.ShouldBindJSON(&updates); err != nil {
		utils.LogError("Failed to bind JSON: " + err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid JSON format: " + err.Error()})
		return
	}

	// Privilege-escalation guard: only admins may CHANGE a role. A non-admin
	// self-edit that merely echoes back the caller's existing role (as some UI
	// forms do) must succeed — reject only an actual role change. Since a
	// non-admin can only edit their own account (enforced above), the caller is
	// the target, so compare against the caller's current role.
	if roleVal, changingRole := updates["role"]; changingRole && !callerIsAdmin {
		newRole := models.NormalizeRole(fmt.Sprintf("%v", roleVal))
		if newRole != "" && newRole != models.NormalizeRole(caller.Role) {
			utils.LogWarning(fmt.Sprintf("RBAC: non-admin user '%s' attempted to change role", caller.Username))
			c.JSON(http.StatusForbidden, gin.H{"error": "Forbidden: administrator privileges required to change role"})
			return
		}
		// Role unchanged — drop it so the service layer performs no role write.
		delete(updates, "role")
	}

	// Remove empty/nil values from updates
	cleanedUpdates := make(map[string]interface{})
	for key, value := range updates {
		if value != nil {
			// Convert string values and check if they're not empty
			if strValue, ok := value.(string); ok {
				if strValue != "" {
					cleanedUpdates[key] = strValue
				}
			} else {
				cleanedUpdates[key] = value
			}
		}
	}

	if len(cleanedUpdates) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "No valid fields provided for update"})
		return
	}

	// Validate allowed fields
	allowedFields := map[string]bool{
		"username": true,
		"email":    true,
		"role":     true,
		"password": true,
	}

	for field := range cleanedUpdates {
		if !allowedFields[field] {
			c.JSON(http.StatusBadRequest, gin.H{"error": fmt.Sprintf("Field '%s' is not allowed for update", field)})
			return
		}
	}

	// Call the updated service method
	if err := h.userService.UpdateUser(userID, cleanedUpdates); err != nil {
		utils.LogError("Failed to update user: " + err.Error())

		// Handle specific error types
		switch {
		case strings.Contains(err.Error(), "user not found"):
			c.JSON(http.StatusNotFound, gin.H{"error": "User not found"})
		case strings.Contains(err.Error(), "username already exists"):
			c.JSON(http.StatusConflict, gin.H{"error": "Username already exists"})
		case strings.Contains(err.Error(), "password validation failed") ||
			strings.Contains(err.Error(), "password must"):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		case strings.Contains(err.Error(), "invalid role"):
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update user"})
		}
		return
	}

	utils.LogInfo(fmt.Sprintf("User %d updated successfully", userID))
	c.JSON(http.StatusOK, gin.H{
		"message": "User updated successfully",
		"user_id": userID,
	})
}

// DeleteUser handles deleting a user by ID.
// @Summary Delete user
// @Description Deletes a user by its ID
// @Tags users
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path string true "User ID"
// @Success 200 {object} models.MessageResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/users/{id} [delete]
func (h *Handler) DeleteUser(c *gin.Context) {
	id := c.Param("id")
	if err := h.userService.DeleteUser(id); err != nil {
		utils.LogError("Failed to delete user: " + err.Error())

		// Handle different error types with appropriate HTTP status codes
		switch err {
		case services.ErrUserNotFound:
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case services.ErrAdminDeletion:
			c.JSON(http.StatusForbidden, gin.H{"error": err.Error()})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		}
		return
	}
	utils.LogInfo("User deleted: " + id)
	c.JSON(http.StatusOK, gin.H{"message": "User deleted"})
}

// GetLoxiLBInstances handles fetching LoxiLB instances.
// @Summary Fetch LoxiLB instances
// @Description Retrieves LoxiLB instances and returns them as JSON.
// @Tags instances
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Success 200 {array} models.LoxiLBInstance
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/loxilbs [get]
func (h *Handler) GetLoxiLBInstances(c *gin.Context) {
	instances, err := h.loxilbService.FetchLoxiLBInstances()
	if err != nil {
		utils.LogError("Failed to fetch LoxiLB instances: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	utils.LogInfo("Fetched LoxiLB instances")
	c.JSON(http.StatusOK, instances)
}

// GetLoxiLBInstanceByID handles fetching a LoxiLB instance by ID.
// @Summary Fetch LoxiLB instance by ID
// @Description Retrieves a LoxiLB instance by ID.
// @Tags instances
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Success 200 {object} models.LoxiLBInstance
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/loxilbs/{id} [get]
func (h *Handler) GetLoxiLBInstanceByID(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	instance, err := h.loxilbService.FetchLoxiLBInstanceByID(id)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "LoxiLB instance not found"})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch LoxiLB instance"})
		return
	}

	c.JSON(http.StatusOK, instance)
}

// CreateLoxiLBInstance handles creating a new LoxiLB instance.
// @Summary Create a new LoxiLB instance
// @Description Create a new LoxiLB instance.
// @Tags instances
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param instance body models.LoxiLBInstanceRequest true "LoxiLB Instance"
// @Success 201 {object} models.LoxiLBInstance
// @Failure 400 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/loxilbs [post]
func (h *Handler) CreateLoxiLBInstance(c *gin.Context) {
	var instance models.LoxiLBInstanceRequest

	if err := c.ShouldBindJSON(&instance); err != nil {
		utils.LogError("Failed to bind JSON: " + err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	fields := models.InstanceFields{
		Name:        instance.Name,
		Host:        instance.Host,
		Port:        instance.Port,
		Protocol:    instance.Protocol,
		Description: instance.Description,
		Version:     instance.Version,
		Cimage:      instance.Cimage,
		Ctag:        instance.Ctag,
	}
	// Normalize first (trim, lowercase the protocol, default version=v1 as
	// this endpoint has always done), then validate — the stored values and
	// the values checked for uniqueness must be the same ones.
	fields.Normalize()
	if verr := fields.Validate(); verr != nil {
		utils.LogWarning("Rejected LoxiLB instance create: " + verr.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": verr.Message, "field": verr.Field})
		return
	}
	if h.rejectInstanceConflict(c, fields, 0) {
		return
	}

	// Set default value for IsActive if not provided
	isActive := true
	if instance.IsActive != nil {
		isActive = *instance.IsActive
	}

	instanceID, apiEndpoint, err := h.loxilbService.AddLoxiLBInstanceWithArgs(fields.Name, fields.Host,
		fields.Port, fields.Protocol, fields.Description, fields.Version, fields.Cimage, fields.Ctag, isActive)
	if err != nil {
		utils.LogError("Failed to add LoxiLB instance: " + err.Error())
		// Lost the race against a concurrent create with the same endpoint.
		if errors.Is(err, services.ErrInstanceEndpointTaken) {
			c.JSON(http.StatusConflict, gin.H{"error": services.ErrInstanceEndpointTaken.Error()})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to add LoxiLB instance"})
		return
	}

	// Create a new LoxiLBInstance using the provided information and return it
	newInstance := models.LoxiLBInstance{
		ID:          instanceID,
		Name:        fields.Name,
		Host:        fields.Host,
		Port:        fields.Port,
		Protocol:    fields.Protocol,
		Description: fields.Description,
		Version:     fields.Version,
		ApiEndpoint: apiEndpoint,
		Cimage:      fields.Cimage,
		Ctag:        fields.Ctag,
		IsActive:    isActive,
		CreatedAt:   time.Now(),
	}

	utils.LogInfo("LoxiLB instance created: " + fields.Name)
	c.JSON(http.StatusCreated, newInstance)
}

// rejectInstanceConflict answers 409 when the name or the derived endpoint is
// already taken by another row, and 500 if the check itself fails. Reports
// whether the request has been answered. excludeID is the row being updated
// (0 on create), so re-saving an instance unchanged is never a conflict.
func (h *Handler) rejectInstanceConflict(c *gin.Context, fields models.InstanceFields, excludeID int) bool {
	nameTaken, err := h.loxilbService.InstanceNameTaken(fields.Name, excludeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate LoxiLB instance"})
		return true
	}
	if nameTaken {
		c.JSON(http.StatusConflict, gin.H{"error": services.ErrInstanceNameTaken.Error(), "field": "name"})
		return true
	}

	endpointTaken, err := h.loxilbService.InstanceEndpointTaken(fields.APIEndpoint(), excludeID)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to validate LoxiLB instance"})
		return true
	}
	if endpointTaken {
		c.JSON(http.StatusConflict, gin.H{"error": services.ErrInstanceEndpointTaken.Error() + ": " + fields.APIEndpoint()})
		return true
	}
	return false
}

// UpdateLoxiLBInstance handles updating a LoxiLB instance.
// @Summary Update LoxiLB instance
// @Description Updates an existing LoxiLB instance with the provided JSON payload
// @Tags instances
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Param instance body models.LoxiLBInstance true "LoxiLB instance data"
// @Success 200 {object} models.LoxiLBInstance
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/loxilbs/{id} [put]
func (h *Handler) UpdateLoxiLBInstance(c *gin.Context) {
	// Extract instance ID from the URL path parameter
	instanceID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid instance ID"})
		return
	}

	var instance models.LoxiLBInstance
	if err := c.ShouldBindJSON(&instance); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// PUT replaces the whole row (the UPDATE writes every column), so the
	// payload has to carry every field. Without this the endpoint accepted
	// {"name":"x"} and blanked host/port/image — and an empty protocol
	// produced the endpoint '://host:port/netlox/', pointing the proxy at
	// nothing. Same rules as create; both go through InstanceFields.
	fields := models.InstanceFields{
		Name:        instance.Name,
		Host:        instance.Host,
		Port:        instance.Port,
		Protocol:    instance.Protocol,
		Description: instance.Description,
		Version:     instance.Version,
		Cimage:      instance.Cimage,
		Ctag:        instance.Ctag,
	}
	fields.Normalize()
	if verr := fields.Validate(); verr != nil {
		utils.LogWarning(fmt.Sprintf("Rejected LoxiLB instance update (id %d): %s", instanceID, verr.Error()))
		c.JSON(http.StatusBadRequest, gin.H{"error": verr.Message, "field": verr.Field})
		return
	}
	if h.rejectInstanceConflict(c, fields, instanceID) {
		return
	}

	// Set the instance ID from the URL path parameter
	instance.ID = instanceID
	instance.Name = fields.Name
	instance.Host = fields.Host
	instance.Port = fields.Port
	instance.Protocol = fields.Protocol
	instance.Description = fields.Description
	instance.Version = fields.Version
	instance.Cimage = fields.Cimage
	instance.Ctag = fields.Ctag
	instance.ApiEndpoint = fields.APIEndpoint()

	if err := h.loxilbService.UpdateLoxiLBInstance(instance); err != nil {
		switch {
		case errors.Is(err, services.ErrInstanceNotFound):
			c.JSON(http.StatusNotFound, gin.H{"error": services.ErrInstanceNotFound.Error()})
		case errors.Is(err, services.ErrInstanceEndpointTaken):
			c.JSON(http.StatusConflict, gin.H{"error": services.ErrInstanceEndpointTaken.Error()})
		default:
			utils.LogError("Failed to update LoxiLB instance: " + err.Error())
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update LoxiLB instance"})
		}
		return
	}

	c.JSON(http.StatusOK, instance)
}

// DeleteLoxiLBInstance handles the deletion of a LoxiLB instance.
// @Summary Delete a LoxiLB instance
// @Description Deletes a LoxiLB instance by ID
// @Tags instances
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Success 200 {object} models.MessageResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/loxilbs/{id} [delete]
func (h *Handler) DeleteLoxiLBInstance(c *gin.Context) {
	id, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid ID"})
		return
	}

	if err := h.loxilbService.DeleteLoxiLBInstance(id); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Instance deleted"})
}

// UpdateLoxiLBInstanceFirmware handles updating the firmware of a LoxiLB instance image.
// @Summary Update LoxiLB instance firmware
// @Description Updates the firmware of a LoxiLB instance image.
// @Tags instances
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Param firmwareRequest body models.UpdateFirmwareRequest true "Firmware update data"
// @Success 200 {object} models.SuccessResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/loxilbs/{id}/firmware [put]
func (h *Handler) UpdateLoxiLBInstanceFirmware(c *gin.Context) {
	// Extract instance ID from the URL path parameter
	instanceID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid instance ID"})
		return
	}

	var firmwareRequest models.UpdateFirmwareRequest
	if err := c.ShouldBindJSON(&firmwareRequest); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Validate required fields
	if firmwareRequest.Cimage == nil || *firmwareRequest.Cimage == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cimage is required"})
		return
	}
	if firmwareRequest.Ctag == nil || *firmwareRequest.Ctag == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ctag is required"})
		return
	}

	// First, fetch the existing instance to preserve all current data
	existingInstance, err := h.loxilbService.FetchLoxiLBInstanceByID(instanceID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "LoxiLB instance not found"})
			return
		}
		utils.LogError("Failed to fetch existing LoxiLB instance: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch LoxiLB instance"})
		return
	}

	// Update only the firmware-related fields while preserving all other data
	instance := *existingInstance
	instance.Cimage = *firmwareRequest.Cimage
	instance.Ctag = *firmwareRequest.Ctag

	// Set optional fields only if provided
	if firmwareRequest.Description != nil {
		instance.Description = *firmwareRequest.Description
	}
	if firmwareRequest.Version != nil {
		instance.Version = *firmwareRequest.Version
	}

	if err := h.loxilbService.UpdateFirmware(instance); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Update the database with the new firmware information
	if err := h.loxilbService.UpdateLoxiLBInstance(instance); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update database: " + err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "LoxiLB instance firmware updated successfully"})
}

// StartLoxiLBInstanceFirmware handles starting the firmware of a LoxiLB instance image.
// @Summary Start LoxiLB instance firmware
// @Description Starts the firmware of a LoxiLB instance image using the instance ID.
// @Tags instances
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Success 200 {object} models.SuccessResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/loxilbs/{id}/firmware/start [put]
func (h *Handler) StartLoxiLBInstanceFirmware(c *gin.Context) {
	// Extract instance ID from the URL path parameter
	instanceID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid instance ID"})
		return
	}

	// Fetch the existing instance data using the ID
	instance, err := h.loxilbService.FetchLoxiLBInstanceByID(instanceID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "LoxiLB instance not found"})
			return
		}
		utils.LogError("Failed to fetch LoxiLB instance: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch LoxiLB instance"})
		return
	}

	if err := h.loxilbService.StartFirmware(*instance); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "LoxiLB instance firmware started successfully"})
}

// StopLoxiLBInstanceFirmware handles stopping the firmware of a LoxiLB instance image.
// @Summary Stop LoxiLB instance firmware
// @Description Stops the firmware of a LoxiLB instance image using the instance ID.
// @Tags instances
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Success 200 {object} models.SuccessResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/loxilbs/{id}/firmware/stop [put]
func (h *Handler) StoptLoxiLBInstanceFirmware(c *gin.Context) {
	// Extract instance ID from the URL path parameter
	instanceID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid instance ID"})
		return
	}

	// Fetch the existing instance data using the ID
	instance, err := h.loxilbService.FetchLoxiLBInstanceByID(instanceID)
	if err != nil {
		if err == sql.ErrNoRows {
			c.JSON(http.StatusNotFound, gin.H{"error": "LoxiLB instance not found"})
			return
		}
		utils.LogError("Failed to fetch LoxiLB instance: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to fetch LoxiLB instance"})
		return
	}

	if err := h.loxilbService.StopFirmware(*instance); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"message": "LoxiLB instance firmware stopped successfully"})
}

// HealthCheck handles the health check requests.
// @Summary Health check
// @Description Checks the health of the application and database connection.
// @Tags health
// @Produce json
// @Success 200 {object} models.HealthCheckResponse
// @Failure 500 {object} models.HealthCheckResponse
// @Router /oam/health [get]
func (h *Handler) HealthCheck(c *gin.Context) {
	err := h.userService.PingDB()
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"status": "unhealthy"})
		return
	}

	c.JSON(http.StatusOK, gin.H{"status": "healthy"})
}

func (h *Handler) GetLogsFromDB(c *gin.Context) {
	limitStr := c.DefaultQuery("limit", strconv.Itoa(config.DefaultLogLimit))
	offsetStr := c.DefaultQuery("offset", strconv.Itoa(config.DefaultLogOffset))
	startTimeStr := c.Query("startTime")
	endTimeStr := c.Query("endTime")

	limit, err := strconv.Atoi(limitStr)
	if err != nil {
		utils.LogError("Invalid limit: " + err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid limit"})
		return
	}

	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		utils.LogError("Invalid offset: " + err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid offset"})
		return
	}

	var startTime, endTime time.Time
	if startTimeStr != "" {
		startTime, err = time.Parse(time.RFC3339, startTimeStr)
		if err != nil {
			utils.LogError("Invalid startTime: " + err.Error())
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid startTime"})
			return
		}
	} else {
		startTime = time.Unix(0, 0) // Unix epoch time for no start time filter
	}

	if endTimeStr != "" {
		endTime, err = time.Parse(time.RFC3339, endTimeStr)
		if err != nil {
			utils.LogError("Invalid endTime: " + err.Error())
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid endTime"})
			return
		}
	} else {
		endTime = time.Now() // Current time for no end time filter
	}

	logs, err := h.logService.FetchLogs(limit, offset, startTime, endTime)
	if err != nil {
		utils.LogError("Failed to fetch logs: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, logs)
}

// GetLogs handles fetching logs from the log file.
// @Summary Fetch logs
// @Description Retrieves logs from the log file within the specified time range.
// @Tags logs
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param lines query int false "Number of lines"
// @Param level query string false "Log level"
// @Param startTime query string false "Start time"
// @Success 200 {object} models.LogResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/logs [get]
func (h *Handler) GetLogs(c *gin.Context) {
	var result models.LogResponse

	linesStr := c.DefaultQuery("lines", strconv.Itoa(config.DefaultLogLines))
	levelStr := c.DefaultQuery("level", config.DefaultLogLevel)
	keywordStr := c.Query("startTime")

	// get client IP from the request
	clientID := getClientIP(c.Request)
	lines, _ := strconv.Atoi(linesStr)

	// Find the log file with the random UUID in the name
	files, err := os.ReadDir(config.DefaultLogFilePath)
	if err != nil {
		utils.LogError("Failed to read log directory: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to read log directory"})
		return
	}

	var logFile string
	for _, file := range files {
		if strings.HasPrefix(file.Name(), config.DefaultOAMLogFile) && strings.HasSuffix(file.Name(), ".log") {
			logFile = filepath.Join(config.DefaultLogFilePath, file.Name())
			break
		}
	}

	if logFile == "" {
		utils.LogError("Log file not found")
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Log file not found"})
		return
	}

	file, err := os.Open(logFile)
	if err != nil {
		utils.LogError("Failed to open log file: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open log file"})
		return
	}
	defer file.Close()

	// Get or initialize the cursor for this client
	startPos := int64(0)
	if val, ok := cursorMap.Load(clientID); ok {
		startPos = val.(int64)
	}

	// Read the next batch of lines
	nextLines, nextCursor := readNextLines(file, startPos, lines)

	// Update the cursor for this client
	cursorMap.Store(clientID, nextCursor)

	// Apply filtering if required
	level := derefString(&levelStr)
	keyword := derefString(&keywordStr)
	filteredLines := filterLogs(nextLines, level, keyword)

	result.Logs = filteredLines

	c.JSON(http.StatusOK, result)
}

// API to list available log archives
// @Summary List log archives
// @Description List available log archives
// @Tags logs
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Success 200 {object} models.LogArchivesResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/logs/archives [get]
func (h *Handler) GetLogArchives(c *gin.Context) {
	var result models.LogArchivesResponse

	files, err := os.ReadDir(config.DefaultLogArchivePath)
	if err != nil {
		utils.LogError("Failed to list log archives: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to list log archives"})
		return
	}

	var archives []string
	for _, file := range files {
		if !file.IsDir() && (strings.HasPrefix(file.Name(), config.DefaultOAMLogFile) && (strings.HasSuffix(file.Name(), ".log") || strings.HasSuffix(file.Name(), ".log.gz"))) {
			archives = append(archives, file.Name())
		}
	}

	result.Archives = archives
	c.JSON(http.StatusOK, result)
}

// API to download a log archive
// @Summary Download log archive
// @Description Download a log archive by filename
// @Tags logs
// @Produce application/octet-stream
// @Param Authorization header string true "Bearer token"
// @Param filename path string true "Log archive filename"
// @Success 200 {file} file
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/logs/archives/{filename} [get]
func (h *Handler) GetLogArchivesFilename(c *gin.Context) {
	filename := c.Param("filename")

	if filename == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Filename is required"})
		return
	}

	// Confine access to the log archive directory: reject any path components
	// and accept only names matching the archive naming scheme used by
	// GetLogArchives, so a caller cannot traverse out of DefaultLogArchivePath.
	if filename != filepath.Base(filename) ||
		!strings.HasPrefix(filename, config.DefaultOAMLogFile) ||
		!(strings.HasSuffix(filename, ".log") || strings.HasSuffix(filename, ".log.gz")) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid filename"})
		return
	}

	filePath := filepath.Join(config.DefaultLogArchivePath, filename)
	file, err := os.Open(filePath)
	if err != nil {
		utils.LogError("Failed to open log archive: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to open log archive"})
		return
	}

	// Check if the file is empty
	fileInfo, err := file.Stat()
	if err != nil {
		file.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to get file info"})
		return
	}
	if fileInfo.Size() == 0 {
		file.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "File is empty"})
		return
	}

	// Set headers and send the file
	c.Header("Content-Disposition", "attachment; filename="+filename)
	c.Header("Content-Type", "application/octet-stream")
	c.Status(http.StatusOK)
	bytesCopied, err := io.Copy(c.Writer, file)
	if err != nil {
		utils.LogError("Failed to copy file content: " + err.Error())
	} else {
		utils.LogInfo(fmt.Sprintf("Successfully copied %d bytes from file: %s", bytesCopied, filePath))
	}
}

// CreateAlert handles the creation of a new alert
// @Summary Create alert
// @Description Creates a new alert in the system
// @Tags alerts
// @Accept json
// @Produce json
// @Param alert body models.CreateAlertRequest true "Alert data"
// @Param Authorization header string true "Bearer token"
// @Success 201 {object} models.CreateAlertResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/alerts [post]
func (h *Handler) CreateAlert(c *gin.Context) {
	var alertReq models.CreateAlertRequest
	if err := c.ShouldBindJSON(&alertReq); err != nil {
		utils.LogError("Failed to bind JSON: " + err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	alertID, err := h.alertService.CreateAlert(alertReq)
	if err != nil {
		utils.LogError("Failed to create alert: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusCreated, gin.H{"status": "success", "alertID": alertID})
}

// GetActiveAlerts retrieves all active alerts
// @Summary Get active alerts
// @Description Retrieves all active alerts from the database with pagination (always returns paginated response)
// @Tags alerts
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param page query int false "Page number (default: 1)"
// @Param limit query int false "Number of items per page (default: 20, max: 100)"
// @Success 200 {object} models.PaginatedAlertsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/alerts [get]
func (h *Handler) GetActiveAlerts(c *gin.Context) {
	// Parse pagination parameters with defaults
	page, err := strconv.Atoi(c.DefaultQuery("page", strconv.Itoa(config.DefaultAlertPage)))
	if err != nil || page < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page parameter"})
		return
	}

	limit, err := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(config.DefaultAlertPageSize)))
	if err != nil || limit < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid limit parameter"})
		return
	}

	if limit > config.MaxAlertPageSize {
		limit = config.MaxAlertPageSize
	}

	// Get paginated results
	alerts, totalCount, err := h.alertService.GetActiveAlertsPaginated(page, limit)
	if err != nil {
		utils.LogError("Failed to fetch active alerts: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Calculate pagination metadata
	totalPages := (totalCount + limit - 1) / limit // Ceiling division
	hasNext := page < totalPages
	hasPrev := page > 1

	response := models.PaginatedAlertsResponse{
		Data: alerts,
		Pagination: models.PaginationMeta{
			Page:       page,
			Limit:      limit,
			TotalCount: totalCount,
			TotalPages: totalPages,
			HasNext:    hasNext,
			HasPrev:    hasPrev,
		},
	}

	utils.LogInfo(fmt.Sprintf("Successfully fetched %d active alerts (page %d of %d)", len(alerts), page, totalPages))
	c.JSON(http.StatusOK, response)
}

// AcknowledgeAlert handles acknowledging an alert
// @Summary Acknowledge alert
// @Description Acknowledges an alert by ID
// @Tags alerts
// @Accept json
// @Produce json
// @Param id path int true "Alert ID"
// @Param ackRequest body models.AcknowledgeRequest true "Acknowledgement data"
// @Param Authorization header string true "Bearer token"
// @Success 200 {object} models.AcknowledgeResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/alerts/{id}/acknowledge [put]
func (h *Handler) AcknowledgeAlert(c *gin.Context) {
	alertID, err := strconv.Atoi(c.Param("id"))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid alert ID"})
		return
	}

	var ackReq models.AcknowledgeRequest
	if err := c.ShouldBindJSON(&ackReq); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	ackTime, err := h.alertService.AcknowledgeAlert(alertID, ackReq.UserID)
	if err != nil {
		utils.LogError("Failed to acknowledge alert: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "acknowledged",
		"alertID": alertID,
		"ackTime": ackTime,
	})
}

// GetAlertHistory retrieves alert history
// @Summary Get alert history
// @Description Retrieves alert history within a specified time range with pagination (always returns paginated response)
// @Tags alerts
// @Produce json
// @Param start query string false "Start time (RFC3339 format)"
// @Param end query string false "End time (RFC3339 format)"
// @Param page query int false "Page number (default: 1)"
// @Param limit query int false "Number of items per page (default: 20, max: 100)"
// @Param Authorization header string true "Bearer token"
// @Success 200 {object} models.PaginatedAlertsResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/alerts/history [get]
func (h *Handler) GetAlertHistory(c *gin.Context) {
	start := c.Query("start")
	end := c.Query("end")

	var startTime, endTime time.Time
	var err error

	// Parse time parameters
	if start != "" {
		startTime, err = time.Parse(time.RFC3339, start)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid start date format. Use RFC3339 format (e.g., 2023-01-01T00:00:00Z)"})
			return
		}
	}
	if end != "" {
		endTime, err = time.Parse(time.RFC3339, end)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid end date format. Use RFC3339 format (e.g., 2023-01-01T23:59:59Z)"})
			return
		}
	}

	// Parse pagination parameters with defaults
	page, err := strconv.Atoi(c.DefaultQuery("page", strconv.Itoa(config.DefaultAlertPage)))
	if err != nil || page < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid page parameter"})
		return
	}

	limit, err := strconv.Atoi(c.DefaultQuery("limit", strconv.Itoa(config.DefaultAlertPageSize)))
	if err != nil || limit < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid limit parameter"})
		return
	}

	if limit > config.MaxAlertPageSize {
		limit = config.MaxAlertPageSize
	}

	// Get paginated results
	history, totalCount, err := h.alertService.GetAlertHistoryPaginated(startTime, endTime, page, limit)
	if err != nil {
		utils.LogError("Failed to fetch alert history: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	// Calculate pagination metadata
	totalPages := (totalCount + limit - 1) / limit // Ceiling division
	hasNext := page < totalPages
	hasPrev := page > 1

	response := models.PaginatedAlertsResponse{
		Data: history,
		Pagination: models.PaginationMeta{
			Page:       page,
			Limit:      limit,
			TotalCount: totalCount,
			TotalPages: totalPages,
			HasNext:    hasNext,
			HasPrev:    hasPrev,
		},
	}

	utils.LogInfo(fmt.Sprintf("Successfully fetched %d alert history records (page %d of %d)", len(history), page, totalPages))
	c.JSON(http.StatusOK, response)
}

// Function to extract client IP address from the request
func getClientIP(r *http.Request) string {
	// Try to get the IP address from the X-Forwarded-For header
	ip := r.Header.Get("X-Forwarded-For")
	if ip != "" {
		// The X-Forwarded-For header can contain multiple IP addresses, the first one is the client's IP
		ips := strings.Split(ip, ",")
		return strings.TrimSpace(ips[0])
	}

	// If the X-Forwarded-For header is not set, use the remote address
	ip = r.RemoteAddr
	if ip != "" {
		// The remote address can contain the port, so we need to remove it
		if strings.Contains(ip, ":") {
			ip = strings.Split(ip, ":")[0]
		}
		return ip
	}

	return ""
}

// Reads the next N lines starting from a given cursor position
func readNextLines(file *os.File, startPos int64, numLines int) ([]string, int64) {
	bufferSize := 4096
	buffer := make([]byte, bufferSize)

	var lines []string
	var line string
	currentPos := startPos

	file.Seek(startPos, os.SEEK_SET) // Start reading from the stored cursor position

	for len(lines) < numLines {
		n, err := file.Read(buffer)
		if err != nil {
			break
		}

		for i := 0; i < n; i++ {
			if buffer[i] == '\n' {
				lines = append(lines, strings.TrimSpace(line))
				line = ""

				if len(lines) >= numLines {
					currentPos += int64(i + 1)
					break
				}
			} else {
				line += string(buffer[i])
			}
		}
		currentPos += int64(n)
	}

	if line != "" && len(lines) < numLines {
		lines = append(lines, strings.TrimSpace(line))
	}

	return lines, currentPos
}

// Filters logs based on level and keyword
func filterLogs(lines []string, level, keyword string) []string {
	var filtered []string
	for _, line := range lines {
		if (level == "" || strings.Contains(line, level)) &&
			(keyword == "" || strings.Contains(line, keyword)) {
			filtered = append(filtered, line) // No additional quotes
		}
	}
	return filtered
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// ProxyToLoxiLB handles proxying requests to LoxiLB instances.
// @Summary Proxy request to LoxiLB instance
// @Description Forwards HTTP requests to the specified LoxiLB instance
// @Tags proxy
// @Accept json
// @Produce json
// @Param Authorization header string true "Bearer token"
// @Param id path int true "LoxiLB Instance ID"
// @Param path path string true "LoxiLB API path"
// @Success 200 {object} models.SuccessPostResponse "Successful response from LoxiLB"
// @Success 204 {object} interface{} "Successful response from LoxiLB"
// @Failure 400 {object} models.ErrorResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 403 {object} models.ErrorResponse
// @Failure 404 {object} models.ErrorResponse
// @Failure 409 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Failure 503 {object} models.ErrorResponse
// @Security BearerAuth
// @Router /oam/loxilbs/{id}/netlox/ [get]
// @Router /oam/loxilbs/{id}/netlox/ [post]
// @Router /oam/loxilbs/{id}/netlox/ [put]
// @Router /oam/loxilbs/{id}/netlox/ [delete]
// @Router /oam/loxilbs/{id}/netlox/ [patch]
func (h *Handler) ProxyToLoxiLB(c *gin.Context) {
	// Extract instance ID from URL parameter
	instanceIDStr := c.Param("id")
	instanceID, err := strconv.Atoi(instanceIDStr)
	if err != nil {
		utils.LogError("Invalid LoxiLB instance ID: " + instanceIDStr)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid LoxiLB instance ID"})
		return
	}

	// Extract the target path (everything after /netlox/)
	targetPath := c.Param("path")
	if targetPath == "" {
		utils.LogError("Missing target path for proxy request")
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing target path"})
		return
	}

	// Forward the request using the proxy service
	err = h.proxyService.ForwardRequest(c, instanceID, targetPath)
	if err != nil {
		// Error handling with appropriate HTTP status codes
		var reservedErr *services.ReservedEndpointError
		switch {
		// Surface the guard's own message: it names the offending VIP and the
		// reservation it hit, which is what the operator needs to fix .env or
		// pick another port. The generic default below would hide both.
		case errors.As(err, &reservedErr):
			c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
		case strings.Contains(err.Error(), "not found"):
			c.JSON(http.StatusNotFound, gin.H{"error": err.Error()})
		case strings.Contains(err.Error(), "failed to connect"):
			c.JSON(http.StatusBadGateway, gin.H{"error": "LoxiLB instance unreachable"})
		case strings.Contains(err.Error(), "timeout"):
			c.JSON(http.StatusGatewayTimeout, gin.H{"error": "Request timeout"})
		default:
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Proxy request failed"})
		}
		return
	}

	// If we reach here, the response has already been written by the proxy service
}

// Admin Setup Handler Methods

// GetSetupStatus returns the current admin credential setup status
// @Summary Get admin credential setup status
// @Description Check if admin credentials need to be updated from defaults
// @Tags Setup
// @Accept json
// @Produce json
// @Success 200 {object} models.SetupStatusResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /oam/setup/status [get]
func (h *Handler) GetSetupStatus(c *gin.Context) {
	status, err := h.userService.GetAdminCredentialStatus()
	if err != nil {
		utils.LogError("Failed to get admin credential status: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to check setup status"})
		return
	}

	c.JSON(http.StatusOK, models.SetupStatusResponse{
		AdminCredentialStatus: *status,
	})
}

// UpdateAdminCredentials handles updating admin credentials from default values
// @Summary Update admin credentials
// @Description Update admin credentials from default username/password to user-defined values
// @Tags Setup
// @Accept json
// @Produce json
// @Param request body models.AdminUpdateRequest true "Admin credential update request"
// @Success 200 {object} models.AdminUpdateResponse
// @Failure 400 {object} models.ErrorResponse
// @Failure 401 {object} models.ErrorResponse
// @Failure 500 {object} models.ErrorResponse
// @Router /oam/setup/update-admin [post]
func (h *Handler) UpdateAdminCredentials(c *gin.Context) {
	var req models.AdminUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.LogError("Invalid request payload: " + err.Error())
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload"})
		return
	}

	// Validate password confirmation
	if req.NewPassword != req.ConfirmPassword {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Password confirmation does not match"})
		return
	}

	// Gate reachability first: this setup endpoint is only meaningful while the
	// admin still holds its bootstrap credentials. Checking this before the
	// credential comparison prevents it from being used as a password oracle on
	// an already-provisioned system.
	hasDefault, adminUserID, err := h.userService.CheckDefaultAdminCredentials()
	if err != nil {
		utils.LogError("Failed to check default credentials: " + err.Error())
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to verify current credentials"})
		return
	}

	if !hasDefault {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Admin credentials have already been updated"})
		return
	}

	// Validate the supplied bootstrap credentials with a constant-time compare.
	if req.CurrentUsername != "admin" ||
		subtle.ConstantTimeCompare([]byte(req.CurrentPassword), []byte(config.DefaultConfigPassword)) != 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Current credentials must be default admin credentials"})
		return
	}

	// Update the credentials
	err = h.userService.UpdateAdminCredentials(
		req.CurrentUsername,
		req.CurrentPassword,
		req.NewUsername,
		req.NewPassword,
		req.NewEmail,
	)
	if err != nil {
		utils.LogError("Failed to update admin credentials: " + err.Error())

		// Provide more specific error messages
		errMsg := err.Error()
		if strings.Contains(errMsg, "password validation failed") ||
			strings.Contains(errMsg, "password must") {
			c.JSON(http.StatusBadRequest, gin.H{"error": errMsg})
		} else if strings.Contains(errMsg, "username already exists") {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Username already exists"})
		} else if strings.Contains(errMsg, "current password is incorrect") {
			c.JSON(http.StatusUnauthorized, gin.H{"error": "Current password is incorrect"})
		} else {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to update credentials"})
		}
		return
	}

	// Generate new access token for the updated user
	newToken, err := utils.GenerateToken(req.NewUsername, models.RoleAdmin, adminUserID, h.expirationMinutes)
	if err != nil {
		utils.LogError("Failed to generate new access token: " + err.Error())
		// Still return success since credentials were updated
		c.JSON(http.StatusOK, models.AdminUpdateResponse{
			Success: true,
			Message: "Admin credentials updated successfully. Please login again.",
		})
		return
	}

	// The token must exist in the server-side store or the auth middleware
	// will reject it as revoked.
	if err := h.userService.SaveToken(strconv.Itoa(adminUserID), newToken); err != nil {
		utils.LogError("Failed to save new access token: " + err.Error())
		c.JSON(http.StatusOK, models.AdminUpdateResponse{
			Success: true,
			Message: "Admin credentials updated successfully. Please login again.",
		})
		return
	}

	utils.LogInfo(fmt.Sprintf("Admin credentials updated successfully for user ID %d", adminUserID))

	c.JSON(http.StatusOK, models.AdminUpdateResponse{
		Success:        true,
		Message:        "Admin credentials updated successfully",
		NewAccessToken: newToken,
	})
}

// The HTTP admin-reset endpoint was intentionally removed: it exposed an
// unauthenticated admin-reset primitive over HTTP. Admin reset is now available
// only as a local break-glass CLI (cmd/reset_admin), which calls
// userService.ResetAdminToDefault directly and requires host shell access.
