package engine

import "github.com/shareed2k/honey/internal/hosts"

// HostExecResult is the outcome of one host execution step.
// HostExecResult ...
type HostExecResult struct {
	Name          string
	IP            string
	Provider      string
	Success       bool
	Skipped       bool
	Changed       bool
	ExitCode      int
	Output        string
	OutputCapture string
	// KVCaptureKey is set when a step wrote its output to the recipe KV store
	// (e.g. plugin.kv_key) instead of a named output capture. Unlike
	// OutputCapture, run.go's graph dispatch never overwrites this field, so
	// it survives to CueRecipeDisplayOutput to suppress the raw dump.
	KVCaptureKey string
	ErrMsg       string
	IsTransient  bool

	StepIndex int    `json:",omitempty"`
	StepID    string `json:",omitempty"`
	StepKind  string `json:",omitempty"`

	HookPhase  string
	HookOutput string
	HookFailed bool
}

// SFTPDownloadJob pairs a target host with a remote and local path for downloading.
// SFTPDownloadJob ...
type SFTPDownloadJob struct {
	Record    hosts.Record
	RemoteAbs string
	LocalAbs  string
}
