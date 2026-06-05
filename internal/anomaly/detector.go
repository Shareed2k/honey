// Package anomaly provides log anomaly detection using ONNX models or a built-in heuristic fallback.
package anomaly

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"sync"

	regexp "github.com/coregx/coregex"
)

// Options configures the anomaly detector.
type Options struct {
	ModelPath     string
	TokenizerPath string
	Threshold     float64
	Window        int

	// LLMEndpoint is the base URL of an OpenAI-compatible API (Ollama: http://localhost:11434/v1,
	// LM Studio: http://localhost:1234/v1). When set, log lines are scored via chat completions.
	LLMEndpoint string
	// LLMModel is the model name sent to the LLM endpoint. Defaults to "llama3" when empty.
	LLMModel string
	// LLMContextLines is the number of recent log lines sent as context with each LLM request.
	// 0 disables context (single-line mode). Default 5 when unset.
	LLMContextLines int
	// FilterThreshold enables CoLA-style two-tier detection when > 0. The fast detector
	// (heuristic when LLM-only, ONNX when ensemble) runs first; the LLM is only invoked
	// when the fast score is at or above this value. Lines below it are returned as normal
	// without an LLM call. Recommended value: 0.40. 0 disables filtering (default).
	FilterThreshold float64
	// FreqWindow is the short-window size used for rate-ratio burst detection. When a log
	// template's occurrence rate in the last FreqWindow lines exceeds FreqRatio × its
	// long-term baseline rate, it is flagged as a frequency spike. Default 100; 0 disables.
	FreqWindow int
	// FreqRatio is the short/long rate ratio that triggers a freq-spike score. Default 5.0.
	FreqRatio float64
	// Preprocessor configures a preprocessor to run before anomaly detection.
	Preprocessor string
}

// Result holds the outcome of scoring a single log line.
type Result struct {
	Score    float64
	Anomaly  bool
	Reason   string
	Original string
}

// Detector scores log lines for anomaly probability.
type Detector interface {
	Score(ctx context.Context, line string) (Result, error)
}

// EmbeddedDetector wraps an ONNX model or falls back to a heuristic scorer.
type EmbeddedDetector struct {
	impl       Detector
	threshold  float64
	window     int
	freqWindow int
	freqRatio  float64
	mu         sync.Mutex
	seen       map[string]int
	recent     []string
	freqRecent []string
	totalLines int
}

// NewEmbeddedDetector creates a detector backed by the ONNX model at opts.ModelPath, or a heuristic if empty.
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
	freqWindow := opts.FreqWindow
	if freqWindow == 0 {
		freqWindow = 100
	}
	freqRatio := opts.FreqRatio
	if freqRatio <= 0 {
		freqRatio = 5.0
	}
	d := &EmbeddedDetector{
		threshold:  threshold,
		window:     window,
		freqWindow: freqWindow,
		freqRatio:  freqRatio,
		seen:       make(map[string]int),
	}
	hasONNX := strings.TrimSpace(opts.ModelPath) != ""
	hasLLM := strings.TrimSpace(opts.LLMEndpoint) != ""
	switch {
	case hasONNX && hasLLM:
		onnxDet, err := newONNXDetector(opts.ModelPath, opts.TokenizerPath, threshold, window)
		if err != nil {
			return nil, err
		}
		llmDet := newLLMDetector(opts.LLMEndpoint, opts.LLMModel, threshold, opts.LLMContextLines)
		if opts.FilterThreshold > 0 {
			// CoLA pattern: ONNX acts as fast filter; LLM only called for suspicious lines.
			d.impl = &filteredLLMDetector{fast: onnxDet, llm: llmDet, filterThreshold: opts.FilterThreshold}
		} else {
			d.impl = &ensembleDetector{a: onnxDet, b: llmDet, threshold: threshold}
		}
	case hasLLM:
		llmDet := newLLMDetector(opts.LLMEndpoint, opts.LLMModel, threshold, opts.LLMContextLines)
		if opts.FilterThreshold > 0 {
			// CoLA pattern: heuristic acts as fast filter; LLM only called for suspicious lines.
			heur := &EmbeddedDetector{threshold: threshold, window: window, freqWindow: freqWindow, freqRatio: freqRatio, seen: make(map[string]int)}
			d.impl = &filteredLLMDetector{fast: heur, llm: llmDet, filterThreshold: opts.FilterThreshold}
		} else {
			d.impl = llmDet
		}
	case hasONNX:
		onnxDet, err := newONNXDetector(opts.ModelPath, opts.TokenizerPath, threshold, window)
		if err != nil {
			return nil, err
		}
		d.impl = onnxDet
	}

	switch strings.ToLower(opts.Preprocessor) {
	case "lshd":
		preproc := NewLSHDDetector()
		if d.impl != nil {
			d.impl = &preprocessedDetector{preprocessor: preproc, inner: d.impl}
		} else {
			d.impl = &preprocessedDetector{
				preprocessor: preproc,
				inner: detectorFunc(func(_ context.Context, line string) (Result, error) {
					return d.scoreHeuristic(line), nil
				}),
			}
		}
	case "lff":
		if d.impl != nil {
			d.impl = &lffPreprocessorDetector{inner: d.impl}
		} else {
			d.impl = &lffPreprocessorDetector{
				inner: detectorFunc(func(_ context.Context, line string) (Result, error) {
					return d.scoreHeuristic(line), nil
				}),
			}
		}
	}
	return d, nil
}

type preprocessedDetector struct {
	preprocessor LSHDDetector
	inner        Detector
}

func (p *preprocessedDetector) Score(ctx context.Context, line string) (Result, error) {
	tpl, err := p.preprocessor.Template(line)
	if err != nil {
		return Result{}, err
	}
	res, err := p.inner.Score(ctx, tpl)
	if err != nil {
		return Result{}, err
	}
	res.Original = line
	return res, nil
}

