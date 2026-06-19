package scheduler

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
)

// recipeWithSchedules is a minimal valid CUE recipe that contains two schedule entries.
const recipeWithSchedules = `recipe: {
	name: "test-recipe"
	schedules: {
		nightly: {
			cron:     "0 2 * * *"
			timezone: "America/New_York"
			env: {
				BATCH_SIZE: "500"
			}
		}
		hourly: {
			cron: "0 * * * *"
		}
	}
	steps: [
		{host: "10.0.0.1", command: "echo hello"},
	]
}
`

// recipeNoSchedules is a valid CUE recipe without any schedules.
const recipeNoSchedules = `recipe: {
	name: "plain-recipe"
	steps: [
		{host: "10.0.0.1", command: "echo world"},
	]
}
`

// recipeEmptyCron is a recipe with a schedule that has an empty cron expression.
const recipeEmptyCron = `recipe: {
	name: "empty-cron-recipe"
	schedules: {
		broken: {
			cron: ""
		}
	}
	steps: [
		{host: "10.0.0.1", command: "echo hi"},
	]
}
`

func writeRecipeFile(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "recipe.cue")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write recipe file %s: %v", path, err)
	}
	return path
}

// makeConfig builds a minimal config.File with the given apps without going
// through disk I/O (bypasses viper / yaml parsing).
func makeConfig(appMap apps.Config) *config.File {
	return &config.File{
		Apps: appMap,
	}
}

