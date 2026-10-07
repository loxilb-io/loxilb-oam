package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/loxilb-io/loxilb-oam/internal/services"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func writeSnapshotErrorFor(err error) *httptest.ResponseRecorder {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	writeSnapshotError(ctx, err)
	return recorder
}

// Taking a snapshot while the Gateway refuses OAM's management credential
// relays the Gateway's 401 on an OAM route. Unmarked, the console read it as
// the operator's own session ending and signed them out.
func TestSnapshotErrorMarksRelayedGatewayStatus(t *testing.T) {
	for _, body := range []string{`{"code":401,"message":"Missing or invalid credentials"}`, "unauthorized"} {
		recorder := writeSnapshotErrorFor(&services.GatewayError{StatusCode: http.StatusUnauthorized, Body: body})
		assert.Equal(t, http.StatusUnauthorized, recorder.Code, "the Gateway's status is still relayed verbatim")
		assert.Equal(t, services.ErrorOriginGateway, recorder.Header().Get(services.ErrorOriginHeader))
	}
}

func TestDecodeRestoreRequest(t *testing.T) {
	req, err := decodeRestoreRequest([]byte(`{"mode":"commit","target_instance_id":2}`))
	assert.NoError(t, err)
	assert.Nil(t, req.Components, "no selection: the whole document")
	assert.Equal(t, "commit", req.Mode)

	req, err = decodeRestoreRequest([]byte(`{"components":["cert","auditsink"]}`))
	assert.NoError(t, err)
	assert.Equal(t, []string{"cert", "auditsink"}, req.Components)

	// Present but empty reaches the service as an empty selection, which the
	// service refuses; it must not arrive there as "no selection".
	req, err = decodeRestoreRequest([]byte(`{"components":[]}`))
	assert.NoError(t, err)
	assert.NotNil(t, req.Components)
	assert.Empty(t, req.Components)

	for _, body := range []string{`{"components":null}`, `{"components":"auditsink"}`, `{"components":{}}`, `{"mode":`, `[]`} {
		_, err := decodeRestoreRequest([]byte(body))
		assert.Error(t, err, body)
	}
}

// What OAM refuses or cannot do on the restore path, as distinct statuses.
func TestSnapshotErrorStatuses(t *testing.T) {
	for want, err := range map[int]error{
		http.StatusBadRequest:          fmt.Errorf("%w: components must name at least one domain", services.ErrInvalidRestoreRequest),
		http.StatusUnprocessableEntity: services.ErrSnapshotCorrupted,
		http.StatusServiceUnavailable:  fmt.Errorf("authorizing: %w", services.ErrGatewayServiceIdentityUnavailable),
		http.StatusBadGateway:          &services.GatewayError{Body: "connection refused"},
	} {
		assert.Equal(t, want, writeSnapshotErrorFor(err).Code, "%v", err)
	}
	// The same condition answers 503 on the instance proxy.
	status, _, _ := classifyProxyError(services.ErrGatewayServiceIdentityUnavailable)
	assert.Equal(t, http.StatusServiceUnavailable, status)
}

// OAM's own failures must stay unmarked: marking them "gateway" would keep a
// console signed in on a session OAM itself has ended.
func TestSnapshotErrorLeavesOAMFailuresUnmarked(t *testing.T) {
	for _, err := range []error{
		services.ErrSnapshotNotFound,
		services.ErrSnapshotPinned,
		errors.New("database is down"),
		&services.GatewayError{Body: "connection refused"},
	} {
		recorder := writeSnapshotErrorFor(err)
		assert.Empty(t, recorder.Header().Get(services.ErrorOriginHeader), "%v", err)
	}
}
