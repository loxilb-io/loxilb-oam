package services_test

import (
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestValidateToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	userService := services.NewUserService(db)

	// Define the expected query
	expectedQuery := "SELECT user_id FROM api_tokens WHERE token_value = \\$1 AND expires_at > NOW()"

	// Set up the expected query and result
	rows := sqlmock.NewRows([]string{"user_id"}).AddRow("user_1")
	mock.ExpectQuery(expectedQuery).WithArgs("dummy_token_1").WillReturnRows(rows)

	// Call the ValidateToken function
	valid, err := userService.ValidateToken("dummy_token_1")

	// Assert the results
	assert.NoError(t, err)
	assert.True(t, valid)

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestValidateTokenExpired(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	userService := services.NewUserService(db)

	// Define the expected query
	expectedQuery := "SELECT user_id FROM api_tokens WHERE token_value = \\$1 AND expires_at > NOW()"

	// Set up the expected query with no results (token expired)
	mock.ExpectQuery(expectedQuery).WithArgs("dummy_token_2").WillReturnRows(sqlmock.NewRows([]string{"user_id"}))

	// Call the ValidateToken function
	valid, err := userService.ValidateToken("dummy_token_2")

	// Absence from the store is a definitive answer, not an error
	assert.NoError(t, err)
	assert.False(t, valid)

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestValidateTokenInvalid(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	userService := services.NewUserService(db)

	// Define the expected query
	expectedQuery := "SELECT user_id FROM api_tokens WHERE token_value = \\$1 AND expires_at > NOW()"

	// Set up the expected query with no results (invalid token)
	mock.ExpectQuery(expectedQuery).WithArgs("invalid_token").WillReturnRows(sqlmock.NewRows([]string{"user_id"}))

	// Call the ValidateToken function
	valid, err := userService.ValidateToken("invalid_token")

	// Absence from the store is a definitive answer, not an error
	assert.NoError(t, err)
	assert.False(t, valid)

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}
func TestValidatePassword(t *testing.T) {
	userService := services.NewUserService(nil)

	tests := []struct {
		username string
		password string
		expected string
	}{
		{"user1", "short", "password must be at least 9 characters long"},
		{"user1user12", "user1user12", "password must not be the same as the username"},
		{"user1", "NoNumber!", "password must contain at least one number"},
		{"user1", "nonumber!", "password must contain at least one uppercase letter"},
		{"user1", "NOLOWER1!", "password must contain at least one lowercase letter"},
		{"user1", "NoSpecial1", "password must contain at least one special character"},
		{"user1", "NoooSpecial1!", "password must not contain the same character more than twice in a row"},
	}

	for _, tt := range tests {
		t.Run(tt.password, func(t *testing.T) {
			err := userService.ValidatePassword(tt.username, tt.password)
			if tt.expected == "" {
				assert.NoError(t, err)
			} else {
				assert.EqualError(t, err, tt.expected)
			}
		})
	}
}
