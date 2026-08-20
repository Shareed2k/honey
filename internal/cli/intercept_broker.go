package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"k8s.io/client-go/rest"

	"github.com/shareed2k/honey/internal/interceptwire"
)

// Server-brokered interception endpoints (see
// internal/webserver/intercept_broker.go). interceptStopPathFmt takes the
// session id.
const (
	interceptConfigPath    = "/api/v1/intercept/config"
	interceptAuthorizePath = "/api/v1/intercept/authorize"
	interceptStopPathFmt   = "/api/v1/intercept/%s/stop"
)

// brokeredAuthorizeReq is the body of a POST to interceptAuthorizePath, minus
// the id_token/nonce that interceptAuthorize adds.
type brokeredAuthorizeReq struct {
	Cluster    string   `json:"cluster"`
	Namespace  string   `json:"namespace"`
	Pod        string   `json:"pod"`
	Container  string   `json:"container,omitempty"`
	Mode       []string `json:"mode,omitempty"`
	UDP        bool     `json:"udp,omitempty"`
	Target     string   `json:"target,omitempty"`
	AgentImage string   `json:"agent_image,omitempty"`
}

// brokeredAuthorizeResp is honey web's response to an authorize request: the
// session handle, the per-session agent token (never logged), and the two
// in-agent ports the caller must port-forward to.
type brokeredAuthorizeResp struct {
	SessionID   string `json:"session_id"`
	Token       string `json:"token"`
	ControlPort int    `json:"control_port"`
	EgressPort  int    `json:"egress_port"`
}

// fetchInterceptConfig queries honey web's brokered-intercept config endpoint,
// reporting whether the operator has enabled server-brokered interception and
// its configured default modes.
func fetchInterceptConfig(ctx context.Context, adminURL string) (enabled bool, defaultMode []string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, adminURL+interceptConfigPath, nil)
	if err != nil {
		return false, nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, nil, fmt.Errorf("fetch intercept config: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode != http.StatusOK {
		return false, nil, fmt.Errorf("fetch intercept config: HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var body struct {
		Enabled     bool     `json:"enabled"`
		DefaultMode []string `json:"default_mode"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		return false, nil, fmt.Errorf("parse intercept config: %w", err)
	}
	return body.Enabled, body.DefaultMode, nil
}

// interceptAuthorize POSTs an authorize request to honey web and returns the
// brokered session handle. The id_token authenticates the request; neither it
// nor the returned token is ever logged.
func interceptAuthorize(ctx context.Context, adminURL, idToken, nonce string, req brokeredAuthorizeReq) (brokeredAuthorizeResp, error) {
	payload := struct {
		IDToken string `json:"id_token"`
		Nonce   string `json:"nonce"`
		brokeredAuthorizeReq
	}{IDToken: idToken, Nonce: nonce, brokeredAuthorizeReq: req}

	body, err := json.Marshal(payload)
	if err != nil {
		return brokeredAuthorizeResp{}, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, adminURL+interceptAuthorizePath, bytes.NewReader(body))
	if err != nil {
		return brokeredAuthorizeResp{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(httpReq)
	if err != nil {
		return brokeredAuthorizeResp{}, fmt.Errorf("intercept authorize: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		// Generic: the response body is not echoed back so a server-side error
		// page can never carry the id_token or session token into a log line.
		return brokeredAuthorizeResp{}, fmt.Errorf("intercept authorize: HTTP %d", resp.StatusCode)
	}

	var out brokeredAuthorizeResp
	if err := json.Unmarshal(raw, &out); err != nil {
		return brokeredAuthorizeResp{}, fmt.Errorf("parse intercept authorize response: %w", err)
	}
	return out, nil
}

// interceptStop POSTs a stop request for sessionID, authenticated by the
// per-session agent token (the capability returned from interceptAuthorize) —
// not the id_token, which may have expired by the time the session ends. Any
// 2xx response (honey web returns 204) is success. token is never logged.
func interceptStop(ctx context.Context, adminURL, sessionID, token string) error {
	payload := struct {
		Token string `json:"token"`
	}{Token: token}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, adminURL+fmt.Sprintf(interceptStopPathFmt, url.PathEscape(sessionID)), bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("intercept stop: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("intercept stop: HTTP %d", resp.StatusCode)
	}
	return nil
}

// brokerPodExecer execs into a specific ephemeral container using honey web's
// own (service-account) cluster credentials. It is the Broker's Execer for one
// cluster: honey web uses it to deliver the per-session token and to signal
// the agent to exit on stop/expiry. It satisfies intercept.PodExecer.
type brokerPodExecer struct {
	cfg                *rest.Config
	ns, pod, container string
}

// ExecInPod runs cmd in the broker's known container, wiring the provided
// streams.
func (e *brokerPodExecer) ExecInPod(ctx context.Context, cmd []string, stdin io.Reader, stdout, stderr io.Writer) error {
	return interceptwire.ExecInPodContainer(ctx, e.cfg, e.ns, e.pod, e.container, cmd, stdin, stdout, stderr)
}
