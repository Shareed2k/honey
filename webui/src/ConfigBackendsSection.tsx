import { useCallback, useEffect, useState, useMemo } from 'react';
import { useForm, useFieldArray, FormProvider, useFormContext } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
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
    if (field.type === 'array') {
      draft[field.key] = [];
      continue;
    }
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

function buildZodSchema(fields: ConfigSchemaFieldSpec[]): z.ZodTypeAny {
  const shape: Record<string, z.ZodTypeAny> = {};
  
  for (const f of fields) {
    let fieldSchema: z.ZodTypeAny;
    
    if (f.type === 'string') {
      let strSchema: z.ZodTypeAny = z.string();
      
      if (f.format === 'ip') {
        strSchema = z.string().refine((val) => {
          if (!val && !f.required) return true;
          return z.ipv4().safeParse(val).success || z.ipv6().safeParse(val).success;
        }, { message: `${f.label} must be a valid IP address` });
      } else if (f.format === 'url') {
        strSchema = z.string().refine((val) => {
          if (!val && !f.required) return true;
          return z.string().url().safeParse(val).success;
        }, { message: `${f.label} must be a valid URL` });
      }

      // If required, we enforce a minimum length of 1 on strings
      // because otherwise an empty string passes z.string()
      if (f.required) {
        if (f.format === 'ip') {
          fieldSchema = strSchema;
        } else if (f.format === 'url') {
          fieldSchema = strSchema;
        } else {
          fieldSchema = (strSchema as z.ZodString).min(1, { message: `${f.label} is required` });
        }
      } else {
        fieldSchema = z.union([strSchema, z.literal(''), z.undefined()]).optional();
      }
    } else if (f.type === 'integer') {
      fieldSchema = z.coerce.number().int();
    } else if (f.type === 'boolean') {
      fieldSchema = z.boolean();
    } else if (f.type === 'array') {
      if (f.items && f.items.length > 0 && f.items[0].key !== '') {
        fieldSchema = z.array(buildZodSchema(f.items));
      } else if (f.items && f.items.length > 0 && f.items[0].type === 'string') {
        let itemStrSchema: z.ZodTypeAny = z.string();
        if (f.items[0].format === 'ip') {
          itemStrSchema = z.string().refine((val) => {
            if (!val) return true;
            return z.ipv4().safeParse(val).success || z.ipv6().safeParse(val).success;
          }, { message: `${f.label} items must be valid IP addresses` });
        } else if (f.items[0].format === 'url') {
          itemStrSchema = z.string().refine((val) => {
            if (!val) return true;
            return z.string().url().safeParse(val).success;
          }, { message: `${f.label} items must be valid URLs` });
        }
        fieldSchema = z.array(itemStrSchema);
      } else {
        fieldSchema = z.array(z.string());
      }
    } else {
      fieldSchema = z.any();
    }

    if (f.enum && f.enum.length > 0) {
      fieldSchema = z.enum(f.enum as [string, ...string[]]);
    }

    if (f.type !== 'string') {
      if (!f.required) {
        fieldSchema = z.union([fieldSchema, z.literal(''), z.undefined()]).optional();
      }
    }
    
    shape[f.key] = fieldSchema;
  }
  
  return z.object(shape);
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
      local: j.local || [],
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
        <EditorModal 
           editor={editor} 
           schema={schema} 
           busy={busy}
           error={err}
           onClose={() => {
             setErr(null);
             setEditor(null);
           }} 
           onSave={async (body) => {
             const { kind, index } = editor;
             const ok = index === null
               ? await persist(() => apiPost(`/api/v1/config/backends/${kind}`, body))
               : await persist(() => apiPutJson(`/api/v1/config/backends/${kind}/${index}`, body));
             if (ok) {
               setEditor(null);
             }
           }} 
        />
      ) : null}
    </section>
  );
}

function EditorModal({
  editor,
  schema,
  busy,
  error,
  onClose,
  onSave
}: {
  editor: { kind: string; index: number | null; draft: Record<string, unknown> };
  schema: ConfigUISchema | null;
  busy: boolean;
  error: string | null;
  onClose: () => void;
  onSave: (body: any) => Promise<void>;
}) {
  const backendDef = schema?.backends[editor.kind];
  
  const zodSchema = useMemo(() => {
    if (!backendDef) return z.any();
    return buildZodSchema(backendDef.fields);
  }, [backendDef]);

  const methods = useForm({
    resolver: zodResolver(zodSchema),
    defaultValues: editor.draft,
    mode: 'onChange',
  });

  const onSubmit = (data: any) => {
    return onSave(data);
  };

  return (
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
        {error ? <div style={{ color: '#f66', marginBottom: '1rem', padding: '0.5rem', border: '1px solid #f66', borderRadius: 4 }}>{error}</div> : null}
        {backendDef ? (
          <FormProvider {...methods}>
            <form onSubmit={methods.handleSubmit(onSubmit)}>
              <BackendFormFields fields={backendDef.fields} path="" />
              <div style={{ marginTop: '0.75rem', display: 'flex', gap: '0.5rem' }}>
                <button type="submit" className="primary" disabled={busy}>
                  Save
                </button>
                <button type="button" disabled={busy} onClick={onClose}>
                  Cancel
                </button>
              </div>
            </form>
          </FormProvider>
        ) : (
          <p style={{ color: '#f66' }}>Missing schema for backend kind "{editor.kind}".</p>
        )}
      </div>
    </div>
  );
}

