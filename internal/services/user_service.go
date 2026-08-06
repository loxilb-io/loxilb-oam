package services

import (
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/utils"
	passwordUtils "github.com/loxilb-io/loxilb-oam/pkg/utils"
	"strings"
	"time"
	"unicode"

	"github.com/go-sql-driver/mysql"
	"github.com/patrickmn/go-cache"
)

type UserService struct {
	DB    *sql.DB
	Cache *cache.Cache
}

// NewUserService initializes a new UserService with the given database connection
// and sets up an in-memory cache with expiration and cleanup intervals from the config.
func NewUserService(db *sql.DB) *UserService {
	// Initialize the in-memory cache with expiration and cleanup intervals from the config
	c := cache.New(time.Duration(config.CacheExpirationTime)*time.Minute, time.Duration(config.CacheCleanupInterval)*time.Minute)
	return &UserService{DB: db, Cache: c}
}

// AddUserWithArgs adds a new user to the database with the given name and password.
// It validates the password, generates a salt and hashed password, and inserts the user
// into the database along with a default license key. Returns the user ID and any error encountered.
func (s *UserService) AddUserWithArgs(name, email, password string) (int, error) {
	var userID int
	err := utils.RetryOperation(func() error {
		// Check if username already exists BEFORE validating password
		var existingUserID int
		checkQuery := `SELECT id FROM users WHERE username = ?`
		err := s.DB.QueryRow(checkQuery, name).Scan(&existingUserID)
		if err == nil {
			// Username already exists
			utils.LogWarning("Duplicate username: " + name)
			return errors.New("username already exists")
		} else if err != sql.ErrNoRows {
			// Some other database error occurred
			utils.LogError("Failed to check username availability: " + err.Error())
			return err
		}
		// If err == sql.ErrNoRows, username is available - continue

		if err := s.validatePassword(name, password); err != nil {
			utils.LogError("Password validation failed: " + err.Error())
			return err
		}

		hashedPasswordBase64, err := passwordUtils.HashPassword(password)
		if err != nil {
			utils.LogError("Failed to hash password: " + err.Error())
			return err
		}

		// Users now get licenses through proper license management system
		// No automatic license generation - admin must install licenses manually
		query := config.InsertUserQuery
		result, err := s.DB.Exec(query, name, email, hashedPasswordBase64)
		if err != nil {
			if isDuplicateEntryError(err) {
				utils.LogWarning("Duplicate username: " + name)
				return errors.New("username already exists")
			}
			utils.LogError("Failed to insert user: " + err.Error())
			return err
		}

		lastInsertID, err := result.LastInsertId()
		if err != nil {
			utils.LogError("Failed to get last insert ID: " + err.Error())
			return err
		}
		userID = int(lastInsertID)

		utils.LogInfo("User created: " + name)
		return nil
	}, config.MaxRetries, config.RetryDelay)
	return userID, err
}

// AddUser adds a new user to the database using the provided User model.
// It validates the password, generates a salt and hashed password, and inserts the user
// into the database along with a default license key. Returns the user ID and any error encountered.
func (s *UserService) AddUser(user models.User) (int, error) {
	var userID int
	err := utils.RetryOperation(func() error {
		// Check if username already exists BEFORE validating password
		var existingUserID int
		checkQuery := `SELECT id FROM users WHERE username = ?`
		err := s.DB.QueryRow(checkQuery, user.Username).Scan(&existingUserID)
		if err == nil {
			// Username already exists
			utils.LogWarning("Duplicate username: " + user.Username)
			return errors.New("username already exists")
		} else if err != sql.ErrNoRows {
			// Some other database error occurred
			utils.LogError("Failed to check username availability: " + err.Error())
			return err
		}
		// If err == sql.ErrNoRows, username is available - continue

		if err := s.validatePassword(user.Username, user.Password); err != nil {
			utils.LogError("Password validation failed: " + err.Error())
			return err
		}

		hashedPasswordBase64, err := passwordUtils.HashPassword(user.Password)
		if err != nil {
			utils.LogError("Failed to hash password: " + err.Error())
			return err
		}

		// Users now get licenses through proper license management system
		// No automatic license generation - admin must install licenses manually
		query := config.InsertUserQuery
		result, err := s.DB.Exec(query, user.Username, user.Email, hashedPasswordBase64)
		if err != nil {
			if isDuplicateEntryError(err) {
				utils.LogWarning("Duplicate username: " + user.Username)
				return errors.New("username already exists")
			}
			utils.LogError("Failed to insert user: " + err.Error())
			return err
		}

		lastInsertID, err := result.LastInsertId()
		if err != nil {
			utils.LogError("Failed to get last insert ID: " + err.Error())
			return err
		}
		userID = int(lastInsertID)

		utils.LogInfo("User created: " + user.Username)
		return nil
	}, config.MaxRetries, config.RetryDelay)
	return userID, err
}

