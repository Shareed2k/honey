package consulprovider

import (
	"context"
	"os"
	"strings"

	"github.com/hashicorp/consul/api"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/hosts"
)

// Consul lists catalog nodes (and basic service metadata).
type Consul struct {
	Name       string // optional config label (--backends)
	Addr       string
	Datacenter string
	Token      string
}

// ID returns the honey backend identifier ("consul").
func (Consul) ID() string { return "consul" }

// BackendName returns the optional YAML backends.consul[].name value.
func (c *Consul) BackendName() string { return strings.TrimSpace(c.Name) }

// CacheIdentity scopes cache entries per Consul address/datacenter (token excluded).
func (c *Consul) CacheIdentity() string {
	return strings.TrimSpace(c.Name) + "\x1e" + c.Addr + "\x1e" + c.Datacenter
}

var _ hosts.Backend = (*Consul)(nil)

// Search returns Consul catalog nodes matching the query.
func (c *Consul) Search(ctx context.Context, q hosts.Query) ([]hosts.Record, error) {
	cfg := api.DefaultConfig()
	addr := c.Addr
	if q.ConsulAddr != "" {
		addr = q.ConsulAddr
	}
	if addr != "" {
		cfg.Address = strings.TrimPrefix(addr, "http://")
		cfg.Address = strings.TrimPrefix(cfg.Address, "https://")
	} else if v := os.Getenv("CONSUL_HTTP_ADDR"); v != "" {
		cfg.Address = strings.TrimPrefix(strings.TrimPrefix(v, "http://"), "https://")
	}
	tok := c.Token
	if q.ConsulToken != "" {
		tok = q.ConsulToken
	}
	if tok != "" {
		cfg.Token = tok
	} else if v := os.Getenv("CONSUL_HTTP_TOKEN"); v != "" {
		cfg.Token = v
	}

	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	opts := &api.QueryOptions{}
	dc := c.Datacenter
	if q.ConsulDatacenter != "" {
		dc = q.ConsulDatacenter
	}
	if dc != "" {
		opts.Datacenter = dc
	}
	opts = opts.WithContext(ctx)

	zap.L().Debug("consul starting catalog nodes query", zap.String("address", cfg.Address), zap.String("datacenter", dc))
	nodes, _, err := client.Catalog().Nodes(opts)
	if err != nil {
		return nil, err
	}

	out := make([]hosts.Record, 0, len(nodes))
	for _, n := range nodes {
		name := n.Node
		ok, err := hosts.NameMatches(name, q)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		addr := n.Address
		if addr == "" {
			continue
		}
		meta := map[string]string{}
		for k, v := range n.Meta {
			meta["label_"+k] = v
		}
		meta["datacenter"] = n.Datacenter

		out = append(out, hosts.Record{
			Provider:  "consul",
			Name:      name,
			PrimaryIP: addr,
			ExtraIPs:  nil,
			Zone:      "",
			Region:    n.Datacenter,
			Meta:      meta,
		})
	}
	return out, nil
}
