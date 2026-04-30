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
