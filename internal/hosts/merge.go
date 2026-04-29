package hosts

import (
	"sort"
	"strings"
)

// DedupeKey returns a stable key for deduplication.
func DedupeKey(h Record) string {
	return strings.Join([]string{h.Provider, h.Name, h.PrimaryIP}, "\x00")
}

// MergeDedupe merges slices and removes exact duplicates by DedupeKey.
func MergeDedupe(slices ...[]Record) []Record {
	seen := make(map[string]struct{})
	var out []Record
	for _, sl := range slices {
		for _, h := range sl {
			k := DedupeKey(h)
			if _, ok := seen[k]; ok {
				continue
			}
			seen[k] = struct{}{}
			out = append(out, h)
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Provider != out[j].Provider {
			return out[i].Provider < out[j].Provider
		}
		return out[i].Name < out[j].Name
	})
	return out
}
