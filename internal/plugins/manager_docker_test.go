package plugins

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	apiv1 "github.com/shareed2k/honey/internal/plugins/api/v1"
)

// fakeTransport is a minimal pluginTransport double so Manager.Call's
// nonzero-exit-vs-PluginError precedence can be tested without a real
// Extism or Docker transport underneath.
type fakeTransport struct {
	exit     int
	outBytes []byte
	err      error
}

func (f fakeTransport) CallRaw(_ context.Context, _ string, _ []byte) (int, []byte, error) {
	return f.exit, f.outBytes, f.err
}

func (f fakeTransport) Close(_ context.Context) error { return nil }

// TestManagerCall_NonZeroExitPrefersPluginErrorText proves that when a
// transport returns a nonzero exit code AND a decodable apiv1.PluginError in
// outBytes (as dockerTransport.CallRaw deliberately does for its nonzero-exit
// case), Manager.Call surfaces that descriptive PluginError.Error text
// instead of the generic "plugin returned exit code N" message.
func TestManagerCall_NonZeroExitPrefersPluginErrorText(t *testing.T) {
	pe := apiv1.PluginError{Error: "custom failure detail"}
	outBytes, err := json.Marshal(pe)
	if err != nil {
		t.Fatalf("marshal PluginError: %v", err)
	}

	m := &Manager{
		enabled: true,
		byID: map[string]*loadedPlugin{
			"fake": {
				manifest:  Manifest{ID: "fake"},
				transport: fakeTransport{exit: 1, outBytes: outBytes},
			},
		},
	}

	callErr := m.Call(context.Background(), "fake", "export", map[string]any{}, nil)
	if callErr == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(callErr.Error(), "custom failure detail") {
		t.Fatalf("error=%q want it to contain %q", callErr.Error(), "custom failure detail")
	}
	if strings.Contains(callErr.Error(), "exit code") {
		t.Fatalf("error=%q should not fall back to generic exit-code message when PluginError.Error is present", callErr.Error())
	}
}
