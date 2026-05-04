package ui

import (
	"os"
	"strings"

	"github.com/olekukonko/tablewriter"

	"github.com/shareed2k/honey/internal/hosts"
)

// PrintStaticTable prints the records as an ASCII table to stdout and exits.
func PrintStaticTable(records []hosts.Record) error {
	if len(records) == 0 {
		return nil
	}

	table := tablewriter.NewWriter(os.Stdout)
	table.SetHeader([]string{"PROVIDER", "NAME", "PRIMARY IP", "ZONE / EXTRA"})
	table.SetAutoWrapText(true)
	table.SetAutoFormatHeaders(true)
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)
	table.SetCenterSeparator("*")
	table.SetColumnSeparator("|")
	table.SetRowSeparator("~")
	table.SetHeaderLine(true)
	table.SetBorder(true)
	table.SetTablePadding("\t") // pad with tabs
	table.SetNoWhiteSpace(true)

	for _, r := range records {
		var zoneExtra []string
		if r.Zone != "" {
			zoneExtra = append(zoneExtra, r.Zone)
		}
		if len(r.ExtraIPs) > 0 {
			zoneExtra = append(zoneExtra, strings.Join(r.ExtraIPs, ", "))
		}
		table.Append([]string{
			r.Provider,
			r.Name,
			r.PrimaryIP,
			strings.Join(zoneExtra, " | "),
		})
	}
	table.Render()
	return nil
}
