package truenasshell

import (
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/truenasprovider"
)

// ConsoleTrueNASAPI selects the TrueNAS middleware web shell instead of SSH.
const ConsoleTrueNASAPI = "truenas_api"

// ShouldUseTrueNASShell reports whether the web terminal should bridge to TrueNAS /websocket/shell.
func ShouldUseTrueNASShell(rec hosts.Record, console string) bool {
	if rec.Provider != "truenas" {
		return false
	}
	if strings.TrimSpace(console) != ConsoleTrueNASAPI {
		return false
	}
	if err := shellOptionsSupported(rec); err != nil {
		return false
	}
	_, ok := truenasprovider.BackendByName(rec.Meta["backend_name"])
	return ok
}
