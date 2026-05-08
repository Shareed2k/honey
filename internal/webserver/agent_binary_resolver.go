package webserver

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"go.uber.org/zap"
)

func ensureExecutable(path string) error {
	st, err := os.Stat(path) // #nosec G304 -- path is selected from controlled options.
	if err != nil {
		return err
	}
	if st.IsDir() {
		return fmt.Errorf("path %q is a directory, expected executable file", path)
	}
	mode := st.Mode()
	if mode&0o111 == 0 {
		if err := os.Chmod(path, mode|0o755); err != nil {
			return err
		}
	}
	return nil
}

func normalizeTargetRuntime(goos, goarch string) (string, string, error) {
	goos = strings.ToLower(strings.TrimSpace(goos))
	goarch = strings.ToLower(strings.TrimSpace(goarch))
	if goos == "" || goarch == "" {
		return "", "", fmt.Errorf("target goos/goarch are required")
	}
	switch goarch {
	case "x86_64", "amd64":
		goarch = "amd64"
	case "aarch64", "arm64":
		goarch = "arm64"
	}
	switch goos {
	case "linux", "darwin":
	default:
		return "", "", fmt.Errorf("unsupported target os %q", goos)
	}
	return goos, goarch, nil
}

func repoRootFromSource() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("could not resolve source path for build root")
	}
	// internal/webserver/<this file> -> repo root is two levels up from internal/webserver.
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	return root, nil
}

func shouldUseUPX() bool {
	disable := strings.ToLower(strings.TrimSpace(os.Getenv("HONEY_TRANSFER_AGENT_DISABLE_UPX")))
	if disable == "1" || disable == "true" || disable == "yes" || disable == "on" {
		return false
	}
	_, err := exec.LookPath("upx")
	return err == nil
}

func packBinaryWithUPX(path string) error {
	// #nosec G204 -- path is the resolved transfer-agent binary under the server-controlled cache dir.
	cmd := exec.Command("upx", "--best", "--lzma", path)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("upx pack failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func normalizeAgentProviderFlavor(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "s3":
		return "s3"
	case "googlecloudstorage", "gcs":
		return "gcs"
	default:
		return "full"
	}
}

