//go:build integration

package integration

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/webserver"
)

// approvalPolicy: recipe_execute requires approval until execution.approved;
// recipe_approve allows only when approver != requester; api/step allowed.
const approvalPolicy = `package honey
import rego.v1
default allow := false
default deny_reason := ""
default decision := ""

allow if input.action == "api_request"
allow if input.action == "step_execute"
allow if input.action == "command_exec"

allow if {
	input.action == "recipe_execute"
	input.execution.approved == true
}
decision := "require_approval" if {
	input.action == "recipe_execute"
	not input.execution.approved
}
deny_reason := "approval required" if {
	input.action == "recipe_execute"
	not input.execution.approved
}

allow if {
	input.action == "recipe_approve"
	input.approver != input.requester
}`

func TestOPAE2E_ApprovalFlow(t *testing.T) {
	target := newSSHTarget(t)
	enf := newEnforcer(t, approvalPolicy)
	client := &http.Client{Timeout: 30 * time.Second}

	out := "/tmp/opa_approval.txt"
	recipePath := writeCmdRecipe(t, "opa-approval", out)
	base := cueExecServer(t, target, recipePath, webserver.Options{
		DisableAuth:      true,
		TrustedProxyNets: mustCIDRs(t, "127.0.0.0/8", "::/0"),
		Enforcer:         enf,
	})

	asAlice := map[string]string{"X-Honey-User": "alice"}

	// 1. Alice runs a high-risk recipe → held pending approval (202).
	resp := postCueExec(t, client, base, recipePath, []hosts.Record{target.rec}, []string{"MARK=run"}, asAlice)
	require.Equal(t, http.StatusAccepted, resp.StatusCode)
	var pending struct {
		Status string `json:"status"`
		ID     string `json:"id"`
	}
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&pending))
	resp.Body.Close()
	require.Equal(t, "pending_approval", pending.Status)
	require.NotEmpty(t, pending.ID)

	// File must not exist yet.
	if _, err := target.readFile(t, out); err == nil {
		t.Fatal("recipe must not run before approval")
	}

	// 2. Self-approval by alice is forbidden by recipe_approve policy.
	respSelf := doJSONHeaders(t, client, http.MethodPost, base+"/api/v1/approvals/"+pending.ID,
		map[string]string{"decision": "approve"}, asAlice)
	require.Equal(t, http.StatusForbidden, respSelf.StatusCode)
	respSelf.Body.Close()

	// 3. Bob approves.
	respApprove := doJSONHeaders(t, client, http.MethodPost, base+"/api/v1/approvals/"+pending.ID,
		map[string]string{"decision": "approve"}, map[string]string{"X-Honey-User": "bob"})
	require.Equal(t, http.StatusOK, respApprove.StatusCode)
	respApprove.Body.Close()

	// 4. Alice re-runs with the approval id → now allowed, command runs.
	body := webserver.CueExecRequest{
		RecipePath: recipePath,
		Execute:    true,
		SSHUser:    "testuser",
		ApprovalID: pending.ID,
		Records:    []hosts.Record{target.rec},
		Env:        []string{"MARK=run"},
	}
	respRun := doJSONHeaders(t, client, http.MethodPost, base+"/api/v1/cue-exec", body, asAlice)
	require.Equal(t, http.StatusOK, respRun.StatusCode)
	respRun.Body.Close()

	time.Sleep(time.Second)
	got, err := target.readFile(t, out)
	require.NoError(t, err)
	require.Contains(t, got, "run")
}
