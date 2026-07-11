package macros

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestResolvePath(t *testing.T) {
	// Setup
	tmpDir, err := os.MkdirTemp("", "macros_test_*")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	t.Run("explicit_path", func(t *testing.T) {
		path, err := ResolvePath("/my/explicit/path.yaml")
		assert.NoError(t, err)
		assert.Equal(t, "/my/explicit/path.yaml", path)
	})

	t.Run("env_var", func(t *testing.T) {
		os.Setenv("HONEY_MACROS_FILE", "/env/path.yaml")
		defer os.Unsetenv("HONEY_MACROS_FILE")
		path, err := ResolvePath("")
		assert.NoError(t, err)
		assert.Equal(t, "/env/path.yaml", path)
	})

	t.Run("default_file", func(t *testing.T) {
		// Change working directory to tmpDir to test default file resolution
		originalWD, _ := os.Getwd()
		os.Chdir(tmpDir)
		defer os.Chdir(originalWD)

		err := os.WriteFile("honeyfile.yaml", []byte(""), 0o644)
		assert.NoError(t, err)

		path, err := ResolvePath("")
		assert.NoError(t, err)
		expected, _ := filepath.Abs("honeyfile.yaml")
		assert.Equal(t, expected, path)
	})

	t.Run("not_found", func(t *testing.T) {
		// Ensure we are in a clean directory
		originalWD, _ := os.Getwd()
		emptyDir, _ := os.MkdirTemp("", "empty_*")
		os.Chdir(emptyDir)
		defer func() {
			os.Chdir(originalWD)
			os.RemoveAll(emptyDir)
		}()

		_, err := ResolvePath("")
		assert.ErrorContains(t, err, "honey macros file not found")
	})
}

func TestLoad(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "macros_test_*")
	assert.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	// Create a dummy config to allow safepath to work
	originalWD, _ := os.Getwd()
	os.Chdir(tmpDir)
	defer os.Chdir(originalWD)

	t.Run("valid_macroset", func(t *testing.T) {
		content := `
apiVersion: honey.shareed2k.io/v1alpha1
kind: MacroSet
metadata:
  name: test-macros
spec:
  macros:
    my-exec:
      kind: exec
      target: prod-*
      command: uptime
    my-recipe:
      kind: recipe
      target: prod-*
      recipePath: ./my-recipe.cue
`
		err := os.WriteFile("valid.yaml", []byte(content), 0o644)
		assert.NoError(t, err)

		ms, err := Load("valid.yaml")
		assert.NoError(t, err)
		assert.NotNil(t, ms)
		assert.Equal(t, "MacroSet", ms.Kind)
		assert.Len(t, ms.Spec.Macros, 2)
	})

	t.Run("invalid_yaml", func(t *testing.T) {
		err := os.WriteFile("invalid.yaml", []byte("invalid: ["), 0o644)
		assert.NoError(t, err)

		_, err = Load("invalid.yaml")
		assert.Error(t, err)
	})

	t.Run("missing_required_fields", func(t *testing.T) {
		content := `
apiVersion: honey.shareed2k.io/v1alpha1
kind: MacroSet
`
		err := os.WriteFile("missing.yaml", []byte(content), 0o644)
		assert.NoError(t, err)

		_, err = Load("missing.yaml")
		assert.ErrorContains(t, err, "Error:Field validation for 'Metadata' failed on the 'required' tag")
	})

	t.Run("invalid_api_version", func(t *testing.T) {
		content := `
apiVersion: v1
kind: MacroSet
metadata:
  name: test
spec:
  macros:
    test:
      kind: exec
      target: test
      command: ls
`
		err := os.WriteFile("bad_version.yaml", []byte(content), 0o644)
		assert.NoError(t, err)

		_, err = Load("bad_version.yaml")
		assert.ErrorContains(t, err, "Error:Field validation for 'APIVersion' failed")
	})

	t.Run("invalid_duration", func(t *testing.T) {
		content := `
apiVersion: honey.shareed2k.io/v1alpha1
kind: MacroSet
metadata:
  name: test
spec:
  macros:
    test:
      kind: exec
      target: test
      command: ls
      timeout: invalid-duration
`
		err := os.WriteFile("bad_duration.yaml", []byte(content), 0o644)
		assert.NoError(t, err)

		_, err = Load("bad_duration.yaml")
		assert.ErrorContains(t, err, "Error:Field validation for 'Timeout' failed")
	})

	t.Run("file_not_found", func(t *testing.T) {
		_, err := Load("does_not_exist.yaml")
		assert.Error(t, err)
	})
}

func TestValidateMacro(t *testing.T) {
	tests := []struct {
		name    string
		macro   Macro
		wantErr string
	}{
		{
			name: "valid exec",
			macro: Macro{
				Kind:    "exec",
				Target:  "web-*",
				Command: "uptime",
			},
			wantErr: "",
		},
		{
			name: "exec missing command",
			macro: Macro{
				Kind:   "exec",
				Target: "web-*",
			},
			wantErr: "requires target and command or commands",
		},
		{
			name: "exec missing target",
			macro: Macro{
				Kind:    "exec",
				Command: "uptime",
			},
			wantErr: "requires target and command or commands",
		},
		{
			name: "exec both command and commands",
			macro: Macro{
				Kind:     "exec",
				Target:   "web-*",
				Command:  "uptime",
				Commands: []string{"ls"},
			},
			wantErr: "supports either command or commands, not both",
		},
		{
			name: "exec empty command in list",
			macro: Macro{
				Kind:     "exec",
				Target:   "web-*",
				Commands: []string{"uptime", ""},
			},
			wantErr: "must be non-empty",
		},
		{
			name: "valid recipe",
			macro: Macro{
				Kind:       "recipe",
				Target:     "web-*",
				RecipePath: "a.cue",
			},
			wantErr: "",
		},
		{
			name: "recipe missing path",
			macro: Macro{
				Kind:   "recipe",
				Target: "web-*",
			},
			wantErr: "recipe requires target and recipePath",
		},
		{
			name: "valid logs",
			macro: Macro{
				Kind:   "logs",
				Target: "web-*",
			},
			wantErr: "",
		},
		{
			name: "logs missing target",
			macro: Macro{
				Kind: "logs",
			},
			wantErr: "logs requires target",
		},
		{
			name: "valid app",
			macro: Macro{
				Kind: "app",
				App:  "myapp",
			},
			wantErr: "",
		},
		{
			name: "app missing app",
			macro: Macro{
				Kind: "app",
			},
			wantErr: "app requires app",
		},
		{
			name: "valid tunnel",
			macro: Macro{
				Kind: "tunnel",
				App:  "myapp",
			},
			wantErr: "",
		},
		{
			name: "tunnel missing app",
			macro: Macro{
				Kind: "tunnel",
			},
			wantErr: "tunnel requires app",
		},
		{
			name: "valid egress",
			macro: Macro{
				Kind:       "egress",
				EgressHost: "host1",
			},
			wantErr: "",
		},
		{
			name: "egress missing host",
			macro: Macro{
				Kind: "egress",
			},
			wantErr: "egress requires host or hosts",
		},
		{
			name: "powershell with runAs",
			macro: Macro{
				Kind:    "exec",
				Target:  "web-*",
				Command: "uptime",
				Shell:   "powershell",
				RunAs:   "admin",
			},
			wantErr: "runAs is not supported with powershell",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateMacro(tt.name, tt.macro)
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}
