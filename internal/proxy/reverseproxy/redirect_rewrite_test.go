package reverseproxy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestForwarder_ModifyResponse_RedirectRewrite(t *testing.T) {
	// 1. Create a test upstream server that redirects to itself
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/redirect-absolute" {
			http.Redirect(w, r, "http://"+r.Host+"/target", http.StatusFound)
			return
		}
		if r.URL.Path == "/redirect-relative" {
			http.Redirect(w, r, "/target", http.StatusFound)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer upstream.Close()

	u, err := url.Parse(upstream.URL)
	if err != nil {
		t.Fatalf("Failed to parse upstream URL: %v", err)
	}

	f := New(u)

	// 2. Test Absolute Redirect Rewrite
	req1 := httptest.NewRequest("GET", "/redirect-absolute", nil)
	req1.Host = "appname.localhost:8765"
	w1 := httptest.NewRecorder()

	f.ServeHTTP(w1, req1)

	loc1 := w1.Result().Header.Get("Location")
	expected1 := "http://appname.localhost:8765/target"
	if loc1 != expected1 {
		t.Errorf("Expected redirect location %q, got %q", expected1, loc1)
	}

	// 3. Test Relative Redirect Rewrite
	req2 := httptest.NewRequest("GET", "/redirect-relative", nil)
	req2.Host = "appname.localhost:8765"
	w2 := httptest.NewRecorder()

	f.ServeHTTP(w2, req2)

	loc2 := w2.Result().Header.Get("Location")
	expected2 := "http://appname.localhost:8765/target"
	if loc2 != expected2 {
		t.Errorf("Expected redirect location %q, got %q", expected2, loc2)
	}
}
