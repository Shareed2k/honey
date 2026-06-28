package cli

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/shareed2k/honey/internal/config"
	"github.com/shareed2k/honey/internal/plugins"
	"github.com/shareed2k/honey/internal/policy"
)

// doctorStatus is the result level for one health check.
type doctorStatus int

const (
	doctorOK   doctorStatus = iota // ✓
	doctorWarn                     // ⚠
	doctorFail                     // ✗
)

// doctorResult holds the outcome of a single check.
type doctorResult struct {
	name    string
	status  doctorStatus
	message string
}

// doctorCheck is an injectable check unit. Accepting a func type keeps the
// doctor itself free of concrete dependencies and enables table-driven tests.
type doctorCheck func(ctx context.Context, cfg *config.File) doctorResult

func init() {
	rootCmd.AddCommand(doctorCmd)
	doctorCmd.Flags().Bool("mcp", false, "Include MCP-specific checks")
	doctorCmd.Flags().Bool("plugins", false, "Include plugin checks")
	doctorCmd.Flags().Bool("web", false, "Include web server listen-addr check")
}

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check honey installation health: config, plugins, OPA policy, SSH key, and more",
	RunE:  runDoctor,
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	ctx, cancel := context.WithTimeout(cmd.Context(), 15*time.Second)
	defer cancel()

	checks := []doctorCheck{
		checkConfigParse,
		checkConfigValidate,
		checkSSHKey,
		checkAuditPath,
		checkOPAPolicy,
	}

	inclPlugins, _ := cmd.Flags().GetBool("plugins")
	inclMCP, _ := cmd.Flags().GetBool("mcp")
	inclWeb, _ := cmd.Flags().GetBool("web")

	if inclPlugins {
		checks = append(checks, checkPlugins)
	}
	if inclMCP {
		checks = append(checks, checkMCPPolicyDir)
	}
	if inclWeb {
		checks = append(checks, checkWebListenAddr)
	}

	out := cmd.OutOrStdout()
	anyFail := false
	for _, chk := range checks {
		r := chk(ctx, resolvedCfg)
		prefix := "✓"
		switch r.status {
		case doctorWarn:
			prefix = "⚠"
		case doctorFail:
			prefix = "✗"
			anyFail = true
		}
		fmt.Fprintf(out, "%s  %-28s %s\n", prefix, r.name, r.message)
	}

	if anyFail {
		return fmt.Errorf("one or more checks failed")
	}
	return nil
}

// --- individual checks ---

func checkConfigParse(_ context.Context, cfg *config.File) doctorResult {
	if cfg == nil {
		return doctorResult{"config: parse", doctorWarn, "no config file found (running with defaults)"}
	}
	return doctorResult{"config: parse", doctorOK, "loaded successfully"}
}

func checkConfigValidate(_ context.Context, cfg *config.File) doctorResult {
	if cfg == nil {
		return doctorResult{"config: validate", doctorOK, "skipped (no config)"}
	}
	err := cfg.Validate()
	if err == nil {
		return doctorResult{"config: validate", doctorOK, "no issues"}
	}
	if verrs, ok := err.(config.ValidationErrors); ok {
		msgs := make([]string, 0, len(verrs))
		for _, e := range verrs {
			msgs = append(msgs, e.Path+": "+e.Message)
		}
		return doctorResult{"config: validate", doctorFail, strings.Join(msgs, "; ")}
	}
	return doctorResult{"config: validate", doctorFail, err.Error()}
}

func checkSSHKey(_ context.Context, cfg *config.File) doctorResult {
	if cfg == nil || strings.TrimSpace(cfg.Defaults.SSHUser) == "" {
		return doctorResult{"ssh: user", doctorOK, "not configured (using current user)"}
	}
	return doctorResult{"ssh: user", doctorOK, cfg.Defaults.SSHUser}
}

func checkAuditPath(_ context.Context, cfg *config.File) doctorResult {
	if cfg == nil || !cfg.Audit.Enabled {
		return doctorResult{"audit: log", doctorOK, "disabled"}
	}
	path := cfg.Audit.EffectivePath()
	dir := path[:strings.LastIndex(path, string(os.PathSeparator))]
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return doctorResult{"audit: log", doctorFail, fmt.Sprintf("cannot create dir %s: %v", dir, err)}
	}
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return doctorResult{"audit: log", doctorFail, fmt.Sprintf("cannot write %s: %v", path, err)}
	}
	_ = f.Close()
	return doctorResult{"audit: log", doctorOK, path}
}

func checkOPAPolicy(ctx context.Context, _ *config.File) doctorResult {
	dir := filepath.Clean(strings.TrimSpace(os.Getenv("HONEY_POLICY_DIR")))
	if dir == "" || dir == "." {
		msg := "HONEY_POLICY_DIR not set — exec allowed only with HONEY_EXEC_ALLOW_UNVERIFIED=1"
		return doctorResult{"opa: policy", doctorWarn, msg}
	}
	if _, err := os.Stat(dir); err != nil {
		return doctorResult{"opa: policy", doctorFail, fmt.Sprintf("policy dir %s: %v", dir, err)}
	}
	if _, err := policy.New(ctx, dir, nil); err != nil {
		return doctorResult{"opa: policy", doctorFail, fmt.Sprintf("compile error: %v", err)}
	}
	return doctorResult{"opa: policy", doctorOK, dir}
}

func checkPlugins(ctx context.Context, cfg *config.File) doctorResult {
	mgr, err := plugins.Open(ctx, cfg)
	if err != nil {
		return doctorResult{"plugins: load", doctorFail, err.Error()}
	}
	defer func() { _ = mgr.Close() }()
	if !mgr.Enabled() {
		return doctorResult{"plugins: load", doctorOK, "disabled"}
	}
	list := mgr.List()
	return doctorResult{"plugins: load", doctorOK, fmt.Sprintf("%d plugin(s) loaded", len(list))}
}

func checkMCPPolicyDir(_ context.Context, _ *config.File) doctorResult {
	dir := strings.TrimSpace(os.Getenv("HONEY_POLICY_DIR"))
	allow := strings.TrimSpace(os.Getenv("HONEY_EXEC_ALLOW_UNVERIFIED"))
	switch {
	case dir != "":
		return doctorResult{"mcp: exec gate", doctorOK, "OPA policy dir set: " + dir}
	case allow != "" && allow != "0" && !strings.EqualFold(allow, "false"):
		return doctorResult{"mcp: exec gate", doctorWarn, "HONEY_EXEC_ALLOW_UNVERIFIED=1 — exec runs without policy verification"}
	default:
		return doctorResult{"mcp: exec gate", doctorFail, "exec_on_host will deny all non-critical commands (set HONEY_POLICY_DIR or HONEY_EXEC_ALLOW_UNVERIFIED=1)"}
	}
}

func checkWebListenAddr(_ context.Context, _ *config.File) doctorResult {
	// Default honey web listen address. A free port means the server can start.
	const defaultAddr = "127.0.0.1:8765"
	l, err := net.Listen("tcp", defaultAddr)
	if err != nil {
		msg := fmt.Sprintf("%s already in use (honey web may already be running): %v", defaultAddr, err)
		return doctorResult{"web: listen addr", doctorWarn, msg}
	}
	_ = l.Close()
	return doctorResult{"web: listen addr", doctorOK, defaultAddr + " (port free)"}
}
