package handlers_test

import (
	"database/sql"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/loxilb-io/loxilb-oam/internal/handlers"
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

// expectUniquenessChecks queues the name + endpoint pre-checks every
// create/update now runs before it writes.
func expectUniquenessChecks(mock sqlmock.Sqlmock, nameTaken, endpointTaken bool) {
	count := func(taken bool) *sqlmock.Rows {
		value := 0
		if taken {
			value = 1
		}
		return sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(value)
	}
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM loxilb_instances WHERE LOWER\\(name\\)").WillReturnRows(count(nameTaken))
	if nameTaken {
		return // the handler answers 409 and never reaches the endpoint check
	}
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM loxilb_instances WHERE LOWER\\(api_endpoint\\)").WillReturnRows(count(endpointTaken))
}

func postInstance(t *testing.T, h *handlers.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.POST("/oam/loxilbs", h.CreateLoxiLBInstance)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/oam/loxilbs", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

func putInstance(t *testing.T, h *handlers.Handler, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := gin.New()
	r.PUT("/oam/loxilbs/:id", h.UpdateLoxiLBInstance)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPut, "/oam/loxilbs/"+id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	return rec
}

const validInstanceBody = `{"name":"edge","host":"10.0.0.1","port":"11111","protocol":"https","version":"v1","cimage":"ghcr.io/loxilb-io/loxilb","ctag":"latest"}`

func TestCreateLoxiLBInstanceOK(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	expectUniquenessChecks(mock, false, false)
	mock.ExpectExec("INSERT INTO loxilb_instances").
		WillReturnResult(sqlmock.NewResult(42, 1))

	rec := postInstance(t, h, validInstanceBody)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), "edge")
	assert.Contains(t, rec.Body.String(), "https://10.0.0.1:11111/netlox/v1")
	assert.NoError(t, mock.ExpectationsWereMet())
}

