package presign

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"cloud.google.com/go/storage"
	"go.uber.org/zap"
	"golang.org/x/oauth2/google"
)

// gcsSigner holds the credentials the GCS V4 signing flow needs.
// Email = the service-account email; PEM = the service account's RSA private key.
type gcsSigner struct {
	Email string
	PEM   []byte
}

// planGCSImpl wraps planGCSWithSigner with default-ADC credential resolution.
// For v1 we only support service-account-key creds (GOOGLE_APPLICATION_CREDENTIALS
// pointing at a JSON key file). Workload-identity / GCE metadata creds are an
// explicit follow-up — they require IAM-based signing.
func planGCSImpl(ctx context.Context, cloud Cloud, fileSize int64, cfg Config) (*Plan, error) {
	signer, err := defaultGCSSigner(ctx)
	if err != nil {
		return nil, err
	}
	return planGCSWithSigner(ctx, cloud, fileSize, cfg, signer)
}

// planGCSWithSigner returns a Plan for transferring through a GCS staging
// object using the given V4 signer. The ctx parameter is currently unused
// (storage.SignedURL is synchronous and takes no context) but kept to match
// the shape of planS3WithClient and so the cleanup closure can pick it up.
func planGCSWithSigner(_ context.Context, cloud Cloud, fileSize int64, cfg Config, signer gcsSigner) (*Plan, error) {
	expiresAt := time.Now().Add(cfg.URLTTL)

	put, err := storage.SignedURL(cloud.Bucket, cloud.Object, &storage.SignedURLOptions{
		GoogleAccessID: signer.Email,
		PrivateKey:     signer.PEM,
		Method:         "PUT",
		Expires:        expiresAt,
		Scheme:         storage.SigningSchemeV4,
	})
	if err != nil {
		return nil, fmt.Errorf("presign gcs put: %w", err)
	}
	zap.L().Debug("presign: generated GCS PUT URL")
	get, err := storage.SignedURL(cloud.Bucket, cloud.Object, &storage.SignedURLOptions{
		GoogleAccessID: signer.Email,
		PrivateKey:     signer.PEM,
		Method:         "GET",
		Expires:        expiresAt,
		Scheme:         storage.SigningSchemeV4,
	})
	if err != nil {
		return nil, fmt.Errorf("presign gcs get: %w", err)
	}
	zap.L().Debug("presign: generated GCS GET URL")
	cleanup := func(ctx context.Context) error {
		zap.L().Debug("presign: cleanup deleting GCS object", zap.String("bucket", cloud.Bucket), zap.String("object", cloud.Object))
		cli, err := storage.NewClient(ctx)
		if err != nil {
			return fmt.Errorf("gcs client: %w", err)
		}
		defer cli.Close()
		return cli.Bucket(cloud.Bucket).Object(cloud.Object).Delete(ctx)
	}
	return &Plan{
		Provider:    "gcs",
		UploadParts: []SignedURL{{Method: "PUT", URL: put}},
		Download:    SignedURL{Method: "GET", URL: get},
		Complete:    nil,
		Cleanup:     cleanup,
		PartSize:    fileSize,
		ExpiresAt:   expiresAt,
	}, nil
}

// defaultGCSSigner returns an error in v1 — the caller-side `chooseTransport`
// will treat this as a signal to fall back to the agent path.
//
// A real impl reads google.FindDefaultCredentials(ctx, storage.ScopeReadWrite)
// and extracts the service-account email + private_key field from the JSON.
// That's a stretch; the simpler v1 path is: if GOOGLE_APPLICATION_CREDENTIALS
// points at a service-account-key JSON file, read it and extract; otherwise
// return an error and fall back.
func defaultGCSSigner(ctx context.Context) (gcsSigner, error) {
	creds, err := google.FindDefaultCredentials(ctx, storage.ScopeReadWrite)
	if err != nil {
		return gcsSigner{}, fmt.Errorf("failed to find default Google credentials: %w", err)
	}

	if creds.JSON == nil {
		return gcsSigner{}, fmt.Errorf("default Google credentials do not contain JSON key data")
	}

	var sa struct {
		Type        string `json:"type"`
		ClientEmail string `json:"client_email"`
		PrivateKey  string `json:"private_key"`
	}
	if err := json.Unmarshal(creds.JSON, &sa); err != nil {
		return gcsSigner{}, fmt.Errorf("failed to parse Google credentials JSON: %w", err)
	}

	if sa.Type != "service_account" {
		return gcsSigner{}, fmt.Errorf("google credentials JSON is not of type 'service_account', got: %q. Presigned URLs require a service account", sa.Type)
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return gcsSigner{}, fmt.Errorf("google credentials JSON missing 'client_email' or 'private_key'")
	}

	return gcsSigner{
		Email: sa.ClientEmail,
		PEM:   []byte(sa.PrivateKey),
	}, nil
}
