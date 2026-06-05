//go:build wasip1 || wasm

package pluginpdk

//go:wasmimport extism:host/user k8s_http
func k8sHTTPImport(inputOffset uint64) uint64

// K8sHTTPInput is sent to the k8s_http host function.
// Path must be the API path without the server host (e.g. "/apis/apps/v1/...").
type K8sHTTPInput struct {
	Method           string            `json:"method"`
	Path             string            `json:"path"`
	Body             []byte            `json:"body,omitempty"`
	Headers          map[string]string `json:"headers,omitempty"`
	MaxResponseBytes int64             `json:"max_response_bytes,omitempty"`
}

// K8sHTTPOutput is returned from the k8s_http host function.
type K8sHTTPOutput struct {
	StatusCode int               `json:"status_code"`
	Body       []byte            `json:"body,omitempty"`
	Headers    map[string]string `json:"headers,omitempty"`
	Error      string            `json:"error,omitempty"`
}

// K8sHTTP makes an HTTP request to the Kubernetes API via the host function.
// The host resolves API server URL and credentials from the current HostRunContext.
func K8sHTTP(method, path string, body []byte, headers map[string]string) (K8sHTTPOutput, error) {
	return callRemote[K8sHTTPOutput](k8sHTTPImport, K8sHTTPInput{
		Method:  method,
		Path:    path,
		Body:    body,
		Headers: headers,
	})
}