// GetUsers retrieves all users from the database and returns them as a slice of User models.
// It performs the database query with retry logic and handles any errors encountered.
func (s *UserService) GetUsers() ([]models.User, error) {
	var users []models.User

	err := utils.RetryOperation(func() error {
		query := "SELECT id, username, password, role, email, created_at FROM users"
		rows, err := s.DB.Query(query)
		if err != nil {
			utils.LogError("Failed to fetch users: " + err.Error())
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var user models.User
			var email, role sql.NullString
			err := rows.Scan(&user.ID, &user.Username, &user.Password, &role, &email, &user.CreatedAt)

			if err != nil {
				utils.LogError("Failed to scan user: " + err.Error())
				return err
			}

			// Handle nullable fields
			if email.Valid {
				user.Email = email.String
			}
			if role.Valid {
				user.Role = role.String
			}

			users = append(users, user)
		}

		if err = rows.Err(); err != nil {
			utils.LogError("Rows error: " + err.Error())
			return err
		}

		return nil
	}, config.MaxRetries, config.RetryDelay)
	return users, err
}

// GetUserByUsername retrieves a user from the database by their username.
// It performs the database query with retry logic and handles any errors encountered.
// Returns the user information without the password field for security.
func (s *UserService) GetUserByUsername(username string) (*models.User, error) {
	var user models.User
	var email sql.NullString

	err := utils.RetryOperation(func() error {
		query := "SELECT id, username, email, role, created_at FROM users WHERE username = ?"
		err := s.DB.QueryRow(query, username).Scan(&user.ID, &user.Username, &email, &user.Role, &user.CreatedAt)
		if err != nil {
			if err == sql.ErrNoRows {
				utils.LogWarning("User not found: " + username)
				return err
			}
			utils.LogError("Failed to query user: " + err.Error())
			return err
		}
		return nil
	}, config.MaxRetries, config.RetryDelay)

	if err != nil {
		return nil, err
	}

	// Convert sql.NullString to regular string (empty string if NULL)
	if email.Valid {
		user.Email = email.String
	}

	return &user, nil
}

func isDuplicateEntryError(err error) bool {
	// Check if the error is a MySQL duplicate entry error
	if mysqlErr, ok := err.(*mysql.MySQLError); ok && mysqlErr.Number == 1062 {
		return true
	}
	return false
}

// UpdateUser updates specific user fields dynamically based on provided updates map.
// Supports partial updates for username, email, role, and password fields.
// Only updates non-empty fields and validates data appropriately.
// Returns any error encountered during the process.
func (s *UserService) UpdateUser(userID int, updates map[string]interface{}) error {
	return utils.RetryOperation(func() error {
		// Check if the user exists first
		var existingUsername string
		checkQuery := "SELECT username FROM users WHERE id = ?"
		err := s.DB.QueryRow(checkQuery, userID).Scan(&existingUsername)
		if err != nil {
			if err == sql.ErrNoRows {
				utils.LogError(fmt.Sprintf("User not found with ID: %d", userID))
				return errors.New("user not found")
			}
			utils.LogError("Failed to query user: " + err.Error())
			return err
		}

		// Build dynamic query parts
		setParts := []string{}
		args := []interface{}{}
		updatedFields := []string{}

		for field, value := range updates {
			switch field {
			case "username":
				if username, ok := value.(string); ok && username != "" {
					setParts = append(setParts, "username = ?")
					args = append(args, username)
					updatedFields = append(updatedFields, "username")
				}
			case "email":
				if email, ok := value.(string); ok {
					setParts = append(setParts, "email = ?")
					args = append(args, email)
					updatedFields = append(updatedFields, "email")
				}
			case "role":
				if role, ok := value.(string); ok && role != "" {
					// Validate role
					if !models.IsValidRole(role) {
						return fmt.Errorf("invalid role: %s. Must be 'admin', 'operator', or 'viewer'", role)
					}
					setParts = append(setParts, "role = ?")
					args = append(args, role)
					updatedFields = append(updatedFields, "role")
				}
			case "password":
				if password, ok := value.(string); ok && password != "" {
					// Use the existing username for password validation
					usernameForValidation := existingUsername
					if newUsername, exists := updates["username"]; exists {
						if un, ok := newUsername.(string); ok && un != "" {
							usernameForValidation = un
						}
					}

					// Validate the new password
					if err := s.validatePassword(usernameForValidation, password); err != nil {
						utils.LogError("Password validation failed: " + err.Error())
						return fmt.Errorf("password validation failed: %w", err)
					}

					// Hash password using utility function
					hashedPasswordBase64, err := passwordUtils.HashPassword(password)
					if err != nil {
						return fmt.Errorf("failed to hash password: %w", err)
					}

					setParts = append(setParts, "password = ?")
					args = append(args, hashedPasswordBase64)
					updatedFields = append(updatedFields, "password")
				}
			}
		}

		if len(setParts) == 0 {
			return errors.New("no valid fields provided for update")
		}

		// Build and execute the dynamic update query
		query := fmt.Sprintf("UPDATE users SET %s WHERE id = ?", strings.Join(setParts, ", "))
		args = append(args, userID)

		result, err := s.DB.Exec(query, args...)
		if err != nil {
			if isDuplicateEntryError(err) {
				return errors.New("username already exists")
			}
			utils.LogError("Failed to update user: " + err.Error())
			return err
		}

		// A 0-row result means the submitted values already match what's stored
		// (MySQL reports 0 affected rows for a no-op UPDATE). The user is known to
		// exist (checked above), so treat this as success rather than a 500.
		if _, err := result.RowsAffected(); err != nil {
			return fmt.Errorf("failed to get affected rows: %w", err)
		}

		utils.LogInfo(fmt.Sprintf("User %d updated successfully. Fields: [%s]", userID, strings.Join(updatedFields, ", ")))
		return nil
	}, config.MaxRetries, config.RetryDelay)
}

