#!/usr/bin/env bash
# Build honey for Android (arm64). Requires Go 1.26+.
# Output lands in android/app/src/main/assets/ for the Gradle project.
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT_DIR="$REPO_ROOT/android/app/src/main/assets"
mkdir -p "$OUT_DIR"

VERSION="$(git -C "$REPO_ROOT" describe --tags --always 2>/dev/null || echo "dev")"
LDFLAGS="-s -w -X main.version=$VERSION"

echo "Building honey for android/arm64 (version: $VERSION)..."
GOOS=android GOARCH=arm64 CGO_ENABLED=0 \
  go build -tags mobile -trimpath -ldflags="$LDFLAGS" \
  -o "$OUT_DIR/honey-arm64" \
  "$REPO_ROOT/cmd/honey"

echo "Built: $OUT_DIR/honey-arm64 ($(du -h "$OUT_DIR/honey-arm64" | cut -f1))"
