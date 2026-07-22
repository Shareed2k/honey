import { create } from 'zustand';
import type { DocState } from './types';
import { parseDiskRecipe } from '../../api/recipes';
import { buildFlowFromRecipe, applyWaveLayout, recipeNameFromFilename } from '../useRecipeGraph';

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

  createDoc(name: string): Promise<void>;
  newDoc(): string;
  freeDoc(id: string): void;
  setActive(id: string | null): void;
}

export const useWorkspaceStore = create<WorkspaceState>((set, get) => ({
  docs: {},
  active: null,

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
}));
