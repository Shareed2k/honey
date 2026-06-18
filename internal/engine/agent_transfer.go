package engine

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/transferagent/presign"
	"go.uber.org/zap"
)

// AgentTransferEndpoint identifies one source/destination endpoint for transfer.
// AgentTransferEndpoint ...
type AgentTransferEndpoint struct {
	Record hosts.Record `json:"record"`
	Path   string       `json:"path"`
}

// AgentCloudBackend describes the cloud object target path for staging.
// AgentCloudBackend ...
type AgentCloudBackend struct {
	Provider string `json:"provider"`
	Bucket   string `json:"bucket"`
	Prefix   string `json:"prefix,omitempty"`
	Object   string `json:"object,omitempty"`
	Region   string `json:"region,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

// AgentTransferJob describes one A->cloud->B transfer orchestration request.
// AgentTransferJob ...
type AgentTransferJob struct {
	SSHUser                 string                `json:"ssh_user"`
	ResolvedSourceUser      string                `json:"resolved_source_user"`
	ResolvedDestUser        string                `json:"resolved_dest_user"`
	AgentLocalAbs           string                `json:"agent_local_path"`
	SourceAgentLocalAbs     string                `json:"source_agent_local_path,omitempty"`
	DestAgentLocalAbs       string                `json:"dest_agent_local_path,omitempty"`
	AgentRemoteDir          string                `json:"agent_remote_dir,omitempty"`
	Source                  AgentTransferEndpoint `json:"source"`
	Destination             AgentTransferEndpoint `json:"destination"`
	Cloud                   AgentCloudBackend     `json:"cloud"`
	CredentialProvider      string                `json:"credential_provider,omitempty"`
	CredentialEnv           map[string]string     `json:"credential_env,omitempty"`
	CredentialExpiresAtUnix int64                 `json:"credential_expires_at_unix,omitempty"`
	KeepObject              bool                  `json:"keep_object,omitempty"`
	MaxRetries              int                   `json:"max_retries,omitempty"`
	// FallbackPlan is non-nil when the curl-based presigned-URL transport should be
	// used instead of the staged-agent path. See docs/superpowers/specs/2026-05-12-presigned-url-transfer-path-design.md.
	FallbackPlan          *presign.Plan `json:"-"`
	FallbackCapabilitySrc string        `json:"-"`
	FallbackCapabilityDst string        `json:"-"`
	// RetryWithAgentOnCurlFailure controls whether a fallback-path failure transparently
	// retries via the agent path.
	RetryWithAgentOnCurlFailure bool `json:"retry_with_agent_on_curl_failure,omitempty"`
}

// AgentTransferEvent is emitted for each orchestration stage.
// AgentTransferEvent ...
type AgentTransferEvent struct {
	Stage     string    `json:"stage"`
	Host      string    `json:"host,omitempty"`
	Success   bool      `json:"success"`
	Message   string    `json:"message,omitempty"`
	Error     string    `json:"error,omitempty"`
	Attempt   int       `json:"attempt,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// Fallback-path stage names. Emitted as the Stage field on AgentTransferEvent
const (
	StageFallbackDetect        = "fallback_detect"
	StageFallbackDetectFailed  = "fallback_detect_failed"
	StagePresignPutStart       = "presign_put_start"
	StagePresignPut            = "presign_put"
	StagePresignPutFailed      = "presign_put_failed"
	StagePresignGetStart       = "presign_get_start"
	StagePresignGet            = "presign_get"
	StagePresignGetFailed      = "presign_get_failed"
	StagePresignMultipartStart = "presign_multipart_start"
	StagePresignMultipart      = "presign_multipart"
	StagePresignMultipartAbort = "presign_multipart_aborted"
	StagePresignComplete       = "presign_complete"
	StagePresignCompleteFailed = "presign_complete_failed"
	StagePresignCleanup        = "presign_cleanup"
	StagePresignCleanupFailed  = "presign_cleanup_failed"
	StagePresignFallback       = "presign_falling_back_to_agent"
)

// AgentTransferValidationError indicates user/input issues (HTTP 400).
// AgentTransferValidationError ...
type AgentTransferValidationError struct {
	msg string
}

func (e *AgentTransferValidationError) Error() string {
	return e.msg
}

func newAgentTransferValidationError(msg string) error {
	return &AgentTransferValidationError{msg: msg}
}

func targetLabel(r hosts.Record) string {
	if strings.TrimSpace(r.Name) != "" {
		return r.Name
	}
	if strings.TrimSpace(r.PrimaryIP) != "" {
		return r.PrimaryIP
	}
	return "unknown"
}

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

func remoteAgentPath(remoteDir string) string {
	rd := strings.TrimSpace(remoteDir)
	if rd == "" {
		rd = "/tmp"
	}
	return strings.TrimRight(rd, "/") + "/honey-transfer-agent"
}

func shouldUploadAgent(client HostClient, localPath, remotePath string) (bool, string, error) {
	localInfo, err := os.Stat(strings.TrimSpace(localPath)) // #nosec G304 -- local path is resolved by server.
	if err != nil {
		return false, "", err
	}
	if localInfo.IsDir() {
		return false, "", fmt.Errorf("agent path is a directory: %s", localPath)
	}
	remoteInfo, err := client.StatRemote(strings.TrimSpace(remotePath))
	if err != nil {
		return true, "remote stat missing; upload required", nil
	}
	if remoteInfo.IsDir {
		return true, "remote agent path is a directory; upload required", nil
	}
	if remoteInfo.Size == localInfo.Size() && remoteInfo.Size > 0 {
		localSHA, err := fileSHA256(strings.TrimSpace(localPath))
		if err != nil {
			return true, "local checksum failed; upload required", nil
		}
		remoteSHA, err := remoteFileSHA256(client, strings.TrimSpace(remotePath))
		if err != nil {
			return true, describeRemoteChecksumError(err), nil
		}
		if localSHA != "" && remoteSHA != "" && strings.EqualFold(localSHA, remoteSHA) {
			return false, fmt.Sprintf("reusing existing remote agent (%d bytes, sha256 match)", remoteInfo.Size), nil
		}
		return true, fmt.Sprintf("checksum mismatch local=%s remote=%s; upload required", localSHA, remoteSHA), nil
	}
	return true, fmt.Sprintf("size mismatch local=%d remote=%d; upload required", localInfo.Size(), remoteInfo.Size), nil
}

func fileSHA256(pathValue string) (string, error) {
	f, err := os.Open(pathValue) // #nosec G304 -- local path is resolved by server.
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func remoteFileSHA256(client HostClient, remotePath string) (string, error) {
	cmd := "if command -v python3 >/dev/null 2>&1; then python3 -c \"import sys,hashlib; h=hashlib.sha256(); f=open(sys.argv[1],'rb'); [h.update(c) for c in iter(lambda:f.read(65536), b'')]; print(h.hexdigest())\" " + shellQuote(remotePath) + "; " +
		"elif command -v python >/dev/null 2>&1; then python -c \"import sys,hashlib; h=hashlib.sha256(); f=open(sys.argv[1],'rb'); [h.update(c) for c in iter(lambda:f.read(65536), b'')]; print(h.hexdigest())\" " + shellQuote(remotePath) + "; " +
		"elif command -v sha256sum >/dev/null 2>&1; then sha256sum " + shellQuote(remotePath) + " | awk '{print $1}'; " +
		"elif command -v shasum >/dev/null 2>&1; then shasum -a 256 " + shellQuote(remotePath) + " | awk '{print $1}'; " +
		"else exit 127; fi"
	raw, err := client.Run(cmd)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(raw)), nil
}

func describeRemoteChecksumError(err error) string {
	msg := strings.ToLower(strings.TrimSpace(err.Error()))
	switch {
	case strings.Contains(msg, "status 127"), strings.Contains(msg, "exit status 127"), strings.Contains(msg, "command not found"):
		return "remote checksum tool missing; upload required"
	default:
		return "remote checksum failed; upload required"
	}
}

func redactTransferText(in string, redactions []string) string {
	out := in
	for _, secret := range redactions {
		secret = strings.TrimSpace(secret)
		if len(secret) < 4 {
			continue
		}
		out = strings.ReplaceAll(out, secret, "***")
	}
	return out
}

func transferRedactionsFromValues(values []string) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, v := range values {
		val := strings.TrimSpace(v)
		if len(val) < 4 {
			continue
		}
		if _, ok := seen[val]; ok {
			continue
		}
		seen[val] = struct{}{}
		out = append(out, val)
	}
	return out
}

