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
gomobile bind -target=android/arm64 -trimpath -o "$OUT_DIR/honey.aar" \
  -ldflags="-s -w -extldflags=-Wl,-z,max-page-size=0x4000" ./pkg/mobile

echo "Build complete: $OUT_DIR/honey.aar"
