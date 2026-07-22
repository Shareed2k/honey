import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { DockviewApi } from 'dockview';

// Shape of the PUT body persistence.ts sends — typed here (rather than left
// `unknown`/`any`) so `putJson.mock.calls[0]` destructures into a real tuple
// and `body.openRecipes`/`body.active` type-check below.
interface WorkspacePutBody {
  layout: unknown;
  openRecipes: string[];
  active: string | null;
}

// vi.mock is hoisted above every other top-level statement in this file, so
// its factory can only safely reference variables that are *also* hoisted —
// hence vi.hoisted() here rather than plain top-level consts (which would
// still be in their temporal dead zone when the factory runs).
const { get, putJson } = vi.hoisted(() => ({
  get: vi.fn(async (_path: string) => ({
    ok: true,
    json: async () => ({ layout: { grid: {} }, openRecipes: ['a.cue'], active: 'a.cue' }),
  })),
  putJson: vi.fn(async (_path: string, _body: WorkspacePutBody) => ({ ok: true })),
}));
vi.mock('../../api/core', () => ({ apiGet: get, apiPutJson: putJson }));

const createDoc = vi.fn(async (_name: string) => {});
const setActive = vi.fn((_id: string | null) => {});

interface FakeStore {
  createDoc(name: string): Promise<void>;
  setActive(id: string | null): void;
  active: string | null;
}

// Minimal fake dockview api — only the members attachWorkspaceSync touches.
// `_fireLayout` drives `onDidLayoutChange` subscribers manually, mirroring
// the fake used in useDockviewSync.test.ts.
function makeApi() {
  const layoutCbs: (() => void)[] = [];
  return {
    toJSON: () => ({ grid: {} }),
    fromJSON: vi.fn(),
    clear: vi.fn(),
    panels: [{ id: 'graph:a.cue' }] as { id: string }[],
    onDidLayoutChange: (cb: () => void) => {
      layoutCbs.push(cb);
      return { dispose() { const i = layoutCbs.indexOf(cb); if (i >= 0) layoutCbs.splice(i, 1); } };
    },
    _fireLayout() { layoutCbs.forEach((c) => c()); },
  };
}

function asApi(fake: ReturnType<typeof makeApi>): DockviewApi {
  return fake as unknown as DockviewApi;
}

import { attachWorkspaceSync } from './persistence';

describe('attachWorkspaceSync', () => {
  beforeEach(() => {
    vi.useFakeTimers();
    putJson.mockClear();
    get.mockClear();
    createDoc.mockClear();
    setActive.mockClear();
  });
  afterEach(() => vi.useRealTimers());

  it('debounces rapid layout changes into a single PUT', async () => {
    const api = makeApi();
    const store: FakeStore = { createDoc, setActive, active: 'a.cue' };
    const sync = attachWorkspaceSync(asApi(api), store);
    await vi.runOnlyPendingTimersAsync(); // let restore() settle
    putJson.mockClear();

    api._fireLayout();
    api._fireLayout();
    api._fireLayout();
    await vi.advanceTimersByTimeAsync(800);

    expect(putJson).toHaveBeenCalledTimes(1);
    sync.dispose();
  });

  it('restore() does not trigger a save (restoring flag suppresses it)', async () => {
    const api = makeApi();
    const store: FakeStore = { createDoc, setActive, active: null };
    const sync = attachWorkspaceSync(asApi(api), store);
    await vi.runOnlyPendingTimersAsync();

    expect(api.fromJSON).toHaveBeenCalledWith({ grid: {} });
    expect(createDoc).toHaveBeenCalledWith('a.cue');
    expect(setActive).toHaveBeenCalledWith('a.cue');
    expect(putJson).not.toHaveBeenCalled();
    sync.dispose();
  });

  it('excludes untitled recipes from the PUT (openRecipes + active)', async () => {
    const api = makeApi();
    api.panels = [{ id: 'graph:a.cue' }, { id: 'graph:untitled-1.cue' }];
    const store: FakeStore = { createDoc, setActive, active: 'untitled-1.cue' };
    const sync = attachWorkspaceSync(asApi(api), store);
    await vi.runOnlyPendingTimersAsync();
    putJson.mockClear();

    api._fireLayout();
    await vi.advanceTimersByTimeAsync(800);

    expect(putJson).toHaveBeenCalledTimes(1);
    const [path, body] = putJson.mock.calls[0];
    expect(path).toBe('/api/v1/studio/workspace');
    expect(body.openRecipes).toEqual(['a.cue']);
    expect(body.active).toBeNull();
    sync.dispose();
  });
});
