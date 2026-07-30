package utils_test

import (
	"crypto/sha256"
	"encoding/base64"
	"strings"
	"testing"

	utils "github.com/loxilb-io/loxilb-oam/pkg/utils"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/crypto/pbkdf2"
)

// legacyHash builds a hash in the pre-2026 unversioned format:
// base64(salt + PBKDF2(password, salt, 10000, 32)).
func legacyHash(password string) string {
	salt := []byte("0123456789abcdef") // 16 bytes
	key := pbkdf2.Key([]byte(password), salt, 10000, 32, sha256.New)
	return base64.StdEncoding.EncodeToString(append(salt, key...))
}

func TestHashPasswordVersionedFormat(t *testing.T) {
	hash, err := utils.HashPassword("S3cure!Password")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if !strings.HasPrefix(hash, "pbkdf2-sha256$600000$") {
		t.Fatalf("expected versioned pbkdf2 format, got %q", hash)
	}
	if utils.NeedsRehash(hash) {
		t.Error("fresh hash should not need rehash")
	}
	if got := utils.GetPasswordHashInfo(hash); got != "pbkdf2-versioned" {
		t.Errorf("GetPasswordHashInfo = %q, want pbkdf2-versioned", got)
	}

	ok, err := utils.VerifyPassword("S3cure!Password", hash)
	if err != nil || !ok {
		t.Fatalf("VerifyPassword(correct) = %v, %v; want true, nil", ok, err)
	}
	ok, err = utils.VerifyPassword("wrong-password", hash)
	if err != nil || ok {
		t.Fatalf("VerifyPassword(wrong) = %v, %v; want false, nil", ok, err)
	}
}

func TestLegacyPBKDF2StillVerifiesAndNeedsRehash(t *testing.T) {
	hash := legacyHash("S3cure!Password")

	ok, err := utils.VerifyPassword("S3cure!Password", hash)
	if err != nil || !ok {
		t.Fatalf("legacy VerifyPassword(correct) = %v, %v; want true, nil", ok, err)
	}
	ok, err = utils.VerifyPassword("wrong-password", hash)
	if err != nil || ok {
		t.Fatalf("legacy VerifyPassword(wrong) = %v, %v; want false, nil", ok, err)
	}
	if !utils.NeedsRehash(hash) {
		t.Error("legacy hash should need rehash")
	}
	if got := utils.GetPasswordHashInfo(hash); got != "pbkdf2-legacy" {
		t.Errorf("GetPasswordHashInfo = %q, want pbkdf2-legacy", got)
	}
}

func TestBcryptStillVerifiesAndNeedsRehash(t *testing.T) {
	raw, err := bcrypt.GenerateFromPassword([]byte("S3cure!Password"), bcrypt.MinCost)
	if err != nil {
		t.Fatalf("bcrypt generate failed: %v", err)
	}
	hash := string(raw)

	ok, err := utils.VerifyPassword("S3cure!Password", hash)
	if err != nil || !ok {
		t.Fatalf("bcrypt VerifyPassword(correct) = %v, %v; want true, nil", ok, err)
	}
	if !utils.NeedsRehash(hash) {
		t.Error("bcrypt hash should need rehash")
	}
}

func TestNeedsRehashOnFewerRounds(t *testing.T) {
	salt := []byte("0123456789abcdef")
	key := pbkdf2.Key([]byte("pw"), salt, 100000, 32, sha256.New)
	hash := "pbkdf2-sha256$100000$" +
		base64.StdEncoding.EncodeToString(salt) + "$" +
		base64.StdEncoding.EncodeToString(key)

	ok, err := utils.VerifyPassword("pw", hash)
	if err != nil || !ok {
		t.Fatalf("VerifyPassword(100k rounds) = %v, %v; want true, nil", ok, err)
	}
	if !utils.NeedsRehash(hash) {
		t.Error("hash with fewer rounds should need rehash")
	}
}
