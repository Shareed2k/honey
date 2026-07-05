// Python and Curl-based agent-transfer fallback transport: detect python/curl on the
// remote, generate shell or python commands for presigned PUT/GET, and parse
// ETag output from multipart loops.

package engine

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/transferagent/presign"
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

// encodeScriptParams marshals the dynamic values as JSON and base64-encodes
// them. The base64 alphabet contains no quote characters, so the blob is safe
// to embed inside both the Python single-quoted literal that decodes it and the
// outer shell single-quoted wrapper — untrusted paths/URLs/headers can never
// break out of either layer.
func encodeScriptParams(params map[string]any) string {
	blob, _ := json.Marshal(params)
	return base64.StdEncoding.EncodeToString(blob)
}

func buildPythonSinglePutScript(pythonBin, srcPath string, u presign.SignedURL, size int64) string {
	b64 := encodeScriptParams(map[string]any{
		"src":     srcPath,
		"url":     u.URL,
		"size":    size,
		"headers": u.Headers,
	})
	code := fmt.Sprintf(`
import urllib.request, json, base64
p = json.loads(base64.b64decode('%s').decode())
with open(p['src'], 'rb') as f:
    req = urllib.request.Request(p['url'], data=f, method='PUT')
    req.add_header('Content-Length', str(p['size']))
    for k, v in (p['headers'] or {}).items():
        req.add_header(k, v)
    with urllib.request.urlopen(req) as resp:
        pass
`, b64)
	return fmt.Sprintf("%s -c '%s'", pythonBin, ShellSingleQuote(minifyPythonScript(code)))
}

func buildPythonDownloadScript(pythonBin, dstPath string, u presign.SignedURL) string {
	b64 := encodeScriptParams(map[string]any{
		"dst": dstPath,
		"url": u.URL,
	})
	code := fmt.Sprintf(`
import urllib.request, shutil, os, json, base64
p = json.loads(base64.b64decode('%s').decode())
os.makedirs(os.path.dirname(p['dst']) or '.', exist_ok=True)
with urllib.request.urlopen(p['url']) as r, open(p['dst'], 'wb') as f:
    shutil.copyfileobj(r, f)
`, b64)
	return fmt.Sprintf("%s -c '%s'", pythonBin, ShellSingleQuote(minifyPythonScript(code)))
}

func buildPythonMultipartScript(pythonBin, srcPath string, partSize int64, parts []presign.SignedURL) string {
	b64 := encodeScriptParams(map[string]any{
		"src":       srcPath,
		"part_size": partSize,
		"parts":     parts,
	})
	code := fmt.Sprintf(`
import sys, urllib.request, json, base64, mmap
p = json.loads(base64.b64decode('%s').decode())
urls = p['parts']
part_size = p['part_size']
with open(p['src'], 'rb') as f:
    mm = mmap.mmap(f.fileno(), 0, access=mmap.ACCESS_READ)
    for i, pt in enumerate(urls):
        u = pt['URL']
        start = i * part_size
        end = min(start + part_size, len(mm))
        req = urllib.request.Request(u, data=memoryview(mm)[start:end], method='PUT')
        for k, v in pt.get('Headers', {}).items():
            req.add_header(k, v)
        with urllib.request.urlopen(req) as resp:
            etag = resp.headers.get('ETag', '').replace('"', '')
            print(f"PART {i+1} {etag}")
`, b64)
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
