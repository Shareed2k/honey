package webauthn

import (
	"testing"
	"time"
)

func TestToken_MintVerify(t *testing.T) {
	secret := []byte("test-secret")
	now := time.Unix(1_000_000, 0)

	tok, err := mintToken(secret, "alice", time.Hour, now)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}

	if !verifyToken(secret, "alice", tok, now) {
		t.Fatal("valid token should verify")
	}
	if verifyToken(secret, "bob", tok, now) {
		t.Fatal("token must be bound to its actor")
	}
	if verifyToken(secret, "alice", tok, now.Add(2*time.Hour)) {
		t.Fatal("expired token must not verify")
	}
	if verifyToken([]byte("other-secret"), "alice", tok, now) {
		t.Fatal("token signed with another secret must not verify")
	}
	if verifyToken(secret, "alice", tok+"x", now) {
		t.Fatal("tampered token must not verify")
	}
	if verifyToken(secret, "alice", "garbage", now) {
		t.Fatal("malformed token must not verify")
	}
}
