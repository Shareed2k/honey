package webserver

import (
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// wildcardHosts are listenAddr hosts that mean "every interface". The listener
// really is answering on this machine's LAN address, so substituting the
// resolved LAN IP produces a URL another device can dial.
var wildcardHosts = map[string]bool{
	"":        true, // e.g. listenAddr ":8765"
	"0.0.0.0": true,
	"::":      true,
	"[::]":    true,
}

// ErrListenerLoopbackOnly reports that honey web is bound to loopback only, so
// no share link can reach another device: substituting a LAN IP would hand out
// a URL nothing is listening on. Loopback is the default (--listen
// localhost:8765), so this is the common case and the caller must surface it as
// actionable operator guidance rather than fabricate an address.
var ErrListenerLoopbackOnly = errors.New("share: honey web is listening on loopback only — restart it with --listen 0.0.0.0:<port> (or set --public-url) for share links to reach another device")

// defaultLANResolver is the resolveLAN implementation used outside tests. It
// returns this host's primary outbound LAN IP: the local address the OS
// would pick to reach an external network. net.Dial("udp", ...) only
// resolves a route and never sends a packet, and 192.0.2.1 (TEST-NET-1) is
// reserved for documentation and unroutable, so nothing leaves the host. If
// the dial fails (e.g. no default route), it falls back to the first
// non-loopback IPv4 address among the host's own interfaces. It never
// returns a loopback address.
var defaultLANResolver = func() (string, error) {
	if conn, err := net.Dial("udp", "192.0.2.1:80"); err == nil {
		defer func() { _ = conn.Close() }()
		if addr, ok := conn.LocalAddr().(*net.UDPAddr); ok && addr.IP != nil && !addr.IP.IsLoopback() {
			return addr.IP.String(), nil
		}
	}

	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return "", fmt.Errorf("resolve lan ip: %w", err)
	}
	for _, a := range addrs {
		ipNet, ok := a.(*net.IPNet)
		if !ok || ipNet.IP.IsLoopback() {
			continue
		}
		if ip4 := ipNet.IP.To4(); ip4 != nil {
			return ip4.String(), nil
		}
	}
	return "", fmt.Errorf("resolve lan ip: no non-loopback ipv4 interface found")
}

// shareBaseURL returns the absolute origin (scheme://host[:port]) a share
// link must use so a recipient on another device can reach this honey web
// instance. Precedence: an explicit publicURL wins (trailing "/" trimmed,
// "http://" prepended if it carries no scheme); otherwise the base is
// derived from listenAddr: a concrete non-loopback host is used as-is, a
// wildcard bind (empty / 0.0.0.0 / ::) is replaced by resolveLAN's primary
// outbound LAN IP (the listener does answer there), and a LOOPBACK bind
// returns ErrListenerLoopbackOnly — nothing outside this machine can reach
// that listener, so there is no share URL to hand out and the caller must say
// so instead of fabricating one. The main honey web listener is plain HTTP, so
// the derived scheme is always "http"; publicURL is how an operator behind a
// TLS reverse proxy supplies "https://..." (and is also the way to publish a
// loopback-bound instance that sits behind a proxy).
func shareBaseURL(publicURL, listenAddr string, resolveLAN func() (string, error)) (string, error) {
	if pu := strings.TrimRight(strings.TrimSpace(publicURL), "/"); pu != "" {
		if !strings.Contains(pu, "://") {
			pu = "http://" + pu
		}
		return pu, nil
	}

	host, port, err := net.SplitHostPort(listenAddr)
	if err != nil {
		// No colon at all: could be a bare port ("8765") or a bare host
		// ("myhost") with no port. net.SplitHostPort can't tell these apart,
		// so treat an all-digit value as a port and anything else as a host.
		trimmed := strings.TrimSpace(listenAddr)
		if _, perr := strconv.Atoi(trimmed); perr == nil {
			host, port = "", trimmed
		} else {
			host, port = trimmed, ""
		}
	}

	// Wildcard vs loopback are NOT interchangeable, and conflating them is how
	// this function shipped a broken link once already: a wildcard bind really
	// does answer on the LAN address, so substituting it is correct, but a
	// loopback bind answers ONLY on loopback — substituting a LAN IP there
	// yields a URL nothing is listening on. Loopback is the default
	// (--listen localhost:8765), so it must fail loudly, not silently guess.
	lowered := strings.ToLower(strings.Trim(host, "[]"))
	switch {
	case wildcardHosts[strings.ToLower(host)] || wildcardHosts[lowered]:
		lan, lerr := resolveLAN()
		if lerr != nil {
			return "", fmt.Errorf("shareBaseURL: no reachable host for listen address %q: %w", listenAddr, lerr)
		}
		host = lan
	case lowered == "localhost":
		return "", ErrListenerLoopbackOnly
	default:
		// A literal check misses forms like 127.0.0.2 or an expanded IPv6
		// loopback, which would otherwise fall through as "concrete reachable"
		// and ship verbatim.
		if ip := net.ParseIP(lowered); ip != nil && ip.IsLoopback() {
			return "", ErrListenerLoopbackOnly
		}
	}

	if strings.Contains(host, ":") && !strings.HasPrefix(host, "[") {
		host = "[" + host + "]"
	}

	base := "http://" + host
	if port != "" {
		base += ":" + port
	}
	return base, nil
}
