package intercept

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/policy"
)

func sampleInput() GateInput {
	return GateInput{
		Actor:      "roman",
		Cluster:    "prod",
		Namespace:  "apps",
		Pod:        "api-0",
		Container:  "app",
		Mode:       []string{"egress"},
		AgentImage: "registry.example/agent:1",
	}
}

func TestGate_allow(t *testing.T) {
	t.Parallel()

	src := `package honey
default allow := false
allow if input.action == "intercept"`
	enf, err := policy.NewFromSource(context.Background(), "intercept.rego", src)
	require.NoError(t, err)

	assert.NoError(t, gate(context.Background(), enf, sampleInput()))
}

func TestGate_deny(t *testing.T) {
	t.Parallel()

	src := `package honey
default allow := false`
	enf, err := policy.NewFromSource(context.Background(), "intercept.rego", src)
	require.NoError(t, err)

	err = gate(context.Background(), enf, sampleInput())
	require.Error(t, err)
	assert.ErrorIs(t, err, errGateDenied)
}

func TestGate_denyOnWrongAction(t *testing.T) {
	t.Parallel()

	// Policy allows only a different action; the intercept input must be denied.
	src := `package honey
default allow := false
allow if input.action == "exec"`
	enf, err := policy.NewFromSource(context.Background(), "intercept.rego", src)
	require.NoError(t, err)

	require.Error(t, gate(context.Background(), enf, sampleInput()))
}

func TestGate_nilEnforcerFailsClosed(t *testing.T) {
	t.Parallel()

	err := gate(context.Background(), nil, sampleInput())
	require.Error(t, err)
	assert.ErrorIs(t, err, errGateDenied)
}

// newAllowPolicy builds an explicit allow policy for action=="intercept".
func newAllowPolicy(t *testing.T) *policy.Enforcer {
	t.Helper()
	enf, err := policy.NewFromSource(context.Background(), "test", `package honey
import rego.v1
default allow := false
allow if { input.action == "intercept" }`)
	require.NoError(t, err)
	return enf
}

func TestGate_IncludesClaimsWhenPresent(t *testing.T) {
	t.Parallel()

	// Policy asserts the actual identity values reach OPA input; test fails if
	// the four if blocks in gate() are deleted.
	src := `package honey
import rego.v1
default allow := false
allow if {
	input.action == "intercept"
	input.subject == "alice-sub"
	input.groups[_] == "developers"
	input.claims.department == "payments"
}`
	enf, err := policy.NewFromSource(context.Background(), "test", src)
	require.NoError(t, err)

	err = gate(context.Background(), enf, GateInput{
		Actor:   "alice",
		Cluster: "prod",
		Mode:    []string{"egress"},
		Subject: "alice-sub",
		Email:   "alice@corp.example",
		Groups:  []string{"developers"},
		Claims:  map[string]any{"department": "payments"},
	})
	require.NoError(t, err)
}

func TestGate_EmptyIdentityOmitsKeys_StillWorks(t *testing.T) {
	t.Parallel()

	// Policy allows ONLY when identity keys are absent; test fails if the gate
	// ever adds those keys on the direct path.
	src := `package honey
import rego.v1
default allow := false
allow if {
	input.action == "intercept"
	not input.subject
	not input.claims
}`
	enf, err := policy.NewFromSource(context.Background(), "test", src)
	require.NoError(t, err)

	err = gate(context.Background(), enf, GateInput{Actor: "alice", Cluster: "prod", Mode: []string{"egress"}})
	require.NoError(t, err)
}
