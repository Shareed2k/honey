package engine

import (
	"errors"
	"strings"
	"testing"
)

func TestRunReporter_renderHTML(t *testing.T) {
	reporter := NewRunReporter("localhost", 25, "", "")

	results := []HostExecResult{
		{Name: "host1", StepIndex: 1, Success: true, Output: "ok"},
		{Name: "host2", StepIndex: 1, Success: false, ErrMsg: "failed to connect"},
	}

	htmlOut := reporter.renderHTML("test-recipe", "FAILED", results, errors.New("recipe run failed"))

	if !strings.Contains(htmlOut, "test-recipe") {
		t.Errorf("expected HTML to contain recipe name")
	}
	if !strings.Contains(htmlOut, "FAILED") {
		t.Errorf("expected HTML to contain FAILED status")
	}
	if !strings.Contains(htmlOut, "host1") || !strings.Contains(htmlOut, "host2") {
		t.Errorf("expected HTML to contain host names")
	}
	if !strings.Contains(htmlOut, "failed to connect") {
		t.Errorf("expected HTML to contain error message")
	}
}

func TestRunReporter_renderLogsHTML(t *testing.T) {
	reporter := NewRunReporter("localhost", 25, "", "")

	results := []HostExecResult{
		{Name: "host1", StepIndex: 1, Success: true, Output: "stdout details"},
	}

	logsOut := reporter.renderLogsHTML(results)

	if !strings.Contains(logsOut, "stdout details") {
		t.Errorf("expected logs HTML to contain stdout output")
	}
}
