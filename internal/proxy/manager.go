package proxy

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"
	"syscall"

	"github.com/google/uuid"
	"github.com/shareed2k/honey/internal/apps"
)

// Manager coordinates all active proxy sessions, saving state to disk and handling TTLs.
type Manager struct {
	state *StateManager
	mu    sync.Mutex
	audit *Logger

	// localSessions keeps track of sessions started by THIS process.
	// This allows us to cleanly call Stop() without sending SIGTERM to ourselves.
	localSessions map[string]*Session
}

// NewManager returns a new initialized proxy manager.
func NewManager(audit *Logger) *Manager {
	return &Manager{
		state:         NewStateManager(),
		audit:         audit,
		localSessions: make(map[string]*Session),
	}
}

func newSessionID() string {
	return uuid.New().String()[:8]
}

// Start starts a proxy session and records it.
func (m *Manager) Start(ctx context.Context, app apps.AppConfig, dialer Dialer, closer io.Closer) (*Session, error) {
	// Enforce singleton: only one active proxy per app name.
	if sessions, err := m.List(); err == nil {
		for _, existing := range sessions {
			if existing.App.Name == app.Name {
				// Stop the existing session for this app (whether local or in another PID)
				_ = m.Stop(existing.ID)
			}
		}
	}

	sessionID := newSessionID()
	var s *Session
	var err error

	if app.Type == apps.AppTypeHTTP {
		s, err = StartHTTPProxy(ctx, app, dialer, sessionID, closer)
	} else {
		s, err = StartTCPProxy(ctx, app, dialer, sessionID, closer)
	}

	if err != nil {
		if m.audit != nil {
			m.audit.Failed(&Session{App: app, ID: sessionID}, err)
		}
		return nil, err
	}

	m.mu.Lock()
	m.localSessions[sessionID] = s
	m.mu.Unlock()

	if err := m.state.Add(*s); err != nil {
		s.Stop()
		if m.audit != nil {
			m.audit.Failed(s, fmt.Errorf("save state: %w", err))
		}
		return nil, fmt.Errorf("failed to save proxy state: %w", err)
	}

	if m.audit != nil {
		m.audit.Started(s)
	}

	// Auto-remove from state when the context is cancelled locally
	go func() {
		<-ctx.Done()
		_ = m.Stop(sessionID)
	}()

	return s, nil
}

// GetLocalSession returns an active session created by this process.
// This allows access to the session's internal state (like the HTTP Handler)
// which is not serialized to the JSON state file.
func (m *Manager) GetLocalSession(id string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.localSessions[id]
}

// GetLocalSessionByApp returns an active session created by this process for the given app name.
func (m *Manager) GetLocalSessionByApp(appName string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.localSessions {
		if s.App.Name == appName {
			return s
		}
	}
	return nil
}

// List returns all active proxy sessions.
func (m *Manager) List() ([]Session, error) {
	return m.state.List()
}

// Stop stops a session by ID. If the session is owned by another process, it sends SIGTERM.
func (m *Manager) Stop(id string) error {
	m.mu.Lock()
	s, ok := m.localSessions[id]
	m.mu.Unlock()

	if ok {
		// Session is local to this process
		s.Stop()
		if m.audit != nil {
			m.audit.Stopped(s)
		}
		m.mu.Lock()
		delete(m.localSessions, id)
		m.mu.Unlock()
		return m.state.Remove(id)
	}

	// Session is owned by another process
	sessions, err := m.state.List()
	if err != nil {
		return err
	}

	var target *Session
	for _, sess := range sessions {
		if sess.ID == id {
			target = &sess
			break
		}
	}

	if target == nil {
		return fmt.Errorf("session %s not found", id)
	}

	if target.PID > 0 {
		process, err := os.FindProcess(target.PID)
		if err == nil {
			// Send SIGTERM to the blocking CLI process
			_ = process.Signal(syscall.SIGTERM)
		}
	}

	if m.audit != nil {
		m.audit.Stopped(target)
	}
	return m.state.Remove(id)
}