func transferRedactionsForCredentialMode(credentialEnv map[string]string, jwes ...string) []string {
	values := make([]string, 0, len(credentialEnv)+len(jwes))
	for _, v := range credentialEnv {
		values = append(values, v)
	}
	values = append(values, jwes...)
	return transferRedactionsFromValues(values)
}

func stageEvent(out *[]AgentTransferEvent, emit func(AgentTransferEvent), redactions []string, stage, host string, ok bool, msg string, err error, attempt int) {
	ev := AgentTransferEvent{
		Stage:     stage,
		Host:      host,
		Success:   ok,
		Message:   redactTransferText(msg, redactions),
		Attempt:   attempt,
		Timestamp: time.Now().UTC(),
	}
	if err != nil {
		ev.Error = redactTransferText(err.Error(), redactions)
	}
	*out = append(*out, ev)
	if emit != nil {
		emit(ev)
	}
}

// stageEventMaybeLocked is like stageEvent but serializes appends when eventMu is non-nil (parallel staging).
func stageEventMaybeLocked(eventMu *sync.Mutex, out *[]AgentTransferEvent, emit func(AgentTransferEvent), redactions []string, stage, host string, ok bool, msg string, err error, attempt int) {
	if eventMu != nil {
		eventMu.Lock()
		defer eventMu.Unlock()
	}
	stageEvent(out, emit, redactions, stage, host, ok, msg, err, attempt)
}

