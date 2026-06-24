// Package hosts defines the host search record model and pluggable cloud backends.
package hosts

import (
	"context"
	"net/netip"
	"regexp"
	"sort"
	"strings"
)

// Backend is implemented by each cloud integration.
type Backend interface {
	ID() string
	// BackendName is an optional config label (YAML backends.*.name) used with --backends.
	// Empty for unnamed backends (e.g. default flag-only quartet).
	BackendName() string
	// CacheIdentity distinguishes multiple instances with the same ID() (e.g. two GCP projects).
	// May be empty when a single implicit backend is used.
	CacheIdentity() string
	Search(ctx context.Context, q Query) ([]Record, error)
}

// Query carries global search filters for a search run.
type Query struct {
	NameSubstring string
	NameRegex     string
	Providers     []string // e.g. gcp, aws, k8s, consul — empty means all
	Backends      []string // e.g. aws:dev, gcp:prod — empty means all
}

// MatchesName applies NameSubstring, NameRegex, or accepts all if both empty.
func (q Query) MatchesName(name string) (bool, error) {
	if q.NameRegex != "" {
		re, err := regexp.Compile(q.NameRegex)
		if err != nil {
			return false, err
		}
		return re.MatchString(name), nil
	}
	if q.NameSubstring != "" {
		return containsFold(name, q.NameSubstring), nil
	}
	return true, nil
}

func containsFold(s, sub string) bool {
	if sub == "" {
		return true
	}
	ls := toLowerASCII(s)
	lsub := toLowerASCII(sub)
	for i := 0; i+len(lsub) <= len(ls); i++ {
		if ls[i:i+len(lsub)] == lsub {
			return true
		}
	}
	return false
}

func toLowerASCII(s string) string {
	b := make([]byte, len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		b[i] = c
	}
	return string(b)
}

// Record is a normalized host across cloud providers.
type Record struct {
	Provider  string                    `json:"provider"`
	Name      string                    `json:"name"`
	PrimaryIP string                    `json:"primary_ip"`
	ExtraIPs  []string                  `json:"extra_ips,omitempty"`
	Zone      string                    `json:"zone,omitempty"`
	Region    string                    `json:"region,omitempty"`
	Meta      map[string]string         `json:"meta,omitempty"`
	Vars      map[string]InventoryValue `json:"vars,omitempty"`
	Groups    []string                  `json:"groups,omitempty"`
}

// DedupeKey returns a stable key for deduplication.
func (r Record) DedupeKey() string {
	return strings.Join([]string{r.Provider, r.Name, r.PrimaryIP}, "\x00")
}

// IsDocker reports whether r is a connectable Docker container or swarm task row.
func (r Record) IsDocker() bool {
	if r.Provider != "docker" {
		return false
	}
	k := strings.ToLower(strings.TrimSpace(r.Meta["kind"]))
	if k == "container" || k == "swarm_task" {
		return true
	}
	// Rows from older builds or clients that omit meta.kind but include container_id.
	return strings.TrimSpace(r.Meta["container_id"]) != ""
}

// IsTrueNASAPIShell reports whether r is a TrueNAS row that can use /websocket/shell
// (shape only; backend config is checked at runtime).
func (r Record) IsTrueNASAPIShell() bool {
	if r.Provider != "truenas" {
		return false
	}
	kind := strings.ToLower(strings.TrimSpace(r.Meta["kind"]))
	switch kind {
	case "appliance":
		return true
	case "virt_instance":
		return strings.TrimSpace(r.Meta["id"]) != ""
	case "vm":
		if strings.TrimSpace(r.Meta["virt_instance_id"]) != "" {
			return true
		}
		return strings.TrimSpace(r.Name) != ""
	default:
		return false
	}
}

// PrimaryIPTrimmed returns r.PrimaryIP with surrounding whitespace removed.
func (r Record) PrimaryIPTrimmed() string {
	return strings.TrimSpace(r.PrimaryIP)
}

// IsConnectable reports whether honey can exec, upload, or open a terminal on r.
func (r Record) IsConnectable() bool {
	if r.IsDocker() {
		return strings.TrimSpace(r.Meta["container_id"]) != ""
	}
	if r.IsTrueNASAPIShell() {
		return true
	}
	if strings.TrimSpace(r.PrimaryIP) != "" {
		return true
	}
	return r.Provider == "k8s" && strings.EqualFold(strings.TrimSpace(r.Meta["kind"]), "pod")
}

// ExternalIP returns the VM's public/out-of-VPC address when present.
// GCP and AWS store it in ExtraIPs while PrimaryIP is the private address used for SSH.
func (r Record) ExternalIP() string {
	primary := strings.TrimSpace(r.PrimaryIP)
	if primary != "" && isPublicUnicast(primary) {
		return primary
	}
	for _, ip := range r.ExtraIPs {
		ip = strings.TrimSpace(ip)
		if ip != "" && isPublicUnicast(ip) {
			return ip
		}
	}
	return ""
}

// NodeDisplayIP prefers ExternalIP for UI tables; falls back to PrimaryIP.
func (r Record) NodeDisplayIP() string {
	if ext := r.ExternalIP(); ext != "" {
		return ext
	}
	return strings.TrimSpace(r.PrimaryIP)
}

func isPublicUnicast(s string) bool {
	addr, err := netip.ParseAddr(s)
	if err != nil {
		return false
	}
	return addr.IsValid() && !addr.IsPrivate() && !addr.IsLoopback() && !addr.IsLinkLocalUnicast() && !addr.IsMulticast() && !addr.IsUnspecified()
}

// RecordSet is a collection of records.
type RecordSet []Record

// MergeDedupe merges multiple slices into this RecordSet and removes exact duplicates by DedupeKey.
func (rs RecordSet) MergeDedupe(others ...RecordSet) RecordSet {
	seen := make(map[string]struct{})
	var out RecordSet
	// Add current set
	for _, h := range rs {
		k := h.DedupeKey()
		if _, ok := seen[k]; !ok {
			seen[k] = struct{}{}
			out = append(out, h)
		}
	}

	for _, sl := range others {
		for _, h := range sl {
			k := h.DedupeKey()
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