// DeleteUser deletes a user from the database with the given ID.
// It prevents deletion of the last admin user to avoid system lockout.
// Multiple admin users can be deleted as long as at least one admin remains.
// It performs the database operation with retry logic and handles any errors encountered.
func (s *UserService) DeleteUser(id string) error {
	return utils.RetryOperation(func() error {
		// First, check if the user has admin role
		var role string
		checkRoleQuery := "SELECT role FROM users WHERE id = ?"
		err := s.DB.QueryRow(checkRoleQuery, id).Scan(&role)
		if err != nil {
			if err == sql.ErrNoRows {
				utils.LogWarning("Attempted to delete non-existent user with ID: " + id)
				return ErrUserNotFound
			}
			utils.LogError("Failed to check user role before deletion: " + err.Error())
			return err
		}

		// If user is admin, check if they are the last admin
		if role == "admin" {
			adminCount, err := s.GetAdminCount()
			if err != nil {
				utils.LogError("Failed to count admin users: " + err.Error())
				return err
			}

			// Prevent deletion if this is the last admin
			if adminCount <= 1 {
				utils.LogWarning("Attempted to delete the last admin user with ID: " + id)
				return ErrAdminDeletion
			}

			utils.LogInfo(fmt.Sprintf("Allowing deletion of admin user (ID: %s) - %d admin(s) will remain", id, adminCount-1))
		}

		// Proceed with deletion
		query := config.DeleteUserQuery
		_, err = s.DB.Exec(query, id)
		if err != nil {
			utils.LogError("Failed to delete user: " + err.Error())
		} else {
			utils.LogInfo("User deleted successfully: ID " + id)
		}
		return err
	}, config.MaxRetries, config.RetryDelay)
}

