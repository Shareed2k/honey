package mcpserver

import (
	"context"

	"github.com/go-playground/mold/v4/modifiers"
	"github.com/go-playground/validator/v10"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
)

const serverVersion = "0.1.1"

var (
	conform  = modifiers.New()
	validate = validator.New(validator.WithRequiredStructEnabled())
)

// serverCfg holds the config loaded by the CLI root command,
// set once in Run() before any tool handlers are invoked.
var (
	serverCfg *config.File
)

// Run starts the MCP server on stdio until the client disconnects.
// cfg and cfgPath are the honey config already loaded by the CLI root PersistentPreRunE.
func Run(ctx context.Context, cfg *config.File, cfgPath string) error {
	serverCfg = cfg
	_ = cfgPath
	s := mcp.NewServer(&mcp.Implementation{Name: "honey", Version: serverVersion}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_hosts",
		Description: "Search hosts across GCP, AWS, Kubernetes, and Consul in parallel (same behavior as honey search). Returns JSON array of records.",
	}, handleSearchHosts)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_backends",
		Description: "List named backends from the honey config file (requires backends with optional name field in YAML).",
	}, handleListBackends)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "exec_on_host",
		Description: "Run a shell command on a host via SSH using its IP or hostname directly (use primary_ip from search_hosts). Records output to session recordings if record_dir is configured.",
	}, handleExecOnHost)
	return s.Run(ctx, &mcp.StdioTransport{})
}

// --- search_hosts ---

type searchHostsInput = hostapi.SearchHostsInput

type searchHostsOutput = hostapi.SearchHostsOutput

func handleSearchHosts(ctx context.Context, _ *mcp.CallToolRequest, in searchHostsInput) (*mcp.CallToolResult, searchHostsOutput, error) {
	if err := conform.Struct(ctx, &in); err != nil {
		return nil, searchHostsOutput{}, err
	}
	if err := validate.Struct(in); err != nil {
		return nil, searchHostsOutput{}, err
	}
	out, err := hostapi.SearchHosts(ctx, &in, nil, nil)
	if err != nil {
		return nil, searchHostsOutput{}, err
	}
	return nil, out, nil
}

// --- list_backends ---

type listBackendsInput struct {
	ConfigPath string `json:"config_path,omitempty" mod:"trim"`
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
	if err := conform.Struct(ctx, &in); err != nil {
		return nil, listBackendsOutput{}, err
	}
	lb, err := hostapi.ListBackends(in.ConfigPath, nil)
	if err != nil {
		return nil, listBackendsOutput{}, err
	}
	rows := make([]listBackendsRow, 0, len(lb.Backends))
	for _, b := range lb.Backends {
		rows = append(rows, listBackendsRow{Kind: b.Kind, Name: b.Name, Hint: b.Hint})
	}
	return nil, listBackendsOutput{Backends: rows}, nil
}
