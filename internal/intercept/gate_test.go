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
