package transferagent

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/shareed2k/honey/internal/safepath"
)

const (
	agentDownloadURLEnv            = "HONEY_TRANSFER_AGENT_DOWNLOAD_URL"
	agentDownloadBaseEnv           = "HONEY_TRANSFER_AGENT_DOWNLOAD_BASE"
	agentDownloadDisableDefaultEnv = "HONEY_TRANSFER_AGENT_DOWNLOAD_DISABLE_DEFAULT"
)

func expandAgentDownloadTemplate(tpl, goos, goarch string) string {
	r := strings.NewReplacer(
		"{os}", goos,
		"{arch}", goarch,
		"{GOOS}", goos,
		"{GOARCH}", goarch,
	)
	return r.Replace(tpl)
}

func downloadDefaultDisabled() bool {
	s := strings.ToLower(strings.TrimSpace(os.Getenv(agentDownloadDisableDefaultEnv)))
	return s == "1" || s == "true" || s == "yes" || s == "on"
}

// transferAgentDownloadURL returns a GET URL for a prebuilt honey-transfer-agent.
// Precedence: HONEY_TRANSFER_AGENT_DOWNLOAD_URL (template) > HONEY_TRANSFER_AGENT_DOWNLOAD_BASE > default
// GitHub release for the embedded honey version (SetEmbeddedHoneyVersion), or latest when version is unset/dev
// (unless HONEY_TRANSFER_AGENT_DOWNLOAD_DISABLE_DEFAULT is set).
// Asset name matches CI: honey-transfer-agent-<goos>-<goarch>.
func transferAgentDownloadURL(goos, goarch string) (string, bool) {
	if u := strings.TrimSpace(os.Getenv(agentDownloadURLEnv)); u != "" {
		return expandAgentDownloadTemplate(u, goos, goarch), true
	}
	if base := strings.TrimRight(strings.TrimSpace(os.Getenv(agentDownloadBaseEnv)), "/"); base != "" {
		return base + "/honey-transfer-agent-" + goos + "-" + goarch, true
	}
	if downloadDefaultDisabled() {
		return "", false
	}
	return defaultGitHubReleaseAssetURL(goos, goarch), true
}

// fetchAgentBinary downloads url into destPath atomically (destPath.tmp then rename).
func fetchAgentBinary(url, destPath string) error {
	client := &http.Client{Timeout: 120 * time.Second}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "honey-transfer-agent-resolver/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %q: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("GET %q: status %s: %s", url, resp.Status, strings.TrimSpace(string(slurp)))
	}
	tmp := destPath + ".tmp"
	f, err := safepath.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, io.LimitReader(resp.Body, 200<<20)); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("write %q: %w", tmp, err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, destPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}
