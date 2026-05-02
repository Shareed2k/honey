package cuetry

import (
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
	r, err := ParseRemoteRecipe([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	if r.Defaults == nil || r.Defaults.RunAs != "nginx" {
		t.Fatalf("defaults: %+v", r.Defaults)
	}
	if EffectiveRunAs(r.Steps[0], r.Defaults) != "nginx" {
		t.Fatal("step0 run_as")
	}
	if EffectiveRunAs(r.Steps[1], r.Defaults) != "root" {
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
	_, err := ParseRemoteRecipe([]byte(src))
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
	_, err := ParseRemoteRecipe([]byte(src))
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
