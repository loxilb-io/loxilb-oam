package services

import (
	"errors"
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestAcknowledgementUpdateFailureRollsBack(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	failure := errors.New("fixture update failure")
	mock.ExpectBegin()
	mock.ExpectExec("INSERT INTO acknowledgments").WithArgs(7, 3, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectExec("UPDATE alerts").WithArgs(sqlmock.AnyArg(), 7).WillReturnError(failure)
	mock.ExpectRollback()
	_, err = NewAlertService(db).AcknowledgeAlert(7, 3)
	require.ErrorIs(t, err, failure)
	require.NoError(t, mock.ExpectationsWereMet())
}
