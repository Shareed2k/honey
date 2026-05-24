package cuetry

import (
	"encoding/json"
	"fmt"
	"strings"
)

// RecipeStepTunnel configures an operator-side port forward for a recipe step.
type RecipeStepTunnel struct {
	Mode           string            `json:"mode,omitempty"`
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
	Protocol       string            `json:"protocol,omitempty"`
	TunLocal       int               `json:"tun_local,omitempty"`
	TunRemote      int               `json:"tun_remote,omitempty"`
	RemoteSocat    bool              `json:"remote_socat,omitempty"`
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

func validateStepTunnel(i int, kind StepKind, s RecipeStep, mode ExecutionMode) error {
	if kind != StepKindTunnel {
		return nil
	}
	if s.Tunnel == nil {
		return fmt.Errorf("cuetry: steps[%d]: internal tunnel step", i)
	}
	t := s.Tunnel
	tm := EffectiveTunnelMode(t)
	if err := validateLoopbackBind(fmt.Sprintf("steps[%d].tunnel.bind", i), t.Bind); err != nil {
		return err
	}
	if err := validateLoopbackBind(fmt.Sprintf("steps[%d].tunnel.remote_bind", i), t.RemoteBind); err != nil {
		return err
	}
	if sk := strings.TrimSpace(t.ShareKey); sk != "" {
		if len(sk) > 128 {
			return fmt.Errorf("cuetry: steps[%d].tunnel.share_key exceeds 128 chars", i)
		}
	}
	switch tm {
	case "local", "udp":
		if !t.UseSSHConfig && t.RemotePort <= 0 {
			return fmt.Errorf("cuetry: steps[%d].tunnel.remote_port is required unless use_ssh_config is true", i)
		}
		if tm == "udp" && !t.RemoteSocat {
			return fmt.Errorf("cuetry: steps[%d].tunnel.remote_socat must be true for udp mode", i)
		}
	case "remote":
		if !t.UseSSHConfig && (t.RemoteListen <= 0 || t.LocalTarget <= 0) {
			return fmt.Errorf("cuetry: steps[%d].tunnel requires remote_listen_port and local_target_port for remote mode", i)
		}
	case "dynamic":
	case "tun":
		if t.TunLocal < 0 || t.TunRemote < 0 {
			return fmt.Errorf("cuetry: steps[%d].tunnel tun ids must be non-negative", i)
		}
	default:
		return fmt.Errorf("cuetry: steps[%d].tunnel.mode %q is invalid", i, tm)
	}
	if t.LocalPort < 0 || t.LocalPort >= 65536 {
		return fmt.Errorf("cuetry: steps[%d].tunnel.local_port out of range", i)
	}
	if mode == ExecutionModeGraph {
		id := strings.TrimSpace(s.ID)
		if id == "" && (len(s.Depends) > 0 || len(s.EnvFrom) > 0) {
			return fmt.Errorf("cuetry: steps[%d]: tunnel step with depends or env_from requires id", i)
		}
	}
	return nil
}

func stepIDsReferencedByTunnelStep(steps []RecipeStep) map[string]struct{} {
	out := make(map[string]struct{})
	for _, s := range steps {
		if s.Plugin == nil || len(s.Plugin.Config) == 0 {
			continue
		}
		var cfg struct {
			TunnelStep string `json:"tunnel_step"`
		}
		if err := json.Unmarshal(s.Plugin.Config, &cfg); err != nil {
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

func validateRecipeTunnelRefs(steps []RecipeStep) error {
	refs := stepIDsReferencedByTunnelStep(steps)
	if len(refs) == 0 {
		return nil
	}
	idKind := make(map[string]StepKind, len(steps))
	for _, s := range steps {
		id := strings.TrimSpace(s.ID)
		if id == "" {
			continue
		}
		k, err := ClassifyStep(s)
		if err != nil {
			continue
		}
		idKind[id] = k
	}
	for refID := range refs {
		k, ok := idKind[refID]
		if !ok {
			return fmt.Errorf("cuetry: tunnel_step references unknown step id %q", refID)
		}
		if k != StepKindTunnel {
			return fmt.Errorf("cuetry: tunnel_step %q is not a tunnel step", refID)
		}
	}
	return nil
}
