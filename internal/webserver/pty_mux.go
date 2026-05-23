package webserver

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"go.uber.org/zap"
)

const honeyMuxPrefix = "honey_"

// validHoneyMuxSessionName reports whether name is a safe honey_* multiplexer session id.
func validHoneyMuxSessionName(name string) bool {
	if !strings.HasPrefix(name, honeyMuxPrefix) {
		return false
	}
	suffix := name[len(honeyMuxPrefix):]
	if suffix == "" || len(name) > len(honeyMuxPrefix)+64 {
		return false
	}
	for i := 0; i < len(suffix); i++ {
		c := suffix[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
		default:
			return false
		}
	}
	return true
}

func tmuxPaneRunning(paneDead, panePID, cmd string) bool {
	if strings.TrimSpace(paneDead) != "0" {
		return false
	}
	if strings.Contains(cmd, "[exited]") {
		return false
	}
	pid := strings.TrimSpace(panePID)
	if pid == "" || pid == "0" {
		return false
	}
	if _, err := strconv.Atoi(pid); err != nil {
		return false
	}
	return true
}

// tmuxSessionAlive reports whether a tmux session exists with a running pty-proxy pane.
func tmuxSessionAlive(name string) bool {
	if !validHoneyMuxSessionName(name) {
		return false
	}
	if err := exec.Command("tmux", "has-session", "-t", name).Run(); err != nil { // #nosec G204 -- name validated
		return false
	}
	out, err := exec.Command("tmux", "list-panes", "-t", name, "-F", "#{pane_dead}\t#{pane_pid}\t#{pane_current_command}").Output() // #nosec G204 -- name validated
	if err != nil {
		return false
	}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		fields := strings.Split(line, "\t")
		if len(fields) < 2 {
			continue
		}
		cmd := ""
		if len(fields) > 2 {
			cmd = fields[2]
		}
		if tmuxPaneRunning(fields[0], fields[1], cmd) {
			return true
		}
	}
	return false
}

func tmuxHasSession(name string) bool {
	if !validHoneyMuxSessionName(name) {
		return false
	}
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil // #nosec G204 -- name validated
}

// tmuxRespawnPane restarts the first pane with a fresh pty-proxy command (browser refresh after shell exit).
func tmuxRespawnPane(name string, proxyArgs []string) error {
	if !validHoneyMuxSessionName(name) {
		return fmt.Errorf("invalid tmux session name %q", name)
	}
	out, err := exec.Command("tmux", "list-panes", "-t", name, "-F", "#{pane_id}").Output() // #nosec G204 -- name validated
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return fmt.Errorf("tmux session %q has no panes", name)
	}
	paneID := strings.TrimSpace(lines[0])
	args := append([]string{"respawn-pane", "-k", "-t", paneID, "--"}, proxyArgs...)
	return exec.Command("tmux", args...).Run() // #nosec G204 -- proxyArgs from os.Executable + server hello
}

// tmuxSessionFullyExited reports whether every pane in the session is dead.
func tmuxSessionFullyExited(name string) bool {
	if !validHoneyMuxSessionName(name) {
		return false
	}
	if err := exec.Command("tmux", "has-session", "-t", name).Run(); err != nil { // #nosec G204 -- name validated
		return false
	}
	out, err := exec.Command("tmux", "list-panes", "-t", name, "-F", "#{pane_dead}").Output() // #nosec G204 -- name validated
	if err != nil {
		return false
	}
	lines := strings.Split(strings.TrimSpace(string(out)), "\n")
	if len(lines) == 0 || (len(lines) == 1 && lines[0] == "") {
		return false
	}
	for _, line := range lines {
		if strings.TrimSpace(line) != "1" {
			return false
		}
	}
	return true
}

func tmuxSessionAttached(name string) bool {
	if !validHoneyMuxSessionName(name) {
		return false
	}
	out, err := exec.Command("tmux", "list-sessions", "-t", name, "-F", "#{session_attached}").Output() // #nosec G204 -- name validated
	if err != nil {
		return false
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	return err == nil && n > 0
}

// tmuxKillSession removes a tmux session (no-op if missing).
func tmuxKillSession(name string) {
	if !validHoneyMuxSessionName(name) {
		return
	}
	_ = exec.Command("tmux", "kill-session", "-t", name).Run() // #nosec G204 -- name validated
}

// pruneHoneyTmuxSessions removes unattached honey_* sessions whose panes have all exited.
// keep is the session name for the current connection (never pruned).
func pruneHoneyTmuxSessions(keep string) {
	out, err := exec.Command("tmux", "list-sessions", "-F", "#{session_name}").Output()
	if err != nil {
		return
	}
	var killed []string
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name = strings.TrimSpace(name)
		if name == "" || !strings.HasPrefix(name, honeyMuxPrefix) || name == keep {
			continue
		}
		if tmuxSessionAttached(name) || !tmuxSessionFullyExited(name) {
			continue
		}
		tmuxKillSession(name)
		killed = append(killed, name)
	}
	if len(killed) > 0 {
		zap.L().Debug("pruned dead honey tmux sessions", zap.Strings("sessions", killed))
	}
}

// zellijSessionAlive reports whether zellij has a non-exited session with the given name.
func zellijSessionAlive(name string) bool {
	if strings.TrimSpace(name) == "" {
		return false
	}
	out, err := exec.Command("zellij", "list-sessions").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != name {
			continue
		}
		if strings.Contains(strings.ToUpper(line), "EXITED") {
			return false
		}
		return true
	}
	return false
}

// zellijKillSession removes a zellij session (no-op if missing).
func zellijKillSession(name string) {
	if !validHoneyMuxSessionName(name) {
		return
	}
	_ = exec.Command("zellij", "delete-session", "-f", name).Run() // #nosec G204 -- name validated
}

// pruneHoneyZellijSessions removes exited, unattached honey_* zellij sessions.
func pruneHoneyZellijSessions(keep string) {
	out, err := exec.Command("zellij", "list-sessions").Output()
	if err != nil {
		return
	}
	var killed []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		name := fields[0]
		if !strings.HasPrefix(name, honeyMuxPrefix) || name == keep {
			continue
		}
		if strings.Contains(strings.ToUpper(line), "ATTACHED") {
			continue
		}
		if zellijSessionAlive(name) {
			continue
		}
		zellijKillSession(name)
		killed = append(killed, name)
	}
	if len(killed) > 0 {
		zap.L().Debug("pruned dead honey zellij sessions", zap.Strings("sessions", killed))
	}
}

// ptyMuxKillSession removes a honey multiplexer session unconditionally.
func ptyMuxKillSession(name string, useZellij bool) {
	if useZellij {
		zellijKillSession(name)
		return
	}
	tmuxKillSession(name)
}

// ptyMuxKillSessionIfExited removes the mux session only when all panes have exited.
func ptyMuxKillSessionIfExited(name string, useZellij bool) {
	if useZellij {
		if !zellijSessionAlive(name) {
			zellijKillSession(name)
		}
		return
	}
	if tmuxSessionFullyExited(name) {
		tmuxKillSession(name)
	}
}
