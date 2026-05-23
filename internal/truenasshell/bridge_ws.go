// Package truenasshell bridges honey web terminals to TrueNAS /websocket/shell.
package truenasshell

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/gorilla/websocket"
)

type wsResizeMsg struct {
	Type string `json:"type"`
	Cols int    `json:"cols"`
	Rows int    `json:"rows"`
}

func bridgeShellToBrowserLoop(browser *websocket.Conn, sess *Session, rec Recorder, exit func(error)) {
	bw := &wsWriter{conn: browser, mu: &sync.Mutex{}}
	for {
		mt, msg, rerr := sess.ReadMessage()
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

func bridgeBrowserToShellLoop(browser *websocket.Conn, sess *Session, rec Recorder, exit func(error)) {
	for {
		mt, payload, rerr := browser.ReadMessage()
		if rerr != nil {
			exit(rerr)
			return
		}
		switch mt {
		case websocket.BinaryMessage:
			detached, werr := StdinChunkToShell(sess, payload, rec)
			if detached {
				exit(nil)
				return
			}
			if werr != nil {
				exit(werr)
				return
			}
		case websocket.TextMessage:
			if bridgeTryDetachOrResize(sess, payload, rec, exit) {
				return
			}
		default:
		}
	}
}

func bridgeTryDetachOrResize(sess *Session, payload []byte, rec Recorder, exit func(error)) bool {
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
	if err := sess.Resize(c, rw); err != nil {
		exit(err)
		return true
	}
	return false
}

// BridgeWebSocket copies between a browser websocket and an opened TrueNAS shell session.
func BridgeWebSocket(_ context.Context, browser *websocket.Conn, sess *Session, rec Recorder) {
	errCh := make(chan error, 1)
	var exitOnce sync.Once
	exit := func(err error) {
		exitOnce.Do(func() {
			errCh <- err
		})
	}

	go bridgeShellToBrowserLoop(browser, sess, rec, exit)
	go bridgeBrowserToShellLoop(browser, sess, rec, exit)

	waitErr := <-errCh
	if rec != nil {
		rec.RecordError(waitErr)
	}
	_ = sess.Close()
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
