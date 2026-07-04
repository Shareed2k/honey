#!/usr/bin/env bash
# Render apisix.yaml (template) into apisix.local.yaml by inlining the PEMs from
# ./certs. APISIX standalone requires cert material inline in the ssl object.
set -euo pipefail

DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CERTS="$DIR/certs"
[ -f "$CERTS/server.crt" ] || { echo "run ./gen-certs.sh first (no certs/)"; exit 1; }

# Indent each PEM to sit under its block scalar. cert:/key: are at 4 spaces
# (content 6); client.ca: is one level deeper at 6 spaces (content 8).
indent() { sed "s/^/$(printf '%*s' "$1" '')/" "$2"; }

python3 - "$DIR/apisix.yaml" "$DIR/apisix.local.yaml" \
  "$(indent 6 "$CERTS/server.crt")" "$(indent 6 "$CERTS/server.key")" "$(indent 8 "$CERTS/ca.crt")" <<'PY'
import sys
tmpl, out, server_crt, server_key, client_ca = sys.argv[1], sys.argv[2], sys.argv[3], sys.argv[4], sys.argv[5]
s = open(tmpl).read()
s = s.replace("__SERVER_CRT__", server_crt).replace("__SERVER_KEY__", server_key).replace("__CLIENT_CA__", client_ca)
open(out, "w").write(s)
print("wrote", out)
PY
