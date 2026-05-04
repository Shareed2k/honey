package cli

import (
	"strconv"
	"strings"
)

// parseSelection parses values from the CRUD menu (e.g. "gcp:0") into provider ID and index.
func parseSelection(sel string) (string, int) {
	parts := strings.Split(sel, ":")
	if len(parts) != 2 {
		return "", -1
	}
	idx, _ := strconv.Atoi(parts[1])
	return parts[0], idx
}
