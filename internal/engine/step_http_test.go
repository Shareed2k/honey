package engine

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// runHTTPStep drives HTTPExecutor.ExecuteStream for a single operator-local
// target and returns the one emitted result.
func runHTTPStep(t *testing.T, step *cuetry.HTTPStep, env map[string]string, retry cuetry.RecipeStepRetry) HostExecResult {
	t.Helper()
	req := ExecutionRequest{
		Index:    0,
		Kind:     cuetry.KindHTTP,
		Step:     step,
		RetryCfg: retry,
		Targets:  []TargetContext{{Record: hosts.Record{Name: "_", PrimaryIP: "-", Provider: "local"}, Env: env}},
	}
	opts := ExecutionOptions{
		Recipe:        cuetry.Recipe{Name: "http-test"},
		OutputStore:   cuetry.NewStepOutputStore(),
		OutputCapture: cuetry.NewRecipeOutputCapture(),
	}
	ch := make(chan HostExecResult, 1)
	if err := (&HTTPExecutor{}).ExecuteStream(context.Background(), req, opts, ch); err != nil {
		t.Fatalf("ExecuteStream: %v", err)
	}
	return <-ch
}

func TestHTTPExecutor_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s, want POST", r.Method)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok-body"))
	}))
	defer srv.Close()

	res := runHTTPStep(t, &cuetry.HTTPStep{HTTP: &cuetry.RecipeStepHTTP{Method: "POST", URL: srv.URL}}, nil, cuetry.RecipeStepRetry{})
	if !res.Success {
		t.Fatalf("Success=false, ErrMsg=%q", res.ErrMsg)
	}
	if res.Output != "ok-body" || res.ExitCode != 200 {
		t.Errorf("Output=%q ExitCode=%d, want ok-body/200", res.Output, res.ExitCode)
	}
}

func TestHTTPExecutor_ExpectStatusMismatch(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK) // 200
	}))
	defer srv.Close()

	res := runHTTPStep(t, &cuetry.HTTPStep{HTTP: &cuetry.RecipeStepHTTP{URL: srv.URL, ExpectStatus: []int{201}}}, nil, cuetry.RecipeStepRetry{})
	if res.Success {
		t.Fatal("Success=true, want false (200 not in expect_status [201])")
	}
	if res.ExitCode != 200 {
		t.Errorf("ExitCode=%d, want 200", res.ExitCode)
	}
}

func TestHTTPExecutor_TemplatedHeaderCarriesSecret(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("x-api-key")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	step := &cuetry.HTTPStep{HTTP: &cuetry.RecipeStepHTTP{
		Method:  "POST",
		URL:     srv.URL,
		Headers: map[string]string{"x-api-key": "{{ .env.DOKPLOY_API_KEY }}"},
	}}
	res := runHTTPStep(t, step, map[string]string{"DOKPLOY_API_KEY": "sekret-123"}, cuetry.RecipeStepRetry{})
	if !res.Success {
		t.Fatalf("Success=false, ErrMsg=%q", res.ErrMsg)
	}
	if gotKey != "sekret-123" {
		t.Errorf("server saw x-api-key=%q, want sekret-123 (env template)", gotKey)
	}
}

func TestHTTPExecutor_RetriesTransientThenSucceeds(t *testing.T) {
	for _, code := range []int{http.StatusServiceUnavailable, http.StatusTooManyRequests} {
		code := code
		t.Run(http.StatusText(code), func(t *testing.T) {
			var n atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				if n.Add(1) < 3 { // fail first two, succeed on the third
					w.WriteHeader(code)
					return
				}
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			retry := cuetry.RecipeStepRetry{Attempts: 3, DelayMS: 1, Backoff: "fixed"}
			res := runHTTPStep(t, &cuetry.HTTPStep{HTTP: &cuetry.RecipeStepHTTP{Method: "POST", URL: srv.URL}}, nil, retry)
			if !res.Success {
				t.Fatalf("Success=false after retries, ErrMsg=%q", res.ErrMsg)
			}
			if got := n.Load(); got != 3 {
				t.Errorf("server got %d requests, want 3 (2 retries)", got)
			}
		})
	}
}

func TestHTTPExecutor_NoRetryOn4xx(t *testing.T) {
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		n.Add(1)
		w.WriteHeader(http.StatusNotFound) // 404 — client error, must not retry
	}))
	defer srv.Close()

	retry := cuetry.RecipeStepRetry{Attempts: 3, DelayMS: 1, Backoff: "fixed"}
	res := runHTTPStep(t, &cuetry.HTTPStep{HTTP: &cuetry.RecipeStepHTTP{URL: srv.URL}}, nil, retry)
	if res.Success {
		t.Fatal("Success=true, want false on 404")
	}
	if got := n.Load(); got != 1 {
		t.Errorf("server got %d requests, want exactly 1 (4xx fails fast, no retry)", got)
	}
}

func TestHTTPExecutor_FailedWhenMarksFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("status: bad"))
	}))
	defer srv.Close()

	step := &cuetry.HTTPStep{
		StepBase: cuetry.StepBase{FailedWhen: `stdout.contains("bad")`},
		HTTP:     &cuetry.RecipeStepHTTP{URL: srv.URL},
	}
	res := runHTTPStep(t, step, nil, cuetry.RecipeStepRetry{})
	if res.Success {
		t.Fatal("Success=true, want false (failed_when matched a 200 body)")
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	now := time.Date(2026, 8, 5, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		val    string
		want   int
		wantOK bool
	}{
		{"absent", "", 0, false},
		{"delta seconds", "5", 5, true},
		{"zero", "0", 0, true},
		{"negative", "-3", 0, false},
		{"garbage", "soon", 0, false},
		{"http date future", now.Add(10 * time.Second).Format(http.TimeFormat), 10, true},
		{"http date past", now.Add(-time.Minute).Format(http.TimeFormat), 0, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.val != "" {
				h.Set("Retry-After", tc.val)
			}
			got, ok := retryAfterSeconds(h, now)
			if ok != tc.wantOK || (ok && got != tc.want) {
				t.Errorf("retryAfterSeconds(%q) = (%d,%v), want (%d,%v)", tc.val, got, ok, tc.want, tc.wantOK)
			}
		})
	}
}

func TestStatusOKAndRetryable(t *testing.T) {
	if !statusOK(204, nil) || statusOK(404, nil) {
		t.Error("statusOK default should accept 2xx only")
	}
	if !statusOK(201, []int{201, 202}) || statusOK(200, []int{201}) {
		t.Error("statusOK with expect list wrong")
	}
	for _, c := range []int{408, 429, 500, 503, 504} {
		if !httpStatusRetryable(c) {
			t.Errorf("httpStatusRetryable(%d) = false, want true", c)
		}
	}
	for _, c := range []int{200, 400, 401, 404} {
		if httpStatusRetryable(c) {
			t.Errorf("httpStatusRetryable(%d) = true, want false", c)
		}
	}
}
