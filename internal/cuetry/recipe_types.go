package cuetry

// Recipe is the decoded "recipe" block from a CUE document.
type Recipe struct {
	Name     string           `json:"name"`
	Defaults *RecipeDefaults `json:"defaults,omitempty"`
	Steps    []RecipeStep     `json:"steps"`
}

// RecipeDefaults holds recipe-level defaults (optional fields).
type RecipeDefaults struct {
	RunAs string `json:"run_as,omitempty"`
}

// RecipeFileTransfer is a local ↔ remote path pair for SFTP put/get steps.
type RecipeFileTransfer struct {
	Local  string `json:"local"`
	Remote string `json:"remote"`
}

// RecipeStep is one remote action. Exactly one of Command, Put, Get, or Script must be set:
//   - Command: shell over SSH (optional run_as via sudo -n).
//   - Put:     upload one local file to the same remote path on each target host.
//   - Get:     download remote file; with one target local is the file path; with
//              multiple targets local must be a directory (see cue-exec validation).
//   - Script:  upload local file then run it with POSIX sh on one SSH connection per host
//              (optional run_as applies to the run phase only; upload uses SSH user).
//
// Host selects targets: literal IP, exact name, "*", or "re:…" (see resolve.go).
type RecipeStep struct {
	Host    string `json:"host"`
	Command string `json:"command,omitempty"`
	Put     *RecipeFileTransfer `json:"put,omitempty"`
	Get     *RecipeFileTransfer `json:"get,omitempty"`
	Script  *RecipeFileTransfer `json:"script,omitempty"`
	RunAs   string              `json:"run_as,omitempty"`
}
