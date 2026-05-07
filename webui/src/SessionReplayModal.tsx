import { useEffect, useMemo, useRef, useState } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import stripAnsi from 'strip-ansi';
import { fetchRecordingEvents } from './api';
import type { HostExecResultRow, RecordingEvent, RecordingListEntry } from './api';

type HostRecord = {
  provider: string;
  name: string;
  primary_ip: string;
};

type Props = {
  record: HostRecord;
  recordings: RecordingListEntry[];
  onClose: () => void;
};

const textDecoder = new TextDecoder();

function decodeB64(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) {
    out[i] = bin.charCodeAt(i);
  }
  return out;
}

/** Strip ANSI + normalize CR so log text does not repaint columns. */
function normalizeCapturedText(s: string): string {
  return stripAnsi(s.replace(/\r\n/g, '\n').replace(/\r/g, '\n'));
}

function recordingHasStructuredBatch(events: RecordingEvent[]): boolean {
  return events.some(
    (e) => e.type === 'result' || (e.type === 'data' && e.direction === 'plan'),
  );
}

function appendStructuredEvent(pre: HTMLElement, ev: RecordingEvent) {
  let chunk = '';
  switch (ev.type) {
    case 'open':
      // Shown once in the banner when the file loads / restarts (avoid duplicating during play).
      break;
    case 'close':
      chunk = '\n[closed]\n';
      break;
    case 'error':
      if (ev.message) {
        chunk = `\n[error] ${normalizeCapturedText(ev.message)}\n`;
      }
      break;
    case 'resize':
      if (ev.cols != null && ev.rows != null) {
        chunk = `\n[resize ${ev.cols}x${ev.rows}]\n`;
      }
      break;
    case 'result':
      if (ev.result) {
        chunk = formatResultBlock(ev.result);
      }
      break;
    case 'data':
      if (ev.direction === 'plan' && ev.data_b64) {
        const body = normalizeCapturedText(textDecoder.decode(decodeB64(ev.data_b64)));
        chunk = `\n[plan]\n${body}\n`;
      } else if ((ev.direction === 'stdout' || ev.direction === 'stderr') && ev.data_b64) {
        chunk = normalizeCapturedText(textDecoder.decode(decodeB64(ev.data_b64)));
      }
      break;
    default:
      break;
  }
  if (chunk) {
    pre.insertAdjacentText('beforeend', chunk);
  }
}

function formatResultBlock(r: HostExecResultRow): string {
  let s = `\n── ${r.Name} (${r.Provider})  ip=${r.IP}  ok=${r.Success}  exit=${r.ExitCode}\n`;
  if (r.ErrMsg) {
    s += `err: ${normalizeCapturedText(r.ErrMsg)}\n`;
  }
  if (r.Output) {
    s += `${normalizeCapturedText(r.Output)}\n`;
  }
  return s;
}

/** TTY replay: host header + optional colors (short lines only). */
function formatHostExecRowTTY(r: HostExecResultRow): string {
  const lines = [
    `\r\n\x1b[36m${r.Name}\x1b[0m (${r.Provider})  ip=${r.IP}  ok=${r.Success}  exit=${r.ExitCode}`,
  ];
  if (r.ErrMsg) {
    lines.push(`\x1b[31m  err:\x1b[0m ${normalizeCapturedText(r.ErrMsg)}`);
  }
  if (r.Output) {
    lines.push(`\x1b[0m  out:\n${normalizeCapturedText(r.Output)}`);
  }
  return lines.join('\n');
}

