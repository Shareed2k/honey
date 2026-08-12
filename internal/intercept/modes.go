package intercept

import (
	"fmt"

	"github.com/shareed2k/mogate/pkg/local"
)

// Mode names accepted by ParseModes and rendered by a Session's audit and gate
// payloads.
const (
	modeEgress   = "egress"
	modeIncoming = "incoming"
	modeFiles    = "files"
)

// ParseModes maps interception mode names to the mode set a Session runs with.
// It accepts "egress", "incoming", and "files" (in any combination), fails on
// an unknown name, and requires at least one mode. Keeping the mapping here
// lets the command layer wire modes without naming the data-plane package.
func ParseModes(names []string) (local.Modes, error) {
	var m local.Modes
	for _, name := range names {
		switch name {
		case modeEgress:
			m.Egress = true
		case modeIncoming:
			m.Incoming = true
		case modeFiles:
			m.Files = true
		default:
			return local.Modes{}, fmt.Errorf("intercept: unknown mode %q (want %s, %s, or %s)", name, modeEgress, modeIncoming, modeFiles)
		}
	}
	if !m.Egress && !m.Incoming && !m.Files {
		return local.Modes{}, fmt.Errorf("intercept: at least one mode is required (%s, %s, or %s)", modeEgress, modeIncoming, modeFiles)
	}
	return m, nil
}
