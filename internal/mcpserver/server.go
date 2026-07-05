package mcpserver

import (
	"context"

	"github.com/go-playground/mold/v4/modifiers"
	"github.com/go-playground/validator/v10"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/audit"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/policy"
	"github.com/shareed2k/honey/internal/searchrun"
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
	// policyEnforcer gates exec_on_host. nil means no OPA policy is configured;
	// built-in critical command-risk denies still apply (secure-by-default).
	policyEnforcer *policy.Enforcer
	// auditSink receives one event per exec_on_host gate decision.
	// Defaults to a no-op sink when audit is disabled in config.
	auditSink audit.Sink = audit.NewNoopSink()
	// globalSearchReg is the global search registry injected into the server.
	globalSearchReg *searchrun.Registry
	// globalExecReg is the global execution registry injected into the server.
	globalExecReg hostexec.Registry
)

// mcpActor is the actor id used in policy input for MCP-driven exec. Stdio MCP
// has no per-call identity, so all MCP exec is attributed to "mcp".
const mcpActor = "mcp"

// NewServer builds the honey MCP server with its tools registered and the given
// config and (optional) policy enforcer wired in. Transport is the caller's
// choice — Run uses stdio; tests use an in-memory transport. A nil enforcer
// leaves OPA opt-in; built-in critical command-risk denies always apply.
func NewServer(cfg *config.File, enf *policy.Enforcer, searchReg *searchrun.Registry, execReg hostexec.Registry) *mcp.Server {
	serverCfg = cfg
	policyEnforcer = enf
	globalSearchReg = searchReg
	globalExecReg = execReg

	// Open audit sink from config if enabled; fall back to no-op.
	auditSink = audit.NewNoopSink()
	if cfg != nil && cfg.Audit.Enabled {
		path := cfg.Audit.EffectivePath()
		if s, err := audit.NewFileSink(path); err != nil {
			zap.L().Warn("audit: failed to open log file", zap.String("path", path), zap.Error(err))
		} else {
			auditSink = s
		}
	}

	s := mcp.NewServer(&mcp.Implementation{Name: "honey", Version: serverVersion}, nil)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_hosts",
		Description: "Search hosts across GCP, AWS, Kubernetes, and Consul in parallel (same behavior as honey search). Returns JSON array of records.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, handleSearchHosts)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_backends",
		Description: "List named backends from the honey config file (requires backends with optional name field in YAML).",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, handleListBackends)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "exec_on_host",
		Description: "Run a shell command on a host via SSH using its IP or hostname directly (use primary_ip from search_hosts). Commands are gated by honey's command-risk engine and OPA policy; critical or policy-denied commands are refused. Records output to session recordings if record_dir is configured.",
		Annotations: &mcp.ToolAnnotations{DestructiveHint: boolPtr(true)},
	}, handleExecOnHost)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "plan_command",
		Description: "Analyze the risk of a shell command without executing it. Returns the risk level, detected signals, and policy decision (allow/deny). Safe: no SSH dial, no side effects.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, handlePlanCommand)
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_host_details",
		Description: "Get full details for a named host across all configured backends: IP addresses, provider, groups, meta, and derived capabilities (ssh, docker_exec). Safe: read-only.",
		Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true},
	}, handleGetHostDetails)
	return s
}

// Run starts the MCP server on stdio until the client disconnects.
// cfg and cfgPath are the honey config already loaded by the CLI root PersistentPreRunE.
func Run(ctx context.Context, cfg *config.File, cfgPath string, searchReg *searchrun.Registry, execReg hostexec.Registry) error {
	_ = cfgPath

	// Build the OPA enforcer when a policy dir is configured. With none set,
	// the enforcer stays nil (opt-in) and only built-in critical command-risk
	// denies apply to exec_on_host.
	var enf *policy.Enforcer
	if dir := config.ResolvePolicyDir(cfg); dir != "" {
		e, err := policy.New(ctx, dir, nil)
		if err != nil {
			return err
		}
		enf = e
	}

	return NewServer(cfg, enf, searchReg, execReg).Run(ctx, &mcp.StdioTransport{})
}

func boolPtr(b bool) *bool { return &b }

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
	out, err := findHostFn(ctx, &in)
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

var listBackendsFn = func(configPath string) (hostapi.ListBackendsOutput, error) {
	return hostapi.ListBackends(configPath, globalSearchReg)
}

func handleListBackends(ctx context.Context, _ *mcp.CallToolRequest, in listBackendsInput) (*mcp.CallToolResult, listBackendsOutput, error) {
	if err := conform.Struct(ctx, &in); err != nil {
		return nil, listBackendsOutput{}, err
	}
	lb, err := listBackendsFn(in.ConfigPath)
	if err != nil {
		return nil, listBackendsOutput{}, err
	}
	rows := make([]listBackendsRow, 0, len(lb.Backends))
	for _, b := range lb.Backends {
		rows = append(rows, listBackendsRow{Kind: b.Kind, Name: b.Name, Hint: b.Hint})
	}
	return nil, listBackendsOutput{Backends: rows}, nil
}
