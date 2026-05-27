# Ant Design Dark Mode Refactor Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Migrate the hostctl webui from vanilla CSS to Ant Design v5 dark mode, replacing the horizontal tab bar with a collapsible sidebar, and splitting the 2,543-line App.tsx into per-tab components.

**Architecture:** Install antd + @ant-design/icons, configure `ConfigProvider` with `theme.darkAlgorithm` in `main.tsx`, then rewrite App.tsx to a thin shell (~300 lines) using `Layout + Sider + Menu`. Each tab becomes its own component in `src/tabs/` (BackendsTab, ConfigTab, TunnelsTab, ApiDocsTab, FilesTab, SearchTab). AppsTab and RecipesTab already have dedicated folders; migrate their internals. Shared state (records, sshUser, backends, terminals) stays in App.tsx and is passed as props.

**Tech Stack:** React 18, TypeScript, Vite, Ant Design v5, @ant-design/icons, existing: CodeMirror, xterm.js, @xyflow/react, Swagger UI (untouched)

**Design spec:** `docs/superpowers/specs/2026-05-28-antd-dark-mode-refactor-design.md`

---

## File Map

**Modified:**
- `webui/package.json` — add antd, @ant-design/icons
- `webui/src/main.tsx` — wrap App with ConfigProvider dark theme
- `webui/src/App.tsx` — thin shell: Layout + Sider + Menu + shared state + modal state
- `webui/src/app.css` — keep only terminal/codemirror/xyflow/swagger overrides (~100 lines)
- `webui/src/AppsTab/index.tsx` — swap native elements with antd components
- `webui/src/AppsTab/apps-tab.css` — trim to editor-specific overrides
- `webui/src/RecipesTab/index.tsx` + step files — swap native elements with antd components
- `webui/src/RecipesTab/recipes-tab.css` — trim to flow-specific overrides
- `webui/vitest.config.ts` — add setupFiles for matchMedia mock (required by antd)

**Created:**
- `webui/src/setupTests.ts` — `window.matchMedia` mock for vitest
- `webui/src/tabs/BackendsTab.tsx` — backends list Table
- `webui/src/tabs/BackendsTab.test.tsx` — smoke test
- `webui/src/tabs/ConfigTab.tsx` — YAML editor + schema actions
- `webui/src/tabs/ConfigTab.test.tsx` — smoke test
- `webui/src/tabs/ApiDocsTab.tsx` — Suspense wrapper for OpenApiDocsTab
- `webui/src/tabs/ApiDocsTab.test.tsx` — smoke test
- `webui/src/tabs/TunnelsTab.tsx` — tunnels list Table + log Modal
- `webui/src/tabs/TunnelsTab.test.tsx` — smoke test
- `webui/src/tabs/FilesTab.tsx` — agent file transfer form
- `webui/src/tabs/FilesTab.test.tsx` — smoke test
- `webui/src/tabs/SearchTab.tsx` — host search, exec, upload, host detail
- `webui/src/tabs/SearchTab.test.tsx` — smoke test

---

## Task 1: Install dependencies + test setup

**Files:**
- Modify: `webui/package.json`
- Modify: `webui/vitest.config.ts`
- Create: `webui/src/setupTests.ts`

- [ ] **Step 1: Install antd and icons**

Run from `webui/`:
```bash
npm install antd @ant-design/icons
```
Expected: both packages added to `node_modules/`, `package-lock.json` updated.

- [ ] **Step 2: Create matchMedia mock for vitest**

Ant Design uses `window.matchMedia` which jsdom doesn't implement. Create `webui/src/setupTests.ts`:
```ts
Object.defineProperty(window, 'matchMedia', {
  writable: true,
  value: (query: string) => ({
    matches: false,
    media: query,
    onchange: null,
    addListener: () => {},
    removeListener: () => {},
    addEventListener: () => {},
    removeEventListener: () => {},
    dispatchEvent: () => false,
  }),
});
```

- [ ] **Step 3: Register setup file in vitest.config.ts**

```ts
// webui/vitest.config.ts
import { defineConfig } from 'vitest/config';
import react from '@vitejs/plugin-react';

export default defineConfig({
  plugins: [react()],
  test: {
    environment: 'jsdom',
    include: ['src/**/*.test.{ts,tsx}'],
    setupFiles: ['src/setupTests.ts'],
  },
});
```

- [ ] **Step 4: Verify existing tests still pass**

Run from `webui/`:
```bash
npm test
```
Expected: all 3 existing tests pass (recipeStepUtils, addStep, StepRetryEditor).

- [ ] **Step 5: Commit**
```bash
git add webui/package.json webui/package-lock.json webui/vitest.config.ts webui/src/setupTests.ts
git commit -m "chore(webui): install antd + icons, add matchMedia mock for vitest"
```

---

## Task 2: Configure Ant Design dark theme in main.tsx

**Files:**
- Modify: `webui/src/main.tsx`

- [ ] **Step 1: Update main.tsx with ConfigProvider**

Replace current `main.tsx` content with:
```tsx
import React from 'react';
import ReactDOM from 'react-dom/client';
import { ConfigProvider, theme } from 'antd';
import { App } from './App';
import '@xterm/xterm/css/xterm.css';
import './app.css';

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <ConfigProvider
      theme={{
        algorithm: theme.darkAlgorithm,
        token: {
          colorBgBase: '#0f1115',
          colorPrimary: '#3d6fb8',
          colorBgContainer: '#141922',
          colorBorderSecondary: '#2a3140',
          borderRadius: 4,
          fontFamily: 'system-ui, -apple-system, Segoe UI, Roboto, sans-serif',
        },
      }}
    >
      <App />
    </ConfigProvider>
  </React.StrictMode>,
);
```

- [ ] **Step 2: Start dev server and verify it loads**

Run from `webui/`:
```bash
npm run dev
```
Open http://localhost:5173. Expected: app loads, dark background visible, no console errors.

- [ ] **Step 3: Commit**
```bash
git add webui/src/main.tsx
git commit -m "feat(webui): configure Ant Design v5 dark theme via ConfigProvider"
```

---

## Task 3: Extract BackendsTab

**Files:**
- Create: `webui/src/tabs/BackendsTab.tsx`
- Create: `webui/src/tabs/BackendsTab.test.tsx`
- Modify: `webui/src/App.tsx` — remove backends section, import BackendsTab

**Context:** The backends tab (lines ~2066–2088 in App.tsx) shows a simple table of `{kind, name, hint}` rows. `backends` and `backErr` are loaded in App.tsx via `loadBackends()` when the tab is active. These can stay in App.tsx since they're also used by SearchTab (filter dropdowns) and FilesTab.

- [ ] **Step 1: Write smoke test**

Create `webui/src/tabs/BackendsTab.test.tsx`:
```tsx
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, it, expect } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { BackendsTab } from './BackendsTab';

const wrap = (ui: React.ReactElement) => (
  <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>{ui}</ConfigProvider>
);

afterEach(cleanup);

describe('BackendsTab', () => {
  it('renders without crashing with empty backends', () => {
    render(wrap(<BackendsTab backends={[]} error={null} />));
    expect(screen.getByRole('table')).toBeTruthy();
  });

  it('shows error when provided', () => {
    render(wrap(<BackendsTab backends={[]} error="connection failed" />));
    expect(screen.getByText('connection failed')).toBeTruthy();
  });

  it('renders backend rows', () => {
    render(wrap(<BackendsTab backends={[{ kind: 's3', name: 'main', hint: 'us-east-1' }]} error={null} />));
    expect(screen.getByText('s3')).toBeTruthy();
    expect(screen.getByText('main')).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
npm test -- BackendsTab
```
Expected: FAIL — `Cannot find module './BackendsTab'`

- [ ] **Step 3: Create BackendsTab component**

