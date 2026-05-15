package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/ui"
	"go.uber.org/zap"
)

// FilesLocalListRequest is the JSON body for listing local files.
type FilesLocalListRequest struct {
	Path string `json:"path"`
}

// FilesRemoteListRequest is the JSON body for listing remote files over SSH.
type FilesRemoteListRequest struct {
	SSHUser string       `json:"ssh_user"`
	Record  hosts.Record `json:"record"`
	Path    string       `json:"path"`
}

// FilesCopyRequest is the JSON body for copy between local and remote paths.
type FilesCopyRequest struct {
	Direction  string       `json:"direction"`
	SSHUser    string       `json:"ssh_user"`
	Record     hosts.Record `json:"record"`
	LocalPath  string       `json:"local_path"`
	RemotePath string       `json:"remote_path"`
}

// FilesAgentTransferRequest is the JSON body for agent-mediated file transfer.
type FilesAgentTransferRequest struct {
	SSHUser         string               `json:"ssh_user"`
	AgentLocalPath  string               `json:"agent_local_path,omitempty"`
	AgentRemoteDir  string               `json:"agent_remote_dir,omitempty"`
	SourceRecord    hosts.Record         `json:"source_record"`
	SourcePath      string               `json:"source_path"`
	DestRecord      hosts.Record         `json:"dest_record"`
	DestPath        string               `json:"dest_path"`
	Cloud           ui.AgentCloudBackend `json:"cloud"`
	CloudBackendRef *ui.CloudBackendRef  `json:"cloud_backend_ref,omitempty"`
	Credentials     map[string]string    `json:"credentials"`
	KeepObject      bool                 `json:"keep_object,omitempty"`
	MaxRetries      int                  `json:"max_retries,omitempty"`
}

// FilesLocalListResponse is the JSON body for local list results.
type FilesLocalListResponse struct {
	Root    string              `json:"root"`
	Path    string              `json:"path"`
	Entries []ui.LocalFileEntry `json:"entries"`
}

// FilesRemoteListResponse is the JSON body for remote list results.
type FilesRemoteListResponse struct {
	Path    string               `json:"path"`
	Entries []ui.RemoteFileEntry `json:"entries"`
}

// FilesAgentTransferResponse is the JSON body for agent transfer results.
type FilesAgentTransferResponse struct {
	Events []ui.AgentTransferEvent `json:"events"`
}

func (s *Server) localFilesRoot() string {
	if root := strings.TrimSpace(s.opts.LocalFilesRoot); root != "" {
		return root
	}
	return ui.DefaultLocalFilesRoot()
}

// handleFilesLocalList lists files under the configured local browser root.
// @Summary List local directory
// @Tags files
// @Accept json
// @Produce json
// @Param body body FilesLocalListRequest true "path relative to browser root"
// @Success 200 {object} object "root, path, entries"
// @Failure 400 {object} map[string]string
// @Router /api/v1/files/local/list [post]
// @Security BearerAuth
func (s *Server) handleFilesLocalList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FilesLocalListRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	root := s.localFilesRoot()
	resolved, entries, err := ui.ListLocalDirUnderRoot(root, req.Path)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FilesLocalListResponse{
		Root:    root,
		Path:    resolved,
		Entries: entries,
	})
}

// handleFilesRemoteList lists a directory on a remote host over SSH/SFTP.
// @Summary List remote directory
// @Tags files
// @Accept json
// @Produce json
// @Param body body object true "ssh_user, record, path"
// @Success 200 {object} object "path, entries"
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Router /api/v1/files/remote/list [post]
// @Security BearerAuth
func (s *Server) handleFilesRemoteList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FilesRemoteListRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	user := strings.TrimSpace(req.SSHUser)
	if user == "" {
		user = os.Getenv("USER")
	}
	if strings.TrimSpace(req.Record.PrimaryIP) == "" {
		httpError(w, fmt.Errorf("record has no connectable IP"), http.StatusBadRequest)
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		path = "."
	}
	entries, err := ui.RemoteListDir(user, req.Record, path, s.fileClientCache)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FilesRemoteListResponse{
		Path:    path,
		Entries: entries,
	})
}

