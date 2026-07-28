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

	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/safepath"
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

// FilesRemoteStatRequest is the JSON body for stating a remote file over SSH.
type FilesRemoteStatRequest struct {
	SSHUser string       `json:"ssh_user"`
	Record  hosts.Record `json:"record"`
	Path    string       `json:"path"`
}

// FilesRemoteMkdirRequest is the JSON body for creating a remote directory over SSH.
type FilesRemoteMkdirRequest struct {
	SSHUser string       `json:"ssh_user"`
	Record  hosts.Record `json:"record"`
	Path    string       `json:"path"`
}

// FilesRemoteRemoveRequest is the JSON body for removing a remote file/directory over SSH.
type FilesRemoteRemoveRequest struct {
	SSHUser   string       `json:"ssh_user"`
	Record    hosts.Record `json:"record"`
	Path      string       `json:"path"`
	Recursive bool         `json:"recursive"`
}

// FilesRemoteStatResponse is the JSON body for remote stat results.
type FilesRemoteStatResponse struct {
	Entry engine.RemoteFileEntry `json:"entry"`
}

// FilesRemoteMkdirResponse is the JSON body for remote mkdir results.
type FilesRemoteMkdirResponse struct {
	Success bool `json:"success"`
}

// FilesRemoteRemoveResponse is the JSON body for remote remove results.
type FilesRemoteRemoveResponse struct {
	Success bool `json:"success"`
}

// FilesAgentTransferRequest is the JSON body for agent-mediated file transfer.
type FilesAgentTransferRequest struct {
	SSHUser         string                   `json:"ssh_user"`
	AgentLocalPath  string                   `json:"agent_local_path,omitempty"`
	AgentRemoteDir  string                   `json:"agent_remote_dir,omitempty"`
	SourceRecord    hosts.Record             `json:"source_record"`
	SourcePath      string                   `json:"source_path"`
	DestRecord      hosts.Record             `json:"dest_record"`
	DestPath        string                   `json:"dest_path"`
	Cloud           engine.AgentCloudBackend `json:"cloud"`
	CloudBackendRef *engine.CloudBackendRef  `json:"cloud_backend_ref,omitempty"`
	Credentials     map[string]string        `json:"credentials"`
	KeepObject      bool                     `json:"keep_object,omitempty"`
	MaxRetries      int                      `json:"max_retries,omitempty"`
}

// FilesLocalListResponse is the JSON body for local list results.
type FilesLocalListResponse struct {
	Root    string              `json:"root"`
	Path    string              `json:"path"`
	Entries []ui.LocalFileEntry `json:"entries"`
}

// FilesRemoteListResponse is the JSON body for remote list results.
type FilesRemoteListResponse struct {
	Path    string                   `json:"path"`
	Entries []engine.RemoteFileEntry `json:"entries"`
}

// FilesAgentTransferResponse is the JSON body for agent transfer results.
type FilesAgentTransferResponse struct {
	Events []engine.AgentTransferEvent `json:"events"`
}

// FilesCopyResponse is returned by POST /api/v1/files/copy.
type FilesCopyResponse struct {
	Status string `json:"status"`
	Local  string `json:"local"`
	Remote string `json:"remote"`
}

