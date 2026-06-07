//go:build integration

package integration

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	opensearch "github.com/opensearch-project/opensearch-go/v2"
	"github.com/opensearch-project/opensearch-go/v2/opensearchapi"
)

func newOpenSearchClient(t *testing.T) *opensearch.Client {
	t.Helper()
	addr := startOpenSearch(t)
	client, err := opensearch.NewClient(opensearch.Config{
		Addresses: []string{addr},
		Username:  "admin",
		Password:  "Qx7#nBm2pLv!",
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // integration test only
		},
	})
	if err != nil {
		t.Fatalf("opensearch client: %v", err)
	}
	return client
}

func TestOpenSearch_IndexAndGet(t *testing.T) {
	client := newOpenSearchClient(t)
	ctx := context.Background()

	index := "test-logs-index"
	docID := "doc-1"
	doc := map[string]any{
		"message": "disk usage 95%",
		"level":   "error",
		"host":    "integration-test-host",
	}
	body, _ := json.Marshal(doc)

	// Index a document.
	indexResp, err := opensearchapi.IndexRequest{
		Index:      index,
		DocumentID: docID,
		Body:       bytes.NewReader(body),
		Refresh:    "true",
	}.Do(ctx, client)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	defer indexResp.Body.Close()
	if indexResp.IsError() {
		t.Fatalf("index error: %s", indexResp.String())
	}

	// Get the document back.
	getResp, err := opensearchapi.GetRequest{
		Index:      index,
		DocumentID: docID,
	}.Do(ctx, client)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	defer getResp.Body.Close()
	if getResp.IsError() {
		t.Fatalf("get error: %s", getResp.String())
	}

	var result map[string]any
	if err := json.NewDecoder(getResp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	src, _ := result["_source"].(map[string]any)
	if src["message"] != "disk usage 95%" {
		t.Fatalf("unexpected message: %v", src["message"])
	}
}

func TestOpenSearch_SearchFilter(t *testing.T) {
	client := newOpenSearchClient(t)
	ctx := context.Background()

	index := "test-logs-filter"

	// Index one error doc and one info doc.
	for i, doc := range []map[string]any{
		{"message": "err: disk full", "level": "error"},
		{"message": "server started", "level": "info"},
	} {
		body, _ := json.Marshal(doc)
		resp, err := opensearchapi.IndexRequest{
			Index:   index,
			Body:    bytes.NewReader(body),
			Refresh: "true",
		}.Do(ctx, client)
		if err != nil {
			t.Fatalf("index doc %d: %v", i, err)
		}
		resp.Body.Close()
		if resp.IsError() {
			t.Fatalf("index doc %d error: %s", i, resp.String())
		}
	}

	// Search for level=error only.
	query := `{"query":{"term":{"level.keyword":"error"}}}`
	searchResp, err := opensearchapi.SearchRequest{
		Index: []string{index},
		Body:  strings.NewReader(query),
	}.Do(ctx, client)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	defer searchResp.Body.Close()
	if searchResp.IsError() {
		t.Fatalf("search error: %s", searchResp.String())
	}

	var result struct {
		Hits struct {
			Total struct{ Value int } `json:"total"`
		} `json:"hits"`
	}
	if err := json.NewDecoder(searchResp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if result.Hits.Total.Value != 1 {
		t.Fatalf("want 1 error doc, got %d", result.Hits.Total.Value)
	}
}
