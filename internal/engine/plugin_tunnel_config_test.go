package engine

import (
	"encoding/json"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

// TestRewritePluginConfigTunnelStep_setsBaseURL ...
func TestRewritePluginConfigTunnelStep_setsBaseURL(t *testing.T) {
	t.Parallel()
	coord := NewRecipeTunnelCoordinator(nil)
	rec := hosts.Record{Name: "app1", PrimaryIP: "10.0.0.1", Provider: "gcp"}
	coord.Register("rcd_tunnel", "ubuntu", rec, TunnelEndpoint{Host: "127.0.0.1", Port: 5572}, nil)

	cfg, _ := json.Marshal(map[string]string{"tunnel_step": "rcd_tunnel"})
	out, err := RewritePluginConfigTunnelStep(cfg, "rclone", coord, "ubuntu", rec, true)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]string
	if err := json.Unmarshal(out, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["base_url"] != "http://127.0.0.1:5572" {
		t.Fatalf("base_url=%q", parsed["base_url"])
	}
}

// TestRewritePluginConfigTunnelStep_rcloneRequiresTunnel ...
func TestRewritePluginConfigTunnelStep_rcloneRequiresTunnel(t *testing.T) {
	t.Parallel()
	cfg, _ := json.Marshal(map[string]string{"rc_user": "honey"})
	_, err := RewritePluginConfigTunnelStep(cfg, "rclone", nil, "ubuntu", hosts.Record{Name: "app1"}, true)
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestRewritePluginConfigTunnelStep_miss ...
func TestRewritePluginConfigTunnelStep_miss(t *testing.T) {
	t.Parallel()
	coord := NewRecipeTunnelCoordinator(nil)
	cfg, _ := json.Marshal(map[string]string{"tunnel_step": "missing"})
	_, err := RewritePluginConfigTunnelStep(cfg, "rclone", coord, "ubuntu", hosts.Record{Name: "app1"}, true)
	if err == nil {
		t.Fatal("expected error")
	}
}
