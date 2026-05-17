package cuetry

import "strings"

func recipeSSHPrivateKeyField(path string) (string, bool) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", false
	}
	return path, true
}

// EffectiveSSHPrivateKey returns the private key path for SSH using recipe precedence:
// step.ssh_private_key, then defaults.ssh_private_key, else "" (use ssh_config / env / ~/.ssh).
func EffectiveSSHPrivateKey(defaults *RecipeDefaults, step RecipeStep) string {
	if p, ok := recipeSSHPrivateKeyField(step.SSHPrivateKey); ok {
		return p
	}
	if defaults != nil {
		if p, ok := recipeSSHPrivateKeyField(defaults.SSHPrivateKey); ok {
			return p
		}
	}
	return ""
}
