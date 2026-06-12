package ui

import (
	"context"
	"fmt"
	"os"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/k8sprovider"
)

// k8sInteractiveRunner is the ui implementation of k8sprovider.InteractiveRunner,
// injected into the k8s provider factory by the composition root.
type k8sInteractiveRunner struct{}

func (k8sInteractiveRunner) RunInteractive(user string, r hosts.Record) error {
	return runK8sInteractiveWithRecorder(user, r, nil)
}

// K8sInteractiveRunner returns the ui-backed k8s interactive session runner.
func K8sInteractiveRunner() k8sprovider.InteractiveRunner { return k8sInteractiveRunner{} }

func runK8sInteractiveWithRecorder(user string, r hosts.Record, recorder *SessionRecorder) error {
	client, err := (&k8sPodExecutor{}).Dial(user, r)
	if err != nil {
		if recorder != nil {
			recorder.RecordError(err)
		}
		return err
	}
	defer func() { _ = client.Close() }()

	podClient, ok := client.(*k8sNativeClient)
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

	// Inject host environment variables into the interactive shell session
	env, _ := cuetry.EffectiveEnvForRun(context.Background(), false, nil, &cuetry.StepBase{}, nil, nil, &r)
	cmd, _ := cuetry.ShellExportPrefixForRemote(env, "sh")

	// Start standard sh for interactive session
	stdin := WrapRecordingReader(os.Stdin, recorder, "stdin")
	stdout := WrapRecordingWriter(os.Stdout, recorder, "stdout")
	stderr := WrapRecordingWriter(os.Stderr, recorder, "stderr")
	execErr := podClient.ExecInPod(context.Background(), []string{"sh", "-c", cmd}, stdin, stdout, stderr, true, nil)
	if execErr != nil && recorder != nil {
		recorder.RecordError(execErr)
	}
	return execErr
}
