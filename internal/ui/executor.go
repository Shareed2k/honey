package ui

import (
	"fmt"
	"honey/internal/hosts"
)

type HostClient interface {
	Run(cmd string) ([]byte, error)
	Upload(localPath, remotePath string) error
	Download(remotePath, localPath string) error
	Close() error
}

type Executor interface {
	Dial(user string, r hosts.Record) (HostClient, error)
	RunInteractive(user string, r hosts.Record) error
}

// defaultSSHExecutor implements standard SSH execution using DialHoneyClient.
type defaultSSHExecutor struct{}

func (e defaultSSHExecutor) Dial(user string, r hosts.Record) (HostClient, error) {
	return DialHoneyClient(user, r.PrimaryIP)
}

func (e defaultSSHExecutor) RunInteractive(user string, r hosts.Record) error {
	return runSSHInteractive(user, r.PrimaryIP)
}

var DefaultExecutor Executor = defaultSSHExecutor{}

// GetExecutor returns the appropriate Executor for a host record.
func GetExecutor(r hosts.Record) Executor {
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" {
		// k8s pod executor will go here
		return k8sPodExecutor{}
	}
	return DefaultExecutor
}

// FormatTargetForDryRun returns a string describing how the target will be connected to.
func FormatTargetForDryRun(r hosts.Record) string {
	if r.Provider == "k8s" && r.Meta["kind"] == "pod" {
		return fmt.Sprintf("k8s_exec(ns=%s pod=%s)", r.Meta["namespace"], r.Meta["pod_name"])
	}
	return fmt.Sprintf("ip=%s", r.PrimaryIP)
}

type k8sPodExecutor struct{}
