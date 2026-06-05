//go:build wasip1 || wasm

package pluginpdk

//go:wasmimport extism:host/user host_exec
func hostExecImport(inputOffset uint64) uint64

// HostExecInput is sent to the host_exec host function (argv-only, no shell).
type HostExecInput struct {
	Argv      []string `json:"argv"`
	Cwd       string   `json:"cwd,omitempty"`
	TimeoutMS int      `json:"timeout_ms,omitempty"`
}

// HostExecOutput is returned from the host_exec host function.
type HostExecOutput struct {
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Error    string `json:"error,omitempty"`
}

// HostExec runs argv on the operator machine via the host_exec host function.
// Requires allow_host_exec: true in plugin.yaml.
func HostExec(argv []string, cwd string, timeoutMS int) (HostExecOutput, error) {
	return callRemote[HostExecOutput](hostExecImport, HostExecInput{
		Argv:      argv,
		Cwd:       cwd,
		TimeoutMS: timeoutMS,
	})
}
