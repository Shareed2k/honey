package dockerprovider

import (
	"context"
	"fmt"
	"strings"

	"github.com/moby/moby/api/types/swarm"
	"github.com/moby/moby/client"

	"github.com/shareed2k/honey/internal/hosts"
)

func searchSwarm(ctx context.Context, cli *client.Client, q hosts.Query, hostURI, backendName string) ([]hosts.Record, error) {
	info, err := cli.Info(ctx, client.InfoOptions{})
	if err != nil {
		return nil, err
	}
	if info.Info.Swarm.LocalNodeState == swarm.LocalNodeStateInactive {
		return nil, nil
	}

	services, err := cli.ServiceList(ctx, client.ServiceListOptions{})
	if err != nil {
		return nil, err
	}
	serviceNames := make(map[string]string, len(services.Items))
	for _, svc := range services.Items {
		serviceNames[svc.ID] = swarmServiceName(svc)
	}

	taskFilter := make(client.Filters).Add("desired-state", "running")
	tasks, err := cli.TaskList(ctx, client.TaskListOptions{Filters: taskFilter})
	if err != nil {
		return nil, err
	}

	out := make([]hosts.Record, 0, len(tasks.Items))
	for _, task := range tasks.Items {
		svcName := serviceNames[task.ServiceID]
		display := swarmTaskDisplayName(svcName, task)
		ok, err := q.MatchesName(display)
		if err != nil {
			return nil, err
		}
		if !ok {
			continue
		}
		containerID := ""
		if task.Status.ContainerStatus != nil {
			containerID = strings.TrimSpace(task.Status.ContainerStatus.ContainerID)
		}
		meta := map[string]string{
			"kind":           "swarm_task",
			"task_id":        task.ID,
			"service_id":     task.ServiceID,
			"service_name":   svcName,
			"node_id":        task.NodeID,
			"state":          string(task.Status.State),
			"docker_host":    hostURI,
			"docker_backend": backendName,
		}
		if containerID != "" {
			meta["container_id"] = containerID
		}
		for k, v := range task.Labels {
			meta["label_"+k] = v
		}
		if containerID == "" {
			// Still list task but exec/file ops will fail until container exists.
			meta["status"] = "no container yet"
		}
		out = append(out, hosts.Record{
			Provider: "docker",
			Name:     display,
			Meta:     meta,
		})
	}
	return out, nil
}

func swarmServiceName(svc swarm.Service) string {
	if svc.Spec.Name != "" {
		return svc.Spec.Name
	}
	return svc.ID[:12]
}

func swarmTaskDisplayName(svcName string, task swarm.Task) string {
	suffix := task.ID
	if len(suffix) > 12 {
		suffix = suffix[:12]
	}
	if svcName == "" {
		return fmt.Sprintf("task-%s", suffix)
	}
	return fmt.Sprintf("%s.%s", svcName, suffix)
}
