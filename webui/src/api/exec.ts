import { apiHeaders, apiGet, apiPost, apiDelete, readNDJSON } from './core';
import { HostExecResultRow, ExecOnHostsBody, LintResponse, ExecSnippet, CueExecRequest } from './types/exec';



export async function fetchHostPorts(req: { ssh_user: string; record: unknown }): Promise<string[]> {
  const r = await apiPost('/api/v1/host-ports', req);
  if (!r.ok) {
    const j = await r.json().catch(() => ({}));
    throw new Error((j as { error?: string }).error || r.statusText);
  }
  const j = (await r.json()) as { ports: string[] };
  return j.ports || [];
}

export async function execOnHosts(body: {
  ssh_user: string;
  command: string;
  records: unknown[];
  record_session?: boolean;
}): Promise<HostExecResultRow[]> {
  const r = await apiPost('/api/v1/exec', body);
  const j = (await r.json().catch(() => ({}))) as { results?: HostExecResultRow[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.results || [];
}

export async function lintScript(language: 'bash' | 'python', content: string): Promise<LintResponse> {
  const r = await fetch('/api/v1/lint', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify({ language, content }),
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  return (await r.json()) as LintResponse;
}

export async function listSnippets(): Promise<ExecSnippet[]> {
  const r = await apiGet('/api/v1/snippets');
  if (!r.ok) throw new Error(r.statusText);
  return (await r.json()) as ExecSnippet[];
}

export async function saveSnippet(s: Omit<ExecSnippet, 'id'> & { id?: string }): Promise<ExecSnippet> {
  const r = await apiPost('/api/v1/snippets', s);
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  return (await r.json()) as ExecSnippet;
}

export async function deleteSnippet(id: string): Promise<void> {
  const r = await apiDelete(`/api/v1/snippets/${encodeURIComponent(id)}`);
  if (!r.ok) throw new Error(r.statusText);
}

export async function execOnHostsStream(
  body: ExecOnHostsBody,
  onRow: (row: HostExecResultRow) => void,
): Promise<void> {
  const r = await fetch('/api/v1/exec?stream=1', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  await readNDJSON<HostExecResultRow>(r, onRow);
}

export async function cueExec(body: CueExecRequest): Promise<{ plan?: string; results?: HostExecResultRow[] }> {
  const r = await apiPost('/api/v1/cue-exec', body);
  const j = (await r.json().catch(() => ({}))) as {
    plan?: string;
    results?: HostExecResultRow[];
    error?: string;
  };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return { plan: j.plan, results: j.results };
}

export async function cueExecStream(
  body: CueExecRequest,
  onRow: (row: HostExecResultRow) => void,
  signal?: AbortSignal,
): Promise<{ recording_id?: string }> {
  const r = await fetch('/api/v1/cue-exec?stream=1', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  const recordingId = r.headers.get('X-Honey-Recording-Id')?.trim() || undefined;
  await readNDJSON<HostExecResultRow>(r, onRow);
  return { recording_id: recordingId };
}