// Package inventory converts honey host records into Ansible-compatible JSON inventory.
package inventory

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/shareed2k/honey/internal/hosts"
)

const (
	groupHoneyPrefix      = "honey"
	groupProviderPrefix   = "honey_provider"
	groupBackendPrefix    = "honey_backend"
	groupRegionPrefix     = "honey_region"
	groupZonePrefix       = "honey_zone"
	groupTagPrefix        = "honey_tag"
	groupLabelPrefix      = "honey_label"
	metaHostvarsKey       = "hostvars"
	metaKey               = "_meta"
	hostvarAnsibleHost    = "ansible_host"
	hostvarAnsibleUser    = "ansible_user"
	hostvarHoneyProvider  = "honey_provider"
	hostvarHoneyName      = "honey_name"
	hostvarHoneyPrimaryIP = "honey_primary_ip"
	hostvarHoneyExtraIPs  = "honey_extra_ips"
	hostvarHoneyZone      = "honey_zone"
	hostvarHoneyRegion    = "honey_region"
	hostvarMetaPrefix     = "honey_meta_"
)

// groupPrefixer determines group names based on stripPrefix flag
func groupPrefix(base, val string, stripPrefix bool) string {
	if stripPrefix {
		return sanitizeLabel(val)
	}
	return base + "_" + sanitizeLabel(val)
}

func isBlacklisted(blacklist []string, val string) bool {
	if len(blacklist) == 0 {
		return false
	}
	val = strings.ToLower(strings.TrimSpace(val))
	valTrimmed := strings.TrimPrefix(val, "label_")

	for _, b := range blacklist {
		b = strings.ToLower(strings.TrimSpace(b))
		if b == val || b == valTrimmed {
			return true
		}
	}
	return false
}

// AnsibleList builds the JSON object returned for `ansible-inventory` / dynamic inventory `--list`.
// Groups: honey (all hosts), honey_provider_<p>, honey_region_<r>, honey_zone_<z> when fields are set.
// Each host gets ansible_host from PrimaryIP when non-empty; connection falls back to the inventory name when empty.
func AnsibleList(records []hosts.Record, ansibleUser string, stripPrefix bool, blacklist []string) map[string]any {
	hostvars := make(map[string]any)
	keys := ansibleHostKeys(records)

	honeyHosts := make([]string, 0, len(records))
	byProvider := map[string][]string{}
	byRegion := map[string][]string{}
	byZone := map[string][]string{}
	byTag := map[string][]string{}
	byLabel := map[string][]string{}
	byProviderBackend := map[string][]string{}
	byBackend := map[string][]string{}

	for i, r := range records {
		key := keys[i]
		honeyHosts = append(honeyHosts, key)

		p := sanitizeLabel(r.Provider)
		backendName := sanitizeLabel(r.Meta["backend_name"])
		if backendName == "x" {
			backendName = ""
		}

		if p != "x" {
			g := groupPrefix(groupProviderPrefix, p, stripPrefix)
			byProvider[g] = append(byProvider[g], key)

			if backendName != "" {
				bg := p + "_" + backendName
				if !stripPrefix {
					bg = groupProviderPrefix + "_" + bg
				}
				byProviderBackend[bg] = append(byProviderBackend[bg], key)
			}
		}

		if backendName != "" {
			bg := groupPrefix(groupBackendPrefix, backendName, stripPrefix)
			byBackend[bg] = append(byBackend[bg], key)
		}

		if z := strings.TrimSpace(r.Region); z != "" {
			g := groupPrefix(groupRegionPrefix, z, stripPrefix)
			byRegion[g] = append(byRegion[g], key)
		}
		if z := strings.TrimSpace(r.Zone); z != "" {
			g := groupPrefix(groupZonePrefix, z, stripPrefix)
			byZone[g] = append(byZone[g], key)
		}

		processRecordTags(r, key, p, backendName, stripPrefix, blacklist, byTag)
		processRecordLabels(r, key, p, backendName, stripPrefix, blacklist, byLabel)

		hostvars[key] = hostvarsForRecord(r, ansibleUser, stripPrefix, blacklist)
	}

	out := map[string]any{
		groupHoneyPrefix: map[string]any{
			"hosts": sortedCopy(honeyHosts),
		},
		metaKey: map[string]any{
			metaHostvarsKey: hostvars,
		},
	}

	for g, hs := range byProvider {
		out[g] = map[string]any{"hosts": sortedCopy(hs)}
	}
	for g, hs := range byProviderBackend {
		out[g] = map[string]any{"hosts": sortedCopy(hs)}
	}
	for g, hs := range byBackend {
		out[g] = map[string]any{"hosts": sortedCopy(hs)}
	}
	for g, hs := range byRegion {
		out[g] = map[string]any{"hosts": sortedCopy(hs)}
	}
	for g, hs := range byZone {
		out[g] = map[string]any{"hosts": sortedCopy(hs)}
	}
	for g, hs := range byTag {
		out[g] = map[string]any{"hosts": sortedCopy(hs)}
	}
	for g, hs := range byLabel {
		out[g] = map[string]any{"hosts": sortedCopy(hs)}
	}

	return out
}

