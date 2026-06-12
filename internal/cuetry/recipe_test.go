package cuetry

import (
	"context"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestValidateRemoteRecipe_ok(t *testing.T) {
	const src = `
recipe: {
	name: "demo"
	steps: [
		{host: "10.0.0.1", command: "uname -a"},
		{host: "10.0.0.2", command: "hostname"},
	]
}
`
	if err := ValidateRemoteRecipe([]byte(src)); err != nil {
		t.Fatal(err)
	}
}

func TestValidateRemoteRecipe_missingRecipe(t *testing.T) {
	const src = `
name: "bad"
`
	err := ValidateRemoteRecipe([]byte(src))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateRemoteRecipe_invalidField(t *testing.T) {
	const src = `
recipe: {
	name: "x"
	steps: [
		{host: "1.1.1.1", command: 42},
	]
}
`
	err := ValidateRemoteRecipe([]byte(src))
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRemoteRecipe_defaultsRunAs(t *testing.T) {
	const src = `
recipe: {
	name: "with-default"
	defaults: { run_as: "nginx" }
	steps: [
		{host: "10.0.0.1", command: "id"},
		{host: "10.0.0.2", command: "id", run_as: "root"},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Defaults == nil || r.Defaults.RunAs != "nginx" {
		t.Fatalf("defaults: %+v", r.Defaults)
	}
	if EffectiveRunAs(r.Steps[0].Step.Base(), r.Defaults) != "nginx" {
		t.Fatal("step0 run_as")
	}
	if EffectiveRunAs(r.Steps[1].Step.Base(), r.Defaults) != "root" {
		t.Fatal("step1 run_as")
	}
}

func TestParseRemoteRecipe_badRunAs(t *testing.T) {
	const src = `
recipe: {
	name: "bad"
	steps: [
		{host: "1.1.1.1", command: "id", run_as: "root;rm"},
	]
}
`
	_, err := ParseRemoteRecipe([]byte(src), nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRemoteRecipe_putAndGet(t *testing.T) {
	const src = `
recipe: {
	name: "files"
	steps: [
		{host: "10.0.0.1", put: {local: "./x", remote: "/tmp/x"}},
		{host: "10.0.0.2", get: {local: "./out", remote: "/etc/hostname"}},
	]
}
`
	if err := ValidateRemoteRecipe([]byte(src)); err != nil {
		t.Fatal(err)
	}
}

func TestParseRemoteRecipe_putWithRunAsRejected(t *testing.T) {
	const src = `
recipe: {
	name: "bad"
	steps: [
		{host: "10.0.0.1", put: {local: "./x", remote: "/tmp/x"}, run_as: "root"},
	]
}
`
	if err := ValidateRemoteRecipe([]byte(src)); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRemoteRecipe_commandAndPutRejected(t *testing.T) {
	const src = `
recipe: {
	name: "bad"
	steps: [
		{host: "10.0.0.1", command: "id", put: {local: "./x", remote: "/tmp/x"}},
	]
}
`
	if err := ValidateRemoteRecipe([]byte(src)); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRemoteRecipe_scriptStep(t *testing.T) {
	const src = `
recipe: {
	name: "scr"
	steps: [
		{host: "10.0.0.1", script: {local: "./a.sh", remote: "/tmp/a.sh"}},
	]
}
`
	if err := ValidateRemoteRecipe([]byte(src)); err != nil {
		t.Fatal(err)
	}
}

func TestParseRemoteRecipe_scriptWithRunAs(t *testing.T) {
	const src = `
recipe: {
	name: "scr2"
	steps: [
		{host: "10.0.0.1", script: {local: "./a.sh", remote: "/tmp/a.sh"}, run_as: "nobody"},
	]
}
`
	if err := ValidateRemoteRecipe([]byte(src)); err != nil {
		t.Fatal(err)
	}
}

func TestScriptRunAfterUpload(t *testing.T) {
	got, err := ScriptRunAfterUpload("/tmp/x.sh", "", nil)
	if err != nil || got != `sh '/tmp/x.sh'` {
		t.Fatalf("%q %v", got, err)
	}
	got2, err := ScriptRunAfterUpload("/tmp/x.sh", "root", nil)
	if err != nil || !strings.Contains(got2, "sudo") {
		t.Fatalf("%q %v", got2, err)
	}
	got3, err := ScriptRunAfterUpload("/tmp/x.sh", "", map[string]string{"FOO": "bar"})
	if err != nil || !strings.HasPrefix(got3, "export FOO='bar'; ") || !strings.Contains(got3, `sh '/tmp/x.sh'`) {
		t.Fatalf("%q %v", got3, err)
	}
}

func TestParseRemoteRecipe_defaultsRunAsWithPutOk(t *testing.T) {
	const src = `
recipe: {
	name: "mix"
	defaults: { run_as: "nginx" }
	steps: [
		{host: "10.0.0.1", put: {local: "./x", remote: "/tmp/x"}},
		{host: "10.0.0.1", command: "id"},
	]
}
`
	if err := ValidateRemoteRecipe([]byte(src)); err != nil {
		t.Fatal(err)
	}
}

func TestParseRemoteRecipe_badHostRegex(t *testing.T) {
	const src = `
recipe: {
	name: "badre"
	steps: [
		{host: "re:(unclosed", command: "id"},
	]
}
`
	_, err := ParseRemoteRecipe([]byte(src), nil)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestResolveHostFromRecords(t *testing.T) {
	recs := []hosts.Record{
		{Name: "web-a", PrimaryIP: "10.0.0.1"},
		{Name: "web-b", PrimaryIP: "10.0.0.2"},
	}
	got, err := ResolveHostFromRecords("web-a", recs)
	if err != nil || got.PrimaryIP != "10.0.0.1" {
		t.Fatalf("%+v %v", got, err)
	}
	_, err = ResolveHostFromRecords("web-", recs)
	if err == nil {
		t.Fatal("expected miss")
	}
	got2, err := ResolveHostFromRecords("10.1.1.1", recs)
	if err != nil || got2.PrimaryIP != "10.1.1.1" {
		t.Fatalf("%+v %v", got2, err)
	}
}

func TestExpandStepHosts_all(t *testing.T) {
	recs := []hosts.Record{
		{Name: "k1", PrimaryIP: "10.0.0.1"},
		{Name: "k2", PrimaryIP: ""},
		{Name: "k3", PrimaryIP: "10.0.0.3"},
	}
	got, err := ExpandStepHosts(MatchAllSearchHosts, recs)
	if err != nil || len(got) != 2 || got[0].Name != "k1" || got[1].Name != "k3" {
		t.Fatalf("%+v %v", got, err)
	}
	one, err := ExpandStepHosts("k1", recs)
	if err != nil || len(one) != 1 || one[0].PrimaryIP != "10.0.0.1" {
		t.Fatalf("%+v %v", one, err)
	}
	_, err = ExpandStepHosts(MatchAllSearchHosts, []hosts.Record{{Name: "x", PrimaryIP: ""}})
	if err == nil {
		t.Fatal("expected error for no IPs")
	}
}

func TestExpandStepHosts_regex(t *testing.T) {
	recs := []hosts.Record{
		{Name: "prod-kafka-1", PrimaryIP: "10.0.0.1"},
		{Name: "prod-kafka-2", PrimaryIP: "10.0.0.2"},
		{Name: "prod-web-1", PrimaryIP: "10.0.0.3"},
	}
	got, err := ExpandStepHosts(`re:^prod-kafka-\d+$`, recs)
	if err != nil || len(got) != 2 {
		t.Fatalf("%+v %v", got, err)
	}
	_, err = ExpandStepHosts(`re:^nomatch$`, recs)
	if err == nil {
		t.Fatal("expected no match error")
	}
}

func TestCountRecipeStreamResults(t *testing.T) {
	t.Parallel()
	recs := []hosts.Record{
		{Name: "a", PrimaryIP: "10.0.0.1"},
		{Name: "b", PrimaryIP: "10.0.0.2"},
	}
	const src = `
recipe: {
	name: "mix"
	steps: [
		{host: "*", command: "id"},
		{host: "a", agent_transfer: {
			dest_host: "b"
			source_path: "/x"
			dest_path: "/y"
			cloud: { provider: "s3", bucket: "bk" }
		}},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), recs)
	if err != nil {
		t.Fatal(err)
	}
	n, err := CountRecipeStreamResults(r, recs)
	if err != nil {
		t.Fatal(err)
	}
	// step1: * → 2 hosts; step2: agent_transfer → 1
	if n != 3 {
		t.Fatalf("got %d want 3", n)
	}
}

func TestParseRemoteRecipe_agentTransfer_okWithRecords(t *testing.T) {
	t.Parallel()
	recs := []hosts.Record{
		{Name: "web-1", PrimaryIP: "10.0.0.1"},
		{Name: "db-1", PrimaryIP: "10.0.0.2"},
	}
	const src = `
recipe: {
	name: "at"
	steps: [
		{
			host: "web-1"
			agent_transfer: {
				dest_host: "db-1"
				source_path: "/tmp/a"
				dest_path: "/tmp/b"
				cloud: { provider: "s3", bucket: "mybucket" }
			}
		},
	]
}
`
	if _, err := ParseRemoteRecipe([]byte(src), recs); err != nil {
		t.Fatal(err)
	}
}

func TestParseRemoteRecipe_agentTransfer_twoSourceHostsFails(t *testing.T) {
	t.Parallel()
	recs := []hosts.Record{
		{Name: "web-1", PrimaryIP: "10.0.0.1"},
		{Name: "web-2", PrimaryIP: "10.0.0.2"},
		{Name: "db-1", PrimaryIP: "10.0.0.3"},
	}
	const src = `
recipe: {
	name: "at"
	steps: [
		{
			host: "re:^web-"
			agent_transfer: {
				dest_host: "db-1"
				source_path: "/tmp/a"
				dest_path: "/tmp/b"
				cloud: { provider: "s3", bucket: "mybucket" }
			}
		},
	]
}
`
	if _, err := ParseRemoteRecipe([]byte(src), recs); err == nil {
		t.Fatal("expected error for multiple source matches")
	}
}

func TestParseRemoteRecipe_agentTransfer_envRejected(t *testing.T) {
	t.Parallel()
	recs := []hosts.Record{
		{Name: "a", PrimaryIP: "10.0.0.1"},
		{Name: "b", PrimaryIP: "10.0.0.2"},
	}
	const src = `
recipe: {
	name: "bad"
	steps: [
		{
			host: "a"
			env: { FOO: "bar" }
			agent_transfer: {
				dest_host: "b"
				source_path: "/x"
				dest_path: "/y"
				cloud: { provider: "s3", bucket: "bk" }
			}
		},
	]
}
`
	if _, err := ParseRemoteRecipe([]byte(src), recs); err == nil {
		t.Fatal("expected error for env on agent_transfer")
	}
}

func TestParseRemoteRecipe_agentTransfer_runAsRejected(t *testing.T) {
	t.Parallel()
	recs := []hosts.Record{
		{Name: "a", PrimaryIP: "10.0.0.1"},
		{Name: "b", PrimaryIP: "10.0.0.2"},
	}
	const src = `
recipe: {
	name: "bad"
	steps: [
		{
			host: "a"
			run_as: "root"
			agent_transfer: {
				dest_host: "b"
				source_path: "/x"
				dest_path: "/y"
				cloud: { provider: "s3", bucket: "bk" }
			}
		},
	]
}
`
	if _, err := ParseRemoteRecipe([]byte(src), recs); err == nil {
		t.Fatal("expected error for run_as on agent_transfer")
	}
}

func TestParseRemoteRecipe_stepNotifyBlock(t *testing.T) {
	t.Parallel()
	const src = `
recipe: {
	name: "n"
	steps: [
		{ host: "10.0.0.1", command: "uptime", notify: {} },
		{ host: "10.0.0.2", command: "hostname", notify: { notify_subject: "Ping" } },
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Steps[0].Step.Base().Notify == nil {
		t.Fatal("expected steps[0].notify present")
	}
	if r.Steps[0].Step.Base().Notify.NotifySubject != "" {
		t.Fatalf("steps[0] subject: %q", r.Steps[0].Step.Base().Notify.NotifySubject)
	}
	if r.Steps[1].Step.Base().Notify == nil || r.Steps[1].Step.Base().Notify.NotifySubject != "Ping" {
		t.Fatalf("steps[1].notify: %+v", r.Steps[1].Step.Base().Notify)
	}
}

func TestParseRemoteRecipe_stepNotifyServicesAndMessage(t *testing.T) {
	t.Parallel()
	const src = `
recipe: {
	name: "svc"
	steps: [
		{
			host: "10.0.0.1"
			command: "uptime"
			notify: {
				message: "fixed body"
				services: {
					slack: { channel_id: "C1" }
					telegram: {}
				}
			}
		},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	n := r.Steps[0].Step.Base().Notify
	if n == nil || n.Message != "fixed body" {
		t.Fatalf("notify: %+v", n)
	}
	if n.Services == nil || n.Services.HTTP != nil {
		t.Fatalf("services http: %+v", n.Services)
	}
	if n.Services.Slack == nil || n.Services.Slack.ChannelID != "C1" {
		t.Fatalf("slack: %+v", n.Services.Slack)
	}
	if n.Services.Telegram == nil {
		t.Fatal("expected telegram marker")
	}
}

func TestParseRemoteRecipe_stepHooks_ok(t *testing.T) {
	const src = `
recipe: {
	name: "hooks"
	steps: [
		{
			host: "10.0.0.1"
			command: "true"
			hooks: {
				on_success: {where: "local", command: "echo ok"}
				on_failure: {where: "remote", command: "echo fail", run_as: "nobody", env: {HOOK: "1"}}
			}
		},
		{
			host: "10.0.0.2"
			script: {local: "./a.sh", remote: "/tmp/a.sh"}
			hooks: {
				on_failure: {where: "local", command: "echo x"}
			}
		},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	b0 := r.Steps[0].Step.Base()
	if b0.Hooks == nil || b0.Hooks.OnSuccess == nil || b0.Hooks.OnSuccess.Where != "local" {
		t.Fatalf("step0 hooks: %+v", b0.Hooks)
	}
	if b0.Hooks.OnFailure == nil || b0.Hooks.OnFailure.RunAs != "nobody" {
		t.Fatalf("step0 on_failure: %+v", b0.Hooks.OnFailure)
	}
}

func TestParseRemoteRecipe_stepHooks_defaultWhereRemote(t *testing.T) {
	const src = `
recipe: {
	name: "hooks-default-remote"
	steps: [{
		host: "10.0.0.1"
		command: "true"
		hooks: {
			on_success: {command: "echo ok"}
		}
	}]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	b0 := r.Steps[0].Step.Base()
	if b0.Hooks == nil || b0.Hooks.OnSuccess == nil {
		t.Fatalf("hooks missing: %+v", b0.Hooks)
	}
	if b0.Hooks.OnSuccess.Where != "" {
		t.Fatalf("where = %q, want empty default", b0.Hooks.OnSuccess.Where)
	}
}

func TestParseRemoteRecipe_stepHooks_localRunAsRejected(t *testing.T) {
	const src = `
recipe: {
	name: "bad"
	steps: [
		{
			host: "10.0.0.1"
			command: "id"
			hooks: {on_success: {where: "local", command: "id", run_as: "root"}}
		},
	]
}
`
	if _, err := ParseRemoteRecipe([]byte(src), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRemoteRecipe_kvTunnelOnPutIgnored(t *testing.T) {
	const src = `
recipe: {
	name: "ok"
	steps: [
		{
			host: "10.0.0.1"
			put: {local: "./x", remote: "/tmp/x"}
			kv_tunnel: true
		},
	]
}
`
	if _, err := ParseRemoteRecipe([]byte(src), nil); err != nil {
		t.Fatalf("kv_tunnel on put is deprecated no-op: %v", err)
	}
}

func TestParseRemoteRecipe_stepHooks_onPutRejected(t *testing.T) {
	const src = `
recipe: {
	name: "bad"
	steps: [
		{
			host: "10.0.0.1"
			put: {local: "./x", remote: "/tmp/x"}
			hooks: {on_success: {where: "local", command: "true"}}
		},
	]
}
`
	if _, err := ParseRemoteRecipe([]byte(src), nil); err == nil {
		t.Fatal("expected error")
	}
}

func TestEffectiveEnvForRemoteHook_overridesStepEnv(t *testing.T) {
	step := &StepBase{
		Env: map[string]string{"A": "step", "B": "b"},
	}
	hook := &RecipeStepHook{Env: map[string]string{"A": "hook"}}
	cli := map[string]string{"C": "cli"}
	rec := hosts.Record{Name: "h1", PrimaryIP: "1.2.3.4", Provider: "p", Zone: "z1"}
	got, err := EffectiveEnvForRemoteHook(context.Background(), true, nil, step, nil, hook, cli, &rec)
	if err != nil {
		t.Fatal(err)
	}
	if got["A"] != "hook" || got["B"] != "b" || got["C"] != "cli" || got["HONEY_HOST_NAME"] != "h1" {
		t.Fatalf("merged: %#v", got)
	}
}

func TestParseRemoteRecipe_putWithSecretsRejected(t *testing.T) {
	const src = `
recipe: {
	name: "bad"
	steps: [
		{host: "10.0.0.1", put: {local: "./x", remote: "/tmp/x"}, secrets: {FOO: "env:BAR"}},
	]
}
`
	if err := ValidateRemoteRecipe([]byte(src)); err == nil {
		t.Fatal("expected error")
	}
}

func TestParseRemoteRecipe_envSecretsOverlapRejected(t *testing.T) {
	const src = `
recipe: {
	name: "bad"
	steps: [
		{host: "10.0.0.1", command: "id", env: {K: "1"}, secrets: {K: "secure:v1:AAAAAAAAAAAAAAAA:YmFj"}},
	]
}
`
	if err := ValidateRemoteRecipe([]byte(src)); err == nil {
		t.Fatal("expected error")
	}
}

func TestWrapRemoteShell(t *testing.T) {
	out, err := WrapRemoteShell("", "hostname")
	if err != nil || out != "hostname" {
		t.Fatalf("%q %v", out, err)
	}
	out, err = WrapRemoteShell("nginx", "id")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" || !strings.Contains(out, "sudo") {
		t.Fatalf("%q", out)
	}
}

func TestParseRemoteRecipe_dynamicFeatures(t *testing.T) {
	const src = `
recipe: {
	name: "dynamic-demo"
	type: "graph"
	defaults: {
		gather_facts: true
	}
	steps: [
		{
			id: "step1"
			host: "10.0.0.1"
			command: "echo hello"
			ignore_errors: true
			check_cmd: "test -f /etc/ready"
			loop_from: {
				step: "other"
				extract: ".items"
			}
			notify_handler: ["restart-service"]
		}
	]
	handlers: [
		{
			id: "restart-service"
			host: "10.0.0.1"
			command: "systemctl restart app"
		}
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	if r.Defaults == nil || r.Defaults.GatherFacts == nil || !*r.Defaults.GatherFacts {
		t.Fatal("expected gather_facts to be true")
	}
	if len(r.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(r.Steps))
	}
	step := r.Steps[0].Step.Base()
	if !step.IgnoreErrors {
		t.Error("expected ignore_errors to be true")
	}
	if step.CheckCmd != "test -f /etc/ready" {
		t.Errorf("expected check_cmd, got %q", step.CheckCmd)
	}
	if step.LoopFrom == nil || step.LoopFrom.Step != "other" || step.LoopFrom.Extract != ".items" {
		t.Errorf("expected loop_from, got %+v", step.LoopFrom)
	}
	if len(step.NotifyHandler) != 1 || step.NotifyHandler[0] != "restart-service" {
		t.Errorf("expected notify_handler, got %+v", step.NotifyHandler)
	}
	if len(r.Handlers) != 1 || r.Handlers[0].Step.Base().ID != "restart-service" {
		t.Fatalf("expected 1 handler, got %+v", r.Handlers)
	}
}

func TestParseRemoteRecipe_loopTemplate(t *testing.T) {
	const src = `
recipe: {
	name: "loop-template"
	type: "graph"
	steps: [
		{
			id: "fetch"
			host: "*"
			command: "printf 'a\\nb\\n'"
		},
		{
			id: "use"
			host: "${item}"
			depends: ["fetch"]
			loop: "{{ stepStdoutLines \"fetch\" | compact | toJson }}"
			command: "echo ${item}"
		},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Steps[1].Step.Base().Loop; got != `{{ stepStdoutLines "fetch" | compact | toJson }}` {
		t.Fatalf("loop = %q", got)
	}
}

func TestValidateRemoteRecipe_loopAndLoopFromConflict(t *testing.T) {
	const src = `
recipe: {
	name: "loop-conflict"
	type: "graph"
	steps: [{
		id: "use"
		host: "${item}"
		command: "echo ${item}"
		loop: "{{ stepStdoutLines \"fetch\" | compact | toJson }}"
		loop_from: {
			step: "fetch"
			extract: ".[]"
		}
	}]
}
`
	err := ValidateRemoteRecipe([]byte(src))
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "only one of loop or loop_from may be set") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseRemoteRecipe_powerFields(t *testing.T) {
	const src = `
recipe: {
	name: "power-fields"
	type: "graph"
	steps: [
		{
			id: "list_nodes"
			host: "*"
			command: "printf 'a\\nb\\n'"
			output: "controllers_raw"
		},
		{
			id: "controllers"
			depends: ["list_nodes"]
			render: "{{ outputStdoutLines \"controllers_raw\" | compact | toJson }}"
			output: "controllers"
		},
		{
			id: "restart"
			depends: ["controllers"]
			host: "${item}"
			serial: 1
			loop: "{{ outputStdout \"controllers\" }}"
			command: "systemctl restart kafka.service"
			changed_when: "true"
			failed_when: "exit_code != 0"
		},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Steps[0].Step.Base().Output; got != "controllers_raw" {
		t.Fatalf("output = %q", got)
	}
	render := r.Steps[1].Step.(*TemplateStep)
	if render.Render == "" || render.Output != "controllers" || render.Host != MatchLocalAIHost {
		t.Fatalf("render step = %+v", render)
	}
	restart := r.Steps[2].Step.(*CommandStep)
	if restart.Serial != 1 || restart.ChangedWhen != "true" || restart.FailedWhen != "exit_code != 0" {
		t.Fatalf("restart step = %+v", restart)
	}
}

func TestParseRemoteRecipe_missingHostRejectedForCommand(t *testing.T) {
	const src = `
recipe: {
	name: "missing-host"
	steps: [{command: "true"}]
}
`
	_, err := ParseRemoteRecipe([]byte(src), nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "host") {
		t.Fatalf("unexpected error: %v", err)
	}
}
