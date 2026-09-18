package services

import (
	"database/sql/driver"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/gin-gonic/gin"
	"github.com/loxilb-io/loxilb-oam/internal/config"
	"github.com/loxilb-io/loxilb-oam/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func mustGatewayIdentity(t *testing.T, mode, token string) GatewayServiceIdentity {
	t.Helper()
	identity, err := NewGatewayServiceIdentity(mode, token)
	require.NoError(t, err)
	return identity
}

func TestGatewayServiceIdentityRequiresTokenInAuthenticatedMode(t *testing.T) {
	_, err := NewGatewayServiceIdentity(GatewayAuthModeServiceToken, "")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrGatewayServiceIdentityUnavailable)
	assert.NotContains(t, err.Error(), "Bearer ")
}

func TestGatewayClientsFailConstructionWhenAuthenticatedModeHasNoToken(t *testing.T) {
	t.Setenv(GatewayAuthModeEnv, GatewayAuthModeServiceToken)
	t.Setenv(GatewayServiceTokenEnv, "")
	t.Setenv(GatewayServiceTokenFileEnv, "")

	proxy, err := NewProxyService(nil)
	require.Error(t, err)
	assert.Nil(t, proxy)
	assert.ErrorIs(t, err, ErrGatewayServiceIdentityUnavailable)

	snapshot, err := NewSnapshotService(nil, nil)
	require.Error(t, err)
	assert.Nil(t, snapshot)
	assert.ErrorIs(t, err, ErrGatewayServiceIdentityUnavailable)
}

func TestGatewayServiceIdentityRejectsImplicitOrAmbiguousCredential(t *testing.T) {
	_, err := NewGatewayServiceIdentity(GatewayAuthModeDisabled, "unused-secret")
	require.Error(t, err)
	assert.Contains(t, err.Error(), GatewayAuthModeEnv)

	_, err = NewGatewayServiceIdentity(GatewayAuthModeServiceToken, "Bearer ambiguous")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "raw token")
}

func TestGatewayServiceIdentityFromEnvDefaultsToExplicitDisabledMode(t *testing.T) {
	t.Setenv(GatewayAuthModeEnv, "")
	t.Setenv(GatewayServiceTokenEnv, "")
	t.Setenv(GatewayServiceTokenFileEnv, "")
	identity, err := GatewayServiceIdentityFromEnv()
	require.NoError(t, err)
	assert.Equal(t, GatewayAuthModeDisabled, identity.Mode())
}

func TestGatewayServiceIdentityFromFile(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "gateway-service-token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("file-service-token\n"), 0o600))
	t.Setenv(GatewayAuthModeEnv, GatewayAuthModeServiceToken)
	t.Setenv(GatewayServiceTokenEnv, "")
	t.Setenv(GatewayServiceTokenFileEnv, tokenFile)

	identity, err := GatewayServiceIdentityFromEnv()
	require.NoError(t, err)
	req := httptest.NewRequest(http.MethodGet, "http://gateway.test/netlox/v1/meta", nil)
	require.NoError(t, identity.Authorize(req))
	assert.Equal(t, "Bearer file-service-token", req.Header.Get("Authorization"))
}

func TestGatewayServiceIdentityRejectsAmbiguousOrUnsafeFileSource(t *testing.T) {
	tokenFile := filepath.Join(t.TempDir(), "gateway-service-token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("file-service-token\n"), 0o600))

	_, err := gatewayServiceIdentityFromSources(GatewayAuthModeServiceToken, "env-service-token", tokenFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "exactly one")
	assert.NotContains(t, err.Error(), "env-service-token")

	_, err = gatewayServiceIdentityFromSources(GatewayAuthModeDisabled, "", tokenFile)
	require.Error(t, err)
	assert.Contains(t, err.Error(), GatewayServiceTokenFileEnv)

	_, err = gatewayServiceIdentityFromSources(GatewayAuthModeServiceToken, "", "relative/token")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "absolute")

	linked := filepath.Join(t.TempDir(), "linked-token")
	require.NoError(t, os.Symlink(tokenFile, linked))
	_, err = gatewayServiceIdentityFromSources(GatewayAuthModeServiceToken, "", linked)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "symlink")
}

func TestGatewayServiceIdentityRejectsInvalidFileContents(t *testing.T) {
	for name, contents := range map[string]string{
		"empty":       "\n",
		"multiline":   "first-line\nsecond-line\n",
		"bearer-form": "Bearer already-prefixed\n",
	} {
		t.Run(name, func(t *testing.T) {
			tokenFile := filepath.Join(t.TempDir(), "gateway-service-token")
			require.NoError(t, os.WriteFile(tokenFile, []byte(contents), 0o600))
			_, err := gatewayServiceIdentityFromSources(GatewayAuthModeServiceToken, "", tokenFile)
			require.Error(t, err)
		})
	}
}

func proxyInstanceRow(endpoint string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "name", "host", "port", "protocol", "description", "version", "api_endpoint", "cimage", "ctag", "is_active", "created_at",
	}).AddRow(
		1, "gateway", "127.0.0.1", "11111", "http", "test", "v1", endpoint,
		"loxilb", "test", true, time.Unix(0, 0).UTC(),
	)
}

