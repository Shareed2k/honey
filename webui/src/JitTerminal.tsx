import { useEffect, useRef, useState } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';

// noticeDisplayMs is how long a transient server notice (e.g. a dropped
// collaborate keystroke, NEW-17) stays visible before auto-clearing.
const noticeDisplayMs = 4000;

export type JitTerminalProps = {
  code: string;
  /**
   * True for a "watch" live-terminal grant: the guest joins an operator's
   * live session read-only. The server is the actual enforcement — the pty
   * bridge never wires a guest's stdin frame into the shared session at all;
   * tmux's own `-r` attach is defense in depth on top of that, not the
   * primary control (tmux still permits its own small set of read-only
   * commands to a `-r` client) — this just stops the client from sending
   * keystrokes that would silently go nowhere.
   */
  readOnly?: boolean;
};

// JitTerminal is the unauthenticated (no honey session, no bearer token)
// browser terminal for a redeemed JIT share link. It is modeled directly on
// the WebSocket wiring in TerminalModal.tsx (same hello/resize/control-frame
// wire protocol as /ws/ssh) but dials the code-scoped redeem endpoint, which
// needs no token: the code itself is the credential.
export function JitTerminal({ code, readOnly = false }: JitTerminalProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  // A transient, non-fatal delivery notice (NEW-17) — rendered as a banner
  // OUTSIDE the terminal buffer, never via term.writeln: writing it into the
  // pane would desync the guest's own mirror of the operator's real content
  // until the next redraw painted over it.
  const [notice, setNotice] = useState<string | null>(null);

  useEffect(() => {
    const el = containerRef.current;
    if (!el) {
      return undefined;
    }

    const term = new Terminal({
      cursorBlink: true,
      fontSize: 14,
      theme: {
        background: '#0f1115',
        foreground: '#e8e8e8',
        cursor: '#e8e8e8',
      },
    });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();

    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const ws = new WebSocket(
      `${proto}//${window.location.host}/api/v1/jit/redeem/${encodeURIComponent(code)}/terminal`,
    );
    ws.binaryType = 'arraybuffer';

    let sawServerError = false;

    ws.onopen = () => {
      fit.fit();
      ws.send(JSON.stringify({ cols: term.cols, rows: term.rows }));
      requestAnimationFrame(() => {
        fit.fit();
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
        }
      });
    };

    ws.onmessage = (ev) => {
      if (typeof ev.data === 'string') {
        try {
          const j = JSON.parse(ev.data) as { closed?: boolean; error?: string; notice?: string };
          if (j.notice) {
            // NEW-17: a transient notice (e.g. a dropped keystroke) is NOT a
            // fatal error — render it out-of-band and, critically, do NOT
            // set sawServerError: a real disconnect reason arriving later
            // must still be shown, not silently suppressed by an earlier
            // notice.
            setNotice(j.notice);
            window.setTimeout(() => setNotice((cur) => (cur === j.notice ? null : cur)), noticeDisplayMs);
          }
          if (j.error) {
            sawServerError = true;
            term.writeln(`\r\n\x1b[31m${j.error}\x1b[0m`);
          }
          if (j.closed) {
            ws.close();
          }
        } catch {
          /* ignore malformed control frame */
        }
        return;
      }
      term.write(new Uint8Array(ev.data as ArrayBuffer));
    };

    ws.onerror = () => {
      if (!sawServerError) {
        term.writeln('\r\n\x1b[31m[websocket error]\x1b[0m');
      }
    };

    ws.onclose = (ev) => {
      if (!sawServerError && ev.code !== 1000) {
        const hint =
          ev.reason?.trim() ||
          (ev.code ? `code ${ev.code}` : 'connection closed before the server replied');
        term.writeln(`\r\n\x1b[31m${hint}\x1b[0m`);
      }
      term.writeln('\r\n\x1b[33m[disconnected]\x1b[0m');
    };

    const enc = new TextEncoder();
    term.onData((data) => {
      // A watch grant is read-only: the server never wires this frame into
      // the shared session anyway (belt and braces alongside tmux's own `-r`
      // attach), so don't even send it.
      //
      // For a collaborate grant, note that xterm answers terminal queries
      // (Device Attributes, Cursor Position Report, OSC color queries)
      // automatically, through this SAME callback, whenever the mirrored
      // pane output contains one — there is no client-side hook to suppress
      // just those replies. The server-side relay is what filters them out
      // before they ever reach the pane (see terminalReportFilter in
      // internal/webserver/pty_proxy.go); this client never needs to (and,
      // being guest-controlled, could not be trusted to).
      if (readOnly || ws.readyState !== WebSocket.OPEN) {
        return;
      }
      ws.send(enc.encode(data));
    });

    const onResize = () => {
      fit.fit();
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
      }
    };
    window.addEventListener('resize', onResize);
    // Also refit when the container itself resizes (e.g. opening full-page or a
    // layout change), not just the window — keeps the terminal filling its box.
    const ro = new ResizeObserver(() => onResize());
    ro.observe(el);

    return () => {
      ro.disconnect();
      window.removeEventListener('resize', onResize);
      ws.close();
      term.dispose();
    };
  }, [code, readOnly]);

  // Fill the parent so callers control the size (a small card box or the full
  // viewport); the ResizeObserver above keeps xterm fitted to whatever that is.
  // The notice banner is a SIBLING of the xterm container, not a child of it —
  // xterm.js owns containerRef's contents exclusively.
  return (
    <div style={{ width: '100%', height: '100%', position: 'relative' }}>
      <div ref={containerRef} style={{ width: '100%', height: '100%' }} />
      {notice ? (
        <div
          role="status"
          style={{
            position: 'absolute',
            top: 8,
            right: 8,
            maxWidth: '70%',
            padding: '6px 10px',
            borderRadius: 4,
            fontSize: 12,
            background: 'rgba(120, 53, 15, 0.92)',
            color: '#fde68a',
            pointerEvents: 'none',
          }}
        >
          {notice}
        </div>
      ) : null}
    </div>
  );
}
