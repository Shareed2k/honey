package truenasshell

import (
	"fmt"
	"net/url"
	"strings"

	"github.com/shareed2k/honey/internal/provider/truenasprovider"
)

const shellPath = "/websocket/shell"

// APIWSURL normalizes a TrueNAS backend URL to wss://host/api/current.
func APIWSURL(raw string, insecure bool) (string, error) {
	wsURL, _, err := truenasprovider.NormalizeWSURL(raw, insecure)
	return wsURL, err
}

// ShellWSURL returns the wss URL for /websocket/shell on the same host as apiWSURL.
func ShellWSURL(apiWSURL string) (string, error) {
	apiWSURL = strings.TrimSpace(apiWSURL)
	if apiWSURL == "" {
		return "", fmt.Errorf("truenas api url is empty")
	}
	u, err := url.Parse(apiWSURL)
	if err != nil {
		return "", fmt.Errorf("truenas api url: %w", err)
	}
	if u.Host == "" {
		return "", fmt.Errorf("truenas api url: missing host")
	}
	if strings.ToLower(u.Scheme) != "wss" && strings.ToLower(u.Scheme) != "ws" {
		return "", fmt.Errorf("truenas api url: want wss scheme, got %q", u.Scheme)
	}
	u.Scheme = "wss"
	u.Path = shellPath
	u.Fragment = ""
	u.RawQuery = ""
	return u.String(), nil
}
