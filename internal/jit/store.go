// Package jit implements a durable, concurrency-safe grant store for
// time-boxed access grants ("share links"). Grants persist as JSONL on disk
// (one JSON object per line) so they survive process restarts; every mutation
// is written to disk before it is considered committed.
package jit

import (
	"bufio"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Capability is a single permission a grant confers on its redeemer.
type Capability string

// Status is the lifecycle state of a Grant.
type Status string

// Delivery describes how the redeemer receives access once the grant is redeemed.
type Delivery string

// Capabilities a grant may confer.
const (
	CapShell  Capability = "shell"
	CapExec   Capability = "exec"
	CapTunnel Capability = "tunnel"
)

// Grant lifecycle states.
const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDenied   Status = "denied"
	StatusRevoked  Status = "revoked"
)

// Delivery mechanisms.
const (
	DeliveryWeb  Delivery = "web"
	DeliveryCert Delivery = "cert"
	DeliveryBoth Delivery = "both"
)

// Sentinel errors returned by Store methods. Wrapped errors remain
// errors.Is-comparable to these.
var (
	ErrGrantNotFound    = errors.New("grant not found")
	ErrGrantNotActive   = errors.New("grant not active")
	ErrGrantNotTerminal = errors.New("grant is not terminal; revoke it first")
	ErrInvalidGrant     = errors.New("invalid grant")
)

// retention is how long a terminal (denied, revoked, or expired-approved)
// grant is kept around after it stopped being live, before gcLocked drops it.
const retention = 7 * 24 * time.Hour

// ResourceRef identifies the target a grant applies to. Meta carries
// provider-specific fields (e.g. ssh_user, env, groups) verbatim.
type ResourceRef struct {
	Name      string            `json:"name"`
	Provider  string            `json:"provider,omitempty"`
	PrimaryIP string            `json:"primary_ip,omitempty"`
	Meta      map[string]string `json:"meta,omitempty"`
}

// Grant is one time-boxed access grant ("share link").
type Grant struct {
	ID              string        `json:"id"`
	CodeHash        string        `json:"code_hash"` // sha-256 hex of the redeem secret; the secret itself is NEVER stored
	Actor           string        `json:"actor"`     // creator
	Recipient       string        `json:"recipient,omitempty"`
	Resource        ResourceRef   `json:"resource"`
	Capabilities    []Capability  `json:"capabilities"`
	Delivery        Delivery      `json:"delivery"`
	Duration        time.Duration `json:"duration"`
	Reason          string        `json:"reason,omitempty"`
	Status          Status        `json:"status"`
	RequireApproval bool          `json:"require_approval"`
	Approver        string        `json:"approver,omitempty"`
	CreatedAt       time.Time     `json:"created_at"`
	DecidedAt       time.Time     `json:"decided_at,omitempty"`
	StartsAt        time.Time     `json:"starts_at,omitempty"`
	ExpiresAt       time.Time     `json:"expires_at,omitempty"`
	MaxRedemptions  int           `json:"max_redemptions"` // 0 = unlimited within the window
	Redemptions     int           `json:"redemptions"`
}

// Store is a durable, concurrency-safe collection of Grants persisted as
// JSONL at path. All exported methods are safe for concurrent use.
type Store struct {
	mu     sync.Mutex
	path   string
	grants map[string]*Grant
	now    func() time.Time
}

// NewStore loads an existing JSONL file at path (if present) into memory and
// returns a ready store. A nil now defaults to time.Now. The parent directory
// is created (mode 0o700) if missing.
func NewStore(path string, now func() time.Time) (*Store, error) {
	if now == nil {
		now = time.Now
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("jit: create store directory: %w", err)
	}
	s := &Store{
		path:   path,
		grants: make(map[string]*Grant),
		now:    now,
	}
	if err := s.load(); err != nil {
		return nil, fmt.Errorf("jit: load store: %w", err)
	}
	return s, nil
}

// load reads the JSONL file into memory. A missing file yields an empty
// store. Blank lines are skipped.
func (s *Store) load() error {
	f, err := os.Open(s.path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open store file: %w", err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var g Grant
		if err := json.Unmarshal([]byte(line), &g); err != nil {
			return fmt.Errorf("decode grant: %w", err)
		}
		stored := g
		s.grants[stored.ID] = &stored
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan store file: %w", err)
	}
	return nil
}

