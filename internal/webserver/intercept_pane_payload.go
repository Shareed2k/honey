package webserver

import "github.com/shareed2k/honey/internal/hosts"

// InterceptPaneRequest is the base64(JSON) payload the web handler hands the
// hidden `honey intercept-pane` subcommand that runs one interception inside a
// tmux pane. It carries NO secrets: the agent image, policy directory, and
// kubeconfig resolution all come from the pane's own --config, never the
// payload — so the encoded argv is safe to pass on a command line and to log.
type InterceptPaneRequest struct {
	// Record is the target host record; it must be a Kubernetes pod (IsPod).
	Record hosts.Record `json:"record"`
	// Modes lists the interception modes to enable (egress|incoming|files|env).
	Modes []string `json:"modes"`
	// UDP includes the UDP tunnels alongside TCP.
	UDP bool `json:"udp"`
	// Command is the local command run under injection; empty ⇒ a shell.
	Command []string `json:"command,omitempty"`
	// Cols/Rows are the initial terminal size in character cells; the pane's
	// SIGWINCH handler supersedes them once the real tty size is known.
	Cols int `json:"cols"`
	Rows int `json:"rows"`
}
