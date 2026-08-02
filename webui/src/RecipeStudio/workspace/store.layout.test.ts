import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useWorkspaceStore } from './store';

// Regression coverage for the flat-layout bug: createDoc/createDocFromRecipe
// used to call applyWaveLayout(nodes) directly on nodes fresh out of
// buildFlowFromRecipe, which never set data.wave — so applyWaveLayout's
// fallbackWave (1, since no node carried a wave yet) stacked EVERY node into
// the same column regardless of its dependency depth. The fix
// (layoutFlowNodes in store.ts) runs computeWavesFromEdges first and stamps
// each node's data.wave before laying it out, matching what switchToVisual
// already did.
//
// Deliberately NOT mocking '../useRecipeGraph' (unlike store.test.ts's
// file-level stub, which fakes buildFlowFromRecipe/applyWaveLayout and would
// hide this exact bug) — the assertions below only pass if the REAL
// buildFlowFromRecipe -> computeWavesFromEdges -> applyWaveLayout pipeline
// actually ran on an a -> b -> c dependency chain.
const CHAIN_RECIPE = {
  name: 'chain',
  type: 'graph',
  steps: [
    { id: 'a', command: 'echo a' },
    { id: 'b', command: 'echo b', depends: ['a'] },
    { id: 'c', command: 'echo c', depends: ['b'] },
  ],
};

vi.mock('../../api/recipes', () => ({
  fetchStoredRecipe: vi.fn(async () => ({
    recipe: {
      name: 'chain',
      type: 'graph',
      steps: [
        { id: 'a', command: 'echo a' },
        { id: 'b', command: 'echo b', depends: ['a'] },
        { id: 'c', command: 'echo c', depends: ['b'] },
      ],
    },
    raw_cue: '',
  })),
  parseDiskRecipe: vi.fn(async () => ({ name: 'unused', steps: [] })),
  syncRecipeAST: vi.fn(),
}));

import { fetchStoredRecipe } from '../../api/recipes';

function wavesOf(doc: { nodes: { id: string; data?: { wave?: number } }[] }) {
  return {
    a: doc.nodes.find((n) => n.id === 'a')?.data?.wave,
    b: doc.nodes.find((n) => n.id === 'b')?.data?.wave,
    c: doc.nodes.find((n) => n.id === 'c')?.data?.wave,
  };
}

describe('createDoc/createDocFromRecipe stagger nodes by dependency wave (flat-layout regression)', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({ docs: {}, active: null });
  });

  it('createDocFromRecipe stamps an a->b->c chain with three DIFFERENT waves (1, 2, 3)', () => {
    useWorkspaceStore.getState().createDocFromRecipe('chain.cue', CHAIN_RECIPE);

    const doc = useWorkspaceStore.getState().docs['chain.cue'];
    const waves = wavesOf(doc);
    expect(waves.a).toBe(1);
    expect(waves.b).toBe(2);
    expect(waves.c).toBe(3);
    // A flat/stacked layout (the pre-fix bug) puts every node at the same
    // fallback wave — asserting three distinct values is what that bug fails.
    expect(new Set([waves.a, waves.b, waves.c]).size).toBe(3);
  });

  it('createDoc (load-by-name) stamps the same a->b->c chain with three DIFFERENT waves (1, 2, 3)', async () => {
    await useWorkspaceStore.getState().createDoc('chain.cue');

    expect(fetchStoredRecipe).toHaveBeenCalledWith('chain.cue');

    const doc = useWorkspaceStore.getState().docs['chain.cue'];
    const waves = wavesOf(doc);
    expect(waves.a).toBe(1);
    expect(waves.b).toBe(2);
    expect(waves.c).toBe(3);
    expect(new Set([waves.a, waves.b, waves.c]).size).toBe(3);
  });
});
