package safepath_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/shareed2k/honey/internal/safepath"
)

func TestUnder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sibling := filepath.Join(filepath.Dir(dir), "sibling")

	tests := []struct {
		name    string
		root    string
		path    string
		wantErr bool
	}{
		{"same path", dir, dir, false},
		{"direct child", dir, filepath.Join(dir, "file.txt"), false},
		{"nested subdir", dir, filepath.Join(dir, "a", "b", "c"), false},
		{"sibling dir", dir, sibling, true},
		{"dot-dot escape", dir, filepath.Join(dir, "..", "escape"), true},
		{"empty root", "", dir, true},
		{"empty path", dir, "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := safepath.Under(tt.root, tt.path)
			if (err != nil) != tt.wantErr {
				t.Errorf("Under(%q, %q) error = %v, wantErr %v", tt.root, tt.path, err, tt.wantErr)
			}
		})
	}
}

func TestJoinUnder(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	tests := []struct {
		name    string
		parts   []string
		wantErr bool
	}{
		{"simple child", []string{"file.txt"}, false},
		{"nested", []string{"a", "b", "c.log"}, false},
		{"dot-dot escape", []string{"..", "escape"}, true},
		{"double dot-dot", []string{"a", "..", "..", "escape"}, true},
		{"empty part", []string{""}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := safepath.JoinUnder(dir, tt.parts...)
			if (err != nil) != tt.wantErr {
				t.Errorf("JoinUnder(%q, %v) error = %v, wantErr %v", dir, tt.parts, err, tt.wantErr)
			}
			if err == nil && got == "" {
				t.Errorf("JoinUnder returned empty path with no error")
			}
		})
	}
}

func TestReadFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	want := []byte("hello safepath")
	path := filepath.Join(dir, "test.txt")
	if err := os.WriteFile(path, want, 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("reads existing file", func(t *testing.T) {
		t.Parallel()
		got, err := safepath.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%q): %v", path, err)
		}
		if string(got) != string(want) {
			t.Errorf("ReadFile got %q, want %q", got, want)
		}
	})

	t.Run("missing file returns error", func(t *testing.T) {
		t.Parallel()
		_, err := safepath.ReadFile(filepath.Join(dir, "missing.txt"))
		if err == nil {
			t.Error("ReadFile missing file: expected error, got nil")
		}
	})

	t.Run("directory path returns error", func(t *testing.T) {
		t.Parallel()
		// Passing a dir as the file itself — safepath.Stat opens parent, stats name
		// If name ends up empty, it should return an error.
		_, err := safepath.ReadFile(dir + string(filepath.Separator))
		if err == nil {
			t.Error("ReadFile on trailing-slash dir: expected error, got nil")
		}
	})
}

func TestStat(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "statme.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	t.Run("stats existing file", func(t *testing.T) {
		t.Parallel()
		info, err := safepath.Stat(path)
		if err != nil {
			t.Fatalf("Stat(%q): %v", path, err)
		}
		if info.Name() != "statme.txt" {
			t.Errorf("Stat name = %q, want %q", info.Name(), "statme.txt")
		}
	})

	t.Run("stats existing dir", func(t *testing.T) {
		t.Parallel()
		info, err := safepath.Stat(dir)
		if err != nil {
			t.Fatalf("Stat dir %q: %v", dir, err)
		}
		if !info.IsDir() {
			t.Errorf("Stat dir: expected IsDir=true, got false")
		}
	})

	t.Run("missing path returns error", func(t *testing.T) {
		t.Parallel()
		_, err := safepath.Stat(filepath.Join(dir, "nope.txt"))
		if err == nil {
			t.Error("Stat missing: expected error, got nil")
		}
	})
}

func TestOpenAppend(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	t.Run("creates and appends", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(dir, "append1.txt")
		for _, chunk := range []string{"hello\n", "world\n"} {
			f, err := safepath.OpenAppend(path, 0o600)
			if err != nil {
				t.Fatalf("OpenAppend: %v", err)
			}
			if _, err := f.WriteString(chunk); err != nil {
				t.Fatalf("Write: %v", err)
			}
			_ = f.Close()
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "hello\nworld\n" {
			t.Errorf("got %q, want %q", got, "hello\nworld\n")
		}
	})

	t.Run("creates parent dirs", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(dir, "nested", "deep", "file.log")
		f, err := safepath.OpenAppend(path, 0o600)
		if err != nil {
			t.Fatalf("OpenAppend nested: %v", err)
		}
		_ = f.Close()
		if _, err := os.Stat(path); err != nil {
			t.Errorf("file not created: %v", err)
		}
	})

	t.Run("empty path returns error", func(t *testing.T) {
		t.Parallel()
		_, err := safepath.OpenAppend("", 0o600)
		if err == nil {
			t.Error("empty path: expected error, got nil")
		}
	})
}

func TestWriteFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	t.Run("writes and reads back", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(dir, "write1.txt")
		want := []byte("atomic content")
		if err := safepath.WriteFile(path, want, 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	t.Run("overwrites existing file atomically", func(t *testing.T) {
		t.Parallel()
		path := filepath.Join(dir, "overwrite.txt")
		_ = safepath.WriteFile(path, []byte("old"), 0o600)
		if err := safepath.WriteFile(path, []byte("new"), 0o600); err != nil {
			t.Fatalf("WriteFile overwrite: %v", err)
		}
		got, _ := os.ReadFile(path)
		if string(got) != "new" {
			t.Errorf("overwrite: got %q, want %q", got, "new")
		}
	})
}
