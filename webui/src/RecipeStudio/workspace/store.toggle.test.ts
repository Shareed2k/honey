import { describe, it, expect, vi, beforeEach } from 'vitest';
import { message } from 'antd';
import { useWorkspaceStore } from './store';
import type { DocState } from './types';
import * as recipesApi from '../../api/recipes';

// Deliberately NOT mocking '../useRecipeGraph' here. store.test.ts stubs
// buildFlowFromRecipe at the file level to always return one fixed node
// regardless of input, which makes its switchToVisual coverage hollow — it
// can't prove the real recipe->graph transform runs. This file exercises the
// REAL buildFlowFromRecipe/buildRecipeFromFlow/computeWavesFromEdges/
// applyWaveLayout implementations so the assertions can only pass if the
// actual transform executed.
vi.mock('../../api/recipes', () => ({
  parseDiskRecipe: vi.fn(async () => ({ name: 'unused', steps: [] })),
  syncRecipeAST: vi.fn(),
}));

function blankDocState(id: string, overrides: Partial<DocState> = {}): DocState {
  return {
    recipeId: id,
    name: id,
    nodes: [],
    edges: [],
    stepData: {},
    recipeDefaults: {},
    selectedNodeId: null,
    rawMode: false,
    rawContent: '',
    originalCue: '',
    validation: { state: 'idle', issues: [] },
    runStatus: {},
    dirty: false,
    ...overrides,
  };
}

function seedDoc(id: string, overrides: Partial<DocState> = {}): void {
  useWorkspaceStore.setState((s) => ({ docs: { ...s.docs, [id]: blankDocState(id, overrides) } }));
}

describe('switchToVisual — real buildFlowFromRecipe transform', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({ docs: {}, active: null });
  });

  it('rebuilds nodes/edges/stepData from the REAL recipe->graph transform', () => {
    // buildFlowFromRecipe (useRecipeGraph.ts ~line 252) reads a top-level
    // `steps` ARRAY, taking each step's `id` (falling back to `step_<n>`) and
    // turning its `depends` array into one edge per dependency, named
    // `edge_from_<dep>_to_<id>`. Two steps with one dependency below means
    // only the REAL transform can produce exactly 2 nodes / 1 edge / these
    // stepData keys — the file-level mock in store.test.ts always returns a
    // single fixed node no matter what's fed in, so it could never satisfy
    // these assertions.
    const recipeJSON = JSON.stringify({
      name: 'toggle-real',
      type: 'graph',
      steps: [
        { id: 'step_one', command: 'echo one' },
        { id: 'step_two', command: 'echo two', depends: ['step_one'] },
      ],
      defaults: {},
    });
    seedDoc('toggle-real.cue', { rawMode: true, rawContent: recipeJSON });

    useWorkspaceStore.getState().switchToVisual('toggle-real.cue');

    const doc = useWorkspaceStore.getState().docs['toggle-real.cue'];
    expect(doc.rawMode).toBe(false);
    expect(doc.nodes).toHaveLength(2);
    expect(doc.edges).toHaveLength(1);
    expect(doc.edges[0]).toMatchObject({ source: 'step_one', target: 'step_two' });
    expect(Object.keys(doc.stepData).sort()).toEqual(['step_one', 'step_two']);
  });

  it('invalid JSON leaves rawMode untouched and surfaces an error toast, without throwing', () => {
    const errorSpy = vi.spyOn(message, 'error').mockImplementation(() => '' as unknown as ReturnType<typeof message.error>);
    seedDoc('toggle-invalid.cue', { rawMode: true, rawContent: 'not json' });

    expect(() => useWorkspaceStore.getState().switchToVisual('toggle-invalid.cue')).not.toThrow();

    const doc = useWorkspaceStore.getState().docs['toggle-invalid.cue'];
    expect(doc.rawMode).toBe(true);
    expect(errorSpy).toHaveBeenCalled();

    errorSpy.mockRestore();
  });
});

describe('switchToRaw — originalCue sync path', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({ docs: {}, active: null });
    vi.mocked(recipesApi.syncRecipeAST).mockReset();
  });

  it('uses syncRecipeAST output when originalCue is present and the sync succeeds', async () => {
    vi.mocked(recipesApi.syncRecipeAST).mockResolvedValue('SYNCED_CUE_OUTPUT');
    seedDoc('sync-ok.cue', {
      originalCue: 'some cue',
      nodes: [{ id: 'step_one', type: 'step', position: { x: 0, y: 0 }, data: {} }],
      stepData: { step_one: { id: 'step_one', kind: 'command', host: '*', command: 'echo hi' } },
    });

    await useWorkspaceStore.getState().switchToRaw('sync-ok.cue');

    const doc = useWorkspaceStore.getState().docs['sync-ok.cue'];
    expect(doc.rawContent).toBe('SYNCED_CUE_OUTPUT');
    expect(doc.rawMode).toBe(true);
  });

  it('falls back to JSON.stringify(visualJSON) when syncRecipeAST rejects, without an unhandled rejection', async () => {
    const warnSpy = vi.spyOn(console, 'warn').mockImplementation(() => undefined);
    vi.mocked(recipesApi.syncRecipeAST).mockRejectedValue(new Error('sync failed'));
    seedDoc('sync-fail.cue', {
      originalCue: 'some cue',
      nodes: [{ id: 'step_one', type: 'step', position: { x: 0, y: 0 }, data: {} }],
      stepData: { step_one: { id: 'step_one', kind: 'command', host: '*', command: 'echo hi' } },
    });

    await useWorkspaceStore.getState().switchToRaw('sync-fail.cue');

    const doc = useWorkspaceStore.getState().docs['sync-fail.cue'];
    expect(doc.rawMode).toBe(true);
    // Fallback must be the JSON.stringify(visualJSON) produced by the REAL
    // buildRecipeFromFlow — assert it parses and carries a `steps` field
    // rather than pinning the exact string, so this doesn't overlap with
    // useRecipeGraph's own buildRecipeFromFlow unit tests.
    const parsed = JSON.parse(doc.rawContent);
    expect(parsed).toHaveProperty('steps');
    // Restored diagnostic breadcrumb: the catch block must log the sync
    // failure instead of swallowing it silently (no unhandled rejection).
    expect(warnSpy).toHaveBeenCalledWith('AST sync failed, falling back to JSON:', expect.any(Error));

    warnSpy.mockRestore();
  });
});
