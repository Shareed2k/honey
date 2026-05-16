package cloud

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
)

// AWSKMSBackend implements [ref.Backend] for aws-kms:<base64 ciphertext>.
type AWSKMSBackend struct{}

// NewAWSKMS returns an AWS KMS decrypt backend.
func NewAWSKMS() ref.Backend { return AWSKMSBackend{} }

// Name implements [ref.Backend].
func (AWSKMSBackend) Name() string { return "aws-kms" }

// Handles implements [ref.Backend].
func (AWSKMSBackend) Handles(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), "aws-kms:")
}

// Resolve implements [ref.Backend].
func (AWSKMSBackend) Resolve(ctx context.Context, ref string) (string, error) {
	b64 := strings.TrimSpace(ref[len("aws-kms:"):])
	if b64 == "" {
		return "", fmt.Errorf("aws-kms: missing ciphertext")
	}
	blob, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return "", fmt.Errorf("aws-kms: base64: %w", err)
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return "", err
	}
	svc := kms.NewFromConfig(cfg)
	out, err := svc.Decrypt(ctx, &kms.DecryptInput{CiphertextBlob: blob})
	if err != nil {
		return "", fmt.Errorf("aws-kms decrypt: %w", err)
	}
	return string(out.Plaintext), nil
}
