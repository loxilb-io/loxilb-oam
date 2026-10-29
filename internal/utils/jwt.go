// Package utils provides utility functions for handling JWT tokens.
package utils

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrTokenExpired = errors.New("token is expired")
var ErrInvalidToken = errors.New("invalid token")

// JWTSecretMissing reports whether OAM_JWT_SECRET is unset. The server refuses
// to start in that case; there is no built-in signing key.
func JWTSecretMissing() bool { return os.Getenv("OAM_JWT_SECRET") == "" }

// jwtKey is the secret key used for signing JWT tokens, sourced entirely from
// OAM_JWT_SECRET. There is no committed fallback: a deployment with an empty
// key is rejected at startup (see main.requireSecrets).
var jwtKey = []byte(os.Getenv("OAM_JWT_SECRET"))

// Claims represents the structure of the JWT claims. Authorization middleware
// resolves the role from the DB, not from these claims, so a role change takes
// effect immediately and stale claims are harmless; Role/UserID are carried
// only so the UI can gate menus without a second call (omitempty for older tokens).
type Claims struct {
	Username string `json:"username"`
	Role     string `json:"role,omitempty"`
	UserID   int    `json:"user_id,omitempty"`
	jwt.RegisteredClaims
}

// newTokenID returns an unpredictable 128-bit identifier for the JWT "jti"
// claim.
func newTokenID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// GenerateToken issues a signed JWT for the given user, expiring after
// expirationMinutes. The role is recorded at issuance time for UI convenience
// only; authorization is always re-resolved from the DB.
//
// Every token carries a random "jti". Without it the claims are fully
// determined by (user, role, expiry-in-seconds), so two logins by the same
// user within one second signed to the identical string — and the second
// insert into the token store, where token_value is unique, failed the login.
// Validation does not require the claim: tokens issued before it existed are
// still looked up, and revoked, by their full value.
func GenerateToken(username, role string, userID int, expirationMinutes int) (string, error) {
	now := time.Now()
	expiration := now.Add(time.Duration(expirationMinutes) * time.Minute)
	tokenID, err := newTokenID()
	if err != nil {
		return "", err
	}
	claims := &Claims{
		Username: username,
		Role:     role,
		UserID:   userID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        tokenID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiration),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(jwtKey)
}

// ValidateToken parses and verifies a JWT string, returning its claims or an
// error if the signature, structure, or expiry is invalid.
func ValidateToken(tokenString string) (*Claims, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(_ *jwt.Token) (interface{}, error) {
		return jwtKey, nil
	}, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}))

	if err != nil {
		if errors.Is(err, jwt.ErrTokenExpired) {
			return nil, ErrTokenExpired
		}
		return nil, ErrInvalidToken
	}

	if !token.Valid {
		return nil, ErrInvalidToken
	}

	return claims, nil
}
