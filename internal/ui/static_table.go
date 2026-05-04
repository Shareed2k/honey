package ui

import (
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/tw"

	"github.com/shareed2k/honey/internal/hosts"
)

// PrintStaticTable prints the records as an ASCII table to stdout and exits.
func PrintStaticTable(records []hosts.Record) error {
	if len(records) == 0 {
		return nil
	}

	tbl := tablewriter.NewTable(
		os.Stdout,
		tablewriter.WithHeader([]string{"PROVIDER", "NAME", "PRIMARY IP", "ZONE / EXTRA"}),
		tablewriter.WithRowAlignment(tw.AlignLeft),
		tablewriter.WithHeaderAlignment(tw.AlignLeft),
		tablewriter.WithSymbols(tw.NewSymbols(tw.StyleDefault)),
	)

	for _, r := range records {
		var zoneExtra []string
		if r.Zone != "" {
			zoneExtra = append(zoneExtra, r.Zone)
		}
		if len(r.ExtraIPs) > 0 {
			zoneExtra = append(zoneExtra, strings.Join(r.ExtraIPs, ", "))
		}
		_ = tbl.Append([]string{
			r.Provider,
			r.Name,
			r.PrimaryIP,
			strings.Join(zoneExtra, " | "),
		})
	}
	return tbl.Render()
}
