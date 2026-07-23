import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import type { DockviewApi } from 'dockview';

// Shape of the workspace JSON blob, used on both sides: the GET response
// persistence.ts's `restore()` consumes, and the PUT body its `save()`
// sends. Typed here (rather than left `unknown`/`any`) with `active` as
// `string | null` — not narrowed to the default GET fixture's literal
// `'a.cue'` — so per-test `get.mockResolvedValueOnce({ ..., active: null })`
// overrides (e.g. the empty-openRecipes regression test below) type-check
// against the same shape, and `putJson.mock.calls[0]` destructures into a
// real tuple with `body.openRecipes`/`body.active` type-checked.
interface WorkspaceBody {
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
    json: async (): Promise<WorkspaceBody> => ({
      layout: { grid: {} },
      openRecipes: ['a.cue'],
      active: 'a.cue',
    }),
  })),
  putJson: vi.fn(async (_path: string, _body: WorkspaceBody) => ({ ok: true })),
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
//
// `fromJSON` simulates real dockview's `onDidLayoutChange`, which is an
// `AsapEvent` (`dockview-core/dist/.../events.js`): firing it only *queues* a
// microtask via `queueMicrotask`, it never invokes subscribers synchronously.
// The shipped fake (`fromJSON: vi.fn()`, a no-op) can't reproduce that, so it
// couldn't catch persistence.ts calling `api.fromJSON(...)` and assuming the
// layout-change event had "landed" by the time `restore()` returns.
function makeApi() {
  const layoutCbs: (() => void)[] = [];
  const fireLayout = () => layoutCbs.forEach((c) => c());
  return {
    toJSON: () => ({ grid: {} }),
    fromJSON: vi.fn(() => {
      queueMicrotask(() => fireLayout());
    }),
    clear: vi.fn(),
    addPanel: vi.fn(),
    panels: [{ id: 'graph:a.cue' }] as { id: string }[],
    onDidLayoutChange: (cb: () => void) => {
      layoutCbs.push(cb);
      return { dispose() { const i = layoutCbs.indexOf(cb); if (i >= 0) layoutCbs.splice(i, 1); } };
    },
    _fireLayout: fireLayout,
  };
}

function asApi(fake: ReturnType<typeof makeApi>): DockviewApi {
  return fake as unknown as DockviewApi;
}

import { attachWorkspaceSync, resetLayout } from './persistence';

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

  // Regression test for the bug this fix addresses: dockview's
  // `onDidLayoutChange` is an `AsapEvent` — `api.fromJSON(...)` only queues
  // its layout-change microtask, it doesn't fire synchronously (see
  // `makeApi`'s doc comment above). `restore()` awaits `store.createDoc(...)`
  // for each saved recipe *after* calling `fromJSON`, which incidentally
  // drains that queued microtask before `restoring` flips back to false —
  // except when `openRecipes` is empty, in which case that loop body never
  // runs, there is no other await between `fromJSON` and the `finally`
  // block, and (pre-fix) `restoring` goes false before the queued event
  // fires. The event then lands with the guard already down, arms the
  // debounce timer via `scheduleSave()`, and ~800ms later `save()` fires a
  // PUT nobody asked for — on every reload, and after every "Reset Layout"
  // (which produces exactly this empty-`openRecipes` shape).
  it('restore() with empty openRecipes does not leak a spurious PUT (dockview async layout event)', async () => {
    get.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ layout: { grid: {} }, openRecipes: [], active: null }),
    });
    const api = makeApi();
    const store: FakeStore = { createDoc, setActive, active: null };
    const sync = attachWorkspaceSync(asApi(api), store);

    // Drive restore() to completion and past it: `fromJSON`'s queued event
    // lives on the *real* microtask queue (fake timers don't fake
    // `queueMicrotask`), so plain `await Promise.resolve()` ticks — not
    // `vi.advanceTimersByTimeAsync` — are what flush it. Interleave with
    // `runOnlyPendingTimersAsync` in case anything schedules a real timer
    // along the way, then advance past the debounce window.
    await vi.runOnlyPendingTimersAsync();
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(800);

    expect(putJson).not.toHaveBeenCalled();
    sync.dispose();
  });

  it('restore() with non-empty openRecipes does not leak a spurious PUT either', async () => {
    get.mockResolvedValueOnce({
      ok: true,
      json: async () => ({ layout: { grid: {} }, openRecipes: ['a.cue'], active: 'a.cue' }),
    });
    const api = makeApi();
    const store: FakeStore = { createDoc, setActive, active: null };
    const sync = attachWorkspaceSync(asApi(api), store);

    await vi.runOnlyPendingTimersAsync();
    await Promise.resolve();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(800);

    expect(putJson).not.toHaveBeenCalled();
    sync.dispose();
  });
});

describe('resetLayout', () => {
  it('clears the layout and re-adds the default tool panels', () => {
    const clear = vi.fn();
    // Typed (rather than a bare `vi.fn()`) so `addPanel.mock.calls` below is
    // a real `[{ id: string }][]` tuple array instead of `any[][]`.
    const addPanel = vi.fn((_opts: { id: string }) => undefined);
    const api = { clear, addPanel } as unknown as DockviewApi;

    resetLayout(api);

    expect(clear).toHaveBeenCalledTimes(1);
    const addedIds = addPanel.mock.calls.map(([opts]) => opts.id);
    expect(addedIds).toEqual(['toolbox', 'records', 'stepeditor', 'settings', 'run', 'validation']);
  });
});
