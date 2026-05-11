package cuetry

import "strings"

// ResolveRecipeAISystemPrompt returns the system message for a recipe ai step.
// Precedence: non-empty ai.system_prompt in CUE, then config defaults.ai_system_prompt, then built-in default.
func ResolveRecipeAISystemPrompt(ai *RecipeAI, configDefault string) string {
	if ai != nil && strings.TrimSpace(ai.SystemPrompt) != "" {
		return strings.TrimSpace(ai.SystemPrompt)
	}
	if strings.TrimSpace(configDefault) != "" {
		return strings.TrimSpace(configDefault)
	}
	return DefaultRecipeAISystemPrompt
}
