import { useCallback, useEffect, useState, useMemo } from 'react';
import { useForm, useFieldArray, FormProvider, useFormContext, Controller } from 'react-hook-form';
import { zodResolver } from '@hookform/resolvers/zod';
import { z } from 'zod';
import { Alert, Button, Card, Checkbox, Divider, Input, InputNumber, Modal, Select, Space, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
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
    if (field.type === 'object') {
      draft[field.key] = initDraft(field.items || []);
      continue;
    }
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
    } else if (f.type === 'object') {
      fieldSchema = buildZodSchema(f.items || []);
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

    if (f.type !== 'string' && f.type !== 'object') {
      if (!f.required) {
        fieldSchema = z.union([fieldSchema, z.literal(''), z.undefined()]).optional();
      }
    }

    shape[f.key] = fieldSchema;
  }

  return z.object(shape);
}

function normalizeBackendsPayload(
  j: BackendsPayload,
  schema: ConfigUISchema | null,
): BackendsPayload {
  const kinds =
    schema?.backend_order?.length
      ? schema.backend_order
      : Object.keys(j).filter((k) => Array.isArray(j[k]));
  const normalized: BackendsPayload = {};
  for (const kind of kinds) {
    normalized[kind] = (Array.isArray(j[kind]) ? j[kind] : []) as Record<string, unknown>[];
  }
  return normalized;
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
    setData(normalizeBackendsPayload(j, schema));
  }, [schema]);

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
    Modal.confirm({
      title: `Delete ${kind} backend #${index}?`,
      okText: 'Delete',
      okButtonProps: { danger: true },
      onOk: () => void persist(() => apiDelete(`/api/v1/config/backends/${kind}/${index}`)),
    });
  };

  const renderRows = (kind: string, rows: unknown[]) => {
    const list = rows as { name?: string }[];
    const cols: ColumnsType<{ name?: string }> = [
      {
        title: '#',
        key: 'idx',
        width: 48,
        render: (_: unknown, _row: unknown, i: number) => i,
      },
      {
        title: 'Name',
        key: 'name',
        render: (_: unknown, row: { name?: string }) => row.name?.trim() || '(unnamed)',
      },
      {
        title: 'Actions',
        key: 'actions',
        width: 140,
        render: (_: unknown, row: { name?: string }, i: number) => (
          <Space size={4}>
            <Button size="small" disabled={busy} onClick={() => openEdit(kind, i, row)}>
              Edit
            </Button>
            <Button size="small" danger disabled={busy} onClick={() => remove(kind, i)}>
              Delete
            </Button>
          </Space>
        ),
      },
    ];
    return (
      <Table
        dataSource={list}
        columns={cols}
        rowKey={(_row, i) => `${kind}-${i ?? 0}`}
        size="small"
        pagination={false}
      />
    );
  };

  return (
    <>
      <Divider />
      <section>
        <Typography.Title level={5}>Backends (structured)</Typography.Title>
        <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 8 }}>
          REST paths use YAML keys from <Typography.Text code>backend_order</Typography.Text> (e.g.{' '}
          <Typography.Text code>gcp</Typography.Text>, <Typography.Text code>aws</Typography.Text>,{' '}
          <Typography.Text code>kubernetes</Typography.Text>, <Typography.Text code>consul</Typography.Text>,{' '}
          <Typography.Text code>proxmox</Typography.Text>, <Typography.Text code>truenas</Typography.Text>,{' '}
          <Typography.Text code>local</Typography.Text>, <Typography.Text code>docker</Typography.Text>).
          Search provider id for Kubernetes is <Typography.Text code>k8s</Typography.Text>; backend rows list{' '}
          <Typography.Text code>kubernetes</Typography.Text> as kind.
        </Typography.Text>
        {err ? <Alert type="error" message={err} style={{ marginBottom: 8 }} /> : null}
        <Space wrap style={{ marginBottom: 12 }}>
          <Button disabled={busy} onClick={() => void load()}>Reload backends JSON</Button>
        </Space>
        {!schema ? (
          <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 8 }}>
            Config schema is required to render backend forms.
          </Typography.Text>
        ) : null}
        {data ? (
          <Space direction="vertical" size={12} style={{ width: '100%' }}>
            {(schema?.backend_order || []).map((kind) => {
              const rows = (data[kind] || []) as unknown[];
              const backendDef = schema?.backends[kind];
              return (
                <div key={kind}>
                  <Space wrap style={{ marginBottom: 4 }}>
                    <Typography.Text strong>{backendDef?.label || kind}</Typography.Text>
                    <Button size="small" disabled={busy || !schema} onClick={() => openAdd(kind)}>
                      Add
                    </Button>
                    <Typography.Text type="secondary">
                      Secrets and full fields appear only in Add/Edit.
                    </Typography.Text>
                  </Space>
                  {rows.length === 0 ? (
                    <Typography.Text type="secondary">(none)</Typography.Text>
                  ) : (
                    renderRows(kind, rows)
                  )}
                </div>
              );
            })}
          </Space>
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
              const ok =
                index === null
                  ? await persist(() => apiPost(`/api/v1/config/backends/${kind}`, body))
                  : await persist(() => apiPutJson(`/api/v1/config/backends/${kind}/${index}`, body));
              if (ok) {
                setEditor(null);
              }
            }}
          />
        ) : null}
      </section>
    </>
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
    <Modal
      open
      title={`${editor.index === null ? 'Add' : 'Edit'} ${editor.kind}`}
      onCancel={onClose}
      footer={null}
      width="min(460px, 92vw)"
      destroyOnHidden
    >
      {error ? <Alert type="error" message={error} style={{ marginBottom: 12 }} /> : null}
      {backendDef ? (
        <FormProvider {...methods}>
          <form onSubmit={methods.handleSubmit(onSubmit)}>
            <BackendFormFields fields={backendDef.fields} path="" />
            <Space style={{ marginTop: 12 }}>
              <Button type="primary" htmlType="submit" disabled={busy}>
                Save
              </Button>
              <Button disabled={busy} onClick={onClose}>
                Cancel
              </Button>
            </Space>
          </form>
        </FormProvider>
      ) : (
        <Typography.Text type="danger">
          Missing schema for backend kind &ldquo;{editor.kind}&rdquo;.
        </Typography.Text>
      )}
    </Modal>
  );
}

