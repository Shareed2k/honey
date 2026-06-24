package engine

import (
	"bufio"
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

func readAgentSessionLine(r *bufio.Reader) ([]byte, error) {
	part, err := r.ReadBytes('\n')
	if len(part) > honeyAgentSessionMaxLine {
		return nil, fmt.Errorf("line exceeds maximum size %d", honeyAgentSessionMaxLine)
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
		zap.L().Debug(
			"agent transfer session attempt start",
			zap.String("stage", stage),
			zap.String("host", host),
			zap.Int("attempt", i),
			zap.Int("max_attempts", attempts),
		)
		stageEvent(out, emit, redactions, stage+"_start", host, true, "starting", nil, i)
		errCh := make(chan error, 1)
		go func() {
			// Recover so a panic in fn surfaces as an error instead of leaving
			// the progress-ticker select loop below spinning forever.
			defer func() {
				if r := recover(); r != nil {
					errCh <- fmt.Errorf("%s panicked: %v", stage, r)
				}
			}()
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
				zap.L().Debug(
					"agent transfer session attempt progress",
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
			zap.L().Debug(
				"agent transfer session attempt success",
				zap.String("stage", stage),
				zap.String("host", host),
				zap.Int("attempt", i),
				zap.Duration("elapsed", time.Since(attemptStart)),
			)
			stageEvent(out, emit, redactions, stage, host, true, "ok", nil, i)
			return nil
		}
		lastErr = runErr
		zap.L().Warn(
			"agent transfer session attempt failed",
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