func agentCompressionMode(agentPath string) string {
	if strings.Contains(strings.ToLower(strings.TrimSpace(agentPath)), "-upx") {
		return "upx"
	}
	return "none"
}

func agentProviderFlavor(agentPath string) string {
	normalized := strings.ToLower(strings.TrimSpace(agentPath))
	switch {
	case strings.Contains(normalized, "-s3"):
		return "s3"
	case strings.Contains(normalized, "-gcs"):
		return "gcs"
	default:
		return "full"
	}
}

func mintCredentialJWE(publicJWK string, scope string, provider string, creds map[string]string, expiresAtUnix int64) (string, error) {
	var jwk jose.JSONWebKey
	if err := json.Unmarshal([]byte(strings.TrimSpace(publicJWK)), &jwk); err != nil {
		return "", fmt.Errorf("parse agent public jwk: %w", err)
	}
	if jwk.Key == nil {
		return "", fmt.Errorf("agent public jwk missing key")
	}
	recipient := jose.Recipient{
		Algorithm: jose.ECDH_ES,
		Key:       jwk.Key,
	}
	enc, err := jose.NewEncrypter(jose.A256GCM, recipient, nil)
	if err != nil {
		return "", fmt.Errorf("build jwe encrypter: %w", err)
	}
	jtiRaw := make([]byte, 16)
	if _, err := rand.Read(jtiRaw); err != nil {
		return "", fmt.Errorf("jti random: %w", err)
	}
	exp := expiresAtUnix
	if exp <= 0 {
		exp = time.Now().UTC().Add(15 * time.Minute).Unix()
	}
	nbf := time.Now().UTC().Add(-15 * time.Second).Unix()
	claims := map[string]any{
		"iss":      "honey",
		"aud":      "honey-transfer-agent",
		"exp":      exp,
		"nbf":      nbf,
		"jti":      hex.EncodeToString(jtiRaw),
		"scope":    scope,
		"provider": provider,
		"creds":    creds,
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", fmt.Errorf("marshal jwe payload: %w", err)
	}
	obj, err := enc.Encrypt(payload)
	if err != nil {
		return "", fmt.Errorf("encrypt jwe payload: %w", err)
	}
	compact, err := obj.CompactSerialize()
	if err != nil {
		return "", fmt.Errorf("serialize jwe payload: %w", err)
	}
	return compact, nil
}

func runUploadWithHeartbeat(
	out *[]AgentTransferEvent,
	emit func(AgentTransferEvent),
	redactions []string,
	stage string,
	host string,
	attempt int,
	uploadFn func() error,
	eventMu *sync.Mutex,
) error {
	uploadStart := time.Now()
	zap.L().Debug(
		"agent transfer upload start",
		zap.String("stage", stage),
		zap.String("host", host),
		zap.Int("attempt", attempt),
	)
	stageEventMaybeLocked(eventMu, out, emit, redactions, stage+"_start", host, true, "starting", nil, attempt)
	errCh := make(chan error, 1)
	go func() {
		errCh <- uploadFn()
	}()
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case err := <-errCh:
			if err != nil {
				zap.L().Warn(
					"agent transfer upload failed",
					zap.String("stage", stage),
					zap.String("host", host),
					zap.Int("attempt", attempt),
					zap.Duration("elapsed", time.Since(uploadStart)),
					zap.Error(err),
				)
			} else {
				zap.L().Debug(
					"agent transfer upload success",
					zap.String("stage", stage),
					zap.String("host", host),
					zap.Int("attempt", attempt),
					zap.Duration("elapsed", time.Since(uploadStart)),
				)
			}
			return err
		case <-ticker.C:
			zap.L().Debug(
				"agent transfer upload progress",
				zap.String("stage", stage),
				zap.String("host", host),
				zap.Int("attempt", attempt),
				zap.Duration("elapsed", time.Since(uploadStart)),
			)
			stageEventMaybeLocked(eventMu, out, emit, redactions, stage+"_progress", host, true, "still uploading", nil, attempt)
		}
	}
}

