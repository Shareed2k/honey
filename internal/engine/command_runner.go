package engine

import (
	"context"
	"fmt"
	"time"

	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/metrics"
	"github.com/shareed2k/honey/internal/searchrun"
)

// CommandRunnerOptions configures a CommandRunner. All fields are injected at
// construction.
type CommandRunnerOptions struct {
	ExecRegistry   hostexec.Registry
	SearchRegistry *searchrun.Registry // required for host resolution
	Metrics        metrics.Observer
	RecordDir      string // "" disables session recording
}

// CommandRunRequest is the high-level input for executing ad-hoc commands or scripts.
type CommandRunRequest struct {
	Command        string
	IsScript       bool
	FileExtension  string
	ScriptOpts     ScriptUploadRunOptions
	Target         *hostapi.SearchHostsInput
	Records        []hosts.Record // bypasses Target resolution if populated
	SSHUser        string
	ActorID        string
	AISystemPrompt string
	RecordSession  bool
	RecordLabel    string
	CmdTimeout     time.Duration
	MaxOutputBytes int
}

// CommandRunner owns the full ad-hoc remote execution lifecycle: target resolution,
// session recording, and streaming execution.
type CommandRunner struct {
	opts CommandRunnerOptions
}

// NewCommandRunner builds a CommandRunner from injected dependencies.
func NewCommandRunner(opts CommandRunnerOptions) *CommandRunner {
	return &CommandRunner{opts: opts}
}

// resolveTargets resolves hosts via SearchHosts if req.Records is empty and req.Target is provided.
func (r *CommandRunner) resolveTargets(ctx context.Context, req *CommandRunRequest) error {
	if len(req.Records) > 0 || req.Target == nil {
		return nil
	}
	if r.opts.SearchRegistry == nil {
		return fmt.Errorf("SearchRegistry is required for target resolution")
	}
	out, err := hostapi.SearchHosts(ctx, req.Target, r.opts.ExecRegistry, r.opts.SearchRegistry)
	if err != nil {
		return fmt.Errorf("search hosts: %w", err)
	}
	if len(out.Records) == 0 {
		return fmt.Errorf("no target hosts found")
	}
	req.Records = out.Records
	return nil
}

// Execute runs the command or script and streams the results.
// It manages the session recording internally if enabled.
func (r *CommandRunner) Execute(ctx context.Context, req CommandRunRequest) (<-chan HostExecResult, error) {
	if err := r.resolveTargets(ctx, &req); err != nil {
		return nil, err
	}

	if len(req.Records) == 0 {
		return nil, fmt.Errorf("no target hosts found")
	}

	var jobs []hosts.Record
	var unconnectable []HostExecResult

	for _, rec := range req.Records {
		if rec.IsConnectable() {
			jobs = append(jobs, rec)
		} else {
			unconnectable = append(unconnectable, HostExecResult{
				Name:     rec.Name,
				IP:       rec.PrimaryIP,
				Provider: rec.Provider,
				Success:  false,
				Skipped:  true,
				ErrMsg:   "not connectable via SSH",
			})
		}
	}

	if !req.IsScript && len(jobs) == 0 {
		return nil, fmt.Errorf("no connectable hosts in selection (check proxy/tunnel configurations)")
	}

	recordJobCount := len(jobs)
	if req.IsScript {
		recordJobCount = len(req.Records)
	}

	var rec *SessionRecorder
	wantRec := req.RecordSession && r.opts.RecordDir != ""
	if wantRec {
		label := req.RecordLabel
		if label == "" {
			label = "cmd-exec"
		}
		var err error
		rec, err = NewBatchSessionRecorder(r.opts.RecordDir, label, req.SSHUser, recordJobCount)
		if err != nil {
			return nil, fmt.Errorf("session recorder: %w", err)
		}
	}

	ch := make(chan HostExecResult, len(req.Records))
	execStart := time.Now()

	go func() {
		defer func() {
			if rec != nil {
				_ = rec.Close()
			}
			close(ch)
		}()

		if req.IsScript {
			for _, res := range unconnectable {
				if rec != nil {
					rec.RecordHostExecResult(res)
				}
				ch <- res
			}
			if len(jobs) == 0 {
				return
			}
			var tcJobs []TargetContext
			for _, j := range jobs {
				tcJobs = append(tcJobs, TargetContext{Record: j})
			}

			inner := make(chan HostExecResult, len(jobs))
			go func() {
				defer close(inner)
				if err := StreamScriptContentRunParallel(ctx, req.SSHUser, tcJobs, req.Command, req.FileExtension, req.ScriptOpts, inner, BatchOptions{Obs: r.opts.Metrics, Reg: r.opts.ExecRegistry, CmdTimeout: req.CmdTimeout, MaxOutputBytes: req.MaxOutputBytes}); err != nil {
					inner <- HostExecResult{Name: req.RecordLabel, Provider: "engine", Success: false, ErrMsg: err.Error()}
				}
			}()

			for res := range inner {
				if rec != nil {
					rec.RecordHostExecResult(res)
				}
				ch <- res
			}
			if metrics.ObserverEnabled(r.opts.Metrics) {
				r.opts.Metrics.ObserveExecCommand("ok", recordJobCount, time.Since(execStart))
			}
			return
		}

		var tcJobs []TargetContext
		for _, j := range jobs {
			tcJobs = append(tcJobs, TargetContext{Record: j})
		}

		inner := make(chan HostExecResult, len(jobs))
		go func() {
			defer close(inner)
			_ = StreamSSHParallel(ctx, req.SSHUser, tcJobs, false, func(_ TargetContext, _ map[string]string) string { return req.Command }, inner, BatchOptions{Obs: r.opts.Metrics, Reg: r.opts.ExecRegistry, CmdTimeout: req.CmdTimeout, MaxOutputBytes: req.MaxOutputBytes})
		}()

		for res := range inner {
			if rec != nil {
				rec.RecordHostExecResult(res)
			}
			ch <- res
		}
		if metrics.ObserverEnabled(r.opts.Metrics) {
			r.opts.Metrics.ObserveExecCommand("ok", recordJobCount, time.Since(execStart))
		}
	}()

	return ch, nil
}

// ExecuteAndWait runs the command to completion and discards the streamed host
// results. For callers that only need side effects (like recording) but not the stream.
func (r *CommandRunner) ExecuteAndWait(ctx context.Context, req CommandRunRequest) ([]HostExecResult, error) {
	ch, err := r.Execute(ctx, req)
	if err != nil {
		return nil, err
	}
	var results []HostExecResult
	var runErr error
	for res := range ch {
		results = append(results, res)
		if res.Provider == "engine" && res.Name == req.RecordLabel && !res.Success {
			runErr = fmt.Errorf("command run failed: %s", res.ErrMsg)
		}
	}
	return results, runErr
}
