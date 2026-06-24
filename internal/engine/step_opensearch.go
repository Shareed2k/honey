package engine

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

// StreamCueStepOpensearch ...
func init() {
	RegisterStepExecutor(cuetry.KindOpensearch, &OpensearchExecutor{})
}

// OpensearchExecutor executes the corresponding recipe step.
type OpensearchExecutor struct{}

// ExecuteDryRun executes a dry run of the step.
func (e *OpensearchExecutor) ExecuteDryRun(_ *StepContext) error {
	return nil
}

// ExecuteStream streams the step execution.
func (e *OpensearchExecutor) ExecuteStream(sc *StepContext) error {
	run, ctx, step, targets, ch, retryCfg, attemptMax := sc.Run, sc.Ctx, sc.Step, sc.Targets, sc.ResultCh, sc.RetryCfg, sc.AttemptMax
	os, _ := step.(*cuetry.OpensearchStep)
	if os == nil || os.Opensearch == nil {
		return fmt.Errorf("internal: opensearch step missing opensearch field")
	}
	es := os.Opensearch
	if es == nil {
		return fmt.Errorf("internal: opensearch step missing config")
	}

	execOne := func(r hosts.Record) HostExecResult {
		outcome := RunHostExecWithRetry(ctx, retryCfg, func() HostExecResult {
			res := HostExecResult{
				Name:     r.Name,
				IP:       r.PrimaryIP,
				Provider: r.Provider,
			}

			var trans http.RoundTripper
			if es.Insecure {
				trans = &http.Transport{
					// #nosec G402
					TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
				}
			}

			header := http.Header{}
			if es.APIKey != "" {
				header.Set("Authorization", "ApiKey "+es.APIKey)
			}

			cfg := opensearch.Config{
				Addresses: es.Addresses,
				Username:  es.Username,
				Password:  es.Password,
				Header:    header,
				Transport: trans,
			}

			client, err := opensearch.NewClient(cfg)
			if err != nil {
				res.Success = false
				res.ErrMsg = fmt.Sprintf("failed to init opensearch client: %s", err.Error())
				return res
			}

			var apiRes *opensearchapi.Response
			action := strings.ToLower(strings.TrimSpace(es.Action))
			switch action {
			case "index":
				bodyBytes, marshalErr := json.Marshal(es.Body)
				if marshalErr != nil {
					res.Success = false
					res.ErrMsg = fmt.Sprintf("failed to marshal document: %s", marshalErr.Error())
					return res
				}
				var opts []func(*opensearchapi.IndexRequest)
				if es.DocID != "" {
					opts = append(opts, client.Index.WithDocumentID(es.DocID))
				}
				apiRes, err = client.Index(es.Index, bytes.NewReader(bodyBytes), opts...)

			case "get":
				apiRes, err = client.Get(es.Index, es.DocID)

			case "search":
				var opts []func(*opensearchapi.SearchRequest)
				opts = append(opts, client.Search.WithIndex(es.Index))
				if len(es.Body) > 0 {
					bodyBytes, marshalErr := json.Marshal(es.Body)
					if marshalErr != nil {
						res.Success = false
						res.ErrMsg = fmt.Sprintf("failed to marshal query: %s", marshalErr.Error())
						return res
					}
					opts = append(opts, client.Search.WithBody(bytes.NewReader(bodyBytes)))
				}
				apiRes, err = client.Search(opts...)
			}

			if err != nil {
				res.Success = false
				res.ErrMsg = fmt.Sprintf("opensearch request failed: %s", err.Error())
				return res
			}
			defer apiRes.Body.Close()

			bodyBytes, err := io.ReadAll(apiRes.Body)
			if err != nil {
				res.Success = false
				res.ErrMsg = fmt.Sprintf("failed to read response: %s", err.Error())
				return res
			}

			if apiRes.IsError() {
				res.Success = false
				res.ErrMsg = fmt.Sprintf("opensearch api error (status %s): %s", apiRes.Status(), string(bodyBytes))
				return res
			}

			res.Success = true
			res.Output = string(bodyBytes)
			return res
		})
		RecordMaxAttempts(attemptMax, outcome.Attempts)
		return outcome.Result
	}

	for _, target := range targets {
		res := execOne(target)
		if res.Success && es.Output != "" && run.OutputCapture != nil {
			run.OutputCapture.Set(es.Output, res.Output)
		}
		ch <- res
	}
	return nil
}
