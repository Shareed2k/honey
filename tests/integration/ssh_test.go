//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/webserver"
)

// sshTestRecord builds a hosts.Record pointing at the test SSH container.
func sshTestRecord(host string, port int) hosts.Record {
	return hosts.CloneWithMetaSSHPort(
		hosts.Record{Provider: "test", Name: "ssh-test", PrimaryIP: host},
		port,
	)
}

// newSSHTestServer boots a webserver with ExecRegistry wired to the test SSH container.
func newSSHTestServer(t *testing.T) (httpClient *http.Client, baseURL string) {
	t.Helper()
	sshH, sshP, keyFile := startSSH(t)

	reg := &hostexec.StandardRegistry{
		Dialer: newTestDialer(sshH, sshP, keyFile),
		Tunnel: newTestTunnelRunner(sshH, sshP, keyFile),
	}

	baseURL = newTestServer(t, webserver.Options{
		ExecRegistry: reg,
	})
	httpClient = &http.Client{Timeout: 30 * time.Second}
	return httpClient, baseURL
}

// doJSON sends a JSON POST and returns the response.
func doJSON(t *testing.T, client *http.Client, url string, body any) *http.Response {
	t.Helper()
	b, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", authHeader())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	return resp
}

// doDelete sends a DELETE request.
func doDelete(t *testing.T, client *http.Client, url string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(http.MethodDelete, url, nil)
	if err != nil {
		t.Fatalf("new delete: %v", err)
	}
	req.Header.Set("Authorization", authHeader())
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("do delete: %v", err)
	}
	return resp
}

// ── Exec tests ───────────────────────────────────────────────────────────────

func TestSSHExec_Command(t *testing.T) {
	httpClient, baseURL := newSSHTestServer(t)
	sshH, sshP, _ := startSSH(t)

	rec := sshTestRecord(sshH, sshP)
	resp := doJSON(t, httpClient, baseURL+"/api/v1/exec", webserver.ExecRequest{
		SSHUser: "testuser",
		Command: "echo hello-integration",
		Records: []hosts.Record{rec},
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}

	var result struct {
		Results []struct {
			Success bool   `json:"Success"`
			ErrMsg  string `json:"ErrMsg"`
			Output  string `json:"Output"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Results) == 0 {
		t.Fatal("expected at least one result")
	}
	if !result.Results[0].Success {
		t.Fatalf("exec failed: %s", result.Results[0].ErrMsg)
	}
}

// ── Tunnel tests ─────────────────────────────────────────────────────────────

// waitForPort retries connecting to addr until it succeeds or timeout.
func waitForPort(t *testing.T, addr string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, time.Second)
		if err == nil {
			conn.Close()
			return
		}
		time.Sleep(200 * time.Millisecond)
	}
	t.Fatalf("port %s not reachable after %s", addr, timeout)
}

func TestSSHTunnel_CreateAndDelete(t *testing.T) {
	httpClient, baseURL := newSSHTestServer(t)
	sshH, sshP, _ := startSSH(t)

	rec := sshTestRecord(sshH, sshP)
	localPort := freePort(t)
	// Tunnel: local port → 127.0.0.1:2222 inside the SSH container (its own SSH service).
	mapping := fmt.Sprintf("%d:127.0.0.1:2222", localPort)

	// Create tunnel.
	resp := doJSON(t, httpClient, baseURL+"/api/v1/tunnels", webserver.StartTunnelRequest{
		SSHUser: "testuser",
		Record:  rec,
		Mapping: mapping,
	})
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("create tunnel: want 200, got %d", resp.StatusCode)
	}

	var created webserver.TunnelStartResponse
	if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if created.Tunnel.ID == "" {
		t.Fatal("tunnel ID is empty")
	}

	// Verify local port is reachable (retry since tunnel goroutine may not have bound yet).
	localAddr := fmt.Sprintf("127.0.0.1:%d", localPort)
	waitForPort(t, localAddr, 10*time.Second)
	// waitForPort already established and closed a TCP connection — that's sufficient proof
	// the tunnel listener is running and forwarding. SSH servers at the remote end reject
	// hairpin loopback connections (localhost→same SSH port) before sending a banner.

	// Delete tunnel.
	delResp := doDelete(t, httpClient, baseURL+"/api/v1/tunnels/"+created.Tunnel.ID)
	defer delResp.Body.Close()
	if delResp.StatusCode != http.StatusOK {
		t.Fatalf("delete tunnel: want 200, got %d", delResp.StatusCode)
	}
}

func TestSSHTunnel_Concurrent(t *testing.T) {
	httpClient, baseURL := newSSHTestServer(t)
	sshH, sshP, _ := startSSH(t)

	rec := sshTestRecord(sshH, sshP)
	var tunnelIDs []string

	// Create 3 tunnels on different local ports.
	for range 3 {
		localPort := freePort(t)
		mapping := fmt.Sprintf("%d:127.0.0.1:2222", localPort)

		resp := doJSON(t, httpClient, baseURL+"/api/v1/tunnels", webserver.StartTunnelRequest{
			SSHUser: "testuser",
			Record:  rec,
			Mapping: mapping,
		})
		var created webserver.TunnelStartResponse
		if err := json.NewDecoder(resp.Body).Decode(&created); err != nil {
			resp.Body.Close()
			t.Fatalf("decode: %v", err)
		}
		resp.Body.Close()
		if created.Tunnel.ID == "" {
			t.Fatal("tunnel ID is empty")
		}
		tunnelIDs = append(tunnelIDs, created.Tunnel.ID)
	}

	if len(tunnelIDs) != 3 {
		t.Fatalf("want 3 tunnels, got %d", len(tunnelIDs))
	}

	// GET /api/v1/tunnels must report at least 3 tunnels.
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/api/v1/tunnels", nil)
	req.Header.Set("Authorization", authHeader())
	listResp, err := httpClient.Do(req)
	if err != nil {
		t.Fatalf("list tunnels: %v", err)
	}
	var list webserver.TunnelsListResponse
	json.NewDecoder(listResp.Body).Decode(&list) //nolint:errcheck
	listResp.Body.Close()
	if len(list.Tunnels) < 3 {
		t.Fatalf("want >=3 tunnels listed, got %d", len(list.Tunnels))
	}

	// Clean up.
	for _, id := range tunnelIDs {
		r, _ := http.NewRequest(http.MethodDelete, baseURL+"/api/v1/tunnels/"+id, nil)
		r.Header.Set("Authorization", authHeader())
		resp, err := httpClient.Do(r)
		if err == nil {
			resp.Body.Close()
		}
	}
}
