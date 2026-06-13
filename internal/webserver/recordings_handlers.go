package webserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"go.uber.org/zap"

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

// RecordingsRetentionInfo describes auto-TTL policy for the record dir.
type RecordingsRetentionInfo struct {
	Enabled bool   `json:"enabled"`
	MaxAge  string `json:"max_age,omitempty"`
}

// RecordingsListResponse is returned by GET /api/v1/recordings.
type RecordingsListResponse struct {
	Items      []RecordingListEntry     `json:"items"`
	FileCount  int                      `json:"file_count"`
	TotalBytes int64                    `json:"total_bytes"`
	Retention  *RecordingsRetentionInfo `json:"retention,omitempty"`
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
	fileCount, totalBytes, _ := recordings.DirStats(recordDir)
	resp := RecordingsListResponse{
		Items:      out,
		FileCount:  fileCount,
		TotalBytes: totalBytes,
	}
	if maxAge, text := s.recordingRetentionMaxAge(); maxAge > 0 {
		resp.Retention = &RecordingsRetentionInfo{Enabled: true, MaxAge: text}
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
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

// RecordingsSummarizeRequest is the JSON body for POST /api/v1/recordings/summarize.
type RecordingsSummarizeRequest struct {
	FileName string `json:"file_name"`
	Model    string `json:"model"`
}

// RecordingsSummarizeResponse is returned by POST /api/v1/recordings/summarize.
type RecordingsSummarizeResponse struct {
	Reply string `json:"reply"`
}

const recordingSummarizeSystemPrompt = `You summarize Honey session recordings of CUE recipe batch runs (.hrec.jsonl).
Focus on: which recipe ran, how many hosts, per-host success/failure, exit codes, and notable stdout/stderr.
Call out failures clearly. Do not invent hosts or steps not present in the log.
Keep the answer structured (short sections or bullets). Do not include secrets or env values.`

// handleRecordingsDelete removes one recording file from the record dir.
// @Summary Delete session recording
// @Tags recordings
// @Param file_name path string true "recording file name"
// @Success 200 {object} map[string]string
// @Failure 400 {object} map[string]string
// @Router /api/v1/recordings/{file_name} [delete]
// @Security BearerAuth
func (s *Server) handleRecordingsDelete(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodDelete {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	recordDir := strings.TrimSpace(s.opts.RecordDir)
	if recordDir == "" {
		httpError(w, fmt.Errorf("session recording is not enabled"), http.StatusBadRequest)
		return
	}
	name := strings.TrimSpace(r.PathValue("file_name"))
	if err := recordings.ValidateBaseName(name); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	root, err := os.OpenRoot(recordDir)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	defer root.Close()
	if err := root.Remove(name); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "deleted", "file_name": name})
}

// handleRecordingsSummarize asks the LLM to summarize a saved recording.
// @Summary Summarize session recording
// @Tags recordings
// @Accept json
// @Produce json
// @Param body body RecordingsSummarizeRequest true "recording file name"
// @Success 200 {object} RecordingsSummarizeResponse
// @Failure 400 {object} map[string]string
// @Failure 503 {object} map[string]string
// @Router /api/v1/recordings/summarize [post]
// @Security BearerAuth
func (s *Server) handleRecordingsSummarize(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if assistAPIKey() == "" {
		httpError(w, errors.New("recording summarize is not configured (set OPENAI_API_KEY)"), http.StatusServiceUnavailable)
		return
	}
	recordDir := strings.TrimSpace(s.opts.RecordDir)
	if recordDir == "" {
		httpError(w, fmt.Errorf("session recording is not enabled"), http.StatusBadRequest)
		return
	}
	var body RecordingsSummarizeRequest
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
	if len(events) == 0 {
		httpError(w, fmt.Errorf("recording has no events"), http.StatusBadRequest)
		return
	}
	const maxSummarizeBytes = 2 << 20
	root, err := os.OpenRoot(recordDir)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	defer root.Close()
	raw, err := root.ReadFile(name)
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if len(raw) > maxSummarizeBytes {
		httpError(w, fmt.Errorf("recording too large to summarize online (max %d bytes); download the .hrec.jsonl file instead", maxSummarizeBytes), http.StatusBadRequest)
		return
	}

	resolveCtx, resolveCancel := context.WithTimeout(r.Context(), 25*time.Second)
	chatModel, err := s.resolveAssistChatModel(resolveCtx, body.Model)
	resolveCancel()
	if err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if !s.assistRL.allow(clientIP(r), assistRPM()) {
		httpError(w, errors.New("rate limit exceeded; try again in a minute"), http.StatusTooManyRequests)
		return
	}

	userContent := recordings.BuildSummarizePrompt(events)
	userAsk := "Summarize this recipe run for an operator: outcomes per host, failures, and anything noteworthy."
	userContent = userAsk + "\n\n--- Recording log ---\n" + userContent

	reply, err := assistCreateChatCompletion(r.Context(), chatModel, recordingSummarizeSystemPrompt, userContent)
	if err != nil {
		zap.L().Warn("recording summarize upstream error", zap.Error(err))
		httpError(w, fmt.Errorf("upstream error: %v", err), http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RecordingsSummarizeResponse{Reply: reply})
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

// handleRecordingsFailedHosts extracts the failed hosts from a specific recording.
// @Summary Extract failed hosts from session recording
// @Tags recordings
// @Produce json
// @Param id path string true "recording file ID (without extension)"
// @Success 200 {array} hosts.Record
// @Failure 400 {object} map[string]string
// @Failure 404 {object} map[string]string
// @Failure 405 {string} string
// @Router /api/v1/recordings/{id}/failed-hosts [get]
// @Security BearerAuth
func (s *Server) handleRecordingsFailedHosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id := r.PathValue("id")
	if id == "" {
		httpError(w, fmt.Errorf("id required"), http.StatusBadRequest)
		return
	}
	events, err := recordings.LoadEvents(s.opts.RecordDir, id+".hrec.jsonl")
	if err != nil {
		httpError(w, err, http.StatusNotFound)
		return
	}

	failed := recordings.ExtractFailedHosts(events)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(failed)
}
