package ui

import (
	"strings"

	"github.com/shareed2k/honey/internal/config"
)

// LoadAISystemPromptFromConfigPath returns defaults.ai_system_prompt from the honey YAML at path, if loadable.
func LoadAISystemPromptFromConfigPath(configPath string) string {
	p := strings.TrimSpace(configPath)
	if p == "" {
		return ""
	}
	f, err := config.Load(p)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(f.Defaults.AISystemPrompt)
}

// LoadTransferConfigFromConfigPath returns the effective transfer config from the
// honey YAML at path. If path is empty or the file fails to load, returns defaults.

// transferConfigFromSessionHoney returns effective transfer config from loaded file or path.

// WriteCueSSHPrivateKeyDryLine prints one plan line when ssh_private_key is set for the step or defaults.
