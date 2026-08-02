package cloudtransfer

import (
	"context"
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNormalizeProvider(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"gcs", "googlecloudstorage"},
		{"GCS", "googlecloudstorage"},
		{"  gcs  ", "googlecloudstorage"},
		{"s3", "s3"},
		{"S3", "s3"},
		{"azureblob", "azureblob"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := NormalizeProvider(tt.input)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestResolveGCPProjectHint(t *testing.T) {
	ctx := context.Background()

	t.Run("explicit hint", func(t *testing.T) {
		got := ResolveGCPProjectHint(ctx, "my-explicit-project")
		assert.Equal(t, "my-explicit-project", got)
	})

	t.Run("env vars", func(t *testing.T) {
		os.Setenv("GOOGLE_CLOUD_PROJECT", "env-project")
		defer os.Unsetenv("GOOGLE_CLOUD_PROJECT")

		got := ResolveGCPProjectHint(ctx, "")
		assert.Equal(t, "env-project", got)
	})

	t.Run("fallback to default credentials without env var", func(_ *testing.T) {
		// Just ensure it doesn't panic. Whether it returns empty or a real project depends
		// on the environment running the test, so we just check it returns a string.
		_ = ResolveGCPProjectHint(ctx, "")
	})
}

func TestResolveCredentialMaterial_Unsupported(t *testing.T) {
	ctx := context.Background()

	_, err := ResolveCredentialMaterial(ctx, CloudBackend{Provider: "unknown-provider"}, SigningHints{})
	assert.ErrorContains(t, err, "unsupported cloud provider")
}

func TestResolveCredentialMaterial_S3(t *testing.T) {
	ctx := context.Background()

	// By setting a fake profile and preventing environment resolution, we can test the fallback
	// without actually hitting AWS or needing real credentials, which will result in an error.
	os.Setenv("AWS_PROFILE", "does-not-exist")
	defer os.Unsetenv("AWS_PROFILE")

	_, err := ResolveCredentialMaterial(ctx, CloudBackend{Provider: "s3"}, SigningHints{AWSProfile: "fake-profile", AWSRegion: "us-west-2"})
	// It will either fail loading config or retrieving credentials. Both are fine for coverage.
	assert.Error(t, err)
}

func TestResolveCredentialMaterial_GCS(t *testing.T) {
	ctx := context.Background()

	// We can't easily mock google.DefaultTokenSource without overriding the http client or setting GOOGLE_APPLICATION_CREDENTIALS
	// to a fake file. Let's set it to a non-existent file to force an error.
	os.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/does/not/exist.json")
	defer os.Unsetenv("GOOGLE_APPLICATION_CREDENTIALS")

	_, err := ResolveCredentialMaterial(ctx, CloudBackend{Provider: "gcs"}, SigningHints{GCPProject: "test-project"})
	assert.Error(t, err)
}
