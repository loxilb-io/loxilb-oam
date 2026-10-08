package handlers_test

import (
	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/loxilb-io/loxilb-oam/internal/middleware"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/stretchr/testify/assert"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAlertAcknowledgementCaller(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		caller     bool
	}{
		{"forged actor", `{"user_id":1}`, http.StatusForbidden, true},
		{"missing caller", `{"user_id":3}`, http.StatusUnauthorized, false},
		{"authenticated actor", `{"user_id":3}`, http.StatusOK, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, mock, done := newTestHandler(t)
			defer done()
			if tc.code == http.StatusOK {
				mock.ExpectBegin()
				mock.ExpectExec("INSERT INTO acknowledgments").WithArgs(7, 3, sqlmock.AnyArg()).WillReturnResult(sqlmock.NewResult(1, 1))
				mock.ExpectExec("UPDATE alerts").WithArgs(sqlmock.AnyArg(), 7).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				if tc.caller {
					c.Set(middleware.CtxCallerUser, &models.User{ID: 3, Username: "operator", Role: "operator"})
				}
			})
			r.PUT("/alerts/:id/acknowledge", h.AcknowledgeAlert)
			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPut, "/alerts/7/acknowledge", strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			r.ServeHTTP(rec, req)
			assert.Equal(t, tc.code, rec.Code, rec.Body.String())
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAlertValidationIsClientError(t *testing.T) {
	for _, body := range []string{
		`{"instance_id":1,"type":"invalid","severity":"INFO","message":"fixture"}`,
		`{"instance_id":1,"type":"HIGH_CPU","severity":"invalid","message":"fixture"}`,
	} {
		h, mock, done := newTestHandler(t)
		r := gin.New()
		r.POST("/alerts", h.CreateAlert)
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/alerts", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.NoError(t, mock.ExpectationsWereMet())
		done()
	}
}
