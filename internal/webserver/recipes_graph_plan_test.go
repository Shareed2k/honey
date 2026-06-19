package webserver

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func testServer(t *testing.T) *Server {
	t.Helper()
	s, err := NewServer(Options{ListenAddr: "127.0.0.1:0", Token: "secret", Version: "0"})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestHandleRecipesGraphPlan_requiresGraph(t *testing.T) {
	t.Parallel()
	s := testServer(t)
	body := `{"recipe_content":{"name":"l","steps":[{"host":"*","command":"x"}]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/graph-plan", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
}

func TestHandleRecipesGraphPlan_ok(t *testing.T) {
	t.Parallel()
	s := testServer(t)
	body := `{"recipe_content":{"name":"g","type":"graph","steps":[
		{"id":"a","host":"*","command":"echo"},
		{"id":"b","host":"*","depends":["a"],"command":"echo"}
	]}}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/recipes/graph-plan", bytes.NewReader([]byte(body)))
	req.Header.Set("Authorization", "Bearer secret")
	rec := httptest.NewRecorder()
	s.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rec.Code, rec.Body.String())
	}
	var plan struct {
		Type  string `json:"type"`
		Nodes []any  `json:"nodes"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&plan); err != nil {
		t.Fatal(err)
	}
	if plan.Type != "graph" || len(plan.Nodes) != 2 {
		t.Fatalf("%+v", plan)
	}
}
