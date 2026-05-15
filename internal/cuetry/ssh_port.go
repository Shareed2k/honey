package cuetry

import "github.com/shareed2k/honey/internal/hosts"

func recipeSSHPortField(p int) (int, bool) {
	if p <= 0 || p >= 65536 {
		return 0, false
	}
	return p, true
}

// EffectiveSSHPort returns the TCP port for SSH to r using recipe precedence:
// step.ssh_port, then defaults.ssh_port, then record meta.ssh_port, else 0 (use ~/.ssh/config / 22 only).
func EffectiveSSHPort(defaults *RecipeDefaults, step RecipeStep, r hosts.Record) int {
	if p, ok := recipeSSHPortField(step.SSHPort); ok {
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

// RecordForSSHDial returns r unchanged or a shallow copy with meta["ssh_port"] set
// so hostexec and SSHClientCacheKey see the effective port after recipe precedence.
func RecordForSSHDial(defaults *RecipeDefaults, step RecipeStep, r hosts.Record) hosts.Record {
	eff := EffectiveSSHPort(defaults, step, r)
	if eff <= 0 {
		return r
	}
	if p, ok := hosts.MetaSSHPort(&r); ok && p == eff {
		return r
	}
	return hosts.CloneWithMetaSSHPort(r, eff)
}
