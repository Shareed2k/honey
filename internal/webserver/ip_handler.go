package webserver

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/go-chi/chi/v5"
	"github.com/olekukonko/tablewriter"
	"github.com/olekukonko/tablewriter/renderer"

	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
)

// handleIP returns the primary IP of a backend host requested via name, regex, or path parameters.
// It acts as a DNS resolver or "ifconfig.me" for configured honey backends.
func (s *Server) handleIP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	wantsJSON := strings.HasSuffix(path, ".json")
	if wantsJSON {
		path = strings.TrimSuffix(path, ".json")
	}

	nameQuery := strings.TrimSpace(r.URL.Query().Get("name"))
	regexQuery := strings.TrimSpace(r.URL.Query().Get("name_regex"))

	var backendParam, providerParam string
	nameParam := chi.URLParam(r, "name")
	if nameParam != "" {
		if strings.HasPrefix(path, "/ip/backend") {
			backendParam = nameParam
		} else if strings.HasPrefix(path, "/ip/provider") {
			providerParam = nameParam
		}
	}

	if nameQuery == "" && regexQuery == "" && backendParam == "" && providerParam == "" {
		http.Error(w, "missing search parameters (name, name_regex, backend, or provider)", http.StatusBadRequest)
		return
	}

	// Request the full inventory to leverage the cache.
	in := &hostapi.SearchHostsInput{
		Backends:   backendParam,
		Providers:  providerParam,
		ConfigPath: s.opts.ConfigPath,
		Config:     s.opts.Config,
		NoCache:    s.opts.NoCache,
		Refresh:    s.opts.Refresh,
	}

	out, err := hostapi.SearchHosts(r.Context(), in, s.opts.ExecRegistry, s.opts.SearchRegistry)
	if err != nil {
		http.Error(w, fmt.Sprintf("search error: %v", err), http.StatusInternalServerError)
		return
	}

	// Filter the records in-memory.
	query := hosts.Query{
		NameSubstring: nameQuery,
		NameRegex:     regexQuery,
	}

	var filtered []hosts.Record
	for _, rec := range out.Records {
		if backendParam != "" && rec.Meta["backend_name"] != backendParam {
			continue
		}
		if providerParam != "" && rec.Provider != providerParam {
			continue
		}

		matches, err := query.MatchesName(rec.Name)
		if err != nil {
			http.Error(w, fmt.Sprintf("regex error: %v", err), http.StatusBadRequest)
			return
		}
		if matches {
			filtered = append(filtered, rec)
		}
	}

	if len(filtered) == 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	if wantsJSON {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(filtered)
		return
	}

	wantsHTML := strings.Contains(r.Header.Get("Accept"), "text/html")
	if wantsHTML {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<!DOCTYPE html>
<html>
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>honey IP Resolver</title>
<style>
	body { font-family: system-ui, -apple-system, sans-serif; padding: 20px; line-height: 1.5; color: #333; }
	table { border-collapse: collapse; width: 100%; max-width: 1000px; margin-top: 20px; }
	th, td { text-align: left; padding: 12px; border-bottom: 1px solid #ddd; }
	th { background-color: #f8f9fa; font-weight: 600; }
	tr:hover { background-color: #f5f5f5; }
	.meta-blob { font-family: monospace; font-size: 0.9em; color: #666; }
	.meta-table { width: auto; margin-top: 0; }
	.meta-table td { padding: 4px 8px; border: none; border-bottom: 1px solid #eee; }
	.meta-table tr:last-child td { border-bottom: none; }
	.meta-table tr:hover { background-color: transparent; }
</style>
</head>
<body>
<h2>Resolved Hosts</h2>
`))

		tbl := tablewriter.NewTable(w, tablewriter.WithRenderer(renderer.NewHTML(renderer.HTMLConfig{EscapeContent: false})))
		tbl.Header("Name", "Primary IP", "Zone", "Provider", "Metadata")

		for _, rec := range filtered {
			var metaBuilder strings.Builder
			if len(rec.Meta) > 0 {
				metaBuilder.WriteString(`<table class="meta-table"><tbody>`)
				keys := make([]string, 0, len(rec.Meta))
				for k := range rec.Meta {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				for _, k := range keys {
					fmt.Fprintf(&metaBuilder, "<tr><td>%s</td><td>%s</td></tr>", html.EscapeString(k), html.EscapeString(rec.Meta[k]))
				}
				metaBuilder.WriteString(`</tbody></table>`)
			}

			tbl.Append([]string{
				html.EscapeString(rec.Name),
				html.EscapeString(rec.PrimaryIP),
				html.EscapeString(rec.Zone),
				html.EscapeString(rec.Provider),
				metaBuilder.String(),
			})
		}

		tbl.Render()

		w.Write([]byte(`</body>
</html>
`))
		return
	}

	// Plain text CLI response
	w.Header().Set("Content-Type", "text/plain")
	if len(filtered) == 1 {
		w.Write([]byte(filtered[0].PrimaryIP + "\n"))
		return
	}

	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tPRIMARY_IP\tPROVIDER\tZONE")
	for _, rec := range filtered {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", rec.Name, rec.PrimaryIP, rec.Provider, rec.Zone)
	}
	tw.Flush()
}
