package anomaly

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveONNXRuntimeLibraryDirFromEnv(t *testing.T) {
	d := t.TempDir()
	t.Setenv("HONEY_ONNXRUNTIME_LIB_DIR", d)
	got, err := resolveONNXRuntimeLibraryDir()
	if err != nil {
		t.Fatalf("resolveONNXRuntimeLibraryDir: %v", err)
	}
	if got != d {
		t.Fatalf("resolveONNXRuntimeLibraryDir = %q, want %q", got, d)
	}
}

func TestNewEmbeddedDetectorModelPathRequiresRuntimeDir(t *testing.T) {
	t.Setenv("HONEY_ONNXRUNTIME_LIB_DIR", filepath.Join(t.TempDir(), "missing"))
	m := filepath.Join(t.TempDir(), "m.onnx")
	v := filepath.Join(t.TempDir(), "vocab.txt")
	if err := os.WriteFile(m, []byte("x"), 0o600); err != nil {
		t.Fatalf("write model: %v", err)
	}
	if err := os.WriteFile(v, []byte("[PAD]\n[UNK]\n[CLS]\n[SEP]\n"), 0o600); err != nil {
		t.Fatalf("write vocab: %v", err)
	}
	_, err := NewEmbeddedDetector(Options{ModelPath: m, TokenizerPath: v})
	if err == nil {
		t.Fatal("expected missing runtime dir error")
	}
}
