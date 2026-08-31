package persistence

import (
	"encoding/base64"
	"testing"
)

func TestGenerateDeviceToken(t *testing.T) {
	seen := make(map[string]bool)

	for range 100 {
		token, err := generateDeviceToken()
		if err != nil {
			t.Fatalf("generateDeviceToken() error = %v", err)
		}

		decoded, err := base64.RawURLEncoding.DecodeString(token)
		if err != nil {
			t.Fatalf("token %q is not valid base64url: %v", token, err)
		}
		if len(decoded) != deviceTokenBytes {
			t.Errorf("token decoded to %d bytes, want %d", len(decoded), deviceTokenBytes)
		}
		if seen[token] {
			t.Fatalf("generateDeviceToken() returned a duplicate token %q", token)
		}
		seen[token] = true
	}
}

func TestGenerateShareToken(t *testing.T) {
	token, err := generateShareToken()
	if err != nil {
		t.Fatalf("generateShareToken() error = %v", err)
	}

	decoded, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		t.Fatalf("token %q is not valid base64url: %v", token, err)
	}
	if len(decoded) != shareTokenBytes {
		t.Errorf("token decoded to %d bytes, want %d", len(decoded), shareTokenBytes)
	}
	// Share tokens go into a URL and a QR code, so they must not need escaping.
	if base64.RawURLEncoding.EncodeToString(decoded) != token {
		t.Errorf("token %q is not URL-safe", token)
	}
}

func TestHashToken(t *testing.T) {
	// Stable: the same token must always resolve to the same stored hash,
	// otherwise devices would lose their history on the next request.
	first := hashToken("some-token")
	if got := hashToken("some-token"); string(got) != string(first) {
		t.Error("hashToken() is not stable for the same input")
	}
	if len(first) != 32 {
		t.Errorf("hashToken() returned %d bytes, want 32", len(first))
	}
	if string(hashToken("other-token")) == string(first) {
		t.Error("hashToken() collided for different inputs")
	}
}
