package stackunwrap

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kms"
)

// AWSKMS unwraps a data key via awskms:// (encryptedkey is base64 ciphertext blob).
type AWSKMS struct{}

// Name implements [DataKeyUnwrapper].
func (AWSKMS) Name() string { return "awskms" }

// Supports implements [DataKeyUnwrapper].
func (AWSKMS) Supports(providerURL string) bool {
	return strings.HasPrefix(strings.TrimSpace(providerURL), "awskms://")
}

func (AWSKMS) Unwrap(ctx context.Context, _ string, encryptedKeyB64 string) ([]byte, error) {
	blob, err := base64.StdEncoding.DecodeString(strings.TrimSpace(encryptedKeyB64))
	if err != nil {
		return nil, fmt.Errorf("awskms encryptedkey base64: %w", err)
	}
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, err
	}
	svc := kms.NewFromConfig(cfg)
	out, err := svc.Decrypt(ctx, &kms.DecryptInput{CiphertextBlob: blob})
	if err != nil {
		return nil, fmt.Errorf("awskms decrypt encryptedkey: %w", err)
	}
	return out.Plaintext, nil
}
