package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestInterceptAuthorizeClient_ParsesSession(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/intercept/authorize", r.URL.Path)
		var body map[string]any
		require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
		require.Equal(t, "api-7d9f", body["pod"])
		require.Equal(t, "prod", body["cluster"])
		require.Equal(t, "prod-ns", body["namespace"])
		require.Equal(t, "idtok", body["id_token"])
		require.Equal(t, "n", body["nonce"])
		_ = json.NewEncoder(w).Encode(map[string]any{
			"session_id": "s1", "token": "t1", "control_port": 30000, "egress_port": 30001,
		})
	}))
	defer srv.Close()
	resp, err := interceptAuthorize(context.Background(), srv.URL, "idtok", "n", brokeredAuthorizeReq{
		Cluster: "prod", Namespace: "prod-ns", Pod: "api-7d9f", Mode: []string{"egress"},
	})
	require.NoError(t, err)
	require.Equal(t, "s1", resp.SessionID)
	require.Equal(t, "t1", resp.Token)
	require.Equal(t, 30000, resp.ControlPort)
	require.Equal(t, 30001, resp.EgressPort)
}

func TestInterceptAuthorizeClient_NonOKStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"intercept not authorized"}`, http.StatusForbidden)
	}))
	defer srv.Close()
	_, err := interceptAuthorize(context.Background(), srv.URL, "idtok", "n", brokeredAuthorizeReq{
		Cluster: "prod", Namespace: "prod-ns", Pod: "api-7d9f",
	})
	require.Error(t, err)
}

func TestFetchInterceptConfig_Enabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": true, "default_mode": []string{"egress", "files"}})
	}))
	defer srv.Close()
	enabled, modes, err := fetchInterceptConfig(context.Background(), srv.URL)
	require.NoError(t, err)
	require.True(t, enabled)
	require.Equal(t, []string{"egress", "files"}, modes)
}

func TestFetchInterceptConfig_Disabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"enabled": false, "default_mode": []string{}})
	}))
	defer srv.Close()
	enabled, _, err := fetchInterceptConfig(context.Background(), srv.URL)
	require.NoError(t, err)
	require.False(t, enabled)
}

func TestInterceptStop_NoContent(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	err := interceptStop(context.Background(), srv.URL, "sess-1", "sess-tok")
	require.NoError(t, err)
	require.Equal(t, "/api/v1/intercept/sess-1/stop", gotPath)
	require.Equal(t, "sess-tok", gotBody["token"], "interceptStop must post the session token")
	_, hasIDToken := gotBody["id_token"]
	require.False(t, hasIDToken, "interceptStop must not post id_token")
}

func TestInterceptStop_ErrorStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":"unknown session"}`, http.StatusNotFound)
	}))
	defer srv.Close()
	err := interceptStop(context.Background(), srv.URL, "sess-1", "sess-tok")
	require.Error(t, err)
}
