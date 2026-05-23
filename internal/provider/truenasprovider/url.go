package truenasprovider

import (
	"fmt"
	"net/url"
	"strings"
)

const apiCurrentPath = "/api/current"

// NormalizeWSURL converts a user URL (https://host, http://host) into a WSS dial URL for /api/current.
func NormalizeWSURL(raw string, insecure bool) (wsURL string, host string, err error) {
	return normalizeWSURL(raw, insecure)
}

// normalizeWSURL converts a user URL (https://host, http://host) into a WSS dial URL.
// TrueNAS requires TLS for API key auth; insecure only skips certificate verification in the client.
func normalizeWSURL(raw string, insecure bool) (wsURL string, host string, err error) {
	_ = insecure // reserved for TLS verify in NewClient; URL always uses wss for API keys
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("truenas url is empty")
	}
	if !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("truenas url: %w", err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("truenas url: missing host")
	}

	switch strings.ToLower(u.Scheme) {
	case "https", "wss", "http", "ws":
		u.Scheme = "wss"
	default:
		return "", "", fmt.Errorf("truenas url: unsupported scheme %q", u.Scheme)
	}

	path := strings.TrimSuffix(u.Path, "/")
	switch {
	case path == "" || path == "/":
		u.Path = apiCurrentPath
	case strings.HasSuffix(path, apiCurrentPath), path == "/websocket":
		// keep explicit API path
	default:
		u.Path = apiCurrentPath
	}

	u.Fragment = ""
	u.RawQuery = ""
	wsURL = u.String()
	host = u.Hostname()
	if host == "" {
		return "", "", fmt.Errorf("truenas url: missing host")
	}
	return wsURL, host, nil
}
