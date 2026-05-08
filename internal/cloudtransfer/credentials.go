// Package cloudtransfer resolves short-lived cloud credentials for staging (S3 / GCS).
package cloudtransfer

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"github.com/aws/aws-sdk-go-v2/config"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/compute/v1"
)

// CloudBackend identifies a cloud object location for transfers.
type CloudBackend struct {
	Provider string
	Bucket   string
	Prefix   string
	Object   string
	Region   string
	Endpoint string
}

// SigningHints optionally scopes AWS/GCP credential loading (profiles, regions, project).
type SigningHints struct {
	AWSProfile string
	AWSRegion  string
	GCPProject string
}

// CredentialMaterial is returned for minting JWE envelopes on remote agents.
type CredentialMaterial struct {
	Provider  string
	Env       map[string]string
	ExpiresAt time.Time
}

// NormalizeProvider maps common aliases to canonical provider ids used by the transfer agent.
func NormalizeProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "gcs":
		return "googlecloudstorage"
	default:
		return strings.ToLower(strings.TrimSpace(provider))
	}
}

// ResolveGCPProjectHint returns an explicit hint or the best-effort default project id.
func ResolveGCPProjectHint(ctx context.Context, hint string) string {
	if p := strings.TrimSpace(hint); p != "" {
		return p
	}
	for _, k := range []string{"GOOGLE_CLOUD_PROJECT", "GCLOUD_PROJECT", "CLOUDSDK_CORE_PROJECT"} {
		if p := strings.TrimSpace(os.Getenv(k)); p != "" {
			return p
		}
	}
	creds, err := google.FindDefaultCredentials(ctx, compute.CloudPlatformScope)
	if err == nil && strings.TrimSpace(creds.ProjectID) != "" {
		return strings.TrimSpace(creds.ProjectID)
	}
	return ""
}

// ResolveCredentialMaterial loads cloud SDK credentials into a flat env map for the transfer agent.
func ResolveCredentialMaterial(ctx context.Context, cloud CloudBackend, hints SigningHints) (CredentialMaterial, error) {
	provider := NormalizeProvider(cloud.Provider)
	switch provider {
	case "s3":
		region := strings.TrimSpace(cloud.Region)
		if region == "" {
			region = strings.TrimSpace(hints.AWSRegion)
		}
		if region == "" {
			region = "us-east-1"
		}
		loadOpts := []func(*config.LoadOptions) error{
			config.WithRegion(region),
		}
		if profile := strings.TrimSpace(hints.AWSProfile); profile != "" {
			loadOpts = append(loadOpts, config.WithSharedConfigProfile(profile))
		}
		cfg, err := config.LoadDefaultConfig(ctx, loadOpts...)
		if err != nil {
			return CredentialMaterial{}, err
		}
		creds, err := cfg.Credentials.Retrieve(ctx)
		if err != nil {
			return CredentialMaterial{}, err
		}
		env := map[string]string{
			"AWS_ACCESS_KEY_ID":     strings.TrimSpace(creds.AccessKeyID),
			"AWS_SECRET_ACCESS_KEY": strings.TrimSpace(creds.SecretAccessKey),
			"AWS_SESSION_TOKEN":     strings.TrimSpace(creds.SessionToken),
			"AWS_REGION":            region,
		}
		return CredentialMaterial{
			Provider:  provider,
			Env:       env,
			ExpiresAt: creds.Expires,
		}, nil
	case "googlecloudstorage":
		tokenSource, err := google.DefaultTokenSource(ctx, storage.ScopeReadWrite)
		if err != nil {
			return CredentialMaterial{}, err
		}
		tok, err := tokenSource.Token()
		if err != nil {
			return CredentialMaterial{}, err
		}
		env := map[string]string{
			"GOOGLE_OAUTH_ACCESS_TOKEN": strings.TrimSpace(tok.AccessToken),
		}
		project := strings.TrimSpace(ResolveGCPProjectHint(ctx, hints.GCPProject))
		if project != "" {
			env["GOOGLE_CLOUD_PROJECT"] = project
		}
		return CredentialMaterial{
			Provider:  provider,
			Env:       env,
			ExpiresAt: tok.Expiry,
		}, nil
	default:
		return CredentialMaterial{}, fmt.Errorf("unsupported cloud provider %q for credential resolution", cloud.Provider)
	}
}
