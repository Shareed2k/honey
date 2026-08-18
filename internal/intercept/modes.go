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
	modeEnv      = "env"
)

// ModeStrings renders a mode set as its canonical string-slice form (the same
// names ParseModes accepts and a Session's audit/gate payloads carry). It lets
// callers outside this package (e.g. the web server's interception registry)
// record the active modes without re-deriving the mapping.
func ModeStrings(m local.Modes) []string {
	return modeStrings(m)
}

// ParseModes maps interception mode names to the mode set a Session runs with.
// It accepts "egress", "incoming", "files", and "env" (in any combination),
// fails on an unknown name, and requires at least one mode. Keeping the mapping
// here lets the command layer wire modes without naming the data-plane package.
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
		case modeEnv:
			m.Env = true
		default:
			return local.Modes{}, fmt.Errorf("intercept: unknown mode %q (want %s, %s, %s, or %s)", name, modeEgress, modeIncoming, modeFiles, modeEnv)
		}
	}
	if !m.Egress && !m.Incoming && !m.Files && !m.Env {
		return local.Modes{}, fmt.Errorf("intercept: at least one mode is required (%s, %s, %s, or %s)", modeEgress, modeIncoming, modeFiles, modeEnv)
	}
	return m, nil
}
