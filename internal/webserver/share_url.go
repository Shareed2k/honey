package webserver

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// unreachableHosts are listenAddr hosts that describe "any interface" or
// "this machine only" rather than an address a recipient on another device
// could actually dial. Any of these must be replaced by the resolved LAN IP
// before a share link is handed out.
var unreachableHosts = map[string]bool{
	"":          true, // e.g. listenAddr ":8765"
	"0.0.0.0":   true,
	"::":        true,
	"::1":       true,
	"[::1]":     true,
	"localhost": true,
	"127.0.0.1": true,
}

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
// derived from listenAddr — a concrete non-loopback host is used as-is,
// while an empty / 0.0.0.0 / :: / localhost / 127.0.0.1 host is replaced by
// resolveLAN's primary outbound LAN IP. The main honey web listener is plain
// HTTP, so the derived scheme is always "http"; publicURL is how an operator
// behind a TLS reverse proxy supplies "https://...".
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

	// unreachableHosts is a fast-path literal-string set, so a listen host that
	// is merely SOME loopback address without being exactly one of those
	// literals (e.g. 127.0.0.2, or an IPv6-mapped/expanded form) would
	// otherwise fall through as "concrete reachable" and ship verbatim in a
	// share link — unreachable from any other device. A real net.ParseIP +
	// IsLoopback check catches every such form the literal set misses.
	needsLAN := unreachableHosts[strings.ToLower(host)]
	if !needsLAN {
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			needsLAN = true
		}
	}
	if needsLAN {
		lan, lerr := resolveLAN()
		if lerr != nil {
			return "", fmt.Errorf("shareBaseURL: no reachable host for listen address %q: %w", listenAddr, lerr)
		}
		host = lan
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
