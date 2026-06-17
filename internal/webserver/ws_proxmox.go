package webserver

import (
	"context"
	"strconv"
	"strings"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/proxmoxprovider"
	"github.com/shareed2k/honey/internal/pvelxc"
)

func isProxmoxSerialWebPVE(rec hosts.Record) bool {
	return pvelxc.ShouldUsePVETTY(rec)
}

// handleWebProxmoxPVESerialTTY bridges the browser WebSocket to Proxmox LXC or QEMU serial vncwebsocket (shared with TUI via pvelxc).
func handleWebProxmoxPVESerialTTY(ctx context.Context, conn *websocket.Conn, record hosts.Record, cols, rows int, recorder *engine.SessionRecorder) {
	b, ok := proxmoxprovider.BackendByName(record.Meta["backend_name"])
	if !ok {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"proxmox backend not configured"}`))
		return
	}
	node := strings.TrimSpace(record.Meta["node"])
	vmid, err := strconv.Atoi(strings.TrimSpace(record.Meta["vmid"]))
	if err != nil || vmid <= 0 || node == "" {
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"proxmox record missing node or vmid"}`))
		return
	}
	guest := strings.TrimSpace(record.Meta["kind"])
	if guest == "" {
		guest = "lxc"
	}

	if cols <= 0 {
		cols = 120
	}
	if rows <= 0 {
		rows = 32
	}

	sess, err := pvelxc.OpenSession(ctx, b, guest, node, vmid, rows, cols)
	if err != nil {
		if recorder != nil {
			recorder.RecordError(err)
		}
		_ = conn.WriteMessage(websocket.TextMessage, []byte(`{"error":"`+escapeJSON(err.Error())+`"}`))
		return
	}
	defer func() { _ = sess.Close() }()

	var sink pvelxc.Recorder
	if recorder != nil {
		sink = recorder
	}
	pvelxc.BridgeWebSocket(ctx, conn, sess, sink)
}
