/* eslint-disable @typescript-eslint/no-explicit-any */
import React, { useState, useEffect, useCallback } from 'react';
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
import { Layout, Button, Drawer, Space, Typography, Select, message } from 'antd';
import { PlusOutlined, SaveOutlined, SyncOutlined, PlayCircleOutlined, CloudDownloadOutlined, UndoOutlined, SettingOutlined } from '@ant-design/icons';

import CustomStepNode from './CustomStepNode';
import DynamicStepForm from './DynamicStepForm';
import StorageModal from './StorageModal';
import GitLoadModal from './GitLoadModal';
import { StepRun } from '../RecipesTab/StepRun';
import { apiGet, apiPost } from '../api';
import {
  applyWaveLayout,
  buildRecipeFromFlow,
  createStepDraft,
  detectStepKind,
  listStepKinds,
  recipeNameFromFilename,
  stepSchemaForKind,
  type StepDraft,
} from './recipeStudioUtils';

const { Header, Content, Sider } = Layout;
const { Title, Text } = Typography;

const nodeTypes = {
  step: CustomStepNode,
};

type Props = {
  selectedRecords?: any[];
  sshUser?: string;
};

export default function StudioWorkspace({ selectedRecords = [], sshUser = 'root' }: Props) {
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
  const loadRecipe = async (name: string) => {
    try {
      setSelectedRecipe(name);
      const res = await apiGet(`/api/v1/recipes/store/${encodeURIComponent(name)}`);
      if (!res.ok) {
        throw new Error(await res.text());
      }
      const rawText = await res.text();
      let recipeJson: any;
      try {
        recipeJson = JSON.parse(rawText);
      } catch {
        // If the file is stored as raw CUE/YAML, parse it via backend parse API
        const parseRes = await apiPost('/api/v1/recipes/parse', { content: rawText });
        if (parseRes.ok) {
          const parsed = await parseRes.json();
          recipeJson = parsed.recipe;
        } else {
          throw new Error(await parseRes.text());
        }
      }

      if (!recipeJson || !recipeJson.steps) {
        message.warning('Selected file is not a valid graph recipe');
        return;
      }

      if (recipeJson.defaults) {
        setRecipeDefaults(recipeJson.defaults);
      } else {
        setRecipeDefaults({});
      }

      // Visual Deconstructor Loop
      const newNodes: any[] = [];
      const newEdges: any[] = [];
      const newStepData: Record<string, StepDraft> = {};

      recipeJson.steps.forEach((step: any, index: number) => {
        const id = step.id || `step_${index + 1}`;
        
        const kind = detectStepKind(step);

        newNodes.push({
          id,
          type: 'step',
          position: { x: 100 + index * 220, y: 150 },
          data: { label: id, kind, host: step.host || '_' },
        });

        if (step.depends) {
          step.depends.forEach((depId: string) => {
            newEdges.push({
              id: `edge_from_${depId}_to_${id}`,
              source: depId,
              target: id,
            });
          });
        }

        newStepData[id] = { ...step, id, kind, host: step.host || '_' };
      });

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
      message.success(`Successfully loaded ${name}!`);
    } catch (err: any) {
      message.error('Failed to load recipe: ' + err.message);
    }
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

      const newNodes: any[] = [];
      const newEdges: any[] = [];
      const newStepData: Record<string, StepDraft> = {};

      recipeJson.steps.forEach((step: any, index: number) => {
        const id = step.id || `step_${index + 1}`;
        const kind = detectStepKind(step);

        newNodes.push({
          id,
          type: 'step',
          position: { x: 100 + index * 220, y: 150 },
          data: { label: id, kind, host: step.host || '_' },
        });

        if (step.depends) {
          step.depends.forEach((depId: string) => {
            newEdges.push({
              id: `edge_from_${depId}_to_${id}`,
              source: depId,
              target: id,
            });
          });
        }

        newStepData[id] = { ...step, id, kind, host: step.host || '_' };
      });

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
          <Button type="default" icon={<SettingOutlined />} onClick={() => setSettingsDrawerOpen(true)}>
            Recipe Settings
          </Button>
          <Button type="default" icon={<UndoOutlined />} onClick={handleReset}>
            Reset
          </Button>
          <Button type="primary" icon={<SyncOutlined />} loading={validating} onClick={handleValidate}>
            Validate Graph
          </Button>
          <Button type="primary" icon={<SaveOutlined />} onClick={() => setSaveModalVisible(true)}>
            Save Recipe
          </Button>
        </Space>
      </Header>

      <Layout>
        <Sider width={220} style={{ background: '#001529', borderRight: '1px solid #1f2937', padding: '16px' }}>
          <Title level={5} style={{ color: '#f0f6fc', marginTop: 0 }}>Toolbox (Drag/Click)</Title>
          <Text style={{ color: '#8b949e' }}>Drop steps onto canvas</Text>
          <div style={{ display: 'flex', flexDirection: 'column', gap: 10, marginTop: 16 }}>
            {listStepKinds(schema).map((stepKind) => (
              <Button
                key={stepKind.kind}
                ghost
                icon={<PlusOutlined />}
                style={{ color: '#fff', borderColor: 'rgba(255,255,255,0.72)' }}
                onClick={() => addStepNode(stepKind.kind)}
              >
                {stepKind.label}
              </Button>
            ))}
          </div>
        </Sider>

        <Content style={{ position: 'relative', height: '100%', background: '#0d1117' }}>
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
              onClick={() => {
                setRunStepId(selectedNodeId);
                setRunCount(c => c + 1);
                setRunPanelOpen(true);
              }}
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
              hosts={selectedRecords}
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
          schema={schema?.definitions?.defaults}
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
    </Layout>
  );
}
