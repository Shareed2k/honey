package anomaly

import (
	"os"
	"testing"
)

func TestDefaultSeedPool_Initialization(t *testing.T) {
	PoolMu.RLock()
	defer PoolMu.RUnlock()

	if len(DefaultSeedPool) == 0 {
		t.Fatal("expected DefaultSeedPool to contain seed demonstrations, got 0")
	}

	for i, demo := range DefaultSeedPool {
		if demo.Source == "" {
			t.Errorf("demo %d has empty Source", i)
		}
		if demo.Template == "" {
			t.Errorf("demo %d has empty Template", i)
		}
		if len(demo.Tokens) == 0 {
			t.Errorf("demo %d: expected Tokens to be automatically initialized by tokenize(), got empty list", i)
		}
		if demo.Reason == "" {
			t.Errorf("demo %d has empty Reason", i)
		}
		if demo.Score < 0.0 || demo.Score > 1.0 {
			t.Errorf("demo %d has invalid score %f", i, demo.Score)
		}
		// Basic sanity check on anomaly vs score
		if demo.Anomaly && demo.Score < 0.5 {
			t.Errorf("demo %d: is anomaly but score is too low: %f", i, demo.Score)
		}
		if !demo.Anomaly && demo.Score > 0.5 {
			t.Errorf("demo %d: is not anomaly but score is too high: %f", i, demo.Score)
		}
	}
}

func TestLoadFeedbackDemos(t *testing.T) {
	// Backup original DefaultSeedPool
	PoolMu.Lock()
	origSeedPool := make([]DemoInstance, len(DefaultSeedPool))
	copy(origSeedPool, DefaultSeedPool)
	PoolMu.Unlock()

	defer func() {
		PoolMu.Lock()
		DefaultSeedPool = origSeedPool
		PoolMu.Unlock()
	}()

	// Create a temporary JSONL feedback file
	tmpFile, err := os.CreateTemp("", "feedback_test_*.jsonl")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	feedbackData := `{"source": "test-nginx", "line": "some test nginx template line", "score": 0.88, "reason": "test reason nginx", "anomaly": true}
{"source": "test-postgres", "line": "some test postgres line", "score": 0.12, "reason": "test reason postgres", "anomaly": false}
`
	if _, err := tmpFile.Write([]byte(feedbackData)); err != nil {
		t.Fatalf("failed to write feedback data: %v", err)
	}
	tmpFile.Close()

	// Load feedback demos
	err = LoadFeedbackDemos(tmpFile.Name())
	if err != nil {
		t.Fatalf("LoadFeedbackDemos failed: %v", err)
	}

	// Verify the prepended items
	PoolMu.RLock()
	defer PoolMu.RUnlock()

	if len(DefaultSeedPool) < len(origSeedPool)+2 {
		t.Fatalf("expected DefaultSeedPool to have at least 2 new items, got %d", len(DefaultSeedPool))
	}

	// Check if the prepended items match what was in the feedback JSONL
	item1 := DefaultSeedPool[0]
	if item1.Source != "test-nginx" {
		t.Errorf("expected source test-nginx, got %s", item1.Source)
	}
	if item1.Template != "some test nginx template line" {
		t.Errorf("expected template, got %s", item1.Template)
	}
	if item1.Score != 0.88 {
		t.Errorf("expected score 0.88, got %f", item1.Score)
	}
	if item1.Reason != "test reason nginx" {
		t.Errorf("expected reason, got %s", item1.Reason)
	}
	if !item1.Anomaly {
		t.Errorf("expected anomaly true, got false")
	}
	if len(item1.Tokens) == 0 {
		t.Errorf("expected tokenized list, got empty")
	}

	item2 := DefaultSeedPool[1]
	if item2.Source != "test-postgres" {
		t.Errorf("expected source test-postgres, got %s", item2.Source)
	}
	if item2.Template != "some test postgres line" {
		t.Errorf("expected template, got %s", item2.Template)
	}
	if item2.Score != 0.12 {
		t.Errorf("expected score 0.12, got %f", item2.Score)
	}
	if item2.Reason != "test reason postgres" {
		t.Errorf("expected reason, got %s", item2.Reason)
	}
	if item2.Anomaly {
		t.Errorf("expected anomaly false, got true")
	}
	if len(item2.Tokens) == 0 {
		t.Errorf("expected tokenized list, got empty")
	}
}

func TestLoadFeedbackDemos_NonExistentFile(t *testing.T) {
	err := LoadFeedbackDemos("this-file-does-not-exist-1234567.jsonl")
	if err != nil {
		t.Fatalf("expected nil error for non-existent file, got: %v", err)
	}
}

func TestLoadFeedbackDemos_EmptyPath(t *testing.T) {
	err := LoadFeedbackDemos("")
	if err != nil {
		t.Fatalf("expected nil error for empty filepath, got: %v", err)
	}
}
