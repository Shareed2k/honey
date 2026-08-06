package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/shareed2k/honey/internal/cuetry"
	"go.uber.org/zap"
)

const (
	controllerDefaultModel    = "gpt-4o"
	controllerDefaultMaxTurns = 25
	controllerFinishTool      = "finish"
	controllerApprovalTool    = "request_approval"
	controllerStepToolPrefix  = "run_"
	controllerLLMOutputCap    = 4000 // bytes of a step's output shown to the model
)

// StreamCueRecipeStepsController runs a type: "controller" recipe: each step is
// exposed to an LLM as a tool, and the LLM decides which to run, in what order,
// until every task (goal) is settled. It is a deep module — one entry point over
// the whole LLM loop — reusing StreamCueRecipeStep (which carries host expansion,
// OPA/when/risk gates, env resolution and output capture) to run each chosen step.
func StreamCueRecipeStepsController(ctx context.Context, run *CueRun, out chan<- HostExecResult) error {
	run.OutputStore = cuetry.NewStepOutputStore()
	run.OutputCapture = cuetry.NewRecipeOutputCapture()
	agent, err := newOpenAIAgent(controllerModel(run.Params.Recipe))
	if err != nil {
		return err
	}
	recipe := run.Params.Recipe
	// Sub-recipe steps expose their callee's declared prompts as tool parameters.
	promptsByStep := loadControllerStepPrompts(recipe, run.Params.RecipeDir)
	// Reuse the full single-step path (host expansion, OPA/when/risk gates, env,
	// output capture) for every LLM-chosen step. For a sub-recipe step, the LLM's
	// arguments are merged into a cloned step's prompts before running.
	runStep := func(sctx context.Context, idx int, args map[string]string) ([]HostExecResult, error) {
		step := recipe.Steps[idx].Step
		if rs, ok := step.(*cuetry.RecipeStep); ok && len(args) > 0 {
			step = cloneRecipeStepWithPrompts(rs, args)
		}
		return StreamCueRecipeStep(sctx, run, idx, step, nil, out)
	}
	return runController(ctx, recipe, out, agent, runStep, newStdinApprover(), promptsByStep)
}

// runController is the agent-driven loop, split from StreamCueRecipeStepsController
// so tests can inject a scripted fake chatAgent, a fake step-runner, and a fake
// approver (no network, no host setup, no operator prompt). runStep executes step
// index idx with the LLM's arguments and returns its per-host results; app decides
// human-approval requests; promptsByStep supplies sub-recipe tool parameters.
func runController(ctx context.Context, recipe cuetry.Recipe, out chan<- HostExecResult, agent chatAgent, runStep func(context.Context, int, map[string]string) ([]HostExecResult, error), app approver, promptsByStep map[int]map[string]cuetry.RecipePrompt) error {
	tools, stepByTool := buildControllerTools(recipe, promptsByStep)
	maxTurns := controllerMaxTurns(recipe)

	messages := []chatMessage{
		{Role: chatRoleSystem, Content: buildControllerSystemPrompt(recipe)},
		{Role: chatRoleUser, Content: "Begin. Run steps to satisfy every task, observing each result, then call finish."},
	}

	// task name -> settled status ("completed"/"skipped"/"failed"); absent = unsettled.
	settled := make(map[string]string, len(recipe.Tasks))

	for turn := 0; turn < maxTurns; turn++ {
		reply, err := agent.Chat(ctx, chatTurn{Messages: messages, Tools: tools})
		if err != nil {
			return err
		}
		messages = append(messages, chatMessage{
			Role:      chatRoleAssistant,
			Content:   reply.AssistantText,
			ToolCalls: reply.ToolCalls,
		})

		if len(reply.ToolCalls) == 0 {
			messages = append(messages, chatMessage{
				Role:    chatRoleUser,
				Content: "Call a step tool to make progress, or finish once every task is settled.",
			})
			continue
		}

		// Every tool_call in an assistant turn MUST get a tool reply before the
		// next turn (OpenAI requirement), so answer each one.
		done := false
		for _, tc := range reply.ToolCalls {
			if tc.Name == controllerFinishTool {
				content, complete := applyFinish(tc.Args, recipe, settled)
				messages = append(messages, chatMessage{Role: chatRoleTool, ToolCallID: tc.ID, Content: content})
				if complete {
					done = true
				}
				continue
			}
			if tc.Name == controllerApprovalTool {
				messages = append(messages, chatMessage{
					Role: chatRoleTool, ToolCallID: tc.ID,
					Content: applyApproval(ctx, tc.Args, app),
				})
				continue
			}
			idx, ok := stepByTool[tc.Name]
			if !ok {
				messages = append(messages, chatMessage{
					Role: chatRoleTool, ToolCallID: tc.ID,
					Content: fmt.Sprintf(`{"error":"unknown tool %q"}`, tc.Name),
				})
				continue
			}
			args, argErr := parseStepArgs(tc.Args)
			if argErr != nil {
				messages = append(messages, chatMessage{
					Role: chatRoleTool, ToolCallID: tc.ID,
					Content: fmt.Sprintf(`{"error":%q}`, argErr.Error()),
				})
				continue
			}
			zap.L().Debug("controller running step",
				zap.String("id", recipe.Steps[idx].Step.Base().ID), zap.Int("turn", turn))
			rows, runErr := runStep(ctx, idx, args)
			messages = append(messages, chatMessage{
				Role: chatRoleTool, ToolCallID: tc.ID,
				Content: summarizeStepForLLM(rows, runErr),
			})
		}
		if done {
			return finalizeController(out, recipe, settled)
		}
	}

	return fmt.Errorf("controller: reached max_turns (%d) before all tasks were settled", maxTurns)
}

