package engine

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/cuetry"
)

// scriptedAgent returns pre-scripted replies in order and records the turns it
// saw (so a test can assert tool results were fed back). Exhausted → empty reply.
type scriptedAgent struct {
	replies []chatReply
	calls   int
	seen    []chatTurn
}

func (a *scriptedAgent) Chat(_ context.Context, turn chatTurn) (chatReply, error) {
	a.seen = append(a.seen, turn)
	if a.calls >= len(a.replies) {
		return chatReply{}, nil
	}
	r := a.replies[a.calls]
	a.calls++
	return r, nil
}

// constAgent always returns the same reply (for the budget test).
type constAgent struct {
	reply chatReply
	calls int
}

func (a *constAgent) Chat(_ context.Context, _ chatTurn) (chatReply, error) {
	a.calls++
	return a.reply, nil
}

// fakeApprover returns a scripted decision and records the requests it saw.
type fakeApprover struct {
	approved bool
	note     string
	seen     []approvalRequest
}

func (a *fakeApprover) approve(_ context.Context, req approvalRequest) approvalDecision {
	a.seen = append(a.seen, req)
	return approvalDecision{Approved: a.approved, Note: a.note}
}

func autoApprove() approver { return &fakeApprover{approved: true} }

func controllerTestRecipe(t *testing.T, src string) cuetry.Recipe {
	t.Helper()
	r, err := cuetry.ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatalf("parse recipe: %v", err)
	}
	return r
}

const twoStepOneTask = `
recipe: {
	name: "c"
	type: "controller"
	tasks: [{name: "t", description: "both steps run"}]
	steps: [
		{id: "a", description: "step a", host: "_", command: "date"},
		{id: "b", description: "step b", host: "_", command: "whoami"},
	]
}`

func finishCall(id, taskStatusPairs string) chatToolCall {
	return chatToolCall{ID: id, Name: controllerFinishTool, Args: taskStatusPairs}
}

func drain(ch <-chan HostExecResult) []HostExecResult {
	var out []HostExecResult
	for {
		select {
		case r := <-ch:
			out = append(out, r)
		default:
			return out
		}
	}
}

func TestController_HappyPath_RunsStepsThenSettles(t *testing.T) {
	recipe := controllerTestRecipe(t, twoStepOneTask)
	agent := &scriptedAgent{replies: []chatReply{
		{ToolCalls: []chatToolCall{{ID: "1", Name: "run_a"}}},
		{ToolCalls: []chatToolCall{{ID: "2", Name: "run_b"}}},
		{ToolCalls: []chatToolCall{finishCall("3", `{"settlements":[{"task":"t","status":"completed"}]}`)}},
	}}

	var ran []int
	runStep := func(_ context.Context, idx int) ([]HostExecResult, error) {
		ran = append(ran, idx)
		return []HostExecResult{{Name: "_", Success: true, Output: "ok"}}, nil
	}
	out := make(chan HostExecResult, 16)

	if err := runController(context.Background(), recipe, out, agent, runStep, autoApprove()); err != nil {
		t.Fatalf("runController: %v", err)
	}
	if len(ran) != 2 || ran[0] != 0 || ran[1] != 1 {
		t.Fatalf("steps ran = %v, want [0 1]", ran)
	}
	// The 2nd turn the agent saw must include the tool result for call "1" (run_a fed back).
	if len(agent.seen) < 2 {
		t.Fatalf("agent saw %d turns, want >=2", len(agent.seen))
	}
	if !turnHasToolResult(agent.seen[1], "1", "ok") {
		t.Errorf("run_a result was not fed back into turn 2")
	}
	res := drain(out)
	if len(res) == 0 || !res[len(res)-1].Success {
		t.Errorf("expected a successful controller summary result, got %+v", res)
	}
}

func turnHasToolResult(turn chatTurn, toolCallID, mustContain string) bool {
	for _, m := range turn.Messages {
		if m.Role == chatRoleTool && m.ToolCallID == toolCallID && strings.Contains(m.Content, mustContain) {
			return true
		}
	}
	return false
}

