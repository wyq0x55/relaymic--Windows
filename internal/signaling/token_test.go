package signaling

import (
	"encoding/base64"
	"testing"
)

func TestNewTokenIs256BitsOfURLSafeBase64(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("NewToken() = %q, not raw url-safe base64: %v", token, err)
	}
	if len(raw) != TokenBytes {
		t.Fatalf("NewToken() decoded to %d bytes, want %d", len(raw), TokenBytes)
	}
}

func TestNewTokenIsUnique(t *testing.T) {
	seen := make(map[string]bool, 64)
	for i := 0; i < 64; i++ {
		token, err := NewToken()
		if err != nil {
			t.Fatalf("NewToken() error = %v", err)
		}
		if seen[token] {
			t.Fatalf("NewToken() repeated %q", token)
		}
		seen[token] = true
	}
}

func TestTokenDigestIsStableAndNotTheToken(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	if DigestToken(token) != DigestToken(token) {
		t.Fatal("DigestToken() is not deterministic")
	}
	digest := DigestToken(token)
	if string(digest[:]) == token {
		t.Fatal("DigestToken() returned the token itself")
	}
	if DigestToken(token) == DigestToken(token+"x") {
		t.Fatal("DigestToken() collided for different tokens")
	}
}

func TestTokenMatches(t *testing.T) {
	token, err := NewToken()
	if err != nil {
		t.Fatalf("NewToken() error = %v", err)
	}
	digest := DigestToken(token)
	if !TokenMatches(digest, token) {
		t.Fatal("TokenMatches() rejected the configured token")
	}
	if TokenMatches(digest, token+"x") {
		t.Fatal("TokenMatches() accepted a modified token")
	}
	if TokenMatches(digest, "") {
		t.Fatal("TokenMatches() accepted an empty token")
	}
}
