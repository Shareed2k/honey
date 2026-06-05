package v1

// K8sHTTPInput is passed to the k8s_http host function.
// Path is the API path (e.g. "/apis/apps/v1/namespaces/default/deployments").
// The host function resolves the API server URL and credentials from HostRunContext.
type K8sHTTPInput struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Body    []byte            `json:"body,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// MaxResponseBytes caps the response body (0 = default 4 MB).
	MaxResponseBytes int64 `json:"max_response_bytes,omitempty"`
}

// K8sHTTPOutput is returned from the k8s_http host function.
type K8sHTTPOutput struct {
	StatusCode int               `json:"status_code"`
	Body       []byte            `json:"body,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Error      string            `json:"error,omitempty"`
}