// stageAgentBinary checks whether the agent binary must be uploaded and uploads it with heartbeats.
// eventMu may be nil when only one goroutine emits transfer events.
func stageAgentBinary(
	eventMu *sync.Mutex,
	out *[]AgentTransferEvent,
	emit func(AgentTransferEvent),
	redactions []string,
	stage string,
	host string,
	client HostClient,
	localPath, remotePath string,
) error {
	needsUpload, reason, err := shouldUploadAgent(client, localPath, remotePath)
	if err != nil {
		stageEventMaybeLocked(eventMu, out, emit, redactions, stage, host, false, "", err, 1)
		return err
	}
	if needsUpload {
		zap.L().Debug(
			"agent transfer stage requires upload",
			zap.String("stage", stage),
			zap.String("host", host),
			zap.String("reason", reason),
		)
		if err := runUploadWithHeartbeat(out, emit, redactions, stage, host, 1, func() error {
			return client.Upload(localPath, remotePath)
		}, eventMu); err != nil {
			stageEventMaybeLocked(eventMu, out, emit, redactions, stage, host, false, "", err, 1)
			return err
		}
		stageEventMaybeLocked(eventMu, out, emit, redactions, stage, host, true, remotePath, nil, 1)
		return nil
	}
	zap.L().Debug(
		"agent transfer stage skipped upload",
		zap.String("stage", stage),
		zap.String("host", host),
		zap.String("reason", reason),
	)
	stageEventMaybeLocked(eventMu, out, emit, redactions, stage, host, true, reason, nil, 1)
	return nil
}

// HostConnectableForTransfer reports whether a record can be dialed for SSH, k8s exec, or docker exec.
// HostConnectableForTransfer ...
func HostConnectableForTransfer(r hosts.Record) bool {
	return r.IsConnectable()
}

func validateAgentTransferJob(job AgentTransferJob) error {
	if !job.Source.Record.IsConnectable() {
		return newAgentTransferValidationError("source record has no connectable target")
	}
	if !job.Destination.Record.IsConnectable() {
		return newAgentTransferValidationError("destination record has no connectable target")
	}
	if job.FallbackPlan == nil && strings.TrimSpace(job.AgentLocalAbs) == "" {
		return newAgentTransferValidationError("agent_local_path is required")
	}
	if strings.TrimSpace(job.Source.Path) == "" {
		return newAgentTransferValidationError("source.path is required")
	}
	if strings.TrimSpace(job.Destination.Path) == "" {
		return newAgentTransferValidationError("destination.path is required")
	}
	if strings.TrimSpace(job.CredentialProvider) == "" {
		return newAgentTransferValidationError("credential_provider is required")
	}
	if len(job.CredentialEnv) == 0 {
		return newAgentTransferValidationError("credential_env is required")
	}
	if strings.TrimSpace(job.Cloud.Provider) == "" || strings.TrimSpace(job.Cloud.Bucket) == "" {
		return newAgentTransferValidationError("cloud.provider and cloud.bucket are required")
	}
	if strings.TrimSpace(job.Cloud.Object) == "" {
		return newAgentTransferValidationError("cloud.object is required")
	}
	return nil
}

func agentTransferEndpointsMismatch(job AgentTransferJob, srcHost, dstHost string) bool {
	return srcHost != dstHost ||
		job.Source.Record.PrimaryIP != job.Destination.Record.PrimaryIP ||
		job.Source.Record.Provider != job.Destination.Record.Provider
}

