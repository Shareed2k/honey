package engine_test

import (
	"testing"

	"github.com/shareed2k/honey/internal/engine"
)

// TestEventInterfaces ...
func TestEventInterfaces(t *testing.T) {
	var e engine.Event = engine.EventStepStarted{StepIdx: 1}
	if e.Kind() != engine.EventKindStepStarted {
		t.Fatal("wrong kind")
	}
}
