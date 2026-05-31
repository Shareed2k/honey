package truenasshell

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
)

const (
	honeyExitMarker        = "__HONEY_EXIT__"
	honeyOutputBegin       = "__HONEY_OUT_BEGIN__"
	honeyOutputEnd         = "__HONEY_OUT_END__"
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
func RunRemoteCommand(ctx context.Context, b truenasprovider.TrueNASBackendRuntime, rec hosts.Record, remoteCmd string) (output []byte, exitCode int, err error) {
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

	return execOverAPIShell(overall, sess, remoteCmd)
}

// execOverAPIShell uses one websocket reader for MOTD drain then command output (avoids drain/reader deadlock).
func execOverAPIShell(ctx context.Context, sess *Session, remoteCmd string) ([]byte, int, error) {
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

	flushCtx, flushCancel := context.WithTimeout(ctx, shellPostDrainFlush)
	_ = discardUntilIdle(flushCtx, ch, shellPostDrainFlush/2)
	flushCancel()

	if err := sess.WriteBinary([]byte(wrapRemoteCommand(remoteCmd))); err != nil {
		return nil, -1, fmt.Errorf("truenas shell write: %w", err)
	}

	out, code, err := readUntilExitMarker(ctx, ch, defaultExecIdle)
	if err != nil {
		return nil, -1, err
	}
	if len(out) > maxRemoteCommandOutput {
		out = append(out[:maxRemoteCommandOutput], []byte("\n…(truncated)")...)
	}
	return out, code, nil
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

func readUntilExitMarker(ctx context.Context, ch <-chan shellRead, idleTimeout time.Duration) ([]byte, int, error) {
	var buf bytes.Buffer
	idle := time.NewTimer(idleTimeout)
	defer idle.Stop()

	for {
		select {
		case <-ctx.Done():
			return buf.Bytes(), -1, ctx.Err()
		case <-idle.C:
			if buf.Len() == 0 {
				return nil, -1, fmt.Errorf("truenas shell: timed out waiting for command output")
			}
			return extractScriptOutput(buf.Bytes()), -1, nil
		case r, ok := <-ch:
			if !ok {
				if buf.Len() == 0 {
					return nil, -1, fmt.Errorf("truenas shell: connection closed")
				}
				return extractScriptOutput(buf.Bytes()), -1, nil
			}
			if r.err != nil {
				if buf.Len() == 0 {
					return nil, -1, fmt.Errorf("truenas shell read: %w", r.err)
				}
				return extractScriptOutput(buf.Bytes()), -1, nil
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(idleTimeout)
			if r.mt != websocket.TextMessage && r.mt != websocket.BinaryMessage {
				continue
			}
			_, _ = buf.Write(r.data)
			if code, found, out := findExitMarkerInBuffer(buf.Bytes()); found {
				return extractScriptOutput(out), code, nil
			}
		}
	}
}

// wrapRemoteCommand runs the script non-interactively: one PTY line decodes base64 into bash -s.
// The script prints __HONEY_OUT_BEGIN__/END so stdout can be extracted despite PTY prompt noise.
func wrapRemoteCommand(cmd string) string {
	script := fmt.Sprintf("printf '%s\\n'\n%s\nprintf '%s\\n'\n", honeyOutputBegin, cmd, honeyOutputEnd)
	b64 := base64.StdEncoding.EncodeToString([]byte(script))
	return fmt.Sprintf(
		"stty -echo 2>/dev/null; printf %%s %s | base64 -d | /bin/bash --noprofile --norc -s 2>&1; printf '\\n%s%%d\\n' \"$?\"\n",
		shellSingleQuoted(b64), honeyExitMarker,
	)
}

// extractScriptOutput keeps only bytes between __HONEY_OUT_BEGIN__ and __HONEY_OUT_END__, then strips PTY noise.
func extractScriptOutput(buf []byte) []byte {
	begin := []byte(honeyOutputBegin)
	end := []byte(honeyOutputEnd)
	bi := bytes.Index(buf, begin)
	ei := bytes.Index(buf, end)
	if bi >= 0 && ei > bi {
		buf = buf[bi+len(begin) : ei]
	}
	return stripCommandOutput(buf)
}

func shellSingleQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, `'`, `'\''`) + "'"
}

func findExitMarkerInBuffer(buf []byte) (code int, found bool, output []byte) {
	needle := []byte(honeyExitMarker)
	search := 0
	for {
		idx := bytes.Index(buf[search:], needle)
		if idx < 0 {
			return 0, false, nil
		}
		idx += search
		rest := buf[idx+len(needle):]
		code, ok := parseExitCodeSuffix(rest)
		if !ok {
			// PTY often echoes the wrapper line containing "__HONEY_EXIT__%d" before the real marker.
			search = idx + 1
			continue
		}
		return code, true, buf[:idx]
	}
}

// parseExitCodeSuffix requires at least one digit immediately after the marker (rejects echoed "%d").
func parseExitCodeSuffix(rest []byte) (code int, ok bool) {
	if len(rest) == 0 || rest[0] < '0' || rest[0] > '9' {
		return 0, false
	}
	code = 0
	for _, b := range rest {
		if b >= '0' && b <= '9' {
			code = code*10 + int(b-'0')
			continue
		}
		break
	}
	return code, true
}

// parseExitMarker scans one websocket chunk for a marker line (tests).
func parseExitMarker(chunk []byte) (exitCode int, found bool, before []byte) {
	code, found, out := findExitMarkerInBuffer(chunk)
	return code, found, out
}

// promptOnlyLine matches a shell prompt with no command output on the same line.
var promptOnlyLine = regexp.MustCompile(`^\s*[\w.-]+@[\w.\[\]-]+:.*[$#>%]\s*$`)

func stripCommandOutput(out []byte) []byte {
	s := stripANSIBytes(out)
	lines := strings.Split(strings.TrimSpace(string(s)), "\n")
	var kept []string
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		if t == "" || strings.Contains(t, honeyExitMarker) ||
			strings.Contains(t, honeyOutputBegin) || strings.Contains(t, honeyOutputEnd) {
			continue
		}
		if rest := stripPromptPrefix(t); rest != "" {
			kept = append(kept, rest)
			continue
		}
		if isPromptOnlyLine(t) {
			continue
		}
		if isShellNoiseLine(t) {
			continue
		}
		if strings.HasPrefix(t, "stty ") || strings.Contains(t, "base64 -d") {
			continue
		}
		if strings.Contains(t, "printf ") && strings.Contains(t, honeyExitMarker) {
			continue
		}
		kept = append(kept, ln)
	}
	return []byte(strings.TrimSpace(strings.Join(kept, "\n")))
}

