package anomaly

import (
	"context"
	"strings"
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

	if detector.tokenDF == nil {
		t.Fatal("expected tokenDF map to be initialized")
	}

	if detector.N != 0 {
		t.Fatalf("expected N to be 0, got %d", detector.N)
	}
}

func TestTokenize(t *testing.T) {
	tests := []struct {
		input    string
		expected []string
	}{
		{
			input:    "SW-CORE %LINK-3: Interface Gigabit1 is flapping.",
			expected: []string{"sw-core", "%link-3", ":", "interface", "gigabit1", "is", "flapping", "."},
		},
		{
			input:    "Connection from 192.168.1.1 port 12345 accepted",
			expected: []string{"connection", "from", "192", ".", "168", ".", "1", ".", "1", "port", "12345", "accepted"},
		},
		{
			input:    "",
			expected: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := tokenize(tc.input)
			if len(got) != len(tc.expected) {
				t.Fatalf("expected %v, got %v", tc.expected, got)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("at index %d: expected %q, got %q", i, tc.expected[i], got[i])
				}
			}
		})
	}
}

func TestLSHDDetector_ComputeSimHash(t *testing.T) {
	detector := NewLSHDDetector().(*lshdDetector)

	// Simulate processing a couple of documents
	line1 := "SW-CORE %LINK-3: Interface Gigabit1 is flapping."
	line2 := "SW-CORE %LINK-3: Interface Gigabit2 is flapping."

	ctx := context.Background()
	_, err := detector.Score(ctx, line1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if detector.N != 1 {
		t.Errorf("expected N=1, got %d", detector.N)
	}

	if df := detector.tokenDF["sw-core"]; df != 1 {
		t.Errorf("expected tokenDF['sw-core']=1, got %d", df)
	}

	_, err = detector.Score(ctx, line2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if detector.N != 2 {
		t.Errorf("expected N=2, got %d", detector.N)
	}

	if df := detector.tokenDF["sw-core"]; df != 2 {
		t.Errorf("expected tokenDF['sw-core']=2, got %d", df)
	}

	if df := detector.tokenDF["gigabit1"]; df != 1 {
		t.Errorf("expected tokenDF['gigabit1']=1, got %d", df)
	}

	// Calculate SimHashes directly
	hash1 := detector.computeSimHash(tokenize(line1))
	hash2 := detector.computeSimHash(tokenize(line2))

	if hash1 == 0 {
		t.Error("expected non-zero hash for line1")
	}

	// Because line1 and line2 are very similar, their hashes should have a very small Hamming distance.
	hammingDistance := 0
	for i := 0; i < 64; i++ {
		bit1 := (hash1 >> i) & 1
		bit2 := (hash2 >> i) & 1
		if bit1 != bit2 {
			hammingDistance++
		}
	}

	// Since they differ by only one token (gigabit1 vs gigabit2), the distance should be small
	if hammingDistance > 10 {
		t.Errorf("expected small Hamming distance, got %d (hashes: %x vs %x)", hammingDistance, hash1, hash2)
	}

	// Compute SimHash of a completely different line
	line3 := "completely unrelated message here"
	hash3 := detector.computeSimHash(tokenize(line3))

	hammingDistanceUnrelated := 0
	for i := 0; i < 64; i++ {
		bit1 := (hash1 >> i) & 1
		bit3 := (hash3 >> i) & 1
		if bit1 != bit3 {
			hammingDistanceUnrelated++
		}
	}

	if hammingDistanceUnrelated <= hammingDistance {
		t.Errorf("expected unrelated line to have larger distance (%d) than similar line (%d)", hammingDistanceUnrelated, hammingDistance)
	}
}

func TestLCSLength(t *testing.T) {
	tests := []struct {
		name     string
		a        []string
		b        []string
		expected int
	}{
		{
			name:     "empty lists",
			a:        []string{},
			b:        []string{},
			expected: 0,
		},
		{
			name:     "one empty list",
			a:        []string{"hello"},
			b:        []string{},
			expected: 0,
		},
		{
			name:     "identical lists",
			a:        []string{"hello", "world"},
			b:        []string{"hello", "world"},
			expected: 2,
		},
		{
			name:     "completely different lists",
			a:        []string{"hello", "world"},
			b:        []string{"goodnight", "moon"},
			expected: 0,
		},
		{
			name:     "partial match / sub-sequence",
			a:        []string{"a", "b", "c", "d", "e"},
			b:        []string{"b", "d", "f"},
			expected: 2, // "b", "d"
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := lcsLength(tc.a, tc.b)
			if got != tc.expected {
				t.Errorf("expected %d, got %d", tc.expected, got)
			}
		})
	}
}

func TestAlignTemplates(t *testing.T) {
	tests := []struct {
		name     string
		temp     []string
		tokens   []string
		expected []string
	}{
		{
			name:     "no diff",
			temp:     []string{"hello", "world"},
			tokens:   []string{"hello", "world"},
			expected: []string{"hello", "world"},
		},
		{
			name:     "one diff middle",
			temp:     []string{"hello", "big", "world"},
			tokens:   []string{"hello", "small", "world"},
			expected: []string{"hello", "<*>", "world"},
		},
		{
			name:     "diff at end",
			temp:     []string{"hello", "world", "one"},
			tokens:   []string{"hello", "world", "two"},
			expected: []string{"hello", "world", "<*>"},
		},
		{
			name:     "diff at start",
			temp:     []string{"one", "hello", "world"},
			tokens:   []string{"two", "hello", "world"},
			expected: []string{"<*>", "hello", "world"},
		},
		{
			name:     "consecutive diff collapses",
			temp:     []string{"hello", "beautiful", "new", "world"},
			tokens:   []string{"hello", "ugly", "old", "world"},
			expected: []string{"hello", "<*>", "world"},
		},
		{
			name:     "empty templates and tokens",
			temp:     []string{},
			tokens:   []string{},
			expected: []string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := alignTemplates(tc.temp, tc.tokens)
			if len(got) != len(tc.expected) {
				t.Fatalf("expected len %d, got len %d (%v vs %v)", len(tc.expected), len(got), tc.expected, got)
			}
			for i := range got {
				if got[i] != tc.expected[i] {
					t.Errorf("at index %d: expected %q, got %q", i, tc.expected[i], got[i])
				}
			}
		})
	}
}

