//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/shareed2k/honey/internal/apps"
	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/cuetry"
	"github.com/shareed2k/honey/internal/engine"
	"github.com/shareed2k/honey/internal/hosts"
)

type fakeHostClient struct {
	engine.HostClient
}

func (c *fakeHostClient) SupportsKVTunnel() bool {
	return false
}

func (c *fakeHostClient) Run(cmd string) ([]byte, error) {
	fmt.Printf("fakeHostClient.Run called with %s\n", cmd)
	return []byte("test-email-output"), nil
}

func (c *fakeHostClient) Close() error {
	return nil
}

type fakeFailingHostClient struct {
	engine.HostClient
}

func (c *fakeFailingHostClient) SupportsKVTunnel() bool {
	return false
}

func (c *fakeFailingHostClient) Run(cmd string) ([]byte, error) {
	fmt.Printf("fakeFailingHostClient.Run called with %s\n", cmd)
	return []byte("test-email-error-output"), fmt.Errorf("simulated failure")
}

func (c *fakeFailingHostClient) Close() error {
	return nil
}

func TestEmailNotificationE2E(t *testing.T) {
	ctx := context.Background()

	// Spin up Mailpit to catch SMTP traffic and verify it via HTTP API.
	// mailhog/mailhog is amd64-only and abandoned (no arm64 image, no qemu
	// emulation on arm64 Docker hosts like Colima); mailpit is its actively
	// maintained, natively multi-arch, SMTP/port-compatible successor.
	reqContainer := testcontainers.ContainerRequest{
		Image:        "axllent/mailpit:latest",
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor:   wait.ForHTTP("/").WithPort("8025/tcp"),
	}
	mailpitC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: reqContainer,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start mailpit: %v", err)
	}
	defer mailpitC.Terminate(ctx)

	host, err := mailpitC.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get mailpit host: %v", err)
	}
	smtpPort, err := mailpitC.MappedPort(ctx, "1025")
	if err != nil {
		t.Fatalf("failed to get mailpit smtp port: %v", err)
	}
	httpPort, err := mailpitC.MappedPort(ctx, "8025")
	if err != nil {
		t.Fatalf("failed to get mailpit http port: %v", err)
	}

	tmpDir := t.TempDir()
	recipePath := filepath.Join(tmpDir, "test_email.cue")
	cueContent := `
recipe: {
	name: "test-email-notifications"
	notification: {
		email: {
			send_on: {
				success: true
			}
			on_success: {
				from: "bot@honey.local"
				to: ["admin@honey.local"]
				prefix: "[HONEY-TEST]"
			}
		}
	}
	steps: [
		{
			host: "localhost"
			command: "echo test-email-output"
		}
	]
}
`
	if err := os.WriteFile(recipePath, []byte(cueContent), 0o600); err != nil {
		t.Fatal(err)
	}

	portInt, _ := strconv.Atoi(smtpPort.Port())
	cfg := &config.File{
		Apps: map[string]apps.AppConfig{},
		SMTP: &config.SMTPConfig{
			Host: host,
			Port: portInt,
		},
	}

	raw, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatal(err)
	}

	recipe, err := cuetry.ParseRemoteRecipeOpts(raw, nil, cuetry.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}

	if recipe.Notification != nil && recipe.Notification.Email != nil && recipe.Notification.Email.SendOn != nil {
		t.Logf("Recipe Notification Email SendOn success: %v", recipe.Notification.Email.SendOn.Success)
	} else {
		t.Log("Recipe Notification Email SendOn is nil!")
	}
	if recipe.Notification != nil && recipe.Notification.Email != nil && recipe.Notification.Email.OnSuccess != nil {
		t.Logf("Recipe Notification Email OnSuccess to: %v", recipe.Notification.Email.OnSuccess.To)
	} else {
		t.Log("Recipe Notification Email OnSuccess is nil!")
	}

	runner := engine.NewRecipeRunner(engine.RunnerOptions{
		Config: cfg,
		ExecRegistry: &testRegistry{Dialer: DialerFunc(func(user, targetHost string, port int, keyFile string) (engine.HostClient, error) {
			return &fakeHostClient{}, nil
		})},
	})

	req := engine.RunRequest{
		Recipe:           recipe,
		RecipeSourcePath: recipePath,
		RecipeDir:        filepath.Dir(recipePath),
		Records:          []hosts.Record{{Name: "localhost", PrimaryIP: "127.0.0.1", Provider: "local"}},
	}

	err = runner.ExecuteAndWait(ctx, req)

	// Add an ugly hack to see the results from RunReporter (since we don't have access to the unexported results).
	// Actually we could just change fakeHostClient or look at the error.
	t.Logf("ExecuteAndWait err=%v", err)
	if err != nil {
		t.Fatalf("expected recipe to succeed, got %v", err)
	}

	// Poll Mailpit's API to verify the email arrived.
	baseURL := "http://" + host + ":" + httpPort.Port()
	t.Logf("SMTP Host: %s, Port: %d, API: %s", host, portInt, baseURL)
	deadline := time.Now().Add(5 * time.Second)
	var foundEmail bool

	for time.Now().Before(deadline) {
		if body, ok := fetchMailpitBody(t, baseURL); ok {
			t.Logf("Got email body: %s", body)
			if strings.Contains(body, "test-email-notifications") && strings.Contains(body, "SUCCEEDED") {
				foundEmail = true
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !foundEmail {
		t.Fatal("timed out waiting for correct email notification to be sent and received by Mailpit")
	}
}

// fetchMailpitBody lists Mailpit's most recent message and fetches its full
// plain-text body. Mailpit's list endpoint only exposes a truncated Snippet,
// so the full Text body is fetched from the per-message endpoint instead.
func fetchMailpitBody(t *testing.T, baseURL string) (string, bool) {
	t.Helper()

	listResp, err := http.Get(baseURL + "/api/v1/messages")
	if err != nil || listResp.StatusCode != http.StatusOK {
		if listResp != nil {
			listResp.Body.Close()
		}
		return "", false
	}
	listBody, _ := io.ReadAll(listResp.Body)
	listResp.Body.Close()

	var list struct {
		Count    int `json:"count"`
		Messages []struct {
			ID string `json:"ID"`
		} `json:"messages"`
	}
	if json.Unmarshal(listBody, &list) != nil || list.Count == 0 {
		return "", false
	}

	msgResp, err := http.Get(baseURL + "/api/v1/message/" + list.Messages[0].ID)
	if err != nil || msgResp.StatusCode != http.StatusOK {
		if msgResp != nil {
			msgResp.Body.Close()
		}
		return "", false
	}
	msgBody, _ := io.ReadAll(msgResp.Body)
	msgResp.Body.Close()

	var msg struct {
		Text string `json:"Text"`
	}
	if json.Unmarshal(msgBody, &msg) != nil {
		return "", false
	}
	return msg.Text, true
}

func TestEmailNotificationFailureE2E(t *testing.T) {
	ctx := context.Background()

	// Spin up Mailpit to catch SMTP traffic and verify it via HTTP API (see
	// TestEmailNotificationE2E for why mailhog/mailhog was replaced).
	reqContainer := testcontainers.ContainerRequest{
		Image:        "axllent/mailpit:latest",
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor:   wait.ForHTTP("/").WithPort("8025/tcp"),
	}
	mailpitC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: reqContainer,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start mailpit: %v", err)
	}
	defer mailpitC.Terminate(ctx)

	host, err := mailpitC.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get mailpit host: %v", err)
	}
	smtpPort, err := mailpitC.MappedPort(ctx, "1025")
	if err != nil {
		t.Fatalf("failed to get mailpit smtp port: %v", err)
	}
	httpPort, err := mailpitC.MappedPort(ctx, "8025")
	if err != nil {
		t.Fatalf("failed to get mailpit http port: %v", err)
	}

	tmpDir := t.TempDir()
	recipePath := filepath.Join(tmpDir, "test_email_fail.cue")
	cueContent := `
recipe: {
	name: "test-email-failure-notifications"
	notification: {
		email: {
			send_on: {
				failure: true
			}
			on_failure: {
				from: "bot@honey.local"
				to: ["oncall@honey.local"]
				prefix: "[HONEY-FAIL]"
				attach_logs: true
			}
		}
	}
	steps: [
		{
			host: "localhost"
			command: "echo test-email-error-output"
		}
	]
}
`
	if err := os.WriteFile(recipePath, []byte(cueContent), 0o600); err != nil {
		t.Fatal(err)
	}

	portInt, _ := strconv.Atoi(smtpPort.Port())
	cfg := &config.File{
		Apps: map[string]apps.AppConfig{},
		SMTP: &config.SMTPConfig{
			Host: host,
			Port: portInt,
		},
	}

	raw, err := os.ReadFile(recipePath)
	if err != nil {
		t.Fatal(err)
	}

	recipe, err := cuetry.ParseRemoteRecipeOpts(raw, nil, cuetry.ParseOptions{})
	if err != nil {
		t.Fatal(err)
	}

	runner := engine.NewRecipeRunner(engine.RunnerOptions{
		Config: cfg,
		ExecRegistry: &testRegistry{Dialer: DialerFunc(func(user, targetHost string, port int, keyFile string) (engine.HostClient, error) {
			return &fakeFailingHostClient{}, nil
		})},
	})

	req := engine.RunRequest{
		Recipe:           recipe,
		RecipeSourcePath: recipePath,
		RecipeDir:        filepath.Dir(recipePath),
		Records:          []hosts.Record{{Name: "localhost", PrimaryIP: "127.0.0.1", Provider: "local"}},
	}

	_ = runner.ExecuteAndWait(ctx, req)

	// Poll Mailpit's API to verify the email arrived.
	baseURL := "http://" + host + ":" + httpPort.Port()
	deadline := time.Now().Add(5 * time.Second)
	var foundEmail bool

	for time.Now().Before(deadline) {
		if body, ok := fetchMailpitBody(t, baseURL); ok {
			t.Logf("Got failure email body: %s", body)
			if strings.Contains(body, "test-email-failure-notifications") && strings.Contains(body, "FAILED") && strings.Contains(body, "test-email-error-output") {
				foundEmail = true
				break
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !foundEmail {
		t.Fatal("timed out waiting for correct failure email notification to be sent and received by Mailpit")
	}
}
