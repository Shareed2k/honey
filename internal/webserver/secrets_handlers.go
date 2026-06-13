package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/cuetry/secrets"
	"github.com/shareed2k/honey/internal/cuetry/secrets/stack"
)

type secretEncryptRequest struct {
	Plaintext string `json:"plaintext"`
}

type secretEncryptResponse struct {
	Encrypted string `json:"encrypted"`
}

func (s *Server) handleSecretsEncrypt(w http.ResponseWriter, r *http.Request) {
	var req secretEncryptRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}

	if strings.TrimSpace(req.Plaintext) == "" {
		httpError(w, fmt.Errorf("plaintext is required"), http.StatusBadRequest)
		return
	}

	cuetryOpts := cuetry.SecretResolverOptionsFromHoney(s.opts.Config)
	opts := secrets.Options{
		SymmetricDataKey: cuetryOpts.SymmetricDataKey,
		SecretsProvider:  cuetryOpts.SecretsProvider,
		EncryptedKey:     cuetryOpts.EncryptedKey,
		AgeIdentityFile:  cuetryOpts.AgeIdentityFile,
	}

	key, err := secrets.ResolveStackDataKey(r.Context(), opts)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	secureRef, err := stack.FormatSecureRef(key, req.Plaintext)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(secretEncryptResponse{Encrypted: secureRef})
}

// SealSecretRequest is the request payload for sealing a secret.
type SealSecretRequest struct {
	Plaintext string `json:"plaintext"`
}

// SealSecretResponse is the response payload containing the sealed secret.
type SealSecretResponse struct {
	Sealed string `json:"sealed"`
}

func (s *Server) handleSecretsSeal(w http.ResponseWriter, r *http.Request) {
	var req SealSecretRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}

	o := cuetry.SecretResolverOptionsFromHoney(s.opts.Config)
	opts := secrets.Options{
		SymmetricDataKey: o.SymmetricDataKey,
		SecretsProvider:  o.SecretsProvider,
		EncryptedKey:     o.EncryptedKey,
		AgeIdentityFile:  o.AgeIdentityFile,
	}

	ref, err := secrets.Seal(r.Context(), opts, req.Plaintext)
	if err != nil {
		httpError(w, fmt.Errorf("seal failed: %w", err), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(SealSecretResponse{Sealed: ref})
}
