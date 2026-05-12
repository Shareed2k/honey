package ui

import (
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

func TestDetectCurlCapability_present(t *testing.T) {
	r := stubRunner{out: "/usr/bin/curl /usr/bin/dd /usr/bin/awk\n", err: nil}
	ok, err := detectCurlCapabilityViaRunner(r, "alice@host1-present")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !ok {
		t.Fatal("expected curl-capable host")
	}
}

func TestDetectCurlCapability_missing(t *testing.T) {
	r := stubRunner{out: "", err: errors.New("exit status 1")}
	ok, err := detectCurlCapabilityViaRunner(r, "alice@host1-missing")
	if err != nil {
		t.Fatalf("err should be nil for clean miss, got: %v", err)
	}
	if ok {
		t.Fatal("expected non-curl-capable host")
	}
}

func TestBuildSinglePutScript(t *testing.T) {
	u := presign.SignedURL{
		Method: "PUT",
		URL:    "https://test-bucket.s3.amazonaws.com/honey-transfer/abc.bin?X-Amz-Signature=...",
	}
	script := buildSinglePutScript("/data/file.bin", u, 12345)
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
}

func TestBuildDownloadScript(t *testing.T) {
	u := presign.SignedURL{Method: "GET", URL: "https://example.com/get?sig=1"}
	script := buildDownloadScript("/dst/path.bin", u)
	if !strings.Contains(script, "curl -fsSL -f") {
		t.Fatal("missing curl flags")
	}
	if !strings.Contains(script, "--create-dirs -o '/dst/path.bin'") {
		t.Fatal("missing output path")
	}
	if !strings.Contains(script, u.URL) {
		t.Fatal("missing URL")
	}
}

func TestBuildMultipartScript(t *testing.T) {
	parts := []presign.SignedURL{
		{Method: "PUT", URL: "https://bucket/?u=1&p=1"},
		{Method: "PUT", URL: "https://bucket/?u=1&p=2"},
		{Method: "PUT", URL: "https://bucket/?u=1&p=3"},
	}
	script := buildMultipartScript("/src/file.bin", 64<<20, parts)
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
}

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

func TestParseMultipartEtags_missing(t *testing.T) {
	out := `PART 1 abc123
PART 2 def456
`
	_, err := parseMultipartEtags(out, 3)
	if err == nil {
		t.Fatal("expected error for missing part 3")
	}
}

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
