// Package transferagent resolves and cross-builds honey-transfer-agent binaries
// for target GOOS/GOARCH and optional cloud flavor (S3/GCS/full).
package transferagent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

var (
	resolveMu     sync.Mutex
	resolvedByKey = map[string]string{}
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

// NormalizeTargetRuntime maps uname-style values to GOOS/GOARCH used for cross-builds.
func NormalizeTargetRuntime(goos, goarch string) (string, string, error) {
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
	// skip=0 is runtime.Caller's own frame (GOROOT/src/runtime); skip=1 is our direct caller
	// (e.g. ResolveBinary in this file), so file is always under internal/transferagent/.
	_, file, _, ok := runtime.Caller(1)
	if !ok {
		return "", fmt.Errorf("could not resolve source path for build root")
	}
	// internal/transferagent/<caller>.go -> repo root is two levels up.
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	// Binaries built elsewhere embed the builder's absolute path; it often does not exist here.
	if st, err := os.Stat(root); err != nil || !st.IsDir() {
		return "", fmt.Errorf("repo root from caller path does not exist or is not a directory: %s", root)
	}
	goMod := filepath.Join(root, "go.mod")
	if _, err := os.Stat(goMod); err != nil {
		return "", fmt.Errorf("repo root %q missing go.mod (not a honey checkout): %w", root, err)
	}
	agentDir := filepath.Join(root, "cmd", "honey-transfer-agent")
	if st, err := os.Stat(agentDir); err != nil || !st.IsDir() {
		return "", fmt.Errorf("repo root %q missing cmd/honey-transfer-agent: %w", root, err)
	}
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

func packBinaryWithUPX(path, targetGOOS string) error {
	// #nosec G204 -- path is the resolved transfer-agent binary under a cache dir.
	// UPX on Linux cannot pack Mach-O without --force-macos (matches CI ghaction-upx args).
	args := []string{"--best", "--lzma"}
	if strings.EqualFold(strings.TrimSpace(targetGOOS), "darwin") {
		args = append(args, "--force-macos")
	}
	args = append(args, path)
	cmd := exec.Command("upx", args...)
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
	// #nosec G204 -- fixed "go build" argv; -o points at cache under repo root from repoRootFromSource.
	cmd := exec.Command("go", args...)
	cmd.Dir = root
	cmd.Env = append(
		os.Environ(),
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
		return time.Time{}, fmt.Errorf("unable to determine transfer-agent source stamp under repo root %q (need cmd/honey-transfer-agent/*.go and go.mod)", root)
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

func resolveTransferAgentFailure(
	buildErr, rootErr, stampErr error,
	lastDlErr error,
	targetOS, targetArch string,
) error {
	if buildErr != nil {
		return fmt.Errorf("build honey-transfer-agent: %w (prebuilt: same release tag as honey by default; disable %s; override %s or %s)", buildErr, agentDownloadDisableDefaultEnv, agentDownloadBaseEnv, agentDownloadURLEnv)
	}
	if rootErr != nil {
		if lastDlErr != nil {
			return fmt.Errorf("transfer agent prebuilt download failed: %w (no local checkout: %v; ensure a GitHub release for this honey version includes honey-transfer-agent-%s-%s; or set %s / %s / %s)", lastDlErr, rootErr, targetOS, targetArch, agentDownloadDisableDefaultEnv, agentDownloadBaseEnv, agentDownloadURLEnv)
		}
		return fmt.Errorf("no checkout for local build: %w (set HONEY_TRANSFER_AGENT, or prebuilt download for same honey version unless %s; override %s or %s)", rootErr, agentDownloadDisableDefaultEnv, agentDownloadBaseEnv, agentDownloadURLEnv)
	}
	if stampErr != nil {
		return fmt.Errorf("transfer agent stamp: %w (set HONEY_TRANSFER_AGENT, or prebuilt download for same honey version unless %s; override %s or %s)", stampErr, agentDownloadDisableDefaultEnv, agentDownloadBaseEnv, agentDownloadURLEnv)
	}
	return fmt.Errorf("transfer agent: could not build or download binary for %s/%s", targetOS, targetArch)
}

// resolveBinaryLocked runs the cache / build / download path; caller must hold resolveMu.
func resolveBinaryLocked(cacheDir, targetOS, targetArch, cloudProvider string) (string, error) {
	useUPX := shouldUseUPX()
	flavor := normalizeAgentProviderFlavor(cloudProvider)
	cacheKey := targetOS + "/" + targetArch + "/" + flavor
	if useUPX {
		cacheKey += "/upx"
	}

	root, rootErr := repoRootFromSource()
	var sourceStamp time.Time
	var stampErr error
	if rootErr == nil {
		sourceStamp, stampErr = transferAgentSourceStamp(root)
	}
	stampOK := rootErr == nil && stampErr == nil

	if stampOK {
		if p := strings.TrimSpace(resolvedByKey[cacheKey]); p != "" {
			if err := ensureExecutable(p); err == nil && isAgentBinaryFresh(p, sourceStamp) {
				zap.L().Debug(
					"transfer agent binary cache hit",
					zap.String("target", targetOS+"/"+targetArch),
					zap.String("provider_flavor", flavor),
					zap.String("path", p),
				)
				return p, nil
			}
		}
	}

	if strings.TrimSpace(cacheDir) == "" {
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
	// GitHub release assets match this name (never UPX-packed).
	dlBinPath := filepath.Join(cacheDir, fmt.Sprintf("honey-transfer-agent-%s-%s-%s", targetOS, targetArch, flavor))

	if stampOK {
		if err := ensureExecutable(binPath); err == nil && isAgentBinaryFresh(binPath, sourceStamp) {
			zap.L().Debug(
				"transfer agent binary cache file hit",
				zap.String("target", targetOS+"/"+targetArch),
				zap.String("provider_flavor", flavor),
				zap.String("path", binPath),
			)
			resolvedByKey[cacheKey] = binPath
			return binPath, nil
		}
	}

	var buildErr error
	if rootErr == nil {
		outPath, outKey, berr := buildAgentMaybeUPX(root, cacheDir, useUPX, targetOS, targetArch, flavor, binPath, cacheKey)
		if berr == nil {
			resolvedByKey[outKey] = outPath
			return outPath, nil
		}
		buildErr = berr
	}

	// Prebuilt assets are always uncompressed. Fall back to download when there is no usable
	// checkout, local build failed, or UPX is enabled (releases are not UPX-packed).
	tryDownload := rootErr != nil || buildErr != nil || !useUPX
	var lastDlErr error
	if tryDownload {
		if u, ok := transferAgentDownloadURL(targetOS, targetArch); ok {
			dest := dlBinPath
			dlErr := fetchAgentBinary(u, dest)
			if dlErr == nil && ensureExecutable(dest) == nil {
				zap.L().Debug(
					"transfer agent binary downloaded",
					zap.String("target", targetOS+"/"+targetArch),
					zap.String("provider_flavor", flavor),
					zap.String("url", u),
					zap.String("path", dest),
				)
				resolvedByKey[cacheKey] = dest
				return dest, nil
			}
			if dlErr != nil {
				lastDlErr = dlErr
				zap.L().Debug("transfer agent download failed", zap.String("url", u), zap.Error(dlErr))
			}
		}
	}

	return "", resolveTransferAgentFailure(buildErr, rootErr, stampErr, lastDlErr, targetOS, targetArch)
}

// ResolveBinary returns a path to honey-transfer-agent for the given target OS/arch and cloud flavor.
// overridePath wins if set; else preferredPath (e.g. server default binary); else cross-build into cacheDir.
func ResolveBinary(overridePath, preferredPath, cacheDir, targetOS, targetArch, cloudProvider string) (string, error) {
	targetOS, targetArch, err := NormalizeTargetRuntime(targetOS, targetArch)
	if err != nil {
		return "", err
	}
	if p := strings.TrimSpace(overridePath); p != "" {
		if err := ensureExecutable(p); err != nil {
			return "", err
		}
		return p, nil
	}
	if p := strings.TrimSpace(preferredPath); p != "" {
		if err := ensureExecutable(p); err != nil {
			return "", err
		}
		return p, nil
	}

	resolveMu.Lock()
	defer resolveMu.Unlock()
	return resolveBinaryLocked(cacheDir, targetOS, targetArch, cloudProvider)
}
