package cuetry

import "github.com/shareed2k/honey/internal/hosts"

func remoteSSHPort(r *RemoteExec) int {
	if r == nil {
		return 0
	}
	return r.SSHPort
}

func recipeSSHPortField(p int) (int, bool) {
	if p <= 0 || p >= 65536 {
		return 0, false
	}
	return p, true
}

// EffectiveSSHPort returns the TCP port for SSH to r using recipe precedence:
// step.ssh_port, then defaults.ssh_port, then record meta.ssh_port, else 0 (use ~/.ssh/config / 22 only).
func EffectiveSSHPort(defaults *RecipeDefaults, step *RemoteExec, r hosts.Record) int {
	if p, ok := recipeSSHPortField(remoteSSHPort(step)); ok {
		return p
	}
	if defaults != nil {
		if p, ok := recipeSSHPortField(defaults.SSHPort); ok {
			return p
		}
	}
	if p, ok := hosts.MetaSSHPort(&r); ok {
		return p
	}
	return 0
}

// RecordForSSHDial returns r unchanged or a shallow copy with recipe SSH dial options
// (meta ssh_port, ssh_identity_file) so hostexec and SSHClientCacheKey see effective settings.
func RecordForSSHDial(defaults *RecipeDefaults, step *RemoteExec, r hosts.Record) hosts.Record {
	out := r
	if eff := EffectiveSSHPort(defaults, step, r); eff > 0 {
		if p, ok := hosts.MetaSSHPort(&out); !ok || p != eff {
			out = hosts.CloneWithMetaSSHPort(out, eff)
		}
	}
	if key := EffectiveSSHPrivateKey(defaults, step); key != "" {
		if cur, ok := hosts.MetaSSHIdentityFile(&out); !ok || cur != key {
			out = hosts.CloneWithMetaSSHIdentityFile(out, key)
		}
	}
	return out
}
