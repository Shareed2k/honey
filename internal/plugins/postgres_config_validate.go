package plugins

import (
	"fmt"
	"strings"

	"github.com/itchyny/gojq"
)

const maxStepKVKeyLen = 256

func validatePostgresKVConfig(kvKey string, perHost bool, extract map[string]string) error {
	kvKey = strings.TrimSpace(kvKey)
	if kvKey != "" {
		if err := validateStepKVKey(kvKey); err != nil {
			return fmt.Errorf("kv_key: %w", err)
		}
		if perHost {
			if err := validateStepKVKey(kvKey + "_host"); err != nil {
				return fmt.Errorf("kv_key per_host: %w", err)
			}
		}
	}
	if len(extract) > 0 && kvKey == "" {
		return fmt.Errorf("extract requires kv_key")
	}
	for name, q := range extract {
		if strings.TrimSpace(name) == "" {
			return fmt.Errorf("extract: empty name")
		}
		if err := validateStepKVKey(kvKey + "_" + name); err != nil {
			return fmt.Errorf("extract[%q] key: %w", name, err)
		}
		q = strings.TrimSpace(q)
		if q == "" {
			return fmt.Errorf("extract[%q]: empty jq query", name)
		}
		if _, err := gojq.Parse(q); err != nil {
			return fmt.Errorf("extract[%q]: jq parse: %w", name, err)
		}
	}
	return nil
}

func validateStepKVKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > maxStepKVKeyLen || strings.Contains(key, "/") || key == "__health" {
		return fmt.Errorf("invalid stepkv key %q", key)
	}
	return nil
}