func expectProxyInstance(mock sqlmock.Sqlmock, endpoint string) {
	mock.ExpectQuery(regexp.QuoteMeta(config.SelectLoxiLBInstanceByIDQuery)).
		WithArgs(driver.Value(1)).
		WillReturnRows(proxyInstanceRow(endpoint))
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func forwardProxyRequest(t *testing.T, identity GatewayServiceIdentity, endpoint string, headers http.Header, capture func(*http.Request)) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectProxyInstance(mock, endpoint)

	proxy := NewProxyServiceWithGatewayIdentity(NewLoxiLBService(db), identity)
	proxy.client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		capture(req)
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"Content-Type": []string{"application/json"}},
			Body:       io.NopCloser(strings.NewReader(`{"ok":true}`)),
			Request:    req,
		}, nil
	})}
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(recorder)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/oam/loxilbs/1/netlox/v1/meta", nil)
	ctx.Request.Header = headers.Clone()

	require.NoError(t, proxy.ForwardRequest(ctx, 1, "/v1/meta"))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestProxyStripsBrowserCredentialsAndInjectsServiceToken(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer browser-user-jwt")
	headers.Set("Cookie", "oam_session=browser-secret")
	headers.Set("X-Api-Key", "data-plane-secret")
	headers.Set("X-Request-ID", "req-123")
	headers.Set("X-Correlation-ID", "corr-456")
	headers.Set("Content-Type", "application/json")

	identity := mustGatewayIdentity(t, GatewayAuthModeServiceToken, "oam-service-token")
	var outbound http.Header
	forwardProxyRequest(t, identity, "http://gateway.test/netlox/v1", headers, func(req *http.Request) {
		outbound = req.Header.Clone()
	})

	assert.Equal(t, "Bearer oam-service-token", outbound.Get("Authorization"))
	assert.NotContains(t, outbound.Get("Authorization"), "browser-user-jwt")
	assert.Empty(t, outbound.Get("Cookie"))
	assert.Empty(t, outbound.Get("X-Api-Key"))
	assert.Equal(t, "req-123", outbound.Get("X-Request-ID"))
	assert.Equal(t, "corr-456", outbound.Get("X-Correlation-ID"))
}

func TestProxyAuthDisabledStillStripsBrowserAuthorization(t *testing.T) {
	headers := make(http.Header)
	headers.Set("Authorization", "Bearer browser-user-jwt")
	identity := mustGatewayIdentity(t, GatewayAuthModeDisabled, "")
	var outbound http.Header
	forwardProxyRequest(t, identity, "http://gateway.test/netlox/v1", headers, func(req *http.Request) {
		outbound = req.Header.Clone()
	})

	assert.Empty(t, outbound.Get("Authorization"))
}

func TestSnapshotGatewayClientUsesServiceIdentityForFetchAndRestore(t *testing.T) {
	type observedRequest struct {
		method        string
		path          string
		authorization string
		contentType   string
	}
	identity := mustGatewayIdentity(t, GatewayAuthModeServiceToken, "snapshot-service-token")
	client := newHTTPGatewayClient(identity)
	requests := make([]observedRequest, 0, 2)
	transport := roundTripFunc(func(req *http.Request) (*http.Response, error) {
		requests = append(requests, observedRequest{req.Method, req.URL.RequestURI(), req.Header.Get("Authorization"), req.Header.Get("Content-Type")})
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     http.Header{"X-Snapshot-Checksum": []string{"sha256:test"}},
			Body:       io.NopCloser(strings.NewReader(`{"kind":"loxilb-snapshot"}`)),
			Request:    req,
		}, nil
	})
	client.take = &http.Client{Transport: transport}
	client.restore = &http.Client{Transport: transport}
	instance := &models.LoxiLBInstance{ApiEndpoint: "http://gateway.test"}

	_, _, err := client.FetchSnapshot(instance)
	require.NoError(t, err)
	_, _, err = client.Restore(instance, []byte(`{"kind":"loxilb-snapshot"}`), RestoreModeDryRun)
	require.NoError(t, err)

	require.Len(t, requests, 2)
	fetch := requests[0]
	restore := requests[1]
	assert.Equal(t, observedRequest{http.MethodGet, "/config/snapshot", "Bearer snapshot-service-token", ""}, fetch)
	assert.Equal(t, observedRequest{http.MethodPost, "/config/restore?mode=dry-run", "Bearer snapshot-service-token", "application/json"}, restore)
}

func TestGatewayServiceIdentityAuthorizeFailsClosedForInvalidValue(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://gateway.test/netlox/v1/meta", nil)
	req.Header.Set("Authorization", "Bearer browser-user-jwt")
	identity := GatewayServiceIdentity{mode: GatewayAuthModeServiceToken}

	err := identity.Authorize(req)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrGatewayServiceIdentityUnavailable))
	assert.Empty(t, req.Header.Get("Authorization"))
}
