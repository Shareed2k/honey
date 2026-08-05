package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	backoff "github.com/cenkalti/backoff/v5"
	"go.uber.org/zap"

	"github.com/shareed2k/honey/internal/cuetry"
)

func init() {
	RegisterStepExecutor(cuetry.KindHTTP, &HTTPExecutor{})
}

const (
	defaultHTTPTimeout = 30 * time.Second
	maxHTTPBodyBytes   = 1 << 20 // 1 MiB — bound the response read (no OOM on a huge body)
	httpErrSnippetLen  = 256
)

// HTTPExecutor runs an `http` recipe step: a templated HTTP request per target,
// with HTTP-aware retry (transient 5xx/429/network retried with backoff, other
// 4xx failing fast). The request is always issued by the operator process.
type HTTPExecutor struct{}

// ExecuteStream issues the request for each target and streams one result each.
func (e *HTTPExecutor) ExecuteStream(ctx context.Context, req ExecutionRequest, opts ExecutionOptions, resCh chan<- HostExecResult) error {
	hs, ok := req.Step.(*cuetry.HTTPStep)
	if !ok || hs.HTTP == nil {
		return fmt.Errorf("internal: http step missing http field")
	}
	timeout := defaultHTTPTimeout
	if hs.HTTP.Timeout != "" {
		d, err := time.ParseDuration(hs.HTTP.Timeout)
		if err != nil {
			return fmt.Errorf("http step: invalid timeout %q: %w", hs.HTTP.Timeout, err)
		}
		timeout = d
	}
	// One client (goroutine-safe, verifies TLS by default) reused across targets.
	client := &http.Client{Timeout: timeout}

	for _, tc := range req.Targets {
		res := e.doWithRetry(ctx, client, hs.HTTP, tc, opts, req.RetryCfg)
		ApplyCueRecipeResultExpressions(opts, req.Step, tc.Record, &res)
		RunCueStepHooks(ctx, opts, req.Index, cuetry.KindHTTP, req.Step, tc.Record, tc, &res, false)
		resCh <- res
	}
	return nil
}

// ExecuteDryRun prints the planned request per target. The URL is printed raw
// (unrendered), so any "{{ .env.SECRET }}" template shows literally rather than
// leaking a resolved secret.
func (e *HTTPExecutor) ExecuteDryRun(_ context.Context, req ExecutionRequest, _ ExecutionOptions, out io.Writer) error {
	hs, _ := req.Step.(*cuetry.HTTPStep)
	method, url := http.MethodGet, ""
	if hs != nil && hs.HTTP != nil {
		if hs.HTTP.Method != "" {
			method = hs.HTTP.Method
		}
		url = hs.HTTP.URL
	}
	for _, t := range req.Targets {
		_, _ = fmt.Fprintf(out, "step %d: kind=http method=%q url=%q name=%q %s provider=%s\n",
			req.Index, method, url, t.Record.Name, FormatTargetForDryRun(t.Record), t.Record.Provider)
	}
	return nil
}

