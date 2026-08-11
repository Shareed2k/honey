package cli

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/oidc"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/webauthn"
)

// Environment variables that activate OPA authorization and actor identity.
const (
	jwtPublicKeyEnv   = "HONEY_JWT_PUBLIC_KEY"  // base64 Ed25519 public key; enables JWT identity
	trustedProxiesEnv = "HONEY_TRUSTED_PROXIES" // CSV of CIDRs/IPs trusted to assert X-Honey-User
	webauthnRPIDEnv   = "HONEY_WEBAUTHN_RPID"   // relying-party id; enables passkey biometric step-up
	webauthnOriginEnv = "HONEY_WEBAUTHN_ORIGIN" // relying-party origin (e.g. https://honey.example)
	webauthnSecretEnv = "HONEY_WEBAUTHN_SECRET" // #nosec G101 -- env var name, not a secret; HMAC secret for biometric tokens
)

// webAuthConfig holds the optional OPA + identity wiring resolved from the
// environment. Each field is independently optional: a zero value disables that
// capability, keeping OPA strictly opt-in.
type webAuthConfig struct {
	enforcer    *policy.Enforcer
	jwtPubKey   ed25519.PublicKey
	trustedNets []*net.IPNet
	webauthn    *webauthn.Manager
	// oidcVerifier is built (via OIDC discovery) only when file.OIDC is set. nil
	// keeps SSO login disabled. Discovery is network I/O, so it uses the passed
	// context and is a hard startup error on failure (never a silent disable).
	oidcVerifier *oidc.Verifier
}

// resolveWebAuthConfig reads HONEY_POLICY_DIR, HONEY_JWT_PUBLIC_KEY, and
// HONEY_TRUSTED_PROXIES. A malformed key or CIDR is a hard error (misconfigured
// security settings must not silently degrade to "off").
func resolveWebAuthConfig(ctx context.Context, file *config.File) (webAuthConfig, error) {
	var cfg webAuthConfig

	if dir := config.ResolvePolicyDir(file); dir != "" {
		data, err := inventoryData(file)
		if err != nil {
			return cfg, fmt.Errorf("policy_dir: inventory data: %w", err)
		}
		enf, err := policy.New(ctx, dir, data)
		if err != nil {
			return cfg, fmt.Errorf("policy_dir: %w", err)
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

	if rpID := strings.TrimSpace(os.Getenv(webauthnRPIDEnv)); rpID != "" {
		mgr, err := buildWebAuthn(rpID)
		if err != nil {
			return cfg, fmt.Errorf("%s: %w", webauthnRPIDEnv, err)
		}
		cfg.webauthn = mgr
		zap.L().Info("WebAuthn biometric step-up enabled", zap.String("rpid", rpID))
	}

	if file != nil && file.OIDC != nil {
		// Fail fast on a misconfigured block rather than starting with an
		// enabled-but-permanently-failing verifier (an empty client_id makes
		// every token verification fail closed, which is safe but confusing).
		if strings.TrimSpace(file.OIDC.Issuer) == "" {
			return cfg, fmt.Errorf("oidc: issuer is required when the oidc block is set")
		}
		if strings.TrimSpace(file.OIDC.ClientID) == "" {
			return cfg, fmt.Errorf("oidc: client_id is required when the oidc block is set")
		}
		v, err := oidc.New(ctx, oidc.Config{
			Issuer:        file.OIDC.Issuer,
			ClientID:      file.OIDC.ClientID,
			UsernameClaim: file.OIDC.UsernameClaim,
			GroupsClaim:   file.OIDC.GroupsClaim,
		})
		if err != nil {
			return cfg, fmt.Errorf("oidc: %w", err)
		}
		cfg.oidcVerifier = v
		zap.L().Info("SSO login (OIDC) enabled", zap.String("issuer", file.OIDC.Issuer))
	}

	return cfg, nil
}

// buildWebAuthn constructs the passkey manager from env. The token secret falls
// back to a random per-process value (tokens then don't survive a restart).
func buildWebAuthn(rpID string) (*webauthn.Manager, error) {
	origin := strings.TrimSpace(os.Getenv(webauthnOriginEnv))
	if origin == "" {
		origin = "https://" + rpID
	}
	secret := []byte(strings.TrimSpace(os.Getenv(webauthnSecretEnv)))
	if len(secret) == 0 {
		secret = make([]byte, 32)
		if _, err := rand.Read(secret); err != nil {
			return nil, err
		}
	}
	return webauthn.New(rpID, origin, secret, 5*time.Minute)
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
