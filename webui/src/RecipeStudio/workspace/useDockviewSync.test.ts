import { describe, it, expect, vi } from 'vitest';
import type { DockviewApi } from 'dockview';
import { attachDockviewSync } from './useDockviewSync';

// Minimal fake panel — only the `id` field attachDockviewSync's handlers touch.
type FakePanel = { id: string };

// Minimal fake dockview api with manual event firing. The subscriptions'
// dispose() actually removes the listener (unlike a no-op stub), mirroring
// real dockview's Emitter — this is what makes the "dispose unsubscribes"
// case below a genuine assertion rather than a type-only check.
function makeApi(panelIds: string[]) {
  const removeCbs: ((p: FakePanel) => void)[] = [];
  const activeCbs: ((p: FakePanel | undefined) => void)[] = [];
  const layoutCbs: (() => void)[] = [];
  let ids = [...panelIds];
  function subscribe<T>(list: T[], cb: T) {
    list.push(cb);
    return { dispose() { const i = list.indexOf(cb); if (i >= 0) list.splice(i, 1); } };
  }
  return {
    get panels() { return ids.map((id) => ({ id })); },
    onDidRemovePanel: (cb: (p: FakePanel) => void) => subscribe(removeCbs, cb),
    onDidActivePanelChange: (cb: (p: FakePanel | undefined) => void) => subscribe(activeCbs, cb),
    onDidLayoutChange: (cb: () => void) => subscribe(layoutCbs, cb),
    _fireRemove(id: string) { ids = ids.filter((x) => x !== id); removeCbs.forEach((cb) => cb({ id })); },
    _fireActive(id: string | undefined) { activeCbs.forEach((cb) => cb(id ? { id } : undefined)); },
    _fireLayout() { layoutCbs.forEach((cb) => cb()); },
  };
}

// Cast through `unknown` (never `any`) so the fake object literal is still
// type-checked against the real dockview surface it stands in for.
function asApi(fake: ReturnType<typeof makeApi>): DockviewApi {
  return fake as unknown as DockviewApi;
}

describe('attachDockviewSync', () => {
  it('frees the doc when its last graph/raw panel is removed', () => {
    const api = makeApi(['graph:deploy.cue', 'toolbox']);
    const store = { freeDoc: vi.fn(), setActive: vi.fn() };
    attachDockviewSync(asApi(api), store);
    api._fireRemove('graph:deploy.cue');
    expect(store.freeDoc).toHaveBeenCalledWith('deploy.cue');
  });

  it('does NOT free the doc while another panel for it remains', () => {
    const api = makeApi(['graph:deploy.cue', 'raw:deploy.cue']);
    const store = { freeDoc: vi.fn(), setActive: vi.fn() };
    attachDockviewSync(asApi(api), store);
    api._fireRemove('raw:deploy.cue'); // graph:deploy.cue still open
    expect(store.freeDoc).not.toHaveBeenCalled();
  });

  it('retargets active to the focused recipe panel', () => {
    const api = makeApi(['graph:a.cue', 'graph:b.cue']);
    const store = { freeDoc: vi.fn(), setActive: vi.fn() };
    attachDockviewSync(asApi(api), store);
    api._fireActive('graph:b.cue');
    expect(store.setActive).toHaveBeenCalledWith('b.cue');
  });

  it('leaves active unchanged when a tool panel gains focus', () => {
    const api = makeApi(['graph:a.cue', 'run']);
    const store = { freeDoc: vi.fn(), setActive: vi.fn() };
    attachDockviewSync(asApi(api), store);
    api._fireActive('run');
    expect(store.setActive).not.toHaveBeenCalled();
  });

  it('fires onLayoutChange callback', () => {
    const api = makeApi(['graph:a.cue']);
    const onLayout = vi.fn();
    attachDockviewSync(asApi(api), { freeDoc: vi.fn(), setActive: vi.fn() }, onLayout);
    api._fireLayout();
    expect(onLayout).toHaveBeenCalled();
  });

  it('dispose unsubscribes (no calls after dispose)', () => {
    const api = makeApi(['graph:a.cue']);
    const store = { freeDoc: vi.fn(), setActive: vi.fn() };
    const onLayout = vi.fn();
    const dispose = attachDockviewSync(asApi(api), store, onLayout);
    dispose();
    // Fire every event kind after dispose; none of our handlers should run.
    api._fireRemove('graph:a.cue');
    api._fireActive(undefined);
    api._fireLayout();
    expect(store.freeDoc).not.toHaveBeenCalled();
    expect(store.setActive).not.toHaveBeenCalled();
    expect(onLayout).not.toHaveBeenCalled();
  });
});
