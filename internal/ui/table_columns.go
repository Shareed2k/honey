package ui

import (
	"charm.land/bubbles/v2/table"
)

func recalculateTableColumns(w int) []table.Column {
	// Fixed widths
	wMark := 2
	wProvider := 8
	wIP := 16
	wZone := 18
	wRegion := 14

	// Borders and padding approximate width
	fixedTotal := wMark + wProvider + wIP + wZone + wRegion + 16 // 16 for approx column padding/borders

	wName := 26 // Default minimum
	if w > fixedTotal {
		remaining := w - fixedTotal
		if remaining > 26 {
			wName = remaining
		}
	}

	return []table.Column{
		{Title: "*", Width: wMark},
		{Title: "Provider", Width: wProvider},
		{Title: "Name", Width: wName},
		{Title: "IP", Width: wIP},
		{Title: "Zone", Width: wZone},
		{Title: "Region/DC", Width: wRegion},
	}
}
