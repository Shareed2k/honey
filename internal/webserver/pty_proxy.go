package webserver

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/creack/pty"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/ui"
)

func handleWebPtyProxy(conn *websocket.Conn, helloRaw []byte, hello WSHello, recorder *ui.SessionRecorder) error {
	zap.L().Debug("handleWebPtyProxy: starting local multiplexer", zap.String("session_id", hello.SessionID))

	bin, err := os.Executable()
	if err != nil {
		zap.L().Error("handleWebPtyProxy: failed to get executable", zap.Error(err))
		return fmt.Errorf("failed to get executable: %w", err)
	}

	encodedPayload := base64.StdEncoding.EncodeToString(helloRaw)

	// Build multiplexer command
	var cmd *exec.Cmd
	if _, err := exec.LookPath("zellij"); err == nil {
		cmd = exec.Command("zellij", "attach", "-c", "-f", fmt.Sprintf("honey_%s", hello.SessionID), "--", bin, "pty-proxy", encodedPayload)
		zap.L().Debug("handleWebPtyProxy: using zellij", zap.Strings("args", cmd.Args))
	} else if _, err := exec.LookPath("tmux"); err == nil {
		cmd = exec.Command("tmux", "new-session", "-A", "-D", "-s", fmt.Sprintf("honey_%s", hello.SessionID), bin, "pty-proxy", encodedPayload)
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

	if err := pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(hello.Cols), Rows: uint16(hello.Rows)}); err != nil {
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
						_ = pty.Setsize(ptmx, &pty.Winsize{Cols: uint16(rz.Cols), Rows: uint16(rz.Rows)})
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
