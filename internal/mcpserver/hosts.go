package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/shareed2k/honey/internal/hostapi"
	"github.com/shareed2k/honey/internal/hosts"
)

// findHostFn is a package-level var so tests can inject a fake search without
// touching real backends.
var findHostFn = func(ctx context.Context, in *hostapi.SearchHostsInput) (hostapi.SearchHostsOutput, error) {
	return hostapi.SearchHosts(ctx, in, nil, globalSearchReg)
}

type getHostDetailsInput struct {
	Name     string `json:"name"               mod:"trim" validate:"required"`
	Provider string `json:"provider,omitempty" mod:"trim"`
}

type getHostDetailsOutput struct {
	Record       hosts.Record `json:"record"`
	Capabilities []string     `json:"capabilities"`
}

// handleGetHostDetails returns full Record details for a named host across all
// backends. Safe: read-only, no SSH dial.
func handleGetHostDetails(ctx context.Context, _ *mcp.CallToolRequest, in getHostDetailsInput) (*mcp.CallToolResult, getHostDetailsOutput, error) {
	if err := conform.Struct(ctx, &in); err != nil {
		return nil, getHostDetailsOutput{}, err
	}
	if err := validate.Struct(in); err != nil {
		return nil, getHostDetailsOutput{}, err
	}

	searchIn := &hostapi.SearchHostsInput{
		Config: serverCfg,
		Name:   in.Name,
	}
	if in.Provider != "" {
		searchIn.Providers = in.Provider
	}

	out, err := findHostFn(ctx, searchIn)
	if err != nil {
		return nil, getHostDetailsOutput{}, fmt.Errorf("get_host_details: search: %w", err)
	}

	var rec *hosts.Record
	for i := range out.Records {
		r := &out.Records[i]
		if r.Name == in.Name && (in.Provider == "" || r.Provider == in.Provider) {
			rec = r
			break
		}
	}
	if rec == nil && len(out.Records) > 0 {
		rec = &out.Records[0]
	}
	if rec == nil {
		return nil, getHostDetailsOutput{}, fmt.Errorf("get_host_details: host %q not found", in.Name)
	}

	return nil, getHostDetailsOutput{
		Record:       *rec,
		Capabilities: rec.DeriveCapabilities(),
	}, nil
}
