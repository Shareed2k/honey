#!/usr/bin/env bash
# Package each production plugin as a standalone .tar.gz for release artifacts.
# Output: dist/plugins/honey-plugin-<name>-wasip1-wasm.tar.gz
set -euo pipefail

PLUGINS_DIR="${1:-plugins}"
OUT_DIR="${2:-dist/plugins}"

mkdir -p "$OUT_DIR"

count=0
for d in "$PLUGINS_DIR"/*/; do
  name=$(basename "$d")
  yaml="$d/plugin.yaml"
  wasm="$d/plugin.wasm"
  if [[ ! -f "$yaml" || ! -f "$wasm" ]]; then
    echo "SKIP $name: missing plugin.yaml or plugin.wasm" >&2
    continue
  fi
  out="$OUT_DIR/honey-plugin-${name}-wasip1-wasm.tar.gz"
  tar -czf "$out" -C "$d" plugin.yaml plugin.wasm
  echo "Packaged $out"
  count=$((count + 1))
done

echo "Done: $count plugin(s) packaged in $OUT_DIR"
