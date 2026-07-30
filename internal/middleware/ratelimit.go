package middleware

import (
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

// RateLimiter is a simple in-memory token-bucket rate limiter keyed by client
// IP. Each key gets `burst` tokens refilled at `rps` tokens/second; a
// request consumes one token. Buckets idle long enough to be full again are
// garbage-collected periodically. Single-process only — sufficient for OAM,
// which runs as one instance.
type RateLimiter struct {
	mu      sync.Mutex
	buckets map[string]*tokenBucket
	rps     float64
	burst   float64
	lastGC  time.Time
}

type tokenBucket struct {
	tokens float64
	last   time.Time
}

const rateLimiterGCInterval = 10 * time.Minute

// NewRateLimiter creates a limiter allowing `rps` sustained requests/second
// with bursts up to `burst` per key.
func NewRateLimiter(rps float64, burst int) *RateLimiter {
	return &RateLimiter{
		buckets: make(map[string]*tokenBucket),
		rps:     rps,
		burst:   float64(burst),
		lastGC:  time.Now(),
	}
}

// Allow reports whether a request for key may proceed, consuming a token if so.
func (rl *RateLimiter) Allow(key string) bool {
	now := time.Now()

	rl.mu.Lock()
	defer rl.mu.Unlock()

	if now.Sub(rl.lastGC) > rateLimiterGCInterval {
		rl.gcLocked(now)
	}

	b, ok := rl.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: rl.burst, last: now}
		rl.buckets[key] = b
	} else {
		b.tokens += now.Sub(b.last).Seconds() * rl.rps
		if b.tokens > rl.burst {
			b.tokens = rl.burst
		}
		b.last = now
	}

	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// gcLocked drops buckets that have fully refilled (idle keys). Callers hold mu.
func (rl *RateLimiter) gcLocked(now time.Time) {
	for key, b := range rl.buckets {
		if b.tokens+now.Sub(b.last).Seconds()*rl.rps >= rl.burst {
			delete(rl.buckets, key)
		}
	}
	rl.lastGC = now
}

// RateLimit returns a gin middleware enforcing the limiter per client IP.
func RateLimit(rl *RateLimiter) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !rl.Allow(c.ClientIP()) {
			c.Header("Retry-After", "1")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error": "Too many requests. Please slow down.",
			})
			return
		}
		c.Next()
	}
}
