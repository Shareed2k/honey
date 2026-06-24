package hostexec

import (
	"context"
	"io"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
)

// Registry handles resolving and dispatching host execution.
type Registry interface {
	ForRecord(r hosts.Record) Executor
	Reconfigure(cfg *config.File)
	RunSSHTunnel(ctx context.Context, user, host string, sshPort int, localFwd string, out io.Writer) error
	BorrowSSH(user string, hop hosts.Record) (any, bool)
}