function BackendFormFields({
  fields,
  path
}: {
  fields: ConfigSchemaFieldSpec[];
  path: string;
}) {
  const { register, formState: { errors } } = useFormContext();

  return (
    <>
      {fields.map((field) => {
        const label = field.required ? `${field.label} *` : field.label;
        const fieldPath = path ? `${path}.${field.key}` : field.key;
        
        // Deep error resolution for nested arrays
        const error = fieldPath.split('.').reduce((obj: any, key) => (obj ? obj[key] : undefined), errors);
        const errorMessage = error?.message as string | undefined;

        if (field.type === 'array') {
          return (
            <div key={field.key} style={{ marginBottom: '1rem', padding: '0.5rem', border: '1px solid #333', borderRadius: 4 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: '0.5rem' }}>
                <strong>{label}</strong>
              </div>
              
              <ArrayFieldManager field={field} path={fieldPath} />
              
              {errorMessage && <div style={{ color: '#f66', fontSize: '0.75rem', marginTop: '0.25rem' }}>{errorMessage}</div>}
            </div>
          );
        }

        if (field.type === 'boolean') {
          return (
            <label key={field.key} style={{ display: 'flex', alignItems: 'center', gap: 8, marginBottom: '0.5rem' }}>
              <input type="checkbox" {...register(fieldPath)} />
              {label}
              {errorMessage && <span style={{ color: '#f66', fontSize: '0.75rem', marginLeft: 'auto' }}>{errorMessage}</span>}
            </label>
          );
        }

        if (field.enum && field.enum.length > 0) {
          return (
            <label key={field.key} style={{ display: 'block', marginBottom: '0.5rem' }}>
              <div style={{ fontSize: '0.8rem', opacity: 0.85 }}>{label}</div>
              <select style={{ width: '100%', borderColor: errorMessage ? '#f66' : undefined }} {...register(fieldPath)}>
                {field.enum.map((option) => (
                  <option key={option} value={option}>
                    {option}
                  </option>
                ))}
              </select>
              {errorMessage && <div style={{ color: '#f66', fontSize: '0.75rem', marginTop: '0.25rem' }}>{errorMessage}</div>}
            </label>
          );
        }

        if (field.type === 'integer') {
          return (
            <label key={field.key} style={{ display: 'block', marginBottom: '0.5rem' }}>
              <div style={{ fontSize: '0.8rem', opacity: 0.85 }}>{label}</div>
              <input
                type="number"
                style={{ width: '100%', borderColor: errorMessage ? '#f66' : undefined }}
                {...register(fieldPath, { valueAsNumber: true })}
              />
              {errorMessage && <div style={{ color: '#f66', fontSize: '0.75rem', marginTop: '0.25rem' }}>{errorMessage}</div>}
            </label>
          );
        }

        return (
          <label key={field.key} style={{ display: 'block', marginBottom: '0.5rem' }}>
            <div style={{ fontSize: '0.8rem', opacity: 0.85 }}>{label}</div>
            <input
              type={field.secret ? 'password' : 'text'}
              style={{ width: '100%', borderColor: errorMessage ? '#f66' : undefined }}
              {...register(fieldPath)}
            />
            {errorMessage && <div style={{ color: '#f66', fontSize: '0.75rem', marginTop: '0.25rem' }}>{errorMessage}</div>}
          </label>
        );
      })}
    </>
  );
}

function ArrayFieldManager({ field, path }: { field: ConfigSchemaFieldSpec, path: string }) {
  const { control, register, formState: { errors } } = useFormContext();
  const { fields, append, remove } = useFieldArray({ control, name: path });
  
  return (
    <div>
      <div style={{ marginBottom: '0.5rem' }}>
        <button
          type="button"
          onClick={() => {
            const newItem = field.items && field.items.length > 0 && field.items[0].key !== '' 
                ? initDraft(field.items) 
                : '';
            append(newItem);
          }}
        >
          Add Item
        </button>
      </div>
      
      {fields.length === 0 ? <div style={{ fontSize: '0.8rem', opacity: 0.7 }}>No items.</div> : null}
      
      {fields.map((item, idx) => {
        const itemError = path.split('.').reduce((obj: any, key) => (obj ? obj[key] : undefined), errors)?.[idx];
        const errorMessage = itemError?.message as string | undefined;

        return (
          <div key={item.id} style={{ marginBottom: '0.75rem', padding: '0.5rem', background: 'rgba(255,255,255,0.05)', borderRadius: 4 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: '0.5rem' }}>
              <span style={{ fontSize: '0.8rem', fontWeight: 'bold' }}>Item #{idx}</span>
              <button
                type="button"
                style={{ fontSize: '0.7rem', padding: '2px 6px' }}
                onClick={() => remove(idx)}
              >
                Remove
              </button>
            </div>
            
            {field.items && field.items.length > 0 && field.items[0].key !== "" ? (
              <BackendFormFields
                fields={field.items}
                path={`${path}.${idx}`}
              />
            ) : (
              <>
                <input
                  type="text"
                  style={{ width: '100%', borderColor: errorMessage ? '#f66' : undefined }}
                  {...register(`${path}.${idx}` as const)}
                />
                {errorMessage && <div style={{ color: '#f66', fontSize: '0.75rem', marginTop: '0.25rem' }}>{errorMessage}</div>}
              </>
            )}
          </div>
        );
      })}
    </div>
  );
}
