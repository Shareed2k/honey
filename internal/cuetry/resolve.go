package cuetry

import (
	"fmt"
	"net"
	"regexp"
	"strings"

	"hostctl/internal/hosts"
)

// MatchAllSearchHosts is a recipe step host value meaning: run this step on
// every host in the current search result set that has a PrimaryIP (same
// filter as parallel SSH in the UI).
const MatchAllSearchHosts = "*"

// MatchHostRegexPrefix starts a host value interpreted as a Go regexp
// (RE2) matched against each search row's Name. Example: re:^prod-kafka-.+$
// Use (?i) inside the pattern for case-insensitive matching.
const MatchHostRegexPrefix = "re:"

// ValidateHostField checks host syntax (empty, regex compile). Call from
// ParseRemoteRecipe; ExpandStepHosts enforces match counts at runtime.
func ValidateHostField(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("empty host key")
	}
	if host == MatchAllSearchHosts {
		return nil
	}
	if strings.HasPrefix(host, MatchHostRegexPrefix) {
		pat := strings.TrimSpace(host[len(MatchHostRegexPrefix):])
		if pat == "" {
			return fmt.Errorf("pattern after %q is empty", MatchHostRegexPrefix)
		}
		if _, err := regexp.Compile(pat); err != nil {
			return fmt.Errorf("invalid regexp: %w", err)
		}
		return nil
	}
	return nil
}

// ExpandStepHosts returns the host records one step should run against.
// If host is MatchAllSearchHosts, returns all records with a non-empty PrimaryIP
// (preserving search order). If host starts with MatchHostRegexPrefix, returns
// every record with PrimaryIP whose Name matches the regexp. Otherwise returns
// a single-element slice from ResolveHostFromRecords (literal IP or exact name match).
func ExpandStepHosts(host string, records []hosts.Record) ([]hosts.Record, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return nil, fmt.Errorf("empty host key")
	}
	if host == MatchAllSearchHosts {
		var out []hosts.Record
		for _, r := range records {
			if strings.TrimSpace(r.PrimaryIP) != "" {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("host %q needs at least one search row with PrimaryIP", MatchAllSearchHosts)
		}
		return out, nil
	}
	if strings.HasPrefix(host, MatchHostRegexPrefix) {
		pat := strings.TrimSpace(host[len(MatchHostRegexPrefix):])
		if pat == "" {
			return nil, fmt.Errorf("pattern after %q is empty", MatchHostRegexPrefix)
		}
		re, err := regexp.Compile(pat)
		if err != nil {
			return nil, fmt.Errorf("invalid regexp: %w", err)
		}
		var out []hosts.Record
		for _, r := range records {
			if strings.TrimSpace(r.PrimaryIP) == "" {
				continue
			}
			if re.MatchString(strings.TrimSpace(r.Name)) {
				out = append(out, r)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("host %q matched no search rows with PrimaryIP", host)
		}
		return out, nil
	}
	one, err := ResolveHostFromRecords(host, records)
	if err != nil {
		return nil, err
	}
	return []hosts.Record{one}, nil
}

// ResolveHostFromRecords maps recipe "host" to a record with PrimaryIP.
// If host looks like an IP address, it returns a synthetic record (Name=host).
// Otherwise it matches Record.Name with case-insensitive equality; multiple
// matches are an error.
func ResolveHostFromRecords(host string, records []hosts.Record) (hosts.Record, error) {
	host = strings.TrimSpace(host)
	if host == "" {
		return hosts.Record{}, fmt.Errorf("empty host key")
	}
	if ip := net.ParseIP(host); ip != nil {
		return hosts.Record{
			Provider:  "cue",
			Name:      host,
			PrimaryIP: host,
		}, nil
	}
	var matches []hosts.Record
	for _, r := range records {
		if strings.EqualFold(strings.TrimSpace(r.Name), host) {
			matches = append(matches, r)
		}
	}
	switch len(matches) {
	case 0:
		return hosts.Record{}, fmt.Errorf("no search result with name %q (use an IP or exact name from search)", host)
	case 1:
		if strings.TrimSpace(matches[0].PrimaryIP) == "" {
			return hosts.Record{}, fmt.Errorf("host %q has no PrimaryIP in search results", host)
		}
		return matches[0], nil
	default:
		return hosts.Record{}, fmt.Errorf("ambiguous host name %q: %d matches", host, len(matches))
	}
}
