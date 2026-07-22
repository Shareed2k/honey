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
  // Only exercised when a doc has a non-empty originalCue; the graph/raw
  // toggle tests below use blank docs (originalCue: '') so this is never
  // actually invoked, but store.ts imports the name so it must exist here.
  syncRecipeAST: vi.fn(async (_originalCue: string, recipeContent: Record<string, unknown>) =>
    JSON.stringify(recipeContent, null, 2)),
  // validate() below configures this per-test via mockResolvedValueOnce.
  validateRecipeContent: vi.fn(),
}));
vi.mock('../../api/core', () => ({
  apiPost: vi.fn(),
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

import { message } from 'antd';
import { useWorkspaceStore } from './store';
import { validateRecipeContent } from '../../api/recipes';
import { apiPost } from '../../api/core';

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

  it('setSchema stores the schema as-is', () => {
    useWorkspaceStore.getState().setSchema({ ok: 1 });
    expect(useWorkspaceStore.getState().schema).toEqual({ ok: 1 });
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

  it('addStep adds a node + matching stepData entry to the active doc only', async () => {
    await useWorkspaceStore.getState().createDoc('other.cue');
    const before = useWorkspaceStore.getState().docs['other.cue'].nodes.length;

    useWorkspaceStore.getState().addStep('deploy.cue', 'run');

    const deployDoc = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(deployDoc.nodes).toHaveLength(2);
    const newNode = deployDoc.nodes.find((n) => n.id !== 's1');
    expect(newNode).toBeTruthy();
    expect(deployDoc.stepData[newNode.id]).toBeTruthy();
    expect(deployDoc.stepData[newNode.id].kind).toBe('run');

    // cross-doc isolation: the other open doc must be untouched.
    expect(useWorkspaceStore.getState().docs['other.cue'].nodes).toHaveLength(before);
  });

  it('setStepData on one doc leaves another open doc\'s stepData untouched', async () => {
    await useWorkspaceStore.getState().createDoc('other.cue');

    useWorkspaceStore.getState().setStepData('deploy.cue', 's1', { kind: 'run', command: 'x' });

    expect(useWorkspaceStore.getState().docs['deploy.cue'].stepData.s1.command).toBe('x');
    expect(useWorkspaceStore.getState().docs['other.cue'].stepData.s1.command).toBeUndefined();
    expect(useWorkspaceStore.getState().docs['other.cue'].stepData.s1.kind).toBe('run');
  });

  it('resetDoc preserves recipeId/name identity while resetting content', () => {
    const nameBefore = useWorkspaceStore.getState().docs['deploy.cue'].name;
    expect(nameBefore).toBeTruthy();

    // mutate the doc so it's non-blank and dirty.
    useWorkspaceStore.getState().setSelectedNode('deploy.cue', 's1');
    useWorkspaceStore.getState().addStep('deploy.cue', 'run');
    const mutated = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(mutated.selectedNodeId).toBe('s1');
    expect(mutated.dirty).toBe(true);
    expect(mutated.nodes.length).toBeGreaterThan(0);

    useWorkspaceStore.getState().resetDoc('deploy.cue');

    const reset = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(reset).toBeTruthy();
    expect(reset.recipeId).toBe('deploy.cue');
    expect(reset.name).toBe(nameBefore);
    expect(reset.selectedNodeId).toBeNull();
    expect(reset.dirty).toBe(false);
    expect(reset.nodes).toHaveLength(0);
  });
});

describe('graph/raw toggle', () => {
  beforeEach(async () => {
    useWorkspaceStore.setState({ docs: {}, active: null });
    await useWorkspaceStore.getState().createDoc('deploy.cue');
  });

  it('switchToRaw sets rawMode=true and populates a non-empty rawContent', async () => {
    useWorkspaceStore.getState().setSelectedNode('deploy.cue', 's1');

    await useWorkspaceStore.getState().switchToRaw('deploy.cue');

    const doc = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(doc.rawMode).toBe(true);
    expect(typeof doc.rawContent).toBe('string');
    expect(doc.rawContent.length).toBeGreaterThan(0);
    expect(doc.selectedNodeId).toBeNull();
  });

  // NOTE: the "valid JSON rebuilds the graph" case used to live here, but with
  // buildFlowFromRecipe stubbed (see the file-level vi.mock above) to always
  // return one fixed node, it could never prove the real recipe->graph
  // transform runs on toggle. That genuine coverage — with buildFlowFromRecipe
  // NOT mocked, asserting real node/edge/stepData counts from a crafted
  // recipe — now lives in store.toggle.test.ts instead.

  it('switchToVisual with invalid JSON does not throw and leaves rawMode unchanged', () => {
    const errorSpy = vi.spyOn(message, 'error').mockImplementation(() => '' as unknown as ReturnType<typeof message.error>);

    useWorkspaceStore.getState().setRawContent('deploy.cue', 'not json');
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'deploy.cue': { ...s.docs['deploy.cue'], rawMode: true } },
    }));

    expect(() => useWorkspaceStore.getState().switchToVisual('deploy.cue')).not.toThrow();

    const doc = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(doc.rawMode).toBe(true);
    expect(errorSpy).toHaveBeenCalled();

    errorSpy.mockRestore();
  });
});

