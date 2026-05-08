package ui

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/cloudtransfer"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/transferagent"
)

// BuildAgentTransferJob wires cloud credentials, per-target agent binaries, and staging object key.
// preferredAgentPath is used when agentOverride is empty (e.g. web server default binary); agentBuildCacheDir
// overrides the directory for cross-compiled agents (empty uses HONEY_TRANSFER_AGENT_CACHE or os temp).
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

	srcOS, srcArch, err := DetectTransferTargetRuntime(cache, sshUser, src)
	if err != nil {
		return AgentTransferJob{}, fmt.Errorf("detect source target runtime: %w", err)
	}
	dstOS, dstArch, err := DetectTransferTargetRuntime(cache, sshUser, dst)
	if err != nil {
		return AgentTransferJob{}, fmt.Errorf("detect destination target runtime: %w", err)
	}

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

	job := AgentTransferJob{
		SSHUser:              strings.TrimSpace(sshUser),
		AgentLocalPath:       srcBin,
		SourceAgentLocalPath: srcBin,
		DestAgentLocalPath:   dstBin,
		AgentRemoteDir:       strings.TrimSpace(agentRemoteDir),
		Source:               AgentTransferEndpoint{Record: src, Path: strings.TrimSpace(srcPath)},
		Destination:          AgentTransferEndpoint{Record: dst, Path: strings.TrimSpace(dstPath)},
		Cloud:                cloudNorm,
		CredentialProvider:   cred.Provider,
		CredentialEnv:        cred.Env,
		KeepObject:           keepObject,
		MaxRetries:           maxRetries,
	}
	if !cred.ExpiresAt.IsZero() {
		job.CredentialExpiresAtUnix = cred.ExpiresAt.Unix()
	}
	job.Cloud.Object = TransferStagingObjectKey(cloudNorm, src, dst)
	return job, nil
}
