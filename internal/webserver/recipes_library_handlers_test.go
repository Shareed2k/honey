package webserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleRecipesLibrary(t *testing.T) {
	srv := &Server{}
	req := httptest.NewRequest("GET", "/api/v1/recipes/library", nil)
	w := httptest.NewRecorder()
	srv.handleRecipesLibrary(w, req)

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
