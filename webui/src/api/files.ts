import { apiHeaders, apiPost, readNDJSON } from './core';
import { HostExecResultRow } from './types/exec';
import { FileBrowserEntry, AgentTransferCloud, AgentTransferBackendRef, AgentTransferEvent, FormDataUploadProgressEvent, UploadStreamServerEvent } from './types/files';



export async function listLocalFiles(path: string): Promise<{ root: string; path: string; entries: FileBrowserEntry[] }> {
  const r = await apiPost('/api/v1/files/local/list', { path });
  const j = (await r.json().catch(() => ({}))) as {
    root?: string;
    path?: string;
    entries?: FileBrowserEntry[];
    error?: string;
  };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return { root: j.root || '', path: j.path || '', entries: j.entries || [] };
}

export async function listRemoteFiles(body: {
  ssh_user: string;
  record: unknown;
  path: string;
}): Promise<{ path: string; entries: FileBrowserEntry[] }> {
  const r = await apiPost('/api/v1/files/remote/list', body);
  const j = (await r.json().catch(() => ({}))) as { path?: string; entries?: FileBrowserEntry[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return { path: j.path || '', entries: j.entries || [] };
}

export async function copyFiles(body: {
  direction: 'local_to_remote' | 'remote_to_local';
  ssh_user: string;
  record: unknown;
  local_path: string;
  remote_path: string;
}): Promise<{ status: string; local: string; remote: string }> {
  const r = await apiPost('/api/v1/files/copy', body);
  const j = (await r.json().catch(() => ({}))) as { status?: string; local?: string; remote?: string; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return { status: j.status || 'ok', local: j.local || '', remote: j.remote || '' };
}

export async function startAgentTransfer(body: {
  ssh_user?: string;
  source_record: unknown;
  source_path: string;
  dest_record: unknown;
  dest_path: string;
  cloud: AgentTransferCloud;
  cloud_backend_ref?: AgentTransferBackendRef;
  keep_object?: boolean;
  max_retries?: number;
}): Promise<AgentTransferEvent[]> {
  const r = await apiPost('/api/v1/files/agent-transfer', body);
  const j = (await r.json().catch(() => ({}))) as { events?: AgentTransferEvent[]; error?: string };
  if (!r.ok) {
    throw new Error(j.error || r.statusText);
  }
  return j.events || [];
}

export async function startAgentTransferStream(
  body: {
    ssh_user?: string;
    source_record: unknown;
    source_path: string;
    dest_record: unknown;
    dest_path: string;
    cloud: AgentTransferCloud;
    cloud_backend_ref?: AgentTransferBackendRef;
    keep_object?: boolean;
    max_retries?: number;
  },
  onEvent: (event: AgentTransferEvent) => void,
  signal?: AbortSignal,
): Promise<void> {
  const r = await fetch('/api/v1/files/agent-transfer?stream=1', {
    method: 'POST',
    headers: { ...apiHeaders(), 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
    signal,
  });
  if (!r.ok) {
    const j = (await r.json().catch(() => ({}))) as { error?: string };
    throw new Error(j.error || r.statusText);
  }
  await readNDJSON<AgentTransferEvent>(r, onEvent);
}

/**
 * POST multipart to Honey with ?stream=1: XHR reports bytes to the server; response body is NDJSON
 * with SFTP progress from the Honey process. Resolves the same result list as the non-streaming upload.
 */
export function uploadFormDataWithSFTPStream(
  url: string,
  formData: FormData,
  opts: {
    onHoneyProgress?: (ev: FormDataUploadProgressEvent) => void;
    onServerEvent?: (ev: UploadStreamServerEvent) => void;
  },
): Promise<HostExecResultRow[]> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', url);
    const h = apiHeaders() as Record<string, string>;
    for (const [k, v] of Object.entries(h)) {
      xhr.setRequestHeader(k, v);
    }

    let parsePos = 0;
    let streamErr: Error | null = null;
    let doneResults: HostExecResultRow[] | null = null;

    const drain = () => {
      const text = xhr.responseText;
      while (parsePos < text.length) {
        const nl = text.indexOf('\n', parsePos);
        if (nl < 0) {
          break;
        }
        const line = text.slice(parsePos, nl).trim();
        parsePos = nl + 1;
        if (!line) {
          continue;
        }
        let row: unknown;
        try {
          row = JSON.parse(line) as unknown;
        } catch {
          continue;
        }
        if (!row || typeof row !== 'object' || !('phase' in row)) {
          continue;
        }
        const ev = row as UploadStreamServerEvent;
        opts.onServerEvent?.(ev);
        const phase = String((row as { phase: string }).phase);
        if (phase === 'error') {
          const msg = (row as { message?: string }).message?.trim() || 'upload failed';
          streamErr = new Error(msg);
        }
        if (phase === 'done') {
          doneResults = (row as { results?: HostExecResultRow[] }).results || [];
        }
      }
    };

    xhr.upload.onprogress = (ev) => {
      opts.onHoneyProgress?.({
        kind: 'uploading',
        loaded: ev.loaded,
        total: ev.lengthComputable && ev.total > 0 ? ev.total : null,
      });
    };
    xhr.upload.onloadend = () => {
      opts.onHoneyProgress?.({ kind: 'awaiting_response' });
    };
    xhr.onreadystatechange = () => {
      if (xhr.readyState >= 3) {
        drain();
      }
    };
    xhr.onprogress = () => {
      drain();
    };
    xhr.onload = () => {
      drain();
      if (xhr.status < 200 || xhr.status >= 300) {
        let msg = xhr.statusText || `HTTP ${xhr.status}`;
        try {
          const j = JSON.parse(xhr.responseText) as { error?: string };
          if (j.error) {
            msg = j.error;
          }
        } catch {
          /* ignore */
        }
        reject(new Error(msg));
        return;
      }
      if (streamErr) {
        reject(streamErr);
        return;
      }
      if (doneResults) {
        resolve(doneResults);
        return;
      }
      reject(new Error('upload stream ended without result'));
    };
    xhr.onerror = () => reject(new Error('network error'));
    xhr.send(formData);
  });
}

/** POST multipart FormData with upload progress (bytes to this origin only). Resolves parsed JSON body. */
export function uploadFormDataWithProgress(
  url: string,
  formData: FormData,
  onProgress?: (ev: FormDataUploadProgressEvent) => void,
): Promise<unknown> {
  return new Promise((resolve, reject) => {
    const xhr = new XMLHttpRequest();
    xhr.open('POST', url);
    const h = apiHeaders() as Record<string, string>;
    for (const [k, v] of Object.entries(h)) {
      xhr.setRequestHeader(k, v);
    }
    xhr.upload.onprogress = (ev) => {
      onProgress?.({
        kind: 'uploading',
        loaded: ev.loaded,
        total: ev.lengthComputable && ev.total > 0 ? ev.total : null,
      });
    };
    xhr.upload.onloadend = () => {
      onProgress?.({ kind: 'awaiting_response' });
    };
    xhr.onload = () => {
      let body: unknown;
      try {
        body = JSON.parse(xhr.responseText) as unknown;
      } catch {
        body = xhr.responseText;
      }
      if (xhr.status >= 200 && xhr.status < 300) {
        resolve(body);
      } else {
        let msg = xhr.statusText || `HTTP ${xhr.status}`;
        if (typeof body === 'object' && body !== null && 'error' in body) {
          msg = String((body as { error: string }).error);
        } else if (typeof body === 'string' && body.trim()) {
          msg = body.trim().slice(0, 800);
        }
        reject(new Error(msg));
      }
    };
    xhr.onerror = () => reject(new Error('network error'));
    xhr.send(formData);
  });
}