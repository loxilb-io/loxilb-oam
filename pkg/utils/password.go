package utils

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"github.com/loxilb-io/loxilb-oam/internal/config"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/pbkdf2"
)

const (
	// PBKDF2 parameters for password hashing (OWASP recommends >=600k rounds
	// for PBKDF2-HMAC-SHA256).
	SaltSize      = 16
	PBKDF2Rounds  = 600000
	PBKDF2KeySize = 32

	// legacyPBKDF2Rounds is the round count of the pre-2026 unversioned format
	// (raw base64(salt+hash) with no parameters embedded). Kept only to verify
	// existing hashes; logins rehash to the current format (see NeedsRehash).
	legacyPBKDF2Rounds = 10000

	// pbkdf2Prefix marks the current, versioned hash format:
	// pbkdf2-sha256$<rounds>$<b64 salt>$<b64 hash>
	pbkdf2Prefix = "pbkdf2-sha256"
)

// HashPassword creates a PBKDF2 hash of the password with a random salt,
// in the versioned format pbkdf2-sha256$<rounds>$<b64 salt>$<b64 hash>.
func HashPassword(password string) (string, error) {
	// Generate random salt
	salt := make([]byte, SaltSize)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("failed to generate salt: %w", err)
	}

	hash := pbkdf2.Key([]byte(password), salt, PBKDF2Rounds, PBKDF2KeySize, sha256.New)

	return fmt.Sprintf("%s$%d$%s$%s",
		pbkdf2Prefix,
		PBKDF2Rounds,
		base64.StdEncoding.EncodeToString(salt),
		base64.StdEncoding.EncodeToString(hash)), nil
}

// VerifyPassword verifies a password against a stored hash.
// Supports the versioned PBKDF2 format (current), the legacy unversioned
// PBKDF2 format, and bcrypt (oldest).
func VerifyPassword(password, storedHash string) (bool, error) {
	if strings.HasPrefix(storedHash, pbkdf2Prefix+"$") {
		return verifyVersionedPBKDF2Password(password, storedHash)
	}

	// Try bcrypt (for legacy/updated passwords)
	err := bcrypt.CompareHashAndPassword([]byte(storedHash), []byte(password))
	if err == nil {
		return true, nil
	}

	// Fall back to the legacy unversioned PBKDF2 format
	return verifyLegacyPBKDF2Password(password, storedHash)
}

// NeedsRehash reports whether a stored hash should be re-hashed with the
// current parameters (bcrypt, legacy unversioned format, or a versioned hash
// with fewer rounds than PBKDF2Rounds). Callers should invoke it only after a
// successful VerifyPassword, when the plaintext is available to rehash.
func NeedsRehash(storedHash string) bool {
	if !strings.HasPrefix(storedHash, pbkdf2Prefix+"$") {
		return true
	}
	parts := strings.Split(storedHash, "$")
	if len(parts) != 4 {
		return true
	}
	rounds, err := strconv.Atoi(parts[1])
	if err != nil {
		return true
	}
	return rounds < PBKDF2Rounds
}

// VerifyDefaultPassword checks whether a stored hash matches the configured
// default admin password (config.DefaultConfigPassword, env-driven). Used to
// detect accounts still on the bootstrap password.
func VerifyDefaultPassword(storedHash string) (bool, error) {
	return VerifyPassword(config.DefaultConfigPassword, storedHash)
}

// verifyVersionedPBKDF2Password verifies against the
// pbkdf2-sha256$<rounds>$<b64 salt>$<b64 hash> format.
func verifyVersionedPBKDF2Password(password, storedHash string) (bool, error) {
	parts := strings.Split(storedHash, "$")
	if len(parts) != 4 {
		return false, nil
	}
	rounds, err := strconv.Atoi(parts[1])
	if err != nil || rounds < 1 {
		return false, nil
	}
	salt, err := base64.StdEncoding.DecodeString(parts[2])
	if err != nil {
		return false, nil
	}
	storedKey, err := base64.StdEncoding.DecodeString(parts[3])
	if err != nil || len(storedKey) == 0 {
		return false, nil
	}

	key := pbkdf2.Key([]byte(password), salt, rounds, len(storedKey), sha256.New)
	return bytes.Equal(storedKey, key), nil
}

// verifyLegacyPBKDF2Password verifies a password against the legacy
// unversioned format: base64(salt + hash) at legacyPBKDF2Rounds.
func verifyLegacyPBKDF2Password(password, storedHash string) (bool, error) {
	hashedPasswordBytes, err := base64.StdEncoding.DecodeString(storedHash)
	if err != nil {
		// If base64 decode fails, it's not a PBKDF2 hash
		return false, nil
	}

	// Check if we have enough bytes for salt + hash
	expectedSize := SaltSize + PBKDF2KeySize // 16 + 32 = 48
	if len(hashedPasswordBytes) < expectedSize {
		// Invalid PBKDF2 format
		return false, nil
	}

	// Extract salt (first 16 bytes) and hash (remaining 32 bytes)
	salt := hashedPasswordBytes[:SaltSize]
	storedPasswordHash := hashedPasswordBytes[SaltSize:]

	passwordHash := pbkdf2.Key([]byte(password), salt, legacyPBKDF2Rounds, PBKDF2KeySize, sha256.New)

	// Compare hashes using constant-time comparison
	return bytes.Equal(storedPasswordHash, passwordHash), nil
}

// GetPasswordHashInfo returns information about the password hash format
// Useful for debugging and migration purposes
func GetPasswordHashInfo(storedHash string) string {
	if strings.HasPrefix(storedHash, pbkdf2Prefix+"$") {
		return "pbkdf2-versioned"
	}

	if len(storedHash) >= 60 && (storedHash[:4] == "$2a$" || storedHash[:4] == "$2b$" || storedHash[:4] == "$2y$") {
		return "bcrypt"
	}

	// Try to decode as base64 legacy PBKDF2
	hashedPasswordBytes, err := base64.StdEncoding.DecodeString(storedHash)
	if err == nil && len(hashedPasswordBytes) == SaltSize+PBKDF2KeySize {
		return "pbkdf2-legacy"
	}

	return "unknown"
}
