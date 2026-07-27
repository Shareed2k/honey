package ui

import (
	"context"
	"fmt"
	"io"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
)

// RunK8sPodWebTTY runs an interactive shell in a Kubernetes pod with TTY over
// stdin/stdout. It routes through the executor seam (Registry.ForRecord +
// hostexec.InteractiveStreamer) instead of constructing a native pod executor
// and down-casting, so a pod reached over the honey mesh is proxied by
// honeyprovider rather than failing with "unexpected client type". resize
// carries [cols, rows] pairs; under a TTY stdout and stderr are one stream.
func RunK8sPodWebTTY(
	ctx context.Context,
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
	return is.RunInteractiveStreams(ctx, "", r, stdin, stdout, cols, rows, resize)
}
