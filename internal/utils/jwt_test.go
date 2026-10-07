package utils

import (
	"sync"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Identical inputs inside the same second used to sign to the identical
// string; the token store's unique key then rejected the second login.
func TestGenerateTokenIsUniquePerIssue(t *testing.T) {
	const n = 64
	tokens := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			tok, err := GenerateToken("alice", "admin", 1, 60)
			assert.NoError(t, err)
			tokens[i] = tok
		}(i)
	}
	wg.Wait()

	seenToken := map[string]bool{}
	seenID := map[string]bool{}
	for _, tok := range tokens {
		require.NotEmpty(t, tok)
		assert.False(t, seenToken[tok], "duplicate token issued")
		seenToken[tok] = true

		claims, err := ValidateToken(tok)
		require.NoError(t, err)
		assert.Len(t, claims.ID, 32, "jti should be 128 bits, hex-encoded")
		assert.False(t, seenID[claims.ID], "duplicate jti issued")
		seenID[claims.ID] = true
		assert.NotNil(t, claims.IssuedAt)
		assert.Equal(t, "alice", claims.Username)
	}
}

// Tokens issued before the jti claim existed must keep validating; the store
// lookup (and so revocation) is by full token value and needs no jti.
func TestValidateTokenAcceptsTokenWithoutID(t *testing.T) {
	legacy, err := jwt.NewWithClaims(jwt.SigningMethodHS256, &Claims{
		Username: "alice",
		RegisteredClaims: jwt.RegisteredClaims{
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}).SignedString(jwtKey)
	require.NoError(t, err)

	claims, err := ValidateToken(legacy)
	require.NoError(t, err)
	assert.Empty(t, claims.ID)
	assert.Equal(t, "alice", claims.Username)
}
