package metrics

import "time"

// Observer records honey operational metrics (recipe runs, plugins, SSH, etc.).
// Pass nil when metrics are disabled; callers must guard before invoking methods
// or use ui helpers that accept a nil Observer.
type Observer interface {
	ObserveRecipeRun(mode, recipeType, status string, d time.Duration)
	ObserveRecipeStep(kind, status string, d time.Duration, retryAttempts int)
	ObserveRecipeHostResult(status string)
	ObservePluginExec(pluginID, action, status string, d time.Duration)
	ObservePluginExecDuration(pluginID, action string, d time.Duration)
	ObserveSSHOperation(op, status string, d time.Duration)
	ObserveAgentTransfer(status string, d time.Duration)
	ObserveRecipeValidate(status string, d time.Duration)
	ObserveExecCommand(status string, hostCount int, d time.Duration)
}

// ObserveRecipeRun records a recipe dry-run or execute completion.
func (r *Registry) ObserveRecipeRun(mode, recipeType, status string, d time.Duration) {
	r.recipeRuns.WithLabelValues(mode, recipeType, status).Inc()
	r.recipeRunDuration.WithLabelValues(mode, recipeType).Observe(d.Seconds())
}

// ObserveRecipeStep records one recipe step completion.
func (r *Registry) ObserveRecipeStep(kind, status string, d time.Duration, retryAttempts int) {
	r.recipeSteps.WithLabelValues(kind, status).Inc()
	r.recipeStepDuration.WithLabelValues(kind).Observe(d.Seconds())
	if retryAttempts > 1 {
		r.recipeStepRetryAttempts.WithLabelValues(kind).Add(float64(retryAttempts - 1))
	}
}

// ObserveRecipeHostResult records one per-host row from a recipe step.
func (r *Registry) ObserveRecipeHostResult(status string) {
	r.recipeHostResults.WithLabelValues(status).Inc()
}

// ObservePluginExec records a WASM plugin action attempt (counter only when d < 0).
func (r *Registry) ObservePluginExec(pluginID, action, status string, d time.Duration) {
	r.pluginExecutions.WithLabelValues(pluginID, action, status).Inc()
	if d >= 0 {
		r.pluginExecutionDuration.WithLabelValues(pluginID, action).Observe(d.Seconds())
	}
}

// ObservePluginExecDuration records final plugin attempt latency without incrementing the counter.
func (r *Registry) ObservePluginExecDuration(pluginID, action string, d time.Duration) {
	r.pluginExecutionDuration.WithLabelValues(pluginID, action).Observe(d.Seconds())
}

// ObserveSSHOperation records one SSH/SFTP/script/truenas operation.
func (r *Registry) ObserveSSHOperation(op, status string, d time.Duration) {
	r.sshOperations.WithLabelValues(op, status).Inc()
	r.sshOperationDuration.WithLabelValues(op).Observe(d.Seconds())
}

// ObserveAgentTransfer records an agent-based cloud transfer.
func (r *Registry) ObserveAgentTransfer(status string, d time.Duration) {
	r.agentTransfers.WithLabelValues(status).Inc()
	r.agentTransferDuration.Observe(d.Seconds())
}

// ObserveRecipeValidate records a recipe validate-content API call.
func (r *Registry) ObserveRecipeValidate(status string, d time.Duration) {
	r.recipeValidate.WithLabelValues(status).Inc()
	r.recipeValidateDuration.Observe(d.Seconds())
}

// ObserveExecCommand records a raw exec API call.
func (r *Registry) ObserveExecCommand(status string, hostCount int, d time.Duration) {
	r.execCommands.WithLabelValues(status).Inc()
	r.execCommandHosts.Observe(float64(hostCount))
	r.execCommandDuration.Observe(d.Seconds())
}
