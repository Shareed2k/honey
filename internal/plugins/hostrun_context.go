package plugins

import (
	"context"

	"github.com/shareed2k/honey/internal/hosts"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

type hostRunContextKey struct{}

// RemoteBridge performs SSH/SFTP operations on behalf of a WASM plugin for one host.
type RemoteBridge interface {
	RemoteExec(ctx context.Context, in apiv1.RemoteExecInput) apiv1.RemoteExecOutput
	RemoteUpload(ctx context.Context, in apiv1.RemoteUploadInput) apiv1.RemoteUploadOutput
	RemoteDownload(ctx context.Context, in apiv1.RemoteDownloadInput) apiv1.RemoteDownloadOutput
	RemoteStat(ctx context.Context, in apiv1.RemoteStatInput) apiv1.RemoteStatOutput
	TemplateRender(ctx context.Context, in apiv1.TemplateRenderInput) apiv1.TemplateRenderOutput
}

// PostgresBridge performs Postgres operations on the operator via pgx.
type PostgresBridge interface {
	Query(ctx context.Context, in apiv1.PostgresSQLInput) apiv1.PostgresOutput
	Exec(ctx context.Context, in apiv1.PostgresSQLInput) apiv1.PostgresOutput
	Migrate(ctx context.Context, in apiv1.PostgresMigrateInput) apiv1.PostgresOutput
}

// TunnelCoordinator resolves recipe tunnel step endpoints for DSN rewrite.
type TunnelCoordinator interface {
	LookupEndpoint(stepID, sshUser string, record hosts.Record) (host string, port int, ok bool)
}

// HostRunContext carries per-host recipe execution state for plugin host functions.
type HostRunContext struct {
	SSHUser              string
	Record               hosts.Record
	RecipeDir            string
	Execute              bool
	SecretsDry           bool
	RunAs                string
	Env                  map[string]string
	Bridge               RemoteBridge
	Postgres             PostgresBridge
	TunnelCoord          TunnelCoordinator
	AllowedPaths         map[string]string
	RecipeSecrets        map[string]string
	ResolveSecret        SecretResolveFunc
	PluginID             string
	MaxPostgresTimeoutMS int
}

// WithHostRunContext attaches host execution context for plugin remote host functions.
func WithHostRunContext(ctx context.Context, h *HostRunContext) context.Context {
	if h == nil {
		return ctx
	}
	return context.WithValue(ctx, hostRunContextKey{}, h)
}

// HostRunContextFromContext returns the host run context for this plugin call, if any.
func HostRunContextFromContext(ctx context.Context) (*HostRunContext, bool) {
	if ctx == nil {
		return nil, false
	}
	h, ok := ctx.Value(hostRunContextKey{}).(*HostRunContext)
	return h, ok && h != nil
}
