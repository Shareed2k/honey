package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
	"go.uber.org/zap"
)

// DetectTransferTargetRuntime runs uname on the host to determine GOOS/GOARCH for agent binaries.
func DetectTransferTargetRuntime(cache *ClientCache, sshUser string, rec hosts.Record) (string, string, error) {
	user := strings.TrimSpace(sshUser)
	if user == "" {
		user = strings.TrimSpace(os.Getenv("USER"))
	}
	if user == "" {
		user = "root"
	}
	const maxAttempts = 3
	var raw []byte
	var err error
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		var client HostClient
		client, err = cache.GetOrDial(user, rec)
		if err != nil {
			if attempt < maxAttempts && IsSSHConnTransientError(err) {
				cache.Evict(user, rec)
				time.Sleep(time.Duration(attempt) * 150 * time.Millisecond)
				continue
			}
			return "", "", err
		}
		raw, err = client.Run("uname -s; uname -m")
		if err != nil {
			if attempt < maxAttempts && IsSSHConnTransientError(err) {
				cache.Evict(user, rec)
				time.Sleep(time.Duration(attempt) * 150 * time.Millisecond)
				continue
			}
			return "", "", err
		}
		break
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) < 2 {
		return "", "", fmt.Errorf("unexpected uname output: %q", strings.TrimSpace(string(raw)))
	}
	goos := strings.ToLower(strings.TrimSpace(lines[0]))
	goarch := strings.ToLower(strings.TrimSpace(lines[1]))
	switch goos {
	case "linux", "darwin":
	default:
		return "", "", fmt.Errorf("unsupported target os: %q", goos)
	}
	switch goarch {
	case "x86_64":
		goarch = "amd64"
	case "aarch64":
		goarch = "arm64"
	case "amd64", "arm64":
	default:
		return "", "", fmt.Errorf("unsupported target arch: %q", goarch)
	}
	zap.L().Debug("detected transfer target runtime",
		zap.String("host_name", rec.Name),
		zap.String("provider", rec.Provider),
		zap.String("goos", goos),
		zap.String("goarch", goarch),
	)
	return goos, goarch, nil
}

// TransferStagingObjectKey builds a unique object key when the caller leaves cloud.Object empty.
func TransferStagingObjectKey(cloud AgentCloudBackend, src, dst hosts.Record) string {
	if strings.TrimSpace(cloud.Object) != "" {
		return strings.TrimSpace(cloud.Object)
	}
	prefix := strings.Trim(strings.TrimSpace(cloud.Prefix), "/")
	source := strings.ReplaceAll(strings.TrimSpace(src.Name), " ", "_")
	if source == "" {
		source = strings.ReplaceAll(strings.TrimSpace(src.PrimaryIP), " ", "_")
	}
	dest := strings.ReplaceAll(strings.TrimSpace(dst.Name), " ", "_")
	if dest == "" {
		dest = strings.ReplaceAll(strings.TrimSpace(dst.PrimaryIP), " ", "_")
	}
	if source == "" {
		source = "source"
	}
	if dest == "" {
		dest = "destination"
	}
	base := fmt.Sprintf("%s_to_%s_%d", source, dest, time.Now().UTC().UnixNano())
	if prefix == "" {
		return base
	}
	return prefix + "/" + base
}
