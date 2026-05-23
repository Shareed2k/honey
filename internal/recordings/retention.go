package recordings

import (
	"fmt"
	"io/fs"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"
)

// PurgeResult summarizes one retention sweep.
type PurgeResult struct {
	Deleted int
	Errors  int
}

// PurgeExpired deletes *.hrec.jsonl files in recordDir older than maxAge by modification time.
func PurgeExpired(recordDir string, maxAge time.Duration) (PurgeResult, error) {
	var res PurgeResult
	if strings.TrimSpace(recordDir) == "" || maxAge <= 0 {
		return res, nil
	}
	root, err := os.OpenRoot(recordDir)
	if err != nil {
		return res, err
	}
	defer root.Close()

	cutoff := time.Now().Add(-maxAge)
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return res, err
	}
	for _, de := range entries {
		if de.IsDir() {
			continue
		}
		name := de.Name()
		if !strings.HasSuffix(name, ".hrec.jsonl") {
			continue
		}
		if err := ValidateBaseName(name); err != nil {
			continue
		}
		info, statErr := de.Info()
		if statErr != nil {
			res.Errors++
			continue
		}
		if info.ModTime().After(cutoff) {
			continue
		}
		if rmErr := root.Remove(name); rmErr != nil {
			zap.L().Warn("recording retention: delete failed", zap.String("file", name), zap.Error(rmErr))
			res.Errors++
			continue
		}
		res.Deleted++
	}
	if res.Deleted > 0 {
		zap.L().Info("recording retention sweep",
			zap.Int("deleted", res.Deleted),
			zap.Duration("max_age", maxAge),
			zap.String("dir", recordDir),
		)
	}
	return res, nil
}

// DirStats returns aggregate size and file count for *.hrec.jsonl in recordDir.
func DirStats(recordDir string) (fileCount int, totalBytes int64, err error) {
	if strings.TrimSpace(recordDir) == "" {
		return 0, 0, fmt.Errorf("empty record dir")
	}
	root, err := os.OpenRoot(recordDir)
	if err != nil {
		return 0, 0, err
	}
	defer root.Close()
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		return 0, 0, err
	}
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".hrec.jsonl") {
			continue
		}
		if err := ValidateBaseName(de.Name()); err != nil {
			continue
		}
		info, statErr := de.Info()
		if statErr != nil {
			continue
		}
		fileCount++
		totalBytes += info.Size()
	}
	return fileCount, totalBytes, nil
}
