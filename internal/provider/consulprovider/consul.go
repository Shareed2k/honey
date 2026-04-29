package consulprovider

import (
	"context"
	"os"
	"strings"

	"github.com/hashicorp/consul/api"

	"hostctl/internal/hosts"
)

// Consul lists catalog nodes (and basic service metadata).
type Consul struct{}

func (Consul) ID() string { return "consul" }

var _ hosts.Backend = (*Consul)(nil)

func (c *Consul) Search(ctx context.Context, q hosts.Query) ([]hosts.Record, error) {
	cfg := api.DefaultConfig()
	if q.ConsulAddr != "" {
		cfg.Address = strings.TrimPrefix(q.ConsulAddr, "http://")
		cfg.Address = strings.TrimPrefix(cfg.Address, "https://")
	} else if v := os.Getenv("CONSUL_HTTP_ADDR"); v != "" {
		cfg.Address = strings.TrimPrefix(strings.TrimPrefix(v, "http://"), "https://")
	}
	if q.ConsulToken != "" {
		cfg.Token = q.ConsulToken
	} else if v := os.Getenv("CONSUL_HTTP_TOKEN"); v != "" {
		cfg.Token = v
	}

	client, err := api.NewClient(cfg)
	if err != nil {
		return nil, err
	}

	opts := &api.QueryOptions{}
	if q.ConsulDatacenter != "" {
		opts.Datacenter = q.ConsulDatacenter
	}
	opts = opts.WithContext(ctx)

	nodes, _, err := client.Catalog().Nodes(opts)
	if err != nil {
		return nil, err
	}

	var out []hosts.Record
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
			meta[k] = v
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
