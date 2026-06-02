package anomaly

import (
	"testing"
)

func TestLSHDDetector_Initialization(t *testing.T) {
	detector := NewLSHDDetector().(*lshdDetector)

	if detector.clusters == nil {
		t.Fatal("expected clusters map to be initialized")
	}

	for i, band := range detector.bands {
		if band == nil {
			t.Fatalf("expected band %d to be initialized", i)
		}
	}
}
