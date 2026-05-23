package truenasprovider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
	"github.com/shareed2k/honey/internal/hosts"
)

func TestSearchApplianceAndVirt(t *testing.T) {
	upgrader := websocket.Upgrader{CheckOrigin: func(_ *http.Request) bool { return true }}
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		for {
			var req jsonRPCRequest
			if err := conn.ReadJSON(&req); err != nil {
				return
			}
			var result any
			switch req.Method {
			case "auth.login_ex":
				result = map[string]string{"response_type": "SUCCESS"}
			case "system.info":
				result = map[string]any{"hostname": "nas-lab", "version": "25.04.0"}
			case "system.version":
				result = "25.04.0"
			case "vm.query":
				result = []vmRow{{ID: 1, Name: "vm1", State: "RUNNING"}, {ID: 2, Name: "vm-stopped", State: "STOPPED"}}
			case "virt.instance.query":
				result = []virtRow{{
					ID: "abc", Name: "px-1", Status: "RUNNING", Type: "CONTAINER",
					Aliases: []virtAlias{{Type: "INET", Address: "10.0.0.5"}},
				}}
			default:
				result = nil
			}
			_ = conn.WriteJSON(jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: mustMarshal(result)})
		}
	}))
	defer srv.Close()

	b := &TrueNAS{
		Name:             "lab",
		URL:              srv.URL,
		Username:         "root",
		APIKey:           "1-test",
		Insecure:         true,
		IncludeAppliance: true,
		IncludeVMs:       true,
		IncludeVirt:      true,
		SSHUser:          "root",
	}
	recs, err := b.Search(context.Background(), hosts.Query{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recs) != 3 {
		t.Fatalf("expected 3 records (appliance + vm + virt), got %d", len(recs))
	}
	foundAppliance, foundVM, foundVirt := false, false, false
	for _, r := range recs {
		switch r.Meta["kind"] {
		case "appliance":
			foundAppliance = true
			if r.PrimaryIP == "" {
				t.Error("appliance missing primary_ip")
			}
		case "vm":
			foundVM = true
			if r.Name != "vm1" {
				t.Errorf("vm name: %s", r.Name)
			}
		case "virt_instance":
			foundVirt = true
			if r.PrimaryIP != "10.0.0.5" {
				t.Errorf("virt ip: %s", r.PrimaryIP)
			}
		}
	}
	if !foundAppliance || !foundVM || !foundVirt {
		t.Fatalf("missing kinds: appliance=%v vm=%v virt=%v", foundAppliance, foundVM, foundVirt)
	}
}

func mustMarshal(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
