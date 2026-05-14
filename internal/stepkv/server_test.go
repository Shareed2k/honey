package stepkv

import (
	"net/http"
	"testing"
	"time"
)

func TestSession_healthGET(t *testing.T) {
	s, err := Start(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	req, err := http.NewRequest(http.MethodGet, s.LocalBaseURL()+"/v1/kv/__health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.Token())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("health: want 200, got %d", res.StatusCode)
	}
}

func TestSession_healthPUT_methodNotAllowed(t *testing.T) {
	s, err := Start(time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	req, err := http.NewRequest(http.MethodPut, s.LocalBaseURL()+"/v1/kv/__health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+s.Token())
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("PUT __health: want 405, got %d", res.StatusCode)
	}
}
