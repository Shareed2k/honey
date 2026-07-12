package honeyprovider

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/meshnet"
)

// trustConfig captures one backend's transport-construction inputs: TLS
// trust settings (unchanged from before this task) plus, new, the mesh
// dial target.
type trustConfig struct {
	insecure bool
	mtls     bool
	serverCA string
	mesh     bool   // true when this backend should dial via the libp2p mesh instead of the normal network path
	meshAddr string // the libp2p multiaddr to dial when mesh is true (HoneyBackend.MeshAddr, threaded through unchanged)
}

// buildTransport returns an *http.Transport configured per cfg: TLS via the
// existing clientTLSConfig helper (unchanged, still in exec.go — do not move
// or duplicate it), DialContext routed through the mesh via meshDialContext
// only when cfg.mesh is true.
func buildTransport(cfg trustConfig) (*http.Transport, error) {
	tlsCfg, err := clientTLSConfig(cfg.insecure, cfg.mtls, cfg.serverCA)
	if err != nil {
		return nil, err
	}
	tr := &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: tlsCfg}
	if dialFn := meshDialContext(cfg.mesh, cfg.meshAddr); dialFn != nil {
		tr.DialContext = dialFn
	}
	return tr, nil
}

// buildWSDialer mirrors buildTransport for the two dialWS call sites.
func buildWSDialer(cfg trustConfig) (*websocket.Dialer, error) {
	tlsCfg, err := clientTLSConfig(cfg.insecure, cfg.mtls, cfg.serverCA)
	if err != nil {
		return nil, err
	}
	d := &websocket.Dialer{Proxy: http.ProxyFromEnvironment, HandshakeTimeout: 15 * time.Second, TLSClientConfig: tlsCfg}
	if dialFn := meshDialContext(cfg.mesh, cfg.meshAddr); dialFn != nil {
		d.NetDialContext = dialFn
	}
	return d, nil
}

// meshDial is swapped in white-box tests (this package's *_test.go files
// are all `package honeyprovider`) so tests never touch a real mesh/relay.
var meshDial = meshnet.DialPeer

// meshDialContext returns a dialer matching http.Transport.DialContext /
// websocket.Dialer.NetDialContext's signature when useMesh is true, or nil
// when false — callers must only assign the result when non-nil, so a
// non-mesh backend's existing default dialer (e.g. the one already present
// on a cloned http.DefaultTransport in honey.go) is left untouched. The
// returned closure ignores the network/address arguments http.Transport
// would normally derive from the backend's URL (meaningless for a mesh
// target — see HoneyBackend.MeshAddr's doc comment) and dials meshAddr
// instead.
func meshDialContext(useMesh bool, meshAddr string) func(ctx context.Context, network, address string) (net.Conn, error) {
	if !useMesh {
		return nil
	}
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return meshDial(ctx, meshAddr)
	}
}
