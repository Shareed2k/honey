package pluginpdk

import (
	"encoding/json"
	"errors"
	"fmt"
)

type kvInput struct {
	Op    string `json:"op"`
	Key   string `json:"key"`
	Value string `json:"value,omitempty"`
}

type kvOutput struct {
	Found bool   `json:"found,omitempty"`
	Value string `json:"value,omitempty"`
	Error string `json:"error,omitempty"`
}

func parseKVOutput(b []byte) (kvOutput, error) {
	var out kvOutput
	if err := json.Unmarshal(b, &out); err != nil {
		return kvOutput{}, fmt.Errorf("kv: decode output: %w", err)
	}
	if out.Error != "" {
		return out, errors.New(out.Error)
	}
	return out, nil
}
