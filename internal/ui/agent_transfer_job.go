package ui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/cloudtransfer"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/transferagent"
	"github.com/shareed2k/honey/internal/transferagent/presign"
	"go.uber.org/zap"
)

// BuildAgentTransferJob wires cloud credentials, per-target agent binaries, and staging object key.
// preferredAgentPath is used when agentOverride is empty (e.g. web server default binary); agentBuildCacheDir
// overrides the directory for cross-compiled agents (empty uses HONEY_TRANSFER_AGENT_CACHE or os temp).
//
// transferCfg controls whether the curl-path (presigned-URL) transport is attempted before
// falling back to staging the transfer-agent binary. When transferCfg.ForceAgentPath is true,
// the curl branch is bypassed.
func BuildAgentTransferJob(
	ctx context.Context,
	cache *ClientCache,
	sshUser, agentOverride, preferredAgentPath, agentBuildCacheDir, agentRemoteDir string,
	src, dst hosts.Record,
	srcPath, dstPath string,
	cloud AgentCloudBackend,
	keepObject bool,
	maxRetries int,
	hints cloudtransfer.SigningHints,
	transferCfg config.TransferConfigEffective,
) (AgentTransferJob, error) {
	cb := cloudtransfer.CloudBackend{
		Provider: cloud.Provider,
		Bucket:   cloud.Bucket,
		Prefix:   cloud.Prefix,
		Object:   cloud.Object,
		Region:   cloud.Region,
		Endpoint: cloud.Endpoint,
	}
	cred, err := cloudtransfer.ResolveCredentialMaterial(ctx, cb, hints)
	if err != nil {
		return AgentTransferJob{}, err
	}
	cloudNorm := cloud
	cloudNorm.Provider = cloudtransfer.NormalizeProvider(cloud.Provider)
	cloudNorm.Object = TransferStagingObjectKey(cloudNorm, src, dst)

	srcOS, srcArch, resolvedSrcUser, err := DetectTransferTargetRuntime(cache, sshUser, src)
	if err != nil {
		return AgentTransferJob{}, fmt.Errorf("detect source target runtime: %w", err)
	}
	dstOS, dstArch, resolvedDstUser, err := DetectTransferTargetRuntime(cache, sshUser, dst)
	if err != nil {
		return AgentTransferJob{}, fmt.Errorf("detect destination target runtime: %w", err)
	}

	job := AgentTransferJob{
		SSHUser:                     strings.TrimSpace(sshUser),
		ResolvedSourceUser:          resolvedSrcUser,
		ResolvedDestUser:            resolvedDstUser,
		AgentRemoteDir:              strings.TrimSpace(agentRemoteDir),
		Source:                      AgentTransferEndpoint{Record: src, Path: strings.TrimSpace(srcPath)},
		Destination:                 AgentTransferEndpoint{Record: dst, Path: strings.TrimSpace(dstPath)},
		Cloud:                       cloudNorm,
		CredentialProvider:          cred.Provider,
		CredentialEnv:               cred.Env,
		KeepObject:                  keepObject,
		MaxRetries:                  maxRetries,
		RetryWithAgentOnCurlFailure: transferCfg.PresignedRetryWithAgent,
	}
	if !cred.ExpiresAt.IsZero() {
		job.CredentialExpiresAtUnix = cred.ExpiresAt.Unix()
	}

	if plan, ok := tryCurlPlan(ctx, cache, resolvedSrcUser, resolvedDstUser, src, dst, srcPath, cloudNorm, transferCfg); ok {
		job.CurlPlan = plan
		return job, nil
	}

	// Fallback: resolve agent binaries (existing path).
	cacheDir := strings.TrimSpace(agentBuildCacheDir)
	if cacheDir == "" {
		cacheDir = strings.TrimSpace(os.Getenv("HONEY_TRANSFER_AGENT_CACHE"))
	}
	defaultAgent := strings.TrimSpace(preferredAgentPath)
	if defaultAgent == "" {
		defaultAgent = strings.TrimSpace(os.Getenv("HONEY_TRANSFER_AGENT"))
	}
	srcBin, err := transferagent.ResolveBinary(agentOverride, defaultAgent, cacheDir, srcOS, srcArch, cloudNorm.Provider)
	if err != nil {
		return AgentTransferJob{}, fmt.Errorf("resolve source transfer agent: %w", err)
	}
	dstBin, err := transferagent.ResolveBinary(agentOverride, defaultAgent, cacheDir, dstOS, dstArch, cloudNorm.Provider)
	if err != nil {
		return AgentTransferJob{}, fmt.Errorf("resolve destination transfer agent: %w", err)
	}
	job.AgentLocalPath = srcBin
	job.SourceAgentLocalPath = srcBin
	job.DestAgentLocalPath = dstBin
	return job, nil
}

