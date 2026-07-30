package services_test

import (
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestFetchLoxiLBInstances(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	loxilbService := services.NewLoxiLBService(db)

	// Define the expected rows
	rows := sqlmock.NewRows([]string{
		"id", "name", "host", "port", "protocol", "description", "version", "api_endpoint", "cimage", "ctag", "is_active", "created_at",
	}).AddRow(
		1, "instance1", "localhost", "8080", "https", "description1", "v1", "https://localhost:8080/netlox/v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7", true, time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
	).AddRow(
		2, "instance2", "localhost", "8081", "https", "description2", "v1", "https://localhost:8081/netlox/v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7", true, time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
	)

	// Expect the query to be executed
	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances").
		WillReturnRows(rows)

	// Call the FetchLoxiLBInstances function
	instances, err := loxilbService.FetchLoxiLBInstances()

	// Assert the results
	assert.NoError(t, err)
	assert.Len(t, instances, 2)
	assert.Equal(t, "instance1", instances[0].Name)
	assert.Equal(t, "instance2", instances[1].Name)

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestFetchLoxiLBInstanceByID(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	loxilbService := services.NewLoxiLBService(db)

	// Define the expected row
	row := sqlmock.NewRows([]string{
		"id", "name", "host", "port", "protocol", "description", "version", "api_endpoint", "cimage", "ctag", "is_active", "created_at",
	}).AddRow(
		1, "instance1", "localhost", "8080", "https", "description1", "v1", "https://localhost:8080/netlox/v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7", true, time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
	)

	// Expect the query to be executed
	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances WHERE id = \\?").
		WithArgs(1).
		WillReturnRows(row)

	// Call the FetchLoxiLBInstanceByID function
	instance, err := loxilbService.FetchLoxiLBInstanceByID(1)

	// Assert the results
	assert.NoError(t, err)
	assert.NotNil(t, instance)
	assert.Equal(t, "instance1", instance.Name)

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestFetchLoxiLBInstanceByIDNotFound(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	loxilbService := services.NewLoxiLBService(db)

	// Expect the query to be executed but return no rows
	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances WHERE id = \\?").
		WithArgs(999).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "host", "port", "protocol", "description", "version", "api_endpoint", "cimage", "ctag", "is_active", "created_at",
		}))

	// Call the FetchLoxiLBInstanceByID function
	instance, err := loxilbService.FetchLoxiLBInstanceByID(999)

	// Assert the results
	assert.Error(t, err)
	assert.Nil(t, instance)

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestAddLoxiLBInstance(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	loxilbService := services.NewLoxiLBService(db)

	// Define the instance to be added
	instance := models.LoxiLBInstance{
		Name:        "instance3",
		Host:        "localhost",
		Port:        "8082",
		Protocol:    "https",
		Description: "description3",
		Version:     "v1",
		ApiEndpoint: "https://localhost:8082/netlox/v1",
		Cimage:      "ghcr.io/loxilb-io/loxilb",
		Ctag:        "v0.9.7",
		IsActive:    true,
	}

	// Expect the insert query to be executed
	mock.ExpectExec("INSERT INTO loxilb_instances").
		WithArgs(instance.Name, instance.Host, instance.Port, instance.Protocol, instance.Description,
			instance.Version, instance.ApiEndpoint, instance.Cimage, instance.Ctag, instance.IsActive).
		WillReturnResult(sqlmock.NewResult(3, 1))

	// Call the AddLoxiLBInstance function
	_, err = loxilbService.AddLoxiLBInstance(instance)

	// Assert the results
	assert.NoError(t, err)

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestUpdateLoxiLBInstance(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	loxilbService := services.NewLoxiLBService(db)

	// Define the instance to be updated
	instance := models.LoxiLBInstance{
		ID:          1,
		Name:        "instance1_updated",
		Host:        "localhost",
		Port:        "8080",
		Protocol:    "https",
		Description: "description1_updated",
		Version:     "v1",
		ApiEndpoint: "https://localhost:8080/netlox/v1",
		Cimage:      "ghcr.io/loxilb-io/loxilb",
		Ctag:        "v0.9.7",
		IsActive:    true,
	}

	// UpdateLoxiLBInstance first verifies the instance exists
	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances WHERE id = \\?").
		WithArgs(instance.ID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "host", "port", "protocol", "description", "version", "api_endpoint", "cimage", "ctag", "is_active", "created_at",
		}).AddRow(
			1, "instance1", "localhost", "8080", "https", "description1", "v1", "https://localhost:8080/netlox/v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7", true, time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		))

	// Expect the update query to be executed
	mock.ExpectExec("UPDATE loxilb_instances SET").
		WithArgs(instance.Name, instance.Host, instance.Port, instance.Protocol, instance.Description,
			instance.Version, instance.ApiEndpoint, instance.Cimage, instance.Ctag, instance.IsActive, instance.ID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Call the UpdateLoxiLBInstance function
	err = loxilbService.UpdateLoxiLBInstance(instance)

	// Assert the results
	assert.NoError(t, err)

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}

func TestDeleteLoxiLBInstance(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	loxilbService := services.NewLoxiLBService(db)

	// Expect the delete query to be executed
	mock.ExpectExec("DELETE FROM loxilb_instances WHERE id = \\?").
		WithArgs(1).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Call the DeleteLoxiLBInstance function
	err = loxilbService.DeleteLoxiLBInstance(1)

	// Assert the results
	assert.NoError(t, err)

	// Ensure all expectations were met
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Errorf("there were unfulfilled expectations: %s", err)
	}
}
