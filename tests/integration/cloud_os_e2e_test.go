//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestRecipeE2E_CloudAndOS(t *testing.T) {
	sshH, sshP, keyFile := startUbuntuSSH(t)

	reg := &testRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: sshH}, sshP)

	cueContent := `
recipe: {
	name: "test-cloud-os"
	type: "graph"
	defaults: { run_as: "root" }
	steps: [
		{
			id: "pkg"
			host: "*"
			package: { name: "jq", state: "present" }
		},
		{
			id: "verify_pkg"
			host: "*"
			depends: ["pkg"]
			command: "jq --version"
		},
		{
			id: "svc"
			host: "*"
			depends: ["verify_pkg"]
			service: { name: "cron", state: "started" }
		},
		{
			id: "verify_svc"
			host: "*"
			depends: ["svc"]
			command: "service cron status"
		},
		{
			id: "aws_test"
			host: "*"
			depends: ["svc"]
			aws: { service: "s3", operation: "ls", params: { bucket: "test-bucket" } }
		},
		{
			id: "gcp_test"
			host: "*"
			depends: ["aws_test"]
			gcp: { service: "compute", operation: "instances list", params: { project: "my-project" } }
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outCh := make(chan engine.HostExecResult, 20)

	params := engine.CueRecipeRunParams{
		Recipe:  recipe,
		Records: []hosts.Record{rec},
		SSHUser: "testuser",
		Execute: true,
		Reg:     reg,
	}

	go func() {
		defer close(outCh)
		err := engine.StreamCueRecipeStepsGraph(ctx, &engine.CueRun{
			Params:        params,
			OutputCapture: cuetry.NewRecipeOutputCapture(),
			OutputStore:   cuetry.NewStepOutputStore(),
		}, outCh)
		assert.NoError(t, err)
	}()

	var results []engine.HostExecResult
	for res := range outCh {
		results = append(results, res)
	}

	require.Len(t, results, 6)

	var outputs []string
	var stepIDs []string
	for _, res := range results {
		t.Logf("Step %s Output: %s", res.Name, res.Output)
		outputs = append(outputs, res.Output)
		stepIDs = append(stepIDs, res.StepID)
	}

	// Since it's a graph, we need to find the specific step output by name
	getOutput := func(id string) string {
		for i, stepID := range stepIDs {
			if stepID == id {
				return outputs[i]
			}
		}
		return ""
	}

	require.Empty(t, getOutput("pkg")) // Output should be silenced
	require.Contains(t, getOutput("verify_pkg"), "jq") // jq --version
	require.Contains(t, getOutput("svc"), "Starting periodic command scheduler") // fallback to service successful
	require.Contains(t, getOutput("verify_svc"), "cron is running")              // service cron status
	require.Contains(t, getOutput("aws_test"), "aws-mock s3 ls --bucket test-bucket")
	require.Contains(t, getOutput("gcp_test"), "gcp-mock compute instances list --project my-project")
}

func TestRecipeE2E_Systemd(t *testing.T) {
	sshH, sshP, keyFile := startUbuntuSystemd(t)

	reg := &testRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
	}

	rec := hosts.CloneWithMetaSSHPort(hosts.Record{Provider: "test", Name: "ssh-systemd-test", PrimaryIP: sshH}, sshP)

	cueContent := `
recipe: {
	name: "test-systemd"
	type: "graph"
	defaults: { run_as: "root" }
	steps: [
		{
			id: "svc"
			host: "*"
			service: { name: "cron", state: "started" }
		},
		{
			id: "verify_svc"
			host: "*"
			depends: ["svc"]
			service: { name: "cron", state: "status" }
		}
	]
}
`
	recipe, err := cuetry.ParseRemoteRecipe([]byte(cueContent), nil)
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	outCh := make(chan engine.HostExecResult, 10)

	params := engine.CueRecipeRunParams{
		Recipe:  recipe,
		Records: []hosts.Record{rec},
		SSHUser: "testuser",
		Execute: true,
		Reg:     reg,
	}

	go func() {
		defer close(outCh)
		err := engine.StreamCueRecipeStepsGraph(ctx, &engine.CueRun{
			Params:        params,
			OutputCapture: cuetry.NewRecipeOutputCapture(),
			OutputStore:   cuetry.NewStepOutputStore(),
		}, outCh)
		assert.NoError(t, err)
	}()

	var results []engine.HostExecResult
	for res := range outCh {
		results = append(results, res)
	}

	require.Len(t, results, 2)

	var outputs []string
	var stepIDs []string
	for _, res := range results {
		t.Logf("Step %s Output: %s", res.Name, res.Output)
		outputs = append(outputs, res.Output)
		stepIDs = append(stepIDs, res.StepID)
	}

	getOutput := func(id string) string {
		for i, stepID := range stepIDs {
			if stepID == id {
				return outputs[i]
			}
		}
		return ""
	}

	require.Contains(t, getOutput("verify_svc"), "active")
}
