import { Suspense, lazy, useCallback, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { Button, Flex, InputNumber, Input, Popover, Select, Splitter, Typography } from 'antd';
import { apiGet, apiPost, getToken } from './api/core';

type HostRecord = {
  provider: string;
  name: string;
  primary_ip: string;
  meta?: Record<string, string>;
};

/** noVNC RFB handle (minimal surface we use). */
type NovncRfbHandle = {
  background: string;
  scaleViewport: boolean;
  disconnect: () => void;
  addEventListener: (type: string, listener: (ev: Event) => void) => void;
};

export type PveConsoleMode = 'serial' | 'vnc';

export type TrueNASConsoleMode = 'ssh' | 'api';

type SessionProps = {
  sessionId: string;
  record: HostRecord;
  sshUser: string;
  recordSession: boolean;
  assistAvailable?: boolean;
  pveConsole?: PveConsoleMode;
  truenasConsole?: TrueNASConsoleMode;
  isActive: boolean;
  registerCloseTabSender?: (id: string, sender: (() => void) | null) => void;
};

export type TerminalSessionConfig = {
  id: string;
  record: HostRecord;
  pve: PveConsoleMode;
  truenasConsole?: TrueNASConsoleMode;
};

type TabsProps = {
  isOpen: boolean;
  terminals: TerminalSessionConfig[];
  activeTermId: string | null;
  sshUser: string;
  recordSession: boolean;
  assistAvailable?: boolean;
  onSetActive: (id: string) => void;
  onCloseTerminal: (id: string) => void;
  onCloseModal: () => void;
};

/** Split-screen layouts for the terminal modal (Rundeck-style tiling). */
export type SplitLayout = 'none' | '2way' | '3way-v' | '3way-h' | '4way' | '5way' | '6way';

const CELL_COUNT: Record<SplitLayout, number> = {
  none: 1,
  '2way': 2,
  '3way-v': 3,
  '3way-h': 3,
  '4way': 4,
  '5way': 5,
  '6way': 6,
};

const LAYOUT_OPTIONS: { value: SplitLayout; label: string }[] = [
  { value: 'none', label: 'None' },
  { value: '2way', label: '2-Way' },
  { value: '3way-v', label: '3-Way (V)' },
  { value: '3way-h', label: '3-Way (H)' },
  { value: '4way', label: '4-Way' },
  { value: '5way', label: '5-Way' },
  { value: '6way', label: '6-Way' },
];

// Split-screen layout + pane assignments are persisted per-tab in sessionStorage so
// they survive a refresh. sessionStorage matches the lifetime of the terminal records
// (honey_term_<id>) that the assignments reference.
const SPLIT_STORAGE_KEY = 'honey_terminal_split';

function readSplitState(): { layout: SplitLayout; paneAssignments: string[] } {
  try {
    const raw = sessionStorage.getItem(SPLIT_STORAGE_KEY);
    if (raw) {
      const v = JSON.parse(raw) as { layout?: unknown; paneAssignments?: unknown };
      const layout: SplitLayout =
        typeof v.layout === 'string' && v.layout in CELL_COUNT ? (v.layout as SplitLayout) : 'none';
      const paneAssignments = Array.isArray(v.paneAssignments)
        ? v.paneAssignments.filter((x: unknown): x is string => typeof x === 'string')
        : [];
      return { layout, paneAssignments };
    }
  } catch {
    /* corrupt/unavailable → defaults */
  }
  return { layout: 'none', paneAssignments: [] };
}

const defaultScrollbackLines = 200;

const detachChar = '\x1d'; // Ctrl+] — honey closes the session; not sent to guest

function isProxmoxPveSerialConsole(r: HostRecord): boolean {
  const k = (r.meta?.kind || '').toLowerCase();
  const m = (r.meta?.exec_mode || '').toLowerCase();
  return r.provider === 'proxmox' && (k === 'lxc' || k === 'qemu') && (m === 'pve' || m === 'hybrid');
}

function isTrueNASAPIShellSession(r: HostRecord, mode: TrueNASConsoleMode): boolean {
  return r.provider === 'truenas' && mode === 'api';
}

const AiMarkdown = lazy(async () => import('./AiMarkdown').then((m) => ({ default: m.AiMarkdown })));

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

function TerminalSession({
  sessionId,
  record,
  sshUser,
  recordSession,
  assistAvailable,
  pveConsole = 'serial',
  truenasConsole = 'ssh',
  isActive,
  registerCloseTabSender,
}: SessionProps) {
  const ref = useRef<HTMLDivElement>(null);
  const vncHostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const rfbRef = useRef<NovncRfbHandle | null>(null);
  const wsRef = useRef<WebSocket | null>(null);
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

  const isVnc = pveConsole === 'vnc';
  const showAssist = !!assistAvailable && !isVnc;

  useEffect(() => {
    if (!assistAvailable || isVnc) {
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
  }, [assistAvailable, isVnc]);

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
    if (isVnc) {
      return undefined;
    }

    const el = ref.current;
    if (!el) {
      setShowConnectOverlay(false);
      return undefined;
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
    termRef.current = term;

    const token = getToken();
    const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    const u = new URL(`/ws/ssh?token=${encodeURIComponent(token)}`, window.location.href);
    u.protocol = proto;
    const ws = new WebSocket(u.toString());
    wsRef.current = ws;
    ws.binaryType = 'arraybuffer';
    ws.onopen = () => {
      fit.fit();
      const cols = term.cols;
      const rows = term.rows;
      const hello: Record<string, unknown> = {
        session_id: sessionId,
        ssh_user: sshUser,
        record,
        cols,
        rows,
        record_session: recordSession,
      };
      if (truenasConsole === 'api') {
        hello.console = 'truenas_api';
      }
      ws.send(JSON.stringify(hello));
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
            sawServerError = true;
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

    let sawServerError = false;
    ws.onerror = () => {
      dismissConnectOverlay();
      if (!sawServerError) {
        term.writeln('\r\n\x1b[31m[websocket error]\x1b[0m');
      }
    };

    ws.onclose = (ev) => {
      dismissConnectOverlay();
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
      const i = data.indexOf(detachChar);
      if (i !== -1) {
        if (i > 0) {
          ws.send(enc.encode(data.slice(0, i)));
        }
        ws.send(JSON.stringify({ type: 'detach' }));
        return;
      }
      ws.send(enc.encode(data));
    });

    const onResize = () => {
      if (isActive) {
        fit.fit();
        if (ws.readyState === WebSocket.OPEN) {
          ws.send(JSON.stringify({ type: 'resize', cols: term.cols, rows: term.rows }));
        }
      }
    };
    window.addEventListener('resize', onResize);

    registerCloseTabSender?.(sessionId, () => {
      if (wsRef.current?.readyState === WebSocket.OPEN) {
        wsRef.current.send(JSON.stringify({ type: 'close_tab' }));
      }
    });

    return () => {
      registerCloseTabSender?.(sessionId, null);
      if (fallbackTimer !== undefined) {
        clearTimeout(fallbackTimer);
      }
      window.removeEventListener('resize', onResize);
      wsRef.current = null;
      ws.close();
      term.dispose();
      termRef.current = null;
    };
  }, [assistAvailable, isVnc, record, recordSession, registerCloseTabSender, sshUser, sessionId, isActive, truenasConsole]);

  // Refit terminal when it becomes active
  useEffect(() => {
    if (isActive && termRef.current) {
      // Small timeout to let flexbox layout finish
      setTimeout(() => {
        window.dispatchEvent(new Event('resize'));
      }, 50);
    }
  }, [isActive]);

  useEffect(() => {
    if (!isVnc) {
      return undefined;
    }

    const host = vncHostRef.current;
    if (!host) {
      setShowConnectOverlay(false);
      return undefined;
    }

    let cancelled = false;
    let fallbackTimer: ReturnType<typeof setTimeout> | undefined;
    const dismissConnectOverlay = () => {
      if (fallbackTimer !== undefined) {
        clearTimeout(fallbackTimer);
        fallbackTimer = undefined;
      }
      setShowConnectOverlay(false);
    };

    setShowConnectOverlay(true);
    rfbRef.current = null;

    const startRfb = () => {
      void (async () => {
        try {
          const mod = await import('@novnc/novnc');
          if (cancelled || !vncHostRef.current) {
            return;
          }
          const RFB = mod.default;
          const token = getToken();
          const offerResp = await apiPost('/api/v1/pve-qemu-vnc-offer', { record });
          const offerJson = (await offerResp.json().catch(() => ({}))) as {
            session_id?: string;
            vnc_password?: string;
            error?: string;
          };
          if (!offerResp.ok) {
            dismissConnectOverlay();
            host.textContent = offerJson.error || offerResp.statusText || 'VNC offer failed';
            return;
          }
          if (!offerJson.session_id || !offerJson.vnc_password) {
            dismissConnectOverlay();
            host.textContent = offerJson.error || 'Server did not return session_id / vnc_password';
            return;
          }

          const proto = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
          const u = new URL(
            `/ws/pve-qemu-vnc?token=${encodeURIComponent(token)}&vnc_session=${encodeURIComponent(offerJson.session_id)}`,
            window.location.href,
          );
          u.protocol = proto;
          const el = vncHostRef.current;
          if (!el || cancelled) {
            return;
          }
          const rfb = new RFB(el, u.toString(), {
            wsProtocols: [],
            credentials: { password: offerJson.vnc_password },
          }) as NovncRfbHandle & {
            resizeSession: boolean;
          };
          rfb.background = '#000000';
          rfb.scaleViewport = true;
          rfb.resizeSession = false;
          rfbRef.current = rfb;
          fallbackTimer = setTimeout(dismissConnectOverlay, 4000);
          const onConn = () => {
            dismissConnectOverlay();
            rfb.scaleViewport = true;
            window.dispatchEvent(new Event('resize'));
          };
          rfb.addEventListener('connect', onConn);
          rfb.addEventListener('disconnect', dismissConnectOverlay);
          rfb.addEventListener('securityfailure', (ev: Event) => {
            dismissConnectOverlay();
            const d = (ev as CustomEvent<{ reason?: string; status?: number }>).detail;
            const why = d?.reason || d?.status?.toString() || 'security handshake failed';
            el.appendChild(document.createTextNode(`VNC security failure: ${why}`));
          });
        } catch (e) {
          dismissConnectOverlay();
          const msg = e instanceof Error ? e.message : String(e);
          host.textContent = `VNC failed to start: ${msg}`;
        }
      })();
    };

    // Wait two frames so the modal flex layout has non-zero size before noVNC attaches ResizeObserver.
    let outerRaf = 0;
    let innerRaf = 0;
    outerRaf = requestAnimationFrame(() => {
      innerRaf = requestAnimationFrame(() => {
        if (!cancelled) {
          startRfb();
        }
      });
    });

    return () => {
      cancelled = true;
      cancelAnimationFrame(outerRaf);
      cancelAnimationFrame(innerRaf);
      if (fallbackTimer !== undefined) {
        clearTimeout(fallbackTimer);
      }
      const rfb = rfbRef.current;
      rfbRef.current = null;
      if (rfb) {
        try {
          rfb.disconnect();
        } catch {
          /* ignore */
        }
      }
      host.replaceChildren();
    };
  }, [isVnc, record]);

  const connectOverlay = showConnectOverlay ? (
    <div className="term-connect-overlay" aria-live="polite" aria-atomic="true">
      <div className="term-spinner" role="status" />
      <span className="sr-only">Connecting…</span>
    </div>
  ) : null;

  const termArea = isVnc ? (
    <div className="term-wrap">
      <div className="term-vnc-host" ref={vncHostRef} tabIndex={-1} />
      {connectOverlay}
    </div>
  ) : (
    <div className="term-wrap">
      <div className="term-xterm-host" ref={ref} />
      {connectOverlay}
    </div>
  );

  return (
    <div style={{ display: isActive ? 'flex' : 'none', width: '100%', height: '100%', minHeight: 0, flexDirection: 'column' }} className={showAssist ? 'modal-terminal-split-inner' : ''}>
      <div className="modal-terminal-body" style={{ flex: 1, minHeight: 0, display: 'flex', flexDirection: 'row', width: '100%' }}>
        {termArea}
        {showAssist ? (
          <aside className="term-assist-panel" aria-label="Terminal assistant">
            <Flex vertical gap="small" style={{ width: '100%', flex: 1 }}>
              <Typography.Text strong style={{ fontSize: '0.9rem' }}>Assistant</Typography.Text>
              <Typography.Text type="secondary" style={{ fontSize: '0.82rem' }}>
                Sends the last lines of scrollback plus your question using a model from the provider list. Terminal data may
                be sensitive—only send what you are allowed to share.
              </Typography.Text>
              {assistModelsLoading ? (
                <Typography.Text type="secondary">Loading models…</Typography.Text>
              ) : null}
              {assistModelsErr ? (
                <Typography.Text type="warning">{assistModelsErr}</Typography.Text>
              ) : null}
              {assistModels.length > 0 ? (
                <Flex vertical gap={2} style={{ width: '100%' }}>
                  <Typography.Text style={{ fontSize: '0.82rem' }}>Model</Typography.Text>
                  <Select
                    size="small"
                    style={{ width: '100%' }}
                    value={assistSelectedModel}
                    onChange={setAssistSelectedModel}
                    options={assistModels.map((id) => ({ value: id, label: id }))}
                  />
                </Flex>
              ) : !assistModelsLoading ? (
                <Typography.Text type="secondary">No models to choose from.</Typography.Text>
              ) : null}
              <Flex vertical gap={2} style={{ width: '100%' }}>
                <Typography.Text style={{ fontSize: '0.82rem' }}>Scrollback lines</Typography.Text>
                <InputNumber
                  size="small"
                  min={1}
                  max={500}
                  value={assistLines}
                  onChange={(v) => setAssistLines(v || defaultScrollbackLines)}
                  style={{ width: '100%' }}
                />
              </Flex>
              <Flex vertical gap={2} style={{ width: '100%' }}>
                <Typography.Text style={{ fontSize: '0.82rem' }}>Your question (optional)</Typography.Text>
                <Input.TextArea
                  value={assistPrompt}
                  onChange={(e) => setAssistPrompt(e.target.value)}
                  placeholder="e.g. Why did this command fail?"
                  rows={3}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter' && !e.shiftKey) {
                      e.preventDefault();
                      void runAssist();
                    }
                  }}
                />
              </Flex>
              <Button
                type="primary"
                block
                loading={assistBusy}
                disabled={!assistCanAsk}
                onClick={() => void runAssist()}
              >
                {assistBusy ? 'Thinking…' : 'Ask assistant'}
              </Button>
              {assistErr ? (
                <Typography.Text type="danger" style={{ fontSize: '0.85rem' }}>{assistErr}</Typography.Text>
              ) : null}
              {assistClipped ? (
                <Typography.Text type="warning">Some scrollback was clipped by server limits.</Typography.Text>
              ) : null}
              {assistReply ? (
                <div className="term-assist-reply" role="region" aria-label="Assistant reply">
                  <Suspense
                    fallback={<pre className="ai-markdown-suspense-fallback">{assistReply}</pre>}
                  >
                    <AiMarkdown content={assistReply} />
                  </Suspense>
                </div>
              ) : null}
            </Flex>
          </aside>
        ) : null}
      </div>
    </div>
  );
}

