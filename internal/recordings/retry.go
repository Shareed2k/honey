package recordings

import "encoding/json"

// SucceededHosts returns a set of "name@ip" keys for hosts that succeeded in a recording.
// Hosts not present in the set (failed, skipped, or new) should be retried.
func SucceededHosts(events []Event) map[string]struct{} {
	out := make(map[string]struct{})
	for _, e := range events {
		if e.Type != "result" || len(e.Result) == 0 {
			continue
		}
		var row struct {
			Name    string `json:"Name"`
			IP      string `json:"IP"`
			Success bool   `json:"Success"`
		}
		if err := json.Unmarshal(e.Result, &row); err != nil {
			continue
		}
		if row.Success {
			out[row.Name+"@"+row.IP] = struct{}{}
		}
	}
	return out
}
