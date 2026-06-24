package truenasprovider

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/hosts"
)

const defaultDialTimeout = 30 * time.Second

// TrueNAS discovers a SCALE appliance and optional guests via the WebSocket API.
type TrueNAS struct {
	Name             string
	URL              string
	Username         string
	APIKey           string
	Insecure         bool
	IncludeAppliance bool
	IncludeVMs       bool
	IncludeVirt      bool
	SSHUser          string
}

// ID returns the honey backend identifier.
func (TrueNAS) ID() string { return "truenas" }

// BackendName returns the optional YAML backends.truenas[].name value.
func (t *TrueNAS) BackendName() string { return strings.TrimSpace(t.Name) }

// CacheIdentity scopes cache entries per controller URL and API user.
func (t *TrueNAS) CacheIdentity() string {
	return strings.TrimSpace(t.Name) + "\x1e" + strings.TrimSpace(t.URL) + "\x1e" + strings.TrimSpace(t.Username)
}

var _ hosts.Backend = (*TrueNAS)(nil)

// Search returns appliance and guest records from TrueNAS SCALE.
func (t *TrueNAS) Search(ctx context.Context, q hosts.Query) ([]hosts.Record, error) {
	if strings.TrimSpace(t.URL) == "" {
		return nil, nil
	}
	wsURL, mgmtHost, err := normalizeWSURL(t.URL, t.Insecure)
	if err != nil {
		return nil, err
	}
	apiKey := strings.TrimSpace(t.APIKey)
	if apiKey == "" {
		return nil, fmt.Errorf("truenas %s: api_key is required", t.URL)
	}
	user := strings.TrimSpace(t.Username)

	dialCtx, cancel := context.WithTimeout(ctx, defaultDialTimeout)
	defer cancel()

	client, err := NewClient(dialCtx, wsURL, user, apiKey, t.Insecure)
	if err != nil {
		return nil, err
	}
	defer func() { _ = client.Close() }()

	var out []hosts.Record
	if t.IncludeAppliance {
		rec, err := searchAppliance(ctx, client, t, mgmtHost, q)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			out = append(out, *rec)
		}
	}
	if t.IncludeVMs {
		virtByName, err := virtInstanceNameIndex(ctx, client)
		if err != nil {
			return nil, err
		}
		recs, err := searchVMs(ctx, client, q, virtByName)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	if t.IncludeVirt {
		recs, err := searchVirtInstances(ctx, client, q)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	return out, nil
}

func searchAppliance(ctx context.Context, client *Client, t *TrueNAS, mgmtHost string, q hosts.Query) (*hosts.Record, error) {
	var info struct {
		Hostname string `json:"hostname"`
		Version  string `json:"version"`
	}
	_ = client.Call(ctx, "system.info", nil, &info)

	name := strings.TrimSpace(info.Hostname)
	if name == "" {
		name = strings.TrimSpace(t.Name)
	}
	if name == "" {
		name = mgmtHost
	}
	ok, err := q.MatchesName(name)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	primaryIP := mgmtHost
	if ip := net.ParseIP(mgmtHost); ip != nil {
		primaryIP = ip.String()
	}

	meta := map[string]string{
		"kind":        "appliance",
		"truenas_url": strings.TrimSpace(t.URL),
	}
	version := strings.TrimSpace(info.Version)
	if version == "" {
		version = fetchSystemVersion(ctx, client)
	}
	if version != "" {
		meta["version"] = version
	}
	if h := strings.TrimSpace(info.Hostname); h != "" {
		meta["hostname"] = h
	}
	if u := strings.TrimSpace(t.SSHUser); u != "" {
		meta["ssh_user"] = u
	}

	return &hosts.Record{
		Provider:  "truenas",
		Name:      name,
		PrimaryIP: primaryIP,
		Meta:      meta,
	}, nil
}

type vmRow struct {
	ID    int64  `json:"id"`
	Name  string `json:"name"`
	State string `json:"state"`
}

func virtInstanceNameIndex(ctx context.Context, client *Client) (map[string]string, error) {
	var rows []virtRow
	if err := client.Call(ctx, "virt.instance.query", nil, &rows); err != nil {
		return nil, fmt.Errorf("truenas virt.instance.query: %w", err)
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.Name)
		id := strings.TrimSpace(row.ID)
		if name != "" && id != "" {
			out[name] = id
		}
	}
	return out, nil
}

