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
// HONEY_INTEGRATION_TEST=1 HONEY_TEST_BUCKET=my-test-bucket AWS_REGION=us-east-1 AWS_ACCESS_KEY_ID=... AWS_SECRET_ACCESS_KEY=... go test -v ./internal/transferagent/presign -run TestS3Integration
//
// You can also test against MinIO:
// HONEY_INTEGRATION_TEST=1 HONEY_TEST_BUCKET=test HONEY_TEST_ENDPOINT=http://localhost:9000 AWS_REGION=us-east-1 AWS_ACCESS_KEY_ID=minioadmin AWS_SECRET_ACCESS_KEY=minioadmin go test -v ./internal/transferagent/presign -run TestS3Integration
func TestS3Integration(t *testing.T) {
	if os.Getenv("HONEY_INTEGRATION_TEST") == "" {
		t.Skip("Skipping integration test; set HONEY_INTEGRATION_TEST=1 to run")
	}

	bucket := os.Getenv("HONEY_TEST_BUCKET")
	if bucket == "" {
		t.Fatal("HONEY_TEST_BUCKET is required for integration test")
	}

	endpoint := os.Getenv("HONEY_TEST_ENDPOINT")
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = "us-east-1"
	}

	ctx := context.Background()
	cloud := Cloud{
		Provider: "s3",
		Bucket:   bucket,
		Object:   "honey-test-integration/test.txt",
		Region:   region,
		Endpoint: endpoint,
	}

	cfg := Config{
		PresignedMaxSizeBytes:   5 * GiB,
		MultipartThresholdBytes: 64 * MiB,
		URLTTL:                  5 * time.Minute,
	}

	// 1. Generate the Plan
	t.Logf("Generating plan against bucket %q (endpoint: %q, region: %q)", bucket, endpoint, region)
	plan, err := PlanTransfer(ctx, cloud, 13, cfg) // 13 bytes for "Hello, World!"
	if err != nil {
		t.Fatalf("PlanTransfer failed: %v", err)
	}

	// 2. Execute the PUT request via standard HTTP client
	if len(plan.UploadParts) != 1 {
		t.Fatalf("Expected 1 upload part, got %d", len(plan.UploadParts))
	}
	putURL := plan.UploadParts[0]
	t.Logf("Executing PUT to %s", putURL.URL)

	req, err := http.NewRequest(putURL.Method, putURL.URL, strings.NewReader("Hello, World!"))
	if err != nil {
		t.Fatalf("Failed to create request: %v", err)
	}
	for k, v := range putURL.Headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Content-Length", "13")

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
	if string(body) != "Hello, World!" {
		t.Fatalf("GET body mismatch, got: %q, want %q", string(body), "Hello, World!")
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
