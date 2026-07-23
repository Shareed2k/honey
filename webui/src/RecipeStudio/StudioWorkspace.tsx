import { useEffect, useRef, useState } from 'react';
import { DockviewReact, type DockviewApi, type DockviewReadyEvent } from 'dockview';
import 'dockview/dist/styles/dockview.css';
import './workspace/dockview-theme-honey.css';
import { Button, Select, Space, message, Modal, Input } from 'antd';
import { FileAddOutlined, FolderOpenOutlined, CloudDownloadOutlined, ReadOutlined, FireOutlined } from '@ant-design/icons';
import { GraphPanel } from './workspace/panels/GraphPanel';
import { RawEditorPanel } from './workspace/panels/RawEditorPanel';
import { StepEditorPanel } from './workspace/panels/StepEditorPanel';
import { ToolboxPanel } from './workspace/panels/ToolboxPanel';
import { RunPanel } from './workspace/panels/RunPanel';
import { RecordsPanel } from './workspace/panels/RecordsPanel';
import { TerminalPanel } from './workspace/panels/TerminalPanel';
import { ValidationPanel } from './workspace/panels/ValidationPanel';
import { SettingsPanel } from './workspace/panels/SettingsPanel';
import { ActivityBar } from './workspace/ActivityBar';
import { attachDockviewSync } from './workspace/useDockviewSync';
import { applyDefaultLayout, openGraph } from './workspace/registry';
import { attachWorkspaceSync, resetLayout } from './workspace/persistence';
import { EditorHeaderActions } from './workspace/EditorHeaderActions';
import { useWorkspaceStore, uniqueDocName } from './workspace/store';
import { apiGet, apiPost } from '../api/core';
import { generateRecipe } from '../api/recipes';
import type { HostRecord } from '../HostPicker';
import GitLoadModal from './GitLoadModal';
import { LibraryModal } from './LibraryModal';
import type { LibraryRecipe } from '../api/types/recipes';

