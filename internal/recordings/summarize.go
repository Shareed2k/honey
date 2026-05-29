package recordings

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/shareed2k/honey/internal/hosts"
)

const (
	maxSummarizeInputRunes = 48000
	maxSummarizePlanRunes  = 8000
	maxSummarizeHostOut    = 1200
)

// RecipeMetaFromEvents returns recipe-meta payload from events, if present.
func RecipeMetaFromEvents(events []Event) (path string, hostCount int, startedAt string, metaHosts []hosts.Record, ok bool) {
	for _, e := range events {
		if e.Type != "recipe-meta" || len(e.Result) == 0 {
			continue
		}
		var p struct {
			RecipePath string         `json:"recipe_path"`
			HostCount  int            `json:"host_count"`
			StartedAt  string         `json:"started_at"`
			Hosts      []hosts.Record `json:"hosts"`
		}
		if err := json.Unmarshal(e.Result, &p); err != nil {
			return "", 0, "", nil, false
		}
		return p.RecipePath, p.HostCount, p.StartedAt, p.Hosts, true
	}
	return "", 0, "", nil, false
}

// BuildSummarizePrompt builds a bounded user prompt for LLM summarization of a recording.
func BuildSummarizePrompt(events []Event) string {
	var b strings.Builder
	path, hostCount, startedAt, metaHosts, hasMeta := RecipeMetaFromEvents(events)
	if hasMeta {
		_, _ = fmt.Fprintf(&b, "Recipe path: %s\nHosts in run: %d\nStarted: %s\n", path, hostCount, startedAt)
		if len(metaHosts) > 0 {
			_, _ = b.WriteString("Host names:\n")
			for i, h := range metaHosts {
				if i >= 50 {
					_, _ = fmt.Fprintf(&b, "… and %d more hosts\n", len(metaHosts)-50)
					break
				}
				_, _ = fmt.Fprintf(&b, "- %s %s (%s)\n", h.Provider, h.Name, h.PrimaryIP)
			}
		}
		_, _ = b.WriteString("\n")
	}

	planRunes := 0
	for _, e := range events {
		if e.Type == "data" && e.Direction == "plan" && e.DataB64 != "" {
			chunk := DecodeDataB64(e.DataB64)
			r := []rune(chunk)
			if planRunes+len(r) > maxSummarizePlanRunes {
				chunk = string(r[:maxSummarizePlanRunes-planRunes]) + "\n…(plan truncated)"
			}
			_, _ = b.WriteString("--- Dry-run plan excerpt ---\n")
			_, _ = b.WriteString(chunk)
			_, _ = b.WriteString("\n\n")
			break
		}
	}

	_, _ = b.WriteString("--- Per-host results ---\n")
	for _, e := range events {
		if e.Type != "result" || len(e.Result) == 0 {
			continue
		}
		var row struct {
			Name     string `json:"Name"`
			IP       string `json:"IP"`
			Provider string `json:"Provider"`
			Success  bool   `json:"Success"`
			Skipped  bool   `json:"Skipped"`
			ExitCode int    `json:"ExitCode"`
			Output   string `json:"Output"`
			ErrMsg   string `json:"ErrMsg"`
		}
		if err := json.Unmarshal(e.Result, &row); err != nil {
			continue
		}
		status := "ok"
		if row.Skipped {
			status = "skipped"
		} else if !row.Success {
			status = fmt.Sprintf("failed exit=%d", row.ExitCode)
		}
		_, _ = fmt.Fprintf(&b, "\n[%s] %s %s — %s\n", row.Provider, row.Name, row.IP, status)
		if row.ErrMsg != "" {
			_, _ = b.WriteString("error: " + clipRunes(row.ErrMsg, 400) + "\n")
		}
		if row.Output != "" {
			_, _ = b.WriteString(clipRunes(row.Output, maxSummarizeHostOut) + "\n")
		}
	}

	out := b.String()
	return clipRunes(out, maxSummarizeInputRunes)
}

func clipRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= limit {
		return s
	}
	return string(r[:limit]) + "\n…(truncated)"
}

// DecodeDataB64 decodes a base64-encoded recording data payload to a UTF-8 string.
func DecodeDataB64(b64 string) string {
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return ""
	}
	if !utf8.Valid(raw) {
		return strings.ToValidUTF8(string(raw), "")
	}
	return string(raw)
}
