package truenasshell

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
)

const (
	defaultExecOverall     = 5 * time.Minute
	defaultExecIdle        = 15 * time.Second
	shellStartupDrainMax   = 8 * time.Second
	shellStartupDrainIdle  = 500 * time.Millisecond
	shellPostDrainFlush    = 200 * time.Millisecond
	maxRemoteCommandOutput = 6000
	defaultExecRows        = 24
	defaultExecCols        = 120
)

type shellRead struct {
	mt   int
	data []byte
	err  error
}

// RunRemoteCommand runs one non-interactive shell command over an API shell session.
func RunRemoteCommand(ctx context.Context, b truenasprovider.TrueNASBackendRuntime, rec hosts.Record, remoteCmd string, maxOutputBytes int) (output []byte, exitCode int, err error) {
	remoteCmd = strings.TrimSpace(remoteCmd)
	if remoteCmd == "" {
		return nil, 0, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	overall, cancel := context.WithTimeout(ctx, defaultExecOverall)
	defer cancel()

	sess, err := OpenSession(overall, b, rec, defaultExecRows, defaultExecCols)
	if err != nil {
		return nil, -1, err
	}
	defer func() { _ = sess.Close() }()

	return execOverAPIShell(overall, sess, remoteCmd, maxOutputBytes)
}

const dumbPipeAgentPython = `
import sys, json, subprocess, base64
for line in sys.stdin:
    if not line.strip(): continue
    try:
        req = json.loads(line)
        cmd = req.get("cmd", "")
        if cmd == "exit": break
        p = subprocess.Popen(cmd, shell=True, stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
        out, _ = p.communicate()
        res = {"code": p.returncode, "out_b64": base64.b64encode(out).decode('ascii')}
        sys.stdout.write(json.dumps(res) + "\n")
        sys.stdout.flush()
    except Exception as e:
        sys.stdout.write(json.dumps({"code": 1, "out_b64": base64.b64encode(str(e).encode()).decode('ascii')}) + "\n")
        sys.stdout.flush()
`

// execOverAPIShell establishes a dumb pipe session over the PTY.
func execOverAPIShell(ctx context.Context, sess *Session, remoteCmd string, maxOutputBytes int) ([]byte, int, error) {
	ch, readerDone := startShellReader(ctx, sess)
	defer func() {
		_ = sess.Close()
		<-readerDone
	}()

	drainCtx, drainCancel := context.WithTimeout(ctx, shellStartupDrainMax)
	if err := discardUntilIdle(drainCtx, ch, shellStartupDrainIdle); err != nil {
		drainCancel()
		return nil, -1, fmt.Errorf("truenas shell startup: %w", err)
	}
	drainCancel()

	agentB64 := base64.StdEncoding.EncodeToString([]byte(dumbPipeAgentPython))
	bootstrapCmd := fmt.Sprintf("stty -echo 2>/dev/null; printf %%s %s | base64 -d | python3\n", shellSingleQuoted(agentB64))
	if err := sess.WriteBinary([]byte(bootstrapCmd)); err != nil {
		return nil, -1, fmt.Errorf("truenas shell write bootstrap: %w", err)
	}

	flushCtx, flushCancel := context.WithTimeout(ctx, shellPostDrainFlush)
	_ = discardUntilIdle(flushCtx, ch, shellPostDrainFlush/2)
	flushCancel()

	req := map[string]string{"cmd": remoteCmd}
	reqBytes, _ := json.Marshal(req)
	if err := sess.WriteBinary(append(reqBytes, '\n')); err != nil {
		return nil, -1, fmt.Errorf("truenas shell write command: %w", err)
	}

	return readAgentResponse(ctx, ch, defaultExecIdle, maxOutputBytes)
}

func shellSingleQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, `'`, `'\''`) + "'"
}

type agentResponse struct {
	Code   int    `json:"code"`
	OutB64 string `json:"out_b64"`
}

func readAgentResponse(ctx context.Context, ch <-chan shellRead, idleTimeout time.Duration, maxOutputBytes int) ([]byte, int, error) {
	var buf bytes.Buffer
	idle := time.NewTimer(idleTimeout)
	defer idle.Stop()

	for {
		select {
		case <-ctx.Done():
			return nil, -1, ctx.Err()
		case <-idle.C:
			return nil, -1, fmt.Errorf("truenas shell: timed out waiting for agent response")
		case r, ok := <-ch:
			if !ok {
				return nil, -1, fmt.Errorf("truenas shell: connection closed")
			}
			if r.err != nil {
				return nil, -1, fmt.Errorf("truenas shell read: %w", r.err)
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(idleTimeout)

			_, _ = buf.Write(r.data)

			// Try to find a valid JSON line
			lines := strings.Split(buf.String(), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if !strings.HasPrefix(line, "{") || !strings.HasSuffix(line, "}") {
					continue
				}
				var res agentResponse
				if err := json.Unmarshal([]byte(line), &res); err == nil {
					outBytes, _ := base64.StdEncoding.DecodeString(res.OutB64)
					limit := maxOutputBytes
					if limit == 0 {
						limit = maxRemoteCommandOutput
					}
					if limit > 0 && len(outBytes) > limit {
						outBytes = append(outBytes[:limit], []byte("\n…(truncated)")...)
					}
					return outBytes, res.Code, nil
				}
			}
		}
	}
}

func startShellReader(ctx context.Context, sess *Session) (<-chan shellRead, <-chan struct{}) {
	ch := make(chan shellRead, 8)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(ch)
		for {
			mt, data, err := sess.ReadMessage()
			r := shellRead{mt: mt, data: data, err: err}
			select {
			case ch <- r:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()
	return ch, done
}

// discardUntilIdle reads and drops PTY output until idleTimeout passes with no new data.
func discardUntilIdle(ctx context.Context, ch <-chan shellRead, idleTimeout time.Duration) error {
	idle := time.NewTimer(idleTimeout)
	defer idle.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-idle.C:
			return nil
		case r, ok := <-ch:
			if !ok {
				return nil
			}
			if r.err != nil {
				return nil
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(idleTimeout)
		}
	}
}
