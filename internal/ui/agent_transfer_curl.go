// Curl-based agent-transfer transport: detect curl/dd/awk on the remote, generate
// shell commands for presigned PUT/GET, and parse ETag output from multipart loops.

package ui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/transferagent/presign"
	"go.uber.org/zap"
)

// remoteCmdRunner is the smallest interface this file needs from the rest of ui.
// In production this is satisfied by an adapter around ClientCache; in tests
// the stubRunner injects canned output.
type remoteCmdRunner interface {
	RunRemoteCmd(host string, cmd string) (string, error)
}

// curlCapabilityCache memoizes the detect result per (sshUser, host) for the
// duration of the operator process.
var curlCapabilityCache sync.Map // map[string]bool

// detectCurlCapabilityViaRunner runs `command -v curl dd awk` on the remote and
// returns true iff all three are present. Result is memoized by key.
//
// A non-zero exit (e.g. one tool missing) is treated as a clean "not capable"
// signal, not an error. Genuine SSH errors propagate.
func detectCurlCapabilityViaRunner(r remoteCmdRunner, key string) (bool, error) {
	if v, ok := curlCapabilityCache.Load(key); ok {
		return v.(bool), nil
	}
	out, err := r.RunRemoteCmd(key, "command -v curl dd awk 2>/dev/null")
	if err != nil {
		if strings.Contains(err.Error(), "exit status") {
			curlCapabilityCache.Store(key, false)
			return false, nil
		}
		return false, fmt.Errorf("detect curl: %w", err)
	}
	parts := strings.Fields(strings.TrimSpace(out))
	ok := len(parts) >= 3
	curlCapabilityCache.Store(key, ok)
	return ok, nil
}

// buildSinglePutScript renders the shell command for a single-PUT upload.
// srcPath is the absolute file path on the remote; size is the file size in bytes.
func buildSinglePutScript(srcPath string, u presign.SignedURL, size int64) string {
	var b strings.Builder
	b.WriteString(`curl -fsSL -f -X PUT`)
	fmt.Fprintf(&b, " -H 'Content-Length: %d'", size)
	for k, v := range u.Headers {
		// signed headers from the SDK are already URL-friendly; the value may
		// contain spaces (e.g. dates) so we quote it.
		fmt.Fprintf(&b, " -H '%s: %s'", k, v)
	}
	fmt.Fprintf(&b, " --data-binary @'%s'", shellSingleQuoteEscape(srcPath))
	fmt.Fprintf(&b, " '%s'", u.URL)
	return b.String()
}

// buildDownloadScript renders the shell command for a single GET download.
func buildDownloadScript(dstPath string, u presign.SignedURL) string {
	return fmt.Sprintf("curl -fsSL -f --create-dirs -o '%s' '%s'",
		shellSingleQuoteEscape(dstPath), u.URL)
}

// buildMultipartScript renders the shell loop that streams each part of the
// source file to its corresponding presigned PUT URL and echoes "PART i etag"
// for the operator to parse.
func buildMultipartScript(srcPath string, partSize int64, parts []presign.SignedURL) string {
	var sb strings.Builder
	sb.WriteString("set -e\n")
	fmt.Fprintf(&sb, "part_size=%d\n", partSize)
	sb.WriteString("i=0\n")
	sb.WriteString("for url in")
	for _, p := range parts {
		fmt.Fprintf(&sb, " '%s'", p.URL)
	}
	sb.WriteString("; do\n")
	sb.WriteString("  i=$((i + 1))\n")
	sb.WriteString("  etag=$( dd if='" + shellSingleQuoteEscape(srcPath) + "' bs=\"$part_size\" skip=$((i - 1)) count=1 status=none \\\n")
	sb.WriteString("          | curl -fsSL -f -X PUT -D /dev/stderr --data-binary @- \"$url\" 2>&1 \\\n")
	sb.WriteString(`          | awk '/^[Ee][Tt][Aa][Gg]:/ { gsub(/"/, "", $2); gsub(/\r/, "", $2); print $2 }' )`)
	sb.WriteString("\n  echo \"PART $i $etag\"\n")
	sb.WriteString("done\n")
	return sb.String()
}

// parseMultipartEtags reads stdout lines of the form `PART <n> <etag>` and
// returns ETags ordered by part number. Returns an error if any part is missing.
func parseMultipartEtags(out string, partCount int) ([]string, error) {
	tags := make([]string, partCount)
	seen := make([]bool, partCount)
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "PART ") {
			continue
		}
		var n int
		var etag string
		if _, err := fmt.Sscanf(line, "PART %d %s", &n, &etag); err != nil {
			continue
		}
		if n < 1 || n > partCount {
			continue
		}
		tags[n-1] = etag
		seen[n-1] = true
	}
	for i, s := range seen {
		if !s {
			return nil, fmt.Errorf("missing ETag for part %d", i+1)
		}
	}
	return tags, nil
}

// shellSingleQuoteEscape escapes a string for inclusion inside single-quoted
// shell context. Any embedded single quote is replaced with the standard
// close-quote/escaped-quote/open-quote sequence.
func shellSingleQuoteEscape(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

// cacheRunner adapts a *ClientCache to the remoteCmdRunner interface so the
// curl-detect probe (and later, the actual upload/download execs) can run
// over already-pooled SSH connections.
type cacheRunner struct{ cache *ClientCache }

// RunRemoteCmd runs a single shell command on the SSH client cached under
// `host` (an SSHClientCacheKey-formatted key) and returns combined output.
func (r cacheRunner) RunRemoteCmd(host string, cmd string) (string, error) {
	return runOneShotRemote(r.cache, host, cmd)
}

// runOneShotRemote runs a single shell command on a pooled SSH client and
// returns the combined stdout/stderr as a string.
//
// `host` is the SSHClientCacheKey-formatted key the cache uses. The caller is
// expected to have populated the cache for this key first (e.g. via
// DetectTransferTargetRuntime, which dials through GetOrDial).
//
// Returns an error if the cache has no entry for `host`, or if the remote
// command exits non-zero.
func runOneShotRemote(cache *ClientCache, host, cmd string) (string, error) {
	if cache == nil {
		return "", fmt.Errorf("runOneShotRemote: nil client cache")
	}
	client := cache.getByKey(host)
	if client == nil {
		zap.L().Debug("runOneShotRemote: cache miss", zap.String("requested_key", host), zap.Any("available_keys", cache.debugKeys()))
		return "", fmt.Errorf("runOneShotRemote: no pooled client for %q", host)
	}
	out, err := client.Run(cmd)
	if err != nil {
		return string(out), err
	}
	return string(out), nil
}
