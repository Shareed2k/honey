package proxmoxprovider

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/Telmate/proxmox-api-go/proxmox"

	"github.com/shareed2k/honey/internal/hostexec"
	"github.com/shareed2k/honey/internal/hosts"
	"github.com/shareed2k/honey/internal/sshclient"
)

type qemuGuestClient struct {
	c   *proxmox.Client
	vmr *proxmox.VmRef
}

func vmRefFromRecordQEMU(r hosts.Record) (*proxmox.VmRef, error) {
	node := strings.TrimSpace(r.Meta["node"])
	vmid64, err := strconv.ParseUint(strings.TrimSpace(r.Meta["vmid"]), 10, 32)
	if err != nil || node == "" {
		return nil, errProxmoxMeta
	}
	vmr := proxmox.NewVmRef(proxmox.GuestID(vmid64))
	vmr.SetNode(node)
	var gt proxmox.GuestType
	if err := gt.Parse("qemu"); err != nil {
		return nil, err
	}
	vmr.SetVmType(gt)
	return vmr, nil
}

func dialPVEQEMU(b hostexec.ProxmoxBackendRuntime, r hosts.Record) (*qemuGuestClient, error) {
	c, err := dialTelmate(context.Background(), b)
	if err != nil {
		return nil, err
	}
	vmr, err := vmRefFromRecordQEMU(r)
	if err != nil {
		return nil, err
	}
	if err := c.CheckVmRef(context.Background(), vmr); err != nil {
		return nil, fmt.Errorf("proxmox qemu: %w", err)
	}
	return &qemuGuestClient{c: c, vmr: vmr}, nil
}

func (q *qemuGuestClient) Run(cmd string) ([]byte, error) {
	ctx := context.Background()
	params := map[string]interface{}{
		"command": []string{"/bin/sh", "-c", cmd},
	}
	res, err := q.c.QemuAgentExec(ctx, q.vmr, params)
	if err != nil {
		return nil, fmt.Errorf("proxmox qemu guest-agent exec: %w (ensure qemu-guest-agent is installed and running)", err)
	}
	pid, _ := res["pid"].(float64)
	if pid == 0 {
		return nil, fmt.Errorf("proxmox qemu guest-agent: no pid in response: %#v", res)
	}
	deadline := time.Now().Add(5 * time.Minute)
	for time.Now().Before(deadline) {
		st, err := q.c.GetExecStatus(ctx, q.vmr, fmt.Sprintf("%.0f", pid))
		if err != nil {
			return nil, err
		}
		exited, _ := st["exited"].(float64)
		if exited == 1 {
			out, _ := st["out-data"].(string)
			errOut, _ := st["err-data"].(string)
			exitCode, _ := st["exitcode"].(float64)
			if exitCode != 0 {
				return nil, fmt.Errorf("proxmox qemu guest-agent: exit %.0f stderr=%s stdout=%s", exitCode, errOut, out)
			}
			return []byte(out), nil
		}
		time.Sleep(150 * time.Millisecond)
	}
	return nil, fmt.Errorf("proxmox qemu guest-agent: timeout waiting for pid %.0f", pid)
}

func (q *qemuGuestClient) RunWithStreams(cmd string, stdin io.Reader, stdout, _ io.Writer) error {
	if stdin != nil {
		b, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		if len(bytes.TrimSpace(b)) > 0 {
			return errProxmoxStreams
		}
	}
	out, err := q.Run(cmd)
	if err != nil {
		return err
	}
	if stdout != nil {
		_, _ = stdout.Write(out)
	}
	return nil
}

func (q *qemuGuestClient) Upload(string, string) error   { return errProxmoxFileOps }
func (q *qemuGuestClient) Download(string, string) error { return errProxmoxFileOps }
func (q *qemuGuestClient) ListRemoteDir(string) ([]hostexec.RemoteFileEntry, error) {
	return nil, errProxmoxFileOps
}

func (q *qemuGuestClient) StatRemote(string) (hostexec.RemoteFileEntry, error) {
	return hostexec.RemoteFileEntry{}, errProxmoxFileOps
}
func (q *qemuGuestClient) MkdirAllRemote(string) error     { return errProxmoxFileOps }
func (q *qemuGuestClient) RemoveRemote(string, bool) error { return errProxmoxFileOps }
func (q *qemuGuestClient) Close() error                    { return nil }

type hybridQEMUClient struct {
	ssh  hostexec.HostClient
	qemu *qemuGuestClient
}

func dialHybridQEMU(b hostexec.ProxmoxBackendRuntime, user string, r hosts.Record) (hostexec.HostClient, error) {
	ip := strings.TrimSpace(r.PrimaryIP)
	if ip == "" {
		return nil, fmt.Errorf("proxmox hybrid: no primary IP for SSH file transfer")
	}

	sshClient, err := sshclient.DialHoneyClient(user, ip, 0, "")
	if err != nil {
		return nil, err
	}
	qc, err := dialPVEQEMU(b, r)
	if err != nil {
		_ = sshClient.Close()
		return nil, err
	}
	return &hybridQEMUClient{ssh: sshClient, qemu: qc}, nil
}

func (h *hybridQEMUClient) Run(cmd string) ([]byte, error) {
	return h.qemu.Run(cmd)
}

func (h *hybridQEMUClient) RunWithStreams(cmd string, stdin io.Reader, stdout, stderr io.Writer) error {
	return h.qemu.RunWithStreams(cmd, stdin, stdout, stderr)
}

func (h *hybridQEMUClient) Upload(a, b string) error   { return h.ssh.Upload(a, b) }
func (h *hybridQEMUClient) Download(a, b string) error { return h.ssh.Download(a, b) }
func (h *hybridQEMUClient) ListRemoteDir(p string) ([]hostexec.RemoteFileEntry, error) {
	return h.ssh.ListRemoteDir(p)
}

func (h *hybridQEMUClient) StatRemote(p string) (hostexec.RemoteFileEntry, error) {
	return h.ssh.StatRemote(p)
}
func (h *hybridQEMUClient) MkdirAllRemote(p string) error         { return h.ssh.MkdirAllRemote(p) }
func (h *hybridQEMUClient) RemoveRemote(p string, rec bool) error { return h.ssh.RemoveRemote(p, rec) }

func (h *hybridQEMUClient) Close() error {
	_ = h.qemu.Close()
	return h.ssh.Close()
}
