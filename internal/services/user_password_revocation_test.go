package services_test

import (
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/services"
	passwordutils "github.com/loxilb-io/loxilb-oam/pkg/utils"
)

func TestPasswordUpdateRevokesSessionsAtomically(t *testing.T) {
	for _, failure := range []string{"", "update", "revoke", "commit"} {
		t.Run(failure, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			hash, err := passwordutils.HashPassword("Previous!72Secret")
			if err != nil {
				t.Fatal(err)
			}
			mock.ExpectQuery(regexp.QuoteMeta("SELECT username FROM users WHERE id = $1")).WithArgs(42).WillReturnRows(sqlmock.NewRows([]string{"username"}).AddRow("operator"))
			mock.ExpectQuery(regexp.QuoteMeta(config.SelectUserPasswordQuery)).WithArgs("operator").WillReturnRows(sqlmock.NewRows([]string{"password"}).AddRow(hash))
			mock.ExpectBegin()
			update := mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET password = $1 WHERE id = $2")).WithArgs(sqlmock.AnyArg(), 42)
			if failure == "update" {
				update.WillReturnError(errors.New("update unavailable"))
				mock.ExpectRollback()
			} else {
				update.WillReturnResult(sqlmock.NewResult(0, 1))
				revoke := mock.ExpectExec(regexp.QuoteMeta("DELETE FROM api_tokens WHERE user_id = $1")).WithArgs("42")
				if failure == "revoke" {
					revoke.WillReturnError(errors.New("token store unavailable"))
					mock.ExpectRollback()
				} else {
					revoke.WillReturnResult(sqlmock.NewResult(0, 2))
					if failure == "commit" {
						mock.ExpectCommit().WillReturnError(errors.New("commit unavailable"))
					} else {
						mock.ExpectCommit()
					}
				}
			}
			err = services.NewUserService(db).UpdateUser(42, map[string]interface{}{"password": "Changed!81Secret"})
			if (err != nil) != (failure != "") {
				t.Fatalf("failure=%q error=%v", failure, err)
			}
			if err := mock.ExpectationsWereMet(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestProfileUpdatePreservesSessions(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT username FROM users WHERE id = $1")).WithArgs(42).WillReturnRows(sqlmock.NewRows([]string{"username"}).AddRow("operator"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE users SET email = $1 WHERE id = $2")).WithArgs("new@example.invalid", 42).WillReturnResult(sqlmock.NewResult(0, 1))
	if err := services.NewUserService(db).UpdateUser(42, map[string]interface{}{"email": "new@example.invalid"}); err != nil {
		t.Fatal(err)
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}

func TestRejectedPasswordPreservesSessions(t *testing.T) {
	db, mock, err := sqlmock.New()
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery(regexp.QuoteMeta("SELECT username FROM users WHERE id = $1")).WithArgs(42).WillReturnRows(sqlmock.NewRows([]string{"username"}).AddRow("operator"))
	if err := services.NewUserService(db).UpdateUser(42, map[string]interface{}{"password": "short"}); err == nil {
		t.Fatal("expected password rejection")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
