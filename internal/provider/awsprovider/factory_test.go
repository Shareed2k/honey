package awsprovider

import (
	"strings"
	"testing"

	"github.com/shareed2k/honey/internal/searchrun"
)

// unwrapAWS extracts the AWS profile and region from a backend returned by
// awsFactory.Default. The backend is a *searchrun.dockerDiscoverWrapper that
// embeds the real *AWS, so CacheIdentity() is promoted and returns
// "<name>\x1e<profile>\x1e<region>".
func unwrapAWS(b interface {
	CacheIdentity() string
},
) (profile, region string) {
	parts := strings.SplitN(b.CacheIdentity(), "\x1e", 3)
	if len(parts) == 3 {
		return parts[1], parts[2]
	}
	return "", ""
}

func TestAWSDefault_FallsBackToCliFlags(t *testing.T) {
	cliFlags.profile = "my-profile"
	cliFlags.region = "us-east-1"
	t.Cleanup(func() {
		cliFlags.profile = ""
		cliFlags.region = ""
	})

	b := awsFactory{}.Default(searchrun.ProviderFlags{})

	if b.ID() != "aws" {
		t.Fatalf("ID: want %q got %q", "aws", b.ID())
	}

	profile, region := unwrapAWS(b)
	if profile != "my-profile" {
		t.Errorf("profile: want %q got %q", "my-profile", profile)
	}
	if region != "us-east-1" {
		t.Errorf("region: want %q got %q", "us-east-1", region)
	}
}

func TestAWSDefault_UsesProviderFlags(t *testing.T) {
	// cliFlags should be ignored when ProviderFlags has explicit values.
	cliFlags.profile = "should-be-ignored"
	cliFlags.region = "should-be-ignored"
	t.Cleanup(func() {
		cliFlags.profile = ""
		cliFlags.region = ""
	})

	b := awsFactory{}.Default(searchrun.ProviderFlags{
		AWSProfile: "prod",
		AWSRegion:  "eu-central-1",
	})

	if b.ID() != "aws" {
		t.Fatalf("ID: want %q got %q", "aws", b.ID())
	}

	profile, region := unwrapAWS(b)
	if profile != "prod" {
		t.Errorf("profile: want %q got %q", "prod", profile)
	}
	if region != "eu-central-1" {
		t.Errorf("region: want %q got %q", "eu-central-1", region)
	}
}
