package cli

import (
	"fmt"
	"io"
	"strings"
)

const logoASCII = `
88 Build by DevOps for DevOps with <3
88
88 Where's my instance,
88,dPPYba,   ,adPPYba,  8b,dPPYba,   ,adPPYba, 8b       d8
88P'    "8a a8"     "8a 88P'   '"8a a8P_____88 '8b     d8'
88       88 8b       d8 88       88 8PP"""""""  '8b   d8'
88       88 "8a,   ,a8" 88       88 "8b,   ,aa   '8b,d8'
88       88  '"YbbdP"'  88       88  '"Ybbd8"'     Y88'
                                                   d8'
                                                  d8' ?`

// Tagline is the one-line product description (CLI short help).
const Tagline = "DevOps tool to help find an instance in sea of clouds"

// BannerText is tagline + ASCII logo (no version metadata). Used in usage/--help and --version.
func BannerText() string {
	return strings.TrimSpace(Tagline) + "\n\n" + strings.Trim(logoASCII, "\n")
}

// VersionTemplate returns the cobra --version template (uses {{.Version}}).
func VersionTemplate() string {
	return "\n" + BannerText() + "\n\nVersion: {{.Version}}\nCommit: " + buildCommit + "\nDate: " + buildDate + "\n"
}

// PrintVersion writes the logo, tagline, and build metadata (for version subcommand).
func PrintVersion(w io.Writer) {
	_, _ = fmt.Fprintln(w, BannerText())
	_, _ = fmt.Fprintf(w, "\nVersion: %s\nCommit: %s\nDate: %s\n", buildVersion, buildCommit, buildDate)
}
