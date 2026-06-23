package webserver

import (
	"fmt"
	"net/http"
)

// webAuthnEnabled reports whether passkey step-up is configured.
func (s *Server) webAuthnEnabled() bool { return s.opts.WebAuthn != nil }

func (s *Server) handleWebAuthnRegisterBegin(w http.ResponseWriter, r *http.Request) {
	if !s.webAuthnEnabled() {
		httpError(w, fmt.Errorf("webauthn not enabled"), http.StatusNotFound)
		return
	}
	opts, err := s.opts.WebAuthn.BeginRegister(actorFromCtx(r.Context()))
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, opts)
}

func (s *Server) handleWebAuthnRegisterFinish(w http.ResponseWriter, r *http.Request) {
	if !s.webAuthnEnabled() {
		httpError(w, fmt.Errorf("webauthn not enabled"), http.StatusNotFound)
		return
	}
	if err := s.opts.WebAuthn.FinishRegister(actorFromCtx(r.Context()), r); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"status": "registered"})
}

func (s *Server) handleWebAuthnAssertBegin(w http.ResponseWriter, r *http.Request) {
	if !s.webAuthnEnabled() {
		httpError(w, fmt.Errorf("webauthn not enabled"), http.StatusNotFound)
		return
	}
	opts, err := s.opts.WebAuthn.BeginAssert(actorFromCtx(r.Context()))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, opts)
}

func (s *Server) handleWebAuthnAssertFinish(w http.ResponseWriter, r *http.Request) {
	if !s.webAuthnEnabled() {
		httpError(w, fmt.Errorf("webauthn not enabled"), http.StatusNotFound)
		return
	}
	token, err := s.opts.WebAuthn.FinishAssert(actorFromCtx(r.Context()), r)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	// The biometric token is replayed by the client as X-Honey-Biometric on the
	// subsequent require_biometric run.
	writeJSON(w, map[string]string{"biometric_token": token})
}