Create `webui/src/tabs/BackendsTab.tsx`:
```tsx
import { Alert, Table } from 'antd';
import type { ColumnsType } from 'antd/es/table';

type BackendRow = { kind: string; name: string; hint: string };

interface Props {
  backends: BackendRow[];
  error: string | null;
}

const columns: ColumnsType<BackendRow> = [
  { title: 'Kind', dataIndex: 'kind', key: 'kind', width: 120 },
  { title: 'Name', dataIndex: 'name', key: 'name', width: 200 },
  { title: 'Hint', dataIndex: 'hint', key: 'hint' },
];

export function BackendsTab({ backends, error }: Props) {
  return (
    <>
      {error && <Alert type="error" message={error} style={{ marginBottom: 12 }} />}
      <Table
        dataSource={backends}
        columns={columns}
        rowKey={(r) => `${r.kind}-${r.name}`}
        size="small"
        pagination={false}
      />
    </>
  );
}
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
npm test -- BackendsTab
```
Expected: 3 tests PASS.

- [ ] **Step 5: Wire BackendsTab into App.tsx**

In `App.tsx`, add import at top:
```tsx
import { BackendsTab } from './tabs/BackendsTab';
```

Find the `{tab === 'backends' ? (` block (~line 2066) and replace it:
```tsx
{tab === 'backends' ? (
  <BackendsTab backends={backends} error={backErr} />
) : null}
```

- [ ] **Step 6: Verify in browser**

With dev server running, click Backends tab. Expected: antd Table renders with same data as before.

- [ ] **Step 7: Commit**
```bash
git add webui/src/tabs/BackendsTab.tsx webui/src/tabs/BackendsTab.test.tsx webui/src/App.tsx
git commit -m "feat(webui): extract BackendsTab with Ant Design Table"
```

---

## Task 4: Extract ConfigTab

**Files:**
- Create: `webui/src/tabs/ConfigTab.tsx`
- Create: `webui/src/tabs/ConfigTab.test.tsx`
- Modify: `webui/src/App.tsx` — remove config section, import ConfigTab

**Context:** Config tab (lines ~2090–2138) owns: `yaml`, `yamlHasLintIssue`, `cfgErr`, `cfgPath`, `cfgSchema`, `cfgSchemaErr`, plus `loadConfig`, `saveConfig`, `loadConfigSchema`. These are only used by the config tab and can fully move. Wraps `RawYamlEditor` (CodeMirror — keep as-is) and `ConfigBackendsSection`.

- [ ] **Step 1: Write smoke test**

Create `webui/src/tabs/ConfigTab.test.tsx`:
```tsx
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, it, vi } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { ConfigTab } from './ConfigTab';

vi.mock('../RawYamlEditor', () => ({ RawYamlEditor: () => <div data-testid="yaml-editor" /> }));
vi.mock('../ConfigBackendsSection', () => ({ ConfigBackendsSection: () => <div /> }));

afterEach(cleanup);

describe('ConfigTab', () => {
  it('renders without crashing', () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <ConfigTab />
      </ConfigProvider>
    );
    expect(screen.getByTestId('yaml-editor')).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
npm test -- ConfigTab
```
Expected: FAIL — `Cannot find module './ConfigTab'`

- [ ] **Step 3: Create ConfigTab component**

Create `webui/src/tabs/ConfigTab.tsx`:
```tsx
import { Suspense, lazy, useCallback, useEffect, useState } from 'react';
import { Alert, Button, Space, Typography } from 'antd';
import { apiGet, apiPut, fetchConfigSchema } from '../api';
import type { ConfigUISchema } from '../api';
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
        <textarea
          style={{ width: '100%', minHeight: '420px', fontFamily: 'monospace', fontSize: '0.85rem' }}
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
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
npm test -- ConfigTab
```
Expected: 1 test PASS.

- [ ] **Step 5: Wire ConfigTab into App.tsx**

Add import:
```tsx
import { ConfigTab } from './tabs/ConfigTab';
```

Replace the `{tab === 'config' ? (` block (~line 2090) with:
```tsx
{tab === 'config' ? <ConfigTab /> : null}
```

Remove the now-unused state variables from App.tsx: `yaml`, `yamlHasLintIssue`, `cfgErr`, `cfgPath`, `cfgSchema`, `cfgSchemaErr`, and the `loadConfig`, `saveConfig`, `loadConfigSchema` functions. Also remove the `useEffect` calls that invoked them.

- [ ] **Step 6: Verify in browser**

Click Config tab. Expected: YAML editor loads, Save/Reload buttons work.

- [ ] **Step 7: Commit**
```bash
git add webui/src/tabs/ConfigTab.tsx webui/src/tabs/ConfigTab.test.tsx webui/src/App.tsx
git commit -m "feat(webui): extract ConfigTab with Ant Design buttons and alerts"
```

---

## Task 5: Extract ApiDocsTab

**Files:**
- Create: `webui/src/tabs/ApiDocsTab.tsx`
- Create: `webui/src/tabs/ApiDocsTab.test.tsx`
- Modify: `webui/src/App.tsx` — remove api-docs section, import ApiDocsTab

- [ ] **Step 1: Write smoke test**

Create `webui/src/tabs/ApiDocsTab.test.tsx`:
```tsx
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, it, vi } from 'vitest';
import { ApiDocsTab } from './ApiDocsTab';

vi.mock('../OpenApiDocsTab', () => ({ OpenApiDocsTab: () => <div data-testid="swagger" /> }));

afterEach(cleanup);

describe('ApiDocsTab', () => {
  it('renders without crashing', async () => {
    render(<ApiDocsTab />);
    expect(await screen.findByTestId('swagger')).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
npm test -- ApiDocsTab
```
Expected: FAIL — `Cannot find module './ApiDocsTab'`

- [ ] **Step 3: Create ApiDocsTab component**

Create `webui/src/tabs/ApiDocsTab.tsx`:
```tsx
import { Suspense, lazy } from 'react';
import { Spin } from 'antd';

const OpenApiDocsTab = lazy(() =>
  import('../OpenApiDocsTab').then((m) => ({ default: m.OpenApiDocsTab }))
);

export function ApiDocsTab() {
  return (
    <Suspense fallback={<Spin tip="Loading API explorer…" style={{ display: 'block', marginTop: 32 }} />}>
      <OpenApiDocsTab />
    </Suspense>
  );
}
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
npm test -- ApiDocsTab
```
Expected: 1 test PASS.

- [ ] **Step 5: Wire ApiDocsTab into App.tsx**

Add import:
```tsx
import { ApiDocsTab } from './tabs/ApiDocsTab';
```

Replace the `{tab === 'api-docs' ? (` block with:
```tsx
{tab === 'api-docs' ? <ApiDocsTab /> : null}
```

