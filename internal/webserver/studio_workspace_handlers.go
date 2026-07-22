package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/webserver/workspacestore"
)

// workspaceStore is the seam the handlers depend on. Defined here (consumer side)
// so a fake can be injected in tests; the concrete impl is workspacestore.Local.
type workspaceStore interface {
	Load(ctx context.Context) (workspacestore.Workspace, error)
	Save(ctx context.Context, ws workspacestore.Workspace) error
}

var _ workspaceStore = (*workspacestore.Local)(nil)

const (
	maxWorkspaceBytes = 256 << 10
	maxOpenRecipes    = 64
)

// handleGetStudioWorkspace returns the persisted studio workspace, or a zero
// document (client falls back to the default layout) when none is stored.
// @Summary Get the studio workspace layout
// @Tags studio
// @Produce json
// @Success 200 {object} workspacestore.Workspace
// @Failure 500 {object} map[string]string
// @Router /api/v1/studio/workspace [get]
// @Security BearerAuth
func (s *Server) handleGetStudioWorkspace(w http.ResponseWriter, r *http.Request) {
	ws, err := s.workspace.Load(r.Context())
	if err != nil {
		zap.L().Error("load studio workspace", zap.Error(err))
		httpError(w, errors.New("failed to load workspace"), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ws)
}

// handlePutStudioWorkspace stores the studio workspace blob.
// @Summary Save the studio workspace layout
// @Tags studio
// @Accept json
// @Produce json
// @Param body body workspacestore.Workspace true "workspace"
// @Success 204
// @Failure 400 {object} map[string]string
// @Failure 500 {object} map[string]string
// @Router /api/v1/studio/workspace [put]
// @Security BearerAuth
func (s *Server) handlePutStudioWorkspace(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxWorkspaceBytes)
	var ws workspacestore.Workspace
	if err := json.NewDecoder(r.Body).Decode(&ws); err != nil {
		httpError(w, errors.New("invalid workspace payload"), http.StatusBadRequest)
		return
	}
	if len(ws.OpenRecipes) > maxOpenRecipes {
		httpError(w, errors.New("too many open recipes"), http.StatusBadRequest)
		return
	}
	if err := s.workspace.Save(r.Context(), ws); err != nil {
		zap.L().Error("save studio workspace", zap.Error(err))
		httpError(w, errors.New("failed to save workspace"), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