describe('validate/save actions', () => {
  beforeEach(async () => {
    useWorkspaceStore.setState({ docs: {}, active: null });
    await useWorkspaceStore.getState().createDoc('deploy.cue');
    vi.mocked(validateRecipeContent).mockReset();
    vi.mocked(apiPost).mockReset();
  });

  it('validate: a valid recipe sets state to valid and records the risk', async () => {
    vi.mocked(validateRecipeContent).mockResolvedValueOnce({
      plan: '', steps: [], risk: { level: 'low' },
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);

    await useWorkspaceStore.getState().validate('deploy.cue');

    const doc = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(doc.validation.state).toBe('valid');
    expect(doc.validation.issues).toHaveLength(0);
    expect(doc.validation.risk).toEqual({ level: 'low' });
  });

  it('validate: a rejected recipe sets state to invalid with its issues', async () => {
    vi.mocked(validateRecipeContent).mockResolvedValueOnce({
      errors: [{ message: 'bad' }],
      // eslint-disable-next-line @typescript-eslint/no-explicit-any
    } as any);

    await useWorkspaceStore.getState().validate('deploy.cue');

    const doc = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(doc.validation.state).toBe('invalid');
    expect(doc.validation.issues).toHaveLength(1);
    expect(doc.validation.issues[0].message).toBe('bad');
  });

  it('save: success posts the built recipe JSON to the store URL and clears dirty', async () => {
    useWorkspaceStore.getState().markDirty('deploy.cue');
    expect(useWorkspaceStore.getState().docs['deploy.cue'].dirty).toBe(true);
    vi.mocked(apiPost).mockResolvedValueOnce({ ok: true } as Response);

    await useWorkspaceStore.getState().save('deploy.cue', {
      storage: 'local', path: 'deploy.cue', commitMessage: '',
    });

    expect(useWorkspaceStore.getState().docs['deploy.cue'].dirty).toBe(false);
    expect(apiPost).toHaveBeenCalledTimes(1);
    const [url, body] = vi.mocked(apiPost).mock.calls[0];
    expect(url).toBe('/api/v1/recipes/store/deploy.cue');
    const content = (body as { content: string }).content;
    expect(typeof content).toBe('string');
    expect(JSON.parse(content)).toHaveProperty('steps');
  });

  it('save: failure throws and leaves the doc dirty', async () => {
    useWorkspaceStore.getState().markDirty('deploy.cue');
    vi.mocked(apiPost).mockResolvedValueOnce({
      ok: false, text: async () => 'nope',
    } as Response);

    await expect(
      useWorkspaceStore.getState().save('deploy.cue', {
        storage: 'local', path: 'deploy.cue', commitMessage: '',
      }),
    ).rejects.toThrow('nope');

    expect(useWorkspaceStore.getState().docs['deploy.cue'].dirty).toBe(true);
  });

  it('save: rawMode posts rawContent verbatim, not JSON.stringify of the visual doc', async () => {
    useWorkspaceStore.getState().setRawContent('deploy.cue', 'raw cue text');
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'deploy.cue': { ...s.docs['deploy.cue'], rawMode: true } },
    }));
    vi.mocked(apiPost).mockResolvedValueOnce({ ok: true } as Response);

    await useWorkspaceStore.getState().save('deploy.cue', {
      storage: 'local', path: 'x.cue', commitMessage: '',
    });

    expect(apiPost).toHaveBeenCalledWith(
      '/api/v1/recipes/store/x.cue',
      { content: 'raw cue text' },
    );
  });

  it('save: git storage appends encoded git_url/git_branch query params to the URL', async () => {
    vi.mocked(apiPost).mockResolvedValueOnce({ ok: true } as Response);

    await useWorkspaceStore.getState().save('deploy.cue', {
      storage: 'git', path: 'x.cue', commitMessage: 'msg',
      gitUrl: 'https://e/r.git', gitBranch: 'main',
    });

    expect(apiPost).toHaveBeenCalledTimes(1);
    const [url] = vi.mocked(apiPost).mock.calls[0];
    // encodeURIComponent('https://e/r.git') turns ':' and '/' into %3A/%2F —
    // asserting the literal encoded substring (not a re-derived expected value)
    // proves encoding actually ran rather than the raw URL being passed through.
    expect(url).toContain('git_url=https%3A%2F%2Fe%2Fr.git');
    expect(url).toContain('git_branch=main');
  });

  it('save: URL-encodes a path that needs encoding (space)', async () => {
    vi.mocked(apiPost).mockResolvedValueOnce({ ok: true } as Response);

    await useWorkspaceStore.getState().save('deploy.cue', {
      storage: 'local', path: 'my recipe.cue', commitMessage: '',
    });

    const [url] = vi.mocked(apiPost).mock.calls[0];
    expect(url).toContain('my%20recipe.cue');
  });

  it('validate: rawMode with invalid JSON sets invalid state and skips validateRecipeContent', async () => {
    useWorkspaceStore.getState().setRawContent('deploy.cue', 'not json');
    useWorkspaceStore.setState((s) => ({
      docs: { ...s.docs, 'deploy.cue': { ...s.docs['deploy.cue'], rawMode: true } },
    }));

    await useWorkspaceStore.getState().validate('deploy.cue');

    const doc = useWorkspaceStore.getState().docs['deploy.cue'];
    expect(doc.validation.state).toBe('invalid');
    expect(doc.validation.issues).toHaveLength(1);
    expect(doc.validation.issues[0].message).toContain('JSON');
    // The raw content never parses to a recipe object, so the parse-error
    // branch must return early instead of calling the server-side validator.
    expect(validateRecipeContent).not.toHaveBeenCalled();
  });
});
