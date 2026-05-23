package plugins

import (
	"context"
	"encoding/json"
	"fmt"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

const maxRemoteOutput = 8192

func truncateRemoteOutput(s string) string {
	if len(s) <= maxRemoteOutput {
		return s
	}
	return s[:maxRemoteOutput]
}

func remoteHostCtx(ctx context.Context) (*HostRunContext, error) {
	h, ok := HostRunContextFromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("host run context not available for this call")
	}
	return h, nil
}

func dryRunPlan(prefix string, h *HostRunContext) string {
	return fmt.Sprintf("%s on %s", prefix, h.Record.Name)
}

func parseRemoteInput[T any](raw string, out *T) error {
	if err := json.Unmarshal([]byte(raw), out); err != nil {
		return fmt.Errorf("parse input: %w", err)
	}
	return nil
}

// RunRemoteExecForTest exposes remote_exec handling for unit tests.
func RunRemoteExecForTest(ctx context.Context, in apiv1.RemoteExecInput) apiv1.RemoteExecOutput {
	return runRemoteExec(ctx, in)
}

// RunRemoteUploadForTest exposes remote_upload handling for unit tests.
func RunRemoteUploadForTest(ctx context.Context, in apiv1.RemoteUploadInput) apiv1.RemoteUploadOutput {
	return runRemoteUpload(ctx, in)
}

// RunRemoteStatForTest exposes remote_stat handling for unit tests.
func RunRemoteStatForTest(ctx context.Context, in apiv1.RemoteStatInput) apiv1.RemoteStatOutput {
	return runRemoteStat(ctx, in)
}

// RunTemplateRenderForTest exposes template_render handling for unit tests.
func RunTemplateRenderForTest(ctx context.Context, in apiv1.TemplateRenderInput) apiv1.TemplateRenderOutput {
	return runTemplateRender(ctx, in)
}
