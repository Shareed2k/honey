package cuetry

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RecipeStepTunnel configures an operator-side port forward for a recipe step.
type RecipeStepTunnel struct {
	Mode           string            `json:"mode,omitempty" jsonschema:"default=local"`
	RemoteHost     string            `json:"remote_host,omitempty"`
	RemotePort     int               `json:"remote_port,omitempty"`
	LocalPort      int               `json:"local_port,omitempty"`
	Bind           string            `json:"bind,omitempty"`
	RemoteBind     string            `json:"remote_bind,omitempty"`
	RemoteListen   int               `json:"remote_listen_port,omitempty"`
	LocalHost      string            `json:"local_host,omitempty"`
	LocalTarget    int               `json:"local_target_port,omitempty"`
	UseSSHConfig   bool              `json:"use_ssh_config,omitempty"`
	SSHConfigMatch string            `json:"ssh_config_match,omitempty"`
	SSHConfigEnv   map[string]string `json:"ssh_config_env,omitempty"`
	ShareKey       string            `json:"share_key,omitempty"`
	Protocol       string            `json:"protocol,omitempty" jsonschema:"default=tcp"`
	TunLocal       int               `json:"tun_local,omitempty"`
	TunRemote      int               `json:"tun_remote,omitempty"`
	RemoteSocat    bool              `json:"remote_socat,omitempty"`
	RemoteSocket   string            `json:"remote_socket,omitempty"`
	LocalSocket    string            `json:"local_socket,omitempty"`
}

// EffectiveTunnelMode returns normalized tunnel mode (local, remote, dynamic, udp, tun).
func EffectiveTunnelMode(t *RecipeStepTunnel) string {
	if t == nil {
		return "local"
	}
	m := strings.ToLower(strings.TrimSpace(t.Mode))
	if m == "" {
		if strings.EqualFold(strings.TrimSpace(t.Protocol), "udp") {
			return "udp"
		}
		return "local"
	}
	if m == "udp" {
		return "udp"
	}
	return m
}

func stepIDsReferencedByTunnelStep(steps []StepWrapper) map[string]struct{} {
	out := make(map[string]struct{})
	for _, w := range steps {
		ps, ok := w.Step.(*PluginStep)
		if !ok || ps.Plugin == nil || len(ps.Plugin.Config) == 0 {
			continue
		}
		var cfg struct {
			TunnelStep string `json:"tunnel_step"`
		}
		if err := json.Unmarshal(ps.Plugin.Config, &cfg); err != nil {
			continue
		}
		if id := strings.TrimSpace(cfg.TunnelStep); id != "" {
			out[id] = struct{}{}
		}
	}
	return out
}

func validateLoopbackBind(field, bind string) error {
	bind = strings.TrimSpace(bind)
	if bind == "" {
		return nil
	}
	if bind != "127.0.0.1" && bind != "::1" && bind != "localhost" {
		return fmt.Errorf("cuetry: %s must be loopback (127.0.0.1, ::1, or localhost)", field)
	}
	return nil
}

// StepIDsReferencedByTunnelStep returns step ids referenced by plugin tunnel_step config.
func StepIDsReferencedByTunnelStep(r Recipe) map[string]struct{} {
	return stepIDsReferencedByTunnelStep(r.Steps)
}

func validateRecipeTunnelRefs(steps []StepWrapper) error {
	refs := stepIDsReferencedByTunnelStep(steps)
	if len(refs) == 0 {
		return nil
	}
	idKind := make(map[string]string, len(steps))
	for _, w := range steps {
		id := strings.TrimSpace(w.Step.Base().ID)
		if id == "" {
			continue
		}
		idKind[id] = w.Step.Kind()
	}
	for refID := range refs {
		k, ok := idKind[refID]
		if !ok {
			return fmt.Errorf("cuetry: tunnel_step references unknown step id %q", refID)
		}
		if k != KindTunnel {
			return fmt.Errorf("cuetry: tunnel_step %q is not a tunnel step", refID)
		}
	}
	return nil
}
