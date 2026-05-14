package presign

import (
	"context"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"
)

// To run this test:
// HONEY_INTEGRATION_TEST=1 HONEY_TEST_GCS_BUCKET=my-gcs-bucket GOOGLE_APPLICATION_CREDENTIALS=/path/to/sa.json go test -v ./internal/transferagent/presign -run TestGCSIntegration
func TestGCSIntegration(t *testing.T) {
	if os.Getenv("HONEY_INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test; set HONEY_INTEGRATION_TEST=1 to run")
	}

	bucket := os.Getenv("HONEY_TEST_GCS_BUCKET")
	if bucket == "" {
		t.Skip("HONEY_TEST_GCS_BUCKET not set, skipping GCS integration test")
	}

	if os.Getenv("GOOGLE_APPLICATION_CREDENTIALS") == "" {
		t.Fatal("GOOGLE_APPLICATION_CREDENTIALS must be set for GCS integration test")
	}

	ctx := context.Background()
	cloud := Cloud{
		Provider: "googlecloudstorage", // We support both "gcs" and "googlecloudstorage"
		Bucket:   bucket,
		Object:   "honey-test-integration/test-gcs.txt",
	}

	cfg := Config{
		PresignedMaxSizeBytes:   5 * GiB,
		MultipartThresholdBytes: 64 * MiB,
		URLTTL:                  5 * time.Minute,
	}

	// 1. Generate the Plan
	t.Logf("Generating GCS plan against bucket %q", bucket)
	plan, err := PlanTransfer(ctx, cloud, 17, cfg) // 17 bytes for "Hello, World GCS!"
	if err != nil {
		t.Fatalf("PlanTransfer failed: %v", err)
	}

	if plan.Provider != "gcs" {
		t.Fatalf("Expected plan provider to be 'gcs', got %q", plan.Provider)
	}

	// 2. Execute the PUT request via standard HTTP client
	if len(plan.UploadParts) != 1 {
		t.Fatalf("Expected 1 upload part, got %d", len(plan.UploadParts))
	}
	putURL := plan.UploadParts[0]
	t.Logf("Executing PUT to %s", putURL.URL)

	req, err := http.NewRequest(putURL.Method, putURL.URL, strings.NewReader("Hello, World GCS!"))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	for k, v := range putURL.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Length", "17")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("PUT request failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("PUT failed with status %d: %s", resp.StatusCode, string(b))
	}
	t.Log("PUT request succeeded")

	// 3. Execute the GET request via standard HTTP client
	getURL := plan.Download
	t.Logf("Executing GET to %s", getURL.URL)

	getReq, err := http.NewRequest(getURL.Method, getURL.URL, nil)
	if err != nil {
		t.Fatalf("Failed to create GET request: %v", err)
	}
	for k, v := range getURL.Headers {
		getReq.Header.Set(k, v)
	}

	getResp, err := http.DefaultClient.Do(getReq)
	if err != nil {
		t.Fatalf("GET request failed: %v", err)
	}
	defer getResp.Body.Close()

	if getResp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(getResp.Body)
		t.Fatalf("GET failed with status %d: %s", getResp.StatusCode, string(b))
	}

	body, _ := io.ReadAll(getResp.Body)
	if string(body) != "Hello, World GCS!" {
		t.Fatalf("GET body mismatch, got: %q, want %q", string(body), "Hello, World GCS!")
	}
	t.Log("GET request succeeded and content matched")

	// 4. Test the cleanup function
	if plan.Cleanup != nil {
		t.Log("Executing Cleanup")
		if err := plan.Cleanup(ctx); err != nil {
			t.Fatalf("Cleanup failed: %v", err)
		}
		t.Log("Cleanup succeeded")
	} else {
		t.Fatal("Expected Cleanup function but got nil")
	}

	// Verify object is actually deleted
	verifyReq, _ := http.NewRequest(getURL.Method, getURL.URL, nil)
	verifyResp, err := http.DefaultClient.Do(verifyReq)
	if err == nil {
		defer verifyResp.Body.Close()
		if verifyResp.StatusCode != http.StatusNotFound && verifyResp.StatusCode != http.StatusForbidden {
			t.Fatalf("Object should be deleted/inaccessible, but got status %d", verifyResp.StatusCode)
		}
	}
}
