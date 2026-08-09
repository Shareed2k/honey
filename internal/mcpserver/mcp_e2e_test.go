package mcpserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shareed2k/honey/internal/commandrisk"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/searchrun"
)

// newMCPSession creates a honey MCP server + in-memory client session.
// The client session is closed in t.Cleanup.
func newMCPSession(t *testing.T) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := NewServer(nil, nil, nil, searchrun.NewRegistry(nil), nil)
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

// callTool invokes a named tool and returns IsError flag and raw JSON text.
func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) (isError bool, text string) {
	t.Helper()
	res, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool %q: %v", name, err)
	}
	if res.IsError {
		return true, ""
	}
	if len(res.Content) == 0 {
		t.Fatalf("CallTool %q: empty content", name)
	}
	tc, ok := res.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("CallTool %q: content[0] not TextContent", name)
	}
	return false, tc.Text
}

// TestMCPE2E_PlanCommand_safeAllow tests that echo hello passes through the full
// MCP stack and returns decision=allow.
func TestMCPE2E_PlanCommand_safeAllow(t *testing.T) {
	session := newMCPSession(t)

	isErr, text := callTool(t, session, "plan_command", map[string]any{"command": "echo hello"})
	if isErr {
		t.Fatal("expected success, got IsError=true")
	}

	var out planCommandOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Decision != "allow" {
		t.Errorf("Decision = %q, want allow", out.Decision)
	}
	if len(out.Signals) != 0 {
		t.Errorf("safe command should have no signals, got %v", out.Signals)
	}
}

// TestMCPE2E_PlanCommand_criticalDeny tests that rm -rf / returns deny + critical
// risk through the full MCP JSON round-trip.
func TestMCPE2E_PlanCommand_criticalDeny(t *testing.T) {
	session := newMCPSession(t)

	isErr, text := callTool(t, session, "plan_command", map[string]any{"command": "rm -rf /"})
	if isErr {
		t.Fatal("expected success result (deny), got IsError=true")
	}

	var out planCommandOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Decision != "deny" {
		t.Errorf("Decision = %q, want deny", out.Decision)
	}
	if out.Risk != commandrisk.SeverityCritical {
		t.Errorf("Risk = %q, want critical", out.Risk)
	}
	if len(out.Signals) == 0 {
		t.Errorf("expected signals for critical command, got none")
	}
}

// TestMCPE2E_PlanCommand_emptyCommand_isError tests that an empty command returns
// IsError=true (tool-level error surfaced to the LLM, not a protocol error).
func TestMCPE2E_PlanCommand_emptyCommand_isError(t *testing.T) {
	session := newMCPSession(t)

	isErr, _ := callTool(t, session, "plan_command", map[string]any{"command": ""})
	if !isErr {
		t.Error("expected IsError=true for empty command, got success")
	}
}

// TestMCPE2E_PlanCommand_pythonInterpreter tests the python3 dispatcher through
// the full MCP round-trip.
func TestMCPE2E_PlanCommand_pythonInterpreter(t *testing.T) {
	session := newMCPSession(t)

	isErr, text := callTool(t, session, "plan_command", map[string]any{
		"command":     `shutil.rmtree("/")`,
		"interpreter": "python3",
	})
	if isErr {
		t.Fatal("expected success result, got IsError=true")
	}

	var out planCommandOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Decision != "deny" {
		t.Errorf("Decision = %q, want deny", out.Decision)
	}
	if out.Risk != commandrisk.SeverityCritical {
		t.Errorf("Risk = %q, want critical", out.Risk)
	}
}

// TestMCPE2E_GetHostDetails_capabilities tests that get_host_details surfaces
// the correct capabilities for a GCP VM through the full MCP JSON round-trip.
func TestMCPE2E_GetHostDetails_capabilities(t *testing.T) {
	rec := hosts.Record{
		Name:      "prod-web-1",
		Provider:  "gcp",
		PrimaryIP: "10.0.0.5",
	}
	withFakeSearch(t, func(_ context.Context, _ *hostapi.SearchHostsInput) (hostapi.SearchHostsOutput, error) {
		return hostapi.SearchHostsOutput{Records: []hosts.Record{rec}, Count: 1}, nil
	})

	session := newMCPSession(t)

	isErr, text := callTool(t, session, "get_host_details", map[string]any{"name": "prod-web-1"})
	if isErr {
		t.Fatal("expected success, got IsError=true")
	}

	var out getHostDetailsOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Record.Name != "prod-web-1" {
		t.Errorf("Record.Name = %q, want prod-web-1", out.Record.Name)
	}
	found := false
	for _, c := range out.Capabilities {
		if c == "ssh" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected ssh in Capabilities, got %v", out.Capabilities)
	}
}

