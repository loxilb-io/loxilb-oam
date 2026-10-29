package handlers_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	passwordUtils "github.com/loxilb-io/loxilb-oam/pkg/utils"
	"github.com/stretchr/testify/assert"
)

func TestLoginAuthenticationStoreUnavailable(t *testing.T) {
	for _, stage := range []string{"lockout lookup", "credential lookup", "failed attempt recording", "successful attempt clearing"} {
		t.Run(stage, func(t *testing.T) {
			h, mock, done := newTestHandler(t)
			defer done()
			outage := errors.New("private database failure")
			tracking := regexp.QuoteMeta(config.SelectLoginAttemptQuery)
			lookup := regexp.QuoteMeta(config.SelectUserIdQuery)
			query := mock.ExpectQuery(tracking).WithArgs("alice", "192.0.2.1")
			if stage == "lockout lookup" {
				query.WillReturnError(outage)
			} else {
				query.WillReturnRows(sqlmock.NewRows([]string{"id", "username", "client_ip", "failed_count", "last_failed_at", "blocked_until", "created_at", "updated_at"}))
				credential := mock.ExpectQuery(lookup).WithArgs("alice")
				switch stage {
				case "credential lookup":
					credential.WillReturnError(outage)
				case "failed attempt recording":
					credential.WillReturnRows(sqlmock.NewRows([]string{"id", "password"}))
					mock.ExpectQuery(tracking).WithArgs("alice", "192.0.2.1").WillReturnError(outage)
				case "successful attempt clearing":
					hash, err := passwordUtils.HashPassword("password")
					assert.NoError(t, err)
					credential.WillReturnRows(sqlmock.NewRows([]string{"id", "password"}).AddRow(1, hash))
					mock.ExpectExec(regexp.QuoteMeta(config.ClearLoginAttemptsQuery)).WithArgs("alice", "192.0.2.1").WillReturnError(outage)
				}
			}
			r := gin.New()
			r.POST("/oam/login", h.Login)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/oam/login", strings.NewReader(`{"username":"alice","password":"password"}`))
			req.RemoteAddr = "192.0.2.1:1234"
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)
			assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
			assert.Equal(t, "5", rec.Header().Get("Retry-After"))
			assert.Contains(t, rec.Body.String(), "authentication_unavailable")
			assert.NotContains(t, rec.Body.String(), "private database")
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestLoginDefinitiveCredentialFailureAndLockout(t *testing.T) {
	for _, locked := range []bool{false, true} {
		t.Run(map[bool]string{false: "bad credentials", true: "locked"}[locked], func(t *testing.T) {
			h, mock, done := newTestHandler(t)
			defer done()
			tracking := regexp.QuoteMeta(config.SelectLoginAttemptQuery)
			cols := []string{"id", "username", "client_ip", "failed_count", "last_failed_at", "blocked_until", "created_at", "updated_at"}
			rows := sqlmock.NewRows(cols)
			if locked {
				now := time.Now()
				rows.AddRow(1, "alice", "192.0.2.1", 6, now, now.Add(time.Minute), now, now)
			}
			mock.ExpectQuery(tracking).WithArgs("alice", "192.0.2.1").WillReturnRows(rows)
			if !locked {
				mock.ExpectQuery(regexp.QuoteMeta(config.SelectUserIdQuery)).WithArgs("alice").WillReturnRows(sqlmock.NewRows([]string{"id", "password"}))
				mock.ExpectQuery(tracking).WithArgs("alice", "192.0.2.1").WillReturnRows(sqlmock.NewRows(cols))
				mock.ExpectExec(regexp.QuoteMeta(config.UpsertLoginAttemptQuery)).WithArgs("alice", "192.0.2.1", sqlmock.AnyArg(), sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
			}
			r := gin.New()
			r.POST("/oam/login", h.Login)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/oam/login", strings.NewReader(`{"username":"alice","password":"wrong"}`))
			req.RemoteAddr = "192.0.2.1:1234"
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)
			if locked {
				assert.Equal(t, 429, rec.Code)
				assert.NotEmpty(t, rec.Header().Get("Retry-After"))
			} else {
				assert.Equal(t, 401, rec.Code)
				assert.Contains(t, rec.Body.String(), "Invalid username or password")
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
