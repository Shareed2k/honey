import { describe, it, expect, vi } from 'vitest';
import type { DockviewApi, IDockviewPanel } from 'dockview';
import { recipeIdFromPanelId, openGraph } from './registry';

// Minimal fake satisfying only the members openGraph/openRaw touch. Cast
// through `unknown` (never `any`) so the object literal itself is still
// type-checked against the real dockview surface it stands in for.
type FakeApi = Pick<DockviewApi, 'getPanel' | 'addPanel'>;

describe('registry helpers', () => {
  it('extracts recipeId from graph/raw panel ids', () => {
    expect(recipeIdFromPanelId('graph:deploy.cue')).toBe('deploy.cue');
    expect(recipeIdFromPanelId('raw:deploy.cue')).toBe('deploy.cue');
    expect(recipeIdFromPanelId('toolbox')).toBeNull();
    expect(recipeIdFromPanelId('run')).toBeNull();
  });

  it('openGraph adds a panel when absent', () => {
    const addPanel = vi.fn();
    const fake: FakeApi = { getPanel: () => undefined, addPanel };
    const api = fake as unknown as DockviewApi;
    openGraph(api, 'deploy.cue');
    expect(addPanel).toHaveBeenCalledWith(
      expect.objectContaining({ id: 'graph:deploy.cue', component: 'graph', params: { recipeId: 'deploy.cue' } }),
    );
  });

  it('openGraph focuses an existing panel instead of re-adding', () => {
    const setActive = vi.fn();
    const addPanel = vi.fn();
    const fakePanel = { api: { setActive } } as unknown as IDockviewPanel;
    const fake: FakeApi = { getPanel: () => fakePanel, addPanel };
    const api = fake as unknown as DockviewApi;
    openGraph(api, 'deploy.cue');
    expect(addPanel).not.toHaveBeenCalled();
    expect(setActive).toHaveBeenCalled();
  });
});
