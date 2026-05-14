package transferagent

import "strings"

// embeddedHoneyVersion is the honey CLI version (e.g. goreleaser ldflags); used for default
// prebuilt transfer-agent URLs. Set once from cmd/honey via SetEmbeddedHoneyVersion.
var embeddedHoneyVersion string

// SetEmbeddedHoneyVersion sets the honey binary version used to pick the matching GitHub release
// for default transfer-agent downloads (releases/download/<tag>/…). Call from cmd/honey/main.go
// with the same string passed to cli.InitBuildInfo.
func SetEmbeddedHoneyVersion(v string) {
	embeddedHoneyVersion = strings.TrimSpace(v)
}

func defaultGitHubReleaseAssetURL(goos, goarch string) string {
	asset := "honey-transfer-agent-" + goos + "-" + goarch
	tag := strings.TrimSpace(embeddedHoneyVersion)
	if tag == "" || tag == "0.0.0-dev" {
		return "https://github.com/shareed2k/honey/releases/latest/download/" + asset
	}
	if !strings.HasPrefix(strings.ToLower(tag), "v") {
		tag = "v" + tag
	}
	return "https://github.com/shareed2k/honey/releases/download/" + tag + "/" + asset
}
