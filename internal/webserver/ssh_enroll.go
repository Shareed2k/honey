package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/jellydator/ttlcache/v3"
	"golang.org/x/crypto/ssh"

	"github.com/shareed2k/honey/internal/sshca"
)

const (
	// sshEnrollDefaultTTL is the default lifetime of a minted SSH user cert when
	// the mint request omits a ttl.
	sshEnrollDefaultTTL = time.Hour
	// sshEnrollMaxTTL caps the requested cert TTL so a code cannot grant an
	// unbounded-lifetime certificate.
	sshEnrollMaxTTL = 24 * time.Hour
)

// sshEnrollGrant is the value stored behind a one-time SSH enrollment code: the
// principals, key ID, and TTL the redeemed certificate will carry.
type sshEnrollGrant struct {
	Principals []string
	KeyID      string
	TTL        time.Duration
}

// SSHEnrollAPI owns the self-service SSH-cert enrollment endpoints (mint code /
// enroll). ca and codes are nil-together when no state dir is available, in
// which case the endpoints report 503. Mirrors EnrollAPI (device mTLS) but
// issues short-lived SSH certificates via internal/sshca.
type SSHEnrollAPI struct {
	ca    *sshca.CA
	codes *ttlcache.Cache[string, sshEnrollGrant]
}

// NewSSHEnrollAPI wires the SSH CA and its one-time-code store. Pass nil when
// SSH enrollment is unavailable (no state dir); both endpoints then report 503.
func NewSSHEnrollAPI(ca *sshca.CA) *SSHEnrollAPI {
	if ca == nil {
		return &SSHEnrollAPI{}
	}
	// No background Start goroutine: expiry is enforced on Get, and codes are
	// deleted on use. Keeps the store leak-free for tests and shutdown.
	return &SSHEnrollAPI{
		ca:    ca,
		codes: ttlcache.New(ttlcache.WithTTL[string, sshEnrollGrant](enrollCodeTTL)),
	}
}

// handleMintSSHEnrollCode (authenticated, operator) mints a one-time code that a
// user later redeems with their SSH public key to receive a short-lived cert.
// Body: {"principals":["alice"],"key_id":"alice","ttl":"1h"} (a single
// "principal" string is also accepted). Returns the code, its lifetime, and the
// CA public key so the operator can print/trust it. Mounted inside the auth group.
func (a *SSHEnrollAPI) handleMintSSHEnrollCode(w http.ResponseWriter, r *http.Request) {
	if a.ca == nil || a.codes == nil {
		httpError(w, fmt.Errorf("ssh enrollment not available (no state dir)"), http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Principal  string   `json:"principal"`
		Principals []string `json:"principals"`
		KeyID      string   `json:"key_id"`
		TTL        string   `json:"ttl"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEnrollBody)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("decode request: %w", err), http.StatusBadRequest)
		return
	}

	principals := trimNonEmpty(append(body.Principals, body.Principal))
	if len(principals) == 0 {
		httpError(w, fmt.Errorf("at least one principal is required"), http.StatusBadRequest)
		return
	}

	ttl := sshEnrollDefaultTTL
	if raw := strings.TrimSpace(body.TTL); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil {
			httpError(w, fmt.Errorf("parse ttl: %w", err), http.StatusBadRequest)
			return
		}
		if d <= 0 {
			httpError(w, fmt.Errorf("ttl must be positive"), http.StatusBadRequest)
			return
		}
		ttl = d
	}
	if ttl > sshEnrollMaxTTL {
		ttl = sshEnrollMaxTTL
	}

	keyID := strings.TrimSpace(body.KeyID)
	if keyID == "" {
		keyID = principals[0]
	}

	code := randToken(32)
	a.codes.Set(code, sshEnrollGrant{Principals: principals, KeyID: keyID, TTL: ttl}, ttlcache.DefaultTTL)

	writeJSON(w, map[string]any{
		"code":               code,
		"expires_in_seconds": int(enrollCodeTTL.Seconds()),
		"ca":                 string(a.ca.AuthorizedKey()),
	})
}

// handleSSHEnroll (code-authenticated, no session token) validates a one-time
// code and signs the submitted SSH public key into a short-lived user cert.
// Body: {"code":"...","public_key":"ssh-ed25519 AAAA... user@host"}. Mounted
// outside the auth group — the one-time code is the credential.
func (a *SSHEnrollAPI) handleSSHEnroll(w http.ResponseWriter, r *http.Request) {
	if a.ca == nil || a.codes == nil {
		httpError(w, fmt.Errorf("ssh enrollment not available (no state dir)"), http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Code      string `json:"code"`
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEnrollBody)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("decode request: %w", err), http.StatusBadRequest)
		return
	}

	code := strings.TrimSpace(body.Code)
	item := a.codes.Get(code)
	if item == nil {
		http.Error(w, `{"error":"invalid or expired enrollment code"}`, http.StatusUnauthorized)
		return
	}
	grant := item.Value()
	a.codes.Delete(code) // single use

	pub, _, _, _, err := ssh.ParseAuthorizedKey([]byte(body.PublicKey))
	if err != nil {
		httpError(w, fmt.Errorf("parse public key: %w", err), http.StatusBadRequest)
		return
	}

	keyID := strings.TrimSpace(grant.KeyID)
	if keyID == "" && len(grant.Principals) > 0 {
		keyID = grant.Principals[0]
	}

	cert, err := a.ca.Sign(sshca.SignRequest{
		PublicKey:  pub,
		KeyID:      keyID,
		Principals: grant.Principals,
		TTL:        grant.TTL,
	})
	if err != nil {
		httpError(w, fmt.Errorf("sign certificate: %w", err), http.StatusBadRequest)
		return
	}

	writeJSON(w, map[string]any{
		"cert":              string(ssh.MarshalAuthorizedKey(cert)),
		"ca":                string(a.ca.AuthorizedKey()),
		"principals":        grant.Principals,
		"valid_before_unix": cert.ValidBefore,
	})
}

// trimNonEmpty returns vals with surrounding whitespace trimmed and empty
// entries dropped, preserving order.
func trimNonEmpty(vals []string) []string {
	out := make([]string, 0, len(vals))
	for _, v := range vals {
		if t := strings.TrimSpace(v); t != "" {
			out = append(out, t)
		}
	}
	return out
}
