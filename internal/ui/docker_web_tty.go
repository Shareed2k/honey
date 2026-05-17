package ui

import (
	"context"
	"fmt"
	"io"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// DockerTerminalSize is a cols/rows pair for docker exec resize.
type DockerTerminalSize struct {
	Cols int
	Rows int
}

// RunDockerWebTTY runs an interactive shell in a container with TTY over stdin/stdout.
func RunDockerWebTTY(
	ctx context.Context,
	user string,
	r hosts.Record,
	stdin io.Reader,
	stdout io.Writer,
	cols, rows int,
	resizeCh <-chan DockerTerminalSize,
) error {
	client, err := dockerExecutor{}.Dial(user, r)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	dc, ok := client.(*dockerNativeClient)
	if !ok {
		return fmt.Errorf("unexpected client type %T", client)
	}

	env, _ := cuetry.EffectiveEnvForRun(context.Background(), false, nil, cuetry.RecipeStep{}, nil, nil, &r)
	shell := "sh"
	if dc.isWindowsContainer() {
		shell = "powershell.exe"
	}
	cmd, _ := cuetry.ShellExportPrefixForRemote(env, shell)
	return dc.execInteractive(ctx, dc.execArgv(cmd), stdin, stdout, cols, rows, resizeCh)
}
