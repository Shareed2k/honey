package webserver

import (
	"net"
	"net/http"

	"github.com/shareed2k/honey/internal/hosts"
)

// ForwardingAPI owns the WebSocket forwarding/relay endpoints (ws/tunnel,
// ws/remote-forward, ws/udp), isolating them from the main Server so the feature
// carries its own deps (mirrors FilesAPI/ProxyAPI/TunnelsAPI, arch-08). authorized
// and sshUser are injected (shared Server-wide). remoteListenerFor and udpDialer
// are the seams tests inject to avoid real SSH/UDP; they move here from Server so
// a test still injects them post-construction (on s.forwardingAPI) before a request.
type ForwardingAPI struct {
	opts       Options
	authorized func(*http.Request) bool
	sshUser    func(string) string
	// remoteListenerFor obtains the reverse listener on the target side; nil
	// selects defaultRemoteListener (the leaf.Listen path). Tests inject an
	// in-memory listener to avoid real SSH.
	remoteListenerFor func(user string, r hosts.Record, bind string, port int) (net.Listener, func(), error)
	// udpDialer obtains the UDP target connection; defaults to realUDPDialer{}.
	// Tests inject a fake to avoid opening real UDP sockets.
	udpDialer udpDialer
}

// NewForwardingAPI wires the shared auth + ssh-user resolvers and the production
// UDP dialer. remoteListenerFor stays nil (handlers fall back to
// defaultRemoteListener); tests overwrite either field on the returned value.
func NewForwardingAPI(opts Options, authorized func(*http.Request) bool, sshUser func(string) string) *ForwardingAPI {
	return &ForwardingAPI{opts: opts, authorized: authorized, sshUser: sshUser, udpDialer: realUDPDialer{}}
}
