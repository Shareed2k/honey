package presign

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

const (
	MiB int64 = 1 << 20
	GiB int64 = 1 << 30
)

// newTestS3Client returns an *s3.Client pointed at a local fake server that
// responds to CreateMultipartUpload with a stub InitiateMultipartUploadResult.
// PresignPutObject / PresignUploadPart / PresignGetObject don't make network
// calls so they "just work" without server-side handling.
func newTestS3Client(t *testing.T) *s3.Client {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := r.URL.Query()["uploads"]; ok {
			w.Header().Set("Content-Type", "application/xml")
			_, _ = w.Write([]byte(`<?xml version="1.0" encoding="UTF-8"?>
<InitiateMultipartUploadResult><Bucket>test-bucket</Bucket><Key>honey-transfer/big.bin</Key><UploadId>fake-upload-id</UploadId></InitiateMultipartUploadResult>`))
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	cfg, _ := awsconfig.LoadDefaultConfig(context.Background(),
		awsconfig.WithRegion("us-east-1"),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider("AKIA", "secret", "")),
	)
	return s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.UsePathStyle = true
		o.BaseEndpoint = aws.String(srv.URL)
	})
}

func TestPlanS3_singlePut(t *testing.T) {
	cfg := Config{
		PresignedMaxSizeBytes:   5 * GiB,
		MultipartThresholdBytes: 64 * MiB,
		URLTTL:                  time.Hour,
	}
	cloud := Cloud{Provider: "s3", Bucket: "test-bucket", Object: "honey-transfer/abc.bin", Region: "us-east-1"}
	plan, err := planS3WithClient(context.Background(), newTestS3Client(t), cloud, 10*MiB, cfg)
	if err != nil {
		t.Fatalf("planS3: %v", err)
	}
	if len(plan.UploadParts) != 1 {
		t.Fatalf("single-PUT should produce 1 part, got %d", len(plan.UploadParts))
	}
	if plan.UploadParts[0].Method != "PUT" {
		t.Fatalf("upload method = %q", plan.UploadParts[0].Method)
	}
	if !strings.Contains(plan.UploadParts[0].URL, "test-bucket") {
		t.Fatalf("URL missing bucket: %s", plan.UploadParts[0].URL)
	}
	if !strings.Contains(plan.UploadParts[0].URL, "abc.bin") {
		t.Fatalf("URL missing object: %s", plan.UploadParts[0].URL)
	}
	if plan.Download.Method != "GET" {
		t.Fatalf("download method = %q", plan.Download.Method)
	}
	if plan.Complete != nil {
		t.Fatalf("single PUT should not set Complete")
	}
	if plan.PartSize != 10*MiB {
		t.Fatalf("PartSize = %d, want %d", plan.PartSize, 10*MiB)
	}
}

func TestPlanS3_multipart(t *testing.T) {
	cfg := Config{
		PresignedMaxSizeBytes:   100 * GiB,
		MultipartThresholdBytes: 64 * MiB,
		URLTTL:                  time.Hour,
	}
	cloud := Cloud{Provider: "s3", Bucket: "test-bucket", Object: "honey-transfer/big.bin", Region: "us-east-1"}
	const size = 200 * MiB
	plan, err := planS3WithClient(context.Background(), newTestS3Client(t), cloud, size, cfg)
	if err != nil {
		t.Fatalf("planS3: %v", err)
	}
	if plan.Complete == nil {
		t.Fatalf("multipart plan should set Complete")
	}
	if plan.Complete.UploadID == "" {
		t.Fatalf("UploadID missing")
	}
	wantParts := int((size + 64*MiB - 1) / (64 * MiB))
	if len(plan.UploadParts) != wantParts {
		t.Fatalf("got %d parts, want %d", len(plan.UploadParts), wantParts)
	}
	for i, p := range plan.UploadParts {
		if p.Method != "PUT" {
			t.Fatalf("part %d method = %q", i, p.Method)
		}
		if !strings.Contains(p.URL, "uploadId=") {
			t.Fatalf("part %d URL missing uploadId: %s", i, p.URL)
		}
		if !strings.Contains(p.URL, "partNumber=") {
			t.Fatalf("part %d URL missing partNumber: %s", i, p.URL)
		}
	}
}
