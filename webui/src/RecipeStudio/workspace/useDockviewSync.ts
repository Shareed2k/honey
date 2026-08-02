import type { DockviewApi, IDockviewPanel } from 'dockview-react';
import { recipeIdFromPanelId } from './registry';

interface SyncStore {
  freeDoc(id: string): void;
  setActive(id: string | null): void;
}

/**
 * Wire dockview lifecycle events to the workspace store. dockview is the single
 * source of truth for what is open; the store is a keyed cache of doc state.
 * Returns a dispose() that removes all subscriptions.
 */
export function attachDockviewSync(
  api: DockviewApi,
  store: SyncStore,
  onLayoutChange?: () => void,
): () => void {
  const disposables = [
    api.onDidRemovePanel((panel: IDockviewPanel) => {
      const recipeId = recipeIdFromPanelId(panel.id);
      if (!recipeId) return;
      const stillOpen = api.panels.some((p) => recipeIdFromPanelId(p.id) === recipeId);
      if (!stillOpen) store.freeDoc(recipeId);
    }),
    api.onDidActivePanelChange((panel: IDockviewPanel | undefined) => {
      const recipeId = panel ? recipeIdFromPanelId(panel.id) : null;
      if (recipeId) store.setActive(recipeId); // tool panels (null) leave active unchanged
    }),
    api.onDidLayoutChange(() => onLayoutChange?.()),
  ];
  return () => disposables.forEach((d) => d.dispose());
}
