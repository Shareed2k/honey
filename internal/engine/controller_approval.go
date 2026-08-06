package engine

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"strings"
)

// approver decides a controller's human-approval request. It is a seam (like the
// chatAgent and the step-runner): production prompts the operator on stdin, tests
// script the decision.
type approver interface {
	approve(ctx context.Context, req approvalRequest) approvalDecision
}

type approvalRequest struct {
	Action string // what the model wants to do (LLM-provided)
	Reason string // why (LLM-provided, optional)
}

type approvalDecision struct {
	Approved bool
	Note     string
}

// stdinApprover prompts the operator on stderr and reads y/N from stdin. When
// stdin is not a terminal (piped / CI / honey web), it auto-denies — an
// unattended controller must not silently proceed through a human gate.
type stdinApprover struct {
	in     *os.File
	reader *bufio.Reader
	out    io.Writer
}

func newStdinApprover() *stdinApprover {
	return &stdinApprover{in: os.Stdin, reader: bufio.NewReader(os.Stdin), out: os.Stderr}
}

func (a *stdinApprover) approve(_ context.Context, req approvalRequest) approvalDecision {
	if a.in != nil {
		if fi, err := a.in.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			return approvalDecision{Approved: false, Note: "non-interactive stdin: auto-denied"}
		}
	}
	fmt.Fprintf(a.out, "\n[controller] approval requested: %s\n", req.Action)
	if strings.TrimSpace(req.Reason) != "" {
		fmt.Fprintf(a.out, "  reason: %s\n", req.Reason)
	}
	fmt.Fprint(a.out, "  approve? [y/N]: ")
	line, _ := a.reader.ReadString('\n')
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return approvalDecision{Approved: true, Note: "operator approved"}
	default:
		return approvalDecision{Approved: false, Note: "operator denied"}
	}
}
