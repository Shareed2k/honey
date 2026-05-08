package webserver

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

	"github.com/shareed2k/honey/internal/ui"
)

type cloudSigningHints struct {
	AWSProfile string
	AWSRegion  string
	GCPProject string
}

type transferCredentialMaterial struct {
	Provider  string
	Env       map[string]string
	ExpiresAt time.Time
}

func resolveGCPProjectHint(ctx context.Context, hint string) string {
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

func (s *Server) resolveTransferCredentialMaterial(ctx context.Context, cloud ui.AgentCloudBackend, hints cloudSigningHints) (transferCredentialMaterial, error) {
	provider := normalizeTransferCloudProvider(cloud.Provider)
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
			return transferCredentialMaterial{}, err
		}
		creds, err := cfg.Credentials.Retrieve(ctx)
		if err != nil {
			return transferCredentialMaterial{}, err
		}
		env := map[string]string{
			"AWS_ACCESS_KEY_ID":     strings.TrimSpace(creds.AccessKeyID),
			"AWS_SECRET_ACCESS_KEY": strings.TrimSpace(creds.SecretAccessKey),
			"AWS_SESSION_TOKEN":     strings.TrimSpace(creds.SessionToken),
			"AWS_REGION":            region,
		}
		return transferCredentialMaterial{
			Provider:  provider,
			Env:       env,
			ExpiresAt: creds.Expires,
		}, nil
	case "googlecloudstorage":
		tokenSource, err := google.DefaultTokenSource(ctx, storage.ScopeReadWrite)
		if err != nil {
			return transferCredentialMaterial{}, err
		}
		tok, err := tokenSource.Token()
		if err != nil {
			return transferCredentialMaterial{}, err
		}
		env := map[string]string{
			"GOOGLE_OAUTH_ACCESS_TOKEN": strings.TrimSpace(tok.AccessToken),
		}
		project := strings.TrimSpace(resolveGCPProjectHint(ctx, hints.GCPProject))
		if project != "" {
			env["GOOGLE_CLOUD_PROJECT"] = project
		}
		return transferCredentialMaterial{
			Provider:  provider,
			Env:       env,
			ExpiresAt: tok.Expiry,
		}, nil
	default:
		return transferCredentialMaterial{}, fmt.Errorf("unsupported cloud provider %q for credential resolution", cloud.Provider)
	}
}
