// Package engine implements the CUE recipe execution loop, decoupled from UI.
package engine

// EventKind identifies the type of an Event.
// EventKind ...
type EventKind string

const (
	// EventKindStepStarted is emitted when a step begins execution.
	EventKindStepStarted EventKind = "StepStarted"
	// EventKindStepStdout is emitted for stdout chunks.
	EventKindStepStdout EventKind = "StepStdout"
	// EventKindStepStderr is emitted for stderr chunks.
	EventKindStepStderr EventKind = "StepStderr"
	// EventKindStepCompleted is emitted when a step succeeds.
	EventKindStepCompleted EventKind = "StepCompleted"
	// EventKindStepFailed is emitted when a step fails.
	EventKindStepFailed EventKind = "StepFailed"
)

// Event is the interface for all engine events.
// Event ...
type Event interface {
	Kind() EventKind
}

// EventStepStarted indicates a step started.
// EventStepStarted ...
type EventStepStarted struct {
	StepIdx int
}

// Kind implements Event.
func (e EventStepStarted) Kind() EventKind { return EventKindStepStarted }

// EventStepStdout indicates stdout output.
// EventStepStdout ...
type EventStepStdout struct {
	StepIdx int
	Output  []byte
}

// Kind implements Event.
func (e EventStepStdout) Kind() EventKind { return EventKindStepStdout }

// EventStepStderr indicates stderr output.
// EventStepStderr ...
type EventStepStderr struct {
	StepIdx int
	Output  []byte
}

// Kind implements Event.
func (e EventStepStderr) Kind() EventKind { return EventKindStepStderr }

// EventStepCompleted indicates step success.
// EventStepCompleted ...
type EventStepCompleted struct {
	StepIdx int
}

// Kind implements Event.
func (e EventStepCompleted) Kind() EventKind { return EventKindStepCompleted }

// EventStepFailed indicates step failure.
// EventStepFailed ...
type EventStepFailed struct {
	StepIdx int
	Error   error
}

// Kind implements Event.
func (e EventStepFailed) Kind() EventKind { return EventKindStepFailed }
