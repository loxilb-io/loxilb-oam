package appliance

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var testKey = []byte("0123456789abcdef0123456789abcdef-test-key")

func signed(t *testing.T, method, target, body string, at time.Time) *http.Request {
	t.Helper()
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	require.NoError(t, SignRequest(r, testKey, []byte(body), at))
	return r
}

func TestVerifyRequestAcceptsWhatSignRequestProduces(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	r := signed(t, http.MethodPost, "/v1/jobs?x=1", `{"a":1}`, now)
	assert.NoError(t, VerifyRequest(r, testKey, []byte(`{"a":1}`), now.Add(5*time.Second), NewNonceCache()))
}

// Every part the MAC covers must actually be covered: changing any one of
// them, or the key, invalidates the signature.
func TestVerifyRequestRejectsTampering(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	body := `{"operation_id":"a"}`

	cases := map[string]func(r *http.Request) (*http.Request, []byte, []byte){
		"body": func(r *http.Request) (*http.Request, []byte, []byte) {
			return r, []byte(`{"operation_id":"b"}`), testKey
		},
		"method": func(r *http.Request) (*http.Request, []byte, []byte) {
			r.Method = http.MethodDelete
			return r, []byte(body), testKey
		},
		"path": func(r *http.Request) (*http.Request, []byte, []byte) {
			r.URL.Path = "/v1/jobs/other"
			return r, []byte(body), testKey
		},
		"query": func(r *http.Request) (*http.Request, []byte, []byte) {
			r.URL.RawQuery = "since_generation=0"
			return r, []byte(body), testKey
		},
		"timestamp": func(r *http.Request) (*http.Request, []byte, []byte) {
			r.Header.Set(HeaderTimestamp, "1800000001")
			return r, []byte(body), testKey
		},
		"nonce": func(r *http.Request) (*http.Request, []byte, []byte) {
			r.Header.Set(HeaderNonce, "00000000000000000000000000000000")
			return r, []byte(body), testKey
		},
		"key": func(r *http.Request) (*http.Request, []byte, []byte) {
			return r, []byte(body), []byte("another-key-another-key-another-key!")
		},
		"signature version": func(r *http.Request) (*http.Request, []byte, []byte) {
			r.Header.Set(HeaderSignature, strings.Replace(r.Header.Get(HeaderSignature), "v1=", "v0=", 1))
			return r, []byte(body), testKey
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			r, b, key := tamper(signed(t, http.MethodPost, "/v1/jobs", body, now))
			assert.ErrorIs(t, VerifyRequest(r, key, b, now, NewNonceCache()), ErrSignatureInvalid)
		})
	}
}

func TestVerifyRequestRejectsUnsigned(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/capabilities", nil)
	assert.ErrorIs(t, VerifyRequest(r, testKey, nil, time.Now(), NewNonceCache()), ErrSignatureMissing)
}

func TestVerifyRequestRejectsStaleAndFutureTimestamps(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	for _, offset := range []time.Duration{MaxClockSkew + time.Second, -(MaxClockSkew + time.Second)} {
		r := signed(t, http.MethodGet, "/v1/capabilities", "", now)
		assert.ErrorIs(t, VerifyRequest(r, testKey, nil, now.Add(offset), NewNonceCache()), ErrSignatureExpired, offset)
	}
}

// A captured request replayed inside the window is refused; the nonce is only
// forgotten once its timestamp could no longer be accepted anyway.
func TestVerifyRequestRejectsReplay(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	nonces := NewNonceCache()
	r := signed(t, http.MethodGet, "/v1/capabilities", "", now)

	require.NoError(t, VerifyRequest(r, testKey, nil, now, nonces))
	assert.ErrorIs(t, VerifyRequest(r, testKey, nil, now.Add(time.Second), nonces), ErrSignatureReplay)

	// A request with a bad signature must not consume a nonce: otherwise
	// anyone could burn nonces without holding the key.
	forged := signed(t, http.MethodGet, "/v1/capabilities", "", now)
	nonce := forged.Header.Get(HeaderNonce)
	forged.Header.Set(HeaderSignature, "v1=00")
	require.ErrorIs(t, VerifyRequest(forged, testKey, nil, now, nonces), ErrSignatureInvalid)
	assert.True(t, nonces.use(nonce, now), "nonce of a rejected request must still be unused")
}
