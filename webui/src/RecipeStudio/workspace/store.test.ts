import { describe, it, expect, beforeEach, vi } from 'vitest';

vi.mock('../../api/recipes', () => ({
  parseDiskRecipe: vi.fn(async (path: string) => ({
    recipe: { defaults: { x: 1 }, steps: [{ id: 's1', run: { command: 'echo hi' } }] },
    name: path,
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
  });

  it('createDoc is idempotent (does not reload an already-open doc)', async () => {
    const api = useWorkspaceStore.getState();
    await api.createDoc('deploy.cue');
    useWorkspaceStore.getState().docs['deploy.cue'].dirty = true;
    await api.createDoc('deploy.cue');
    expect(useWorkspaceStore.getState().docs['deploy.cue']).toBeTruthy();
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
  });
});
