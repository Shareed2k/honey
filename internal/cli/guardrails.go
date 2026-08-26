package cli

import (
	"github.com/shareed2k/honey/internal/config"
)

// webGuardMode reads web.guard_mode (config.WebConfig.GuardMode), the
// operator-terminal counterpart to ssh_gateway.guardrail.mode. Empty/unset
// resolves to "off" downstream (termguard.ParseMode's fail-safe default).
func webGuardMode(cfg *config.File) string {
	if cfg == nil || cfg.Web == nil {
		return ""
	}
	return cfg.Web.GuardMode
}

// configWebPublicURL reads web.public_url (config.WebConfig.PublicURL); the
// --public-url flag wins over it (see runWeb).
func configWebPublicURL(cfg *config.File) string {
	if cfg == nil || cfg.Web == nil {
		return ""
	}
	return cfg.Web.PublicURL
}
