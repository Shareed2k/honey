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

	"github.com/shareed2k/honey/internal/cloudtransfer"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/ui"
	"go.uber.org/zap"
)

type filesLocalListRequest struct {
	Path string `json:"path"`
}

type filesRemoteListRequest struct {
	SSHUser string       `json:"ssh_user"`
	Record  hosts.Record `json:"record"`
	Path    string       `json:"path"`
}

type filesCopyRequest struct {
	Direction  string       `json:"direction"`
	SSHUser    string       `json:"ssh_user"`
	Record     hosts.Record `json:"record"`
	LocalPath  string       `json:"local_path"`
	RemotePath string       `json:"remote_path"`
}

type filesAgentTransferRequest struct {
	SSHUser         string                `json:"ssh_user"`
	AgentLocalPath  string                `json:"agent_local_path,omitempty"`
	AgentRemoteDir  string                `json:"agent_remote_dir,omitempty"`
	SourceRecord    hosts.Record          `json:"source_record"`
	SourcePath      string                `json:"source_path"`
	DestRecord      hosts.Record          `json:"dest_record"`
	DestPath        string                `json:"dest_path"`
	Cloud           ui.AgentCloudBackend  `json:"cloud"`
	CloudBackendRef *filesCloudBackendRef `json:"cloud_backend_ref,omitempty"`
	Credentials     map[string]string     `json:"credentials"`
	KeepObject      bool                  `json:"keep_object,omitempty"`
	MaxRetries      int                   `json:"max_retries,omitempty"`
}

type filesCloudBackendRef struct {
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	Index *int   `json:"index,omitempty"`
}

type filesLocalListResponse struct {
	Root    string              `json:"root"`
	Path    string              `json:"path"`
	Entries []ui.LocalFileEntry `json:"entries"`
}

type filesRemoteListResponse struct {
	Path    string               `json:"path"`
	Entries []ui.RemoteFileEntry `json:"entries"`
}

type filesAgentTransferResponse struct {
	Events []ui.AgentTransferEvent `json:"events"`
}

func (s *Server) localFilesRoot() string {
	if root := strings.TrimSpace(s.opts.LocalFilesRoot); root != "" {
		return root
	}
	return ui.DefaultLocalFilesRoot()
}

func (s *Server) handleFilesLocalList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req filesLocalListRequest
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
	_ = json.NewEncoder(w).Encode(filesLocalListResponse{
		Root:    root,
		Path:    resolved,
		Entries: entries,
	})
}

func (s *Server) handleFilesRemoteList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req filesRemoteListRequest
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
	_ = json.NewEncoder(w).Encode(filesRemoteListResponse{
		Path:    path,
		Entries: entries,
	})
}

