package plugins

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const (
	pluginWASM     = "plugin.wasm"
	pluginManifest = "plugin.yaml"
	installMaxSize = 200 << 20 // 200 MiB
)

// Install installs a plugin from src (https:// URL, local .tar.gz/.zip, or local directory)
// into destDir/<plugin-id>/. Returns the installed Manifest. If force is false, returns an
// error when the plugin id already exists in destDir.
func Install(ctx context.Context, src, destDir string, force bool) (*Manifest, error) {
	var srcDir string
	var cleanup func()
	var err error

	switch {
	case strings.HasPrefix(src, "http://") || strings.HasPrefix(src, "https://"):
		srcDir, cleanup, err = downloadAndExtract(ctx, src)
	case strings.HasSuffix(src, ".tar.gz") || strings.HasSuffix(src, ".tgz"):
		srcDir, cleanup, err = extractArchiveTarGz(src)
	case strings.HasSuffix(src, ".zip"):
		srcDir, cleanup, err = extractArchiveZip(src)
	default:
		// Treat as a local directory.
		info, statErr := os.Stat(src)
		if statErr != nil {
			return nil, fmt.Errorf("plugin source %q: %w", src, statErr)
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("plugin source %q is not a directory, .tar.gz, .zip, or URL", src)
		}
		srcDir = src
	}
	if err != nil {
		return nil, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	manifestPath := filepath.Join(srcDir, pluginManifest)
	m, err := loadManifest(manifestPath)
	if err != nil {
		return nil, fmt.Errorf("install: %w", err)
	}
	wasmPath := filepath.Join(srcDir, pluginWASM)
	if _, err := os.Stat(wasmPath); err != nil {
		return nil, fmt.Errorf("install: %s not found in plugin source", pluginWASM)
	}

	destPlugin := filepath.Join(destDir, m.ID)
	if _, err := os.Stat(destPlugin); err == nil && !force {
		return nil, fmt.Errorf("plugin %q already installed at %s (use --force to overwrite)", m.ID, destPlugin)
	}
	if err := os.MkdirAll(destDir, 0o750); err != nil {
		return nil, fmt.Errorf("create plugins dir: %w", err)
	}
	if err := os.RemoveAll(destPlugin); err != nil {
		return nil, fmt.Errorf("remove existing plugin dir: %w", err)
	}
	if err := os.MkdirAll(destPlugin, 0o750); err != nil {
		return nil, fmt.Errorf("create plugin dir: %w", err)
	}
	if err := copyFile(manifestPath, filepath.Join(destPlugin, pluginManifest)); err != nil {
		return nil, fmt.Errorf("copy manifest: %w", err)
	}
	if err := copyFile(wasmPath, filepath.Join(destPlugin, pluginWASM)); err != nil {
		return nil, fmt.Errorf("copy wasm: %w", err)
	}
	return &m, nil
}

func downloadAndExtract(ctx context.Context, url string) (string, func(), error) {
	client := &http.Client{Timeout: 5 * time.Minute}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", nil, err
	}
	req.Header.Set("User-Agent", "honey-plugins/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return "", nil, fmt.Errorf("GET %q: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return "", nil, fmt.Errorf("GET %q: status %s: %s", url, resp.Status, strings.TrimSpace(string(slurp)))
	}

	tmp, err := os.CreateTemp("", "honey-plugin-*.tar.gz")
	if err != nil {
		return "", nil, err
	}
	tmpName := tmp.Name()
	if _, err := io.Copy(tmp, io.LimitReader(resp.Body, installMaxSize)); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return "", nil, fmt.Errorf("download %q: %w", url, err)
	}
	_ = tmp.Close()

	var dir string
	var cleanup func()
	lower := strings.ToLower(url)
	switch {
	case strings.Contains(lower, ".zip"):
		dir, cleanup, err = extractArchiveZip(tmpName)
	default:
		dir, cleanup, err = extractArchiveTarGz(tmpName)
	}
	wrappedCleanup := func() {
		_ = os.Remove(tmpName)
		if cleanup != nil {
			cleanup()
		}
	}
	if err != nil {
		wrappedCleanup()
		return "", nil, err
	}
	return dir, wrappedCleanup, nil
}

func extractArchiveTarGz(path string) (string, func(), error) {
	f, err := os.Open(path) // #nosec G304 -- user-supplied archive path
	if err != nil {
		return "", nil, err
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return "", nil, fmt.Errorf("gzip %q: %w", path, err)
	}
	defer gr.Close()

	return extractTar(tar.NewReader(gr), path)
}

func extractTar(tr *tar.Reader, src string) (string, func(), error) {
	dir, err := os.MkdirTemp("", "honey-plugin-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			cleanup()
			return "", nil, fmt.Errorf("read tar %q: %w", src, err)
		}
		base := filepath.Base(hdr.Name)
		if base != pluginManifest && base != pluginWASM {
			continue
		}
		dest := filepath.Join(dir, base)
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- dest is under temp dir
		if err != nil {
			cleanup()
			return "", nil, err
		}
		if _, err := io.Copy(out, io.LimitReader(tr, installMaxSize)); err != nil { // #nosec G110 -- limited
			_ = out.Close()
			cleanup()
			return "", nil, err
		}
		_ = out.Close()
	}
	return dir, cleanup, nil
}

func extractArchiveZip(path string) (string, func(), error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", nil, fmt.Errorf("open zip %q: %w", path, err)
	}
	defer zr.Close()

	dir, err := os.MkdirTemp("", "honey-plugin-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(dir) }

	for _, f := range zr.File {
		base := filepath.Base(f.Name)
		if base != pluginManifest && base != pluginWASM {
			continue
		}
		dest := filepath.Join(dir, base)
		rc, err := f.Open()
		if err != nil {
			cleanup()
			return "", nil, err
		}
		out, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- dest is under temp dir
		if err != nil {
			_ = rc.Close()
			cleanup()
			return "", nil, err
		}
		if _, err := io.Copy(out, io.LimitReader(rc, installMaxSize)); err != nil { // #nosec G110 -- limited
			_ = rc.Close()
			_ = out.Close()
			cleanup()
			return "", nil, err
		}
		_ = rc.Close()
		_ = out.Close()
	}
	return dir, cleanup, nil
}

func copyFile(src, dst string) error {
	in, err := os.Open(src) // #nosec G304 -- internal use only
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600) // #nosec G304 -- dst is under plugins dir
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}
