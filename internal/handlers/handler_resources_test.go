package handlers_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

// instanceRows returns a stub result set matching the column order the loxilb
// service scans for instance reads.
func instanceRows() *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "name", "host", "port", "protocol", "description", "version",
		"api_endpoint", "cimage", "ctag", "is_active", "created_at",
	}).AddRow(1, "inst1", "localhost", "8080", "https", "d1", "v1",
		"https://localhost:8080/netlox/v1", "ghcr.io/loxilb-io/loxilb", "v0.9.7", true,
		time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC))
}

func TestGetLoxiLBInstancesOK(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances").WillReturnRows(instanceRows())

	r := gin.New()
	r.GET("/oam/loxilbs", h.GetLoxiLBInstances)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/loxilbs", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "inst1")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLoxiLBInstancesDBError(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances").WillReturnError(sqlmock.ErrCancelled)

	r := gin.New()
	r.GET("/oam/loxilbs", h.GetLoxiLBInstances)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/loxilbs", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLoxiLBInstanceByIDOK(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances WHERE id = \\?").
		WithArgs(1).WillReturnRows(instanceRows())

	r := gin.New()
	r.GET("/oam/loxilbs/:id", h.GetLoxiLBInstanceByID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/loxilbs/1", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "inst1")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestGetLoxiLBInstanceByIDBadID(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.GET("/oam/loxilbs/:id", h.GetLoxiLBInstanceByID)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/loxilbs/abc", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateLoxiLBInstanceBadJSON(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.POST("/oam/loxilbs", h.CreateLoxiLBInstance)

	// Missing required fields (host/port/protocol/cimage/ctag) → binding 400.
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oam/loxilbs", strings.NewReader(`{"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateLoxiLBInstanceOK(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	mock.ExpectExec("INSERT INTO loxilb_instances").
		WillReturnResult(sqlmock.NewResult(42, 1))

	r := gin.New()
	r.POST("/oam/loxilbs", h.CreateLoxiLBInstance)

	body := `{"name":"edge","host":"10.0.0.1","port":"11111","protocol":"https","cimage":"ghcr.io/loxilb-io/loxilb","ctag":"latest"}`
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oam/loxilbs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), "edge")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteLoxiLBInstanceOK(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	mock.ExpectExec("DELETE FROM loxilb_instances WHERE id = \\?").
		WithArgs(5).WillReturnResult(sqlmock.NewResult(0, 1))

	r := gin.New()
	r.DELETE("/oam/loxilbs/:id", h.DeleteLoxiLBInstance)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/oam/loxilbs/5", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "deleted")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestDeleteLoxiLBInstanceBadID(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.DELETE("/oam/loxilbs/:id", h.DeleteLoxiLBInstance)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/oam/loxilbs/xyz", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestUpdateLoxiLBInstanceBadID(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.PUT("/oam/loxilbs/:id", h.UpdateLoxiLBInstance)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/oam/loxilbs/nan", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreateAlertBadJSON(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.POST("/oam/alerts", h.CreateAlert)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oam/alerts", strings.NewReader(`{"type":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetActiveAlertsBadPage(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.GET("/oam/alerts", h.GetActiveAlerts)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/alerts?page=0", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetActiveAlertsBadLimit(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.GET("/oam/alerts", h.GetActiveAlerts)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/oam/alerts?limit=abc", nil)
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestAcknowledgeAlertBadID(t *testing.T) {
	h, _, done := newTestHandler(t)
	defer done()

	r := gin.New()
	r.PUT("/oam/alerts/:id/acknowledge", h.AcknowledgeAlert)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/oam/alerts/notanid/acknowledge", strings.NewReader(`{"user_id":1}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}