func stageSourceDestinationAgents(
	job AgentTransferJob,
	events *[]AgentTransferEvent,
	emit func(AgentTransferEvent),
	redactions []string,
	srcHost, dstHost string,
	srcClient, dstClient HostClient,
	srcAgentPath, dstAgentPath, agentRemoteAbs string,
) error {
	if !agentTransferEndpointsMismatch(job, srcHost, dstHost) {
		if err := stageAgentBinary(nil, events, emit, redactions, "stage_agent_source", srcHost, srcClient, srcAgentPath, agentRemoteAbs); err != nil {
			return err
		}
		stageEvent(events, emit, redactions, "stage_agent_destination", dstHost, true, "same host as source; skipped duplicate stage", nil, 1)
		return nil
	}
	var eventMu sync.Mutex
	type stageOutcome struct {
		role string
		err  error
	}
	outcomes := make(chan stageOutcome, 2)
	// runStage always delivers exactly one outcome, even if stageAgentBinary
	// panics — otherwise the for-range below would deadlock on a missing send.
	runStage := func(role, eventName, host string, client HostClient, agentPath string) {
		defer func() {
			if r := recover(); r != nil {
				outcomes <- stageOutcome{role: role, err: fmt.Errorf("stage %s panicked: %v", role, r)}
			}
		}()
		err := stageAgentBinary(&eventMu, events, emit, redactions, eventName, host, client, agentPath, agentRemoteAbs)
		outcomes <- stageOutcome{role: role, err: err}
	}
	go runStage("source", "stage_agent_source", srcHost, srcClient, srcAgentPath)
	go runStage("destination", "stage_agent_destination", dstHost, dstClient, dstAgentPath)
	var srcStageErr, dstStageErr error
	for range 2 {
		o := <-outcomes
		switch o.role {
		case "source":
			srcStageErr = o.err
		case "destination":
			dstStageErr = o.err
		}
	}
	if srcStageErr != nil {
		return srcStageErr
	}
	return dstStageErr
}

