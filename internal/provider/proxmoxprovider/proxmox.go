// Package proxmoxprovider provides Proxmox search capabilities for honey.
package proxmoxprovider

import (
	"context"
	"crypto/tls"
	"fmt"
	"strings"

	"github.com/Telmate/proxmox-api-go/proxmox"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/hosts"
)

// Proxmox configures a connection to a Proxmox VE cluster.
type Proxmox struct {
	Name        string
	URL         string
	User        string
	Password    string
	TokenID     string
	TokenSecret string
	Insecure    bool
}

// ID returns the honey backend identifier.
func (Proxmox) ID() string { return "proxmox" }

// BackendName returns the optional YAML backends.proxmox[].name value.
func (p *Proxmox) BackendName() string { return strings.TrimSpace(p.Name) }

// CacheIdentity scopes cache entries per URL.
func (p *Proxmox) CacheIdentity() string {
	return strings.TrimSpace(p.Name) + "\x1e" + p.URL + "\x1e" + p.User
}

var _ hosts.Backend = (*Proxmox)(nil)

// Search returns Proxmox VMs and containers matching the query.
func (p *Proxmox) Search(ctx context.Context, q hosts.Query) ([]hosts.Record, error) {
	if p.URL == "" {
		return nil, nil // unconfigured
	}

	// #nosec G402 -- p.Insecure is explicitly configured by the user, common for Proxmox self-signed certs
	tlsConfig := &tls.Config{InsecureSkipVerify: p.Insecure}
	c, err := proxmox.NewClient(p.URL, nil, "", tlsConfig, "", 300, false)
	if err != nil {
		return nil, fmt.Errorf("proxmox init %s: %w", p.URL, err)
	}

	switch {
	case p.TokenID != "":
		var tokenID proxmox.ApiTokenID
		if err := tokenID.Parse(p.TokenID); err != nil {
			return nil, fmt.Errorf("proxmox token id %s: %w", p.TokenID, err)
		}
		c.SetAPIToken(tokenID, proxmox.ApiTokenSecret(p.TokenSecret))
	case p.User != "":
		if err := c.Login(ctx, p.User, p.Password, ""); err != nil {
			return nil, fmt.Errorf("proxmox login %s: %w", p.URL, err)
		}
	default:
		return nil, fmt.Errorf("proxmox %s requires token or user/password", p.URL)
	}

	list, err := c.GetResourceList(ctx, "vm")
	if err != nil {
		return nil, fmt.Errorf("proxmox list: %w", err)
	}

	out := make([]hosts.Record, 0, len(list))
	for _, item := range list {
		m, ok := item.(map[string]any)
		if !ok {
			continue
		}

		name, _ := m["name"].(string)
		status, _ := m["status"].(string)

		// Skip templates
		if templateRaw, ok := m["template"]; ok {
			if isTemplate, ok := templateRaw.(float64); ok && isTemplate == 1 {
				continue
			} else if isTemplateInt, ok := templateRaw.(int); ok && isTemplateInt == 1 {
				continue
			}
		}

		okMatch, err := hosts.NameMatches(name, q)
		if err != nil {
			return nil, err
		}
		if !okMatch {
			continue
		}

		vmidRaw, _ := m["vmid"].(float64)
		vmid := proxmox.GuestID(vmidRaw)
		node, _ := m["node"].(string)
		vmTypeRaw, _ := m["type"].(string)
		pool, _ := m["pool"].(string)

		primaryIP, extraIPs := p.fetchIPs(ctx, c, vmid, node, vmTypeRaw)

		meta := map[string]string{
			"kind":   vmTypeRaw, // "qemu" or "lxc"
			"node":   node,
			"vmid":   fmt.Sprintf("%d", vmid),
			"status": status,
		}
		if pool != "" {
			meta["pool"] = pool
		}
		if p.BackendName() != "" {
			meta["backend_name"] = p.BackendName()
		}

		out = append(out, hosts.Record{
			Provider:  "proxmox",
			Name:      name,
			PrimaryIP: primaryIP,
			ExtraIPs:  extraIPs,
			Zone:      node,
			Meta:      meta,
		})
	}

	return out, nil
}

func (p *Proxmox) fetchIPs(ctx context.Context, c *proxmox.Client, vmid proxmox.GuestID, node, vmType string) (string, []string) {
	vmr := proxmox.NewVmRef(vmid)
	vmr.SetNode(node)

	var gt proxmox.GuestType
	if err := gt.Parse(vmType); err != nil {
		return "", nil
	}

	switch vmType {
	case "qemu":
		return fetchQemuIPs(ctx, c, vmr, node, vmid)
	case "lxc":
		return fetchLXCIPs(ctx, c, vmr, node, vmid)
	}

	return "", nil
}

