import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest';
import { create } from 'zustand';
import type { DockviewApi } from 'dockview';
import type { PersistedWorkspace } from './types';

// `PersistedWorkspace` (types.ts) is the shape of the workspace JSON blob,
// used on both sides: the GET response persistence.ts's `restore()`
// consumes, and the PUT body its `save()` sends. Its `active` field is
// `string | null` — not narrowed to the default GET fixture's literal
// `'a.cue'` — so per-test `get.mockResolvedValueOnce({ ..., active: null })`
// overrides (e.g. the empty-openRecipes regression test below) type-check
// against the same shape, and `putJson.mock.calls[0]` destructures into a
// real tuple with `body.openRecipes`/`body.active` type-checked.

// vi.mock is hoisted above every other top-level statement in this file, so
// its factory can only safely reference variables that are *also* hoisted —
// hence vi.hoisted() here rather than plain top-level consts (which would
// still be in their temporal dead zone when the factory runs).
const { get, putJson } = vi.hoisted(() => ({
  get: vi.fn(async (_path: string) => ({
    ok: true,
    json: async (): Promise<PersistedWorkspace> => ({
      layout: { grid: {} },
      openRecipes: ['a.cue'],
      active: 'a.cue',
    }),
  })),
  putJson: vi.fn(async (_path: string, _body: PersistedWorkspace) => ({ ok: true })),
}));
vi.mock('../../api/core', () => ({ apiGet: get, apiPutJson: putJson }));

const createDoc = vi.fn(async (_name: string) => {});
const setActive = vi.fn((_id: string | null) => {});

interface FakeState {
  createDoc(name: string): Promise<void>;
  setActive(id: string | null): void;
  active: string | null;
}

// Wraps a plain state literal in a `getState()` accessor — matches the store
// API shape `attachWorkspaceSync` now requires (see persistence.ts's
// `SyncStoreApi`). Fine for tests that don't need `active` to change mid-test
// (a plain closed-over object is effectively a snapshot); the dedicated
// "reads live active" test below uses a REAL zustand store instead, since
// proving liveness requires state that can actually change out from under an
// already-captured reference.
function makeStoreApi(initial: FakeState): { getState(): FakeState } {
  return { getState: () => initial };
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
    const store = makeStoreApi({ createDoc, setActive, active: 'a.cue' });
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
    const store = makeStoreApi({ createDoc, setActive, active: null });
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
    const store = makeStoreApi({ createDoc, setActive, active: 'untitled-1.cue' });
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
    const store = makeStoreApi({ createDoc, setActive, active: null });
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
    const store = makeStoreApi({ createDoc, setActive, active: null });
    const sync = attachWorkspaceSync(asApi(api), store);

    await vi.runOnlyPendingTimersAsync();
    await Promise.resolve();
    await Promise.resolve();
    await vi.advanceTimersByTimeAsync(800);

    expect(putJson).not.toHaveBeenCalled();
    sync.dispose();
  });

  // Regression test for finding #1 (persisted `active` is always stale):
  // `attachWorkspaceSync` used to take a ONE-TIME `useWorkspaceStore.getState()`
  // snapshot as `store`, then read `store.active` directly in `save()`.
  // zustand v5 replaces state via `Object.assign` on every `setState` — it
  // never mutates the old state object — so that captured snapshot's
  // `.active` was frozen at attach-time forever, and every subsequent PUT
  // wrote back whatever `active` was at attach (here: `null`, since the doc
  // isn't selected until after attach), never the doc the user actually has
  // open. A REAL zustand store is used (rather than `makeStoreApi`'s plain
  // literal) because the bug is specifically about reading state THROUGH a
  // live `getState()` after it changes — a fixed literal can't distinguish
  // "reads live" from "reads a snapshot that happens to still be right".
  it('save() reads the LIVE active doc, not a snapshot frozen at attach time', async () => {
    const api = makeApi();
    const liveStore = create<FakeState>((set) => ({
      active: null,
      createDoc,
      setActive: (id) => set({ active: id }),
    }));

    const sync = attachWorkspaceSync(asApi(api), liveStore);
    await vi.runOnlyPendingTimersAsync(); // let restore() settle
    putJson.mockClear();

    // Select a doc AFTER attach — this is the live update a frozen snapshot
    // would miss.
    liveStore.getState().setActive('some.cue');

    api._fireLayout();
    await vi.advanceTimersByTimeAsync(800);

    expect(putJson).toHaveBeenCalledTimes(1);
    const [, body] = putJson.mock.calls[0];
    expect(body.active).toBe('some.cue');
    sync.dispose();
  });

  // Regression test for finding #3 (dispose() drops a pending save):
  // dispose() used to just clear the debounce timer, silently discarding up
  // to DEBOUNCE_MS worth of the final layout change (e.g. navigating away
  // right after a drag-resize).
  it('dispose() flushes a pending debounced save instead of dropping it', async () => {
    const api = makeApi();
    const store = makeStoreApi({ createDoc, setActive, active: 'a.cue' });
    const sync = attachWorkspaceSync(asApi(api), store);
    await vi.runOnlyPendingTimersAsync(); // let restore() settle
    putJson.mockClear();

    api._fireLayout(); // arms the debounce timer; save() has not fired yet
    sync.dispose();

    expect(putJson).toHaveBeenCalledTimes(1);

    // The timer must actually be cleared too — advancing past the debounce
    // window shouldn't produce a second, duplicate PUT.
    putJson.mockClear();
    await vi.advanceTimersByTimeAsync(800);
    expect(putJson).not.toHaveBeenCalled();
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
