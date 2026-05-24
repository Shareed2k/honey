package postgres

import (
	"encoding/json"
	"fmt"
)

const maxParams = 64

// ParseParams unmarshals params JSON into pgx query arguments.
func ParseParams(raw json.RawMessage) ([]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var args []any
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("postgres: parse params: %w", err)
	}
	if len(args) > maxParams {
		return nil, fmt.Errorf("postgres: too many params (max %d)", maxParams)
	}
	return args, nil
}

// ValidateParamPlaceholders ensures sql uses $1..$n when args are present.
func ValidateParamPlaceholders(sql string, n int) error {
	if n == 0 {
		return nil
	}
	for i := 1; i <= n; i++ {
		needle := fmt.Sprintf("$%d", i)
		if !containsPlaceholder(sql, needle) {
			return fmt.Errorf("postgres: sql missing placeholder %s", needle)
		}
	}
	return nil
}

func containsPlaceholder(sql, needle string) bool {
	for i := 0; i < len(sql); i++ {
		if sql[i] != needle[0] {
			continue
		}
		if i+len(needle) <= len(sql) && sql[i:i+len(needle)] == needle {
			next := i + len(needle)
			if next >= len(sql) {
				return true
			}
			c := sql[next]
			if c < '0' || c > '9' {
				return true
			}
		}
	}
	return false
}
