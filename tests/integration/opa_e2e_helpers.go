//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/searchrun"
)

// mustCIDRs parses CIDR strings into networks, failing the test on error.
func mustCIDRs(t *testing.T, cidrs ...string) []*net.IPNet {
	t.Helper()
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			t.Fatalf("parse cidr %q: %v", c, err)
		}
		nets = append(nets, n)
	}
	return nets
}

// doJSONHeaders posts/gets with caller-supplied headers (for JWT / proxy /
// custom auth), unlike doJSON which always sends the shared bearer token.
func doJSONHeaders(t *testing.T, client *http.Client, method, url string, body any, headers map[string]string) *http.Response {
	t.Helper()
	var rdr *bytes.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	} else {
		rdr = bytes.NewReader(nil)
	}
	req, err := http.NewRequest(method, url, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

// writePolicy writes a single rego module to a fresh temp dir and returns the dir.
func writePolicy(t *testing.T, rego string) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "policy.rego"), []byte(rego), 0o600); err != nil {
		t.Fatalf("write policy: %v", err)
	}
	return dir
}

// newEnforcer builds a *policy.Enforcer from a rego string.
func newEnforcer(t *testing.T, rego string) *policy.Enforcer {
	t.Helper()
	enf, err := policy.New(context.Background(), writePolicy(t, rego))
	if err != nil {
		t.Fatalf("policy.New: %v", err)
	}
	return enf
}

// signJWT mints an Ed25519 JWT with the given subject.
func signJWT(t *testing.T, priv ed25519.PrivateKey, sub string) string {
	t.Helper()
	tok := jwt.NewWithClaims(jwt.SigningMethodEdDSA, jwt.RegisteredClaims{
		Subject:   sub,
		ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
	})
	s, err := tok.SignedString(priv)
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	return s
}

// sshTarget bundles a running SSH container target plus the registries wired to
// reach it, for cue-exec / webhook e2e tests that run a real command on it.
type sshTarget struct {
	rec       hosts.Record
	searchReg *searchrun.Registry
	execReg   *testRegistry
	host      string
	port      int
	keyFile   string
}

// newSSHTarget starts the shared SSH container and builds a connectable record
// (name "ssh-test") plus search/exec registries pointed at it.
func newSSHTarget(t *testing.T) sshTarget {
	t.Helper()
	host, port, keyFile := startSSH(t)
	rec := hosts.CloneWithMetaSSHPort(
		hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: host},
		port,
	)
	return sshTarget{
		rec:       rec,
		searchReg: searchrun.NewRegistry([]searchrun.ProviderFactory{webhookTestFactory{rec: rec}}),
		execReg:   &testRegistry{Dialer: newTestDialer(host, port, keyFile)},
		host:      host,
		port:      port,
		keyFile:   keyFile,
	}
}

// readFile runs `cat path` on the SSH container, returning (output, err). A
// non-nil err means the file is absent (command never ran / was skipped).
func (s sshTarget) readFile(t *testing.T, path string) (string, error) {
	t.Helper()
	client, err := s.execReg.Dialer.Dial("testuser", s.host, s.port, s.keyFile)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer client.Close()
	out, err := client.Run("cat " + path)
	return string(out), err
}
