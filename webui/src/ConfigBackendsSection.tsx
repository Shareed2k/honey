import { useCallback, useEffect, useState } from 'react';
import { apiDelete, apiGet, apiPost, apiPutJson } from './api';

type GCPBackend = { name: string; project: string; zone: string };
type AWSBackend = { name: string; profile: string; region: string };
type KubernetesBackend = { name: string; context: string; kubeconfig: string; mode: string; debug_image: string };
type ConsulBackend = { name: string; addr: string; datacenter: string; token: string };
type ProxmoxBackend = {
  name: string;
  url: string;
  user: string;
  password: string;
  token_id: string;
  token_secret: string;
  insecure: boolean;
};

export type BackendsPayload = {
  gcp?: GCPBackend[];
  aws?: AWSBackend[];
  kubernetes?: KubernetesBackend[];
  consul?: ConsulBackend[];
  proxmox?: ProxmoxBackend[];
};

type Kind = 'gcp' | 'aws' | 'kubernetes' | 'consul' | 'proxmox';

const emptyGCP = (): GCPBackend => ({ name: '', project: '', zone: '' });
const emptyAWS = (): AWSBackend => ({ name: '', profile: '', region: '' });
const emptyK8s = (): KubernetesBackend => ({ name: '', context: '', kubeconfig: '', mode: 'nodes', debug_image: '' });
const emptyConsul = (): ConsulBackend => ({ name: '', addr: '', datacenter: '', token: '' });
const emptyProxmox = (): ProxmoxBackend => ({
  name: '',
  url: '',
  user: '',
  password: '',
  token_id: '',
  token_secret: '',
  insecure: false,
});

type Props = {
  onSaved: () => void;
};

