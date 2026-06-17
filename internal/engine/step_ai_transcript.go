package engine

import (
	"fmt"
	"strings"
)

// BuildCueRecipeTranscript formats prior CUE step HostExecResult groups for an AI summarizer.
// BuildCueRecipeTranscript ...
func BuildCueRecipeTranscript(history [][]HostExecResult) string {
	var b strings.Builder
	for si, group := range history {
		if len(group) == 0 {
			continue
		}
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		_, _ = fmt.Fprintf(&b, "=== Prior step %d (%d host result(s)) ===\n", si+1, len(group))
		for _, r := range group {
			_, _ = fmt.Fprintf(&b, "--- %s @ %s (%s) success=%v exit=%d err=%q ---\n",
				r.Name, r.IP, r.Provider, r.Success, r.ExitCode, r.ErrMsg)
			if strings.TrimSpace(r.Output) != "" {
				b.WriteString(strings.TrimSpace(r.Output))
				b.WriteByte('\n')
			}
			if strings.TrimSpace(r.HookOutput) != "" {
				_, _ = fmt.Fprintf(&b, "hook (%s):\n%s\n", strings.TrimSpace(r.HookPhase), strings.TrimSpace(r.HookOutput))
			}
		}
	}
	return strings.TrimSpace(b.String())
}

// FormatCueStepHostResultsForNotify formats one step’s host results for notify bodies (non-AI steps).
// FormatCueStepHostResultsForNotify ...
func FormatCueStepHostResultsForNotify(stepNo int, group []HostExecResult) string {
	if len(group) == 0 {
		return "(no host results)"
	}
	var b strings.Builder
	_, _ = fmt.Fprintf(&b, "=== Step %d (%d host result(s)) ===\n", stepNo, len(group))
	for _, r := range group {
		_, _ = fmt.Fprintf(&b, "--- %s @ %s (%s) success=%v exit=%d err=%q ---\n",
			r.Name, r.IP, r.Provider, r.Success, r.ExitCode, r.ErrMsg)
		if strings.TrimSpace(r.Output) != "" {
			b.WriteString(strings.TrimSpace(r.Output))
			b.WriteByte('\n')
		}
		if strings.TrimSpace(r.HookOutput) != "" {
			_, _ = fmt.Fprintf(&b, "hook (%s):\n%s\n", strings.TrimSpace(r.HookPhase), strings.TrimSpace(r.HookOutput))
		}
	}
	return strings.TrimSpace(b.String())
}

// TruncateCueTranscript limits transcript size for LLM input; keeps head and tail with a banner if truncated.
// TruncateCueTranscript ...
func TruncateCueTranscript(s string, maxChars int) string {
	if maxChars <= 0 || len(s) <= maxChars {
		return s
	}
	const banner = "\n…(transcript truncated by honey max_input_chars)…\n"
	keep := maxChars - len(banner)
	if keep < 64 {
		return s[:maxChars]
	}
	half := keep / 2
	return s[:half] + banner + s[len(s)-(keep-half):]
}
