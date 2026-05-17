package plugins

import (
	"fmt"
	"net"
	"path/filepath"
	"strings"

	"github.com/shareed2k/honey/internal/config"
)

const maxAllowedHosts = 64

// effectiveNetworkPolicy resolves allowed_hosts after honey config policy.
func effectiveNetworkPolicy(m Manifest, cfg config.PluginsEffective) ([]string, error) {
	if cfg.NetworkDeny {
		if len(m.AllowedHosts) > 0 {
			return nil, fmt.Errorf("plugins: plugin %q declares allowed_hosts but plugins.network_deny is true", m.ID)
		}
		return nil, nil
	}
	hosts, err := validateAllowedHosts(m.AllowedHosts)
	if err != nil {
		return nil, fmt.Errorf("plugins: plugin %q: %w", m.ID, err)
	}
	if len(cfg.NetworkAllowHosts) == 0 {
		return hosts, nil
	}
	global, err := validateAllowedHosts(cfg.NetworkAllowHosts)
	if err != nil {
		return nil, fmt.Errorf("plugins: plugins.network_allow_hosts: %w", err)
	}
	allow := make(map[string]struct{}, len(global))
	for _, h := range global {
		allow[h] = struct{}{}
	}
	var out []string
	for _, h := range hosts {
		if _, ok := allow[h]; !ok {
			return nil, fmt.Errorf("plugins: plugin %q allowed_hosts %q not permitted by honey config plugins.network_allow_hosts", m.ID, h)
		}
		out = append(out, h)
	}
	return out, nil
}

func validateAllowedHosts(hosts []string) ([]string, error) {
	if len(hosts) == 0 {
		return nil, nil
	}
	if len(hosts) > maxAllowedHosts {
		return nil, fmt.Errorf("too many allowed_hosts (max %d)", maxAllowedHosts)
	}
	seen := make(map[string]struct{}, len(hosts))
	var out []string
	for _, raw := range hosts {
		h, err := normalizeAllowedHost(raw)
		if err != nil {
			return nil, err
		}
		if _, dup := seen[h]; dup {
			continue
		}
		seen[h] = struct{}{}
		out = append(out, h)
	}
	return out, nil
}

func normalizeAllowedHost(raw string) (string, error) {
	h := strings.TrimSpace(strings.ToLower(raw))
	if h == "" {
		return "", fmt.Errorf("empty allowed_hosts entry")
	}
	if strings.Contains(h, "*") || strings.Contains(h, "?") {
		return "", fmt.Errorf("wildcards are not allowed in allowed_hosts: %q", raw)
	}
	// Strip optional scheme for validation.
	h = strings.TrimPrefix(h, "https://")
	h = strings.TrimPrefix(h, "http://")
	if i := strings.IndexByte(h, '/'); i >= 0 {
		h = h[:i]
	}
	host, port, err := net.SplitHostPort(h)
	if err != nil {
		host = h
		port = ""
	}
	if host == "" {
		return "", fmt.Errorf("invalid allowed_hosts entry: %q", raw)
	}
	if !strings.EqualFold(host, "localhost") {
		if ip := net.ParseIP(host); ip == nil && !isDNSHostname(host) {
			return "", fmt.Errorf("invalid allowed_hosts hostname: %q", raw)
		}
	}
	if port != "" {
		return net.JoinHostPort(host, port), nil
	}
	return host, nil
}

func isDNSHostname(host string) bool {
	if len(host) == 0 || len(host) > 253 {
		return false
	}
	for _, label := range strings.Split(host, ".") {
		if len(label) == 0 || len(label) > 63 {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-' {
				continue
			}
			return false
		}
		if label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
	}
	return true
}

func validateAllowedPaths(paths map[string]string) (map[string]string, error) {
	if len(paths) == 0 {
		return nil, nil
	}
	if len(paths) > 16 {
		return nil, fmt.Errorf("too many allowed_paths entries (max 16)")
	}
	out := make(map[string]string, len(paths))
	for guest, host := range paths {
		guest = strings.TrimSpace(guest)
		host = strings.TrimSpace(host)
		if guest == "" || host == "" {
			return nil, fmt.Errorf("allowed_paths: empty guest or host path")
		}
		if !filepath.IsAbs(guest) || !filepath.IsAbs(host) {
			return nil, fmt.Errorf("allowed_paths: guest and host paths must be absolute (%q -> %q)", guest, host)
		}
		guest = filepath.Clean(guest)
		host = filepath.Clean(host)
		if strings.Contains(guest, "..") || strings.Contains(host, "..") {
			return nil, fmt.Errorf("allowed_paths: path must not contain .. (%q -> %q)", guest, host)
		}
		out[guest] = host
	}
	return out, nil
}

func validateManifestPolicy(m Manifest, cfg config.PluginsEffective) (hosts []string, paths map[string]string, err error) {
	hosts, err = effectiveNetworkPolicy(m, cfg)
	if err != nil {
		return nil, nil, err
	}
	paths, err = validateAllowedPaths(m.AllowedPaths)
	if err != nil {
		return nil, nil, fmt.Errorf("plugins: plugin %q: %w", m.ID, err)
	}
	return hosts, paths, nil
}
