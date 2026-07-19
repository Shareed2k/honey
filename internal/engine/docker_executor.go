package engine

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
)

// dockerInteractiveRunner is the ui implementation of dockerprovider.InteractiveRunner,
// injected into the docker provider factory by the composition root.
type dockerInteractiveRunner struct{}

func (dockerInteractiveRunner) RunInteractive(user string, r hosts.Record, reg hostexec.Registry) error {
	return RunDockerInteractiveWithRecorder(user, r, nil, reg)
}

// DockerInteractiveRunner returns the ui-backed docker interactive session runner.
// DockerInteractiveRunner ...
func DockerInteractiveRunner() dockerprovider.InteractiveRunner { return dockerInteractiveRunner{} }

// DialDockerCheck verifies that a docker record can reach the Engine API (dial + close).
// Re-exported from dockerprovider for callers that already import ui.
// DialDockerCheck ...
func DialDockerCheck(user string, r hosts.Record, reg hostexec.Registry) error {
	return dockerprovider.DialDockerCheck(user, r, reg)
}

// RunDockerInteractiveWithRecorder ...
func RunDockerInteractiveWithRecorder(user string, r hosts.Record, recorder *SessionRecorder, reg hostexec.Registry) error {
	// A record proxied through a honey upstream backend runs on the upstream
	// server: delegate to the honeyprovider Executor's interactive proxy, which
	// forwards the record over /ws/ssh so the server runs docker exec. Without
	// this, reg.ForRecord returns the honeyprovider client and the
	// *DockerNativeClient assertion below fails with "unexpected client type
	// *honeyprovider.Client".
	if reg != nil && r.Meta["honey_upstream_backend"] != "" {
		if ex := reg.ForRecord(r); ex != nil {
			return ex.RunInteractive(user, r)
		}
	}

	client, err := reg.ForRecord(r).Dial(user, r)
	if err != nil {
		if recorder != nil {
			recorder.RecordError(err)
		}
		return err
	}
	defer func() { _ = client.Close() }()

	dc, ok := client.(*DockerNativeClient)
	if !ok {
		err := fmt.Errorf("unexpected client type %T", client)
		if recorder != nil {
			recorder.RecordError(err)
		}
		return err
	}

	fd := int(os.Stdin.Fd())
	if !termIsTerminal(fd) {
		err := fmt.Errorf("stdin is not a terminal")
		if recorder != nil {
			recorder.RecordError(err)
		}
		return err
	}
	oldState, err := termMakeRaw(fd)
	if err != nil {
		if recorder != nil {
			recorder.RecordError(err)
		}
		return err
	}
	defer func() { _ = termRestore(fd, oldState) }()

	execEnv, _ := cuetry.EnvForDockerInteractive(&r)

	var stdin io.Reader = os.Stdin
	var stdout io.Writer = os.Stdout
	if recorder != nil {
		stdin = WrapRecordingReader(os.Stdin, recorder, "stdin")
		stdout = WrapRecordingWriter(os.Stdout, recorder, "stdout")
	}
	execErr := dc.ExecInteractive(context.Background(), dockerprovider.DockerInteractiveShellCmd(dc), execEnv, stdin, stdout, 0, 0, nil)
	if execErr != nil && recorder != nil {
		recorder.RecordError(execErr)
	}
	return execErr
}
