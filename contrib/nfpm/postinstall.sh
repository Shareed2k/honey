#!/bin/sh
set -e

# Create an unprivileged system user for the honey.service (no login shell,
# no home beyond its state dir). Idempotent — skip if it already exists.
if ! getent passwd honey >/dev/null 2>&1; then
	useradd --system --no-create-home --home-dir /var/lib/honey \
		--shell /usr/sbin/nologin --comment "honey service" honey 2>/dev/null ||
		adduser --system --no-create-home --home /var/lib/honey \
			--shell /usr/sbin/nologin honey 2>/dev/null || true
fi

# NOTE: we deliberately DO NOT add honey to the docker group here. That group
# is effectively root-equivalent (a container can mount the host filesystem),
# and a package must not silently grant its service account root-equivalent
# access on install. honey's core features (search, ssh exec, web, mesh) need
# no docker access. If you want docker: steps / docker plugins, opt in
# explicitly — see the message printed below.

# State dir + config ownership. config.yaml may hold secrets (mesh.private_key,
# tokens) → readable by honey but not world.
install -d -o honey -g honey -m 0750 /var/lib/honey 2>/dev/null || mkdir -p /var/lib/honey
if [ -f /etc/honey/config.yaml ]; then
	chown root:honey /etc/honey/config.yaml 2>/dev/null || true
	chmod 0640 /etc/honey/config.yaml 2>/dev/null || true
fi

if command -v systemctl >/dev/null 2>&1; then
	systemctl daemon-reload >/dev/null 2>&1 || true
fi

cat <<'MSG'
honey installed (runs as the unprivileged 'honey' user).
  1. edit  /etc/honey/config.yaml
  2. start: systemctl enable --now honey
  3. logs:  journalctl -u honey -f

Optional — docker: steps / docker plugins need honey to
reach the Docker socket. This grants root-equivalent access, so it is opt-in:
  usermod -aG docker honey && systemctl restart honey
(the arch-matched honey-plugin-init shim is already in /usr/bin).
MSG
