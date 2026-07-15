#!/bin/sh
set -e

# Stop + disable the service before the package's files are removed, so an
# uninstall doesn't leave a running unit pointing at deleted binaries.
if command -v systemctl >/dev/null 2>&1; then
	systemctl disable --now honey.service >/dev/null 2>&1 || true
fi
