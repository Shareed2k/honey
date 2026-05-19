package truenasprovider

import (
	"fmt"
	"net/url"
	"strings"
)

const apiCurrentPath = "/api/current"

// normalizeWSURL converts a user URL (https://host, wss://host/api/current) into a WebSocket dial URL.
func normalizeWSURL(raw string, insecure bool) (wsURL string, host string, err error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", fmt.Errorf("truenas url is empty")
	}
	if !strings.Contains(raw, "://") {
		if insecure {
			raw = "http://" + raw
		} else {
			raw = "https://" + raw
		}
	}
	u, err := url.Parse(raw)
	if err != nil {
		return "", "", fmt.Errorf("truenas url: %w", err)
	}
	if u.Host == "" {
		return "", "", fmt.Errorf("truenas url: missing host")
	}

	scheme := strings.ToLower(u.Scheme)
	switch scheme {
	case "https", "wss":
		u.Scheme = "wss"
	case "http", "ws":
		u.Scheme = "ws"
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
