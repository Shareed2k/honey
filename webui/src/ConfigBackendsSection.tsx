import { useCallback, useEffect, useState } from 'react';
import { apiDelete, apiGet, apiPost, apiPutJson } from './api';
import type { ConfigSchemaFieldSpec, ConfigUISchema } from './api';

export type BackendsPayload = Record<string, Record<string, unknown>[] | undefined>;

type Props = {
  onSaved: () => void;
  schema: ConfigUISchema | null;
};

function initDraft(fields: ConfigSchemaFieldSpec[]): Record<string, unknown> {
  const draft: Record<string, unknown> = {};
  for (const field of fields) {
    if (field.default !== undefined) {
      draft[field.key] = field.default;
      continue;
    }
    if (field.type === 'boolean') {
      draft[field.key] = false;
      continue;
    }
    if (field.enum && field.enum.length > 0) {
      draft[field.key] = field.enum[0];
      continue;
    }
    draft[field.key] = '';
  }
  return draft;
}

export function ConfigBackendsSection({ onSaved, schema }: Props) {
  const [data, setData] = useState<BackendsPayload | null>(null);
  const [err, setErr] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [editor, setEditor] = useState<{
    kind: string;
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

  const openAdd = (kind: string) => {
    const backend = schema?.backends[kind];
    if (!backend) {
      setErr(`Missing schema for backend kind "${kind}".`);
      return;
    }
    const draft = initDraft(backend.fields);
    setEditor({ kind, index: null, draft });
  };

  const openEdit = (kind: string, index: number, row: unknown) => {
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

  const remove = (kind: string, index: number) => {
    if (!window.confirm(`Delete ${kind} backend #${index}?`)) {
      return;
    }
    void persist(() => apiDelete(`/api/v1/config/backends/${kind}/${index}`));
  };

  const renderRows = (kind: string, rows: unknown[]) => {
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
      {!schema ? <p style={{ opacity: 0.8, fontSize: '0.85rem' }}>Config schema is required to render backend forms.</p> : null}
      {data ? (
        <div style={{ display: 'grid', gap: '1rem' }}>
          {(schema?.backend_order || []).map((kind) => {
            const rows = (data[kind] || []) as unknown[];
            const backendDef = schema?.backends[kind];
            return (
              <div key={kind}>
                <div style={{ display: 'flex', alignItems: 'center', gap: '0.5rem', flexWrap: 'wrap' }}>
                  <strong>{backendDef?.label || kind}</strong>
                  <button type="button" disabled={busy || !schema} onClick={() => openAdd(kind)}>
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
            {schema?.backends[editor.kind] ? (
              <BackendFormFields
                fields={schema.backends[editor.kind].fields}
                draft={editor.draft}
                setDraft={(d) => setEditor({ ...editor, draft: d })}
              />
            ) : (
              <p style={{ color: '#f66' }}>Missing schema for backend kind "{editor.kind}".</p>
            )}
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
  fields,
  draft,
  setDraft,
}: {
  fields: ConfigSchemaFieldSpec[];
  draft: Record<string, unknown>;
  setDraft: (d: Record<string, unknown>) => void;
}) {
  const set = (k: string, v: string | boolean | number) => setDraft({ ...draft, [k]: v });

  return (
    <>
      {fields.map((field) => {
        const label = field.required ? `${field.label} *` : field.label;
        if (field.type === 'boolean') {
          return (
            <label key={field.key} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: '0.5rem' }}>
              <input type="checkbox" checked={Boolean(draft[field.key])} onChange={(e) => set(field.key, e.target.checked)} />
              {label}
            </label>
          );
        }

        if (field.enum && field.enum.length > 0) {
          return (
            <label key={field.key} style={{ display: 'block', marginBottom: '0.5rem' }}>
              <div style={{ fontSize: '0.8rem', opacity: 0.85 }}>{label}</div>
              <select
                style={{ width: '100%' }}
                value={String(draft[field.key] ?? field.enum[0] ?? '')}
                onChange={(e) => set(field.key, e.target.value)}
              >
                {field.enum.map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
            </label>
          );
        }

        if (field.type === 'integer') {
          return (
            <label key={field.key} style={{ display: 'block', marginBottom: '0.5rem' }}>
              <div style={{ fontSize: '0.8rem', opacity: 0.85 }}>{label}</div>
              <input
                type="number"
                style={{ width: '100%' }}
                value={String(draft[field.key] ?? '')}
                onChange={(e) => {
                  const raw = e.target.value.trim();
                  set(field.key, raw === '' ? 0 : Number.parseInt(raw, 10));
                }}
              />
            </label>
          );
        }

        return (
          <label key={field.key} style={{ display: 'block', marginBottom: '0.5rem' }}>
            <div style={{ fontSize: '0.8rem', opacity: 0.85 }}>{label}</div>
            <input
              type={field.secret ? 'password' : 'text'}
              style={{ width: '100%' }}
              value={String(draft[field.key] ?? '')}
              onChange={(e) => set(field.key, e.target.value)}
            />
          </label>
        );
      })}
    </>
  );
}