function BackendFormFields({
  fields,
  path
}: {
  fields: ConfigSchemaFieldSpec[];
  path: string;
}) {
  const { control, formState: { errors } } = useFormContext();

  return (
    <>
      {fields.map((field) => {
        const label = field.required ? `${field.label} *` : field.label;
        const fieldPath = path ? `${path}.${field.key}` : field.key;

        // Deep error resolution for nested arrays
        const error = fieldPath.split('.').reduce((obj: any, key) => (obj ? obj[key] : undefined), errors);
        const errorMessage = error?.message as string | undefined;

        if (field.type === 'object') {
          return (
            <Card key={field.key} size="small" title={<Typography.Text strong>{label}</Typography.Text>} style={{ marginBottom: 8 }}>
              <BackendFormFields fields={field.items || []} path={fieldPath} />
            </Card>
          );
        }

        if (field.type === 'array') {
          return (
            <Card key={field.key} size="small" title={<Typography.Text strong>{label}</Typography.Text>} style={{ marginBottom: 8 }}>
              <ArrayFieldManager field={field} path={fieldPath} />
              {errorMessage && (
                <Typography.Text type="danger" style={{ fontSize: '0.75rem' }}>
                  {errorMessage}
                </Typography.Text>
              )}
            </Card>
          );
        }

        if (field.type === 'boolean') {
          return (
            <div key={field.key} style={{ marginBottom: 8 }}>
              <Controller
                control={control}
                name={fieldPath}
                render={({ field: f }) => (
                  <Checkbox checked={!!f.value} onChange={(e) => f.onChange(e.target.checked)}>
                    {label}
                  </Checkbox>
                )}
              />
              {errorMessage && (
                <Typography.Text type="danger" style={{ display: 'block', fontSize: '0.75rem' }}>
                  {errorMessage}
                </Typography.Text>
              )}
            </div>
          );
        }

        if (field.enum && field.enum.length > 0) {
          return (
            <div key={field.key} style={{ marginBottom: 8 }}>
              <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 4 }}>
                {label}
              </Typography.Text>
              <Controller
                control={control}
                name={fieldPath}
                render={({ field: f }) => (
                  <Select
                    {...f}
                    style={{ width: '100%' }}
                    status={errorMessage ? 'error' : undefined}
                    options={field.enum!.map((o) => ({ value: o, label: o }))}
                  />
                )}
              />
              {errorMessage && (
                <Typography.Text type="danger" style={{ display: 'block', fontSize: '0.75rem' }}>
                  {errorMessage}
                </Typography.Text>
              )}
            </div>
          );
        }

        if (field.type === 'integer') {
          return (
            <div key={field.key} style={{ marginBottom: 8 }}>
              <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 4 }}>
                {label}
              </Typography.Text>
              <Controller
                control={control}
                name={fieldPath}
                render={({ field: f }) => (
                  <InputNumber
                    {...f}
                    style={{ width: '100%' }}
                    status={errorMessage ? 'error' : undefined}
                  />
                )}
              />
              {errorMessage && (
                <Typography.Text type="danger" style={{ display: 'block', fontSize: '0.75rem' }}>
                  {errorMessage}
                </Typography.Text>
              )}
            </div>
          );
        }

        return (
          <div key={field.key} style={{ marginBottom: 8 }}>
            <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 4 }}>
              {label}
            </Typography.Text>
            <Controller
              control={control}
              name={fieldPath}
              render={({ field: f }) =>
                field.secret ? (
                  <Input.Password
                    {...f}
                    style={{ width: '100%' }}
                    status={errorMessage ? 'error' : undefined}
                  />
                ) : (
                  <Input
                    {...f}
                    style={{ width: '100%' }}
                    status={errorMessage ? 'error' : undefined}
                  />
                )
              }
            />
            {errorMessage && (
              <Typography.Text type="danger" style={{ display: 'block', fontSize: '0.75rem' }}>
                {errorMessage}
              </Typography.Text>
            )}
          </div>
        );
      })}
    </>
  );
}