// ValidationError represents different types of validation failures
type ValidationError struct {
	Type    string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// Common validation error types
var (
	ErrUserNotFound    = &ValidationError{Type: "USER_NOT_FOUND", Message: "User not found"}
	ErrInvalidPassword = &ValidationError{Type: "INVALID_PASSWORD", Message: "Invalid password"}
	ErrSystemError     = &ValidationError{Type: "SYSTEM_ERROR", Message: "System error occurred"}
	ErrAdminDeletion   = &ValidationError{Type: "ADMIN_DELETION_FORBIDDEN", Message: "Cannot delete the last admin user: this would prevent system access"}
)

// ValidateUser validates the given username and password against the stored hashed password in the database.
// It also validates the user's license key. Returns the user ID, license key, validation status, and any error encountered.
func (s *UserService) ValidateUser(username, password string) (int, string, bool, error) {
	var hashedPasswordBase64 string
	var user_id int

	// Query the database for the hashed password. Absence (sql.ErrNoRows) is a
	// definitive answer, not a transient failure — returning it as an error
	// would make RetryOperation sleep/retry on every bad-username login.
	notFound := false
	err := utils.RetryOperation(func() error {
		notFound = false
		query := config.SelectUserIdQuery
		err := s.DB.QueryRow(query, username).Scan(&user_id, &hashedPasswordBase64)
		if err != nil {
			if err == sql.ErrNoRows {
				notFound = true
				return nil // Definitive: user does not exist
			}
			utils.LogError("Failed to query user: " + err.Error())
			return err // Other errors
		}
		return nil
	}, config.MaxRetries, config.RetryDelay)

	if err != nil {
		return 0, "", false, ErrSystemError // Other errors
	}
	if notFound {
		utils.LogWarning("User not found: " + username)
		return 0, "", false, ErrUserNotFound
	}

	// License validation is now handled separately through GetLicenseStatus()
	// ValidateUser only validates username/password - license is checked in handlers

	// Use the utility function to verify password
	isValid, err := passwordUtils.VerifyPassword(password, hashedPasswordBase64)
	if err != nil {
		utils.LogError("Failed to verify password: " + err.Error())
		return 0, "", false, ErrSystemError
	}

	if isValid {
		utils.LogInfo("User validated successfully: " + username)
		// Transparent hash upgrade: re-hash legacy/weaker hashes with the
		// current PBKDF2 parameters while the plaintext is available.
		if passwordUtils.NeedsRehash(hashedPasswordBase64) {
			if newHash, hashErr := passwordUtils.HashPassword(password); hashErr != nil {
				utils.LogError("Failed to rehash password for user " + username + ": " + hashErr.Error())
			} else if _, updErr := s.DB.Exec(config.UpdateUserPasswordQuery, newHash, user_id); updErr != nil {
				utils.LogError("Failed to store rehashed password for user " + username + ": " + updErr.Error())
			} else {
				utils.LogInfo("Upgraded password hash for user " + username + " (" + passwordUtils.GetPasswordHashInfo(hashedPasswordBase64) + " -> pbkdf2-versioned)")
			}
		}
		return user_id, "", true, nil // Valid password (license key no longer returned)
	}

	utils.LogWarning("Invalid password for user: " + username)

	return 0, "", false, ErrInvalidPassword // Invalid password
}

// SaveToken saves the generated token for the given username in the database with the configured expiration time.
// Returns any error encountered during the process.
func (s *UserService) SaveToken(username, token string) error {
	return s.saveToken(username, token)
}

// saveToken saves the generated token for the given username in the database with the configured expiration time.
// Returns any error encountered during the process.
func (s *UserService) saveToken(userId, token string) error {
	// Define the token expiration time
	expirationTime := time.Now().Add(time.Duration(config.TokenExpirationMinutes) * time.Minute)

	// Create the APIToken struct
	apiToken := models.APIToken{
		TokenValue: token,
		UserID:     userId,
		ExpiresAt:  expirationTime,
	}

	// Convert the scopes slice to a comma-separated string
	scopesStr := strings.Join(apiToken.Scopes, ",")
	query := config.InsertTokenQuery

	// Execute the insert query
	result, err := s.DB.Exec(query, apiToken.TokenValue, apiToken.UserID, scopesStr, apiToken.ExpiresAt)
	if err != nil {
		utils.LogError("Failed to save token: " + err.Error())
		return err
	}

	// Check the number of affected rows
	rowsAffected, err := result.RowsAffected()
	if err != nil {
		utils.LogError("Failed to get affected rows: " + err.Error())
		return err
	}

	if rowsAffected == 0 {
		utils.LogWarning("No rows were inserted")
	}

	return nil
}

// Validate the password against the following rules:
// - Must be at least 9 characters long
// - Must contain at least one uppercase letter
// - Must contain at least one lowercase letter
// - Must contain at least one number
// - Must contain at least one special character
// - Must not contain the same character more than twice in a row
// - Must not contain consecutive characters
// - Must not be the same as the username
// - Must not be the same as the previous password
func (s *UserService) validatePassword(username, password string) error {
	if len(password) < config.MinPasswordLength {
		err := errors.New("password must be at least 9 characters long")
		utils.LogError(err.Error())
		return err
	}

	if password == username {
		err := errors.New("password must not be the same as the username")
		utils.LogError(err.Error())
		return err
	}

	var hasUpper, hasLower, hasNumber, hasSpecial bool
	var prevChar rune
	var repeatCount int

	for i, char := range password {
		switch {
		case unicode.IsUpper(char):
			hasUpper = true
		case unicode.IsLower(char):
			hasLower = true
		case unicode.IsNumber(char):
			hasNumber = true
		case unicode.IsPunct(char) || unicode.IsSymbol(char):
			hasSpecial = true
		}

		if i > 0 {
			if char == prevChar {
				repeatCount++
				if repeatCount >= 2 {
					err := errors.New("password must not contain the same character more than twice in a row")
					utils.LogError(err.Error())
					return err
				}
			} else {
				repeatCount = 0
			}
		}

		prevChar = char
	}

	if !hasUpper {
		err := errors.New("password must contain at least one uppercase letter")
		utils.LogError(err.Error())
		return err
	}
	if !hasLower {
		err := errors.New("password must contain at least one lowercase letter")
		utils.LogError(err.Error())
		return err
	}
	if !hasNumber {
		err := errors.New("password must contain at least one number")
		utils.LogError(err.Error())
		return err
	}
	if !hasSpecial {
		err := errors.New("password must contain at least one special character")
		utils.LogError(err.Error())
		return err
	}

	// Reject reuse of the current password.
	var previousPassword string
	query := config.SelectUserPasswordQuery
	err := s.DB.QueryRow(query, username).Scan(&previousPassword)
	if err != nil {
		if err == sql.ErrNoRows {
			// No previous password found, continue with validation
			utils.LogInfo("No previous password found for username: " + username)
		} else {
			utils.LogError("Failed to query previous password: " + err.Error())
			return err
		}
	} else {
		// Use utility function to verify password
		isMatch, err := passwordUtils.VerifyPassword(password, previousPassword)
		if err != nil {
			utils.LogError("Failed to verify previous password: " + err.Error())
			return err
		}
		if isMatch {
			err := errors.New("password must not be the same as the previous password")
			utils.LogError(err.Error())
			return err
		}
	}

	utils.LogInfo("Password validated successfully")

	return nil
}

// ValidateToken validates the given token using the in-memory cache and the database as a fallback.
// Returns (true, nil) when the token exists in the store, (false, nil) when it is
// definitively absent (revoked, expired, or never issued), and (false, err) only on
// system errors. Absence is NOT retried — the auth middleware calls this on every
// request, and a retry sleep would penalize each rejected request.
func (s *UserService) ValidateToken(token string) (bool, error) {
	// Check the cache first
	if _, found := s.Cache.Get(token); found {
		return true, nil
	}

	// If not found in cache, check the database
	var username string
	notFound := false
	err := utils.RetryOperation(func() error {
		notFound = false
		query := config.ValidateTokenQuery
		err := s.DB.QueryRow(query, token).Scan(&username)
		if err != nil {
			if err == sql.ErrNoRows {
				notFound = true
				return nil // Definitive answer: token not in store
			}
			utils.LogError("Failed to query token: " + err.Error())
			return err // Other errors
		}
		return nil
	}, config.MaxRetries, config.RetryDelay)

	if err != nil {
		return false, err
	}
	if notFound {
		utils.LogWarning("Token not found in store (revoked or expired)")
		return false, nil
	}

	// Cache the token
	s.Cache.Set(token, username, cache.DefaultExpiration)

	return true, nil
}

// Logout logs out the user by deleting the given token from the cache and the database.
// Returns any error encountered during the process.
func (s *UserService) Logout(tokenString string) error {
	// Remove the token from the cache
	s.Cache.Delete(tokenString)

	return utils.RetryOperation(func() error {
		query := config.DeleteTokenQuery
		_, err := s.DB.Exec(query, tokenString)
		if err != nil {
			utils.LogError("Failed to delete token: " + err.Error())
			return err
		}

		utils.LogInfo("User logged out and token deleted: " + tokenString)
		return nil
	}, config.MaxRetries, config.RetryDelay)
}

// PingDB pings the database to check its availability. Returns any error encountered during the process.
func (s *UserService) PingDB() error {
	return utils.RetryOperation(func() error {
		return s.DB.Ping()
	}, config.MaxRetries, config.RetryDelay)
}

// ValidatePassword is a public wrapper function that validates the given password against the username.
// Returns any validation error encountered.
func (s *UserService) ValidatePassword(username, password string) error {
	return s.validatePassword(username, password)
}

// GetAdminCount returns the number of admin users in the system
func (s *UserService) GetAdminCount() (int, error) {
	var count int
	query := "SELECT COUNT(*) FROM users WHERE role = 'admin'"
	err := s.DB.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count admin users: %w", err)
	}
	return count, nil
}

