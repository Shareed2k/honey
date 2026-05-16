package plugins

import (
	"context"
	"encoding/json"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
	"github.com/shareed2k/honey/internal/stepkv"
)

// ExecuteStep runs the execute_step export for a plugin step on one host.
// kvSession is optional; when non-nil it is bound for allow_kv plugins via the kv host function.
func (m *Manager) ExecuteStep(ctx context.Context, pluginID, action string, config json.RawMessage, stepIndex int, hostJSON []byte, env map[string]string, execute, secretsDry bool, kvSession *stepkv.Session) (apiv1.ExecuteStepOutput, error) {
	var out apiv1.ExecuteStepOutput
	in := apiv1.ExecuteStepInput{
		APIVersion: apiv1.APIVersion,
		StepIndex:  stepIndex,
		Host:       hostJSON,
		Env:        env,
		PluginID:   pluginID,
		Action:     action,
		Config:     config,
		Execute:    execute,
		SecretsDry: secretsDry,
	}
	callCtx := ctx
	if kvSession != nil {
		callCtx = WithKVSession(ctx, kvSession)
	}
	if err := m.Call(callCtx, pluginID, "execute_step", in, &out); err != nil {
		return out, err
	}
	return out, nil
}

// OnStepResult runs the on_step_result export for a local hook plugin.
func (m *Manager) OnStepResult(ctx context.Context, pluginID, action string, config json.RawMessage, in apiv1.OnStepResultInput, kvSession *stepkv.Session) (apiv1.OnStepResultOutput, error) {
	var out apiv1.OnStepResultOutput
	in.APIVersion = apiv1.APIVersion
	in.PluginID = pluginID
	in.Action = action
	in.Config = config
	callCtx := ctx
	if kvSession != nil {
		callCtx = WithKVSession(ctx, kvSession)
	}
	if err := m.Call(callCtx, pluginID, "on_step_result", in, &out); err != nil {
		return out, err
	}
	return out, nil
}