export function SessionReplayModal({ record, recordings, onClose }: Props) {
  const ref = useRef<HTMLDivElement>(null);
  const structuredRef = useRef<HTMLPreElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);

  const [selectedFile, setSelectedFile] = useState(recordings[0]?.file_name || '');
  const [events, setEvents] = useState<RecordingEvent[]>([]);
  const [loadErr, setLoadErr] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [playing, setPlaying] = useState(false);
  const [speed, setSpeed] = useState(1);
  const [cursor, setCursor] = useState(0);
  const [elapsedBase, setElapsedBase] = useState(0);
  const startTsRef = useRef(0);
  const elapsedRef = useRef(0);

  const hasStructuredLog = useMemo(() => recordingHasStructuredBatch(events), [events]);

  useEffect(() => {
    if (hasStructuredLog) {
      if (termRef.current) {
        termRef.current.dispose();
        termRef.current = null;
        fitRef.current = null;
      }
      return;
    }
    const el = ref.current;
    if (!el) {
      return;
    }
    const term = new Terminal({ cursorBlink: false, fontSize: 14, disableStdin: true });
    const fit = new FitAddon();
    term.loadAddon(fit);
    term.open(el);
    fit.fit();
    termRef.current = term;
    fitRef.current = fit;

    const onResize = () => fit.fit();
    window.addEventListener('resize', onResize);
    return () => {
      window.removeEventListener('resize', onResize);
      term.dispose();
      termRef.current = null;
      fitRef.current = null;
    };
  }, [hasStructuredLog]);

  useEffect(() => {
    if (!selectedFile) {
      setEvents([]);
      return;
    }
    setLoading(true);
    setLoadErr(null);
    setPlaying(false);
    setCursor(0);
    setElapsedBase(0);
    elapsedRef.current = 0;
    void (async () => {
      try {
        const loaded = await fetchRecordingEvents(selectedFile);
        const structured = recordingHasStructuredBatch(loaded);
        setEvents(loaded);
        queueMicrotask(() => {
          if (structured && structuredRef.current) {
            const open = loaded.find((e) => e.type === 'open');
            structuredRef.current.textContent =
              `Replaying ${selectedFile}\n` +
              (open?.message ? `[open] ${open.message}\n\n` : '\n');
          } else {
            const term = termRef.current;
            if (term) {
              term.clear();
              term.reset();
              term.writeln(`\x1b[36mReplaying ${selectedFile}\x1b[0m`);
            }
          }
        });
      } catch (e) {
        setLoadErr(e instanceof Error ? e.message : String(e));
        setEvents([]);
      } finally {
        setLoading(false);
      }
    })();
  }, [selectedFile]);

  useEffect(() => {
    if (!playing) {
      return;
    }
    startTsRef.current = performance.now();
    const id = window.setInterval(() => {
      const elapsed = elapsedRef.current + (performance.now() - startTsRef.current) * speed;
      setCursor((prev) => {
        let idx = prev;
        while (idx < events.length && (events[idx].time_ms || 0) <= elapsed) {
          const ev = events[idx];
          if (hasStructuredLog) {
            const pre = structuredRef.current;
            if (pre) {
              appendStructuredEvent(pre, ev);
              pre.scrollTop = pre.scrollHeight;
            }
          } else {
            const term = termRef.current;
            if (!term) {
              idx++;
              continue;
            }
            if (ev.type === 'data' && (ev.direction === 'stdout' || ev.direction === 'stderr') && ev.data_b64) {
              term.write(decodeB64(ev.data_b64));
            } else if (ev.type === 'data' && ev.direction === 'plan' && ev.data_b64) {
              const text = normalizeCapturedText(textDecoder.decode(decodeB64(ev.data_b64)));
              term.writeln(`\r\n\x1b[35m[plan]\x1b[0m\n${text}`);
            } else if (ev.type === 'result' && ev.result) {
              term.writeln(formatHostExecRowTTY(ev.result));
            } else if (ev.type === 'open' && ev.message) {
              term.writeln(`\r\n\x1b[34m[open] ${ev.message}\x1b[0m`);
            } else if (ev.type === 'error' && ev.message) {
              term.writeln(`\r\n\x1b[31m[error] ${ev.message}\x1b[0m`);
            } else if (ev.type === 'close') {
              term.writeln('\r\n\x1b[33m[closed]\x1b[0m');
            }
          }
          idx++;
        }
        if (idx >= events.length) {
          setPlaying(false);
        }
        return idx;
      });
      setElapsedBase(elapsed);
      elapsedRef.current = elapsed;
      startTsRef.current = performance.now();
    }, 30);
    return () => window.clearInterval(id);
  }, [events, playing, speed, hasStructuredLog]);

  const duration = useMemo(() => (events.length ? events[events.length - 1].time_ms || 0 : 0), [events]);

  return (
    <div className="modal-backdrop" role="presentation">
      <div className="modal" role="dialog" aria-label={`Replay: ${record.name}`}>
        <header>
          <strong>
            Replay
            {record.primary_ip?.trim()
              ? ` — ${record.name} (${record.primary_ip})`
              : record.name
                ? ` — ${record.name}`
                : ''}
          </strong>
          <button type="button" onClick={onClose}>
            Close
          </button>
        </header>
        <div className="modal-replay-toolbar">
          <select
            value={selectedFile}
            onChange={(e) => setSelectedFile(e.target.value)}
            style={{ minWidth: 280, maxWidth: 480 }}
          >
            {recordings.map((r) => (
              <option key={r.file_name} value={r.file_name}>
                {r.file_name}
              </option>
            ))}
          </select>
          <button
            type="button"
            disabled={loading || events.length === 0}
            onClick={() => {
              const term = termRef.current;
              if (term) {
                term.clear();
                term.reset();
              }
              const pre = structuredRef.current;
              if (pre && selectedFile) {
                const open = events.find((e) => e.type === 'open');
                pre.textContent =
                  `Replaying ${selectedFile}\n` +
                  (open?.message ? `[open] ${open.message}\n\n` : '\n');
              }
              setCursor(0);
              setElapsedBase(0);
              elapsedRef.current = 0;
            }}
          >
            Restart
          </button>
          <button type="button" disabled={loading || events.length === 0} onClick={() => setPlaying((p) => !p)}>
            {playing ? 'Pause' : 'Play'}
          </button>
          <label style={{ fontSize: '0.85rem' }}>
            Speed{' '}
            <select value={speed} onChange={(e) => setSpeed(Number(e.target.value))}>
              <option value={0.5}>0.5x</option>
              <option value={1}>1x</option>
              <option value={2}>2x</option>
              <option value={4}>4x</option>
            </select>
          </label>
          <span style={{ fontSize: '0.8rem', opacity: 0.8 }}>
            {Math.round(elapsedBase)}ms / {duration}ms
          </span>
        </div>
        {loadErr ? <p style={{ color: '#f66', marginTop: 0 }}>{loadErr}</p> : null}
        <div className="term-wrap">
          {!hasStructuredLog ? (
            <div className="term-xterm-host" ref={ref} />
          ) : (
            <div className="term-xterm-host">
              <pre ref={structuredRef} className="term-structured-replay" />
            </div>
          )}
          {loading ? (
            <div className="term-connect-overlay" aria-live="polite" aria-atomic="true">
              <div className="term-spinner" role="status" />
              <span className="sr-only">Loading recording…</span>
            </div>
          ) : null}
        </div>
      </div>
    </div>
  );
}
