package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/alexedwards/argon2id"
)

// argonParams follows OWASP guidance (64 MiB, t=3, p=4 is the
// argon2id.DefaultParams baseline; we raise memory slightly).
var argonParams = &argon2id.Params{
	Memory: 64 * 1024, Iterations: 3, Parallelism: 4,
	SaltLength: 16, KeyLength: 32,
}

func HashPassword(password string) (string, error) {
	return argon2id.CreateHash(password, argonParams)
}

func VerifyPassword(password, hash string) bool {
	ok, err := argon2id.ComparePasswordAndHash(password, hash)
	return err == nil && ok
}

const (
	SessionCookieName = "rmm_session"
	apiTokenPrefix    = "rmm_"
)

// NewSessionToken returns (plaintext token, sha256 hash to store).
func NewSessionToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// NewAPIToken returns (plaintext "rmm_..." token, sha256 hash to store).
func NewAPIToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := apiTokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

func IsAPIToken(token string) bool { return strings.HasPrefix(token, apiTokenPrefix) }

// NewEnrollmentToken returns (plaintext "rmme_..." token, sha256 hash).
// Distinct prefix from API tokens so leaked values are identifiable.
func NewEnrollmentToken() (string, []byte, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", nil, err
	}
	token := "rmme_" + base64.RawURLEncoding.EncodeToString(raw)
	return token, HashToken(token), nil
}

// HashToken is how all bearer secrets (sessions, API tokens, recovery
// codes via HashRecoveryCode) are stored: only the digest hits the DB.
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// NewRecoveryCodes generates n codes formatted XXXXX-XXXXX (base32, no
// padding) plus their storage hashes.
func NewRecoveryCodes(n int) (codes []string, hashes []string, err error) {
	enc := base32.StdEncoding.WithPadding(base32.NoPadding)
	for i := 0; i < n; i++ {
		raw := make([]byte, 7)
		if _, err := rand.Read(raw); err != nil {
			return nil, nil, err
		}
		s := enc.EncodeToString(raw)[:10]
		code := fmt.Sprintf("%s-%s", s[:5], s[5:])
		codes = append(codes, code)
		hashes = append(hashes, HashRecoveryCode(code))
	}
	return codes, hashes, nil
}

func HashRecoveryCode(code string) string {
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(code), " ", ""))
	h := sha256.Sum256([]byte(normalized))
	return base64.RawStdEncoding.EncodeToString(h[:])
}

// ConstantTimeEqual avoids leaking comparison timing on secrets.
func ConstantTimeEqual(a, b []byte) bool {
	return subtle.ConstantTimeCompare(a, b) == 1
}
