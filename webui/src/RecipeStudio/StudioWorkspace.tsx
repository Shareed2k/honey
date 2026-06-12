/* eslint-disable @typescript-eslint/no-explicit-any */
import React, { useState, useEffect, useCallback, lazy, Suspense } from 'react';
import { 
  ReactFlow, 
  Controls, 
  Background, 
  useNodesState, 
  useEdgesState, 
  addEdge,
  Connection,
  Edge
} from '@xyflow/react';
import '@xyflow/react/dist/style.css';
import './studio.css';
import { Layout, Button, Drawer, Space, Typography, Select, message, Modal } from 'antd';
import { PlusOutlined, SaveOutlined, SyncOutlined, PlayCircleOutlined, CloudDownloadOutlined, UndoOutlined, SettingOutlined, CodeOutlined } from '@ant-design/icons';

import CustomStepNode from './CustomStepNode';
import DynamicStepForm from './DynamicStepForm';
import StorageModal from './StorageModal';
import GitLoadModal from './GitLoadModal';
import { StepRun } from '../RecipesTab/StepRun';
import { HostPicker, recordKey, type HostRecord } from '../HostPicker';
import { apiGet, apiPost } from '../api';
import {
  applyWaveLayout,
  buildFlowFromRecipe,
  buildRecipeFromFlow,
  computeWavesFromEdges,
  createStepDraft,
  listStepKinds,
  recipeNameFromFilename,
  stepSchemaForKind,
  type StepDraft,
} from './recipeStudioUtils';

const CodeEditor = lazy(() => import('../CodeEditor'));

const { Header, Content, Sider } = Layout;
const { Title, Text } = Typography;

const nodeTypes = {
  step: CustomStepNode,
};

type Props = {
  records?: HostRecord[];
  selectedRecords?: any[];
  sshUser?: string;
};

