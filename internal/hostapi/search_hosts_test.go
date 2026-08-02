package hostapi

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

func TestMergeSearchDefaultsFromConfig(t *testing.T) {
	t.Run("nil config", func(t *testing.T) {
		q := hosts.Query{}
		MergeSearchDefaultsFromConfig(nil, &q)
		assert.Empty(t, q.NameSubstring)
		assert.Empty(t, q.NameRegex)
	})

	t.Run("applies defaults when empty", func(t *testing.T) {
		cfg := &config.File{}
		cfg.Defaults.Name = "default-name"
		cfg.Defaults.NameRegex = "default-regex"
		q := hosts.Query{}
		MergeSearchDefaultsFromConfig(cfg, &q)
		assert.Equal(t, "default-name", q.NameSubstring)
		assert.Equal(t, "default-regex", q.NameRegex)
	})

	t.Run("does not override if provided", func(t *testing.T) {
		cfg := &config.File{}
		cfg.Defaults.Name = "default-name"
		cfg.Defaults.NameRegex = "default-regex"
		q := hosts.Query{
			NameSubstring: "provided-name",
			NameRegex:     "provided-regex",
		}
		MergeSearchDefaultsFromConfig(cfg, &q)
		assert.Equal(t, "provided-name", q.NameSubstring)
		assert.Equal(t, "provided-regex", q.NameRegex)
	})
}

func TestSearchHosts(t *testing.T) {
	ctx := context.Background()

	tmpDir := t.TempDir()
	cfgPath := filepath.Join(tmpDir, "honeyfile.yaml")
	err := os.WriteFile(cfgPath, []byte(""), 0o644)
	require.NoError(t, err)

	f1 := &mockFactory{id: "mock1"}
	reg := searchrun.NewRegistry([]searchrun.ProviderFactory{f1})

	t.Run("nil input", func(t *testing.T) {
		out, err := SearchHosts(ctx, nil, nil, reg)
		assert.ErrorContains(t, err, "nil input")
		assert.Empty(t, out.Records)
	})

	t.Run("missing config", func(t *testing.T) {
		in := &SearchHostsInput{ConfigPath: "missing.yaml"}
		_, err := SearchHosts(ctx, in, nil, reg)
		assert.ErrorContains(t, err, "no such file or directory")
	})

	t.Run("invalid config", func(t *testing.T) {
		badCfgPath := filepath.Join(tmpDir, "bad.yaml")
		err := os.WriteFile(badCfgPath, []byte("invalid yaml content: ["), 0o644)
		require.NoError(t, err)

		in := &SearchHostsInput{ConfigPath: badCfgPath}
		_, err = SearchHosts(ctx, in, nil, reg)
		assert.ErrorContains(t, err, "config:")
	})

	t.Run("pre-loaded config", func(t *testing.T) {
		cfg := &config.File{}
		in := &SearchHostsInput{Config: cfg}
		out, err := SearchHosts(ctx, in, nil, reg)
		assert.NoError(t, err)
		assert.Len(t, out.Records, 1)
		assert.Equal(t, "test-record", out.Records[0].Name)
	})

	t.Run("success with config path", func(t *testing.T) {
		in := &SearchHostsInput{ConfigPath: cfgPath}
		out, err := SearchHosts(ctx, in, nil, reg)
		assert.NoError(t, err)
		assert.Len(t, out.Records, 1)
		assert.Equal(t, "test-record", out.Records[0].Name)
		assert.Equal(t, 1, out.Count)
	})

	t.Run("unknown backend filter", func(t *testing.T) {
		in := &SearchHostsInput{
			ConfigPath: cfgPath,
			Backends:   "unknown-backend",
		}
		_, err := SearchHosts(ctx, in, nil, reg)
		assert.ErrorContains(t, err, "no backends match")
	})
}
