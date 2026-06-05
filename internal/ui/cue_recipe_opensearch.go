package ui

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/hosts"
)

func streamCueStepOpensearch(ctx context.Context, run *cueRun, _ int, step cuetry.RecipeStep, targets []hosts.Record, ch chan<- HostExecResult, retryCfg cuetry.RecipeStepRetry, attemptMax *atomic.Int32) error {
	es := step.Opensearch
	if es == nil {
		return fmt.Errorf("internal: opensearch step missing config")
	}

	execOne := func(r hosts.Record) HostExecResult {
		outcome := runHostExecWithRetry(ctx, retryCfg, func() HostExecResult {
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
		recordMaxAttempts(attemptMax, outcome.Attempts)
		return outcome.Result
	}

	for _, target := range targets {
		res := execOne(target)
		if res.Success && es.Output != "" && run.outputCapture != nil {
			run.outputCapture.Set(es.Output, res.Output)
		}
		ch <- res
	}
	return nil
}
