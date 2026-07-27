package ui

import (
	"context"
	"fmt"
	"io"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// RunDockerWebTTY runs an interactive shell in a container with TTY over
// stdin/stdout. It routes through the executor seam (Registry.ForRecord +
// hostexec.InteractiveStreamer) instead of dialing and down-casting to a
// concrete native client, so a container reached over the honey mesh is proxied
// by honeyprovider rather than failing with "unexpected client type". resize
// carries [cols, rows] pairs.
func RunDockerWebTTY(
	ctx context.Context,
	user string,
	r hosts.Record,
	stdin io.Reader,
	stdout io.Writer,
	cols, rows int,
	resize <-chan [2]int,
	reg hostexec.Registry,
) error {
	is, ok := reg.ForRecord(r).(hostexec.InteractiveStreamer)
	if !ok {
		return fmt.Errorf("no interactive terminal available for record %q", r.Name)
	}
	return is.RunInteractiveStreams(ctx, user, r, stdin, stdout, cols, rows, resize)
}