func (f *FilesAPI) localFilesRoot() string {
	if root := strings.TrimSpace(f.opts.LocalFilesRoot); root != "" {
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
// @Success 200 {object} FilesLocalListResponse
// @Failure 400 {object} map[string]string
// @Router /api/v1/files/local/list [post]
// @Security BearerAuth
func (f *FilesAPI) handleFilesLocalList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FilesLocalListRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	root := f.localFilesRoot()
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
// @Param body body FilesRemoteListRequest true "ssh_user, record, path"
// @Success 200 {object} FilesRemoteListResponse
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Router /api/v1/files/remote/list [post]
// @Security BearerAuth
func (f *FilesAPI) handleFilesRemoteList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FilesRemoteListRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	user := f.sshUser(req.SSHUser)
	if !req.Record.IsConnectable() {
		httpError(w, fmt.Errorf("record is not connectable (need IP, k8s pod, or docker container)"), http.StatusBadRequest)
		return
	}
	path := strings.TrimSpace(req.Path)
	if path == "" {
		path = "."
	}
	entries, err := ui.RemoteListDir(user, req.Record, path, f.fileClientCache)
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
// @Param body body FilesCopyRequest true "copy between local and remote paths"
// @Success 200 {object} FilesCopyResponse
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Router /api/v1/files/copy [post]
// @Security BearerAuth
func (f *FilesAPI) handleFilesCopy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FilesCopyRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	user := f.sshUser(req.SSHUser)
	if !req.Record.IsConnectable() {
		httpError(w, fmt.Errorf("record is not connectable (need IP, k8s pod, or docker container)"), http.StatusBadRequest)
		return
	}
	localAbs, err := safepath.JoinUnder(f.localFilesRoot(), req.LocalPath)
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
		st, stErr := safepath.Stat(localAbs)
		if stErr != nil {
			httpError(w, fmt.Errorf("local path stat: %w", stErr), http.StatusBadRequest)
			return
		}
		if st.IsDir() {
			httpError(w, fmt.Errorf("directory upload is not supported in this action"), http.StatusBadRequest)
			return
		}
		copyErr = ui.RemoteCopyLocalToRemote(user, req.Record, localAbs, remotePath, f.fileClientCache)
	case "remote_to_local":
		if mkErr := os.MkdirAll(filepath.Dir(localAbs), 0o750); mkErr != nil {
			httpError(w, mkErr, http.StatusInternalServerError)
			return
		}
		copyErr = ui.RemoteCopyRemoteToLocal(user, req.Record, remotePath, localAbs, f.fileClientCache)
	default:
		httpError(w, fmt.Errorf("invalid direction: %q", req.Direction), http.StatusBadRequest)
		return
	}
	if copyErr != nil {
		httpError(w, copyErr, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FilesCopyResponse{
		Status: "ok",
		Local:  localAbs,
		Remote: remotePath,
	})
}

// handleFilesAgentTransfer runs A-to-cloud-to-B agent transfer.
// @Summary Agent-based cloud transfer
// @Tags files
// @Accept json
// @Produce json
// @Param body body FilesAgentTransferRequest true "agent transfer request"
// @Success 200 {object} FilesAgentTransferResponse
// @Failure 400 {object} map[string]string
// @Router /api/v1/files/agent-transfer [post]
// @Security BearerAuth
func (f *FilesAPI) handleFilesAgentTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FilesAgentTransferRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	user := f.sshUser(req.SSHUser)
	signingHints, err := engine.ResolveAgentTransferSigningHints(f.opts.ConfigPath, req.Cloud, req.CloudBackendRef)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if len(req.Credentials) > 0 {
		zap.L().Warn("ignoring direct credentials in honey-managed credential mode", zap.Int("count", len(req.Credentials)))
	}
	transferCfg := engine.LoadTransferConfigFromConfigPath(f.opts.ConfigPath)
	zap.L().Debug(
		"web agent transfer request received",
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
		transferStart := time.Now()
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		enc := json.NewEncoder(w)
		fl, _ := w.(http.Flusher)
		emit := func(ev engine.AgentTransferEvent) {
			_ = enc.Encode(ev)
			if fl != nil {
				fl.Flush()
			}
		}
		_, err := engine.RunAgentTransferWithFallback(
			r.Context(),
			f.fileClientCache,
			user,
			strings.TrimSpace(req.AgentLocalPath),
			strings.TrimSpace(f.opts.AgentBinaryPath),
			strings.TrimSpace(f.opts.AgentBuildCacheDir),
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
			emit(engine.AgentTransferEvent{
				Stage:     "fatal_error",
				Success:   false,
				Error:     err.Error(),
				Timestamp: time.Now().UTC(),
			})
		}
		if f.metrics != nil {
			status := "ok"
			if err != nil {
				status = "error"
			}
			f.metrics.ObserveAgentTransfer(status, time.Since(transferStart))
		}
		return
	}

	transferStart := time.Now()
	events, err := engine.RunAgentTransferWithFallback(
		r.Context(),
		f.fileClientCache,
		user,
		strings.TrimSpace(req.AgentLocalPath),
		strings.TrimSpace(f.opts.AgentBinaryPath),
		strings.TrimSpace(f.opts.AgentBuildCacheDir),
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
		if f.metrics != nil {
			f.metrics.ObserveAgentTransfer("error", time.Since(transferStart))
		}
		status := http.StatusBadGateway
		if engine.IsAgentTransferValidationError(err) {
			status = http.StatusBadRequest
		}
		httpError(w, fmt.Errorf("agent transfer: %w", err), status)
		return
	}
	if f.metrics != nil {
		f.metrics.ObserveAgentTransfer("ok", time.Since(transferStart))
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FilesAgentTransferResponse{Events: events})
}

// handleFilesRemoteStat stats a file or directory on a remote host over SSH/SFTP.
// @Summary Stat remote file
// @Tags files
// @Accept json
// @Produce json
// @Param body body FilesRemoteStatRequest true "ssh_user, record, path"
// @Success 200 {object} FilesRemoteStatResponse
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Router /api/v1/files/remote/stat [post]
// @Security BearerAuth
func (f *FilesAPI) handleFilesRemoteStat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FilesRemoteStatRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	user := f.sshUser(req.SSHUser)
	if !req.Record.IsConnectable() {
		httpError(w, fmt.Errorf("record is not connectable"), http.StatusBadRequest)
		return
	}
	entry, err := ui.RemoteStat(user, req.Record, req.Path, f.fileClientCache)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FilesRemoteStatResponse{Entry: entry})
}

// handleFilesRemoteMkdir creates a directory on a remote host over SSH/SFTP.
// @Summary Mkdir remote directory
// @Tags files
// @Accept json
// @Produce json
// @Param body body FilesRemoteMkdirRequest true "ssh_user, record, path"
// @Success 200 {object} FilesRemoteMkdirResponse
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Router /api/v1/files/remote/mkdir [post]
// @Security BearerAuth
func (f *FilesAPI) handleFilesRemoteMkdir(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FilesRemoteMkdirRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	user := f.sshUser(req.SSHUser)
	if !req.Record.IsConnectable() {
		httpError(w, fmt.Errorf("record is not connectable"), http.StatusBadRequest)
		return
	}
	err := ui.RemoteMkdirAll(user, req.Record, req.Path, f.fileClientCache)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FilesRemoteMkdirResponse{Success: true})
}

// handleFilesRemoteRemove removes a file or directory on a remote host over SSH/SFTP.
// @Summary Remove remote file
// @Tags files
// @Accept json
// @Produce json
// @Param body body FilesRemoteRemoveRequest true "ssh_user, record, path, recursive"
// @Success 200 {object} FilesRemoteRemoveResponse
// @Failure 400 {object} map[string]string
// @Failure 502 {object} map[string]string
// @Router /api/v1/files/remote/remove [post]
// @Security BearerAuth
func (f *FilesAPI) handleFilesRemoteRemove(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req FilesRemoteRemoveRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	user := f.sshUser(req.SSHUser)
	if !req.Record.IsConnectable() {
		httpError(w, fmt.Errorf("record is not connectable"), http.StatusBadRequest)
		return
	}
	err := ui.RemoteRemove(user, req.Record, req.Path, req.Recursive, f.fileClientCache)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(FilesRemoteRemoveResponse{Success: true})
}