func searchVMs(ctx context.Context, client *Client, q hosts.Query, virtByName map[string]string) ([]hosts.Record, error) {
	var rows []vmRow
	if err := client.Call(ctx, "vm.query", nil, &rows); err != nil {
		return nil, fmt.Errorf("truenas vm.query: %w", err)
	}
	out := make([]hosts.Record, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			continue
		}
		state := strings.ToUpper(strings.TrimSpace(row.State))
		if state != "" && state != "RUNNING" {
			continue
		}
		ok, err := q.MatchesName(name)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		meta := map[string]string{
			"kind":   "vm",
			"vm_id":  fmt.Sprintf("%d", row.ID),
			"state":  strings.TrimSpace(row.State),
			"status": strings.TrimSpace(row.State),
		}
		if virtByName != nil {
			if vid, ok := virtByName[name]; ok {
				meta["virt_instance_id"] = vid
			}
		}
		out = append(out, hosts.Record{
			Provider: "truenas",
			Name:     name,
			Meta:     meta,
		})
	}
	return out, nil
}

type virtAlias struct {
	Type    string `json:"type"`
	Address string `json:"address"`
}

type virtRow struct {
	ID      string      `json:"id"`
	Name    string      `json:"name"`
	Status  string      `json:"status"`
	Type    string      `json:"type"`
	Aliases []virtAlias `json:"aliases"`
}

func searchVirtInstances(ctx context.Context, client *Client, q hosts.Query) ([]hosts.Record, error) {
	var rows []virtRow
	if err := client.Call(ctx, "virt.instance.query", nil, &rows); err != nil {
		return nil, fmt.Errorf("truenas virt.instance.query: %w", err)
	}
	out := make([]hosts.Record, 0, len(rows))
	for _, row := range rows {
		name := strings.TrimSpace(row.Name)
		if name == "" {
			continue
		}
		status := strings.ToUpper(strings.TrimSpace(row.Status))
		if status != "" && status != "RUNNING" {
			continue
		}
		ok, err := q.MatchesName(name)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		primary, extras := ipsFromVirtAliases(row.Aliases)
		meta := map[string]string{
			"kind":   "virt_instance",
			"id":     strings.TrimSpace(row.ID),
			"status": strings.TrimSpace(row.Status),
		}
		if typ := strings.TrimSpace(row.Type); typ != "" {
			meta["virt_type"] = typ
		}
		out = append(out, hosts.Record{
			Provider:  "truenas",
			Name:      name,
			PrimaryIP: primary,
			ExtraIPs:  extras,
			Meta:      meta,
		})
	}
	return out, nil
}

func ipsFromVirtAliases(aliases []virtAlias) (primary string, extras []string) {
	for _, a := range aliases {
		addr := strings.TrimSpace(a.Address)
		if addr == "" {
			continue
		}
		ip := net.ParseIP(addr)
		if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
			continue
		}
		if ip.To4() == nil {
			continue
		}
		s := ip.String()
		if primary == "" {
			primary = s
		} else {
			extras = append(extras, s)
		}
	}
	return primary, extras
}

// systemVersion is used when system.info lacks version (older responses).
func fetchSystemVersion(ctx context.Context, client *Client) string {
	var raw json.RawMessage
	if err := client.Call(ctx, "system.version", nil, &raw); err != nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return strings.TrimSpace(s)
	}
	return strings.TrimSpace(string(raw))
}
