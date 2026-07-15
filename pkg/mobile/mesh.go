package mobile

import (
	"context"
	"encoding/json"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/meshnet"
)

// startMeshIfConfigured starts the process-wide libp2p mesh singleton
// (internal/meshnet) when cfg enables it. Mirrors internal/cli/root.go's
// function of the same name, which only runs from Cobra's PersistentPreRunE —
// a hook pkg/mobile never goes through (SearchHosts/Exec/StartVPN call
// cli.GetSearchRegistry()/hostapi.SearchHosts directly), so a `mesh: true`
// honey backend configured on a phone would otherwise never get its libp2p
// Host started. Called from LoadConfig/InitDefaultConfig (config.go) so any
// path that loads the on-device config.yaml also brings the mesh singleton
// up.
//
// Failures are swallowed: pkg/mobile has no logger wired, and per
// meshnet.Start's own contract a mesh startup problem must never block or
// fail the caller — the same "log and continue" philosophy as root.go, minus
// the log (see MeshStatus to inspect the outcome after the fact). meshnet.Start
// is idempotent — safe to call repeatedly / from multiple entrypoints; only
// the first call across the process does real work.
func startMeshIfConfigured(cfg *config.File) {
	if cfg == nil || !cfg.Mesh.Enabled {
		return
	}
	_ = meshnet.Start(context.Background(), meshnet.Config{
		Enabled:    cfg.Mesh.Enabled,
		PrivateKey: cfg.Mesh.PrivateKey,
		RelayAddrs: cfg.Mesh.RelayAddrs,
		ListenMesh: cfg.Mesh.ListenMesh,
	})
}

// MeshStatus reports this process's libp2p mesh connectivity as JSON:
// {"peer_id":"...","connected":true,"relays":["..."]}, or {"error":"..."}
// when mesh was never started (disabled, or startMeshIfConfigured hasn't run
// yet). Optional/diagnostic — nothing in pkg/mobile requires a caller to
// check this before using a mesh-routed honey backend.
func MeshStatus() string {
	st, err := meshnet.Status()
	if err != nil {
		b, _ := json.Marshal(map[string]any{"error": err.Error()})
		return string(b)
	}
	b, _ := json.Marshal(map[string]any{
		"peer_id":   st.PeerID,
		"connected": st.Connected,
		"relays":    st.Relays,
	})
	return string(b)
}
