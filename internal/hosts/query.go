package hosts

// Query carries global search filters for a search run.
type Query struct {
	NameSubstring string
	NameRegex     string
	Providers     []string // e.g. gcp, aws, k8s, consul — empty means all
}
