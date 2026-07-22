import { create } from 'zustand';
import { message } from 'antd';
import type { DocState, RunStatus } from './types';
import { parseDiskRecipe, syncRecipeAST, validateRecipeContent } from '../../api/recipes';
import { apiPost } from '../../api/core';
import type { ParsedRecipe } from '../../api/types/recipes';
import {
  buildFlowFromRecipe,
  buildRecipeFromFlow,
  computeWavesFromEdges,
  applyWaveLayout,
  recipeNameFromFilename,
  createStepDraft,
  uniqueStepID,
} from '../useRecipeGraph';

// Shape produced by buildFlowFromRecipe's `nodes` (typed `any[]` there since it
// spans several call sites) — named here so `.map` over it keeps `id`/`position`
// as known properties instead of losing them to index-signature spread erasure.
type FlowNode = { id: string; position: { x: number; y: number }; data?: Record<string, unknown> };

// helper: apply a patch to one doc; no-op if the id is unknown.
function patchDoc(
  set: (fn: (s: WorkspaceState) => Partial<WorkspaceState>) => void,
  id: string,
  patch: (d: DocState) => DocState,
) {
  set((s) => {
    const doc = s.docs[id];
    if (!doc) return {};
    return { docs: { ...s.docs, [id]: patch(doc) } };
  });
}

// Builds the same recipe-JSON shape switchToRaw/validate/save all need from a
// doc's visual-mode state (nodes/edges/stepData [+ defaults]). Shared here so
// the three call sites can't drift out of sync with each other.
function buildDocRecipe(doc: DocState): Record<string, unknown> {
  const base = buildRecipeFromFlow({
    name: recipeNameFromFilename(doc.recipeId),
    nodes: doc.nodes,
    edges: doc.edges,
    stepData: doc.stepData,
  });
  const recipe = base as unknown as Record<string, unknown>;
  if (doc.recipeDefaults && Object.keys(doc.recipeDefaults as object).length > 0) {
    recipe.defaults = doc.recipeDefaults;
  }
  return recipe;
}

function blankDoc(recipeId: string, name: string): DocState {
  return {
    recipeId, name,
    nodes: [], edges: [], stepData: {}, recipeDefaults: {},
    selectedNodeId: null,
    rawMode: false, rawContent: '', originalCue: '',
    // Fresh literal per call — must NOT share an array/object reference across docs,
    // or an in-place mutation on one doc's `issues` would corrupt every other doc.
    validation: { state: 'idle', issues: [] },
    runStatus: {}, dirty: false,
  };
}

let untitledCounter = 0;

interface WorkspaceState {
  docs: Record<string, DocState>;
  active: string | null;
  schema: unknown;

  createDoc(name: string): Promise<void>;
  newDoc(): string;
  freeDoc(id: string): void;
  setActive(id: string | null): void;
  setSchema(schema: unknown): void;

  setSelectedNode(id: string, nodeId: string | null): void;
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  setStepData(id: string, nodeId: string, value: any): void;
  addStep(id: string, kind: string): void;
  setRawContent(id: string, text: string): void;
  setRawMode(id: string, on: boolean): void;
  setNodeRunStatus(id: string, nodeIds: string[], status: RunStatus): void;
  markDirty(id: string): void;
  resetDoc(id: string): void;

  switchToRaw(id: string): Promise<void>;
  switchToVisual(id: string): void;

  validate(id: string): Promise<void>;
  save(
    id: string,
    options: { storage: string; path: string; commitMessage: string; gitUrl?: string; gitBranch?: string },
  ): Promise<void>;
}

