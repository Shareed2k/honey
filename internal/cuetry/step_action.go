package cuetry

import (
	"fmt"
	"strings"
)

// StepKind describes which action a recipe step performs.
type StepKind int

// StepKind values correspond to exactly one populated field on RecipeStep.
const (
	StepKindCommand StepKind = iota
	StepKindPut
	StepKindGet
	StepKindScript
	StepKindAgentTransfer
	StepKindAI
	StepKindTemplate
	StepKindPlugin
	StepKindTunnel
	StepKindK8s
	StepKindDocker
)

// StepKindLabel returns a short stable name for defaults and logging.
func StepKindLabel(k StepKind) string {
	switch k {
	case StepKindCommand:
		return "command"
	case StepKindPut:
		return "put"
	case StepKindGet:
		return "get"
	case StepKindScript:
		return "script"
	case StepKindAgentTransfer:
		return "agent_transfer"
	case StepKindAI:
		return "ai"
	case StepKindTemplate:
		return "template"
	case StepKindPlugin:
		return "plugin"
	case StepKindTunnel:
		return "tunnel"
	case StepKindK8s:
		return "k8s"
	case StepKindDocker:
		return "docker"
	default:
		return "unknown"
	}
}

// ClassifyStep returns the step kind after validating exactly one action field.
func ClassifyStep(s RecipeStep) (StepKind, error) {
	var kind StepKind
	var found bool

	check := func(k StepKind, ok bool, validate func() error) error {
		if !ok {
			return nil
		}
		if found {
			return fmt.Errorf("only one of command, put, get, script, agent_transfer, ai, template, plugin, tunnel, k8s, or docker allowed")
		}
		if validate != nil {
			if err := validate(); err != nil {
				return err
			}
		}
		kind = k
		found = true
		return nil
	}

	if strings.TrimSpace(s.Command) != "" {
		kind = StepKindCommand
		found = true
	}
	if err := check(StepKindTemplate, strings.TrimSpace(s.Render) != "", nil); err != nil {
		return 0, err
	}
	if err := check(StepKindPut, s.Put != nil, func() error { return validateFileTransfer("put", s.Put) }); err != nil {
		return 0, err
	}
	if err := check(StepKindGet, s.Get != nil, func() error { return validateFileTransfer("get", s.Get) }); err != nil {
		return 0, err
	}
	if err := check(StepKindScript, s.Script != nil, func() error { return validateFileTransfer("script", s.Script) }); err != nil {
		return 0, err
	}
	if err := check(StepKindAgentTransfer, s.AgentTransfer != nil, nil); err != nil {
		return 0, err
	}
	if err := check(StepKindAI, s.AI != nil, nil); err != nil {
		return 0, err
	}
	if err := check(StepKindTemplate, s.Template != nil, nil); err != nil {
		return 0, err
	}
	if err := check(StepKindPlugin, s.Plugin != nil, func() error {
		if strings.TrimSpace(s.Plugin.ID) == "" {
			return fmt.Errorf("plugin.id is required")
		}
		if strings.TrimSpace(s.Plugin.Action) == "" {
			return fmt.Errorf("plugin.action is required")
		}
		return nil
	}); err != nil {
		return 0, err
	}
	if err := check(StepKindTunnel, s.Tunnel != nil, nil); err != nil {
		return 0, err
	}
	if err := check(StepKindK8s, s.K8s != nil, func() error { return validateK8sStep(s.K8s) }); err != nil {
		return 0, err
	}
	if err := check(StepKindDocker, s.Docker != nil, func() error { return validateDockerStep(s.Docker) }); err != nil {
		return 0, err
	}

	if !found {
		return 0, fmt.Errorf("need exactly one of command, put, get, script, agent_transfer, ai, template, plugin, tunnel, k8s, or docker")
	}
	return kind, nil
}

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

// ValidateStepRunAsForKind rejects per-step run_as on put/get (SFTP only).
// Script steps allow run_as for the execute phase; defaults.run_as applies there too.
func ValidateStepRunAsForKind(kind StepKind, step RecipeStep) error {
	if (kind == StepKindPut || kind == StepKindGet || kind == StepKindAgentTransfer || kind == StepKindAI || kind == StepKindTemplate || kind == StepKindTunnel || kind == StepKindK8s) && strings.TrimSpace(step.RunAs) != "" {
		return fmt.Errorf("run_as on put/get/agent_transfer/ai/template/tunnel/k8s steps is not supported")
	}
	return nil
}
