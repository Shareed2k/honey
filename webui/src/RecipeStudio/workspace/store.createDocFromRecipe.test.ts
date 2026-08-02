import { describe, it, expect, beforeEach, vi } from 'vitest';
import { useWorkspaceStore, uniqueDocName } from './store';

// createDocFromRecipe is the shared entry point for the Generate/Library/
// Git-load triggers in StudioWorkspace.tsx — unlike createDoc (which loads a
// STORED recipe by name via fetchStoredRecipe), it builds a doc directly
// from an in-memory recipe object the caller already has in hand. Exercise
// the REAL buildFlowFromRecipe/buildRecipeFromFlow/applyWaveLayout transform
// here (not the file-level stub store.test.ts uses) so the assertions can
// only pass if the actual recipe->graph transform ran — see
// store.toggle.test.ts for the same rationale.
vi.mock('../../api/recipes', () => ({
  parseDiskRecipe: vi.fn(async () => ({ name: 'unused', steps: [] })),
  syncRecipeAST: vi.fn(),
}));

describe('createDocFromRecipe', () => {
  beforeEach(() => {
    useWorkspaceStore.setState({ docs: {}, active: null });
  });

  it('builds a doc from an in-memory recipe object, keyed by name', () => {
    const recipe = {
      name: 'generated',
      type: 'graph',
      steps: [
        { id: 'step_one', command: 'echo one' },
        { id: 'step_two', command: 'echo two', depends: ['step_one'] },
      ],
      defaults: { retries: 2 },
    };

    useWorkspaceStore.getState().createDocFromRecipe('generated-123.cue', recipe, 'raw cue text');

    const doc = useWorkspaceStore.getState().docs['generated-123.cue'];
    expect(doc).toBeTruthy();
    // real buildFlowFromRecipe transform: one node per step, one edge for the
    // `depends` link (see store.toggle.test.ts for the same shape assertion).
    expect(doc.nodes).toHaveLength(2);
    expect(doc.edges).toHaveLength(1);
    expect(Object.keys(doc.stepData).sort()).toEqual(['step_one', 'step_two']);
    expect(doc.recipeDefaults).toEqual({ retries: 2 });
    expect(doc.originalCue).toBe('raw cue text');
    // new/unsaved — never loaded from the store as-is.
    expect(doc.dirty).toBe(true);
    expect(doc.recipeId).toBe('generated-123.cue');
  });

  it('defaults recipeDefaults to {} and originalCue to "" when the recipe has no defaults / no rawCue is passed', () => {
    useWorkspaceStore.getState().createDocFromRecipe('no-defaults.cue', { steps: [] });

    const doc = useWorkspaceStore.getState().docs['no-defaults.cue'];
    expect(doc.recipeDefaults).toEqual({});
    expect(doc.originalCue).toBe('');
  });

  it('does not clobber an existing open doc with the same name — suffixes instead', () => {
    useWorkspaceStore.getState().createDocFromRecipe('deploy.cue', {
      steps: [{ id: 'existing_step', command: 'echo existing' }],
    });
    const existing = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(existing.stepData).toHaveProperty('existing_step');

    useWorkspaceStore.getState().createDocFromRecipe('deploy.cue', {
      steps: [{ id: 'new_step', command: 'echo new' }],
    });

    const state = useWorkspaceStore.getState();
    // the original doc, keyed 'deploy.cue', is untouched.
    expect(state.docs['deploy.cue'].stepData).toHaveProperty('existing_step');
    expect(state.docs['deploy.cue'].stepData).not.toHaveProperty('new_step');
    // the second recipe landed under a suffixed key instead of overwriting it.
    const suffixed = state.docs['deploy-2.cue'];
    expect(suffixed).toBeTruthy();
    expect(suffixed.stepData).toHaveProperty('new_step');
    expect(suffixed.recipeId).toBe('deploy-2.cue');

    // exactly two docs exist — nothing got dropped.
    expect(Object.keys(state.docs).sort()).toEqual(['deploy-2.cue', 'deploy.cue']);
  });

  it('a third collision on the same base name advances to -3', () => {
    useWorkspaceStore.getState().createDocFromRecipe('x.cue', { steps: [] });
    useWorkspaceStore.getState().createDocFromRecipe('x.cue', { steps: [] });
    useWorkspaceStore.getState().createDocFromRecipe('x.cue', { steps: [] });

    const state = useWorkspaceStore.getState();
    expect(Object.keys(state.docs).sort()).toEqual(['x-2.cue', 'x-3.cue', 'x.cue']);
  });
});

describe('uniqueDocName', () => {
  it('returns the base name verbatim when it is free', () => {
    expect(uniqueDocName('foo.cue', {})).toBe('foo.cue');
  });

  it('suffixes with -2 (preserving the extension) when the base name is taken', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const docs = { 'foo.cue': {} as any };
    expect(uniqueDocName('foo.cue', docs)).toBe('foo-2.cue');
  });

  it('handles a name with no extension', () => {
    // eslint-disable-next-line @typescript-eslint/no-explicit-any
    const docs = { 'untitled-1': {} as any };
    expect(uniqueDocName('untitled-1', docs)).toBe('untitled-1-2');
  });

  it('keeps advancing past multiple existing collisions', () => {
    const docs = {
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      'a.cue': {} as any,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      'a-2.cue': {} as any,
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
      'a-3.cue': {} as any,
    };
    expect(uniqueDocName('a.cue', docs)).toBe('a-4.cue');
  });
});