func controllerModel(r cuetry.Recipe) string {
	if r.Controller != nil && strings.TrimSpace(r.Controller.Model) != "" {
		return strings.TrimSpace(r.Controller.Model)
	}
	if m := strings.TrimSpace(os.Getenv("OPENAI_MODEL")); m != "" {
		return m
	}
	return controllerDefaultModel
}

func controllerMaxTurns(r cuetry.Recipe) int {
	if r.Controller != nil && r.Controller.MaxTurns > 0 {
		return r.Controller.MaxTurns
	}
	return controllerDefaultMaxTurns
}

// buildControllerTools maps each step to a no-argument tool (name "run_<id>") plus
// the built-in finish tool, and returns the toolName->stepIndex lookup.
func buildControllerTools(r cuetry.Recipe, promptsByStep map[int]map[string]cuetry.RecipePrompt) ([]chatTool, map[string]int) {
	tools := make([]chatTool, 0, len(r.Steps)+2)
	byTool := make(map[string]int, len(r.Steps))
	for i, ws := range r.Steps {
		step := ws.Step
		id := step.Base().ID
		name := controllerStepToolPrefix + id
		desc := strings.TrimSpace(step.Base().Description)
		if desc == "" {
			desc = fmt.Sprintf("run the %q step (kind %s)", id, step.Kind())
		}
		tool := chatTool{Name: name, Description: desc}
		if prompts := promptsByStep[i]; len(prompts) > 0 { // sub-recipe step: LLM-fillable params
			tool.Parameters = promptsToToolSchema(prompts)
		}
		tools = append(tools, tool)
		byTool[name] = i
	}
	tools = append(tools, chatTool{
		Name:        controllerApprovalTool,
		Description: "Ask the human operator to approve an action before you take it (e.g. a destructive or irreversible step). Returns whether it was approved.",
		Parameters:  json.RawMessage(controllerApprovalSchema),
	})
	tools = append(tools, chatTool{
		Name:        controllerFinishTool,
		Description: "Settle each task once its goal is met (or cannot be). Call when done.",
		Parameters:  json.RawMessage(controllerFinishSchema),
	})
	return tools, byTool
}

const controllerApprovalSchema = `{
  "type": "object",
  "properties": {
    "action": {"type": "string", "description": "what you want to do, in one line"},
    "reason": {"type": "string", "description": "why it is needed"}
  },
  "required": ["action"]
}`

// applyApproval prompts the operator (via app) for a request_approval tool call
// and returns the decision as the tool reply.
func applyApproval(ctx context.Context, args string, app approver) string {
	var req struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &req); err != nil {
		return fmt.Sprintf(`{"approved":false,"note":"invalid approval arguments: %s"}`, err.Error())
	}
	dec := app.approve(ctx, approvalRequest{Action: req.Action, Reason: req.Reason})
	b, _ := json.Marshal(struct {
		Approved bool   `json:"approved"`
		Note     string `json:"note,omitempty"`
	}{Approved: dec.Approved, Note: dec.Note})
	return string(b)
}

const controllerFinishSchema = `{
  "type": "object",
  "properties": {
    "settlements": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "task": {"type": "string"},
          "status": {"type": "string", "enum": ["completed", "skipped", "failed"]},
          "note": {"type": "string"}
        },
        "required": ["task", "status"]
      }
    }
  },
  "required": ["settlements"]
}`

