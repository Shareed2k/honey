package anomaly

import "context"

type ensembleDetector struct {
	a, b      Detector
	threshold float64
}

type scoreOutcome struct {
	r   Result
	err error
}

func (e *ensembleDetector) Score(ctx context.Context, line string) (Result, error) {
	chA := make(chan scoreOutcome, 1)
	chB := make(chan scoreOutcome, 1)
	go func() { r, err := e.a.Score(ctx, line); chA <- scoreOutcome{r, err} }()
	go func() { r, err := e.b.Score(ctx, line); chB <- scoreOutcome{r, err} }()

	oA := <-chA
	oB := <-chB
	if oA.err != nil {
		return Result{}, oA.err
	}
	if oB.err != nil {
		return Result{}, oB.err
	}

	score := clamp01((oA.r.Score + oB.r.Score) / 2)
	return Result{
		Score:    score,
		Anomaly:  score >= e.threshold,
		Reason:   oA.r.Reason + "+" + oB.r.Reason,
		Original: line,
	}, nil
}
