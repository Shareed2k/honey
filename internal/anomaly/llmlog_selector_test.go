package anomaly

import (
	"sync"
	"testing"
)

func TestSelectDefaultDemonstrations(t *testing.T) {
	tests := []struct {
		name           string
		targetTokens   []string
		maxDemos       int
		expectedSource string
	}{
		{
			name:           "Postgres Connection target",
			targetTokens:   []string{"connection", "received", "host"},
			maxDemos:       2,
			expectedSource: "postgres",
		},
		{
			name:           "Postgres Select Statement target",
			targetTokens:   []string{"statement", "select", "from"},
			maxDemos:       2,
			expectedSource: "postgres",
		},
		{
			name:           "Nginx GET request target",
			targetTokens:   []string{"get", "http/1.1", "200"},
			maxDemos:       2,
			expectedSource: "nginx",
		},
		{
			name:           "Node Listening Port target",
			targetTokens:   []string{"server", "listening", "port"},
			maxDemos:       2,
			expectedSource: "node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			demos := SelectDefaultDemonstrations(tt.targetTokens, tt.maxDemos)
			if len(demos) == 0 {
				t.Fatalf("expected demonstrations to be returned, got 0")
			}
			if len(demos) > tt.maxDemos {
				t.Errorf("expected at most %d demonstrations, got %d", tt.maxDemos, len(demos))
			}

			// Verify that the chosen demonstrations are highly relevant (have the expected source)
			matchedExpectedSource := false
			for _, d := range demos {
				if d.Source == tt.expectedSource {
					matchedExpectedSource = true
				}
			}

			if !matchedExpectedSource {
				t.Errorf("expected to find at least one demonstration from source %q, but got none. Returned demos: %+v", tt.expectedSource, demos)
			}
		})
	}
}

func TestSelectDemonstrations_WildcardAndExact(t *testing.T) {
	pool := []DemoInstance{
		{
			Source:   "nginx",
			Template: `GET /etc/passwd`,
			Tokens:   []string{"get", "/etc/passwd"},
		},
		{
			Source:   "postgres",
			Template: `SELECT <*> FROM table`,
			Tokens:   []string{"select", "<*>", "from", "table"},
		},
	}

	// 1. Exact match on target tokens
	targetExact := []string{"get", "/etc/passwd"}
	demosExact := SelectDemonstrations(targetExact, pool, 1)
	if len(demosExact) != 1 || demosExact[0].Source != "nginx" {
		t.Errorf("expected Nginx demo for exact match, got: %+v", demosExact)
	}

	// 2. Wildcard match
	targetWildcard := []string{"select", "<*>", "from"}
	demosWildcard := SelectDemonstrations(targetWildcard, pool, 1)
	if len(demosWildcard) != 1 || demosWildcard[0].Source != "postgres" {
		t.Errorf("expected Postgres demo for wildcard match, got: %+v", demosWildcard)
	}
}

func TestSelectDemonstrations_EmptyAndEdgeCases(t *testing.T) {
	pool := []DemoInstance{
		{
			Source:   "nginx",
			Template: `GET /etc/passwd`,
			Tokens:   []string{"get", "/etc/passwd"},
		},
	}

	// Empty targetTokens
	if demos := SelectDemonstrations(nil, pool, 5); demos != nil {
		t.Errorf("expected nil for empty targetTokens, got: %+v", demos)
	}

	// Empty pool
	if demos := SelectDemonstrations([]string{"get"}, nil, 5); demos != nil {
		t.Errorf("expected nil for empty pool, got: %+v", demos)
	}

	// Non-positive maxDemos
	if demos := SelectDemonstrations([]string{"get"}, pool, 0); demos != nil {
		t.Errorf("expected nil for maxDemos <= 0, got: %+v", demos)
	}
}

func TestSelectDemonstrations_OnTheFlyTokenization(t *testing.T) {
	// A pool instance with an empty Tokens slice but non-empty Template
	pool := []DemoInstance{
		{
			Source:   "postgres",
			Template: `SELECT <*> FROM table`,
			Tokens:   nil, // Test automatic on-the-fly tokenization
		},
	}

	target := []string{"select", "<*>"}
	demos := SelectDemonstrations(target, pool, 1)
	if len(demos) != 1 || demos[0].Source != "postgres" {
		t.Errorf("expected Postgres demo with automatic tokenization, got: %+v", demos)
	}
}

func TestSelectDemonstrations_ThreadSafety(t *testing.T) {
	// Let's run multiple goroutines concurrently calling SelectDefaultDemonstrations
	var wg sync.WaitGroup
	numGoroutines := 20
	target := []string{"get", "http/1.1", "200"}

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			demos := SelectDefaultDemonstrations(target, 2)
			if len(demos) == 0 {
				t.Errorf("expected demonstrations, got 0")
			}
		}()
	}
	wg.Wait()
}
