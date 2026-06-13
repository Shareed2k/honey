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
import { Layout, Button, Drawer, Space, Typography, Select, message, Modal, Alert, Input } from 'antd';
import { PlusOutlined, SaveOutlined, SyncOutlined, PlayCircleOutlined, CloudDownloadOutlined, UndoOutlined, SettingOutlined, CodeOutlined, ReadOutlined, FireOutlined } from '@ant-design/icons';

import CustomStepNode from './CustomStepNode';
import DynamicStepForm from './DynamicStepForm';
import StorageModal from './StorageModal';
import GitLoadModal from './GitLoadModal';
import { LibraryModal } from './LibraryModal';
import { ParameterPromptModal } from './ParameterPromptModal';
import { StepRun } from '../RecipesTab/StepRun';
import { HostPicker, recordKey, type HostRecord } from '../HostPicker';
import { apiGet, apiPost, fixRecipeErrors, generateRecipe } from '../api';
import {
  applyWaveLayout,
  buildFlowFromRecipe,
  buildRecipeFromFlow,
  collectAncestorNodeIDs,
  computeWavesFromEdges,
  createStepDraft,
  listStepKinds,
  recipeNameFromFilename,
  recipeStudioSnippets,
  stepSchemaForKind,
  uniqueStepID,
  type StepDraft,
} from './recipeStudioUtils';
import type { HostExecResultRow, RiskReport } from '../api';

const CodeEditor = lazy(() => import('../CodeEditor'));

const { Header, Content, Sider } = Layout;
const { Title, Text } = Typography;

const nodeTypes = {
  step: CustomStepNode,
};

type ValidationIssue = {
  path?: string;
  kind?: string;
  message: string;
};

