package cuetry

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/itchyny/gojq"
)

// EvalJQ runs a jq query against a JSON document string.
// Scalar results are formatted as strings; arrays and objects are compact JSON.
func EvalJQ(jsonDoc, query string) (string, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("cuetry: jq query is empty")
	}
	doc := strings.TrimSpace(jsonDoc)
	if doc == "" {
		return "", fmt.Errorf("cuetry: jq input is empty")
	}
	var input any
	if err := json.Unmarshal([]byte(doc), &input); err != nil {
		return "", fmt.Errorf("cuetry: jq input parse: %w", err)
	}
	q, err := gojq.Parse(query)
	if err != nil {
		return "", fmt.Errorf("cuetry: jq parse: %w", err)
	}
	iter := q.Run(input)
	var results []any
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, isErr := v.(error); isErr {
			if halt, ok := err.(*gojq.HaltError); ok && halt.Value() == nil {
				break
			}
			return "", fmt.Errorf("cuetry: jq eval: %w", err)
		}
		results = append(results, v)
	}
	if len(results) == 0 {
		return "", nil
	}
	if len(results) == 1 {
		return formatJQValue(results[0]), nil
	}
	b, err := json.Marshal(results)
	if err != nil {
		return "", fmt.Errorf("cuetry: jq encode: %w", err)
	}
	return string(b), nil
}

func formatJQValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case json.Number:
		return x.String()
	case bool:
		if x {
			return "true"
		}
		return "false"
	case float64, int, int64, uint64:
		return fmt.Sprint(x)
	default:
		b, err := json.Marshal(x)
		if err != nil {
			return fmt.Sprint(x)
		}
		return string(b)
	}
}

// ValidateJQQuery parses a jq query for static validation.
func ValidateJQQuery(query string) error {
	query = strings.TrimSpace(query)
	if query == "" {
		return fmt.Errorf("cuetry: jq query is empty")
	}
	_, err := gojq.Parse(query)
	if err != nil {
		return fmt.Errorf("cuetry: jq parse: %w", err)
	}
	return nil
}
