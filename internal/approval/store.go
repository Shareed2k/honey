// Package approval holds pending recipe runs that an OPA policy flagged as
// require_approval, until an authorized actor approves or denies them. The store
// is the mechanism; whether approval is required and who may approve is decided
// by policy. The default store is in-memory with a TTL; runs not decided within
// the TTL expire.
package approval

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"sync"
	"time"
)

// Status is the lifecycle state of a pending run.
type Status string

// Pending-run lifecycle states.
const (
	StatusPending  Status = "pending"
	StatusApproved Status = "approved"
	StatusDenied   Status = "denied"
)

// Record is one pending run awaiting a decision.
type Record struct {
	ID        string    `json:"id"`
	Actor     string    `json:"actor"`
	Recipe    string    `json:"recipe"`
	Hosts     []string  `json:"hosts,omitempty"`
	Reason    string    `json:"reason,omitempty"`
	Status    Status    `json:"status"`
	Approver  string    `json:"approver,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	DecidedAt time.Time `json:"decided_at"`
}

// Store keeps pending runs in memory with a TTL. Safe for concurrent use.
type Store struct {
	mu    sync.Mutex
	recs  map[string]*Record
	ttl   time.Duration
	nowFn func() time.Time
	idFn  func() string
}

// NewStore returns an in-memory store. A zero ttl disables expiry.
func NewStore(ttl time.Duration) *Store {
	return &Store{
		recs:  make(map[string]*Record),
		ttl:   ttl,
		nowFn: time.Now,
		idFn:  randomID,
	}
}

// Create records a new pending run and returns it.
func (s *Store) Create(actor, recipe string, hosts []string, reason string) *Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	rec := &Record{
		ID:        s.idFn(),
		Actor:     actor,
		Recipe:    recipe,
		Hosts:     hosts,
		Reason:    reason,
		Status:    StatusPending,
		CreatedAt: s.nowFn(),
	}
	s.recs[rec.ID] = rec
	return rec
}

// Get returns a copy of the record and whether it exists (and is unexpired).
func (s *Store) Get(id string) (Record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	rec, ok := s.recs[id]
	if !ok {
		return Record{}, false
	}
	return *rec, true
}

// List returns copies of all unexpired records.
func (s *Store) List() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	out := make([]Record, 0, len(s.recs))
	for _, rec := range s.recs {
		out = append(out, *rec)
	}
	return out
}

// Decide marks a pending record approved or denied by approver. It errors if the
// record is missing or already decided.
func (s *Store) Decide(id, approver string, approve bool) (Record, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.gcLocked()
	rec, ok := s.recs[id]
	if !ok {
		return Record{}, fmt.Errorf("approval %q not found", id)
	}
	if rec.Status != StatusPending {
		return Record{}, fmt.Errorf("approval %q already %s", id, rec.Status)
	}
	if approve {
		rec.Status = StatusApproved
	} else {
		rec.Status = StatusDenied
	}
	rec.Approver = approver
	rec.DecidedAt = s.nowFn()
	return *rec, nil
}

// gcLocked drops expired records. Caller holds the lock.
func (s *Store) gcLocked() {
	if s.ttl <= 0 {
		return
	}
	cutoff := s.nowFn().Add(-s.ttl)
	for id, rec := range s.recs {
		if rec.CreatedAt.Before(cutoff) {
			delete(s.recs, id)
		}
	}
}

func randomID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return fmt.Sprintf("appr-%d", time.Now().UnixNano())
	}
	return "appr_" + hex.EncodeToString(b)
}