Remove the `OpenApiDocsTab` lazy import from App.tsx (it's now inside ApiDocsTab.tsx).

- [ ] **Step 6: Commit**
```bash
git add webui/src/tabs/ApiDocsTab.tsx webui/src/tabs/ApiDocsTab.test.tsx webui/src/App.tsx
git commit -m "feat(webui): extract ApiDocsTab"
```

---

## Task 6: Extract TunnelsTab

**Files:**
- Create: `webui/src/tabs/TunnelsTab.tsx`
- Create: `webui/src/tabs/TunnelsTab.test.tsx`
- Modify: `webui/src/App.tsx` — remove tunnels list section, import TunnelsTab

**Context:** TunnelsTab shows the list of active tunnels (lines ~2159–2217). State `tunnelsList`, `tunnelsListErr`, `tunnelLogOpen`, `tunnelLogContent`, `tunnelLogErr` all move to TunnelsTab. The tunnel *creation* form (triggered from SearchTab host actions) stays in App.tsx as it's tied to the search workflow. `loadTunnels` is called in TunnelsTab on mount and via Refresh button. The tunnel log Modal gets converted to antd Modal.

- [ ] **Step 1: Write smoke test**

Create `webui/src/tabs/TunnelsTab.test.tsx`:
```tsx
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, it } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { TunnelsTab } from './TunnelsTab';

afterEach(cleanup);

describe('TunnelsTab', () => {
  it('renders empty state', () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <TunnelsTab onNavigateToSearch={() => {}} />
      </ConfigProvider>
    );
    expect(screen.getByText(/No active tunnels/i)).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
npm test -- TunnelsTab
```
Expected: FAIL — `Cannot find module './TunnelsTab'`

- [ ] **Step 3: Create TunnelsTab component**

Create `webui/src/tabs/TunnelsTab.tsx`:
```tsx
import { useCallback, useEffect, useState } from 'react';
import { Alert, Button, Modal, Space, Table, Typography } from 'antd';
import type { ColumnsType } from 'antd/es/table';
import { fetchTunnels, fetchTunnelLogs, stopTunnel } from '../api';
import type { TunnelInfo } from '../api';

interface Props {
  onNavigateToSearch: () => void;
}

export function TunnelsTab({ onNavigateToSearch }: Props) {
  const [tunnelsList, setTunnelsList] = useState<TunnelInfo[]>([]);
  const [tunnelsListErr, setTunnelsListErr] = useState<string | null>(null);
  const [tunnelLogOpen, setTunnelLogOpen] = useState<string | null>(null);
  const [tunnelLogContent, setTunnelLogContent] = useState('');
  const [tunnelLogErr, setTunnelLogErr] = useState<string | null>(null);

  const loadTunnels = useCallback(async () => {
    setTunnelsListErr(null);
    try {
      setTunnelsList(await fetchTunnels());
    } catch (e) {
      setTunnelsListErr(e instanceof Error ? e.message : String(e));
    }
  }, []);

  useEffect(() => { void loadTunnels(); }, [loadTunnels]);

  useEffect(() => {
    if (!tunnelLogOpen) { setTunnelLogContent(''); setTunnelLogErr(null); return; }
    fetchTunnelLogs(tunnelLogOpen)
      .then(setTunnelLogContent)
      .catch((e) => setTunnelLogErr(e instanceof Error ? e.message : String(e)));
  }, [tunnelLogOpen]);

  const columns: ColumnsType<TunnelInfo> = [
    { title: 'Host', dataIndex: 'host_name', key: 'host_name' },
    { title: 'Mapping (Local:Remote)', dataIndex: 'mapping', key: 'mapping', render: (v) => <code>{v}</code> },
    { title: 'Status/Started', dataIndex: 'started_at', key: 'started_at' },
    {
      title: 'Actions',
      key: 'actions',
      align: 'right',
      render: (_, t) => (
        <Space>
          <Button size="small" onClick={() => setTunnelLogOpen(t.id)}>Logs</Button>
          <Button size="small" danger onClick={async () => {
            try { await stopTunnel(t.id); await loadTunnels(); }
            catch (e) { setTunnelsListErr(e instanceof Error ? e.message : String(e)); }
          }}>Stop</Button>
        </Space>
      ),
    },
  ];

  return (
    <>
      {tunnelsListErr && <Alert type="error" message={tunnelsListErr} style={{ marginBottom: 12 }} />}
      <Space style={{ marginBottom: 12 }}>
        <Button onClick={() => void loadTunnels()}>Refresh</Button>
      </Space>
      {tunnelsList.length === 0 ? (
        <Typography.Text type="secondary">
          No active tunnels. You can start one from the{' '}
          <Button type="link" style={{ padding: 0 }} onClick={onNavigateToSearch}>Search tab</Button>.
        </Typography.Text>
      ) : (
        <Table dataSource={tunnelsList} columns={columns} rowKey="id" size="small" pagination={false} />
      )}
      <Modal
        open={!!tunnelLogOpen}
        title="Tunnel Logs"
        footer={<Button onClick={() => setTunnelLogOpen(null)}>Close</Button>}
        onCancel={() => setTunnelLogOpen(null)}
        width="min(800px, 94vw)"
        styles={{ body: { maxHeight: '60vh', overflow: 'auto' } }}
      >
        {tunnelLogErr && <Alert type="error" message={tunnelLogErr} style={{ marginBottom: 8 }} />}
        <pre style={{ margin: 0, fontSize: '0.78rem', whiteSpace: 'pre-wrap', wordBreak: 'break-word' }}>
          {tunnelLogContent || 'Loading...'}
        </pre>
      </Modal>
    </>
  );
}
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
npm test -- TunnelsTab
```
Expected: 1 test PASS.

- [ ] **Step 5: Wire TunnelsTab into App.tsx**

Add import:
```tsx
import { TunnelsTab } from './tabs/TunnelsTab';
```

Replace the `{tab === 'tunnels' ? (` block with:
```tsx
{tab === 'tunnels' ? (
  <TunnelsTab onNavigateToSearch={() => setTab('search')} />
) : null}
```

Remove from App.tsx: `tunnelsList`, `tunnelsListErr`, `tunnelLogOpen`, `tunnelLogContent`, `tunnelLogErr` state, `loadTunnels` callback, and the `useEffect` that invoked `loadTunnels`. Also remove the `tunnelLogOpen` Modal JSX from the bottom of the return.

- [ ] **Step 6: Verify in browser**

Click Tunnels tab. Expected: table renders, Refresh button works, log modal opens.

- [ ] **Step 7: Commit**
```bash
git add webui/src/tabs/TunnelsTab.tsx webui/src/tabs/TunnelsTab.test.tsx webui/src/App.tsx
git commit -m "feat(webui): extract TunnelsTab with Ant Design Table and Modal"
```

---

## Task 7: Extract FilesTab

**Files:**
- Create: `webui/src/tabs/FilesTab.tsx`
- Create: `webui/src/tabs/FilesTab.test.tsx`
- Modify: `webui/src/App.tsx` — remove files section, import FilesTab

**Context:** The Files tab (lines ~1896–2064) is the agent transfer UI. It has its own state: `transferSourceHostKey`, `transferDestHostKey`, `transferSourcePath`, `transferDestPath`, `transferCloud`, `transferBackendRefValue`, `transferKeepObject`, `transferMaxRetries`, `transferBusy`, `transferErr`, `transferEvents`, `transferAbortRef`. It also reads `backends` (from App.tsx) and `records` (the host search results). Pass these as props.

- [ ] **Step 1: Write smoke test**

Create `webui/src/tabs/FilesTab.test.tsx`:
```tsx
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, it } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { FilesTab } from './FilesTab';

afterEach(cleanup);

describe('FilesTab', () => {
  it('renders without crashing', () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <FilesTab records={[]} backends={[]} />
      </ConfigProvider>
    );
    expect(screen.getByText(/Source host/i)).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
npm test -- FilesTab
```
Expected: FAIL — `Cannot find module './FilesTab'`

- [ ] **Step 3: Create FilesTab component**

Move the full Files tab JSX from App.tsx into `webui/src/tabs/FilesTab.tsx`. Replace native `<select>`, `<input>`, `<button>` with antd `Select`, `Input`, `Button`. Import all required state, types, and API calls (they currently live in App.tsx).

Create `webui/src/tabs/FilesTab.tsx`:
```tsx
import { useEffect, useMemo, useRef, useState } from 'react';
import { Alert, Button, Input, Select, Space, Typography } from 'antd';
import {
  apiGet, startAgentTransferStream,
  type AgentTransferBackendRef, type AgentTransferCloud,
  type AgentTransferEvent,
} from '../api';
import type { HostRecord } from '../HostPicker';
import { recordKey } from '../HostPicker';

type BackendRow = { kind: string; name: string; hint: string };

interface Props {
  records: HostRecord[];
  backends: BackendRow[];
}

export function FilesTab({ records, backends }: Props) {
  const [transferSourceHostKey, setTransferSourceHostKey] = useState('');
  const [transferDestHostKey, setTransferDestHostKey] = useState('');
  const [transferSourcePath, setTransferSourcePath] = useState('/tmp/source.bin');
  const [transferDestPath, setTransferDestPath] = useState('/tmp/dest.bin');
  const [transferCloud, setTransferCloud] = useState<AgentTransferCloud>({
    provider: 's3', bucket: '', prefix: 'honey-transfer', object: '', region: '', endpoint: '',
  });
  const [transferBackendRefValue, setTransferBackendRefValue] = useState('');
  const [transferKeepObject, setTransferKeepObject] = useState(false);
  const [transferMaxRetries, setTransferMaxRetries] = useState(2);
  const [transferBusy, setTransferBusy] = useState(false);
  const [transferErr, setTransferErr] = useState<string | null>(null);
  const [transferEvents, setTransferEvents] = useState<AgentTransferEvent[]>([]);
  const transferAbortRef = useRef<AbortController | null>(null);

  useEffect(() => () => { transferAbortRef.current?.abort(); }, []);

  const transferHostOptions = useMemo(() => records.filter((r) => !!r.primary_ip.trim()), [records]);
  const transferBackendKind = transferCloud.provider === 'googlecloudstorage' ? 'gcp' : 'aws';
  const transferBackendOptions = useMemo(
    () => backends.filter((b) => b.kind.toLowerCase() === transferBackendKind && b.name.trim() !== ''),
    [backends, transferBackendKind],
  );

  useEffect(() => {
    if (transferHostOptions.length > 0 && !transferSourceHostKey) {
      setTransferSourceHostKey(recordKey(transferHostOptions[0]));
    }
    if (transferHostOptions.length > 1 && !transferDestHostKey) {
      setTransferDestHostKey(recordKey(transferHostOptions[1]));
    }
  }, [transferHostOptions, transferSourceHostKey, transferDestHostKey]);

  const runTransfer = async () => {
    setTransferBusy(true);
    setTransferErr(null);
    setTransferEvents([]);
    transferAbortRef.current = new AbortController();
    const backendRef: AgentTransferBackendRef = transferBackendRefValue
      ? { ref: transferBackendRefValue }
      : { kind: transferBackendKind, name: '' };
    try {
      const srcRec = transferHostOptions.find((r) => recordKey(r) === transferSourceHostKey);
      const dstRec = transferHostOptions.find((r) => recordKey(r) === transferDestHostKey);
      if (!srcRec || !dstRec) throw new Error('Select source and destination hosts.');
      await startAgentTransferStream({
        source: { record: srcRec, path: transferSourcePath },
        dest: { record: dstRec, path: transferDestPath },
        cloud: transferCloud,
        backend_ref: backendRef,
        keep_object: transferKeepObject,
        max_retries: transferMaxRetries,
      }, {
        onEvent: (ev) => setTransferEvents((p) => [...p, ev]),
        signal: transferAbortRef.current.signal,
      });
    } catch (e) {
      if ((e as Error).name !== 'AbortError') {
        setTransferErr(e instanceof Error ? e.message : String(e));
      }
    } finally {
      setTransferBusy(false);
    }
  };

  const hostSelectOptions = transferHostOptions.map((r) => ({
    value: recordKey(r),
    label: `${r.name} (${r.primary_ip})`,
  }));

  const backendSelectOptions = transferBackendOptions.map((b) => ({
    value: `${b.kind}:${b.name}`.toLowerCase(),
    label: `${b.kind}: ${b.name}`,
  }));

  return (
    <div style={{ display: 'grid', gap: '0.6rem', maxWidth: 720 }}>
      <Typography.Text type="secondary" style={{ fontSize: '0.85rem' }}>
        Transfer path: source host uploads to cloud object, destination host downloads from cloud using ephemeral agent over SSH control-plane.
        Cloud credentials are resolved only on Honey, and remotes receive encrypted short-lived credential envelopes.
      </Typography.Text>
      <div style={{ display: 'grid', gridTemplateColumns: 'repeat(2, minmax(260px, 1fr))', gap: '0.55rem' }}>
        <div>
          <Typography.Text style={{ fontSize: '0.85rem' }}>Source host</Typography.Text>
          <Select
            style={{ width: '100%', marginTop: 4 }}
            value={transferSourceHostKey || undefined}
            onChange={setTransferSourceHostKey}
            options={hostSelectOptions}
            placeholder="Select source host"
          />
        </div>
        <div>
          <Typography.Text style={{ fontSize: '0.85rem' }}>Destination host</Typography.Text>
          <Select
            style={{ width: '100%', marginTop: 4 }}
            value={transferDestHostKey || undefined}
            onChange={setTransferDestHostKey}
            options={hostSelectOptions}
            placeholder="Select destination host"
          />
        </div>
        <div>
          <Typography.Text style={{ fontSize: '0.85rem' }}>Source path</Typography.Text>
          <Input
            style={{ marginTop: 4, fontFamily: 'monospace' }}
            value={transferSourcePath}
            onChange={(e) => setTransferSourcePath(e.target.value)}
          />
        </div>
        <div>
          <Typography.Text style={{ fontSize: '0.85rem' }}>Destination path</Typography.Text>
          <Input
            style={{ marginTop: 4, fontFamily: 'monospace' }}
            value={transferDestPath}
            onChange={(e) => setTransferDestPath(e.target.value)}
          />
        </div>
        <div>
          <Typography.Text style={{ fontSize: '0.85rem' }}>Cloud provider</Typography.Text>
          <Select
            style={{ width: '100%', marginTop: 4 }}
            value={transferCloud.provider}
            onChange={(v) => setTransferCloud((p) => ({ ...p, provider: v as AgentTransferCloud['provider'] }))}
            options={[
              { value: 's3', label: 'S3' },
              { value: 'googlecloudstorage', label: 'Google Cloud Storage' },
            ]}
          />
        </div>
        <div>
          <Typography.Text style={{ fontSize: '0.85rem' }}>Backend</Typography.Text>
          <Select
            style={{ width: '100%', marginTop: 4 }}
            value={transferBackendRefValue || undefined}
            onChange={setTransferBackendRefValue}
            options={backendSelectOptions}
            placeholder="Select backend"
            allowClear
          />
        </div>
        <div>
          <Typography.Text style={{ fontSize: '0.85rem' }}>Bucket</Typography.Text>
          <Input style={{ marginTop: 4 }} value={transferCloud.bucket} onChange={(e) => setTransferCloud((p) => ({ ...p, bucket: e.target.value }))} />
        </div>
        <div>
          <Typography.Text style={{ fontSize: '0.85rem' }}>Object prefix</Typography.Text>
          <Input style={{ marginTop: 4 }} value={transferCloud.prefix} onChange={(e) => setTransferCloud((p) => ({ ...p, prefix: e.target.value }))} />
        </div>
      </div>
      {transferErr && <Alert type="error" message={transferErr} />}
      <Space>
        <Button type="primary" loading={transferBusy} onClick={() => void runTransfer()}>
          Transfer
        </Button>
        {transferBusy && (
          <Button onClick={() => transferAbortRef.current?.abort()}>Cancel</Button>
        )}
      </Space>
      {transferEvents.length > 0 && (
        <pre style={{ margin: 0, fontSize: '0.78rem', whiteSpace: 'pre-wrap', background: '#141922', border: '1px solid #2a3140', borderRadius: 4, padding: '0.65rem', maxHeight: '300px', overflow: 'auto' }}>
          {transferEvents.map((e, i) => `[${e.phase}] ${JSON.stringify(e)}`).join('\n')}
        </pre>
      )}
    </div>
  );
}
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
npm test -- FilesTab
```
Expected: 1 test PASS.

- [ ] **Step 5: Wire FilesTab into App.tsx**

Add import:
```tsx
import { FilesTab } from './tabs/FilesTab';
```

Replace the `{tab === 'files' ? (` block with:
```tsx
{tab === 'files' ? (
  <FilesTab records={records} backends={backends} />
) : null}
```

Remove from App.tsx: all `transfer*` state variables, `transferAbortRef`, the `useEffect` for abort cleanup, `transferHostOptions` and `transferBackendOptions` memos, and the `runTransfer` function (now inside FilesTab).

- [ ] **Step 6: Verify in browser**

Click Files tab. Expected: form renders with host/backend selects populated from search results.

- [ ] **Step 7: Commit**
```bash
git add webui/src/tabs/FilesTab.tsx webui/src/tabs/FilesTab.test.tsx webui/src/App.tsx
git commit -m "feat(webui): extract FilesTab with Ant Design form controls"
```

---

## Task 8: Extract SearchTab

**Files:**
- Create: `webui/src/tabs/SearchTab.tsx`
- Create: `webui/src/tabs/SearchTab.test.tsx`
- Modify: `webui/src/App.tsx` — remove search section, import SearchTab

**Context:** SearchTab is the largest section (~600 lines of JSX in App.tsx). It owns: host search filters, exec command, exec results, upload modal, host detail panel. It reads and writes `records`, `selectedKeys`, and reads `backends`, `providerIds`, `sshUser`, `meta`. SearchTab also triggers the tunnel creation modal (which stays in App.tsx since tunnel creation navigates to TunnelsTab) and replay modal — pass callbacks for both. The `UploadProgressBar` component and `UploadXferState` type move to SearchTab.

- [ ] **Step 1: Write smoke test**

Create `webui/src/tabs/SearchTab.test.tsx`:
```tsx
import { cleanup, render, screen } from '@testing-library/react';
import { afterEach, describe, it, vi } from 'vitest';
import { ConfigProvider, theme } from 'antd';
import { SearchTab } from './SearchTab';

vi.mock('../HostPicker', () => ({
  HostPicker: () => <div data-testid="host-picker" />,
  recordKey: (r: { provider: string; name: string }) => `${r.provider}:${r.name}`,
  recordHaystack: () => '',
}));

afterEach(cleanup);

describe('SearchTab', () => {
  it('renders without crashing', () => {
    render(
      <ConfigProvider theme={{ algorithm: theme.darkAlgorithm }}>
        <SearchTab
          records={[]}
          selectedKeys={{}}
          onRecordsChange={() => {}}
          onSelectedKeysChange={() => {}}
          backends={[]}
          providerIds={[]}
          sshUser=""
          onSshUserChange={() => {}}
          meta={null}
          onOpenTunnel={() => {}}
          onOpenReplay={() => {}}
          onOpenReplayAll={() => {}}
          onOpenTerminal={() => {}}
        />
      </ConfigProvider>
    );
    expect(screen.getByTestId('host-picker')).toBeTruthy();
  });
});
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
npm test -- SearchTab
```
Expected: FAIL — `Cannot find module './SearchTab'`

- [ ] **Step 3: Create SearchTab component**

Create `webui/src/tabs/SearchTab.tsx` by moving the search section JSX (lines ~1442–1894 of App.tsx) plus all state/handlers that are search-specific. Move `UploadXferState` type, `UploadProgressBar` component, `formatUploadBytes` function, `cloneHostRecord`, `recordIndex`, helper booleans (`canProxmoxQemuVnc`, `canTrueNASAPIShell`, `canPortForwardTunnel`, `truenasAPIShellLabel`) into this file.

The props interface for SearchTab:
```tsx
interface Props {
  records: HostRecord[];
  selectedKeys: Record<string, boolean>;
  onRecordsChange: (records: HostRecord[]) => void;
  onSelectedKeysChange: (keys: Record<string, boolean>) => void;
  backends: BackendRow[];
  providerIds: string[];
  sshUser: string;
  onSshUserChange: (v: string) => void;
  meta: { version: string; config_path: string; session_recording_available?: boolean; terminal_assist_available?: boolean; } | null;
  onOpenTunnel: (rec: HostRecord) => void;
  onOpenReplay: (rec: HostRecord) => void;
  onOpenReplayAll: () => void;
  onOpenTerminal: (cfg: TerminalSessionConfig) => void;
}
```

State that moves into SearchTab: `name`, `resultFilter`, `searchErr`, `searching`, `execCommand`, `execBusy`, `execErr`, `execResults`, `execCurrentPage`, `hostDetailRecord`, `visibleRecords`, `providerMenuOpen`, `backendMenuOpen`, `providerMenuRef`, `backendMenuRef`, `uploadModalOpen`, `uploadTargetIdx`, `uploadRemote`, `uploadXfer`, `uploadStatus`, `uploadStatusIsError`, `fileInputRef`, `recordWebSession`.

**State that stays in App.tsx (shared with AppsTab):** `selectedProviders`, `selectedBackends`. Pass to SearchTab as controlled props (`selectedProviders`, `onSelectedProvidersChange`, `selectedBackends`, `onSelectedBackendsChange`) so the filter dropdowns can update them while AppsTab still reads them.

Replace vanilla HTML in the search section:
- `<button className="primary">` → `<Button type="primary">`
- `<button>` → `<Button>`
- `<input>` → `<Input>`
- `<select multiple>` dropdown panels → `<Select mode="multiple">`
- Upload `<Modal>` backdrop div → `<Modal>` from antd
- Host detail panel border div → `<Card>`
- Status text (online/offline/error) → `<Tag color="green">` / `<Tag color="red">`
- Loading spinner → `<Spin>`
- Exec results `<table>` → `<Table>`

The upload Modal using antd:
```tsx
<Modal
  open={uploadModalOpen}
  title="SFTP upload"
  footer={<Button onClick={closeUploadModal}>Close</Button>}
  onCancel={closeUploadModal}
  width="min(480px, 94vw)"
>
  {/* form contents */}
</Modal>
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
npm test -- SearchTab
```
Expected: 1 test PASS.

- [ ] **Step 5: Wire SearchTab into App.tsx**

Add import:
```tsx
import { SearchTab } from './tabs/SearchTab';
```

Replace the `{tab === 'search' ? (` block with:
```tsx
{tab === 'search' ? (
  <SearchTab
    records={records}
    selectedKeys={selectedKeys}
    onRecordsChange={setRecords}
    onSelectedKeysChange={setSelectedKeys}
    backends={backends}
    providerIds={providerIds}
    sshUser={sshUser}
    onSshUserChange={setSshUser}
    meta={meta}
    onOpenTunnel={(rec) => {
      setTunnelOpen({ record: rec });
      setTunnelLocalPort('');
      setTunnelRemotePort('');
      setTunnelRemoteHost('');
      setTunnelErr(null);
    }}
    onOpenReplay={openReplayModal}
    onOpenReplayAll={openReplayAllRecordings}
    onOpenTerminal={openTerminal}
  />
) : null}
```

Remove from App.tsx the now-moved state, functions, and helpers. App.tsx should now only retain:
- `tab`, `setTab` (navigation)
- `records`, `setRecords` (shared with RecipesTab/FilesTab)
- `selectedKeys`, `setSelectedKeys` (shared with RecipesTab)
- `sshUser`, `setSshUser` (shared with AppsTab, tunnel creation)
- `backends`, `backErr` (shared with FilesTab, BackendsTab)
- `providerIds` (shared with SearchTab)
- `meta`, `tokenMsg` (global)
- `terminals`, `activeTermId`, `isTerminalModalOpen` (global terminal modal)
- `tunnelOpen`, tunnel creation form state + `submitTunnel`
- `replayRecord`, `replayItems`, `replayListMeta`, `replayErr` + modal
- `recipePreview`, `recipeAssistOpen` + modal state

- [ ] **Step 6: Run all tests**

```bash
npm test
```
Expected: all tests pass.

- [ ] **Step 7: Verify in browser**

Click Search tab. Search for hosts. Run a command. Verify exec results table renders. Open upload modal. Verify terminal modal launches from host row.

- [ ] **Step 8: Commit**
```bash
git add webui/src/tabs/SearchTab.tsx webui/src/tabs/SearchTab.test.tsx webui/src/App.tsx
git commit -m "feat(webui): extract SearchTab with Ant Design components"
```

---

## Task 9: Rewrite App.tsx shell with Ant Design Layout + Sider

**Files:**
- Modify: `webui/src/App.tsx` — replace `<main>` + `<nav className="tabs">` with Layout + Sider + Menu

**Context:** After Tasks 3–8, App.tsx's `return` should only contain: the `<nav>` tab bar, the `{tab === '...' ? <TabComponent />}` conditionals, terminal modal, replay modal, recipe preview/assist modals, upload modal (now in SearchTab), and tunnel creation modal. This task replaces the outer shell with Ant Design Layout and converts remaining modals to antd Modal.

- [ ] **Step 1: Add Ant Design icons to the menu items**

At the top of App.tsx, add imports:
```tsx
import {
  Layout, Menu, Typography, Modal, Button, Input, Select, Alert, Spin,
  type MenuProps,
} from 'antd';
import {
  SearchOutlined, FileOutlined, CloudOutlined, SettingOutlined,
  PlayCircleOutlined, ApiOutlined, AppstoreOutlined, DatabaseOutlined,
} from '@ant-design/icons';
```

- [ ] **Step 2: Define menu items**

Add in App.tsx, just before the return:
```tsx
type Tab = 'search' | 'files' | 'backends' | 'config' | 'recipes' | 'tunnels' | 'apps' | 'api-docs';

const menuItems: MenuProps['items'] = [
  { key: 'search',   icon: <SearchOutlined />,       label: 'Search' },
  { key: 'files',    icon: <FileOutlined />,          label: 'Files' },
  { key: 'backends', icon: <CloudOutlined />,         label: 'Backends' },
  { key: 'config',   icon: <SettingOutlined />,       label: 'Config' },
  { key: 'recipes',  icon: <PlayCircleOutlined />,    label: 'Recipes' },
  { key: 'tunnels',  icon: <ApiOutlined />,           label: 'Tunnels' },
  { key: 'apps',     icon: <DatabaseOutlined />,      label: 'Apps & Proxies' },
  { key: 'api-docs', icon: <AppstoreOutlined />,      label: 'API Docs' },
];
```

- [ ] **Step 3: Replace the return JSX**

Replace the entire `return (` block:
```tsx
return (
  <Layout style={{ minHeight: '100vh' }}>
    <Layout.Sider collapsible width={200} theme="dark">
      <div style={{ padding: '16px 20px', borderBottom: '1px solid #1d2535', display: 'flex', alignItems: 'center', gap: 8 }}>
        <Typography.Text strong style={{ color: '#e6e6e6', fontSize: 14 }}>
          hostctl
        </Typography.Text>
        {meta && (
          <Typography.Text style={{ color: '#666', fontSize: 11 }}>
            v{meta.version}
          </Typography.Text>
        )}
      </div>
      {tokenMsg && (
        <Alert message={tokenMsg} type="warning" banner style={{ fontSize: 11 }} />
      )}
      <Menu
        theme="dark"
        mode="inline"
        selectedKeys={[tab]}
        items={menuItems}
        onSelect={({ key }) => setTab(key as Tab)}
        style={{ flex: 1, borderRight: 0 }}
      />
    </Layout.Sider>

    <Layout>
      <Layout.Content style={{ padding: '16px 20px', minHeight: 0 }}>
        {tab === 'search'   && <SearchTab   records={records} selectedKeys={selectedKeys} onRecordsChange={setRecords} onSelectedKeysChange={setSelectedKeys} backends={backends} providerIds={providerIds} sshUser={sshUser} onSshUserChange={setSshUser} meta={meta} onOpenTunnel={openTunnelModal} onOpenReplay={openReplayModal} onOpenReplayAll={openReplayAllRecordings} onOpenTerminal={openTerminal} />}
        {tab === 'files'    && <FilesTab    records={records} backends={backends} />}
        {tab === 'backends' && <BackendsTab backends={backends} error={backErr} />}
        {tab === 'config'   && <ConfigTab />}
        {tab === 'recipes'  && <RecipesTab  records={records} selectedRecords={selectedRecords} onSelectedRecordsChange={(hosts) => { const next: Record<string, boolean> = {}; for (const h of hosts) next[recordKey(h)] = true; setSelectedKeys(next); }} onViewSource={(path, name) => void openRecipePreview(path, name)} onAiAssist={openRecipeAssist} sessionRecordingAvailable={!!meta?.session_recording_available} terminalAssistAvailable={!!meta?.terminal_assist_available} />}
        {tab === 'tunnels'  && <TunnelsTab  onNavigateToSearch={() => setTab('search')} />}
        {tab === 'apps'     && <AppsTab     sshUser={sshUser} providers={selectedProviders} backends={selectedBackends} />}
        {tab === 'api-docs' && <ApiDocsTab />}
      </Layout.Content>
    </Layout>

    {/* Terminal modal — global, survives tab switches */}
    {terminals.length > 0 ? (
      <TerminalTabsModal
        isOpen={isTerminalModalOpen}
        terminals={terminals}
        activeTermId={activeTermId}
        sshUser={sshUser}
        recordSession={false}
        assistAvailable={!!meta?.terminal_assist_available}
        onSetActive={setActiveTermId}
        onCloseTerminal={(id) => {
          sessionStorage.removeItem(`honey_term_${id}`);
          setTerminals((prev) => {
            const next = prev.filter((t) => t.id !== id);
            if (activeTermId === id) setActiveTermId(next.length > 0 ? next[next.length - 1].id : null);
            if (next.length === 0) setIsTerminalModalOpen(false);
            return next;
          });
        }}
        onCloseModal={() => setIsTerminalModalOpen(false)}
      />
    ) : null}

    {/* Replay modal — pass same props as original App.tsx */}
    {replayRecord && replayItems.length > 0 ? (
      <SessionReplayModal
        record={replayRecord}
        recordings={replayItems}
        listStats={replayListMeta ? { file_count: replayListMeta.file_count, total_bytes: replayListMeta.total_bytes } : undefined}
        retention={replayListMeta?.retention}
        assistAvailable={!!meta?.terminal_assist_available}
        onRecordingsChange={() => void openReplayAllRecordings()}
        onClose={() => { setReplayRecord(null); setReplayListMeta(null); }}
      />
    ) : replayRecord ? (
      <Modal open title="Session replay" onCancel={() => setReplayRecord(null)} footer={<Button onClick={() => setReplayRecord(null)}>Close</Button>}>
        {replayErr ? <Alert type="error" message={replayErr} /> : <Spin tip="Loading recordings…" />}
      </Modal>
    ) : null}

    {/* Tunnel creation modal */}
    <Modal
      open={!!tunnelOpen}
      title={`Port Forward / Tunnel — ${tunnelOpen?.record.name ?? ''}`}
      onCancel={() => setTunnelOpen(null)}
      footer={null}
      width="min(420px, 94vw)"
    >
      {tunnelOpen && (
        <div style={{ display: 'flex', flexDirection: 'column', gap: '0.65rem' }}>
          <Typography.Text type="secondary" style={{ fontSize: '0.85rem' }}>
            Ports will be opened on the machine running the Honey server.
          </Typography.Text>
          <div>
            <Typography.Text style={{ fontSize: '0.85rem' }}>Local port (on server)</Typography.Text>
            <Input style={{ marginTop: 4 }} placeholder="e.g. 8080" value={tunnelLocalPort} onChange={(e) => setTunnelLocalPort(e.target.value)} />
          </div>
          {tunnelOpen.record.provider !== 'k8s' && (
            <div>
              <Typography.Text style={{ fontSize: '0.85rem' }}>Target remote host (optional, defaults to localhost)</Typography.Text>
              <Input style={{ marginTop: 4 }} placeholder="e.g. localhost" value={tunnelRemoteHost} onChange={(e) => setTunnelRemoteHost(e.target.value)} />
            </div>
          )}
          <div>
            <Typography.Text style={{ fontSize: '0.85rem' }}>Remote port</Typography.Text>
            <Input style={{ marginTop: 4 }} placeholder="e.g. 5432" value={tunnelRemotePort} onChange={(e) => setTunnelRemotePort(e.target.value)} />
            {tunnelPortsLoading && <Typography.Text type="secondary" style={{ fontSize: '0.8rem' }}>Detecting open ports…</Typography.Text>}
            {tunnelPortsErr && <Alert type="error" message={`Error detecting ports: ${tunnelPortsErr}`} style={{ marginTop: 4 }} />}
            {tunnelPorts.length > 0 && (
              <Space wrap style={{ marginTop: 4 }}>
                {tunnelPorts.map((port) => (
                  <Button key={port} size="small" onClick={() => setTunnelRemotePort(port)} style={{ fontFamily: 'monospace' }}>{port}</Button>
                ))}
              </Space>
            )}
          </div>
          {tunnelErr && <Alert type="error" message={tunnelErr} />}
          <Button type="primary" loading={tunnelBusy} disabled={!tunnelLocalPort.trim() || !tunnelRemotePort.trim()} onClick={() => void submitTunnel()}>
            Start Tunnel
          </Button>
        </div>
      )}
    </Modal>

    {/* Recipe preview modal */}
    <Modal
      open={!!recipePreview}
      title={recipePreview?.title}
      onCancel={() => setRecipePreview(null)}
      footer={<Button onClick={() => setRecipePreview(null)}>Close</Button>}
      width="min(720px, 96vw)"
      styles={{ body: { maxHeight: '80vh', overflow: 'auto', padding: 0 } }}
    >
      {recipePreview && (
        <Suspense fallback={<CodeLoadingFallback code={recipePreview.content} />}>
          <HighlightedCode className="recipe-preview-code" code={recipePreview.content} language={detectCodeLanguage(recipePreview.title)} />
        </Suspense>
      )}
    </Modal>

    {/* Recipe AI assist modal */}
    {recipeAssistOpen ? (
      <Modal
        open
        title={`AI explain: ${recipeAssistOpen.name}`}
        onCancel={() => closeRecipeAssist()}
        footer={<Button onClick={() => closeRecipeAssist()}>Close</Button>}
        width="min(640px, 96vw)"
        styles={{ body: { maxHeight: '80vh', overflow: 'auto', display: 'flex', flexDirection: 'column', gap: '0.55rem' } }}
      >
        <Typography.Text type="secondary" style={{ fontSize: '0.82rem' }}>
          Explanations are generated from the recipe file, optional dry-run against your selected hosts ({selectedRecords.length} selected), and your question.
        </Typography.Text>
        {recipeAssistModelsLoading && <Spin size="small" />}
        {recipeAssistModelsErr && <Alert type="warning" message={recipeAssistModelsErr} />}
        {recipeAssistModels.length > 0 && (
          <div>
            <Typography.Text style={{ fontSize: '0.82rem' }}>Model</Typography.Text>
            <Select style={{ width: '100%', marginTop: 4 }} value={recipeAssistSelectedModel} onChange={setRecipeAssistSelectedModel} options={recipeAssistModels.map((id) => ({ value: id, label: id }))} />
          </div>
        )}
        <div>
          <Typography.Text style={{ fontSize: '0.82rem' }}>Question (optional)</Typography.Text>
          <Input.TextArea style={{ marginTop: 4 }} value={recipeAssistPrompt} onChange={(e) => setRecipeAssistPrompt(e.target.value)} placeholder="e.g. What does step 2 do on k8s pods?" rows={3} />
        </div>
        <Button type="primary" loading={recipeAssistBusy} disabled={recipeAssistModelsLoading || recipeAssistModels.length === 0 || !recipeAssistSelectedModel.trim()} onClick={() => void submitRecipeAssist()}>
          {recipeAssistBusy ? 'Thinking…' : 'Get explanation'}
        </Button>
        {recipeAssistErr && <Alert type="error" message={recipeAssistErr} />}
        {recipeAssistReply && (
          <div className="recipe-assist-reply" style={{ padding: '0.55rem', background: '#0f1115', border: '1px solid #2a3140', borderRadius: 6, maxHeight: '42vh', overflow: 'auto' }}>
            <Suspense fallback={<pre className="ai-markdown-suspense-fallback">{recipeAssistReply}</pre>}>
              <AiMarkdown content={recipeAssistReply} />
            </Suspense>
          </div>
        )}
      </Modal>
    ) : null}

    {/* Floating terminal button */}
    {terminals.length > 0 && !isTerminalModalOpen && (
      <Button
        type="primary"
        shape="round"
        style={{ position: 'fixed', bottom: 32, right: 32, zIndex: 40 }}
        onClick={() => setIsTerminalModalOpen(true)}
      >
        🖥️ Open Terminals ({terminals.length})
      </Button>
    )}
  </Layout>
);
```

Note: fill in the tunnel creation form, replay modal, and recipe assist modal contents inline (they are currently verbatim JSX from App.tsx — convert `<button>` → `<Button>`, `<input>` → `<Input>`, `<select>` → `<Select>`).

- [ ] **Step 4: Verify app loads with sidebar**

```bash
npm run dev
```
Expected: sidebar with icons visible, clicking menu items switches content, all 8 tabs work.

- [ ] **Step 5: Run all tests**

```bash
npm test
```
Expected: all tests pass.

- [ ] **Step 6: Commit**
```bash
git add webui/src/App.tsx
git commit -m "feat(webui): rewrite App shell with Ant Design Layout + Sider + Menu"
```

---

## Task 10: Migrate AppsTab internals

**Files:**
- Modify: `webui/src/AppsTab/index.tsx`
- Modify: `webui/src/AppsTab/apps-tab.css`

**Context:** AppsTab already has its own folder. Replace `<button>`, `<input>`, `<select>`, `<table>`, and modal divs inside AppsTab with Ant Design components. The SQL editor (CodeMirror via `@uiw/react-codemirror`) stays unchanged — just wrap it in an antd `Card`. Keep apps-tab.css for editor-specific overrides only.

- [ ] **Step 1: Read the current AppsTab**

Read `webui/src/AppsTab/index.tsx` fully to identify all native elements to replace.

- [ ] **Step 2: Replace native elements**

In `webui/src/AppsTab/index.tsx`, replace:
- `<button className="primary">` → `<Button type="primary">`
- `<button>` → `<Button>`
- `<input>` → `<Input>` or `<Input.Search>`
- `<select>` → `<Select>`
- `<table>` results grid → `<Table>`
- Loading state → `<Spin>`
- Error messages → `<Alert type="error">`
- Section wrappers → `<Card>`
- Status badges → `<Tag>`

Wrap the CodeMirror editor in a `<Card bodyStyle={{ padding: 0 }}>` to give it consistent dark border.

- [ ] **Step 3: Trim apps-tab.css**

Remove all rules that are now handled by antd (button, input, select, table styles). Keep only CodeMirror editor overrides (`.cm-editor`, `.cm-content`, etc.).

- [ ] **Step 4: Verify in browser**

Click Apps & Proxies tab. Expected: proxy session list, SQL editor, and results all render correctly with dark theme.

- [ ] **Step 5: Run all tests**

```bash
npm test
```
Expected: all tests pass.

- [ ] **Step 6: Commit**
```bash
git add webui/src/AppsTab/index.tsx webui/src/AppsTab/apps-tab.css
git commit -m "feat(webui): migrate AppsTab internals to Ant Design components"
```

---

## Task 11: Migrate RecipesTab internals

**Files:**
- Modify: `webui/src/RecipesTab/index.tsx`
- Modify: `webui/src/RecipesTab/StepEditors.tsx`
- Modify: `webui/src/RecipesTab/StepPlan.tsx`
- Modify: `webui/src/RecipesTab/StepRun.tsx`
- Modify: `webui/src/RecipesTab/StepRecipe.tsx`
- Modify: `webui/src/RecipesTab/StepHosts.tsx`
- Modify: `webui/src/RecipesTab/EditForm.tsx`
- Modify: `webui/src/RecipesTab/recipes-tab.css`

**Context:** RecipesTab is a 4-step wizard. Replace native elements with antd equivalents. The `@xyflow/react` diagram in `RecipeGraphFlow.tsx` is kept as-is — wrap it in a `<Card bodyStyle={{ padding: 0 }}>`. The `react-hook-form` + `zod` forms in `EditForm.tsx` stay; just replace the native `<input>`, `<select>`, `<textarea>` with antd `Input`, `Select`, `Input.TextArea`. Keep recipes-tab.css for xyflow canvas overrides only.

- [ ] **Step 1: Run existing RecipesTab tests before starting**

```bash
npm test -- RecipesTab
```
Expected: 3 tests pass (recipeStepUtils, addStep, StepRetryEditor). Record these as baseline.

- [ ] **Step 2: Migrate RecipesTab/index.tsx**

Replace in `index.tsx`:
- `<button>` / `<button className="primary">` → `<Button>` / `<Button type="primary">`
- `<input>` / `<textarea>` → `<Input>` / `<Input.TextArea>`
- `<select>` → `<Select>`
- Step indicator (numbered buttons) → `<Steps>` from antd
- Loading spinner → `<Spin>`
- Error/warning messages → `<Alert>`
- Section panels → `<Card>`
- AI markdown panel → keep `<AiMarkdown>`, wrap in `<Card>`

- [ ] **Step 3: Migrate StepEditors.tsx**

Replace native elements in each step editor component with antd equivalents. The `StepRetryEditor` uses `<input type="checkbox">` for the retry toggle — replace with antd `<Switch>` or `<Checkbox>`. **Important:** the test `StepRetryEditor.test.tsx` queries `screen.getByRole('checkbox', { name: 'retry' })`. If switching from `<input type="checkbox">` to antd `<Checkbox>`, verify the test still finds it (antd Checkbox renders an accessible checkbox role). Run tests after this step.

- [ ] **Step 4: Run RecipesTab tests**

```bash
npm test -- RecipesTab
```
Expected: same 3 tests still pass. If StepRetryEditor test fails, update the query selector to match the new antd Checkbox structure.

- [ ] **Step 5: Migrate EditForm.tsx**

Replace `<input>`, `<select>`, `<textarea>` with antd form controls. Keep `react-hook-form` controller pattern; just swap the input components.

- [ ] **Step 6: Migrate remaining step files**

Apply same pattern to StepPlan.tsx, StepRun.tsx, StepRecipe.tsx, StepHosts.tsx.

- [ ] **Step 7: Trim recipes-tab.css**

Remove all rules now handled by antd. Keep only xyflow canvas styles (`.react-flow`, `.react-flow__node`, etc.).

- [ ] **Step 8: Verify in browser**

Click Recipes tab. Walk through the 4-step wizard. Verify flow diagram renders. Check AI assist response rendering.

- [ ] **Step 9: Run all tests**

```bash
npm test
```
Expected: all tests pass.

- [ ] **Step 10: Commit**
```bash
git add webui/src/RecipesTab/
git commit -m "feat(webui): migrate RecipesTab internals to Ant Design components"
```

---

## Task 12: Trim app.css + xyflow/swagger dark overrides

**Files:**
- Modify: `webui/src/app.css`

**Context:** After all migrations, app.css should only contain styles for specialized components that antd doesn't style: xterm.js terminal, xyflow canvas, Swagger UI dark overrides, CodeMirror wrapper, and the `sr-only` utility class. Delete everything else.

- [ ] **Step 1: Identify what to keep**

These CSS class groups stay (they can't be replaced by antd):
- `.term-wrap`, `.term-xterm-host`, `.term-vnc-host`, `.term-connect-overlay`, `.term-spinner` — xterm/VNC display
- `.terminal-tabs-*`, `.terminal-tab*`, `.terminal-maximize-btn`, `.modal-maximized`, `.modal-terminal-*` — terminal modal tabs (these are structural, not styling)
- `.term-assist-panel`, `.term-assist-reply` — AI assist sidebar in terminal
- `.term-structured-replay` — batch replay log
- `.ai-markdown*` — GFM markdown rendering
- `.upload-progress-*` — upload progress bar (uses CSS animation)
- `.honey-openapi-swagger .swagger-ui *` — Swagger UI dark overrides
- `.raw-yaml-editor`, `.code-highlight` — CodeMirror borders
- `.floating-terminal-btn` — now replaced by antd Button (delete this)
- `.sr-only` — accessibility utility

**Delete these (now handled by antd):**
- `* { box-sizing: border-box }` — antd sets this
- `body { ... }` — antd sets dark background and font
- `main { ... }` — now Ant Design Layout.Content
- `nav.tabs { ... }` — replaced by Sider + Menu
- `input, textarea, select { ... }` — antd Input/Select
- `button.primary { ... }` — antd Button
- `table, th, td, tr { ... }` — antd Table
- `.modal-backdrop`, `.modal { ... }` — antd Modal
- `.modal header { ... }` — antd Modal.header

- [ ] **Step 2: Edit app.css**

Delete the rule sets listed above. The file should shrink from ~762 lines to ~300 lines.

- [ ] **Step 3: Verify no visual regressions**

With dev server running, check each tab: Search, Config (YAML editor), Recipes (flow graph + AI panel), Apps (SQL editor), Tunnels (log modal), and open a terminal session.

- [ ] **Step 4: Run all tests**

```bash
npm test
```
Expected: all tests pass.

- [ ] **Step 5: Build to confirm no TypeScript errors**

```bash
npm run build
```
Expected: build succeeds, output written to `../internal/webserver/static/`.

- [ ] **Step 6: Commit**
```bash
git add webui/src/app.css
git commit -m "chore(webui): trim app.css to specialized-component overrides only"
```

---

## Verification Checklist

After all tasks:

- [ ] `npm run dev` — app loads with dark sidebar, all 8 tabs accessible
- [ ] `npm test` — all tests pass
- [ ] `npm run build` — no TypeScript errors, build succeeds
- [ ] Search tab: host search, filter dropdowns, exec command, results table, upload modal, host detail card
- [ ] Files tab: agent transfer form with host/backend dropdowns
- [ ] Backends tab: antd Table with backend rows
- [ ] Config tab: YAML editor (CodeMirror), Save/Reload buttons, ConfigBackendsSection
- [ ] Recipes tab: 4-step wizard, AI assist, xyflow diagram
- [ ] Tunnels tab: active tunnels table, Refresh, Stop, Logs modal
- [ ] Apps tab: DB proxy sessions, SQL editor, results
- [ ] API Docs tab: Swagger UI renders with dark overrides
- [ ] Terminal modal: opens from host row, xterm renders, AI assist panel works
- [ ] Session replay modal: opens, playback works
- [ ] Sidebar collapse: 200px → 48px icon-only, content reflows
- [ ] URL tab state preserved on reload (`?tab=search`)