export function ConfigBackendsSection({ onSaved }: Props) {
  const [data, setData] = useState<BackendsPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [editor, setEditor] = useState<{
    kind: Kind;
    index: number | null;
    draft: Record<string, unknown>;
  } | null>(null);

  const load = useCallback(async () => {
    setErr(null);
    const r = await apiGet('/api/v1/config/backends');
    if (!r.ok) {
      const j = await r.json().catch(() => ({}));
      setErr((j as { error?: string }).error || r.statusText);
      setData(null);
      return;
    }
    const j = (await r.json()) as BackendsPayload;
    setData({
      gcp: j.gcp || [],
      aws: j.aws || [],
      kubernetes: j.kubernetes || [],
      consul: j.consul || [],
      proxmox: j.proxmox || [],
    });
  }, []);

  const persist = async (fn: () => Promise<Response>): Promise<boolean> => {
    setBusy(true);
    setErr(null);
    try {
      const r = await fn();
      const j = await r.json().catch(() => ({}));
      if (!r.ok) {
        setErr((j as { error?: string }).error || r.statusText);
        return false;
      }
      await load();
      onSaved();
      return true;
    } finally {
      setBusy(false);
    }
  };

  useEffect(() => {
    void load();
  }, [load]);

  const openAdd = (kind: Kind) => {
    const draft =
      kind === 'gcp'
        ? (emptyGCP() as unknown as Record<string, unknown>)
        : kind === 'aws'
          ? (emptyAWS() as unknown as Record<string, unknown>)
          : kind === 'kubernetes'
            ? (emptyK8s() as unknown as Record<string, unknown>)
            : kind === 'consul'
              ? (emptyConsul() as unknown as Record<string, unknown>)
              : (emptyProxmox() as unknown as Record<string, unknown>);
    setEditor({ kind, index: null, draft });
  };

  const openEdit = (kind: Kind, index: number, row: unknown) => {
    setEditor({ kind, index, draft: { ...(row as Record<string, unknown>) } });
  };

  const saveEditor = () => {
    if (!editor) {
      return;
    }
    const { kind, index, draft } = editor;
    const body = draft;
    void (async () => {
      const ok =
        index === null
          ? await persist(() => apiPost(`/api/v1/config/backends/${kind}`, body))
          : await persist(() => apiPutJson(`/api/v1/config/backends/${kind}/${index}`, body));
      if (ok) {
        setEditor(null);
      }
    })();
  };

  const remove = (kind: Kind, index: number) => {
    if (!window.confirm(`Delete ${kind} backend #${index}?`)) {
      return;
    }
    void persist(() => apiDelete(`/api/v1/config/backends/${kind}/${index}`));
  };

  const renderRows = (kind: Kind, rows: unknown[]) => {
    const list = rows as { name?: string }[];
    return (
      <table style={{ width: '100%', marginTop: '0.35rem' }}>
        <thead>
          <tr>
            <th style={{ textAlign: 'left', width: '3rem' }}>#</th>
            <th style={{ textAlign: 'left' }}>Name</th>
            <th style={{ textAlign: 'left' }}>Actions</th>
          </tr>
        </thead>
        <tbody>
          {list.map((row, i) => {
            const displayName = row.name?.trim() ? row.name : '(unnamed)';
            return (
              <tr key={`${kind}-${i}`}>
                <td>{i}</td>
                <td>{displayName}</td>
                <td style={{ whiteSpace: 'nowrap' }}>
                  <button type="button" disabled={busy} onClick={() => openEdit(kind, i, row)}>
                    Edit
                  </button>{' '}
                  <button type="button" disabled={busy} onClick={() => remove(kind, i)}>
                    Delete
                  </button>
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    );
  };

  return (
    <section style={{ marginTop: '1.25rem', borderTop: '1px solid #333', paddingTop: '1rem' }}>
      <h2 style={{ fontSize: '1.1rem' }}>Backends (structured)</h2>
      <p style={{ fontSize: '0.8rem', opacity: 0.8 }}>
        REST paths use YAML keys: <code>gcp</code>, <code>aws</code>, <code>kubernetes</code>, <code>consul</code>, <code>proxmox</code>.
        Search provider id for Kubernetes is <code>k8s</code>; backend rows list <code>kubernetes</code> as kind.
      </p>
      {err ? <p style={{ color: '#f66' }}>{err}</p> : null}
      <div style={{ display: 'flex', gap: '0.5rem', flexWrap: 'wrap', marginBottom: '0.75rem' }}>
        <button type="button" disabled={busy} onClick={() => void load()}>
          Reload backends JSON
        </button>
      </div>
      {data ? (
        <div style={{ display: 'grid', gap: '1rem' }}>
          {(['gcp', 'aws', 'kubernetes', 'consul', 'proxmox'] as Kind[]).map((kind) => {
            const rows = (data[kind] || []) as unknown[];
            return (
              <div key={kind}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', flexWrap: 'wrap' }}>
                  <strong>{kind}</strong>
                  <button type="button" disabled={busy} onClick={() => openAdd(kind)}>
                    Add
                  </button>
                  <span style={{ fontSize: '0.75rem', opacity: 0.75 }}>
                    Secrets and full fields appear only in Add/Edit.
                  </span>
                </div>
                {rows.length === 0 ? <p style={{ opacity: 0.7, fontSize: '0.85rem' }}>(none)</p> : renderRows(kind, rows)}
              </div>
            );
          })}
        </div>
      ) : null}

      {editor ? (
        <div
          style={{
            position: 'fixed',
            inset: 0,
            background: 'rgba(0,0,0,0.6)',
            display: 'flex',
            alignItems: 'center',
            justifyContent: 'center',
            zIndex: 50,
          }}
        >
          <div
            style={{
              background: '#1a1a1a',
              padding: '1rem',
              borderRadius: 8,
              minWidth: 'min(420px, 92vw)',
              maxHeight: '90vh',
              overflow: 'auto',
            }}
          >
            <h3 style={{ marginTop: 0 }}>
              {editor.index === null ? 'Add' : 'Edit'} {editor.kind}
            </h3>
            <BackendFormFields kind={editor.kind} draft={editor.draft} setDraft={(d) => setEditor({ ...editor, draft: d })} />
            <div style={{ marginTop: '0.75rem', display: 'flex', gap: '0.5rem' }}>
              <button type="button" className="primary" disabled={busy} onClick={() => saveEditor()}>
                Save
              </button>
              <button type="button" disabled={busy} onClick={() => setEditor(null)}>
                Cancel
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </section>
  );
}

function BackendFormFields({
  kind,
  draft,
  setDraft,
}: {
  kind: Kind;
  draft: Record<string, unknown>;
  setDraft: (d: Record<string, unknown>) => void;
}) {
  const set = (k: string, v: string | boolean) => setDraft({ ...draft, [k]: v });
  const inp = (key: string, label: string, type: 'text' | 'password' = 'text') => (
    <label style={{ display: 'block', marginBottom: '0.5rem' }}>
      <div style={{ fontSize: '0.8rem', opacity: 0.85 }}>{label}</div>
      <input
        type={type}
        style={{ width: '100%' }}
        value={String(draft[key] ?? '')}
        onChange={(e) => set(key, e.target.value)}
      />
    </label>
  );
  const chk = (key: string, label: string) => (
    <label style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: '0.5rem' }}>
      <input type="checkbox" checked={Boolean(draft[key])} onChange={(e) => set(key, e.target.checked)} />
      {label}
    </label>
  );

  switch (kind) {
    case 'gcp':
      return (
        <>
          {inp('name', 'Name')}
          {inp('project', 'Project')}
          {inp('zone', 'Zone')}
        </>
      );
    case 'aws':
      return (
        <>
          {inp('name', 'Name')}
          {inp('profile', 'Profile')}
          {inp('region', 'Region')}
        </>
      );
    case 'kubernetes':
      return (
        <>
          {inp('name', 'Name')}
          {inp('context', 'Context')}
          {inp('kubeconfig', 'Kubeconfig path')}
          {inp('mode', 'Mode (nodes or pods)')}
          {inp('debug_image', 'Debug image')}
        </>
      );
    case 'consul':
      return (
        <>
          {inp('name', 'Name')}
          {inp('addr', 'Address')}
          {inp('datacenter', 'Datacenter')}
          {inp('token', 'Token', 'password')}
        </>
      );
    case 'proxmox':
      return (
        <>
          {inp('name', 'Name')}
          {inp('url', 'URL')}
          {inp('user', 'User')}
          {inp('password', 'Password', 'password')}
          {inp('token_id', 'Token ID')}
          {inp('token_secret', 'Token secret', 'password')}
          {chk('insecure', 'Insecure TLS')}
        </>
      );
    default:
      return null;
  }
}
