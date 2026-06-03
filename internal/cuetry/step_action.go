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
	default:
		return "unknown"
	}
}

// ClassifyStep returns the step kind after validating exactly one of command / put / get / script / agent_transfer / ai / template / plugin / tunnel / k8s.
func ClassifyStep(s RecipeStep) (StepKind, error) {
	cmd := strings.TrimSpace(s.Command)
	hasPut := s.Put != nil
	hasGet := s.Get != nil
	hasScript := s.Script != nil
	hasAgent := s.AgentTransfer != nil
	hasAI := s.AI != nil
	hasTemplate := s.Template != nil
	hasPlugin := s.Plugin != nil
	hasTunnel := s.Tunnel != nil
	hasK8s := s.K8s != nil
	n := 0
	if cmd != "" {
		n++
	}
	if hasPut {
		n++
	}
	if hasGet {
		n++
	}
	if hasScript {
		n++
	}
	if hasAgent {
		n++
	}
	if hasAI {
		n++
	}
	if hasTemplate {
		n++
	}
	if hasPlugin {
		n++
	}
	if hasTunnel {
		n++
	}
	if hasK8s {
		n++
	}
	if n == 0 {
		return 0, fmt.Errorf("need exactly one of command, put, get, script, agent_transfer, ai, template, plugin, tunnel, or k8s")
	}
	if n > 1 {
		return 0, fmt.Errorf("only one of command, put, get, script, agent_transfer, ai, template, plugin, tunnel, k8s allowed")
	}
	if hasPut {
		if err := validateFileTransfer("put", s.Put); err != nil {
			return 0, err
		}
		return StepKindPut, nil
	}
	if hasGet {
		if err := validateFileTransfer("get", s.Get); err != nil {
			return 0, err
		}
		return StepKindGet, nil
	}
	if hasScript {
		if err := validateFileTransfer("script", s.Script); err != nil {
			return 0, err
		}
		return StepKindScript, nil
	}
	if hasAgent {
		return StepKindAgentTransfer, nil
	}
	if hasAI {
		return StepKindAI, nil
	}
	if hasTemplate {
		return StepKindTemplate, nil
	}
	if hasPlugin {
		if strings.TrimSpace(s.Plugin.ID) == "" {
			return 0, fmt.Errorf("plugin.id is required")
		}
		if strings.TrimSpace(s.Plugin.Action) == "" {
			return 0, fmt.Errorf("plugin.action is required")
		}
		return StepKindPlugin, nil
	}
	if hasTunnel {
		return StepKindTunnel, nil
	}
	if hasK8s {
		if err := validateK8sStep(s.K8s); err != nil {
			return 0, err
		}
		return StepKindK8s, nil
	}
	return StepKindCommand, nil
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
