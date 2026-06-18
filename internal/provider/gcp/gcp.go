package gcp

import (
	"context"
	"os"
	"strconv"
	"strings"

	compute "cloud.google.com/go/compute/apiv1"
	computepb "cloud.google.com/go/compute/apiv1/computepb"
	"go.uber.org/zap"
	"google.golang.org/api/iterator"

	"github.com/shareed2k/honey/internal/hosts"
)

// GCP implements provider.Provider for Compute Engine instances.
type GCP struct {
	Name    string // optional config label (--backends)
	Project string
	Zone    string // empty = aggregated all zones
}

// ID returns the honey backend identifier ("gcp").
func (GCP) ID() string { return "gcp" }

// BackendName returns the optional YAML backends.gcp[].name value.
func (g *GCP) BackendName() string { return strings.TrimSpace(g.Name) }

// CacheIdentity scopes cache entries per configured project/zone.
func (g *GCP) CacheIdentity() string {
	return strings.TrimSpace(g.Name) + "\x1e" + g.Project + "\x1e" + g.Zone
}

var _ hosts.Backend = (*GCP)(nil)

// Search returns Compute Engine instances matching the query.
func (g *GCP) Search(ctx context.Context, q hosts.Query) (out []hosts.Record, err error) {
	project := g.Project
	if project == "" {
		project = os.Getenv("GOOGLE_CLOUD_PROJECT")
	}
	if project == "" {
		project = os.Getenv("GCP_PROJECT")
	}
	if project == "" {
		return []hosts.Record{}, nil
	}

	client, err := compute.NewInstancesRESTClient(ctx)
	if err != nil {
		return nil, err
	}
	zap.L().Debug("gcp starting search", zap.String("project", project))
	defer func() {
		if cerr := client.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	zone := g.Zone

	if zone != "" {
		it := client.List(ctx, &computepb.ListInstancesRequest{
			Project: project,
			Zone:    zone,
		})
		for {
			inst, err := it.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, err
			}
			h, ok, err := instanceToRecord(inst, q)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, h)
			}
		}
		return out, nil
	}

	it := client.AggregatedList(ctx, &computepb.AggregatedListInstancesRequest{
		Project: project,
	})
	for {
		pair, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		sl := pair.Value
		if sl == nil {
			continue
		}
		for _, inst := range sl.GetInstances() {
			h, ok, err := instanceToRecord(inst, q)
			if err != nil {
				return nil, err
			}
			if ok {
				out = append(out, h)
			}
		}
	}
	return out, nil
}

func instanceToRecord(inst *computepb.Instance, q hosts.Query) (hosts.Record, bool, error) {
	name := inst.GetName()
	ok, err := q.MatchesName(name)
	if err != nil {
		return hosts.Record{}, false, err
	}
	if !ok {
		return hosts.Record{}, false, nil
	}
	st := inst.GetStatus()
	if st != "RUNNING" && st != "STAGING" {
		return hosts.Record{}, false, nil
	}

	meta := map[string]string{
		"status": st,
		"id":     strconv.FormatUint(inst.GetId(), 10),
	}

	if tags := inst.GetTags(); tags != nil && len(tags.Items) > 0 {
		meta["tags"] = strings.Join(tags.Items, ",")
	}

	for k, v := range inst.GetLabels() {
		meta["label_"+k] = v
	}
	// Prefer internal (VPC) addresses for PrimaryIP; NAT / ephemeral public IPs are extras
	// so SSH and in-VPC tooling default to the private path.
	var internalIPs []string
	var natIPs []string
	for _, ni := range inst.GetNetworkInterfaces() {
		if ip := strings.TrimSpace(ni.GetNetworkIP()); ip != "" {
			internalIPs = append(internalIPs, ip)
		}
		for _, ac := range ni.GetAccessConfigs() {
			if nat := strings.TrimSpace(ac.GetNatIP()); nat != "" {
				natIPs = append(natIPs, nat)
			}
		}
	}
	var primary string
	var extras []string
	if len(internalIPs) > 0 {
		primary = internalIPs[0]
		extras = append(extras, natIPs...)
		extras = append(extras, internalIPs[1:]...)
	} else if len(natIPs) > 0 {
		primary = natIPs[0]
		extras = natIPs[1:]
	}
	if primary == "" {
		return hosts.Record{}, false, nil
	}
	zoneURL := inst.GetZone()
	zone := zoneURL[strings.LastIndex(zoneURL, "/")+1:]

	return hosts.Record{
		Provider:  "gcp",
		Name:      name,
		PrimaryIP: primary,
		ExtraIPs:  extras,
		Zone:      zone,
		Region:    regionFromZone(zone),
		Meta:      meta,
	}, true, nil
}

func regionFromZone(zone string) string {
	if zone == "" {
		return ""
	}
	i := strings.LastIndex(zone, "-")
	if i <= 0 {
		return ""
	}
	return zone[:i]
}
