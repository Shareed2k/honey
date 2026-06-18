import { apiHeaders, apiGet, apiPost, readNDJSON } from './core';
import { LogsStreamRequest, LogsDefaultsResponse, FeedbackRecord, FeedbackSuggestResponse, LogTemplateStat } from './types/logs';



export async function fetchLogsDefaults(): Promise<LogsDefaultsResponse> {
  const r = await apiGet('/api/v1/logs/default');
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  return (await r.json()) as LogsDefaultsResponse;
}

export async function streamLogs(
  req: LogsStreamRequest,
  onLine: (line: string) => void,
  signal: AbortSignal,
): Promise<void> {
  const r = await fetch('/api/v1/logs/stream', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(req),
    signal,
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  await readNDJSON<{ line: string }>(r, (obj) => onLine(obj.line));
}

export async function fetchFeedbackRecords(): Promise<FeedbackRecord[]> {
  const r = await apiGet('/api/v1/logs/feedback');
  const j = (await r.json().catch(() => ({}))) as { records?: FeedbackRecord[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.records || [];
}

export async function saveFeedbackRecords(records: FeedbackRecord[]): Promise<void> {
  const r = await apiPost('/api/v1/logs/feedback', { records });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
}

export async function suggestFeedbackAnomaly(line: string, source: string): Promise<FeedbackSuggestResponse> {
  const r = await apiPost('/api/v1/logs/feedback/suggest', { line, source });
  if (!r.ok) {
    const err = await r.json().catch(() => ({}));
    throw new Error((err as { error?: string }).error || r.statusText);
  }
  return await r.json() as FeedbackSuggestResponse;
}

export async function fetchRcaDiagnosis(anomalyLine: string, context: string[], source: string): Promise<string> {
  const r = await apiPost('/api/v1/logs/rca', { anomaly_line: anomalyLine, context, source });
  if (!r.ok) {
    const err = await r.json().catch(() => ({}));
    throw new Error((err as { error?: string }).error || r.statusText);
  }
  const j = await r.json() as { markdown: string };
  return j.markdown;
}

export async function fetchLogSummary(stats: LogTemplateStat[]): Promise<string> {
  const r = await apiPost('/api/v1/logs/summary', { stats });
  if (!r.ok) {
    const err = await r.json().catch(() => ({}));
    throw new Error((err as { error?: string }).error || r.statusText);
  }
  const j = await r.json() as { markdown: string };
  return j.markdown;
}