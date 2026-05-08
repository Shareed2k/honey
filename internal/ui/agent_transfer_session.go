package ui

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"go.uber.org/zap"
)

const (
	honeyAgentSessionMaxLine         = 32 << 20
	honeyAgentSessionProtocolVersion = 1
)

type agentSessionKeyReady struct {
	Type      string `json:"type"`
	Protocol  int    `json:"protocol"`
	KID       string `json:"kid"`
	PublicJWK string `json:"public_jwk"`
}

type agentSessionWireResult struct {
	OK     bool   `json:"ok"`
	Error  string `json:"error,omitempty"`
	Detail string `json:"detail,omitempty"`
}

type agentSessionHostMsg struct {
	Op          string `json:"op"`
	CredsJWE    string `json:"creds_jwe,omitempty"`
	ProbeAccess string `json:"probe_access,omitempty"`
	Path        string `json:"path,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Bucket      string `json:"bucket,omitempty"`
	Object      string `json:"object,omitempty"`
	Region      string `json:"region,omitempty"`
	Endpoint    string `json:"endpoint,omitempty"`
}

func writeAgentSessionLine(w io.Writer, v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := w.Write(b); err != nil {
		return err
	}
	_, err = w.Write([]byte{'\n'})
	return err
}

func readAgentSessionLine(r *bufio.Reader, maxBytes int) ([]byte, error) {
	part, err := r.ReadBytes('\n')
	if len(part) > maxBytes {
		return nil, fmt.Errorf("line exceeds maximum size %d", maxBytes)
	}
	if err != nil {
		if err == io.EOF && len(part) > 0 {
			return part, nil
		}
		return nil, err
	}
	return part, nil
}

func mergeCloudOp(base agentSessionHostMsg, op agentSessionHostMsg) agentSessionHostMsg {
	op.Provider = base.Provider
	op.Bucket = base.Bucket
	op.Object = base.Object
	op.Region = base.Region
	op.Endpoint = base.Endpoint
	return op
}

func shortenAgentSessionErr(s string, maxRunes int) string {
	if maxRunes <= 0 {
		maxRunes = 240
	}
	s = strings.TrimSpace(s)
	if len(s) <= maxRunes {
		return s
	}
	return s[:maxRunes] + "…"
}

func outdatedAgentSessionError(runErr error, stderr string) error {
	msg := "remote transfer agent does not support session protocol v1; please re-stage or upgrade honey-transfer-agent on target host"
	se := strings.TrimSpace(stderr)
	if se == "" {
		if runErr != nil {
			return fmt.Errorf("%s (%v)", msg, runErr)
		}
		return errors.New(msg)
	}
	return fmt.Errorf("%s (stderr=%s)", msg, shortenAgentSessionErr(se, 500))
}

// runHoneyTransferAgentSession runs one remote agent process in "session" mode: ephemeral ECDH
// private key stays in the agent process memory only (no key file). Protocol is newline-delimited JSON.
func runHoneyTransferAgentSession(
	client HostClient,
	agentRemotePath string,
	mintJWE func(publicJWK string) (string, error),
	postBootstrap []agentSessionHostMsg,
) (mintedJWE string, err error) {
	cmd := shellQuote(agentRemotePath) + " session"

	stdoutR, stdoutW := io.Pipe()
	stdinR, stdinW := io.Pipe()
	var stderrBuf bytes.Buffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- client.RunWithStreams(cmd, stdinR, stdoutW, &stderrBuf)
		_ = stdoutW.Close()
	}()

	waitRemote := func() error {
		_ = stdinW.Close()
		return <-errCh
	}

	br := bufio.NewReaderSize(stdoutR, 256*1024)
	readRes := func() (agentSessionWireResult, error) {
		line, rerr := readAgentSessionLine(br, honeyAgentSessionMaxLine)
		if rerr != nil {
			return agentSessionWireResult{}, rerr
		}
		var res agentSessionWireResult
		if uerr := json.Unmarshal(line, &res); uerr != nil {
			return agentSessionWireResult{}, fmt.Errorf("parse result line: %w (line=%q)", uerr, shortenAgentSessionErr(string(line), 400))
		}
		return res, nil
	}

	keyLine, err := readAgentSessionLine(br, honeyAgentSessionMaxLine)
	if err != nil {
		runErr := waitRemote()
		se := strings.TrimSpace(stderrBuf.String())
		if errors.Is(err, io.EOF) {
			return "", outdatedAgentSessionError(runErr, se)
		}
		return "", fmt.Errorf("read key line: %w", err)
	}
	var key agentSessionKeyReady
	if err := json.Unmarshal(keyLine, &key); err != nil {
		_ = waitRemote()
		return "", fmt.Errorf("parse key line: %w", err)
	}
	if strings.TrimSpace(key.Type) != "key_ready" {
		_ = waitRemote()
		return "", fmt.Errorf("invalid session key line type %q", key.Type)
	}
	if key.Protocol != honeyAgentSessionProtocolVersion {
		_ = waitRemote()
		return "", fmt.Errorf(
			"%w (got_protocol=%d expected_protocol=%d)",
			outdatedAgentSessionError(nil, stderrBuf.String()),
			key.Protocol,
			honeyAgentSessionProtocolVersion,
		)
	}
	if strings.TrimSpace(key.PublicJWK) == "" {
		_ = waitRemote()
		return "", fmt.Errorf("missing public_jwk in key line")
	}
	jwe, err := mintJWE(key.PublicJWK)
	if err != nil {
		_ = waitRemote()
		return "", err
	}
	mintedJWE = jwe
	bootstrap := agentSessionHostMsg{Op: "bootstrap", CredsJWE: jwe}
	if err := writeAgentSessionLine(stdinW, bootstrap); err != nil {
		_ = waitRemote()
		return mintedJWE, err
	}
	bootRes, err := readRes()
	if err != nil {
		_ = waitRemote()
		return mintedJWE, err
	}
	if !bootRes.OK {
		_ = waitRemote()
		return mintedJWE, fmt.Errorf("bootstrap: %s", bootRes.Error)
	}
	for _, op := range postBootstrap {
		op := op
		if strings.TrimSpace(op.Op) == "" {
			continue
		}
		if err := writeAgentSessionLine(stdinW, op); err != nil {
			_ = waitRemote()
			return mintedJWE, err
		}
		opRes, err := readRes()
		if err != nil {
			_ = waitRemote()
			return mintedJWE, err
		}
		if !opRes.OK {
			_ = waitRemote()
			return mintedJWE, fmt.Errorf("%s: %s", strings.TrimSpace(op.Op), opRes.Error)
		}
	}
	if err := writeAgentSessionLine(stdinW, agentSessionHostMsg{Op: "close"}); err != nil {
		_ = waitRemote()
		return mintedJWE, err
	}
	closeAck, err := readRes()
	if err != nil {
		_ = waitRemote()
		return mintedJWE, err
	}
	if !closeAck.OK {
		_ = waitRemote()
		return mintedJWE, fmt.Errorf("close: %s", closeAck.Error)
	}
	_, _ = io.Copy(io.Discard, br)

	_ = stdinW.Close()
	runErr := <-errCh
	if runErr != nil {
		se := strings.TrimSpace(stderrBuf.String())
		if se != "" {
			return mintedJWE, fmt.Errorf("remote session: %w (stderr=%s)", runErr, se)
		}
		return mintedJWE, fmt.Errorf("remote session: %w", runErr)
	}
	return mintedJWE, nil
}

// evictOnTransientRetry is optional; when non-nil it is called with the failed attempt
// index before the next attempt after a failure classified as IsSSHConnTransientError.
func runAgentSessionWithRetries(
	stage, host string,
	attempts int,
	out *[]AgentTransferEvent,
	emit func(AgentTransferEvent),
	redactions []string,
	fn func() error,
	evictOnTransientRetry func(failedAttempt int),
) error {
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 1; i <= attempts; i++ {
		attemptStart := time.Now()
		zap.L().Debug("agent transfer session attempt start",
			zap.String("stage", stage),
			zap.String("host", host),
			zap.Int("attempt", i),
			zap.Int("max_attempts", attempts),
		)
		stageEvent(out, emit, redactions, stage+"_start", host, true, "starting", nil, i)
		errCh := make(chan error, 1)
		go func() {
			errCh <- fn()
		}()
		ticker := time.NewTicker(5 * time.Second)
		var runErr error
		running := true
		for running {
			select {
			case runErr = <-errCh:
				running = false
			case <-ticker.C:
				zap.L().Debug("agent transfer session attempt progress",
					zap.String("stage", stage),
					zap.String("host", host),
					zap.Int("attempt", i),
					zap.Duration("elapsed", time.Since(attemptStart)),
				)
				stageEvent(out, emit, redactions, stage+"_progress", host, true, "still running", nil, i)
			}
		}
		ticker.Stop()
		if runErr == nil {
			zap.L().Debug("agent transfer session attempt success",
				zap.String("stage", stage),
				zap.String("host", host),
				zap.Int("attempt", i),
				zap.Duration("elapsed", time.Since(attemptStart)),
			)
			stageEvent(out, emit, redactions, stage, host, true, "ok", nil, i)
			return nil
		}
		lastErr = runErr
		zap.L().Warn("agent transfer session attempt failed",
			zap.String("stage", stage),
			zap.String("host", host),
			zap.Int("attempt", i),
			zap.Duration("elapsed", time.Since(attemptStart)),
			zap.Error(runErr),
		)
		stageEvent(out, emit, redactions, stage, host, false, "", runErr, i)
		if i < attempts && evictOnTransientRetry != nil && IsSSHConnTransientError(runErr) {
			evictOnTransientRetry(i)
		}
	}
	return lastErr
}