// GetTotalUserCount returns the total number of users in the system
func (s *UserService) GetTotalUserCount() (int, error) {
	var count int
	query := "SELECT COUNT(*) FROM users"
	err := s.DB.QueryRow(query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count total users: %w", err)
	}
	return count, nil
}

// CreateUserWithRole creates a user with the given role in a single transaction.
func (s *UserService) CreateUserWithRole(username, email, password, role string) (int, error) {
	// Least-privilege default: unspecified role gets read-only viewer.
	if role == "" {
		role = models.RoleViewer
	}

	// Validate role
	if !models.IsValidRole(role) {
		return 0, fmt.Errorf("invalid role: %s. Must be 'admin', 'operator', or 'viewer'", role)
	}

	var userID int
	err := utils.RetryOperation(func() error {
		tx, err := s.DB.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback()

		// Check if username already exists BEFORE validating password
		var existingUserID int
		checkQuery := `SELECT id FROM users WHERE username = ?`
		err = tx.QueryRow(checkQuery, username).Scan(&existingUserID)
		if err == nil {
			// Username already exists
			return fmt.Errorf("username already exists")
		} else if err != sql.ErrNoRows {
			// Some other database error occurred
			return fmt.Errorf("failed to check username availability: %w", err)
		}
		// If err == sql.ErrNoRows, username is available - continue

		// Validate password (only for new users, not against existing passwords)
		if err := s.validatePassword(username, password); err != nil {
			return fmt.Errorf("password validation failed: %w", err)
		}

		// Hash password
		hashedPasswordBase64, err := passwordUtils.HashPassword(password)
		if err != nil {
			return fmt.Errorf("failed to hash password: %w", err)
		}

		// Insert user with role
		insertQuery := `INSERT INTO users (username, email, password, role) VALUES (?, ?, ?, ?)`
		result, err := tx.Exec(insertQuery, username, email, hashedPasswordBase64, role)
		if err != nil {
			if isDuplicateEntryError(err) {
				return fmt.Errorf("username already exists")
			}
			return fmt.Errorf("failed to insert user: %w", err)
		}

		userIDInt64, err := result.LastInsertId()
		if err != nil {
			return fmt.Errorf("failed to get user ID: %w", err)
		}
		userID = int(userIDInt64)

		if err = tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}

		utils.LogInfo(fmt.Sprintf("User created with role '%s': %s", role, username))
		return nil
	}, config.MaxRetries, config.RetryDelay)

	return userID, err
}

