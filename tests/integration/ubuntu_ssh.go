//go:build integration

package integration

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	gossh "golang.org/x/crypto/ssh"
)

var (
	ubuntuSSHOnce     sync.Once
	ubuntuSSHHost     string
	ubuntuSSHPort     int
	ubuntuSSHKeyFile  string
	ubuntuSSHStartErr error

	ubuntuSystemdOnce     sync.Once
	ubuntuSystemdHost     string
	ubuntuSystemdPort     int
	ubuntuSystemdKeyFile  string
	ubuntuSystemdStartErr error
)

func startUbuntuSSH(t *testing.T) (host string, port int, keyFile string) {
	t.Helper()
	ubuntuSSHOnce.Do(func() {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			ubuntuSSHStartErr = err
			return
		}
		sshPub, err := gossh.NewPublicKey(pub)
		if err != nil {
			ubuntuSSHStartErr = err
			return
		}
		authorizedKey := string(gossh.MarshalAuthorizedKey(sshPub))

		privPEMBlock, err := gossh.MarshalPrivateKey(priv, "")
		if err != nil {
			ubuntuSSHStartErr = err
			return
		}
		keyPath, err := os.CreateTemp("", "ubuntu_test_ed25519_*")
		if err != nil {
			ubuntuSSHStartErr = err
			return
		}
		keyPath.Close()
		if err := os.WriteFile(keyPath.Name(), pem.EncodeToMemory(privPEMBlock), 0o600); err != nil {
			ubuntuSSHStartErr = err
			return
		}
		ubuntuSSHKeyFile = keyPath.Name()

		ctx := context.Background()

		// Create a temporary context directory for the Docker build
		buildCtx, err := os.MkdirTemp("", "ubuntu-ssh-build-*")
		if err != nil {
			ubuntuSSHStartErr = err
			return
		}

		dockerfile := fmt.Sprintf(`FROM ubuntu:22.04
ENV DEBIAN_FRONTEND=noninteractive
RUN apt-get update && apt-get install -y openssh-server sudo cron && rm -rf /var/lib/apt/lists/*
RUN mkdir /var/run/sshd
RUN useradd -m -s /bin/bash testuser && echo "testuser ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers
RUN mkdir -p /home/testuser/.ssh && echo "%s" > /home/testuser/.ssh/authorized_keys && chown -R testuser:testuser /home/testuser/.ssh && chmod 700 /home/testuser/.ssh && chmod 600 /home/testuser/.ssh/authorized_keys
# Remove systemctl to force the fallback to "service" wrapper
RUN rm -f /bin/systemctl /usr/bin/systemctl
# Mock aws and gcloud
RUN echo '#!/bin/bash\necho "aws-mock $*"' > /usr/local/bin/aws && chmod +x /usr/local/bin/aws
RUN echo '#!/bin/bash\necho "gcp-mock $*"' > /usr/local/bin/gcloud && chmod +x /usr/local/bin/gcloud
CMD ["/usr/sbin/sshd", "-D"]
`, strings.TrimSpace(authorizedKey))

		if err := os.WriteFile(filepath.Join(buildCtx, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
			ubuntuSSHStartErr = err
			return
		}

		req := testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    buildCtx,
				Dockerfile: "Dockerfile",
			},
			ExposedPorts: []string{"22/tcp"},
			WaitingFor:   wait.ForListeningPort("22/tcp").WithStartupTimeout(120 * time.Second),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			ubuntuSSHStartErr = err
			return
		}
		h, err := c.Host(ctx)
		if err != nil {
			ubuntuSSHStartErr = err
			return
		}
		p, err := c.MappedPort(ctx, "22")
		if err != nil {
			ubuntuSSHStartErr = err
			return
		}
		ubuntuSSHHost = h
		ubuntuSSHPort = int(p.Num())
		// Register cleanup for this test run
		if t != nil {
			t.Cleanup(func() {
				_ = c.Terminate(context.Background())
				os.RemoveAll(buildCtx)
			})
		}
	})
	if ubuntuSSHStartErr != nil {
		t.Skipf("start ubuntu ssh skipped: %v", ubuntuSSHStartErr)
	}
	return ubuntuSSHHost, ubuntuSSHPort, ubuntuSSHKeyFile
}

