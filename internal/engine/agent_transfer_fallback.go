// Python and Curl-based agent-transfer fallback transport: detect python/curl on the
// remote, generate shell or python commands for presigned PUT/GET, and parse
// ETag output from multipart loops.

package engine

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/shareed2k/honey/internal/transferagent/presign"
	"go.uber.org/zap"
)

type remoteCmdRunner interface {
	RunRemoteCmd(host string, cmd string) (string, error)
}

var fallbackCapabilityCache sync.Map // map[string]string ("python3", "python", "curl", "")

func detectFallbackCapabilityViaRunner(r remoteCmdRunner, key string) (string, error) {
	if v, ok := fallbackCapabilityCache.Load(key); ok {
		return v.(string), nil
	}
	cmd := `if command -v python3 >/dev/null 2>&1; then echo python3; elif command -v python >/dev/null 2>&1 && python -c 'import sys; sys.exit(0 if sys.version_info[0] >= 3 else 1)' >/dev/null 2>&1; then echo python; elif command -v curl >/dev/null 2>&1 && command -v dd >/dev/null 2>&1 && command -v awk >/dev/null 2>&1; then echo curl; else exit 1; fi`
	out, err := r.RunRemoteCmd(key, cmd)
	if err != nil {
		if strings.Contains(err.Error(), "exit status") {
			fallbackCapabilityCache.Store(key, "")
			return "", nil
		}
		return "", fmt.Errorf("detect fallback: %w", err)
	}
	capStr := strings.TrimSpace(out)
	fallbackCapabilityCache.Store(key, capStr)
	return capStr, nil
}

func minifyPythonScript(code string) string {
	var lines []string
	for _, line := range strings.Split(code, "\n") {
		line = strings.TrimRight(line, " \t\r\n")
		if line == "" {
			continue
		}
		indent := len(line) - len(strings.TrimLeft(line, " "))
		if indent > 0 {
			line = strings.Repeat(" ", indent/4) + strings.TrimLeft(line, " ")
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

func buildPythonSinglePutScript(pythonBin, srcPath string, u presign.SignedURL, size int64) string {
	headersJSON, _ := json.Marshal(u.Headers)
	code := fmt.Sprintf(`
import urllib.request, json
with open('%s', 'rb') as f:
    req = urllib.request.Request('%s', data=f, method='PUT')
    req.add_header('Content-Length', '%d')
    headers = json.loads('%s') or {}
    for k, v in headers.items():
        req.add_header(k, v)
    with urllib.request.urlopen(req) as resp:
        pass
`, ShellSingleQuote(srcPath), u.URL, size, string(headersJSON))
	return fmt.Sprintf("%s -c '%s'", pythonBin, ShellSingleQuote(minifyPythonScript(code)))
}

func buildPythonDownloadScript(pythonBin, dstPath string, u presign.SignedURL) string {
	code := fmt.Sprintf(`
import urllib.request, shutil, os
os.makedirs(os.path.dirname('%s') or '.', exist_ok=True)
with urllib.request.urlopen('%s') as r, open('%s', 'wb') as f:
    shutil.copyfileobj(r, f)
`, ShellSingleQuote(dstPath), u.URL, ShellSingleQuote(dstPath))
	return fmt.Sprintf("%s -c '%s'", pythonBin, ShellSingleQuote(minifyPythonScript(code)))
}

func buildPythonMultipartScript(pythonBin, srcPath string, partSize int64, parts []presign.SignedURL) string {
	urlsJSON, _ := json.Marshal(parts)
	code := fmt.Sprintf(`
import sys, urllib.request, json, mmap
urls = json.loads('%s')
part_size = %d
with open('%s', 'rb') as f:
    mm = mmap.mmap(f.fileno(), 0, access=mmap.ACCESS_READ)
    for i, p in enumerate(urls):
        u = p['URL']
        start = i * part_size
        end = min(start + part_size, len(mm))
        req = urllib.request.Request(u, data=memoryview(mm)[start:end], method='PUT')
        for k, v in p.get('Headers', {}).items():
            req.add_header(k, v)
        with urllib.request.urlopen(req) as resp:
            etag = resp.headers.get('ETag', '').replace('"', '')
            print(f"PART {i+1} {etag}")
`, string(urlsJSON), partSize, ShellSingleQuote(srcPath))
	return fmt.Sprintf("%s -c '%s'", pythonBin, ShellSingleQuote(minifyPythonScript(code)))
}

func buildCurlSinglePutScript(srcPath string, u presign.SignedURL, size int64) string {
	var b strings.Builder
	b.WriteString(`curl -fsSL -f -X PUT`)
	fmt.Fprintf(&b, " -H 'Content-Length: %d'", size)
	for k, v := range u.Headers {
		fmt.Fprintf(&b, " -H '%s: %s'", k, v)
	}
	fmt.Fprintf(&b, " --data-binary @'%s'", ShellSingleQuote(srcPath))
	fmt.Fprintf(&b, " '%s'", u.URL)
	return b.String()
}

func buildCurlDownloadScript(dstPath string, u presign.SignedURL) string {
	return fmt.Sprintf("curl -fsSL -f --create-dirs -o '%s' '%s'",
		ShellSingleQuote(dstPath), u.URL)
}

func buildCurlMultipartScript(srcPath string, partSize int64, parts []presign.SignedURL) string {
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
	sb.WriteString("  etag=$( dd if='" + ShellSingleQuote(srcPath) + "' bs=\"$part_size\" skip=$((i - 1)) count=1 status=none \\\n")
	sb.WriteString("          | curl -fsSL -f -X PUT -D /dev/stderr --data-binary @- \"$url\" 2>&1 \\\n")
	sb.WriteString(`          | awk '/^[Ee][Tt][Aa][Gg]:/ { gsub(/"/, "", $2); gsub(/\r/, "", $2); print $2 }' )`)
	sb.WriteString("\n  echo \"PART $i $etag\"\n")
	sb.WriteString("done\n")
	return sb.String()
}

func buildSinglePutScript(capValue, srcPath string, u presign.SignedURL, size int64) string {
	if capValue == "python" || capValue == "python3" {
		return buildPythonSinglePutScript(capValue, srcPath, u, size)
	}
	return buildCurlSinglePutScript(srcPath, u, size)
}

func buildDownloadScript(capValue, dstPath string, u presign.SignedURL) string {
	if capValue == "python" || capValue == "python3" {
		return buildPythonDownloadScript(capValue, dstPath, u)
	}
	return buildCurlDownloadScript(dstPath, u)
}

func buildMultipartScript(capValue, srcPath string, partSize int64, parts []presign.SignedURL) string {
	if capValue == "python" || capValue == "python3" {
		return buildPythonMultipartScript(capValue, srcPath, partSize, parts)
	}
	return buildCurlMultipartScript(srcPath, partSize, parts)
}

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

// ShellSingleQuote ...
func ShellSingleQuote(s string) string {
	return strings.ReplaceAll(s, "'", `'\''`)
}

type cacheRunner struct{ cache *ClientCache }

func (r cacheRunner) RunRemoteCmd(host string, cmd string) (string, error) {
	return runOneShotRemote(r.cache, host, cmd)
}

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