// doWithRetry issues one request with HTTP-aware retry driven by cfg (the step's
// resolved retry block), on cenkalti/backoff/v5. The final HostExecResult is
// captured in a closure so it survives regardless of backoff.Retry's return.
func (e *HTTPExecutor) doWithRetry(ctx context.Context, client *http.Client, h *cuetry.RecipeStepHTTP, tc TargetContext, opts ExecutionOptions, cfg cuetry.RecipeStepRetry) HostExecResult {
	r := tc.Record
	var last HostExecResult

	op := func() (HostExecResult, error) {
		res := HostExecResult{Name: r.Name, IP: r.PrimaryIP, Provider: r.Provider}

		url, err := renderMaybe(h.URL, opts, tc.Env)
		if err != nil {
			res.ErrMsg = fmt.Sprintf("render url: %v", err)
			last = res
			return res, backoff.Permanent(fmt.Errorf("http step: render url: %w", err))
		}
		body, err := renderMaybe(h.Body, opts, tc.Env)
		if err != nil {
			res.ErrMsg = fmt.Sprintf("render body: %v", err)
			last = res
			return res, backoff.Permanent(fmt.Errorf("http step: render body: %w", err))
		}
		method := h.Method
		if method == "" {
			method = http.MethodGet
		}
		httpReq, err := http.NewRequestWithContext(ctx, method, url, strings.NewReader(body))
		if err != nil {
			res.ErrMsg = err.Error()
			last = res
			return res, backoff.Permanent(fmt.Errorf("http step: build request: %w", err))
		}
		for k, v := range h.Headers {
			hv, err := renderMaybe(v, opts, tc.Env)
			if err != nil {
				res.ErrMsg = fmt.Sprintf("render header %s: %v", k, err)
				last = res
				return res, backoff.Permanent(fmt.Errorf("http step: render header %q: %w", k, err))
			}
			httpReq.Header.Set(k, hv)
		}

		resp, err := client.Do(httpReq)
		if err != nil {
			// Transport-level failure (connection refused, timeout, DNS) — retryable.
			res.Success = false
			res.IsTransient = true
			res.ErrMsg = err.Error()
			last = res
			return res, fmt.Errorf("http step: request failed: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxHTTPBodyBytes))

		res.ExitCode = resp.StatusCode
		res.Output = string(b)
		res.Stdout = res.Output
		res.Success = statusOK(resp.StatusCode, h.ExpectStatus)
		last = res
		if res.Success {
			return res, nil
		}

		res.ErrMsg = fmt.Sprintf("http %d: %s", resp.StatusCode, httpBodySnippet(b))
		if httpStatusRetryable(resp.StatusCode) {
			res.IsTransient = true
			last = res
			if secs, ok := retryAfterSeconds(resp.Header, time.Now()); ok {
				return res, backoff.RetryAfter(secs)
			}
			return res, errors.New(res.ErrMsg)
		}
		// Definitive client error (4xx other than 408/429): won't self-heal.
		return res, backoff.Permanent(errors.New(res.ErrMsg))
	}

	if !cfg.Enabled() {
		res, _ := op()
		return res
	}

	retryOpts := []backoff.RetryOption{
		backoff.WithMaxTries(uint(cfg.Attempts)),
		backoff.WithBackOff(cuetry.BuildBackOff(cfg)),
		backoff.WithNotify(func(err error, next time.Duration) {
			zap.L().Debug("http step retry waiting",
				zap.String("host_name", r.Name),
				zap.Int("max_attempts", cfg.Attempts),
				zap.Duration("next", next),
				zap.Error(err),
			)
		}),
	}
	_, _ = backoff.Retry(ctx, op, retryOpts...)
	return last
}

// statusOK reports whether code is an accepted status: a member of expect when
// non-empty, otherwise any 2xx.
func statusOK(code int, expect []int) bool {
	if len(expect) == 0 {
		return code >= 200 && code < 300
	}
	for _, c := range expect {
		if c == code {
			return true
		}
	}
	return false
}

// httpStatusRetryable reports whether a status code represents a transient
// failure worth retrying: 408 (timeout), 429 (rate limit), or 5xx.
func httpStatusRetryable(code int) bool {
	switch code {
	case http.StatusRequestTimeout, http.StatusTooManyRequests:
		return true
	}
	return code >= 500 && code <= 599
}

// retryAfterSeconds parses a Retry-After header (delta-seconds or HTTP-date)
// into whole seconds to wait, relative to now. Reports ok=false when absent or
// unparseable (caller falls back to the configured backoff schedule).
func retryAfterSeconds(header http.Header, now time.Time) (int, bool) {
	v := strings.TrimSpace(header.Get("Retry-After"))
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return secs, true
	}
	if t, err := http.ParseTime(v); err == nil {
		d := t.Sub(now)
		if d <= 0 {
			return 0, true
		}
		return int(d.Round(time.Second).Seconds()), true
	}
	return 0, false
}

// renderMaybe renders a possibly-empty template value: an empty input yields ""
// (renderStepTemplate rejects an empty template), so optional fields like body
// or an absent header don't error.
func renderMaybe(s string, opts ExecutionOptions, env map[string]string) (string, error) {
	if s == "" {
		return "", nil
	}
	return renderStepTemplate(s, opts, env)
}

// httpBodySnippet returns a short single-line excerpt of a response body for an
// error message.
func httpBodySnippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > httpErrSnippetLen {
		return s[:httpErrSnippetLen] + "…"
	}
	return s
}
