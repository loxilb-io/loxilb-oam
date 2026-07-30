package tests

import (
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
)

func TestValidateToken(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	// Initialize the in-memory cache
	c := cache.New(5*time.Minute, 10*time.Minute)
	userService := services.NewUserService(db)
	userService.Cache = c

	// Define the expected query
	expectedQuery := "SELECT user_id FROM api_tokens WHERE token_value = \\? AND expires_at > NOW()"

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

	// Initialize the in-memory cache
	c := cache.New(5*time.Minute, 10*time.Minute)
	userService := services.NewUserService(db)
	userService.Cache = c

	// Define the expected query
	expectedQuery := "SELECT user_id FROM api_tokens WHERE token_value = \\? AND expires_at > NOW()"

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

	// Initialize the in-memory cache
	c := cache.New(5*time.Minute, 10*time.Minute)
	userService := services.NewUserService(db)
	userService.Cache = c

	// Define the expected query
	expectedQuery := "SELECT user_id FROM api_tokens WHERE token_value = \\? AND expires_at > NOW()"

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
