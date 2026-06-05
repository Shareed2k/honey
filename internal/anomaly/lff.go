package anomaly

import (
	"context"
	"strings"

	regexp "github.com/coregx/coregex"
)

var (
	reLFFSpaces = regexp.MustCompile(`[ \t\n\r]+`)
	reLFFText   = regexp.MustCompile(`[A-Za-z]+`)
	reLFFDigits = regexp.MustCompile(`[0-9]+`)
	reLFFAProps = regexp.MustCompile(`a(\s+a)+`)
)

// LFF transforms a raw log line into its Elastic Log Format Fingerprint (LFF)
// following the exact inside-out ES|QL execution order:
// 1. Whitespaces collapsed -> ' '
// 2. Letters -> 'a'
// 3. Digits -> '0'
// 4. Multiple 'a' tokens with single space collapsed -> 'a'
// 5. Special characters/symbols preserved.
func LFF(line string) string {
	line = reLFFSpaces.ReplaceAllString(line, " ")
	line = reLFFText.ReplaceAllString(line, "a")
	line = reLFFDigits.ReplaceAllString(line, "0")
	line = reLFFAProps.ReplaceAllString(line, "a")
	return strings.TrimSpace(line)
}

type lffPreprocessorDetector struct {
	inner Detector
}

func (p *lffPreprocessorDetector) Score(ctx context.Context, line string) (Result, error) {
	fingerprint := LFF(line)
	res, err := p.inner.Score(ctx, fingerprint)
	if err != nil {
		return Result{}, err
	}
	res.Original = line
	return res, nil
}
