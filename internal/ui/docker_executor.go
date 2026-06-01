package ui

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

func init() {
	dockerprovider.SetRunInteractive(func(user string, r hosts.Record, reg hostexec.Registry) error {
		return runDockerInteractiveWithRecorder(user, r, nil, reg)
	})
}

// DialDockerCheck verifies that a docker record can reach the Engine API (dial + close).
// Re-exported from dockerprovider for callers that already import ui.
func DialDockerCheck(user string, r hosts.Record, reg hostexec.Registry) error {
	return dockerprovider.DialDockerCheck(user, r, reg)
}

func runDockerInteractiveWithRecorder(user string, r hosts.Record, recorder *SessionRecorder, reg hostexec.Registry) error {
	client, err := reg.ForRecord(r).Dial(user, r)
	if err != nil {
		if recorder != nil {
			recorder.RecordError(err)
		}
		return err
	}
	defer func() { _ = client.Close() }()

	dc, ok := client.(*dockerNativeClient)
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
