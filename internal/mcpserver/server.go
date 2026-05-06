package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shareed2k/honey/internal/hostapi"
)

const serverVersion = "0.1.0"

// Run starts the MCP server on stdio until the client disconnects.
func Run(ctx context.Context) error {
	s := mcp.NewServer(&mcp.Implementation{Name: "honey", Version: serverVersion}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_hosts",
		Description: "Search hosts across GCP, AWS, Kubernetes, and Consul in parallel (same behavior as honey search). Returns JSON array of records.",
	}, handleSearchHosts)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_backends",
		Description: "List named backends from the honey config file (requires backends with optional name field in YAML).",
	}, handleListBackends)
	return s.Run(ctx, &mcp.StdioTransport{})
}

// --- search_hosts ---

type searchHostsInput = hostapi.SearchHostsInput

type searchHostsOutput = hostapi.SearchHostsOutput

func handleSearchHosts(ctx context.Context, _ *mcp.CallToolRequest, in searchHostsInput) (*mcp.CallToolResult, searchHostsOutput, error) {
	out, err := hostapi.SearchHosts(ctx, &in)
	if err != nil {
		return nil, searchHostsOutput{}, err
	}
	return nil, out, nil
}

// --- list_backends ---

type listBackendsInput struct {
	ConfigPath string `json:"config_path,omitempty" jsonschema:"explicit path to honey YAML; empty uses HONEY_CONFIG or default paths"`
}

type listBackendsOutput struct {
	Backends []listBackendsRow `json:"backends"`
}

// listBackendsRow mirrors config.BackendRow for stable MCP JSON without importing config in tool output alias.
type listBackendsRow struct {
	Kind string `json:"kind"`
	Name string `json:"name"`
	Hint string `json:"hint"`
}

func handleListBackends(ctx context.Context, _ *mcp.CallToolRequest, in listBackendsInput) (*mcp.CallToolResult, listBackendsOutput, error) {
	_ = ctx
	lb, err := hostapi.ListBackends(in.ConfigPath)
	if err != nil {
		return nil, listBackendsOutput{}, err
	}
	rows := make([]listBackendsRow, 0, len(lb.Backends))
	for _, b := range lb.Backends {
		rows = append(rows, listBackendsRow{Kind: b.Kind, Name: b.Name, Hint: b.Hint})
	}
	return nil, listBackendsOutput{Backends: rows}, nil
}
