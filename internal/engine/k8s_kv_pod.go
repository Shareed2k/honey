package engine

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	_ "embed"
)

//go:embed k8s_kv_pod_server.py
var k8sKVPodServerPy string

// ShellSingleQuoted ...
func ShellSingleQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// wrapK8sPodKVShell wraps innerCommand so an in-pod Python KV server starts before it runs (same /v1/kv API as SSH kv_tunnel).
// Used for non–recipe-scoped kv_tunnel on Kubernetes. Recipe-scoped cue-exec uses EnsureK8sExecBridgeEnv instead.
// Requires python3 in the debug container image (see recipe.defaults.k8s_debug_image).
func wrapK8sPodKVShell(innerCommand string) (string, error) {
	innerCommand = strings.TrimSpace(innerCommand)
	if innerCommand == "" {
		return "", nil
	}
	tok := make([]byte, 24)
	if _, err := rand.Read(tok); err != nil {
		return "", err
	}
	token := hex.EncodeToString(tok)
	pyB64 := base64.StdEncoding.EncodeToString([]byte(k8sKVPodServerPy))
	cmdB64 := base64.StdEncoding.EncodeToString([]byte(innerCommand))

	// Decoded by sh on the pod (not single-quoted outer), so $PORT / $PYB64 / etc expand correctly.
	// Run inner via a temp script (not sh -c "$(base64)" — command substitution strips trailing newlines).
	// Send python stdio to /dev/null so the background server does not hold the kubectl exec SPDY streams open
	// (otherwise client-go StreamWithContext can hang after the inner shell exits).
	// Do not use exec for inner: keep this shell so EXIT trap runs cleanup and stops python3.
	bootstrap := fmt.Sprintf(`set -e
if ! command -v python3 >/dev/null 2>&1; then
  echo "kv_tunnel on kubernetes requires python3 in the debug container image (set recipe.defaults.k8s_debug_image, e.g. nicolaka/netshoot:latest)" >&2
  exit 127
fi
PORT=$((17379 + RANDOM %% 601))
TOKEN='%s'
export HONEY_KV_URL=http://127.0.0.1:$PORT
export HONEY_KV_TOKEN="$TOKEN"
PYB64='%s'
INB64='%s'
cleanup() {
  if test -n "${KV_PID:-}" && kill -0 "$KV_PID" 2>/dev/null; then kill "$KV_PID" 2>/dev/null || true; fi
  rm -f "/tmp/honey-kv-$$.py" "/tmp/honey-inner-$$.sh" 2>/dev/null || true
}
trap cleanup EXIT INT HUP TERM
printf %%s "$PYB64" | base64 -d > "/tmp/honey-kv-$$.py"
python3 "/tmp/honey-kv-$$.py" "$PORT" "$TOKEN" >/dev/null 2>&1 &
KV_PID=$!
i=0
while ! kill -0 "$KV_PID" 2>/dev/null; do
  i=$((i+1))
  if test "$i" -gt 100; then echo "kv_tunnel: kv server failed to start" >&2; exit 1; fi
  sleep 0.05
done
j=0
while test "$j" -lt 60; do
  if command -v nc >/dev/null 2>&1 && nc -z 127.0.0.1 "$PORT" 2>/dev/null; then break; fi
  j=$((j+1))
  sleep 0.05
done
sleep 0.05
printf %%s "$INB64" | base64 -d > "/tmp/honey-inner-$$.sh"
chmod 700 "/tmp/honey-inner-$$.sh" 2>/dev/null || true
sh -e "/tmp/honey-inner-$$.sh"
ec=$?
exit "$ec"
`, token, pyB64, cmdB64)

	outer := base64.StdEncoding.EncodeToString([]byte(bootstrap))
	// Avoid "exec sh" here: run bootstrap in a normal shell so the session ends cleanly when it exits.
	return fmt.Sprintf(`printf %%s %s|base64 -d|sh`, ShellSingleQuoted(outer)), nil
}
