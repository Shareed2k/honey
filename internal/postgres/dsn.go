package postgres

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// RewriteDSNHostPort replaces hostname and/or port in a postgres connection URL or DSN.
func RewriteDSNHostPort(dsn, hostOverride, portOverride string) (string, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return "", fmt.Errorf("postgres: empty dsn")
	}
	hostOverride = strings.TrimSpace(hostOverride)
	portOverride = strings.TrimSpace(portOverride)
	if hostOverride == "" && portOverride == "" {
		return dsn, nil
	}
	u, err := url.Parse(dsn)
	if err != nil {
		return "", fmt.Errorf("postgres: parse dsn: %w", err)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		host = u.Host
		port = ""
	}
	if hostOverride != "" {
		host = hostOverride
	}
	if portOverride != "" {
		p, perr := strconv.Atoi(portOverride)
		if perr != nil || p <= 0 || p >= 65536 {
			return "", fmt.Errorf("postgres: invalid port override %q", portOverride)
		}
		port = portOverride
	}
	if port == "" {
		port = "5432"
	}
	u.Host = net.JoinHostPort(host, port)
	return u.String(), nil
}