func buildTransferAgentBinary(root, binPath, targetOS, targetArch string) error {
	args := make([]string, 0, 7)
	args = append(args, "build", "-trimpath", "-ldflags", "-s -w")
	args = append(args, "-o", binPath, "./cmd/honey-transfer-agent")
	// #nosec G204 -- fixed "go build" argv; -o points at cache under repo root from runtime.Caller.
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(),
		"GOOS="+targetOS,
		"GOARCH="+targetArch,
		"CGO_ENABLED=0",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("build honey-transfer-agent failed: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func transferAgentSourceStamp(root string) (time.Time, error) {
	files, err := filepath.Glob(filepath.Join(root, "cmd", "honey-transfer-agent", "*.go"))
	if err != nil {
		return time.Time{}, err
	}
	files = append(files, filepath.Join(root, "go.mod"), filepath.Join(root, "go.sum"))
	latest := time.Time{}
	for _, p := range files {
		st, err := os.Stat(p) // #nosec G304 -- controlled paths under repo root.
		if err != nil {
			continue
		}
		if st.ModTime().After(latest) {
			latest = st.ModTime()
		}
	}
	if latest.IsZero() {
		return time.Time{}, fmt.Errorf("unable to determine transfer-agent source stamp")
	}
	return latest, nil
}

func isAgentBinaryFresh(binPath string, sourceStamp time.Time) bool {
	st, err := os.Stat(binPath) // #nosec G304 -- controlled cache path.
	if err != nil || st.IsDir() {
		return false
	}
	return !st.ModTime().Before(sourceStamp)
}

func (s *Server) resolveTransferAgentBinaryForTargetAndProvider(overridePath, targetOS, targetArch, provider string) (string, error) {
	targetOS, targetArch, err := normalizeTargetRuntime(targetOS, targetArch)
	if err != nil {
		return "", err
	}
	if p := strings.TrimSpace(overridePath); p != "" {
		if err := ensureExecutable(p); err != nil {
			return "", err
		}
		return p, nil
	}
	if p := strings.TrimSpace(s.opts.AgentBinaryPath); p != "" {
		if err := ensureExecutable(p); err != nil {
			return "", err
		}
		return p, nil
	}

	s.agentResolveMu.Lock()
	defer s.agentResolveMu.Unlock()
	useUPX := shouldUseUPX()
	flavor := normalizeAgentProviderFlavor(provider)
	cacheKey := targetOS + "/" + targetArch + "/" + flavor
	if useUPX {
		cacheKey += "/upx"
	}
	root, err := repoRootFromSource()
	if err != nil {
		return "", err
	}
	sourceStamp, err := transferAgentSourceStamp(root)
	if err != nil {
		return "", err
	}
	if p := strings.TrimSpace(s.agentResolvedPath[cacheKey]); p != "" {
		if err := ensureExecutable(p); err == nil && isAgentBinaryFresh(p, sourceStamp) {
			zap.L().Debug("transfer agent binary cache hit",
				zap.String("target", targetOS+"/"+targetArch),
				zap.String("provider_flavor", flavor),
				zap.String("path", p),
			)
			return p, nil
		}
	}

	cacheDir := strings.TrimSpace(s.opts.AgentBuildCacheDir)
	if cacheDir == "" {
		cacheDir = filepath.Join(os.TempDir(), "honey-transfer-agent-cache")
	}
	if err := os.MkdirAll(cacheDir, 0o750); err != nil {
		return "", err
	}
	binName := fmt.Sprintf("honey-transfer-agent-%s-%s-%s", targetOS, targetArch, flavor)
	if useUPX {
		binName += "-upx"
	}
	binPath := filepath.Join(cacheDir, binName)
	if err := ensureExecutable(binPath); err == nil && isAgentBinaryFresh(binPath, sourceStamp) {
		zap.L().Debug("transfer agent binary cache file hit",
			zap.String("target", targetOS+"/"+targetArch),
			zap.String("provider_flavor", flavor),
			zap.String("path", binPath),
		)
		s.agentResolvedPath[cacheKey] = binPath
		return binPath, nil
	}
	if err := buildTransferAgentBinary(root, binPath, targetOS, targetArch); err != nil {
		return "", err
	}
	if err := ensureExecutable(binPath); err != nil {
		return "", err
	}
	zap.L().Debug("transfer agent binary built",
		zap.String("target", targetOS+"/"+targetArch),
		zap.String("provider_flavor", flavor),
		zap.String("path", binPath),
	)
	if useUPX {
		if err := packBinaryWithUPX(binPath); err != nil {
			zap.L().Warn("transfer agent upx packing failed, using uncompressed binary",
				zap.String("path", binPath),
				zap.String("provider_flavor", flavor),
				zap.Error(err),
			)
			// Fallback to non-packed path for this target.
			cacheKey = targetOS + "/" + targetArch + "/" + flavor
			binName = fmt.Sprintf("honey-transfer-agent-%s-%s-%s", targetOS, targetArch, flavor)
			binPath = filepath.Join(cacheDir, binName)
			if err := buildTransferAgentBinary(root, binPath, targetOS, targetArch); err != nil {
				return "", fmt.Errorf("rebuild uncompressed honey-transfer-agent: %w", err)
			}
			if err := ensureExecutable(binPath); err != nil {
				return "", err
			}
		} else {
			zap.L().Debug("transfer agent binary packed with upx",
				zap.String("target", targetOS+"/"+targetArch),
				zap.String("provider_flavor", flavor),
				zap.String("path", binPath),
			)
		}
	}
	s.agentResolvedPath[cacheKey] = binPath
	return binPath, nil
}
