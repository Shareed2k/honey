package intercept

import (
	"context"

	"github.com/shareed2k/mogate/pkg/local"
)

// defaultRunner is the production LocalRunner: it delegates to the data-plane
// dependency's Run. It is the single seam through which a real interception
// invokes the local injection session.
type defaultRunner struct{}

// Run executes command under the local injection session described by cfg.
func (defaultRunner) Run(ctx context.Context, cfg local.Config, command []string) error {
	return local.Run(ctx, cfg, command)
}

// DefaultLocalRunner returns the production LocalRunner used by honey intercept.
// Tests substitute their own LocalRunner instead.
func DefaultLocalRunner() LocalRunner {
	return defaultRunner{}
}
