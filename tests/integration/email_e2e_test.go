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

func TestEmailNotificationE2E(t *testing.T) {
	ctx := context.Background()

	// Spin up Mailhog to catch SMTP traffic and verify it via HTTP API
	reqContainer := testcontainers.ContainerRequest{
		Image:        "mailhog/mailhog:latest",
		ExposedPorts: []string{"1025/tcp", "8025/tcp"},
		WaitingFor:   wait.ForHTTP("/").WithPort("8025/tcp"),
	}
	mailhogC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: reqContainer,
		Started:          true,
	})
	if err != nil {
		t.Fatalf("failed to start mailhog: %v", err)
	}
	defer mailhogC.Terminate(ctx)

	host, err := mailhogC.Host(ctx)
	if err != nil {
		t.Fatalf("failed to get mailhog host: %v", err)
	}
	smtpPort, err := mailhogC.MappedPort(ctx, "1025")
	if err != nil {
		t.Fatalf("failed to get mailhog smtp port: %v", err)
	}
	httpPort, err := mailhogC.MappedPort(ctx, "8025")
	if err != nil {
		t.Fatalf("failed to get mailhog http port: %v", err)
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

	// Poll Mailhog API to verify the email arrived
	apiURL := "http://" + host + ":" + httpPort.Port() + "/api/v2/messages"
	t.Logf("SMTP Host: %s, Port: %d, API: %s", host, portInt, apiURL)
	deadline := time.Now().Add(5 * time.Second)
	var foundEmail bool

	for time.Now().Before(deadline) {
		resp, err := http.Get(apiURL)
		if err == nil && resp.StatusCode == http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()

			var result struct {
				Count int `json:"count"`
				Items []struct {
					Content struct {
						Body string `json:"Body"`
					} `json:"Content"`
				} `json:"items"`
			}

			if json.Unmarshal(body, &result) == nil && result.Count > 0 {
				emailBody := result.Items[0].Content.Body
				t.Logf("Got email body: %s", emailBody)
				if strings.Contains(emailBody, "test-email-notifications") && strings.Contains(emailBody, "SUCCEEDED") {
					foundEmail = true
					break
				}
			} else {
				t.Logf("Mailhog count: %d, body: %s", result.Count, string(body))
			}
		}
		time.Sleep(500 * time.Millisecond)
	}

	if !foundEmail {
		t.Fatal("timed out waiting for correct email notification to be sent and received by Mailhog")
	}
}
