import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert, Button, Card, Checkbox, Input, Modal, Popover, Progress, Segmented, Select, Space, Table, Tag, Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { apiPost } from '../api/core';
import { deleteSnippet, execOnHostsStream, listSnippets, saveSnippet } from '../api/exec';
import { uploadFormDataWithSFTPStream } from '../api/files';
import type { ExecOnHostsBody, ExecSnippet, HostExecResultRow } from '../api/types/exec';
import type { FormDataUploadProgressEvent, UploadStreamServerEvent } from '../api/types/files';
import type { EditorLanguage } from '../CodeEditor';
import { HostPicker, recordKey } from '../HostPicker';
import { useHostSelection } from '../contexts/HostSelectionContext';
import { useAppContext } from '../contexts/AppContext';
import { useTunnel } from '../contexts/TunnelContext';
import { useReplay } from '../contexts/ReplayContext';
import { useTerminal } from '../contexts/TerminalContext';
import type { BackendRow } from '../contexts/AppContext';
import { ShareAccessModal } from './ShareAccessModal';
import { InterceptModal } from './InterceptModal';
import {
  fetchInterceptEnabled,
  fetchInterceptSessions,
  stopInterceptSession,
  type InterceptOptions,
  type InterceptSession,
} from '../api/intercept';

const CodeEditor = lazy(() => import('../CodeEditor'));
import type { HostRecord } from '../HostPicker';
import type { TerminalSessionConfig, PveConsoleMode, TrueNASConsoleMode } from '../TerminalModal';

type UploadXferState = {
  honeyLoaded: number;
  honeyTotal: number | null;
  awaitingResponse: boolean;
  sftpSent: number;
  sftpTotal: number;
  sftpActive: boolean;
};

// ─── helpers ─────────────────────────────────────────────────────────────────

function cloneHostRecord(rec: HostRecord): HostRecord {
  return {
    ...rec,
    extra_ips: rec.extra_ips?.length ? [...rec.extra_ips] : undefined,
    meta: rec.meta ? { ...rec.meta } : undefined,
  };
}

function canProxmoxQemuVnc(rec: HostRecord): boolean {
  const k = (rec.meta?.kind || '').toLowerCase();
  const m = (rec.meta?.exec_mode || '').toLowerCase();
  return rec.provider === 'proxmox' && k === 'qemu' && (m === 'pve' || m === 'hybrid');
}

function canTrueNASAPIShell(rec: HostRecord): boolean {
  if (rec.provider !== 'truenas') return false;
  const k = (rec.meta?.kind || '').toLowerCase();
  return k === 'appliance' || k === 'vm' || k === 'virt_instance';
}

function canPortForwardTunnel(rec: HostRecord): boolean {
  if ((rec.primary_ip || '').trim()) return true;
  if (rec.provider === 'k8s') return true;
  if (rec.provider === 'truenas') return canTrueNASAPIShell(rec);
  return false;
}

function canIntercept(rec: HostRecord): boolean {
  return rec.provider === 'k8s' && (rec.meta?.kind || '').toLowerCase() === 'pod';
}

function truenasAPIShellLabel(rec: HostRecord): string {
  const k = (rec.meta?.kind || '').toLowerCase();
  switch (k) {
    case 'vm':
      return 'VM console';
    case 'virt_instance':
      return 'Container shell';
    default:
      return 'API shell';
  }
}

function recordIndex(records: HostRecord[], rec: HostRecord): number {
  const k = recordKey(rec);
  return records.findIndex((r) => recordKey(r) === k);
}

function formatUploadBytes(n: number): string {
  if (!Number.isFinite(n) || n < 0) return '0 B';
  if (n < 1024) return `${Math.round(n)} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(n < 10 * 1024 ? 1 : 0)} KB`;
  return `${(n / (1024 * 1024)).toFixed(n < 10 * 1024 * 1024 ? 1 : 0)} MB`;
}

function backendRef(b: BackendRow): string {
  return `${b.kind}:${b.name}`.toLowerCase();
}

// ─── sub-components ──────────────────────────────────────────────────────────

function UploadProgressBar({ xfer }: { xfer: UploadXferState }) {
  const honeyPct =
    xfer.honeyTotal != null && xfer.honeyTotal > 0
      ? Math.min(100, Math.round((100 * xfer.honeyLoaded) / xfer.honeyTotal))
      : null;
  const honeyDone =
    xfer.sftpActive ||
    (xfer.honeyTotal != null && xfer.honeyTotal > 0 && xfer.honeyLoaded >= xfer.honeyTotal) ||
    (honeyPct != null && honeyPct >= 100);

  const sftpPct =
    xfer.sftpActive && xfer.sftpTotal > 0
      ? Math.min(100, Math.round((100 * xfer.sftpSent) / xfer.sftpTotal))
      : null;
  const sftpWaiting = xfer.awaitingResponse && !xfer.sftpActive;
  const sftpIndeterminate = xfer.sftpActive && xfer.sftpTotal <= 0;

  const honeyStatus: 'success' | 'active' | 'normal' = honeyDone
    ? 'success'
    : honeyPct == null
      ? 'active'
      : 'normal';

  const sftpStatus: 'success' | 'active' | 'normal' =
    sftpWaiting || sftpIndeterminate
      ? 'active'
      : sftpPct != null && sftpPct >= 100
        ? 'success'
        : 'normal';

  return (
    <Space direction="vertical" size={4} style={{ width: '100%' }}>
      <Space direction="vertical" size={2} style={{ width: '100%' }}>
        <Typography.Text type="secondary">1. Send file to Honey</Typography.Text>
        <Progress
          percent={honeyPct ?? (honeyDone ? 100 : 0)}
          status={honeyStatus}
          size="small"
          format={honeyDone
            ? () => xfer.honeyTotal != null && xfer.honeyTotal > 0
              ? `${formatUploadBytes(xfer.honeyLoaded)} / ${formatUploadBytes(xfer.honeyTotal)}`
              : xfer.honeyLoaded > 0 ? `${formatUploadBytes(xfer.honeyLoaded)} sent` : 'Done'
            : undefined}
        />
      </Space>
      <Space direction="vertical" size={2} style={{ width: '100%' }}>
        <Typography.Text type="secondary">2. Write to host (SFTP)</Typography.Text>
        {sftpWaiting ? (
          <Typography.Text type="secondary" style={{ fontSize: '0.8rem' }}>
            Waiting for Honey to start the SSH transfer…
          </Typography.Text>
        ) : xfer.sftpActive ? (
          <Progress
            percent={sftpPct ?? 0}
            status={sftpStatus}
            size="small"
            format={sftpPct != null && !sftpIndeterminate
              ? () => xfer.sftpTotal > 0
                ? `${formatUploadBytes(xfer.sftpSent)} / ${formatUploadBytes(xfer.sftpTotal)}`
                : xfer.sftpSent > 0 ? formatUploadBytes(xfer.sftpSent) : ''
              : undefined}
          />
        ) : (
          <Typography.Text type="secondary" style={{ fontSize: '0.8rem' }}>
            Starts after the file reaches Honey.
          </Typography.Text>
        )}
      </Space>
    </Space>
  );
}