// GetSystemInstallationID gets or creates a unique installation ID
func (s *UserService) GetSystemInstallationID() (string, error) {
	var installationID string
	query := "SELECT setting_value FROM system_settings WHERE setting_key = 'installation_id'"
	err := s.DB.QueryRow(query).Scan(&installationID)
	if err == sql.ErrNoRows {
		// Create new installation ID if not exists
		installationID = s.generateInstallationID()
		insertQuery := "INSERT INTO system_settings (setting_key, setting_value) VALUES ('installation_id', ?)"
		_, err = s.DB.Exec(insertQuery, installationID)
		if err != nil {
			return "", fmt.Errorf("failed to create installation ID: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("failed to get installation ID: %w", err)
	}
	return installationID, nil
}

// generateInstallationID creates a unique installation identifier
func (s *UserService) generateInstallationID() string {
	// Use current time, random data, and system info to create unique ID
	data := fmt.Sprintf("%d-%d-%s", time.Now().UnixNano(), time.Now().Unix(), "oam-loxilb")
	hash := sha256.Sum256([]byte(data))
	return fmt.Sprintf("OAM-INST-%s", base64.URLEncoding.EncodeToString(hash[:16]))
}

// Admin Credential Management Methods

// CheckDefaultAdminCredentials checks if the admin user still has default credentials
func (s *UserService) CheckDefaultAdminCredentials() (bool, int, error) {
	var userID int
	var hashedPassword string

	query := "SELECT id, password FROM users WHERE username = ? AND role = ?"
	err := s.DB.QueryRow(query, "admin", "admin").Scan(&userID, &hashedPassword)

	if err == sql.ErrNoRows {
		return false, 0, nil // No admin user found
	}
	if err != nil {
		return false, 0, fmt.Errorf("failed to query admin user: %w", err)
	}

	// Check whether the password still matches the configured default admin
	// password (OAM_DEFAULT_ADMIN_PASSWORD).
	hasDefaultPassword, err := passwordUtils.VerifyDefaultPassword(hashedPassword)
	if err != nil {
		return false, 0, fmt.Errorf("failed to verify default password: %w", err)
	}

	return hasDefaultPassword, userID, nil
}

// GetAdminCredentialStatus returns the current status of admin credentials
func (s *UserService) GetAdminCredentialStatus() (*models.AdminCredentialStatus, error) {
	hasDefault, adminUserID, err := s.CheckDefaultAdminCredentials()
	if err != nil {
		return nil, fmt.Errorf("failed to check default credentials: %w", err)
	}

	adminExists := (adminUserID > 0)

	// Check if credentials have been updated
	var credentialsUpdated bool
	if adminExists {
		query := "SELECT COALESCE(credentials_updated, FALSE) FROM users WHERE id = ?"
		err = s.DB.QueryRow(query, adminUserID).Scan(&credentialsUpdated)
		if err != nil {
			return nil, fmt.Errorf("failed to check credentials updated status: %w", err)
		}
	}

	// Get system info
	installationID, _ := s.GetSystemInstallationID()

	return &models.AdminCredentialStatus{
		NeedsCredentialUpdate: hasDefault && !credentialsUpdated,
		AdminExists:           adminExists,
		HasDefaultCredentials: hasDefault,
		CredentialsUpdated:    credentialsUpdated,
		SystemInfo: models.SystemInfo{
			Version:        "1.0",
			InstallationID: installationID,
			AdminUserID:    adminUserID,
		},
	}, nil
}

// UpdateAdminCredentials updates admin credentials from default values
func (s *UserService) UpdateAdminCredentials(currentUsername, currentPassword, newUsername, newPassword, newEmail string) error {
	return utils.RetryOperation(func() error {
		// Start transaction
		tx, err := s.DB.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback()

		// Verify current credentials
		var userID int
		var hashedPassword string
		query := "SELECT id, password FROM users WHERE username = ? AND role = ?"
		err = tx.QueryRow(query, currentUsername, "admin").Scan(&userID, &hashedPassword)
		if err == sql.ErrNoRows {
			return errors.New("admin user not found")
		}
		if err != nil {
			return fmt.Errorf("failed to query admin user: %w", err)
		}

		// Verify current password
		passwordMatch, err := passwordUtils.VerifyPassword(currentPassword, hashedPassword)
		if err != nil {
			return fmt.Errorf("failed to verify current password: %w", err)
		}
		if !passwordMatch {
			return errors.New("current password is incorrect")
		}

		// Validate new password
		if err := s.validatePassword(newUsername, newPassword); err != nil {
			return fmt.Errorf("new password validation failed: %w", err)
		}

		// Check if new username already exists (if different from current)
		if newUsername != currentUsername {
			var existingID int
			checkQuery := "SELECT id FROM users WHERE username = ? AND id != ?"
			err = tx.QueryRow(checkQuery, newUsername, userID).Scan(&existingID)
			if err == nil {
				return errors.New("username already exists")
			} else if err != sql.ErrNoRows {
				return fmt.Errorf("failed to check username uniqueness: %w", err)
			}
		}

		// Hash new password
		newHashedPasswordBase64, err := passwordUtils.HashPassword(newPassword)
		if err != nil {
			return fmt.Errorf("failed to hash new password: %w", err)
		}

		// Update user credentials
		updateQuery := `UPDATE users
			SET username = ?, password = ?, email = ?,
			    credentials_updated = TRUE, credentials_updated_at = NOW(),
			    must_change_password = FALSE
			WHERE id = ?`
		_, err = tx.Exec(updateQuery, newUsername, newHashedPasswordBase64, newEmail, userID)
		if err != nil {
			return fmt.Errorf("failed to update user credentials: %w", err)
		}

		// Update system config to mark admin credentials as updated
		configQuery := `INSERT INTO system_config (config_key, config_value)
			VALUES ('admin_credentials_updated', 'true')
			ON DUPLICATE KEY UPDATE config_value = 'true', updated_at = NOW()`
		_, err = tx.Exec(configQuery)
		if err != nil {
			return fmt.Errorf("failed to update system config: %w", err)
		}

		// Commit transaction
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit credential update: %w", err)
		}

		utils.LogInfo(fmt.Sprintf("Admin credentials updated successfully for user ID %d", userID))
		return nil
	}, config.DbMaxRetries, config.DbRetryDelay)
}

// HasDefaultCredentials checks if a specific user has default credentials
func (s *UserService) HasDefaultCredentials(userID int) (bool, error) {
	var hashedPassword string
	query := "SELECT password FROM users WHERE id = ?"
	err := s.DB.QueryRow(query, userID).Scan(&hashedPassword)
	if err != nil {
		return false, fmt.Errorf("failed to query user password: %w", err)
	}

	// Check whether the password still matches the configured default admin
	// password (OAM_DEFAULT_ADMIN_PASSWORD).
	return passwordUtils.VerifyDefaultPassword(hashedPassword)
}

// MarkCredentialsUpdated marks that a user has updated their credentials
func (s *UserService) MarkCredentialsUpdated(userID int) error {
	query := `UPDATE users
		SET credentials_updated = TRUE, credentials_updated_at = NOW()
		WHERE id = ?`
	_, err := s.DB.Exec(query, userID)
	if err != nil {
		return fmt.Errorf("failed to mark credentials as updated: %w", err)
	}
	return nil
}

// ResetAdminToDefault resets the admin account to default credentials
// This is useful for recovery situations or resetting test environments
// Returns the admin user ID and any error encountered
func (s *UserService) ResetAdminToDefault() (int, error) {
	defaultUsername := "admin"
	defaultPassword := config.DefaultConfigPassword
	defaultEmail := "admin@oam-loxilb.local"

	var adminUserID int

	err := utils.RetryOperation(func() error {
		// Start transaction
		tx, err := s.DB.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}
		defer tx.Rollback()

		// Check if admin user exists
		var existingUserID int
		var currentUsername string
		checkQuery := "SELECT id, username FROM users WHERE role = ? LIMIT 1"
		err = tx.QueryRow(checkQuery, "admin").Scan(&existingUserID, &currentUsername)

		if err == sql.ErrNoRows {
			// No admin exists, create one
			utils.LogInfo("No admin user found. Creating default admin account...")

			// Hash the default password
			hashedPasswordBase64, err := passwordUtils.HashPassword(defaultPassword)
			if err != nil {
				return fmt.Errorf("failed to hash default password: %w", err)
			}

			// Insert new admin user
			insertQuery := `INSERT INTO users (username, email, password, role, credentials_updated) 
				VALUES (?, ?, ?, 'admin', FALSE)`
			result, err := tx.Exec(insertQuery, defaultUsername, defaultEmail, hashedPasswordBase64)
			if err != nil {
				return fmt.Errorf("failed to create admin user: %w", err)
			}

			adminIDInt64, err := result.LastInsertId()
			if err != nil {
				return fmt.Errorf("failed to get admin user ID: %w", err)
			}
			existingUserID = int(adminIDInt64)

			utils.LogInfo(fmt.Sprintf("Created default admin account (ID: %d)", existingUserID))
		} else if err != nil {
			return fmt.Errorf("failed to check for existing admin: %w", err)
		} else {
			// Admin exists, reset to default credentials
			utils.LogInfo(fmt.Sprintf("Resetting admin user '%s' (ID: %d) to default credentials...", currentUsername, existingUserID))

			// Hash the default password
			hashedPasswordBase64, err := passwordUtils.HashPassword(defaultPassword)
			if err != nil {
				return fmt.Errorf("failed to hash default password: %w", err)
			}

			// Update admin to default credentials
			updateQuery := `UPDATE users 
				SET username = ?, email = ?, password = ?, credentials_updated = FALSE 
				WHERE id = ?`
			_, err = tx.Exec(updateQuery, defaultUsername, defaultEmail, hashedPasswordBase64, existingUserID)
			if err != nil {
				return fmt.Errorf("failed to reset admin credentials: %w", err)
			}

			utils.LogInfo(fmt.Sprintf("Reset admin user (ID: %d) to default credentials", existingUserID))
		}

		// Invalidate all existing tokens for the admin user
		deleteTokenQuery := "DELETE FROM user_tokens WHERE user_id = ?"
		_, err = tx.Exec(deleteTokenQuery, existingUserID)
		if err != nil {
			utils.LogWarning(fmt.Sprintf("Failed to delete admin tokens: %s", err))
			// Continue anyway - not critical
		}

		// Commit transaction
		if err = tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %w", err)
		}

		// Store the admin user ID for return
		adminUserID = existingUserID

		utils.LogInfo("Admin account reset completed")
		utils.LogInfo(fmt.Sprintf("   Username: %s", defaultUsername))
		utils.LogInfo(fmt.Sprintf("   Password: %s", defaultPassword))
		utils.LogInfo(fmt.Sprintf("   Email: %s", defaultEmail))
		utils.LogInfo("Please change these credentials after logging in.")

		return nil
	}, config.DbMaxRetries, config.DbRetryDelay)

	return adminUserID, err
}