func TestMCPE2E_SearchHosts(t *testing.T) {
	rec := hosts.Record{
		Name:      "redis-prod-1",
		Provider:  "gcp",
		PrimaryIP: "10.0.0.10",
	}
	withFakeSearch(t, func(_ context.Context, in *hostapi.SearchHostsInput) (hostapi.SearchHostsOutput, error) {
		if in.NameRegex != "redis" {
			t.Errorf("expected NameRegex=redis, got %q", in.NameRegex)
		}
		if in.Providers != "gcp" {
			t.Errorf("expected Providers=gcp, got %q", in.Providers)
		}
		return hostapi.SearchHostsOutput{Records: []hosts.Record{rec}, Count: 1}, nil
	})

	session := newMCPSession(t)

	isErr, text := callTool(t, session, "search_hosts", map[string]any{
		"name_regex": "redis",
		"providers":  "gcp",
	})
	if isErr {
		t.Fatal("expected success, got IsError=true")
	}

	var out searchHostsOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if out.Count != 1 {
		t.Fatalf("Count = %d, want 1", out.Count)
	}
	if len(out.Records) != 1 {
		t.Fatalf("len(Records) = %d, want 1", len(out.Records))
	}
	if out.Records[0].Name != "redis-prod-1" {
		t.Errorf("Records[0].Name = %q, want redis-prod-1", out.Records[0].Name)
	}
}

func TestMCPE2E_ListBackends(t *testing.T) {
	withFakeListBackends(t, func(configPath string) (hostapi.ListBackendsOutput, error) {
		return hostapi.ListBackendsOutput{
			ConfigPath: configPath,
			Backends: []config.BackendRow{
				{Kind: "gcp", Name: "gcp-prod", Hint: "gcp project"},
			},
		}, nil
	})

	session := newMCPSession(t)

	isErr, text := callTool(t, session, "list_backends", map[string]any{
		"config_path": "",
	})
	if isErr {
		t.Fatal("expected success, got IsError=true")
	}

	var out listBackendsOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(out.Backends) != 1 {
		t.Fatalf("len(Backends) = %d, want 1", len(out.Backends))
	}
	if out.Backends[0].Name != "gcp-prod" {
		t.Errorf("Backends[0].Name = %q, want gcp-prod", out.Backends[0].Name)
	}
}

// TestMCPE2E_GetHostDetails_notFound tests that missing host returns IsError=true.
func TestMCPE2E_GetHostDetails_notFound(t *testing.T) {
	withFakeSearch(t, func(_ context.Context, _ *hostapi.SearchHostsInput) (hostapi.SearchHostsOutput, error) {
		return hostapi.SearchHostsOutput{Records: []hosts.Record{}, Count: 0}, nil
	})

	session := newMCPSession(t)

	isErr, _ := callTool(t, session, "get_host_details", map[string]any{"name": "ghost"})
	if !isErr {
		t.Error("expected IsError=true for missing host, got success")
	}
}

func newMCPSessionWithExecReg(t *testing.T, execReg hostexec.Registry) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	server := NewServer(nil, nil, nil, searchrun.NewRegistry(nil), execReg)
	st, ct := mcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, st, nil); err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil)
	session, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type mockExecRegistry struct {
	hostexec.Registry
	output string
}

func (m mockExecRegistry) ForRecord(_ hosts.Record) hostexec.Executor {
	return mockExecutor{output: m.output}
}

type mockExecutor struct {
	hostexec.Executor
	output string
}

func (m mockExecutor) Dial(_ string, _ hosts.Record) (hostexec.HostClient, error) {
	return mockClient{output: m.output}, nil
}

type mockClient struct {
	hostexec.HostClient
	output string
}

func (m mockClient) Run(_ string) ([]byte, error) {
	return []byte(m.output), nil
}

func (m mockClient) Close() error {
	return nil
}

func TestMCPE2E_ExecOnHost(t *testing.T) {
	t.Setenv(execAllowUnverifiedEnv, "1")

	execReg := mockExecRegistry{output: "mock ssh output"}
	session := newMCPSessionWithExecReg(t, execReg)

	isErr, text := callTool(t, session, "exec_on_host", map[string]any{
		"command": "ls /tmp",
		"host":    "10.201.0.45",
	})
	if isErr {
		t.Fatal("expected success, got IsError=true")
	}

	var out execOnHostOutput
	if err := json.Unmarshal([]byte(text), &out); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("len(Results) = %d, want 1", len(out.Results))
	}
	if out.Results[0].Output != "mock ssh output" {
		t.Errorf("Results[0].Output = %q, want 'mock ssh output'", out.Results[0].Output)
	}
}
