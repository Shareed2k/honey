//go:build wasip1 || wasm

package pluginpdk

import (
	"encoding/json"
	"fmt"
)

// ReadConfig unmarshals plugin step config JSON into T.
func ReadConfig[T any](config []byte) (T, error) {
	var out T
	if len(config) == 0 {
		return out, fmt.Errorf("plugin config is empty")
	}
	if err := json.Unmarshal(config, &out); err != nil {
		return out, fmt.Errorf("parse plugin config: %w", err)
	}
	return out, nil
}
