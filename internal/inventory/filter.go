package inventory

import (
	"fmt"
	"strings"

	"github.com/shareed2k/honey/internal/hosts"
)

// IsFilterToken reports whether token is an inventory-aware search filter.
func IsFilterToken(token string) bool {
	token = strings.TrimSpace(token)
	return strings.HasPrefix(token, "group:") || strings.HasPrefix(token, "var:")
}

// FilterRecords returns records matching all filters.
func FilterRecords(records []hosts.Record, filters []string) ([]hosts.Record, error) {
	if len(filters) == 0 {
		return records, nil
	}
	if err := validateFilters(filters); err != nil {
		return nil, err
	}
	out := make([]hosts.Record, 0, len(records))
	for _, record := range records {
		ok, err := recordMatchesAll(record, filters)
		if err != nil {
			return nil, err
		}
		if ok {
			out = append(out, record)
		}
	}
	return out, nil
}

func validateFilters(filters []string) error {
	for _, raw := range filters {
		filter := strings.TrimSpace(raw)
		if filter == "" {
			continue
		}
		switch {
		case strings.HasPrefix(filter, "group:"):
			if strings.TrimSpace(strings.TrimPrefix(filter, "group:")) == "" {
				return fmt.Errorf("group filter %q: empty group", filter)
			}
		case strings.HasPrefix(filter, "var:"):
			if _, _, err := parseVarFilter(strings.TrimSpace(strings.TrimPrefix(filter, "var:"))); err != nil {
				return err
			}
		default:
			return fmt.Errorf("unsupported inventory filter %q", filter)
		}
	}
	return nil
}

func recordMatchesAll(record hosts.Record, filters []string) (bool, error) {
	for _, raw := range filters {
		filter := strings.TrimSpace(raw)
		if filter == "" {
			continue
		}
		var ok bool
		var err error
		switch {
		case strings.HasPrefix(filter, "group:"):
			ok = recordHasGroup(record, strings.TrimSpace(strings.TrimPrefix(filter, "group:")))
		case strings.HasPrefix(filter, "var:"):
			ok, err = recordHasVar(record, strings.TrimSpace(strings.TrimPrefix(filter, "var:")))
		default:
			return false, fmt.Errorf("unsupported inventory filter %q", filter)
		}
		if err != nil || !ok {
			return ok, err
		}
	}
	return true, nil
}

func recordHasGroup(record hosts.Record, group string) bool {
	if group == "" {
		return false
	}
	for _, got := range record.Groups {
		if got == group {
			return true
		}
	}
	return false
}

func recordHasVar(record hosts.Record, expr string) (bool, error) {
	key, want, err := parseVarFilter(expr)
	if err != nil {
		return false, err
	}
	got, ok := record.Vars[key]
	return ok && got.String() == want, nil
}

func parseVarFilter(expr string) (string, string, error) {
	key, want, ok := strings.Cut(expr, "=")
	if !ok {
		return "", "", fmt.Errorf("var filter %q: expected var:key=value", expr)
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", fmt.Errorf("var filter %q: empty key", expr)
	}
	return key, want, nil
}
