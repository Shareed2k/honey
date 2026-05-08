package ui

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	jose "github.com/go-jose/go-jose/v4"
	"github.com/shareed2k/honey/internal/hosts"
	"go.uber.org/zap"
)

// AgentTransferEndpoint identifies one source/destination endpoint for transfer.
type AgentTransferEndpoint struct {
	Record hosts.Record `json:"record"`
	Path   string       `json:"path"`
}

// AgentCloudBackend describes the cloud object target path for staging.
type AgentCloudBackend struct {
	Provider string `json:"provider"`
	Bucket   string `json:"bucket"`
	Prefix   string `json:"prefix,omitempty"`
	Object   string `json:"object,omitempty"`
	Region   string `json:"region,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

// AgentTransferJob describes one A->cloud->B transfer orchestration request.
type AgentTransferJob struct {
	SSHUser                 string                `json:"ssh_user"`
	AgentLocalPath          string                `json:"agent_local_path"`
	SourceAgentLocalPath    string                `json:"source_agent_local_path,omitempty"`
	DestAgentLocalPath      string                `json:"dest_agent_local_path,omitempty"`
	AgentRemoteDir          string                `json:"agent_remote_dir,omitempty"`
	Source                  AgentTransferEndpoint `json:"source"`
	Destination             AgentTransferEndpoint `json:"destination"`
	Cloud                   AgentCloudBackend     `json:"cloud"`
	CredentialProvider      string                `json:"credential_provider,omitempty"`
	CredentialEnv           map[string]string     `json:"credential_env,omitempty"`
	CredentialExpiresAtUnix int64                 `json:"credential_expires_at_unix,omitempty"`
	KeepObject              bool                  `json:"keep_object,omitempty"`
	MaxRetries              int                   `json:"max_retries,omitempty"`
}

// AgentTransferEvent is emitted for each orchestration stage.
type AgentTransferEvent struct {
	Stage     string    `json:"stage"`
	Host      string    `json:"host,omitempty"`
	Success   bool      `json:"success"`
	Message   string    `json:"message,omitempty"`
	Error     string    `json:"error,omitempty"`
	Attempt   int       `json:"attempt,omitempty"`
	Timestamp time.Time `json:"timestamp"`
}

// AgentTransferValidationError indicates user/input issues (HTTP 400).
type AgentTransferValidationError struct {
	msg string
}

func (e *AgentTransferValidationError) Error() string {
	return e.msg
}

func newAgentTransferValidationError(format string, args ...any) error {
	return &AgentTransferValidationError{msg: fmt.Sprintf(format, args...)}
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

func envPrefix(env map[string]string) string {
	if len(env) == 0 {
		return ""
	}
	var b strings.Builder
	for k, v := range env {
		k = strings.TrimSpace(k)
		if k == "" {
			continue
		}
		b.WriteString("export ")
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(shellQuote(v))
		b.WriteString("; ")
	}
	return b.String()
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
	cmd := "if command -v sha256sum >/dev/null 2>&1; then sha256sum " + shellQuote(remotePath) + " | awk '{print $1}'; " +
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

func buildAgentCommand(agentPath, operation string, args map[string]string, env map[string]string) string {
	var b strings.Builder
	b.WriteString(envPrefix(env))
	b.WriteString(shellQuote(agentPath))
	b.WriteByte(' ')
	b.WriteString(shellQuote(operation))
	for k, v := range args {
		if strings.TrimSpace(v) == "" {
			continue
		}
		b.WriteByte(' ')
		b.WriteString("--")
		b.WriteString(k)
		b.WriteByte(' ')
		b.WriteString(shellQuote(v))
	}
	return b.String()
}

type agentKeygenResult struct {
	KID            string `json:"kid"`
	PublicJWK      string `json:"public_jwk"`
	PrivateKeyFile string `json:"private_key_file"`
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

func runWithRetries(client HostClient, stage, host string, attempts int, out *[]AgentTransferEvent, emit func(AgentTransferEvent), redactions []string, cmd string) error {
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 1; i <= attempts; i++ {
		attemptStart := time.Now()
		zap.L().Debug("agent transfer command attempt start",
			zap.String("stage", stage),
			zap.String("host", host),
			zap.Int("attempt", i),
			zap.Int("max_attempts", attempts),
		)
		stageEvent(out, emit, redactions, stage+"_start", host, true, "starting", nil, i)
		type runResult struct {
			raw []byte
			err error
		}
		resCh := make(chan runResult, 1)
		go func() {
			raw, err := client.Run(cmd)
			resCh <- runResult{raw: raw, err: err}
		}()
		ticker := time.NewTicker(5 * time.Second)
		var res runResult
		running := true
		for running {
			select {
			case res = <-resCh:
				running = false
			case <-ticker.C:
				zap.L().Debug("agent transfer command attempt progress",
					zap.String("stage", stage),
					zap.String("host", host),
					zap.Int("attempt", i),
					zap.Duration("elapsed", time.Since(attemptStart)),
				)
				stageEvent(out, emit, redactions, stage+"_progress", host, true, "still running", nil, i)
			}
		}
		ticker.Stop()
		raw, err := res.raw, res.err
		if err == nil {
			msg := strings.TrimSpace(string(raw))
			if msg == "" {
				msg = "ok"
			}
			zap.L().Debug("agent transfer command attempt success",
				zap.String("stage", stage),
				zap.String("host", host),
				zap.Int("attempt", i),
				zap.Duration("elapsed", time.Since(attemptStart)),
				zap.Int("output_len", len(msg)),
			)
			stageEvent(out, emit, redactions, stage, host, true, msg, nil, i)
			return nil
		}
		lastErr = err
		msg := strings.TrimSpace(string(raw))
		zap.L().Warn("agent transfer command attempt failed",
			zap.String("stage", stage),
			zap.String("host", host),
			zap.Int("attempt", i),
			zap.Duration("elapsed", time.Since(attemptStart)),
			zap.Int("output_len", len(msg)),
			zap.Error(err),
		)
		stageEvent(out, emit, redactions, stage, host, false, msg, err, i)
	}
	return lastErr
}

func runUploadWithHeartbeat(
	out *[]AgentTransferEvent,
	emit func(AgentTransferEvent),
	redactions []string,
	stage string,
	host string,
	attempt int,
	uploadFn func() error,
) error {
	uploadStart := time.Now()
	zap.L().Debug("agent transfer upload start",
		zap.String("stage", stage),
		zap.String("host", host),
		zap.Int("attempt", attempt),
	)
	stageEvent(out, emit, redactions, stage+"_start", host, true, "starting", nil, attempt)
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
				zap.L().Warn("agent transfer upload failed",
					zap.String("stage", stage),
					zap.String("host", host),
					zap.Int("attempt", attempt),
					zap.Duration("elapsed", time.Since(uploadStart)),
					zap.Error(err),
				)
			} else {
				zap.L().Debug("agent transfer upload success",
					zap.String("stage", stage),
					zap.String("host", host),
					zap.Int("attempt", attempt),
					zap.Duration("elapsed", time.Since(uploadStart)),
				)
			}
			return err
		case <-ticker.C:
			zap.L().Debug("agent transfer upload progress",
				zap.String("stage", stage),
				zap.String("host", host),
				zap.Int("attempt", attempt),
				zap.Duration("elapsed", time.Since(uploadStart)),
			)
			stageEvent(out, emit, redactions, stage+"_progress", host, true, "still uploading", nil, attempt)
		}
	}
}

// ExecuteAgentCloudTransfer orchestrates source upload and destination download using
// ephemeral transfer agents over existing HostClient connections (SSH / k8s pod exec abstraction).
func ExecuteAgentCloudTransfer(job AgentTransferJob, cache *ClientCache) ([]AgentTransferEvent, error) {
	return ExecuteAgentCloudTransferWithEmit(job, cache, nil)
}

// ExecuteAgentCloudTransferWithEmit runs the transfer and calls emit for each event.
func ExecuteAgentCloudTransferWithEmit(job AgentTransferJob, cache *ClientCache, emit func(AgentTransferEvent)) ([]AgentTransferEvent, error) {
	var events []AgentTransferEvent
	transferStart := time.Now()
	user := strings.TrimSpace(job.SSHUser)
	if user == "" {
		user = strings.TrimSpace(os.Getenv("USER"))
	}
	if user == "" {
		user = "root"
	}
	if strings.TrimSpace(job.Source.Record.PrimaryIP) == "" && !(job.Source.Record.Provider == "k8s" && strings.EqualFold(job.Source.Record.Meta["kind"], "pod")) {
		return events, newAgentTransferValidationError("source record has no connectable target")
	}
	if strings.TrimSpace(job.Destination.Record.PrimaryIP) == "" && !(job.Destination.Record.Provider == "k8s" && strings.EqualFold(job.Destination.Record.Meta["kind"], "pod")) {
		return events, newAgentTransferValidationError("destination record has no connectable target")
	}
	if strings.TrimSpace(job.AgentLocalPath) == "" {
		return events, newAgentTransferValidationError("agent_local_path is required")
	}
	if strings.TrimSpace(job.Source.Path) == "" {
		return events, newAgentTransferValidationError("source.path is required")
	}
	if strings.TrimSpace(job.Destination.Path) == "" {
		return events, newAgentTransferValidationError("destination.path is required")
	}
	if strings.TrimSpace(job.CredentialProvider) == "" {
		return events, newAgentTransferValidationError("credential_provider is required")
	}
	if len(job.CredentialEnv) == 0 {
		return events, newAgentTransferValidationError("credential_env is required")
	}
	if strings.TrimSpace(job.Cloud.Provider) == "" || strings.TrimSpace(job.Cloud.Bucket) == "" {
		return events, newAgentTransferValidationError("cloud.provider and cloud.bucket are required")
	}
	objectKey := strings.TrimSpace(job.Cloud.Object)
	if objectKey == "" {
		return events, newAgentTransferValidationError("cloud.object is required")
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
	zap.L().Debug("agent transfer begin",
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

	agentRemotePath := remoteAgentPath(job.AgentRemoteDir)
	srcAgentPath := strings.TrimSpace(job.SourceAgentLocalPath)
	if srcAgentPath == "" {
		srcAgentPath = strings.TrimSpace(job.AgentLocalPath)
	}
	dstAgentPath := strings.TrimSpace(job.DestAgentLocalPath)
	if dstAgentPath == "" {
		dstAgentPath = strings.TrimSpace(job.AgentLocalPath)
	}
	stageEvent(&events, emit, redactions, "agent_binary_source", srcHost, true, fmt.Sprintf("%s (provider=%s compression=%s)", srcAgentPath, agentProviderFlavor(srcAgentPath), agentCompressionMode(srcAgentPath)), nil, 1)
	stageEvent(&events, emit, redactions, "agent_binary_destination", dstHost, true, fmt.Sprintf("%s (provider=%s compression=%s)", dstAgentPath, agentProviderFlavor(dstAgentPath), agentCompressionMode(dstAgentPath)), nil, 1)
	srcNeedsUpload, srcReason, err := shouldUploadAgent(srcClient, srcAgentPath, agentRemotePath)
	if err != nil {
		stageEvent(&events, emit, redactions, "stage_agent_source", srcHost, false, "", err, 1)
		return events, err
	}
	if srcNeedsUpload {
		zap.L().Debug("agent transfer source stage requires upload",
			zap.String("host", srcHost),
			zap.String("reason", srcReason),
		)
		if err := runUploadWithHeartbeat(&events, emit, redactions, "stage_agent_source", srcHost, 1, func() error {
			return srcClient.Upload(srcAgentPath, agentRemotePath)
		}); err != nil {
			stageEvent(&events, emit, redactions, "stage_agent_source", srcHost, false, "", err, 1)
			return events, err
		}
		stageEvent(&events, emit, redactions, "stage_agent_source", srcHost, true, agentRemotePath, nil, 1)
	} else {
		zap.L().Debug("agent transfer source stage skipped upload",
			zap.String("host", srcHost),
			zap.String("reason", srcReason),
		)
		stageEvent(&events, emit, redactions, "stage_agent_source", srcHost, true, srcReason, nil, 1)
	}
	if srcHost != dstHost || job.Source.Record.PrimaryIP != job.Destination.Record.PrimaryIP || job.Source.Record.Provider != job.Destination.Record.Provider {
		dstNeedsUpload, dstReason, err := shouldUploadAgent(dstClient, dstAgentPath, agentRemotePath)
		if err != nil {
			stageEvent(&events, emit, redactions, "stage_agent_destination", dstHost, false, "", err, 1)
			return events, err
		}
		if dstNeedsUpload {
			zap.L().Debug("agent transfer destination stage requires upload",
				zap.String("host", dstHost),
				zap.String("reason", dstReason),
			)
			if err := runUploadWithHeartbeat(&events, emit, redactions, "stage_agent_destination", dstHost, 1, func() error {
				return dstClient.Upload(dstAgentPath, agentRemotePath)
			}); err != nil {
				stageEvent(&events, emit, redactions, "stage_agent_destination", dstHost, false, "", err, 1)
				return events, err
			}
			stageEvent(&events, emit, redactions, "stage_agent_destination", dstHost, true, agentRemotePath, nil, 1)
		} else {
			zap.L().Debug("agent transfer destination stage skipped upload",
				zap.String("host", dstHost),
				zap.String("reason", dstReason),
			)
			stageEvent(&events, emit, redactions, "stage_agent_destination", dstHost, true, dstReason, nil, 1)
		}
	}
	if srcHost == dstHost && job.Source.Record.PrimaryIP == job.Destination.Record.PrimaryIP && job.Source.Record.Provider == job.Destination.Record.Provider {
		stageEvent(&events, emit, redactions, "stage_agent_destination", dstHost, true, "same host as source; skipped duplicate stage", nil, 1)
	}
	_, _ = srcClient.Run("chmod +x " + shellQuote(agentRemotePath))
	_, _ = dstClient.Run("chmod +x " + shellQuote(agentRemotePath))

	baseEnv := map[string]string{
		"HONEY_TRANSFER_PROVIDER": job.Cloud.Provider,
		"HONEY_TRANSFER_BUCKET":   job.Cloud.Bucket,
		"HONEY_TRANSFER_OBJECT":   objectKey,
		"HONEY_TRANSFER_REGION":   job.Cloud.Region,
		"HONEY_TRANSFER_ENDPOINT": job.Cloud.Endpoint,
	}
	keygenCmd := buildAgentCommand(agentRemotePath, "keygen", map[string]string{
		"key_dir": strings.TrimSpace(job.AgentRemoteDir),
	}, nil)
	sourceKeyRaw, err := srcClient.Run(keygenCmd)
	if err != nil {
		stageEvent(&events, emit, redactions, "keygen_source", srcHost, false, "", err, 1)
		return events, err
	}
	var sourceKey agentKeygenResult
	if err := json.Unmarshal(sourceKeyRaw, &sourceKey); err != nil {
		stageEvent(&events, emit, redactions, "keygen_source", srcHost, false, "", err, 1)
		return events, fmt.Errorf("parse source keygen: %w", err)
	}
	stageEvent(&events, emit, redactions, "keygen_source", srcHost, true, "ok", nil, 1)
	destKey := sourceKey
	if srcHost != dstHost || job.Source.Record.PrimaryIP != job.Destination.Record.PrimaryIP || job.Source.Record.Provider != job.Destination.Record.Provider {
		destKeyRaw, derr := dstClient.Run(keygenCmd)
		if derr != nil {
			stageEvent(&events, emit, redactions, "keygen_destination", dstHost, false, "", derr, 1)
			return events, derr
		}
		if err := json.Unmarshal(destKeyRaw, &destKey); err != nil {
			stageEvent(&events, emit, redactions, "keygen_destination", dstHost, false, "", err, 1)
			return events, fmt.Errorf("parse destination keygen: %w", err)
		}
		stageEvent(&events, emit, redactions, "keygen_destination", dstHost, true, "ok", nil, 1)
	} else {
		stageEvent(&events, emit, redactions, "keygen_destination", dstHost, true, "same host as source; key reused", nil, 1)
	}
	srcJWE, err := mintCredentialJWE(sourceKey.PublicJWK, "source", job.CredentialProvider, job.CredentialEnv, job.CredentialExpiresAtUnix)
	if err != nil {
		stageEvent(&events, emit, redactions, "encrypt_credentials_source", srcHost, false, "", err, 1)
		return events, err
	}
	dstJWE, err := mintCredentialJWE(destKey.PublicJWK, "destination", job.CredentialProvider, job.CredentialEnv, job.CredentialExpiresAtUnix)
	if err != nil {
		stageEvent(&events, emit, redactions, "encrypt_credentials_destination", dstHost, false, "", err, 1)
		return events, err
	}
	redactions = append(redactions, transferRedactionsForCredentialMode(job.CredentialEnv, srcJWE, dstJWE)...)
	zap.L().Debug("agent transfer environment prepared",
		zap.String("credential_provider", strings.TrimSpace(job.CredentialProvider)),
		zap.Bool("has_source_jwe", strings.TrimSpace(srcJWE) != ""),
		zap.Bool("has_destination_jwe", strings.TrimSpace(dstJWE) != ""),
	)
	retries := job.MaxRetries
	if retries <= 0 {
		retries = 2
	}
	srcEnv := map[string]string{}
	for k, v := range baseEnv {
		srcEnv[k] = v
	}
	srcEnv["HONEY_TRANSFER_CREDS_JWE"] = srcJWE
	srcEnv["HONEY_TRANSFER_KEY_FILE"] = strings.TrimSpace(sourceKey.PrivateKeyFile)
	dstEnv := map[string]string{}
	for k, v := range baseEnv {
		dstEnv[k] = v
	}
	dstEnv["HONEY_TRANSFER_CREDS_JWE"] = dstJWE
	dstEnv["HONEY_TRANSFER_KEY_FILE"] = strings.TrimSpace(destKey.PrivateKeyFile)

	cloudProbeSourceCmd := buildAgentCommand(agentRemotePath, "probe", map[string]string{
		"probe_access": "write",
		"provider":     job.Cloud.Provider,
		"bucket":       job.Cloud.Bucket,
		"object":       objectKey,
		"region":       job.Cloud.Region,
		"endpoint":     job.Cloud.Endpoint,
	}, srcEnv)
	if err := runWithRetries(srcClient, "cloud_probe_source", srcHost, 1, &events, emit, redactions, cloudProbeSourceCmd); err != nil {
		return events, err
	}
	if srcHost != dstHost || job.Source.Record.PrimaryIP != job.Destination.Record.PrimaryIP || job.Source.Record.Provider != job.Destination.Record.Provider {
		cloudProbeDestinationCmd := buildAgentCommand(agentRemotePath, "probe", map[string]string{
			"probe_access": "read",
			"provider":     job.Cloud.Provider,
			"bucket":       job.Cloud.Bucket,
			"object":       objectKey,
			"region":       job.Cloud.Region,
			"endpoint":     job.Cloud.Endpoint,
		}, dstEnv)
		if err := runWithRetries(dstClient, "cloud_probe_destination", dstHost, 1, &events, emit, redactions, cloudProbeDestinationCmd); err != nil {
			return events, err
		}
	}

	uploadCmd := buildAgentCommand(agentRemotePath, "upload", map[string]string{
		"path":     strings.TrimSpace(job.Source.Path),
		"provider": job.Cloud.Provider,
		"bucket":   job.Cloud.Bucket,
		"object":   objectKey,
		"region":   job.Cloud.Region,
		"endpoint": job.Cloud.Endpoint,
	}, srcEnv)
	if err := runWithRetries(srcClient, "upload", srcHost, retries, &events, emit, redactions, uploadCmd); err != nil {
		return events, err
	}

	downloadCmd := buildAgentCommand(agentRemotePath, "download", map[string]string{
		"path":     strings.TrimSpace(job.Destination.Path),
		"provider": job.Cloud.Provider,
		"bucket":   job.Cloud.Bucket,
		"object":   objectKey,
		"region":   job.Cloud.Region,
		"endpoint": job.Cloud.Endpoint,
	}, dstEnv)
	if err := runWithRetries(dstClient, "download", dstHost, retries, &events, emit, redactions, downloadCmd); err != nil {
		return events, err
	}

	if !job.KeepObject {
		cleanupObjectCmd := buildAgentCommand(agentRemotePath, "cleanup", map[string]string{
			"provider": job.Cloud.Provider,
			"bucket":   job.Cloud.Bucket,
			"object":   objectKey,
			"region":   job.Cloud.Region,
			"endpoint": job.Cloud.Endpoint,
		}, srcEnv)
		_ = runWithRetries(srcClient, "cleanup_object", srcHost, 1, &events, emit, redactions, cleanupObjectCmd)
	}
	_, _ = srcClient.Run("rm -f " + shellQuote(agentRemotePath))
	_, _ = dstClient.Run("rm -f " + shellQuote(agentRemotePath))
	if strings.TrimSpace(sourceKey.PrivateKeyFile) != "" {
		_, _ = srcClient.Run("rm -f " + shellQuote(sourceKey.PrivateKeyFile))
	}
	if strings.TrimSpace(destKey.PrivateKeyFile) != "" && (srcHost != dstHost || sourceKey.PrivateKeyFile != destKey.PrivateKeyFile) {
		_, _ = dstClient.Run("rm -f " + shellQuote(destKey.PrivateKeyFile))
	}
	stageEvent(&events, emit, redactions, "cleanup_agent", srcHost, true, "removed ephemeral agent", nil, 1)
	if dstHost != srcHost {
		stageEvent(&events, emit, redactions, "cleanup_agent", dstHost, true, "removed ephemeral agent", nil, 1)
	}
	zap.L().Debug("agent transfer completed",
		zap.String("source_host", srcHost),
		zap.String("destination_host", dstHost),
		zap.Duration("elapsed", time.Since(transferStart)),
		zap.Int("event_count", len(events)),
	)
	return events, nil
}

func IsAgentTransferValidationError(err error) bool {
	var ve *AgentTransferValidationError
	return errors.As(err, &ve)
}
