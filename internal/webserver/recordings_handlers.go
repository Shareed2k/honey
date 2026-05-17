package webserver

import (
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/shareed2k/honey/internal/recordings"
)

// RecordingListEntry is one session recording file in a list response.
type RecordingListEntry struct {
	FileName       string `json:"file_name"`
	ModifiedUnixMS int64  `json:"modified_unix_ms"`
	SizeBytes      int64  `json:"size_bytes"`
	Trigger        string `json:"trigger,omitempty"`
	Mode           string `json:"mode,omitempty"`
	Provider       string `json:"provider,omitempty"`
	HostName       string `json:"host_name,omitempty"`
	HostIP         string `json:"host_ip,omitempty"`
	User           string `json:"user,omitempty"`
}

// RecordingsListResponse is returned by GET /api/v1/recordings.
type RecordingsListResponse struct {
	Items []RecordingListEntry `json:"items"`
}

// RecordingsPlayRequest is the JSON body for POST /api/v1/recordings/play.
type RecordingsPlayRequest struct {
	FileName string `json:"file_name"`
}

// RecordingsPlayResponse is returned by POST /api/v1/recordings/play.
type RecordingsPlayResponse struct {
	FileName string             `json:"file_name"`
	Events   []recordings.Event `json:"events"`
}

// handleRecordingsList lists session recording files with optional filters.
// @Summary List session recordings
// @Tags recordings
// @Produce json
// @Param provider query string false "filter by provider"
// @Param host_name query string false "filter by host name"
// @Param host_ip query string false "filter by host IP"
// @Success 200 {object} RecordingsListResponse
// @Failure 400 {object} map[string]string
// @Router /api/v1/recordings [get]
// @Security BearerAuth
func (s *Server) handleRecordingsList(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	recordDir := strings.TrimSpace(s.opts.RecordDir)
	if recordDir == "" {
		httpError(w, fmt.Errorf("session recording is not enabled"), http.StatusBadRequest)
		return
	}

	provider := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("provider")))
	hostName := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("host_name")))
	hostIP := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("host_ip")))

	root, err := os.OpenRoot(recordDir)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	defer root.Close()

	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	out := make([]RecordingListEntry, 0, len(entries))
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".hrec.jsonl") {
			continue
		}
		st, statErr := root.Stat(name)
		if statErr != nil {
			continue
		}
		entry := RecordingListEntry{
			FileName:       name,
			ModifiedUnixMS: st.ModTime().UnixMilli(),
			SizeBytes:      st.Size(),
		}
		fillRecordingMeta(&entry, recordings.ReadOpenMessage(root, name))
		if provider != "" && strings.ToLower(entry.Provider) != provider {
			continue
		}
		if hostName != "" && strings.ToLower(entry.HostName) != hostName {
			continue
		}
		if hostIP != "" && strings.ToLower(entry.HostIP) != hostIP {
			continue
		}
		out = append(out, entry)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].ModifiedUnixMS > out[j].ModifiedUnixMS
	})
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RecordingsListResponse{Items: out})
}

// handleRecordingsPlay loads events from a recording file.
// @Summary Load recording events
// @Tags recordings
// @Accept json
// @Produce json
// @Param body body RecordingsPlayRequest true "recording file name"
// @Success 200 {object} RecordingsPlayResponse
// @Failure 400 {object} map[string]string
// @Router /api/v1/recordings/play [post]
// @Security BearerAuth
func (s *Server) handleRecordingsPlay(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	recordDir := strings.TrimSpace(s.opts.RecordDir)
	if recordDir == "" {
		httpError(w, fmt.Errorf("session recording is not enabled"), http.StatusBadRequest)
		return
	}
	var body RecordingsPlayRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<20)).Decode(&body); err != nil {
		httpError(w, fmt.Errorf("json: %w", err), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(body.FileName)
	if err := recordings.ValidateBaseName(name); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	events, err := recordings.LoadEvents(recordDir, name)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RecordingsPlayResponse{
		FileName: name,
		Events:   events,
	})
}

func fillRecordingMeta(dst *RecordingListEntry, msg string) {
	for _, part := range strings.Fields(strings.TrimSpace(msg)) {
		kv := strings.SplitN(part, "=", 2)
		if len(kv) != 2 {
			continue
		}
		k := kv[0]
		v := kv[1]
		switch k {
		case "trigger":
			dst.Trigger = v
		case "mode":
			dst.Mode = v
		case "provider":
			dst.Provider = v
		case "host":
			dst.HostName = v
		case "ip":
			dst.HostIP = v
		case "user":
			dst.User = v
		}
	}
}
