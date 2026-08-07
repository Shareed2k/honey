/* eslint-disable @typescript-eslint/no-explicit-any */
import { useState, useCallback, useEffect } from 'react';
import { message } from 'antd';
import { apiGet, apiPost } from '../api/core';
import { fixRecipeErrors, generateRecipe, syncRecipeAST } from '../api/recipes';
import {
  useRecipeGraph,
  applyWaveLayout,
  buildFlowFromRecipe,
  buildRecipeFromFlow,
  collectAncestorNodeIDs,
  computeWavesFromEdges,
  createStepDraft,
  recipeNameFromFilename,
  recipeStudioSnippets,
  uniqueStepID,
  type StepDraft,
} from './useRecipeGraph';
import type { RiskReport } from '../api/types/core';

type ValidationIssue = {
  path?: string;
  kind?: string;
  message: string;
};

type ValidationState = 'idle' | 'validating' | 'valid' | 'invalid';
type NodeRunStatus = 'running' | 'ok' | 'err' | 'skipped';

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

export function useRecipeStudioEngine() {
  const { nodes, setNodes, onNodesChange, edges, setEdges, onEdgesChange, onConnect, stepData, setStepData, resetGraph } = useRecipeGraph();
  const [selectedNodeId, setSelectedNodeId] = useState<string | null>(null);
  
  const [schema, setSchema] = useState<any>(null);
  const [recipeDefaults, setRecipeDefaults] = useState<any>({});
  
  const [rawMode, setRawMode] = useState(false);
  const [rawContent, setRawContent] = useState('');
  const [originalCue, setOriginalCue] = useState<string>('');
  
  const [validating, setValidating] = useState(false);
  const [validationIssues, setValidationIssues] = useState<ValidationIssue[]>([]);
  const [validationState, setValidationState] = useState<ValidationState>('idle');
  const [validationRisk, setValidationRisk] = useState<RiskReport | undefined>(undefined);
  
  const [fixBusy, setFixBusy] = useState(false);
  const [generateBusy, setGenerateBusy] = useState(false);
  
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
      setOriginalCue(data.raw_cue || '');
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
  }, [edges, nodes, recipeDefaults, selectedRecipe, stepData]);

  const handleSwitchToRaw = async () => {
    try {
      const visualJSON = buildRecipeJSON();
      
      let newRaw = '';
      if (originalCue) {
        try {
          newRaw = await syncRecipeAST(originalCue, visualJSON);
        } catch (syncErr) {
          console.warn("AST Sync failed, falling back to JSON:", syncErr);
          newRaw = JSON.stringify(visualJSON, null, 2);
        }
      } else {
        newRaw = JSON.stringify(visualJSON, null, 2);
      }
      
      setRawContent(newRaw);
      setRawMode(true);
      setSelectedNodeId(null);
    } finally {
      // Done
    }
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

    let reqBody: any;
    if (rawMode) {
      reqBody = { recipe_content_raw: rawContent };
    } else {
      try {
        reqBody = { recipe_content: buildRecipeJSON() };
      } catch (err: any) {
        const issue = { kind: 'json', message: err?.message || 'Invalid visual structure' };
        setValidationIssues([issue]);
        setValidationState('invalid');
        setValidationRisk(undefined);
        if (!quiet) {
          message.error('Validation failed: ' + issue.message);
        }
        return;
      }
    }

    setValidating(true);
    setValidationState('validating');
    setValidationRisk(undefined);
    try {
      const res = await apiPost('/api/v1/recipes/validate-content', reqBody);
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
  }, [applyValidationResultToNodes, buildRecipeJSON, nodes.length, rawMode, rawContent]);

  useEffect(() => {
    const timer = window.setTimeout(() => {
      void validateCurrentRecipe(true);
    }, 600);
    return () => window.clearTimeout(timer);
  }, [validateCurrentRecipe]);

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
    let contentStr: string;
    if (rawMode) {
      contentStr = rawContent;
    } else {
      const content = buildRecipeJSON();
      contentStr = JSON.stringify(content, null, 2);
    }
    
    let url = `/api/v1/recipes/store/${encodeURIComponent(options.path)}`;
    if (options.storage === 'git') {
      url += `?git_url=${encodeURIComponent(options.gitUrl || '')}&git_branch=${encodeURIComponent(options.gitBranch || '')}`;
    }
    const res = await apiPost(url, { content: contentStr });
    if (!res.ok) {
      const err = await res.text();
      throw new Error(err);
    }
    loadRecipesList();
  };

  const handleReset = () => {
    resetGraph();
    setSelectedNodeId(null);
    setSelectedRecipe(undefined);
    setRecipeDefaults({});
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
      message.success(`Successfully loaded ${options.path}!`);
    } catch (err: any) {
      message.error('Failed to load git recipe: ' + err.message);
      throw err;
    }
  };

  const handleFixWithAI = async (intent: string = "") => {
    setFixBusy(true);
    try {
      const res = await fixRecipeErrors(buildRecipeJSON(), validationIssues, intent);
      setRawContent(JSON.stringify(res.recipe, null, 2));
      handleSwitchToVisual();
      message.success("AI Fix applied: " + res.explanation);
    } catch (err) {
      message.error("AI Fix failed: " + (err as Error).message);
    } finally {
      setFixBusy(false);
    }
  };

  const handleGenerateAI = async (intent: string) => {
    setGenerateBusy(true);
    try {
      const res = await generateRecipe(intent, "");
      setRawContent(JSON.stringify(res.recipe, null, 2));
      handleSwitchToVisual();
      message.success("AI Generation applied: " + res.explanation);
    } catch (err) {
      message.error("AI Generation failed: " + (err as Error).message);
      throw err;
    } finally {
      setGenerateBusy(false);
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

  return {
    // state
    nodes, edges, stepData,
    selectedNodeId, schema, recipeDefaults,
    rawMode, rawContent, originalCue,
    validating, validationIssues, validationState, validationRisk,
    fixBusy, generateBusy,
    availableRecipes, selectedRecipe,

    // setters
    setSelectedNodeId, setRecipeDefaults, setRawContent, setRawMode, setOriginalCue,
    setSelectedRecipe,

    // actions
    onNodesChange, onEdgesChange, onConnect,
    doLoadRecipe, handleStepDataChange, buildRecipeJSON,
    handleSwitchToRaw, handleSwitchToVisual, setNodeRunStatus,
    validateCurrentRecipe, addStepNode, addSnippet,
    handleSaveRecipe, handleReset, doGitLoad,
    handleFixWithAI, handleGenerateAI,
    buildSubgraphRecipe, buildDownstreamRecipe,
    
    // exposing extra setters needed for manual operations (e.g. library load)
    setNodes, setEdges, setStepData
  };
}