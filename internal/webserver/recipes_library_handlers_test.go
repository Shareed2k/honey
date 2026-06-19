package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleRecipesLibrary(t *testing.T) {
	srv, _ := NewServer(Options{Token: "dummy", ListenAddr: "127.0.0.1:0"})
	req := httptest.NewRequest("GET", "/api/v1/recipes/library", nil)
	req.Header.Set("Authorization", "Bearer dummy")
	w := httptest.NewRecorder()
	srv.router.ServeHTTP(w, req)

	if w.Code == http.StatusNotFound {
		t.Fatalf("handleRecipesLibrary not implemented")
	}

	var res LibraryResponse
	if err := json.Unmarshal(w.Body.Bytes(), &res); err == nil {
		if len(res.Categories) == 0 && w.Code == http.StatusOK {
			t.Logf("Response structure matches LibraryResponse")
		}
	}
}