function ArrayFieldManager({ field, path }: { field: ConfigSchemaFieldSpec, path: string }) {
  const { control, formState: { errors } } = useFormContext();
  const { fields, append, remove } = useFieldArray({ control, name: path });

  return (
    <div>
      <Button
        size="small"
        type="dashed"
        style={{ marginBottom: 8 }}
        onClick={() => {
          const newItem =
            field.items && field.items.length > 0 && field.items[0].key !== ''
              ? initDraft(field.items)
              : '';
          append(newItem);
        }}
      >
        Add Item
      </Button>

      {fields.length === 0 ? (
        <Typography.Text type="secondary">No items.</Typography.Text>
      ) : null}

      {fields.map((item, idx) => {
        const itemError = path
          .split('.')
          .reduce((obj: any, key) => (obj ? obj[key] : undefined), errors)?.[idx];
        const errorMessage = itemError?.message as string | undefined;

        return (
          <Card key={item.id} size="small" style={{ marginBottom: 8 }}>
            <Space style={{ marginBottom: 4, width: '100%', justifyContent: 'space-between' }}>
              <Typography.Text strong>Item #{idx}</Typography.Text>
              <Button size="small" danger onClick={() => remove(idx)}>
                Remove
              </Button>
            </Space>

            {field.items && field.items.length > 0 && field.items[0].key !== '' ? (
              <BackendFormFields fields={field.items} path={`${path}.${idx}`} />
            ) : (
              <>
                <Controller
                  control={control}
                  name={`${path}.${idx}` as const}
                  render={({ field: f }) => (
                    <Input
                      {...f}
                      style={{ width: '100%' }}
                      status={errorMessage ? 'error' : undefined}
                    />
                  )}
                />
                {errorMessage && (
                  <Typography.Text
                    type="danger"
                    style={{ display: 'block', fontSize: '0.75rem', marginTop: 2 }}
                  >
                    {errorMessage}
                  </Typography.Text>
                )}
              </>
            )}
          </Card>
        );
      })}
    </div>
  );
}
