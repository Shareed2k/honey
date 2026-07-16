#!/usr/bin/env bash
# Build honey for Android using gomobile
# Output lands in android/app/libs/honey.aar for the Gradle project.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

OUT_DIR="android/app/libs"
mkdir -p "$OUT_DIR"

echo "Ensuring gomobile is installed..."
if ! command -v gomobile >/dev/null 2>&1; then
    go install golang.org/x/mobile/cmd/gomobile@latest
    gomobile init
fi

echo "Building AAR for Android..."
# We compile the pkg/mobile package into an AAR library.
# -androidapi 26 matches the app's minSdk. Lower values (gomobile defaults to 16)
# fail against NDK r27 ("unsupported API version 16") and lack getifaddrs, which
# the netstatus cgo dependency needs (declared on Android API >= 24).
# -checklinkname=0: wlynxg/anet (via go-libp2p) uses //go:linkname net.zoneCache,
# which the Go 1.23+ linker rejects by default (per anet README, required on Android).
gomobile bind -target=android/arm64 -androidapi 26 -trimpath -o "$OUT_DIR/honey.aar" \
  -ldflags="-s -w -checklinkname=0 -extldflags=-Wl,-z,max-page-size=0x4000" ./pkg/mobile

echo "Build complete: $OUT_DIR/honey.aar"
