import { useEffect, useRef, useState } from 'react';
import { DockviewReact, type DockviewApi, type DockviewReadyEvent } from 'dockview';
import 'dockview/dist/styles/dockview.css';
import './workspace/dockview-theme-honey.css';
import { Button, Select, Space, message } from 'antd';
import { FileAddOutlined, FolderOpenOutlined } from '@ant-design/icons';
import { GraphPanel } from './workspace/panels/GraphPanel';
import { RawEditorPanel } from './workspace/panels/RawEditorPanel';
import { StepEditorPanel } from './workspace/panels/StepEditorPanel';
import { ToolboxPanel } from './workspace/panels/ToolboxPanel';
import { RunPanel } from './workspace/panels/RunPanel';
import { RecordsPanel } from './workspace/panels/RecordsPanel';
import { TerminalPanel } from './workspace/panels/TerminalPanel';
import { ActivityBar } from './workspace/ActivityBar';
import { attachDockviewSync } from './workspace/useDockviewSync';
import { openGraph } from './workspace/registry';
import { EditorHeaderActions } from './workspace/EditorHeaderActions';
import { useWorkspaceStore } from './workspace/store';
import { apiGet } from '../api/core';
import type { HostRecord } from '../HostPicker';

const components = {
  graph: GraphPanel,
  raweditor: RawEditorPanel,
  stepeditor: StepEditorPanel,
  toolbox: ToolboxPanel,
  run: RunPanel,
  records: RecordsPanel,
  terminal: TerminalPanel,
};

interface RecipeStoreEntry {
  name: string;
}

export default function StudioWorkspace() {
  const [api, setApi] = useState<DockviewApi | null>(null);
  const disposeRef = useRef<(() => void) | null>(null);
  const [recipeList, setRecipeList] = useState<RecipeStoreEntry[]>([]);
  const setSchema = useWorkspaceStore((s) => s.setSchema);
  const newDoc = useWorkspaceStore((s) => s.newDoc);
  const createDoc = useWorkspaceStore((s) => s.createDoc);

  useEffect(() => {
    apiGet('/api/v1/recipes/schema')
      .then((r) => (r.ok ? r.json() : null))
      .then((data) => data && setSchema(data))
      .catch(() => {});
  }, [setSchema]);

  useEffect(() => {
    apiGet('/api/v1/recipes/store')
      .then((r) => (r.ok ? r.json() : []))
      .then((data) => setRecipeList(Array.isArray(data) ? data : []))
      .catch(() => {});
  }, []);

  const onReady = (event: DockviewReadyEvent) => {
    const a = event.api;
    setApi(a);
    // First-run tool panel arrangement — toolbox on the left (Records tabbed
    // alongside it), step editor to the right, run panel docked below the
    // step editor. `graph`/`raweditor`/`terminal` panels open on demand
    // (New/Open, Terminal action).
    a.addPanel({ id: 'toolbox', component: 'toolbox', title: 'Toolbox' });
    a.addPanel({
      id: 'records',
      component: 'records',
      title: 'Records',
      position: { referencePanel: 'toolbox', direction: 'within' },
    });
    a.addPanel({
      id: 'stepeditor',
      component: 'stepeditor',
      title: 'Step',
      position: { direction: 'right' },
    });
    a.addPanel({ id: 'run', component: 'run', title: 'Run', position: { direction: 'below' } });
    disposeRef.current = attachDockviewSync(a, useWorkspaceStore.getState());

    // Wire the store's `openTerminal` slot so the Records panel's Terminal
    // action (decoupled from this module — see RecordsPanel.tsx) can spawn a
    // Terminal panel here. Each call opens a new panel keyed by a fresh
    // uuid, so multiple sessions can be open concurrently.
    useWorkspaceStore.getState().setOpenTerminal((rec: HostRecord) => {
      const id = `term:${crypto.randomUUID()}`;
      a.addPanel({ id, component: 'terminal', params: { record: rec, pve: 'serial' }, title: rec.name ?? 'terminal' });
    });
  };

  useEffect(
    () => () => {
      disposeRef.current?.();
      useWorkspaceStore.getState().setOpenTerminal(null);
    },
    [],
  );

  const handleNew = () => {
    const id = newDoc();
    if (api) openGraph(api, id);
  };

  const handleOpen = (name: string) => {
    createDoc(name)
      .then(() => {
        if (api) openGraph(api, name);
      })
      .catch((err) => message.error(`Failed to open ${name}: ${err instanceof Error ? err.message : err}`));
  };

  return (
    <div style={{ display: 'flex', height: '100%', width: '100%' }}>
      <ActivityBar api={api} />
      <div style={{ display: 'flex', flexDirection: 'column', flex: 1, minWidth: 0 }}>
        <div style={{ padding: '6px 12px', background: '#001529', borderBottom: '1px solid #1f2937' }}>
          <Space>
            <Button size="small" icon={<FileAddOutlined />} onClick={handleNew}>
              New Recipe
            </Button>
            <Select
              size="small"
              style={{ width: 220 }}
              placeholder="Open recipe…"
              suffixIcon={<FolderOpenOutlined />}
              showSearch
              optionFilterProp="label"
              options={recipeList.map((r) => ({ value: r.name, label: r.name }))}
              onSelect={handleOpen}
            />
          </Space>
        </div>
        <div style={{ flex: 1, minHeight: 0 }}>
          <DockviewReact
            components={components}
            rightHeaderActionsComponent={EditorHeaderActions}
            onReady={onReady}
            className="dockview-theme-honey"
          />
        </div>
      </div>
    </div>
  );
}
