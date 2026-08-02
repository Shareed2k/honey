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
	"github.com/shareed2k/honey/internal/safepath"
)

// handleFilesRemoteUpload handles raw binary streaming upload to the remote host.
// @Summary Upload file to remote host
// @Tags files
// @Accept application/octet-stream
// @Produce json
// @Param ssh_user query string false "SSH user"
// @Param record query string true "JSON-encoded Record"
// @Param path query string true "Remote destination path"
// @Router /api/v1/files/remote/upload [post]
// @Security BearerAuth
func (f *FilesAPI) handleFilesRemoteUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f.handleFilesStreamTransfer(w, r, "upload")
}

// handleFilesRemoteDownload handles raw binary streaming download from the remote host.
// @Summary Download file from remote host
// @Tags files
// @Produce application/octet-stream
// @Param ssh_user query string false "SSH user"
// @Param record query string true "JSON-encoded Record"
// @Param path query string true "Remote source path"
// @Router /api/v1/files/remote/download [get]
// @Security BearerAuth
func (f *FilesAPI) handleFilesRemoteDownload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	f.handleFilesStreamTransfer(w, r, "download")
}

func (f *FilesAPI) handleFilesStreamTransfer(w http.ResponseWriter, r *http.Request, direction string) {
	q := r.URL.Query()
	user := f.sshUser(q.Get("ssh_user"))
	path := strings.TrimSpace(q.Get("path"))

	if path == "" {
		httpError(w, fmt.Errorf("missing path parameter"), http.StatusBadRequest)
		return
	}

	recordJSON := q.Get("record")
	if recordJSON == "" {
		httpError(w, fmt.Errorf("missing record parameter"), http.StatusBadRequest)
		return
	}

	var record hosts.Record
	if err := json.Unmarshal([]byte(recordJSON), &record); err != nil {
		httpError(w, fmt.Errorf("invalid record JSON: %w", err), http.StatusBadRequest)
		return
	}

	if !record.IsConnectable() {
		httpError(w, fmt.Errorf("record is not connectable"), http.StatusBadRequest)
		return
	}

	// We use the same dialer as RemoteListDir, but we don't have direct access to SFTP streams
	// via HostClient yet. As a workaround, we'll write to a temp file and use the existing HostClient.Upload/Download.
	client, err := f.fileClientCache.GetOrDial(user, record)
	if err != nil {
		httpError(w, err, http.StatusBadGateway)
		return
	}
	// We do NOT close the client here since it's managed by the cache.

	tmpFile, err := os.CreateTemp("", "honey-transfer-*")
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	tmpName := tmpFile.Name()
	defer os.Remove(tmpName)

	switch direction {
	case "upload":
		defer r.Body.Close()
		if _, err := io.Copy(tmpFile, r.Body); err != nil {
			tmpFile.Close()
			httpError(w, err, http.StatusBadGateway)
			return
		}
		// Ensure all bytes are written to disk before closing so upload sees the complete file
		tmpFile.Sync()
		tmpFile.Close()

		if err := client.Upload(tmpName, path); err != nil {
			httpError(w, err, http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"success":true}`))

	case "download":
		tmpFile.Close() // Close before Download writes to it

		if err := client.Download(path, tmpName); err != nil {
			httpError(w, err, http.StatusBadGateway)
			return
		}

		downloadFile, err := safepath.Open(tmpName)
		if err != nil {
			httpError(w, err, http.StatusInternalServerError)
			return
		}
		defer downloadFile.Close()

		w.Header().Set("Content-Type", "application/octet-stream")
		// Determine filename from path for disposition
		filename := filepath.Base(path)
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
		if _, err := io.Copy(w, downloadFile); err != nil {
			// Headers already sent, log the error
			return
		}
	}
}
