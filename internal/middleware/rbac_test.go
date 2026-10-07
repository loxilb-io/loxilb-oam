package middleware_test

import (
	"testing"

	"github.com/loxilb-io/loxilb-oam/internal/middleware"
	"github.com/loxilb-io/loxilb-oam/internal/models"

	"github.com/stretchr/testify/assert"
)

func TestCapabilityMatrix(t *testing.T) {
	tests := []struct {
		role   string
		action middleware.Action
		want   bool
	}{
		// admin holds everything
		{models.RoleAdmin, middleware.ActUserAdmin, true},
		{models.RoleAdmin, middleware.ActInstanceWrite, true},
		{models.RoleAdmin, middleware.ActGatewayWrite, true},
		{models.RoleAdmin, middleware.ActGatewayAdmin, true},
		{models.RoleAdmin, middleware.ActConfigWrite, true},
		{models.RoleAdmin, middleware.ActAlertWrite, true},
		{models.RoleAdmin, middleware.ActLogRead, true},
		// operator: gateway + alerts only
		{models.RoleOperator, middleware.ActGatewayWrite, true},
		{models.RoleOperator, middleware.ActAlertWrite, true},
		{models.RoleOperator, middleware.ActUserAdmin, false},
		{models.RoleOperator, middleware.ActInstanceWrite, false},
		{models.RoleOperator, middleware.ActConfigWrite, false},
		{models.RoleOperator, middleware.ActGatewayAdmin, false},
		{models.RoleLegacyUser, middleware.ActGatewayAdmin, false},
		{models.RoleViewer, middleware.ActGatewayAdmin, false},
		// the raw server log is admin-only: it can incidentally carry
		// credentials or tokens from any code path, so a lower-privileged
		// reader could escalate. Reads of *resources* stay ungated.
		{models.RoleOperator, middleware.ActLogRead, false},
		{models.RoleViewer, middleware.ActLogRead, false},
		{models.RoleLegacyUser, middleware.ActLogRead, false},
		// legacy "user" behaves as operator
		{models.RoleLegacyUser, middleware.ActGatewayWrite, true},
		{models.RoleLegacyUser, middleware.ActUserAdmin, false},
		// viewer: nothing
		{models.RoleViewer, middleware.ActGatewayWrite, false},
		{models.RoleViewer, middleware.ActAlertWrite, false},
		{models.RoleViewer, middleware.ActUserAdmin, false},
		// unknown role: nothing
		{"bogus", middleware.ActGatewayWrite, false},
		{"bogus", middleware.ActApplianceRead, false},

		// Appliance: everyone may look.
		{models.RoleAdmin, middleware.ActApplianceRead, true},
		{models.RoleOperator, middleware.ActApplianceRead, true},
		{models.RoleLegacyUser, middleware.ActApplianceRead, true},
		{models.RoleViewer, middleware.ActApplianceRead, true},
	}
	// Appliance lifecycle actions are admin-only, each on its own.
	for _, action := range []middleware.Action{
		middleware.ActApplianceBackup, middleware.ActApplianceRestore, middleware.ActApplianceUpdate,
		middleware.ActApplianceRollback, middleware.ActApplianceReset, middleware.ActApplianceDiagnostics,
	} {
		tests = append(tests,
			struct {
				role   string
				action middleware.Action
				want   bool
			}{models.RoleAdmin, action, true},
			struct {
				role   string
				action middleware.Action
				want   bool
			}{models.RoleOperator, action, false},
			struct {
				role   string
				action middleware.Action
				want   bool
			}{models.RoleViewer, action, false},
		)
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, middleware.Can(tt.role, tt.action),
			"Can(%q, %q)", tt.role, tt.action)
	}
}

func TestRoleHelpers(t *testing.T) {
	assert.Equal(t, models.RoleOperator, models.NormalizeRole(models.RoleLegacyUser))
	assert.Equal(t, models.RoleAdmin, models.NormalizeRole(models.RoleAdmin))

	assert.True(t, models.IsValidRole(models.RoleAdmin))
	assert.True(t, models.IsValidRole(models.RoleOperator))
	assert.True(t, models.IsValidRole(models.RoleViewer))
	assert.True(t, models.IsValidRole(models.RoleLegacyUser))
	assert.False(t, models.IsValidRole("root"))
	assert.False(t, models.IsValidRole(""))
}
