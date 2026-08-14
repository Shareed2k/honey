// Package intercept implements honey's OPA-gated, audited local-to-cluster
// interception session. It deploys an interception agent as an ephemeral
// container in the target pod, delivers a per-session token, extracts the
// per-platform injector library, and gates and audits the whole flow. This
// file mints and delivers the session token.
package intercept

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// tokenBytes is the number of random bytes drawn for a session token. 32 bytes
// (256 bits) is well above any guessing-resistance floor.
const tokenBytes = 32

// tokenFileName is the basename of the local token file written into the
// session's temporary directory.
const tokenFileName = "token"

// agentRunDir is the in-agent directory the interception agent reads its
// per-session token file from. It lives under /tmp, not /var/run, because the
// agent runs as a non-root user (the agent image's default, e.g. mogate's
// 65532): that user cannot create a directory under root-owned /var/run, but
// /tmp is world-writable, so honey's exec delivery (`mkdir -p` + write) and the
// agent's read both succeed without running the agent as root. The token
// basename is appended at delivery time.
const agentRunDir = "/tmp/mogate"

// PodExecer runs a command inside a target pod, streaming stdin to the process
// and its stdout and stderr to the given writers. honey's Kubernetes provider
// satisfies this interface.
type PodExecer interface {
	// ExecInPod executes cmd in the pod, wiring stdin, stdout, and stderr.
	ExecInPod(ctx context.Context, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error
}

// mintToken returns a fresh session token: 32 random bytes drawn from
// crypto/rand, hex-encoded. The result is a secret and must never be logged or
// placed on a command line.
func mintToken() (string, error) {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("intercept: mint token: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// writeTokenFile writes token to a mode-0600 file inside dir (the caller is
// expected to pass a mode-0700 temporary directory) and returns the file path.
// The token is written to disk only; it is never logged.
func writeTokenFile(dir, token string) (string, error) {
	p := filepath.Join(dir, tokenFileName)
	if err := os.WriteFile(p, []byte(token), 0o600); err != nil {
		return "", fmt.Errorf("intercept: write token file: %w", err)
	}
	return p, nil
}

// deliverToken writes token into the interception agent at its token path by
// executing a shell command that reads the token from stdin. It writes to a
// temporary file and renames it into place so a reader that polls for the token
// (the agent waits for this file at startup) never observes a partially written
// value — the rename is atomic within the directory. The token is never passed
// as a command-line argument or environment variable, and is never included in
// the returned error.
func deliverToken(ctx context.Context, exec PodExecer, token string) error {
	dst := agentRunDir + "/" + tokenFileName
	tmp := dst + ".tmp"
	cmd := []string{"sh", "-c", "umask 077; mkdir -p " + agentRunDir + " && cat > " + tmp + " && mv " + tmp + " " + dst}
	var stderr bytes.Buffer
	if err := exec.ExecInPod(ctx, cmd, strings.NewReader(token), io.Discard, &stderr); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("intercept: deliver token: %w: %s", err, msg)
		}
		return fmt.Errorf("intercept: deliver token: %w", err)
	}
	return nil
}
