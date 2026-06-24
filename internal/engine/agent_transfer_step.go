package engine

import (
	"fmt"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
)

func init() {
	RegisterStepExecutor(cuetry.KindAgentTransfer, &AgentTransferExecutor{})
}

// AgentTransferExecutor executes the corresponding recipe step.
type AgentTransferExecutor struct{}

// AgentTransferCloudFromRecipe ...
func AgentTransferCloudFromRecipe(c *cuetry.RecipeAgentTransferCloud) AgentCloudBackend {
	if c == nil {
		return AgentCloudBackend{}
	}
	return AgentCloudBackend{
		Provider: strings.TrimSpace(c.Provider),
		Bucket:   strings.TrimSpace(c.Bucket),
		Prefix:   strings.TrimSpace(c.Prefix),
		Object:   strings.TrimSpace(c.Object),
		Region:   strings.TrimSpace(c.Region),
		Endpoint: strings.TrimSpace(c.Endpoint),
	}
}

// CloudBackendRefFromRecipe ...
func CloudBackendRefFromRecipe(r *cuetry.RecipeCloudBackendRef) *CloudBackendRef {
	if r == nil {
		return nil
	}
	return &CloudBackendRef{
		Kind:  strings.TrimSpace(r.Kind),
		Name:  strings.TrimSpace(r.Name),
		Index: r.Index,
	}
}

// SummarizeAgentTransferEvents ...
func SummarizeAgentTransferEvents(events []AgentTransferEvent) string {
	var b strings.Builder
	for _, ev := range events {
		line := ev.Stage
		if ev.Host != "" {
			line = fmt.Sprintf("%s@%s", ev.Stage, ev.Host)
		}
		if strings.TrimSpace(ev.Message) != "" {
			line += ": " + strings.TrimSpace(ev.Message)
		}
		if !ev.Success && strings.TrimSpace(ev.Error) != "" {
			line += " (" + strings.TrimSpace(ev.Error) + ")"
		}
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(strings.TrimSpace(line))
	}
	s := strings.TrimSpace(b.String())
	if s == "" {
		return "(no events)"
	}
	return s
}

// ExecuteDryRun executes a dry run of the step.
func (e *AgentTransferExecutor) ExecuteDryRun(sc *StepContext) error {
	out, records, sshUser, configPath, i, step := sc.Out, sc.Run.Params.Records, sc.Run.Params.SSHUser, sc.Run.Params.ConfigPath, sc.Index, sc.Step

	ats, _ := step.(*cuetry.AgentTransferStep)
	if ats == nil || ats.AgentTransfer == nil {
		return fmt.Errorf("step %d: internal: missing agent_transfer", i)
	}
	at := ats.AgentTransfer
	srcHosts, err := cuetry.ExpandStepHosts(step.Base().Host, records)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	dstHosts, err := cuetry.ExpandStepHosts(at.DestHost, records)
	if err != nil {
		return fmt.Errorf("step %d dest_host: %w", i, err)
	}
	if len(srcHosts) != 1 {
		return fmt.Errorf("step %d: agent_transfer requires exactly one source host; got %d", i, len(srcHosts))
	}
	if len(dstHosts) != 1 {
		return fmt.Errorf("step %d: agent_transfer requires exactly one destination host; got %d", i, len(dstHosts))
	}
	cloud := AgentTransferCloudFromRecipe(at.Cloud)
	_, _ = fmt.Fprintf(out, "step %d: kind=agent_transfer ssh_user=%q\n", i, strings.TrimSpace(sshUser))
	_, _ = fmt.Fprintf(out, "  source: name=%q %s provider=%s path=%q\n",
		srcHosts[0].Name, FormatTargetForDryRun(srcHosts[0]), srcHosts[0].Provider, strings.TrimSpace(at.SourcePath))
	_, _ = fmt.Fprintf(out, "  dest:   name=%q %s provider=%s path=%q\n",
		dstHosts[0].Name, FormatTargetForDryRun(dstHosts[0]), dstHosts[0].Provider, strings.TrimSpace(at.DestPath))
	_, _ = fmt.Fprintf(out, "  cloud: provider=%q bucket=%q prefix=%q object=%q region=%q endpoint=%q\n",
		cloud.Provider, cloud.Bucket, cloud.Prefix, cloud.Object, cloud.Region, cloud.Endpoint)
	if at.CloudBackendRef != nil {
		_, _ = fmt.Fprintf(out, "  cloud_backend_ref: kind=%q name=%q index=%v\n",
			at.CloudBackendRef.Kind, at.CloudBackendRef.Name, at.CloudBackendRef.Index)
		if _, err := ResolveAgentTransferSigningHints(configPath, cloud, CloudBackendRefFromRecipe(at.CloudBackendRef)); err != nil {
			return fmt.Errorf("step %d signing hints: %w", i, err)
		}
		_, _ = fmt.Fprintln(out, "  signing hints: resolvable (config + ref)")
	} else {
		_, _ = fmt.Fprintln(out, "  cloud_backend_ref: (none — empty signing hints)")
	}
	if at.KeepObject {
		_, _ = fmt.Fprintln(out, "  keep_object: true")
	}
	if at.MaxRetries > 0 {
		_, _ = fmt.Fprintf(out, "  max_retries: %d\n", at.MaxRetries)
	}
	if ard := strings.TrimSpace(at.AgentRemoteDir); ard != "" {
		_, _ = fmt.Fprintf(out, "  agent_remote_dir: %q\n", ard)
	}
	WriteCueStepNotifyDryLine(out, step)
	return nil
}