// ─── main component ───────────────────────────────────────────────────────────

export function SearchTab() {
  const {
    records, setRecords, selectedKeys, setSelectedKeys,
    selectedProviders, setSelectedProviders, selectedBackends, setSelectedBackends,
    providerIds, sshUser, setSshUser
  } = useHostSelection();
  const { meta, backends } = useAppContext();
  const { handleOpenTunnel } = useTunnel();
  const { openReplayModal, openReplayAllRecordings } = useReplay();
  const { handleOpenTerminal, terminals = [], closeTerminal } = useTerminal();

  const [name, setName] = useState(() => {
    return new URLSearchParams(window.location.search).get('name') || '';
  });
  const [resultFilter, setResultFilter] = useState(() => {
    return new URLSearchParams(window.location.search).get('resultFilter') || '';
  });
  const [searchErr, setSearchErr] = useState<string | null>(null);
  const [searching, setSearching] = useState(false);

  const [execCommand, setExecCommand] = useState(() => {
    return new URLSearchParams(window.location.search).get('execCommand') || '';
  });
  const [execBusy, setExecBusy] = useState(false);
  const [execErr, setExecErr] = useState<string | null>(null);
  const [execResults, setExecResults] = useState<HostExecResultRow[] | null>(null);

  // Script-mode exec options (Rundeck-style: upload script, chmod +x, run, cleanup).
  const [execMode, setExecMode] = useState<'command' | 'script'>('command');
  const [scriptInterpreter, setScriptInterpreter] = useState(''); // '' = use shebang
  const [interpreterArgsQuoted, setInterpreterArgsQuoted] = useState(false);
  const [scriptInterpreterCustom, setScriptInterpreterCustom] = useState('');
  const [removeTmpFile, setRemoveTmpFile] = useState(true);
  const [execRunAs, setExecRunAs] = useState('');
  const [execTimeout, setExecTimeout] = useState('');
  // execTotal = hosts submitted this run; execResults grows as NDJSON results
  // stream in, so execResults.length / execTotal is the live "done / total".
  const [execTotal, setExecTotal] = useState(0);
  const execAbortRef = useRef<AbortController | null>(null);

  // Saved exec snippets (server-side, pluggable storage).
  const [snippets, setSnippets] = useState<ExecSnippet[]>([]);
  const [selectedSnippetId, setSelectedSnippetId] = useState<string | undefined>(undefined);
  const [saveSnippetOpen, setSaveSnippetOpen] = useState(false);
  const [saveSnippetName, setSaveSnippetName] = useState('');
  const [snippetBusy, setSnippetBusy] = useState(false);

  const [hostDetailRecord, setHostDetailRecord] = useState<HostRecord | null>(null);
  const [visibleRecords, setVisibleRecords] = useState<HostRecord[]>([]);
  const [shareTarget, setShareTarget] = useState<HostRecord | null>(null);

  const [interceptTarget, setInterceptTarget] = useState<HostRecord | null>(null);
  const [interceptEnabled, setInterceptEnabled] = useState(true);
  const [interceptSessions, setInterceptSessions] = useState<InterceptSession[]>([]);

  const [recordWebSession, setRecordWebSession] = useState(() => {
    return new URLSearchParams(window.location.search).get('recordWebSession') === 'true';
  });

  const [uploadModalOpen, setUploadModalOpen] = useState(false);
  const [uploadTargetIdx, setUploadTargetIdx] = useState(0);
  const [uploadRemote, setUploadRemote] = useState('/tmp/');
  const [uploadXfer, setUploadXfer] = useState<UploadXferState | null>(null);
  const [uploadStatus, setUploadStatus] = useState('');
  const [uploadStatusIsError, setUploadStatusIsError] = useState(false);
  const fileInputRef = useRef<HTMLInputElement>(null);

  // ── derived ──────────────────────────────────────────────────────────────

  const selectedRecords = useMemo(
    () => records.filter((r) => selectedKeys[recordKey(r)]),
    [records, selectedKeys],
  );

  const providerOptions = useMemo(
    () => providerIds.map((id) => ({ value: id, label: id })),
    [providerIds],
  );

  const backendOptions = useMemo(
    () => backends
      .filter((b) => b.name.trim() !== '')
      .map((b) => ({ value: backendRef(b), label: `${b.kind}: ${b.name}` })),
    [backends],
  );

  // Local buffer for provider/backend selects — value only syncs to parent on
  // dropdown close so the open dropdown never re-renders due to App state updates.
  const [localProviders, setLocalProviders] = useState<string[]>(selectedProviders);
  const [localBackends, setLocalBackends] = useState<string[]>(selectedBackends);

  useEffect(() => { setLocalProviders(selectedProviders); }, [selectedProviders]);
  useEffect(() => { setLocalBackends(selectedBackends); }, [selectedBackends]);

  // ── effects ───────────────────────────────────────────────────────────────

  useEffect(() => {
    setHostDetailRecord((prev) => {
      if (!prev) return null;
      const match = records.find((r) => recordKey(r) === recordKey(prev));
      if (!match) return null;
      return cloneHostRecord(match);
    });
  }, [records]);

  useEffect(() => {
    if (meta !== null && !meta.session_recording_available) {
      setRecordWebSession(false);
    }
  }, [meta]);

  useEffect(() => {
    setUploadTargetIdx((i) => {
      if (records.length === 0) return 0;
      return Math.min(Math.max(0, i), records.length - 1);
    });
  }, [records]);

  // Whether server-side intercept is enabled at all — defensive: an absent/erroring
  // `/api/v1/intercept/config` (a later backend task) resolves to `true`, so the button
  // is not hidden just because that optional endpoint hasn't shipped yet.
  useEffect(() => {
    let cancelled = false;
    void fetchInterceptEnabled().then((enabled) => {
      if (!cancelled) setInterceptEnabled(enabled);
    });
    return () => {
      cancelled = true;
    };
  }, []);

  // Active-intercepts affordance: `/api/v1/intercept/sessions` is added by a later
  // backend task, so this defensively resolves to [] (hiding the panel) until then.
  const refreshInterceptSessions = useCallback(() => {
    void fetchInterceptSessions().then(setInterceptSessions);
  }, []);

  useEffect(() => {
    refreshInterceptSessions();
  }, [refreshInterceptSessions]);

  const stopIntercept = async (s: InterceptSession) => {
    // Close the matching terminal tab immediately (by pod key), so Stop feels
    // instant instead of leaving a dead tab until the reconcile poll catches up.
    if (s.record_key) {
      for (const t of terminals) {
        if (t.intercept && ((t.record as { _key?: string })._key || recordKey(t.record)) === s.record_key) {
          closeTerminal(t.id);
        }
      }
    }
    try {
      await stopInterceptSession(s.id);
    } catch {
      // best-effort — refresh below will reflect whatever the server actually did
    } finally {
      refreshInterceptSessions();
    }
  };

  // ── actions ───────────────────────────────────────────────────────────────

  const runSearch = async () => {
    setSearching(true);
    setSearchErr(null);
    try {
      const r = await apiPost('/api/v1/search', {
        name,
        providers: selectedProviders.join(','),
        backends: selectedBackends.join(','),
      });
      const j = await r.json();
      if (!r.ok) {
        setSearchErr((j as { error?: string }).error || r.statusText);
        setRecords([]);
        return;
      }
      setExecResults(null);
      setExecErr(null);
      setRecords((j as { records: HostRecord[] }).records || []);
    } finally {
      setSearching(false);
    }
  };

  // Auto-run search on mount if URL restored any filters
  useEffect(() => {
    if (name.trim() || selectedProviders.length > 0 || selectedBackends.length > 0) {
      void runSearch();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const toggleRowSelected = (rec: HostRecord) => {
    const k = recordKey(rec);
    setSelectedKeys(
      (() => {
        const next = { ...selectedKeys };
        if (next[k]) {
          delete next[k];
        } else {
          next[k] = true;
        }
        return next;
      })(),
    );
  };

  const selectVisibleHosts = () => {
    const next = { ...selectedKeys };
    for (const r of visibleRecords) {
      next[recordKey(r)] = true;
    }
    setSelectedKeys(next);
  };

  const clearHostSelection = () => setSelectedKeys({});

  const clearExecOutput = () => {
    setExecErr(null);
    setExecResults(null);
  };

  const effectiveInterpreter = scriptInterpreter === '__custom__' ? scriptInterpreterCustom.trim() : scriptInterpreter;

  // Editor language follows the interpreter; '' (shebang) / custom sniff the first line.
  const editorLanguage = useMemo<EditorLanguage>(() => {
    if (effectiveInterpreter.includes('python')) return 'python';
    if (/\b(bash|sh|zsh|ksh)\b/.test(effectiveInterpreter)) return 'bash';
    const firstLine = execCommand.split('\n', 1)[0] || '';
    if (firstLine.startsWith('#!')) {
      if (firstLine.includes('python')) return 'python';
      if (/\b(bash|sh|zsh|ksh)\b/.test(firstLine)) return 'bash';
    }
    return 'bash'; // sensible default for script mode
  }, [effectiveInterpreter, execCommand]);

  const INTERPRETER_PRESETS = ['', 'bash', 'bash -lc', 'sh', 'python3'];

  const refreshSnippets = useCallback(async () => {
    try {
      setSnippets(await listSnippets());
    } catch {
      // snippets are optional; ignore load errors
    }
  }, []);

  useEffect(() => {
    void refreshSnippets();
  }, [refreshSnippets]);

  const applySnippet = (id: string) => {
    const snip = snippets.find((s) => s.id === id);
    if (!snip) return;
    setSelectedSnippetId(id);
    setExecMode(snip.mode);
    setExecCommand(snip.command);
    setExecRunAs(snip.run_as || '');
    const interp = snip.script_interpreter || '';
    if (INTERPRETER_PRESETS.includes(interp)) {
      setScriptInterpreter(interp);
      setScriptInterpreterCustom('');
    } else {
      setScriptInterpreter('__custom__');
      setScriptInterpreterCustom(interp);
    }
    setInterpreterArgsQuoted(!!snip.interpreter_args_quoted);
  };

  const doSaveSnippet = async () => {
    const name = saveSnippetName.trim();
    if (!name || !execCommand.trim()) return;
    setSnippetBusy(true);
    try {
      const saved = await saveSnippet({
        name,
        mode: execMode,
        command: execCommand,
        script_interpreter: execMode === 'script' ? effectiveInterpreter : undefined,
        interpreter_args_quoted: execMode === 'script' ? interpreterArgsQuoted : undefined,
        run_as: execRunAs.trim() || undefined,
      });
      setSaveSnippetOpen(false);
      setSaveSnippetName('');
      await refreshSnippets();
      setSelectedSnippetId(saved.id);
    } catch (e) {
      setExecErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSnippetBusy(false);
    }
  };

  const doDeleteSnippet = async () => {
    if (!selectedSnippetId) return;
    setSnippetBusy(true);
    try {
      await deleteSnippet(selectedSnippetId);
      setSelectedSnippetId(undefined);
      await refreshSnippets();
    } catch (e) {
      setExecErr(e instanceof Error ? e.message : String(e));
    } finally {
      setSnippetBusy(false);
    }
  };

  const runParallelExec = async () => {
    const isScript = execMode === 'script';
    const body = execCommand.trim();
    if (!body || selectedRecords.length === 0) {
      setExecErr(isScript ? 'Select at least one host and enter a script.' : 'Select at least one host and enter a command.');
      return;
    }
    setExecBusy(true);
    setExecErr(null);
    setExecResults([]);
    setExecTotal(selectedRecords.length);
    try {
      const req: ExecOnHostsBody = {
        ssh_user: sshUser.trim(),
        command: execCommand,
        records: selectedRecords,
        record_session: !!(recordWebSession && meta?.session_recording_available),
      };
      if (execRunAs.trim()) req.run_as = execRunAs.trim();
      if (execTimeout.trim()) req.timeout = execTimeout.trim();
      if (isScript) {
        req.exec_mode = 'script';
        if (effectiveInterpreter) req.script_interpreter = effectiveInterpreter;
        if (interpreterArgsQuoted) req.interpreter_args_quoted = true;
        if (!removeTmpFile) req.remove_tmp_file = false;
      } else {
        req.command = body;
      }
      const ctrl = new AbortController();
      execAbortRef.current = ctrl;
      await execOnHostsStream(req, (row) => setExecResults((prev) => [...(prev || []), row]), ctrl.signal);
    } catch (e) {
      // AbortError = user pressed Stop; not a real error.
      if (!(e instanceof DOMException && e.name === 'AbortError')) {
        setExecErr(e instanceof Error ? e.message : String(e));
      }
    } finally {
      execAbortRef.current = null;
      setExecBusy(false);
    }
  };

  const stopParallelExec = () => execAbortRef.current?.abort();

  const closeUploadModal = () => {
    setUploadModalOpen(false);
    setUploadXfer(null);
    setUploadStatus('');
    setUploadStatusIsError(false);
    if (fileInputRef.current) {
      fileInputRef.current.value = '';
    }
  };

  const onUploadXferProgress = useCallback((ev: FormDataUploadProgressEvent) => {
    if (ev.kind === 'uploading') {
      setUploadXfer((prev) => ({
        honeyLoaded: ev.loaded,
        honeyTotal: ev.total,
        awaitingResponse: false,
        sftpSent: prev?.sftpSent ?? 0,
        sftpTotal: prev?.sftpTotal ?? 0,
        sftpActive: prev?.sftpActive ?? false,
      }));
    } else {
      setUploadXfer((prev) => ({
        honeyLoaded: prev?.honeyLoaded ?? 0,
        honeyTotal: prev?.honeyTotal ?? null,
        awaitingResponse: true,
        sftpSent: prev?.sftpSent ?? 0,
        sftpTotal: prev?.sftpTotal ?? 0,
        sftpActive: prev?.sftpActive ?? false,
      }));
    }
  }, []);

  const runUpload = async (rec: HostRecord, file: File, remotePath: string, user: string) => {
    setUploadXfer({
      honeyLoaded: 0,
      honeyTotal: null,
      awaitingResponse: false,
      sftpSent: 0,
      sftpTotal: 0,
      sftpActive: false,
    });
    setUploadStatusIsError(false);
    setUploadStatus('');
    const fd = new FormData();
    fd.append('file', file);
    fd.append(
      'meta',
      JSON.stringify({
        ssh_user: user,
        remote_path: remotePath,
        record: rec,
      }),
    );
    try {
      const body = await uploadFormDataWithSFTPStream('/api/v1/upload?stream=1', fd, {
        onHoneyProgress: onUploadXferProgress,
        onServerEvent: (ev: UploadStreamServerEvent) => {
          if (ev.phase === 'sftp_start') {
            setUploadXfer((p) =>
              p
                ? {
                    ...p,
                    awaitingResponse: false,
                    sftpTotal: ev.total_bytes,
                    sftpSent: 0,
                    sftpActive: true,
                  }
                : p,
            );
          } else if (ev.phase === 'sftp') {
            setUploadXfer((p) =>
              p
                ? {
                    ...p,
                    awaitingResponse: false,
                    sftpSent: ev.sent_bytes,
                    sftpTotal: ev.total_bytes,
                    sftpActive: true,
                  }
                : p,
            );
          }
        },
      });
      if (Array.isArray(body)) {
        const bad = body.filter(
          (r: { Success?: boolean }) => (r as { Success?: boolean }).Success === false,
        ) as { Name?: string; ErrMsg?: string }[];
        if (bad.length) {
          const msg = bad
            .map((r) => `${r.Name ?? '?'}: ${(r.ErrMsg || 'failed').trim() || 'failed'}`)
            .join('; ');
          throw new Error(msg);
        }
      }
      setUploadStatus('Upload finished.');
      setUploadXfer(null);
      if (fileInputRef.current) {
        fileInputRef.current.value = '';
      }
    } catch (e) {
      setUploadStatusIsError(true);
      setUploadStatus(e instanceof Error ? e.message : String(e));
      setUploadXfer(null);
    }
  };

  const onUploadSubmit = () => {
    const inp = fileInputRef.current;
    const file = inp?.files?.[0];
    if (!file || records.length === 0) {
      setUploadStatusIsError(true);
      setUploadStatus('Choose a file and ensure search results include a target host.');
      return;
    }
    const rec = records[uploadTargetIdx];
    if (!rec) return;
    const remote = uploadRemote.trim() || `/tmp/${file.name}`;
    void runUpload(rec, file, remote, sshUser.trim());
  };

  const openUploadModal = (rec?: HostRecord) => {
    if (rec && records.length > 0) {
      const idx = recordIndex(records, rec);
      setUploadTargetIdx(idx >= 0 ? idx : 0);
    }
    setUploadModalOpen(true);
    setUploadXfer(null);
    setUploadStatus('');
    setUploadStatusIsError(false);
  };

  const onDropUpload = (rec: HostRecord, files: FileList | null) => {
    if (!files?.length) return;
    const f = files[0];
    const idx = recordIndex(records, rec);
    setUploadTargetIdx(idx >= 0 ? idx : 0);
    setUploadRemote(`/tmp/${f.name}`);
    setUploadModalOpen(true);
    setUploadStatusIsError(false);
    setUploadStatus('');
    void runUpload(rec, f, `/tmp/${f.name}`, sshUser.trim());
  };

  const openTerminalSession = (rec: HostRecord, pve: PveConsoleMode, truenasConsole?: TrueNASConsoleMode) => {
    const id = crypto.randomUUID();
    sessionStorage.setItem(`honey_term_${id}`, JSON.stringify(rec));
    const cfg: TerminalSessionConfig = { id, record: rec, pve, truenasConsole };
    handleOpenTerminal(cfg);
  };

  const openInterceptSession = (opts: InterceptOptions) => {
    const rec = interceptTarget;
    if (!rec) return;
    const id = crypto.randomUUID();
    sessionStorage.setItem(`honey_term_${id}`, JSON.stringify(rec));
    const cfg: TerminalSessionConfig = { id, record: rec, pve: 'serial', intercept: opts };
    handleOpenTerminal(cfg);
    setInterceptTarget(null);
    // The new session shows up server-side once the injected container is running —
    // refresh shortly after so the active-intercepts badge picks it up.
    window.setTimeout(refreshInterceptSessions, 1500);
  };

  // ── exec table columns ────────────────────────────────────────────────────

  const execColumns: ColumnsType<HostExecResultRow> = [
    { title: 'Host', dataIndex: 'Name', key: 'Name', width: 160 },
    {
      title: 'OK',
      dataIndex: 'Success',
      key: 'Success',
      width: 50,
      render: (v: boolean) => v ? <Tag color="green">yes</Tag> : <Tag color="red">no</Tag>,
    },
    { title: 'Exit', dataIndex: 'ExitCode', key: 'ExitCode', width: 50 },
    {
      title: 'Output / error',
      key: 'output',
      render: (_: unknown, row: HostExecResultRow) => (
        <pre style={{ margin: 0, fontSize: '0.78rem', whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxWidth: '100%' }}>
          {row.ErrMsg ? <Typography.Text type="danger">{row.ErrMsg}</Typography.Text> : null}
          {row.ErrMsg && row.Output ? '\n' : null}
          {row.Output}
        </pre>
      ),
    },
  ];

  // ── render ────────────────────────────────────────────────────────────────

  return (
    <section style={{ width: '100%', minWidth: 0, overflow: 'hidden' }}>
      {/* Row 1: Providers / backends / browse recordings */}
      <Space wrap style={{ marginBottom: 8, width: '100%' }}>
        <Select
          mode="multiple"
          placeholder="All providers"
          value={localProviders}
          onChange={setLocalProviders}
          onDropdownVisibleChange={(open) => {
            if (!open) setSelectedProviders(localProviders);
          }}
          options={providerOptions}
          style={{ width: 180 }}
          maxTagCount={0}
          popupMatchSelectWidth={false}
          allowClear
          virtual={false}
        />
        <Select
          mode="multiple"
          placeholder="All backends"
          value={localBackends}
          onChange={setLocalBackends}
          onDropdownVisibleChange={(open) => {
            if (!open) setSelectedBackends(localBackends);
          }}
          options={backendOptions}
          style={{ width: 200 }}
          maxTagCount={0}
          popupMatchSelectWidth={false}
          allowClear
          virtual={false}
        />
        {meta?.session_recording_available && (
          <Button onClick={() => void openReplayAllRecordings()}>Browse recordings</Button>
        )}
        {interceptSessions.length > 0 && (
          <Popover
            trigger="click"
            placement="bottomLeft"
            title="Active intercepts"
            content={
              <Space direction="vertical" size={6} style={{ minWidth: 240 }}>
                {interceptSessions.map((s) => (
                  <Space key={s.id} style={{ width: '100%', justifyContent: 'space-between' }}>
                    <Typography.Text style={{ fontSize: '0.82rem' }} ellipsis={{ tooltip: s.name || s.record_key || s.id }}>
                      {s.name || s.record_key || s.id}
                    </Typography.Text>
                    <Button size="small" danger onClick={() => void stopIntercept(s)}>
                      Stop
                    </Button>
                  </Space>
                ))}
              </Space>
            }
          >
            <Button>Intercepts: {interceptSessions.length}</Button>
          </Popover>
        )}
      </Space>

      {/* Row 2: Name filter / SSH user / Record / Search */}
      <Space wrap style={{ marginBottom: 12, width: '100%' }}>
        <Input.Search
          placeholder="Name contains"
          value={name}
          onChange={(e) => setName(e.target.value)}
          onSearch={() => void runSearch()}
          style={{ width: 220 }}
        />
        <Input
          placeholder="SSH user"
          value={sshUser}
          onChange={(e) => setSshUser(e.target.value)}
          style={{ width: 140 }}
        />
        <Checkbox
          checked={recordWebSession}
          disabled={!meta?.session_recording_available}
          onChange={(e) => setRecordWebSession(e.target.checked)}
          title={
            meta?.session_recording_available
              ? 'When checked, record new SSH/K8s terminal sessions, parallel command runs, and CUE recipe runs (dry-run and execute) to the server record-dir.'
              : 'Recording unavailable: start honey web with --record-dir to enable.'
          }
        >
          Record sessions
        </Checkbox>
        <Button type="primary" loading={searching} onClick={() => void runSearch()}>
          Search
        </Button>
      </Space>

      {/* Search error */}
      {searchErr ? <Alert type="error" message={searchErr} style={{ marginBottom: 12 }} /> : null}

      {/* Exec panel */}
      <Card size="small" style={{ marginBottom: 12 }}>
        <Space wrap style={{ marginBottom: 8 }}>
          <Typography.Text>Selected: <strong>{selectedRecords.length}</strong></Typography.Text>
          <Button size="small" disabled={visibleRecords.length === 0} onClick={selectVisibleHosts}>Select visible</Button>
          <Button size="small" onClick={clearHostSelection}>Clear</Button>
          <Segmented
            size="small"
            value={execMode}
            onChange={(v) => setExecMode(v as 'command' | 'script')}
            options={[{ label: 'Command', value: 'command' }, { label: 'Script', value: 'script' }]}
          />
          <Select
            size="small"
            placeholder="Snippets"
            allowClear
            value={selectedSnippetId}
            style={{ width: 200 }}
            onChange={(id) => (id ? applySnippet(id) : setSelectedSnippetId(undefined))}
            options={snippets.map((s) => ({ label: `${s.name} (${s.mode})`, value: s.id }))}
          />
          <Button size="small" onClick={() => { setSaveSnippetName(''); setSaveSnippetOpen(true); }} disabled={!execCommand.trim()}>
            Save snippet
          </Button>
          <Button
            size="small"
            danger
            disabled={!selectedSnippetId || snippetBusy}
            onClick={() =>
              Modal.confirm({
                title: 'Delete snippet',
                content: `Delete "${snippets.find((s) => s.id === selectedSnippetId)?.name}"?`,
                okText: 'Delete',
                okType: 'danger',
                onOk: () => void doDeleteSnippet(),
              })
            }
          >
            Delete snippet
          </Button>
        </Space>
        {execMode === 'script' ? (
          <div style={{ marginBottom: 8, border: '1px solid #2a3140', borderRadius: 4, overflow: 'hidden' }}>
            <Suspense fallback={<div style={{ padding: 8, fontFamily: 'monospace', fontSize: '0.85rem' }}>Loading editor…</div>}>
              <CodeEditor
                value={execCommand}
                onChange={setExecCommand}
                language={editorLanguage}
                lint
                height="260px"
                placeholder={'#!/usr/bin/env bash\nset -euo pipefail\necho "$@"'}
              />
            </Suspense>
          </div>
        ) : (
          <Input.TextArea
            value={execCommand}
            onChange={(e) => setExecCommand(e.target.value)}
            placeholder="e.g. uname -a"
            rows={3}
            style={{ fontFamily: 'monospace', fontSize: '0.85rem', marginBottom: 8 }}
          />
        )}
        {execMode === 'script' && (
          <Space wrap style={{ marginBottom: 8 }}>
            <Select
              size="small"
              value={scriptInterpreter}
              onChange={(v) => {
                setScriptInterpreter(v);
                setInterpreterArgsQuoted(v === 'bash -lc' || v === 'sh -lc');
              }}
              style={{ width: 180 }}
              options={[
                { label: 'Shebang / none', value: '' },
                { label: 'bash', value: 'bash' },
                { label: 'bash -lc', value: 'bash -lc' },
                { label: 'sh', value: 'sh' },
                { label: 'python3', value: 'python3' },
                { label: 'Custom…', value: '__custom__' },
              ]}
            />
            {scriptInterpreter === '__custom__' && (
              <Input
                size="small"
                placeholder="interpreter or ${scriptfile}"
                value={scriptInterpreterCustom}
                onChange={(e) => setScriptInterpreterCustom(e.target.value)}
                style={{ width: 200, fontFamily: 'monospace' }}
              />
            )}
            <Checkbox checked={!removeTmpFile} onChange={(e) => setRemoveTmpFile(!e.target.checked)}>
              Keep temp file
            </Checkbox>
          </Space>
        )}
        <Space wrap style={{ marginBottom: 4 }}>
          <Input
            size="small"
            prefix="Run as"
            placeholder="sudo user (optional)"
            value={execRunAs}
            onChange={(e) => setExecRunAs(e.target.value)}
            style={{ width: 220 }}
          />
          <Input
            size="small"
            prefix="Timeout"
            placeholder="per-host, e.g. 30s"
            value={execTimeout}
            onChange={(e) => setExecTimeout(e.target.value)}
            style={{ width: 200 }}
          />
          <Button
            type="primary"
            loading={execBusy}
            disabled={selectedRecords.length === 0 || !execCommand.trim()}
            onClick={() => void runParallelExec()}
            style={{ marginLeft: 16 }}
          >
            {execMode === 'script' ? 'Run script on' : 'Run on'} {selectedRecords.length} host(s)
          </Button>
          {execBusy && (
            <Button danger onClick={stopParallelExec}>
              Stop
            </Button>
          )}
          {(execBusy || (execResults?.length ?? 0) > 0) && (() => {
            const done = execResults?.length ?? 0;
            const okN = execResults?.reduce((n, r) => n + (r.Success ? 1 : 0), 0) ?? 0;
            const total = execTotal || done;
            const pct = total ? Math.round((done / total) * 100) : 0;
            return (
              <Space size={6} style={{ marginLeft: 8 }}>
                <Progress
                  percent={pct}
                  showInfo={false}
                  status={execBusy ? 'active' : done < total ? 'exception' : 'success'}
                  style={{ width: 140 }}
                />
                <Typography.Text strong style={{ fontVariantNumeric: 'tabular-nums' }}>
                  {done} / {total}
                </Typography.Text>
                {okN > 0 && <Tag color="green" style={{ marginInlineEnd: 0 }}>{okN} ok</Tag>}
                {done - okN > 0 && <Tag color="red" style={{ marginInlineEnd: 0 }}>{done - okN} fail</Tag>}
              </Space>
            );
          })()}
          <Button onClick={clearExecOutput}>Clear results</Button>
        </Space>
        <Modal maskClosable={false}           title="Save snippet"
          open={saveSnippetOpen}
          onOk={() => void doSaveSnippet()}
          onCancel={() => setSaveSnippetOpen(false)}
          okButtonProps={{ loading: snippetBusy, disabled: !saveSnippetName.trim() }}
          okText="Save"
        >
          <Input
            placeholder="Snippet name"
            value={saveSnippetName}
            onChange={(e) => setSaveSnippetName(e.target.value)}
            onPressEnter={() => void doSaveSnippet()}
            autoFocus
          />
        </Modal>
        {execErr && <Alert type="error" title={execErr} style={{ marginTop: 8 }} />}
        {execResults && execResults.length > 0 && (
          <Table<HostExecResultRow>
            dataSource={execResults}
            rowKey={(row, i) => `${row.Name}-${i ?? 0}`}
            size="small"
            pagination={{ pageSize: 10, showSizeChanger: false }}
            style={{ marginTop: 8 }}
            columns={execColumns}
          />
        )}
      </Card>

      <HostPicker
        records={records}
        selectedKeys={selectedKeys}
        onToggleRow={toggleRowSelected}
        onVisibleRecordsChange={setVisibleRecords}
        filter={resultFilter}
        onFilterChange={setResultFilter}
        isRowHighlighted={(rec) =>
          !!hostDetailRecord && recordKey(hostDetailRecord) === recordKey(rec)
        }
        onRowClick={(rec) =>
          setHostDetailRecord((prev) =>
            prev && recordKey(prev) === recordKey(rec) ? null : cloneHostRecord(rec),
          )
        }
        onRowDrop={(rec, files) => onDropUpload(rec, files)}
        renderRowActions={(rec) => {
          const recKey = recordKey(rec);

          const activeTerms = terminals.filter((t) => recordKey(t.record) === recKey);
          const serialTerms = activeTerms.filter((t) => t.pve === 'serial' && (t.truenasConsole ?? 'ssh') === 'ssh' && !t.intercept);
          const apiTerms = activeTerms.filter((t) => t.truenasConsole === 'api');
          const vncTerms = activeTerms.filter((t) => t.pve === 'vnc');
          const interceptTerms = activeTerms.filter((t) => !!t.intercept);

          const hasSSH = !!(rec.primary_ip || '').trim();
          const showTrueNASAPI = canTrueNASAPIShell(rec);
          const apiLabel = truenasAPIShellLabel(rec);

          return (
            <Space size={4}>
              {hasSSH || rec.provider !== 'truenas' ? (
                <Button
                  size="small"
                  type={serialTerms.length > 0 ? 'primary' : 'default'}
                  disabled={rec.provider === 'truenas' && !hasSSH}
                  title={rec.provider === 'truenas' && !hasSSH ? 'No SSH target IP on this record' : undefined}
                  onClick={() => openTerminalSession(rec, 'serial', 'ssh')}
                >
                  {serialTerms.length > 0 ? 'Terminal (Open)' : 'Terminal'}
                </Button>
              ) : null}
              {showTrueNASAPI ? (
                <Button
                  size="small"
                  type={apiTerms.length > 0 ? 'primary' : 'default'}
                  onClick={() => openTerminalSession(rec, 'serial', 'api')}
                >
                  {apiTerms.length > 0 ? `${apiLabel} (Open)` : apiLabel}
                </Button>
              ) : null}
              {canProxmoxQemuVnc(rec) ? (
                <Button
                  size="small"
                  type={vncTerms.length > 0 ? 'primary' : 'default'}
                  onClick={() => openTerminalSession(rec, 'vnc')}
                >
                  {vncTerms.length > 0 ? 'VNC (Open)' : 'VNC'}
                </Button>
              ) : null}
              {canIntercept(rec) && interceptEnabled ? (
                <Button
                  size="small"
                  type={interceptTerms.length > 0 ? 'primary' : 'default'}
                  onClick={() => setInterceptTarget(rec)}
                >
                  {interceptTerms.length > 0 ? 'Intercept (Open)' : 'Intercept'}
                </Button>
              ) : null}
              <Button size="small" onClick={() => openUploadModal(rec)}>
                Upload
              </Button>
              <Button size="small" onClick={() => setShareTarget(rec)}>
                Share
              </Button>
              <Button
                size="small"
                disabled={!canPortForwardTunnel(rec)}
                title={
                  !canPortForwardTunnel(rec)
                    ? 'Port-forward requires SSH IP or a TrueNAS API shell target'
                    : undefined
                }
                onClick={() => handleOpenTunnel(rec)}
              >
                Tunnel
              </Button>
              {meta?.session_recording_available ? (
                <Button size="small" onClick={() => void openReplayModal(rec)}>
                  Play
                </Button>
              ) : null}
            </Space>
          );
        }}
      />

      {/* Host detail panel */}
      {hostDetailRecord ? (
        <Card
          size="small"
          title={`${hostDetailRecord.provider} / ${hostDetailRecord.name}`}
          extra={<Button size="small" onClick={() => setHostDetailRecord(null)}>Dismiss</Button>}
          style={{ marginTop: 8 }}
        >
          <Space direction="vertical" size={4} style={{ width: '100%', fontSize: '0.85rem' }}>
            <div>
              <Typography.Text type="secondary">Primary IP </Typography.Text>
              <code>{hostDetailRecord.primary_ip || '—'}</code>
            </div>
            {hostDetailRecord.meta?.kind === 'pod' && hostDetailRecord.meta.node ? (
              <div>
                <Typography.Text type="secondary">Node </Typography.Text>
                <code>{hostDetailRecord.meta.node}</code>
                {hostDetailRecord.meta.node_ip ? (
                  <>
                    {' · '}
                    <Typography.Text type="secondary">node IP </Typography.Text>
                    <code>{hostDetailRecord.meta.node_ip}</code>
                    {hostDetailRecord.meta.node_extra_ips ? (
                      <Typography.Text type="secondary" style={{ fontSize: '0.85em' }}>
                        {' '}(also {hostDetailRecord.meta.node_extra_ips})
                      </Typography.Text>
                    ) : null}
                  </>
                ) : null}
              </div>
            ) : null}
            {hostDetailRecord.extra_ips && hostDetailRecord.extra_ips.length > 0 ? (
              <div>
                <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 4 }}>Extras</Typography.Text>
                <Typography.Text type="secondary" style={{ display: 'block', fontSize: '0.85em', marginBottom: 4 }}>
                  Secondary IPs; for Kubernetes pods this includes the node name and that node&apos;s IP
                  addresses when the cluster allows listing nodes.
                </Typography.Text>
                <Space direction="vertical" size={2}>
                  {hostDetailRecord.extra_ips.map((ip, i) => (
                    <Typography.Text key={`${i}:${ip}`} code>{ip}</Typography.Text>
                  ))}
                </Space>
              </div>
            ) : (
              <Typography.Text type="secondary">No extras on this record.</Typography.Text>
            )}
            {hostDetailRecord.meta?.tags && (
              <div>
                <Typography.Text type="secondary">Tags </Typography.Text>
                <code>{hostDetailRecord.meta.tags}</code>
              </div>
            )}
            {(() => {
              const labels = Object.entries(hostDetailRecord.meta || {})
                .filter(([k]) => k.startsWith('label_'))
                .map(([k, v]) => `${k.slice(6)}: ${v}`);
              if (labels.length === 0) return null;
              return (
                <div>
                  <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 4 }}>Labels</Typography.Text>
                  <Space size={4} wrap>
                    {labels.map((l) => <Tag key={l}>{l}</Tag>)}
                  </Space>
                </div>
              );
            })()}
            {(hostDetailRecord.region || hostDetailRecord.zone) && (
              <div>
                {hostDetailRecord.region ? <Typography.Text type="secondary">Region: {hostDetailRecord.region} </Typography.Text> : null}
                {hostDetailRecord.zone ? <Typography.Text type="secondary">Zone: {hostDetailRecord.zone}</Typography.Text> : null}
              </div>
            )}
          </Space>
        </Card>
      ) : null}

      <Typography.Text type="secondary" style={{ display: 'block', fontSize: '0.85rem' }}>
        Use row <strong>Upload</strong> for the file dialog. Drop a file on a row to upload to{' '}
        <code>/tmp/&lt;filename&gt;</code>{' '}
        (opens progress in the upload window).
      </Typography.Text>

      {/* Upload modal */}
      <Modal maskClosable={false}         open={uploadModalOpen}
        title="SFTP upload"
        onCancel={closeUploadModal}
        footer={<Button onClick={closeUploadModal}>Close</Button>}
        width="min(480px, 94vw)"
      >
        <Space direction="vertical" size={10} style={{ width: '100%' }}>
          <div>
            <Typography.Text style={{ display: 'block', marginBottom: 4 }}>Target host</Typography.Text>
            <Select
              style={{ width: '100%' }}
              value={records.length ? uploadTargetIdx : 0}
              disabled={records.length === 0}
              onChange={(v) => setUploadTargetIdx(v as number)}
              options={
                records.length === 0
                  ? [{ value: 0, label: 'Run a search first', key: '' }]
                  : records.map((rec, i) => ({
                      value: i,
                      label: `${rec.name} (${rec.provider}) — ${rec.primary_ip}`,
                      key: recordKey(rec),
                    }))
              }
            />
          </div>
          <div>
            <Typography.Text style={{ display: 'block', marginBottom: 4 }}>File</Typography.Text>
            <input ref={fileInputRef} type="file" style={{ display: 'block' }} />
          </div>
          <div>
            <Typography.Text style={{ display: 'block', marginBottom: 4 }}>Remote path</Typography.Text>
            <Input
              style={{ fontFamily: 'monospace' }}
              value={uploadRemote}
              onChange={(e) => setUploadRemote(e.target.value)}
              placeholder="/tmp/filename"
            />
          </div>
          <Typography.Text type="secondary">
            SSH user comes from the field next to Search on the main screen.
          </Typography.Text>
          <Button
            type="primary"
            loading={uploadXfer !== null}
            disabled={records.length === 0}
            onClick={() => onUploadSubmit()}
          >
            Upload
          </Button>
          {uploadXfer ? <UploadProgressBar xfer={uploadXfer} /> : null}
          {uploadStatus ? (
            <Alert
              type={uploadStatusIsError ? 'error' : 'success'}
              message={uploadStatus}
            />
          ) : null}
        </Space>
      </Modal>

      <ShareAccessModal
        record={shareTarget}
        open={shareTarget !== null}
        onClose={() => setShareTarget(null)}
      />

      <InterceptModal
        record={interceptTarget}
        open={interceptTarget !== null}
        onClose={() => setInterceptTarget(null)}
        onLaunch={openInterceptSession}
      />
    </section>
  );
}
