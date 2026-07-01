/* eslint-disable @typescript-eslint/no-explicit-any */
import React, { useState, lazy, Suspense } from 'react';
import { 
  ReactFlow, 
  Controls, 
  Background
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
import { apiPost } from '../api/core';
import { listStepKinds, stepSchemaForKind } from '../api/recipes';
import {
  applyWaveLayout,
  buildFlowFromRecipe,
  recipeNameFromFilename,
  recipeStudioSnippets,
  collectAncestorNodeIDs
} from './useRecipeGraph';
import type { HostExecResultRow } from '../api/types/exec';

import { useHostSelection } from '../contexts/HostSelectionContext';
import { useRecipeStudioEngine } from './useRecipeStudioEngine';

const CodeEditor = lazy(() => import('../CodeEditor'));

const { Header, Content, Sider } = Layout;
const { Title, Text } = Typography;

const nodeTypes = {
  step: CustomStepNode,
};

type NodeRunStatus = 'running' | 'ok' | 'err' | 'skipped';

export default function StudioWorkspace() {
  const { records = [], selectedRecords = [], sshUser = 'root' } = useHostSelection();
  
  const engine = useRecipeStudioEngine();
  const {
    nodes, edges, stepData,
    selectedNodeId, schema, recipeDefaults,
    rawMode, rawContent,
    validating, validationIssues, validationState, validationRisk,
    fixBusy, generateBusy,
    availableRecipes, selectedRecipe,
    
    setSelectedNodeId, setRecipeDefaults, setRawContent, setRawMode, setOriginalCue,
    
    onNodesChange, onEdgesChange, onConnect,
    doLoadRecipe, handleStepDataChange,
    handleSwitchToRaw, handleSwitchToVisual, setNodeRunStatus,
    validateCurrentRecipe, addStepNode, addSnippet,
    handleSaveRecipe, handleReset, doGitLoad,
    handleFixWithAI, handleGenerateAI,
    buildSubgraphRecipe, buildDownstreamRecipe,
    
    setNodes, setEdges, setStepData
  } = engine;

  const [saveModalVisible, setSaveModalVisible] = useState(false);
  const [gitLoadModalVisible, setGitLoadModalVisible] = useState(false);
  const [runPanelOpen, setRunPanelOpen] = useState(false);
  const [runStepId, setRunStepId] = useState<string | null>(null);
  const [runCount, setRunCount] = useState(0);
  const [settingsDrawerOpen, setSettingsDrawerOpen] = useState(false);
  
  const [runHosts, setRunHosts] = useState<HostRecord[]>([]);
  const [hostPickerOpen, setHostPickerOpen] = useState(false);
  const [pendingRunStepId, setPendingRunStepId] = useState<string | null>(null);
  const [modalSelectedKeys, setModalSelectedKeys] = useState<Record<string, boolean>>({});
  
  const [snippetChoice, setSnippetChoice] = useState<string | undefined>(undefined);
  const [promptsOpen, setPromptsOpen] = useState(false);
  const [pendingRun, setPendingRun] = useState<{stepId: string, hosts: HostRecord[]} | null>(null);
  const [runExtraEnv, setRunExtraEnv] = useState<{key: string, value: string}[]>([]);
  const [runMode, setRunMode] = useState<'upstream' | 'downstream'>('upstream');

  const [generateModalOpen, setGenerateModalOpen] = useState(false);
  const [intent, setIntent] = useState("");
  const [libraryOpen, setLibraryOpen] = useState(false);

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

  const onNodeClick = (_: any, node: any) => {
    setSelectedNodeId(node.id);
  };

  const modalHosts = records.filter((r) => modalSelectedKeys[recordKey(r)]);

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

  const handleValidate = () => {
    void validateCurrentRecipe(false);
  };

  const internalHandleReset = () => {
    handleReset();
    setRunPanelOpen(false);
    setRunStepId(null);
    setRunCount(0);
    setSettingsDrawerOpen(false);
    message.info('Canvas reset');
  };

  const internalHandleGitLoad = async (options: {
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
        onOk: () => {
          doGitLoad(options)
            .then(() => setGitLoadModalVisible(false))
            .catch(() => {});
        },
      });
      return;
    }
    doGitLoad(options)
      .then(() => setGitLoadModalVisible(false))
      .catch(() => {});
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
          <Button type="default" icon={<UndoOutlined />} onClick={internalHandleReset}>
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
              onClick={() => handleFixWithAI()}
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
        onSave={(options) => {
          handleSaveRecipe(options)
            .then(() => setSaveModalVisible(false))
            .catch((err) => message.error(err.message));
        }}
      />

      {gitLoadModalVisible && (
        <GitLoadModal
          visible={gitLoadModalVisible}
          onCancel={() => setGitLoadModalVisible(false)}
          onLoad={internalHandleGitLoad}
        />
      )}

      <LibraryModal
        open={libraryOpen}
        onCancel={() => setLibraryOpen(false)}
        onSelect={async (libRecipe) => {
          setLibraryOpen(false);
          try {
            setRawContent(libRecipe.content);
            setOriginalCue(libRecipe.content);
            setRawMode(true);
            setTimeout(async () => {
               try {
                 const parseRes = await apiPost('/api/v1/recipes/parse', { content: libRecipe.content });
                 if (!parseRes.ok) throw new Error(await parseRes.text());
                 const parseData = await parseRes.json();
                 
                 // Apply the parsed visual nodes, but DO NOT overwrite rawContent with JSON!
                 const recipeJson = parseData.recipe;
                 if (recipeJson.defaults) {
                   setRecipeDefaults(recipeJson.defaults);
                 } else {
                   setRecipeDefaults({});
                 }
                 const { nodes: newNodes, edges: newEdges, stepData: newStepData } = buildFlowFromRecipe(recipeJson);
                 setNodes(applyWaveLayout(newNodes));
                 setEdges(newEdges);
                 setStepData(newStepData);
                 
                 message.success(`Loaded ${libRecipe.name} from Library`);
               } catch {
                 message.success(`Loaded ${libRecipe.name} (Switch to visual manually after fixing validation if needed)`);
               }
            }, 50);
          } catch (err) {
            message.error("Failed to load library recipe: " + (err as Error).message);
          }
        }}
      />

      <Modal maskClosable={false}         title="Select hosts to run on"
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
        recipeName={recipeNameFromFilename(selectedRecipe)}
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

      <Modal maskClosable={false}         title="Generate Recipe with AI"
        open={generateModalOpen}
        onCancel={() => setGenerateModalOpen(false)}
        okText="Generate"
        confirmLoading={generateBusy}
        onOk={() => {
          if (!intent.trim()) return;
          handleGenerateAI(intent)
            .then(() => {
              setGenerateModalOpen(false);
              setIntent("");
            })
            .catch(() => {});
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
