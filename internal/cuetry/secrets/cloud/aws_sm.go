package cloud

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"

	"github.com/shareed2k/honey/internal/cuetry/secrets/ref"
)

// AWSSMBackend implements [ref.Backend] for aws-sm:id.
type AWSSMBackend struct{}

// NewAWSSM returns an AWS Secrets Manager backend.
func NewAWSSM() ref.Backend { return AWSSMBackend{} }

// Name implements [ref.Backend].
func (AWSSMBackend) Name() string { return "aws-sm" }

// Handles implements [ref.Backend].
func (AWSSMBackend) Handles(ref string) bool {
	return strings.HasPrefix(strings.TrimSpace(ref), "aws-sm:")
}

// Resolve implements [ref.Backend].
func (AWSSMBackend) Resolve(ctx context.Context, ref string) (string, error) {
	secretID := strings.TrimSpace(ref[len("aws-sm:"):])
	if secretID == "" {
		return "", fmt.Errorf("aws-sm: missing secret id")
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return "", err
	}
	svc := secretsmanager.NewFromConfig(cfg)
	out, err := svc.GetSecretValue(ctx, &secretsmanager.GetSecretValueInput{SecretId: aws.String(secretID)})
	if err != nil {
		return "", fmt.Errorf("aws-sm:%s: %w", secretID, err)
	}
	if out.SecretString != nil {
		return *out.SecretString, nil
	}
	if len(out.SecretBinary) > 0 {
		return string(out.SecretBinary), nil
	}
	return "", fmt.Errorf("aws-sm:%s: empty secret", secretID)
}
