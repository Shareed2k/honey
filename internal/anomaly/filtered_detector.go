package anomaly

import "context"

// filteredLLMDetector implements the CoLA two-tier detection pattern:
// the fast detector (heuristic or ONNX) runs first, and the LLM is only
// invoked when the fast score is at or above filterThreshold. Lines that the
// fast model classifies with high confidence as normal bypass the LLM entirely,
// giving a significant throughput improvement on high-volume log streams.
type filteredLLMDetector struct {
	fast            Detector
	llm             *llmDetector
	filterThreshold float64
}

func (d *filteredLLMDetector) Score(ctx context.Context, line string) (Result, error) {
	fast, err := d.fast.Score(ctx, line)
	if err != nil {
		return Result{}, err
	}
	if fast.Score < d.filterThreshold {
		return fast, nil
	}
	return d.llm.Score(ctx, line)
}
