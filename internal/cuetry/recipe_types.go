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
	RunAs string            `json:"run_as,omitempty"`
	Env   map[string]string `json:"env,omitempty"`
}

// RecipeFileTransfer is a local ↔ remote path pair for SFTP put/get steps.
type RecipeFileTransfer struct {
	Local  string `json:"local"`
	Remote string `json:"remote"`
}

// RecipeStep is one remote action: exactly one of Command, Put, Get, or Script.
// Host selects targets: literal IP, exact name, "*", or "re:…" (see resolve.go).
type RecipeStep struct {
	Host    string              `json:"host"`
	Command string              `json:"command,omitempty"`
	Put     *RecipeFileTransfer `json:"put,omitempty"`
	Get     *RecipeFileTransfer `json:"get,omitempty"`
	Script  *RecipeFileTransfer `json:"script,omitempty"`
	RunAs   string              `json:"run_as,omitempty"`
	Env     map[string]string   `json:"env,omitempty"`
}
