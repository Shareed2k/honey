package sshca_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/sshca"
)

// newUserKey generates a throwaway ed25519 user keypair and returns its SSH
// public key (the thing the CA certifies).
func newUserKey(t *testing.T) ssh.PublicKey {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate user key: %v", err)
	}
	sshPub, err := ssh.NewPublicKey(pub)
	if err != nil {
		t.Fatalf("ssh public key: %v", err)
	}
	return sshPub
}

func TestLoadOrCreateCA_PersistsAndReloads(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	first, err := sshca.LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateCA (create): %v", err)
	}
	reloaded, err := sshca.LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateCA (reload): %v", err)
	}

	// The reloaded CA must be the same key: a cert it signs verifies against the
	// first CA's public key.
	if string(first.AuthorizedKey()) != string(reloaded.AuthorizedKey()) {
		t.Fatalf("reloaded CA public key differs from original")
	}

	cert, err := reloaded.Sign(sshca.SignRequest{
		PublicKey:  newUserKey(t),
		KeyID:      "alice",
		Principals: []string{"alice"},
		TTL:        time.Hour,
	})
	if err != nil {
		t.Fatalf("Sign with reloaded CA: %v", err)
	}
	checker := &ssh.CertChecker{
		IsUserAuthority: func(auth ssh.PublicKey) bool {
			return trustsKey(first.PublicKey(), auth)
		},
	}
	if err := checker.CheckCert("alice", cert); err != nil {
		t.Fatalf("CheckCert against original CA: %v", err)
	}
}

func TestLoadCAPublicKey(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	// Absent CA => (nil, false, nil).
	pub, ok, err := sshca.LoadCAPublicKey(dir)
	if err != nil {
		t.Fatalf("LoadCAPublicKey (absent): unexpected error: %v", err)
	}
	if ok || pub != nil {
		t.Fatalf("LoadCAPublicKey (absent): got (%v, %v), want (nil, false)", pub, ok)
	}

	ca, err := sshca.LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}

	pub, ok, err = sshca.LoadCAPublicKey(dir)
	if err != nil {
		t.Fatalf("LoadCAPublicKey (present): %v", err)
	}
	if !ok || pub == nil {
		t.Fatalf("LoadCAPublicKey (present): got (%v, %v), want (pub, true)", pub, ok)
	}
	if !trustsKey(ca.PublicKey(), pub) {
		t.Fatalf("LoadCAPublicKey returned a key that does not match the CA")
	}
}

func TestSign_Validation(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ca, err := sshca.LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}
	userKey := newUserKey(t)

	tests := []struct {
		name    string
		req     sshca.SignRequest
		wantErr bool
	}{
		{
			name:    "happy path",
			req:     sshca.SignRequest{PublicKey: userKey, KeyID: "alice", Principals: []string{"alice"}, TTL: time.Hour},
			wantErr: false,
		},
		{
			name:    "nil public key",
			req:     sshca.SignRequest{PublicKey: nil, Principals: []string{"alice"}, TTL: time.Hour},
			wantErr: true,
		},
		{
			name:    "no principals",
			req:     sshca.SignRequest{PublicKey: userKey, Principals: nil, TTL: time.Hour},
			wantErr: true,
		},
		{
			name:    "zero ttl",
			req:     sshca.SignRequest{PublicKey: userKey, Principals: []string{"alice"}, TTL: 0},
			wantErr: true,
		},
		{
			name:    "negative ttl",
			req:     sshca.SignRequest{PublicKey: userKey, Principals: []string{"alice"}, TTL: -time.Hour},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cert, serr := ca.Sign(tt.req)
			if tt.wantErr {
				if serr == nil {
					t.Fatalf("Sign(%s): expected error, got nil", tt.name)
				}
				return
			}
			if serr != nil {
				t.Fatalf("Sign(%s): unexpected error: %v", tt.name, serr)
			}
			if cert.CertType != ssh.UserCert {
				t.Errorf("Sign(%s): CertType = %d, want UserCert", tt.name, cert.CertType)
			}
			if _, ok := cert.Extensions["permit-pty"]; !ok {
				t.Errorf("Sign(%s): missing permit-pty extension", tt.name)
			}
		})
	}
}

func TestSign_CheckCert(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	ca, err := sshca.LoadOrCreateCA(dir)
	if err != nil {
		t.Fatalf("LoadOrCreateCA: %v", err)
	}

	trust := func(auth ssh.PublicKey) bool { return trustsKey(ca.PublicKey(), auth) }

	tests := []struct {
		name      string
		principal string // principal presented to CheckCert
		signFor   []string
		ttl       time.Duration
		clock     func() time.Time
		wantErr   bool
	}{
		{
			name:      "listed principal accepted",
			principal: "alice",
			signFor:   []string{"alice", "ops"},
			ttl:       time.Hour,
			wantErr:   false,
		},
		{
			name:      "unlisted principal rejected",
			principal: "mallory",
			signFor:   []string{"alice"},
			ttl:       time.Hour,
			wantErr:   true,
		},
		{
			name:      "expired cert rejected",
			principal: "alice",
			signFor:   []string{"alice"},
			ttl:       time.Minute,
			// Verify far in the future so the cert is past ValidBefore.
			clock:   func() time.Time { return time.Now().Add(24 * time.Hour) },
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cert, serr := ca.Sign(sshca.SignRequest{
				PublicKey:  newUserKey(t),
				KeyID:      tt.name,
				Principals: tt.signFor,
				TTL:        tt.ttl,
			})
			if serr != nil {
				t.Fatalf("Sign: %v", serr)
			}
			checker := &ssh.CertChecker{IsUserAuthority: trust, Clock: tt.clock}
			cerr := checker.CheckCert(tt.principal, cert)
			if tt.wantErr && cerr == nil {
				t.Fatalf("CheckCert(%q): expected error, got nil", tt.principal)
			}
			if !tt.wantErr && cerr != nil {
				t.Fatalf("CheckCert(%q): unexpected error: %v", tt.principal, cerr)
			}
		})
	}
}

// trustsKey reports whether want and got are the same SSH public key (by
// marshaled wire form).
func trustsKey(want, got ssh.PublicKey) bool {
	return string(want.Marshal()) == string(got.Marshal())
}