// =================== Login Attempt Tracking Methods ===================

// GetLoginAttempt retrieves the login attempt record for a username and client IP
func (s *UserService) GetLoginAttempt(username, clientIP string) (*models.LoginAttempt, error) {
	var attempt models.LoginAttempt
	var blockedUntil sql.NullTime

	err := utils.RetryOperation(func() error {
		query := config.SelectLoginAttemptQuery
		err := s.DB.QueryRow(query, username, clientIP).Scan(
			&attempt.ID,
			&attempt.Username,
			&attempt.ClientIP,
			&attempt.FailedCount,
			&attempt.LastFailedAt,
			&blockedUntil,
			&attempt.CreatedAt,
			&attempt.UpdatedAt,
		)
		if err != nil {
			if err == sql.ErrNoRows {
				return nil // No record found - not an error
			}
			utils.LogError("Failed to query login attempt: " + err.Error())
			return err
		}

		if blockedUntil.Valid {
			attempt.BlockedUntil = &blockedUntil.Time
		}

		return nil
	}, config.MaxRetries, config.RetryDelay)

	if err != nil {
		return nil, err
	}

	return &attempt, nil
}

// IsLoginBlocked reports whether login is currently blocked for the given
// username and client IP, along with the remaining lockout duration.
func (s *UserService) IsLoginBlocked(username, clientIP string, now time.Time) (bool, time.Duration, error) {
	attempt, err := s.GetLoginAttempt(username, clientIP)
	if err != nil {
		return false, 0, err
	}

	if attempt == nil {
		return false, 0, nil // No record, not blocked
	}

	if attempt.BlockedUntil != nil && now.Before(*attempt.BlockedUntil) {
		remaining := time.Until(*attempt.BlockedUntil)
		utils.LogWarning(fmt.Sprintf("Login blocked for user %s from IP %s, remaining time: %v", username, clientIP, remaining))
		return true, remaining, nil
	}

	return false, 0, nil
}

