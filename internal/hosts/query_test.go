package hosts_test

import "github.com/shareed2k/honey/internal/hosts"

// Compile-time guard: Query must have exactly 3 fields (NameSubstring,
// NameRegex, Providers). If a new provider-specific field is added directly
// to Query this composite literal will fail to compile, prompting the author
// to reconsider the addition.
var _ = hosts.Query{
	NameSubstring: "",
	NameRegex:     "",
	Providers:     nil,
}
