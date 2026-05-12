package presign

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func loadTestSigningKey(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "test-signing-key.pem"))
	if err != nil {
		t.Fatalf("read test signing key: %v", err)
	}
	return b
}

func TestPlanGCS_singlePut(t *testing.T) {
	cfg := Config{
		PresignedMaxSizeBytes:   5 * GiB,
		MultipartThresholdBytes: 64 * MiB,
		URLTTL:                  time.Hour,
	}
	cloud := Cloud{Provider: "gcs", Bucket: "test-bucket", Object: "honey-transfer/abc.bin"}
	plan, err := planGCSWithSigner(context.Background(), cloud, 10*MiB, cfg, gcsSigner{
		Email: "test@iam.gserviceaccount.com",
		PEM:   loadTestSigningKey(t),
	})
	if err != nil {
		t.Fatalf("planGCS: %v", err)
	}
	if len(plan.UploadParts) != 1 {
		t.Fatalf("GCS plan should always have 1 upload URL, got %d", len(plan.UploadParts))
	}
	if !strings.Contains(plan.UploadParts[0].URL, "test-bucket") {
		t.Fatalf("URL missing bucket: %s", plan.UploadParts[0].URL)
	}
	if !strings.Contains(plan.UploadParts[0].URL, "X-Goog-Signature") {
		t.Fatalf("URL missing X-Goog-Signature: %s", plan.UploadParts[0].URL)
	}
	if plan.Download.Method != "GET" {
		t.Fatalf("download method = %q", plan.Download.Method)
	}
	if !strings.Contains(plan.Download.URL, "X-Goog-Signature") {
		t.Fatalf("download URL missing X-Goog-Signature: %s", plan.Download.URL)
	}
	if plan.Complete != nil {
		t.Fatalf("GCS plan should not set Complete")
	}
}

func TestPlanGCS_largeFile_singleURL(t *testing.T) {
	// GCS resumable uploads still produce a single URL; the remote streams to
	// it in chunks. No multipart breakdown by the operator.
	cfg := Config{
		PresignedMaxSizeBytes:   100 * GiB,
		MultipartThresholdBytes: 64 * MiB,
		URLTTL:                  time.Hour,
	}
	cloud := Cloud{Provider: "gcs", Bucket: "test-bucket", Object: "honey-transfer/big.bin"}
	plan, err := planGCSWithSigner(context.Background(), cloud, 200*MiB, cfg, gcsSigner{
		Email: "test@iam.gserviceaccount.com",
		PEM:   loadTestSigningKey(t),
	})
	if err != nil {
		t.Fatalf("planGCS: %v", err)
	}
	if len(plan.UploadParts) != 1 {
		t.Fatalf("GCS large file should still use 1 URL (resumable), got %d", len(plan.UploadParts))
	}
}
