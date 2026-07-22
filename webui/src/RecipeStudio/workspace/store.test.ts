import { describe, it, expect, beforeEach, vi } from 'vitest';

// parseDiskRecipe resolves to the ParsedRecipe itself — top-level `name`/`defaults`/`steps`,
// no nested `{recipe: ...}` envelope (see api/recipes.ts parseDiskRecipe + api/types/recipes.ts
// ParsedRecipe). The mock must mirror that shape so the test exercises the real production
// code path in store.ts (which reads `recipeJson.defaults`/`recipeJson.steps` directly).
vi.mock('../../api/recipes', () => ({
  parseDiskRecipe: vi.fn(async (path: string) => ({
    name: path,
    defaults: { x: 1 },
    steps: [{ id: 's1', command: 'echo hi' }],
  })),
}));
vi.mock('../useRecipeGraph', async (orig) => {
  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  const actual = await (orig as any)();
  return {
    ...actual,
    buildFlowFromRecipe: () => ({
      nodes: [{ id: 's1', data: {} }],
      edges: [],
      stepData: { s1: { kind: 'run' } },
    }),
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    applyWaveLayout: (n: any[]) => n,
  };
});

import { useWorkspaceStore } from './store';

describe('WorkspaceStore lifecycle', () => {
  beforeEach(() => useWorkspaceStore.setState({ docs: {}, active: null }));

  it('createDoc loads a recipe into docs keyed by name', async () => {
    await useWorkspaceStore.getState().createDoc('deploy.cue');
    const doc = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(doc).toBeTruthy();
    expect(doc.nodes).toHaveLength(1);
    expect(doc.stepData.s1.kind).toBe('run');
    expect(doc.dirty).toBe(false);
    expect(doc.recipeDefaults).toEqual({ x: 1 });
  });

  it('createDoc is idempotent (does not reload an already-open doc)', async () => {
    const api = useWorkspaceStore.getState();
    await api.createDoc('deploy.cue');
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, ['deploy.cue']: { ...s.docs['deploy.cue'], dirty: true } },
    }));
    await api.createDoc('deploy.cue');
    // A reload would have gone through blankDoc() again and reset dirty to false —
    // asserting it's still true proves the idempotency guard actually short-circuited.
    expect(useWorkspaceStore.getState().docs['deploy.cue'].dirty).toBe(true);
  });

  it('newDoc creates an untitled doc and returns its id', () => {
    const id = useWorkspaceStore.getState().newDoc();
    expect(id).toMatch(/^untitled-/);
    expect(useWorkspaceStore.getState().docs[id]).toBeTruthy();
  });

  it('freeDoc removes a doc; setActive updates active', async () => {
    await useWorkspaceStore.getState().createDoc('deploy.cue');
    useWorkspaceStore.getState().setActive('deploy.cue');
    expect(useWorkspaceStore.getState().active).toBe('deploy.cue');
    useWorkspaceStore.getState().freeDoc('deploy.cue');
    expect(useWorkspaceStore.getState().docs['deploy.cue']).toBeUndefined();
    expect(useWorkspaceStore.getState().active).toBeNull();
  });
});

describe('WorkspaceStore per-doc actions', () => {
  beforeEach(async () => {
    useWorkspaceStore.setState({ docs: {}, active: null });
    await useWorkspaceStore.getState().createDoc('deploy.cue');
  });

  it('mutates only the targeted doc', async () => {
    await useWorkspaceStore.getState().createDoc('other.cue');
    useWorkspaceStore.getState().setSelectedNode('deploy.cue', 's1');
    expect(useWorkspaceStore.getState().docs['deploy.cue'].selectedNodeId).toBe('s1');
    expect(useWorkspaceStore.getState().docs['other.cue'].selectedNodeId).toBeNull();
  });

  it('setNodeRunStatus sets status per node id', () => {
    useWorkspaceStore.getState().setNodeRunStatus('deploy.cue', ['s1'], 'running');
    expect(useWorkspaceStore.getState().docs['deploy.cue'].runStatus.s1).toBe('running');
  });

  it('setStepData marks the doc dirty', () => {
    useWorkspaceStore.getState().setStepData('deploy.cue', 's1', { kind: 'run', command: 'x' });
    expect(useWorkspaceStore.getState().docs['deploy.cue'].dirty).toBe(true);
  });

  it('actions on an unknown id are a no-op (no throw)', () => {
    expect(() => useWorkspaceStore.getState().setSelectedNode('nope', 's1')).not.toThrow();
  });
});