export const useWorkspaceStore = create<WorkspaceState>((set, get) => ({
  docs: {},
  active: null,
  schema: null,

  async createDoc(name) {
    if (get().docs[name]) return; // idempotent — already open
    // parseDiskRecipe already unwraps the `{recipe: ...}` envelope server-side
    // and resolves to the ParsedRecipe itself (top-level `steps`/`defaults`) —
    // no further `.recipe` unwrap is needed or correct here.
    const recipeJson = await parseDiskRecipe(name);
    const { nodes, edges, stepData } = buildFlowFromRecipe(recipeJson);
    set((s) => ({
      docs: {
        ...s.docs,
        [name]: {
          ...blankDoc(name, recipeNameFromFilename(name)),
          nodes: applyWaveLayout(nodes),
          edges,
          stepData,
          recipeDefaults: recipeJson?.defaults ?? {},
        },
      },
    }));
  },

  newDoc() {
    const id = `untitled-${++untitledCounter}.cue`;
    set((s) => ({ docs: { ...s.docs, [id]: blankDoc(id, id) } }));
    return id;
  },

  freeDoc(id) {
    set((s) => {
      const docs = { ...s.docs };
      delete docs[id];
      const active = s.active === id ? null : s.active;
      return { docs, active };
    });
  },

  setActive(id) {
    set({ active: id });
  },

  setSchema(schema) {
    set({ schema });
  },

  setSelectedNode(id, nodeId) {
    patchDoc(set, id, (d) => ({ ...d, selectedNodeId: nodeId }));
  },
  setStepData(id, nodeId, value) {
    patchDoc(set, id, (d) => ({
      ...d,
      stepData: { ...d.stepData, [nodeId]: value },
      dirty: true,
    }));
  },
  addStep(id, kind) {
    patchDoc(set, id, (d) => {
      const used = new Set(d.nodes.map((n) => n.id));
      const newId = uniqueStepID(kind, used);
      const draft = createStepDraft(kind, newId);
      return {
        ...d,
        nodes: applyWaveLayout([
          ...d.nodes,
          { id: newId, type: 'step', position: { x: 0, y: 0 }, data: { label: newId, kind } },
        ]),
        stepData: { ...d.stepData, [newId]: draft },
        dirty: true,
      };
    });
  },
  setRawContent(id, text) {
    patchDoc(set, id, (d) => ({ ...d, rawContent: text, dirty: true }));
  },
  setRawMode(id, on) {
    patchDoc(set, id, (d) => ({ ...d, rawMode: on }));
  },
  setNodeRunStatus(id, nodeIds, status) {
    patchDoc(set, id, (d) => {
      const runStatus = { ...d.runStatus };
      for (const nid of nodeIds) runStatus[nid] = status;
      return { ...d, runStatus };
    });
  },
  markDirty(id) {
    patchDoc(set, id, (d) => ({ ...d, dirty: true }));
  },
  resetDoc(id) {
    patchDoc(set, id, (d) => {
      return blankDoc(d.recipeId, d.name);
    });
  },

  async switchToRaw(id) {
    const doc = get().docs[id];
    if (!doc) return;
    const visualJSON = buildDocRecipe(doc);
    let newRaw: string;
    if (doc.originalCue) {
      try {
        newRaw = await syncRecipeAST(doc.originalCue, visualJSON);
      } catch (syncErr) {
        console.warn('AST sync failed, falling back to JSON:', syncErr);
        newRaw = JSON.stringify(visualJSON, null, 2);
      }
    } else {
      newRaw = JSON.stringify(visualJSON, null, 2);
    }
    patchDoc(set, id, (d) => ({ ...d, rawContent: newRaw, rawMode: true, selectedNodeId: null }));
  },

  switchToVisual(id) {
    const doc = get().docs[id];
    if (!doc) return;
    try {
      const parsed = JSON.parse(doc.rawContent);
      const { nodes, edges, stepData } = buildFlowFromRecipe(parsed);
      const waveByNode = computeWavesFromEdges(nodes, edges);
      const withWave = (nodes as FlowNode[]).map((n) => ({
        ...n,
        data: { ...(n.data ?? {}), wave: waveByNode[n.id] ?? 1 },
      }));
      patchDoc(set, id, (d) => ({
        ...d,
        nodes: applyWaveLayout(withWave),
        edges,
        stepData,
        recipeDefaults: parsed.defaults ?? {},
        rawMode: false,
      }));
    } catch {
      message.error('Invalid JSON — fix errors before switching to visual mode');
    }
  },

  async validate(id) {
    const doc = get().docs[id];
    if (!doc) return;
    patchDoc(set, id, (d) => ({ ...d, validation: { ...d.validation, state: 'validating' } }));

    let recipe: ParsedRecipe;
    if (doc.rawMode) {
      try {
        recipe = JSON.parse(doc.rawContent) as ParsedRecipe;
      } catch {
        patchDoc(set, id, (d) => ({
          ...d,
          validation: { state: 'invalid', issues: [{ message: 'invalid JSON' }] },
        }));
        return;
      }
    } else {
      recipe = buildDocRecipe(doc) as unknown as ParsedRecipe;
    }

    try {
      const res = await validateRecipeContent(recipe);
      if ('errors' in res) {
        patchDoc(set, id, (d) => ({
          ...d,
          validation: {
            state: 'invalid',
            issues: res.errors.map((e) => ({ path: e.path, kind: e.kind, message: e.message })),
            risk: undefined,
          },
        }));
      } else {
        patchDoc(set, id, (d) => ({
          ...d,
          validation: { state: 'valid', issues: [], risk: res.risk },
        }));
      }
    } catch (err) {
      patchDoc(set, id, (d) => ({
        ...d,
        validation: { state: 'invalid', issues: [{ message: String(err) }] },
      }));
    }
  },

  async save(id, options) {
    const doc = get().docs[id];
    if (!doc) return;
    const contentStr = doc.rawMode
      ? doc.rawContent
      : JSON.stringify(buildDocRecipe(doc), null, 2);

    let url = `/api/v1/recipes/store/${encodeURIComponent(options.path)}`;
    if (options.storage === 'git') {
      url += `?git_url=${encodeURIComponent(options.gitUrl || '')}&git_branch=${encodeURIComponent(options.gitBranch || '')}`;
    }
    const res = await apiPost(url, { content: contentStr });
    if (!res.ok) {
      throw new Error(await res.text());
    }
    patchDoc(set, id, (d) => ({ ...d, dirty: false }));
  },
}));
