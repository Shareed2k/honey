#!/usr/bin/env bash
# Build honey for Android using gomobile
# Output lands in android/app/libs/honey.aar for the Gradle project.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

OUT_DIR="android/app/libs"
mkdir -p "$OUT_DIR"

echo "Ensuring gomobile is installed..."
go install golang.org/x/mobile/cmd/gomobile@latest
gomobile init

echo "Building AAR for Android..."
# We compile the pkg/mobile package into an AAR library.
gomobile bind -target=android/arm64 -o "$OUT_DIR/honey.aar" ./pkg/mobile

echo "Build complete: $OUT_DIR/honey.aar"
