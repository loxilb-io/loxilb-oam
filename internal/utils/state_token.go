package utils

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"github.com/loxilb-io/loxilb-oam/internal/config"
)

var (
	stateTokens      = make(map[string]time.Time)
	stateTokensMutex sync.Mutex
)

// GenerateStateToken generates a secure state token and stores it with an expiration time.
// GenerateStateToken generates a random state token, encodes it in URL-safe
// base64, and stores it with an expiration time. The state token is used to
// prevent CSRF attacks during OAuth authentication flows. It returns the
// generated state token as a string. If there is an error generating the
// random bytes, the function will panic.
func GenerateStateToken() string {
	b := make([]byte, 32)
	_, err := rand.Read(b)
	if err != nil {
		panic(err) // This should never happen
	}
	stateToken := base64.URLEncoding.EncodeToString(b)

	// Store the state token with an expiration time
	stateTokensMutex.Lock()
	stateTokens[stateToken] = time.Now().Add(config.StateTokenTTL)
	stateTokensMutex.Unlock()

	return stateToken
}

// ValidateStateToken validates the state token and removes it from the store if valid.
// ValidateStateToken checks if the provided state token exists and is not expired.
// If the token is valid, it removes it from the store to prevent reuse and returns true.
// If the token does not exist or is expired, it returns false.
func ValidateStateToken(token string) bool {
	stateTokensMutex.Lock()
	defer stateTokensMutex.Unlock()

	expirationTime, exists := stateTokens[token]
	if !exists {
		return false
	}

	if time.Now().After(expirationTime) {
		delete(stateTokens, token)
		return false
	}

	// Token is valid, remove it from the store to prevent reuse
	delete(stateTokens, token)
	return true
}

// CleanupExpiredStateTokens removes expired state tokens from the store.
// CleanupExpiredStateTokens iterates through the stored state tokens and removes
// any that have expired. This helps to keep the state token store clean and
// prevents memory leaks.
func CleanupExpiredStateTokens() {
	stateTokensMutex.Lock()
	defer stateTokensMutex.Unlock()

	now := time.Now()
	for token, expirationTime := range stateTokens {
		if now.After(expirationTime) {
			delete(stateTokens, token)
		}
	}
}
