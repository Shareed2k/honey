import { Suspense, lazy, useCallback, useEffect, useState } from 'react';
import { Alert, Button, Input, Space, Typography } from 'antd';
import { apiGet, apiPut } from '../api/core';
import { fetchConfigSchema } from '../api/config';
import type { ConfigUISchema } from '../api/types/config';
import { ConfigBackendsSection } from '../ConfigBackendsSection';

const RawYamlEditor = lazy(() =>
  import('../RawYamlEditor').then((m) => ({ default: m.RawYamlEditor }))
);

export function ConfigTab() {
  const [yaml, setYaml] = useState('');
  const [yamlHasLintIssue, setYamlHasLintIssue] = useState(false);
  const [cfgErr, setCfgErr] = useState<string | null>(null);
  const [cfgPath, setCfgPath] = useState<string | null>(null);
  const [cfgSchema, setCfgSchema] = useState<ConfigUISchema | null>(null);
  const [cfgSchemaErr, setCfgSchemaErr] = useState<string | null>(null);

  const loadConfig = useCallback(async () => {
    setCfgErr(null);
    const r = await apiGet('/api/v1/config');
    if (!r.ok) {
      const j = await r.json().catch(() => ({}));
      setCfgErr((j as { error?: string }).error || r.statusText);
      return;
    }
    setCfgPath(r.headers.get('X-Config-Path'));
    setYaml(await r.text());
  }, []);

  const saveConfig = async () => {
    setCfgErr(null);
    const r = await apiPut('/api/v1/config', yaml);
    if (!r.ok) {
      const j = await r.json().catch(() => ({}));
      setCfgErr((j as { error?: string }).error || r.statusText);
    }
  };

  const loadConfigSchema = useCallback(async () => {
    setCfgSchemaErr(null);
    try {
      const schema = await fetchConfigSchema();
      setCfgSchema(schema.ui_schema);
    } catch (e) {
      setCfgSchema(null);
      setCfgSchemaErr(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => { void loadConfig(); }, [loadConfig]);
  useEffect(() => { void loadConfigSchema(); }, [loadConfigSchema]);

  return (
    <>
      {cfgErr && <Alert type="error" message={cfgErr} style={{ marginBottom: 12 }} />}
      {cfgSchemaErr && <Alert type="warning" message={`Schema warning: ${cfgSchemaErr}`} style={{ marginBottom: 12 }} />}
      {cfgPath && <Typography.Text type="secondary" style={{ display: 'block', marginBottom: 8 }}>Path: {cfgPath}</Typography.Text>}
      <Typography.Title level={5} style={{ marginBottom: 8 }}>Raw YAML</Typography.Title>
      <Suspense fallback={
        <Input.TextArea
          style={{ minHeight: 420, fontFamily: 'monospace', fontSize: '0.85rem' }}
          value={yaml}
          onChange={(e) => setYaml(e.target.value)}
        />
      }>
        <RawYamlEditor
          value={yaml}
          onChange={(next) => { setYaml(next); if (cfgErr) setCfgErr(null); }}
          schema={cfgSchema}
          backendError={cfgErr}
          onSave={() => { if (!yamlHasLintIssue) void saveConfig(); }}
          onLintStateChange={setYamlHasLintIssue}
        />
      </Suspense>
      <Space style={{ marginTop: 8 }}>
        <Button type="primary" disabled={yamlHasLintIssue} onClick={() => void saveConfig()}>
          Save YAML
        </Button>
        <Button onClick={() => void loadConfig()}>Reload</Button>
        <Button onClick={() => void loadConfigSchema()}>Reload schema</Button>
      </Space>
      {yamlHasLintIssue && (
        <Alert type="warning" message="Fix YAML diagnostics before saving." style={{ marginTop: 8 }} />
      )}
      <ConfigBackendsSection schema={cfgSchema} onSaved={() => void loadConfig()} />
    </>
  );
}