// executeFallbackPath drives a single transfer using only curl on the remotes,
// with the operator-generated presigned URLs from job.FallbackPlan. Emits stage
// events for each phase. Cleanup (DeleteObject) runs in a deferred best-effort
// pass regardless of success.
func executeFallbackPath(job AgentTransferJob, cache *ClientCache, emit func(AgentTransferEvent)) error {
	plan := job.FallbackPlan
	if plan == nil {
		return fmt.Errorf("executeFallbackPath: nil plan")
	}
	defer func() {
		if plan.Cleanup == nil {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := plan.Cleanup(ctx); err != nil {
			emit(AgentTransferEvent{Stage: StagePresignCleanupFailed, Success: false, Error: err.Error()})
		} else {
			emit(AgentTransferEvent{Stage: StagePresignCleanup, Success: true})
		}
	}()

	srcKey := SSHClientCacheKey(job.ResolvedSourceUser, job.Source.Record)
	dstKey := SSHClientCacheKey(job.ResolvedDestUser, job.Destination.Record)

	// Upload phase: single or multipart.
	if len(plan.UploadParts) == 1 {
		emit(AgentTransferEvent{Stage: StagePresignPutStart, Host: srcKey, Success: true, Message: "single-PUT"})
		script := buildSinglePutScript(job.FallbackCapabilitySrc, job.Source.Path, plan.UploadParts[0], plan.PartSize)
		if out, err := runOneShotRemote(cache, srcKey, script); err != nil {
			emit(AgentTransferEvent{Stage: StagePresignPutFailed, Host: srcKey, Success: false, Error: err.Error(), Message: "stdout: " + out})
			return fmt.Errorf("fallback put: %w, output: %s", err, out)
		}
		emit(AgentTransferEvent{Stage: StagePresignPut, Host: srcKey, Success: true})
	} else {
		emit(AgentTransferEvent{Stage: StagePresignMultipartStart, Host: srcKey, Success: true, Message: fmt.Sprintf("%d parts", len(plan.UploadParts))})
		script := buildMultipartScript(job.FallbackCapabilitySrc, job.Source.Path, plan.PartSize, plan.UploadParts)
		out, err := runOneShotRemote(cache, srcKey, script)
		if err != nil {
			_ = abortMultipartIfS3(plan, job.Cloud)
			emit(AgentTransferEvent{Stage: StagePresignMultipartAbort, Host: srcKey, Success: false, Error: err.Error(), Message: "stdout: " + out})
			return fmt.Errorf("multipart upload: %w, output: %s", err, out)
		}
		tags, perr := parseMultipartEtags(out, len(plan.UploadParts))
		if perr != nil {
			_ = abortMultipartIfS3(plan, job.Cloud)
			emit(AgentTransferEvent{Stage: StagePresignMultipartAbort, Host: srcKey, Success: false, Error: perr.Error()})
			return fmt.Errorf("parse etags: %w", perr)
		}
		if plan.Complete != nil {
			copy(plan.Complete.PartTags, tags)
		}
		if err := completeMultipart(plan, job.Cloud); err != nil {
			emit(AgentTransferEvent{Stage: StagePresignCompleteFailed, Success: false, Error: err.Error()})
			return fmt.Errorf("complete multipart: %w", err)
		}
		emit(AgentTransferEvent{Stage: StagePresignComplete, Success: true})
	}

	// Download phase.
	emit(AgentTransferEvent{Stage: StagePresignGetStart, Host: dstKey, Success: true})
	script := buildDownloadScript(job.FallbackCapabilityDst, job.Destination.Path, plan.Download)
	if out, err := runOneShotRemote(cache, dstKey, script); err != nil {
		emit(AgentTransferEvent{Stage: StagePresignGetFailed, Host: dstKey, Success: false, Error: err.Error(), Message: "stdout: " + out})
		return fmt.Errorf("fallback get: %w, output: %s", err, out)
	}
	emit(AgentTransferEvent{Stage: StagePresignGet, Host: dstKey, Success: true})
	return nil
}

// completeMultipart finalizes a multipart upload after the operator has all
// per-part ETags. No-op for non-S3 providers.
func completeMultipart(plan *presign.Plan, cloud AgentCloudBackend) error {
	if plan.Provider != "s3" || plan.Complete == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cli, err := presign.DefaultS3Client(ctx, presign.Cloud{
		Bucket: cloud.Bucket, Object: cloud.Object, Region: cloud.Region, Endpoint: cloud.Endpoint,
	})
	if err != nil {
		return err
	}
	return presign.CompleteS3Multipart(ctx, cli, presign.Cloud{
		Bucket: cloud.Bucket, Object: cloud.Object, Region: cloud.Region, Endpoint: cloud.Endpoint,
	}, plan.Complete)
}

// abortMultipartIfS3 calls AbortMultipartUpload when the in-flight plan is S3 multipart.
func abortMultipartIfS3(plan *presign.Plan, cloud AgentCloudBackend) error {
	if plan.Provider != "s3" || plan.Complete == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	cli, err := presign.DefaultS3Client(ctx, presign.Cloud{
		Bucket: cloud.Bucket, Object: cloud.Object, Region: cloud.Region, Endpoint: cloud.Endpoint,
	})
	if err != nil {
		return err
	}
	return presign.AbortS3Multipart(ctx, cli, presign.Cloud{
		Bucket: cloud.Bucket, Object: cloud.Object, Region: cloud.Region, Endpoint: cloud.Endpoint,
	}, plan.Complete.UploadID)
}

// ExecuteAgentCloudTransfer orchestrates source upload and destination download using
// ephemeral transfer agents over existing HostClient connections (SSH / k8s pod exec abstraction).
// ExecuteAgentCloudTransfer ...
func ExecuteAgentCloudTransfer(job AgentTransferJob, cache *ClientCache) ([]AgentTransferEvent, error) {
	return ExecuteAgentCloudTransferWithEmit(job, cache, nil)
}

// runFallbackPathBranch runs the fallback-path transfer when job.FallbackPlan != nil, emitting
// events through both emit and the appended events slice. Returns the accumulated
// events and any error from the curl path; on failure with
// RetryWithAgentOnCurlFailure, surfaces a clear fallback event explaining that
// re-orchestration is required.
func runFallbackPathBranch(job AgentTransferJob, cache *ClientCache, emit func(AgentTransferEvent)) ([]AgentTransferEvent, error) {
	var events []AgentTransferEvent
	emitWrap := func(ev AgentTransferEvent) {
		if ev.Timestamp.IsZero() {
			ev.Timestamp = time.Now().UTC()
		}
		events = append(events, ev)
		if emit != nil {
			emit(ev)
		}
	}
	fallbackErr := executeFallbackPath(job, cache, emitWrap)
	if fallbackErr == nil {
		return events, nil
	}
	if !job.RetryWithAgentOnCurlFailure {
		return events, fallbackErr
	}
	// Fall-back to the agent path would require re-resolving agent binaries
	// that the fallback branch deliberately skipped. The cleanest place to do
	// that is one layer up at the orchestrator (web handler / CLI command),
	// which just passes ForceAgentPath=true and rebuilds the job.
	emitWrap(AgentTransferEvent{
		Stage:   StageFallbackDetectFailed,
		Success: false,
		Error:   "retry-with-agent requires re-orchestration with ForceAgentPath=true; original error: " + fallbackErr.Error(),
	})
	return events, fallbackErr
}

// ExecuteAgentCloudTransferWithEmit runs the transfer and calls emit for each event.
// ExecuteAgentCloudTransferWithEmit ...
func ExecuteAgentCloudTransferWithEmit(job AgentTransferJob, cache *ClientCache, emit func(AgentTransferEvent)) ([]AgentTransferEvent, error) {
	var events []AgentTransferEvent
	transferStart := time.Now()
	if err := validateAgentTransferJob(job); err != nil {
		return events, err
	}
	if job.FallbackPlan != nil {
		return runFallbackPathBranch(job, cache, emit)
	}
	objectKey := strings.TrimSpace(job.Cloud.Object)
	user := strings.TrimSpace(job.SSHUser)
	if user == "" {
		user = strings.TrimSpace(os.Getenv("USER"))
	}
	if user == "" {
		user = "root"
	}
	redactions := transferRedactionsForCredentialMode(job.CredentialEnv)
	ownCache := false
	if cache == nil {
		cache = NewClientCache()
		ownCache = true
	}
	if ownCache {
		defer cache.CloseAll()
	}
	srcHost := targetLabel(job.Source.Record)
	dstHost := targetLabel(job.Destination.Record)
	zap.L().Debug(
		"agent transfer begin",
		zap.String("source_host", srcHost),
		zap.String("destination_host", dstHost),
		zap.String("cloud_provider", strings.TrimSpace(job.Cloud.Provider)),
		zap.String("cloud_bucket", strings.TrimSpace(job.Cloud.Bucket)),
		zap.Bool("keep_object", job.KeepObject),
		zap.Int("max_retries", job.MaxRetries),
	)
	srcClient, err := cache.GetOrDial(user, job.Source.Record)
	if err != nil {
		stageEvent(&events, emit, redactions, "dial_source", srcHost, false, "", err, 1)
		return events, err
	}
	stageEvent(&events, emit, redactions, "dial_source", srcHost, true, "connected", nil, 1)
	dstClient, err := cache.GetOrDial(user, job.Destination.Record)
	if err != nil {
		stageEvent(&events, emit, redactions, "dial_destination", dstHost, false, "", err, 1)
		return events, err
	}
	stageEvent(&events, emit, redactions, "dial_destination", dstHost, true, "connected", nil, 1)

	agentRemoteAbs := remoteAgentPath(job.AgentRemoteDir)
	srcAgentPath := strings.TrimSpace(job.SourceAgentLocalAbs)
	if srcAgentPath == "" {
		srcAgentPath = strings.TrimSpace(job.AgentLocalAbs)
	}
	dstAgentPath := strings.TrimSpace(job.DestAgentLocalAbs)
	if dstAgentPath == "" {
		dstAgentPath = strings.TrimSpace(job.AgentLocalAbs)
	}
	stageEvent(&events, emit, redactions, "agent_binary_source", srcHost, true, fmt.Sprintf("%s (provider=%s compression=%s)", srcAgentPath, agentProviderFlavor(srcAgentPath), agentCompressionMode(srcAgentPath)), nil, 1)
	stageEvent(&events, emit, redactions, "agent_binary_destination", dstHost, true, fmt.Sprintf("%s (provider=%s compression=%s)", dstAgentPath, agentProviderFlavor(dstAgentPath), agentCompressionMode(dstAgentPath)), nil, 1)

	if err := stageSourceDestinationAgents(job, &events, emit, redactions, srcHost, dstHost, srcClient, dstClient, srcAgentPath, dstAgentPath, agentRemoteAbs); err != nil {
		return events, err
	}
	_, _ = srcClient.Run("chmod +x " + shellQuote(agentRemoteAbs))
	_, _ = dstClient.Run("chmod +x " + shellQuote(agentRemoteAbs))

	retries := job.MaxRetries
	if retries <= 0 {
		retries = 2
	}

	evictSource := func(failedAttempt int) {
		evictCachedSSHClient(cache, user, job.Source.Record, failedAttempt, nil)
	}
	evictDestination := func(failedAttempt int) {
		evictCachedSSHClient(cache, user, job.Destination.Record, failedAttempt, nil)
	}

	cloudBase := agentSessionHostMsg{
		Provider: job.Cloud.Provider,
		Bucket:   job.Cloud.Bucket,
		Object:   objectKey,
		Region:   job.Cloud.Region,
		Endpoint: job.Cloud.Endpoint,
	}
	mintSrcJWE := func(publicJWK string) (string, error) {
		return mintCredentialJWE(publicJWK, "source", job.CredentialProvider, job.CredentialEnv, job.CredentialExpiresAtUnix)
	}
	mintDstJWE := func(publicJWK string) (string, error) {
		return mintCredentialJWE(publicJWK, "destination", job.CredentialProvider, job.CredentialEnv, job.CredentialExpiresAtUnix)
	}

	var srcJWE, dstJWE string
	srcOps := []agentSessionHostMsg{
		mergeCloudOp(cloudBase, agentSessionHostMsg{Op: "probe", ProbeAccess: "write"}),
		mergeCloudOp(cloudBase, agentSessionHostMsg{Op: "upload", Path: strings.TrimSpace(job.Source.Path)}),
	}
	// Do not run cloud cleanup inside the source session: destination must download first.
	sourceSessionFn := func() error {
		c, dialErr := cache.GetOrDial(user, job.Source.Record)
		if dialErr != nil {
			return dialErr
		}
		jwe, e := runHoneyTransferAgentSession(c, agentRemoteAbs, mintSrcJWE, srcOps)
		if e != nil {
			return e
		}
		if strings.TrimSpace(jwe) != "" {
			srcJWE = jwe
			redactions = append(redactions, transferRedactionsForCredentialMode(nil, jwe)...)
		}
		return nil
	}
	if err := runAgentSessionWithRetries("transfer_session_source", srcHost, retries, &events, emit, redactions, sourceSessionFn, evictSource); err != nil {
		return events, err
	}

	var dstOps []agentSessionHostMsg
	if agentTransferEndpointsMismatch(job, srcHost, dstHost) {
		dstOps = append(dstOps, mergeCloudOp(cloudBase, agentSessionHostMsg{Op: "probe", ProbeAccess: "read"}))
	}
	dstOps = append(dstOps, mergeCloudOp(cloudBase, agentSessionHostMsg{Op: "download", Path: strings.TrimSpace(job.Destination.Path)}))
	destSessionFn := func() error {
		c, dialErr := cache.GetOrDial(user, job.Destination.Record)
		if dialErr != nil {
			return dialErr
		}
		jwe, e := runHoneyTransferAgentSession(c, agentRemoteAbs, mintDstJWE, dstOps)
		if e != nil {
			return e
		}
		if strings.TrimSpace(jwe) != "" {
			dstJWE = jwe
			redactions = append(redactions, transferRedactionsForCredentialMode(nil, jwe)...)
		}
		return nil
	}
	if err := runAgentSessionWithRetries("transfer_session_destination", dstHost, retries, &events, emit, redactions, destSessionFn, evictDestination); err != nil {
		return events, err
	}

	if !job.KeepObject {
		cleanupOps := []agentSessionHostMsg{
			mergeCloudOp(cloudBase, agentSessionHostMsg{Op: "cleanup"}),
		}
		cleanupSessionFn := func() error {
			c, dialErr := cache.GetOrDial(user, job.Source.Record)
			if dialErr != nil {
				return dialErr
			}
			jwe, e := runHoneyTransferAgentSession(c, agentRemoteAbs, mintSrcJWE, cleanupOps)
			if e != nil {
				return e
			}
			if strings.TrimSpace(jwe) != "" {
				redactions = append(redactions, transferRedactionsForCredentialMode(nil, jwe)...)
			}
			return nil
		}
		// Match legacy behavior: best-effort delete of the staged object from the source side.
		_ = runAgentSessionWithRetries("cleanup_object", srcHost, 1, &events, emit, redactions, cleanupSessionFn, evictSource)
	}

	zap.L().Debug(
		"agent transfer credential envelopes minted",
		zap.String("credential_provider", strings.TrimSpace(job.CredentialProvider)),
		zap.Bool("has_source_jwe", strings.TrimSpace(srcJWE) != ""),
		zap.Bool("has_destination_jwe", strings.TrimSpace(dstJWE) != ""),
	)

	if c, err := cache.GetOrDial(user, job.Source.Record); err == nil {
		_, _ = c.Run("rm -f " + shellQuote(agentRemoteAbs))
	}
	if c, err := cache.GetOrDial(user, job.Destination.Record); err == nil {
		_, _ = c.Run("rm -f " + shellQuote(agentRemoteAbs))
	}
	stageEvent(&events, emit, redactions, "cleanup_agent", srcHost, true, "removed ephemeral agent", nil, 1)
	if dstHost != srcHost {
		stageEvent(&events, emit, redactions, "cleanup_agent", dstHost, true, "removed ephemeral agent", nil, 1)
	}
	zap.L().Debug(
		"agent transfer completed",
		zap.String("source_host", srcHost),
		zap.String("destination_host", dstHost),
		zap.Duration("elapsed", time.Since(transferStart)),
		zap.Int("event_count", len(events)),
	)
	return events, nil
}

// IsAgentTransferValidationError reports whether err is an AgentTransferValidationError (HTTP 400 class input).
// IsAgentTransferValidationError ...
func IsAgentTransferValidationError(err error) bool {
	var ve *AgentTransferValidationError
	return errors.As(err, &ve)
}
