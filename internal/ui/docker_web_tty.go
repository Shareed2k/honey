package ui

import (
	"context"
	"fmt"
	"io"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/provider/dockerprovider"
)

// DockerTerminalSize is a cols/rows pair for docker exec resize.
// It is an alias for dockerprovider.DockerTerminalSize.
type DockerTerminalSize = dockerprovider.DockerTerminalSize

// RunDockerWebTTY runs an interactive shell in a container with TTY over stdin/stdout.
func RunDockerWebTTY(
	ctx context.Context,
	user string,
	r hosts.Record,
	stdin io.Reader,
	stdout io.Writer,
	cols, rows int,
	resizeCh <-chan DockerTerminalSize,
	reg hostexec.Registry,
) error {
	client, err := reg.ForRecord(r).Dial(user, r)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	dc, ok := client.(*engine.DockerNativeClient)
	if !ok {
		return fmt.Errorf("unexpected client type %T", client)
	}

	execEnv, _ := cuetry.EnvForDockerInteractive(&r)
	return dc.ExecInteractive(ctx, dockerprovider.DockerInteractiveShellCmd(dc), execEnv, stdin, stdout, cols, rows, resizeCh)
}
