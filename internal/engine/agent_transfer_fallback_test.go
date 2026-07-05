package engine

import (
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/transferagent/presign"
)

type stubRunner struct {
	out string
	err error
}

func (s stubRunner) RunRemoteCmd(_ string, _ string) (string, error) {
	return s.out, s.err
}

// TestDetectFallbackCapability_present_python ...
func TestDetectFallbackCapability_present_python(t *testing.T) {
	r := stubRunner{out: "python3\n", err: nil}
	capValue, err := detectFallbackCapabilityViaRunner(r, "alice@host1-python")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if capValue != "python3" {
		t.Fatalf("expected python3, got %q", capValue)
	}
}

// TestDetectFallbackCapability_present_curl ...
func TestDetectFallbackCapability_present_curl(t *testing.T) {
	r := stubRunner{out: "curl\n", err: nil}
	capValue, err := detectFallbackCapabilityViaRunner(r, "alice@host1-curl")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if capValue != "curl" {
		t.Fatalf("expected curl, got %q", capValue)
	}
}

// TestDetectFallbackCapability_missing ...
func TestDetectFallbackCapability_missing(t *testing.T) {
	r := stubRunner{out: "", err: errors.New("exit status 1")}
	capValue, err := detectFallbackCapabilityViaRunner(r, "alice@host1-missing")
	if err != nil {
		t.Fatalf("err should be nil for clean miss, got: %v", err)
	}
	if capValue != "" {
		t.Fatalf("expected empty capability, got %q", capValue)
	}
}

// TestBuildSinglePutScript ...
func TestBuildSinglePutScript(t *testing.T) {
	u := presign.SignedURL{
		Method: "PUT",
		URL:    "https://test-bucket.s3.amazonaws.com/honey-transfer/abc.bin?X-Amz-Signature=...",
	}
	script := buildSinglePutScript("curl", "/data/file.bin", u, 12345)
	if !strings.Contains(script, "curl -fsSL -f") {
		t.Fatal("missing curl flags")
	}
	if !strings.Contains(script, "-X PUT") {
		t.Fatal("missing PUT method")
	}
	if !strings.Contains(script, `--data-binary @'/data/file.bin'`) {
		t.Fatalf("missing --data-binary @file:\n%s", script)
	}
	if !strings.Contains(script, "Content-Length: 12345") {
		t.Fatalf("missing or wrong Content-Length:\n%s", script)
	}
	if !strings.Contains(script, u.URL) {
		t.Fatal("missing URL")
	}

	pyScript := buildSinglePutScript("python3", "/data/file.bin", u, 12345)
	if !strings.Contains(pyScript, "python3 -c '") {
		t.Fatal("missing python invocation")
	}
	if !strings.Contains(pyScript, "urllib.request.Request") {
		t.Fatal("missing python urlopen")
	}
}

// TestBuildDownloadScript ...
func TestBuildDownloadScript(t *testing.T) {
	u := presign.SignedURL{Method: "GET", URL: "https://example.com/get?sig=1"}
	script := buildDownloadScript("curl", "/dst/path.bin", u)
	if !strings.Contains(script, "curl -fsSL -f") {
		t.Fatal("missing curl flags")
	}
	if !strings.Contains(script, "--create-dirs -o '/dst/path.bin'") {
		t.Fatal("missing output path")
	}
	if !strings.Contains(script, u.URL) {
		t.Fatal("missing URL")
	}

	pyScript := buildDownloadScript("python", "/dst/path.bin", u)
	if !strings.Contains(pyScript, "python -c '") {
		t.Fatal("missing python invocation")
	}
	if !strings.Contains(pyScript, "shutil.copyfileobj") {
		t.Fatal("missing shutil in python download script")
	}
}

// TestBuildMultipartScript ...
func TestBuildMultipartScript(t *testing.T) {
	parts := []presign.SignedURL{
		{Method: "PUT", URL: "https://bucket/?u=1&p=1"},
		{Method: "PUT", URL: "https://bucket/?u=1&p=2"},
		{Method: "PUT", URL: "https://bucket/?u=1&p=3"},
	}
	script := buildMultipartScript("curl", "/src/file.bin", 64<<20, parts)
	if !strings.Contains(script, "set -e") {
		t.Fatal("missing set -e")
	}
	if !strings.Contains(script, "https://bucket/?u=1&p=2") {
		t.Fatalf("missing part 2 URL:\n%s", script)
	}
	if !strings.Contains(script, `echo "PART`) {
		t.Fatalf("missing PART output:\n%s", script)
	}
	if !strings.Contains(script, "dd if='/src/file.bin'") {
		t.Fatalf("missing dd command:\n%s", script)
	}

	pyScript := buildMultipartScript("python", "/src/file.bin", 64<<20, parts)
	if !strings.Contains(pyScript, "mmap.mmap") {
		t.Fatal("missing python mmap implementation")
	}
	if !strings.Contains(pyScript, "memoryview") {
		t.Fatal("missing python memoryview implementation")
	}
}

// TestBuildPythonScript_QuoteInjectionSafe verifies untrusted paths/URLs are
// base64-encoded into the Python script rather than interpolated raw, so a
// single quote cannot break out of the Python literal (or the outer shell wrap).
func TestBuildPythonScript_QuoteInjectionSafe(t *testing.T) {
	evilPath := `/data/o'brien; rm -rf /.bin`
	u := presign.SignedURL{Method: "PUT", URL: "https://bucket/key?sig='or'1=1"}

	single := buildSinglePutScript("python3", evilPath, u, 42)
	multi := buildMultipartScript("python", evilPath, 1<<20, []presign.SignedURL{u})

	for name, script := range map[string]string{"single": single, "multi": multi} {
		if !strings.Contains(script, "base64.b64decode(") {
			t.Fatalf("%s: expected base64 decode in script:\n%s", name, script)
		}
		if strings.Contains(script, "o'brien") {
			t.Fatalf("%s: raw srcPath leaked into script (injection):\n%s", name, script)
		}
		if strings.Contains(script, "'or'1=1") {
			t.Fatalf("%s: raw URL leaked into script (injection):\n%s", name, script)
		}
	}

	// The encoder must round-trip the untrusted values intact.
	b64 := encodeScriptParams(map[string]any{"src": evilPath, "url": u.URL})
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if !strings.Contains(string(raw), evilPath) || !strings.Contains(string(raw), u.URL) {
		t.Fatalf("encoder dropped values: %s", raw)
	}
}

// TestParseMultipartEtags_happy ...
func TestParseMultipartEtags_happy(t *testing.T) {
	out := `PART 1 abc123
PART 2 def456
PART 3 789xyz
`
	tags, err := parseMultipartEtags(out, 3)
	if err != nil {
		t.Fatal(err)
	}
	if len(tags) != 3 || tags[0] != "abc123" || tags[1] != "def456" || tags[2] != "789xyz" {
		t.Fatalf("got %#v", tags)
	}
}

// TestParseMultipartEtags_missing ...
func TestParseMultipartEtags_missing(t *testing.T) {
	out := `PART 1 abc123
PART 2 def456
`
	_, err := parseMultipartEtags(out, 3)
	if err == nil {
		t.Fatal("expected error for missing part 3")
	}
}

// TestParseMultipartEtags_outOfOrder ...
func TestParseMultipartEtags_outOfOrder(t *testing.T) {
	out := `PART 2 def456
PART 1 abc123
`
	tags, err := parseMultipartEtags(out, 2)
	if err != nil {
		t.Fatal(err)
	}
	if tags[0] != "abc123" || tags[1] != "def456" {
		t.Fatalf("expected ordered output, got %#v", tags)
	}
}
