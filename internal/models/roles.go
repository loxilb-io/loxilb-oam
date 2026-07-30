package models

// Role values for users.role.
// RoleLegacyUser is the pre-RBAC "user" value; it is kept in the DB enum and
// accepted on input for compatibility, and is treated as operator everywhere.
const (
	RoleAdmin      = "admin"
	RoleOperator   = "operator"
	RoleViewer     = "viewer"
	RoleLegacyUser = "user"
)

// NormalizeRole maps legacy role values onto the 3-role model.
func NormalizeRole(role string) string {
	if role == RoleLegacyUser {
		return RoleOperator
	}
	return role
}

// IsValidRole reports whether role is an accepted users.role value.
func IsValidRole(role string) bool {
	switch role {
	case RoleAdmin, RoleOperator, RoleViewer, RoleLegacyUser:
		return true
	}
	return false
}
