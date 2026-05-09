// Package cuetry parses, validates, and resolves CUE remote recipes for honey.
package cuetry

// Recipe is the decoded "recipe" block from a CUE document.
type Recipe struct {
	Name     string          `json:"name"`
	Defaults *RecipeDefaults `json:"defaults,omitempty"`
	Steps    []RecipeStep    `json:"steps"`
}

// RecipeDefaults holds recipe-level defaults (optional fields).
type RecipeDefaults struct {
	RunAs         string            `json:"run_as,omitempty"`
	Env           map[string]string `json:"env,omitempty"`
	K8sDebugImage string            `json:"k8s_debug_image,omitempty"`
}

// RecipeFileTransfer is a local ↔ remote path pair for SFTP put/get steps.
type RecipeFileTransfer struct {
	Local  string `json:"local"`
	Remote string `json:"remote"`
}

// RecipeAgentTransferCloud is the staging object location (S3/GCS, etc.).
type RecipeAgentTransferCloud struct {
	Provider string `json:"provider"`
	Bucket   string `json:"bucket"`
	Prefix   string `json:"prefix,omitempty"`
	Object   string `json:"object,omitempty"`
	Region   string `json:"region,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
}

// RecipeCloudBackendRef selects a backend entry from honey YAML for signing hints (AWS profile, GCP project).
type RecipeCloudBackendRef struct {
	Kind  string `json:"kind"`
	Name  string `json:"name,omitempty"`
	Index *int   `json:"index,omitempty"`
}

// RecipeAgentTransfer is source host (top-level host) → cloud → destination (dest_host), same flow as the web UI.
type RecipeAgentTransfer struct {
	DestHost        string                    `json:"dest_host"`
	SourcePath      string                    `json:"source_path"`
	DestPath        string                    `json:"dest_path"`
	Cloud           *RecipeAgentTransferCloud `json:"cloud"`
	CloudBackendRef *RecipeCloudBackendRef    `json:"cloud_backend_ref,omitempty"`
	KeepObject      bool                      `json:"keep_object,omitempty"`
	MaxRetries      int                       `json:"max_retries,omitempty"`
	AgentRemoteDir  string                    `json:"agent_remote_dir,omitempty"`
}

// RecipeStep is one remote action: exactly one of command, put, get, script, or agent_transfer.
// Host selects targets: literal IP, exact name, "*", or "re:…" (see resolve.go). For agent_transfer,
// host selects the source endpoint (must match exactly one row); agent_transfer.dest_host selects the destination.
type RecipeStep struct {
	Host          string               `json:"host"`
	Command       string               `json:"command,omitempty"`
	Put           *RecipeFileTransfer  `json:"put,omitempty"`
	Get           *RecipeFileTransfer  `json:"get,omitempty"`
	Script        *RecipeFileTransfer  `json:"script,omitempty"`
	AgentTransfer *RecipeAgentTransfer `json:"agent_transfer,omitempty"`
	RunAs         string               `json:"run_as,omitempty"`
	Env           map[string]string    `json:"env,omitempty"`
}