// AnsibleHostVars returns host variables for a single inventory hostname (dynamic inventory `--host`).
func AnsibleHostVars(records []hosts.Record, ansibleUser, hostname string, stripPrefix bool, blacklist []string) (map[string]any, error) {
	keys := ansibleHostKeys(records)
	for i, k := range keys {
		if k == hostname {
			return hostvarsForRecord(records[i], ansibleUser, stripPrefix, blacklist), nil
		}
	}
	return nil, fmt.Errorf("unknown host %q", hostname)
}

func hostvarsForRecord(r hosts.Record, ansibleUser string, stripPrefix bool, blacklist []string) map[string]any {
	hv := make(map[string]any)
	if ip := strings.TrimSpace(r.PrimaryIP); ip != "" {
		hv[hostvarAnsibleHost] = ip
	}
	if u := strings.TrimSpace(ansibleUser); u != "" {
		hv[hostvarAnsibleUser] = u
	}

	if stripPrefix {
		hv["provider"] = r.Provider
		hv["name"] = r.Name
		hv["primary_ip"] = r.PrimaryIP
		if len(r.ExtraIPs) > 0 {
			hv["extra_ips"] = append([]string(nil), r.ExtraIPs...)
		}
		if r.Zone != "" {
			hv["zone"] = r.Zone
		}
		if r.Region != "" {
			hv["region"] = r.Region
		}
	} else {
		hv[hostvarHoneyProvider] = r.Provider
		hv[hostvarHoneyName] = r.Name
		hv[hostvarHoneyPrimaryIP] = r.PrimaryIP
		if len(r.ExtraIPs) > 0 {
			hv[hostvarHoneyExtraIPs] = append([]string(nil), r.ExtraIPs...)
		}
		if r.Zone != "" {
			hv[hostvarHoneyZone] = r.Zone
		}
		if r.Region != "" {
			hv[hostvarHoneyRegion] = r.Region
		}
	}

	for mk, mv := range r.Meta {
		if isBlacklisted(blacklist, mk) {
			continue
		}

		if mk == "tags" {
			var filtered []string
			for _, t := range strings.Split(mv, ",") {
				t = strings.TrimSpace(t)
				if t != "" && !isBlacklisted(blacklist, t) {
					filtered = append(filtered, t)
				}
			}
			if len(filtered) == 0 {
				continue
			}
			mv = strings.Join(filtered, ",")
		}

		k := sanitizeMetaKey(mk)
		if k == "" {
			continue
		}

		var finalValue any = mv
		// K8s ports are a comma-separated string (e.g. "80,443").
		// We split it into a native string slice for cleaner Ansible JSON output.
		if k == "ports" {
			// Backwards compatibility if it's still a JSON string in some cache
			finalValue = strings.Split(mv, ",")
		}

		if stripPrefix {
			hv[k] = finalValue
		} else {
			hv[hostvarMetaPrefix+k] = finalValue
		}
	}
	return hv
}

func ansibleHostKeys(records []hosts.Record) []string {
	keys := make([]string, len(records))
	used := map[string]struct{}{}
	for i, r := range records {
		base := sanitizeHostName(r.Name)
		if base == "" {
			base = ipHostFallback(r.PrimaryIP)
		}
		if base == "" {
			base = "host"
		}
		keys[i] = uniqueHostKey(base, r.Provider, used)
	}
	return keys
}

func uniqueHostKey(base, provider string, used map[string]struct{}) string {
	try := base
	if _, ok := used[try]; !ok {
		used[try] = struct{}{}
		return try
	}
	suf := sanitizeLabel(provider)
	if suf == "" || suf == "x" {
		suf = "p"
	}
	try = base + "__" + suf
	if _, ok := used[try]; !ok {
		used[try] = struct{}{}
		return try
	}
	for n := 2; ; n++ {
		try = fmt.Sprintf("%s__%s__%d", base, suf, n)
		if _, ok := used[try]; !ok {
			used[try] = struct{}{}
			return try
		}
	}
}

func ipHostFallback(ip string) string {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return ""
	}
	return "ip_" + strings.ReplaceAll(strings.ReplaceAll(ip, ":", "_"), ".", "_")
}

func sanitizeHostName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		case unicode.IsSpace(r):
			b.WriteByte('-')
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-_.")
	for strings.Contains(out, "--") {
		out = strings.ReplaceAll(out, "--", "-")
	}
	return out
}

func sanitizeLabel(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '_', r == '-', r == '.', r == '/', r == ':':
			b.WriteByte('_')
		default:
			b.WriteByte('_')
		}
	}
	o := strings.Trim(b.String(), "_")
	for strings.Contains(o, "__") {
		o = strings.ReplaceAll(o, "__", "_")
	}
	if o == "" {
		return "x"
	}
	return o
}

func sanitizeMetaKey(k string) string {
	k = strings.TrimSpace(strings.ToLower(k))
	k = strings.ReplaceAll(k, "-", "_")
	return sanitizeLabel(k)
}

func sortedCopy(xs []string) []string {
	cp := append([]string(nil), xs...)
	sort.Strings(cp)
	return cp
}
