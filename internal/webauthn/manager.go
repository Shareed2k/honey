package webauthn

import (
	"crypto/sha256"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

// Manager wraps a WebAuthn relying party with per-actor credential and session
// stores (in-memory) and mints biometric tokens on successful assertions. Safe
// for concurrent use.
type Manager struct {
	wa     *webauthn.WebAuthn
	secret []byte
	ttl    time.Duration
	nowFn  func() time.Time

	mu       sync.Mutex
	creds    map[string][]webauthn.Credential // actor → passkeys
	sessions map[string]*webauthn.SessionData // actor → in-flight ceremony
}

// New builds a Manager for the given relying-party id and origin. secret signs
// biometric tokens; ttl bounds their validity.
func New(rpID, rpOrigin string, secret []byte, ttl time.Duration) (*Manager, error) {
	wa, err := webauthn.New(&webauthn.Config{
		RPID:          rpID,
		RPDisplayName: "honey",
		RPOrigins:     []string{rpOrigin},
	})
	if err != nil {
		return nil, fmt.Errorf("webauthn: %w", err)
	}
	return &Manager{
		wa:       wa,
		secret:   secret,
		ttl:      ttl,
		nowFn:    time.Now,
		creds:    make(map[string][]webauthn.Credential),
		sessions: make(map[string]*webauthn.SessionData),
	}, nil
}

// actorUser adapts an actor id to the webauthn.User interface.
type actorUser struct {
	actor string
	creds []webauthn.Credential
}

func (u actorUser) WebAuthnID() []byte {
	sum := sha256.Sum256([]byte(u.actor))
	return sum[:]
}
func (u actorUser) WebAuthnName() string                       { return u.actor }
func (u actorUser) WebAuthnDisplayName() string                { return u.actor }
func (u actorUser) WebAuthnCredentials() []webauthn.Credential { return u.creds }

func (m *Manager) user(actor string) actorUser {
	return actorUser{actor: actor, creds: m.creds[actor]}
}

// BeginRegister starts a passkey registration ceremony for actor.
func (m *Manager) BeginRegister(actor string) (*protocol.CredentialCreation, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	opts, session, err := m.wa.BeginRegistration(m.user(actor))
	if err != nil {
		return nil, err
	}
	m.sessions[actor] = session
	return opts, nil
}

// FinishRegister completes registration, storing the new passkey for actor.
func (m *Manager) FinishRegister(actor string, r *http.Request) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[actor]
	if !ok {
		return fmt.Errorf("webauthn: no registration in progress")
	}
	cred, err := m.wa.FinishRegistration(m.user(actor), *session, r)
	if err != nil {
		return err
	}
	delete(m.sessions, actor)
	m.creds[actor] = append(m.creds[actor], *cred)
	return nil
}

// BeginAssert starts an assertion (login) ceremony for actor.
func (m *Manager) BeginAssert(actor string) (*protocol.CredentialAssertion, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.creds[actor]) == 0 {
		return nil, errNoCredentials
	}
	opts, session, err := m.wa.BeginLogin(m.user(actor))
	if err != nil {
		return nil, err
	}
	m.sessions[actor] = session
	return opts, nil
}

// FinishAssert verifies the assertion and, on success, mints a biometric token.
func (m *Manager) FinishAssert(actor string, r *http.Request) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	session, ok := m.sessions[actor]
	if !ok {
		return "", fmt.Errorf("webauthn: no assertion in progress")
	}
	if _, err := m.wa.FinishLogin(m.user(actor), *session, r); err != nil {
		return "", err
	}
	delete(m.sessions, actor)
	return mintToken(m.secret, actor, m.ttl, m.nowFn())
}

// VerifyToken reports whether token is a valid biometric proof for actor.
func (m *Manager) VerifyToken(actor, token string) bool {
	return verifyToken(m.secret, actor, token, m.nowFn())
}
