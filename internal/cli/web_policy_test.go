package cli

import (
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/config"
)

func TestInventoryData(t *testing.T) {
	if d, err := inventoryData(nil); err != nil || d != nil {
		t.Fatalf("nil config → nil data, got %v err %v", d, err)
	}
	if d, err := inventoryData(&config.File{}); err != nil || d != nil {
		t.Fatalf("empty inventory → nil data, got %v err %v", d, err)
	}

	file := &config.File{Inventory: config.Inventory{
		Vars: map[string]config.InventoryValue{"tier": config.MustInventoryValue("prod")},
	}}
	d, err := inventoryData(file)
	if err != nil {
		t.Fatalf("inventoryData: %v", err)
	}
	inv, ok := d["inventory"].(map[string]any)
	if !ok {
		t.Fatalf("data.inventory not a map: %T", d["inventory"])
	}
	vars, ok := inv["vars"].(map[string]any)
	if !ok || vars["tier"] != "prod" {
		t.Fatalf("inventory.vars.tier = %v, want prod", vars["tier"])
	}
}

func TestResolveWebAuthConfig_AllDisabledByDefault(t *testing.T) {
	t.Setenv("HONEY_POLICY_DIR", "")
	t.Setenv(jwtPublicKeyEnv, "")
	t.Setenv(trustedProxiesEnv, "")

	cfg, err := resolveWebAuthConfig(context.Background(), nil)
	if err != nil {
		t.Fatalf("resolveWebAuthConfig: %v", err)
	}
	if cfg.enforcer != nil || cfg.jwtPubKey != nil || cfg.trustedNets != nil {
		t.Fatalf("expected all-nil config, got %+v", cfg)
	}
}

func TestResolveWebAuthConfig_PolicyDir(t *testing.T) {
	dir := t.TempDir()
	rego := `package honey
import rego.v1
default allow := false
`
	if err := os.WriteFile(filepath.Join(dir, "p.rego"), []byte(rego), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HONEY_POLICY_DIR", dir)
	t.Setenv(jwtPublicKeyEnv, "")
	t.Setenv(trustedProxiesEnv, "")

	cfg, err := resolveWebAuthConfig(context.Background(), nil)
	if err != nil {
		t.Fatalf("resolveWebAuthConfig: %v", err)
	}
	if cfg.enforcer == nil {
		t.Fatal("expected enforcer to be built from HONEY_POLICY_DIR")
	}
	d, err := cfg.enforcer.Evaluate(context.Background(), map[string]any{"actor": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if d.Allow {
		t.Fatal("deny-all policy should not allow")
	}
}

func TestResolveWebAuthConfig_BadPolicyDirErrors(t *testing.T) {
	t.Setenv("HONEY_POLICY_DIR", filepath.Join(t.TempDir(), "does-not-exist"))
	t.Setenv(jwtPublicKeyEnv, "")
	t.Setenv(trustedProxiesEnv, "")

	if _, err := resolveWebAuthConfig(context.Background(), nil); err == nil {
		t.Fatal("expected error for missing policy dir")
	}
}

func TestResolveWebAuthConfig_JWTKey(t *testing.T) {
	pub, _, _ := ed25519.GenerateKey(nil)
	t.Setenv("HONEY_POLICY_DIR", "")
	t.Setenv(jwtPublicKeyEnv, base64.StdEncoding.EncodeToString(pub))
	t.Setenv(trustedProxiesEnv, "")

	cfg, err := resolveWebAuthConfig(context.Background(), nil)
	if err != nil {
		t.Fatalf("resolveWebAuthConfig: %v", err)
	}
	if !cfg.jwtPubKey.Equal(pub) {
		t.Fatal("parsed JWT key does not match")
	}
}

func TestResolveWebAuthConfig_BadJWTKeyErrors(t *testing.T) {
	t.Setenv("HONEY_POLICY_DIR", "")
	t.Setenv(jwtPublicKeyEnv, "not-base64!!!")
	t.Setenv(trustedProxiesEnv, "")

	if _, err := resolveWebAuthConfig(context.Background(), nil); err == nil {
		t.Fatal("expected error for malformed JWT key")
	}

	// Valid base64 but wrong length.
	t.Setenv(jwtPublicKeyEnv, base64.StdEncoding.EncodeToString([]byte("short")))
	if _, err := resolveWebAuthConfig(context.Background(), nil); err == nil {
		t.Fatal("expected error for wrong key length")
	}
}

func TestResolveWebAuthConfig_TrustedProxies(t *testing.T) {
	t.Setenv("HONEY_POLICY_DIR", "")
	t.Setenv(jwtPublicKeyEnv, "")
	t.Setenv(trustedProxiesEnv, "127.0.0.0/8, 10.0.0.5")

	cfg, err := resolveWebAuthConfig(context.Background(), nil)
	if err != nil {
		t.Fatalf("resolveWebAuthConfig: %v", err)
	}
	if len(cfg.trustedNets) != 2 {
		t.Fatalf("nets = %d, want 2", len(cfg.trustedNets))
	}
	if !cfg.trustedNets[0].Contains(net.ParseIP("127.0.0.1")) {
		t.Fatal("127.0.0.0/8 should contain 127.0.0.1")
	}
	if !cfg.trustedNets[1].Contains(net.ParseIP("10.0.0.5")) {
		t.Fatal("bare IP should become single-host net containing itself")
	}
	if cfg.trustedNets[1].Contains(net.ParseIP("10.0.0.6")) {
		t.Fatal("single-host net must not contain a different IP")
	}
}

func TestResolveWebAuthConfig_BadProxyErrors(t *testing.T) {
	t.Setenv("HONEY_POLICY_DIR", "")
	t.Setenv(jwtPublicKeyEnv, "")
	t.Setenv(trustedProxiesEnv, "not-an-ip")

	if _, err := resolveWebAuthConfig(context.Background(), nil); err == nil {
		t.Fatal("expected error for invalid proxy entry")
	}
}

func TestResolveWebAuthConfig_NoOIDCLeavesVerifierNil(t *testing.T) {
	t.Setenv("HONEY_POLICY_DIR", "")
	t.Setenv(jwtPublicKeyEnv, "")
	t.Setenv(trustedProxiesEnv, "")

	// A config with no oidc block must not build a verifier (SSO stays disabled).
	cfg, err := resolveWebAuthConfig(context.Background(), &config.File{Version: 1})
	if err != nil {
		t.Fatalf("resolveWebAuthConfig: %v", err)
	}
	if cfg.oidcVerifier != nil {
		t.Fatal("expected nil oidcVerifier when cfg.OIDC is absent")
	}
}

func TestResolveWebAuthConfig_OIDCUnreachableIssuerFailsClosed(t *testing.T) {
	t.Setenv("HONEY_POLICY_DIR", "")
	t.Setenv(jwtPublicKeyEnv, "")
	t.Setenv(trustedProxiesEnv, "")

	// A configured issuer that fails discovery must be a hard startup error, not
	// a silent disable. 127.0.0.1:1 refuses immediately (no external network).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	file := &config.File{Version: 1, OIDC: &config.OIDCConfig{
		Issuer:   "http://127.0.0.1:1/",
		ClientID: "honey-kube",
	}}
	if _, err := resolveWebAuthConfig(ctx, file); err == nil {
		t.Fatal("expected error when OIDC discovery fails")
	}
}
