import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import {
  Alert, Button, Card, Checkbox, Input, Modal, Progress, Select, Space, Table, Tag, Typography,
} from 'antd';
import type { ColumnsType } from 'antd/es/table';
import {
  apiPost,
  execOnHostsStream,
  uploadFormDataWithSFTPStream,
} from '../api';
import type {
  FormDataUploadProgressEvent,
  HostExecResultRow,
  UploadStreamServerEvent,
} from '../api';
import { HostPicker, recordKey } from '../HostPicker';
import type { HostRecord } from '../HostPicker';
import type { TerminalSessionConfig, PveConsoleMode, TrueNASConsoleMode } from '../TerminalModal';

export type BackendRow = { kind: string; name: string; hint: string };

type MetaInfo = {
  version: string;
  config_path: string;
  session_recording_available?: boolean;
  terminal_assist_available?: boolean;
};

type UploadXferState = {
  honeyLoaded: number;
  honeyTotal: number | null;
  awaitingResponse: boolean;
  sftpSent: number;
  sftpTotal: number;
  sftpActive: boolean;
};

interface Props {
  records: HostRecord[];
  selectedKeys: Record<string, boolean>;
  onRecordsChange: (records: HostRecord[]) => void;
  onSelectedKeysChange: (keys: Record<string, boolean>) => void;
  selectedProviders: string[];
  onSelectedProvidersChange: (v: string[]) => void;
  selectedBackends: string[];
  onSelectedBackendsChange: (v: string[]) => void;
  backends: BackendRow[];
  providerIds: string[];
  sshUser: string;
  onSshUserChange: (v: string) => void;
  meta: MetaInfo | null;
  onOpenTunnel: (rec: HostRecord) => void;
  onOpenReplay: (rec: HostRecord) => void;
  onOpenReplayAll: () => void;
  onOpenTerminal: (cfg: TerminalSessionConfig) => void;
  /** Externally tracked terminal configs, needed to show open-state on buttons */
  terminals?: TerminalSessionConfig[];
}

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

export function SearchTab({
  records,
  selectedKeys,
  onRecordsChange,
  onSelectedKeysChange,
  selectedProviders,
  onSelectedProvidersChange,
  selectedBackends,
  onSelectedBackendsChange,
  backends,
  providerIds,
  sshUser,
  onSshUserChange,
  meta,
  onOpenTunnel,
  onOpenReplay,
  onOpenReplayAll,
  onOpenTerminal,
  terminals = [],
}: Props) {
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

  const [hostDetailRecord, setHostDetailRecord] = useState<HostRecord | null>(null);
  const [visibleRecords, setVisibleRecords] = useState<HostRecord[]>([]);

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

  const namedBackends = backends.filter((b) => b.name.trim() !== '');
  const backendOptions = namedBackends.map((b) => ({
    value: backendRef(b),
    label: `${b.kind}: ${b.name}`,
  }));

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
        onRecordsChange([]);
        return;
      }
      setExecResults(null);
      setExecErr(null);
      onRecordsChange((j as { records: HostRecord[] }).records || []);
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
    onSelectedKeysChange(
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
    onSelectedKeysChange(next);
  };

  const clearHostSelection = () => onSelectedKeysChange({});

  const clearExecOutput = () => {
    setExecErr(null);
    setExecResults(null);
  };

  const runParallelExec = async () => {
    const cmd = execCommand.trim();
    if (!cmd || selectedRecords.length === 0) {
      setExecErr('Select at least one host and enter a command.');
      return;
    }
    setExecBusy(true);
    setExecErr(null);
    setExecResults([]);
    try {
      await execOnHostsStream(
        {
          ssh_user: sshUser.trim(),
          command: cmd,
          records: selectedRecords,
          record_session: !!(recordWebSession && meta?.session_recording_available),
        },
        (row) => setExecResults((prev) => [...(prev || []), row]),
      );
    } catch (e) {
      setExecErr(e instanceof Error ? e.message : String(e));
    } finally {
      setExecBusy(false);
    }
  };

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
    const id = Math.random().toString(36).slice(2);
    sessionStorage.setItem(`honey_term_${id}`, JSON.stringify(rec));
    const cfg: TerminalSessionConfig = { id, record: rec, pve, truenasConsole };
    onOpenTerminal(cfg);
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
        <pre style={{ margin: 0, fontSize: '0.78rem', whiteSpace: 'pre-wrap', wordBreak: 'break-word', maxWidth: 420 }}>
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
          value={selectedProviders}
          onChange={onSelectedProvidersChange}
          options={providerIds.map((id) => ({ value: id, label: id }))}
          style={{ minWidth: 160 }}
          maxTagCount="responsive"
          allowClear
        />
        <Select
          mode="multiple"
          placeholder="All backends"
          value={selectedBackends}
          onChange={onSelectedBackendsChange}
          options={backendOptions.map((o) => ({ value: o.value, label: o.label }))}
          style={{ minWidth: 160 }}
          maxTagCount="responsive"
          allowClear
        />
        {meta?.session_recording_available && (
          <Button onClick={() => void onOpenReplayAll()}>Browse recordings</Button>
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
          onChange={(e) => onSshUserChange(e.target.value)}
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
        </Space>
        <Input.TextArea
          value={execCommand}
          onChange={(e) => setExecCommand(e.target.value)}
          placeholder="e.g. uname -a"
          rows={3}
          style={{ fontFamily: 'monospace', fontSize: '0.85rem', marginBottom: 8 }}
        />
        <Space>
          <Button
            type="primary"
            loading={execBusy}
            disabled={selectedRecords.length === 0 || !execCommand.trim()}
            onClick={() => void runParallelExec()}
          >
            Run on {selectedRecords.length} host(s)
          </Button>
          <Button onClick={clearExecOutput}>Clear results</Button>
        </Space>
        {execErr && <Alert type="error" message={execErr} style={{ marginTop: 8 }} />}
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
          const serialTerms = activeTerms.filter((t) => t.pve === 'serial' && (t.truenasConsole ?? 'ssh') === 'ssh');
          const apiTerms = activeTerms.filter((t) => t.truenasConsole === 'api');
          const vncTerms = activeTerms.filter((t) => t.pve === 'vnc');

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
              <Button size="small" onClick={() => openUploadModal(rec)}>
                Upload
              </Button>
              <Button
                size="small"
                disabled={!canPortForwardTunnel(rec)}
                title={
                  !canPortForwardTunnel(rec)
                    ? 'Port-forward requires SSH IP or a TrueNAS API shell target'
                    : undefined
                }
                onClick={() => onOpenTunnel(rec)}
              >
                Tunnel
              </Button>
              {meta?.session_recording_available ? (
                <Button size="small" onClick={() => void onOpenReplay(rec)}>
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
      <Modal
        open={uploadModalOpen}
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
    </section>
  );
}
