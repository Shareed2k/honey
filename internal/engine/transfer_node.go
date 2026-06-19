package engine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
	"go.uber.org/zap"
)

// TransferNode abstracts a target node for agent execution and staging.
// This deepens the module by encapsulating transport-specific details (e.g. bash scripts, OS stat).
type TransferNode interface {
	// HostLabel returns the display label for the node.
	HostLabel() string

	// Record returns the underlying inventory record.
	Record() hosts.Record

	// StageAgent ensures the agent binary is staged on the node.
	// Returns a boolean indicating if an upload actually occurred, a reason string, and an error.
	StageAgent(ctx context.Context, localAgentPath, remoteAgentPath string) (uploaded bool, reason string, err error)

	// RunAgentSession starts the agent binary in session mode over the transport streams.
	RunAgentSession(ctx context.Context, agentPath string, mintJWE func(string) (string, error), ops []agentSessionHostMsg) (jwe string, err error)

	// CleanupAgent removes the ephemeral agent binary from the node.
	CleanupAgent(ctx context.Context, agentPath string) error

	// RunScript executes a raw script on the node (used for the fallback path).
	RunScript(ctx context.Context, script string) (string, error)
}

// HostClientTransferNode implements TransferNode by wrapping a generic HostClient.
type HostClientTransferNode struct {
	record hosts.Record
	client HostClient
	label  string
}

// NewHostClientTransferNode creates a new TransferNode around a HostClient.
func NewHostClientTransferNode(record hosts.Record, client HostClient) *HostClientTransferNode {
	return &HostClientTransferNode{
		record: record,
		client: client,
		label:  targetLabel(record),
	}
}

// HostLabel returns the label.
func (n *HostClientTransferNode) HostLabel() string {
	return n.label
}

// Record returns the record.
func (n *HostClientTransferNode) Record() hosts.Record {
	return n.record
}

// StageAgent stages the agent binary onto the node.
func (n *HostClientTransferNode) StageAgent(_ context.Context, localPath, remotePath string) (bool, string, error) {
	localInfo, err := os.Stat(strings.TrimSpace(localPath))
	if err != nil {
		return false, "", err
	}
	if localInfo.IsDir() {
		return false, "", fmt.Errorf("agent path is a directory: %s", localPath)
	}

	remoteInfo, err := n.client.StatRemote(strings.TrimSpace(remotePath))
	if err != nil {
		// Needs upload: stat failed
		err = n.client.Upload(localPath, remotePath)
		if err != nil {
			return false, "", err
		}
		return true, "remote stat missing; upload required", nil
	}
	if remoteInfo.IsDir {
		err = n.client.Upload(localPath, remotePath)
		if err != nil {
			return false, "", err
		}
		return true, "remote agent path is a directory; upload required", nil
	}
	if remoteInfo.Size == localInfo.Size() && remoteInfo.Size > 0 {
		localSHA, err := fileSHA256(strings.TrimSpace(localPath))
		if err != nil {
			err = n.client.Upload(localPath, remotePath)
			if err != nil {
				return false, "", err
			}
			return true, "local checksum failed; upload required", nil
		}
		remoteSHA, err := remoteFileSHA256(n.client, strings.TrimSpace(remotePath))
		if err != nil {
			err = n.client.Upload(localPath, remotePath)
			if err != nil {
				return false, "", err
			}
			return true, describeRemoteChecksumError(err), nil
		}
		if localSHA == remoteSHA {
			// Sizes and checksums match, upload skipped
			return false, "checksums match; upload skipped", nil
		}
		err = n.client.Upload(localPath, remotePath)
		if err != nil {
			return false, "", err
		}
		return true, "checksum mismatch; upload required", nil
	}

	// Sizes mismatch
	err = n.client.Upload(localPath, remotePath)
	if err != nil {
		return false, "", err
	}
	return true, "size mismatch; upload required", nil
}

// RunAgentSession runs the agent session.
func (n *HostClientTransferNode) RunAgentSession(_ context.Context, agentPath string, mintJWE func(string) (string, error), postBootstrap []agentSessionHostMsg) (string, error) {
	cmd := shellQuote(agentPath) + " session"

	stdoutR, stdoutW := io.Pipe()
	stdinR, stdinW := io.Pipe()
	var stderrBuf bytes.Buffer

	errCh := make(chan error, 1)
	go func() {
		errCh <- n.client.RunWithStreams(cmd, stdinR, stdoutW, &stderrBuf)
		_ = stdoutW.Close()
	}()

	waitRemote := func() error {
		_ = stdinW.Close()
		return <-errCh
	}

	br := bufio.NewReaderSize(stdoutR, 256*1024)
	readRes := func() (agentSessionWireResult, error) {
		line, rerr := readAgentSessionLine(br)
		if rerr != nil {
			return agentSessionWireResult{}, rerr
		}
		var res agentSessionWireResult
		if uerr := json.Unmarshal(line, &res); uerr != nil {
			return agentSessionWireResult{}, fmt.Errorf("parse result line: %w (line=%q)", uerr, shortenAgentSessionErr(string(line), 400))
		}
		return res, nil
	}

	keyLine, err := readAgentSessionLine(br)
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
	if key.Type != "key_ready" || key.PublicJWK == "" {
		_ = waitRemote()
		return "", fmt.Errorf("unexpected handshake: %+v", key)
	}

	jwe, err := mintJWE(key.PublicJWK)
	if err != nil {
		_ = waitRemote()
		return "", fmt.Errorf("mint credential jwe: %w", err)
	}

	initMsg := agentSessionHostMsg{
		Op:       "init",
		CredsJWE: jwe,
	}
	if err := writeAgentSessionLine(stdinW, initMsg); err != nil {
		_ = waitRemote()
		return "", fmt.Errorf("send init: %w", err)
	}

	res, err := readRes()
	if err != nil {
		_ = waitRemote()
		return "", fmt.Errorf("read init res: %w", err)
	}
	if !res.OK {
		_ = waitRemote()
		return "", fmt.Errorf("agent init failed: %s", res.Error)
	}

	for _, msg := range postBootstrap {
		if err := writeAgentSessionLine(stdinW, msg); err != nil {
			_ = waitRemote()
			return jwe, fmt.Errorf("send %s: %w", msg.Op, err)
		}
		res, err := readRes()
		if err != nil {
			_ = waitRemote()
			return jwe, fmt.Errorf("read %s res: %w", msg.Op, err)
		}
		if !res.OK {
			if strings.TrimSpace(res.Error) != "" {
				zap.L().Warn("agent session operation failed remotely", zap.String("op", msg.Op), zap.String("error", res.Error))
			}
			_ = waitRemote()
			return jwe, fmt.Errorf("agent %s failed: %s", msg.Op, res.Error)
		}
	}

	_ = writeAgentSessionLine(stdinW, agentSessionHostMsg{Op: "exit"})
	runErr := waitRemote()
	if runErr != nil && !strings.Contains(runErr.Error(), "exited with status") {
		return jwe, fmt.Errorf("agent session exit: %w", runErr)
	}
	return jwe, nil
}

// CleanupAgent removes the ephemeral agent binary.
func (n *HostClientTransferNode) CleanupAgent(_ context.Context, agentPath string) error {
	_, err := n.client.Run("rm -f " + shellQuote(agentPath))
	return err
}

// RunScript executes a script.
func (n *HostClientTransferNode) RunScript(_ context.Context, script string) (string, error) {
	out, err := n.client.Run(script)
	return string(out), err
}
