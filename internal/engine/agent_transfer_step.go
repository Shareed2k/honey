package engine

import (
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// StreamCueStepAgentTransferWhen ...
func StreamCueStepAgentTransferWhen(ctx context.Context, run *CueRun, i int, step cuetry.Step) ([]HostExecResult, error) {
	ats, _ := step.(*cuetry.AgentTransferStep)
	if ats == nil || ats.AgentTransfer == nil {
		return nil, fmt.Errorf("step %d: internal: missing agent_transfer", i)
	}
	at := ats.AgentTransfer
	srcHosts, err := cuetry.ExpandStepHosts(step.Base().Host, run.Params.Records)
	if err != nil {
		return nil, fmt.Errorf("step %d: %w", i, err)
	}
	dstHosts, err := cuetry.ExpandStepHosts(at.DestHost, run.Params.Records)
	if err != nil {
		return nil, fmt.Errorf("step %d dest_host: %w", i, err)
	}
	if len(srcHosts) != 1 || len(dstHosts) != 1 {
		return StreamCueStepAgentTransfer(ctx, run.Params.Records, run.Params.SSHUser, run.Params.ConfigPath, i, step, run.Cache)
	}
	src := srcHosts[0]
	dst := dstHosts[0]
	kv := KvReaderFromCoordinator(run.RecipeKV)
	ok, err := EvalAgentTransferWhen(ctx, run.Params.Recipe, step, src, dst, run.OutputStore, run.Params.SecretResolver, kv, run.Params.CLIEnv, run.Params.Execute)
	if err != nil {
		return nil, fmt.Errorf("step %d: %w", i, err)
	}
	if !ok {
		res := WhenSkippedResult(src)
		res.Name = fmt.Sprintf("Step %d | %s", i+1, res.Name)
		return []HostExecResult{res}, nil
	}
	return StreamCueStepAgentTransfer(ctx, run.Params.Records, run.Params.SSHUser, run.Params.ConfigPath, i, step, run.Cache)
}

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

// RunCueStepAgentTransferDry ...
func RunCueStepAgentTransferDry(out io.Writer, records []hosts.Record, sshUser, configPath string, i int, step cuetry.Step) error {
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

// StreamCueStepAgentTransfer ...
func StreamCueStepAgentTransfer(ctx context.Context, records []hosts.Record, sshUser, configPath string, i int, step cuetry.Step, cache *ClientCache) ([]HostExecResult, error) {
	ats, _ := step.(*cuetry.AgentTransferStep)
	if ats == nil || ats.AgentTransfer == nil {
		return nil, fmt.Errorf("step %d: internal: missing agent_transfer", i)
	}
	at := ats.AgentTransfer
	srcHosts, err := cuetry.ExpandStepHosts(step.Base().Host, records)
	if err != nil {
		return nil, fmt.Errorf("step %d: %w", i, err)
	}
	dstHosts, err := cuetry.ExpandStepHosts(at.DestHost, records)
	if err != nil {
		return nil, fmt.Errorf("step %d dest_host: %w", i, err)
	}
	if len(srcHosts) != 1 || len(dstHosts) != 1 {
		msg := fmt.Sprintf("need exactly one source and one dest host; got src=%d dst=%d", len(srcHosts), len(dstHosts))
		res := HostExecResult{
			Name:     fmt.Sprintf("Step %d | agent_transfer", i+1),
			Success:  false,
			ErrMsg:   msg,
			Provider: "local",
		}
		return []HostExecResult{res}, fmt.Errorf("step %d: %s", i, msg)
	}
	src := srcHosts[0]
	dst := dstHosts[0]
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
		return []HostExecResult{res}, fmt.Errorf("step %d: %w", i, err)
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
		return []HostExecResult{res}, fmt.Errorf("step %d agent_transfer: %w", i, err)
	}
	res := HostExecResult{
		Name:     fmt.Sprintf("Step %d | %s", i+1, resName),
		IP:       strings.TrimSpace(src.PrimaryIP),
		Provider: src.Provider,
		Success:  true,
		Output:   outStr,
	}
	return []HostExecResult{res}, nil
}
