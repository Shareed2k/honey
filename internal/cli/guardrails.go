package cli

import (
	"fmt"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/guardrails"
)

// buildGuardrailRuleset compiles the config-level guardrail rules into an
// immutable *guardrails.Ruleset, used as a deterministic floor by the shared
// command gate (recipe engine, MCP, SSH gateway). A nil config or an empty
// guardrails list yields a nil ruleset — a no-op floor, byte-for-byte the
// pre-guardrail behavior.
//
// A compile error fails command startup (fail-closed): a malformed operator
// ruleset must never silently degrade to "no guardrails".
func buildGuardrailRuleset(cfg *config.File) (*guardrails.Ruleset, error) {
	if cfg == nil || len(cfg.Guardrails) == 0 {
		return nil, nil
	}
	rules := make([]guardrails.Rule, 0, len(cfg.Guardrails))
	for _, r := range cfg.Guardrails {
		rules = append(rules, guardrails.Rule{
			Name:        r.Name,
			Description: r.Description,
			Action:      guardrails.Action(r.Action),
			AppliesTo:   guardrails.Kind(r.AppliesTo),
			Words:       r.Words,
			Patterns:    r.Patterns,
			Absent:      r.Absent,
			Message:     r.Message,
			Targets:     r.Targets,
		})
	}
	rs, err := guardrails.NewRuleset(rules)
	if err != nil {
		return nil, fmt.Errorf("build guardrail ruleset: %w", err)
	}
	return rs, nil
}

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
