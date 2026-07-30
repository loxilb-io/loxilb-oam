package services_test

import (
	"github.com/loxilb-io/loxilb-oam/internal/services"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

func TestFetchLogs(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	defer db.Close()

	logService := services.NewLogService(db)

	columns := []string{"id", "level", "timestamp", "severity", "facility", "programname", "host", "message", "created_at"}
	from := time.Now()
	to := time.Now()
	mock.ExpectQuery("SELECT").WithArgs(from, to, 10, 0).WillReturnRows(sqlmock.NewRows(columns))

	_, err = logService.FetchLogs(10, 0, from, to)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}