func (s *Server) handleFilesCopy(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req filesCopyRequest
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

func (s *Server) handleFilesAgentTransfer(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req filesAgentTransferRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 4<<20)).Decode(&req); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	signingHints, err := s.resolveTransferCloudSigningHints(req.Cloud, req.CloudBackendRef)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if len(req.Credentials) > 0 {
		zap.L().Warn("ignoring direct credentials in honey-managed credential mode", zap.Int("count", len(req.Credentials)))
	}
	job, err := ui.BuildAgentTransferJob(
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
	)
	if err != nil {
		httpError(w, fmt.Errorf("prepare agent transfer: %w", err), http.StatusBadGateway)
		return
	}
	zap.L().Debug("web agent transfer request received",
		zap.String("source_name", job.Source.Record.Name),
		zap.String("source_provider", job.Source.Record.Provider),
		zap.String("destination_name", job.Destination.Record.Name),
		zap.String("destination_provider", job.Destination.Record.Provider),
		zap.String("cloud_provider", strings.TrimSpace(job.Cloud.Provider)),
		zap.String("cloud_bucket", strings.TrimSpace(job.Cloud.Bucket)),
		zap.Bool("signed_url_mode", false),
		zap.Bool("credential_envelope_mode", true),
		zap.Int("credential_env_count", len(job.CredentialEnv)),
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
		_, err := ui.ExecuteAgentCloudTransferWithEmit(job, s.fileClientCache, emit)
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

	events, err := ui.ExecuteAgentCloudTransfer(job, s.fileClientCache)
	if err != nil {
		status := http.StatusBadGateway
		if ui.IsAgentTransferValidationError(err) {
			status = http.StatusBadRequest
		}
		httpError(w, fmt.Errorf("agent transfer: %w", err), status)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(filesAgentTransferResponse{Events: events})
}

func (s *Server) resolveTransferCloudSigningHints(cloud ui.AgentCloudBackend, ref *filesCloudBackendRef) (cloudtransfer.SigningHints, error) {
	var hints cloudtransfer.SigningHints
	if ref == nil {
		return hints, nil
	}
	kind := strings.ToLower(strings.TrimSpace(ref.Kind))
	if kind == "" {
		return hints, fmt.Errorf("cloud_backend_ref.kind is required")
	}
	cfgPath, err := config.ResolvePath(strings.TrimSpace(s.opts.ConfigPath))
	if err != nil {
		return hints, fmt.Errorf("resolve config path: %w", err)
	}
	if cfgPath == "" {
		return hints, fmt.Errorf("cloud_backend_ref requires a config file (set --config or HONEY_CONFIG)")
	}
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return hints, fmt.Errorf("load config: %w", err)
	}
	provider := cloudtransfer.NormalizeProvider(cloud.Provider)
	switch kind {
	case "aws":
		if provider != "" && provider != "s3" {
			return hints, fmt.Errorf("cloud_backend_ref.kind=aws requires cloud.provider=s3")
		}
		backend, err := pickAWSBackend(cfg.Backends.AWS, ref)
		if err != nil {
			return hints, err
		}
		if p := strings.TrimSpace(backend.Profile); p != "" {
			hints.AWSProfile = p
		}
		if strings.TrimSpace(cloud.Region) == "" {
			if region := strings.TrimSpace(backend.Region); region != "" {
				hints.AWSRegion = region
			}
		}
		return hints, nil
	case "gcp", "googlecloud":
		if provider != "" && provider != "googlecloudstorage" {
			return hints, fmt.Errorf("cloud_backend_ref.kind=gcp requires cloud.provider=googlecloudstorage")
		}
		backend, err := pickGCPBackend(cfg.Backends.GCP, ref)
		if err != nil {
			return hints, err
		}
		hints.GCPProject = strings.TrimSpace(backend.Project)
		return hints, nil
	default:
		return hints, fmt.Errorf("unsupported cloud_backend_ref.kind %q (supported: aws, gcp)", ref.Kind)
	}
}

func pickAWSBackend(backends []config.AWSBackend, ref *filesCloudBackendRef) (config.AWSBackend, error) {
	if len(backends) == 0 {
		return config.AWSBackend{}, fmt.Errorf("no aws backends configured")
	}
	if ref.Index != nil {
		idx := *ref.Index
		if idx < 0 || idx >= len(backends) {
			return config.AWSBackend{}, fmt.Errorf("cloud_backend_ref.index out of range for aws backends")
		}
		return backends[idx], nil
	}
	name := strings.TrimSpace(ref.Name)
	if name != "" {
		for _, b := range backends {
			if strings.EqualFold(strings.TrimSpace(b.Name), name) {
				return b, nil
			}
		}
		return config.AWSBackend{}, fmt.Errorf("aws backend %q not found", name)
	}
	if len(backends) == 1 {
		return backends[0], nil
	}
	return config.AWSBackend{}, fmt.Errorf("multiple aws backends configured; provide cloud_backend_ref.name or index")
}

func pickGCPBackend(backends []config.GCPBackend, ref *filesCloudBackendRef) (config.GCPBackend, error) {
	if len(backends) == 0 {
		return config.GCPBackend{}, fmt.Errorf("no gcp backends configured")
	}
	if ref.Index != nil {
		idx := *ref.Index
		if idx < 0 || idx >= len(backends) {
			return config.GCPBackend{}, fmt.Errorf("cloud_backend_ref.index out of range for gcp backends")
		}
		return backends[idx], nil
	}
	name := strings.TrimSpace(ref.Name)
	if name != "" {
		for _, b := range backends {
			if strings.EqualFold(strings.TrimSpace(b.Name), name) {
				return b, nil
			}
		}
		return config.GCPBackend{}, fmt.Errorf("gcp backend %q not found", name)
	}
	if len(backends) == 1 {
		return backends[0], nil
	}
	return config.GCPBackend{}, fmt.Errorf("multiple gcp backends configured; provide cloud_backend_ref.name or index")
}