// persistLocked writes every in-memory grant to a temp file in the same
// directory as the store file, then renames it over the target so the write
// is atomic. Caller holds the lock.
func (s *Store) persistLocked() error {
	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, "jit-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = os.Remove(tmpName)
		}
	}()

	ids := make([]string, 0, len(s.grants))
	for id := range s.grants {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	w := bufio.NewWriter(tmp)
	for _, id := range ids {
		b, err := json.Marshal(s.grants[id])
		if err != nil {
			_ = tmp.Close()
			return fmt.Errorf("encode grant: %w", err)
		}
		if _, err := w.Write(b); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("write grant: %w", err)
		}
		if err := w.WriteByte('\n'); err != nil {
			_ = tmp.Close()
			return fmt.Errorf("write grant: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("flush temp file: %w", err)
	}
	// os.CreateTemp already creates the file with mode 0o600; set it
	// explicitly so the on-disk permission is documented, not incidental.
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("rename temp file: %w", err)
	}
	committed = true
	return nil
}

// Create validates the caller-supplied fields, generates the redeem code,
// stores only its hash, assigns ID + timestamps + initial status/window, and
// persists the grant. It returns the stored grant plus the one-time
// plaintext code — the only time it is ever available.
func (s *Store) Create(g Grant) (Grant, string, error) {
	if err := validateGrant(g); err != nil {
		return Grant{}, "", err
	}
	id, err := newID()
	if err != nil {
		return Grant{}, "", fmt.Errorf("jit: generate grant id: %w", err)
	}
	code, codeHash, err := newCode()
	if err != nil {
		return Grant{}, "", fmt.Errorf("jit: generate redeem code: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()

	now := s.now()
	stored := Grant{
		ID:        id,
		CodeHash:  codeHash,
		Actor:     g.Actor,
		Recipient: g.Recipient,
		Resource: ResourceRef{
			Name:      g.Resource.Name,
			Provider:  g.Resource.Provider,
			PrimaryIP: g.Resource.PrimaryIP,
			Meta:      copyMeta(g.Resource.Meta),
		},
		Capabilities:    append([]Capability(nil), g.Capabilities...),
		Delivery:        g.Delivery,
		Duration:        g.Duration,
		Reason:          g.Reason,
		RequireApproval: g.RequireApproval,
		MaxRedemptions:  g.MaxRedemptions,
		CreatedAt:       now,
	}
	if stored.RequireApproval {
		stored.Status = StatusPending
	} else {
		stored.Status = StatusApproved
		stored.StartsAt = now
		stored.ExpiresAt = now.Add(stored.Duration)
	}

	s.grants[stored.ID] = &stored
	if err := s.persistLocked(); err != nil {
		delete(s.grants, stored.ID)
		return Grant{}, "", fmt.Errorf("jit: persist grant: %w", err)
	}
	return copyGrant(&stored), code, nil
}

// Get returns a copy of the grant and whether it exists.
func (s *Store) Get(id string) (Grant, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	g, ok := s.grants[id]
	if !ok {
		return Grant{}, false
	}
	return copyGrant(g), true
}

// List returns copies of all grants, newest-first by CreatedAt. The sort is
// stable so a caller paginating over repeated calls (e.g. the webserver's
// grants/sessions listings) gets a deterministic order even when two grants
// share an identical CreatedAt.
func (s *Store) List() []Grant {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	out := make([]Grant, 0, len(s.grants))
	for _, g := range s.grants {
		out = append(out, copyGrant(g))
	}
	sort.SliceStable(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out
}

// Decide moves a Pending grant to Approved (opening the redemption window:
// StartsAt=now, ExpiresAt=now+Duration) or Denied. It errors if the grant is
// missing (ErrGrantNotFound) or not Pending (ErrGrantNotActive).
func (s *Store) Decide(id, approver string, approve bool) (Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()

	g, ok := s.grants[id]
	if !ok {
		return Grant{}, fmt.Errorf("jit: decide grant %q: %w", id, ErrGrantNotFound)
	}
	if g.Status != StatusPending {
		return Grant{}, fmt.Errorf("jit: decide grant %q: %w", id, ErrGrantNotActive)
	}

	prev := *g
	now := s.now()
	g.Approver = approver
	g.DecidedAt = now
	if approve {
		g.Status = StatusApproved
		g.StartsAt = now
		g.ExpiresAt = now.Add(g.Duration)
	} else {
		g.Status = StatusDenied
	}
	if err := s.persistLocked(); err != nil {
		*g = prev
		return Grant{}, fmt.Errorf("jit: persist decision for grant %q: %w", id, err)
	}
	return copyGrant(g), nil
}

// Revoke marks a Pending or Approved grant Revoked. It errors if the grant is
// missing (ErrGrantNotFound) or already terminal (ErrGrantNotActive). The
// revoking actor is recorded in Approver, alongside DecidedAt.
func (s *Store) Revoke(id, actor string) (Grant, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()

	g, ok := s.grants[id]
	if !ok {
		return Grant{}, fmt.Errorf("jit: revoke grant %q: %w", id, ErrGrantNotFound)
	}
	if g.Status != StatusPending && g.Status != StatusApproved {
		return Grant{}, fmt.Errorf("jit: revoke grant %q: %w", id, ErrGrantNotActive)
	}

	prev := *g
	g.Status = StatusRevoked
	g.Approver = actor
	g.DecidedAt = s.now()
	if err := s.persistLocked(); err != nil {
		*g = prev
		return Grant{}, fmt.Errorf("jit: persist revocation of grant %q: %w", id, err)
	}
	return copyGrant(g), nil
}

// Delete permanently removes a TERMINAL grant (denied, revoked, or an
// approved grant past its expiry — see isTerminal, the same predicate
// gcLocked uses). It errors with ErrGrantNotFound for an unknown id and
// ErrGrantNotTerminal for an ACTIVE grant (pending, or approved and still
// within its window): the caller must revoke it first, so a delete can never
// silently drop a live share's audit trail before it actually ends.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()

	g, ok := s.grants[id]
	if !ok {
		return fmt.Errorf("jit: delete grant %q: %w", id, ErrGrantNotFound)
	}
	if !isTerminal(g, s.now()) {
		return fmt.Errorf("jit: delete grant %q: %w", id, ErrGrantNotTerminal)
	}

	prev := g
	delete(s.grants, id)
	if err := s.persistLocked(); err != nil {
		s.grants[id] = prev
		return fmt.Errorf("jit: persist deletion of grant %q: %w", id, err)
	}
	return nil
}

// Purge permanently removes every currently TERMINAL grant, regardless of how
// long ago it went terminal (unlike gcLocked, which only reaps ones older
// than retention), and returns how many were deleted. It is the "delete all
// finished grants" bulk action; active grants are never touched.
func (s *Store) Purge() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()

	now := s.now()
	removed := make(map[string]*Grant)
	for id, g := range s.grants {
		if isTerminal(g, now) {
			removed[id] = g
			delete(s.grants, id)
		}
	}
	if len(removed) == 0 {
		return 0, nil
	}
	if err := s.persistLocked(); err != nil {
		for id, g := range removed {
			s.grants[id] = g
		}
		return 0, fmt.Errorf("jit: persist purge: %w", err)
	}
	return len(removed), nil
}

// Active reports whether the grant is currently redeemable.
func (s *Store) Active(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	g, ok := s.grants[id]
	if !ok {
		return false
	}
	return isActive(g, s.now())
}

// isActive implements the Active predicate shared by Active and Redeem.
func isActive(g *Grant, now time.Time) bool {
	if g.Status != StatusApproved {
		return false
	}
	if now.Before(g.StartsAt) {
		return false
	}
	if !now.Before(g.ExpiresAt) {
		return false
	}
	if g.MaxRedemptions != 0 && g.Redemptions >= g.MaxRedemptions {
		return false
	}
	return true
}

// Redeem looks the grant up by hashing the presented code and comparing
// against every stored hash in constant time (so lookup time does not leak
// which grant, if any, matched), then checks the active window and
// redemption cap. On success it increments Redemptions and persists.
func (s *Store) Redeem(code string) (Grant, error) {
	hash := []byte(hashCode(code))

	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()

	var match *Grant
	for _, g := range s.grants {
		if subtle.ConstantTimeCompare([]byte(g.CodeHash), hash) == 1 {
			match = g
		}
	}
	if match == nil {
		return Grant{}, fmt.Errorf("jit: redeem: %w", ErrGrantNotFound)
	}
	if !isActive(match, s.now()) {
		return Grant{}, fmt.Errorf("jit: redeem grant %q: %w", match.ID, ErrGrantNotActive)
	}

	prev := *match
	match.Redemptions++
	if err := s.persistLocked(); err != nil {
		*match = prev
		return Grant{}, fmt.Errorf("jit: persist redemption of grant %q: %w", match.ID, err)
	}
	return copyGrant(match), nil
}

// Peek looks a grant up by hashing the presented code and comparing against
// every stored hash in constant time, WITHOUT consuming a redemption or
// altering the matched grant. Returns ErrGrantNotFound if no code matches. Unlike
// Redeem it returns the grant regardless of active state, so callers can show
// an accurate status (pending / expired / revoked) for a valid code.
func (s *Store) Peek(code string) (Grant, error) {
	hash := []byte(hashCode(code))

	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()

	var match *Grant
	for _, g := range s.grants {
		if subtle.ConstantTimeCompare([]byte(g.CodeHash), hash) == 1 {
			match = g
		}
	}
	if match == nil {
		return Grant{}, fmt.Errorf("jit: peek: %w", ErrGrantNotFound)
	}
	return copyGrant(match), nil
}

// gcLocked drops grants that are terminal (denied, revoked, or an approved
// grant past its expiry) and whose terminal reference time — the later of
// CreatedAt, ExpiresAt, and DecidedAt — is older than retention. Caller
// holds the lock. GC is synchronous; this package starts no goroutines.
func (s *Store) gcLocked() {
	now := s.now()
	cutoff := now.Add(-retention)
	for id, g := range s.grants {
		if !isTerminal(g, now) {
			continue
		}
		ref := g.CreatedAt
		if g.ExpiresAt.After(ref) {
			ref = g.ExpiresAt
		}
		if g.DecidedAt.After(ref) {
			ref = g.DecidedAt
		}
		if ref.Before(cutoff) {
			delete(s.grants, id)
		}
	}
}

// isTerminal reports whether the grant can no longer transition to Active.
func isTerminal(g *Grant, now time.Time) bool {
	switch g.Status {
	case StatusDenied, StatusRevoked:
		return true
	case StatusApproved:
		return !g.ExpiresAt.IsZero() && !now.Before(g.ExpiresAt)
	default:
		return false
	}
}

// validateGrant checks the caller-supplied fields of a Grant passed to Create.
func validateGrant(g Grant) error {
	if g.Duration <= 0 {
		return fmt.Errorf("%w: duration must be positive", ErrInvalidGrant)
	}
	if len(g.Capabilities) == 0 {
		return fmt.Errorf("%w: at least one capability is required", ErrInvalidGrant)
	}
	for _, c := range g.Capabilities {
		switch c {
		case CapShell, CapExec, CapTunnel:
		default:
			return fmt.Errorf("%w: invalid capability %q", ErrInvalidGrant, c)
		}
	}
	switch g.Delivery {
	case DeliveryWeb, DeliveryCert, DeliveryBoth:
	default:
		return fmt.Errorf("%w: invalid delivery %q", ErrInvalidGrant, g.Delivery)
	}
	if strings.TrimSpace(g.Resource.Name) == "" {
		return fmt.Errorf("%w: resource name is required", ErrInvalidGrant)
	}
	if g.MaxRedemptions < 0 {
		return fmt.Errorf("%w: max redemptions must be non-negative", ErrInvalidGrant)
	}
	return nil
}

// newID returns a new grant ID: "jit_" followed by 12 random bytes, hex-encoded.
func newID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("read random id bytes: %w", err)
	}
	return "jit_" + hex.EncodeToString(b), nil
}

// newCode generates a one-time redeem secret (32 random bytes, url-safe
// base64) and returns it alongside the hex-encoded sha-256 hash that is
// actually persisted.
func newCode() (code string, codeHash string, err error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", "", fmt.Errorf("read random code bytes: %w", err)
	}
	code = base64.RawURLEncoding.EncodeToString(b)
	return code, hashCode(code), nil
}

// hashCode returns the hex-encoded sha-256 hash of a redeem code.
func hashCode(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// copyGrant returns a deep copy of g so callers cannot mutate internal state
// via the Capabilities slice or Resource.Meta map.
func copyGrant(g *Grant) Grant {
	out := *g
	out.Capabilities = append([]Capability(nil), g.Capabilities...)
	out.Resource.Meta = copyMeta(g.Resource.Meta)
	return out
}

// copyMeta returns a shallow copy of m, preserving nilness.
func copyMeta(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
