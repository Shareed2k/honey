package plugins

import (
	"context"

	extism "github.com/extism/go-sdk"
)

// pluginTransport is the seam between Manager.Call's JSON-envelope handling
// (timeout, apiv1.PluginError check, JSON decode) and how a specific plugin
// runtime actually executes a call. extismTransport is today's WASM path,
// unchanged. dockerTransport (docker_transport.go) drives a container's
// honey-plugin-init sidecar over HTTP.
type pluginTransport interface {
	CallRaw(ctx context.Context, export string, inBytes []byte) (exitCode int, outBytes []byte, err error)
	Close(ctx context.Context) error
}

// extismTransport wraps an *extism.Plugin — byte-for-byte today's behavior.
type extismTransport struct {
	plugin *extism.Plugin
}

func (t *extismTransport) CallRaw(ctx context.Context, export string, inBytes []byte) (int, []byte, error) {
	exit, out, err := t.plugin.CallWithContext(ctx, export, inBytes)
	return int(exit), out, err
}

func (t *extismTransport) Close(ctx context.Context) error {
	return t.plugin.Close(ctx)
}