// RecordFailedLogin records a failed login attempt and implements lockout logic
// Returns the blockedUntil time if lockout is triggered, and any error
func (s *UserService) RecordFailedLogin(username, clientIP string, now time.Time) (*time.Time, error) {
	var blockedUntil *time.Time

	err := utils.RetryOperation(func() error {
		// Check existing attempt
		attempt, err := s.GetLoginAttempt(username, clientIP)
		if err != nil {
			return err
		}

		// Determine if we should reset the counter based on attempt window
		shouldReset := false
		if attempt != nil && !attempt.LastFailedAt.IsZero() {
			timeSinceLastAttempt := now.Sub(attempt.LastFailedAt)
			if timeSinceLastAttempt > config.LoginAttemptWindow {
				shouldReset = true
			}
		}

		if shouldReset {
			// Reset counter to 1 since this is a fresh attempt
			query := config.ResetLoginAttemptCountQuery
			_, err = s.DB.Exec(query, now, username, clientIP)
			if err != nil {
				utils.LogError("Failed to reset login attempt count: " + err.Error())
				return err
			}
			utils.LogInfo(fmt.Sprintf("Reset login attempt counter for user %s from IP %s", username, clientIP))
			return nil
		}

		// Calculate new failed count
		newFailedCount := 1
		if attempt != nil {
			newFailedCount = attempt.FailedCount + 1
		}

		// Check if we should block. The lockout starts at LoginLockoutBase and
		// doubles with every further failed attempt, capped at LoginLockoutMax.
		var blockUntilTime sql.NullTime
		if newFailedCount > config.MaxFailedLoginAttempts {
			duration := config.LoginLockoutBase
			for i := newFailedCount - config.MaxFailedLoginAttempts; i > 1 && duration < config.LoginLockoutMax; i-- {
				duration *= 2
			}
			if duration > config.LoginLockoutMax {
				duration = config.LoginLockoutMax
			}
			lockoutEnd := now.Add(duration)
			blockedUntil = &lockoutEnd
			blockUntilTime = sql.NullTime{Time: lockoutEnd, Valid: true}
			utils.LogWarning(fmt.Sprintf("Login blocked for user %s from IP %s until %v (attempt %d)", username, clientIP, lockoutEnd, newFailedCount))
		}

		// Insert or update attempt record
		query := config.UpsertLoginAttemptQuery
		_, err = s.DB.Exec(query, username, clientIP, now, blockUntilTime)
		if err != nil {
			utils.LogError("Failed to upsert login attempt: " + err.Error())
			return err
		}

		utils.LogInfo(fmt.Sprintf("Recorded failed login for user %s from IP %s (attempt %d)", username, clientIP, newFailedCount))
		return nil
	}, config.MaxRetries, config.RetryDelay)

	return blockedUntil, err
}

// ClearLoginAttempts clears the login attempt record for successful authentication
func (s *UserService) ClearLoginAttempts(username, clientIP string) error {
	err := utils.RetryOperation(func() error {
		query := config.ClearLoginAttemptsQuery
		result, err := s.DB.Exec(query, username, clientIP)
		if err != nil {
			utils.LogError("Failed to clear login attempts: " + err.Error())
			return err
		}

		rowsAffected, _ := result.RowsAffected()
		if rowsAffected > 0 {
			utils.LogInfo(fmt.Sprintf("Cleared login attempts for user %s from IP %s", username, clientIP))
		}

		return nil
	}, config.MaxRetries, config.RetryDelay)

	return err
}
