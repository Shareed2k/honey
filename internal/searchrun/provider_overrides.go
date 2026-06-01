package searchrun

import "encoding/json"

// ProviderOverrides is an opaque map of provider key → raw JSON config override.
// Each factory knows its own key and unmarshals its own section — callers need not
// know what fields any provider accepts.
type ProviderOverrides map[string]json.RawMessage

// FirstNonEmpty returns the first non-blank string from vals.
func FirstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
