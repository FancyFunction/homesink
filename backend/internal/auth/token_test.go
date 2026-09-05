package auth_test

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/FancyFunction/homesink/backend/internal/auth"
)

func TestGenerateToken_HasTheContractShapeAndIsUnique(t *testing.T) {
	const draws = 1000
	seen := map[string]struct{}{}
	for range draws {
		token, err := auth.GenerateToken()
		if err != nil {
			t.Fatalf("GenerateToken: %v", err)
		}
		body, ok := strings.CutPrefix(token, auth.TokenPrefix)
		if !ok {
			t.Fatalf("token %q lacks the hs_ prefix", token)
		}
		if len(body) != 43 {
			t.Fatalf("token body is %d characters, want 43 (32 bytes base64url)", len(body))
		}
		raw, err := base64.RawURLEncoding.DecodeString(body)
		if err != nil {
			t.Fatalf("token body is not base64url: %v", err)
		}
		if len(raw) != 32 {
			t.Fatalf("token carries %d bytes of entropy, want 32", len(raw))
		}
		if !auth.ValidTokenFormat(token) {
			t.Fatalf("ValidTokenFormat rejected a freshly generated token %q", token)
		}
		seen[token] = struct{}{}
	}
	if len(seen) != draws {
		t.Errorf("%d distinct tokens in %d draws; the generator repeats itself", len(seen), draws)
	}
}

func TestValidTokenFormat(t *testing.T) {
	good, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	tests := []struct {
		name  string
		token string
		want  bool
	}{
		{"generated", good, true},
		{"empty", "", false},
		{"no prefix", strings.TrimPrefix(good, auth.TokenPrefix), false},
		{"wrong prefix", "hx_" + strings.TrimPrefix(good, auth.TokenPrefix), false},
		{"too short", good[:len(good)-1], false},
		{"too long", good + "A", false},
		{"not base64url", auth.TokenPrefix + strings.Repeat("+", 43), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := auth.ValidTokenFormat(tc.token); got != tc.want {
				t.Errorf("ValidTokenFormat(%q) = %v, want %v", tc.token, got, tc.want)
			}
		})
	}
}

func TestHashTokenAndVerifyToken(t *testing.T) {
	token, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	hash := auth.HashToken(token)

	if hash == token {
		t.Fatal("HashToken returned the token itself")
	}
	if len(hash) != 64 {
		t.Errorf("hash is %d characters, want 64 hex", len(hash))
	}
	if auth.HashToken(token) != hash {
		t.Error("HashToken is not deterministic")
	}
	if !auth.VerifyToken(token, hash) {
		t.Error("VerifyToken rejected the token its own hash came from")
	}

	other, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	if auth.VerifyToken(other, hash) {
		t.Error("VerifyToken accepted a different token")
	}
	if auth.VerifyToken(token, "") {
		t.Error("VerifyToken accepted an empty stored hash")
	}
}

func TestFingerprint_IsShortAndNotTheSecret(t *testing.T) {
	token, err := auth.GenerateToken()
	if err != nil {
		t.Fatalf("GenerateToken: %v", err)
	}
	fp := auth.Fingerprint(token)
	if len(fp) != 12 {
		t.Errorf("fingerprint is %d characters, want 12 (00-ARCHITECTURE.md §6)", len(fp))
	}
	if strings.Contains(token, fp) {
		t.Error("the fingerprint is a substring of the token it stands in for")
	}
	if !strings.HasPrefix(auth.HashToken(token), fp) {
		t.Error("the fingerprint is not the leading hash characters")
	}
	if auth.Fingerprint("") != "" {
		t.Error("an absent secret must fingerprint as the empty string")
	}
}
