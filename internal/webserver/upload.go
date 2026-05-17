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
)

// UploadResponse is the non-stream JSON body for POST /api/v1/upload.
type UploadResponse struct {
	Results []ui.HostExecResult `json:"results"`
}

// UploadRequestMeta is the JSON in multipart field "meta" for POST /api/v1/upload.
type UploadRequestMeta struct {
	SSHUser    string       `json:"ssh_user"`
	RemotePath string       `json:"remote_path"`
	Record     hosts.Record `json:"record"`
}

// handleUpload accepts multipart form: meta (JSON UploadRequestMeta) + file; optional query stream=1 for NDJSON progress.
// @Summary Upload file to remote
// @Tags files
// @Accept multipart/form-data
// @Produce json
// @Param meta formData string true "JSON UploadRequestMeta: ssh_user, remote_path, record (hosts.Record)"
// @Param file formData file true "File contents"
// @Param stream query int false "set to 1 for NDJSON streaming response"
// @Success 200 {object} UploadResponse "JSON body; NDJSON progress lines when stream=1"
// @Failure 400 {object} map[string]string
// @Router /api/v1/upload [post]
// @Security BearerAuth
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(s.opts.MaxUploadSize); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	metaStr := r.FormValue("meta")
	if strings.TrimSpace(metaStr) == "" {
		httpError(w, fmt.Errorf("missing form field meta (JSON)"), http.StatusBadRequest)
		return
	}
	var meta UploadRequestMeta
	if err := json.Unmarshal([]byte(metaStr), &meta); err != nil {
		httpError(w, fmt.Errorf("meta json: %w", err), http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(meta.RemotePath) == "" {
		httpError(w, fmt.Errorf("empty remote_path"), http.StatusBadRequest)
		return
	}
	if strings.Contains(meta.RemotePath, "..") {
		httpError(w, fmt.Errorf("invalid remote_path"), http.StatusBadRequest)
		return
	}
	user := strings.TrimSpace(meta.SSHUser)
	if user == "" {
		user = os.Getenv("USER")
	}
	f, hdr, err := r.FormFile("file")
	if err != nil {
		httpError(w, fmt.Errorf("file: %w", err), http.StatusBadRequest)
		return
	}
	defer func() { _ = f.Close() }()

	tmpDir, err := os.MkdirTemp("", "honey-web-upload-*")
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()
	base := "upload"
	if hdr != nil && hdr.Filename != "" {
		base = filepath.Base(hdr.Filename)
	}
	localPath := filepath.Clean(filepath.Join(tmpDir, base))
	out, err := os.Create(localPath) // #nosec G304 -- localPath is securely joined in a temporary directory
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if _, err := io.Copy(out, io.LimitReader(f, s.opts.MaxUploadSize)); err != nil {
		_ = out.Close()
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if err := out.Close(); err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	rec := meta.Record
	k8sPod := rec.Provider == "k8s" && strings.EqualFold(rec.Meta["kind"], "pod")
	if strings.TrimSpace(rec.PrimaryIP) == "" && !k8sPod {
		httpError(w, fmt.Errorf("record has no connectable target"), http.StatusBadRequest)
		return
	}

	st, err := os.Stat(localPath)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	if r.URL.Query().Get("stream") == "1" {
		s.handleUploadStream(w, user, rec, localPath, meta.RemotePath, st.Size())
		return
	}

	results, err := ui.ExecuteSFTPUploadParallel(user, []hosts.Record{rec}, localPath, meta.RemotePath, 1)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	if len(results) == 0 {
		httpError(w, fmt.Errorf("upload did not run (record may be missing IP or k8s pod metadata)"), http.StatusBadRequest)
		return
	}
	for _, res := range results {
		if !res.Success {
			httpError(w, fmt.Errorf("%s: %s", res.Name, strings.TrimSpace(res.ErrMsg)), http.StatusBadGateway)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(UploadResponse{Results: results})
}

// handleUploadStream streams NDJSON progress while copying the saved file to the host over SFTP.
func (s *Server) handleUploadStream(w http.ResponseWriter, user string, rec hosts.Record, localPath, remotePath string, fileSize int64) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, `{"error":"streaming upload requires http.Flusher"}`, http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	writeLine := func(v any) {
		b, mErr := json.Marshal(v)
		if mErr != nil {
			return
		}
		_, _ = w.Write(b)
		_, _ = w.Write([]byte("\n"))
		fl.Flush()
	}

	writeLine(map[string]any{"phase": "sftp_start", "total_bytes": fileSize})

	var lastEmit int64
	var lastAt time.Time
	progress := func(written, total int64) {
		if written <= 0 {
			return
		}
		now := time.Now()
		atEnd := total > 0 && written >= total
		if !atEnd && written-lastEmit < 256<<10 && now.Sub(lastAt) < 200*time.Millisecond {
			return
		}
		lastEmit = written
		lastAt = now
		writeLine(map[string]any{"phase": "sftp", "sent_bytes": written, "total_bytes": total})
	}

	res := ui.RunOneSFTPUploadWithProgress(user, rec, localPath, remotePath, s.fileClientCache, progress)
	if !res.Success {
		writeLine(map[string]any{
			"phase":   "error",
			"message": strings.TrimSpace(res.ErrMsg),
			"result":  res,
		})
		return
	}
	writeLine(map[string]any{"phase": "done", "results": []ui.HostExecResult{res}})
}
