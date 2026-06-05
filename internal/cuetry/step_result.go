package cuetry

import "strings"

// HostStepResult is the per-host outcome of a completed or skipped step.
type HostStepResult struct {
	Succeeded bool
	Skipped   bool
	ExitCode  int
	Stdout    string
}

// StepView is the CEL-facing view of a prior step for one host.
type StepView struct {
	Succeeded bool
	Skipped   bool
	Stdout    string
	ExitCode  int
}

// StepResultStore holds per-step per-host results (stdout, success, skip, exit code).
type StepResultStore struct {
	byStep map[string]map[string]HostStepResult
}

// NewStepResultStore creates an empty result store.
func NewStepResultStore() *StepResultStore {
	return &StepResultStore{byStep: make(map[string]map[string]HostStepResult)}
}

// NewStepOutputStore is an alias for backward compatibility with env_from capture.
func NewStepOutputStore() *StepResultStore {
	return NewStepResultStore()
}

// StepOutputStore is the historical name for StepResultStore.
type StepOutputStore = StepResultStore

// Record stores trimmed stdout for a host after a successful capture step.
func (s *StepResultStore) Record(stepID, hostName, stdout string) {
	s.RecordHost(stepID, hostName, HostStepResult{
		Succeeded: true,
		Stdout:    stdout,
	})
}

// RecordHost stores a full host result for a step id.
func (s *StepResultStore) RecordHost(stepID, hostName string, r HostStepResult) {
	if s == nil {
		return
	}
	stepID = strings.TrimSpace(stepID)
	hostName = strings.TrimSpace(hostName)
	if stepID == "" || hostName == "" {
		return
	}
	out := strings.TrimSpace(r.Stdout)
	if len(out) > maxStepOutputBytes {
		out = out[:maxStepOutputBytes]
	}
	r.Stdout = out
	if s.byStep[stepID] == nil {
		s.byStep[stepID] = make(map[string]HostStepResult)
	}
	s.byStep[stepID][hostName] = r
}

// FirstStdout returns the first non-empty stdout captured for stepID across any host.
func (s *StepResultStore) FirstStdout(stepID string) (string, bool) {
	if s == nil || s.byStep == nil {
		return "", false
	}
	m := s.byStep[strings.TrimSpace(stepID)]
	if m == nil {
		return "", false
	}
	for _, r := range m {
		if strings.TrimSpace(r.Stdout) != "" {
			return r.Stdout, true
		}
	}
	return "", false
}

// Get returns captured stdout for stepID and hostName.
func (s *StepResultStore) Get(stepID, hostName string) (string, bool) {
	if s == nil || s.byStep == nil {
		return "", false
	}
	m := s.byStep[strings.TrimSpace(stepID)]
	if m == nil {
		return "", false
	}
	v, ok := m[strings.TrimSpace(hostName)]
	if !ok {
		return "", false
	}
	return v.Stdout, true
}

// HostResult returns the full result for stepID and hostName.
func (s *StepResultStore) HostResult(stepID, hostName string) (HostStepResult, bool) {
	if s == nil || s.byStep == nil {
		return HostStepResult{}, false
	}
	m := s.byStep[strings.TrimSpace(stepID)]
	if m == nil {
		return HostStepResult{}, false
	}
	v, ok := m[strings.TrimSpace(hostName)]
	return v, ok
}

// StepsTemplateData builds a template-facing aggregate view of prior step results.
func (s *StepResultStore) StepsTemplateData() map[string]any {
	out := make(map[string]any)
	if s == nil || s.byStep == nil {
		return out
	}
	for stepID, hosts := range s.byStep {
		var view HostStepResult
		for _, r := range hosts {
			if r.Succeeded && strings.TrimSpace(r.Stdout) != "" {
				view = r
				break
			}
			if view.Stdout == "" {
				view = r
			}
		}
		stdout := strings.TrimSpace(view.Stdout)
		out[stepID] = map[string]any{
			"succeeded":    view.Succeeded,
			"skipped":      view.Skipped,
			"exit_code":    view.ExitCode,
			"stdout":       stdout,
			"stdout_lines": strings.Split(stdout, "\n"),
		}
	}
	return out
}

// StepsViewAggregated builds a per-step view across all hosts (any succeeded, first stdout).
func (s *StepResultStore) StepsViewAggregated() map[string]StepView {
	out := make(map[string]StepView)
	if s == nil || s.byStep == nil {
		return out
	}
	for stepID, hosts := range s.byStep {
		var v StepView
		allSkipped := len(hosts) > 0
		for _, r := range hosts {
			if r.Succeeded {
				v.Succeeded = true
				if v.Stdout == "" {
					v.Stdout = r.Stdout
				}
			}
			if !r.Skipped {
				allSkipped = false
			}
			if r.ExitCode != 0 && !r.Skipped {
				v.ExitCode = r.ExitCode
			}
		}
		if allSkipped && len(hosts) > 0 {
			v.Skipped = true
		}
		out[stepID] = v
	}
	return out
}

// StepsViewForHost builds the steps map for CEL for one host name.
func (s *StepResultStore) StepsViewForHost(hostName string) map[string]StepView {
	out := make(map[string]StepView)
	if s == nil || s.byStep == nil {
		return out
	}
	hostName = strings.TrimSpace(hostName)
	for stepID, hosts := range s.byStep {
		if r, ok := hosts[hostName]; ok {
			out[stepID] = StepView{
				Succeeded: r.Succeeded,
				Skipped:   r.Skipped,
				Stdout:    r.Stdout,
				ExitCode:  r.ExitCode,
			}
		}
	}
	return out
}
