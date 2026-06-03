// Package snippets stores reusable web-UI exec snippets (saved commands/scripts)
// behind a pluggable Store interface. v1 ships a local JSON-file backend; the
// interface is the seam for future backends (git, GCS, ...).
package snippets

import (
	"context"
	"errors"
)

// ErrNotFound is returned by Store.Delete when no snippet has the given ID.
var ErrNotFound = errors.New("snippet not found")

// ExecSnippet is a saved exec configuration (full state) for the web UI exec panel.
type ExecSnippet struct {
	ID                    string `json:"id"`
	Name                  string `json:"name"`
	Mode                  string `json:"mode"` // command | script
	Command               string `json:"command"`
	ScriptInterpreter     string `json:"script_interpreter,omitempty"`
	InterpreterArgsQuoted bool   `json:"interpreter_args_quoted,omitempty"`
	RunAs                 string `json:"run_as,omitempty"`
}

// Store persists exec snippets. Implementations must be safe for concurrent use.
type Store interface {
	List(ctx context.Context) ([]ExecSnippet, error)
	// Save upserts by ID, generating an ID when empty, and returns the stored snippet.
	Save(ctx context.Context, s ExecSnippet) (ExecSnippet, error)
	// Delete removes a snippet by ID, returning ErrNotFound if absent.
	Delete(ctx context.Context, id string) error
}