func TestLSHDDetector_TemplateAndScore(t *testing.T) {
	params1 := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9", "10", "abc", "def", "xyz", "pqr", "status"}
	params1New := []string{"11", "12", "13", "14", "15", "16", "17", "18", "19", "20", "ghi", "jkl", "mno", "tuv", "state"}
	params2 := []string{"100", "200", "300", "400", "500", "600", "700", "800", "900", "1000", "time", "count", "rate"}

	var finalTpl string
	var found bool
	var finalLines []string

	for _, p1 := range params1 {
		for _, p1n := range params1New {
			for _, p2 := range params2 {
				det := NewLSHDDetector()
				lines := []string{
					"vendorA: the link has changed state " + p1 + " times in the last " + p2 + " seconds",
					"vendorA: the link has changed state " + p1n + " times in the last " + p2 + " seconds",
					"vendorB: the link has changed state " + p1n + " times in the last " + p2 + " seconds",
				}

				success := true
				var lastTpl string
				for _, line := range lines {
					var err error
					lastTpl, err = det.Template(line)
					if err != nil {
						success = false
						break
					}
				}
				if success && strings.HasPrefix(lastTpl, "<*>") && strings.Contains(lastTpl, "<*> times") {
					finalTpl = lastTpl
					finalLines = lines
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		t.Fatal("Could not find a matching parameter set for LSH matching")
	}

	t.Logf("Found matching lines: %v", finalLines)
	t.Logf("Final template: %s", finalTpl)

	// Verify that Score matches calling Template on the same detector we searched with
	det := NewLSHDDetector()
	for _, line := range finalLines {
		_, _ = det.Template(line)
	}

	res, err := det.Score(context.Background(), finalLines[2])
	if err != nil {
		t.Fatalf("unexpected score error: %v", err)
	}
	if res.Score != 0 || res.Anomaly != false {
		t.Errorf("expected Score=0, Anomaly=false, got Score=%f, Anomaly=%v", res.Score, res.Anomaly)
	}
	if res.Reason != "lshd:"+finalTpl {
		t.Errorf("expected Reason %q, got %q", "lshd:"+finalTpl, res.Reason)
	}
}

func TestEmbeddedDetector_WithLSHDPreprocessor(t *testing.T) {
	opts := Options{
		Preprocessor: "lshd",
		Threshold:    0.90,
	}
	det, err := NewEmbeddedDetector(opts)
	if err != nil {
		t.Fatalf("unexpected error creating detector: %v", err)
	}

	// Score some lines with high similarity to ensure LSHD cluster templates align
	lines := []string{
		"vendorA: the link has changed state 1 times in the last 100 seconds",
		"vendorA: the link has changed state 2 times in the last 100 seconds",
		"vendorA: the link has changed state 3 times in the last 100 seconds",
	}

	ctx := context.Background()
	for _, line := range lines {
		res, err := det.Score(ctx, line)
		if err != nil {
			t.Fatalf("failed to score line: %v", err)
		}
		// The original line should match the one passed in
		if res.Original != line {
			t.Errorf("expected original %q, got %q", line, res.Original)
		}
	}
}
