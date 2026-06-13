package webserver

import (
	"bufio"
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/safepath"
)

// LibraryRecipe represents a parsed recipe from the examples directory.
type LibraryRecipe struct {
	Name        string `json:"name"`
	Filename    string `json:"filename"`
	Description string `json:"description"`
	Content     string `json:"content"`
	Category    string `json:"category"`
}

// LibraryCategory groups LibraryRecipes by domain.
type LibraryCategory struct {
	Name    string          `json:"name"`
	Recipes []LibraryRecipe `json:"recipes"`
}

// LibraryResponse is the JSON body for GET /api/v1/recipes/library.
type LibraryResponse struct {
	Categories []LibraryCategory `json:"categories"`
}

// parseRecipeMeta reads the first few lines looking for special comments.
// Fallback to defaults if missing.
func parseRecipeMeta(content []byte, filename string) (title, desc, cat string) {
	scanner := bufio.NewScanner(bytes.NewReader(content))
	title = filename
	cat = "General / Utilities"

	var descLines []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if !strings.HasPrefix(line, "//") {
			if line != "" {
				break
			}
			continue
		}

		text := strings.TrimSpace(strings.TrimPrefix(line, "//"))
		lowerText := strings.ToLower(text)

		switch {
		case strings.HasPrefix(lowerText, "title:"):
			title = strings.TrimSpace(text[6:])
		case strings.HasPrefix(lowerText, "category:"):
			cat = strings.TrimSpace(text[9:])
		case strings.HasPrefix(lowerText, "description:"):
			descLines = append(descLines, strings.TrimSpace(text[12:]))
		case len(descLines) > 0:
			// Continue description if we already started
			descLines = append(descLines, text)
		}
	}

	if len(descLines) == 0 {
		desc = "A Honey CUE recipe template."
	} else {
		desc = strings.Join(descLines, " ")
	}

	return title, desc, cat
}

func (s *Server) handleRecipesLibrary(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, `{"error":"method not allowed"}`, http.StatusMethodNotAllowed)
		return
	}

	dir := "examples/recipe"
	if s.opts.ConfigPath != "" {
		if abs, err := filepath.Abs(filepath.Join(filepath.Dir(s.opts.ConfigPath), "examples", "recipe")); err == nil {
			dir = abs
		}
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		writeJSONOK(w, LibraryResponse{Categories: []LibraryCategory{}})
		return
	}

	catMap := make(map[string][]LibraryRecipe)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".cue") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := safepath.ReadFile(path)
		if err != nil {
			continue
		}

		recipe, err := cuetry.ParseRemoteRecipe(raw, nil)
		if err != nil {
			continue
		}

		title, desc, cat := parseRecipeMeta(raw, e.Name())
		// If title wasn't found in comments, fallback to parsed CUE name or filename
		if title == e.Name() && recipe.Name != "" {
			title = recipe.Name
		}

		catMap[cat] = append(catMap[cat], LibraryRecipe{
			Name:        title,
			Filename:    e.Name(),
			Description: desc,
			Content:     string(raw),
			Category:    cat,
		})
	}

	var cats []LibraryCategory
	for k, v := range catMap {
		cats = append(cats, LibraryCategory{Name: k, Recipes: v})
	}

	writeJSONOK(w, LibraryResponse{Categories: cats})
}
