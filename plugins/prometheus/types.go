package main

// promConfig is the per-step recipe config for the "query" action.
type promConfig struct {
	Query   string `json:"query"`
	Timeout string `json:"timeout,omitempty"` // e.g. "10s"; defaults to defaultQueryTimeout
}

// queryOutput is the step's Stdout JSON shape for a query result.
type queryOutput struct {
	Type     string   `json:"type"`
	Warnings []string `json:"warnings,omitempty"`
	Result   any      `json:"result"`
	Scalar   *float64 `json:"scalar,omitempty"`
}
