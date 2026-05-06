package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/ui"
)

type uploadRequestMeta struct {
	SSHUser    string       `json:"ssh_user"`
	RemotePath string       `json:"remote_path"`
	Record     hosts.Record `json:"record"`
}

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
	var meta uploadRequestMeta
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
	_ = json.NewEncoder(w).Encode(results)
}
