import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Terminal } from '@xterm/xterm';
import { FitAddon } from '@xterm/addon-fit';
import stripAnsi from 'strip-ansi';
import { Alert, Button, Modal, Select, Space, Spin, Typography } from 'antd';
import { deleteRecording, fetchRecordingEvents, fetchTerminalAssistModels, summarizeRecording } from './api';
import type {
  HostExecResultRow,
  RecordingEvent,
  RecordingListEntry,
  RecordingsRetentionInfo,
} from './api';

const AiMarkdown = lazy(async () => import('./AiMarkdown').then((m) => ({ default: m.AiMarkdown })));

type HostRecord = {
  provider: string;
  name: string;
  primary_ip: string;
};

function formatBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) {
    return '0 B';
  }
  if (n < 1024) {
    return `${n} B`;
  }
  if (n < 1024 * 1024) {
    return `${(n / 1024).toFixed(1)} KiB`;
  }
  return `${(n / (1024 * 1024)).toFixed(1)} MiB`;
}

type Props = {
  record: HostRecord;
  recordings: RecordingListEntry[];
  onClose: () => void;
  onRecordingsChange?: () => void;
  listStats?: { file_count: number; total_bytes: number };
  retention?: RecordingsRetentionInfo;
  assistAvailable?: boolean;
  onRetryFailed?: (fileName: string) => void;
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
  if (r.HookPhase || r.HookOutput) {
    s += `hook (${r.HookPhase || '?'}):\n`;
    if (r.HookOutput) {
      s += `${normalizeCapturedText(r.HookOutput)}\n`;
    }
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
  if (r.HookPhase || r.HookOutput) {
    lines.push(`\x1b[35m  hook (${r.HookPhase || '?'}):\x1b[0m`);
    if (r.HookOutput) {
      lines.push(normalizeCapturedText(r.HookOutput));
    }
  }
  return lines.join('\n');
}

export function SessionReplayModal({
  record,
  recordings,
  onClose,
  onRecordingsChange,
  listStats,
  retention,
  assistAvailable,
  onRetryFailed,
}: Props) {
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
  const [_cursor, setCursor] = useState(0);
  const [elapsedBase, setElapsedBase] = useState(0);
  const startTsRef = useRef(0);
  const elapsedRef = useRef(0);
  const [summary, setSummary] = useState<string | null>(null);
  const [summaryBusy, setSummaryBusy] = useState(false);
  const [summaryErr, setSummaryErr] = useState<string | null>(null);
  const [deleteBusy, setDeleteBusy] = useState(false);
  const [assistModels, setAssistModels] = useState<string[]>([]);
  const [assistModelsLoading, setAssistModelsLoading] = useState(false);
  const [assistModelsErr, setAssistModelsErr] = useState<string | null>(null);
  const [assistSelectedModel, setAssistSelectedModel] = useState('');

  const hasStructuredLog = useMemo(() => recordingHasStructuredBatch(events), [events]);
  const assistCanSummarize =
    assistModels.length > 0 && assistSelectedModel.trim() !== '' && !assistModelsLoading;

  const refreshList = useCallback(() => {
    onRecordingsChange?.();
  }, [onRecordingsChange]);

  useEffect(() => {
    if (!assistAvailable) {
      return undefined;
    }
    let cancelled = false;
    setAssistModelsLoading(true);
    setAssistModelsErr(null);
    void (async () => {
      try {
        const list = await fetchTerminalAssistModels();
        if (cancelled) {
          return;
        }
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

  async function handleDelete() {
    if (!selectedFile || deleteBusy) return;
    Modal.confirm({
      title: `Delete recording ${selectedFile}?`,
      okText: 'Delete',
      okButtonProps: { danger: true },
      onOk: async () => {
        setDeleteBusy(true);
        try {
          await deleteRecording(selectedFile);
          refreshList();
          onClose();
        } catch (e) {
          setLoadErr(e instanceof Error ? e.message : String(e));
        } finally {
          setDeleteBusy(false);
        }
      },
    });
  }

  async function handleSummarize() {
    if (!selectedFile || summaryBusy || !assistAvailable) {
      return;
    }
    if (!assistCanSummarize) {
      setSummaryErr('Pick a model from the list (models must load from the server).');
      return;
    }
    setSummaryBusy(true);
    setSummaryErr(null);
    try {
      setSummary(await summarizeRecording(selectedFile, assistSelectedModel));
    } catch (e) {
      setSummaryErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSummaryBusy(false);
    }
  }

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
    <Modal
      open
      title={`Replay${
        record.primary_ip?.trim()
          ? ` — ${record.name} (${record.primary_ip})`
          : record.name
            ? ` — ${record.name}`
            : ''
      }`}
      onCancel={onClose}
      footer={null}
      width="min(960px, 96vw)"
      styles={{ body: { height: 'min(580px, 80vh)', display: 'flex', flexDirection: 'column', padding: '8px 12px' } }}
      destroyOnHidden
    >
      {listStats ? (
        <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 8, fontSize: '0.85rem' }}>
          {listStats.file_count} file{listStats.file_count === 1 ? '' : 's'} · {formatBytes(listStats.total_bytes)}
          {retention?.enabled && retention.max_age ? (
            <> · auto-delete older than {retention.max_age}</>
          ) : null}
        </Typography.Text>
      ) : null}
      <div className="modal-replay-toolbar">
        <Select
          value={selectedFile}
          onChange={(v) => setSelectedFile(v)}
          style={{ minWidth: 200, maxWidth: 400, flex: 1 }}
          options={recordings.map((r) => ({ value: r.file_name, label: r.file_name }))}
        />
        <Button
          disabled={loading || events.length === 0}
          onClick={() => {
            const term = termRef.current;
            if (term) { term.clear(); term.reset(); }
            const pre = structuredRef.current;
            if (pre && selectedFile) {
              const open = events.find((e) => e.type === 'open');
              pre.textContent = `Replaying ${selectedFile}\n` + (open?.message ? `[open] ${open.message}\n\n` : '\n');
            }
            setCursor(0);
            setElapsedBase(0);
            elapsedRef.current = 0;
          }}
        >
          Restart
        </Button>
        <Button disabled={loading || events.length === 0} onClick={() => setPlaying((p) => !p)}>
          {playing ? 'Pause' : 'Play'}
        </Button>
        <Space size={4}>
          <Typography.Text type="secondary">Speed</Typography.Text>
          <Select
            value={speed}
            onChange={(v) => setSpeed(Number(v))}
            style={{ width: 72 }}
            size="small"
            options={[
              { value: 0.5, label: '0.5x' },
              { value: 1, label: '1x' },
              { value: 2, label: '2x' },
              { value: 4, label: '4x' },
            ]}
          />
        </Space>
        <Typography.Text type="secondary">
          {Math.round(elapsedBase)}ms / {duration}ms
        </Typography.Text>
        {assistAvailable ? (
          <>
            {assistModelsLoading ? (
              <Space size={4}>
                <Spin size="small" />
                <Typography.Text type="secondary">Loading models…</Typography.Text>
              </Space>
            ) : assistModels.length > 0 ? (
              <Space size={4}>
                <Typography.Text type="secondary">Model</Typography.Text>
                <Select
                  value={assistSelectedModel}
                  onChange={(v) => setAssistSelectedModel(v)}
                  style={{ minWidth: 140 }}
                  size="small"
                  options={assistModels.map((id) => ({ value: id, label: id }))}
                />
              </Space>
            ) : (
              <Typography.Text type="warning">No models available</Typography.Text>
            )}
            <Button
              disabled={loading || !selectedFile || summaryBusy || !assistCanSummarize}
              onClick={() => void handleSummarize()}
            >
              {summaryBusy ? 'Summarizing…' : 'Summarize run'}
            </Button>
          </>
        ) : null}
        {hasStructuredLog && onRetryFailed ? (
          <Button
            disabled={loading || !selectedFile}
            onClick={() => onRetryFailed?.(selectedFile)}
          >
            Retry Failed
          </Button>
        ) : null}
        <Button danger disabled={!selectedFile || deleteBusy} onClick={() => void handleDelete()}>
          {deleteBusy ? 'Deleting…' : 'Delete'}
        </Button>
      </div>
      {loadErr ? <Alert type="error" message={loadErr} showIcon style={{ marginBottom: 8 }} /> : null}
      {assistModelsErr ? <Alert type="warning" message={assistModelsErr} showIcon style={{ marginBottom: 8 }} /> : null}
      {summaryErr ? <Alert type="error" message={summaryErr} showIcon style={{ marginBottom: 8 }} /> : null}
      {summary ? (
        <div className="rcp-summary">
          <Suspense fallback={<pre>{summary}</pre>}>
            <AiMarkdown content={summary} />
          </Suspense>
        </div>
      ) : null}
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
            <Spin />
          </div>
        ) : null}
      </div>
    </Modal>
  );
}
