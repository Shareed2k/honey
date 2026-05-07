import { useCallback, useEffect, useRef, useState } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { apiGet, apiPost, getToken } from './api';

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
  /** When true, server has OPENAI_API_KEY; show assist side panel. */
  assistAvailable?: boolean;
  onClose: () => void;
};

const defaultScrollbackLines = 200;

function collectScrollback(term: Terminal, maxLines: number): string {
  const buf = term.buffer.active;
  const len = buf.length;
  const n = Math.min(Math.max(1, maxLines), 500);
  const start = Math.max(0, len - n);
  const out: string[] = [];
  for (let i = start; i < len; i++) {
    const line = buf.getLine(i);
    out.push(line ? line.translateToString(true) : '');
  }
  return out.join('\n');
}

export function TerminalModal({ record, sshUser, recordSession, assistAvailable, onClose }: Props) {
  const ref = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const [showConnectOverlay, setShowConnectOverlay] = useState(true);

  const [assistPrompt, setAssistPrompt] = useState('');
  const [assistLines, setAssistLines] = useState(defaultScrollbackLines);
  const [assistBusy, setAssistBusy] = useState(false);
  const [assistErr, setAssistErr] = useState<string | null>(null);
  const [assistReply, setAssistReply] = useState('');
  const [assistClipped, setAssistClipped] = useState(false);

  const [assistModels, setAssistModels] = useState<string[]>([]);
  const [assistModelsLoading, setAssistModelsLoading] = useState(false);
  const [assistModelsErr, setAssistModelsErr] = useState<string | null>(null);
  const [assistSelectedModel, setAssistSelectedModel] = useState('');

  useEffect(() => {
    if (!assistAvailable) {
      return undefined;
    }
    let cancelled = false;
    setAssistModelsLoading(true);
    setAssistModelsErr(null);
    void (async () => {
      try {
        const r = await apiGet('/api/v1/terminal-assist/models');
        const j = (await r.json().catch(() => ({}))) as {
          models?: string[];
          error?: string;
        };
        if (cancelled) {
          return;
        }
        if (!r.ok) {
          setAssistModels([]);
          setAssistSelectedModel('');
          setAssistModelsErr(j.error || r.statusText || 'Could not load models');
          return;
        }
        const list = Array.isArray(j.models) ? j.models : [];
        setAssistModels(list);
        if (list.length > 0) {
          setAssistSelectedModel(list[0]);
          setAssistModelsErr(null);
        } else {
          setAssistSelectedModel('');
          setAssistModelsErr('No models returned by the provider. Check OPENAI_BASE_URL and that /v1/models works.');
        }
      } catch (e) {
        if (!cancelled) {
          setAssistModels([]);
          setAssistSelectedModel('');
          setAssistModelsErr(e instanceof Error ? e.message : String(e));
        }
      } finally {
        if (!cancelled) {
          setAssistModelsLoading(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [assistAvailable]);

  const assistCanAsk = assistModels.length > 0 && assistSelectedModel.trim() !== '' && !assistModelsLoading;

  const runAssist = useCallback(async () => {
    const term = termRef.current;
    if (!term) {
      setAssistErr('Terminal is not ready yet.');
      return;
    }
    if (!assistCanAsk) {
      setAssistErr('Pick a model from the list (models must load from the server).');
      return;
    }
    setAssistBusy(true);
    setAssistErr(null);
    setAssistReply('');
    setAssistClipped(false);
    try {
      const scrollback = collectScrollback(term, assistLines);
      if (!scrollback.trim()) {
        setAssistErr('No scrollback to send yet.');
        setAssistBusy(false);
        return;
      }
      const model = assistSelectedModel.trim();
      const r = await apiPost('/api/v1/terminal-assist', {
        user_prompt: assistPrompt.trim(),
        scrollback,
        max_lines: assistLines,
        model,
      });
      const j = (await r.json().catch(() => ({}))) as {
        error?: string;
        reply?: string;
        scrollback_clipped?: boolean;
      };
      if (!r.ok) {
        setAssistErr(j.error || r.statusText || 'Request failed');
        return;
      }
      setAssistReply((j.reply || '').trim());
      setAssistClipped(!!j.scrollback_clipped);
    } catch (e) {
      setAssistErr(e instanceof Error ? e.message : String(e));
    } finally {
      setAssistBusy(false);
    }
  }, [assistCanAsk, assistLines, assistPrompt, assistSelectedModel]);

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
    };
  }, [assistAvailable, record, recordSession, sshUser]);

  const connectOverlay = showConnectOverlay ? (
    <div className="term-connect-overlay" aria-live="polite" aria-atomic="true">
      <div className="term-spinner" role="status" />
      <span className="sr-only">Connecting…</span>
    </div>
  ) : null;

  const termArea = (
    <div className="term-wrap">
      <div className="term-xterm-host" ref={ref} />
      {connectOverlay}
    </div>
  );

  return (
    <div className="modal-backdrop" role="presentation">
      <div
        className={`modal${assistAvailable ? ' modal-terminal-split' : ''}`}
        role="dialog"
        aria-busy={showConnectOverlay}
        aria-label={`Terminal: ${record.name}`}
      >
        <header>
          <strong>
            {record.name} ({record.primary_ip})
          </strong>
          <button type="button" onClick={onClose}>
            Close
          </button>
        </header>
        {assistAvailable ? (
          <div className="modal-terminal-body">
            {termArea}
            <aside className="term-assist-panel" aria-label="Terminal assistant">
              <strong style={{ fontSize: '0.9rem' }}>Assistant</strong>
              <small>
                Sends the last lines of scrollback plus your question using a model from the provider list. Terminal data may
                be sensitive—only send what you are allowed to share.
              </small>
              {assistModelsLoading ? (
                <small style={{ color: '#9aa4b2' }}>Loading models…</small>
              ) : null}
              {assistModelsErr ? (
                <small style={{ color: '#f5a623' }}>{assistModelsErr}</small>
              ) : null}
              {assistModels.length > 0 ? (
                <label style={{ fontSize: '0.82rem', display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
                  Model
                  <select value={assistSelectedModel} onChange={(e) => setAssistSelectedModel(e.target.value)}>
                    {assistModels.map((id) => (
                      <option key={id} value={id}>
                        {id}
                      </option>
                    ))}
                  </select>
                </label>
              ) : !assistModelsLoading ? (
                <small style={{ color: '#9aa4b2' }}>No models to choose from.</small>
              ) : null}
              <label style={{ fontSize: '0.82rem', display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
                Scrollback lines
                <input
                  type="number"
                  min={1}
                  max={500}
                  value={assistLines}
                  onChange={(e) => setAssistLines(Number(e.target.value) || defaultScrollbackLines)}
                />
              </label>
              <label style={{ fontSize: '0.82rem', display: 'flex', flexDirection: 'column', gap: '0.25rem' }}>
                Your question (optional)
                <textarea
                  value={assistPrompt}
                  onChange={(e) => setAssistPrompt(e.target.value)}
                  placeholder="e.g. Why did this command fail?"
                  rows={3}
                />
              </label>
              <button
                type="button"
                className="primary"
                disabled={assistBusy || !assistCanAsk}
                onClick={() => void runAssist()}
              >
                {assistBusy ? 'Thinking…' : 'Ask assistant'}
              </button>
              {assistErr ? (
                <p style={{ color: '#f66', margin: 0, fontSize: '0.85rem' }}>{assistErr}</p>
              ) : null}
              {assistClipped ? (
                <small style={{ color: '#f5a623' }}>Some scrollback was clipped by server limits.</small>
              ) : null}
              {assistReply ? (
                <div className="term-assist-reply" role="region" aria-label="Assistant reply">
                  {assistReply}
                </div>
              ) : null}
            </aside>
          </div>
        ) : (
          termArea
        )}
      </div>
    </div>
  );
}
