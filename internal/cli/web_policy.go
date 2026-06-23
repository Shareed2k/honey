package cli

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/policy"
)

// Environment variables that activate OPA authorization and actor identity.
const (
	policyDirEnv      = "HONEY_POLICY_DIR"      // dir of .rego files; enables the OPA gates
	jwtPublicKeyEnv   = "HONEY_JWT_PUBLIC_KEY"  // base64 Ed25519 public key; enables JWT identity
	trustedProxiesEnv = "HONEY_TRUSTED_PROXIES" // CSV of CIDRs/IPs trusted to assert X-Honey-User
)

// webAuthConfig holds the optional OPA + identity wiring resolved from the
// environment. Each field is independently optional: a zero value disables that
// capability, keeping OPA strictly opt-in.
type webAuthConfig struct {
	enforcer    *policy.Enforcer
	jwtPubKey   ed25519.PublicKey
	trustedNets []*net.IPNet
}

// resolveWebAuthConfig reads HONEY_POLICY_DIR, HONEY_JWT_PUBLIC_KEY, and
// HONEY_TRUSTED_PROXIES. A malformed key or CIDR is a hard error (misconfigured
// security settings must not silently degrade to "off").
func resolveWebAuthConfig(ctx context.Context, file *config.File) (webAuthConfig, error) {
	var cfg webAuthConfig

	if dir := strings.TrimSpace(os.Getenv(policyDirEnv)); dir != "" {
		data, err := inventoryData(file)
		if err != nil {
			return cfg, fmt.Errorf("%s: inventory data: %w", policyDirEnv, err)
		}
		enf, err := policy.New(ctx, dir, data)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", policyDirEnv, err)
		}
		cfg.enforcer = enf
		zap.L().Info("OPA policy enabled", zap.String("dir", dir), zap.Bool("inventory", data != nil))
	}

	if raw := strings.TrimSpace(os.Getenv(jwtPublicKeyEnv)); raw != "" {
		key, err := parseEd25519PublicKey(raw)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", jwtPublicKeyEnv, err)
		}
		cfg.jwtPubKey = key
		zap.L().Info("JWT identity enabled")
	}

	if raw := strings.TrimSpace(os.Getenv(trustedProxiesEnv)); raw != "" {
		nets, err := parseTrustedProxies(raw)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", trustedProxiesEnv, err)
		}
		cfg.trustedNets = nets
		zap.L().Info("trusted-proxy identity header enabled", zap.Int("networks", len(nets)))
	}

	return cfg, nil
}

// inventoryData converts a config's inventory into the OPA data document
// {"inventory": ...} via a JSON round-trip. Returns nil when there is no
// inventory to expose (no config, or empty inventory) so the enforcer skips the
// data store entirely.
func inventoryData(file *config.File) (map[string]any, error) {
	if file == nil {
		return nil, nil
	}
	inv := file.Inventory
	if len(inv.Vars) == 0 && len(inv.Groups) == 0 && len(inv.Hosts) == 0 {
		return nil, nil
	}
	b, err := json.Marshal(inv)
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return map[string]any{"inventory": m}, nil
}

// parseEd25519PublicKey decodes a base64 (standard or raw) Ed25519 public key.
func parseEd25519PublicKey(raw string) (ed25519.PublicKey, error) {
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		if b, err = base64.RawStdEncoding.DecodeString(raw); err != nil {
			return nil, fmt.Errorf("not valid base64: %w", err)
		}
	}
	if len(b) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("key must be %d bytes, got %d", ed25519.PublicKeySize, len(b))
	}
	return ed25519.PublicKey(b), nil
}

// parseTrustedProxies parses a comma-separated list of CIDRs and bare IPs into
// networks. A bare IP becomes a single-host network (/32 or /128).
func parseTrustedProxies(raw string) ([]*net.IPNet, error) {
	parts := strings.Split(raw, ",")
	nets := make([]*net.IPNet, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		if !strings.Contains(p, "/") {
			ip := net.ParseIP(p)
			if ip == nil {
				return nil, fmt.Errorf("invalid IP %q", p)
			}
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			p = fmt.Sprintf("%s/%d", p, bits)
		}
		_, n, err := net.ParseCIDR(p)
		if err != nil {
			return nil, fmt.Errorf("invalid CIDR %q: %w", p, err)
		}
		nets = append(nets, n)
	}
	if len(nets) == 0 {
		return nil, fmt.Errorf("no usable entries")
	}
	return nets, nil
}
