package webserver

import (
	"context"

	"github.com/gorilla/websocket"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
	"github.com/shareed2k/honey/internal/truenasshell"
	"github.com/shareed2k/honey/internal/ui"
)

func handleWebTrueNASShellTTY(ctx context.Context, conn *websocket.Conn, record hosts.Record, cols, rows int, recorder *ui.SessionRecorder) {
	b, ok := truenasprovider.BackendByName(record.Meta["backend_name"])
	if !ok {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"truenas backend not configured"}`))
		return
	}
	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}

	sess, err := truenasshell.OpenSession(ctx, b, record, rows, cols)
	if err != nil {
		if recorder != nil {
			recorder.RecordError(err)
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}

	var sink truenasshell.Recorder
	if recorder != nil {
		sink = recorder
	}
	truenasshell.BridgeWebSocket(ctx, conn, sess, sink)
}
