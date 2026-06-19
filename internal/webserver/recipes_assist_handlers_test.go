package webserver

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/hosts"
)

func TestCapRecipeAssistRecords(t *testing.T) {
	var recs []hosts.Record
	for i := 0; i < maxRecipeAssistRecords+10; i++ {
		recs = append(recs, hosts.Record{Name: string(rune('a' + i%26))})
	}
	out := capRecipeAssistRecords(recs)
	if len(out) != maxRecipeAssistRecords {
		t.Fatalf("len=%d want %d", len(out), maxRecipeAssistRecords)
	}
	small := []hosts.Record{{Name: "a"}}
	if capRecipeAssistRecords(small) == nil || len(capRecipeAssistRecords(small)) != 1 {
		t.Fatalf("small slice")
	}
}

func TestClipRunesForRecipeAssist(t *testing.T) {
	s := string([]rune{'a', 'b', 'c', 'd'})
	got := clipRunesForRecipeAssist(s, 3)
	if got == s || !strings.Contains(got, "truncated") {
		t.Fatalf("got %q", got)
	}
}

func TestHandleRecipesAIFixAndGenerate(t *testing.T) {
	// We just test the signature and auth/method bounds since we don't have an OpenAI key in tests.
	srv, _ := NewServer(Options{Token: "dummy", ListenAddr: "127.0.0.1:0"})

	req1 := httptest.NewRequest("POST", "/api/v1/recipes/assist-fix", bytes.NewBufferString(`{}`))
	req1.Header.Set("Authorization", "Bearer dummy")
	w1 := httptest.NewRecorder()
	srv.router.ServeHTTP(w1, req1)
	if w1.Code == http.StatusNotFound {
		t.Errorf("handleRecipesAssistFix not implemented")
	}

	req2 := httptest.NewRequest("POST", "/api/v1/recipes/generate", bytes.NewBufferString(`{}`))
	req2.Header.Set("Authorization", "Bearer dummy")
	w2 := httptest.NewRecorder()
	srv.router.ServeHTTP(w2, req2)
	if w2.Code == http.StatusNotFound {
		t.Errorf("handleRecipesGenerate not implemented")
	}
}
