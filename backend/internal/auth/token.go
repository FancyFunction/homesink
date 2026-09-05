// Package auth implements device enrolment and request authentication: the
// pairing-code exchange of D-01, the device token it issues, the middleware
// that verifies that token on every other request, and the self-signed TLS
// material of D-02.
//
// Secrets. A pairing code and a device token exist in cleartext exactly twice:
// in the response that hands them out, and in the request that presents them.
// Only their SHA-256 is persisted, and neither value is ever logged at any
// level — log lines carry Fingerprint(), the first 12 hex of the hash
// (00-ARCHITECTURE.md §6).
package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

const (
	// TokenPrefix marks a Homesink device token; the remainder is base64url.
	TokenPrefix = "hs_"
	// tokenBytes is the entropy behind a token: 32 bytes of crypto/rand (D-01).
	tokenBytes = 32
	// tokenBodyLen is len(base64url(32 bytes)) without padding.
	tokenBodyLen = 43
	// fingerprintLen truncates a hash for logging (00-ARCHITECTURE.md §6).
	fingerprintLen = 12
	// idHexLen is the hex length of a generated identifier's random part,
	// matching the "itm_" + 16 hex convention of 03-DATA-MODEL.md §1.1.
	idHexLen = 16
)

// GenerateToken returns a fresh device token, "hs_" followed by 43 base64url
// characters over 32 bytes of crypto/rand (D-01).
func GenerateToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: read random bytes: %w", err)
	}
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(b), nil
}

// HashToken is the value stored in devices.token_hash: the SHA-256 of the whole
// token string, lowercase hex. It is what the auth middleware looks up through
// idx_devices_token, and it is one-way, so a stolen database yields no usable
// token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// VerifyToken reports whether token hashes to storedHash. The comparison is
// constant-time so a caller cannot learn a stored hash by timing the middleware
// (WP-B3).
func VerifyToken(token, storedHash string) bool {
	got := HashToken(token)
	return subtle.ConstantTimeCompare([]byte(got), []byte(storedHash)) == 1
}

// ValidTokenFormat reports whether s is shaped like a device token. It is a
// cheap pre-filter in front of the database lookup, never a substitute for
// VerifyToken.
func ValidTokenFormat(s string) bool {
	body, ok := strings.CutPrefix(s, TokenPrefix)
	if !ok || len(body) != tokenBodyLen {
		return false
	}
	_, err := base64.RawURLEncoding.DecodeString(body)
	return err == nil
}

// Fingerprint is the safe-to-log stand-in for a secret: the first 12 hex
// characters of its SHA-256. The empty string fingerprints as "" so a missing
// credential does not turn into a constant that looks like a real one.
func Fingerprint(secret string) string {
	if secret == "" {
		return ""
	}
	return HashToken(secret)[:fingerprintLen]
}

// newID returns prefix + 16 random hex characters, the identifier shape used
// throughout the schema ("itm_" + 16 hex, 03-DATA-MODEL.md §1.1).
func newID(prefix string) (string, error) {
	b := make([]byte, idHexLen/2)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("auth: read random bytes: %w", err)
	}
	return prefix + hex.EncodeToString(b), nil
}
