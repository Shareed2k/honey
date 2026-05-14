# Minimal KV HTTP server for honey cue-exec kv_tunnel on Kubernetes pods (ephemeral debug container).
# Invoked as: python3 k8s_kv_pod_server.py <listen_port> <token>
import sys
import time
from http.server import BaseHTTPRequestHandler, HTTPServer

if len(sys.argv) != 3:
    print("usage: python3 k8s_kv_pod_server.py <port> <token>", file=sys.stderr)
    sys.exit(2)

PORT = int(sys.argv[1])
TOKEN = sys.argv[2]
MAX_KEY = 256
MAX_VAL = 65536
DEFAULT_TTL = 1800.0
_store: dict[str, tuple[str, float]] = {}


def _auth(h):
    auth = h.get("Authorization", "")
    if auth.lower().startswith("bearer "):
        got = auth[7:].strip()
        return got == TOKEN
    return h.get("X-Honey-Kv-Token", "").strip() == TOKEN


class Handler(BaseHTTPRequestHandler):
    def log_message(self, *_args):
        return

    def _bad(self, code, msg):
        self.send_response(code)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.end_headers()
        self.wfile.write(msg.encode())

    def do_GET(self):
        if not _auth(self.headers):
            return self._bad(401, "unauthorized")
        if self.path == "/v1/kv/__health":
            self.send_response(200)
            self.end_headers()
            return
        if not self.path.startswith("/v1/kv/"):
            return self._bad(404, "not found")
        key = self.path[len("/v1/kv/") :].strip()
        if not key or "/" in key or len(key) > MAX_KEY:
            return self._bad(400, "bad key")
        now = time.monotonic()
        ent = _store.get(key)
        if ent is None or ent[1] < now:
            if ent is not None:
                _store.pop(key, None)
            return self._bad(404, "not found")
        self.send_response(200)
        self.send_header("Content-Type", "text/plain; charset=utf-8")
        self.end_headers()
        self.wfile.write(ent[0].encode())

    def do_PUT(self):
        if not _auth(self.headers):
            return self._bad(401, "unauthorized")
        if not self.path.startswith("/v1/kv/"):
            return self._bad(404, "not found")
        key = self.path[len("/v1/kv/") :].strip()
        if not key or "/" in key or len(key) > MAX_KEY:
            return self._bad(400, "bad key")
        ln = int(self.headers.get("Content-Length", "0"))
        if ln < 0 or ln > MAX_VAL:
            return self._bad(400, "bad length")
        body = self.rfile.read(ln)
        if len(body) > MAX_VAL:
            return self._bad(400, "value too long")
        exp = time.monotonic() + DEFAULT_TTL
        _store[key] = (body.decode("utf-8", errors="replace"), exp)
        self.send_response(204)
        self.end_headers()

    def do_DELETE(self):
        if not _auth(self.headers):
            return self._bad(401, "unauthorized")
        if not self.path.startswith("/v1/kv/"):
            return self._bad(404, "not found")
        key = self.path[len("/v1/kv/") :].strip()
        if not key or "/" in key or len(key) > MAX_KEY:
            return self._bad(400, "bad key")
        _store.pop(key, None)
        self.send_response(204)
        self.end_headers()


def main():
    now = time.monotonic()
    for k, (_, exp) in list(_store.items()):
        if exp < now:
            _store.pop(k, None)
    srv = HTTPServer(("127.0.0.1", PORT), Handler)
    srv.serve_forever()


if __name__ == "__main__":
    main()
