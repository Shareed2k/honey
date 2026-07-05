package webserver

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/jellydator/ttlcache/v3"
)

const (
	enrollCodeTTL = 10 * time.Minute
	deviceCertTTL = 720 * time.Hour // 30 days
	maxEnrollBody = 1 << 20
)

// enrollStore holds pending one-time enrollment codes (code -> desired CN) and a
// record of issued device certs. Codes are single-use and short-lived.
type enrollStore struct {
	codes   *ttlcache.Cache[string, string]
	mu      sync.Mutex
	devices []DeviceRecord
}

// DeviceRecord is an issued device certificate, for listing / audit.
type DeviceRecord struct {
	CN          string    `json:"cn"`
	Fingerprint string    `json:"fingerprint"`
	IssuedAt    time.Time `json:"issued_at"`
	NotAfter    time.Time `json:"not_after"`
}

func newEnrollStore() *enrollStore {
	// No background Start goroutine: expiry is enforced on Get, and codes are
	// deleted on use. Keeps the store leak-free for tests and shutdown.
	return &enrollStore{codes: ttlcache.New(ttlcache.WithTTL[string, string](enrollCodeTTL))}
}

func (s *enrollStore) record(cn string, cert *x509.Certificate) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.devices = append(s.devices, DeviceRecord{
		CN:          cn,
		Fingerprint: certFingerprint(cert),
		IssuedAt:    time.Now().UTC(),
		NotAfter:    cert.NotAfter.UTC(),
	})
}

func (s *enrollStore) list() []DeviceRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]DeviceRecord, len(s.devices))
	copy(out, s.devices)
	return out
}

// handleMintEnrollCode (authenticated) creates a one-time enrollment code the
// operator hands to a device (rendered as a QR). Returns the CA fingerprint so
// the device can pin it during enrollment.
func (s *Server) handleMintEnrollCode(w http.ResponseWriter, r *http.Request) {
	if s.deviceCA == nil || s.enroll == nil {
		httpError(w, fmt.Errorf("device enrollment not available (no state dir)"), http.StatusServiceUnavailable)
		return
	}
	var body struct {
		CN string `json:"cn"`
	}
	_ = json.NewDecoder(io.LimitReader(r.Body, maxEnrollBody)).Decode(&body)
	cn := strings.TrimSpace(body.CN)
	if cn == "" {
		cn = "device:" + randToken(6)
	}
	code := randToken(32)
	s.enroll.codes.Set(code, cn, ttlcache.DefaultTTL)

	writeJSON(w, map[string]any{
		"code":           code,
		"cn":             cn,
		"enroll_path":    "/api/v1/devices/enroll",
		"ca_fingerprint": s.deviceCA.Fingerprint(),
		"ca_pem":         string(s.deviceCA.CertPEM()),
		"expires_in":     int(enrollCodeTTL.Seconds()),
	})
}

// handleDeviceEnroll (code-authenticated, no session token) validates a one-time
// code, signs the submitted CSR into a short-lived client cert, and returns it
// with the CA chain. Mounted outside the auth group.
func (s *Server) handleDeviceEnroll(w http.ResponseWriter, r *http.Request) {
	if s.deviceCA == nil || s.enroll == nil {
		httpError(w, fmt.Errorf("device enrollment not available (no state dir)"), http.StatusServiceUnavailable)
		return
	}
	var body struct {
		Code string `json:"code"`
		CSR  string `json:"csr"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, maxEnrollBody)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("decode request: %w", err), http.StatusBadRequest)
		return
	}

	item := s.enroll.codes.Get(strings.TrimSpace(body.Code))
	if item == nil {
		http.Error(w, `{"error":"invalid or expired enrollment code"}`, http.StatusUnauthorized)
		return
	}
	cn := item.Value()
	s.enroll.codes.Delete(strings.TrimSpace(body.Code)) // single use

	block, _ := pem.Decode([]byte(body.CSR))
	if block == nil {
		httpError(w, fmt.Errorf("invalid CSR PEM"), http.StatusBadRequest)
		return
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		httpError(w, fmt.Errorf("parse CSR: %w", err), http.StatusBadRequest)
		return
	}
	certPEM, err := s.deviceCA.Sign(csr, cn, deviceCertTTL)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if b, _ := pem.Decode(certPEM); b != nil {
		if cert, perr := x509.ParseCertificate(b.Bytes); perr == nil {
			s.enroll.record(cn, cert)
		}
	}

	writeJSON(w, map[string]any{
		"cn":   cn,
		"cert": string(certPEM),
		"ca":   string(s.deviceCA.CertPEM()),
	})
}

// handleListDevices (authenticated) lists issued device certs.
func (s *Server) handleListDevices(w http.ResponseWriter, _ *http.Request) {
	if s.enroll == nil {
		writeJSON(w, map[string]any{"devices": []DeviceRecord{}})
		return
	}
	writeJSON(w, map[string]any{"devices": s.enroll.list()})
}

func randToken(nBytes int) string {
	b := make([]byte, nBytes)
	if _, err := rand.Read(b); err != nil {
		return hex.EncodeToString(fmt.Appendf(nil, "%d", time.Now().UnixNano()))
	}
	return hex.EncodeToString(b)
}

func certFingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}
