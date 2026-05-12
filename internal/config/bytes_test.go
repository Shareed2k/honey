package config

import (
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

func TestParseBytes(t *testing.T) {
	tests := []struct {
		in   string
		want int64
		err  bool
	}{
		{"0", 0, false},
		{"100", 100, false},
		{"1KiB", 1024, false},
		{"1MiB", 1 << 20, false},
		{"64MiB", 64 << 20, false},
		{"5GiB", 5 << 30, false},
		{"2TiB", 2 << 40, false},
		{" 1MiB ", 1 << 20, false},
		{"1mib", 1 << 20, false},
		{"", 0, true},
		{"abc", 0, true},
		{"5GB", 0, true},    // SI not supported in v1 — keep the surface small
		{"-1", 0, true},     // no negatives
		{"1.5GiB", 0, true}, // no fractions
	}
	for _, tc := range tests {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseBytes(tc.in)
			if tc.err {
				if err == nil {
					t.Fatalf("expected error, got %d", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTransferConfig_yamlDefaults(t *testing.T) {
	yamlSrc := `
version: 1
transfer:
  presigned_max_size: 5GiB
  multipart_threshold: 64MiB
  presigned_url_ttl: 1h
  presigned_retry_with_agent: true
  force_agent_path: false
`
	var f File
	if err := yaml.Unmarshal([]byte(yamlSrc), &f); err != nil {
		t.Fatal(err)
	}
	eff := f.Transfer.WithDefaults()
	if eff.PresignedMaxSizeBytes != 5<<30 {
		t.Fatalf("PresignedMaxSize = %d, want %d", eff.PresignedMaxSizeBytes, 5<<30)
	}
	if eff.MultipartThresholdBytes != 64<<20 {
		t.Fatalf("MultipartThreshold = %d, want %d", eff.MultipartThresholdBytes, 64<<20)
	}
	if eff.PresignedURLTTL != time.Hour {
		t.Fatalf("PresignedURLTTL = %v, want 1h", eff.PresignedURLTTL)
	}
	if !eff.PresignedRetryWithAgent {
		t.Fatal("PresignedRetryWithAgent should be true")
	}
	if eff.ForceAgentPath {
		t.Fatal("ForceAgentPath should be false")
	}
}

func TestTransferConfig_unsetUsesDefaults(t *testing.T) {
	var f File
	if err := yaml.Unmarshal([]byte(`version: 1`), &f); err != nil {
		t.Fatal(err)
	}
	got := f.Transfer.WithDefaults()
	if got.PresignedMaxSizeBytes != 5<<30 {
		t.Fatalf("default max size = %d, want %d", got.PresignedMaxSizeBytes, 5<<30)
	}
	if got.MultipartThresholdBytes != 64<<20 {
		t.Fatalf("default multipart threshold = %d", got.MultipartThresholdBytes)
	}
	if got.PresignedURLTTL != time.Hour {
		t.Fatalf("default TTL = %v", got.PresignedURLTTL)
	}
	if !got.PresignedRetryWithAgent {
		t.Fatal("default PresignedRetryWithAgent should be true")
	}
}