// tryCurlPlan returns a presign.Plan for the curl path, or (nil, false) when the
// agent path should be used. Reasons to return false: ForceAgentPath, missing
// curl on either host, file too large, planning error.
//
// Preconditions: cache has live entries for both src and dst (the caller has
// already invoked DetectTransferTargetRuntime which dials through GetOrDial).
func tryCurlPlan(
	ctx context.Context,
	cache *ClientCache,
	resolvedSrcUser string,
	resolvedDstUser string,
	src, dst hosts.Record,
	srcPath string,
	cloud AgentCloudBackend,
	cfg config.TransferConfigEffective,
) (*presign.Plan, bool) {
	if cfg.ForceAgentPath {
		zap.L().Debug("tryCurlPlan: skipping because ForceAgentPath is true")
		return nil, false
	}
	srcKey := SSHClientCacheKey(resolvedSrcUser, src)
	dstKey := SSHClientCacheKey(resolvedDstUser, dst)
	runner := cacheRunner{cache: cache}

	srcOK, srcErr := detectCurlCapabilityViaRunner(runner, srcKey)
	if srcErr != nil || !srcOK {
		zap.L().Debug("tryCurlPlan: skipping because source host lacks curl capability", zap.Error(srcErr), zap.Bool("ok", srcOK))
		return nil, false
	}
	dstOK, dstErr := detectCurlCapabilityViaRunner(runner, dstKey)
	if dstErr != nil || !dstOK {
		zap.L().Debug("tryCurlPlan: skipping because destination host lacks curl capability", zap.Error(dstErr), zap.Bool("ok", dstOK))
		return nil, false
	}

	// Stat src file size on the remote.
	sizeOut, err := runner.RunRemoteCmd(srcKey,
		fmt.Sprintf("stat -c %%s %s 2>/dev/null || stat -f %%z %s", shellQuote(srcPath), shellQuote(srcPath)))
	if err != nil {
		zap.L().Debug("tryCurlPlan: skipping because stat failed", zap.Error(err))
		return nil, false
	}
	var size int64
	if _, err := fmt.Sscanf(strings.TrimSpace(sizeOut), "%d", &size); err != nil {
		zap.L().Debug("tryCurlPlan: skipping because size parse failed", zap.String("output", sizeOut), zap.Error(err))
		return nil, false
	}
	if size > cfg.PresignedMaxSizeBytes {
		zap.L().Debug("tryCurlPlan: skipping because file too large", zap.Int64("size", size), zap.Int64("max", cfg.PresignedMaxSizeBytes))
		return nil, false
	}

	plan, err := presign.PlanTransfer(ctx, presign.Cloud{
		Provider: cloud.Provider,
		Bucket:   cloud.Bucket,
		Object:   cloud.Object,
		Region:   cloud.Region,
		Endpoint: cloud.Endpoint,
	}, size, presign.Config{
		PresignedMaxSizeBytes:   cfg.PresignedMaxSizeBytes,
		MultipartThresholdBytes: cfg.MultipartThresholdBytes,
		URLTTL:                  cfg.PresignedURLTTL,
	})
	if err != nil {
		zap.L().Debug("tryCurlPlan: PlanTransfer failed", zap.Error(err))
		return nil, false
	}
	return plan, true
}

// RunAgentTransferWithFallback runs Build + Execute, transparently retrying via
// the agent path on curl-path failure when transferCfg.PresignedRetryWithAgent
// is true. Returns the combined event timeline across both attempts so the
// caller always sees the full record.
//
// emit may be nil; when non-nil it receives events as they happen for both
// attempts (the original curl-path attempt and the agent-path retry).
func RunAgentTransferWithFallback(
	ctx context.Context,
	cache *ClientCache,
	sshUser, agentOverride, preferredAgentPath, agentBuildCacheDir, agentRemoteDir string,
	src, dst hosts.Record,
	srcPath, dstPath string,
	cloud AgentCloudBackend,
	keepObject bool,
	maxRetries int,
	hints cloudtransfer.SigningHints,
	transferCfg config.TransferConfigEffective,
	emit func(AgentTransferEvent),
) ([]AgentTransferEvent, error) {
	job, err := BuildAgentTransferJob(
		ctx, cache,
		sshUser, agentOverride, preferredAgentPath, agentBuildCacheDir, agentRemoteDir,
		src, dst, srcPath, dstPath,
		cloud, keepObject, maxRetries, hints, transferCfg,
	)
	if err != nil {
		return nil, err
	}

	events, execErr := ExecuteAgentCloudTransferWithEmit(job, cache, emit)
	// Retry only when: the curl path was actually attempted, it failed, and the
	// effective config opts into retry-with-agent.
	if execErr != nil && job.CurlPlan != nil && transferCfg.PresignedRetryWithAgent {
		agentCfg := transferCfg
		agentCfg.ForceAgentPath = true
		retryJob, retryBuildErr := BuildAgentTransferJob(
			ctx, cache,
			sshUser, agentOverride, preferredAgentPath, agentBuildCacheDir, agentRemoteDir,
			src, dst, srcPath, dstPath,
			cloud, keepObject, maxRetries, hints, agentCfg,
		)
		if retryBuildErr == nil {
			moreEvents, retryExecErr := ExecuteAgentCloudTransferWithEmit(retryJob, cache, emit)
			events = append(events, moreEvents...)
			execErr = retryExecErr
		}
	}
	return events, execErr
}
