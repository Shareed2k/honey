package cuetry

import (
	"fmt"
	"strings"
	"time"
)

func validateDockerStep(d *RecipeStepDocker) error {
	actions := 0
	if d.Build != nil {
		actions++
		if strings.TrimSpace(d.Build.Context) == "" {
			return fmt.Errorf("docker.build.context is required")
		}
	}
	if d.Push != nil {
		actions++
		if strings.TrimSpace(d.Push.Image) == "" {
			return fmt.Errorf("docker.push.image is required")
		}
	}
	if d.Pull != nil {
		actions++
		if strings.TrimSpace(d.Pull.Image) == "" {
			return fmt.Errorf("docker.pull.image is required")
		}
	}
	if d.Run != nil {
		actions++
		if strings.TrimSpace(d.Run.Image) == "" {
			return fmt.Errorf("docker.run.image is required")
		}
	}
	if d.Exec != nil {
		actions++
		if strings.TrimSpace(d.Exec.Container) == "" {
			return fmt.Errorf("docker.exec.container is required")
		}
		if len(d.Exec.Command) == 0 {
			return fmt.Errorf("docker.exec.command is required")
		}
	}
	if d.Stop != nil {
		actions++
		if strings.TrimSpace(d.Stop.Container) == "" {
			return fmt.Errorf("docker.stop.container is required")
		}
	}

	if actions == 0 {
		return fmt.Errorf("docker step requires exactly one action (build, push, pull, run, exec, or stop)")
	}
	if actions > 1 {
		return fmt.Errorf("docker step allows only one action per step")
	}
	return nil
}

func validateHTTPStep(h *RecipeStepHTTP) error {
	if strings.TrimSpace(h.URL) == "" {
		return fmt.Errorf("http.url is required")
	}
	if m := strings.TrimSpace(h.Method); m != "" {
		switch m {
		case "GET", "POST", "PUT", "PATCH", "DELETE", "HEAD":
		default:
			return fmt.Errorf("http.method must be one of GET, POST, PUT, PATCH, DELETE, HEAD, got %q", m)
		}
	}
	if t := strings.TrimSpace(h.Timeout); t != "" {
		if _, err := time.ParseDuration(t); err != nil {
			return fmt.Errorf("http.timeout %q is not a valid duration: %w", t, err)
		}
	}
	for _, code := range h.ExpectStatus {
		if code < 100 || code > 599 {
			return fmt.Errorf("http.expect_status %d is out of range (100-599)", code)
		}
	}
	return nil
}

func validateK8sStep(k *RecipeStepK8s) error {
	actions := 0
	if k.Apply != nil {
		actions++
		if strings.TrimSpace(k.Apply.Manifest) == "" {
			return fmt.Errorf("k8s.apply.manifest is required")
		}
	}
	if k.Delete != nil {
		actions++
		if strings.TrimSpace(k.Delete.Resource) == "" {
			return fmt.Errorf("k8s.delete.resource is required")
		}
	}
	if k.Scale != nil {
		actions++
		if strings.TrimSpace(k.Scale.Resource) == "" {
			return fmt.Errorf("k8s.scale.resource is required")
		}
		if k.Scale.Replicas < 0 {
			return fmt.Errorf("k8s.scale.replicas must be >= 0")
		}
	}
	if k.RolloutRestart != nil {
		actions++
		if strings.TrimSpace(k.RolloutRestart.Resource) == "" {
			return fmt.Errorf("k8s.rollout_restart.resource is required")
		}
	}
	if k.Wait != nil {
		actions++
		if strings.TrimSpace(k.Wait.Resource) == "" {
			return fmt.Errorf("k8s.wait.resource is required")
		}
		if strings.TrimSpace(k.Wait.For) == "" {
			return fmt.Errorf("k8s.wait.for is required")
		}
	}
	if k.Get != nil {
		actions++
		if strings.TrimSpace(k.Get.Resource) == "" {
			return fmt.Errorf("k8s.get.resource is required")
		}
	}
	if k.Exec != nil {
		actions++
		if strings.TrimSpace(k.Exec.Pod) == "" {
			return fmt.Errorf("k8s.exec.pod is required")
		}
		if len(k.Exec.Command) == 0 {
			return fmt.Errorf("k8s.exec.command is required")
		}
	}
	if k.CreateJob != nil {
		actions++
		if strings.TrimSpace(k.CreateJob.Name) == "" {
			return fmt.Errorf("k8s.create_job.name is required")
		}
		if strings.TrimSpace(k.CreateJob.Image) == "" {
			return fmt.Errorf("k8s.create_job.image is required")
		}
	}
	if actions == 0 {
		return fmt.Errorf("k8s step requires exactly one action (apply, delete, scale, rollout_restart, wait, get, exec, or create_job)")
	}
	if actions > 1 {
		return fmt.Errorf("k8s step allows only one action per step")
	}
	outputActions := 0
	if k.Get != nil {
		outputActions++
	}
	if k.Exec != nil {
		outputActions++
	}
	if k.CreateJob != nil {
		outputActions++
	}
	if strings.TrimSpace(k.Output) != "" && outputActions == 0 {
		return fmt.Errorf("k8s.output is only valid with get, exec, or create_job actions")
	}
	return nil
}

func validateFileTransfer(label string, op *RecipeFileTransfer) error {
	if strings.TrimSpace(op.Local) == "" {
		return fmt.Errorf("%s.local is empty", label)
	}
	if strings.TrimSpace(op.Remote) == "" {
		return fmt.Errorf("%s.remote is empty", label)
	}
	return nil
}

func validateOpensearchStep(o *RecipeStepOpensearch) error {
	action := strings.ToLower(strings.TrimSpace(o.Action))
	if action != "get" && action != "search" && action != "index" {
		return fmt.Errorf("opensearch.action must be one of: get, search, index")
	}
	if action == "get" && strings.TrimSpace(o.DocID) == "" {
		return fmt.Errorf("opensearch.doc_id is required for get action")
	}
	if strings.TrimSpace(o.Index) == "" {
		return fmt.Errorf("opensearch.index is required")
	}
	return nil
}

func validatePostgresStep(p *RecipeStepPostgres) error {
	action := strings.ToLower(strings.TrimSpace(p.Action))
	if action != "query" && action != "exec" && action != "migrate" {
		return fmt.Errorf("postgres.action must be one of: query, exec, migrate")
	}
	if strings.TrimSpace(p.DSNSecret) == "" {
		return fmt.Errorf("postgres.dsn_secret is required")
	}
	if (action == "query" || action == "exec") && strings.TrimSpace(p.SQL) == "" {
		return fmt.Errorf("postgres.sql is required for %s action", action)
	}
	return nil
}
