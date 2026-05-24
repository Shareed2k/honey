package plugins

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	extism "github.com/extism/go-sdk"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
	"github.com/shareed2k/honey/internal/safepath"
)

func remoteUploadCallback(_ string) extism.HostFunctionStackCallback {
	return func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
		stack[0] = writeRemoteJSON(p, runRemoteUploadFromHost(ctx, readHostInput(p, stack)))
	}
}

func remoteDownloadCallback(_ string) extism.HostFunctionStackCallback {
	return func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
		stack[0] = writeRemoteJSON(p, runRemoteDownloadFromHost(ctx, readHostInput(p, stack)))
	}
}

func remoteStatCallback(_ string) extism.HostFunctionStackCallback {
	return func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
		stack[0] = writeRemoteJSON(p, runRemoteStatFromHost(ctx, readHostInput(p, stack)))
	}
}

func templateRenderCallback(_ string) extism.HostFunctionStackCallback {
	return func(ctx context.Context, p *extism.CurrentPlugin, stack []uint64) {
		stack[0] = writeRemoteJSON(p, runTemplateRenderFromHost(ctx, readHostInput(p, stack)))
	}
}

func runRemoteUploadFromHost(ctx context.Context, raw string) any {
	var in apiv1.RemoteUploadInput
	if err := parseRemoteInput(raw, &in); err != nil {
		return apiv1.RemoteUploadOutput{Failed: true, Error: err.Error()}
	}
	return runRemoteUpload(ctx, in)
}

func runRemoteDownloadFromHost(ctx context.Context, raw string) any {
	var in apiv1.RemoteDownloadInput
	if err := parseRemoteInput(raw, &in); err != nil {
		return apiv1.RemoteDownloadOutput{Failed: true, Error: err.Error()}
	}
	return runRemoteDownload(ctx, in)
}

func runRemoteStatFromHost(ctx context.Context, raw string) any {
	var in apiv1.RemoteStatInput
	if err := parseRemoteInput(raw, &in); err != nil {
		return apiv1.RemoteStatOutput{Failed: true, Error: err.Error()}
	}
	return runRemoteStat(ctx, in)
}

func runTemplateRenderFromHost(ctx context.Context, raw string) any {
	var in apiv1.TemplateRenderInput
	if err := parseRemoteInput(raw, &in); err != nil {
		return apiv1.TemplateRenderOutput{Failed: true, Error: err.Error()}
	}
	return runTemplateRender(ctx, in)
}

func runRemoteUpload(ctx context.Context, in apiv1.RemoteUploadInput) apiv1.RemoteUploadOutput {
	h, err := remoteHostCtx(ctx)
	if err != nil {
		return apiv1.RemoteUploadOutput{Failed: true, Error: err.Error()}
	}
	local := strings.TrimSpace(in.LocalPath)
	remote := strings.TrimSpace(in.RemotePath)
	if remote == "" {
		return apiv1.RemoteUploadOutput{Failed: true, Error: "remote_path is required"}
	}
	if local == "" && in.Content == "" {
		return apiv1.RemoteUploadOutput{Failed: true, Error: "local_path or content is required"}
	}
	if local != "" {
		resolved, err := resolvePluginLocalPath(h, local)
		if err != nil {
			return apiv1.RemoteUploadOutput{Failed: true, Error: err.Error()}
		}
		in.LocalPath = resolved
	}
	if !h.Execute {
		return apiv1.RemoteUploadOutput{Changed: true}
	}
	if h.Bridge == nil {
		return apiv1.RemoteUploadOutput{Failed: true, Error: "remote bridge not configured"}
	}
	return h.Bridge.RemoteUpload(ctx, in)
}

func runRemoteDownload(ctx context.Context, in apiv1.RemoteDownloadInput) apiv1.RemoteDownloadOutput {
	h, err := remoteHostCtx(ctx)
	if err != nil {
		return apiv1.RemoteDownloadOutput{Failed: true, Error: err.Error()}
	}
	remote := strings.TrimSpace(in.RemotePath)
	if remote == "" {
		return apiv1.RemoteDownloadOutput{Failed: true, Error: "remote_path is required"}
	}
	if !h.Execute {
		return apiv1.RemoteDownloadOutput{Changed: true}
	}
	if h.Bridge == nil {
		return apiv1.RemoteDownloadOutput{Failed: true, Error: "remote bridge not configured"}
	}
	out := h.Bridge.RemoteDownload(ctx, in)
	out.Content = truncateRemoteOutput(out.Content)
	return out
}

func runRemoteStat(ctx context.Context, in apiv1.RemoteStatInput) apiv1.RemoteStatOutput {
	h, err := remoteHostCtx(ctx)
	if err != nil {
		return apiv1.RemoteStatOutput{Failed: true, Error: err.Error()}
	}
	path := strings.TrimSpace(in.Path)
	if path == "" {
		return apiv1.RemoteStatOutput{Failed: true, Error: "path is required"}
	}
	if !h.Execute {
		return apiv1.RemoteStatOutput{Changed: true}
	}
	if h.Bridge == nil {
		return apiv1.RemoteStatOutput{Failed: true, Error: "remote bridge not configured"}
	}
	return h.Bridge.RemoteStat(ctx, in)
}

func runTemplateRender(ctx context.Context, in apiv1.TemplateRenderInput) apiv1.TemplateRenderOutput {
	h, err := remoteHostCtx(ctx)
	if err != nil {
		return apiv1.TemplateRenderOutput{Failed: true, Error: err.Error()}
	}
	if strings.TrimSpace(in.Template) == "" {
		return apiv1.TemplateRenderOutput{Failed: true, Error: "template is required"}
	}
	if !h.Execute {
		return apiv1.TemplateRenderOutput{Content: dryRunPlan("would render template", h)}
	}
	if h.Bridge == nil {
		return apiv1.TemplateRenderOutput{Failed: true, Error: "remote bridge not configured"}
	}
	out := h.Bridge.TemplateRender(ctx, in)
	out.Content = truncateRemoteOutput(out.Content)
	return out
}

func resolvePluginLocalPath(h *HostRunContext, local string) (string, error) {
	local = strings.TrimSpace(local)
	if local == "" {
		return "", fmt.Errorf("empty local path")
	}
	var abs string
	if filepath.IsAbs(local) {
		abs = filepath.Clean(local)
	} else {
		recipeDir := strings.TrimSpace(h.RecipeDir)
		if recipeDir == "" {
			return "", fmt.Errorf("empty recipe directory")
		}
		abs = filepath.Clean(filepath.Join(recipeDir, local))
	}
	if _, err := safepath.Stat(abs); err != nil {
		return "", err
	}
	if len(h.AllowedPaths) > 0 && !allowedLocalPath(h.AllowedPaths, abs) {
		return "", fmt.Errorf("local path %q not allowed by plugin manifest", local)
	}
	return abs, nil
}

func allowedLocalPath(allowed map[string]string, abs string) bool {
	for _, host := range allowed {
		host = filepath.Clean(host)
		if abs == host || strings.HasPrefix(abs, host+string(filepath.Separator)) {
			return true
		}
	}
	return false
}
