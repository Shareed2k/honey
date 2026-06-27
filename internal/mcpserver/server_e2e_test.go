package mcpserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/policy"
)

// newE2ESession wires a real MCP client to the honey server over an in-memory
// transport — exercising tool registration, JSON-RPC dispatch, the exec gate,
// and result marshaling end to end.
func newE2ESession(t *testing.T, enf *policy.Enforcer) *mcp.ClientSession {
	t.Helper()
	ctx := context.Background()
	srv := NewServer(&config.File{}, enf)
	serverT, clientT := mcp.NewInMemoryTransports()
	if _, err := srv.Connect(ctx, serverT, nil); err != nil {
		t.Fatalf("server connect: %v", err)
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0"}, nil)
	cs, err := client.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

func callExec(t *testing.T, cs *mcp.ClientSession, command string) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "exec_on_host",
		Arguments: map[string]any{"host": "10.0.0.1", "name": "web1", "command": command},
	})
	if err != nil {
		t.Fatalf("CallTool transport error: %v", err)
	}
	return res
}

func resultText(r *mcp.CallToolResult) string {
	var b strings.Builder
	for _, c := range r.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String()
}

func TestE2E_criticalCommandBlocked(t *testing.T) {
	called := withFakeExec(t)
	cs := newE2ESession(t, nil) // no enforcer: only built-in critical denies

	res := callExec(t, cs, "mkfs.ext4 /dev/sda")
	if !res.IsError {
		t.Fatalf("critical command must be refused: %+v", res)
	}
	if !strings.Contains(resultText(res), "command risk") {
		t.Fatalf("text=%q", resultText(res))
	}
	if *called {
		t.Fatal("SSH must NOT be reached for a blocked command")
	}
}

func TestE2E_requireApprovalBlocked(t *testing.T) {
	called := withFakeExec(t)
	enf, err := policy.NewFromSource(context.Background(), "p.rego", `package honey
import rego.v1
default allow := true
decision := "require_approval"
`)
	if err != nil {
		t.Fatal(err)
	}
	cs := newE2ESession(t, enf)

	res := callExec(t, cs, "whoami")
	if !res.IsError {
		t.Fatalf("require_approval must refuse over MCP: %+v", res)
	}
	if *called {
		t.Fatal("SSH must NOT be reached when approval required")
	}
}

func TestE2E_benignCommandExecutes(t *testing.T) {
	called := withFakeExec(t)
	cs := newE2ESession(t, nil)

	res := callExec(t, cs, "whoami")
	if res.IsError {
		t.Fatalf("benign command must run: %s", resultText(res))
	}
	if !*called {
		t.Fatal("SSH should be reached for an allowed command")
	}
	// The structured result carries the exec output ("ok" from the fake).
	if sc := structuredJSON(t, res); !strings.Contains(sc, "ok") {
		t.Fatalf("structured result missing output: %s", sc)
	}
}

func TestE2E_denyPolicyBlocks(t *testing.T) {
	called := withFakeExec(t)
	enf, err := policy.NewFromSource(context.Background(), "p.rego", `package honey
import rego.v1
default allow := false
deny_reason := "blocked by test policy" if not allow
`)
	if err != nil {
		t.Fatal(err)
	}
	cs := newE2ESession(t, enf)

	res := callExec(t, cs, "whoami")
	if !res.IsError {
		t.Fatalf("deny policy must refuse: %+v", res)
	}
	if !strings.Contains(resultText(res), "blocked by test policy") {
		t.Fatalf("text=%q", resultText(res))
	}
	if *called {
		t.Fatal("SSH must NOT be reached when policy denies")
	}
}

func structuredJSON(t *testing.T, r *mcp.CallToolResult) string {
	t.Helper()
	if r.StructuredContent == nil {
		return ""
	}
	b, err := json.Marshal(r.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured: %v", err)
	}
	return string(b)
}
