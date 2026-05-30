package cli

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
)

func runTunMode(ctx context.Context, socksHost string, socksPort int, hostName string, sshIPs []string, extraBypasses []string) error {
	bin, err := findTun2ProxyBin()
	if err != nil {
		return err
	}

	args := []string{
		"--proxy", fmt.Sprintf("socks5://%s:%d", socksHost, socksPort),
		"--setup",
		"--dns", "over-tcp",
		"--dns-addr", "8.8.8.8",
		"--verbosity", "warn",
	}
	for _, ip := range sshIPs {
		if ip != "" {
			args = append(args, "--bypass", ip+"/32")
		}
	}
	for _, cidr := range extraBypasses {
		if cidr != "" {
			args = append(args, "--bypass", cidr)
		}
	}

	fmt.Printf("Starting tun2proxy (VPN exit via %s)...\nPress Ctrl+C to stop.\n", hostName)
	// #nosec G204 -- bin is resolved via LookPath or a hardcoded trusted path.
	cmd := exec.Command(bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("tun2proxy start: %w", err)
	}

	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case <-ctx.Done():
		// Send SIGTERM so tun2proxy can restore routes before exiting.
		_ = cmd.Process.Signal(syscall.SIGTERM)
		<-done
		return nil
	case err := <-done:
		if err != nil {
			return fmt.Errorf("tun2proxy: %w", err)
		}
		return nil
	}
}

func findTun2ProxyBin() (string, error) {
	for _, name := range []string{"tun2proxy-bin", "tun2proxy"} {
		if p, err := exec.LookPath(name); err == nil {
			return p, nil
		}
	}
	// Homebrew on Apple Silicon
	if _, err := os.Stat("/opt/homebrew/bin/tun2proxy-bin"); err == nil {
		return "/opt/homebrew/bin/tun2proxy-bin", nil
	}
	return "", fmt.Errorf("tun2proxy not found; install with: brew install tun2proxy")
}
