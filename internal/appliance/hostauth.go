package appliance

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Requests between OAM and the host adapter are authenticated twice: by the
// permissions on the socket file, and by an HMAC over the request with a key
// both sides read from a file. The socket alone would let any process that
// can open it issue commands; the MAC alone would be fine but costs nothing
// to combine.
//
// The MAC covers method, path (with query), a timestamp, a nonce and the body
// hash. The timestamp bounds how long a captured request is useful and the
// nonce makes it single-use inside that bound.
const (
	HeaderSignature = "X-Appliance-Signature"
	HeaderTimestamp = "X-Appliance-Timestamp"
	HeaderNonce     = "X-Appliance-Nonce"

	signatureVersion = "v1"
	// MaxClockSkew is how far a request's timestamp may be from the
	// verifier's clock. Both ends are on the same host, so this only has to
	// absorb scheduling delay.
	MaxClockSkew = 60 * time.Second
	// MinKeyBytes is the shortest key accepted.
	MinKeyBytes = 32
)

var (
	ErrSignatureMissing = errors.New("request is not signed")
	ErrSignatureInvalid = errors.New("request signature is invalid")
	ErrSignatureExpired = errors.New("request timestamp is outside the accepted window")
	ErrSignatureReplay  = errors.New("request nonce was already used")
)

func signingInput(method, requestURI, timestamp, nonce string, body []byte) []byte {
	sum := sha256.Sum256(body)
	return []byte(strings.Join([]string{method, requestURI, timestamp, nonce, hex.EncodeToString(sum[:])}, "\n"))
}

func mac(key, input []byte) string {
	h := hmac.New(sha256.New, key)
	h.Write(input)
	return hex.EncodeToString(h.Sum(nil))
}

// SignRequest adds the authentication headers to r. body must be exactly the
// bytes that will be sent.
func SignRequest(r *http.Request, key, body []byte, now time.Time) error {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate nonce: %w", err)
	}
	timestamp := strconv.FormatInt(now.Unix(), 10)
	nonceHex := hex.EncodeToString(nonce)
	r.Header.Set(HeaderTimestamp, timestamp)
	r.Header.Set(HeaderNonce, nonceHex)
	r.Header.Set(HeaderSignature, signatureVersion+"="+mac(key, signingInput(r.Method, r.URL.RequestURI(), timestamp, nonceHex, body)))
	return nil
}

// NonceCache remembers nonces for as long as their timestamp could still be
// accepted, so a captured request cannot be replayed inside the window.
type NonceCache struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

// NewNonceCache returns an empty cache.
func NewNonceCache() *NonceCache { return &NonceCache{seen: map[string]time.Time{}} }

// use records nonce and reports whether it was new.
func (c *NonceCache) use(nonce string, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for n, at := range c.seen {
		if now.Sub(at) > 2*MaxClockSkew {
			delete(c.seen, n)
		}
	}
	if _, dup := c.seen[nonce]; dup {
		return false
	}
	c.seen[nonce] = now
	return true
}

// VerifyRequest checks the authentication headers of r against key and body.
// The signature is checked before the nonce is recorded, so an attacker
// without the key cannot fill the cache.
func VerifyRequest(r *http.Request, key, body []byte, now time.Time, nonces *NonceCache) error {
	signature := r.Header.Get(HeaderSignature)
	timestamp := r.Header.Get(HeaderTimestamp)
	nonce := r.Header.Get(HeaderNonce)
	if signature == "" || timestamp == "" || nonce == "" {
		return ErrSignatureMissing
	}
	version, got, ok := strings.Cut(signature, "=")
	if !ok || version != signatureVersion {
		return ErrSignatureInvalid
	}
	want := mac(key, signingInput(r.Method, r.URL.RequestURI(), timestamp, nonce, body))
	if !hmac.Equal([]byte(got), []byte(want)) {
		return ErrSignatureInvalid
	}
	seconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrSignatureInvalid
	}
	if skew := now.Sub(time.Unix(seconds, 0)); skew > MaxClockSkew || skew < -MaxClockSkew {
		return ErrSignatureExpired
	}
	if !nonces.use(nonce, now) {
		return ErrSignatureReplay
	}
	return nil
}
