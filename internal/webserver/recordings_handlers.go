package webserver

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/shareed2k/honey/internal/safepath"
)

const (
	maxRecordingPlayBytes  = 8 << 20
	maxRecordingPlayEvents = 30000
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

type recordingEvent struct {
	TimeMS    int64           `json:"time_ms"`
	Type      string          `json:"type"`
	Direction string          `json:"direction,omitempty"`
	DataB64   string          `json:"data_b64,omitempty"`
	Cols      int             `json:"cols,omitempty"`
	Rows      int             `json:"rows,omitempty"`
	Message   string          `json:"message,omitempty"`
	Result    json.RawMessage `json:"result,omitempty"`
}

type recordingsPlayResponse struct {
	FileName string           `json:"file_name"`
	Events   []recordingEvent `json:"events"`
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

	entries, err := os.ReadDir(recordDir)
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
		fullPath := filepath.Join(recordDir, name)
		st, statErr := os.Stat(fullPath)
		if statErr != nil {
			continue
		}
		entry := recordingListEntry{
			FileName:       name,
			ModifiedUnixMS: st.ModTime().UnixMilli(),
			SizeBytes:      st.Size(),
		}
		fillRecordingMeta(&entry, readRecordingOpenMessage(fullPath))
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
	if name == "" || strings.Contains(name, "/") || strings.Contains(name, `\`) || !strings.HasSuffix(name, ".hrec.jsonl") {
		httpError(w, fmt.Errorf("invalid recording file name"), http.StatusBadRequest)
		return
	}
	fullPath := filepath.Join(recordDir, name)
	absPath, err := filepath.Abs(filepath.Clean(fullPath))
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	raw, err := safepath.ReadFile(absPath)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if len(raw) > maxRecordingPlayBytes {
		httpError(w, fmt.Errorf("recording file too large (max %d bytes)", maxRecordingPlayBytes), http.StatusBadRequest)
		return
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	events := make([]recordingEvent, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var evt recordingEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			httpError(w, fmt.Errorf("invalid recording event JSON"), http.StatusBadRequest)
			return
		}
		events = append(events, evt)
		if len(events) > maxRecordingPlayEvents {
			httpError(w, fmt.Errorf("too many recording events (max %d)", maxRecordingPlayEvents), http.StatusBadRequest)
			return
		}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(recordingsPlayResponse{
		FileName: name,
		Events:   events,
	})
}

func readRecordingOpenMessage(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		return ""
	}
	var evt recordingEvent
	if err := json.Unmarshal(sc.Bytes(), &evt); err != nil {
		return ""
	}
	if evt.Type != "open" {
		return ""
	}
	return evt.Message
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
