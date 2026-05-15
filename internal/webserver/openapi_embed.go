package webserver

import (
	_ "embed"
	"net/http"
)

//go:embed swaggerdocs/openapi.json
var embeddedOpenAPIv3 []byte

func (s *Server) handleOpenAPIJSON(w http.ResponseWriter, r *http.Request) {
	_ = r
	if len(embeddedOpenAPIv3) == 0 {
		http.Error(w, `{"error":"openapi spec not embedded; run go generate in internal/webserver"}`, http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(embeddedOpenAPIv3)
}