// ExecuteStream streams the step execution.
func (e *AgentTransferExecutor) ExecuteStream(sc *StepContext) error {
	run, ctx, i, step, ch := sc.Run, sc.Ctx, sc.Index, sc.Step, sc.ResultCh
	records, sshUser, configPath, cache := sc.Run.Params.Records, sc.Run.Params.SSHUser, sc.Run.Params.ConfigPath, sc.Run.Cache

	ats, _ := step.(*cuetry.AgentTransferStep)
	if ats == nil || ats.AgentTransfer == nil {
		return fmt.Errorf("step %d: internal: missing agent_transfer", i)
	}
	at := ats.AgentTransfer
	srcHosts, err := cuetry.ExpandStepHosts(step.Base().Host, records)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	dstHosts, err := cuetry.ExpandStepHosts(at.DestHost, records)
	if err != nil {
		return fmt.Errorf("step %d dest_host: %w", i, err)
	}
	if len(srcHosts) != 1 || len(dstHosts) != 1 {
		msg := fmt.Sprintf("need exactly one source and one dest host; got src=%d dst=%d", len(srcHosts), len(dstHosts))
		res := HostExecResult{
			Name:     fmt.Sprintf("Step %d | agent_transfer", i+1),
			Success:  false,
			ErrMsg:   msg,
			Provider: "local",
		}
		AnnotateCueStepResult(&res, i, step, cuetry.KindAgentTransfer)
		ch <- res
		return fmt.Errorf("step %d: %s", i, msg)
	}
	src := srcHosts[0]
	dst := dstHosts[0]

	stepStart := time.Now()
	kv := KvReaderFromCoordinator(run.RecipeKV)
	ok, err := EvalAgentTransferWhen(ctx, run.Params.Recipe, step, src, dst, run.OutputStore, run.Params.SecretResolver, kv, run.Params.CLIEnv, run.Params.Execute)
	if err != nil {
		return fmt.Errorf("step %d: %w", i, err)
	}
	if !ok {
		res := WhenSkippedResult(src)
		res.Name = fmt.Sprintf("Step %d | %s", i+1, res.Name)
		AnnotateCueStepResult(&res, i, step, cuetry.KindAgentTransfer)
		ch <- res
		ObserveRecipeStep(run.Params.Obs, cuetry.KindAgentTransfer, stepStart, []HostExecResult{res}, 1)
		return nil
	}
	cloud := AgentTransferCloudFromRecipe(at.Cloud)
	ref := CloudBackendRefFromRecipe(at.CloudBackendRef)
	hints, err := ResolveAgentTransferSigningHints(configPath, cloud, ref)
	if err != nil {
		res := HostExecResult{
			Name:     fmt.Sprintf("Step %d | agent_transfer %s → %s", i+1, strings.TrimSpace(src.Name), strings.TrimSpace(dst.Name)),
			IP:       strings.TrimSpace(src.PrimaryIP),
			Provider: src.Provider,
			Success:  false,
			ErrMsg:   err.Error(),
		}
		AnnotateCueStepResult(&res, i, step, cuetry.KindAgentTransfer)
		ch <- res
		return fmt.Errorf("step %d: %w", i, err)
	}
	events, err := RunAgentTransferWithFallback(ctx, cache, sshUser, "", "", "", strings.TrimSpace(at.AgentRemoteDir),
		src, dst, strings.TrimSpace(at.SourcePath), strings.TrimSpace(at.DestPath),
		cloud, at.KeepObject, at.MaxRetries, hints,
		LoadTransferConfigFromConfigPath(configPath), nil)
	outStr := SummarizeAgentTransferEvents(events)
	resName := fmt.Sprintf("agent_transfer %s → %s", strings.TrimSpace(src.Name), strings.TrimSpace(dst.Name))
	if err != nil {
		res := HostExecResult{
			Name:     fmt.Sprintf("Step %d | %s", i+1, resName),
			IP:       strings.TrimSpace(src.PrimaryIP),
			Provider: src.Provider,
			Success:  false,
			ErrMsg:   err.Error(),
			Output:   outStr,
		}
		AnnotateCueStepResult(&res, i, step, cuetry.KindAgentTransfer)
		ch <- res
		return fmt.Errorf("step %d agent_transfer: %w", i, err)
	}
	res := HostExecResult{
		Name:     fmt.Sprintf("Step %d | %s", i+1, resName),
		IP:       strings.TrimSpace(src.PrimaryIP),
		Provider: src.Provider,
		Success:  true,
		Output:   outStr,
	}
	AnnotateCueStepResult(&res, i, step, cuetry.KindAgentTransfer)
	ch <- res
	return nil
}
