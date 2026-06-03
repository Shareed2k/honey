package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/ui"
)

type execOnHostInput struct {
	// Host is the IP address or hostname to connect to directly via SSH.
	// Use the primary_ip or name from a prior search_hosts call.
	Host       string `json:"host"                  mod:"trim"           validate:"required"`
	Name       string `json:"name,omitempty"        mod:"trim"`
	Command    string `json:"command"               mod:"trim"           validate:"required"`
	Shell      string `json:"shell,omitempty"       mod:"trim,lcase"     validate:"omitempty,oneof=bash sh"`
	TimeoutSec int    `json:"timeout_sec,omitempty"                      validate:"min=0,max=3600"`
}

type execOnHostOutput struct {
	Results []execHostResult `json:"results"`
}

type execHostResult struct {
	Host     string `json:"host"`
	IP       string `json:"ip"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
	Error    string `json:"error,omitempty"`
}

func handleExecOnHost(ctx context.Context, _ *mcp.CallToolRequest, in execOnHostInput) (*mcp.CallToolResult, execOnHostOutput, error) {
	if err := conform.Struct(ctx, &in); err != nil {
		return nil, execOnHostOutput{}, err
	}
	if err := validate.Struct(in); err != nil {
		return nil, execOnHostOutput{}, err
	}

	name := in.Name
	if name == "" {
		name = in.Host
	}
	record := hosts.Record{Name: name, PrimaryIP: in.Host}

	cmd := buildShellCmd(in.Command, in.Shell)
	recordDir := ""
	if serverCfg != nil {
		recordDir = serverCfg.Defaults.RecordDir
	}

	rawResults, err := ui.ExecuteSSHParallel("", []hosts.Record{record}, func(_ hosts.Record) string { return cmd }, 8, nil)
	if err != nil {
		return nil, execOnHostOutput{}, fmt.Errorf("ssh exec: %w", err)
	}

	out := execOnHostOutput{Results: make([]execHostResult, 0, len(rawResults))}
	for _, r := range rawResults {
		res := execHostResult{
			Host:     r.Name,
			IP:       r.IP,
			Output:   r.Output,
			ExitCode: r.ExitCode,
		}
		if r.ErrMsg != "" {
			res.Error = r.ErrMsg
		}
		out.Results = append(out.Results, res)

		// Session recording: if record_dir configured, write a recording file per host.
		if recordDir != "" {
			recordExecResult(recordDir, r)
		}
	}
	return nil, out, nil
}

// buildShellCmd wraps the command with an explicit interpreter when shell is set.
func buildShellCmd(cmd, shell string) string {
	switch strings.ToLower(strings.TrimSpace(shell)) {
	case "bash":
		var b bytes.Buffer
		b.WriteString("bash -c ")
		b.WriteString(shellQuote(cmd))
		return b.String()
	case "sh":
		var b bytes.Buffer
		b.WriteString("sh -c ")
		b.WriteString(shellQuote(cmd))
		return b.String()
	default:
		return cmd
	}
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}

func recordExecResult(recordDir string, r ui.HostExecResult) {
	rec, err := ui.NewSessionRecorder(ui.SessionRecorderOptions{
		Dir:      recordDir,
		Trigger:  "mcp",
		Mode:     "exec",
		HostName: r.Name,
		HostIP:   r.IP,
	})
	if err != nil {
		return
	}
	rec.RecordHostExecResult(r)
	_ = rec.Close()
}
