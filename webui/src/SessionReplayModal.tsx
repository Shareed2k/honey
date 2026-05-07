import { useEffect, useMemo, useRef, useState } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import { fetchRecordingEvents } from './api';
import type { RecordingEvent, RecordingListEntry } from './api';

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

function decodeB64(s: string): Uint8Array {
  const bin = atob(s);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) {
    out[i] = bin.charCodeAt(i);
  }
  return out;
}

export function SessionReplayModal({ record, recordings, onClose }: Props) {
  const ref = useRef<HTMLDivElement>(null);
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

  useEffect(() => {
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
  }, []);

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
        setEvents(loaded);
        const term = termRef.current;
        if (term) {
          term.clear();
          term.reset();
          term.writeln(`\x1b[36mReplaying ${selectedFile}\x1b[0m`);
        }
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
      const term = termRef.current;
      if (!term) {
        return;
      }
      const elapsed = elapsedRef.current + (performance.now() - startTsRef.current) * speed;
      setCursor((prev) => {
        let idx = prev;
        while (idx < events.length && (events[idx].time_ms || 0) <= elapsed) {
          const ev = events[idx];
          if (ev.type === 'data' && (ev.direction === 'stdout' || ev.direction === 'stderr') && ev.data_b64) {
            term.write(decodeB64(ev.data_b64));
          } else if (ev.type === 'open' && ev.message) {
            term.writeln(`\r\n\x1b[34m[open] ${ev.message}\x1b[0m`);
          } else if (ev.type === 'error' && ev.message) {
            term.writeln(`\r\n\x1b[31m[error] ${ev.message}\x1b[0m`);
          } else if (ev.type === 'close') {
            term.writeln('\r\n\x1b[33m[closed]\x1b[0m');
          }
          idx++
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
  }, [events, playing, speed]);

  const duration = useMemo(() => (events.length ? events[events.length - 1].time_ms || 0 : 0), [events]);

  return (
    <div className="modal-backdrop" role="presentation">
      <div className="modal" role="dialog" aria-label={`Replay: ${record.name}`} style={{ width: 'min(1000px, 96vw)' }}>
        <header>
          <strong>
            Replay session — {record.name} ({record.primary_ip})
          </strong>
          <button type="button" onClick={onClose}>
            Close
          </button>
        </header>
        <div style={{ display: 'flex', gap: '0.6rem', alignItems: 'center', marginBottom: '0.45rem', flexWrap: 'wrap' }}>
          <select
            value={selectedFile}
            onChange={(e) => setSelectedFile(e.target.value)}
            style={{ minWidth: 360, maxWidth: 560 }}
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
          <div className="term-xterm-host" ref={ref} />
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
