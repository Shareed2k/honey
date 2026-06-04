#!/bin/sh
# internal/ui/facts.sh
# Gathers system facts and outputs as a JSON object

FACT_OS=$(uname -s | tr '[:upper:]' '[:lower:]')
FACT_ARCH=$(uname -m | tr '[:upper:]' '[:lower:]')
FACT_ID="unknown"
FACT_VERSION="unknown"

if [ "$FACT_OS" = "linux" ]; then
    if [ -f /etc/os-release ]; then
        # shellcheck disable=SC1091
        . /etc/os-release
        FACT_ID=$ID
        FACT_VERSION=$VERSION_ID
    fi
elif [ "$FACT_OS" = "darwin" ]; then
    FACT_ID="macos"
    FACT_VERSION=$(sw_vers -productVersion)
fi

FACT_INIT="unknown"
if command -v systemctl >/dev/null 2>&1; then
    FACT_INIT="systemd"
elif [ -f /sbin/openrc-run ]; then
    FACT_INIT="openrc"
elif [ "$FACT_OS" = "darwin" ]; then
    FACT_INIT="launchd"
fi

FACT_PKG="unknown"
if command -v apt-get >/dev/null 2>&1; then
    FACT_PKG="apt"
elif command -v apk >/dev/null 2>&1; then
    FACT_PKG="apk"
elif command -v dnf >/dev/null 2>&1; then
    FACT_PKG="dnf"
elif command -v yum >/dev/null 2>&1; then
    FACT_PKG="yum"
elif command -v brew >/dev/null 2>&1; then
    FACT_PKG="brew"
fi

# Output as JSON (without requiring jq)
cat <<EOF
{
  "os": "${FACT_OS}",
  "arch": "${FACT_ARCH}",
  "id": "${FACT_ID}",
  "version": "${FACT_VERSION}",
  "init": "${FACT_INIT}",
  "pkg_mgr": "${FACT_PKG}"
}
EOF