// Every field lands in the endpoint URL, so each of these would otherwise
// register a target that is malformed or simply not the intended one.
func TestCreateLoxiLBInstanceRejectsInvalidFields(t *testing.T) {
	cases := []struct {
		name  string
		body  string
		field string
	}{
		{"name with a space", `{"name":"edge gw","host":"10.0.0.1","port":"11111","protocol":"https","cimage":"loxilb","ctag":"latest"}`, "name"},
		{"host carrying a scheme", `{"name":"edge","host":"https://10.0.0.1","port":"11111","protocol":"https","cimage":"loxilb","ctag":"latest"}`, "host"},
		{"host carrying a path", `{"name":"edge","host":"10.0.0.1/netlox","port":"11111","protocol":"https","cimage":"loxilb","ctag":"latest"}`, "host"},
		{"unbracketed IPv6", `{"name":"edge","host":"2001:db8::1","port":"11111","protocol":"https","cimage":"loxilb","ctag":"latest"}`, "host"},
		{"mistyped IPv4", `{"name":"edge","host":"192.0.2.999","port":"11111","protocol":"https","cimage":"loxilb","ctag":"latest"}`, "host"},
		{"port out of range", `{"name":"edge","host":"10.0.0.1","port":"70000","protocol":"https","cimage":"loxilb","ctag":"latest"}`, "port"},
		{"non-numeric port", `{"name":"edge","host":"10.0.0.1","port":"http","protocol":"https","cimage":"loxilb","ctag":"latest"}`, "port"},
		{"unsupported protocol", `{"name":"edge","host":"10.0.0.1","port":"11111","protocol":"ftp","cimage":"loxilb","ctag":"latest"}`, "protocol"},
		{"version traversal", `{"name":"edge","host":"10.0.0.1","port":"11111","protocol":"https","version":"../../config","cimage":"loxilb","ctag":"latest"}`, "version"},
		{"tag inside the image", `{"name":"edge","host":"10.0.0.1","port":"11111","protocol":"https","cimage":"loxilb:latest","ctag":"latest"}`, "cimage"},
		{"malformed tag", `{"name":"edge","host":"10.0.0.1","port":"11111","protocol":"https","cimage":"loxilb","ctag":"-latest"}`, "ctag"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, mock, done := newTestHandler(t)
			defer done()

			rec := postInstance(t, h, tc.body)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
			assert.Contains(t, rec.Body.String(), `"field":"`+tc.field+`"`)
			// Nothing may reach the database on a rejected payload.
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestCreateLoxiLBInstanceDuplicateNameIsConflict(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	expectUniquenessChecks(mock, true, false)

	rec := postInstance(t, h, validInstanceBody)

	// The UI resolves ?name=… by exact match, so a duplicate name would make
	// one of the two instances unreachable — 409, not a silent second row.
	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "name already exists")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLoxiLBInstanceDuplicateEndpointIsConflict(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	expectUniquenessChecks(mock, false, true)

	rec := postInstance(t, h, validInstanceBody)

	assert.Equal(t, http.StatusConflict, rec.Code)
	assert.Contains(t, rec.Body.String(), "https://10.0.0.1:11111/netlox/v1")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLoxiLBInstanceDefaultsVersionAndNormalizes(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	expectUniquenessChecks(mock, false, false)
	mock.ExpectExec("INSERT INTO loxilb_instances").WillReturnResult(sqlmock.NewResult(7, 1))

	// version has never been a required field — it still defaults to v1, and
	// surrounding whitespace / protocol casing are normalized rather than
	// rejected, so existing API clients keep working.
	rec := postInstance(t, h, `{"name":" edge ","host":" 10.0.0.1 ","port":"11111","protocol":"HTTPS","cimage":"loxilb","ctag":"latest"}`)

	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), "https://10.0.0.1:11111/netlox/v1")
	assert.Contains(t, rec.Body.String(), `"name":"edge"`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLoxiLBInstanceStillRequiresProtocol(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	// protocol carries binding:"required" — the proxy target's scheme is not
	// something to guess at. Rejected before any DB call.
	rec := postInstance(t, h, `{"name":"edge","host":"10.0.0.1","port":"11111","cimage":"loxilb","ctag":"latest"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestCreateLoxiLBInstanceDoesNotLeakSQLErrors(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	expectUniquenessChecks(mock, false, false)
	mock.ExpectExec("INSERT INTO loxilb_instances").WillReturnError(errors.New("Error 1146: Table 'oam.loxilb_instances' doesn't exist"))

	rec := postInstance(t, h, validInstanceBody)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.NotContains(t, rec.Body.String(), "1146")
	assert.NotContains(t, rec.Body.String(), "oam.loxilb_instances")
}

// PUT rewrites every column, so a partial body used to blank the row and an
// empty protocol produced the endpoint '://host:port/netlox/'.
func TestUpdateLoxiLBInstanceRejectsPartialBody(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	rec := putInstance(t, h, "5", `{"name":"edge"}`)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), `"field":"host"`)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateLoxiLBInstanceOK(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	expectUniquenessChecks(mock, false, false)
	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances WHERE id = \\?").WillReturnRows(instanceRows())
	mock.ExpectExec("UPDATE loxilb_instances SET").WillReturnResult(sqlmock.NewResult(0, 1))

	rec := putInstance(t, h, "5", `{"name":"edge","host":"10.0.0.1","port":"11111","protocol":"http","version":"v1","cimage":"loxilb","ctag":"latest"}`)

	assert.Equal(t, http.StatusOK, rec.Code)
	// The endpoint is re-derived from the submitted protocol — an http
	// instance must stay http.
	assert.Contains(t, rec.Body.String(), "http://10.0.0.1:11111/netlox/v1")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateLoxiLBInstanceMissingRowIs404(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	expectUniquenessChecks(mock, false, false)
	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances WHERE id = \\?").WillReturnError(sql.ErrNoRows)

	rec := putInstance(t, h, "404", validInstanceBody)

	assert.Equal(t, http.StatusNotFound, rec.Code)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestUpdateLoxiLBInstanceConflictWithAnotherInstance(t *testing.T) {
	h, mock, done := newTestHandler(t)
	defer done()

	expectUniquenessChecks(mock, false, true)

	rec := putInstance(t, h, "5", validInstanceBody)

	assert.Equal(t, http.StatusConflict, rec.Code)
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
