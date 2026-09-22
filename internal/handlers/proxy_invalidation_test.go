package handlers

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/loxilb-io/loxilb-oam/internal/services"
)

// countingListener counts accepted TCP connections, which is how many times a
// client had to dial. It is the only vantage point from which connection reuse
// is observable without reaching into the proxy's unexported client.
type countingListener struct {
	net.Listener
	mu      sync.Mutex
	accepts int
}

func (l *countingListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err == nil {
		l.mu.Lock()
		l.accepts++
		l.mu.Unlock()
	}
	return conn, err
}

func (l *countingListener) count() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.accepts
}

// The protection that justifies enabling connection reuse is the invalidation
// on the instance mutation paths. Nothing else in the suite would notice if
// those calls were dropped, so assert the behaviour they exist to provide:
// after an instance is updated, the next proxied request must dial afresh
// rather than reuse a connection to what may now be a different endpoint.
func TestInstanceUpdateInvalidatesPooledProxyConnections(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	listener := &countingListener{Listener: server.Listener}
	server.Listener = listener
	server.Start()
	defer server.Close()

	endpoint := server.URL + "/netlox/v1"

	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	instanceRow := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{
			"id", "name", "host", "port", "protocol", "description",
			"version", "api_endpoint", "cimage", "ctag", "is_active", "created_at",
		}).AddRow(1, "gw", "127.0.0.1", "11111", "http", "", "v1", endpoint,
			"loxilb", "latest", true, time.Unix(0, 0).UTC())
	}
	countRow := func() *sqlmock.Rows {
		return sqlmock.NewRows([]string{"COUNT(*)"}).AddRow(0)
	}

	// Two proxied reads, then the update, then a third proxied read.
	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances WHERE id = \\$1").WillReturnRows(instanceRow())
	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances WHERE id = \\$1").WillReturnRows(instanceRow())
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM loxilb_instances WHERE LOWER\\(name\\)").WillReturnRows(countRow())
	mock.ExpectQuery("SELECT COUNT\\(\\*\\) FROM loxilb_instances WHERE LOWER\\(api_endpoint\\)").WillReturnRows(countRow())
	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances WHERE id = \\$1").WillReturnRows(instanceRow())
	mock.ExpectExec("UPDATE loxilb_instances SET").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT (.+) FROM loxilb_instances WHERE id = \\$1").WillReturnRows(instanceRow())

	identity, err := services.NewGatewayServiceIdentity(services.GatewayAuthModeDisabled, "")
	require.NoError(t, err)
	loxilbService := services.NewLoxiLBService(db)
	proxy := services.NewProxyServiceWithGatewayIdentity(loxilbService, identity)
	h := NewHandler(nil, loxilbService, nil, nil, proxy, nil, 60)

	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.GET("/oam/loxilbs/:id/netlox/*path", h.ProxyToLoxiLB)
	router.PUT("/oam/loxilbs/:id", h.UpdateLoxiLBInstance)

	proxyGet := func() {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/oam/loxilbs/1/netlox/v1/config/meta", nil))
		require.Equal(t, http.StatusOK, rec.Code)
	}

	proxyGet()
	require.Equal(t, 1, listener.count(), "the first request dials")
	proxyGet()
	require.Equal(t, 1, listener.count(), "the second request must reuse the pooled connection")

	rec := httptest.NewRecorder()
	body := `{"name":"gw","host":"127.0.0.1","port":"11111","protocol":"http","version":"v1","cimage":"loxilb","ctag":"latest"}`
	req := httptest.NewRequest(http.MethodPut, "/oam/loxilbs/1", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code)

	proxyGet()
	assert.Equal(t, 2, listener.count(),
		"the update must have dropped the pool, forcing a fresh dial")
	assert.NoError(t, mock.ExpectationsWereMet())
}
