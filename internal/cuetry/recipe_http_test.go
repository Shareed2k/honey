package cuetry

import (
	"strings"
	"testing"
)

func TestClassifyHTTPStep(t *testing.T) {
	step := &HTTPStep{
		StepBase: StepBase{Host: "_"},
		HTTP:     &RecipeStepHTTP{Method: "POST", URL: "http://example/api"},
	}
	if err := step.Validate(StepValidateCtx{}); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
	if step.Kind() != KindHTTP {
		t.Errorf("expected KindHTTP, got %v", step.Kind())
	}
}

func TestParseRemoteRecipe_httpOk(t *testing.T) {
	const src = `
recipe: {
	name: "http-test"
	steps: [
		{
			host: "_"
			http: {
				method: "POST"
				url:    "http://example.test/api/restart"
				timeout: "15s"
				headers: {
					"x-api-key":    "secret"
					"Content-Type": "application/json"
				}
				body:          "{\"id\":\"c1\"}"
				expect_status: [200, 202]
			}
		},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatalf("failed to parse valid http recipe: %v", err)
	}
	if len(r.Steps) != 1 {
		t.Fatalf("expected 1 step, got %d", len(r.Steps))
	}
	step, ok := r.Steps[0].Step.(*HTTPStep)
	if !ok {
		t.Fatalf("expected *HTTPStep, got %T", r.Steps[0].Step)
	}
	if step.HTTP == nil || step.HTTP.Method != "POST" || step.HTTP.URL != "http://example.test/api/restart" {
		t.Fatalf("parsed http config wrong: %+v", step.HTTP)
	}
	if step.HTTP.Timeout != "15s" {
		t.Errorf("timeout = %q, want 15s (override honored)", step.HTTP.Timeout)
	}
	if step.HTTP.Headers["x-api-key"] != "secret" || step.HTTP.Body != `{"id":"c1"}` {
		t.Errorf("headers/body wrong: %+v / %q", step.HTTP.Headers, step.HTTP.Body)
	}
	if len(step.HTTP.ExpectStatus) != 2 || step.HTTP.ExpectStatus[0] != 200 {
		t.Errorf("expect_status wrong: %v", step.HTTP.ExpectStatus)
	}
}

func TestParseRemoteRecipe_httpDefaultsTimeoutEmpty(t *testing.T) {
	const src = `
recipe: {
	name: "http-default"
	steps: [{host: "_", http: {url: "http://example.test/x"}}]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	step := r.Steps[0].Step.(*HTTPStep)
	if step.HTTP.Timeout != "" {
		t.Errorf("timeout = %q, want empty (executor applies the 30s default)", step.HTTP.Timeout)
	}
	if step.HTTP.Method != "" {
		t.Errorf("method = %q, want empty (executor defaults to GET)", step.HTTP.Method)
	}
}

func TestValidateHTTPStep_errors(t *testing.T) {
	cases := map[string]struct {
		http *RecipeStepHTTP
		want string
	}{
		"missing url":      {&RecipeStepHTTP{Method: "GET"}, "url is required"},
		"bad method":       {&RecipeStepHTTP{URL: "http://x", Method: "FETCH"}, "method must be one of"},
		"bad timeout":      {&RecipeStepHTTP{URL: "http://x", Timeout: "10sec"}, "not a valid duration"},
		"status too small": {&RecipeStepHTTP{URL: "http://x", ExpectStatus: []int{99}}, "out of range"},
		"status too big":   {&RecipeStepHTTP{URL: "http://x", ExpectStatus: []int{600}}, "out of range"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			err := (&HTTPStep{HTTP: tc.http}).Validate(StepValidateCtx{})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("Validate err = %v, want containing %q", err, tc.want)
			}
		})
	}
}

func TestParseRemoteRecipe_httpEnvFromOk(t *testing.T) {
	const src = `
recipe: {
	name: "http-env-from"
	type: "graph"
	steps: [
		{id: "list", host: "_", http: {method: "POST", url: "http://x/api/list", body: "{}"}},
		{
			id:      "act"
			host:    "_"
			depends: ["list"]
			env_from: [{step: "list", extract: {CID: ".id"}}]
			http: {method: "POST", url: "http://x/api/act", body: "{\"id\":\"{{ .env.CID }}\"}"}
		},
	]
}
`
	r, err := ParseRemoteRecipe([]byte(src), nil)
	if err != nil {
		t.Fatalf("http step with env_from should validate: %v", err)
	}
	act, ok := r.Steps[1].Step.(*HTTPStep)
	if !ok {
		t.Fatalf("step 1 is %T, want *HTTPStep", r.Steps[1].Step)
	}
	if len(act.Base().EnvFrom) != 1 || act.Base().EnvFrom[0].Extract["CID"] != ".id" {
		t.Fatalf("env_from not parsed onto http step: %+v", act.Base().EnvFrom)
	}
}

func TestParseRemoteRecipe_httpBadMethodRejected(t *testing.T) {
	const src = `
recipe: {
	name: "http-bad-method"
	steps: [{host: "_", http: {url: "http://x", method: "FETCH"}}]
}
`
	if _, err := ParseRemoteRecipe([]byte(src), nil); err == nil {
		t.Fatal("expected a schema/validation error for method: FETCH, got nil")
	}
}
