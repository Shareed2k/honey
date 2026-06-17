# Guest-side TCP dial bridge for TrueNAS API shell port-forward.
# Operator sends TYPE_OPEN; guest dials HONEY_REMOTE_HOST:HONEY_REMOTE_PORT and multiplexes streams.
# First stdout line is "READY 0\n". Requires Python 3.6+.

import os
import socket
import struct
import sys
import threading
from typing import Optional, Tuple

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


def _remote_target() -> Tuple[str, int]:
    host = os.environ.get("HONEY_REMOTE_HOST", "127.0.0.1").strip() or "127.0.0.1"
    port_s = os.environ.get("HONEY_REMOTE_PORT", "").strip()
    if not port_s:
        raise ValueError("HONEY_REMOTE_PORT not set")
    port = int(port_s)
    if port <= 0 or port > 65535:
        raise ValueError("invalid HONEY_REMOTE_PORT")
    return host, port


def _pump_remote(cid: int, sock: socket.socket, outb, out_lock: threading.Lock) -> None:
    try:
        while True:
            data = sock.recv(65536)
            if not data:
                break
            _write_frame_raw(outb, out_lock, TYPE_DATA, cid, data)
    except (BrokenPipeError, ConnectionResetError, OSError):
        pass
    finally:
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


def main() -> None:
    remote_host, remote_port = _remote_target()
    sys.stdout.write("READY 1\n")
    sys.stdout.flush()
    outb = sys.stdout.buffer
    out_lock = threading.Lock()

    socks: dict[int, socket.socket] = {}
    socks_lock = threading.Lock()

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

        if typ == TYPE_OPEN:
            try:
                remote = socket.create_connection((remote_host, remote_port), timeout=15)
            except OSError:
                try:
                    _write_frame_raw(outb, out_lock, TYPE_CLOSE, cid, b"")
                except (BrokenPipeError, OSError):
                    pass
                continue
            with socks_lock:
                old = socks.pop(cid, None)
                if old is not None:
                    try:
                        old.close()
                    except OSError:
                        pass
                socks[cid] = remote
            threading.Thread(
                target=_pump_remote, args=(cid, remote, outb, out_lock), daemon=True
            ).start()
        elif typ == TYPE_DATA:
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

    with socks_lock:
        for s in list(socks.values()):
            try:
                s.close()
            except OSError:
                pass
        socks.clear()


if __name__ == "__main__":
    main()
