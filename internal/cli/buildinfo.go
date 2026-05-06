package cli

// Build metadata (set from main via InitBuildInfo, e.g. goreleaser ldflags).
var (
	buildVersion = "0.0.0-dev"
	buildCommit  = "none"
	buildDate    = "unknown"
)

// InitBuildInfo sets version strings used by --version and the version command.
func InitBuildInfo(version, commit, date string) {
	if version != "" {
		buildVersion = version
	}
	if commit != "" {
		buildCommit = commit
	}
	if date != "" {
		buildDate = date
	}
	rootCmd.Version = buildVersion
	rootCmd.SetVersionTemplate(VersionTemplate())
	rootCmd.InitDefaultVersionFlag()
}

// BuildVersion returns the binary version (for embedded web UI meta).
func BuildVersion() string { return buildVersion }

// BuildCommit returns the git commit string embedded at link time.
func BuildCommit() string { return buildCommit }

// BuildDate returns the build date string embedded at link time.
func BuildDate() string { return buildDate }