// stripPromptPrefix returns command text after a PTY prompt on the same line (e.g. "host:~# echo hi" → "echo hi").
func stripPromptPrefix(t string) string {
	if strings.IndexByte(t, '@') < 0 {
		return ""
	}
	last := -1
	for i := 0; i < len(t); i++ {
		switch t[i] {
		case '#', '$', '>':
			last = i
		}
	}
	if last < 0 || last >= len(t)-1 {
		return ""
	}
	rest := strings.TrimSpace(t[last+1:])
	if rest == "" || isPromptOnlyLine(t) {
		return ""
	}
	return rest
}

func isPromptOnlyLine(t string) bool {
	if t == ">" || t == "%" {
		return true
	}
	return promptOnlyLine.MatchString(t)
}

func isShellNoiseLine(t string) bool {
	lower := strings.ToLower(t)
	switch {
	case strings.HasPrefix(lower, "linux truenas"),
		strings.HasPrefix(lower, "welcome to truenas"),
		strings.HasPrefix(lower, "welcome to"),
		strings.HasPrefix(lower, "last login:"),
		strings.Contains(lower, "ixsystems"),
		strings.Contains(lower, "webui, cli, and api"),
		strings.Contains(lower, "undefined behavior"),
		strings.Contains(lower, "http://truenas.com"),
		strings.Contains(lower, "lgplv3"),
		strings.Contains(lower, "gplv3"):
		return true
	}
	return false
}

func stripANSIBytes(b []byte) []byte {
	var out strings.Builder
	out.Grow(len(b))
	for i := 0; i < len(b); i++ {
		if b[i] != '\x1b' {
			out.WriteByte(b[i])
			continue
		}
		i++
		if i >= len(b) {
			break
		}
		switch b[i] {
		case '[':
			i++
			for i < len(b) && (b[i] >= '0' && b[i] <= '9' || b[i] == ';' || b[i] == '?') {
				i++
			}
			if i < len(b) {
				i++
			}
		case ']':
			i++
			for i < len(b) && b[i] != '\x07' {
				i++
			}
		default:
			out.WriteByte('\x1b')
			out.WriteByte(b[i])
		}
	}
	return []byte(out.String())
}