type ValidationState = 'idle' | 'validating' | 'valid' | 'invalid';
type NodeRunStatus = 'running' | 'ok' | 'err' | 'skipped';

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
  const [validationIssues, setValidationIssues] = useState<ValidationIssue[]>([]);
  const [validationState, setValidationState] = useState<ValidationState>('idle');
  const [validationRisk, setValidationRisk] = useState<RiskReport | undefined>(undefined);
  const [snippetChoice, setSnippetChoice] = useState<string | undefined>(undefined);
  const [promptsOpen, setPromptsOpen] = useState(false);
  const [pendingRun, setPendingRun] = useState<{stepId: string, hosts: HostRecord[]} | null>(null);
  const [runExtraEnv, setRunExtraEnv] = useState<{key: string, value: string}[]>([]);
  const [runMode, setRunMode] = useState<'upstream' | 'downstream'>('upstream');

  const [fixBusy, setFixBusy] = useState(false);
  const [generateModalOpen, setGenerateModalOpen] = useState(false);
  const [intent, setIntent] = useState("");
  const [generateBusy, setGenerateBusy] = useState(false);
  const [libraryOpen, setLibraryOpen] = useState(false);

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
  const buildRecipeJSON = useCallback(() => {
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
  }, [edges, nodes, rawContent, rawMode, recipeDefaults, selectedRecipe, stepData]);

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
    const visited = collectAncestorNodeIDs(edges, targetId);
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

  const buildDownstreamRecipe = (targetId: string) => {
    const visited = new Set<string>([targetId]);
    const visit = (id: string) => {
      for (const edge of edges as any[]) {
        if (edge.source === id && !visited.has(edge.target)) {
          visited.add(edge.target);
          visit(edge.target);
        }
      }
    };
    visit(targetId);

    const subNodes = nodes.filter((n: any) => visited.has(n.id));
    const subEdges = edges.filter((e: any) => visited.has(e.source) && visited.has(e.target));

    const base = buildRecipeFromFlow({
      name: recipeNameFromFilename(selectedRecipe) + '-resume',
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

  const setNodeRunStatus = (ids: Set<string>, status: NodeRunStatus | undefined) => {
    setNodes((nds) =>
      nds.map((node) => {
        if (!ids.has(node.id)) return node;
        return {
          ...node,
          data: { ...node.data, runStatus: status },
        };
      }),
    );
  };

  const prepareStepRun = (stepId: string, hosts: HostRecord[]) => {
    const prompts = recipeDefaults?.prompts;
    if (prompts && Object.keys(prompts).length > 0) {
      setPendingRun({ stepId, hosts });
      setPromptsOpen(true);
      return;
    }
    doPrepareStepRun(stepId, hosts, []);
  };

  const doPrepareStepRun = (stepId: string, hosts: HostRecord[], extraEnv: {key: string, value: string}[]) => {
    const ids = collectAncestorNodeIDs(edges, stepId);
    setNodeRunStatus(ids, 'running');
    setRunHosts(hosts);
    setRunStepId(stepId);
    setRunCount((c) => c + 1);
    setRunPanelOpen(true);
    setRunExtraEnv(extraEnv);
  };

  const handleRunRow = (row: HostExecResultRow) => {
    const stepId = row.StepID || (row.StepIndex ? nodes[row.StepIndex - 1]?.id : '');
    if (!stepId) {
      return;
    }
    const status: NodeRunStatus = row.Skipped ? 'skipped' : row.Success ? 'ok' : 'err';
    setNodeRunStatus(new Set([stepId]), status);
  };

  const handleRunStep = (stepId: string) => {
    setRunMode('upstream');
    if (selectedRecords.length > 0) {
      prepareStepRun(stepId, selectedRecords as HostRecord[]);
    } else {
      setPendingRunStepId(stepId);
      setModalSelectedKeys({});
      setHostPickerOpen(true);
    }
  };

  const handleResumeStep = (stepId: string) => {
    setRunMode('downstream');
    if (selectedRecords.length > 0) {
      prepareStepRun(stepId, selectedRecords as HostRecord[]);
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
    prepareStepRun(pendingRunStepId!, modalHosts);
    setHostPickerOpen(false);
  };

  const applyValidationResultToNodes = useCallback((data: any, errors: ValidationIssue[], layout: boolean) => {
    const waveByNode = graphWaveByNode(data?.graph);
    setNodes((nds) => {
      const mapped = nds.map((n, index) => {
        const err = errorForNode(errors, n.id, index);
        const stepSummary = data?.steps?.find((s: any) => s.id === n.id);
        return {
          ...n,
          data: {
            ...n.data,
            wave: waveByNode[n.id] ?? stepSummary?.wave ?? n.data?.wave,
            error: err?.message,
          },
        };
      });
      const next = layout ? applyWaveLayout(mapped) : mapped;
      return nodesEqualForValidation(nds, next) ? nds : next;
    });
  }, [setNodes]);

  // 7. Real-time validation loop
  const validateCurrentRecipe = useCallback(async (quiet = false) => {
    if (!rawMode && nodes.length === 0) {
      setValidationIssues([]);
      setValidationState('idle');
      setValidationRisk(undefined);
      return;
    }

    let recipeContent: any;
    try {
      recipeContent = buildRecipeJSON();
    } catch (err: any) {
      const issue = { kind: 'json', message: err?.message || 'Invalid JSON' };
      setValidationIssues([issue]);
      setValidationState('invalid');
      setValidationRisk(undefined);
      if (!quiet) {
        message.error('Validation failed: ' + issue.message);
      }
      return;
    }

    setValidating(true);
    setValidationState('validating');
    setValidationRisk(undefined);
    try {
      const res = await apiPost('/api/v1/recipes/validate-content', { recipe_content: recipeContent });
      const data = await res.json();
      const errors: ValidationIssue[] = Array.isArray(data.errors) ? data.errors : [];
      if (!res.ok || errors.length > 0) {
        const issues = errors.length > 0 ? errors : [{ kind: 'validation', message: res.statusText }];
        setValidationIssues(issues);
        setValidationState('invalid');
        setValidationRisk(data.risk);
        applyValidationResultToNodes(data, issues, !quiet);
        if (!quiet) {
          message.warning('Recipe contains validation issues');
        }
      } else {
        setValidationIssues([]);
        setValidationState('valid');
        setValidationRisk(data.risk);
        applyValidationResultToNodes(data, [], !quiet);
        if (!quiet) {
          message.success('Recipe is fully valid & verified!');
        }
      }
    } catch (err: any) {
      const issue = { kind: 'network', message: err?.message || String(err) };
      setValidationIssues([issue]);
      setValidationState('invalid');
      setValidationRisk(undefined);
      if (!quiet) {
        message.error('Validation failed: ' + issue.message);
      }
    } finally {
      setValidating(false);
    }
  }, [applyValidationResultToNodes, buildRecipeJSON, nodes.length, rawMode]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void validateCurrentRecipe(true);
    }, 600);
    return () => window.clearTimeout(timer);
  }, [validateCurrentRecipe]);

  const handleValidate = () => {
    void validateCurrentRecipe(false);
  };

  // 8. Draggable Toolbar Add Step
  const addStepNode = (kind: string) => {
    const id = uniqueStepID(`${kind}_${nodes.length + 1}`, new Set(nodes.map((node) => node.id)));
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

  const addSnippet = (snippetId: string) => {
    const snippet = recipeStudioSnippets.find((s) => s.id === snippetId);
    setSnippetChoice(undefined);
    if (!snippet) return;

    const usedIDs = new Set(nodes.map((node) => node.id));
    const idMap = new Map<string, string>();
    for (const step of snippet.steps) {
      idMap.set(step.id, uniqueStepID(step.id, usedIDs));
    }

    const baseX = 100 + nodes.length * 220;
    const newNodes = snippet.steps.map((step, index) => {
      const id = idMap.get(step.id)!;
      return {
        id,
        type: 'step',
        position: { x: baseX + index * 220, y: 150 + (index % 2) * 90 },
        data: { label: id, kind: step.kind, host: step.host },
      };
    });
    const newEdges = snippet.edges.map((edge) => {
      const source = idMap.get(edge.source)!;
      const target = idMap.get(edge.target)!;
      return { id: `edge_from_${source}_to_${target}`, source, target };
    });
    const newStepData = Object.fromEntries(
      snippet.steps.map((step) => {
        const id = idMap.get(step.id)!;
        return [id, remapSnippetStep(step, id, idMap)];
      }),
    );

    setNodes((nds) => [...nds, ...newNodes]);
    setEdges((eds) => [...eds, ...newEdges]);
    setStepData((prev) => ({ ...prev, ...newStepData }));
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
        <Space size="small">
          <Title level={5} style={{ color: '#fff', margin: 0 }}>CUE Recipe Studio</Title>
          <Select
            placeholder="Load existing recipe..."
            style={{ width: 200 }}
            value={selectedRecipe}
            onChange={loadRecipe}
            options={availableRecipes.map((r) => ({ value: r.name, label: r.name }))}
            allowClear
          />
          <Button type="default" icon={<CloudDownloadOutlined />} onClick={() => setGitLoadModalVisible(true)}>
            Load from Git
          </Button>
          <Button type="default" icon={<ReadOutlined />} onClick={() => setLibraryOpen(true)}>
            Library
          </Button>
          <Button type="default" icon={<FireOutlined />} onClick={() => setGenerateModalOpen(true)}>
            Generate
          </Button>
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

      <div className="studio-validation-strip">
        {validationState === 'validating' ? (
          <Alert type="info" showIcon message="Validating recipe..." />
        ) : validationIssues.length > 0 ? (
          <div style={{ display: 'flex', alignItems: 'flex-start' }}>
            <Alert
              type="error"
              showIcon
              message={`${validationIssues.length} validation issue${validationIssues.length === 1 ? '' : 's'}`}
              description={
                <ul className="studio-validation-list">
                  {validationIssues.slice(0, 4).map((issue, index) => (
                    <li key={`${issue.kind || 'issue'}-${index}`}>
                      {issue.path ? `${issue.path}: ` : ''}
                      {issue.message}
                    </li>
                  ))}
                </ul>
              }
              style={{ flex: 1 }}
            />
            <Button
              size="small"
              type="primary"
              style={{ marginLeft: 16, marginTop: 8 }}
              loading={fixBusy}
              onClick={async () => {
                setFixBusy(true);
                try {
                  const res = await fixRecipeErrors(buildRecipeJSON(), validationIssues, "");
                  setRawContent(JSON.stringify(res.recipe, null, 2));
                  handleSwitchToVisual();
                  message.success("AI Fix applied: " + res.explanation);
                } catch (err) {
                  message.error("AI Fix failed: " + (err as Error).message);
                } finally {
                  setFixBusy(false);
                }
              }}
            >
              ✨ Fix with AI
            </Button>
          </div>
        ) : validationState === 'valid' ? (
          <Alert type="success" showIcon message="Recipe is valid" />
        ) : null}
        
        {validationRisk && validationRisk.score > 0 && (
          <div style={{ marginTop: 8 }}>
            <Alert
              type={validationRisk.level === 'High' ? 'error' : validationRisk.level === 'Medium' ? 'warning' : 'info'}
              showIcon
              message={`Risk Level: ${validationRisk.level} (Score: ${validationRisk.score})`}
              description={
                <ul style={{ margin: 0, paddingLeft: 20, marginTop: 8 }}>
                  {validationRisk.findings.map((f: string, i: number) => <li key={i}>{f}</li>)}
                </ul>
              }
            />
          </div>
        )}
      </div>

      <Layout style={{ flex: 1, minHeight: 0, overflow: 'hidden' }}>
        <Sider width={220} style={{ background: '#001529', borderRight: '1px solid #1f2937', padding: '16px' }}>
          <Title level={5} style={{ color: '#f0f6fc', marginTop: 0 }}>Toolbox (Drag/Click)</Title>
          <Text style={{ color: '#8b949e' }}>Drop steps onto canvas</Text>
          <Select
            placeholder="Insert snippet"
            style={{ width: '100%', marginTop: 12 }}
            value={snippetChoice}
            onChange={(value) => {
              setSnippetChoice(value);
              addSnippet(value);
            }}
            options={recipeStudioSnippets.map((snippet) => ({ value: snippet.id, label: snippet.label }))}
            allowClear
          />
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
                language="cue"
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
            <Button
              type="default"
              icon={<PlayCircleOutlined />}
              style={{ marginTop: 8, width: '100%' }}
              onClick={() => handleResumeStep(selectedNodeId!)}
            >
              Resume from here
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
              recipe={(runMode === 'downstream' ? buildDownstreamRecipe(runStepId) : buildSubgraphRecipe(runStepId)) as any}
              recipeBasePath={null}
              hosts={runHosts}
              envOverrides={runExtraEnv}
              sshUser={sshUser}
              recordSession={false}
              sessionRecordingAvailable={false}
              onViewRecording={() => {}}
              onRunAgain={() => {
                if (runStepId) {
                  setNodeRunStatus(collectAncestorNodeIDs(edges, runStepId), 'running');
                }
                setRunCount(c => c + 1);
              }}
              onStartNew={() => setRunPanelOpen(false)}
              onRow={handleRunRow}
              onStatusChange={(status) => {
                if (status === 'err' && runStepId) {
                  setNodeRunStatus(collectAncestorNodeIDs(edges, runStepId), 'err');
                }
              }}
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

      <LibraryModal
        open={libraryOpen}
        onCancel={() => setLibraryOpen(false)}
        onSelect={async (libRecipe) => {
          setLibraryOpen(false);
          try {
            setRawContent(libRecipe.content);
            setRawMode(true);
            setTimeout(async () => {
               try {
                 const parseRes = await apiPost('/api/v1/recipes/parse', { content: libRecipe.content });
                 if (!parseRes.ok) throw new Error(await parseRes.text());
                 const parseData = await parseRes.json();
                 setRawContent(JSON.stringify(parseData.recipe, null, 2));
                 message.success(`Loaded ${libRecipe.name} from Library (CUE loaded into Raw view)`);
               } catch (e) {
                 message.success(`Loaded ${libRecipe.name} (Switch to visual manually after fixing validation if needed)`);
               }
            }, 50);
          } catch (err) {
            message.error("Failed to load library recipe: " + (err as Error).message);
          }
        }}
      />

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

      <ParameterPromptModal
        open={promptsOpen}
        prompts={recipeDefaults?.prompts || {}}
        onCancel={() => setPromptsOpen(false)}
        onSubmit={(vals) => {
          setPromptsOpen(false);
          const extra = Object.entries(vals).map(([k, v]) => ({ key: k, value: v }));
          if (pendingRun) {
            doPrepareStepRun(pendingRun.stepId, pendingRun.hosts, extra);
            setPendingRun(null);
          }
        }}
      />

      <Modal
        title="Generate Recipe with AI"
        open={generateModalOpen}
        onCancel={() => setGenerateModalOpen(false)}
        okText="Generate"
        confirmLoading={generateBusy}
        onOk={async () => {
          if (!intent.trim()) return;
          setGenerateBusy(true);
          try {
            const res = await generateRecipe(intent, "");
            setRawContent(JSON.stringify(res.recipe, null, 2));
            handleSwitchToVisual(); // Render the new JSON
            message.success("AI Generation applied: " + res.explanation);
            setGenerateModalOpen(false);
            setIntent("");
          } catch (err) {
            message.error("AI Generation failed: " + (err as Error).message);
          } finally {
            setGenerateBusy(false);
          }
        }}
      >
        <Input.TextArea
          value={intent}
          onChange={(e) => setIntent(e.target.value)}
          placeholder="Describe what you want to automate... e.g., Restart all nginx pods"
          rows={4}
        />
      </Modal>
    </Layout>
  );
}

function graphWaveByNode(graph: any): Record<string, number> {
  const waves: any[][] = Array.isArray(graph?.waves) ? graph.waves : [];
  const out: Record<string, number> = {};
  waves.forEach((wave, waveIndex) => {
    wave.forEach((node) => {
      const id = typeof node === 'string' ? node : node?.id;
      if (id) out[id] = waveIndex + 1;
    });
  });
  return out;
}

function errorForNode(errors: ValidationIssue[], nodeId: string, index: number): ValidationIssue | undefined {
  return errors.find((err) => {
    const haystack = `${err.path || ''} ${err.message || ''}`;
    return haystack.includes(nodeId) || haystack.includes(`step ${index + 1}`) || haystack.includes(`steps[${index}]`);
  });
}

function nodesEqualForValidation(prev: any[], next: any[]): boolean {
  if (prev.length !== next.length) return false;
  return prev.every((node, index) => {
    const other = next[index];
    return (
      node.id === other.id &&
      node.position?.x === other.position?.x &&
      node.position?.y === other.position?.y &&
      node.data?.wave === other.data?.wave &&
      node.data?.error === other.data?.error
    );
  });
}

function remapSnippetStep(step: StepDraft, id: string, idMap: Map<string, string>): StepDraft {
  const remapped = remapSnippetValue(step, idMap) as StepDraft;
  return { ...remapped, id };
}

function remapSnippetValue(value: unknown, idMap: Map<string, string>): unknown {
  if (typeof value === 'string') {
    return idMap.get(value) || value;
  }
  if (Array.isArray(value)) {
    return value.map((item) => remapSnippetValue(item, idMap));
  }
  if (value && typeof value === 'object') {
    return Object.fromEntries(
      Object.entries(value).map(([key, item]) => [key, remapSnippetValue(item, idMap)]),
    );
  }
  return value;
}
