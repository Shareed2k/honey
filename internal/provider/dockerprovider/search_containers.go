package dockerprovider

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/client"

	"github.com/shareed2k/honey/internal/hosts"
)

func searchContainers(ctx context.Context, cli *client.Client, q hosts.Query, hostURI, backendName string, metaBase map[string]string) ([]hosts.Record, error) {
	listResult, err := cli.ContainerList(ctx, ListOptionsForBackend(q.DockerAllContainers))
	if err != nil {
		return nil, err
	}
	out := make([]hosts.Record, 0, len(listResult.Items))
	for _, c := range listResult.Items {
		name := containerDisplayName(c.Names)
		ok, err := hosts.NameMatches(name, q)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		meta := map[string]string{
			"kind":           "container",
			"container_id":   c.ID,
			"image":          c.Image,
			"state":          string(c.State),
			"status":         c.Status,
			"docker_host":    hostURI,
			"docker_backend": backendName,
		}
		for k, v := range metaBase {
			meta[k] = v
		}
		for k, v := range c.Labels {
			meta["label_"+k] = v
		}
		out = append(out, hosts.Record{
			Provider: "docker",
			Name:     name,
			Meta:     meta,
		})
	}
	return out, nil
}

func containerDisplayName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	n := strings.TrimPrefix(names[0], "/")
	if n != "" {
		return n
	}
	return names[0]
}

func searchBackend(ctx context.Context, bc BackendConfig, q hosts.Query, opts APIClientOptions) ([]hosts.Record, error) {
	cli, err := NewAPIClient(ctx, bc, opts)
	if err != nil {
		return nil, err
	}
	defer cli.Close()

	mode := NormalizeMode(q.DockerMode)
	if mode == "" {
		mode = NormalizeMode(bc.Mode)
	}
	hostURI := bc.ResolvedHost()
	backendName := strings.TrimSpace(bc.Name)

	hop, _, _ := ResolveSSHHop(bc, opts.VMRecord)
	metaBase := RecordMetaBase(bc, hop, opts.VMRecord != nil)
	if opts.VMRecord != nil {
		metaBase = mergeVMNodeMeta(metaBase, *opts.VMRecord, true)
	}

	var out []hosts.Record
	if mode == "containers" || mode == "both" {
		recs, err := searchContainers(ctx, cli, q, hostURI, backendName, metaBase)
		if err != nil {
			return nil, err
		}
		out = append(out, recs...)
	}
	if mode == "swarm" || mode == "both" {
		recs, err := searchSwarm(ctx, cli, q, hostURI, backendName)
		if err != nil {
			return nil, err
		}
		for i := range recs {
			for k, v := range metaBase {
				if recs[i].Meta == nil {
					recs[i].Meta = make(map[string]string)
				}
				recs[i].Meta[k] = v
			}
		}
		out = append(out, recs...)
	}
	if opts.VMRecord != nil {
		for i := range out {
			applyVMNodeRecordFields(&out[i], *opts.VMRecord)
		}
	}
	return out, nil
}

// searchVMContainers lists containers on one cloud VM via Honey SSH (auto-discover pass).
func searchVMContainers(ctx context.Context, vm hosts.Record, q hosts.Query) ([]hosts.Record, error) {
	socket := strings.TrimSpace(vm.Meta["docker_discover_socket"])
	if socket == "" {
		socket = strings.TrimSpace(q.DockerSocket)
	}
	platform := strings.TrimSpace(vm.Meta["docker_discover_platform"])
	if platform == "" {
		platform = strings.TrimSpace(q.DockerPlatform)
	}
	runAs := strings.TrimSpace(vm.Meta["docker_discover_run_as"])

	bc := BackendConfig{
		SSHUser:  strings.TrimSpace(q.DockerSSHUser),
		Socket:   socket,
		Platform: platform,
		RunAs:    runAs,
		Mode:     q.DockerMode,
	}
	opts := APIClientOptions{
		SSHUser:  q.DockerSSHUser,
		VMRecord: &vm,
		DiscoverOpts: &DiscoverOpts{
			Socket:   socket,
			Platform: platform,
			RunAs:    runAs,
		},
	}
	recs, err := searchBackend(ctx, bc, q, opts)
	if err != nil {
		return nil, fmt.Errorf("%s/%s: %w", vm.Provider, vm.Name, err)
	}
	return recs, nil
}
