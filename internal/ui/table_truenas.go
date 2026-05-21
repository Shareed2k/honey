package ui

import (
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
)

// tableEnterAction selects the TUI action for Enter on the given record.
func tableEnterAction(r hosts.Record) action {
	if r.Provider == "truenas" {
		return actTrueNASAPI
	}
	return actSSH
}

// truenasSSHKeyAllowed reports whether s should open SSH on a TrueNAS row.
func truenasSSHKeyAllowed(r hosts.Record) bool {
	return r.Provider == "truenas" && strings.TrimSpace(r.PrimaryIP) != ""
}