// TestLoadScheduleEntries_nilConfig verifies that a nil config returns nil, nil.
func TestLoadScheduleEntries_nilConfig(t *testing.T) {
	entries, err := LoadScheduleEntries(nil, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if entries != nil {
		t.Fatalf("expected nil entries for nil config, got %v", entries)
	}
}

// TestLoadScheduleEntries_singleSchedule parses a recipe with two schedules
// and verifies that both entries are returned with correct fields.
func TestLoadScheduleEntries_singleSchedule(t *testing.T) {
	dir := t.TempDir()
	recipePath := writeRecipeFile(t, dir, recipeWithSchedules)

	cfg := makeConfig(apps.Config{
		"myapp": {
			Type:         apps.AppTypeRecipe,
			TargetRecipe: recipePath,
		},
	})

	entries, err := LoadScheduleEntries(cfg, filepath.Join(dir, "honey.yaml"))
	if err != nil {
		t.Fatalf("LoadScheduleEntries: %v", err)
	}

	if len(entries) != 2 {
		t.Fatalf("expected 2 schedule entries, got %d", len(entries))
	}

	// Build a map for stable lookup regardless of iteration order.
	byName := make(map[string]ScheduleEntry, len(entries))
	for _, e := range entries {
		byName[e.ScheduleName] = e
	}

	nightly, ok := byName["nightly"]
	if !ok {
		t.Fatal("missing schedule entry 'nightly'")
	}
	if nightly.AppName != "myapp" {
		t.Errorf("nightly.AppName = %q, want %q", nightly.AppName, "myapp")
	}
	if nightly.RecipePath != recipePath {
		t.Errorf("nightly.RecipePath = %q, want %q", nightly.RecipePath, recipePath)
	}
	if nightly.Schedule.Cron != "0 2 * * *" {
		t.Errorf("nightly.Schedule.Cron = %q, want %q", nightly.Schedule.Cron, "0 2 * * *")
	}
	if nightly.Schedule.TimeZone != "America/New_York" {
		t.Errorf("nightly.Schedule.TimeZone = %q, want %q", nightly.Schedule.TimeZone, "America/New_York")
	}
	if nightly.Schedule.Env["BATCH_SIZE"] != "500" {
		t.Errorf("nightly.Schedule.Env[BATCH_SIZE] = %q, want %q", nightly.Schedule.Env["BATCH_SIZE"], "500")
	}

	hourly, ok := byName["hourly"]
	if !ok {
		t.Fatal("missing schedule entry 'hourly'")
	}
	if hourly.Schedule.Cron != "0 * * * *" {
		t.Errorf("hourly.Schedule.Cron = %q, want %q", hourly.Schedule.Cron, "0 * * * *")
	}
}

// TestLoadScheduleEntries_noSchedules verifies that a recipe without schedules
// returns zero entries (not an error).
func TestLoadScheduleEntries_noSchedules(t *testing.T) {
	dir := t.TempDir()
	recipePath := writeRecipeFile(t, dir, recipeNoSchedules)

	cfg := makeConfig(apps.Config{
		"plainapp": {
			Type:         apps.AppTypeRecipe,
			TargetRecipe: recipePath,
		},
	})

	entries, err := LoadScheduleEntries(cfg, filepath.Join(dir, "honey.yaml"))
	if err != nil {
		t.Fatalf("LoadScheduleEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

// TestLoadScheduleEntries_skipsNonRecipeApps verifies that apps whose type is
// not AppTypeRecipe are silently skipped.
func TestLoadScheduleEntries_skipsNonRecipeApps(t *testing.T) {
	cfg := makeConfig(apps.Config{
		"httpapp": {
			Type: apps.AppTypeHTTP,
		},
		"tcpapp": {
			Type: apps.AppTypeTCP,
		},
	})

	entries, err := LoadScheduleEntries(cfg, "")
	if err != nil {
		t.Fatalf("LoadScheduleEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

// TestLoadScheduleEntries_skipsMissingRecipeFile verifies that apps whose recipe
// file cannot be read are logged and skipped without returning an error.
func TestLoadScheduleEntries_skipsMissingRecipeFile(t *testing.T) {
	cfg := makeConfig(apps.Config{
		"missingapp": {
			Type:         apps.AppTypeRecipe,
			TargetRecipe: "/nonexistent/path/recipe.cue",
		},
	})

	entries, err := LoadScheduleEntries(cfg, "")
	if err != nil {
		t.Fatalf("LoadScheduleEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries, got %d", len(entries))
	}
}

// TestLoadScheduleEntries_skipsEmptyCron verifies that schedules with an empty
// cron expression are skipped without returning an error.
func TestLoadScheduleEntries_skipsEmptyCron(t *testing.T) {
	dir := t.TempDir()
	recipePath := writeRecipeFile(t, dir, recipeEmptyCron)

	cfg := makeConfig(apps.Config{
		"badcronapp": {
			Type:         apps.AppTypeRecipe,
			TargetRecipe: recipePath,
		},
	})

	entries, err := LoadScheduleEntries(cfg, filepath.Join(dir, "honey.yaml"))
	if err != nil {
		t.Fatalf("LoadScheduleEntries: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected 0 entries (empty cron skipped), got %d", len(entries))
	}
}

// TestNew_nilConfigReturnsError verifies that New returns an error when no config is provided.
func TestNew_nilConfigReturnsError(t *testing.T) {
	_, err := New(Options{Config: nil})
	if err == nil {
		t.Fatal("expected error when config is nil, got nil")
	}
}

// TestNew_emptyConfigReturnsManager verifies that New succeeds with a valid (but empty) config.
func TestNew_emptyConfigReturnsManager(t *testing.T) {
	cfg := makeConfig(apps.Config{})
	m, err := New(Options{Config: cfg})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m == nil {
		t.Fatal("expected non-nil Manager")
	}
	if len(m.Entries()) != 0 {
		t.Fatalf("expected 0 entries for empty config, got %d", len(m.Entries()))
	}
}

// TestManager_entriesMatchLoadScheduleEntries verifies that Manager.Entries()
// returns the same set of entries that LoadScheduleEntries would return.
func TestManager_entriesMatchLoadScheduleEntries(t *testing.T) {
	dir := t.TempDir()
	recipePath := writeRecipeFile(t, dir, recipeWithSchedules)
	configPath := filepath.Join(dir, "honey.yaml")

	cfg := makeConfig(apps.Config{
		"myapp": {
			Type:         apps.AppTypeRecipe,
			TargetRecipe: recipePath,
		},
	})

	m, err := New(Options{Config: cfg, ConfigPath: configPath})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	standalone, err := LoadScheduleEntries(cfg, configPath)
	if err != nil {
		t.Fatalf("LoadScheduleEntries: %v", err)
	}

	if len(m.Entries()) != len(standalone) {
		t.Fatalf("Manager.Entries() returned %d entries, LoadScheduleEntries returned %d",
			len(m.Entries()), len(standalone))
	}
}
