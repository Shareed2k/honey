package cuetry

import (
	"fmt"
	"strings"
)

// SanitizeKVHostSuffix maps host names to a single stepkv key segment.
func SanitizeKVHostSuffix(hostName string) string {
	return strings.NewReplacer("/", "_", ":", "_").Replace(strings.TrimSpace(hostName))
}

// ResolveStepKVBaseKey returns a step's kv_key with optional per-host suffix.
// Shared by any step kind that supports kv_key/kv_key_per_host (postgres,
// plugin, ...) — the logic has never been postgres-specific.
func ResolveStepKVBaseKey(base string, perHost bool, hostName string) (string, error) {
	key := strings.TrimSpace(base)
	if key == "" {
		return "", nil
	}
	if err := stepkvValidateKey(key); err != nil {
		return "", err
	}
	if !perHost {
		return key, nil
	}
	suffix := SanitizeKVHostSuffix(hostName)
	if suffix == "" {
		return key, nil
	}
	key = key + "_" + suffix
	if err := stepkvValidateKey(key); err != nil {
		return "", err
	}
	return key, nil
}

// PostgresExtractKVKey returns the KV key for an extract variable name.
func PostgresExtractKVKey(baseKey, name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", fmt.Errorf("cuetry: empty extract name")
	}
	key := strings.TrimSpace(baseKey) + "_" + name
	if err := stepkvValidateKey(key); err != nil {
		return "", err
	}
	return key, nil
}