type detectorFunc func(context.Context, string) (Result, error)

func (f detectorFunc) Score(ctx context.Context, line string) (Result, error) {
	return f(ctx, line)
}

var (
	reUUID       = regexp.MustCompile(`[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}`)
	reMAC        = regexp.MustCompile(`\b[0-9a-fA-F]{2}(?::[0-9a-fA-F]{2}){5}\b`)
	reIP         = regexp.MustCompile(`\b\d{1,3}(?:\.\d{1,3}){3}\b`)
	reHex        = regexp.MustCompile(`\b0x[0-9a-fA-F]{2,}\b`)
	reEmail      = regexp.MustCompile(`\b[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}\b`)
	reBool       = regexp.MustCompile(`\b(?:true|false)\b`)
	reNum        = regexp.MustCompile(`\b\d{3,}\b`)
	reKeyQuoted  = regexp.MustCompile(`\b(\w+)="[^"]*"`)
	reKeySingle  = regexp.MustCompile(`\b(\w+)='[^']*'`)
	reJSONVal    = regexp.MustCompile(`"(\w+)"\s*:\s*"[^"]*"`)
	reKeyBareVal = regexp.MustCompile(`\b(\w+)=[a-zA-Z][a-zA-Z0-9._/\-]{2,}`)
	reTimestamp  = regexp.MustCompile(`\b\d{4}-\d{2}-\d{2}[tT\s]\d{2}:\d{2}:\d{2}(?:\.\d+)?(?:[zZ]|[\+\-]\d{2}:?\d{2})?\b|\b\d{2}:\d{2}:\d{2}(?:\.\d+)?\b|\b(?:Jan|Feb|Mar|Apr|May|Jun|Jul|Aug|Sep|Oct|Nov|Dec)\s+\d{1,2}\s+\d{2}:\d{2}:\d{2}\b`)
)

// Normalize cleans and normalizes a log line by stripping parameter noise (timestamps, IPs, UUIDs).
func Normalize(line string) string {
	line = strings.ToLower(strings.TrimSpace(line))
	line = reTimestamp.ReplaceAllString(line, "<timestamp>")
	// Strip key=value pairs before number replacement to avoid partial matches.
	line = reKeyQuoted.ReplaceAllString(line, "$1=<val>")
	line = reKeySingle.ReplaceAllString(line, "$1=<val>")
	line = reJSONVal.ReplaceAllString(line, `"$1":"<val>"`)
	line = reKeyBareVal.ReplaceAllString(line, "$1=<val>")
	line = reUUID.ReplaceAllString(line, "<uuid>")
	line = reMAC.ReplaceAllString(line, "<mac>")
	line = reIP.ReplaceAllString(line, "<ip>")
	line = reHex.ReplaceAllString(line, "<hex>")
	line = reEmail.ReplaceAllString(line, "<email>")
	line = reBool.ReplaceAllString(line, "<bool>")
	line = reNum.ReplaceAllString(line, "<num>")
	return line
}

// Score returns the anomaly score for a single log line.
func (d *EmbeddedDetector) Score(ctx context.Context, line string) (Result, error) {
	if d.impl != nil {
		return d.impl.Score(ctx, line)
	}
	return d.scoreHeuristic(line), nil
}

func (d *EmbeddedDetector) scoreHeuristic(line string) Result {
	n := Normalize(line)
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

	d.seen[n] = count + 1
	d.totalLines++
	d.recent = append(d.recent, n)
	if len(d.recent) > d.window {
		d.recent = d.recent[len(d.recent)-d.window:]
	}

	// Rate-ratio burst: flag when short-window rate exceeds long-term baseline by freqRatio.
	// Only fires for templates with an established baseline (seen in >0.1% of all lines).
	if d.freqWindow > 0 {
		d.freqRecent = append(d.freqRecent, n)
		if len(d.freqRecent) > d.freqWindow {
			d.freqRecent = d.freqRecent[len(d.freqRecent)-d.freqWindow:]
		}
		longRate := float64(d.seen[n]) / float64(d.totalLines)
		if longRate > 0.001 && len(d.freqRecent) >= d.freqWindow/2 {
			shortCount := 0
			for _, x := range d.freqRecent {
				if x == n {
					shortCount++
				}
			}
			shortRate := float64(shortCount) / float64(len(d.freqRecent))
			ratio := shortRate / longRate
			if ratio >= d.freqRatio {
				v := 0.70 + math.Min(0.25, (ratio-d.freqRatio)*0.01)
				if v > score {
					score = v
					reason = fmt.Sprintf("freq-spike(%.1fx)", ratio)
				}
			}
		}
	}

	if score > 1 {
		score = 1
	}
	return Result{Score: score, Anomaly: score >= d.threshold, Reason: reason, Original: line}
}

func severityScore(lower string) float64 {
	switch {
	// Cybersecurity-specific patterns (checked first — higher priority than generic severity).
	case strings.Contains(lower, "exploit"), strings.Contains(lower, "shellcode"), strings.Contains(lower, "payload"):
		return 0.99
	case strings.Contains(lower, "injection"), strings.Contains(lower, "xss"), strings.Contains(lower, "lfi"), strings.Contains(lower, "rfi"), strings.Contains(lower, "ssrf"):
		return 0.97
	case strings.Contains(lower, "privilege escalat"), strings.Contains(lower, "privesc"):
		return 0.97
	case strings.Contains(lower, "brute force"), strings.Contains(lower, "credential stuffing"), strings.Contains(lower, "password spray"):
		return 0.95
	case strings.Contains(lower, "unauthorized"), strings.Contains(lower, "access denied"):
		return 0.93
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
