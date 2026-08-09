import { useEffect, useRef } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';

export type JitTerminalProps = {
  code: string;
};

// JitTerminal is the unauthenticated (no honey session, no bearer token)
// browser terminal for a redeemed JIT share link. It is modeled directly on
// the WebSocket wiring in TerminalModal.tsx (same hello/resize/control-frame
// wire protocol as /ws/ssh) but dials the code-scoped redeem endpoint, which
// needs no token: the code itself is the credential.
export function JitTerminal({ code }: JitTerminalProps) {
  const containerRef = useRef<HTMLDivElement | null>(null);

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
          const j = JSON.parse(ev.data) as { closed?: boolean; error?: string };
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
      if (ws.readyState !== WebSocket.OPEN) {
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

    return () => {
      window.removeEventListener('resize', onResize);
      ws.close();
      term.dispose();
    };
  }, [code]);

  return <div ref={containerRef} style={{ width: '100%', height: 420 }} />;
}
