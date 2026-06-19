package webserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/snippets"
)

const maxSnippetName = 200

// snippetsFilePath returns the snippets.json path: beside the resolved config
// file when one exists, else under the runtime state dir.
func snippetsFilePath(configPath string) string {
	if cp, err := config.ResolvePath(strings.TrimSpace(configPath)); err == nil && cp != "" {
		return filepath.Join(filepath.Dir(cp), "snippets.json")
	}
	if dir, err := config.ResolveStateDir(); err == nil && dir != "" {
		return filepath.Join(dir, "snippets.json")
	}
	return "snippets.json"
}

// handleSnippetsList returns all saved exec snippets.
// @Summary List exec snippets
// @Tags snippets
// @Produce json
// @Success 200 {array} snippets.ExecSnippet
// @Failure 500 {object} map[string]string
// @Router /api/v1/snippets [get]
// @Security BearerAuth
func (s *Server) handleSnippetsList(w http.ResponseWriter, r *http.Request) {
	list, err := s.snippetStore.List(r.Context())
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, list)
}

// handleSnippetsSave upserts an exec snippet (creates when id is empty).
// @Summary Save exec snippet
// @Tags snippets
// @Accept json
// @Produce json
// @Param body body snippets.ExecSnippet true "snippet"
// @Success 200 {object} snippets.ExecSnippet
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/snippets [post]
// @Security BearerAuth
func (s *Server) handleSnippetsSave(w http.ResponseWriter, r *http.Request) {
	var snip snippets.ExecSnippet
	if err := json.NewDecoder(io.LimitReader(r.Body, maxWebExecScript)).Decode(&snip); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	if err := validateSnippet(snip); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	saved, err := s.snippetStore.Save(r.Context(), snip)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, saved)
}

// handleSnippetsDelete removes an exec snippet by id.
// @Summary Delete exec snippet
// @Tags snippets
// @Produce json
// @Param id path string true "snippet id"
// @Success 200 {object} StatusResponse
// @Failure 404 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/snippets/{id} [delete]
// @Security BearerAuth
func (s *Server) handleSnippetsDelete(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(chi.URLParam(r, "id"))
	err := s.snippetStore.Delete(r.Context(), id)
	if errors.Is(err, snippets.ErrNotFound) {
		httpError(w, err, http.StatusNotFound)
		return
	}
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, StatusResponse{Status: "ok"})
}

func validateSnippet(snip snippets.ExecSnippet) error {
	if strings.TrimSpace(snip.Name) == "" {
		return fmt.Errorf("snippet name required")
	}
	if len(snip.Name) > maxSnippetName {
		return fmt.Errorf("snippet name too long (max %d)", maxSnippetName)
	}
	if strings.TrimSpace(snip.Command) == "" {
		return fmt.Errorf("snippet command required")
	}
	if len(snip.Command) > maxWebExecScript {
		return fmt.Errorf("snippet command too long (max %d)", maxWebExecScript)
	}
	if snip.Mode != execModeCommand && snip.Mode != execModeScript {
		return fmt.Errorf("invalid mode %q (want %q or %q)", snip.Mode, execModeCommand, execModeScript)
	}
	if strings.TrimSpace(snip.RunAs) != "" {
		if err := cuetry.ValidateRunAsUser(snip.RunAs); err != nil {
			return err
		}
	}
	return nil
}
