# Pod-side bridge: listen on loopback, multiplex accepted TCP connections over kubectl exec
# stdin/stdout using binary frames (see k8s_kv_exec_bridge.go). First stdout line is "READY <port>\\n".
# Requires Python 3.6+.

import socket
import struct
import sys
import threading
from typing import Optional

TYPE_DATA = 0
TYPE_CLOSE = 1
TYPE_OPEN = 2

_HDR = struct.Struct("!BII")

def _read_exact(r, n: int) -> Optional[bytes]:
    buf = bytearray()
    while len(buf) < n:
        chunk = r.read(n - len(buf))
        if not chunk:
            return None
        buf.extend(chunk)
    return bytes(buf)


def _write_frame_raw(outb, lock: threading.Lock, typ: int, cid: int, payload: bytes = b"") -> None:
    if len(payload) > 16_777_216:
        raise ValueError("frame too large")
    pkt = _HDR.pack(typ, cid, len(payload)) + payload
    with lock:
        outb.write(pkt)
        outb.flush()


def main() -> None:
    ln = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
    ln.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
    ln.bind(("127.0.0.1", 0))
    ln.listen(16)
    port = ln.getsockname()[1]

    sys.stdout.write("READY %d\n" % port)
    sys.stdout.flush()
    outb = sys.stdout.buffer
    out_lock = threading.Lock()

    socks: dict[int, socket.socket] = {}
    socks_lock = threading.Lock()
    next_cid = 1

    def pump_client(cid: int, sock: socket.socket) -> None:
        try:
            while True:
                data = sock.recv(65536)
                if not data:
                    break
                _write_frame_raw(outb, out_lock, TYPE_DATA, cid, data)
        except (BrokenPipeError, ConnectionResetError, OSError):
            pass
        finally:
            with socks_lock:
                socks.pop(cid, None)
            try:
                sock.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            try:
                sock.close()
            except OSError:
                pass
            try:
                _write_frame_raw(outb, out_lock, TYPE_CLOSE, cid, b"")
            except (BrokenPipeError, OSError):
                pass

    def accept_loop() -> None:
        nonlocal next_cid
        while True:
            try:
                client, _ = ln.accept()
            except OSError:
                return
            with socks_lock:
                cid = next_cid
                next_cid += 1
                socks[cid] = client
            try:
                _write_frame_raw(outb, out_lock, TYPE_OPEN, cid, b"")
            except (BrokenPipeError, OSError):
                try:
                    client.close()
                except OSError:
                    pass
                with socks_lock:
                    socks.pop(cid, None)
                continue
            threading.Thread(target=pump_client, args=(cid, client), daemon=True).start()

    acc_thread = threading.Thread(target=accept_loop, daemon=True)
    acc_thread.start()

    stdin = sys.stdin.buffer
    while True:
        hdr = _read_exact(stdin, _HDR.size)
        if hdr is None or len(hdr) < _HDR.size:
            break
        typ, cid, ln0 = _HDR.unpack(hdr)
        payload = b""
        if ln0:
            payload = _read_exact(stdin, ln0)
            if payload is None or len(payload) != ln0:
                break
        if typ == TYPE_DATA:
            with socks_lock:
                s = socks.get(cid)
            if s is None:
                continue
            try:
                s.sendall(payload)
            except OSError:
                pass
        elif typ == TYPE_CLOSE:
            with socks_lock:
                s = socks.pop(cid, None)
            if s is not None:
                try:
                    s.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
                try:
                    s.close()
                except OSError:
                    pass
        else:
            # Unknown type from operator; ignore
            pass

    try:
        ln.close()
    except OSError:
        pass
    with socks_lock:
        for s in list(socks.values()):
            try:
                s.close()
            except OSError:
                pass
        socks.clear()

if __name__ == "__main__":
    main()
