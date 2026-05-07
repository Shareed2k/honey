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

type recordingListEntry struct {
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

type recordingsListResponse struct {
	Items []recordingListEntry `json:"items"`
}

type recordingsPlayRequest struct {
	FileName string `json:"file_name"`
}

type recordingsPlayResponse struct {
	FileName string             `json:"file_name"`
	Events   []recordings.Event `json:"events"`
}

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
	out := make([]recordingListEntry, 0, len(entries))
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
		entry := recordingListEntry{
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
	_ = json.NewEncoder(w).Encode(recordingsListResponse{Items: out})
}

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
	var body recordingsPlayRequest
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
	_ = json.NewEncoder(w).Encode(recordingsPlayResponse{
		FileName: name,
		Events:   events,
	})
}

func fillRecordingMeta(dst *recordingListEntry, msg string) {
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
