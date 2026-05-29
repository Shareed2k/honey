package recordings

import (
	"fmt"
	"regexp"
)

// GrepMatch is one matching event plus its optional context window.
type GrepMatch struct {
	Basename  string
	BaseMS    int64 // TimeMS of first event (for computing context offsets)
	OffsetMS  int64 // match event time relative to BaseMS
	Direction string
	Text      string  // decoded content of the matching event
	Before    []Event // up to N events before the match
	After     []Event // up to N events after the match
}

// GrepRecording searches decoded data events for content matching re.
// Searched directions: stdout + stderr always; stdin only if includeStdin is true.
// before and after control the size of the context window around each match.
func GrepRecording(events []Event, basename string, re *regexp.Regexp, includeStdin bool, before, after int) []GrepMatch {
	var baseMS int64
	if len(events) > 0 {
		baseMS = events[0].TimeMS
	}

	var matches []GrepMatch
	for i, e := range events {
		if e.Type != "data" || e.DataB64 == "" {
			continue
		}
		switch e.Direction {
		case "stdout", "stderr":
		case "stdin":
			if !includeStdin {
				continue
			}
		default:
			continue
		}

		text := DecodeDataB64(e.DataB64)
		if !re.MatchString(text) {
			continue
		}

		m := GrepMatch{
			Basename:  basename,
			BaseMS:    baseMS,
			OffsetMS:  e.TimeMS - baseMS,
			Direction: e.Direction,
			Text:      text,
		}
		if before > 0 {
			m.Before = append([]Event(nil), events[max(0, i-before):i]...)
		}
		if after > 0 {
			m.After = append([]Event(nil), events[i+1:min(len(events), i+1+after)]...)
		}
		matches = append(matches, m)
	}
	return matches
}

// FormatOffsetMS formats a session-relative millisecond offset as MM:SS.mmm.
func FormatOffsetMS(offsetMS int64) string {
	if offsetMS < 0 {
		offsetMS = 0
	}
	totalSec := offsetMS / 1000
	ms := offsetMS % 1000
	return fmt.Sprintf("%02d:%02d.%03d", totalSec/60, totalSec%60, ms)
}