func buildControllerSystemPrompt(r cuetry.Recipe) string {
	var b strings.Builder
	b.WriteString("You are a controller for an infrastructure automation run. ")
	b.WriteString("You are given TASKS (goals that must be true when finished) and a set of STEP tools you may call. ")
	b.WriteString("Decide which steps to run, in what order, to satisfy every task. Observe each step's result; a failed step is an observation you can react to, not a fatal error. ")
	b.WriteString("When a task's goal is met (or determined impossible), settle it via the finish tool (completed/skipped/failed). Call finish once every task is settled. ")
	b.WriteString("Before any destructive or irreversible action, call request_approval and proceed only if the operator approves.\n\n")
	b.WriteString("TASKS:\n")
	for _, t := range r.Tasks {
		fmt.Fprintf(&b, "- %s: %s\n", t.Name, t.Description)
	}
	b.WriteString("\nSTEPS (call the matching tool to run one):\n")
	for _, ws := range r.Steps {
		step := ws.Step
		desc := strings.TrimSpace(step.Base().Description)
		if desc == "" {
			desc = "(no description)"
		}
		fmt.Fprintf(&b, "- %s%s (%s): %s\n", controllerStepToolPrefix, step.Base().ID, step.Kind(), desc)
	}
	if r.Controller != nil && strings.TrimSpace(r.Controller.SystemPrompt) != "" {
		b.WriteString("\n")
		b.WriteString(strings.TrimSpace(r.Controller.SystemPrompt))
	}
	return b.String()
}

// summarizeStepForLLM renders a step's result as compact JSON for the model:
// success, exit code, and a truncated combined output.
func summarizeStepForLLM(rows []HostExecResult, runErr error) string {
	type hostOut struct {
		Host    string `json:"host"`
		Success bool   `json:"success"`
		Exit    int    `json:"exit_code"`
		Output  string `json:"output"`
	}
	out := struct {
		Error string    `json:"error,omitempty"`
		Hosts []hostOut `json:"hosts"`
	}{}
	if runErr != nil {
		out.Error = runErr.Error()
	}
	for _, r := range rows {
		o := strings.TrimSpace(r.Output)
		if len(o) > controllerLLMOutputCap {
			o = o[:controllerLLMOutputCap] + "…(truncated)"
		}
		out.Hosts = append(out.Hosts, hostOut{Host: r.Name, Success: r.Success, Exit: r.ExitCode, Output: o})
	}
	b, err := json.Marshal(out)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(b)
}

type controllerSettlement struct {
	Task   string `json:"task"`
	Status string `json:"status"`
	Note   string `json:"note,omitempty"`
}

// applyFinish records the LLM's task settlements and reports back whether every
// task is now settled. content is the tool reply; complete is true only when all
// tasks have a status (so a premature finish keeps the loop going).
func applyFinish(args string, r cuetry.Recipe, settled map[string]string) (content string, complete bool) {
	var payload struct {
		Settlements []controllerSettlement `json:"settlements"`
	}
	if err := json.Unmarshal([]byte(args), &payload); err != nil {
		return fmt.Sprintf(`{"error":"invalid finish arguments: %s"}`, err.Error()), false
	}
	valid := make(map[string]struct{}, len(r.Tasks))
	for _, t := range r.Tasks {
		valid[t.Name] = struct{}{}
	}
	for _, s := range payload.Settlements {
		if _, ok := valid[s.Task]; !ok {
			continue // ignore settlements for unknown task names
		}
		switch s.Status {
		case "completed", "skipped", "failed":
			settled[s.Task] = s.Status
		}
	}
	var unsettled []string
	for _, t := range r.Tasks {
		if _, ok := settled[t.Name]; !ok {
			unsettled = append(unsettled, t.Name)
		}
	}
	if len(unsettled) > 0 {
		return fmt.Sprintf(`{"accepted":true,"unsettled":%q,"note":"keep going until every task is settled"}`, unsettled), false
	}
	return `{"accepted":true,"unsettled":[]}`, true
}

// finalizeController emits a summary result and reports run success/failure: the
// run fails if any task settled "failed".
func finalizeController(out chan<- HostExecResult, r cuetry.Recipe, settled map[string]string) error {
	var b strings.Builder
	var failed []string
	b.WriteString("controller finished — task outcomes:\n")
	for _, t := range r.Tasks {
		status := settled[t.Name]
		if status == "" {
			status = "unsettled"
		}
		if status == "failed" {
			failed = append(failed, t.Name)
		}
		fmt.Fprintf(&b, "  %s: %s\n", t.Name, status)
	}
	res := HostExecResult{
		Name:     "controller",
		IP:       "-",
		Provider: "local",
		Success:  len(failed) == 0,
		Output:   strings.TrimSpace(b.String()),
	}
	if out != nil {
		out <- res
	}
	if len(failed) > 0 {
		return fmt.Errorf("controller finished with failed task(s): %s", strings.Join(failed, ", "))
	}
	return nil
}
