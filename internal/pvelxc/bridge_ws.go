// Package pvelxc implements Proxmox VE LXC and QEMU serial console (termproxy/vncwebsocket) bridging for the web UI and TUI.
package pvelxc

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type wsResizeMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func bridgePingLoop(ctx context.Context, done <-chan struct{}, pve *Session) {
	t := time.NewTicker(30 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-t.C:
			if err := pve.WritePing(); err != nil {
				return
			}
		}
	}
}

func bridgePVEToBrowserLoop(browser *websocket.Conn, pve *Session, rec Recorder, exit func(error)) {
	bw := &wsWriter{conn: browser, mu: &sync.Mutex{}}
	for {
		mt, msg, rerr := pve.ReadMessage()
		if rerr != nil {
			if websocket.IsCloseError(rerr, websocket.CloseNormalClosure, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				exit(nil)
			} else {
				exit(rerr)
			}
			return
		}
		if rec != nil {
			rec.RecordData("stdout", msg)
		}
		if mt != websocket.BinaryMessage && mt != websocket.TextMessage {
			continue
		}
		if _, werr := bw.Write(msg); werr != nil {
			exit(werr)
			return
		}
	}
}

func bridgeBrowserToPVELoop(browser *websocket.Conn, pve *Session, rec Recorder, exit func(error)) {
	for {
		mt, payload, rerr := browser.ReadMessage()
		if rerr != nil {
			exit(rerr)
			return
		}
		switch mt {
		case websocket.BinaryMessage:
			detached, werr := StdinChunkToPVE(pve, payload, rec)
			if detached {
				exit(nil)
				return
			}
			if werr != nil {
				exit(werr)
				return
			}
		case websocket.TextMessage:
			if bridgeTryDetachOrResize(pve, payload, rec, exit) {
				return
			}
		default:
		}
	}
}

func bridgeTryDetachOrResize(pve *Session, payload []byte, rec Recorder, exit func(error)) bool {
	var meta struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(payload, &meta) == nil && meta.Type == "detach" {
		exit(nil)
		return true
	}
	var rz wsResizeMsg
	if json.Unmarshal(payload, &rz) != nil || rz.Type != "resize" {
		return false
	}
	c, rw := rz.Cols, rz.Rows
	if c <= 0 || rw <= 0 {
		return false
	}
	if rec != nil {
		rec.RecordResize(c, rw)
	}
	if err := pve.WriteResize(rw, c); err != nil {
		exit(err)
		return true
	}
	return false
}

// BridgeWebSocket copies between a browser websocket and an opened PVE LXC Session (same roles as the web UI handler).
func BridgeWebSocket(ctx context.Context, browser *websocket.Conn, pve *Session, rec Recorder) {
	done := make(chan struct{})
	defer close(done)

	go bridgePingLoop(ctx, done, pve)

	errCh := make(chan error, 1)
	var exitOnce sync.Once
	exit := func(err error) {
		exitOnce.Do(func() {
			errCh <- err
		})
	}

	go bridgePVEToBrowserLoop(browser, pve, rec, exit)
	go bridgeBrowserToPVELoop(browser, pve, rec, exit)

	waitErr := <-errCh
	if rec != nil {
		rec.RecordError(waitErr)
	}
	_ = pve.writeMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
	_ = pve.Close()
	bridgeWriteBrowserClosed(browser, waitErr)
}

func bridgeWriteBrowserClosed(browser *websocket.Conn, waitErr error) {
	if waitErr != nil && !websocket.IsCloseError(waitErr, websocket.CloseGoingAway, websocket.CloseNormalClosure) &&
		!strings.Contains(waitErr.Error(), "use of closed network connection") {
		_ = browser.WriteMessage(websocket.TextMessage, []byte(`{"closed":true,"error":"`+escapeJSON(waitErr.Error())+`"}`))
		return
	}
	_ = browser.WriteMessage(websocket.TextMessage, []byte(`{"closed":true}`))
}

type wsWriter struct {
	conn *websocket.Conn
	mu   *sync.Mutex
}

func (w *wsWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.conn.WriteMessage(websocket.BinaryMessage, p); err != nil {
		return 0, err
	}
	return len(p), nil
}

func escapeJSON(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	s = strings.ReplaceAll(s, "\n", `\n`)
	s = strings.ReplaceAll(s, "\r", `\r`)
	return s
}
