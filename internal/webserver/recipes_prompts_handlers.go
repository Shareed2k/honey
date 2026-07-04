package webserver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"github.com/shareed2k/honey/internal/safepath"
)

// PromptUploadResponse is returned by POST /api/v1/recipes/prompts/upload.
type PromptUploadResponse struct {
	ID       string `json:"id"`
	Path     string `json:"path"`
	Filename string `json:"filename"`
	SHA      string `json:"sha"`
}

// handleRecipesPromptsUpload receives a file for a recipe file prompt and saves it in a temporary location.
// @Summary Upload file for recipe prompt
// @Tags recipes
// @Accept multipart/form-data
// @Produce json
// @Param file formData file true "File contents"
// @Success 200 {object} PromptUploadResponse
// @Failure 400 {object} map[string]string
// @Router /api/v1/recipes/prompts/upload [post]
// @Security BearerAuth
func (api *RecipesAPI) handleRecipesPromptsUpload(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(api.opts.MaxUploadSize); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}

	f, hdr, err := r.FormFile("file")
	if err != nil {
		httpError(w, fmt.Errorf("file: %w", err), http.StatusBadRequest)
		return
	}
	defer func() { _ = f.Close() }()

	tmpDir, err := os.MkdirTemp("", "honey-prompt-file-*")
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}
	// We do not defer os.RemoveAll(tmpDir) because the file needs to persist until recipe execution

	base := "upload"
	if hdr != nil && hdr.Filename != "" {
		base = filepath.Base(hdr.Filename)
	}
	localPath := filepath.Clean(filepath.Join(tmpDir, base))

	out, err := safepath.Create(localPath)
	if err != nil {
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	hash := sha256.New()
	mw := io.MultiWriter(out, hash)

	if _, err := io.Copy(mw, io.LimitReader(f, api.opts.MaxUploadSize)); err != nil {
		_ = out.Close()
		_ = os.RemoveAll(tmpDir)
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	if err := out.Close(); err != nil {
		_ = os.RemoveAll(tmpDir)
		httpError(w, err, http.StatusInternalServerError)
		return
	}

	shaHex := hex.EncodeToString(hash.Sum(nil))

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(PromptUploadResponse{
		ID:       uuid.New().String(),
		Path:     localPath,
		Filename: base,
		SHA:      shaHex,
	})
}

// PromptChoicesRequest is the request for POST /api/v1/recipes/prompts/choices
type PromptChoicesRequest struct {
	URL      string `json:"url"`
	JSONPath string `json:"json_path"` // Unused for now, we'll do filtering on frontend
}

// handleRecipesPromptsChoices proxies remote URL requests for prompt choices.
// @Summary Fetch remote choices for recipe prompt
// @Tags recipes
// @Accept json
// @Produce json
// @Param body body PromptChoicesRequest true "URL to fetch"
// @Success 200 {object} map[string]interface{}
// @Failure 400 {object} map[string]string
// @Router /api/v1/recipes/prompts/choices [post]
// @Security BearerAuth
func (api *RecipesAPI) handleRecipesPromptsChoices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req PromptChoicesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		httpError(w, err, http.StatusBadRequest)
		return
	}
	if req.URL == "" {
		httpError(w, fmt.Errorf("url is required"), http.StatusBadRequest)
		return
	}

	resp, err := http.Get(req.URL) // #nosec G107
	if err != nil {
		httpError(w, fmt.Errorf("fetch %s failed: %w", req.URL, err), http.StatusBadGateway)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	w.Header().Set("Content-Type", resp.Header.Get("Content-Type"))
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}