export default function StudioWorkspace({ records = [], selectedRecords = [], sshUser = 'root' }: Props) {
  const [nodes, setNodes, onNodesChange] = useNodesState<any>([]);
  const [edges, setEdges, onEdgesChange] = useEdgesState<any>([]);
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  const [schema, setSchema] = useState<any>(null);
  const [stepData, setStepData] = useState<Record<string, StepDraft>>({});
  const [validating, setValidating] = useState(false);
  const [saveModalVisible, setSaveModalVisible] = useState(false);
  const [gitLoadModalVisible, setGitLoadModalVisible] = useState(false);
  const [runPanelOpen, setRunPanelOpen] = useState(false);
  const [runStepId, setRunStepId] = useState<string | null>(null);
  const [runCount, setRunCount] = useState(0);
  const [recipeDefaults, setRecipeDefaults] = useState<any>({});
  const [settingsDrawerOpen, setSettingsDrawerOpen] = useState(false);
  const [rawMode, setRawMode] = useState(false);
  const [rawContent, setRawContent] = useState('');
  const [runHosts, setRunHosts] = useState<HostRecord[]>([]);
  const [hostPickerOpen, setHostPickerOpen] = useState(false);
  const [pendingRunStepId, setPendingRunStepId] = useState<string | null>(null);
  const [modalSelectedKeys, setModalSelectedKeys] = useState<Record<string, boolean>>({});

  // Storage selection & loading state
  const [availableRecipes, setAvailableRecipes] = useState<any[]>([]);
  const [selectedRecipe, setSelectedRecipe] = useState<string | undefined>(undefined);

  // 1. Fetch JSON Schema & Recipe List on Mount
  useEffect(() => {
    apiGet('/api/v1/recipes/schema')
      .then((res) => {
        if (!res.ok) throw new Error(res.statusText);
        return res.json();
      })
      .then((data) => setSchema(data))
      .catch((err) => message.error('Failed to load JSON Schema: ' + err.message));

    loadRecipesList();
  }, []);

  const loadRecipesList = () => {
    apiGet('/api/v1/recipes/store')
      .then((res) => {
        if (!res.ok) throw new Error(res.statusText);
        return res.json();
      })
      .then((data) => {
        if (Array.isArray(data)) {
          setAvailableRecipes(data);
        }
      })
      .catch((err) => message.error('Failed to load recipes: ' + err.message));
  };

  // 2. Load and Deconstruct Recipe
  const doLoadRecipe = async (name: string) => {
    try {
      setSelectedRecipe(name);
      const res = await apiGet(`/api/v1/recipes/store/${encodeURIComponent(name)}`);
      if (!res.ok) {
        throw new Error(await res.text());
      }
      const data = await res.json();
      const recipeJson = data.recipe;

      if (!recipeJson || !recipeJson.steps) {
        message.warning('Selected file is not a valid graph recipe');
        return;
      }

      if (data.errors?.length) {
        message.warning('Loaded recipe contains validation issues');
      }

      setRecipeDefaults(recipeJson.defaults ?? {});

      const { nodes: newNodes, edges: newEdges, stepData: newStepData } = buildFlowFromRecipe(recipeJson);

      const waveByNode = graphWaveByNode(data.graph);
      const finalNodes = newNodes.map((node) => {
        const stepSummary = data.steps?.find((s: any) => s.id === node.id);
        return {
          ...node,
          data: { ...node.data, wave: waveByNode[node.id] ?? stepSummary?.wave },
        };
      });

      setNodes(applyWaveLayout(finalNodes));
      setEdges(newEdges);
      setStepData(newStepData);
      setRawMode(false);
      message.success(`Successfully loaded ${name}!`);
    } catch (err: any) {
      message.error('Failed to load recipe: ' + err.message);
    }
  };

  const loadRecipe = (name: string | undefined) => {
    if (!name) return;
    if (nodes.length > 0 && selectedRecipe && name !== selectedRecipe) {
      Modal.confirm({
        title: 'Load new recipe?',
        content: 'Current canvas will be discarded.',
        okText: 'Discard & Load',
        cancelText: 'Cancel',
        onOk: () => doLoadRecipe(name),
      });
      return;
    }
    doLoadRecipe(name);
  };

  // 3. Select Node Click
  const onNodeClick = (_: any, node: any) => {
    setSelectedNodeId(node.id);
  };

  // 4. Connect Edges (Dependency mapping)
  const onConnect = useCallback(
    (params: Connection | Edge) => setEdges((eds) => addEdge(params, eds)),
    [setEdges]
  );

  // 5. Update Node Parameters
  const handleStepDataChange = (nextData: any) => {
    if (!selectedNodeId) return;
    setStepData((prev) => ({
      ...prev,
      [selectedNodeId]: nextData,
    }));
    // Sync React Flow Node Label/Host
    setNodes((nds) =>
      nds.map((n) => {
        if (n.id === selectedNodeId) {
          return {
            ...n,
            data: {
              ...n.data,
              host: nextData.host || '_',
            },
          };
        }
        return n;
      })
    );
  };

  // 6. Build full serializable Recipe JSON
  const buildRecipeJSON = () => {
    if (rawMode) return JSON.parse(rawContent);
    const base = buildRecipeFromFlow({
      name: recipeNameFromFilename(selectedRecipe),
      nodes,
      edges,
      stepData,
    });
    if (Object.keys(recipeDefaults || {}).length > 0) {
      (base as any).defaults = recipeDefaults;
    }
    return base;
  };

  const handleSwitchToRaw = () => {
    setRawContent(JSON.stringify(buildRecipeJSON(), null, 2));
    setRawMode(true);
    setRunPanelOpen(false);
    setSelectedNodeId(null);
  };

  const handleSwitchToVisual = () => {
    try {
      const parsed = JSON.parse(rawContent);
      const { nodes: newNodes, edges: newEdges, stepData: newStepData } = buildFlowFromRecipe(parsed);
      const waveByNode = computeWavesFromEdges(newNodes, newEdges);
      const nodesWithWave = newNodes.map((n) => ({
        ...n,
        data: { ...n.data, wave: waveByNode[n.id] ?? 1 },
      }));
      setNodes(applyWaveLayout(nodesWithWave));
      setEdges(newEdges);
      setStepData(newStepData);
      setRecipeDefaults(parsed.defaults ?? {});
      setRawMode(false);
    } catch {
      message.error('Invalid JSON — fix errors before switching to visual mode');
    }
  };

  const buildSubgraphRecipe = (targetId: string) => {
    const visited = new Set<string>();
    const queue = [targetId];
    
    while (queue.length > 0) {
      const current = queue.shift()!;
      if (visited.has(current)) continue;
      visited.add(current);
      
      const neighbors = edges
        .filter((e: any) => e.target === current || e.source === current)
        .map((e: any) => (e.target === current ? e.source : e.target));
      queue.push(...neighbors);
    }

    const subNodes = nodes.filter((n: any) => visited.has(n.id));
    const subEdges = edges.filter((e: any) => visited.has(e.source) && visited.has(e.target));

    const base = buildRecipeFromFlow({
      name: recipeNameFromFilename(selectedRecipe) + '-run-step',
      nodes: subNodes,
      edges: subEdges,
      stepData,
    });
    
    if (Object.keys(recipeDefaults || {}).length > 0) {
      (base as any).defaults = recipeDefaults;
    }
    return base;
  };

  const modalHosts = records.filter((r) => modalSelectedKeys[recordKey(r)]);

  const handleRunStep = (stepId: string) => {
    if (selectedRecords.length > 0) {
      setRunHosts(selectedRecords as HostRecord[]);
      setRunStepId(stepId);
      setRunCount((c) => c + 1);
      setRunPanelOpen(true);
    } else {
      setPendingRunStepId(stepId);
      setModalSelectedKeys({});
      setHostPickerOpen(true);
    }
  };

  const handleModalRun = () => {
    if (modalHosts.length === 0) {
      message.warning('Select at least one host to run on');
      return;
    }
    setRunHosts(modalHosts);
    setRunStepId(pendingRunStepId!);
    setRunCount((c) => c + 1);
    setHostPickerOpen(false);
    setRunPanelOpen(true);
  };

  const graphWaveByNode = (graph: any): Record<string, number> => {
    const waves: any[][] = Array.isArray(graph?.waves) ? graph.waves : [];
    const out: Record<string, number> = {};
    waves.forEach((wave, waveIndex) => {
      wave.forEach((node) => {
        const id = typeof node === 'string' ? node : node?.id;
        if (id) out[id] = waveIndex + 1;
      });
    });
    return out;
  };

  // 7. Real-Time Dynamic Validation Loop
  const handleValidate = async () => {
    setValidating(true);
    const recipeContent = buildRecipeJSON();
    try {
      const res = await apiPost('/api/v1/recipes/validate-content', { recipe_content: recipeContent });
      const data = await res.json();
      if (data.errors && data.errors.length > 0) {
        message.warning('Recipe contains validation issues');
        setNodes((nds) =>
          nds.map((n) => {
            const err = data.errors.find((e: any) => e.path?.includes(n.id));
            return {
              ...n,
              data: {
                ...n.data,
                  error: err ? err.message : undefined,
              },
            };
          })
        );
      } else {
        message.success('Recipe is fully valid & verified!');
        const waveByNode = graphWaveByNode(data.graph);
        setNodes((nds) =>
          applyWaveLayout(nds.map((n) => {
            const stepSummary = data.steps?.find((s: any) => s.id === n.id);
            return {
              ...n,
              data: {
                ...n.data,
                wave: waveByNode[n.id] ?? stepSummary?.wave,
                error: undefined,
              },
            };
          }))
        );
      }
    } catch (err: any) {
      message.error('Validation failed: ' + err.message);
    } finally {
      setValidating(false);
    }
  };

  // 8. Draggable Toolbar Add Step
  const addStepNode = (kind: string) => {
    const id = `${kind}_${nodes.length + 1}`;
    const draft = createStepDraft(kind, id);
    const newNode = {
      id,
      type: 'step',
      position: { x: 100 + nodes.length * 220, y: 150 },
      data: { label: id, kind, host: draft.host },
    };
    setNodes((nds) => [...nds, newNode]);
    setStepData((prev) => ({
      ...prev,
      [id]: draft,
    }));
  };

  // 9. Save Recipe Handler
  const handleSaveRecipe = async (options: {
    storage: string;
    path: string;
    commitMessage: string;
    gitUrl?: string;
    gitBranch?: string;
  }) => {
    const content = buildRecipeJSON();
    let url = `/api/v1/recipes/store/${encodeURIComponent(options.path)}`;
    if (options.storage === 'git') {
      url += `?git_url=${encodeURIComponent(options.gitUrl || '')}&git_branch=${encodeURIComponent(options.gitBranch || '')}`;
    }
    const res = await apiPost(url, { content: JSON.stringify(content, null, 2) });
    if (!res.ok) {
      const err = await res.text();
      throw new Error(err);
    }
    loadRecipesList();
  };

  const handleReset = () => {
    setNodes([]);
    setEdges([]);
    setStepData({});
    setSelectedNodeId(null);
    setSelectedRecipe(undefined);
    setRecipeDefaults({});
    setRunPanelOpen(false);
    setRunStepId(null);
    setRunCount(0);
    setSettingsDrawerOpen(false);
    message.info('Canvas reset');
  };

  const handleGitLoad = async (options: {
    gitUrl: string;
    gitBranch: string;
    path: string;
    gitUser: string;
    gitPass: string;
    gitSsh: string;
  }) => {
    if (nodes.length > 0) {
      Modal.confirm({
        title: 'Load recipe from Git?',
        content: 'Current canvas will be discarded.',
        okText: 'Discard & Load',
        cancelText: 'Cancel',
        onOk: () => doGitLoad(options),
      });
      return;
    }
    doGitLoad(options);
  };

  const doGitLoad = async (options: {
    gitUrl: string;
    gitBranch: string;
    path: string;
    gitUser: string;
    gitPass: string;
    gitSsh: string;
  }) => {
    try {
      const payload = {
        path: options.path,
        git_url: options.gitUrl,
        git_branch: options.gitBranch,
        git_user: options.gitUser,
        git_pass: options.gitPass === '••••••••' ? '' : options.gitPass,
        git_ssh: options.gitSsh === '••••••••' ? '' : options.gitSsh,
      };

      const loadRes = await apiPost('/api/v1/recipes/store/git-load', payload);
      if (!loadRes.ok) {
        throw new Error(await loadRes.text());
      }
      const responseData = await loadRes.json();
      const content = responseData.content;

      const parseRes = await apiPost('/api/v1/recipes/parse', { content });
      if (!parseRes.ok) {
        throw new Error(await parseRes.text());
      }
      const parseData = await parseRes.json();
      const recipeJson = parseData.recipe;

      if (!recipeJson || !recipeJson.steps) {
        message.warning('Selected file is not a valid graph recipe');
        return;
      }

      if (recipeJson.defaults) {
        setRecipeDefaults(recipeJson.defaults);
      } else {
        setRecipeDefaults({});
      }

      const { nodes: newNodes, edges: newEdges, stepData: newStepData } = buildFlowFromRecipe(recipeJson);

      const validatedNodes = [...newNodes];
      try {
        const validateRes = await apiPost('/api/v1/recipes/validate-content', { recipe_content: recipeJson });
        const validation = await validateRes.json();
        if (validation.errors?.length) {
          message.warning('Loaded recipe contains validation issues');
        } else {
          const waveByNode = graphWaveByNode(validation.graph);
          validatedNodes.forEach((node) => {
            const stepSummary = validation.steps?.find((s: any) => s.id === node.id);
            node.data = {
              ...node.data,
              wave: waveByNode[node.id] ?? stepSummary?.wave,
            };
          });
        }
      } catch {
        message.warning('Loaded recipe, but validation metadata could not be refreshed');
      }

      setNodes(applyWaveLayout(validatedNodes));
      setEdges(newEdges);
      setStepData(newStepData);
      setRawMode(false);
      setGitLoadModalVisible(false);
      message.success(`Successfully loaded ${options.path}!`);
    } catch (err: any) {
      message.error('Failed to load git recipe: ' + err.message);
    }
  };

  return (
    <Layout style={{ height: '100vh', background: '#0f1115' }}>
      <Header style={{ background: '#001529', display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '0 24px' }}>
        <Space size="large">
          <Title level={4} style={{ color: '#fff', margin: 0 }}>CUE Recipe Studio</Title>
          <Select
            placeholder="Load existing recipe..."
            style={{ width: 240 }}
            value={selectedRecipe}
            onChange={loadRecipe}
            options={availableRecipes.map((r) => ({ value: r.name, label: r.name }))}
            allowClear
          />
          <Button type="default" icon={<CloudDownloadOutlined />} onClick={() => setGitLoadModalVisible(true)}>
            Load from Git
          </Button>
        </Space>
        <Space>
          <Button
            type={rawMode ? 'primary' : 'default'}
            icon={<CodeOutlined />}
            onClick={rawMode ? handleSwitchToVisual : handleSwitchToRaw}
          >
            {rawMode ? 'Visual' : 'Raw'}
          </Button>
          <Button type="default" icon={<SettingOutlined />} onClick={() => setSettingsDrawerOpen(true)}>
            Recipe Settings
          </Button>
          <Button type="default" icon={<UndoOutlined />} onClick={handleReset}>
            Reset
          </Button>
          <Button type="default" icon={<SyncOutlined />} loading={validating} onClick={handleValidate}>
            Validate Graph
          </Button>
          <Button type="default" icon={<SaveOutlined />} onClick={() => setSaveModalVisible(true)}>
            Save Recipe
          </Button>
        </Space>
      </Header>

      <Layout style={{ flex: 1, minHeight: 0, overflow: 'hidden' }}>
        <Sider width={220} style={{ background: '#001529', borderRight: '1px solid #1f2937', padding: '16px' }}>
          <Title level={5} style={{ color: '#f0f6fc', marginTop: 0 }}>Toolbox (Drag/Click)</Title>
          <Text style={{ color: '#8b949e' }}>Drop steps onto canvas</Text>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 16 }}>
            {listStepKinds(schema).map((stepKind) => (
              <Button
                key={stepKind.kind}
                type="default"
                icon={<PlusOutlined />}
                onClick={() => addStepNode(stepKind.kind)}
              >
                {stepKind.label}
              </Button>
            ))}
          </div>
        </Sider>

        <Content style={{ position: 'relative', overflow: 'hidden', background: '#0d1117' }}>
          {rawMode ? (
            <Suspense fallback={<div style={{ padding: 16, color: '#8b949e' }}>Loading editor…</div>}>
              <CodeEditor
                value={rawContent}
                onChange={setRawContent}
                language="plain"
                height="100%"
              />
            </Suspense>
          ) : (
            <ReactFlow
              nodes={nodes}
              edges={edges}
              onNodesChange={onNodesChange}
              onEdgesChange={onEdgesChange}
              onConnect={onConnect}
              onNodeClick={onNodeClick}
              nodeTypes={nodeTypes}
              fitView
            >
              <Controls className="studio-flow-controls" />
              <Background />
            </ReactFlow>
          )}
        </Content>
      </Layout>

      <Drawer
        title={`Edit Step: ${selectedNodeId}`}
        width={360}
        open={selectedNodeId !== null}
        onClose={() => setSelectedNodeId(null)}
      >
        {selectedNodeId && (
          <>
            <DynamicStepForm
              schema={stepSchemaForKind(schema, stepData[selectedNodeId]?.kind)}
              value={stepData[selectedNodeId]}
              onChange={handleStepDataChange}
            />
            <Button
              type="primary"
              icon={<PlayCircleOutlined />}
              style={{ marginTop: 16, width: '100%' }}
              onClick={() => handleRunStep(selectedNodeId!)}
            >
              Run Step
            </Button>
          </>
        )}
      </Drawer>

      {runPanelOpen && runStepId && stepData[runStepId] && (
        <div style={{ position: 'absolute', bottom: 0, left: 0, right: 0, height: 300, background: '#1f2937', borderTop: '1px solid #30363d', zIndex: 10, overflowY: 'auto' }}>
          <div style={{ display: 'flex', justifyContent: 'space-between', padding: '8px 16px', background: '#0d1117' }}>
            <Typography.Text strong style={{ color: '#fff' }}>Run: {runStepId}</Typography.Text>
            <Button size="small" onClick={() => setRunPanelOpen(false)}>Close</Button>
          </div>
          <div style={{ padding: 16 }}>
            <StepRun 
              key={`${runStepId}-${runCount}`}
              recipe={buildSubgraphRecipe(runStepId) as any}
              recipeBasePath={null}
              hosts={runHosts}
              envOverrides={[]}
              sshUser={sshUser}
              recordSession={false}
              sessionRecordingAvailable={false}
              onViewRecording={() => {}}
              onRunAgain={() => setRunCount(c => c + 1)}
              onStartNew={() => setRunPanelOpen(false)}
            />
          </div>
        </div>
      )}

      <Drawer
        title="Recipe Settings (Defaults)"
        width={360}
        open={settingsDrawerOpen}
        onClose={() => setSettingsDrawerOpen(false)}
      >
        <DynamicStepForm
          schema={stepSchemaForKind(schema, 'defaults')}
          value={recipeDefaults}
          onChange={setRecipeDefaults}
        />
      </Drawer>

      <StorageModal
        visible={saveModalVisible}
        currentRecipeName={selectedRecipe}
        onCancel={() => setSaveModalVisible(false)}
        onSave={handleSaveRecipe}
      />

      {gitLoadModalVisible && (
        <GitLoadModal
          visible={gitLoadModalVisible}
          onCancel={() => setGitLoadModalVisible(false)}
          onLoad={handleGitLoad}
        />
      )}

      <Modal
        title="Select hosts to run on"
        open={hostPickerOpen}
        onCancel={() => setHostPickerOpen(false)}
        onOk={handleModalRun}
        okText="Run"
        width={720}
      >
        <HostPicker
          records={records}
          selectedKeys={modalSelectedKeys}
          pageSize={5}
          onToggleRow={(rec) => {
            const key = recordKey(rec);
            setModalSelectedKeys((prev) => ({ ...prev, [key]: !prev[key] }));
          }}
        />
      </Modal>
    </Layout>
  );
}
