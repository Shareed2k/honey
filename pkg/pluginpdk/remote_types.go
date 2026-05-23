//go:build wasip1 || wasm

package pluginpdk

type remoteExecInput struct {
	Shell     string `json:"shell,omitempty"`
	Script    string `json:"script,omitempty"`
	RunAs     string `json:"run_as,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

type RemoteExecOutput struct {
	ExitCode int    `json:"exit_code,omitempty"`
	Stdout   string `json:"stdout,omitempty"`
	Stderr   string `json:"stderr,omitempty"`
	Changed  bool   `json:"changed,omitempty"`
	Failed   bool   `json:"failed,omitempty"`
	Error    string `json:"error,omitempty"`
}

type remoteUploadInput struct {
	LocalPath  string `json:"local_path,omitempty"`
	RemotePath string `json:"remote_path"`
	Mode       string `json:"mode,omitempty"`
	Content    string `json:"content,omitempty"`
}

type RemoteUploadOutput struct {
	Changed bool   `json:"changed,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
	Error   string `json:"error,omitempty"`
}

type remoteDownloadInput struct {
	RemotePath string `json:"remote_path"`
	LocalPath  string `json:"local_path,omitempty"`
	MaxBytes   int64  `json:"max_bytes,omitempty"`
}

type RemoteDownloadOutput struct {
	Content string `json:"content,omitempty"`
	Size    int64  `json:"size,omitempty"`
	Changed bool   `json:"changed,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
	Error   string `json:"error,omitempty"`
}

type remoteStatInput struct {
	Path string `json:"path"`
}

type RemoteStatOutput struct {
	Exists  bool   `json:"exists,omitempty"`
	IsDir   bool   `json:"is_dir,omitempty"`
	Mode    string `json:"mode,omitempty"`
	Size    int64  `json:"size,omitempty"`
	MTime   string `json:"mtime,omitempty"`
	Changed bool   `json:"changed,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
	Error   string `json:"error,omitempty"`
}

type templateRenderInput struct {
	Template string         `json:"template"`
	Data     map[string]any `json:"data,omitempty"`
}

type TemplateRenderOutput struct {
	Content string `json:"content,omitempty"`
	Failed  bool   `json:"failed,omitempty"`
	Error   string `json:"error,omitempty"`
}
