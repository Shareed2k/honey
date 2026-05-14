// Package presign generates short-lived presigned URLs for transferring files
// to/from S3 or GCS without requiring an agent binary on the remote host.
package presign

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// SignedURL describes one HTTP request the remote will make against the cloud.
type SignedURL struct {
	Method  string // "PUT" | "GET"
	URL     string
	Headers map[string]string // signed headers the client must echo back
}

// S3MultipartCommit carries the bookkeeping the operator needs to finalize an
// S3 multipart upload after the remote-side curl loop reports per-part ETags.
type S3MultipartCommit struct {
	UploadID string
	PartTags []string // length == len(UploadParts); ETag per part filled in by the caller
}

// Plan is the operator-side output of PlanTransfer: everything needed to drive
// the curl path on both source and destination ends.
type Plan struct {
	Provider    string                          // "s3" | "gcs"
	UploadParts []SignedURL                     // 1 entry for single PUT, N for multipart
	Download    SignedURL                       // GET for the staging object
	Complete    *S3MultipartCommit              // nil unless multipart S3
	Cleanup     func(ctx context.Context) error // operator-side DELETE
	PartSize    int64                           // 0 for single-PUT, else read size per part
	ExpiresAt   time.Time
}

// Cloud names a staging object on a specific provider/bucket.
type Cloud struct {
	Provider string
	Bucket   string
	Object   string
	Region   string
	Endpoint string // optional override (MinIO etc.)
}

// Config carries the operator-side limits that decide single vs multipart.
type Config struct {
	PresignedMaxSizeBytes   int64
	MultipartThresholdBytes int64
	URLTTL                  time.Duration
}

// partLayout describes how an upload is split into N parts of size PartSize.
type partLayout struct {
	Single    bool // true iff PartCount == 1 and we use a single PUT (not multipart)
	PartCount int
	PartSize  int64
}

const (
	s3MaxPartCount = 10000
	s3MaxPartSize  = 5 << 30 // 5 GiB
)

// choosePartLayout picks part count and per-part size for an upload.
// Single-PUT iff size <= threshold; otherwise multipart with ceil(size/threshold)
// parts, growing PartSize when needed to stay under s3MaxPartCount.
func choosePartLayout(size, threshold int64) partLayout {
	if threshold <= 0 {
		threshold = 64 << 20
	}
	if size <= threshold {
		layout := partLayout{Single: true, PartCount: 1, PartSize: size}
		zap.L().Debug("presign: calculated layout",
			zap.Bool("single", layout.Single),
			zap.Int("part_count", layout.PartCount),
			zap.Int64("part_size", layout.PartSize),
			zap.Int64("file_size", size),
			zap.Int64("threshold", threshold),
		)
		return layout
	}
	partSize := threshold
	count := (size + partSize - 1) / partSize
	if count > s3MaxPartCount {
		partSize = (size + s3MaxPartCount - 1) / s3MaxPartCount
		if partSize > s3MaxPartSize {
			partSize = s3MaxPartSize
		}
		count = (size + partSize - 1) / partSize
	}
	layout := partLayout{Single: false, PartCount: int(count), PartSize: partSize}
	zap.L().Debug("presign: calculated layout",
		zap.Bool("single", layout.Single),
		zap.Int("part_count", layout.PartCount),
		zap.Int64("part_size", layout.PartSize),
		zap.Int64("file_size", size),
		zap.Int64("threshold", threshold),
	)
	return layout
}

// PlanTransfer produces a Plan for moving a file of the given size through the
// given staging object. The Plan is the only thing callers need to drive the
// curl path on both ends.
//
// Returns an error if cloud.Provider is unknown or the underlying SDK can't
// authenticate / sign URLs for the bucket.
func PlanTransfer(ctx context.Context, cloud Cloud, fileSize int64, cfg Config) (*Plan, error) {
	if fileSize < 0 {
		return nil, fmt.Errorf("presign: negative file size")
	}
	if cfg.URLTTL <= 0 {
		cfg.URLTTL = time.Hour
	}
	zap.L().Debug("presign: planning transfer",
		zap.String("provider", cloud.Provider),
		zap.String("bucket", cloud.Bucket),
		zap.String("object", cloud.Object),
		zap.Int64("file_size", fileSize),
		zap.Int64("max_size", cfg.PresignedMaxSizeBytes),
		zap.Int64("multipart_threshold", cfg.MultipartThresholdBytes),
		zap.Duration("ttl", cfg.URLTTL),
	)
	switch cloud.Provider {
	case "s3":
		return planS3Impl(ctx, cloud, fileSize, cfg)
	case "gcs", "googlecloudstorage":
		return planGCSImpl(ctx, cloud, fileSize, cfg)
	default:
		return nil, fmt.Errorf("presign: unsupported provider %q", cloud.Provider)
	}
}
