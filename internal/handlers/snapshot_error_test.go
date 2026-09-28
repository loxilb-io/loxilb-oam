package handlers

import (
	"errors"
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
