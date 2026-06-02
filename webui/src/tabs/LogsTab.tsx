import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert, AutoComplete, Button, Checkbox, Collapse, Input, InputNumber, Select, Space, Spin, Typography,
} from 'antd';
import { PlayCircleOutlined, StopOutlined, ClearOutlined } from '@ant-design/icons';
import type { HostRecord } from '../HostPicker';
import { apiPost, fetchLogsDefaults, streamLogs } from '../api';
import type { LogsStreamRequest } from '../api';

const ANOM_RE = /^\[ANOM /;
const LS_KEY = 'hostctl_logs_filters';

function loadSaved(): Record<string, unknown> {
  try { return JSON.parse(localStorage.getItem(LS_KEY) ?? '{}'); } catch { return {}; }
}

function hasSavedField(saved: Record<string, unknown>, key: string): boolean {
  return Object.prototype.hasOwnProperty.call(saved, key);
}

interface Props {
  sshUser: string;
  providers: string[];
  backends: string[];
  logsCommandAllowed?: boolean;
}

export function LogsTab({ sshUser, providers, backends, logsCommandAllowed = false }: Props) {
  const saved = useMemo(loadSaved, []);

  const [target, setTarget] = useState<string>((saved.target as string) ?? '');
  const [source, setSource] = useState<string>((saved.source as string) ?? '');
  const [grep, setGrep] = useState<string>((saved.grep as string) ?? '');
  const [since, setSince] = useState<string>((saved.since as string) ?? '');
  const [container, setContainer] = useState<string>((saved.container as string) ?? '');
  const [unit, setUnit] = useState<string>((saved.unit as string) ?? '');
  const [command, setCommand] = useState<string>((saved.command as string) ?? '');
  const [runAs, setRunAs] = useState<string>((saved.runAs as string) ?? '');
  const [follow, setFollow] = useState<boolean>((saved.follow as boolean) ?? false);
  const [tail, setTail] = useState<number>((saved.tail as number) ?? 100);

  const [anomaly, setAnomaly] = useState<boolean>((saved.anomaly as boolean) ?? false);
  const [anomalyPreprocessor, setAnomalyPreprocessor] = useState<string>((saved.anomalyPreprocessor as string) ?? '');
  const [anomalyThreshold, setAnomalyThreshold] = useState<number>((saved.anomalyThreshold as number) ?? 0.90);
  const [anomalyOnly, setAnomalyOnly] = useState<boolean>((saved.anomalyOnly as boolean) ?? false);
  const [anomalyEndpoint, setAnomalyEndpoint] = useState<string>((saved.anomalyEndpoint as string) ?? '');
  const [anomalyLLMModel, setAnomalyLLMModel] = useState<string>((saved.anomalyLLMModel as string) ?? 'llama3');
  const [anomalyContext, setAnomalyContext] = useState<number>((saved.anomalyContext as number) ?? 5);
  const [anomalyFilterThresh, setAnomalyFilterThresh] = useState<number>((saved.anomalyFilterThresh as number) ?? 0);
  const [anomalyFreqWindow, setAnomalyFreqWindow] = useState<number>((saved.anomalyFreqWindow as number) ?? 100);
  const [anomalyFreqRatio, setAnomalyFreqRatio] = useState<number>((saved.anomalyFreqRatio as number) ?? 5.0);

  const [lines, setLines] = useState<string[]>([]);
  const [streaming, setStreaming] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [autoScroll, setAutoScroll] = useState(true);

  const [searchOptions, setSearchOptions] = useState<{ value: string; label: React.ReactNode }[]>([]);
  const [searching, setSearching] = useState(false);
  const searchTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  const abortRef = useRef<AbortController | null>(null);
  const logEndRef = useRef<HTMLDivElement | null>(null);
  const logBoxRef = useRef<HTMLDivElement | null>(null);

  // Persist filter state to localStorage
  useEffect(() => {
    localStorage.setItem(LS_KEY, JSON.stringify({
      target, source, grep, since, container, unit, command, runAs, follow, tail,
      anomaly, anomalyPreprocessor, anomalyThreshold, anomalyOnly, anomalyEndpoint, anomalyLLMModel,
      anomalyContext, anomalyFilterThresh, anomalyFreqWindow, anomalyFreqRatio,
    }));
  }, [
    target, source, grep, since, container, unit, command, runAs, follow, tail,
    anomaly, anomalyPreprocessor, anomalyThreshold, anomalyOnly, anomalyEndpoint, anomalyLLMModel,
    anomalyContext, anomalyFilterThresh, anomalyFreqWindow, anomalyFreqRatio,
  ]);

  useEffect(() => {
    if (autoScroll) {
      logEndRef.current?.scrollIntoView({ behavior: 'auto' });
    }
  }, [lines, autoScroll]);

  useEffect(() => {
    let cancelled = false;
    (async () => {
      try {
        const defaults = await fetchLogsDefaults();
        if (cancelled) {
          return;
        }
        if (!hasSavedField(saved, 'anomaly')) {
          setAnomaly(defaults.anomaly);
        }
        if (!hasSavedField(saved, 'anomalyPreprocessor')) {
          setAnomalyPreprocessor(defaults.anomaly_preprocessor || '');
        }
        if (!hasSavedField(saved, 'anomalyThreshold')) {
          setAnomalyThreshold(defaults.anomaly_threshold);
        }
        if (!hasSavedField(saved, 'anomalyOnly')) {
          setAnomalyOnly(defaults.anomaly_only);
        }
        if (!hasSavedField(saved, 'anomalyEndpoint')) {
          setAnomalyEndpoint(defaults.anomaly_endpoint ?? '');
        }
        if (!hasSavedField(saved, 'anomalyLLMModel')) {
          setAnomalyLLMModel(defaults.anomaly_llm_model ?? 'llama3');
        }
        if (!hasSavedField(saved, 'anomalyContext')) {
          setAnomalyContext(defaults.anomaly_context_lines);
        }
        if (!hasSavedField(saved, 'anomalyFilterThresh')) {
          setAnomalyFilterThresh(defaults.anomaly_filter_threshold);
        }
        if (!hasSavedField(saved, 'anomalyFreqWindow')) {
          setAnomalyFreqWindow(defaults.anomaly_freq_window);
        }
        if (!hasSavedField(saved, 'anomalyFreqRatio')) {
          setAnomalyFreqRatio(defaults.anomaly_freq_ratio);
        }
      } catch {
        // Keep existing local defaults when endpoint is unavailable.
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [saved]);

  const handleScroll = useCallback(() => {
    const el = logBoxRef.current;
    if (!el) return;
    const atBottom = el.scrollHeight - el.scrollTop - el.clientHeight < 20;
    setAutoScroll(atBottom);
  }, []);

  const handleTargetSearch = useCallback((val: string) => {
    if (searchTimerRef.current) clearTimeout(searchTimerRef.current);
    if (!val.trim()) { setSearchOptions([]); return; }
    searchTimerRef.current = setTimeout(async () => {
      setSearching(true);
      try {
        const res = await apiPost('/api/v1/search', {
          name: val.trim(),
          providers: providers.join(','),
          backends: backends.join(','),
          ssh_user: sshUser,
        });
        if (!res.ok) { setSearchOptions([]); return; }
        const data = (await res.json()) as { records?: HostRecord[] };
        const records = data.records ?? [];
        setSearchOptions(records.map((r) => ({
          value: r.name,
          label: (
            <span>
              {r.name}
              {' '}
              <Typography.Text type="secondary" style={{ fontSize: 11 }}>
                {r.provider}{r.region ? `/${r.region}` : ''}
              </Typography.Text>
            </span>
          ),
        })));
      } catch {
        setSearchOptions([]);
      } finally {
        setSearching(false);
      }
    }, 300);
  }, [providers, backends, sshUser]);

  const handleStart = useCallback(async () => {
    if (!target.trim()) {
      setError('Target is required');
      return;
    }
    setError(null);
    setStreaming(true);
    setLines([]);

    const ctrl = new AbortController();
    abortRef.current = ctrl;

    try {
      const searchRes = await apiPost('/api/v1/search', {
        name: target.trim(),
        providers: providers.join(','),
        backends: backends.join(','),
        ssh_user: sshUser,
      });
      if (!searchRes.ok) {
        const j = (await searchRes.json().catch(() => ({}))) as { error?: string };
        throw new Error(j.error || searchRes.statusText);
      }
      const searchData = (await searchRes.json()) as { records?: HostRecord[] };
      const records = searchData.records ?? [];
      if (records.length === 0) {
        throw new Error(`No hosts match "${target.trim()}"`);
      }

      const req: LogsStreamRequest = {
        records,
        ssh_user: sshUser || undefined,
        source: source || undefined,
        follow,
        tail,
        since: since || undefined,
        container: container || undefined,
        unit: unit || undefined,
        command: command || undefined,
        run_as: runAs || undefined,
        grep: grep || undefined,
        anomaly,
        anomaly_preprocessor: anomalyPreprocessor || undefined,
        anomaly_threshold: anomalyThreshold,
        anomaly_only: anomalyOnly,
        anomaly_endpoint: anomalyEndpoint || undefined,
        anomaly_llm_model: anomalyLLMModel || undefined,
        anomaly_context: anomalyContext,
        anomaly_filter_threshold: anomalyFilterThresh,
        anomaly_freq_window: anomalyFreqWindow,
        anomaly_freq_ratio: anomalyFreqRatio,
      };

      await streamLogs(req, (line) => {
        setLines((prev) => {
          const next = prev.length >= 5000 ? prev.slice(-4999) : prev;
          return [...next, line];
        });
      }, ctrl.signal);
    } catch (e: unknown) {
      if (e instanceof Error && e.name === 'AbortError') return;
      setError(e instanceof Error ? e.message : String(e));
    } finally {
      setStreaming(false);
    }
  }, [
    target, source, grep, since, container, unit, command, runAs, follow, tail,
    anomaly, anomalyPreprocessor, anomalyThreshold, anomalyOnly, anomalyEndpoint, anomalyLLMModel,
    anomalyContext, anomalyFilterThresh, anomalyFreqWindow, anomalyFreqRatio,
    sshUser, providers, backends,
  ]);

  const handleStop = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  const handleClear = useCallback(() => {
    setLines([]);
  }, []);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', padding: '16px', gap: '12px' }}>
      {/* Controls */}
      <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
        <Space wrap>
          <AutoComplete
            value={target}
            options={searchOptions}
            onSearch={handleTargetSearch}
            onChange={setTarget}
            placeholder="Target (e.g. prod-api)"
            style={{ width: 280 }}
            allowClear
            notFoundContent={searching ? <Spin size="small" /> : null}
            onKeyDown={(e) => { if (e.key === 'Enter' && !streaming) handleStart(); }}
          />
          <Input
            placeholder="Source (file/unit, optional)"
            value={source}
            onChange={(e) => setSource(e.target.value)}
            style={{ width: 200 }}
          />
          {!streaming ? (
            <Button type="primary" icon={<PlayCircleOutlined />} onClick={handleStart}>
              Stream
            </Button>
          ) : (
            <Button danger icon={<StopOutlined />} onClick={handleStop}>
              Stop
            </Button>
          )}
        </Space>

        <Space wrap>
          <Space>
            <Typography.Text type="secondary">Tail</Typography.Text>
            <InputNumber
              min={1} max={10000} value={tail}
              onChange={(v) => setTail(v ?? 100)}
              style={{ width: 80 }}
            />
          </Space>
          <Input
            placeholder="Since (e.g. 1h)"
            value={since}
            onChange={(e) => setSince(e.target.value)}
            style={{ width: 120 }}
          />
          <Input
            placeholder="Grep"
            value={grep}
            onChange={(e) => setGrep(e.target.value)}
            style={{ width: 160 }}
          />
          <Checkbox checked={follow} onChange={(e) => setFollow(e.target.checked)}>
            Follow
          </Checkbox>
        </Space>

        {/* Advanced / Anomaly */}
        <Collapse
          size="small"
          items={[{
            key: 'advanced',
            label: 'Advanced & Anomaly Detection',
            children: (
              <div style={{ display: 'flex', flexDirection: 'column', gap: '8px' }}>
                <Space wrap>
                  <Input placeholder="Container" value={container} onChange={(e) => setContainer(e.target.value)} style={{ width: 160 }} />
                  <Input placeholder="Unit" value={unit} onChange={(e) => setUnit(e.target.value)} style={{ width: 160 }} />
                  <Input
                    placeholder="Command"
                    value={command}
                    onChange={(e) => setCommand(e.target.value)}
                    style={{ width: 200 }}
                    disabled={!logsCommandAllowed}
                    title={logsCommandAllowed ? undefined : 'Start honey web with --allow-logs-command to enable'}
                  />
                  <Input placeholder="Run as user" value={runAs} onChange={(e) => setRunAs(e.target.value)} style={{ width: 140 }} />
                </Space>
                <Space wrap>
                  <Checkbox checked={anomaly} onChange={(e) => setAnomaly(e.target.checked)}>
                    Enable anomaly detection
                  </Checkbox>
                  <Checkbox checked={anomalyOnly} onChange={(e) => setAnomalyOnly(e.target.checked)} disabled={!anomaly}>
                    Anomaly-only
                  </Checkbox>
                  <Space>
                    <Typography.Text type="secondary">Preprocessor</Typography.Text>
                    <Select
                      value={anomalyPreprocessor}
                      onChange={(val) => setAnomalyPreprocessor(val)}
                      style={{ width: 120 }}
                      disabled={!anomaly}
                      options={[
                        { value: '', label: 'None' },
                        { value: 'lshd', label: 'LSHD' },
                      ]}
                    />
                  </Space>
                </Space>
                <Space wrap>
                  <Space>
                    <Typography.Text type="secondary">Threshold</Typography.Text>
                    <InputNumber
                      min={0.01} max={1} step={0.01} value={anomalyThreshold}
                      onChange={(v) => setAnomalyThreshold(v ?? 0.90)}
                      style={{ width: 80 }} disabled={!anomaly}
                    />
                  </Space>
                  <Space>
                    <Typography.Text type="secondary">Freq window</Typography.Text>
                    <InputNumber
                      min={0} max={10000} value={anomalyFreqWindow}
                      onChange={(v) => setAnomalyFreqWindow(v ?? 100)}
                      style={{ width: 80 }} disabled={!anomaly}
                    />
                  </Space>
                  <Space>
                    <Typography.Text type="secondary">Freq ratio</Typography.Text>
                    <InputNumber
                      min={0} step={0.5} value={anomalyFreqRatio}
                      onChange={(v) => setAnomalyFreqRatio(v ?? 5.0)}
                      style={{ width: 80 }} disabled={!anomaly}
                    />
                  </Space>
                </Space>
                <Space wrap>
                  <Input
                    placeholder="LLM endpoint (e.g. http://localhost:11434/v1)"
                    value={anomalyEndpoint}
                    onChange={(e) => setAnomalyEndpoint(e.target.value)}
                    style={{ width: 320 }} disabled={!anomaly}
                  />
                  <Input
                    placeholder="LLM model (e.g. llama3)"
                    value={anomalyLLMModel}
                    onChange={(e) => setAnomalyLLMModel(e.target.value)}
                    style={{ width: 180 }} disabled={!anomaly}
                  />
                  <Space>
                    <Typography.Text type="secondary">Context lines</Typography.Text>
                    <InputNumber
                      min={0} max={20} value={anomalyContext}
                      onChange={(v) => setAnomalyContext(v ?? 5)}
                      style={{ width: 70 }} disabled={!anomaly}
                    />
                  </Space>
                  <Space>
                    <Typography.Text type="secondary">Filter threshold</Typography.Text>
                    <InputNumber
                      min={0} max={1} step={0.05} value={anomalyFilterThresh}
                      onChange={(v) => setAnomalyFilterThresh(v ?? 0)}
                      style={{ width: 80 }} disabled={!anomaly}
                    />
                  </Space>
                </Space>
              </div>
            ),
          }]}
        />
      </div>

      {error && <Alert type="error" message={error} closable onClose={() => setError(null)} />}

      {/* Log viewer */}
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <Typography.Text type="secondary">{lines.length} line{lines.length !== 1 ? 's' : ''}</Typography.Text>
        <Button size="small" icon={<ClearOutlined />} onClick={handleClear}>Clear</Button>
      </div>

      <div
        ref={logBoxRef}
        onScroll={handleScroll}
        style={{
          flex: 1,
          overflowY: 'auto',
          background: '#0f1319',
          border: '1px solid #2a3140',
          borderRadius: 6,
          padding: '8px 12px',
          fontFamily: 'monospace',
          fontSize: 13,
          lineHeight: '1.6',
          minHeight: 0,
        }}
      >
        {lines.map((line, i) => {
          const isAnom = ANOM_RE.test(line);
          return (
            <div
              key={i}
              style={{
                whiteSpace: 'pre-wrap',
                wordBreak: 'break-all',
                background: isAnom ? 'rgba(239,68,68,0.12)' : undefined,
                color: isAnom ? '#fca5a5' : '#d1d5db',
                padding: isAnom ? '1px 4px' : undefined,
                borderRadius: isAnom ? 3 : undefined,
              }}
            >
              {line}
            </div>
          );
        })}
        <div ref={logEndRef} />
      </div>
    </div>
  );
}
