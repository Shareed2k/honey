package recordings

import (
	"fmt"
	"time"
)

// SessionStats holds computed statistics for one recording.
type SessionStats struct {
	Basename    string `json:"basename"`
	DurationMS  int64  `json:"duration_ms"`
	Duration    string `json:"-"` // human-readable; omitted from JSON
	StdinBytes  int    `json:"stdin_bytes"`
	StdoutBytes int    `json:"stdout_bytes"`
	StderrBytes int    `json:"stderr_bytes"`
	ErrorCount  int    `json:"error_count"`
	ExitCode    int    `json:"exit_code"` // 0=close, 1=error, -1=unknown
	Trigger     string `json:"trigger,omitempty"`
	Provider    string `json:"provider,omitempty"`
	HostName    string `json:"host_name,omitempty"`
	User        string `json:"user,omitempty"`
}

// ComputeStats derives a SessionStats from a loaded slice of events.
func ComputeStats(events []Event, basename string) SessionStats {
	s := SessionStats{Basename: basename, ExitCode: -1}
	if len(events) == 0 {
		return s
	}
	s.DurationMS = events[len(events)-1].TimeMS - events[0].TimeMS
	s.Duration = formatDurationMS(s.DurationMS)

	for _, e := range events {
		switch e.Type {
		case "open":
			s.Trigger, _, s.Provider, s.HostName, _, s.User = ParseOpenMessage(e.Message)
		case "data":
			if e.DataB64 == "" {
				continue
			}
			n := len(DecodeDataB64(e.DataB64))
			switch e.Direction {
			case "stdin":
				s.StdinBytes += n
			case "stdout":
				s.StdoutBytes += n
			case "stderr":
				s.StderrBytes += n
			}
		case "error":
			s.ErrorCount++
		}
	}

	for i := len(events) - 1; i >= 0; i-- {
		switch events[i].Type {
		case "close":
			s.ExitCode = 0
			goto done
		case "error":
			s.ExitCode = 1
			goto done
		}
	}
done:
	return s
}

func formatDurationMS(ms int64) string {
	if ms < 0 {
		ms = 0
	}
	d := time.Duration(ms) * time.Millisecond
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	s := int(d.Seconds()) % 60
	millis := int(d.Milliseconds()) % 1000
	switch {
	case h > 0:
		return fmt.Sprintf("%dh%02dm%02ds", h, m, s)
	case m > 0:
		return fmt.Sprintf("%dm%02ds", m, s)
	case s > 0:
		return fmt.Sprintf("%d.%03ds", s, millis)
	default:
		return fmt.Sprintf("%dms", millis)
	}
}

// FormatBytes formats a byte count as a human-readable string.
func FormatBytes(n int) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%d KB", n>>10)
	default:
		return fmt.Sprintf("%d B", n)
	}
}