const components = {
  graph: GraphPanel,
  raweditor: RawEditorPanel,
  stepeditor: StepEditorPanel,
  toolbox: ToolboxPanel,
  run: RunPanel,
  records: RecordsPanel,
  terminal: TerminalPanel,
  validation: ValidationPanel,
  settings: SettingsPanel,
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
  const createDocFromRecipe = useWorkspaceStore((s) => s.createDocFromRecipe);

  const [generateOpen, setGenerateOpen] = useState(false);
  const [generateIntent, setGenerateIntent] = useState('');
  const [generateBusy, setGenerateBusy] = useState(false);
  const [libraryOpen, setLibraryOpen] = useState(false);
  const [gitLoadOpen, setGitLoadOpen] = useState(false);

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
    // (New/Open, Terminal action). If a workspace was previously saved,
    // `attachWorkspaceSync`'s restore() replaces this via `api.fromJSON`
    // below — laying these out first (before any sync is attached, so
    // neither subscription sees these adds) just avoids a blank shell while
    // the GET is in flight.
    applyDefaultLayout(a);

    const disposers: (() => void)[] = [];
    disposers.push(attachDockviewSync(a, useWorkspaceStore.getState()));
    const sync = attachWorkspaceSync(a, useWorkspaceStore.getState());
    disposers.push(sync.dispose);
    disposeRef.current = () => disposers.forEach((d) => d());

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

  // Opens a new doc from an in-memory recipe object and focuses its graph
  // panel — the shared tail end of the Generate/Library/Git-load flows below.
  // `uniqueDocName` is computed here (against the CURRENT synchronous docs
  // snapshot) rather than trusting `desiredName` verbatim, so the panel we
  // open always matches the key createDocFromRecipe actually used, even if
  // `desiredName` collided with an already-open doc.
  const openNewRecipeDoc = (desiredName: string, recipeJson: unknown, rawCue?: string) => {
    const finalName = uniqueDocName(desiredName, useWorkspaceStore.getState().docs);
    createDocFromRecipe(finalName, recipeJson, rawCue);
    if (api) openGraph(api, finalName);
    return finalName;
  };

  // Ported from the old useRecipeStudioEngine.ts's handleGenerateAI: same
  // generateRecipe(intent, model) call, with `model` left as the old
  // hardcoded empty string (the old shell never surfaced a model picker
  // either — see StudioWorkspace.tsx@0f1c4fb's Generate modal, which called
  // handleGenerateAI(intent) with no model argument of its own).
  const handleGenerate = async () => {
    if (!generateIntent.trim()) return;
    setGenerateBusy(true);
    try {
      const res = await generateRecipe(generateIntent, '');
      const name = openNewRecipeDoc(`generated-${Date.now()}.cue`, res.recipe);
      setGenerateOpen(false);
      setGenerateIntent('');
      message.success(`AI Generation applied: ${res.explanation || name}`);
    } catch (err) {
      message.error('AI Generation failed: ' + (err instanceof Error ? err.message : String(err)));
    } finally {
      setGenerateBusy(false);
    }
  };

  // Ported from the old StudioWorkspace.tsx's LibraryModal onSelect: a
  // library entry's `.content` is raw CUE, so it goes through the
  // content-based parse endpoint (POST /api/v1/recipes/parse — no path
  // validation, content travels in the body) to get recipe JSON before it
  // can be turned into a graph.
  const handleLibrarySelect = async (libRecipe: LibraryRecipe) => {
    setLibraryOpen(false);
    try {
      const parseRes = await apiPost('/api/v1/recipes/parse', { content: libRecipe.content });
      if (!parseRes.ok) throw new Error(await parseRes.text());
      const parseData = await parseRes.json();
      openNewRecipeDoc(libRecipe.name, parseData.recipe, libRecipe.content);
      message.success(`Loaded ${libRecipe.name} from Library`);
    } catch (err) {
      message.error('Failed to load library recipe: ' + (err instanceof Error ? err.message : String(err)));
    }
  };

  // Ported from the old useRecipeStudioEngine.ts's doGitLoad: POST the git
  // coordinates to /api/v1/recipes/store/git-load (fetches file content from
  // the given repo/branch/path — does NOT save it into the local store),
  // then parse that content the same content-based way the Library flow
  // does. Unlike the old single-doc shell (which overwrote the current
  // canvas and needed a discard-confirm), this always lands in a brand-new
  // doc, so no confirm dialog is needed here.
  const handleGitLoad = async (options: {
    gitUrl: string;
    gitBranch: string;
    path: string;
    gitUser: string;
    gitPass: string;
    gitSsh: string;
  }) => {
    const payload = {
      path: options.path,
      git_url: options.gitUrl,
      git_branch: options.gitBranch,
      git_user: options.gitUser,
      git_pass: options.gitPass === '••••••••' ? '' : options.gitPass,
      git_ssh: options.gitSsh === '••••••••' ? '' : options.gitSsh,
    };
    try {
      const loadRes = await apiPost('/api/v1/recipes/store/git-load', payload);
      if (!loadRes.ok) throw new Error(await loadRes.text());
      const { content } = await loadRes.json();

      const parseRes = await apiPost('/api/v1/recipes/parse', { content });
      if (!parseRes.ok) throw new Error(await parseRes.text());
      const parseData = await parseRes.json();
      const recipeJson = parseData.recipe as { steps?: unknown[] } | undefined;
      if (!recipeJson || !recipeJson.steps) {
        message.warning('Selected file is not a valid graph recipe');
        return;
      }

      openNewRecipeDoc(options.path, recipeJson, content);
      setGitLoadOpen(false);
      message.success(`Successfully loaded ${options.path}!`);
    } catch (err) {
      message.error('Failed to load git recipe: ' + (err instanceof Error ? err.message : String(err)));
      throw err; // GitLoadModal keeps itself open on a rejected onLoad.
    }
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
            <Button size="small" icon={<CloudDownloadOutlined />} onClick={() => setGitLoadOpen(true)}>
              Load from Git
            </Button>
            <Button size="small" icon={<ReadOutlined />} onClick={() => setLibraryOpen(true)}>
              Library
            </Button>
            <Button size="small" icon={<FireOutlined />} onClick={() => setGenerateOpen(true)}>
              Generate
            </Button>
            <Button size="small" onClick={() => api && resetLayout(api)}>
              Reset Layout
            </Button>
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

      {gitLoadOpen && (
        <GitLoadModal
          visible={gitLoadOpen}
          onCancel={() => setGitLoadOpen(false)}
          onLoad={handleGitLoad}
        />
      )}

      <LibraryModal
        open={libraryOpen}
        onCancel={() => setLibraryOpen(false)}
        onSelect={handleLibrarySelect}
      />

      <Modal
        maskClosable={false}
        title="Generate Recipe with AI"
        open={generateOpen}
        onCancel={() => setGenerateOpen(false)}
        okText="Generate"
        confirmLoading={generateBusy}
        onOk={handleGenerate}
      >
        <Input.TextArea
          value={generateIntent}
          onChange={(e) => setGenerateIntent(e.target.value)}
          placeholder="Describe what you want to automate... e.g., Restart all nginx pods"
          rows={4}
        />
      </Modal>
    </div>
  );
}
