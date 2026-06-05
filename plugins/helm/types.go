package main

type helmConfig struct {
	Release    string            `json:"release"`
	Chart      string            `json:"chart,omitempty"`
	Namespace  string            `json:"namespace,omitempty"`
	Values     map[string]any    `json:"values,omitempty"`
	Set        map[string]string `json:"set,omitempty"`
	Version    string            `json:"version,omitempty"`
	Wait       bool              `json:"wait,omitempty"`
	Timeout    string            `json:"timeout,omitempty"`
	Atomic     bool              `json:"atomic,omitempty"`
	Force      bool              `json:"force,omitempty"`
	Kubeconfig string            `json:"kubeconfig,omitempty"`
	Context    string            `json:"context,omitempty"`
	Repo       string            `json:"repo,omitempty"`
	RepoURL    string            `json:"repo_url,omitempty"`
	Revision   int               `json:"revision,omitempty"`
}
