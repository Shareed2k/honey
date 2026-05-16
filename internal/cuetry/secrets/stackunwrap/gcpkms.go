package stackunwrap

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"google.golang.org/api/cloudkms/v1"
)

// GCPKMS unwraps a data key via gcpkms://projects/…/cryptoKeys/….
type GCPKMS struct{}

// Name implements [DataKeyUnwrapper].
func (GCPKMS) Name() string { return "gcpkms" }

// Supports implements [DataKeyUnwrapper].
func (GCPKMS) Supports(providerURL string) bool {
	return strings.HasPrefix(strings.TrimSpace(providerURL), "gcpkms://")
}

func (GCPKMS) Unwrap(ctx context.Context, providerURL, encryptedKeyB64 string) ([]byte, error) {
	rest := strings.TrimPrefix(strings.TrimSpace(providerURL), "gcpkms://")
	name, err := parseGCPKMSResource(rest)
	if err != nil {
		return nil, err
	}
	svc, err := cloudkms.NewService(ctx)
	if err != nil {
		return nil, fmt.Errorf("gcpkms client: %w", err)
	}
	req := &cloudkms.DecryptRequest{Ciphertext: strings.TrimSpace(encryptedKeyB64)}
	resp, err := svc.Projects.Locations.KeyRings.CryptoKeys.Decrypt(name, req).Context(ctx).Do()
	if err != nil {
		return nil, fmt.Errorf("gcpkms decrypt encryptedkey: %w", err)
	}
	out, err := base64.StdEncoding.DecodeString(resp.Plaintext)
	if err != nil {
		return nil, fmt.Errorf("gcpkms plaintext base64: %w", err)
	}
	return out, nil
}

func parseGCPKMSResource(rest string) (string, error) {
	rest = strings.TrimSpace(rest)
	if rest == "" {
		return "", fmt.Errorf("gcpkms: empty resource")
	}
	if !strings.HasPrefix(rest, "projects/") {
		return "", fmt.Errorf("gcpkms: resource must start with projects/…, got %q", rest)
	}
	return rest, nil
}
