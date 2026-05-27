package anomaly

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

// resolveONNXRuntimeLibraryDir returns the best-effort directory containing
// ONNX runtime shared libraries.
//
// Search order:
// 1) HONEY_ONNXRUNTIME_LIB_DIR
// 2) alongside binary: ../runtime/onnx/<os>/<arch>
// 3) alongside binary: runtime/onnx/<os>/<arch>
func resolveONNXRuntimeLibraryDir() (string, error) {
	if v := strings.TrimSpace(os.Getenv("HONEY_ONNXRUNTIME_LIB_DIR")); v != "" {
		if st, err := os.Stat(v); err == nil && st.IsDir() {
			return v, nil
		}
		return "", fmt.Errorf("HONEY_ONNXRUNTIME_LIB_DIR not found or not a directory: %s", v)
	}

	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	base := filepath.Dir(exe)
	rel := filepath.Join("runtime", "onnx", runtime.GOOS, runtime.GOARCH)
	for _, cand := range []string{filepath.Join(base, "..", rel), filepath.Join(base, rel)} {
		if st, err := os.Stat(cand); err == nil && st.IsDir() {
			return cand, nil
		}
	}
	return "", fmt.Errorf("onnx runtime library directory not found")
}
