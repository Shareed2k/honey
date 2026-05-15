package webserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"os/exec"
	"strings"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/ui"
)

// ptyMuxSessionName builds a tmux/zellij session name from the client-provided id.
// Only safe ASCII characters are kept; if nothing remains, a short stable digest is used
// so multiplexer argv never embed raw untrusted strings (gosec G204).
func ptyMuxSessionName(sessionID string) string {
	const prefix = "honey_"
	s := strings.TrimSpace(sessionID)
	var b strings.Builder
	b.Grow(len(prefix) + 64)
	b.WriteString(prefix)
	for i := 0; i < len(s) && b.Len() < len(prefix)+64; i++ {
		c := s[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			b.WriteByte(c)
		}
	}
	if b.Len() == len(prefix) {
		sum := sha256.Sum256([]byte(s))
		b.WriteString(hex.EncodeToString(sum[:8]))
	}
	return b.String()
}

// ptyWinsize clamps terminal dimensions to a valid pty.Winsize range (defaults match handleWebSSH).
func ptyWinsize(cols, rows int) pty.Winsize {
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}
	if cols > math.MaxUint16 {
		cols = math.MaxUint16
	}
	if rows > math.MaxUint16 {
		rows = math.MaxUint16
	}
	return pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)}
}

func handleWebPtyProxy(conn *websocket.Conn, helloRaw []byte, hello WSHello, recorder *ui.SessionRecorder) error {
	zap.L().Debug("handleWebPtyProxy: starting local multiplexer", zap.String("session_id", hello.SessionID))

	bin, err := os.Executable()
	if err != nil {
		zap.L().Error("handleWebPtyProxy: failed to get executable", zap.Error(err))
		return fmt.Errorf("failed to get executable: %w", err)
	}

	encodedPayload := base64.StdEncoding.EncodeToString(helloRaw)
	muxName := ptyMuxSessionName(hello.SessionID)

	// Build multiplexer command (argv: fixed multiplexer literals, sanitized session name,
	// path from os.Executable(), base64(JSON) from the same WS hello the server already accepted).
	var cmd *exec.Cmd
	if _, err := exec.LookPath("zellij"); err == nil {
		cmd = exec.Command("zellij", "attach", "-c", "-f", muxName, "--", bin, "pty-proxy", encodedPayload) // #nosec G204 -- see comment above
		zap.L().Debug("handleWebPtyProxy: using zellij", zap.Strings("args", cmd.Args))
	} else if _, err := exec.LookPath("tmux"); err == nil {
		cmd = exec.Command("tmux", "new-session", "-A", "-D", "-s", muxName, bin, "pty-proxy", encodedPayload) // #nosec G204 -- see comment above
		zap.L().Debug("handleWebPtyProxy: using tmux", zap.Strings("args", cmd.Args))
	} else {
		zap.L().Debug("handleWebPtyProxy: no multiplexer found, falling back")
		return fmt.Errorf("neither zellij nor tmux found on the server")
	}

	ptmx, err := pty.Start(cmd)
	if err != nil {
		zap.L().Error("handleWebPtyProxy: failed to start pty", zap.Error(err))
		return fmt.Errorf("failed to start pty: %w", err)
	}
	zap.L().Debug("handleWebPtyProxy: pty started successfully")
	defer ptmx.Close()

	ws := ptyWinsize(hello.Cols, hello.Rows)
	if err := pty.Setsize(ptmx, &ws); err != nil {
		zap.L().Warn("failed to resize pty", zap.Error(err))
	}

	done := make(chan struct{})

	// Read from PTY -> Write to WS
	go func() {
		defer close(done)
		buf := make([]byte, 4096)
		for {
			n, err := ptmx.Read(buf)
			if n > 0 {
				out := buf[:n]
				if recorder != nil {
					recorder.RecordData("stdout", out)
				}
				if werr := conn.WriteMessage(websocket.BinaryMessage, out); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	// Read from WS -> Write to PTY
	go func() {
		defer ptmx.Close() // Force PTY to close if WS drops!
		for {
			mt, payload, err := conn.ReadMessage()
			if err != nil {
				return
			}
			switch mt {
			case websocket.BinaryMessage:
				if recorder != nil {
					recorder.RecordData("stdin", payload)
				}
				_, _ = ptmx.Write(payload)
			case websocket.TextMessage:
				var rz wsResize
				if json.Unmarshal(payload, &rz) == nil && rz.Type == "resize" {
					if rz.Cols > 0 && rz.Rows > 0 {
						if recorder != nil {
							recorder.RecordResize(rz.Cols, rz.Rows)
						}
						ws := ptyWinsize(rz.Cols, rz.Rows)
						_ = pty.Setsize(ptmx, &ws)
					}
				} else if rz.Type == "detach" {
					return // Just stop pumping, let PTY live (it will close via defer)
				}
			}
		}
	}()

	<-done
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
	}
	return nil
}