export function TerminalTabsModal({
  isOpen,
  terminals,
  activeTermId,
  sshUser,
  recordSession,
  assistAvailable,
  onSetActive,
  onCloseTerminal,
  onCloseModal,
}: TabsProps) {
  const [isMaximized, setIsMaximized] = useState(false);

  const closeTabSendersRef = useRef(new Map<string, () => void>());
  const registerCloseTabSender = useCallback((id: string, sender: (() => void) | null) => {
    if (sender) {
      closeTabSendersRef.current.set(id, sender);
    } else {
      closeTabSendersRef.current.delete(id);
    }
  }, []);

  const tabsRef = useRef<HTMLDivElement>(null);
  const [canScrollLeft, setCanScrollLeft] = useState(false);
  const [canScrollRight, setCanScrollRight] = useState(false);

  const checkScroll = useCallback(() => {
    const el = tabsRef.current;
    if (!el) return;
    // Add a tiny 1px tolerance to avoid floating point rounding issues
    setCanScrollLeft(el.scrollLeft > 1);
    setCanScrollRight(el.scrollLeft + el.clientWidth < el.scrollWidth - 1);
  }, []);

  useEffect(() => {
    checkScroll();
    window.addEventListener('resize', checkScroll);
    return () => window.removeEventListener('resize', checkScroll);
  }, [checkScroll, terminals]); // Re-check when terminals array changes

  // Also re-check when the modal actually opens, since display:none might have hidden the true scrollWidth
  // and trigger a global resize to refit xterm instances that were mounted while hidden!
  useEffect(() => {
    if (isOpen) {
      setTimeout(() => {
        checkScroll();
        window.dispatchEvent(new Event('resize'));
      }, 50);
    }
  }, [isOpen, checkScroll]);

  // Dispatch a resize event when maximizing so the terminal resizes properly
  useEffect(() => {
    if (isOpen) {
      setTimeout(() => {
        window.dispatchEvent(new Event('resize'));
      }, 50); // small delay to let CSS layout apply
    }
  }, [isMaximized, isOpen]);

  const scrollByAmount = (offset: number) => {
    if (tabsRef.current) {
      tabsRef.current.scrollBy({ left: offset, behavior: 'smooth' });
    }
  };

  // --- Split-screen state ---------------------------------------------------
  // Sessions are mounted once in a hidden pool and portaled into layout slots,
  // so changing layout / pane assignment never remounts a TerminalSession
  // (which would drop its live WebSocket + xterm).
  // Restored from sessionStorage so the layout survives a page refresh.
  const [layout, setLayout] = useState<SplitLayout>(() => readSplitState().layout);
  const [paneAssignments, setPaneAssignments] = useState<string[]>(() => readSplitState().paneAssignments);
  const slotRefs = useRef<(HTMLDivElement | null)[]>([]);
  const holderRef = useRef<HTMLDivElement | null>(null);
  const slotCbs = useRef(new Map<number, (el: HTMLDivElement | null) => void>());
  const [slotVersion, setSlotVersion] = useState(0);

  // getSlotRef returns a STABLE callback ref per cell index. An inline ref
  // (`ref={(el) => ...}`) is recreated every render, so React re-invokes it on
  // every render; the setState inside then loops forever (React error #185).
  // A cached, identity-stable callback fires only on actual mount/unmount, and
  // the equality guard skips redundant bumps.
  const getSlotRef = useCallback((i: number) => {
    let cb = slotCbs.current.get(i);
    if (!cb) {
      cb = (el: HTMLDivElement | null) => {
        if (slotRefs.current[i] === el) return;
        slotRefs.current[i] = el;
        setSlotVersion((v) => v + 1); // re-render so portals pick up the slot element
      };
      slotCbs.current.set(i, cb);
    }
    return cb;
  }, []);
  const registerHolder = useCallback((el: HTMLDivElement | null) => {
    if (holderRef.current === el) return;
    holderRef.current = el;
    setSlotVersion((v) => v + 1);
  }, []);

  // changeLayout rebuilds pane assignments to the new cell count: keep still-open
  // assignments, fill the rest from unassigned terminals (active first).
  const changeLayout = useCallback(
    (next: SplitLayout) => {
      setLayout(next);
      setPaneAssignments((prev) => {
        const count = CELL_COUNT[next];
        const openIds = new Set(terminals.map((t) => t.id));
        const cells: string[] = [];
        for (let i = 0; i < count; i++) {
          const id = prev[i];
          cells.push(id && openIds.has(id) ? id : '');
        }
        const used = new Set(cells.filter(Boolean));
        const ordered = [
          ...terminals.filter((t) => t.id === activeTermId),
          ...terminals.filter((t) => t.id !== activeTermId),
        ]
          .map((t) => t.id)
          .filter((id) => !used.has(id));
        let oi = 0;
        for (let i = 0; i < cells.length; i++) {
          if (!cells[i] && oi < ordered.length) {
            cells[i] = ordered[oi++];
          }
        }
        return cells;
      });
    },
    [terminals, activeTermId],
  );

  // assignPane sets a cell to a terminal, vacating any other cell that held it
  // (a terminal shows in at most one pane).
  const assignPane = useCallback((cell: number, id: string) => {
    setPaneAssignments((prev) => {
      const next = [...prev];
      if (id) {
        for (let i = 0; i < next.length; i++) {
          if (next[i] === id) next[i] = '';
        }
      }
      next[cell] = id;
      return next;
    });
  }, []);

  // Prune assignments referencing closed terminals.
  useEffect(() => {
    const openIds = new Set(terminals.map((t) => t.id));
    setPaneAssignments((prev) => {
      if (!prev.some((id) => id && !openIds.has(id))) return prev;
      return prev.map((id) => (id && openIds.has(id) ? id : ''));
    });
  }, [terminals]);

  // Persist layout + assignments per-tab so a refresh restores the split.
  useEffect(() => {
    try {
      sessionStorage.setItem(SPLIT_STORAGE_KEY, JSON.stringify({ layout, paneAssignments }));
    } catch {
      /* ignore quota/availability errors */
    }
  }, [layout, paneAssignments]);

  // Refit every visible pane after a layout / assignment / slot change.
  useEffect(() => {
    if (!isOpen) return undefined;
    const tid = window.setTimeout(() => window.dispatchEvent(new Event('resize')), 50);
    return () => window.clearTimeout(tid);
  }, [layout, paneAssignments, slotVersion, isOpen]);

  const handleSplitResize = useCallback(() => {
    window.dispatchEvent(new Event('resize'));
  }, []);

  if (terminals.length === 0) {
    return null;
  }

  const activeTerm = terminals.find((t) => t.id === activeTermId) || terminals[terminals.length - 1];
  const isVnc = activeTerm.pve === 'vnc';
  const splitActive = layout !== 'none';
  const showAssist = !!assistAvailable && !isVnc && !splitActive;

  const modalClass =
    `modal${showAssist ? ' modal-terminal-split' : ''}${splitActive ? ' modal-terminal-split-active' : ''}${isVnc ? ' modal-pve-vnc' : ''}${isMaximized ? ' modal-maximized' : ''}`.trim();

  const termOptions = terminals.map((t) => ({ value: t.id, label: t.record.name }));

  // renderCell draws one split leaf: a borderless terminal picker strip + an
  // empty slot that a TerminalSession portals into (placeholder when empty).
  const renderCell = (i: number) => {
    const assignedId = paneAssignments[i] || '';
    return (
      <div className="term-split-cell">
        <div className="term-pane-strip">
          <Select
            size="small"
            variant="borderless"
            placeholder="Choose terminal"
            style={{ flex: 1, minWidth: 0 }}
            value={assignedId || undefined}
            onChange={(id) => assignPane(i, id)}
            options={termOptions}
          />
        </div>
        <div className="term-split-slot-wrap">
          <div className="term-split-slot" ref={getSlotRef(i)} />
          {assignedId ? null : <div className="term-pane-placeholder">Select a terminal</div>}
        </div>
      </div>
    );
  };

  const renderSplit = () => {
    const cell = (i: number) => <Splitter.Panel min="15%">{renderCell(i)}</Splitter.Panel>;
    switch (layout) {
      case '2way':
        return (
          <Splitter onResize={handleSplitResize}>
            {cell(0)}
            {cell(1)}
          </Splitter>
        );
      case '3way-v':
        return (
          <Splitter onResize={handleSplitResize}>
            {cell(0)}
            <Splitter.Panel min="15%">
              <Splitter orientation="vertical" onResize={handleSplitResize}>
                {cell(1)}
                {cell(2)}
              </Splitter>
            </Splitter.Panel>
          </Splitter>
        );
      case '3way-h':
        return (
          <Splitter orientation="vertical" onResize={handleSplitResize}>
            <Splitter.Panel min="15%">
              <Splitter onResize={handleSplitResize}>
                {cell(0)}
                {cell(1)}
              </Splitter>
            </Splitter.Panel>
            {cell(2)}
          </Splitter>
        );
      case '4way':
        return (
          <Splitter orientation="vertical" onResize={handleSplitResize}>
            <Splitter.Panel min="15%">
              <Splitter onResize={handleSplitResize}>
                {cell(0)}
                {cell(1)}
              </Splitter>
            </Splitter.Panel>
            <Splitter.Panel min="15%">
              <Splitter onResize={handleSplitResize}>
                {cell(2)}
                {cell(3)}
              </Splitter>
            </Splitter.Panel>
          </Splitter>
        );
      case '5way':
        return (
          <Splitter orientation="vertical" onResize={handleSplitResize}>
            <Splitter.Panel min="15%">
              <Splitter onResize={handleSplitResize}>
                {cell(0)}
                {cell(1)}
                {cell(2)}
              </Splitter>
            </Splitter.Panel>
            <Splitter.Panel min="15%">
              <Splitter onResize={handleSplitResize}>
                {cell(3)}
                {cell(4)}
              </Splitter>
            </Splitter.Panel>
          </Splitter>
        );
      case '6way':
        return (
          <Splitter orientation="vertical" onResize={handleSplitResize}>
            <Splitter.Panel min="15%">
              <Splitter onResize={handleSplitResize}>
                {cell(0)}
                {cell(1)}
                {cell(2)}
              </Splitter>
            </Splitter.Panel>
            <Splitter.Panel min="15%">
              <Splitter onResize={handleSplitResize}>
                {cell(3)}
                {cell(4)}
                {cell(5)}
              </Splitter>
            </Splitter.Panel>
          </Splitter>
        );
      default:
        return null;
    }
  };

  // sessionTarget decides where a terminal's DOM is portaled and whether it is
  // visible. Unassigned sessions go to the hidden holder (stay mounted).
  const sessionTarget = (id: string): { el: HTMLDivElement | null; visible: boolean } => {
    if (layout === 'none') {
      const visible = id === activeTerm.id;
      return { el: visible ? slotRefs.current[0] ?? null : null, visible };
    }
    const idx = paneAssignments.indexOf(id);
    if (idx >= 0) return { el: slotRefs.current[idx] ?? null, visible: true };
    return { el: null, visible: false };
  };

  const splitPopover = (
    <div className="term-split-popover">
      {LAYOUT_OPTIONS.map((o) => (
        <button
          key={o.value}
          type="button"
          className={`term-split-tile tile-${o.value}${layout === o.value ? ' active' : ''}`}
          onClick={() => changeLayout(o.value)}
        >
          <span className="term-split-icon" aria-hidden="true">
            {Array.from({ length: CELL_COUNT[o.value] }).map((_, k) => (
              <i key={k} />
            ))}
          </span>
          <span className="term-split-tile-label">{o.label}</span>
        </button>
      ))}
    </div>
  );

  return (
    <div className="modal-backdrop" role="presentation" style={{ display: isOpen ? 'flex' : 'none' }}>
      <div
        className={modalClass}
        role="dialog"
        aria-label="Terminal Sessions"
        style={{ padding: 0 }}
      >
        <header className="terminal-tabs-header">
          <div className="terminal-tabs-scroll-wrapper">
            {canScrollLeft && (
              <button type="button" className="terminal-tab-scroll-btn" onClick={() => scrollByAmount(-200)} title="Scroll Left">
                ‹
              </button>
            )}
            <div className="terminal-tabs-container" ref={tabsRef} onScroll={checkScroll}>
              {terminals.map((t) => (
                <div
                  key={t.id}
                  className={`terminal-tab ${t.id === activeTermId ? 'active' : ''}`}
                  onClick={() => onSetActive(t.id)}
                  title={`${t.record.name} (${t.truenasConsole === 'api' ? 'truenas-api' : t.pve})`}
                >
                  <span className="terminal-tab-title">{t.record.name}</span>
                  <button
                    type="button"
                    className="terminal-tab-close"
                    onClick={(e) => {
                      e.stopPropagation();
                      const sendClose = closeTabSendersRef.current.get(t.id);
                      if (sendClose) {
                        sendClose();
                        // Let the close_tab frame flush before unmount closes the WebSocket.
                        window.setTimeout(() => onCloseTerminal(t.id), 50);
                      } else {
                        onCloseTerminal(t.id);
                      }
                    }}
                    title="Close Terminal"
                  >
                    &times;
                  </button>
                </div>
              ))}
            </div>
            {canScrollRight && (
              <button type="button" className="terminal-tab-scroll-btn right" onClick={() => scrollByAmount(200)} title="Scroll Right">
                ›
              </button>
            )}
          </div>
          <div className="terminal-tabs-actions">
            <Popover trigger="click" placement="bottomRight" content={splitPopover}>
              <button
                type="button"
                className={`terminal-split-btn${splitActive ? ' active' : ''}`}
                title="Split screen"
              >
                ⊞ Split
              </button>
            </Popover>
            <button
              type="button"
              className="terminal-maximize-btn"
              onClick={() => setIsMaximized((prev) => !prev)}
              title={isMaximized ? "Restore size" : "Maximize"}
            >
              {isMaximized ? '🗗' : '🗖'}
            </button>
            <button type="button" onClick={onCloseModal}>
              Close
            </button>
          </div>
        </header>

        {isProxmoxPveSerialConsole(activeTerm.record) && !isVnc ? (
          <p className="term-pve-hint" style={{ margin: '0.35rem 1rem', fontSize: '0.82rem', color: '#9aa4b2', flexShrink: 0 }}>
            Proxmox serial console: use <kbd>Ctrl+]</kbd> to disconnect, or Close tab. If the guest uses autologin on tty,{' '}
            <kbd>exit</kbd> may immediately open a new shell — that is normal on the guest.
          </p>
        ) : null}
        {isTrueNASAPIShellSession(activeTerm.record, activeTerm.truenasConsole ?? 'ssh') && !isVnc ? (
          <p className="term-pve-hint" style={{ margin: '0.35rem 1rem', fontSize: '0.82rem', color: '#9aa4b2', flexShrink: 0 }}>
            TrueNAS API shell: use <kbd>Ctrl+]</kbd> to disconnect. Requires Web Shell privilege for the API key user.
          </p>
        ) : null}

        <div className="modal-terminal-sessions-container" style={{ flex: 1, display: 'flex', flexDirection: 'column', minHeight: 0, padding: '0.5rem' }}>
          {/* Layout host: slot elements the sessions portal into. */}
          {layout === 'none' ? (
            <div
              className="term-split-slot"
              ref={getSlotRef(0)}
              style={{ flex: 1, minHeight: 0, display: 'flex' }}
            />
          ) : (
            <div className="term-split-grid" style={{ flex: 1, minHeight: 0 }}>
              {renderSplit()}
            </div>
          )}

          {/* Hidden holder keeps unassigned sessions mounted (ws alive). */}
          <div ref={registerHolder} style={{ display: 'none' }} />

          {/* Pool: every session mounted once, portaled into its slot/holder. */}
          {terminals.map((t) => {
            const { el, visible } = sessionTarget(t.id);
            const target = el ?? holderRef.current;
            if (!target) return null;
            return createPortal(
              <TerminalSession
                sessionId={t.id}
                record={t.record}
                sshUser={sshUser}
                recordSession={recordSession}
                assistAvailable={layout === 'none' ? assistAvailable : false}
                pveConsole={t.pve}
                truenasConsole={t.truenasConsole ?? 'ssh'}
                isActive={visible}
                registerCloseTabSender={registerCloseTabSender}
              />,
              target,
              t.id,
            );
          })}
        </div>
      </div>
    </div>
  );
}