func TestController_FailedTask_ReturnsError(t *testing.T) {
	recipe := controllerTestRecipe(t, twoStepOneTask)
	agent := &scriptedAgent{replies: []chatReply{
		{ToolCalls: []chatToolCall{finishCall("1", `{"settlements":[{"task":"t","status":"failed","note":"broken"}]}`)}},
	}}
	out := make(chan HostExecResult, 8)
	err := runController(context.Background(), recipe, out, agent,
		func(context.Context, int) ([]HostExecResult, error) { return nil, nil }, autoApprove())
	if err == nil || !strings.Contains(err.Error(), "failed task") {
		t.Fatalf("err = %v, want a failed-task error", err)
	}
}

func TestController_BudgetCap_StopsLoop(t *testing.T) {
	recipe := controllerTestRecipe(t, `
recipe: {
	name: "c"
	type: "controller"
	controller: {max_turns: 3}
	tasks: [{name: "t", description: "d"}]
	steps: [{id: "a", description: "step a", host: "_", command: "date"}]
}`)
	agent := &constAgent{reply: chatReply{ToolCalls: []chatToolCall{{ID: "x", Name: "run_a"}}}} // never finishes
	var ran int
	out := make(chan HostExecResult, 32)
	err := runController(context.Background(), recipe, out, agent,
		func(context.Context, int) ([]HostExecResult, error) { ran++; return nil, nil }, autoApprove())
	if err == nil || !strings.Contains(err.Error(), "max_turns") {
		t.Fatalf("err = %v, want a max_turns error", err)
	}
	if agent.calls != 3 || ran != 3 {
		t.Errorf("calls=%d ran=%d, want 3/3 (bounded by max_turns)", agent.calls, ran)
	}
}

func TestController_RequestApproval_FeedsDecisionBack(t *testing.T) {
	recipe := controllerTestRecipe(t, twoStepOneTask)
	app := &fakeApprover{approved: false, note: "operator denied"}
	agent := &scriptedAgent{replies: []chatReply{
		{ToolCalls: []chatToolCall{{ID: "1", Name: controllerApprovalTool, Args: `{"action":"delete data dir","reason":"cleanup"}`}}},
		{ToolCalls: []chatToolCall{finishCall("2", `{"settlements":[{"task":"t","status":"skipped","note":"denied"}]}`)}},
	}}
	out := make(chan HostExecResult, 8)
	if err := runController(context.Background(), recipe, out, agent,
		func(context.Context, int) ([]HostExecResult, error) { return nil, nil }, app); err != nil {
		t.Fatalf("runController: %v", err)
	}
	if len(app.seen) != 1 || app.seen[0].Action != "delete data dir" || app.seen[0].Reason != "cleanup" {
		t.Fatalf("approver saw %+v, want one request for 'delete data dir'/'cleanup'", app.seen)
	}
	// The operator's denial must be reported back to the model on the next turn.
	if len(agent.seen) < 2 || !turnHasToolResult(agent.seen[1], "1", "operator denied") {
		t.Errorf("approval decision was not fed back to the model")
	}
}

func TestController_PrematureFinish_KeepsGoing(t *testing.T) {
	recipe := controllerTestRecipe(t, `
recipe: {
	name: "c"
	type: "controller"
	tasks: [
		{name: "t1", description: "d1"},
		{name: "t2", description: "d2"},
	]
	steps: [{id: "a", description: "step a", host: "_", command: "date"}]
}`)
	agent := &scriptedAgent{replies: []chatReply{
		{ToolCalls: []chatToolCall{finishCall("1", `{"settlements":[{"task":"t1","status":"completed"}]}`)}}, // t2 unsettled → continue
		{ToolCalls: []chatToolCall{finishCall("2", `{"settlements":[{"task":"t2","status":"completed"}]}`)}}, // now complete
	}}
	out := make(chan HostExecResult, 8)
	if err := runController(context.Background(), recipe, out, agent,
		func(context.Context, int) ([]HostExecResult, error) { return nil, nil }, autoApprove()); err != nil {
		t.Fatalf("runController: %v", err)
	}
	if agent.calls != 2 {
		t.Errorf("agent called %d times, want 2 (premature finish kept the loop going)", agent.calls)
	}
	// The reply to the first finish must tell the model t2 is still unsettled.
	if len(agent.seen) < 2 || !turnHasToolResult(agent.seen[1], "1", "t2") {
		t.Errorf("premature-finish reply did not report the unsettled task")
	}
}