func startUbuntuSystemd(t *testing.T) (host string, port int, keyFile string) {
	t.Helper()
	ubuntuSystemdOnce.Do(func() {
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			ubuntuSystemdStartErr = err
			return
		}
		sshPub, err := gossh.NewPublicKey(pub)
		if err != nil {
			ubuntuSystemdStartErr = err
			return
		}
		authorizedKey := string(gossh.MarshalAuthorizedKey(sshPub))

		privPEMBlock, err := gossh.MarshalPrivateKey(priv, "")
		if err != nil {
			ubuntuSystemdStartErr = err
			return
		}
		keyPath, err := os.CreateTemp("", "ubuntu_systemd_test_ed25519_*")
		if err != nil {
			ubuntuSystemdStartErr = err
			return
		}
		keyPath.Close()
		if err := os.WriteFile(keyPath.Name(), pem.EncodeToMemory(privPEMBlock), 0o600); err != nil {
			ubuntuSystemdStartErr = err
			return
		}
		ubuntuSystemdKeyFile = keyPath.Name()

		ctx := context.Background()

		// Create a temporary context directory for the Docker build
		buildCtx, err := os.MkdirTemp("", "ubuntu-systemd-build-*")
		if err != nil {
			ubuntuSystemdStartErr = err
			return
		}

		dockerfile := fmt.Sprintf(`FROM ubuntu:22.04
ENV container docker
ENV LC_ALL C
ENV DEBIAN_FRONTEND noninteractive

RUN apt-get update && apt-get install -y systemd systemd-sysv openssh-server sudo cron && apt-get clean && rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/*
RUN mkdir -p /var/run/sshd
RUN rm -f /lib/systemd/system/multi-user.target.wants/* \
    /etc/systemd/system/*.wants/* \
    /lib/systemd/system/local-fs.target.wants/* \
    /lib/systemd/system/sockets.target.wants/*udev* \
    /lib/systemd/system/sockets.target.wants/*initctl* \
    /lib/systemd/system/basic.target.wants/* \
    /lib/systemd/system/anaconda.target.wants/* \
    /lib/systemd/system/plymouth* \
    /lib/systemd/system/systemd-update-utmp*

RUN useradd -m -s /bin/bash testuser && echo "testuser ALL=(ALL) NOPASSWD:ALL" >> /etc/sudoers
RUN passwd -d testuser
RUN mkdir -p /home/testuser/.ssh && echo "%s" > /home/testuser/.ssh/authorized_keys && chown -R testuser:testuser /home/testuser/.ssh && chmod 700 /home/testuser/.ssh && chmod 600 /home/testuser/.ssh/authorized_keys
RUN sed -i 's/^#UsePAM yes/UsePAM no/' /etc/ssh/sshd_config || true
RUN sed -i 's/^UsePAM yes/UsePAM no/' /etc/ssh/sshd_config || true
RUN ssh-keygen -A
RUN systemctl enable ssh cron

VOLUME [ "/sys/fs/cgroup" ]
CMD ["/lib/systemd/systemd"]
`, strings.TrimSpace(authorizedKey))

		if err := os.WriteFile(filepath.Join(buildCtx, "Dockerfile"), []byte(dockerfile), 0o644); err != nil {
			ubuntuSystemdStartErr = err
			return
		}

		req := testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    buildCtx,
				Dockerfile: "Dockerfile",
			},
			ExposedPorts: []string{"22/tcp"},
			HostConfigModifier: func(hc *container.HostConfig) {
				hc.Privileged = true
				hc.Binds = []string{"/sys/fs/cgroup:/sys/fs/cgroup:rw"}
				hc.CgroupnsMode = "host"
			},
			WaitingFor: wait.ForListeningPort("22/tcp").WithStartupTimeout(120 * time.Second),
		}
		c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
			ContainerRequest: req,
			Started:          true,
		})
		if err != nil {
			ubuntuSystemdStartErr = err
			return
		}
		h, err := c.Host(ctx)
		if err != nil {
			ubuntuSystemdStartErr = err
			return
		}
		p, err := c.MappedPort(ctx, "22")
		if err != nil {
			ubuntuSystemdStartErr = err
			return
		}
		ubuntuSystemdHost = h
		ubuntuSystemdPort = int(p.Num())
		// Register cleanup for this test run
		if t != nil {
			t.Cleanup(func() {
				_ = c.Terminate(context.Background())
				os.RemoveAll(buildCtx)
			})
		}
	})
	if ubuntuSystemdStartErr != nil {
		t.Skipf("start ubuntu systemd skipped: %v", ubuntuSystemdStartErr)
	}
	return ubuntuSystemdHost, ubuntuSystemdPort, ubuntuSystemdKeyFile
}
