package webserver

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

type recipeMetaEvent struct {
	Type   string          `json:"type"`
	Result json.RawMessage `json:"result"`
}

type recipeMetaPayload struct {
	RecipePath        string                  `json:"recipe_path"`
	HostCount         int                     `json:"host_count"`
	RecipeContentHash string                  `json:"recipe_content_hash"`
	StartedAt         string                  `json:"started_at"`
	Hosts             []hosts.Record          `json:"hosts,omitempty"`
	Plan              string                  `json:"plan,omitempty"`
	Graph             *cuetry.RecipeGraphPlan `json:"graph,omitempty"`
}

// RecentRunEntry is one recent recipe run.
type RecentRunEntry struct {
	RecipeName        string                  `json:"recipe_name"`
	RecipePath        string                  `json:"recipe_path"`
	HostCount         int                     `json:"host_count"`
	StartedAt         string                  `json:"started_at"`
	RecordingID       string                  `json:"recording_id"`
	RecipeContentHash string                  `json:"recipe_content_hash,omitempty"`
	Edited            bool                    `json:"edited"`
	Hosts             []hosts.Record          `json:"hosts,omitempty"`
	Plan              string                  `json:"plan,omitempty"`
	Graph             *cuetry.RecipeGraphPlan `json:"graph,omitempty"`
}

// RecentRunsResponse is returned by GET /api/v1/recipes/recent-runs.
type RecentRunsResponse struct {
	Runs []RecentRunEntry `json:"runs"`
}

// handleRecipesRecentRuns lists recent recipe runs from session recording dir.
// @Summary Recent recipe runs
// @Tags recipes
// @Produce json
// @Param limit query int false "max entries (default 20, max 200)"
// @Success 200 {object} RecentRunsResponse
// @Router /api/v1/recipes/recent-runs [get]
// @Security BearerAuth
func (s *Server) handleRecipesRecentRuns(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	limit := 20
	if q := r.URL.Query().Get("limit"); q != "" {
		if n, err := strconv.Atoi(q); err == nil && n > 0 && n <= 200 {
			limit = n
		}
	}
	dir := strings.TrimSpace(s.opts.RecordDir)
	if dir == "" {
		writeJSONOK(w, RecentRunsResponse{Runs: []RecentRunEntry{}})
		return
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSONOK(w, RecentRunsResponse{Runs: []RecentRunEntry{}})
		return
	}
	runs := make([]RecentRunEntry, 0, len(entries))
	for _, e := range entries {
		name := e.Name()
		if !strings.HasSuffix(name, ".hrec.jsonl") {
			continue
		}
		// Match cue-exec recordings but exclude dry-run variants.
		if !strings.Contains(name, "cue-exec") || strings.Contains(name, "cue-exec-dry") {
			continue
		}
		meta, ok := readRecipeMeta(filepath.Join(dir, name))
		if !ok {
			continue
		}
		runs = append(runs, RecentRunEntry{
			RecipeName:        filepath.Base(meta.RecipePath),
			RecipePath:        meta.RecipePath,
			HostCount:         meta.HostCount,
			StartedAt:         meta.StartedAt,
			RecordingID:       strings.TrimSuffix(name, ".hrec.jsonl"),
			RecipeContentHash: meta.RecipeContentHash,
			Edited:            isEdited(meta),
			Hosts:             meta.Hosts,
			Plan:              meta.Plan,
			Graph:             meta.Graph,
		})
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].StartedAt > runs[j].StartedAt })
	if len(runs) > limit {
		runs = runs[:limit]
	}
	writeJSONOK(w, RecentRunsResponse{Runs: runs})
}

// readRecipeMeta scans the first ~10 lines of the recording file for a
// recipe-meta event and returns the parsed payload.
func readRecipeMeta(path string) (recipeMetaPayload, bool) {
	f, err := os.Open(path) // #nosec G304 -- path comes from os.ReadDir on a server-configured dir
	if err != nil {
		return recipeMetaPayload{}, false
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64<<10), 1<<20)
	for i := 0; i < 10 && scanner.Scan(); i++ {
		var ev recipeMetaEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}
		if ev.Type != "recipe-meta" {
			continue
		}
		var p recipeMetaPayload
		if err := json.Unmarshal(ev.Result, &p); err != nil {
			return recipeMetaPayload{}, false
		}
		return p, true
	}
	return recipeMetaPayload{}, false
}

// isEdited compares the recorded hash to the hash of the disk recipe.
// Missing disk file or unparseable recipe -> edited=true (safe default).
func isEdited(meta recipeMetaPayload) bool {
	if meta.RecipePath == "" {
		return true
	}
	b, err := os.ReadFile(meta.RecipePath) // #nosec G304 -- path comes from a recording we wrote
	if err != nil {
		return true
	}
	loaded, err := cuetry.ParseRemoteRecipe(b, nil)
	if err != nil {
		return true
	}
	h, err := loaded.HashJSON()
	if err != nil {
		return true
	}
	return h != meta.RecipeContentHash
}

func writeJSONOK(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
