import { apiGet, apiPost, apiDelete } from './core';
import { RecordingsListResponse, RecordingListEntry, RecordingEvent } from './types/recordings';



/** List recordings; omit filters to return everything in record-dir (e.g. batch exec files use host_name batch-N). */
export async function fetchRecordingsList(filters?: {
  provider?: string;
  host_name?: string;
  host_ip?: string;
}): Promise<RecordingsListResponse> {
  const q = new URLSearchParams();
  if (filters?.provider?.trim()) {
    q.set('provider', filters.provider.trim());
  }
  if (filters?.host_name?.trim()) {
    q.set('host_name', filters.host_name.trim());
  }
  if (filters?.host_ip?.trim()) {
    q.set('host_ip', filters.host_ip.trim());
  }
  const qs = q.toString();
  const path = qs ? `/api/v1/recordings?${qs}` : '/api/v1/recordings';
  const r = await apiGet(path);
  const j = (await r.json().catch(() => ({}))) as RecordingsListResponse & { error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return {
    items: j.items || [],
    file_count: j.file_count ?? (j.items?.length ?? 0),
    total_bytes: j.total_bytes ?? 0,
    retention: j.retention,
  };
}

export async function deleteRecording(fileName: string): Promise<void> {
  const r = await apiDelete(`/api/v1/recordings/${encodeURIComponent(fileName)}`);
  const j = (await r.json().catch(() => ({}))) as { error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
}

export async function summarizeRecording(fileName: string, model: string): Promise<string> {
  const r = await apiPost('/api/v1/recordings/summarize', { file_name: fileName, model: model.trim() });
  const j = (await r.json().catch(() => ({}))) as { reply?: string; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.reply ?? '';
}

export async function fetchRecordingsForHost(params: {
  provider: string;
  host_name: string;
  host_ip: string;
}): Promise<RecordingListEntry[]> {
  const resp = await fetchRecordingsList({
    provider: params.provider,
    host_name: params.host_name,
    host_ip: params.host_ip,
  });
  return resp.items;
}

export async function fetchRecordingsFailedHosts(fileName: string): Promise<HostRecord[]> {
  const r = await apiGet(`/api/v1/recordings/${encodeURIComponent(fileName.replace('.hrec.jsonl', ''))}/failed-hosts`);
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  return await r.json();
}

export async function fetchRecordingEvents(fileName: string): Promise<RecordingEvent[]> {
  const r = await apiPost('/api/v1/recordings/play', { file_name: fileName });
  const j = (await r.json().catch(() => ({}))) as { events?: RecordingEvent[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.events || [];
}