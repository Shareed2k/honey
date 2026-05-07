import { useEffect, useRef, useState } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { getToken } from './api';

type HostRecord = {
  provider: string;
  name: string;
  primary_ip: string;
  meta?: Record<string, string>;
};

type Props = {
  record: HostRecord;
  sshUser: string;
  recordSession: boolean;
  onClose: () => void;
};

export function TerminalModal({ record, sshUser, recordSession, onClose }: Props) {
  const ref = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
  const [showConnectOverlay, setShowConnectOverlay] = useState(true);

  useEffect(() => {
    const el = ref.current;
    if (!el) {
      setShowConnectOverlay(false);
      return;
    }

    let connectUiDismissed = false;
    let fallbackTimer: ReturnType<typeof setTimeout> | undefined;

    const dismissConnectOverlay = () => {
      if (connectUiDismissed) {
        return;
      }
      connectUiDismissed = true;
      if (fallbackTimer !== undefined) {
        clearTimeout(fallbackTimer);
        fallbackTimer = undefined;
      }
      setShowConnectOverlay(false);
    };

    setShowConnectOverlay(true);

    const term = new Terminal({ cursorBlink: true, fontSize: 14 });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();
    termRef.current = term;

    const token = getToken();
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const u = new URL(`/ws/ssh?token=${encodeURIComponent(token)}`, window.location.href);
    u.protocol = proto;
    const ws = new WebSocket(u.toString());
    ws.binaryType = 'arraybuffer';
    wsRef.current = ws;

    ws.onopen = () => {
      fit.fit();
      const cols = term.cols;
      const rows = term.rows;
      ws.send(
        JSON.stringify({
          ssh_user: sshUser,
          record,
          cols,
          rows,
          record_session: recordSession,
        }),
      );
      requestAnimationFrame(() => {
        fit.fit();
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
        }
      });
      fallbackTimer = setTimeout(dismissConnectOverlay, 2000);
    };

    ws.onmessage = (ev) => {
      dismissConnectOverlay();
      if (typeof ev.data === 'string') {
        try {
          const j = JSON.parse(ev.data) as { closed?: boolean; error?: string };
          if (j.error) {
            term.writeln(`\r\n\x1b[31m${j.error}\x1b[0m`);
          }
          if (j.closed) {
            ws.close();
          }
        } catch {
          /* ignore */
        }
        return;
      }
      const buf = new Uint8Array(ev.data as ArrayBuffer);
      term.write(buf);
    };

    ws.onerror = () => {
      dismissConnectOverlay();
      term.writeln('\r\n\x1b[31m[websocket error]\x1b[0m');
    };

    ws.onclose = () => {
      dismissConnectOverlay();
      term.writeln('\r\n\x1b[33m[disconnected]\x1b[0m');
    };

    const enc = new TextEncoder();
    term.onData((data) => {
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(enc.encode(data));
      }
    });

    const onResize = () => {
      fit.fit();
      if (ws.readyState === WebSocket.OPEN) {
        ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
      }
    };
    window.addEventListener('resize', onResize);

    return () => {
      if (fallbackTimer !== undefined) {
        clearTimeout(fallbackTimer);
      }
      window.removeEventListener('resize', onResize);
      ws.close();
      term.dispose();
      termRef.current = null;
      wsRef.current = null;
    };
  }, [record, recordSession, sshUser]);

  return (
    <div className="modal-backdrop" role="presentation">
      <div className="modal" role="dialog" aria-busy={showConnectOverlay} aria-label={`Terminal: ${record.name}`}>
        <header>
          <strong>
            {record.name} ({record.primary_ip})
          </strong>
          <button type="button" onClick={onClose}>
            Close
          </button>
        </header>
        <div className="term-wrap">
          <div className="term-xterm-host" ref={ref} />
          {showConnectOverlay ? (
            <div className="term-connect-overlay" aria-live="polite" aria-atomic="true">
              <div className="term-spinner" role="status" />
              <span className="sr-only">Connecting…</span>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}
