package presign

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"go.uber.org/zap"
)

// DefaultS3Client builds an *s3.Client from the ambient environment / IAM role.
// Exported so callers in other packages (operator-side multipart complete/abort)
// can reuse the same credential resolution.
func DefaultS3Client(ctx context.Context, cloud Cloud) (*s3.Client, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx, awsconfig.WithRegion(cloud.Region))
	if err != nil {
		return nil, fmt.Errorf("s3 config: %w", err)
	}
	opts := []func(*s3.Options){}
	if cloud.Endpoint != "" {
		opts = append(opts, func(o *s3.Options) {
			o.BaseEndpoint = aws.String(cloud.Endpoint)
			o.UsePathStyle = true
		})
	}
	return s3.NewFromConfig(cfg, opts...), nil
}

// planS3Impl wraps planS3WithClient with the default-config client.
func planS3Impl(ctx context.Context, cloud Cloud, fileSize int64, cfg Config) (*Plan, error) {
	cli, err := DefaultS3Client(ctx, cloud)
	if err != nil {
		return nil, err
	}
	return planS3WithClient(ctx, cli, cloud, fileSize, cfg)
}

func planS3WithClient(ctx context.Context, cli *s3.Client, cloud Cloud, fileSize int64, cfg Config) (*Plan, error) {
	layout := choosePartLayout(fileSize, cfg.MultipartThresholdBytes)
	presigner := s3.NewPresignClient(cli)
	expiresAt := time.Now().Add(cfg.URLTTL)
	expireOpt := s3.WithPresignExpires(cfg.URLTTL)

	// GET URL is identical for single-PUT and multipart.
	dlReq, err := presigner.PresignGetObject(ctx, &s3.GetObjectInput{
		Bucket: aws.String(cloud.Bucket),
		Key:    aws.String(cloud.Object),
	}, expireOpt)
	if err != nil {
		return nil, fmt.Errorf("presign get: %w", err)
	}
	dl := SignedURL{Method: "GET", URL: dlReq.URL, Headers: copyHeaderMap(dlReq.SignedHeader)}
	zap.L().Debug("presign: generated S3 GET URL")

	if layout.Single {
		putReq, err := presigner.PresignPutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(cloud.Bucket),
			Key:    aws.String(cloud.Object),
		}, expireOpt)
		if err != nil {
			return nil, fmt.Errorf("presign put: %w", err)
		}
		zap.L().Debug("presign: generated S3 single PUT URL")
		return &Plan{
			Provider:    "s3",
			UploadParts: []SignedURL{{Method: "PUT", URL: putReq.URL, Headers: copyHeaderMap(putReq.SignedHeader)}},
			Download:    dl,
			Complete:    nil,
			Cleanup:     deleteS3ObjectCleanup(cli, cloud),
			PartSize:    fileSize,
			ExpiresAt:   expiresAt,
		}, nil
	}

	// Multipart path: create upload, presign N parts, return a Complete handle.
	init, err := cli.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{
		Bucket: aws.String(cloud.Bucket),
		Key:    aws.String(cloud.Object),
	})
	if err != nil {
		return nil, fmt.Errorf("create multipart upload: %w", err)
	}
	zap.L().Debug("presign: created S3 multipart upload", zap.String("upload_id", aws.ToString(init.UploadId)), zap.Int("part_count", layout.PartCount))
	parts := make([]SignedURL, 0, layout.PartCount)
	for i := 0; i < layout.PartCount; i++ {
		partReq, err := presigner.PresignUploadPart(ctx, &s3.UploadPartInput{
			Bucket:     aws.String(cloud.Bucket),
			Key:        aws.String(cloud.Object),
			PartNumber: aws.Int32(int32(i + 1)),
			UploadId:   init.UploadId,
		}, expireOpt)
		if err != nil {
			_, _ = cli.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
				Bucket:   aws.String(cloud.Bucket),
				Key:      aws.String(cloud.Object),
				UploadId: init.UploadId,
			})
			return nil, fmt.Errorf("presign part %d: %w", i+1, err)
		}
		parts = append(parts, SignedURL{Method: "PUT", URL: partReq.URL, Headers: copyHeaderMap(partReq.SignedHeader)})
	}
	zap.L().Debug("presign: generated S3 multipart PUT URLs", zap.Int("part_count", len(parts)))
	return &Plan{
		Provider:    "s3",
		UploadParts: parts,
		Download:    dl,
		Complete:    &S3MultipartCommit{UploadID: aws.ToString(init.UploadId), PartTags: make([]string, layout.PartCount)},
		Cleanup:     deleteS3ObjectCleanup(cli, cloud),
		PartSize:    layout.PartSize,
		ExpiresAt:   expiresAt,
	}, nil
}

// CompleteS3Multipart finalizes a multipart upload using the per-part ETags
// the operator collected from the remote-side curl loop. If the call fails,
// the upload is aborted before the error is returned.
func CompleteS3Multipart(ctx context.Context, cli *s3.Client, cloud Cloud, commit *S3MultipartCommit) error {
	parts := make([]s3types.CompletedPart, 0, len(commit.PartTags))
	for i, etag := range commit.PartTags {
		if etag == "" {
			return fmt.Errorf("complete: missing ETag for part %d", i+1)
		}
		parts = append(parts, s3types.CompletedPart{
			PartNumber: aws.Int32(int32(i + 1)),
			ETag:       aws.String(etag),
		})
	}
	_, err := cli.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket:          aws.String(cloud.Bucket),
		Key:             aws.String(cloud.Object),
		UploadId:        aws.String(commit.UploadID),
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		_, _ = cli.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
			Bucket:   aws.String(cloud.Bucket),
			Key:      aws.String(cloud.Object),
			UploadId: aws.String(commit.UploadID),
		})
		zap.L().Debug("presign: failed to complete S3 multipart upload, aborted", zap.String("upload_id", commit.UploadID), zap.Error(err))
		return fmt.Errorf("complete multipart upload: %w", err)
	}
	zap.L().Debug("presign: completed S3 multipart upload", zap.String("upload_id", commit.UploadID), zap.Int("part_count", len(parts)))
	return nil
}

// AbortS3Multipart cancels a multipart upload (called on per-part failure).
func AbortS3Multipart(ctx context.Context, cli *s3.Client, cloud Cloud, uploadID string) error {
	zap.L().Debug("presign: aborting S3 multipart upload", zap.String("upload_id", uploadID))
	_, err := cli.AbortMultipartUpload(ctx, &s3.AbortMultipartUploadInput{
		Bucket:   aws.String(cloud.Bucket),
		Key:      aws.String(cloud.Object),
		UploadId: aws.String(uploadID),
	})
	return err
}

func deleteS3ObjectCleanup(cli *s3.Client, cloud Cloud) func(context.Context) error {
	return func(ctx context.Context) error {
		zap.L().Debug("presign: cleanup deleting S3 object", zap.String("bucket", cloud.Bucket), zap.String("object", cloud.Object))
		_, err := cli.DeleteObject(ctx, &s3.DeleteObjectInput{
			Bucket: aws.String(cloud.Bucket),
			Key:    aws.String(cloud.Object),
		})
		return err
	}
}

// copyHeaderMap converts the SDK's []string-valued header map into the
// single-string-valued shape used by SignedURL.
func copyHeaderMap(h map[string][]string) map[string]string {
	if len(h) == 0 {
		return nil
	}
	out := make(map[string]string, len(h))
	for k, v := range h {
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}
