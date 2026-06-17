package engine

import "testing"

// TestResolveAgentTransferSigningHints_nilRef ...
func TestResolveAgentTransferSigningHints_nilRef(t *testing.T) {
	t.Parallel()
	h, err := ResolveAgentTransferSigningHints("", AgentCloudBackend{Provider: "s3"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if h.AWSProfile != "" || h.AWSRegion != "" || h.GCPProject != "" {
		t.Fatalf("expected empty hints, got %+v", h)
	}
}
