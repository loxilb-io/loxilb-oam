package models

// SystemInfo represents system information returned in API responses
type SystemInfo struct {
	Version        string `json:"version"`
	InstallationID string `json:"installationId"`
	AdminUserID    int    `json:"adminUserId"`
}

// AdminCredentialStatus represents the current status of admin credentials
type AdminCredentialStatus struct {
	NeedsCredentialUpdate bool       `json:"needsCredentialUpdate"`
	AdminExists           bool       `json:"adminExists"`
	HasDefaultCredentials bool       `json:"hasDefaultCredentials"`
	CredentialsUpdated    bool       `json:"credentialsUpdated"`
	SystemInfo            SystemInfo `json:"systemInfo"`
}

// AdminUpdateRequest represents a request to update admin credentials
type AdminUpdateRequest struct {
	CurrentUsername string `json:"currentUsername" binding:"required"`
	CurrentPassword string `json:"currentPassword" binding:"required"`
	NewUsername     string `json:"newUsername" binding:"required,min=3,max=50"`
	NewPassword     string `json:"newPassword" binding:"required,min=9"`
	NewEmail        string `json:"newEmail" binding:"required,email"`
	ConfirmPassword string `json:"confirmPassword" binding:"required"`
}

// AdminUpdateResponse represents the response after updating admin credentials
type AdminUpdateResponse struct {
	Success        bool   `json:"success"`
	Message        string `json:"message"`
	NewAccessToken string `json:"newAccessToken,omitempty"`
}

// SetupStatusResponse represents the response for checking setup status
type SetupStatusResponse struct {
	AdminCredentialStatus
}

// AdminResetRequest represents a request to reset admin account to defaults
type AdminResetRequest struct {
	Confirm bool `json:"confirm" binding:"required"` // Must be true to proceed
}

// DefaultAdminInfo contains the default admin credentials after reset
type DefaultAdminInfo struct {
	UserID   int    `json:"userId"`
	Username string `json:"username"`
	Password string `json:"password"`
	Email    string `json:"email"`
}

// AdminResetResponse represents the response after resetting admin account
type AdminResetResponse struct {
	Success   bool             `json:"success"`
	Message   string           `json:"message"`
	AdminInfo DefaultAdminInfo `json:"adminInfo"`
	Warning   string           `json:"warning"`
}
