package anomaly

import (
	"context"
	"fmt"
	"math"
	"os"
	"regexp"
	"strings"
	"sync"
)

type Options struct {
	ModelPath     string
	TokenizerPath string
	Threshold     float64
	Window        int
}

type Result struct {
	Score    float64
	Anomaly  bool
	Reason   string
	Original string
}

type Detector interface {
	Score(ctx context.Context, line string) (Result, error)
}

type EmbeddedDetector struct {
	impl      Detector
	threshold float64
	window    int
	mu        sync.Mutex
	seen      map[string]int
	recent    []string
}

func NewEmbeddedDetector(opts Options) (*EmbeddedDetector, error) {
	if opts.ModelPath != "" {
		if _, err := os.Stat(opts.ModelPath); err != nil {
			return nil, fmt.Errorf("anomaly model path: %w", err)
		}
	}
	threshold := opts.Threshold
	if threshold <= 0 || threshold > 1 {
		threshold = 0.90
	}
	window := opts.Window
	if window <= 0 {
		window = 32
	}
	d := &EmbeddedDetector{threshold: threshold, window: window, seen: make(map[string]int)}
	if strings.TrimSpace(opts.ModelPath) != "" {
		onnxDet, err := newONNXDetector(opts.ModelPath, opts.TokenizerPath, threshold, window)
		if err != nil {
			return nil, err
		}
		d.impl = onnxDet
	}
	return d, nil
}

var (
	reUUID = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}`)
	reIP   = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	reNum  = regexp.MustCompile(`\b\d{3,}\b`)
)

func normalize(line string) string {
	line = strings.ToLower(strings.TrimSpace(line))
	line = reUUID.ReplaceAllString(line, "<uuid>")
	line = reIP.ReplaceAllString(line, "<ip>")
	line = reNum.ReplaceAllString(line, "<num>")
	return line
}

func (d *EmbeddedDetector) Score(ctx context.Context, line string) (Result, error) {
	if d.impl != nil {
		return d.impl.Score(ctx, line)
	}
	return d.scoreHeuristic(line), nil
}

func (d *EmbeddedDetector) scoreHeuristic(line string) Result {
	n := normalize(line)
	if n == "" {
		return Result{Score: 0, Anomaly: false, Reason: "empty", Original: line}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	reason := "baseline"
	score := 0.05
	sev := severityScore(n)
	if sev > score {
		score = sev
		reason = "severity"
	}

	count := d.seen[n]
	if count == 0 {
		if score < 0.60 {
			score = 0.60
		}
		reason = "novel"
	}

	burst := 0
	for _, x := range d.recent {
		if x == n {
			burst++
		}
	}
	if burst >= 3 {
		v := 0.70 + math.Min(0.25, float64(burst-2)*0.05)
		if v > score {
			score = v
			reason = "burst"
		}
	}

	d.seen[n] = count + 1
	d.recent = append(d.recent, n)
	if len(d.recent) > d.window {
		d.recent = d.recent[len(d.recent)-d.window:]
	}

	if score > 1 {
		score = 1
	}
	return Result{Score: score, Anomaly: score >= d.threshold, Reason: reason, Original: line}
}

func severityScore(lower string) float64 {
	switch {
	case strings.Contains(lower, "panic"), strings.Contains(lower, "fatal"), strings.Contains(lower, "segfault"):
		return 0.98
	case strings.Contains(lower, "exception"), strings.Contains(lower, "out of memory"), strings.Contains(lower, "oom"):
		return 0.95
	case strings.Contains(lower, "error"), strings.Contains(lower, "failed"), strings.Contains(lower, "denied"):
		return 0.92
	case strings.Contains(lower, "warn"):
		return 0.55
	default:
		return 0.05
	}
}