// handleFilesCopy copies a single file between local (under files root) and remote.
// @Summary Copy file local/remote
// @Tags files
// @Accept json
// @Produce json
// @Param body body object true "direction, ssh_user, record, local_path, remote_path"
// @Success 200 {object} map[string]string "status, local, remote"
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Router /api/v1/files/copy [post]
// @Security BearerAuth
func (s *Server) handleFilesCopy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FilesCopyRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	user := strings.TrimSpace(req.SSHUser)
	if user == "" {
		user = os.Getenv("USER")
	}
	if strings.TrimSpace(req.Record.PrimaryIP) == "" {
		httpError(w, fmt.Errorf("record has no connectable IP"), http.StatusBadRequest)
		return
	}
	localAbs, err := ui.ResolveLocalPathUnderRoot(s.localFilesRoot(), req.LocalPath)
	if err != nil {
		httpError(w, fmt.Errorf("local path: %w", err), http.StatusBadRequest)
		return
	}
	remotePath := strings.TrimSpace(req.RemotePath)
	if remotePath == "" {
		httpError(w, fmt.Errorf("empty remote_path"), http.StatusBadRequest)
		return
	}
	var copyErr error
	switch strings.TrimSpace(req.Direction) {
	case "local_to_remote":
		st, stErr := os.Stat(localAbs) // #nosec G304 -- localAbs is validated under configured root.
		if stErr != nil {
			httpError(w, fmt.Errorf("local path stat: %w", stErr), http.StatusBadRequest)
			return
		}
		if st.IsDir() {
			httpError(w, fmt.Errorf("directory upload is not supported in this action"), http.StatusBadRequest)
			return
		}
		copyErr = ui.RemoteCopyLocalToRemote(user, req.Record, localAbs, remotePath, s.fileClientCache)
	case "remote_to_local":
		if mkErr := os.MkdirAll(filepath.Dir(localAbs), 0o750); mkErr != nil {
			httpError(w, mkErr, http.StatusInternalServerError)
			return
		}
		copyErr = ui.RemoteCopyRemoteToLocal(user, req.Record, remotePath, localAbs, s.fileClientCache)
	default:
		httpError(w, fmt.Errorf("invalid direction: %q", req.Direction), http.StatusBadRequest)
		return
	}
	if copyErr != nil {
		httpError(w, copyErr, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{
		"status": "ok",
		"local":  localAbs,
		"remote": remotePath,
	})
}

// handleFilesAgentTransfer runs A-to-cloud-to-B agent transfer.
// @Summary Agent-based cloud transfer
// @Tags files
// @Accept json
// @Produce json
// @Param body body object true "agent transfer request (see web UI)"
// @Success 200 {object} object "events array"
// @Failure 400 {object} map[string]string
// @Router /api/v1/files/agent-transfer [post]
// @Security BearerAuth
func (s *Server) handleFilesAgentTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FilesAgentTransferRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	signingHints, err := ui.ResolveAgentTransferSigningHints(s.opts.ConfigPath, req.Cloud, req.CloudBackendRef)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if len(req.Credentials) > 0 {
		zap.L().Warn("ignoring direct credentials in honey-managed credential mode", zap.Int("count", len(req.Credentials)))
	}
	transferCfg := ui.LoadTransferConfigFromConfigPath(s.opts.ConfigPath)
	zap.L().Debug("web agent transfer request received",
		zap.String("source_name", req.SourceRecord.Name),
		zap.String("source_provider", req.SourceRecord.Provider),
		zap.String("destination_name", req.DestRecord.Name),
		zap.String("destination_provider", req.DestRecord.Provider),
		zap.String("cloud_provider", strings.TrimSpace(req.Cloud.Provider)),
		zap.String("cloud_bucket", strings.TrimSpace(req.Cloud.Bucket)),
		zap.Bool("signed_url_mode", false),
		zap.Bool("credential_envelope_mode", true),
	)

	if r.URL.Query().Get("stream") == "1" {
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		fl, _ := w.(http.Flusher)
		emit := func(ev ui.AgentTransferEvent) {
			_ = enc.Encode(ev)
			if fl != nil {
				fl.Flush()
			}
		}
		_, err := ui.RunAgentTransferWithFallback(
			r.Context(),
			s.fileClientCache,
			req.SSHUser,
			strings.TrimSpace(req.AgentLocalPath),
			strings.TrimSpace(s.opts.AgentBinaryPath),
			strings.TrimSpace(s.opts.AgentBuildCacheDir),
			strings.TrimSpace(req.AgentRemoteDir),
			req.SourceRecord,
			req.DestRecord,
			req.SourcePath,
			req.DestPath,
			req.Cloud,
			req.KeepObject,
			req.MaxRetries,
			signingHints,
			transferCfg,
			emit,
		)
		if err != nil {
			emit(ui.AgentTransferEvent{
				Stage:     "fatal_error",
				Success:   false,
				Error:     err.Error(),
				Timestamp: time.Now().UTC(),
			})
		}
		return
	}

	events, err := ui.RunAgentTransferWithFallback(
		r.Context(),
		s.fileClientCache,
		req.SSHUser,
		strings.TrimSpace(req.AgentLocalPath),
		strings.TrimSpace(s.opts.AgentBinaryPath),
		strings.TrimSpace(s.opts.AgentBuildCacheDir),
		strings.TrimSpace(req.AgentRemoteDir),
		req.SourceRecord,
		req.DestRecord,
		req.SourcePath,
		req.DestPath,
		req.Cloud,
		req.KeepObject,
		req.MaxRetries,
		signingHints,
		transferCfg,
		nil,
	)
	if err != nil {
		status := http.StatusBadGateway
		if ui.IsAgentTransferValidationError(err) {
			status = http.StatusBadRequest
		}
		httpError(w, fmt.Errorf("agent transfer: %w", err), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FilesAgentTransferResponse{Events: events})
}