func fetchQemuIPs(ctx context.Context, c *proxmox.Client, vmr *proxmox.VmRef, node string, vmid proxmox.GuestID) (string, []string) {
	var primary string
	var extras []string

	info, state, err := vmr.GetAgentInformation(ctx, c)
	switch {
	case err != nil:
		zap.L().Debug("proxmox qemu agent info failed", zap.Uint32("vmid", uint32(vmid)), zap.Error(err))
	case state != proxmox.GuestAgentStateRunning:
		zap.L().Debug("proxmox qemu agent not running", zap.Uint32("vmid", uint32(vmid)), zap.Int8("state", int8(state)))
	case info != nil:
		for _, iface := range info.Get() {
			if iface.Name == "lo" {
				continue
			}
			for _, ip := range iface.IpAddresses {
				if ip.To4() != nil && !ip.IsLoopback() && !ip.IsLinkLocalUnicast() {
					ipStr := ip.String()
					if primary == "" {
						primary = ipStr
					} else {
						extras = append(extras, ipStr)
					}
				}
			}
		}
	}

	if primary == "" {
		primary, extras = fallbackQemuIPs(ctx, c, node, vmid, primary, extras)
	}
	return primary, extras
}

func fallbackQemuIPs(ctx context.Context, c *proxmox.Client, node string, vmid proxmox.GuestID, primary string, extras []string) (string, []string) {
	agentURL := fmt.Sprintf("/nodes/%s/qemu/%d/agent/network-get-interfaces", node, vmid)
	res, rawErr := c.GetItemConfigMapStringInterface(ctx, agentURL, "agent", "GET")
	if rawErr != nil || res == nil {
		return primary, extras
	}
	resultRaw, ok := res["result"]
	if !ok {
		return primary, extras
	}
	resultList, ok := resultRaw.([]interface{})
	if !ok {
		return primary, extras
	}
	for _, ifaceRaw := range resultList {
		iface, ok := ifaceRaw.(map[string]interface{})
		if !ok || iface["name"] == "lo" {
			continue
		}
		ipsRaw, _ := iface["ip-addresses"].([]interface{})
		for _, ipRaw := range ipsRaw {
			ipMap, ok := ipRaw.(map[string]interface{})
			if !ok {
				continue
			}
			ipType, _ := ipMap["ip-address-type"].(string)
			ipAddr, _ := ipMap["ip-address"].(string)
			if ipType == "ipv4" && ipAddr != "" && ipAddr != "127.0.0.1" {
				if primary == "" {
					primary = ipAddr
				} else {
					extras = append(extras, ipAddr)
				}
			}
		}
	}
	return primary, extras
}

func fetchLXCIPs(ctx context.Context, c *proxmox.Client, vmr *proxmox.VmRef, node string, vmid proxmox.GuestID) (string, []string) {
	var primary string
	var extras []string

	configLXC, err := proxmox.NewRawConfigLXCFromAPI(ctx, vmr, c)
	if err != nil {
		zap.L().Debug("proxmox lxc config failed", zap.Uint32("vmid", uint32(vmid)), zap.Error(err))
	} else {
		cfg := configLXC.Get(*vmr, proxmox.PowerStateUnknown)
		if cfg != nil {
			for _, netw := range cfg.Networks {
				if netw.IPv4 != nil && netw.IPv4.Address != nil {
					ipStr := netw.IPv4.Address.String()
					if idx := strings.IndexByte(ipStr, '/'); idx >= 0 {
						ipStr = ipStr[:idx]
					}
					if primary == "" {
						primary = ipStr
					} else {
						extras = append(extras, ipStr)
					}
				}
			}
		}
	}

	if primary == "" {
		primary, extras = fallbackLXCIPs(ctx, c, node, vmid, primary, extras)
	}
	return primary, extras
}

func fallbackLXCIPs(ctx context.Context, c *proxmox.Client, node string, vmid proxmox.GuestID, primary string, extras []string) (string, []string) {
	ifacesURL := fmt.Sprintf("/nodes/%s/lxc/%d/interfaces", node, vmid)

	resList, rawErr := c.GetItemConfigInterfaceArray(ctx, ifacesURL, "interfaces", "GET")
	if rawErr != nil || resList == nil {
		return primary, extras
	}

	for _, ifaceRaw := range resList {
		iface, ok := ifaceRaw.(map[string]interface{})
		if !ok || iface["name"] == "lo" {
			continue
		}
		if ipAddr, ok := iface["inet"].(string); ok && ipAddr != "" && ipAddr != "127.0.0.1" {
			if idx := strings.IndexByte(ipAddr, '/'); idx >= 0 {
				ipAddr = ipAddr[:idx]
			}
			if primary == "" {
				primary = ipAddr
			} else {
				extras = append(extras, ipAddr)
			}
		}
	}
	return primary, extras
}
