package intercept

import (
	"context"
	"sync"
	"time"
)

// PersistedSession is the durable record of one brokered interception
// session: everything the Broker needs to rebuild an execer and tear the
// session down (Stop or the janitor's reap), independent of the process that
// authorized it. TokenHash is the sha256 of the per-session agent token; the
// plaintext token itself is never persisted or logged.
type PersistedSession struct {
	// ID is the opaque session identifier.
	ID string
	// Actor is the authenticated subject that owns the session.
	Actor string
	// Cluster is the target Kubernetes cluster name.
	Cluster string
	// Namespace is the target pod's namespace.
	Namespace string
	// Pod is the target pod name.
	Pod string
	// Container is the ephemeral agent container name.
	Container string
	// Modes is the set of active interception capabilities (as their string
	// names, e.g. "egress", "incoming", "files").
	Modes []string
	// AgentImage is the operator-configured interception agent image.
	AgentImage string
	// TokenHash is the sha256 of the per-session agent token. Opaque: never
	// logged and never compared with anything but constant-time comparison.
	TokenHash []byte
	// StartedAt is when the session was authorized.
	StartedAt time.Time
	// ExpiresAt is the absolute time the janitor reaps this session.
	ExpiresAt time.Time
}

// SessionStore persists brokered interception sessions so they survive a
// honey web restart: the janitor can reap a persisted-expired session and
// /stop can find a session authorized by a prior process. Implementations
// must be safe for concurrent use.
type SessionStore interface {
	// Save upserts ps: a session with the same ID is replaced.
	Save(ctx context.Context, ps PersistedSession) error
	// Get returns the session with the given id. A missing id is not an
	// error: it returns a zero PersistedSession and ok == false.
	Get(ctx context.Context, id string) (PersistedSession, bool, error)
	// Delete removes the session with the given id and reports whether it
	// actually removed a row (existed == true). Deleting a missing id is not
	// an error; it returns (false, nil). This compare-and-delete return value
	// lets a caller distinguish "I tore this session down" from "someone else
	// already did", so two concurrent teardowns of the same session (e.g. a
	// Stop racing the janitor's reap of the same just-expired session) signal
	// the agent and audit the stop exactly once.
	Delete(ctx context.Context, id string) (bool, error)
	// List returns every persisted session.
	List(ctx context.Context) ([]PersistedSession, error)
}

// memStore is an in-memory SessionStore backed by a map guarded by a
// sync.RWMutex. It is the default store (no configuration required) and the
// reference implementation the sql-backed stores are tested against via
// runStoreConformance. Every session is deep-copied on the way in and out so
// a caller mutating a PersistedSession it passed to Save, or one it got back
// from Get/List, can never corrupt the stored state.
type memStore struct {
	mu       sync.RWMutex
	sessions map[string]PersistedSession
}

// newMemStore constructs an empty memStore.
func newMemStore() *memStore {
	return &memStore{sessions: make(map[string]PersistedSession)}
}

// NewMemStore returns an in-memory SessionStore safe for concurrent use. It is
// the default registry when no persistent store is configured; callers that own
// their own registry (e.g. the web server's browser-interception registry when
// no Broker is present) construct one with this.
func NewMemStore() SessionStore {
	return newMemStore()
}

// Save stores a deep copy of ps, replacing any existing session with the
// same ID.
func (m *memStore) Save(_ context.Context, ps PersistedSession) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[ps.ID] = clonePersistedSession(ps)
	return nil
}

// Get returns a deep copy of the stored session with the given id, or
// ok == false if none exists.
func (m *memStore) Get(_ context.Context, id string) (PersistedSession, bool, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	ps, ok := m.sessions[id]
	if !ok {
		return PersistedSession{}, false, nil
	}
	return clonePersistedSession(ps), true, nil
}

// Delete removes the session with the given id and reports whether it
// existed. It is not an error if no such session exists.
func (m *memStore) Delete(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.sessions[id]; !ok {
		return false, nil
	}
	delete(m.sessions, id)
	return true, nil
}

// List returns a deep copy of every stored session.
func (m *memStore) List(_ context.Context) ([]PersistedSession, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]PersistedSession, 0, len(m.sessions))
	for _, ps := range m.sessions {
		out = append(out, clonePersistedSession(ps))
	}
	return out, nil
}

// clonePersistedSession returns a deep copy of ps: the Modes and TokenHash
// slices are copied so the clone shares no backing array with ps, in either
// direction (store-to-caller or caller-to-store).
func clonePersistedSession(ps PersistedSession) PersistedSession {
	out := ps
	if ps.Modes != nil {
		out.Modes = append([]string(nil), ps.Modes...)
	}
	if ps.TokenHash != nil {
		out.TokenHash = append([]byte(nil), ps.TokenHash...)
	}
	return out
}
